package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kontrolplane/tsui/pkg/client"
	"github.com/kontrolplane/tsui/pkg/tsui"
	"github.com/kontrolplane/tsui/pkg/tui/messages"
	"github.com/kontrolplane/tsui/pkg/tui/styles"
)

// settle feeds the messages of cmd back into the model, for forms that move on through commands.
// Commands that do not answer at once, like cursor blinks, are let go.
func settle(t *testing.T, m model, cmd tea.Cmd, depth int) model {
	t.Helper()
	if cmd == nil || depth == 0 {
		return m
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(50 * time.Millisecond):
		return m
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			m = settle(t, m, c, depth-1)
		}
		return m
	}
	if msg == nil {
		return m
	}
	m, cmd = update(t, m, msg)
	return settle(t, m, cmd, depth-1)
}

func pressSettled(t *testing.T, m model, keys ...string) model {
	t.Helper()
	for _, k := range keys {
		var cmd tea.Cmd
		m, cmd = update(t, m, press(k))
		m = settle(t, m, cmd, 4)
	}
	return m
}

func TestFlowActionsAreRefusedWhileDisconnected(t *testing.T) {
	m, _ := liveModel(t)
	m = openOrders(t, m)
	m.conn.Close()

	m, _ = update(t, m, press("ctrl+n"))
	m.state.messagePublish.subject.SetValue("orders.eu")
	m, cmd := update(t, m, press("ctrl+s"))
	if m.busy || cmd != nil || m.page != messagePublish || m.error != "not connected to nats, nothing was published" {
		t.Fatalf("expected the publish to be refused and the form kept, busy %v page %v error %q", m.busy, m.page, m.error)
	}
	m, _ = update(t, m, press("x"), press("esc"), press("esc"))

	m, _ = update(t, m, press("ctrl+d"), press("y"))
	if m.busy || m.page != messageDelete || !strings.Contains(m.error, "nothing was deleted") {
		t.Errorf("expected the delete to be refused, busy %v error %q", m.busy, m.error)
	}
	m, _ = update(t, m, press("x"), press("n"))

	m, _ = update(t, m, press("r"))
	if m.statusMsg != offlineStatus || m.statusTone != styles.ToneWarning {
		t.Errorf("expected r to say the data is cached, got %q", m.statusMsg)
	}
	m.refreshedAt = time.Now()
	if _, foot := m.frameMeta(); !strings.Contains(ansi.Strip(foot), "● closed") || strings.Contains(foot, "refreshed") {
		t.Errorf("expected the frame to show the connection is down, got %q", ansi.Strip(foot))
	}

	m, _ = update(t, m, press("ctrl+p"), press("y"))
	m = typeText(t, m, "ORDERS")
	m, _ = update(t, m, press("enter"))
	if m.busy || !strings.Contains(m.error, "nothing was purged") {
		t.Errorf("expected the purge to be refused, busy %v error %q", m.busy, m.error)
	}
}

func TestPublishWithoutAckSaysTheOutcomeIsUnknown(t *testing.T) {
	m := detailsModel(t)
	m, _ = update(t, m, press("ctrl+n"))
	m.state.messagePublish.subject.SetValue("orders.eu.created")
	m, _ = update(t, m, press("ctrl+s"))
	if !m.busy {
		t.Fatal("expected the publish to start")
	}
	if foot := ansi.Strip(m.renderFooter()); strings.Contains(foot, "esc") || strings.Contains(foot, "cancel") {
		t.Errorf("expected no cancel hint while publishing, got %q", foot)
	}
	m, _ = update(t, m, messages.MessagePublishedMsg{Stream: "ORDERS", Err: fmt.Errorf("publish: %w", context.DeadlineExceeded)})
	if m.page != messagePublish || strings.Contains(m.error, "could not publish") || !strings.Contains(m.error, "may still be stored") {
		t.Errorf("expected a timeout to leave the outcome open, got page %v error %q", m.page, m.error)
	}
	m, _ = update(t, m, press("x"))
	m, _ = update(t, m, messages.MessagePublishedMsg{Stream: "ORDERS", Err: errors.New("nats: API error: code=400 err_code=10065 description=subject does not match consumer")})
	if m.error != "could not publish: subject does not match consumer" {
		t.Errorf("expected the api error without its codes, got %q", m.error)
	}
}

func TestStreamCreateShowsFieldErrors(t *testing.T) {
	m := loadedModel(t)
	m, _ = update(t, m, press("ctrl+n"))
	m = typeText(t, m, "bad name!")
	m = pressSettled(t, m, "enter")
	if view := ansi.Strip(m.render()); !strings.Contains(view, "✗ only alphanumeric characters") {
		t.Fatalf("expected the name error on screen:\n%s", view)
	}

	m, _ = update(t, m, press("esc"), press("esc"), press("ctrl+n"))
	m = typeText(t, m, "GOOD")
	m = pressSettled(t, m, "enter", "enter", "enter", "enter", "enter", "enter", "enter")
	if m.state.streamCreate.currentStep != 2 {
		t.Fatalf("expected the limits step, got step %d", m.state.streamCreate.currentStep)
	}
	m = typeText(t, m, "abc")
	m = pressSettled(t, m, "enter")
	if view := ansi.Strip(m.render()); !strings.Contains(view, "✗ must be a positive number") {
		t.Errorf("expected the maximum messages error on screen:\n%s", view)
	}
}

func TestSubjectFilterShowsTheConsumersCoveringIt(t *testing.T) {
	m := detailsModel(t)
	m, _ = update(t, m, messages.ConsumersLoadedMsg{Stream: "ORDERS", Consumers: []tsui.Consumer{
		{Stream: "ORDERS", Name: "all"},
		{Stream: "ORDERS", Name: "eu", FilterSubjects: []string{"orders.eu.>"}},
		{Stream: "ORDERS", Name: "us", FilterSubjects: []string{"orders.us.*"}},
	}})
	m, _ = update(t, m, press("/"))
	m = typeText(t, m, "orders.eu.created")
	var names []string
	for _, c := range m.getFilteredConsumers() {
		names = append(names, c.Name)
	}
	if strings.Join(names, ",") != "all,eu" {
		t.Errorf("expected the consumers receiving orders.eu.created, got %v", names)
	}

	wide := tsui.Stream{Subjects: []string{">"}}
	if subjectLike("hello", wide, nil) {
		t.Error("expected a plain word not to be a subject on a stream taking every subject")
	}
	if !subjectLike("hello", wide, []tsui.SubjectCount{{Subject: "hello", Messages: 1}}) {
		t.Error("expected a plain word naming a known subject to be one")
	}
	if !subjectLike("orders", tsui.Stream{Subjects: []string{"orders"}}, nil) {
		t.Error("expected a subject of the stream to be one")
	}
}

func TestCreatedStreamIsUnderTheCursorOnReturn(t *testing.T) {
	m := loadedModel(t)
	m, _ = update(t, m, press("ctrl+n"))
	m, _ = update(t, m, messages.StreamCreatedMsg{Stream: tsui.Stream{Name: "INVOICES", Subjects: []string{"invoices.>"}}})
	if m.page != streamDetails {
		t.Fatalf("expected the details of the new stream, got page %v", m.page)
	}
	m, _ = update(t, m, press("q"))
	if s, ok := m.currentStream(); m.page != streamOverview || !ok || s.Name != "INVOICES" {
		t.Errorf("expected the cursor on INVOICES, got page %v stream %q", m.page, s.Name)
	}
	if n := len(m.state.streamOverview.streams); n != len(testStreams)+1 {
		t.Errorf("expected the new stream to be listed, got %d streams", n)
	}
}

func TestFollowResumesWithOnePress(t *testing.T) {
	m := detailsModel(t)
	foot := func(m model) string {
		_, f := m.frameMeta()
		return ansi.Strip(f)
	}
	m, _ = update(t, m, press("j"))
	if !strings.Contains(foot(m), "follow off") {
		t.Errorf("expected follow off once the cursor left the top row, got %q", foot(m))
	}
	m, _ = update(t, m, press("f"))
	d := m.state.streamDetails
	if !d.follow || d.selectedMessage != 0 || !strings.Contains(foot(m), "following") {
		t.Errorf("expected one f to jump to the newest message and follow, cursor %d foot %q", d.selectedMessage, foot(m))
	}
}

func TestDiscardPromptLeavesWithTheForm(t *testing.T) {
	m := loadedModel(t)
	m, _ = update(t, m, press("ctrl+n"))
	m = typeText(t, m, "X")
	m, _ = update(t, m, press("esc"))
	if m.statusMsg != discardPrompt {
		t.Fatalf("expected the discard prompt, got %q", m.statusMsg)
	}
	m, _ = update(t, m, press("esc"))
	if m.page != streamOverview || m.statusMsg != "" {
		t.Errorf("expected the prompt to go with the form, page %v status %q", m.page, m.statusMsg)
	}
}

func TestHeaderGivesTheUrlPriority(t *testing.T) {
	m := loadedModel(t)
	m.info = client.Info{Context: "local", URL: "nats://127.0.0.1:4222"}
	m, _ = update(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	defer setLayout(160, 50)
	id := "NDJ3TQ2K6L5QXKJXJ2QEVGTX7UYRMD3OGH4T5VKXRBSC6ACZ7YXVPNGU"
	m, _ = update(t, m, messages.ServerLoadedMsg{Server: tsui.Server{Name: id, Version: "2.12.0", Streams: 3}})
	header := ansi.Strip(m.renderHeader())
	if !strings.Contains(header, "nats://127.0.0.1:4222") {
		t.Errorf("expected the url in full:\n%s", header)
	}
	if !strings.Contains(header, "NDJ3TQ2K…") || strings.Contains(header, id) {
		t.Errorf("expected the server id shortened:\n%s", header)
	}
}

func TestConsumerStatus(t *testing.T) {
	for _, c := range []struct {
		consumer tsui.Consumer
		want     string
	}{
		{tsui.Consumer{}, "idle, no client pulling"},
		{tsui.Consumer{NumWaiting: 1}, "active"},
		{tsui.Consumer{LastActive: time.Now()}, "active"},
		{tsui.Consumer{Push: true}, "idle, no client bound"},
		{tsui.Consumer{Push: true, PushBound: true}, "active"},
		{tsui.Consumer{Paused: true, NumWaiting: 1}, "paused"},
	} {
		if got := styles.Render(consumerStatus(c.consumer)...); !strings.Contains(ansi.Strip(got), c.want) {
			t.Errorf("consumerStatus(%+v) = %q, want %q", c.consumer, ansi.Strip(got), c.want)
		}
	}
}

func TestWording(t *testing.T) {
	m := detailsModel(t)
	m, _ = update(t, m, press("space"), press("j"), press("space"), press("ctrl+d"))
	if view := ansi.Strip(m.MessageDeleteView()); !strings.Contains(view, "the sequences are removed") {
		t.Errorf("expected the dialog to speak of the sequences:\n%s", view)
	}

	purged := testStreams[2]
	purged.Messages, purged.FirstSeq, purged.LastSeq = 0, 241, 240
	m = detailsModel(t)
	m, _ = update(t, m,
		messages.StreamLoadedMsg{Name: "ORDERS", Stream: purged},
		messages.MessagesLoadedMsg{Stream: "ORDERS", Gen: m.msgsGen},
		messages.SubjectsLoadedMsg{Stream: "ORDERS"},
	)
	if view := ansi.Strip(m.StreamDetailsView()); !strings.Contains(view, "no messages in ORDERS, press ctrl+n") {
		t.Errorf("expected a purged stream not to wait for its first message:\n%s", view)
	}
	if !strings.Contains(m.state.streamDetails.subjectsTable.empty, "no messages in ORDERS") || strings.Contains(m.state.streamDetails.subjectsTable.empty, "yet") {
		t.Errorf("expected the subjects of a purged stream not to wait either, got %q", m.state.streamDetails.subjectsTable.empty)
	}

	s := tsui.Stream{Name: "S", Messages: 5, FirstSeq: 10, LastSeq: 20}
	for seq, want := range map[uint64]string{
		12: "message #12 was deleted",
		30: "no message #30 (stream holds #10–#20)",
	} {
		if got := missingMessage(s, seq); got != want {
			t.Errorf("missingMessage(%d) = %q, want %q", seq, got, want)
		}
	}
}

func TestSubjectsTabCountsBeforeTheyLoad(t *testing.T) {
	stream := testStreams[2]
	stream.NumSubjects = 7
	m := detailsModel(t)
	m, _ = update(t, m, messages.StreamLoadedMsg{Name: "ORDERS", Stream: stream})
	if bar := ansi.Strip(m.renderTabBar()); !strings.Contains(bar, "subjects 7") {
		t.Errorf("expected the subjects tab to count from the stream, got %q", bar)
	}
}

func TestPublishFlagsABadHeaderLineWhileTyping(t *testing.T) {
	m := detailsModel(t)
	m, _ = update(t, m, press("ctrl+n"), press("tab"))
	m = typeText(t, m, "Nats-Msg-Id: 1")
	if strings.Contains(ansi.Strip(m.MessagePublishView()), "✗ header line") {
		t.Error("expected a valid header not to be flagged")
	}
	m = typeText(t, m, "\nnot a header")
	m, _ = update(t, m, press("enter"))
	m = typeText(t, m, "x")
	if view := ansi.Strip(m.MessagePublishView()); !strings.Contains(view, "✗ header line 2 must look like") {
		t.Errorf("expected the bad line to be flagged under the headers:\n%s", view)
	}
	if m.error != "" {
		t.Errorf("expected no dialog while typing, got %q", m.error)
	}
}

func TestOutdatedConsumerListsAreDropped(t *testing.T) {
	m := detailsModel(t)
	fresh := []tsui.Consumer{{Stream: "ORDERS", Name: "billing"}, {Stream: "ORDERS", Name: "fresh"}}
	m, _ = update(t, m, messages.ConsumersLoadedMsg{Stream: "ORDERS", Gen: 2, Consumers: fresh})
	m, _ = update(t, m, press("tab"), press("j"), press("enter"))
	if m.page != consumerDetails || m.state.consumerDetails.consumer.Name != "fresh" {
		t.Fatalf("expected the details of fresh, got page %v", m.page)
	}
	m, _ = update(t, m, messages.ConsumersLoadedMsg{Stream: "ORDERS", Gen: 1, Consumers: fresh[:1]})
	if m.page != consumerDetails || m.statusMsg != "" {
		t.Errorf("expected an older list not to report fresh gone, page %v status %q", m.page, m.statusMsg)
	}
}
