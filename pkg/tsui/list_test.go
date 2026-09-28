package tsui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

func sequences(msgs []Message) []uint64 {
	seqs := make([]uint64, len(msgs))
	for i, m := range msgs {
		seqs[i] = m.Sequence
	}
	return seqs
}

func TestListMessagesSparseKV(t *testing.T) {
	ctx := context.Background()
	js := runServer(t)

	kv, err := js.CreateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: "cfg", History: 1})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 5 {
		if _, err := kv.Put(ctx, fmt.Sprintf("key-%d", i), []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 3000 {
		if _, err := kv.Put(ctx, "hot", []byte(fmt.Sprint(i))); err != nil {
			t.Fatal(err)
		}
	}

	want := []uint64{3005, 5, 4, 3, 2, 1}
	msgs, err := ListMessages(ctx, js, "KV_cfg", 100)
	if err != nil {
		t.Fatal(err)
	}
	if got := sequences(msgs); !slices.Equal(got, want) {
		t.Errorf("expected sequences %v, got %v", want, got)
	}
	if msgs[0].Subject != "$KV.cfg.hot" || string(msgs[0].Data) != "2999" || msgs[0].Header != nil {
		t.Errorf("unexpected newest message: %+v", msgs[0])
	}

	s, err := js.Stream(ctx, "KV_cfg")
	if err != nil {
		t.Fatal(err)
	}
	msgs, err = listFallback(ctx, s, "KV_cfg", s.CachedInfo().State, 100)
	if err != nil {
		t.Fatal(err)
	}
	if got := sequences(msgs); !slices.Equal(got, want) {
		t.Errorf("fallback: expected sequences %v, got %v", want, got)
	}
}

func TestListMessagesDirectMatchesFallback(t *testing.T) {
	ctx := context.Background()
	js := runServer(t)

	if _, err := CreateStream(ctx, js, StreamConfig{Name: "GAPS", Subjects: []string{"gaps.*"}, AllowDirect: true, MaxMsgs: -1, MaxBytes: -1}); err != nil {
		t.Fatal(err)
	}
	// Old messages, a long run of purged ones and a newer region with scattered deletes.
	for i := 1; i <= 3000; i++ {
		subject := "gaps.keep"
		if i > 200 && i <= 2500 {
			subject = "gaps.drop"
		}
		if _, err := PublishMessage(ctx, js, subject, nats.Header{"N": []string{fmt.Sprint(i)}}, []byte(fmt.Sprint(i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := PurgeStream(ctx, js, "GAPS", "gaps.drop"); err != nil {
		t.Fatal(err)
	}
	var del []uint64
	for seq := uint64(2501); seq <= 3000; seq += 3 {
		del = append(del, seq)
	}
	del = append(del, 3000, 1, 150)
	if _, err := DeleteMessages(ctx, js, "GAPS", del); err != nil {
		t.Fatal(err)
	}

	s, err := js.Stream(ctx, "GAPS")
	if err != nil {
		t.Fatal(err)
	}
	info := s.CachedInfo()
	for _, limit := range []int{1, 10, 100, 333, 400, 1000} {
		direct, err := listDirect(ctx, js, "GAPS", info.State, limit)
		if err != nil {
			t.Fatal(err)
		}
		fallback, err := listFallback(ctx, s, "GAPS", info.State, limit)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(sequences(direct), sequences(fallback)) {
			t.Errorf("limit %d: direct returned %v, fallback %v", limit, sequences(direct), sequences(fallback))
		}
		if len(direct) != min(limit, int(info.State.Msgs)) {
			t.Errorf("limit %d: expected %d messages, got %d", limit, min(limit, int(info.State.Msgs)), len(direct))
		}
		for _, m := range direct {
			if string(m.Data) != fmt.Sprint(m.Sequence) || m.Header.Get("N") != fmt.Sprint(m.Sequence) || len(m.Header) != 1 || m.Time.IsZero() || m.Subject != "gaps.keep" || m.Stream != "GAPS" {
				t.Fatalf("limit %d: unexpected message %+v", limit, m)
			}
		}
	}
}

func TestListMessagesDirectSpansBatches(t *testing.T) {
	ctx := context.Background()
	js := runServer(t)

	if _, err := CreateStream(ctx, js, StreamConfig{Name: "BIG", Subjects: []string{"big"}, AllowDirect: true, MaxMsgs: -1, MaxBytes: -1}); err != nil {
		t.Fatal(err)
	}
	for i := range 2500 {
		if _, err := PublishMessage(ctx, js, "big", nil, []byte(fmt.Sprint(i))); err != nil {
			t.Fatal(err)
		}
	}
	msgs, err := ListMessages(ctx, js, "BIG", 2400)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2400 || msgs[0].Sequence != 2500 || msgs[2399].Sequence != 101 {
		t.Fatalf("expected sequences 2500 down to 101, got %d messages", len(msgs))
	}
	for i := 1; i < len(msgs); i++ {
		if msgs[i].Sequence != msgs[i-1].Sequence-1 {
			t.Fatalf("unexpected order at %d: %d after %d", i, msgs[i].Sequence, msgs[i-1].Sequence)
		}
	}
}

func TestDeleteStreamsPartialFailure(t *testing.T) {
	ctx := context.Background()
	js := runServer(t)

	for _, name := range []string{"A", "B", "C"} {
		if _, err := CreateStream(ctx, js, StreamConfig{Name: name, Subjects: []string{strings.ToLower(name)}}); err != nil {
			t.Fatal(err)
		}
	}
	deleted, err := DeleteStreams(ctx, js, []string{"A", "MISSING1", "B", "MISSING2", "C"})
	if !slices.Equal(deleted, []string{"A", "B", "C"}) {
		t.Errorf("expected A, B and C to be deleted, got %v", deleted)
	}
	if err == nil || !strings.Contains(err.Error(), "deleting stream MISSING1") || !strings.Contains(err.Error(), "deleting stream MISSING2") {
		t.Errorf("expected both failures to be reported, got %v", err)
	}
	if !errors.Is(err, jetstream.ErrStreamNotFound) {
		t.Errorf("expected the failures to wrap ErrStreamNotFound, got %v", err)
	}
	if streams, _ := ListStreams(ctx, js); len(streams) != 0 {
		t.Errorf("expected no streams left, got %v", streams)
	}
}

func TestGetAndDeleteConsumers(t *testing.T) {
	ctx := context.Background()
	js := runServer(t)

	if _, err := CreateStream(ctx, js, StreamConfig{Name: "JOBS", Subjects: []string{"jobs.>"}}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b"} {
		if _, err := js.CreateConsumer(ctx, "JOBS", jetstream.ConsumerConfig{Durable: name, FilterSubject: "jobs." + name}); err != nil {
			t.Fatal(err)
		}
	}
	c, err := GetConsumer(ctx, js, "JOBS", "a")
	if err != nil || c.Name != "a" || c.Stream != "JOBS" || !c.Durable || len(c.FilterSubjects) != 1 {
		t.Errorf("unexpected consumer %+v, %v", c, err)
	}
	if _, err := GetConsumer(ctx, js, "JOBS", "missing"); err == nil {
		t.Error("expected an error for a missing consumer")
	}

	deleted, err := DeleteConsumers(ctx, js, "JOBS", []string{"a", "missing", "b"})
	if !slices.Equal(deleted, []string{"a", "b"}) || err == nil || !strings.Contains(err.Error(), "deleting consumer missing") {
		t.Errorf("unexpected result %v, %v", deleted, err)
	}
	if consumers, _ := ListConsumers(ctx, js, "JOBS"); len(consumers) != 0 {
		t.Errorf("expected no consumers left, got %v", consumers)
	}
}

func TestSubjectsOverlap(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"orders", "orders", true},
		{"orders", "events", false},
		{"orders.eu", "orders", false},
		{"orders.*", "orders.eu", true},
		{"orders.*", "orders.eu.created", false},
		{"orders.>", "orders.eu.created", true},
		{"orders.>", "orders", false},
		{"orders.>", "orders.*", true},
		{"*.eu", "orders.*", true},
		{"*.eu", "orders.us", false},
		{"*.eu.>", "orders.*.created", true},
		{">", "anything.at.all", true},
		{">", "*", true},
		{"*", "*.*", false},
		{"a.*.c", "a.b.d", false},
		{"", "orders", false},
		{"orders", "", false},
	} {
		if got := SubjectsOverlap(tc.a, tc.b); got != tc.want {
			t.Errorf("SubjectsOverlap(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
		if got := SubjectsOverlap(tc.b, tc.a); got != tc.want {
			t.Errorf("SubjectsOverlap(%q, %q) = %v, want %v", tc.b, tc.a, got, tc.want)
		}
	}
}

func TestServerAtLeast(t *testing.T) {
	for v, want := range map[string]bool{
		"2.11.0": true, "2.12.3": true, "3.0.0": true, "v2.11.1": true, "2.11.0-beta.1": true,
		"2.10.22": false, "1.99.0": false, "": false, "garbage": false,
	} {
		if got := serverAtLeast(v, 2, 11); got != want {
			t.Errorf("serverAtLeast(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestAPIPrefix(t *testing.T) {
	nc := &nats.Conn{}
	for _, tc := range []struct {
		js   func() (jetstream.JetStream, error)
		want string
	}{
		{func() (jetstream.JetStream, error) { return jetstream.New(nc) }, "$JS.API."},
		{func() (jetstream.JetStream, error) { return jetstream.NewWithDomain(nc, "hub") }, "$JS.hub.API."},
		{func() (jetstream.JetStream, error) { return jetstream.NewWithAPIPrefix(nc, "JS.acc") }, "JS.acc."},
		{func() (jetstream.JetStream, error) { return jetstream.NewWithAPIPrefix(nc, "JS.acc.") }, "JS.acc."},
	} {
		js, err := tc.js()
		if err != nil {
			t.Fatal(err)
		}
		if got := apiPrefix(js); got != tc.want {
			t.Errorf("apiPrefix = %q, want %q", got, tc.want)
		}
	}
}
