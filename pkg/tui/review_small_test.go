package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// Only ctrl+c is offered while the too-small notice hides the UI, yet keys still act on it.
func TestReviewTooSmallStillActsOnHiddenUI(t *testing.T) {
	defer setLayout(142, 34)
	m := detailsModel(t)
	m, _ = update(t, m, tea.WindowSizeMsg{Width: 60, Height: 12})
	if !m.tooSmall() {
		t.Fatal("expected the too-small notice")
	}
	m, cmd := update(t, m, press("ctrl+d"), press("y"))
	if m.busy || cmd != nil && m.page == messageDelete {
		t.Fatalf("a hidden delete dialog was answered: page %v busy %v", m.page, m.busy)
	}
}
