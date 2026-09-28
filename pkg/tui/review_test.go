package tui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/kontrolplane/tsui/pkg/tsui"
)

// drainN is drain with a bound, reporting whether the model settled.
func drainN(t *testing.T, m model, cmd tea.Cmd, limit int) (model, bool) {
	t.Helper()
	queue := collect(cmd)
	for i := 0; len(queue) > 0; i++ {
		if i > limit {
			return m, false
		}
		msg := queue[0]
		queue = queue[1:]
		m, cmd = update(t, m, msg)
		queue = append(queue, collect(cmd)...)
	}
	return m, true
}

// Older pages loaded, then the stream is emptied elsewhere (purge, workqueue drained, max age),
// then more than a page of new messages arrives: every load answers with another load.
func TestReviewPagedEmptyReloadLoop(t *testing.T) {
	m, js := liveModel(t)
	ctx := context.Background()
	if _, err := tsui.CreateStream(ctx, js, tsui.StreamConfig{Name: "WQ", Subjects: []string{"wq"}, MaxMsgs: -1, MaxBytes: -1, AllowDirect: true}); err != nil {
		t.Fatal(err)
	}
	publishN(t, js, "wq", 250)
	m = openStream(t, m, "WQ")
	m = step(t, m, "G")
	if !m.state.streamDetails.paged {
		t.Fatal("expected older pages")
	}
	if err := tsui.PurgeStream(ctx, js, "WQ", ""); err != nil {
		t.Fatal(err)
	}
	m, cmd := update(t, m, refreshTick(m))
	m = drain(t, m, cmd)
	if d := m.state.streamDetails; len(d.messages) != 0 || !d.paged {
		t.Fatalf("setup: expected an empty paged list, got %d paged %v", len(d.messages), d.paged)
	}
	publishN(t, js, "wq", 150)
	m, cmd = update(t, m, refreshTick(m))
	m, ok := drainN(t, m, cmd, 60)
	if !ok {
		t.Fatalf("message loads never settle: msgsGen=%d, %d messages shown", m.msgsGen, len(m.state.streamDetails.messages))
	}
	if n := len(m.state.streamDetails.messages); n == 0 {
		t.Fatalf("expected the new messages, got none")
	}
}
