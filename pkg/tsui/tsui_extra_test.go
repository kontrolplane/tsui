package tsui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

func runServerConn(t *testing.T) (*nats.Conn, jetstream.JetStream) {
	t.Helper()
	srv, err := server.NewServer(&server.Options{
		ServerName: "tsui-test",
		Host:       "127.0.0.1",
		Port:       -1,
		JetStream:  true,
		StoreDir:   t.TempDir(),
		NoLog:      true,
		NoSigs:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats server not ready")
	}
	t.Cleanup(srv.Shutdown)

	nc, err := nats.Connect(srv.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)

	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	return nc, js
}

// The stream form enables allow_direct by default, which makes GetMsg go through direct get. The
// server adds its own headers to those responses and they must not show up as message headers.
func TestListMessagesDirectGetStripsServerHeaders(t *testing.T) {
	ctx := context.Background()
	js := runServer(t)

	if _, err := CreateStream(ctx, js, StreamConfig{Name: "DIRECT", Subjects: []string{"direct.>"}, AllowDirect: true, MaxMsgs: -1, MaxBytes: -1}); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishMessage(ctx, js, "direct.a", nats.Header{"Order": []string{"1"}, "X-Multi": []string{"a", "b"}}, []byte("with headers")); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishMessage(ctx, js, "direct.b", nil, []byte("no headers")); err != nil {
		t.Fatal(err)
	}

	msgs, err := ListMessages(ctx, js, "DIRECT", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Header != nil {
		t.Errorf("expected no headers on a message published without any, got %v", msgs[0].Header)
	}
	h := msgs[1].Header
	if len(h) != 2 || h.Get("Order") != "1" || len(h["X-Multi"]) != 2 {
		t.Errorf("expected only the published headers, got %v", h)
	}
	if msgs[1].Time.IsZero() || msgs[1].Subject != "direct.a" || msgs[1].Sequence != 1 {
		t.Errorf("unexpected message metadata: %+v", msgs[1])
	}
}

func TestListMessagesEdgeCases(t *testing.T) {
	ctx := context.Background()
	js := runServer(t)

	if _, err := ListMessages(ctx, js, "MISSING", 10); err == nil {
		t.Error("expected an error for a missing stream")
	}

	if _, err := CreateStream(ctx, js, StreamConfig{Name: "EMPTY", Subjects: []string{"empty"}}); err != nil {
		t.Fatal(err)
	}
	if msgs, err := ListMessages(ctx, js, "EMPTY", 10); err != nil || msgs != nil {
		t.Errorf("expected no messages for an empty stream, got %v, %v", msgs, err)
	}

	if _, err := PublishMessage(ctx, js, "empty", nil, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if msgs, err := ListMessages(ctx, js, "EMPTY", 0); err != nil || msgs != nil {
		t.Errorf("expected no messages for a zero limit, got %v, %v", msgs, err)
	}

	// After a purge the first sequence moves past the old messages.
	if err := PurgeStream(ctx, js, "EMPTY", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishMessage(ctx, js, "empty", nil, []byte("after purge")); err != nil {
		t.Fatal(err)
	}
	msgs, err := ListMessages(ctx, js, "EMPTY", 10)
	if err != nil || len(msgs) != 1 || msgs[0].Sequence != 2 || string(msgs[0].Data) != "after purge" {
		t.Errorf("expected only the message published after the purge, got %+v, %v", msgs, err)
	}
}

func TestPublishWithoutStreamFriendlyError(t *testing.T) {
	js := runServer(t)
	_, err := PublishMessage(context.Background(), js, "nowhere", nil, []byte("x"))
	if err == nil || !strings.Contains(err.Error(), `no stream is listening on subject "nowhere"`) {
		t.Fatalf("expected a friendly error, got %v", err)
	}
}

func TestPublishReturnsSequenceAndDeduplicates(t *testing.T) {
	ctx := context.Background()
	js := runServer(t)

	if _, err := CreateStream(ctx, js, StreamConfig{Name: "DEDUPE", Subjects: []string{"dedupe"}, Duplicates: time.Minute}); err != nil {
		t.Fatal(err)
	}
	h := nats.Header{"Nats-Msg-Id": []string{"order-1"}}
	for i, dup := range []bool{false, true} {
		ack, err := PublishMessage(ctx, js, "dedupe", h, []byte("x"))
		if err != nil {
			t.Fatal(err)
		}
		if ack.Sequence != 1 || ack.Duplicate != dup || ack.Stream != "DEDUPE" {
			t.Errorf("publish %d: expected sequence 1 in DEDUPE with duplicate %v, got %+v", i, dup, ack)
		}
	}
	s, err := GetStream(ctx, js, "DEDUPE")
	if err != nil || s.Messages != 1 || s.Duplicates != time.Minute {
		t.Errorf("expected one deduplicated message, got %+v, %v", s, err)
	}
}

func TestCreateStreamPolicies(t *testing.T) {
	ctx := context.Background()
	js := runServer(t)

	s, err := CreateStream(ctx, js, StreamConfig{
		Name:              "WORK",
		Description:       "work queue",
		Subjects:          []string{"work.>"},
		Storage:           "FILE",
		Retention:         "workqueue",
		Discard:           "new",
		MaxMsgs:           100,
		MaxBytes:          1 << 20,
		MaxAge:            time.Hour,
		MaxMsgsPerSubject: 10,
		MaxMsgSize:        1024,
		DenyDelete:        true,
		DenyPurge:         true,
		AllowDirect:       true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.Storage != "file" || s.Retention != "workqueue" || s.Discard != "new" || s.Description != "work queue" {
		t.Errorf("unexpected policies: %+v", s)
	}
	if s.MaxMsgs != 100 || s.MaxBytes != 1<<20 || s.MaxAge != time.Hour || s.MaxMsgsPerSubject != 10 || s.MaxMsgSize != 1024 {
		t.Errorf("unexpected limits: %+v", s)
	}
	if !s.DenyDelete || !s.DenyPurge || !s.AllowDirect || s.Sealed {
		t.Errorf("unexpected flags: %+v", s)
	}

	i, err := CreateStream(ctx, js, StreamConfig{Name: "INTEREST", Subjects: []string{"interest"}, Retention: "interest"})
	if err != nil || i.Retention != "interest" || i.Discard != "old" || i.Storage != "file" {
		t.Errorf("unexpected defaults: %+v, %v", i, err)
	}

	// deny_purge and deny_delete are enforced by the server.
	if err := PurgeStream(ctx, js, "WORK", ""); err == nil {
		t.Error("expected purging a deny_purge stream to fail")
	}
	if _, err := PublishMessage(ctx, js, "work.a", nil, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := DeleteMessages(ctx, js, "WORK", []uint64{1}); err == nil {
		t.Error("expected deleting from a deny_delete stream to fail")
	}

	// Creating a stream with the same name and another config fails.
	if _, err := CreateStream(ctx, js, StreamConfig{Name: "WORK", Subjects: []string{"other"}}); err == nil {
		t.Error("expected creating a conflicting stream to fail")
	}
}

func TestCreateStreamInvalidConfig(t *testing.T) {
	js := runServer(t)
	for _, c := range []StreamConfig{
		{Name: "A", Storage: "disk"},
		{Name: "A", Retention: "forever"},
		{Name: "A", Discard: "middle"},
	} {
		if _, err := CreateStream(context.Background(), js, c); err == nil || !strings.Contains(err.Error(), "unknown") {
			t.Errorf("expected an unknown policy error for %+v, got %v", c, err)
		}
	}
}

func TestMirrorAndSources(t *testing.T) {
	ctx := context.Background()
	js := runServer(t)

	if _, err := CreateStream(ctx, js, StreamConfig{Name: "ORIGIN", Subjects: []string{"origin.>"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateStream(ctx, js, StreamConfig{Name: "OTHER", Subjects: []string{"other.>"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := js.CreateStream(ctx, jetstream.StreamConfig{Name: "MIRROR", Mirror: &jetstream.StreamSource{Name: "ORIGIN"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := js.CreateStream(ctx, jetstream.StreamConfig{Name: "AGG", Sources: []*jetstream.StreamSource{{Name: "ORIGIN"}, {Name: "OTHER"}}}); err != nil {
		t.Fatal(err)
	}
	m, err := GetStream(ctx, js, "MIRROR")
	if err != nil || m.Mirror != "ORIGIN" || len(m.Subjects) != 0 {
		t.Errorf("expected a mirror of ORIGIN, got %+v, %v", m, err)
	}
	a, err := GetStream(ctx, js, "AGG")
	if err != nil || len(a.Sources) != 2 || a.Sources[0] != "ORIGIN" || a.Sources[1] != "OTHER" {
		t.Errorf("expected two sources, got %+v, %v", a, err)
	}
}

func TestListSubjects(t *testing.T) {
	ctx := context.Background()
	js := runServer(t)

	if _, err := ListSubjects(ctx, js, "MISSING"); err == nil {
		t.Error("expected an error for a missing stream")
	}
	if _, err := CreateStream(ctx, js, StreamConfig{Name: "ORDERS", Subjects: []string{"orders.>"}}); err != nil {
		t.Fatal(err)
	}
	subjects, err := ListSubjects(ctx, js, "ORDERS")
	if err != nil || len(subjects) != 0 {
		t.Errorf("expected no subjects on an empty stream, got %v, %v", subjects, err)
	}

	counts := map[string]int{"orders.us": 3, "orders.eu": 3, "orders.apac": 5, "orders.eu.created": 1}
	for subject, n := range counts {
		for range n {
			if _, err := PublishMessage(ctx, js, subject, nil, []byte("x")); err != nil {
				t.Fatal(err)
			}
		}
	}
	subjects, err = ListSubjects(ctx, js, "ORDERS")
	if err != nil {
		t.Fatal(err)
	}
	want := []SubjectCount{{"orders.apac", 5}, {"orders.eu", 3}, {"orders.us", 3}, {"orders.eu.created", 1}}
	if len(subjects) != len(want) {
		t.Fatalf("expected %d subjects, got %+v", len(want), subjects)
	}
	for i := range want {
		if subjects[i] != want[i] {
			t.Errorf("subject %d: expected %+v, got %+v (busiest first, ties by name)", i, want[i], subjects[i])
		}
	}
}

func TestGetServer(t *testing.T) {
	ctx := context.Background()
	nc, js := runServerConn(t)

	if _, err := CreateStream(ctx, js, StreamConfig{Name: "MEM", Subjects: []string{"mem"}, Storage: "memory"}); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishMessage(ctx, js, "mem", nil, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if _, err := js.CreateConsumer(ctx, "MEM", jetstream.ConsumerConfig{Durable: "c"}); err != nil {
		t.Fatal(err)
	}

	s, err := GetServer(ctx, nc, js)
	if err != nil {
		t.Fatal(err)
	}
	// Windows' clock can measure a loopback round trip as zero.
	if s.Name != "tsui-test" || s.Version == "" || s.RTT < 0 {
		t.Errorf("unexpected server info: %+v", s)
	}
	if s.Streams != 1 || s.Consumers != 1 || s.Memory == 0 || s.Store != 0 {
		t.Errorf("unexpected account usage: %+v", s)
	}

	nc.Close()
	if _, err := GetServer(ctx, nc, js); err == nil {
		t.Error("expected an error on a closed connection")
	}
}

func TestConsumerFromInfo(t *testing.T) {
	last := time.Now()
	info := &jetstream.ConsumerInfo{
		Stream: "S",
		Name:   "push",
		Config: jetstream.ConsumerConfig{
			FilterSubject:  "a",
			DeliverSubject: "deliver",
			DeliverPolicy:  jetstream.DeliverLastPerSubjectPolicy,
			AckPolicy:      jetstream.AckNonePolicy,
		},
		Delivered:  jetstream.SequenceInfo{Stream: 7, Last: &last},
		AckFloor:   jetstream.SequenceInfo{Stream: 5},
		PushBound:  true,
		NumPending: 2,
	}
	c := consumerFromInfo(info)
	if c.Durable || !c.Push || !c.PushBound || c.DeliverPolicy != "last/subj" || c.AckPolicy != "none" {
		t.Errorf("unexpected consumer: %+v", c)
	}
	if c.Delivered != 7 || c.AckFloor != 5 || !c.LastActive.Equal(last) || len(c.FilterSubjects) != 1 {
		t.Errorf("unexpected state: %+v", c)
	}

	for p, want := range map[jetstream.DeliverPolicy]string{
		jetstream.DeliverAllPolicy:             "all",
		jetstream.DeliverLastPolicy:            "last",
		jetstream.DeliverNewPolicy:             "new",
		jetstream.DeliverByStartSequencePolicy: "by seq",
		jetstream.DeliverByStartTimePolicy:     "by time",
	} {
		if got := deliverPolicyName(p); got != want {
			t.Errorf("deliverPolicyName(%v) = %q, want %q", p, got, want)
		}
	}
	if ackPolicyName(jetstream.AckAllPolicy) != "all" || ackPolicyName(jetstream.AckExplicitPolicy) != "explicit" {
		t.Error("unexpected ack policy names")
	}
}

func TestConsumerMultipleFiltersAndErrors(t *testing.T) {
	ctx := context.Background()
	js := runServer(t)

	if _, err := ListConsumers(ctx, js, "MISSING"); err == nil {
		t.Error("expected an error listing consumers of a missing stream")
	}
	if _, err := CreateStream(ctx, js, StreamConfig{Name: "JOBS", Subjects: []string{"jobs.>"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := js.CreateConsumer(ctx, "JOBS", jetstream.ConsumerConfig{
		FilterSubjects: []string{"jobs.a", "jobs.b"},
		DeliverPolicy:  jetstream.DeliverNewPolicy,
		AckPolicy:      jetstream.AckAllPolicy,
	}); err != nil {
		t.Fatal(err)
	}
	consumers, err := ListConsumers(ctx, js, "JOBS")
	if err != nil || len(consumers) != 1 {
		t.Fatalf("expected one consumer, got %v, %v", consumers, err)
	}
	c := consumers[0]
	if c.Durable || c.DeliverPolicy != "new" || c.AckPolicy != "all" || len(c.FilterSubjects) != 2 || c.Stream != "JOBS" {
		t.Errorf("unexpected ephemeral consumer: %+v", c)
	}
	if err := DeleteConsumer(ctx, js, "JOBS", "missing"); err == nil {
		t.Error("expected an error deleting a missing consumer")
	}
}

func TestDeleteAndPurgeErrors(t *testing.T) {
	ctx := context.Background()
	js := runServer(t)

	if err := DeleteStream(ctx, js, "MISSING"); err == nil {
		t.Error("expected an error deleting a missing stream")
	}
	if err := PurgeStream(ctx, js, "MISSING", ""); err == nil {
		t.Error("expected an error purging a missing stream")
	}
	if _, err := DeleteMessages(ctx, js, "MISSING", []uint64{1}); err == nil {
		t.Error("expected an error deleting from a missing stream")
	}

	if _, err := CreateStream(ctx, js, StreamConfig{Name: "S", Subjects: []string{"s.*"}}); err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		if _, err := PublishMessage(ctx, js, fmt.Sprintf("s.%d", i), nil, []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	deleted, err := DeleteMessages(ctx, js, "S", []uint64{1, 42, 2})
	if err == nil || !strings.Contains(err.Error(), "deleting message 42") {
		t.Errorf("expected the failing sequence to be named, got %v", err)
	}
	if len(deleted) != 2 || deleted[0] != 1 || deleted[1] != 2 {
		t.Errorf("expected sequences 1 and 2 to be reported as deleted, got %v", deleted)
	}
	s, _ := GetStream(ctx, js, "S")
	if s.Messages != 1 {
		t.Errorf("expected a failure not to stop the other deletes, leaving 1 message, got %d", s.Messages)
	}

	// A wildcard subject purge removes every matching subject.
	if err := PurgeStream(ctx, js, "S", "s.*"); err != nil {
		t.Fatal(err)
	}
	s, _ = GetStream(ctx, js, "S")
	if s.Messages != 0 {
		t.Errorf("expected wildcard purge to empty the stream, got %d", s.Messages)
	}
}

func TestHasWildcard(t *testing.T) {
	for s, want := range map[string]bool{"a.*": true, "a.>": true, "*": true, "a.b": false, "a*.b": false, "a.b>": false, "": false} {
		if got := HasWildcard(s); got != want {
			t.Errorf("HasWildcard(%q) = %v, want %v", s, got, want)
		}
	}
}
