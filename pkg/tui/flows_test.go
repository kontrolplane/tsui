package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/kontrolplane/tsui/pkg/tsui"
	"github.com/kontrolplane/tsui/pkg/tui/messages"
)

// runServer starts an embedded JetStream server with an ORDERS stream holding 12 messages (6 on
// orders.eu, 6 on orders.us) and a durable consumer, plus an empty EVENTS stream.
func runServer(t *testing.T) (*nats.Conn, jetstream.JetStream) {
	t.Helper()
	srv, err := server.NewServer(&server.Options{
		Host:      "127.0.0.1",
		Port:      -1,
		JetStream: true,
		StoreDir:  t.TempDir(),
		NoLog:     true,
		NoSigs:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats server not ready")
	}
	t.Cleanup(srv.Shutdown)

	nc, err := nats.Connect(srv.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if _, err := tsui.CreateStream(ctx, js, tsui.StreamConfig{Name: "ORDERS", Subjects: []string{"orders.>"}, MaxMsgs: -1, MaxBytes: -1, AllowDirect: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := tsui.CreateStream(ctx, js, tsui.StreamConfig{Name: "EVENTS", Subjects: []string{"events.*"}}); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 12; i++ {
		subject := "orders.eu"
		if i%2 == 0 {
			subject = "orders.us"
		}
		if _, err := tsui.PublishMessage(ctx, js, subject, nil, []byte(fmt.Sprintf(`{"n":%d}`, i))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := js.CreateConsumer(ctx, "ORDERS", jetstream.ConsumerConfig{Durable: "billing", AckPolicy: jetstream.AckExplicitPolicy}); err != nil {
		t.Fatal(err)
	}
	return nc, js
}

// cmdName returns the name of the function behind a command.
func cmdName(cmd tea.Cmd) string {
	return runtime.FuncForPC(reflect.ValueOf(cmd).Pointer()).Name()
}

// cmdFile returns the source file that defines the function behind a command.
func cmdFile(cmd tea.Cmd) string {
	pc := reflect.ValueOf(cmd).Pointer()
	file, _ := runtime.FuncForPC(pc).FileLine(pc)
	return filepath.ToSlash(file)
}

// collect runs the commands that talk to NATS and returns their messages. Timers (refresh ticks,
// status clearing, spinner, cursor blink) and the system clipboard are skipped, which keeps the
// flows fast and deterministic.
func collect(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	// Closure names depend on what the compiler inlines (a command can show up as
	// pkg/tui.model.Init.LoadStreams.request.func3), so commands are told apart by the file
	// that defines them instead.
	name := cmdName(cmd)
	batch := strings.Contains(name, ".compactCmds[")
	if !batch && (!strings.HasSuffix(cmdFile(cmd), "/pkg/tui/commands/commands.go") || strings.Contains(name, "CopyToClipboard")) {
		return nil
	}
	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		var msgs []tea.Msg
		for _, c := range b {
			msgs = append(msgs, collect(c)...)
		}
		return msgs
	}
	if msg == nil {
		return nil
	}
	return []tea.Msg{msg}
}

// drain runs cmd and feeds every resulting message back into the model until it settles.
func drain(t *testing.T, m model, cmd tea.Cmd) model {
	t.Helper()
	queue := collect(cmd)
	for i := 0; len(queue) > 0; i++ {
		if i > 100 {
			t.Fatal("model did not settle")
		}
		msg := queue[0]
		queue = queue[1:]
		m, cmd = update(t, m, msg)
		queue = append(queue, collect(cmd)...)
	}
	return m
}

// step presses the keys one by one, draining the commands each one returns.
func step(t *testing.T, m model, keys ...string) model {
	t.Helper()
	for _, k := range keys {
		var cmd tea.Cmd
		m, cmd = update(t, m, press(k))
		m = drain(t, m, cmd)
	}
	return m
}

func liveModel(t *testing.T) (model, jetstream.JetStream) {
	t.Helper()
	nc, js := runServer(t)
	m := newModel("test", "tsui")
	m.width, m.height = 160, 50
	m.conn, m.js = nc, js
	m = drain(t, m, m.Init())
	return m, js
}

func streamState(t *testing.T, js jetstream.JetStream, name string) tsui.Stream {
	t.Helper()
	s, err := tsui.GetStream(context.Background(), js, name)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func openOrders(t *testing.T, m model) model {
	t.Helper()
	m = step(t, m, "j", "enter")
	d := m.state.streamDetails
	if m.page != streamDetails || d.stream.Name != "ORDERS" || m.loading || !d.messagesLoaded || !d.consumersLoaded {
		t.Fatalf("expected loaded details of ORDERS, got page %v stream %q loading %v", m.page, d.stream.Name, m.loading)
	}
	return m
}

func TestFlowInitLoadsStreamsAndServer(t *testing.T) {
	m, _ := liveModel(t)
	if m.loading || m.error != "" {
		t.Fatalf("expected the overview to be loaded, loading %v error %q", m.loading, m.error)
	}
	if got := m.state.streamOverview.streams; len(got) != 2 || got[0].Name != "EVENTS" || got[1].Name != "ORDERS" {
		t.Fatalf("expected EVENTS and ORDERS, got %+v", got)
	}
	if !m.serverOK || m.server.Streams != 2 || m.server.Consumers != 1 {
		t.Errorf("expected the header to show the account usage, got %+v", m.server)
	}
	view := ansi.Strip(m.render())
	for _, want := range []string{"connected", "rtt", "ORDERS", "streams 2"} {
		if !strings.Contains(view, want) {
			t.Errorf("expected the rendered page to contain %q", want)
		}
	}
}

func TestFlowBrowseDeleteAndPurge(t *testing.T) {
	m, js := liveModel(t)
	m = openOrders(t, m)

	d := m.state.streamDetails
	if len(d.messages) != 12 || d.messages[0].Sequence != 12 || len(d.consumers) != 1 {
		t.Fatalf("expected 12 messages newest first and 1 consumer, got %d messages, %d consumers", len(d.messages), len(d.consumers))
	}

	// Open the newest message, cancel a delete, then confirm it.
	m = step(t, m, "enter")
	if m.page != messageDetails || m.state.messageDetails.message.Sequence != 12 {
		t.Fatalf("expected message 12, got page %v", m.page)
	}
	if !strings.Contains(ansi.Strip(m.render()), `"n": 12`) {
		t.Error("expected the payload to be shown")
	}
	m = step(t, m, "ctrl+d")
	if m.page != messageDelete || !m.state.messageDelete.fromDetails {
		t.Fatalf("expected the delete dialog from the message details, got %v", m.page)
	}
	m = step(t, m, "q")
	if m.page != messageDetails {
		t.Fatalf("expected cancelling to return to the message, got %v", m.page)
	}
	m = step(t, m, "ctrl+d", "l", "enter")
	if m.page != streamDetails || m.loading || len(m.state.streamDetails.messages) != 11 {
		t.Fatalf("expected details with 11 messages after delete, got page %v, %d messages", m.page, len(m.state.streamDetails.messages))
	}
	if s := streamState(t, js, "ORDERS"); s.Messages != 11 {
		t.Errorf("expected message 12 to be deleted on the server, got %d messages", s.Messages)
	}
	if m.state.streamDetails.messages[0].Sequence != 11 {
		t.Errorf("expected message 11 to be the newest, got %d", m.state.streamDetails.messages[0].Sequence)
	}

	// Consumers: open, cancel a delete with esc, then confirm with tab + enter.
	m = step(t, m, "tab", "enter")
	if m.page != consumerDetails || m.state.consumerDetails.consumer.Name != "billing" {
		t.Fatalf("expected consumer details of billing, got %v", m.page)
	}
	if view := ansi.Strip(m.render()); !strings.Contains(view, "billing") || !strings.Contains(view, "durable") {
		t.Error("expected the consumer details to render")
	}
	m = step(t, m, "ctrl+d", "esc")
	if m.page != consumerDetails {
		t.Fatalf("expected cancelling to return to the consumer, got %v", m.page)
	}
	m = step(t, m, "ctrl+d", "tab", "enter")
	if m.page != streamDetails || len(m.state.streamDetails.consumers) != 0 {
		t.Fatalf("expected no consumers after delete, got page %v, %d consumers", m.page, len(m.state.streamDetails.consumers))
	}
	if cs, _ := tsui.ListConsumers(context.Background(), js, "ORDERS"); len(cs) != 0 {
		t.Errorf("expected the consumer to be deleted on the server, got %d", len(cs))
	}

	// Purging the whole stream asks for its name, and cancelling changes nothing.
	m = step(t, m, "ctrl+p", "l", "enter")
	if m.state.streamPurge.step != purgeStepType || m.loading {
		t.Fatal("expected to be asked for the stream name")
	}
	if !strings.Contains(ansi.Strip(m.StreamPurgeView()), "type ORDERS to confirm") {
		t.Error("expected the name prompt to be shown")
	}
	m = step(t, m, "esc")
	if s := streamState(t, js, "ORDERS"); m.page != streamDetails || s.Messages != 11 {
		t.Fatalf("expected esc to cancel the purge, got page %v and %d messages", m.page, s.Messages)
	}
	m = step(t, m, "ctrl+p", "y")
	m = typeText(t, m, "ORDERS")
	m = step(t, m, "enter")
	if m.page != streamDetails || m.error != "" || m.statusMsg != "purged ORDERS" {
		t.Fatalf("expected to return to the details after purging, got page %v error %q", m.page, m.error)
	}
	if s := streamState(t, js, "ORDERS"); s.Messages != 0 {
		t.Errorf("expected an empty stream after purge, got %d", s.Messages)
	}
	if m.state.streamDetails.stream.Messages != 0 || len(m.state.streamDetails.messages) != 0 {
		t.Error("expected the details to be refreshed after purge")
	}

	// Back to the overview and delete the stream.
	m = step(t, m, "q")
	if m.page != streamOverview || m.loading {
		t.Fatalf("expected the loaded overview, got %v", m.page)
	}
	m = step(t, m, "G", "ctrl+d")
	if m.page != streamDelete || m.state.streamDelete.streams[0] != "ORDERS" {
		t.Fatalf("expected the delete dialog for ORDERS, got %v %v", m.page, m.state.streamDelete.streams)
	}
	m = typeText(t, m, "ORDERS")
	m = step(t, m, "enter")
	if m.page != streamOverview || len(m.state.streamOverview.streams) != 1 || m.state.streamOverview.streams[0].Name != "EVENTS" {
		t.Fatalf("expected only EVENTS to be left, got %+v", m.state.streamOverview.streams)
	}
}

func TestFlowBulkDeleteStreams(t *testing.T) {
	m, js := liveModel(t)
	m = step(t, m, "space", "j", "space", "ctrl+d")
	if len(m.state.streamDelete.streams) != 2 || !strings.Contains(ansi.Strip(m.StreamDeleteView()), "2 streams") {
		t.Fatalf("expected both streams in the dialog, got %v", m.state.streamDelete.streams)
	}
	m = typeText(t, m, "delete 2 streams")
	m = step(t, m, "enter")
	if m.statusMsg != "deleted 2 streams" {
		t.Errorf("unexpected status %q", m.statusMsg)
	}
	if len(m.state.streamOverview.streams) != 0 || len(m.state.streamOverview.selectedItems) != 0 {
		t.Errorf("expected no streams and no selection left, got %+v", m.state.streamOverview.streams)
	}
	if names, _ := m.getSelectedStreams(); len(names) != 0 {
		t.Errorf("expected nothing to delete on an empty overview, got %v", names)
	}
	if streams, _ := tsui.ListStreams(context.Background(), js); len(streams) != 0 {
		t.Errorf("expected the streams to be deleted on the server, got %d", len(streams))
	}
}

func TestFlowPublish(t *testing.T) {
	m, js := liveModel(t)
	m = openOrders(t, m)

	m = step(t, m, "ctrl+n")
	m = typeText(t, m, "eu.created")
	m = step(t, m, "tab")
	m = typeText(t, m, "X-Id: 42")
	m = step(t, m, "tab")
	m = typeText(t, m, `{"ok":true}`)
	if !strings.Contains(ansi.Strip(m.MessagePublishView()), "stored by ORDERS via orders.>") {
		t.Error("expected the subject status to confirm the stream")
	}
	m = step(t, m, "ctrl+s")

	if m.page != streamDetails || m.error != "" || m.loading {
		t.Fatalf("expected to return to the details, got page %v error %q", m.page, m.error)
	}
	if m.statusMsg != "published to ORDERS at #13" {
		t.Errorf("unexpected status %q", m.statusMsg)
	}
	got := m.state.streamDetails.messages[0]
	if got.Sequence != 13 || got.Subject != "orders.eu.created" || got.Header.Get("X-Id") != "42" || string(got.Data) != `{"ok":true}` {
		t.Errorf("unexpected published message: %+v", got)
	}
	if s := streamState(t, js, "ORDERS"); s.Messages != 13 {
		t.Errorf("expected 13 messages on the server, got %d", s.Messages)
	}
}

func TestFlowPublishButtons(t *testing.T) {
	m, js := liveModel(t)
	m = openOrders(t, m)

	// shift+tab from the subject wraps to the publish button.
	shiftTab := tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	m = step(t, m, "ctrl+n")
	m.state.messagePublish.subject.SetValue("orders.us")
	m, _ = update(t, m, shiftTab)
	if m.state.messagePublish.focus != publishFocusSubmit {
		t.Fatalf("expected the publish button to be focused, got %v", m.state.messagePublish.focus)
	}
	// left/right move between the buttons, typing does nothing.
	m = step(t, m, "h", "x")
	if m.state.messagePublish.focus != publishFocusCancel {
		t.Fatalf("expected the cancel button to be focused, got %v", m.state.messagePublish.focus)
	}
	m = step(t, m, "enter")
	if m.page != streamDetails {
		t.Fatalf("expected cancel to return to the details, got %v", m.page)
	}
	if s := streamState(t, js, "ORDERS"); s.Messages != 12 {
		t.Error("expected cancel not to publish")
	}

	m = step(t, m, "ctrl+n")
	m.state.messagePublish.subject.SetValue("orders.us")
	m, _ = update(t, m, shiftTab)
	m = step(t, m, "enter")
	if m.page != streamDetails || m.state.streamDetails.messages[0].Sequence != 13 {
		t.Fatalf("expected the publish button to publish, got page %v", m.page)
	}

	// esc leaves the form from any field.
	m = step(t, m, "ctrl+n", "tab", "esc")
	if m.page != streamDetails {
		t.Errorf("expected esc to cancel publishing, got %v", m.page)
	}
}

func TestFlowPublishToMirrorIsRefused(t *testing.T) {
	m := detailsModel(t)
	m.state.streamDetails.stream.Subjects = nil
	m.state.streamDetails.stream.Mirror = "ORIGIN"
	m, _ = update(t, m, press("ctrl+n"))
	if m.page != streamDetails || !strings.Contains(m.error, "no subjects of its own") {
		t.Errorf("expected publishing to a mirror to be refused, got page %v error %q", m.page, m.error)
	}
}

func TestFlowPurgeBySubject(t *testing.T) {
	m, js := liveModel(t)
	m = openOrders(t, m)

	// Load the subjects so the dialog can estimate the purge.
	m = step(t, m, "tab", "tab")
	if !m.state.streamDetails.subjectsLoaded || len(m.state.streamDetails.subjects) != 2 {
		t.Fatalf("expected 2 subjects, got %+v", m.state.streamDetails.subjects)
	}

	m = step(t, m, "ctrl+p")
	if got := m.purgeSubject(); got != "orders.eu" {
		t.Fatalf("expected the purge to start from the subject under the cursor, got %q", got)
	}
	if view := ansi.Strip(m.StreamPurgeView()); !strings.Contains(view, "purge messages on orders.eu") || !strings.Contains(view, "removes 6 messages") {
		t.Errorf("expected a subject purge with its estimate, got\n%s", view)
	}
	m = step(t, m, "tab")
	if !m.textInputActive() {
		t.Fatal("expected the subject field to take input")
	}
	m.state.streamPurge.subjectInput.SetValue("")
	m = typeText(t, m, "payments.x")
	m = step(t, m, "enter", "l", "enter")
	if !strings.Contains(m.error, "not part of stream ORDERS") {
		t.Fatalf("expected a subject outside the stream to be refused, got %q", m.error)
	}
	m = step(t, m, "x") // dismiss

	m.state.streamPurge.subjectInput.SetValue("orders.us")
	if view := ansi.Strip(m.StreamPurgeView()); !strings.Contains(view, "removes 6 messages") || !strings.Contains(view, "purge messages on orders.us") {
		t.Errorf("expected the subject purge to be estimated, got\n%s", view)
	}
	// A small subject purge does not ask twice.
	m = step(t, m, "y")
	if m.page != streamDetails || m.statusMsg != "purged ORDERS (orders.us)" {
		t.Fatalf("expected the subject purge to run at once, got page %v", m.page)
	}
	s := streamState(t, js, "ORDERS")
	if s.Messages != 6 {
		t.Errorf("expected only orders.us to be purged, got %d messages left", s.Messages)
	}
	for _, msg := range m.state.streamDetails.messages {
		if msg.Subject != "orders.eu" {
			t.Errorf("unexpected message on %s after purge", msg.Subject)
		}
	}
}

func TestFlowPurgeFromOverview(t *testing.T) {
	m, js := liveModel(t)
	m = step(t, m, "j", "ctrl+p")
	if m.page != streamPurge || !m.state.streamPurge.fromOverview {
		t.Fatalf("expected the purge dialog from the overview, got %v", m.page)
	}
	m = step(t, m, "esc")
	if m.page != streamOverview {
		t.Fatalf("expected esc to cancel, got %v", m.page)
	}
	m = step(t, m, "ctrl+p", "tab", "esc")
	if m.page != streamOverview {
		t.Fatalf("expected esc in the subject field to cancel, got %v", m.page)
	}
	m = step(t, m, "ctrl+p", "y")
	m = typeText(t, m, "ORDERS")
	m = step(t, m, "enter")
	if m.page != streamOverview || m.loading {
		t.Fatalf("expected to return to the overview, got %v", m.page)
	}
	if s := streamState(t, js, "ORDERS"); s.Messages != 0 {
		t.Errorf("expected ORDERS to be purged, got %d messages", s.Messages)
	}
	if m.state.streamOverview.streams[1].Messages != 0 {
		t.Error("expected the overview to be refreshed")
	}
}

func TestDenyPurge(t *testing.T) {
	m := loadedModel(t)
	m.state.streamOverview.streams[0].DenyPurge = true
	m, _ = update(t, m, press("ctrl+p"))
	if m.page != streamOverview || !strings.Contains(m.error, "deny_purge") {
		t.Errorf("expected a deny_purge error, got page %v error %q", m.page, m.error)
	}
}

func TestFlowCreateStream(t *testing.T) {
	m, js := liveModel(t)

	m = step(t, m, "ctrl+n")
	if m.page != streamCreate || m.state.streamCreate.form == nil {
		t.Fatalf("expected the create form, got %v", m.page)
	}
	if !strings.Contains(ansi.Strip(m.StreamCreateView()), "stream name") {
		t.Error("expected the form to render")
	}
	m = step(t, m, "esc")
	if m.page != streamOverview {
		t.Fatalf("expected esc to leave the form, got %v", m.page)
	}

	m = step(t, m, "ctrl+n")
	*m.state.streamCreate.input = streamCreateInput{name: "PAYMENTS", storage: "memory", retention: "limits", discard: "old", replicas: 1, maxAge: "1h"}
	m, cmd := m.submitStreamCreate()
	m = drain(t, m, cmd)
	if m.page != streamDetails || m.state.streamDetails.stream.Name != "PAYMENTS" || m.error != "" {
		t.Fatalf("expected the details of the new stream, got page %v error %q", m.page, m.error)
	}
	s := streamState(t, js, "PAYMENTS")
	if s.Storage != "memory" || s.MaxAge != time.Hour || len(s.Subjects) != 1 || s.Subjects[0] != "payments.>" {
		t.Errorf("unexpected stream on the server: %+v", s)
	}

	// The server refuses a stream whose subjects overlap an existing one.
	m = step(t, m, "q", "ctrl+n")
	*m.state.streamCreate.input = streamCreateInput{name: "CLASH", subjects: "orders.eu", storage: "file", retention: "limits", discard: "old", replicas: 1}
	m, cmd = m.submitStreamCreate()
	m = drain(t, m, cmd)
	if m.page != streamCreate || !strings.Contains(m.error, "could not create stream") || m.state.streamCreate.input.name != "CLASH" {
		t.Errorf("expected the form back with the create error, got page %v error %q", m.page, m.error)
	}

	// Invalid input never reaches the server.
	m = step(t, m, "x")
	m.state.streamCreate.input.name = "bad name"
	m, _ = m.submitStreamCreate()
	if m.page != streamCreate || !strings.Contains(m.error, "alphanumeric") {
		t.Errorf("expected a validation error, got page %v error %q", m.page, m.error)
	}
}

func TestFlowStreamGoneWhileBrowsing(t *testing.T) {
	m, js := liveModel(t)
	if err := tsui.DeleteStream(context.Background(), js, "ORDERS"); err != nil {
		t.Fatal(err)
	}
	m = step(t, m, "j", "enter")
	if m.page != streamOverview || m.loading || m.error != "" || m.statusMsg != "stream ORDERS no longer exists" {
		t.Fatalf("expected to return to the overview, got page %v loading %v error %q status %q", m.page, m.loading, m.error, m.statusMsg)
	}
	if len(m.state.streamOverview.streams) != 1 {
		t.Error("expected the overview to be refreshed")
	}
}

func TestFlowDisconnectedHidesLoadErrors(t *testing.T) {
	m, _ := liveModel(t)
	m = openOrders(t, m)
	m.state.streamDetails.messagesLoaded = false
	m.conn.Close()
	m, _ = update(t, m,
		messages.MessagesLoadedMsg{Stream: "ORDERS", Gen: m.msgsGen, Err: nats.ErrConnectionClosed},
		messages.ServerLoadedMsg{Err: nats.ErrConnectionClosed},
	)
	if m.error != "" || m.statusMsg != "" {
		t.Errorf("expected no error while disconnected, got error %q status %q", m.error, m.statusMsg)
	}
	if _, foot := m.frameMeta(); strings.Contains(ansi.Strip(foot), "rtt") || !strings.Contains(ansi.Strip(foot), "closed") {
		t.Errorf("expected the frame to show the connection state without rtt, got %q", ansi.Strip(foot))
	}
}

func TestFlowRefreshTicks(t *testing.T) {
	m, js := liveModel(t)
	ctx := context.Background()
	if _, err := tsui.CreateStream(ctx, js, tsui.StreamConfig{Name: "LATE", Subjects: []string{"late"}}); err != nil {
		t.Fatal(err)
	}
	_, cmd := update(t, m, refreshTick(m))
	m = drain(t, m, cmd)
	if len(m.state.streamOverview.streams) != 3 {
		t.Errorf("expected the overview tick to pick up the new stream, got %d", len(m.state.streamOverview.streams))
	}

	m = step(t, m, "G", "enter") // ORDERS
	if _, err := tsui.PublishMessage(ctx, js, "orders.eu", nil, []byte("new")); err != nil {
		t.Fatal(err)
	}
	m, cmd = update(t, m, refreshTick(m))
	m = drain(t, m, cmd)
	if m.state.streamDetails.stream.Messages != 13 || len(m.state.streamDetails.messages) != 13 {
		t.Errorf("expected the details tick to pick up the new message, got %d", m.state.streamDetails.stream.Messages)
	}

	m = step(t, m, "tab", "enter")
	if _, err := js.Consumer(ctx, "ORDERS", "billing"); err != nil {
		t.Fatal(err)
	}
	c, _ := js.Consumer(ctx, "ORDERS", "billing")
	batch, err := c.Fetch(3, jetstream.FetchMaxWait(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	for range batch.Messages() {
	}
	m, cmd = update(t, m, refreshTick(m))
	m = drain(t, m, cmd)
	got := m.state.consumerDetails.consumer
	if got.NumAckPending != 3 || got.Delivered != 3 {
		t.Errorf("expected the consumer tick to pick up the deliveries, got %+v", got)
	}
	if !strings.Contains(ansi.Strip(m.ConsumerDetailsView()), "#3") {
		t.Error("expected the delivered sequence to be drawn")
	}
}

// refreshTick returns the tick the model is currently waiting for.
func refreshTick(m model) tea.Msg {
	return messages.RefreshTickMsg{Gen: m.refreshGen}
}
