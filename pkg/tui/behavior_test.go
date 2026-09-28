package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/kontrolplane/tsui/pkg/tsui"
	"github.com/kontrolplane/tsui/pkg/tui/messages"
	"github.com/kontrolplane/tsui/pkg/tui/styles"
)

var shiftTab = tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

// Server data must not carry terminal sequences into the view: OSC (hyperlinks, clipboard,
// title), attacker SGR such as conceal, or C1 controls sent as UTF-8.
func TestUntrustedTextIsEscaped(t *testing.T) {
	evil := "\x1b]8;;https://evil.example/\x07x\x1b]8;;\x07\x1b[8m\u009d0;t\u009c"
	check := func(where, view string) {
		t.Helper()
		for _, bad := range []string{"\x1b]", "\x1b[8m", "\u009d"} {
			if strings.Contains(view, bad) {
				t.Errorf("%s: raw %q reached the view", where, bad)
			}
		}
	}

	stream := tsui.Stream{Name: "S" + evil, Description: evil, Subjects: []string{"orders.>"}, LastSeq: 2}
	m, _ := update(t, newTestModel(), messages.StreamsLoadedMsg{Streams: []tsui.Stream{stream}})
	check("overview", m.render())

	m, _ = update(t, m, press("enter"))
	m, _ = update(t, m, messages.StreamLoadedMsg{Name: stream.Name, Stream: stream},
		messages.MessagesLoadedMsg{Stream: stream.Name, Messages: []tsui.Message{
			{Stream: stream.Name, Subject: "orders." + evil, Sequence: 2, Header: nats.Header{"X-Evil" + evil: {evil}}, Data: []byte(evil)},
			{Stream: stream.Name, Subject: "orders.json", Sequence: 1, Data: []byte(`{"k":"` + "\u009d0;t\u009c" + `"}`)},
		}})
	check("stream details", m.render())

	m, _ = update(t, m, press("enter"))
	if m.page != messageDetails {
		t.Fatalf("expected the message details, got %v", m.page)
	}
	check("message details", m.render())
	check("json payload", highlightBody([]byte(`{"k":"`+"\u009d0;t\u009c"+`"}`)))

	m.error = "could not load stream: " + evil
	check("error", m.render())
}

func TestCopyWarnsAboutControlCharacters(t *testing.T) {
	m := detailsModel(t)
	m, _ = update(t, m, messages.ClipboardCopiedMsg{Text: "plain\ttext\n"})
	if m.statusTone != styles.ToneSuccess || m.statusMsg != "copied to clipboard" {
		t.Errorf("unexpected status %q", m.statusMsg)
	}
	m, _ = update(t, m, messages.ClipboardCopiedMsg{Text: "a\x1b[2Jb"})
	if m.statusTone != styles.ToneWarning || !strings.Contains(m.statusMsg, "control characters") {
		t.Errorf("expected a warning for control characters, got %q", m.statusMsg)
	}
}

func TestActionInFlightIgnoresKeys(t *testing.T) {
	m := detailsModel(t)
	m, _ = update(t, m, press("ctrl+n"))
	m.state.messagePublish.subject.SetValue("orders.eu.created")
	m, cmd := update(t, m, press("ctrl+s"))
	if !m.busy || cmd == nil {
		t.Fatal("expected the publish to start")
	}
	if _, cmd = update(t, m, press("ctrl+s")); cmd != nil {
		t.Error("expected a second ctrl+s while publishing to do nothing")
	}
	if _, cmd = update(t, m, press("ctrl+c")); !isQuit(cmd) {
		t.Error("expected ctrl+c to still quit")
	}
	m, _ = update(t, m, messages.MessagePublishedMsg{Stream: "ORDERS", Sequence: 4})
	if m.busy || m.loading || m.page != streamDetails {
		t.Errorf("expected the result to end the action, busy %v page %v", m.busy, m.page)
	}

	m = detailsModel(t)
	m, _ = update(t, m, press("ctrl+d"), press("l"), press("enter"))
	if !m.busy || m.state.messageDelete.selected != 0 {
		t.Fatalf("expected the delete to start with the dialog back on no, selected %d", m.state.messageDelete.selected)
	}
	m, _ = update(t, m, press("q"))
	if m.page != messageDelete {
		t.Errorf("expected q while deleting to stay, got page %v", m.page)
	}
	// A refresh landing meanwhile keeps the spinner of the delete.
	m, _ = update(t, m, messages.StreamLoadedMsg{Name: "ORDERS", Stream: testStreams[2], Refresh: true})
	if !m.loading {
		t.Error("expected a background stream load not to end the loading of the delete")
	}
}

func TestStreamCreateSubmitsOnce(t *testing.T) {
	m := loadedModel(t)
	m, _ = update(t, m, press("ctrl+n"))
	m.state.streamCreate.input.name = "NEW"
	m.state.streamCreate.form.State = huh.StateCompleted
	m, cmd := update(t, m, messages.StatusClearMsg{Gen: 99})
	if !m.loading || cmd == nil {
		t.Fatal("expected the stream to be submitted")
	}
	m.spinning = true
	if _, cmd = update(t, m, messages.StatusClearMsg{Gen: 98}); cmd != nil {
		t.Error("expected an unrelated message while creating not to submit again")
	}
}

func TestResultsOnlyApplyToTheirPage(t *testing.T) {
	m := detailsModel(t)
	m, _ = update(t, m, press("q"))
	if m.page != streamOverview {
		t.Fatalf("expected the overview, got %v", m.page)
	}
	changed := testStreams[2]
	changed.Messages = 999
	m, _ = update(t, m,
		messages.StreamLoadedMsg{Name: "ORDERS", Stream: changed},
		messages.MessagesLoadedMsg{Stream: "ORDERS", Messages: []tsui.Message{{Sequence: 99}}},
		messages.ConsumersLoadedMsg{Stream: "ORDERS", Err: errors.New("boom")},
	)
	d := m.state.streamDetails
	if d.stream.Messages == 999 || len(d.messages) != 3 || m.error != "" {
		t.Errorf("expected late details results to be ignored on the overview, error %q", m.error)
	}

	m = detailsModel(t)
	m, _ = update(t, m, messages.StreamsLoadedMsg{Err: errors.New("boom")})
	if m.error != "" {
		t.Errorf("expected a stream list error to only show on the overview, got %q", m.error)
	}
}

func TestOutdatedMessageLoadsAreDropped(t *testing.T) {
	m := detailsModel(t)
	var older, newer tea.Cmd
	m, older = m.loadMessages()
	m, newer = m.loadMessages()
	if older == nil || newer == nil {
		t.Fatal("expected message loads")
	}
	m, _ = update(t, m,
		messages.MessagesLoadedMsg{Stream: "ORDERS", Gen: m.msgsGen, Messages: []tsui.Message{{Stream: "ORDERS", Sequence: 5}}},
		messages.MessagesLoadedMsg{Stream: "ORDERS", Gen: m.msgsGen - 1, Messages: []tsui.Message{{Stream: "ORDERS", Sequence: 4}}},
	)
	if got := m.state.streamDetails.messages; len(got) != 1 || got[0].Sequence != 5 {
		t.Errorf("expected the newer load to stay, got %+v", got)
	}
}

func TestRefreshReloadsMessagesOnlyWhenTheStreamChanged(t *testing.T) {
	m := detailsModel(t)
	gen := m.msgsGen
	m, _ = update(t, m, messages.StreamLoadedMsg{Name: "ORDERS", Stream: testStreams[2], Refresh: true})
	if m.msgsGen != gen {
		t.Error("expected an unchanged stream not to reload its messages")
	}
	changed := testStreams[2]
	changed.LastSeq = 241
	m, _ = update(t, m, messages.StreamLoadedMsg{Name: "ORDERS", Stream: changed, Refresh: true})
	if m.msgsGen != gen+1 {
		t.Error("expected a new message to reload the messages")
	}
	m, _ = update(t, m, press("r"))
	if m.msgsGen != gen+2 {
		t.Error("expected r to reload everything")
	}

	// The consumer details follow the stream, so the sequence rails move.
	m, _ = update(t, m, press("tab"), press("enter"))
	changed.LastSeq = 300
	m, _ = update(t, m, messages.StreamLoadedMsg{Name: "ORDERS", Stream: changed, Refresh: true})
	if m.state.streamDetails.stream.LastSeq != 300 {
		t.Error("expected the stream to be refreshed on the consumer details")
	}
}

func TestBackgroundErrorsGoToTheFooter(t *testing.T) {
	m := detailsModel(t)
	m, _ = update(t, m, messages.MessagesLoadedMsg{Stream: "ORDERS", Err: errors.New("boom")})
	if m.error != "" || m.statusTone != styles.ToneDanger || !strings.Contains(m.statusMsg, "boom") {
		t.Errorf("expected the refresh error in the footer, error %q status %q", m.error, m.statusMsg)
	}
	if len(m.state.streamDetails.messages) != 3 {
		t.Error("expected the messages on screen to stay")
	}

	m, _ = update(t, m, messages.ServerLoadedMsg{Server: tsui.Server{RTT: time.Millisecond}})
	m, _ = update(t, m, messages.ServerLoadedMsg{Err: errors.New("timeout")})
	if m.server.RTT != 0 {
		t.Error("expected a failed server load to clear the rtt")
	}
	if _, foot := m.frameMeta(); strings.Contains(ansi.Strip(foot), "rtt") {
		t.Error("expected no rtt without a connection")
	}
}

func TestStreamGoneWhileViewing(t *testing.T) {
	m := detailsModel(t)
	m, _ = update(t, m, messages.StreamLoadedMsg{Name: "ORDERS", Err: fmt.Errorf("x: %w", jetstream.ErrStreamNotFound)})
	if m.page != streamOverview || m.error != "" || m.statusMsg != "stream ORDERS no longer exists" {
		t.Errorf("expected to return to the overview, got page %v error %q status %q", m.page, m.error, m.statusMsg)
	}
	if len(m.getFilteredStreams()) != 2 {
		t.Error("expected the stream to leave the overview")
	}
}

func TestConsumerGoneWhileViewing(t *testing.T) {
	for name, msg := range map[string]tea.Msg{
		"list": messages.ConsumersLoadedMsg{Stream: "ORDERS"},
		"get":  messages.ConsumerLoadedMsg{Stream: "ORDERS", Name: "billing", Err: jetstream.ErrConsumerNotFound},
	} {
		m := detailsModel(t)
		m, _ = update(t, m, press("tab"), press("enter"))
		if m.page != consumerDetails {
			t.Fatalf("%s: expected the consumer details, got %v", name, m.page)
		}
		m, _ = update(t, m, msg)
		if m.page != streamDetails || m.state.streamDetails.tab != tabConsumers || m.statusMsg != "consumer billing no longer exists" {
			t.Errorf("%s: expected the consumers tab, got page %v status %q", name, m.page, m.statusMsg)
		}
	}

	m := detailsModel(t)
	m, _ = update(t, m, press("tab"), press("enter"))
	m, _ = update(t, m, messages.ConsumerLoadedMsg{Stream: "ORDERS", Name: "billing", Consumer: tsui.Consumer{Stream: "ORDERS", Name: "billing", Delivered: 7}})
	if m.state.consumerDetails.consumer.Delivered != 7 || m.state.streamDetails.consumers[0].Delivered != 7 {
		t.Error("expected the consumer to be refreshed")
	}
}

func TestCursorStaysOnItsItem(t *testing.T) {
	now := time.Now()
	newer := []tsui.Message{
		{Stream: "ORDERS", Subject: "orders.eu.created", Sequence: 4, Time: now},
		{Stream: "ORDERS", Subject: "orders.eu.created", Sequence: 3, Time: now},
		{Stream: "ORDERS", Subject: "orders.us.created", Sequence: 2, Time: now},
		{Stream: "ORDERS", Subject: "orders.eu.shipped", Sequence: 1, Time: now},
	}

	m := detailsModel(t)
	m, _ = update(t, m, press("j"), messages.MessagesLoadedMsg{Stream: "ORDERS", Messages: newer})
	if cur, _ := m.currentMessage(); cur.Sequence != 2 {
		t.Errorf("expected the cursor to stay on sequence 2, got %d", cur.Sequence)
	}

	m = detailsModel(t)
	m, _ = update(t, m, messages.MessagesLoadedMsg{Stream: "ORDERS", Messages: newer})
	if cur, _ := m.currentMessage(); cur.Sequence != 4 {
		t.Errorf("expected the top row to follow the newest message, got %d", cur.Sequence)
	}

	m = loadedModel(t)
	m, _ = update(t, m, press("j"), press("j"))
	m, _ = update(t, m, messages.StreamsLoadedMsg{Streams: append([]tsui.Stream{{Name: "ALPHA"}}, testStreams...)})
	if s, _ := m.currentStream(); s.Name != "ORDERS" {
		t.Errorf("expected the cursor to stay on ORDERS, got %s", s.Name)
	}
}

func TestSelectionOnlyHoldsListedItems(t *testing.T) {
	m := detailsModel(t)
	m, _ = update(t, m, press("space"))
	m, _ = update(t, m, messages.MessagesLoadedMsg{Stream: "ORDERS", Messages: []tsui.Message{
		{Stream: "ORDERS", Subject: "orders.eu.created", Sequence: 5},
		{Stream: "ORDERS", Subject: "orders.eu.created", Sequence: 4},
	}})
	if n := m.detailsSelectionCount(); n != 0 {
		t.Errorf("expected the selection of a message no longer listed to be dropped, got %d", n)
	}

	if got := selectedOr(map[uint64]bool{99: true}, []tsui.Message{{Sequence: 1}}, func(m tsui.Message) uint64 { return m.Sequence },
		func() (tsui.Message, bool) { return tsui.Message{Sequence: 1}, true }); len(got) != 0 {
		t.Errorf("expected a selection not to fall back to the cursor, got %v", got)
	}

	m = detailsModel(t)
	m, _ = update(t, m, press("space"), messages.StreamPurgedMsg{Stream: "ORDERS"})
	if len(m.state.streamDetails.selectedMessages) != 0 {
		t.Error("expected a purge to clear the message selection")
	}
}

func TestStreamSelectionHiddenByTheFilter(t *testing.T) {
	m := loadedModel(t)
	m, _ = update(t, m, press("space"), press("/"))
	m = typeText(t, m, "orders")
	m, _ = update(t, m, press("enter"), press("ctrl+d"))
	if m.page != streamOverview || m.statusTone != styles.ToneWarning {
		t.Fatalf("expected a hidden selection not to be deleted, got page %v status %q", m.page, m.statusMsg)
	}
	m, _ = update(t, m, press("space"), press("ctrl+d"))
	if m.page != streamDelete || len(m.state.streamDelete.streams) != 1 || m.state.streamDelete.streams[0] != "ORDERS" {
		t.Fatalf("expected only the visible ORDERS, got %v", m.state.streamDelete.streams)
	}
	if view := ansi.Strip(m.StreamDeleteView()); !strings.Contains(view, "1 selected stream hidden by the filter is not included") {
		t.Errorf("expected the dialog to mention the hidden stream:\n%s", view)
	}
}

func TestEscNeverQuitsTheOverview(t *testing.T) {
	m := loadedModel(t)
	m, _ = update(t, m, press("/"))
	m = typeText(t, m, "ORD")
	m, _ = update(t, m, press("enter"))
	m, cmd := update(t, m, press("esc"))
	if isQuit(cmd) || m.state.streamOverview.filterText != "" {
		t.Fatal("expected esc to clear the filter")
	}
	if _, cmd = update(t, m, press("esc")); isQuit(cmd) {
		t.Error("expected esc not to quit")
	}
	if _, cmd = update(t, m, press("q")); !isQuit(cmd) {
		t.Error("expected q to quit")
	}
}

func TestBackKeepsTheOverviewAsItWas(t *testing.T) {
	m := loadedModel(t)
	m, _ = update(t, m, press("/"))
	m = typeText(t, m, "O")
	m, _ = update(t, m, press("enter"), press("j"), press("enter"))
	if m.state.streamDetails.stream.Name != "ORDERS" {
		t.Fatalf("expected to open ORDERS, got %s", m.state.streamDetails.stream.Name)
	}
	m, _ = update(t, m, press("q"), messages.StreamsLoadedMsg{Streams: testStreams})
	s, _ := m.currentStream()
	if m.page != streamOverview || m.state.streamOverview.filterText != "O" || s.Name != "ORDERS" {
		t.Errorf("expected the filter and cursor to be kept, got filter %q cursor on %s", m.state.streamOverview.filterText, s.Name)
	}
}

func TestSubjectDrillReturnsToTheSubject(t *testing.T) {
	m := detailsModel(t)
	m, _ = update(t, m, press("tab"), shiftTab)
	if m.state.streamDetails.tab != tabMessages {
		t.Fatalf("expected shift+tab to go back a tab, got %v", m.state.streamDetails.tab)
	}
	m, _ = update(t, m, shiftTab, messages.SubjectsLoadedMsg{Stream: "ORDERS", Subjects: []tsui.SubjectCount{
		{Subject: "orders.eu.created", Messages: 1}, {Subject: "orders.eu.shipped", Messages: 1}, {Subject: "orders.us.created", Messages: 1},
	}})
	m, _ = update(t, m, press("j"), press("j"), press("enter"))
	if d := m.state.streamDetails; d.tab != tabMessages || d.filterText != "orders.us.created" {
		t.Fatalf("expected the messages on orders.us.created, got tab %v filter %q", d.tab, d.filterText)
	}
	m, _ = update(t, m, press("q"))
	d := m.state.streamDetails
	if s, _ := m.currentSubject(); d.tab != tabSubjects || d.filterText != "" || s.Subject != "orders.us.created" {
		t.Errorf("expected q to return to the subject, got tab %v filter %q subject %q", d.tab, d.filterText, s.Subject)
	}
}

func TestFooterHelpFollowsTheTab(t *testing.T) {
	m := detailsModel(t)
	for _, want := range []struct {
		has, hasNot string
	}{
		{"copy payload", "copy name"},
		{"copy name", "publish"},
		{"copy subject", "select"},
	} {
		help := ansi.Strip(m.renderShortHelp(frameWidth))
		if !strings.Contains(help, want.has) || strings.Contains(help, want.hasNot) {
			t.Errorf("tab %v: unexpected help %q", m.state.streamDetails.tab, help)
		}
		m, _ = update(t, m, press("tab"))
	}
}

func TestYesNoShortcuts(t *testing.T) {
	m := detailsModel(t)
	m, cmd := update(t, m, press("ctrl+d"), press("y"))
	if !m.busy || cmd == nil {
		t.Error("expected y to confirm the delete")
	}
	m = detailsModel(t)
	m, _ = update(t, m, press("ctrl+d"), press("n"))
	if m.page != streamDetails || m.busy {
		t.Errorf("expected n to cancel, got page %v", m.page)
	}
}

func TestMessageDetailsTopAndBottom(t *testing.T) {
	m := detailsModel(t)
	m.state.streamDetails.messages[0].Data = []byte(strings.Repeat("line\n", 200))
	m = m.updateMessagesTable()
	m, _ = update(t, m, press("enter"), press("G"))
	if m.state.messageDetails.viewport.YOffset() == 0 {
		t.Fatal("expected G to scroll to the bottom")
	}
	m, _ = update(t, m, press("g"))
	if m.state.messageDetails.viewport.YOffset() != 0 {
		t.Error("expected g to scroll to the top")
	}
}

func TestConsumerWildcardFilter(t *testing.T) {
	all := tsui.Consumer{Name: "all"}
	shipped := tsui.Consumer{Name: "shipping", FilterSubjects: []string{"orders.*.shipped"}}
	payments := tsui.Consumer{Name: "payments", FilterSubjects: []string{"payments.>"}}
	for c, want := range map[*tsui.Consumer]bool{&all: true, &shipped: true, &payments: false} {
		if got := consumerMatches(*c, "orders.eu.*", true); got != want {
			t.Errorf("consumerMatches(%s, orders.eu.*) = %v, want %v", c.Name, got, want)
		}
	}
	if !consumerMatches(shipped, "SHIP", false) {
		t.Error("expected a plain filter to match the name ignoring case")
	}
}

func TestOverviewFilterMatching(t *testing.T) {
	for _, c := range []struct {
		subject, filter string
		want            bool
	}{
		{"ORDERS.>", "orders.eu", true},
		{"orders.eu", "*.eu", true},
		{"orders.eu", "ORD", true},
		{"events.*", "events.a.b", false},
	} {
		if got := streamMatches(tsui.Stream{Name: "X", Subjects: []string{c.subject}}, c.filter); got != c.want {
			t.Errorf("streamMatches(%s, %q) = %v, want %v", c.subject, c.filter, got, c.want)
		}
	}
}

func TestPurgeSubjects(t *testing.T) {
	s := tsui.Stream{Subjects: []string{"orders.>", "payments.*"}}
	for subject, want := range map[string]bool{
		"orders.eu": true, "orders.*": true, ">": true, "payments.card": true, "*.eu": true,
		"payments.a.b": false, "refunds.eu": false, "payments.*.eu": false,
	} {
		if got := subjectInStream(s, subject); got != want {
			t.Errorf("subjectInStream(%q) = %v, want %v", subject, got, want)
		}
	}
	for subject, want := range map[string]bool{"": true, ">": true, "orders.eu": false, "*.*": false} {
		if got := coversStream(s, subject); got != want {
			t.Errorf("coversStream(%q) = %v, want %v", subject, got, want)
		}
	}
	for _, c := range []struct {
		filter, pattern string
		want            bool
	}{
		{"orders.>", "orders.>", true},
		{"orders.>", "orders.*", true},
		{"orders.*", "orders.>", false},
		{"*.*", "orders.*", true},
		{"orders.eu", "orders.*", false},
	} {
		if got := covers(c.filter, c.pattern); got != c.want {
			t.Errorf("covers(%q, %q) = %v, want %v", c.filter, c.pattern, got, c.want)
		}
	}
	for s, want := range map[string]bool{"orders.eu": true, "orders.>": true, "orders..eu": false, "orders.>.eu": false, "a b": false} {
		if got := validSubjectFilter(s); got != want {
			t.Errorf("validSubjectFilter(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestFilterExplainsTheNewestWindow(t *testing.T) {
	m := detailsModel(t)
	m, _ = update(t, m, press("/"))
	m = typeText(t, m, "nothing-matches")
	if !strings.Contains(m.StreamDetailsView(), "no match among the newest 3 messages") {
		t.Error("expected the empty filter state to mention the loaded window")
	}
}

func TestStreamDeleteAsksToTypeTheName(t *testing.T) {
	m := loadedModel(t)
	m, _ = update(t, m, press("ctrl+d"))
	if m.page != streamDelete || !m.textInputActive() {
		t.Fatalf("expected the delete dialog, got %v", m.page)
	}
	m = typeText(t, m, "EVENT")
	m, cmd := update(t, m, press("enter"))
	if m.busy || cmd != nil {
		t.Fatal("expected enter to do nothing before the name matches")
	}
	if !strings.Contains(ansi.Strip(m.StreamDeleteView()), "✗") {
		t.Error("expected a mismatch mark")
	}
	m = typeText(t, m, "S")
	if !strings.Contains(ansi.Strip(m.StreamDeleteView()), "✓") {
		t.Error("expected a match mark")
	}
	m, cmd = update(t, m, press("enter"))
	if !m.busy || cmd == nil {
		t.Fatal("expected the delete to start")
	}

	m = loadedModel(t)
	m, _ = update(t, m, press("space"), press("j"), press("space"), press("ctrl+d"))
	m = typeText(t, m, "q?")
	if m.page != streamDelete || m.showHelp || m.state.streamDelete.confirm.input.Value() != "q?" {
		t.Fatal("expected q and ? to be typed")
	}
	view := ansi.Strip(m.StreamDeleteView())
	for _, want := range []string{"delete 2 streams", "EVENTS", "JOBS"} {
		if !strings.Contains(view, want) {
			t.Errorf("expected the dialog to contain %q:\n%s", want, view)
		}
	}
	m, _ = update(t, m, press("esc"))
	if m.page != streamOverview {
		t.Error("expected esc to cancel")
	}
}

func TestPurge(t *testing.T) {
	// A purge of the whole stream asks for its name.
	m := detailsModel(t)
	m, _ = update(t, m, press("ctrl+p"), press("y"))
	p := m.state.streamPurge
	if p.step != purgeStepType || m.busy {
		t.Fatalf("expected to be asked for the name, got step %v", p.step)
	}
	if view := ansi.Strip(m.StreamPurgeView()); !strings.Contains(view, "all 240 messages") {
		t.Errorf("unexpected view:\n%s", view)
	}
	m = typeText(t, m, "ORDERS")
	m, cmd := update(t, m, press("enter"))
	if !m.busy || cmd == nil || m.state.streamPurge.subject != "" {
		t.Fatal("expected the purge to start")
	}

	// So does a subject that covers every subject of the stream.
	m = detailsModel(t)
	m, _ = update(t, m, press("ctrl+p"), press("tab"))
	m = typeText(t, m, ">")
	m, _ = update(t, m, press("tab"), press("y"))
	if m.state.streamPurge.step != purgeStepType {
		t.Error("expected > to be treated as a purge of everything")
	}

	// A subject of unknown size asks twice.
	m = detailsModel(t)
	m, _ = update(t, m, press("ctrl+p"), press("tab"))
	m = typeText(t, m, "orders.eu.*")
	m, _ = update(t, m, press("tab"))
	if view := ansi.Strip(m.StreamPurgeView()); !strings.Contains(view, "an unknown number of messages") {
		t.Errorf("expected the size to be unknown:\n%s", view)
	}
	m, _ = update(t, m, press("y"))
	if m.state.streamPurge.step != purgeStepSure || m.busy {
		t.Fatalf("expected a second prompt, got step %v", m.state.streamPurge.step)
	}
	m, cmd = update(t, m, press("y"))
	if !m.busy || cmd == nil || m.state.streamPurge.subject != "orders.eu.*" {
		t.Fatal("expected the subject purge to start")
	}
	m, _ = update(t, m, messages.StreamPurgedMsg{Stream: "ORDERS"})
	if m.page != streamDetails || m.statusMsg != "purged ORDERS (orders.eu.*)" {
		t.Errorf("unexpected result, page %v status %q", m.page, m.statusMsg)
	}

	// A small subject with a known size purges at once.
	m = detailsModel(t)
	m, _ = update(t, m, messages.SubjectsLoadedMsg{Stream: "ORDERS", Subjects: []tsui.SubjectCount{{Subject: "orders.eu.created", Messages: 3}}})
	m, _ = update(t, m, press("ctrl+p"), press("tab"))
	m = typeText(t, m, "orders.eu.created")
	m, _ = update(t, m, press("tab"), press("y"))
	if !m.busy {
		t.Error("expected a small subject purge to start at once")
	}

	m = detailsModel(t)
	m, _ = update(t, m, press("ctrl+p"), press("tab"))
	m = typeText(t, m, "orders..eu")
	m, _ = update(t, m, press("tab"), press("y"))
	if !strings.Contains(m.error, "not a valid subject") {
		t.Errorf("expected an invalid subject to be refused, got %q", m.error)
	}
}

func TestDialogsListWhatTheyAffect(t *testing.T) {
	m := detailsModel(t)
	m.state.messageDelete.sequences = []uint64{10, 9, 8, 7, 6, 5, 4, 3, 2, 1}
	m, _ = m.MessageDeleteSwitchPage()
	view := ansi.Strip(m.MessageDeleteView())
	for _, want := range []string{"10 messages", "#10", "#3", "… +2 more"} {
		if !strings.Contains(view, want) {
			t.Errorf("expected %q in:\n%s", want, view)
		}
	}
	if strings.Contains(view, "#2,") {
		t.Error("expected the list to stop after 8")
	}
}

func TestResultsAreReported(t *testing.T) {
	m := loadedModel(t)
	m.state.streamDelete.streams = []string{"EVENTS"}
	m, _ = update(t, m, messages.StreamsDeletedMsg{Names: []string{"EVENTS"}})
	if m.statusMsg != "deleted stream EVENTS" || len(m.state.streamOverview.streams) != 2 {
		t.Errorf("unexpected status %q", m.statusMsg)
	}

	m = loadedModel(t)
	m.state.streamDelete.streams = []string{"EVENTS", "JOBS"}
	m, _ = update(t, m, messages.StreamsDeletedMsg{Names: []string{"EVENTS"}, Err: errors.New("deleting stream JOBS: denied")})
	if !strings.Contains(m.error, "deleted 1 of 2 streams") || !strings.Contains(m.error, "JOBS: denied") {
		t.Errorf("expected a partial failure, got %q", m.error)
	}

	m = detailsModel(t)
	m.state.consumerDelete.consumers = []string{"billing"}
	m, _ = update(t, m, messages.ConsumersDeletedMsg{Stream: "ORDERS", Names: []string{"billing"}})
	if m.statusMsg != "deleted consumer billing" || m.state.streamDetails.tab != tabConsumers {
		t.Errorf("unexpected status %q", m.statusMsg)
	}

	m = detailsModel(t)
	m.state.messageDelete.sequences = []uint64{3, 2}
	m, _ = update(t, m, messages.MessagesDeletedMsg{Stream: "ORDERS", Sequences: []uint64{3, 2}})
	if m.statusMsg != "deleted 2 messages from ORDERS" || len(m.state.streamDetails.messages) != 1 {
		t.Errorf("unexpected status %q", m.statusMsg)
	}

	m = detailsModel(t)
	m, _ = update(t, m, messages.MessagePublishedMsg{Stream: "ORDERS", Sequence: 4})
	if m.statusMsg != "published to ORDERS at #4" {
		t.Errorf("unexpected status %q", m.statusMsg)
	}
	m, _ = update(t, m, messages.MessagePublishedMsg{Stream: "ORDERS", Sequence: 2, Duplicate: true})
	if m.statusMsg != "duplicate Nats-Msg-Id, already stored at #2" || m.statusTone != styles.ToneWarning {
		t.Errorf("unexpected status %q", m.statusMsg)
	}

	m = loadedModel(t)
	m, _ = update(t, m, messages.StreamCreatedMsg{Stream: tsui.Stream{Name: "NEW"}})
	if m.page != streamDetails || m.statusMsg != "created stream NEW" {
		t.Errorf("unexpected status %q", m.statusMsg)
	}
}

func TestStreamCreateKeepsTheInputOnError(t *testing.T) {
	m := loadedModel(t)
	m, _ = update(t, m, press("ctrl+n"))
	m = typeText(t, m, "FOO")
	m.state.streamCreate.input.maxAge = "1h"
	m, _ = update(t, m, messages.StreamCreatedMsg{Err: errors.New("insufficient resources")})
	if m.page != streamCreate || !strings.Contains(m.error, "insufficient resources") {
		t.Fatalf("expected the form with the error, got page %v error %q", m.page, m.error)
	}
	m, _ = update(t, m, press("x"))
	if m.page != streamCreate || m.state.streamCreate.input.name != "FOO" || !strings.Contains(ansi.Strip(m.StreamCreateView()), "FOO") {
		t.Error("expected the form to keep what was entered")
	}

	m.state.streamCreate.input.duplicates = "2h"
	m, _ = m.submitStreamCreate()
	if m.page != streamCreate || !strings.Contains(m.error, "maximum age") || m.busy {
		t.Errorf("expected a duplicate window longer than the maximum age to be refused, got %q", m.error)
	}
}

func TestStreamCreateInputChecks(t *testing.T) {
	if _, err := parseBytes("9999999999TB"); err == nil {
		t.Error("expected an overflowing size to fail")
	}
	if err := validateDuplicates("2m", "1h"); err != nil {
		t.Error(err)
	}
	if err := validateDuplicates("2h", ""); err != nil {
		t.Error(err)
	}
	if _, opts := replicaOptions(false); len(opts) != 1 || opts[0].Value != 1 {
		t.Errorf("expected a single replica without a cluster, got %v", opts)
	}
	if _, opts := replicaOptions(true); len(opts) != 3 {
		t.Errorf("expected 1, 3 or 5 replicas in a cluster, got %v", opts)
	}
	cfg, err := buildStreamConfig(&streamCreateInput{name: "Orders"})
	if err != nil || cfg.Subjects[0] != "orders.>" {
		t.Errorf("expected the subjects to default to the lowercased name, got %v %v", cfg.Subjects, err)
	}
}

func TestEscDiscardsAFormWithInputOnTheSecondPress(t *testing.T) {
	m := loadedModel(t)
	m, _ = update(t, m, press("ctrl+n"), press("esc"))
	if m.page != streamOverview {
		t.Fatal("expected esc to leave an empty form at once")
	}
	m, _ = update(t, m, press("ctrl+n"))
	m = typeText(t, m, "X")
	m, _ = update(t, m, press("esc"))
	if m.page != streamCreate || !strings.Contains(m.statusMsg, "esc again") {
		t.Fatalf("expected the first esc to ask, got page %v", m.page)
	}
	m = typeText(t, m, "Y")
	m, _ = update(t, m, press("esc"))
	if m.page != streamCreate {
		t.Fatal("expected typing to disarm the discard")
	}
	m, _ = update(t, m, press("esc"))
	if m.page != streamOverview {
		t.Error("expected the second esc to discard")
	}

	m = detailsModel(t)
	m, _ = update(t, m, press("ctrl+n"), press("esc"))
	if m.page != streamDetails {
		t.Fatal("expected esc to leave an untouched publish form at once")
	}
	m, _ = update(t, m, press("ctrl+n"), press("tab"), press("tab"))
	m = typeText(t, m, "hello")
	m, _ = update(t, m, press("esc"))
	if m.page != messagePublish {
		t.Fatal("expected the first esc to ask")
	}
	m, _ = update(t, m, press("esc"))
	if m.page != streamDetails {
		t.Error("expected the second esc to discard")
	}
}

func TestPublishHeadersAndPayload(t *testing.T) {
	h, err := parseHeaders("nats-msg-id: abc\nNATS-TTL: 1s\nx-app: y")
	if err != nil {
		t.Fatal(err)
	}
	if h["Nats-Msg-Id"][0] != "abc" || h["Nats-TTL"][0] != "1s" || h["x-app"][0] != "y" {
		t.Errorf("expected the nats headers to be spelled as the server expects, got %v", h)
	}
	for body, want := range map[string]string{`{"a":1}`: "json ✓", `[1,`: "invalid json", "hello": "B"} {
		if got := ansi.Strip(payloadMeta(body)); !strings.Contains(got, want) {
			t.Errorf("payloadMeta(%q) = %q, want %q", body, got, want)
		}
	}
}

func TestPreviewScansOnlyWhatItShows(t *testing.T) {
	data := []byte("  a\n\tb   " + strings.Repeat("x", 1<<20))
	if got := preview(data, 10); got != "a b xxxxx…" {
		t.Errorf("unexpected preview %q", got)
	}
	if got := truncate("日本語テキスト", 7); got != "日本語…" {
		t.Errorf("unexpected wide truncation %q", got)
	}
}

func TestLeavingAPageWhileItLoads(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m, _ = update(t, m, press("ctrl+n"))
	if m.page != streamCreate || m.loading {
		t.Fatalf("expected the create form without a spinner, got page %v loading %v", m.page, m.loading)
	}
	m = typeText(t, m, "A")
	if m.state.streamCreate.input.name != "A" {
		t.Error("expected the form to take input")
	}
	m, _ = update(t, m, messages.StreamsLoadedMsg{Streams: testStreams})
	if m.page != streamCreate || m.loading {
		t.Error("expected the stream list to load in the background")
	}
}
