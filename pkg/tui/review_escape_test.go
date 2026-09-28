package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/kontrolplane/tsui/pkg/tsui"
	"github.com/kontrolplane/tsui/pkg/tui/messages"
)

func TestReviewServerAcceptsEscInStreamName(t *testing.T) {
	_, js := liveModel(t)
	_, err := tsui.CreateStream(context.Background(), js, tsui.StreamConfig{Name: "S\x1b]8;;x\x07", Subjects: []string{"esc.>"}})
	t.Logf("create stream with ESC in name: err=%v", err)
}

func TestReviewUntrustedTextInEmptyStatesAndLoading(t *testing.T) {
	evil := "\x1b]8;;https://evil.example/\x07x\x1b]8;;\x07"
	check := func(where, view string) {
		t.Helper()
		if strings.Contains(view, "\x1b]") {
			t.Errorf("%s: raw OSC reached the view", where)
		}
	}
	stream := tsui.Stream{Name: "S" + evil, Subjects: []string{"orders.>"}}
	m, _ := update(t, newTestModel(), messages.StreamsLoadedMsg{Streams: []tsui.Stream{stream}})
	m, _ = update(t, m, press("enter"))
	m, _ = update(t, m, messages.StreamLoadedMsg{Name: stream.Name, Stream: stream},
		messages.MessagesLoadedMsg{Stream: stream.Name, Gen: m.msgsGen},
		messages.ConsumersLoadedMsg{Stream: stream.Name})
	check("empty messages", m.render())
	m, _ = update(t, m, press("tab"))
	check("empty consumers", m.render())
	m, _ = update(t, m, press("tab"))
	m, _ = update(t, m, messages.SubjectsLoadedMsg{Stream: stream.Name})
	check("empty subjects", m.render())

	m.loading, m.loadingMsg = true, "purging "+stream.Name+"…"
	check("loading", m.render())
}

func TestReviewUntrustedSubjectInTabHint(t *testing.T) {
	evil := "\x1b]8;;https://evil.example/\x07x"
	stream := tsui.Stream{Name: "S", Subjects: []string{"orders.>"}, Messages: 500}
	m, _ := update(t, newTestModel(), messages.StreamsLoadedMsg{Streams: []tsui.Stream{stream}})
	m, _ = update(t, m, press("enter"))
	m, _ = update(t, m, messages.StreamLoadedMsg{Name: "S", Stream: stream}, messages.ConsumersLoadedMsg{Stream: "S"})
	m, _ = update(t, m, press("tab"), press("tab"))
	m, _ = update(t, m, messages.SubjectsLoadedMsg{Stream: "S", Subjects: []tsui.SubjectCount{{Subject: "orders." + evil, Messages: 300}}})
	m, _ = update(t, m, press("enter"))
	m, _ = update(t, m, messages.MessagesLoadedMsg{Stream: "S", Subject: "orders." + evil, Gen: m.msgsGen, Next: 5,
		Messages: []tsui.Message{{Stream: "S", Subject: "orders." + evil, Sequence: 9}}})
	if v := m.render(); strings.Contains(v, "\x1b]") {
		t.Error("raw OSC from a subject reached the tab bar hint")
	}
}
