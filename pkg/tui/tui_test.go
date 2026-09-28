package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kontrolplane/tsui/pkg/tsui"
	"github.com/kontrolplane/tsui/pkg/tui/messages"
	"github.com/kontrolplane/tsui/pkg/tui/styles"
)

func newTestModel() model {
	m := newModel("test", "tsui")
	m.width = 160
	m.height = 50
	m.loading = false
	return m
}

func press(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	}
	if ctrl, ok := strings.CutPrefix(s, "ctrl+"); ok {
		return tea.KeyPressMsg{Code: rune(ctrl[0]), Mod: tea.ModCtrl}
	}
	return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
}

func update(t *testing.T, m model, msgs ...tea.Msg) (model, tea.Cmd) {
	t.Helper()
	var cmd tea.Cmd
	for _, msg := range msgs {
		var next tea.Model
		next, cmd = m.Update(msg)
		m = next.(model)
	}
	return m, cmd
}

func typeText(t *testing.T, m model, text string) model {
	t.Helper()
	for _, r := range text {
		m, _ = update(t, m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return m
}

var testStreams = []tsui.Stream{
	{Name: "EVENTS", Subjects: []string{"events.*"}, Storage: "memory", Retention: "limits", Messages: 3},
	{Name: "JOBS", Subjects: []string{"jobs.>"}, Storage: "file", Retention: "workqueue", DenyDelete: true},
	{Name: "ORDERS", Subjects: []string{"orders.>"}, Storage: "file", Retention: "limits", Messages: 240},
}

func loadedModel(t *testing.T) model {
	t.Helper()
	m, _ := update(t, newTestModel(), messages.StreamsLoadedMsg{Streams: testStreams})
	return m
}

func TestStreamsLoaded(t *testing.T) {
	m := newTestModel()
	m.loading = true

	m, cmd := update(t, m, messages.StreamsLoadedMsg{Streams: testStreams})
	if m.loading {
		t.Error("expected loading to be false after StreamsLoadedMsg")
	}
	if len(m.state.streamOverview.table.Rows()) != 3 {
		t.Errorf("expected 3 rows, got %d", len(m.state.streamOverview.table.Rows()))
	}
	if cmd == nil {
		t.Error("expected a refresh to be scheduled")
	}
	if !strings.Contains(m.render(), "ORDERS") {
		t.Error("expected overview to render stream names")
	}
}

func TestStreamsLoadedWithError(t *testing.T) {
	m := newTestModel()
	m, _ = update(t, m, messages.StreamsLoadedMsg{Err: errors.New("boom")})
	if !strings.Contains(m.error, "boom") {
		t.Fatalf("expected error to be set, got %q", m.error)
	}

	// Any key dismisses the error without triggering the page action.
	m, cmd := update(t, m, press("q"))
	if m.error != "" {
		t.Error("expected error to be dismissed")
	}
	if cmd != nil {
		t.Error("expected dismissing the error not to quit")
	}
}

func TestEmptyOverview(t *testing.T) {
	m, _ := update(t, newTestModel(), messages.StreamsLoadedMsg{})
	if !strings.Contains(m.StreamOverviewView(), "no streams yet") {
		t.Error("expected empty state message")
	}
}

func TestOverviewNavigation(t *testing.T) {
	m := loadedModel(t)

	m, _ = update(t, m, press("j"), press("j"), press("j"))
	if m.state.streamOverview.selected != 2 {
		t.Errorf("expected selection to stop at 2, got %d", m.state.streamOverview.selected)
	}
	m, _ = update(t, m, press("k"))
	if m.state.streamOverview.selected != 1 {
		t.Errorf("expected selection 1, got %d", m.state.streamOverview.selected)
	}
}

func TestOverviewFilterAndSelection(t *testing.T) {
	m := loadedModel(t)

	m, _ = update(t, m, press("/"))
	if !m.state.streamOverview.filtering {
		t.Fatal("expected filter mode")
	}
	m = typeText(t, m, "orders")
	m, _ = update(t, m, press("enter"))
	if got := m.getFilteredStreams(); len(got) != 1 || got[0].Name != "ORDERS" {
		t.Fatalf("expected only ORDERS, got %+v", got)
	}

	// Selection is keyed by name, so it survives clearing the filter.
	m, _ = update(t, m, press("space"), press("q"))
	if m.state.streamOverview.filterText != "" {
		t.Error("expected q to clear the filter first")
	}
	if !m.state.streamOverview.selectedItems["ORDERS"] {
		t.Error("expected ORDERS to stay selected")
	}

	m, _ = update(t, m, press("ctrl+d"))
	if m.page != streamDelete || len(m.state.streamDelete.streams) != 1 || m.state.streamDelete.streams[0] != "ORDERS" {
		t.Fatalf("expected delete dialog for ORDERS, got page %v with %v", m.page, m.state.streamDelete.streams)
	}
	if !strings.Contains(m.StreamDeleteView(), "ORDERS") {
		t.Error("expected dialog to mention the stream")
	}

	m, _ = update(t, m, press("enter"))
	if m.page != streamDelete || m.busy {
		t.Error("expected enter to do nothing until the name is typed")
	}
	m, _ = update(t, m, press("esc"))
	if m.page != streamOverview {
		t.Error("expected esc to return to the overview")
	}
}

func TestOverviewQuit(t *testing.T) {
	m := loadedModel(t)
	_, cmd := update(t, m, press("q"))
	if cmd == nil {
		t.Fatal("expected quit command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("expected q on the overview to quit")
	}
}

func TestHelpIgnoredWhileTyping(t *testing.T) {
	m := loadedModel(t)
	m, _ = update(t, m, press("/"), press("?"))
	if m.showHelp {
		t.Error("expected ? to be typed into the filter instead of opening help")
	}
	if m.state.streamOverview.filterText != "?" {
		t.Errorf("expected filter text '?', got %q", m.state.streamOverview.filterText)
	}
}

func TestStaleRefreshTicksAreDropped(t *testing.T) {
	m := loadedModel(t)
	gen := m.refreshGen

	_, cmd := update(t, m, messages.RefreshTickMsg{Gen: gen - 1})
	if cmd != nil {
		if _, ok := cmd().(messages.StreamsLoadedMsg); ok {
			t.Error("expected stale tick to be ignored")
		}
	}
}

func detailsModel(t *testing.T) model {
	t.Helper()
	m := loadedModel(t)
	m, _ = update(t, m, press("j"), press("j"), press("enter"))
	if m.page != streamDetails || m.state.streamDetails.stream.Name != "ORDERS" {
		t.Fatalf("expected details of ORDERS, got page %v", m.page)
	}
	now := time.Now()
	m, _ = update(t, m,
		messages.StreamLoadedMsg{Name: "ORDERS", Stream: testStreams[2]},
		messages.MessagesLoadedMsg{Stream: "ORDERS", Messages: []tsui.Message{
			{Stream: "ORDERS", Subject: "orders.eu.created", Sequence: 3, Time: now, Data: []byte(`{"id":3}`)},
			{Stream: "ORDERS", Subject: "orders.us.created", Sequence: 2, Time: now, Data: []byte(`{"id":2}`)},
			{Stream: "ORDERS", Subject: "orders.eu.shipped", Sequence: 1, Time: now, Data: []byte(`{"id":1}`)},
		}},
		messages.ConsumersLoadedMsg{Stream: "ORDERS", Consumers: []tsui.Consumer{
			{Stream: "ORDERS", Name: "billing", AckPolicy: "explicit", DeliverPolicy: "all"},
		}},
	)
	return m
}

func TestStreamDetails(t *testing.T) {
	m := detailsModel(t)

	if m.loading {
		t.Error("expected loading to be false")
	}
	view := ansi.Strip(m.StreamDetailsView())
	for _, want := range []string{"orders.eu.created", "messages 3", "consumers 1", "subjects"} {
		if !strings.Contains(view, want) {
			t.Errorf("expected details view to contain %q", want)
		}
	}
	if strings.Contains(view, "subjects …") {
		t.Error("expected the subjects tab not to look cut short before its subjects are loaded")
	}

	m, _ = update(t, m, press("tab"))
	if m.state.streamDetails.tab != tabConsumers || !strings.Contains(m.StreamDetailsView(), "billing") {
		t.Error("expected tab to switch to consumers")
	}
}

func TestSubjectsTab(t *testing.T) {
	m := detailsModel(t)
	m, cmd := update(t, m, press("tab"), press("tab"))
	if m.state.streamDetails.tab != tabSubjects {
		t.Fatalf("expected subjects tab, got %v", m.state.streamDetails.tab)
	}
	if cmd == nil {
		t.Error("expected the subjects to be loaded")
	}

	m, _ = update(t, m, messages.SubjectsLoadedMsg{Stream: "ORDERS", Subjects: []tsui.SubjectCount{
		{Subject: "orders.eu.created", Messages: 160},
		{Subject: "orders.us.created", Messages: 80},
	}})
	if view := ansi.Strip(m.StreamDetailsView()); !strings.Contains(view, "orders.us.created") || !strings.Contains(view, "subjects 2") {
		t.Error("expected the subjects to be listed")
	}

	m, cmd = update(t, m, press("j"), press("enter"))
	d := m.state.streamDetails
	if d.tab != tabMessages || d.filterText != "orders.us.created" || d.subject != "orders.us.created" || cmd == nil {
		t.Fatalf("expected enter to load the messages on the subject, got tab %v filter %q subject %q", d.tab, d.filterText, d.subject)
	}
	if !strings.Contains(ansi.Strip(m.StreamDetailsView()), "loading the messages on orders.us.created") {
		t.Error("expected the subject load to be shown")
	}
	m, _ = update(t, m, messages.MessagesLoadedMsg{Stream: "ORDERS", Subject: "orders.us.created", Gen: m.msgsGen, Messages: []tsui.Message{
		{Stream: "ORDERS", Subject: "orders.us.created", Sequence: 2},
	}})
	if got := m.getFilteredMessages(); len(got) != 1 || got[0].Sequence != 2 {
		t.Errorf("expected only sequence 2, got %+v", got)
	}
	if view := ansi.Strip(m.StreamDetailsView()); !strings.Contains(view, "1 of 80") || strings.Contains(view, "no match among") {
		t.Errorf("expected the hint to count the subject's messages:\n%s", view)
	}
}

func TestPublishSubjectStatus(t *testing.T) {
	m := detailsModel(t)
	m, _ = update(t, m, press("ctrl+n"))
	for subject, want := range map[string]string{
		"orders.":           "complete the subject",
		"orders.*":          "no wildcards",
		"payments.created":  "not bound to ORDERS",
		"orders.eu.created": "stored by ORDERS via orders.>",
	} {
		m.state.messagePublish.subject.SetValue(subject)
		if got := ansi.Strip(styles.Render(m.publishSubjectStatus()...)); !strings.Contains(got, want) {
			t.Errorf("subject %q: expected %q, got %q", subject, want, got)
		}
	}
}

func TestHighlightJSON(t *testing.T) {
	data := []byte(`{"id":3,"ok":true,"tags":["a","b"],"n":null,"price":-1.5e3}`)
	if got, want := ansi.Strip(highlightBody(data)), formatBody(data); got != want {
		t.Errorf("highlighting changed the payload:\n%s\nwant\n%s", got, want)
	}
}

func TestStaleDetailsResultsAreIgnored(t *testing.T) {
	m := detailsModel(t)
	m, _ = update(t, m, messages.MessagesLoadedMsg{Stream: "EVENTS", Messages: []tsui.Message{{Sequence: 99}}})
	if len(m.state.streamDetails.messages) != 3 {
		t.Error("expected messages of another stream to be ignored")
	}
}

func TestMessageWildcardFilter(t *testing.T) {
	m := detailsModel(t)
	m, _ = update(t, m, press("/"))
	m = typeText(t, m, "orders.eu.*")
	if got := m.getFilteredMessages(); len(got) != 2 || got[0].Sequence != 3 || got[1].Sequence != 1 {
		t.Errorf("expected typing to filter the loaded messages to 3 and 1, got %+v", got)
	}
	m, _ = update(t, m, press("enter"))
	if d := m.state.streamDetails; d.subject != "orders.eu.*" || d.messagesLoaded {
		t.Fatalf("expected enter to load the messages on orders.eu.* from the server, got subject %q", d.subject)
	}
	m, _ = update(t, m, messages.MessagesLoadedMsg{Stream: "ORDERS", Subject: "orders.eu.*", Gen: m.msgsGen, Messages: []tsui.Message{
		{Stream: "ORDERS", Subject: "orders.eu.created", Sequence: 3},
		{Stream: "ORDERS", Subject: "orders.eu.shipped", Sequence: 1},
	}})
	got := m.getFilteredMessages()
	if len(got) != 2 || got[0].Sequence != 3 || got[1].Sequence != 1 {
		t.Errorf("expected sequences 3 and 1, got %+v", got)
	}
	m, _ = update(t, m, press("esc"))
	if d := m.state.streamDetails; d.subject != "" || d.filterText != "" || d.messagesLoaded {
		t.Errorf("expected clearing the filter to load the newest messages again, got subject %q", d.subject)
	}
}

func TestMessageSelectionAndDelete(t *testing.T) {
	m := detailsModel(t)
	m, _ = update(t, m, press("space"), press("j"), press("space"), press("ctrl+d"))
	if m.page != messageDelete {
		t.Fatalf("expected delete dialog, got page %v", m.page)
	}
	if seqs := m.state.messageDelete.sequences; len(seqs) != 2 || seqs[0] != 3 || seqs[1] != 2 {
		t.Errorf("expected sequences [3 2], got %v", seqs)
	}
}

func TestDenyDelete(t *testing.T) {
	m := detailsModel(t)
	m.state.streamDetails.stream.DenyDelete = true
	m, _ = update(t, m, press("ctrl+d"))
	if m.page != streamDetails || !strings.Contains(m.error, "deny_delete") {
		t.Errorf("expected deny_delete error, got page %v error %q", m.page, m.error)
	}
}

func TestMessageDetails(t *testing.T) {
	m := detailsModel(t)
	m, _ = update(t, m, press("enter"))
	if m.page != messageDetails {
		t.Fatalf("expected message details, got page %v", m.page)
	}
	view := ansi.Strip(m.MessageDetailsView())
	if !strings.Contains(view, `"id": 3`) {
		t.Error("expected pretty printed payload")
	}
	m, _ = update(t, m, press("q"))
	if m.page != streamDetails {
		t.Error("expected q to go back to the stream details")
	}
}

func TestPublishValidation(t *testing.T) {
	m := detailsModel(t)
	m, _ = update(t, m, press("ctrl+n"))
	if m.page != messagePublish {
		t.Fatalf("expected publish page, got %v", m.page)
	}
	if got := m.state.messagePublish.subject.Value(); got != "orders." {
		t.Errorf("expected subject to be prefilled with 'orders.', got %q", got)
	}

	// q is regular input here, not a shortcut.
	m = typeText(t, m, "q")
	if m.page != messagePublish {
		t.Fatal("expected typing q to stay on the publish page")
	}

	m.state.messagePublish.subject.SetValue("payments.created")
	m, _ = update(t, m, press("ctrl+s"))
	if !strings.Contains(m.error, "not bound to stream") {
		t.Errorf("expected subject validation error, got %q", m.error)
	}

	m, _ = update(t, m, press("x")) // dismiss
	m.state.messagePublish.subject.SetValue("orders.eu.created")
	m.state.messagePublish.headers.SetValue("not a header")
	m, _ = update(t, m, press("ctrl+s"))
	if !strings.Contains(m.error, "Key: Value") {
		t.Errorf("expected header validation error, got %q", m.error)
	}
}

func TestPublishedMessageKeepsDraftOnError(t *testing.T) {
	m := detailsModel(t)
	m, _ = update(t, m, press("ctrl+n"), press("tab"), press("tab"))
	m = typeText(t, m, "hello")
	m, _ = update(t, m, messages.MessagePublishedMsg{Stream: "ORDERS", Err: errors.New("timeout")})
	if m.page != messagePublish || m.state.messagePublish.body.Value() != "hello" {
		t.Error("expected publish failure to keep the draft")
	}
}

func TestParseHeaders(t *testing.T) {
	h, err := parseHeaders("Nats-Msg-Id: abc\n\nContent-Type: application/json\nX-Multi: a\nX-Multi: b")
	if err != nil {
		t.Fatal(err)
	}
	if h.Get("Nats-Msg-Id") != "abc" || len(h["X-Multi"]) != 2 {
		t.Errorf("unexpected headers: %v", h)
	}
	if _, err := parseHeaders("no colon"); err == nil {
		t.Error("expected error for a line without colon")
	}
}

func TestBuildStreamConfig(t *testing.T) {
	cfg, err := buildStreamConfig(&streamCreateInput{
		name:      "PAYMENTS",
		storage:   "memory",
		retention: "workqueue",
		discard:   "new",
		maxAge:    "7d",
		maxBytes:  "1GB",
		maxMsgs:   "1,000",
		replicas:  3,
		flags:     []string{flagAllowDirect, flagDenyPurge},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Subjects[0] != "payments.>" {
		t.Errorf("expected default subject payments.>, got %v", cfg.Subjects)
	}
	if cfg.MaxAge != 7*24*time.Hour || cfg.MaxBytes != 1<<30 || cfg.MaxMsgs != 1000 {
		t.Errorf("unexpected limits: %+v", cfg)
	}
	if cfg.MaxMsgsPerSubject != -1 || cfg.MaxMsgSize != -1 {
		t.Errorf("expected unset limits to be unlimited: %+v", cfg)
	}
	if !cfg.AllowDirect || !cfg.DenyPurge || cfg.DenyDelete {
		t.Errorf("unexpected flags: %+v", cfg)
	}

	if _, err := buildStreamConfig(&streamCreateInput{name: "bad.name"}); err == nil {
		t.Error("expected invalid stream name to fail")
	}
}

func TestParsers(t *testing.T) {
	for in, want := range map[string]int64{"512": 512, "1k": 1024, "2MB": 2 << 20, "1 GiB": 1 << 30} {
		if got, err := parseBytes(in); err != nil || got != want {
			t.Errorf("parseBytes(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	if _, err := parseBytes("-1"); err == nil {
		t.Error("expected negative size to fail")
	}
	if d, err := parseDuration("90m"); err != nil || d != 90*time.Minute {
		t.Errorf("parseDuration(90m) = %v, %v", d, err)
	}
	if err := validateSubjects("orders.>.eu"); err == nil {
		t.Error("expected '>' in the middle of a subject to fail")
	}
}

func TestFormatters(t *testing.T) {
	if formatBytes(1536) != "1.5 KiB" || formatCount(1234567) != "1,234,567" || formatLimit(-1, false) != "unlimited" {
		t.Error("unexpected formatter output")
	}
	if got := preview([]byte("{\n  \"a\": 1\n}"), 50); got != `{ "a": 1 }` {
		t.Errorf("expected payload on a single line, got %q", got)
	}
	if got := truncate("abcdefghij", 5); got != "abcd…" {
		t.Errorf("unexpected truncation: %q", got)
	}
}
