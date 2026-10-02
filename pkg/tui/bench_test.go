package tui

import (
	"fmt"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/kontrolplane/tsui/pkg/tsui"
	"github.com/kontrolplane/tsui/pkg/tui/messages"
)

// BenchmarkRenderMessages draws a frame of the messages tab, full of JSON payloads. The view is
// drawn after every message the program gets, keys and refreshes alike.
func BenchmarkRenderMessages(b *testing.B) {
	defer setLayout(140, 25)
	t := &testing.T{}
	m, _ := update(t, newTestModel(), tea.WindowSizeMsg{Width: 200, Height: 50}, messages.StreamsLoadedMsg{Streams: testStreams})
	m, _ = update(t, m, press("j"), press("j"), press("enter"))
	now := time.Now()
	msgs := make([]tsui.Message, 500)
	for i := range msgs {
		msgs[i] = tsui.Message{Stream: "ORDERS", Subject: fmt.Sprintf("orders.eu.%d", i), Sequence: uint64(500 - i), Time: now,
			Data: fmt.Appendf(nil, `{"id":%d,"customer":"cus_%04d","status":"created","total":%d.95,"msg":"order placed"}`, i, i, i)}
	}
	m, _ = update(t, m,
		messages.StreamLoadedMsg{Name: "ORDERS", Stream: testStreams[2]},
		messages.MessagesLoadedMsg{Stream: "ORDERS", Messages: msgs, Gen: m.msgsGen},
	)
	if m.page != streamDetails || len(m.state.streamDetails.messages) == 0 {
		b.Fatalf("page %v with %d messages", m.page, len(m.state.streamDetails.messages))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = m.render()
	}
}
