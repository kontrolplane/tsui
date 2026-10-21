package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestFlowRedriveMovesSelectedMessages(t *testing.T) {
	m, js := liveModel(t)
	m = openOrders(t, m)

	m = step(t, m, "space", "j", "space", "ctrl+r")
	r := m.state.messageRedrive
	if m.page != messageRedrive || r.fromDetails || len(r.sequences) != 2 || len(r.subjects) != 2 {
		t.Fatalf("expected the redrive dialog for 2 messages, got page %v %+v", m.page, r)
	}
	// Typing goes into the subject, q included.
	m = typeText(t, m, "events.q")
	if m.page != messageRedrive || m.redriveSubject() != "events.q" {
		t.Fatalf("expected the subject to take the text, got %q on page %v", m.redriveSubject(), m.page)
	}
	if view := ansi.Strip(m.render()); !strings.Contains(view, "stored by EVENTS") || !strings.Contains(view, "redrive 2 messages from ORDERS") {
		t.Errorf("expected the dialog to name the target stream:\n%s", view)
	}
	m = step(t, m, "tab", "space", "tab", "y")
	if m.page != streamDetails || m.error != "" || m.statusMsg != "moved 2 messages to EVENTS" {
		t.Fatalf("expected to return moved, got page %v error %q status %q", m.page, m.error, m.statusMsg)
	}
	d := m.state.streamDetails
	if len(d.messages) != 10 || len(d.selectedMessages) != 0 {
		t.Errorf("expected 10 messages and no selection left, got %d and %d", len(d.messages), len(d.selectedMessages))
	}
	if s := streamState(t, js, "ORDERS"); s.Messages != 10 {
		t.Errorf("expected 10 messages in ORDERS, got %d", s.Messages)
	}
	if s := streamState(t, js, "EVENTS"); s.Messages != 2 {
		t.Errorf("expected 2 messages in EVENTS, got %d", s.Messages)
	}
}

func TestFlowRedriveFromMessageDetails(t *testing.T) {
	m, js := liveModel(t)
	m = openOrders(t, m)

	m = step(t, m, "enter", "ctrl+r")
	if m.page != messageRedrive || !m.state.messageRedrive.fromDetails || m.helpPage() != messageDetails {
		t.Fatalf("expected the redrive dialog over the message, got %v", m.page)
	}
	if view := ansi.Strip(m.render()); !strings.Contains(view, "stored again by ORDERS") {
		t.Errorf("expected a warning that the message goes back into its own stream:\n%s", view)
	}
	m = step(t, m, "esc")
	if m.page != messageDetails {
		t.Fatalf("expected esc to return to the message, got %v", m.page)
	}

	// enter moves through the fields, the buttons start on no.
	m = step(t, m, "ctrl+r", "enter", "enter", "enter")
	if m.page != messageDetails || streamState(t, js, "ORDERS").Messages != 12 {
		t.Fatalf("expected no to cancel, got page %v", m.page)
	}
	m = step(t, m, "ctrl+r", "enter", "enter", "l", "enter")
	if m.page != messageDetails || m.statusMsg != "redrove 1 message to ORDERS" {
		t.Fatalf("expected to return to the message, got page %v status %q error %q", m.page, m.statusMsg, m.error)
	}
	if s := streamState(t, js, "ORDERS"); s.Messages != 13 {
		t.Errorf("expected the message to be stored again, got %d", s.Messages)
	}
}

func TestRedriveRefusesInvalidSubjectAndDenyDelete(t *testing.T) {
	m, js := liveModel(t)
	m = openOrders(t, m)
	m.state.streamDetails.stream.DenyDelete = true

	m = step(t, m, "ctrl+r")
	m = typeText(t, m, "events.*")
	m = step(t, m, "tab", "space")
	if m.state.messageRedrive.remove {
		t.Error("expected deleting to stay off on a deny_delete stream")
	}
	m = step(t, m, "tab", "y")
	if m.busy || !strings.Contains(m.error, "not a valid subject") {
		t.Fatalf("expected the wildcard subject to be refused, got error %q", m.error)
	}
	if s := streamState(t, js, "EVENTS"); s.Messages != 0 {
		t.Errorf("expected nothing redriven, got %d", s.Messages)
	}
}
