package tsui

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

func runServer(t *testing.T) jetstream.JetStream {
	t.Helper()
	srv, err := server.NewServer(&server.Options{
		Host:      "127.0.0.1",
		Port:      -1,
		JetStream: true,
		StoreDir:  t.TempDir(),
		NoLog:     true,
		NoSigs:    true,
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
	return js
}

func TestStreamLifecycle(t *testing.T) {
	ctx := context.Background()
	js := runServer(t)

	created, err := CreateStream(ctx, js, StreamConfig{
		Name:      "ORDERS",
		Subjects:  []string{"orders.>"},
		Storage:   "memory",
		Retention: "limits",
		Replicas:  1,
		MaxMsgs:   -1,
		MaxBytes:  -1,
	})
	if err != nil {
		t.Fatalf("create stream: %v", err)
	}
	if created.Storage != "memory" || created.Retention != "limits" {
		t.Errorf("unexpected stream config: %+v", created)
	}

	if _, err := CreateStream(ctx, js, StreamConfig{Name: "EVENTS", Subjects: []string{"events.*"}}); err != nil {
		t.Fatalf("create stream: %v", err)
	}

	streams, err := ListStreams(ctx, js)
	if err != nil {
		t.Fatalf("list streams: %v", err)
	}
	if len(streams) != 2 || streams[0].Name != "EVENTS" || streams[1].Name != "ORDERS" {
		t.Fatalf("expected streams sorted by name, got %+v", streams)
	}

	for i := range 5 {
		subject := "orders.eu"
		if i%2 == 1 {
			subject = "orders.us"
		}
		if _, err := PublishMessage(ctx, js, subject, nats.Header{"Order": []string{fmt.Sprint(i)}}, []byte(fmt.Sprintf(`{"n":%d}`, i))); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}

	if err := PurgeStream(ctx, js, "ORDERS", "orders.us"); err != nil {
		t.Fatalf("purge subject: %v", err)
	}
	s, err := GetStream(ctx, js, "ORDERS")
	if err != nil {
		t.Fatal(err)
	}
	if s.Messages != 3 {
		t.Errorf("expected 3 messages after subject purge, got %d", s.Messages)
	}

	if err := PurgeStream(ctx, js, "ORDERS", ""); err != nil {
		t.Fatalf("purge: %v", err)
	}
	s, _ = GetStream(ctx, js, "ORDERS")
	if s.Messages != 0 {
		t.Errorf("expected empty stream after purge, got %d", s.Messages)
	}

	if err := DeleteStream(ctx, js, "ORDERS"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := GetStream(ctx, js, "ORDERS"); err == nil {
		t.Error("expected error fetching deleted stream")
	}
}

func TestListMessagesNewestFirstSkippingGaps(t *testing.T) {
	ctx := context.Background()
	js := runServer(t)

	if _, err := CreateStream(ctx, js, StreamConfig{Name: "LOGS", Subjects: []string{"logs.*"}, MaxMsgs: -1, MaxBytes: -1}); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 60; i++ {
		if _, err := PublishMessage(ctx, js, "logs.app", nil, []byte(fmt.Sprint(i))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := DeleteMessages(ctx, js, "LOGS", []uint64{60, 58, 30}); err != nil {
		t.Fatal(err)
	}

	msgs, err := ListMessages(ctx, js, "LOGS", 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []uint64{59, 57, 56, 55, 54, 53, 52, 51, 50, 49}
	if len(msgs) != len(want) {
		t.Fatalf("expected %d messages, got %d", len(want), len(msgs))
	}
	for i, seq := range want {
		if msgs[i].Sequence != seq {
			t.Errorf("message %d: expected seq %d, got %d", i, seq, msgs[i].Sequence)
		}
	}
	if msgs[0].Stream != "LOGS" || msgs[0].Subject != "logs.app" || string(msgs[0].Data) != "59" {
		t.Errorf("unexpected message: %+v", msgs[0])
	}

	all, err := ListMessages(ctx, js, "LOGS", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 57 {
		t.Errorf("expected 57 messages, got %d", len(all))
	}
}

func TestPublishWithoutStream(t *testing.T) {
	js := runServer(t)
	if _, err := PublishMessage(context.Background(), js, "nowhere", nil, []byte("x")); err == nil {
		t.Fatal("expected error publishing to a subject without a stream")
	}
}

func TestConsumers(t *testing.T) {
	ctx := context.Background()
	js := runServer(t)

	if _, err := CreateStream(ctx, js, StreamConfig{Name: "JOBS", Subjects: []string{"jobs.>"}, MaxMsgs: -1, MaxBytes: -1}); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := PublishMessage(ctx, js, "jobs.build", nil, []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"worker", "auditor"} {
		if _, err := js.CreateConsumer(ctx, "JOBS", jetstream.ConsumerConfig{
			Durable:       name,
			FilterSubject: "jobs.build",
			AckPolicy:     jetstream.AckExplicitPolicy,
		}); err != nil {
			t.Fatal(err)
		}
	}

	consumers, err := ListConsumers(ctx, js, "JOBS")
	if err != nil {
		t.Fatal(err)
	}
	if len(consumers) != 2 || consumers[0].Name != "auditor" {
		t.Fatalf("expected consumers sorted by name, got %+v", consumers)
	}
	c := consumers[1]
	if !c.Durable || c.AckPolicy != "explicit" || c.NumPending != 3 || len(c.FilterSubjects) != 1 || c.FilterSubjects[0] != "jobs.build" {
		t.Errorf("unexpected consumer: %+v", c)
	}

	if err := DeleteConsumer(ctx, js, "JOBS", "worker"); err != nil {
		t.Fatal(err)
	}
	consumers, _ = ListConsumers(ctx, js, "JOBS")
	if len(consumers) != 1 {
		t.Errorf("expected 1 consumer after delete, got %d", len(consumers))
	}
}

func TestSubjectMatches(t *testing.T) {
	cases := []struct {
		pattern, subject string
		want             bool
	}{
		{"orders.>", "orders.eu", true},
		{"orders.>", "orders.eu.created", true},
		{"orders.>", "orders", false},
		{"orders.*", "orders.eu", true},
		{"orders.*", "orders.eu.created", false},
		{"*.created", "orders.created", true},
		{"orders.eu", "orders.eu", true},
		{"orders.eu", "orders.us", false},
		{">", "anything.at.all", true},
		{"", "orders", false},
	}
	for _, c := range cases {
		if got := SubjectMatches(c.pattern, c.subject); got != c.want {
			t.Errorf("SubjectMatches(%q, %q) = %v, want %v", c.pattern, c.subject, got, c.want)
		}
	}
}

func TestSubjectHelpers(t *testing.T) {
	if SubjectPrefix("orders.>") != "orders." || SubjectPrefix("a.*.c") != "a." || SubjectPrefix("x.y") != "x.y" || SubjectPrefix(">") != "" {
		t.Error("unexpected SubjectPrefix result")
	}
	for s, want := range map[string]bool{"orders.eu": true, "orders.*": false, "orders..eu": false, "orders eu": false, "": false, "orders.": false} {
		if got := ValidPublishSubject(s); got != want {
			t.Errorf("ValidPublishSubject(%q) = %v, want %v", s, got, want)
		}
	}
}
