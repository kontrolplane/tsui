package tsui

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/nats-io/nats.go"
)

// A stored message with an empty payload and a user header named Status is read as a server status.
func TestReviewStatusHeaderPoisonsDirectListing(t *testing.T) {
	for _, status := range []string{"503", "204", "404"} {
		t.Run(status, func(t *testing.T) {
			ctx := context.Background()
			js := runServer(t)
			if _, err := CreateStream(ctx, js, StreamConfig{Name: "S", Subjects: []string{"s.>"}, AllowDirect: true, MaxMsgs: -1, MaxBytes: -1}); err != nil {
				t.Fatal(err)
			}
			for i := range 10 {
				h := nats.Header{}
				var data []byte = []byte(fmt.Sprint(i))
				if i == 3 {
					h.Set("Status", status)
					data = nil
				}
				if _, err := PublishMessage(ctx, js, "s.x", h, data); err != nil {
					t.Fatal(err)
				}
			}
			msgs, err := ListMessages(ctx, js, "S", 100)
			if err != nil {
				t.Fatalf("listing failed: %v", err)
			}
			if len(msgs) != 10 {
				t.Fatalf("expected 10 messages, got %d: %v", len(msgs), sequences(msgs))
			}
			sm, _, err := ListSubjectMessages(ctx, js, "S", "s.x", 0, 100)
			if err != nil || len(sm) != 10 {
				t.Fatalf("subject listing: expected 10, got %d err %v", len(sm), err)
			}
		})
	}
}

// Batches stop at the server's max pending bytes; the rest must still be read.
func TestReviewDirectBatchMaxBytes(t *testing.T) {
	ctx := context.Background()
	js := runServer(t)
	if _, err := CreateStream(ctx, js, StreamConfig{Name: "L", Subjects: []string{"l"}, AllowDirect: true, MaxMsgs: -1, MaxBytes: -1}); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("x"), 1000*1000)
	for range 80 {
		if _, err := PublishMessage(ctx, js, "l", nil, payload); err != nil {
			t.Fatal(err)
		}
	}
	msgs, err := ListMessages(ctx, js, "L", 100)
	if err != nil || len(msgs) != 80 {
		t.Fatalf("expected 80, got %d err %v", len(msgs), err)
	}
}
