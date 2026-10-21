package tsui

import (
	"context"
	"slices"
	"testing"

	"github.com/nats-io/nats.go"
)

func TestRedriveMessages(t *testing.T) {
	ctx := context.Background()
	js := runServer(t)
	for _, cfg := range []StreamConfig{
		{Name: "ORDERS", Subjects: []string{"orders.>"}, MaxMsgs: -1, MaxBytes: -1},
		{Name: "EVENTS", Subjects: []string{"events.*"}, MaxMsgs: -1, MaxBytes: -1},
	} {
		if _, err := CreateStream(ctx, js, cfg); err != nil {
			t.Fatal(err)
		}
	}
	publish := func(subject string, header nats.Header, data string) {
		t.Helper()
		if _, err := PublishMessage(ctx, js, subject, header, []byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	publish("orders.a", nats.Header{"Nats-Expected-Stream": {"ORDERS"}, "X-Id": {"1"}}, "one")
	publish("orders.b", nats.Header{"Nats-Msg-Id": {"dup"}}, "two")
	publish("orders.c", nil, "three")
	publish("events.x", nats.Header{"Nats-Msg-Id": {"dup"}}, "already there")

	// Asked for newest first, redriven oldest first; the duplicate stays in ORDERS.
	res, err := RedriveMessages(ctx, js, "ORDERS", []uint64{3, 1, 2}, "events.x", true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Redriven, []uint64{1, 3}) || !slices.Equal(res.Duplicates, []uint64{2}) ||
		!slices.Equal(res.Deleted, []uint64{1, 3}) || !slices.Equal(res.Streams, []string{"EVENTS"}) {
		t.Fatalf("unexpected result %+v", res)
	}
	events, err := ListMessages(ctx, js, "EVENTS", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || string(events[1].Data) != "one" || string(events[0].Data) != "three" {
		t.Fatalf("expected one and three appended to EVENTS in order, got %+v", events)
	}
	if h := events[1].Header; h.Get("X-Id") != "1" || h.Get("Nats-Expected-Stream") != "" {
		t.Errorf("expected the headers to be kept without Nats-Expected-*, got %v", h)
	}
	orders, err := ListMessages(ctx, js, "ORDERS", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 1 || orders[0].Sequence != 2 {
		t.Fatalf("expected only the duplicate left in ORDERS, got %+v", orders)
	}

	// Without a subject each message goes back on its own.
	res, err = RedriveMessages(ctx, js, "EVENTS", []uint64{2, 3}, "", false)
	if err != nil || !slices.Equal(res.Redriven, []uint64{2, 3}) || len(res.Deleted) != 0 {
		t.Fatalf("unexpected result %+v, %v", res, err)
	}
	if s, _ := GetStream(ctx, js, "EVENTS"); s.Messages != 5 {
		t.Errorf("expected the messages to stay in EVENTS and be stored again, got %d", s.Messages)
	}

	// A subject no stream listens on stops at the first message.
	res, err = RedriveMessages(ctx, js, "EVENTS", []uint64{2, 3}, "nowhere", true)
	if err == nil || len(res.Redriven) != 0 || len(res.Deleted) != 0 {
		t.Fatalf("expected the redrive to stop, got %+v, %v", res, err)
	}
}
