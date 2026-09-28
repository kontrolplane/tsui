package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/kontrolplane/tsui/pkg/tsui"
	"github.com/kontrolplane/tsui/pkg/tui/messages"
	"github.com/kontrolplane/tsui/pkg/tui/styles"
)

// bigSubject is the subject of message i in the BIG stream: every 25th on big.rare.x, the others
// alternating between big.a.x and big.b.x.
func bigSubject(i int) string {
	switch {
	case i%25 == 0:
		return "big.rare.x"
	case i%2 == 0:
		return "big.a.x"
	}
	return "big.b.x"
}

// bigStream creates the BIG stream with 1000 messages, with every 7th and 300 through 449 deleted.
// It returns the sequences left, newest first.
func bigStream(t *testing.T, js jetstream.JetStream, direct bool) []uint64 {
	t.Helper()
	ctx := context.Background()
	if _, err := tsui.CreateStream(ctx, js, tsui.StreamConfig{Name: "BIG", Subjects: []string{"big.>"}, MaxMsgs: -1, MaxBytes: -1, AllowDirect: direct}); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 1000; i++ {
		if _, err := js.PublishAsync(bigSubject(i), []byte(fmt.Sprintf(`{"n":%d}`, i))); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-js.PublishAsyncComplete():
	case <-time.After(10 * time.Second):
		t.Fatal("publishing timed out")
	}
	var deleted, left []uint64
	for seq := uint64(1000); seq >= 1; seq-- {
		if seq%7 == 0 || (seq >= 300 && seq < 450) {
			deleted = append(deleted, seq)
		} else {
			left = append(left, seq)
		}
	}
	if _, err := tsui.DeleteMessages(ctx, js, "BIG", deleted); err != nil {
		t.Fatal(err)
	}
	return left
}

// openStream opens the details of a stream and waits for them to load.
func openStream(t *testing.T, m model, name string) model {
	t.Helper()
	m.state.streamDetails.stream = tsui.Stream{Name: name}
	m, cmd := m.StreamDetailsSwitchPage()
	m = drain(t, m, cmd)
	if m.page != streamDetails || !m.state.streamDetails.messagesLoaded {
		t.Fatalf("expected the loaded details of %s, got page %v error %q", name, m.page, m.error)
	}
	return m
}

// pageToStart moves to the last row until every older page is loaded.
func pageToStart(t *testing.T, m model) model {
	t.Helper()
	for i := 0; m.state.streamDetails.olderNext != 0; i++ {
		if i > 100 {
			t.Fatal("paging did not reach the start of the stream")
		}
		m = step(t, m, "G")
	}
	return m
}

func sequences(msgs []tsui.Message) []uint64 {
	seqs := make([]uint64, len(msgs))
	for i, msg := range msgs {
		seqs[i] = msg.Sequence
	}
	return seqs
}

// checkNewestFirst fails when seqs are not strictly descending, which a duplicate also breaks.
func checkNewestFirst(t *testing.T, seqs []uint64) {
	t.Helper()
	for i := 1; i < len(seqs); i++ {
		if seqs[i] >= seqs[i-1] {
			t.Fatalf("expected strictly descending sequences, got %d after %d", seqs[i], seqs[i-1])
		}
	}
}

func publishN(t *testing.T, js jetstream.JetStream, subject string, n int) {
	t.Helper()
	for i := range n {
		if _, err := tsui.PublishMessage(context.Background(), js, subject, nil, []byte(fmt.Sprint(i))); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFlowPagingReachesTheFirstMessage(t *testing.T) {
	for _, direct := range []bool{true, false} {
		t.Run(fmt.Sprintf("direct %v", direct), func(t *testing.T) {
			m, js := liveModel(t)
			want := bigStream(t, js, direct)
			m = openStream(t, m, "BIG")
			d := m.state.streamDetails
			if len(d.messages) < 100 || d.olderNext == 0 || d.messages[0].Sequence != 1000 {
				t.Fatalf("expected the newest 100 messages first, got %d from #%d", len(d.messages), d.messages[0].Sequence)
			}
			if hint := ansi.Strip(m.renderTabBar()); !strings.Contains(hint, "newest") {
				t.Errorf("expected the newest window in the hint, got %q", hint)
			}

			m = step(t, m, "G")
			if hint := ansi.Strip(m.renderTabBar()); !m.state.streamDetails.paged || !strings.Contains(hint, "loaded") {
				t.Errorf("expected an older page to load at the last row, hint %q", hint)
			}
			m = pageToStart(t, m)
			got := sequences(m.state.streamDetails.messages)
			checkNewestFirst(t, got)
			if len(got) != len(want) || got[len(got)-1] != want[len(want)-1] {
				t.Fatalf("expected all %d messages down to #%d, got %d down to #%d", len(want), want[len(want)-1], len(got), got[len(got)-1])
			}
			if hint := ansi.Strip(m.renderTabBar()); !strings.Contains(hint, "start of stream") {
				t.Errorf("expected the start of the stream in the hint, got %q", hint)
			}
			// Moving past the end once everything is loaded asks for nothing more.
			if _, cmd := update(t, m, press("j")); len(collect(cmd)) != 0 {
				t.Error("expected no load past the start of the stream")
			}
		})
	}
}

func TestFlowRefreshKeepsOlderPages(t *testing.T) {
	m, js := liveModel(t)
	bigStream(t, js, true)
	m = openStream(t, m, "BIG")
	m = step(t, m, "G", "G")
	d := m.state.streamDetails
	lowest := d.messages[len(d.messages)-1].Sequence
	loaded := len(d.messages)
	cursor, _ := m.currentMessage()
	if !d.paged || cursor.Sequence == d.messages[0].Sequence {
		t.Fatalf("expected older pages with the cursor down, got paged %v", d.paged)
	}

	publishN(t, js, "big.a.x", 5)
	m, cmd := update(t, m, refreshTick(m))
	m = drain(t, m, cmd)
	d = m.state.streamDetails
	checkNewestFirst(t, sequences(d.messages))
	if len(d.messages) != loaded+5 || d.messages[0].Sequence != 1005 || d.messages[len(d.messages)-1].Sequence != lowest {
		t.Fatalf("expected the new messages on top of the older pages, got %d messages #%d to #%d", len(d.messages), d.messages[0].Sequence, d.messages[len(d.messages)-1].Sequence)
	}
	if cur, _ := m.currentMessage(); cur.Sequence != cursor.Sequence {
		t.Errorf("expected the cursor to stay on #%d, got #%d", cursor.Sequence, cur.Sequence)
	}

	// r reloads the newest messages and keeps the older ones too.
	m = step(t, m, "r")
	if d = m.state.streamDetails; len(d.messages) != loaded+5 || d.messages[len(d.messages)-1].Sequence != lowest {
		t.Errorf("expected r to keep the older pages, got %d messages", len(d.messages))
	}

	// Paging on after the refresh continues where it was.
	m = pageToStart(t, m)
	checkNewestFirst(t, sequences(m.state.streamDetails.messages))

	// Too many new messages to reach the loaded ones lets the older pages go.
	publishN(t, js, "big.a.x", 1100)
	m, cmd = update(t, m, refreshTick(m))
	m = drain(t, m, cmd)
	if d = m.state.streamDetails; d.paged || d.messages[0].Sequence != 2105 || m.statusTone != styles.ToneWarning {
		t.Errorf("expected the newest messages alone with a warning, got paged %v from #%d status %q", d.paged, d.messages[0].Sequence, m.statusMsg)
	}
	checkNewestFirst(t, sequences(m.state.streamDetails.messages))
}

func TestFlowJumpToSequence(t *testing.T) {
	m, js := liveModel(t)
	m = openOrders(t, m)
	if _, err := tsui.DeleteMessages(context.Background(), js, "ORDERS", []uint64{3}); err != nil {
		t.Fatal(err)
	}

	m = step(t, m, "#")
	if !m.textInputActive() || !strings.Contains(ansi.Strip(m.renderFooter()), "enter open") {
		t.Fatal("expected the sequence input in the footer")
	}
	m = typeText(t, m, "x5q")
	m = step(t, m, "enter")
	if m.page != messageDetails || m.state.messageDetails.message.Sequence != 5 {
		t.Fatalf("expected message #5 to open, got page %v", m.page)
	}
	if !strings.Contains(ansi.Strip(m.render()), `"n": 5`) {
		t.Error("expected the payload of #5")
	}

	m = step(t, m, "q", "#")
	m = typeText(t, m, "3")
	m = step(t, m, "enter")
	if m.page != streamDetails || m.statusMsg != "message #3 was deleted" || m.statusTone != styles.ToneWarning {
		t.Fatalf("expected a deleted message to be reported, got page %v status %q", m.page, m.statusMsg)
	}
	m = step(t, m, "#")
	m = typeText(t, m, "99")
	m = step(t, m, "enter")
	if m.statusMsg != "no message #99 (stream holds #1–#12)" {
		t.Errorf("expected a sequence past the stream to be reported with its range, got %q", m.statusMsg)
	}
	m = step(t, m, "#", "enter")
	if !m.state.streamDetails.jumping || m.statusMsg != "enter a sequence number" {
		t.Errorf("expected an empty sequence to be refused, got status %q", m.statusMsg)
	}
	m = step(t, m, "esc")
	if m.state.streamDetails.jumping || m.page != streamDetails {
		t.Error("expected esc to close the input")
	}
}

func TestFlowSubjectDrillLoadsOlderMessages(t *testing.T) {
	m, js := liveModel(t)
	left := bigStream(t, js, true)
	var rare []uint64
	for _, seq := range left {
		if bigSubject(int(seq)) == "big.rare.x" {
			rare = append(rare, seq)
		}
	}
	m = openStream(t, m, "BIG")
	m = step(t, m, "tab", "tab")
	d := m.state.streamDetails
	i := -1
	for j, s := range m.getFilteredSubjects() {
		if s.Subject == "big.rare.x" {
			i = j
		}
	}
	if i < 0 {
		t.Fatalf("expected big.rare.x among the subjects, got %+v", d.subjects)
	}
	for range i {
		m = step(t, m, "j")
	}
	m = step(t, m, "enter")
	d = m.state.streamDetails
	if d.subject != "big.rare.x" || d.tab != tabMessages {
		t.Fatalf("expected the messages on big.rare.x, got subject %q", d.subject)
	}
	m = pageToStart(t, m)
	got := sequences(m.state.streamDetails.messages)
	if fmt.Sprint(got) != fmt.Sprint(rare) {
		t.Fatalf("expected %v, got %v", rare, got)
	}
	if view := ansi.Strip(m.StreamDetailsView()); strings.Contains(view, "no match among") {
		t.Error("expected no mention of the newest window for a subject load")
	}

	m = step(t, m, "q")
	if d = m.state.streamDetails; d.tab != tabSubjects || d.subject != "" || len(d.messages) < 100 {
		t.Errorf("expected q to return to the subjects and the newest messages, got tab %v subject %q", d.tab, d.subject)
	}
}

func TestFlowFilterLoadsSubjectsFromTheServer(t *testing.T) {
	m, js := liveModel(t)
	var rare []uint64
	for _, seq := range bigStream(t, js, false) {
		if bigSubject(int(seq)) == "big.rare.x" {
			rare = append(rare, seq)
		}
	}
	m = openStream(t, m, "BIG")

	m = step(t, m, "/")
	m = typeText(t, m, "big.rare.*")
	m = step(t, m, "enter")
	d := m.state.streamDetails
	if got := sequences(d.messages); d.subject != "big.rare.*" || fmt.Sprint(got) != fmt.Sprint(rare) {
		t.Fatalf("expected the messages on big.rare.* from the server %v, got subject %q and %v", rare, d.subject, got)
	}

	m = step(t, m, "esc")
	if d = m.state.streamDetails; d.subject != "" || d.filterText != "" || len(d.messages) < 100 {
		t.Fatalf("expected esc to return to the newest messages, got subject %q", d.subject)
	}

	// A word no subject of the stream takes searches the loaded messages.
	m = step(t, m, "/")
	m = typeText(t, m, "rare")
	m = step(t, m, "enter")
	d = m.state.streamDetails
	if d.subject != "" || !d.messagesLoaded {
		t.Fatalf("expected a plain filter to stay local, got subject %q", d.subject)
	}
	for _, msg := range m.getFilteredMessages() {
		if msg.Subject != "big.rare.x" {
			t.Errorf("unexpected match %s", msg.Subject)
		}
	}
	if n := len(m.getFilteredMessages()); n == 0 || n > 5 {
		t.Errorf("expected the matches among the newest messages only, got %d", n)
	}

	// A literal subject that holds nothing suggests its wildcard.
	m = step(t, m, "esc", "/")
	m = typeText(t, m, "big.rare")
	m = step(t, m, "enter")
	if view := ansi.Strip(m.StreamDetailsView()); !strings.Contains(view, "no messages on big.rare, try big.rare.>") {
		t.Errorf("expected the empty subject to be explained:\n%s", view)
	}
}

func TestFlowFollow(t *testing.T) {
	m, js := liveModel(t)
	m = openOrders(t, m)
	refresh := func(m model) model {
		m, cmd := update(t, m, refreshTick(m))
		return drain(t, m, cmd)
	}
	foot := func(m model) string {
		_, f := m.frameMeta()
		return ansi.Strip(f)
	}
	if !m.state.streamDetails.follow || !strings.Contains(foot(m), "following") {
		t.Fatalf("expected to follow by default, foot %q", foot(m))
	}

	publishN(t, js, "orders.eu", 1)
	m = refresh(m)
	if cur, _ := m.currentMessage(); cur.Sequence != 13 {
		t.Errorf("expected the cursor to follow the newest message, got #%d", cur.Sequence)
	}

	m = step(t, m, "f")
	if m.state.streamDetails.follow || !strings.Contains(foot(m), "follow off") {
		t.Fatalf("expected f to stop following, foot %q", foot(m))
	}
	publishN(t, js, "orders.eu", 1)
	m = refresh(m)
	if cur, _ := m.currentMessage(); cur.Sequence != 13 || m.state.streamDetails.selectedMessage != 1 {
		t.Errorf("expected the cursor to stay on #13, got #%d", cur.Sequence)
	}

	m = step(t, m, "f")
	if cur, _ := m.currentMessage(); !m.state.streamDetails.follow || cur.Sequence != 14 {
		t.Errorf("expected f to follow again from the newest message, got #%d", cur.Sequence)
	}
	// Moved off the top row, the cursor stays on its message while following.
	m = step(t, m, "j")
	publishN(t, js, "orders.eu", 1)
	m = refresh(m)
	if cur, _ := m.currentMessage(); cur.Sequence != 13 {
		t.Errorf("expected the cursor to stay on #13 off the top row, got #%d", cur.Sequence)
	}

	// f only follows on the messages tab.
	m = step(t, m, "tab", "f")
	if !m.state.streamDetails.follow {
		t.Error("expected f to do nothing on the consumers tab")
	}
}

func TestFlowPauseStopsRefresh(t *testing.T) {
	m, _ := liveModel(t)
	m = openOrders(t, m)
	loads := func(cmd tea.Cmd) int {
		n := 0
		for _, msg := range collect(cmd) {
			switch msg.(type) {
			case messages.StreamLoadedMsg, messages.ConsumersLoadedMsg:
				n++
			}
		}
		return n
	}

	m = step(t, m, "p")
	if _, foot := m.frameMeta(); !m.paused || !strings.Contains(ansi.Strip(foot), "paused") {
		t.Fatal("expected p to pause with the frame saying so")
	}
	if _, cmd := update(t, m, refreshTick(m)); loads(cmd) != 0 {
		t.Error("expected a tick while paused not to load anything")
	}
	if _, cmd := update(t, m, press("r")); loads(cmd) == 0 {
		t.Error("expected r to refresh while paused")
	}

	m, cmd := update(t, m, press("p"))
	if m.paused || loads(cmd) == 0 {
		t.Fatal("expected p to resume with a refresh")
	}
	gen := m.refreshGen
	m = drain(t, m, cmd)
	if m.refreshGen == gen {
		t.Error("expected resuming to schedule the next tick")
	}
	if _, cmd := update(t, m, refreshTick(m)); loads(cmd) == 0 {
		t.Error("expected ticks to load again after resuming")
	}

	// p is typed into the filter, not taken as pause.
	m = step(t, m, "/", "p")
	if m.paused || m.state.streamDetails.filterText != "p" {
		t.Error("expected p to go into the filter")
	}
}

func TestRefreshedAgo(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0:                "refreshed just now",
		3 * time.Second:  "refreshed 3s ago",
		59 * time.Second: "refreshed 59s ago",
		2 * time.Minute:  "refreshed 2m ago",
		3 * time.Hour:    "refreshed 3h ago",
	} {
		if got := formatRefreshed(d + 50*time.Millisecond); got != want {
			t.Errorf("%v: got %q, want %q", d, got, want)
		}
	}
	for d, want := range map[time.Duration]time.Duration{
		1500 * time.Millisecond: 500 * time.Millisecond,
		90 * time.Second:        30 * time.Second,
		90 * time.Minute:        30 * time.Minute,
	} {
		if got := untilAgoChanges(d); got != want {
			t.Errorf("%v: next change in %v, want %v", d, got, want)
		}
	}

	m := detailsModel(t)
	if _, cmd := update(t, m, messages.StreamLoadedMsg{Name: "ORDERS", Stream: testStreams[2]}); cmd == nil {
		t.Fatal("expected commands after the stream loaded")
	}
	m, _ = update(t, m, messages.StreamLoadedMsg{Name: "ORDERS", Stream: testStreams[2]})
	if !m.clocking || m.refreshedAt.IsZero() {
		t.Fatal("expected the clock to tick once the details are refreshed")
	}
	if _, foot := m.frameMeta(); !strings.Contains(ansi.Strip(foot), "refreshed just now") {
		t.Errorf("expected the time since the refresh in the frame, got %q", ansi.Strip(foot))
	}
	// Off a page that refreshes, the clock stops at its next tick.
	m, _ = update(t, m, press("enter"))
	m, cmd := update(t, m, messages.ClockTickMsg{})
	if m.clocking || cmd != nil {
		t.Error("expected the clock to stop where it is not shown")
	}
}

func TestMergeNewer(t *testing.T) {
	msgs := func(seqs ...uint64) []tsui.Message {
		out := make([]tsui.Message, len(seqs))
		for i, s := range seqs {
			out[i] = tsui.Message{Sequence: s}
		}
		return out
	}
	loaded := msgs(10, 9, 7, 5, 3, 2)
	for _, c := range []struct {
		fresh []tsui.Message
		next  uint64
		want  string
	}{
		{msgs(12, 11, 10, 9), 8, "[12 11 10 9 7 5 3 2]"},
		{msgs(12, 10), 9, "[12 10 7 5 3 2]"}, // 11 and 9 were deleted
		{msgs(12, 11), 0, "[12 11]"},
	} {
		if got := fmt.Sprint(sequences(mergeNewer(loaded, c.fresh, c.next))); got != c.want {
			t.Errorf("next %d: got %s, want %s", c.next, got, c.want)
		}
	}
}

func TestOlderPagesOnlyApplyWhileAskedFor(t *testing.T) {
	m := detailsModel(t)
	m.state.streamDetails.olderNext = 1
	m, cmd := update(t, m, press("G"))
	d := m.state.streamDetails
	if cmd == nil || !d.loadingOlder {
		t.Fatal("expected G at the last row to load older messages")
	}
	if !strings.Contains(ansi.Strip(m.renderTabBar()), "loading older…") {
		t.Errorf("expected the hint to say older messages are loading, got %q", ansi.Strip(m.renderTabBar()))
	}
	older := messages.OlderMessagesLoadedMsg{Stream: "ORDERS", Gen: m.olderGen, Before: 1, Messages: []tsui.Message{{Stream: "ORDERS", Sequence: 0}}}
	stale := older
	stale.Gen--
	m, _ = update(t, m, stale)
	if !m.state.streamDetails.loadingOlder || len(m.state.streamDetails.messages) != 3 {
		t.Fatal("expected a page no longer asked for to be dropped")
	}

	// A newer load that does not reach the loaded messages is asked for again.
	m.state.streamDetails.paged = true
	m, cmd = update(t, m, messages.MessagesLoadedMsg{Stream: "ORDERS", Gen: m.msgsGen + 1, Next: 50, Messages: []tsui.Message{{Stream: "ORDERS", Sequence: 60}}})
	if cmd == nil || len(m.state.streamDetails.messages) != 3 {
		t.Errorf("expected the load to be asked for again, got %d messages", len(m.state.streamDetails.messages))
	}

	// Loading another subject drops the page in flight.
	m, _ = update(t, m, press("/"))
	m = typeText(t, m, "orders.eu.*")
	m, _ = update(t, m, press("enter"), older)
	if d := m.state.streamDetails; d.loadingOlder || len(d.messages) != 0 {
		t.Errorf("expected the older page of the whole stream to be dropped, got %d messages", len(d.messages))
	}
}
