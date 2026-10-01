package tui

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/kontrolplane/tsui/pkg/client"
	keys "github.com/kontrolplane/tsui/pkg/keys"
	"github.com/kontrolplane/tsui/pkg/tsui"
	"github.com/kontrolplane/tsui/pkg/tui/commands"
	"github.com/kontrolplane/tsui/pkg/tui/messages"
	"github.com/kontrolplane/tsui/pkg/tui/styles"
)

func NewModel(
	projectName string,
	programName string,
	conn *nats.Conn,
	js jetstream.JetStream,
	info client.Info,
) tea.Model {
	m := newModel(projectName, programName)
	m.conn = conn
	m.js = js
	m.info = info
	return m
}

func newModel(projectName, programName string) model {
	return model{
		projectName: projectName,
		programName: programName,
		page:        streamOverview,
		context:     context.Background(),
		loading:     true,
		loadingMsg:  "loading streams…",
		spinner:     spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		keys:        keys.Keys,
		state: state{
			streamOverview: streamOverviewState{
				table:         newDataTable(streamOverviewColumns, contentWidth-4, contentHeight-tableHeaderRows),
				selectedItems: make(map[string]bool),
				filterInput:   initFilterInput("filter by name or subject…"),
			},
			streamDetails: streamDetailsState{
				selectedMessages:  make(map[uint64]bool),
				selectedConsumers: make(map[string]bool),
				filterInput:       initFilterInput("filter by subject (wildcards allowed) or content…"),
				jumpInput:         initJumpInput(),
				follow:            true,
			},
		},
	}
}

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{commands.LoadStreams(m.context, m.js), m.spinner.Tick}
	if m.conn != nil {
		cmds = append(cmds, commands.LoadServer(m.context, m.conn, m.js))
	}
	return tea.Batch(cmds...)
}

// textInputActive reports whether keystrokes are currently going into a text field,
// in which case single character shortcuts must not trigger.
func (m model) textInputActive() bool {
	switch m.page {
	case streamCreate, messagePublish, streamDelete:
		return true
	case streamOverview:
		return m.state.streamOverview.filtering
	case streamDetails:
		return m.state.streamDetails.filtering || m.state.streamDetails.jumping
	case streamPurge:
		p := m.state.streamPurge
		return p.focus == purgeFocusSubject || p.step == purgeStepType
	}
	return false
}

// inStream reports whether the current page shows the stream held in the details state, so
// results loaded for that stream still belong on screen.
func (m model) inStream() bool {
	switch m.page {
	case streamDetails, messageDetails, messagePublish, messageDelete, consumerDetails, consumerDelete:
		return true
	case streamPurge:
		return !m.state.streamPurge.fromOverview
	}
	return false
}

// disconnected reports whether the NATS connection is down, which the header already shows.
func (m model) disconnected() bool {
	return m.conn != nil && !m.conn.IsConnected()
}

// keepSpinning restarts the loading spinner when a load began after it last stopped.
func (m model) keepSpinning() (model, tea.Cmd) {
	if !m.loading || m.spinning {
		return m, nil
	}
	m.spinning = true
	return m, m.spinner.Tick
}

func (m model) scheduleRefresh() (model, tea.Cmd) {
	m.refreshGen++
	return m, commands.ScheduleRefresh(m.refreshGen)
}

// setStatus shows text in the footer for a few seconds, in the success, warning or danger tone.
func (m model) setStatus(text string, tone styles.Tone) (model, tea.Cmd) {
	m.statusGen++
	m.statusMsg = text
	m.statusTone = tone
	return m, commands.ClearStatusAfter(3*time.Second, m.statusGen)
}

// withStatus is setStatus for a handler that already has a command to return.
func (m model) withStatus(cmd tea.Cmd, text string, tone styles.Tone) (model, tea.Cmd) {
	m, status := m.setStatus(text, tone)
	return m, tea.Batch(cmd, status)
}

// finish ends a create, delete, purge or publish once its result is in: it stops the spinner,
// navigates with back, and reports done in the footer, or failed with the error in a dialog.
func (m model) finish(back func(model) (model, tea.Cmd), done, failed string, err error) (model, tea.Cmd) {
	m.busy, m.loading = false, false
	m, cmd := back(m)
	if err != nil {
		m.error = fmt.Sprintf("%s: %s", failed, errorText(err))
		return m, cmd
	}
	return m.withStatus(cmd, done, styles.ToneSuccess)
}

// bulkResult describes the outcome of deleting items: what succeeded, and what to say when some
// or all of them failed.
func bulkResult(noun string, items, deleted []string) (done, failed string) {
	label := func(names []string) string {
		if len(names) == 1 {
			return noun + " " + names[0]
		}
		return plural(len(names), noun)
	}
	done = "deleted " + label(deleted)
	if len(deleted) == 0 {
		return done, "could not delete " + label(items)
	}
	return done, fmt.Sprintf("deleted %d of %s, the others failed", len(deleted), plural(len(items), noun))
}

// loadError reports a failed load. Data already on screen stays there with the error in the
// footer, so a refresh failing every few seconds does not keep raising a dialog. While the
// connection is down the header says so and the error is not repeated.
func (m model) loadError(what string, err error, onScreen bool) (model, tea.Cmd) {
	switch {
	case m.disconnected():
		return m, nil
	case onScreen:
		return m.setStatus(fmt.Sprintf("%s: %s", what, errorText(err)), styles.ToneDanger)
	}
	m.error = fmt.Sprintf("%s: %s", what, errorText(err))
	return m, nil
}

// streamGone leaves a stream that was deleted elsewhere.
func (m model) streamGone(name string) (model, tea.Cmd) {
	m.loading = false
	o := &m.state.streamOverview
	o.streams = deleteNames(o.streams, []string{name}, func(s tsui.Stream) string { return s.Name })
	m = m.updateStreamOverviewTable()
	m, cmd := m.StreamOverviewGoBack()
	return m.withStatus(cmd, fmt.Sprintf("stream %s no longer exists", name), styles.ToneDanger)
}

// consumerGone leaves the details of a consumer that was deleted elsewhere, or expired.
func (m model) consumerGone(name string) (model, tea.Cmd) {
	d := &m.state.streamDetails
	d.consumers = deleteNames(d.consumers, []string{name}, func(c tsui.Consumer) string { return c.Name })
	d.tab = tabConsumers
	m = m.focusDetailsTab().updateConsumersTable()
	m, cmd := m.StreamDetailsGoBack()
	return m.withStatus(cmd, fmt.Sprintf("consumer %s no longer exists", name), styles.ToneDanger)
}

// streamContentChanged reports whether messages were added, removed or expired since prev.
func streamContentChanged(prev, cur tsui.Stream) bool {
	return prev.LastSeq != cur.LastSeq || prev.FirstSeq != cur.FirstSeq ||
		prev.Messages != cur.Messages || prev.NumDeleted != cur.NumDeleted
}

// hasControlChars reports whether s holds control characters other than line breaks and tabs.
func hasControlChars(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool {
		return unicode.IsControl(r) && r != '\n' && r != '\t' && r != '\r'
	})
}

// apiErrorPrefix is how nats.go introduces an error returned by the JetStream API, which says
// nothing the description after it does not.
var apiErrorPrefix = regexp.MustCompile(`nats: API error: code=\d+ err_code=\d+ description=`)

// errorText words err for the interface, without the API error codes.
func errorText(err error) string {
	return apiErrorPrefix.ReplaceAllString(err.Error(), "")
}

// offlineStatus answers a refresh asked for while the connection is down.
const offlineStatus = "not connected, showing cached data"

// notConnected is what an action refused while the connection is down says.
func notConnected(nothing string) string {
	return "not connected to nats, nothing was " + nothing
}

// publishError words a failed publish. Without an ack the outcome is unknown: nats.go buffers a
// publish made while reconnecting and sends it once connected, so the message may still be stored.
func (m model) publishError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, nats.ErrTimeout) || m.disconnected() {
		return "no ack received, the message may still be stored. check the stream before publishing again"
	}
	return "could not publish: " + errorText(err)
}

// showCreatedStream puts a stream just created into the overview with the cursor on it, so
// leaving its details returns to it before the next refresh lists it.
func (m model) showCreatedStream(s tsui.Stream) model {
	o := &m.state.streamOverview
	i, found := slices.BinarySearchFunc(o.streams, s.Name, func(s tsui.Stream, name string) int { return strings.Compare(s.Name, name) })
	if found {
		o.streams[i] = s
	} else {
		o.streams = slices.Insert(slices.Clone(o.streams), i, s)
	}
	o.selected = follow(m.getFilteredStreams(), o.selected, func(f tsui.Stream) bool { return f.Name == s.Name })
	return m.updateStreamOverviewTable()
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	var cmd tea.Cmd

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		setLayout(msg.Width, msg.Height)
		m = m.resize()

	case spinner.TickMsg:
		if !m.loading {
			m.spinning = false
			return m, nil
		}
		m.spinning = true
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case messages.ServerLoadedMsg:
		if msg.Err == nil {
			m.server = msg.Server
			m.serverOK = true
			m.rttOK = true
		} else {
			m.rttOK = false
		}
		return m, commands.ScheduleServerRefresh()

	case messages.ServerTickMsg:
		return m, commands.LoadServer(m.context, m.conn, m.js)

	case tea.KeyPressMsg:
		if key.Matches(msg, m.keys.ForceQuit) {
			return m, tea.Quit
		}
		if m.busy || m.tooSmall() {
			return m, nil
		}
		if m.error != "" {
			m.error = ""
			return m, nil
		}
		if !m.textInputActive() && key.Matches(msg, m.keys.Help) {
			m.showHelp = !m.showHelp
			return m, nil
		}
		if m.showHelp {
			m.showHelp = false
			return m, nil
		}
		if !key.Matches(msg, m.keys.Back) {
			m.armed = false
		}
		if !m.textInputActive() && m.refreshes() && key.Matches(msg, m.keys.Pause) {
			return m.togglePause()
		}

	case messages.ClockTickMsg:
		m.clocking = false
		return m.keepClock()

	case messages.StreamsLoadedMsg:
		o := &m.state.streamOverview
		if m.page == streamOverview {
			m.loading = false
		}
		if msg.Err != nil {
			if m.page == streamOverview {
				m, cmd = m.loadError("could not load streams", msg.Err, o.loaded)
				cmds = append(cmds, cmd)
			}
		} else {
			prev, ok := m.currentStream()
			o.streams = msg.Streams
			o.loaded = true
			if m.page == streamOverview {
				m.refreshedAt = time.Now()
			}
			prune(o.selectedItems, o.streams, func(s tsui.Stream) string { return s.Name })
			if ok {
				o.selected = follow(m.getFilteredStreams(), o.selected, func(s tsui.Stream) bool { return s.Name == prev.Name })
			}
			m = m.updateStreamOverviewTable()
		}
		if m.page == streamOverview {
			m, cmd = m.scheduleRefresh()
			cmds = append(cmds, cmd)
		}

	case messages.StreamLoadedMsg:
		d := &m.state.streamDetails
		if msg.Name != d.stream.Name || !m.inStream() {
			break
		}
		if m.page == streamDetails {
			m.loading = false
		}
		switch {
		case errors.Is(msg.Err, jetstream.ErrStreamNotFound) && !m.busy:
			return m.streamGone(msg.Name)
		case msg.Err != nil:
			m, cmd = m.loadError("could not load stream "+msg.Name, msg.Err, d.attributesTable != "")
			cmds = append(cmds, cmd)
		default:
			prev := d.stream
			d.stream = msg.Stream
			d.attributesTable = renderAttributesTable(msg.Stream)
			if m.page == streamDetails {
				m.refreshedAt = time.Now()
			}
			if msg.Refresh && (!d.messagesLoaded || d.messagesStale || streamContentChanged(prev, msg.Stream)) {
				m, cmd = m.loadMessages()
				cmds = append(cmds, cmd)
				if d.tab == tabSubjects {
					cmds = append(cmds, m.loadSubjects())
				}
			}
		}
		if m.page == streamDetails {
			m, cmd = m.scheduleRefresh()
			cmds = append(cmds, cmd)
		}

	case messages.MessagesLoadedMsg:
		d := &m.state.streamDetails
		if msg.Stream != d.stream.Name || msg.Subject != d.subject || !m.inStream() || msg.Gen < m.msgsApplied {
			break
		}
		m.msgsApplied = msg.Gen
		if msg.Err != nil {
			d.messagesStale = true
			if !errors.Is(msg.Err, jetstream.ErrStreamNotFound) {
				m, cmd = m.loadError("could not load messages", msg.Err, d.messagesLoaded)
				cmds = append(cmds, cmd)
			}
			break
		}
		m, cmd = m.applyMessages(msg)
		cmds = append(cmds, cmd)

	case messages.OlderMessagesLoadedMsg:
		d := &m.state.streamDetails
		if msg.Stream != d.stream.Name || msg.Gen != m.olderGen || !d.loadingOlder || !m.inStream() {
			break
		}
		m, cmd = m.applyOlderMessages(msg)
		cmds = append(cmds, cmd)

	case messages.MessageFetchedMsg:
		if msg.Stream != m.state.streamDetails.stream.Name || m.page != streamDetails {
			break
		}
		return m.openFetchedMessage(msg)

	case messages.ConsumersLoadedMsg:
		d := &m.state.streamDetails
		if msg.Stream != d.stream.Name || !m.inStream() || msg.Gen < m.consApplied {
			break
		}
		m.consApplied = msg.Gen
		if msg.Err != nil {
			if !errors.Is(msg.Err, jetstream.ErrStreamNotFound) {
				m, cmd = m.loadError("could not load consumers", msg.Err, d.consumersLoaded)
				cmds = append(cmds, cmd)
			}
			break
		}
		prev, ok := m.currentConsumer()
		d.consumers = msg.Consumers
		d.consumersLoaded = true
		prune(d.selectedConsumers, d.consumers, func(c tsui.Consumer) string { return c.Name })
		if ok {
			d.selectedConsumer = follow(m.getFilteredConsumers(), d.selectedConsumer, func(c tsui.Consumer) bool { return c.Name == prev.Name })
		}
		m = m.updateConsumersTable()
		if m.page == consumerDetails {
			var found bool
			if m, found = m.syncConsumerDetails(); !found && !m.busy {
				return m.consumerGone(m.state.consumerDetails.consumer.Name)
			}
		}

	case messages.ConsumerLoadedMsg:
		c := &m.state.consumerDetails
		if msg.Stream != m.state.streamDetails.stream.Name || msg.Name != c.consumer.Name || m.page != consumerDetails {
			break
		}
		switch {
		case errors.Is(msg.Err, jetstream.ErrConsumerNotFound):
			return m.consumerGone(msg.Name)
		case errors.Is(msg.Err, jetstream.ErrStreamNotFound):
			return m.streamGone(msg.Stream)
		case msg.Err != nil:
			m, cmd = m.loadError("could not load consumer "+msg.Name, msg.Err, true)
			cmds = append(cmds, cmd)
		default:
			m.refreshedAt = time.Now()
			c.consumer = msg.Consumer
			d := &m.state.streamDetails
			for i := range d.consumers {
				if d.consumers[i].Name == msg.Name {
					d.consumers[i] = msg.Consumer
				}
			}
			m = m.updateConsumersTable()
		}
		m, cmd = m.scheduleRefresh()
		cmds = append(cmds, cmd)

	case messages.SubjectsLoadedMsg:
		d := &m.state.streamDetails
		if msg.Stream != d.stream.Name || !m.inStream() {
			break
		}
		if msg.Err != nil {
			if !errors.Is(msg.Err, jetstream.ErrStreamNotFound) {
				m, cmd = m.loadError("could not load subjects", msg.Err, d.subjectsLoaded)
				cmds = append(cmds, cmd)
			}
			break
		}
		prev, ok := m.currentSubject()
		d.subjects = msg.Subjects
		d.subjectsLoaded = true
		if ok {
			d.selectedSubject = follow(m.getFilteredSubjects(), d.selectedSubject, func(s tsui.SubjectCount) bool { return s.Subject == prev.Subject })
		}
		m = m.updateSubjectsTable()

	case messages.StreamCreatedMsg:
		m.busy, m.loading = false, false
		if msg.Err != nil {
			return m.reopenStreamCreate("could not create stream: " + errorText(msg.Err))
		}
		m = m.showCreatedStream(msg.Stream)
		m.state.streamDetails.stream = msg.Stream
		m, cmd = m.StreamDetailsSwitchPage()
		return m.withStatus(cmd, "created stream "+msg.Stream.Name, styles.ToneSuccess)

	case messages.StreamsDeletedMsg:
		o := &m.state.streamOverview
		for _, name := range msg.Names {
			delete(o.selectedItems, name)
		}
		o.streams = deleteNames(o.streams, msg.Names, func(s tsui.Stream) string { return s.Name })
		m = m.updateStreamOverviewTable()
		done, failed := bulkResult("stream", m.state.streamDelete.streams, msg.Names)
		return m.finish(model.StreamOverviewGoBack, done, failed, msg.Err)

	case messages.StreamPurgedMsg:
		p := m.state.streamPurge
		if msg.Stream == m.state.streamDetails.stream.Name {
			m.state.streamDetails.selectedMessages = make(map[uint64]bool)
			m = m.resetMessages(m.state.streamDetails.subject).updateMessagesTable()
		}
		done := "purged " + msg.Stream
		if p.subject != "" {
			done += " (" + p.subject + ")"
		}
		return m.finish(model.streamPurgeGoBack, done, "could not purge "+msg.Stream, msg.Err)

	case messages.MessagePublishedMsg:
		m.busy, m.loading = false, false
		if msg.Err != nil {
			m.error = m.publishError(msg.Err)
			return m, nil
		}
		d := &m.state.streamDetails
		d.tab = tabMessages
		d.selectedMessage = 0
		m = m.focusDetailsTab()
		m, cmd = m.StreamDetailsGoBack()
		if msg.Duplicate {
			return m.withStatus(cmd, fmt.Sprintf("duplicate Nats-Msg-Id, already stored at #%d", msg.Sequence), styles.ToneWarning)
		}
		return m.withStatus(cmd, fmt.Sprintf("published to %s at #%d", msg.Stream, msg.Sequence), styles.ToneSuccess)

	case messages.MessagesDeletedMsg:
		d := &m.state.streamDetails
		for _, seq := range msg.Sequences {
			delete(d.selectedMessages, seq)
		}
		d.messages = deleteNames(d.messages, msg.Sequences, func(msg tsui.Message) uint64 { return msg.Sequence })
		m = m.updateMessagesTable()
		n, total := len(msg.Sequences), len(m.state.messageDelete.sequences)
		done := fmt.Sprintf("deleted %s from %s", plural(n, "message"), msg.Stream)
		failed := "could not delete " + plural(total, "message")
		if n > 0 {
			failed = fmt.Sprintf("deleted %d of %s, the others failed", n, plural(total, "message"))
		}
		return m.finish(model.StreamDetailsGoBack, done, failed, msg.Err)

	case messages.ConsumersDeletedMsg:
		d := &m.state.streamDetails
		for _, name := range msg.Names {
			delete(d.selectedConsumers, name)
		}
		d.consumers = deleteNames(d.consumers, msg.Names, func(c tsui.Consumer) string { return c.Name })
		d.tab = tabConsumers
		m = m.focusDetailsTab().updateConsumersTable()
		done, failed := bulkResult("consumer", m.state.consumerDelete.consumers, msg.Names)
		return m.finish(model.StreamDetailsGoBack, done, failed, msg.Err)

	case messages.ClipboardCopiedMsg:
		if msg.Err != nil {
			cmds = append(cmds, tea.SetClipboard(msg.Text))
		}
		if hasControlChars(msg.Text) {
			m, cmd = m.setStatus("copied, payload contains control characters", styles.ToneWarning)
		} else {
			m, cmd = m.setStatus("copied to clipboard", styles.ToneSuccess)
		}
		cmds = append(cmds, cmd)

	case messages.StatusClearMsg:
		if msg.Gen == m.statusGen {
			m.statusMsg = ""
		}

	case messages.RefreshTickMsg:
		// While paused the tick is let go, which ends the refresh loop until it resumes.
		if msg.Gen != m.refreshGen || m.paused {
			break
		}
		m, cmd = m.refreshPage()
		cmds = append(cmds, cmd)
	}

	switch m.page {
	case streamOverview:
		m, cmd = m.StreamOverviewUpdate(msg)
	case streamDetails:
		m, cmd = m.StreamDetailsUpdate(msg)
	case streamCreate:
		m, cmd = m.StreamCreateUpdate(msg)
	case streamDelete:
		m, cmd = m.StreamDeleteUpdate(msg)
	case streamPurge:
		m, cmd = m.StreamPurgeUpdate(msg)
	case messageDetails:
		m, cmd = m.MessageDetailsUpdate(msg)
	case messagePublish:
		m, cmd = m.MessagePublishUpdate(msg)
	case messageDelete:
		m, cmd = m.MessageDeleteUpdate(msg)
	case consumerDetails:
		m, cmd = m.ConsumerDetailsUpdate(msg)
	case consumerDelete:
		m, cmd = m.ConsumerDeleteUpdate(msg)
	}
	cmds = append(cmds, cmd)
	m, cmd = m.keepSpinning()
	cmds = append(cmds, cmd)
	m, cmd = m.keepClock()
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = m.programName
	v.ForegroundColor = styles.P.Text
	if styles.Paint {
		v.BackgroundColor = styles.P.Base
	}
	return v
}

// tooSmall reports whether the terminal is smaller than the layout needs.
func (m model) tooSmall() bool {
	return m.width > 0 && (m.width < minContentWidth+chromeWidth || m.height < minContentHeight+chromeHeight)
}

// tooSmallView asks for a larger terminal, in place of a layout that would not fit.
func (m model) tooSmallView() string {
	notice := lipgloss.JoinVertical(lipgloss.Center,
		styles.Render(styles.B("terminal too small", styles.ToneWarning)),
		styles.Muted(fmt.Sprintf("%d×%d, needs %d×%d", m.width, m.height, minContentWidth+chromeWidth, minContentHeight+chromeHeight)),
		styles.Faint("ctrl+c to quit"),
	)
	return clip(lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, notice), m.width, m.height)
}

func (m model) render() string {
	if m.tooSmall() {
		return m.tooSmallView()
	}

	c := m.content()
	meta, foot := m.frameMeta()
	mainView := m.renderHeader() + "\n\n" +
		frame(styles.Render(m.breadcrumb()...), meta, foot, c) + "\n" +
		m.renderFooter()

	placed := lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, mainView)
	if m.width > 0 {
		placed = clip(placed, m.width, m.height)
	}
	return placed
}

// content renders what the frame holds: the page, or the error, loading or help in its place.
func (m model) content() string {
	var c string

	switch {
	case m.error != "":
		c = m.ErrorView()
	case m.loading:
		c = m.LoadingView()
	default:
		switch m.page {
		case streamOverview:
			c = m.StreamOverviewView()
		case streamDetails:
			c = m.StreamDetailsView()
		case streamCreate:
			c = m.StreamCreateView()
		case streamDelete:
			c = m.StreamDeleteView()
		case streamPurge:
			c = m.StreamPurgeView()
		case messageDetails:
			c = m.MessageDetailsView()
		case messagePublish:
			c = m.MessagePublishView()
		case messageDelete:
			c = m.MessageDeleteView()
		case consumerDetails:
			c = m.ConsumerDetailsView()
		case consumerDelete:
			c = m.ConsumerDeleteView()
		default:
			c = errNoPageSelected
		}
	}

	if m.showHelp {
		c = m.renderHelpOverlay()
	}

	return c
}

func (m model) LoadingView() string {
	msg := m.loadingMsg
	if msg == "" {
		msg = "loading…"
	}
	return lipgloss.Place(contentWidth, contentHeight, lipgloss.Center, lipgloss.Center,
		styles.Accent(m.spinner.View())+" "+styles.Muted(truncate(styles.Clean(msg), contentWidth-4)))
}

// frameMeta returns what is set into the frame's edges: the active filter on top, and at the
// bottom the refresh clock, the cursor position of the visible table and the connection state.
func (m model) frameMeta() (string, string) {
	var filter string
	var t *dataTable
	switch m.page {
	case streamOverview:
		filter = m.state.streamOverview.filterText
		t = &m.state.streamOverview.table
	case streamDetails:
		d := &m.state.streamDetails
		filter = d.filterText
		t = d.activeTable()
	}
	var meta string
	if filter != "" {
		spans := []styles.Span{styles.S("filter ", styles.ToneFaint)}
		if tsui.HasWildcard(filter) {
			spans = append(spans, styles.Subject(filter, styles.ToneText)...)
		} else {
			spans = append(spans, styles.S(filter, styles.ToneText))
		}
		meta = styles.Render(spans...)
	}
	if m.loading || m.error != "" {
		return meta, m.connectionState()
	}
	var foot []string
	if d := m.state.streamDetails; m.page == streamDetails && d.tab == tabMessages {
		if d.following() {
			foot = append(foot, styles.Render(styles.S("● ", styles.ToneAccent), styles.S("following", styles.ToneMuted)))
		} else {
			foot = append(foot, styles.Faint("follow off"))
		}
	}
	if m.clockVisible() && !m.disconnected() {
		foot = append(foot, styles.Faint(formatRefreshed(time.Since(m.refreshedAt))))
	}
	if t != nil && t.position() != "" {
		foot = append(foot, styles.Faint(t.position()))
	}
	foot = append(foot, m.connectionState())
	return meta, strings.Join(foot, styles.Faint(" · "))
}

func (m model) renderFooter() string {
	var status string
	if m.statusMsg != "" {
		switch m.statusTone {
		case styles.ToneDanger:
			status = styles.Render(styles.S("✗ ", styles.ToneDanger), styles.S(m.statusMsg, styles.ToneDanger))
		case styles.ToneWarning:
			status = styles.Render(styles.S("▲ ", styles.ToneWarning), styles.S(m.statusMsg, styles.ToneWarning))
		default:
			status = styles.Render(styles.S("✓ ", styles.ToneSuccess), styles.S(m.statusMsg, styles.ToneBody))
		}
	}
	if w := lipgloss.Width(status); w > frameWidth/2 {
		status = ansi.Truncate(status, frameWidth/2, "…")
	}
	width := frameWidth - 2 - lipgloss.Width(status) - 2
	left := ansi.Truncate(m.renderFooterLeft(width), width, "…")
	return " " + spread(left, status+" ", frameWidth-1)
}

// renderFooterLeft renders what the footer shows next to the status, in at most width columns.
func (m model) renderFooterLeft(width int) string {
	switch m.page {
	case streamOverview:
		o := m.state.streamOverview
		switch {
		case o.filtering:
			return m.renderFilterBar(o.filterInput.View())
		case len(o.selectedItems) > 0:
			return m.renderSelectionInfo(len(o.selectedItems), "stream")
		}
	case streamDetails:
		d := m.state.streamDetails
		switch {
		case d.jumping:
			return styles.Accent("# ") + d.jumpInput.View() + "  " +
				hints([2]string{"enter", "open"}, [2]string{"esc", "cancel"})
		case d.filtering:
			return m.renderFilterBar(d.filterInput.View())
		case d.tab == tabMessages && len(d.selectedMessages) > 0:
			return m.renderSelectionInfo(len(d.selectedMessages), "message")
		case d.tab == tabConsumers && len(d.selectedConsumers) > 0:
			return m.renderSelectionInfo(len(d.selectedConsumers), "consumer")
		}
	}
	return m.renderShortHelp(width)
}

func (m model) renderSelectionInfo(count int, itemType string) string {
	return styles.Render(styles.S("● ", styles.ToneAccent), styles.B(plural(count, itemType)+" selected", styles.ToneText)) +
		"    " + hints([2]string{"ctrl+d", "delete"}, [2]string{"q", "clear"})
}

func (m model) renderFilterBar(inputView string) string {
	return styles.Accent("/ ") + inputView + "  " +
		hints([2]string{"enter", "apply"}, [2]string{"esc", "clear"})
}

// fitHints renders the hints that fit width. Hints are dropped from the end but for the last two,
// help and back or quit, which stay.
func fitHints(width int, pairs ...[2]string) string {
	pairs = slices.Clone(pairs)
	for len(pairs) > 2 && lipgloss.Width(hints(pairs...)) > width {
		pairs = slices.Delete(pairs, len(pairs)-3, len(pairs)-2)
	}
	return hints(pairs...)
}

func hints(pairs ...[2]string) string {
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		parts[i] = styles.Key(p[0], p[1])
	}
	return strings.Join(parts, styles.Faint("  ·  "))
}

var confirmHelp = [][2]string{{"y/n", "yes/no"}, {"←/→", "choose"}, {"enter", "confirm"}, {"esc", "cancel"}}

var shortHelp = map[page][][2]string{
	streamOverview:  {{"enter", "open"}, {"ctrl+n", "new stream"}, {"ctrl+p", "purge"}, {"ctrl+d", "delete"}, {"/", "filter"}, {"p", "pause"}, {"?", "help"}, {"q", "quit"}},
	streamCreate:    {{"enter", "next"}, {"shift+tab", "previous"}, {"esc", "cancel"}},
	streamDelete:    {{"enter", "delete"}, {"esc", "cancel"}},
	messageDetails:  {{"↑/↓", "scroll"}, {"g/G", "top/bottom"}, {"c", "copy payload"}, {"ctrl+d", "delete"}, {"?", "help"}, {"q", "back"}},
	messagePublish:  {{"tab", "next field"}, {"ctrl+s", "publish"}, {"esc", "cancel"}},
	messageDelete:   confirmHelp,
	consumerDetails: {{"c", "copy name"}, {"r", "refresh"}, {"p", "pause"}, {"ctrl+d", "delete"}, {"?", "help"}, {"q", "back"}},
	consumerDelete:  confirmHelp,
}

var detailsTabHelp = map[detailsTab][][2]string{
	tabMessages:  {{"enter", "open"}, {"space", "select"}, {"ctrl+n", "publish"}, {"ctrl+d", "delete"}, {"#", "go to"}, {"f", "follow"}, {"p", "pause"}, {"c", "copy payload"}, {"tab", "next tab"}, {"/", "filter"}, {"?", "help"}, {"q", "back"}},
	tabConsumers: {{"enter", "open"}, {"space", "select"}, {"ctrl+d", "delete"}, {"c", "copy name"}, {"p", "pause"}, {"tab", "next tab"}, {"/", "filter"}, {"?", "help"}, {"q", "back"}},
	tabSubjects:  {{"enter", "messages"}, {"ctrl+p", "purge"}, {"c", "copy subject"}, {"p", "pause"}, {"tab", "next tab"}, {"/", "filter"}, {"?", "help"}, {"q", "back"}},
}

func (m model) renderShortHelp(width int) string {
	return fitHints(width, m.shortHelp()...)
}

func (m model) shortHelp() [][2]string {
	switch {
	case m.busy:
		// Nothing cancels an action in flight, so no key is offered.
		return nil
	case m.error != "":
		return [][2]string{{"any key", "dismiss"}}
	}
	switch m.page {
	case streamDetails:
		return detailsTabHelp[m.state.streamDetails.tab]
	case streamPurge:
		p := m.state.streamPurge
		switch {
		case p.step == purgeStepType:
			return [][2]string{{"enter", "purge"}, {"esc", "cancel"}}
		case p.focus == purgeFocusSubject:
			return [][2]string{{"tab", "done"}, {"esc", "cancel"}}
		case p.step == purgeStepAsk:
			return append([][2]string{{"tab", "subject filter"}}, confirmHelp...)
		}
		return confirmHelp
	}
	return shortHelp[m.page]
}

// renderHelpOverlay draws the key reference in a card, as roomy as the content area allows.
func (m model) renderHelpOverlay() string {
	var overlay string
	for _, fit := range []struct{ padY, padX, gap int }{{1, 4, 6}, {1, 2, 3}, {0, 2, 3}} {
		overlay = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(styles.P.RuleBold).
			Padding(fit.padY, fit.padX).
			Render(m.renderHelpContent(contentWidth-2-2*fit.padX, fit.gap))
		if lipgloss.Width(overlay) <= contentWidth && lipgloss.Height(overlay) <= contentHeight {
			break
		}
	}
	return lipgloss.Place(contentWidth, contentHeight, lipgloss.Center, lipgloss.Center, overlay)
}

// renderHelpContent lays the help out in three columns, or the subjects under the other two when
// three do not fit width.
func (m model) renderHelpContent(width, gap int) string {
	title := func(s string) string {
		return styles.Fg(styles.ToneAccent).Bold(true).MarginBottom(1).Render(s)
	}
	keyStyle := styles.Fg(styles.ToneText).Bold(true).Width(11)
	row := func(key, desc string) string {
		return keyStyle.Render(key) + styles.Muted(desc)
	}

	navigation := lipgloss.JoinVertical(lipgloss.Left,
		title("navigation"),
		row("↑/k ↓/j", "move"),
		row("g / G", "first / last"),
		row("pgup/pgdn", "page"),
		row("↓ at end", "older messages"),
		row("enter", "open"),
		row("tab/⇧tab", "next / previous"),
		row("y / n", "answer a dialog"),
		row("q", "back, quit"),
		row("esc", "back, clear filter"),
		row("ctrl+c", "quit"),
	)

	actions := lipgloss.JoinVertical(lipgloss.Left,
		title("actions"),
		row("space", "select"),
		row("c", "copy payload or name"),
		row("ctrl+n", "new stream, publish"),
		row("ctrl+d", "delete"),
		row("ctrl+p", "purge"),
		row("r / p", "refresh / pause"),
		row("f", "follow newest"),
		row("#", "go to sequence"),
		row("/", "filter"),
		row("?", "help"),
	)

	wildcard := func(subject, desc string) string {
		return lipgloss.NewStyle().Width(18).Render(styles.Render(styles.Subject(subject, styles.ToneText)...)) + styles.Muted(desc)
	}
	subjects := lipgloss.JoinVertical(lipgloss.Left,
		title("subjects"),
		wildcard("orders.*.created", "one token"),
		wildcard("orders.>", "the rest"),
		"",
		styles.Muted("filters, purges and"),
		styles.Muted("consumer filters take"),
		styles.Muted("wildcards, publishing"),
		styles.Muted("does not."),
	)

	spaced := lipgloss.NewStyle().MarginRight(gap)
	columns := lipgloss.JoinHorizontal(lipgloss.Top, spaced.Render(navigation), spaced.Render(actions), subjects)
	if lipgloss.Width(columns) > width {
		columns = lipgloss.JoinVertical(lipgloss.Left,
			lipgloss.JoinHorizontal(lipgloss.Top, spaced.Render(navigation), actions), "", subjects)
	}

	return lipgloss.JoinVertical(lipgloss.Center, columns, "", styles.Faint("press any key to close"))
}
