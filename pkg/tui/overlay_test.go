package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestOverlayKeepsThePageAround(t *testing.T) {
	defer setLayout(140, 25)
	setLayout(minContentWidth+chromeWidth, minContentHeight+chromeHeight)
	page := make([]string, contentHeight)
	for i := range page {
		page[i] = strings.Repeat(string(rune('a'+i)), contentWidth)
	}
	got := strings.Split(ansi.Strip(overlay(strings.Join(page, "\n"), "XX")), "\n")
	mid := (contentHeight - 1) / 2
	for i, line := range got {
		want := page[i]
		if i == mid {
			// The card, with a blank cell either side of it.
			at := (contentWidth - 4) / 2
			want = want[:at] + " XX " + want[at+4:]
		}
		if line != want {
			t.Errorf("line %d = %q, want %q", i, line, want)
		}
	}
}

// A confirmation, an error and the help are set over the page they are about rather than in
// its place.
func TestDialogsKeepThePageInSight(t *testing.T) {
	defer setLayout(140, 25)
	m, _ := update(t, loadedModel(t), tea.WindowSizeMsg{Width: 160, Height: 40})
	m, _ = update(t, m, press("j"), press("j"), press("ctrl+d"))
	if m.page != streamDelete {
		t.Fatalf("expected the delete dialog, got page %v", m.page)
	}
	view := ansi.Strip(m.render())
	for _, want := range []string{"delete stream", "EVENTS", "JOBS"} {
		if !strings.Contains(view, want) {
			t.Errorf("expected %q on screen with the delete dialog open", want)
		}
	}

	m, _ = update(t, m, press("esc"))
	m.error = "could not load streams: boom"
	if view := ansi.Strip(m.render()); !strings.Contains(view, "boom") || !strings.Contains(view, "EVENTS") {
		t.Error("expected the error over the stream overview")
	}
	m.error = ""
	m, _ = update(t, m, press("?"))
	if view := ansi.Strip(m.render()); !strings.Contains(view, "press any key to close") || !strings.Contains(view, "EVENTS") {
		t.Error("expected the help over the stream overview")
	}
}

func TestDialogFrom(t *testing.T) {
	m := newTestModel()
	for _, tt := range []struct {
		setup func(*model)
		want  page
	}{
		{func(m *model) { m.page = streamDelete }, streamOverview},
		{func(m *model) { m.page, m.state.streamPurge.fromOverview = streamPurge, true }, streamOverview},
		{func(m *model) { m.page, m.state.streamPurge.fromOverview = streamPurge, false }, streamDetails},
		{func(m *model) { m.page, m.state.messageDelete.fromDetails = messageDelete, true }, messageDetails},
		{func(m *model) { m.page, m.state.messageDelete.fromDetails = messageDelete, false }, streamDetails},
		{func(m *model) { m.page, m.state.consumerDelete.fromDetails = consumerDelete, true }, consumerDetails},
		{func(m *model) { m.page, m.state.consumerDelete.fromDetails = consumerDelete, false }, streamDetails},
	} {
		c := m
		tt.setup(&c)
		if got, ok := c.dialogFrom(); !ok || got != tt.want {
			t.Errorf("page %v: dialog from %v (%v), want %v", c.page, got, ok, tt.want)
		}
	}
	if _, ok := m.dialogFrom(); ok {
		t.Error("the overview is not a dialog")
	}
}

// The help puts the keys of the page it is asked on first, and keeps them when the rest does not
// fit.
func TestHelpPutsTheCurrentPageFirst(t *testing.T) {
	defer setLayout(140, 25)
	first := func(m model) string {
		view := ansi.Strip(m.render())
		at := len(view)
		name := ""
		for _, s := range helpSections {
			if i := strings.Index(view, "│    "+s.title+" "); i >= 0 && i < at {
				at, name = i, s.title
			}
		}
		return name
	}
	m, _ := update(t, detailsModel(t), tea.WindowSizeMsg{Width: 200, Height: 50})
	if m, _ := update(t, m, press("?")); first(m) != "stream" {
		t.Errorf("on the stream details the first section is %q", first(m))
	}
	m, _ = update(t, m, press("enter"), press("?"))
	if first(m) != "message" {
		t.Errorf("on a message the first section is %q", first(m))
	}
	m, _ = update(t, m, press("esc"), tea.WindowSizeMsg{Width: minContentWidth + chromeWidth, Height: minContentHeight + chromeHeight}, press("?"))
	if view := ansi.Strip(m.render()); !strings.Contains(view, "copy payload") || strings.Contains(view, "new stream") {
		t.Error("on the smallest terminal the help should keep the keys of the message alone")
	}
	m, _ = update(t, m, press("esc"), press("ctrl+d"))
	if m.page != messageDelete || m.helpPage() != messageDetails {
		t.Errorf("a dialog asked on a message should help with the message, page %v help %v", m.page, m.helpPage())
	}
}
