package tui

import (
	"fmt"
	"image/color"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/nats-io/nats.go"

	"github.com/kontrolplane/tsui/pkg/client"
	"github.com/kontrolplane/tsui/pkg/tsui"
	"github.com/kontrolplane/tsui/pkg/tui/messages"
	"github.com/kontrolplane/tsui/pkg/tui/styles"
)

// layoutSizes are the terminal sizes every page is drawn at: below the minimum, at it, and above.
var layoutSizes = [][2]int{
	{20, 5}, {40, 12}, {60, 20}, {80, 24}, {101, 40}, {160, 24},
	{minContentWidth + chromeWidth, minContentHeight + chromeHeight},
	{102, 26}, {120, 40}, {250, 70},
}

func nastyStreams() []tsui.Stream {
	long := strings.Repeat("VERY_LONG_STREAM_NAME_", 8)
	subject := strings.Repeat("tok.", 19) + "end"
	return []tsui.Stream{
		{Name: long, Subjects: []string{subject, "a.>", "日本語.件名.*"}, Storage: "file", Retention: "limits", Discard: "old",
			Messages: 12345678901, Bytes: 1 << 50, Consumers: 1234567, Replicas: 3, Leader: strings.Repeat("leader-", 20),
			LastTime: time.Now(), FirstSeq: 1, LastSeq: 18446744073709551615, NumSubjects: 99999999999, NumDeleted: 1234567890,
			MaxMsgs: 1 << 62, MaxBytes: 1 << 62, MaxAge: 36500 * 24 * time.Hour, MaxMsgsPerSubject: 1 << 62, Duplicates: 1234567 * time.Second,
			AllowDirect: true, DenyDelete: false, Sealed: true, Created: time.Now(), Description: strings.Repeat("description ", 30)},
		{Name: "注文ストリーム🚀", Subjects: []string{"注文.*.作成"}, Storage: "memory", Retention: "workqueue", Messages: 999999999999999},
		{Name: "tab\there\x1b[31m", Subjects: []string{"x"}, Storage: "file", Retention: "interest"},
		{Name: "MIRROR", Mirror: long, Storage: "file", Retention: "limits"},
		{Name: "SOURCED", Sources: []string{long, long, long}, Storage: "file", Retention: "limits"},
	}
}

// nastyMessages returns messages with long subjects, headers and payloads of every kind. The
// multi-megabyte ones are left out under -short.
func nastyMessages(short bool) []tsui.Message {
	h := nats.Header{}
	for i := range 200 {
		h.Add(fmt.Sprintf("X-Header-With-A-Very-Long-Name-%03d", i), strings.Repeat("v", 300))
	}
	h.Add("Nats-Msg-Id", "id\x1b]0;title\x07")
	msgs := []tsui.Message{
		{Stream: "S", Subject: strings.Repeat("tok.", 19) + "end", Sequence: 18446744073709551615, Time: time.Now(), Header: h, Data: []byte("line1\n\tline2\ttabbed\r\nline3 日本語 🚀🚀🚀 " + strings.Repeat("wide 漢字 ", 40))},
		{Stream: "S", Subject: "empty", Sequence: 2},
		{Stream: "S", Subject: "bin", Sequence: 3, Data: []byte{0xff, 0xfe, 0, 1, 2, 0x1b, '[', '3', '1', 'm'}},
		{Stream: "S", Subject: "ansi", Sequence: 5, Data: []byte("\x1b[31mRED\x1b[0m \x1b]0;pwned\x07 \x1b[2J clear")},
		{Stream: "S", Subject: "json", Sequence: 6, Data: []byte(`{"k":"` + strings.Repeat("x", 5000) + `","n":[1,2,3],"s":"日本語"}`)},
	}
	if !short {
		var dense strings.Builder
		dense.WriteString("[")
		for dense.Len() < 4<<20 {
			dense.WriteString(`{"id":123,"ok":true,"n":null,"s":"abc"},`)
		}
		dense.WriteString("1]")
		msgs = append(msgs,
			tsui.Message{Stream: "S", Subject: "big", Sequence: 7, Data: []byte(strings.Repeat(`{"k":"`+strings.Repeat("x", 1000)+`"},`, 3000))},
			tsui.Message{Stream: "S", Subject: "dense", Sequence: 8, Data: []byte(dense.String())},
			tsui.Message{Stream: "S", Subject: "line", Sequence: 9, Data: []byte(strings.Repeat("x", 3<<20))},
		)
	}
	return msgs
}

func nastyConsumers(stream string) []tsui.Consumer {
	return []tsui.Consumer{
		{Stream: stream, Name: strings.Repeat("consumer-name-", 10), FilterSubjects: []string{strings.Repeat("f.", 15) + ">", "a.*"},
			Description: strings.Repeat("desc ", 60), Delivered: 5, AckFloor: 3, NumPending: 9999999999, NumAckPending: 123456789,
			NumRedelivered: 987654321, NumWaiting: 12345678, Durable: true, LastActive: time.Now(), AckWait: 90061 * time.Second,
			DeliverPolicy: "by_start_sequence", AckPolicy: "explicit", MaxDeliver: 1 << 30, MaxAckPending: 1 << 30, Created: time.Now()},
		{Stream: stream, Name: "push", Push: true, AckPolicy: "none", Paused: true},
	}
}

// layoutPages opens every page of a model sized to the terminal, with the nasty fixtures loaded.
func layoutPages(t *testing.T, w, h int, short bool) map[string]model {
	t.Helper()
	m := newModel("kontrolplane", "tsui")
	m.info = tsuiInfo()
	m.loading = false
	m, _ = update(t, m, tea.WindowSizeMsg{Width: w, Height: h})
	pages := map[string]model{"overview empty": m}

	m, _ = update(t, m, messages.StreamsLoadedMsg{Streams: nastyStreams()})
	m.serverOK = true
	m.server = tsui.Server{Name: strings.Repeat("nats-server-", 6), Version: "2.11.4", Cluster: strings.Repeat("cluster-", 6), Domain: "hub",
		Streams: 1234567890, Consumers: 98765432109, Store: 5 << 40, MaxStore: 10 << 40, Memory: 12 << 30, MaxMemory: 16 << 30, RTT: time.Millisecond}
	pages["overview"] = m

	o := m
	o.showHelp = true
	pages["help"] = o
	o = m
	o.error = strings.Repeat("an error with a long description ", 40) + strings.Repeat("x", 300)
	pages["error"] = o
	o = m
	o.loading, o.loadingMsg = true, strings.Repeat("loading a lot ", 20)
	pages["loading"] = o
	o = m
	o, _ = update(t, o, press("/"))
	o.state.streamOverview.filterInput.SetValue(strings.Repeat("filter*", 20))
	o = typeText(t, o, "x")
	pages["overview filtering"] = o
	o, _ = update(t, o, press("enter"))
	pages["overview filtered"] = o
	o, _ = update(t, m, press("space"), press("j"), press("space"))
	o, _ = o.setStatus(strings.Repeat("a long status message ", 20), styles.ToneDanger)
	pages["overview selection and status"] = o

	st := nastyStreams()[0]
	m.state.streamDetails.stream = st
	m, _ = m.StreamDetailsSwitchPage()
	m.loading = false
	pages["details loading"] = m
	m, _ = update(t, m,
		messages.StreamLoadedMsg{Name: st.Name, Stream: st},
		messages.MessagesLoadedMsg{Stream: st.Name, Messages: nastyMessages(short), Gen: m.msgsGen},
		messages.ConsumersLoadedMsg{Stream: st.Name, Consumers: nastyConsumers(st.Name)},
		messages.SubjectsLoadedMsg{Stream: st.Name, Subjects: []tsui.SubjectCount{{Subject: strings.Repeat("tok.", 19) + "end", Messages: 12345678901}, {Subject: "日本語.件名.x", Messages: 1}}},
	)
	m.loading = false
	pages["details messages"] = m
	d := m
	d.state.streamDetails.tab = tabConsumers
	d = d.focusDetailsTab()
	pages["details consumers"] = d
	d.state.streamDetails.tab = tabSubjects
	d = d.focusDetailsTab()
	pages["details subjects"] = d
	d, _ = update(t, m, press("space"), press("/"))
	d.state.streamDetails.filterInput.SetValue(strings.Repeat("orders.*.", 12))
	d = typeText(t, d, "x")
	pages["details filtering"] = d

	d = m
	d.state.streamDetails.olderNext, d.state.streamDetails.paged, d.state.streamDetails.loadingOlder = 5, true, true
	d.paused, d.refreshedAt = true, time.Now().Add(-90*time.Second)
	d = d.updateMessagesTable()
	pages["details loading older, paused"] = d
	d.state.streamDetails.olderNext, d.state.streamDetails.loadingOlder, d.state.streamDetails.follow = 0, false, false
	pages["details start of stream, follow off"] = d
	d = m
	subject := strings.Repeat("orders.*.", 12) + "x"
	d.state.streamDetails.subject, d.state.streamDetails.olderNext = subject, 7
	d = d.setDetailsFilter(subject)
	pages["details subject"] = d
	d.state.streamDetails.messages = nil
	d = d.updateMessagesTable()
	pages["details subject empty"] = d
	d, _ = update(t, m, press("#"))
	d = typeText(t, d, "18446744073709551615")
	pages["details jumping"] = d

	for _, msg := range nastyMessages(short) {
		d := m
		d.state.messageDetails.message = msg
		d, _ = d.MessageDetailsSwitchPage()
		pages["message details "+msg.Subject] = d
		if msg.Subject == "big" {
			d, _ = update(t, d, press("G"))
			pages["message details big, at the bottom"] = d
		}
	}

	d = m
	d.state.consumerDetails.consumer = d.state.streamDetails.consumers[0]
	d, _ = d.ConsumerDetailsSwitchPage()
	pages["consumer details"] = d
	d.state.consumerDetails.consumer = d.state.streamDetails.consumers[1]
	pages["consumer details paused"] = d

	d, _ = m.MessagePublishSwitchPage()
	pages["publish"] = d
	d.state.messagePublish.subject.SetValue(strings.Repeat("orders.eu.", 30))
	d = typeText(t, d, "x")
	pages["publish long subject"] = d

	m.state.streamPurge.stream = st
	d, _ = m.StreamPurgeSwitchPage()
	pages["purge"] = d
	d, _ = update(t, d, press("tab"))
	d.state.streamPurge.subjectInput.SetValue(strings.Repeat("tok.", 19))
	d = typeText(t, d, "*")
	pages["purge subject"] = d
	d, _ = update(t, d, press("tab"), press("y"))
	pages["purge sure"] = d
	d, _ = m.StreamPurgeSwitchPage()
	d, _ = update(t, d, press("y"))
	d.state.streamPurge.confirm.input.SetValue(strings.Repeat("typed ", 40))
	d = typeText(t, d, "x")
	pages["purge typed"] = d

	o.state.streamDelete.streams = []string{st.Name}
	d, _ = o.StreamDeleteSwitchPage()
	d.state.streamDelete.confirm.input.SetValue(strings.Repeat("typed ", 40))
	d = typeText(t, d, "x")
	pages["delete stream"] = d
	var names []string
	for i := range 20 {
		names = append(names, fmt.Sprintf("%s-%d", st.Name, i))
	}
	o.state.streamDelete.streams, o.state.streamDelete.hidden = names, 3
	d, _ = o.StreamDeleteSwitchPage()
	pages["delete streams"] = d

	var seqs []uint64
	for i := range 40 {
		seqs = append(seqs, 18446744073709551615-uint64(i))
	}
	d = m
	d.state.messageDelete.sequences = seqs
	d, _ = d.MessageDeleteSwitchPage()
	pages["delete messages"] = d
	d = m
	d.state.consumerDelete.consumers = names
	d, _ = d.ConsumerDeleteSwitchPage()
	pages["delete consumers"] = d
	d.state.consumerDelete.consumers = names[:1]
	pages["delete consumer"] = d

	d, _ = o.StreamCreateSwitchPage()
	pages["create"] = d
	d = typeText(t, d, strings.Repeat("N", 120))
	pages["create long name"] = d
	return pages
}

// TestLayoutFitsTheTerminal draws every page at every size and checks that nothing is wider or
// taller than the terminal, and that a terminal below the minimum gets the notice instead.
func TestLayoutFitsTheTerminal(t *testing.T) {
	t.Cleanup(func() { setLayout(142, 34) })
	short := testing.Short()
	checked := 0
	for _, size := range layoutSizes {
		w, h := size[0], size[1]
		small := w < minContentWidth+chromeWidth || h < minContentHeight+chromeHeight
		for name, m := range layoutPages(t, w, h, short) {
			setLayout(w, h)
			out := m.render()
			lines := strings.Split(out, "\n")
			if len(lines) > h {
				t.Errorf("%s at %dx%d: %d lines, taller than the terminal", name, w, h, len(lines))
			}
			for i, line := range lines {
				if lw := ansi.StringWidth(line); lw > w {
					t.Errorf("%s at %dx%d: line %d is %d wide:\n%s", name, w, h, i, lw, ansi.Strip(line))
					break
				}
			}
			if got := strings.Contains(ansi.Strip(out), "terminal too small"); got != small {
				t.Errorf("%s at %dx%d: expected the too small notice %v, got %v", name, w, h, small, got)
			}
			// The frame clips what does not fit, the pages are drawn to not need it.
			if c := m.content(); !small && (lipgloss.Width(c) > contentWidth || lipgloss.Height(c) > contentHeight) {
				t.Errorf("%s at %dx%d: content is %dx%d, the frame holds %dx%d:\n%s", name, w, h,
					lipgloss.Width(c), lipgloss.Height(c), contentWidth, contentHeight, ansi.Strip(c))
			}
			checked++
		}
	}
	t.Logf("checked %d page and size combinations", checked)
}

// TestTooSmallNotice names the size the terminal has and the size it needs.
func TestTooSmallNotice(t *testing.T) {
	t.Cleanup(func() { setLayout(142, 34) })
	m, _ := update(t, newTestModel(), tea.WindowSizeMsg{Width: 80, Height: 24})
	out := ansi.Strip(m.render())
	for _, want := range []string{"terminal too small", "80×24, needs 102×24", "ctrl+c to quit"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in the notice:\n%s", want, out)
		}
	}
	m, _ = update(t, m, tea.WindowSizeMsg{Width: 102, Height: 24})
	if strings.Contains(ansi.Strip(m.render()), "terminal too small") {
		t.Error("expected the layout at the minimum size")
	}
}

func TestRenderCellMarksEveryCut(t *testing.T) {
	subject := cell(styles.Subject("orders.eu.created", styles.ToneText))
	for width, want := range map[int]string{6: "order…", 9: "orders.e…", 10: "orders.eu…", 11: "orders.eu.…", 17: "orders.eu.created", 20: "orders.eu.created   "} {
		if got := ansi.Strip(renderCell(subject, column{width: width}, nil)); got != want {
			t.Errorf("width %d: got %q, want %q", width, got, want)
		}
	}
	if got := ansi.Strip(renderCell(text("12345678901", styles.ToneText), column{width: 9, right: true}, nil)); got != "12345678…" {
		t.Errorf("got %q", got)
	}
}

func TestTablesDropColumnsToFit(t *testing.T) {
	tbl := newDataTable(consumerColumns, minContentWidth-4, 10)
	if w := tbl.width(); w > minContentWidth-4 {
		t.Errorf("consumers table is %d wide at the minimum, wider than %d", w, minContentWidth-4)
	}
	var hidden []string
	for _, c := range tbl.columns {
		if c.hide {
			hidden = append(hidden, c.title)
		}
	}
	if len(hidden) == 0 {
		t.Error("expected columns to be hidden at the minimum width")
	}
	header := ansi.Strip(strings.Split(tbl.View(), "\n")[0])
	for _, title := range hidden {
		if strings.Contains(header, title) {
			t.Errorf("hidden column %q is still drawn: %q", title, header)
		}
	}
	wide := newDataTable(consumerColumns, 200, 10)
	for _, c := range wide.columns {
		if c.hide {
			t.Errorf("column %q hidden while there is room", c.title)
		}
	}
}

func tsuiInfo() client.Info {
	return client.Info{Context: strings.Repeat("production-context-", 5), URL: strings.Repeat("tls://nats.example.com:4222,", 5)}
}

// TestWriteSpanMatchesLipgloss keeps the cached style sequences byte for byte what lipgloss draws.
func TestWriteSpanMatchesLipgloss(t *testing.T) {
	t.Cleanup(func() { styles.Use(true) })
	for _, dark := range []bool{true, false} {
		styles.Use(dark)
		for tone := styles.ToneText; tone <= styles.ToneRule; tone++ {
			for _, bold := range []bool{false, true} {
				for _, bg := range []color.Color{nil, styles.P.Selection, styles.P.Surface} {
					for _, txt := range []string{"", "abc", "x y"} {
						var b strings.Builder
						writeSpan(&b, tone, bold, bg, txt)
						style := styles.Span{Tone: tone, Bold: bold}.Style()
						if bg != nil {
							style = style.Background(bg)
						}
						if want := style.Render(txt); b.String() != want {
							t.Fatalf("tone %v bold %v bg %v: %q, lipgloss draws %q", tone, bold, bg, b.String(), want)
						}
					}
				}
			}
		}
	}
}

func TestExplicitThemePaintsTheBackground(t *testing.T) {
	t.Cleanup(func() { styles.Paint = false; styles.Use(true) })
	m := newTestModel()
	if v := m.View(); v.BackgroundColor != nil {
		t.Error("expected a detected theme to keep the terminal background")
	}
	styles.Use(false)
	styles.Paint = true
	if v := m.View(); v.BackgroundColor != styles.Light.Base {
		t.Errorf("expected the light stock as background, got %v", v.BackgroundColor)
	}
}
