package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kontrolplane/tsui/pkg/tsui"
	"github.com/kontrolplane/tsui/pkg/tui/messages"
	"github.com/kontrolplane/tsui/pkg/tui/styles"
)

func TestResizeFitsTablesAndClampsToMinimum(t *testing.T) {
	t.Cleanup(func() { setLayout(142, 34) }) // back to the defaults other tests expect

	m := detailsModel(t)
	m, _ = update(t, m, tea.WindowSizeMsg{Width: 202, Height: 60})
	if contentWidth != 200 || contentHeight != 52 || frameWidth != 202 {
		t.Fatalf("unexpected layout %dx%d frame %d", contentWidth, contentHeight, frameWidth)
	}
	if w := m.state.streamDetails.messagesTable.width(); w != contentWidth-4 {
		t.Errorf("expected the messages table to fill %d columns, got %d", contentWidth-4, w)
	}
	for _, line := range strings.Split(m.render(), "\n") {
		if w := ansi.StringWidth(line); w > 202 {
			t.Fatalf("rendered line is %d wide, wider than the terminal", w)
		}
	}

	m, _ = update(t, m, tea.WindowSizeMsg{Width: 40, Height: 10})
	if contentWidth != minContentWidth || contentHeight != minContentHeight {
		t.Errorf("expected the layout to clamp to %dx%d, got %dx%d", minContentWidth, minContentHeight, contentWidth, contentHeight)
	}
	if m.state.streamDetails.messagesTable.height < minTableHeight {
		t.Error("expected tables to keep their minimum height")
	}
}

func TestResizeOpenPublishAndMessagePages(t *testing.T) {
	t.Cleanup(func() { setLayout(142, 34) })

	m := detailsModel(t)
	m, _ = update(t, m, press("ctrl+n"), tea.WindowSizeMsg{Width: 180, Height: 44})
	if m.state.messagePublish.body.Width() == 0 || m.state.messagePublish.body.Height() != publishBodyHeight() {
		t.Errorf("expected the publish body to be resized, got height %d", m.state.messagePublish.body.Height())
	}
	m, _ = update(t, m, press("esc"), press("enter"), tea.WindowSizeMsg{Width: 150, Height: 40})
	if m.page != messageDetails || m.state.messageDetails.viewport.Height() != detailsViewportHeight() {
		t.Errorf("expected the payload viewport to be resized on page %v", m.page)
	}
}

func TestHelpOverlay(t *testing.T) {
	m := loadedModel(t)
	m, _ = update(t, m, press("?"))
	if !m.showHelp || !strings.Contains(ansi.Strip(m.render()), "press any key to close") {
		t.Fatal("expected the help overlay")
	}
	// Any key closes help without acting on it.
	m, cmd := update(t, m, press("q"))
	if m.showHelp || cmd != nil {
		t.Error("expected q to only close the help")
	}
	if m.page != streamOverview {
		t.Errorf("expected to stay on the overview, got %v", m.page)
	}
}

func TestForceQuitFromAnyPage(t *testing.T) {
	m := detailsModel(t)
	m, _ = update(t, m, press("ctrl+n"))
	m.error = "something"
	_, cmd := update(t, m, press("ctrl+c"))
	if cmd == nil {
		t.Fatal("expected a quit command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("expected ctrl+c to quit even while typing with an error shown")
	}
}

func TestRenderEveryPage(t *testing.T) {
	m := detailsModel(t)
	m.state.streamDetails.consumers[0].NumPending = 4
	m.state.streamDetails.stream.FirstSeq, m.state.streamDetails.stream.LastSeq = 1, 3
	m.state.streamDetails.consumers[0].Delivered = 2
	pages := map[page]func(model) model{
		streamOverview: func(m model) model { m, _ = m.StreamOverviewGoBack(); return m },
		streamDetails:  func(m model) model { return m },
		streamCreate:   func(m model) model { m, _ = m.StreamCreateSwitchPage(); return m },
		streamDelete: func(m model) model {
			m.state.streamDelete.streams = []string{"A", "B"}
			m, _ = m.StreamDeleteSwitchPage()
			return m
		},
		streamPurge: func(m model) model {
			m.state.streamPurge.stream = m.state.streamDetails.stream
			m, _ = m.StreamPurgeSwitchPage()
			return m
		},
		messageDetails: func(m model) model { m, _ = update(t, m, press("enter")); return m },
		messagePublish: func(m model) model { m, _ = update(t, m, press("ctrl+n")); return m },
		messageDelete: func(m model) model {
			m.state.messageDelete.sequences = []uint64{1, 2}
			m, _ = m.MessageDeleteSwitchPage()
			return m
		},
		consumerDetails: func(m model) model { m, _ = update(t, m, press("tab"), press("enter")); return m },
		consumerDelete: func(m model) model {
			m.state.consumerDelete.consumers = []string{"billing"}
			m, _ = m.ConsumerDeleteSwitchPage()
			return m
		},
	}
	for p, open := range pages {
		opened := open(m)
		if opened.page != p {
			t.Errorf("%v: expected page %v, got %v", p, p, opened.page)
			continue
		}
		v := opened.View()
		if !v.AltScreen || v.WindowTitle != "tsui" {
			t.Errorf("%v: unexpected view settings", p)
		}
		out := ansi.Strip(opened.render())
		if strings.Contains(out, errNoPageSelected) || !strings.Contains(out, "streams") {
			t.Errorf("%v: unexpected render:\n%s", p, out)
		}
	}
}

func TestLoadingView(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.loadingMsg = "Deleting stream..."
	if !strings.Contains(ansi.Strip(m.render()), "Deleting stream...") {
		t.Error("expected the loading message")
	}
	// The spinner only keeps ticking while loading.
	m.loading = false
	m, cmd := update(t, m, m.spinner.Tick())
	if cmd != nil || m.spinning {
		t.Error("expected the spinner to stop once loading is done")
	}
}

func TestStatusMessageClears(t *testing.T) {
	m := detailsModel(t)
	m, _ = update(t, m, messages.MessagePublishedMsg{Stream: "ORDERS", Sequence: 4})
	if m.statusMsg == "" || !strings.Contains(ansi.Strip(m.renderFooter()), "published to ORDERS at #4") {
		t.Fatal("expected a status message in the footer")
	}
	gen := m.statusGen
	m, _ = update(t, m, messages.ClipboardCopiedMsg{Text: "x"})
	m, _ = update(t, m, messages.StatusClearMsg{Gen: gen})
	if m.statusMsg != "copied to clipboard" {
		t.Errorf("expected a stale clear to keep the newer status, got %q", m.statusMsg)
	}
	m, _ = update(t, m, messages.StatusClearMsg{Gen: m.statusGen})
	if m.statusMsg != "" {
		t.Errorf("expected the status to clear, got %q", m.statusMsg)
	}
}

func TestClipboardFallsBackToOSC52(t *testing.T) {
	m := detailsModel(t)
	_, cmd := update(t, m, messages.ClipboardCopiedMsg{Text: "payload", Err: errors.New("no clipboard")})
	if cmd == nil {
		t.Fatal("expected a command")
	}
	msgs := []tea.Msg{cmd()}
	found := false
	for len(msgs) > 0 {
		msg := msgs[0]
		msgs = msgs[1:]
		if b, ok := msg.(tea.BatchMsg); ok {
			for _, c := range b {
				if c != nil && !strings.Contains(cmdName(c), "Tick") {
					msgs = append(msgs, c())
				}
			}
			continue
		}
		if strings.Contains(strings.ToLower(fmt.Sprintf("%T", msg)), "clipboard") {
			found = true
		}
	}
	if !found {
		t.Error("expected an OSC52 clipboard fallback when the system clipboard fails")
	}
}

func TestDetailsReturnToTheSameTab(t *testing.T) {
	m := detailsModel(t)
	m, _ = update(t, m, press("tab"), press("enter"))
	if m.page != consumerDetails || !strings.Contains(ansi.Strip(styles.Render(m.breadcrumb()...)), "consumers › billing") {
		t.Fatalf("expected consumer details, got %v", m.page)
	}
	m, _ = update(t, m, press("q"))
	if m.page != streamDetails || m.state.streamDetails.tab != tabConsumers {
		t.Errorf("expected to return to the consumers tab, got page %v tab %v", m.page, m.state.streamDetails.tab)
	}
}

func TestDetailsFilterAppliesToEveryTab(t *testing.T) {
	m := detailsModel(t)
	m.state.streamDetails.consumers = append(m.state.streamDetails.consumers,
		tsui.Consumer{Stream: "ORDERS", Name: "shipping", FilterSubjects: []string{"orders.*.shipped"}})
	m = m.updateConsumersTable()

	m, _ = update(t, m, press("/"))
	m = typeText(t, m, "shipped")
	if len(m.getFilteredConsumers()) != 1 || len(m.getFilteredMessages()) != 1 {
		t.Errorf("expected one consumer and one message to match, got %d and %d", len(m.getFilteredConsumers()), len(m.getFilteredMessages()))
	}
	if !strings.Contains(ansi.Strip(m.render()), "filter shipped") {
		t.Error("expected the active filter to be shown in the frame")
	}
	m, _ = update(t, m, press("esc"))
	if m.state.streamDetails.filterText != "" || len(m.getFilteredMessages()) != 3 {
		t.Error("expected esc to clear the filter")
	}

	// A filter matching nothing explains the empty table.
	m, _ = update(t, m, press("/"))
	m = typeText(t, m, "nothing-matches")
	m, _ = update(t, m, press("enter"))
	if !strings.Contains(m.StreamDetailsView(), "no match among the newest 3 messages") {
		t.Error("expected the empty filter state")
	}
	m, _ = update(t, m, press("q"))
	if m.page != streamDetails || m.state.streamDetails.filterText != "" {
		t.Error("expected q to clear the filter before leaving")
	}
	m, _ = update(t, m, press("space"), press("q"))
	if m.page != streamDetails || len(m.state.streamDetails.selectedMessages) != 0 {
		t.Error("expected q to clear the selection before leaving")
	}
	m, cmd := update(t, m, press("q"))
	if m.page != streamOverview || cmd == nil {
		t.Error("expected q to go back to the overview and reload it")
	}
}

func TestOverviewFilterMatchesSubjectWildcards(t *testing.T) {
	m := loadedModel(t)
	for filter, want := range map[string]int{
		"orders.eu.created": 1, // a subject the stream would store
		"jobs":              1,
		"events.a.b":        0, // events.* takes a single token
		"workqueue":         0, // retention is not searched
	} {
		m.state.streamOverview.filterText = filter
		if got := len(m.getFilteredStreams()); got != want {
			t.Errorf("filter %q: expected %d streams, got %d", filter, want, got)
		}
	}
	m.state.streamOverview.filterText = "nope"
	m = m.updateStreamOverviewTable()
	if !strings.Contains(m.StreamOverviewView(), "no streams match the filter") {
		t.Error("expected the empty filter state")
	}
	m, _ = update(t, m, press("enter"))
	if m.page != streamOverview {
		t.Error("expected enter on an empty table to do nothing")
	}
}

func TestMessageDetailsDenyDeleteAndScroll(t *testing.T) {
	m := detailsModel(t)
	m.state.streamDetails.messages[0].Data = []byte(strings.Repeat(`{"line":1}`+"\n", 200))
	m.state.streamDetails.messages[0].Header = map[string][]string{"Nats-Msg-Id": {"a"}, "X-App": {"b", "c"}}
	m = m.updateMessagesTable()
	m, _ = update(t, m, press("enter"))
	view := ansi.Strip(m.MessageDetailsView())
	for _, want := range []string{"Nats-Msg-Id", "X-App", "b, c", "text ·"} {
		if !strings.Contains(view, want) {
			t.Errorf("expected message details to contain %q", want)
		}
	}
	before := m.state.messageDetails.viewport.YOffset()
	m, _ = update(t, m, press("j"))
	if m.state.messageDetails.viewport.YOffset() != before+1 {
		t.Error("expected j to scroll the payload")
	}

	m.state.streamDetails.stream.DenyDelete = true
	m, _ = update(t, m, press("ctrl+d"))
	if m.page != messageDetails || !strings.Contains(m.error, "deny_delete") {
		t.Errorf("expected a deny_delete error, got page %v error %q", m.page, m.error)
	}
}

func TestServerHeader(t *testing.T) {
	m := loadedModel(t)
	if _, foot := m.frameMeta(); !strings.Contains(ansi.Strip(foot), "offline") {
		t.Error("expected the frame to show offline without a connection")
	}
	m, cmd := update(t, m, messages.ServerLoadedMsg{Server: tsui.Server{
		Name: "n1", Version: "2.12.0", Cluster: "c1", Domain: "hub", RTT: 1500 * time.Microsecond,
		Streams: 3, Consumers: 2, Store: 900, MaxStore: 1000, Memory: 10, MaxMemory: -1,
	}})
	if !m.serverOK || cmd == nil {
		t.Fatal("expected the server info to be stored and a refresh scheduled")
	}
	view := ansi.Strip(m.renderHeader())
	for _, want := range []string{"n1  v2.12.0", "c1  domain hub", "streams", "900 B / 1000 B"} {
		if !strings.Contains(view, want) {
			t.Errorf("expected the header to contain %q:\n%s", want, view)
		}
	}
	if strings.Contains(ansi.Strip(m.render()), "rtt") {
		t.Error("expected no rtt without a connection")
	}
}

func TestFormatHelpers(t *testing.T) {
	now := time.Now()
	for d, want := range map[time.Duration]string{
		-time.Minute:    "just now",
		5 * time.Second: "5s ago",
		3 * time.Minute: "3m ago",
		2 * time.Hour:   "2h ago",
		49 * time.Hour:  "2d ago",
	} {
		if got := formatAgo(now.Add(-d)); got != want {
			t.Errorf("formatAgo(-%v) = %q, want %q", d, got, want)
		}
	}
	if formatAgo(time.Time{}) != "never" || formatTime(time.Time{}) != "-" {
		t.Error("expected zero times to be shown as missing")
	}
	if formatDuration(48*time.Hour) != "2d" || formatDuration(90*time.Minute) != "1h30m" || formatDuration(0) != "unlimited" {
		t.Error("unexpected formatDuration output")
	}
	for d, want := range map[time.Duration]string{
		time.Hour: "1h", 2 * time.Minute: "2m", 30 * time.Second: "30s", 90 * time.Second: "1m30s",
		36 * time.Hour: "36h", 7 * 24 * time.Hour: "7d", 1500 * time.Millisecond: "1.5s", 0: "0s",
	} {
		if got := compactDuration(d); got != want {
			t.Errorf("compactDuration(%v) = %q, want %q", d, got, want)
		}
	}
	for n, want := range map[uint64]string{
		0: "0", 999: "999", 9_999_999: "9,999,999", 10_000_000: "10.0M", 12_345_678: "12.3M",
		999_999_999: "999M", 4_567_890_123: "4.5B", 12_345_678_901: "12.3B", 18446744073709551615: "18446Q",
	} {
		if got := compactCount(n); got != want {
			t.Errorf("compactCount(%d) = %q, want %q", n, got, want)
		}
	}
	if formatLimit(2048, true) != "2.0 KiB" || formatLimit(1500, false) != "1,500" {
		t.Error("unexpected formatLimit output")
	}
	for data, want := range map[string]string{"": "empty", "\xff\xfe": "binary · 2 B", `{"a":1}`: "json · 7 B", "hi": "text · 2 B"} {
		if got := payloadKind([]byte(data)); got != want {
			t.Errorf("payloadKind(%q) = %q, want %q", data, got, want)
		}
	}
	if got := ansi.Strip(highlightBody(nil)); got != "empty payload" {
		t.Errorf("unexpected empty highlight %q", got)
	}
	if got := ansi.Strip(highlightBody([]byte("plain"))); got != "plain" {
		t.Errorf("unexpected text highlight %q", got)
	}
	if got := formatBody([]byte{0xff}); !strings.Contains(got, "binary") {
		t.Errorf("unexpected binary body %q", got)
	}
	if got := ansi.Strip(highlightBody([]byte(`{"s":"a \"quoted\" : value"}`))); got != formatBody([]byte(`{"s":"a \"quoted\" : value"}`)) {
		t.Errorf("highlighting changed an escaped string: %q", got)
	}
	if formatRTT(300*time.Microsecond) != "300µs" || formatRTT(12345*time.Microsecond) != "12.3ms" {
		t.Errorf("unexpected rtt formatting: %q %q", formatRTT(300*time.Microsecond), formatRTT(12345*time.Microsecond))
	}
	if formatRTT(0) != "<1ms" {
		t.Errorf("expected a round trip below the clock resolution to read <1ms, got %q", formatRTT(0))
	}
	if plural(1, "stream") != "1 stream" || plural(1200, "message") != "1,200 messages" {
		t.Error("unexpected plural output")
	}
}

func TestSequencePosition(t *testing.T) {
	cases := []struct {
		seq, first, last uint64
		want             float64
	}{
		{0, 1, 10, 0},
		{5, 1, 0, 0},
		{10, 1, 10, 1},
		{5, 1, 10, 0.5},
		{7, 7, 7, 1},
		{20, 1, 10, 1},
	}
	for _, c := range cases {
		if got := sequencePosition(c.seq, c.first, c.last); got != c.want {
			t.Errorf("sequencePosition(%d, %d, %d) = %v, want %v", c.seq, c.first, c.last, got, c.want)
		}
	}
	stream := tsui.Stream{FirstSeq: 1, LastSeq: 10}
	for c, want := range map[*tsui.Consumer]string{
		{Paused: true}:                "paused",
		{}:                            "caught up",
		{NumPending: 5, Delivered: 5}: "50%",
	} {
		if got := ansi.Strip(renderCell(consumerDelivery(*c, stream), column{width: 40}, nil)); !strings.Contains(got, want) {
			t.Errorf("consumerDelivery(%+v) = %q, want %q", *c, got, want)
		}
	}
}
