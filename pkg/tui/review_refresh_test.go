package tui

import (
	"context"
	"testing"

	"github.com/kontrolplane/tsui/pkg/tui/messages"
)

// A reload that fails after the stream changed is never retried by the refresh, since the stream
// state it compares against was already taken over.
func TestReviewFailedReloadIsNotRetried(t *testing.T) {
	m := detailsModel(t)
	changed := testStreams[2]
	changed.LastSeq = 241
	m, _ = update(t, m, messages.StreamLoadedMsg{Name: "ORDERS", Stream: changed, Refresh: true})
	gen := m.msgsGen
	m, _ = update(t, m, messages.MessagesLoadedMsg{Stream: "ORDERS", Gen: gen, Err: context.DeadlineExceeded})
	m, _ = update(t, m, messages.StreamLoadedMsg{Name: "ORDERS", Stream: changed, Refresh: true})
	if m.msgsGen == gen {
		t.Fatal("the failed message reload is not retried: the new messages never show until the stream changes again")
	}
}
