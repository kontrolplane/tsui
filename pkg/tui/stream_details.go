package tui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kontrolplane/tsui/pkg/tsui"
	"github.com/kontrolplane/tsui/pkg/tui/commands"
	"github.com/kontrolplane/tsui/pkg/tui/styles"
)

type detailsTab int

const (
	tabMessages detailsTab = iota
	tabConsumers
	tabSubjects
	tabCount
)

type streamDetailsState struct {
	stream          tsui.Stream
	attributesTable string
	tab             detailsTab

	messages         []tsui.Message
	messagesLoaded   bool
	messagesStale    bool // the last reload failed, the next refresh retries it
	messagesTable    dataTable
	selectedMessage  int
	selectedMessages map[uint64]bool // sequences selected for bulk operations
	subject          string          // the messages are those on this subject filter, "" for the whole stream
	olderNext        uint64          // the next older page holds messages below this sequence, 0 when none remain
	paged            bool            // older pages were added below the newest messages
	loadingOlder     bool
	fillTo           int  // older pages load on their own until this many messages are loaded
	autoPages        int  // pages loaded on their own towards fillTo
	follow           bool // the top row stays on the newest message as new ones arrive

	consumers         []tsui.Consumer
	consumersLoaded   bool
	consumersTable    dataTable
	selectedConsumer  int
	selectedConsumers map[string]bool // consumer names selected for bulk operations

	subjects        []tsui.SubjectCount
	subjectsLoaded  bool
	subjectsTable   dataTable
	selectedSubject int

	filtering   bool
	filterInput textinput.Model
	filterText  string
	drillFrom   string // subject opened from the subjects tab, q returns to it

	jumping   bool
	jumpInput textinput.Model
}

// following reports whether the cursor stays on the newest message as new ones arrive: follow is
// on and the cursor is on the top row.
func (d *streamDetailsState) following() bool {
	return d.follow && d.selectedMessage == 0
}

// activeTable returns the table of the open tab.
func (d *streamDetailsState) activeTable() *dataTable {
	switch d.tab {
	case tabConsumers:
		return &d.consumersTable
	case tabSubjects:
		return &d.subjectsTable
	}
	return &d.messagesTable
}

var messageColumns = []column{
	{title: "sequence", width: 13, right: true},
	{title: "subject", width: 34, grow: 1},
	{title: "payload", width: 56, grow: 2},
	{title: "hdrs", width: 4, right: true},
	{title: "published", width: 12, right: true},
	{title: "size", width: 9, right: true},
}

var consumerColumns = []column{
	{title: "consumer", width: 20, grow: 1, min: 12},
	{title: "filter", width: 20, grow: 1, min: 10},
	{title: "mode", width: 12, drop: 1},
	{title: "ack", width: 8, drop: 2},
	{title: "delivery", width: 13},
	{title: "pending", width: 9, right: true},
	{title: "in flight", width: 9, right: true},
	{title: "redelivered", width: 11, right: true, drop: 3},
	{title: "waiting", width: 7, right: true, drop: 4},
	{title: "active", width: 10, right: true},
}

var subjectColumns = []column{
	{title: "subject", width: 64, grow: 1},
	{title: "messages", width: 12, right: true},
	{title: "share", width: 31},
}

const attributeLabelWidth = 16

func attributeColumn(width int, rows []attribute) string {
	label := styles.Fg(styles.ToneFaint).Width(attributeLabelWidth)
	lines := make([]string, len(rows))
	for i, r := range rows {
		lines[i] = label.Render(r.label) + renderCell(r.value, column{width: width - attributeLabelWidth}, nil)
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}

type attribute struct {
	label string
	value cell
}

func sep() styles.Span { return styles.S(" · ", styles.ToneFaint) }

func renderAttributesTable(s tsui.Stream) string {
	storage := cell{styles.S(s.Storage, styles.ToneBody), sep(), styles.S(fmt.Sprintf("R%d", max(s.Replicas, 1)), styles.ToneBody)}
	if s.Replicas > 1 && s.Leader != "" {
		storage = append(storage, sep(), styles.S("leader ", styles.ToneFaint), styles.S(s.Leader, styles.ToneBody))
	}

	var options cell
	for _, o := range []struct {
		on   bool
		name string
		tone styles.Tone
	}{
		{s.AllowDirect, "direct get", styles.ToneBody},
		{s.DenyDelete, "deny delete", styles.ToneWarning},
		{s.DenyPurge, "deny purge", styles.ToneWarning},
		{s.Sealed, "sealed", styles.ToneWarning},
	} {
		if !o.on {
			continue
		}
		if len(options) > 0 {
			options = append(options, sep())
		}
		options = append(options, styles.S(o.name, o.tone))
	}
	if len(options) == 0 {
		options = text("-", styles.ToneFaint)
	}

	limit := func(label, value string) cell {
		tone := styles.ToneBody
		if value == "unlimited" {
			value, tone = "∞", styles.ToneFaint
		}
		return cell{styles.S(label+" ", styles.ToneFaint), styles.S(value, tone)}
	}
	limits := slices.Concat(
		limit("msgs", formatLimit(s.MaxMsgs, false)), cell{sep()},
		limit("bytes", formatLimit(s.MaxBytes, true)), cell{sep()},
		limit("age", formatDuration(s.MaxAge)), cell{sep()},
		limit("per subject", formatLimit(s.MaxMsgsPerSubject, false)),
	)

	leftWidth := (contentWidth - 6) * 53 / 100
	left := attributeColumn(leftWidth, []attribute{
		{"subjects", streamSubjectSpans(s)},
		{"storage", storage},
		{"retention", cell{styles.S(s.Retention, styles.ToneBody), sep(), styles.S("discard ", styles.ToneFaint), styles.S(s.Discard, styles.ToneBody)}},
		{"limits", limits},
		{"options", options},
	})

	right := attributeColumn(contentWidth-6-leftWidth, []attribute{
		{"messages", cell{styles.B(formatCount(s.Messages), styles.ToneText), sep(), styles.S(formatBytes(s.Bytes), styles.ToneBody)}},
		{"first", seqTime(s.FirstSeq, s.FirstTime, s.Messages)},
		{"last", seqTime(s.LastSeq, s.LastTime, s.Messages)},
		{"subjects", cell{styles.S(formatCount(s.NumSubjects)+" unique", styles.ToneBody), sep(), styles.S(formatCount(uint64(s.NumDeleted))+" deleted", styles.ToneFaint)}},
		{"created", cell{styles.S(formatTime(s.Created), styles.ToneBody)}},
	})

	return lipgloss.JoinHorizontal(lipgloss.Top, left, "  ", right)
}

func seqTime(seq uint64, t time.Time, msgs uint64) cell {
	if msgs == 0 {
		return text("-", styles.ToneFaint)
	}
	c := cell{styles.S(fmt.Sprintf("#%d", seq), styles.ToneText)}
	if t.IsZero() {
		return c
	}
	return append(c, sep(), styles.S(formatTime(t), styles.ToneBody), styles.S("  "+formatAgo(t), styles.ToneFaint))
}

func (m model) StreamDetailsSwitchPage() (model, tea.Cmd) {
	m.error = ""
	m = m.SwitchPage(streamDetails)
	m.loading = true
	m.loadingMsg = "loading stream details…"

	d := &m.state.streamDetails
	d.attributesTable = ""
	d.tab = tabMessages
	d.selectedMessages = make(map[uint64]bool)
	d.messagesTable = newDataTable(messageColumns, contentWidth-4, m.getDetailsTableHeight())
	d.consumers = nil
	d.consumersLoaded = false
	d.selectedConsumer = 0
	d.selectedConsumers = make(map[string]bool)
	d.consumersTable = newDataTable(consumerColumns, contentWidth-4, m.getDetailsTableHeight())
	d.subjects = nil
	d.subjectsLoaded = false
	d.selectedSubject = 0
	d.subjectsTable = newDataTable(subjectColumns, contentWidth-4, m.getDetailsTableHeight())
	d.filtering = false
	d.filterText = ""
	d.filterInput.SetValue("")
	d.drillFrom = ""
	d.jumping = false
	m = m.resetMessages("").focusDetailsTab()
	return m.loadStreamDetails()
}

// loadStreamDetails loads the stream, its messages and its consumers, and its subjects when they
// are shown.
func (m model) loadStreamDetails() (model, tea.Cmd) {
	m, load := m.loadMessages()
	d := m.state.streamDetails
	m.consGen++
	cmd := commands.LoadStreamDetails(m.context, m.js, d.stream.Name, load, m.consGen)
	if d.tab == tabSubjects {
		cmd = tea.Batch(cmd, m.loadSubjects())
	}
	return m, cmd
}

// StreamDetailsGoBack returns to the stream details and refreshes them in the background. Their
// messages are only reloaded when the stream changed.
func (m model) StreamDetailsGoBack() (model, tea.Cmd) {
	m.error = ""
	m = m.SwitchPage(streamDetails)
	m.consGen++
	return m, commands.RefreshStreamDetails(m.context, m.js, m.state.streamDetails.stream.Name, m.consGen)
}

func (m model) focusDetailsTab() model {
	d := &m.state.streamDetails
	d.messagesTable.Blur()
	d.consumersTable.Blur()
	d.subjectsTable.Blur()
	switch d.tab {
	case tabMessages:
		d.messagesTable.Focus()
	case tabConsumers:
		d.consumersTable.Focus()
	case tabSubjects:
		d.subjectsTable.Focus()
	}
	return m
}

func (m model) loadSubjects() tea.Cmd {
	d := m.state.streamDetails
	if d.stream.NumSubjects > tsui.MaxListedSubjects {
		return nil
	}
	return commands.LoadSubjects(m.context, m.js, d.stream.Name)
}

func messageMatches(msg tsui.Message, filter string) bool {
	if tsui.HasWildcard(filter) {
		return tsui.SubjectMatches(filter, msg.Subject)
	}
	filter = strings.ToLower(filter)
	return strings.Contains(strings.ToLower(msg.Subject), filter) ||
		strings.Contains(strings.ToLower(string(msg.Data)), filter) ||
		strconv.FormatUint(msg.Sequence, 10) == filter
}

// consumerMatches reports whether the name or a filter subject of a consumer contains filter, or,
// for a filter naming subjects, whether the consumer receives messages on subjects it matches. A
// consumer without filter subjects receives them all.
func consumerMatches(c tsui.Consumer, filter string, subject bool) bool {
	if subject {
		if len(c.FilterSubjects) == 0 {
			return true
		}
		if slices.ContainsFunc(c.FilterSubjects, func(f string) bool { return tsui.SubjectsOverlap(f, filter) }) {
			return true
		}
		return strings.Contains(strings.ToLower(c.Name), strings.ToLower(filter))
	}
	filter = strings.ToLower(filter)
	if strings.Contains(strings.ToLower(c.Name), filter) {
		return true
	}
	return slices.ContainsFunc(c.FilterSubjects, func(f string) bool { return strings.Contains(strings.ToLower(f), filter) })
}

func subjectFilterMatches(subject, filter string) bool {
	if tsui.HasWildcard(filter) {
		return tsui.SubjectMatches(filter, subject)
	}
	return strings.Contains(strings.ToLower(subject), strings.ToLower(filter))
}

func (m model) getFilteredMessages() []tsui.Message {
	d := m.state.streamDetails
	return filterBy(d.messages, d.filterText, messageMatches)
}

func (m model) getFilteredSubjects() []tsui.SubjectCount {
	d := m.state.streamDetails
	return filterBy(d.subjects, d.filterText, func(s tsui.SubjectCount, f string) bool { return subjectFilterMatches(s.Subject, f) })
}

func (m model) getFilteredConsumers() []tsui.Consumer {
	d := m.state.streamDetails
	subject := m.subjectFilter(d.filterText)
	return filterBy(d.consumers, d.filterText, func(c tsui.Consumer, f string) bool { return consumerMatches(c, f, subject) })
}

// setDetailsFilter applies filter to every tab and moves their cursors back to the top.
func (m model) setDetailsFilter(filter string) model {
	d := &m.state.streamDetails
	d.filterText = filter
	d.filterInput.SetValue(filter)
	d.selectedMessage, d.selectedConsumer, d.selectedSubject = 0, 0, 0
	return m.updateDetailsTables()
}

func (m model) updateMessagesTable() model {
	d := &m.state.streamDetails
	var rows []tableRow
	for _, msg := range m.getFilteredMessages() {
		seq := cell{styles.S(strconv.FormatUint(msg.Sequence, 10), styles.ToneMuted)}
		if d.selectedMessages[msg.Sequence] {
			seq = cell{styles.S("● ", styles.ToneAccent), styles.S(strconv.FormatUint(msg.Sequence, 10), styles.ToneText)}
		}
		hdrs := text("-", styles.ToneFaint)
		if n := len(msg.Header); n > 0 {
			hdrs = text(strconv.Itoa(n), styles.ToneBody)
		}
		rows = append(rows, tableRow{
			seq,
			styles.Subject(msg.Subject, styles.ToneText),
			payloadPreview(msg.Data),
			hdrs,
			lastActivity(msg.Time),
			text(formatBytes(uint64(len(msg.Data))), styles.ToneMuted),
		})
	}
	d.messagesTable.empty = m.messagesEmpty()
	d.selectedMessage = setRows(&d.messagesTable, rows, d.selectedMessage)
	return m
}

func payloadPreview(data []byte) cell {
	switch {
	case len(data) == 0:
		return text("empty", styles.ToneFaint)
	case !utf8.Valid(data):
		return text(fmt.Sprintf("<binary, %s>", formatBytes(uint64(len(data)))), styles.ToneFaint)
	default:
		// JSON and logfmt keep their keys back, so the values are what the column reads as.
		return tint(styles.Clean(preview(data, 200)))
	}
}

// consumerDelivery shows where a consumer's delivered sequence sits in the stream. Position is
// used rather than a message count because a filtered consumer never sees the whole stream.
func consumerDelivery(c tsui.Consumer, stream tsui.Stream) cell {
	switch {
	case c.Paused:
		return text("⏸ paused", styles.ToneWarning)
	case c.NumPending == 0 && c.NumAckPending == 0:
		return cell{styles.S("✓ ", styles.ToneSuccess), styles.S("caught up", styles.ToneBody)}
	}
	ratio := sequencePosition(c.Delivered, stream.FirstSeq, stream.LastSeq)
	return append(cell(styles.Bar(ratio, 8, styles.ToneAccent)), styles.S(fmt.Sprintf(" %3.0f%%", ratio*100), styles.ToneMuted))
}

// sequencePosition returns how far seq is between the first and last sequence of a stream.
func sequencePosition(seq, first, last uint64) float64 {
	if seq < first || last == 0 {
		return 0
	}
	if last <= first {
		return 1
	}
	return min(float64(seq-first+1)/float64(last-first+1), 1)
}

func consumerMode(c tsui.Consumer) cell {
	if !c.Push {
		return text("pull", styles.ToneBody)
	}
	if c.PushBound {
		return cell{styles.S("push", styles.ToneBody), styles.S(" bound", styles.ToneFaint)}
	}
	return cell{styles.S("push", styles.ToneBody), styles.S(" unbound", styles.ToneWarning)}
}

func consumerFilter(c tsui.Consumer) cell {
	if len(c.FilterSubjects) == 0 {
		return cell{styles.B(">", styles.ToneAccent), styles.S(" all", styles.ToneFaint)}
	}
	return styles.Subjects(c.FilterSubjects, styles.ToneBody)
}

func (m model) updateConsumersTable() model {
	d := &m.state.streamDetails
	var rows []tableRow
	for _, c := range m.getFilteredConsumers() {
		nameTone := styles.ToneText
		if !c.Durable {
			nameTone = styles.ToneBody
		}
		name := cell{styles.B(c.Name, nameTone)}
		if d.selectedConsumers[c.Name] {
			name = cell{styles.S("● ", styles.ToneAccent), styles.B(c.Name, nameTone)}
		}
		rows = append(rows, tableRow{
			name,
			consumerFilter(c),
			consumerMode(c),
			text(c.AckPolicy, styles.ToneBody),
			consumerDelivery(c, d.stream),
			tableCount(c.NumPending, styles.ToneWarning),
			tableCount(uint64(c.NumAckPending), styles.ToneInfo),
			tableCount(uint64(c.NumRedelivered), styles.ToneWarning),
			tableCount(uint64(c.NumWaiting), styles.ToneBody),
			lastActivity(c.LastActive),
		})
	}
	switch {
	case !d.consumersLoaded:
		d.consumersTable.empty = "loading consumers…"
	case len(d.consumers) == 0:
		d.consumersTable.empty = fmt.Sprintf("%s has no consumers", d.stream.Name)
	default:
		d.consumersTable.empty = "no consumers match the filter"
	}
	d.selectedConsumer = setRows(&d.consumersTable, rows, d.selectedConsumer)
	return m
}

func (m model) updateSubjectsTable() model {
	d := &m.state.streamDetails
	var rows []tableRow
	for _, s := range m.getFilteredSubjects() {
		ratio := float64(s.Messages) / float64(max(d.stream.Messages, 1))
		rows = append(rows, tableRow{
			styles.Subject(s.Subject, styles.ToneText),
			tableCount(s.Messages, styles.ToneText),
			append(cell(styles.Bar(ratio, 24, styles.ToneAccent)), styles.S(fmt.Sprintf(" %5.1f%%", ratio*100), styles.ToneMuted)),
		})
	}
	switch {
	case d.stream.NumSubjects > tsui.MaxListedSubjects:
		d.subjectsTable.empty = fmt.Sprintf("%s holds %s subjects, too many to list", d.stream.Name, formatCount(d.stream.NumSubjects))
	case !d.subjectsLoaded:
		d.subjectsTable.empty = "loading subjects…"
	case len(d.subjects) == 0:
		d.subjectsTable.empty = fmt.Sprintf("no messages in %s%s", d.stream.Name, yet(d.stream))
	default:
		d.subjectsTable.empty = "no subjects match the filter"
	}
	d.selectedSubject = setRows(&d.subjectsTable, rows, d.selectedSubject)
	return m
}

func (m model) updateDetailsTables() model {
	return m.updateMessagesTable().updateConsumersTable().updateSubjectsTable()
}

func (m model) currentSubject() (tsui.SubjectCount, bool) {
	subjects := m.getFilteredSubjects()
	if len(subjects) == 0 {
		return tsui.SubjectCount{}, false
	}
	return subjects[m.state.streamDetails.selectedSubject], true
}

func (m model) currentMessage() (tsui.Message, bool) {
	msgs := m.getFilteredMessages()
	if len(msgs) == 0 {
		return tsui.Message{}, false
	}
	return msgs[m.state.streamDetails.selectedMessage], true
}

func (m model) currentConsumer() (tsui.Consumer, bool) {
	consumers := m.getFilteredConsumers()
	if len(consumers) == 0 {
		return tsui.Consumer{}, false
	}
	return consumers[m.state.streamDetails.selectedConsumer], true
}

// getSelectedMessages returns the selected sequences, or the message under the cursor when nothing is selected.
func (m model) getSelectedMessages() []uint64 {
	d := m.state.streamDetails
	return selectedOr(d.selectedMessages, d.messages, func(msg tsui.Message) uint64 { return msg.Sequence }, m.currentMessage)
}

// getSelectedConsumers returns the selected consumers, or the consumer under the cursor when nothing is selected.
func (m model) getSelectedConsumers() []string {
	d := m.state.streamDetails
	return selectedOr(d.selectedConsumers, d.consumers, func(c tsui.Consumer) string { return c.Name }, m.currentConsumer)
}

func (m model) toggleDetailsSelection() model {
	d := &m.state.streamDetails
	switch d.tab {
	case tabMessages:
		if msg, ok := m.currentMessage(); ok {
			toggle(d.selectedMessages, msg.Sequence)
		}
		return m.updateMessagesTable()
	case tabConsumers:
		if c, ok := m.currentConsumer(); ok {
			toggle(d.selectedConsumers, c.Name)
		}
		return m.updateConsumersTable()
	}
	return m
}

func (m model) detailsSelectionCount() int {
	switch m.state.streamDetails.tab {
	case tabMessages:
		return len(m.state.streamDetails.selectedMessages)
	case tabConsumers:
		return len(m.state.streamDetails.selectedConsumers)
	}
	return 0
}

func (m model) StreamDetailsUpdate(msg tea.Msg) (model, tea.Cmd) {
	var cmd tea.Cmd
	d := &m.state.streamDetails

	if d.jumping {
		return m.jumpUpdate(msg)
	}
	if d.filtering {
		if msg, ok := msg.(tea.KeyPressMsg); ok {
			switch {
			case key.Matches(msg, m.keys.Back):
				d.filtering = false
				d.filterInput.Blur()
				d.drillFrom = ""
				return m.setDetailsFilter("").setMessageSource("")
			case key.Matches(msg, m.keys.View):
				d.filtering = false
				d.filterInput.Blur()
				return m.updateMessagesTable().applyFilterSource()
			}
		}
		d.filterInput, cmd = d.filterInput.Update(msg)
		if d.filterText != d.filterInput.Value() {
			return m.setDetailsFilter(d.filterInput.Value()), cmd
		}
		return m, cmd
	}

	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}

	stream := d.stream
	switch {
	case key.Matches(keyMsg, m.keys.Filter):
		d.filtering = true
		return m, d.filterInput.Focus()
	case key.Matches(keyMsg, m.keys.Jump):
		d.jumping = true
		d.jumpInput.SetValue("")
		return m, d.jumpInput.Focus()
	case d.tab == tabMessages && key.Matches(keyMsg, m.keys.Follow):
		// Not following, whether turned off or moved off the top row, f jumps to the newest
		// message and follows it.
		d.follow = !d.following()
		if d.follow {
			d.selectedMessage = 0
		}
		return m.updateMessagesTable(), nil
	case key.Matches(keyMsg, m.keys.SwitchTab, m.keys.PrevTab):
		if key.Matches(keyMsg, m.keys.PrevTab) {
			d.tab = (d.tab + tabCount - 1) % tabCount
		} else {
			d.tab = (d.tab + 1) % tabCount
		}
		if d.tab == tabSubjects {
			return m.focusDetailsTab().updateSubjectsTable(), m.loadSubjects()
		}
		return m.focusDetailsTab(), nil
	case key.Matches(keyMsg, m.keys.Select):
		return m.toggleDetailsSelection(), nil
	case key.Matches(keyMsg, m.keys.Refresh):
		if m.disconnected() {
			return m.setStatus(offlineStatus, styles.ToneWarning)
		}
		return m.loadStreamDetails()
	case key.Matches(keyMsg, m.keys.View):
		switch d.tab {
		case tabSubjects:
			if s, ok := m.currentSubject(); ok {
				d.tab = tabMessages
				d.drillFrom = s.Subject
				return m.focusDetailsTab().setDetailsFilter(s.Subject).setMessageSource(s.Subject)
			}
		case tabMessages:
			if message, ok := m.currentMessage(); ok {
				m.state.messageDetails.message = message
				return m.MessageDetailsSwitchPage()
			}
		case tabConsumers:
			if c, ok := m.currentConsumer(); ok {
				m.state.consumerDetails.consumer = c
				return m.ConsumerDetailsSwitchPage()
			}
		}
	case key.Matches(keyMsg, m.keys.Create):
		if len(stream.Subjects) == 0 {
			m.error = fmt.Sprintf("stream %s has no subjects of its own, publish to its origin stream instead.", stream.Name)
			return m, nil
		}
		return m.MessagePublishSwitchPage()
	case key.Matches(keyMsg, m.keys.Delete):
		switch d.tab {
		case tabMessages:
			if stream.DenyDelete {
				m.error = fmt.Sprintf("stream %s does not allow deleting messages (deny_delete).", stream.Name)
				return m, nil
			}
			if seqs := m.getSelectedMessages(); len(seqs) > 0 {
				m.state.messageDelete.sequences = seqs
				m.state.messageDelete.fromDetails = false
				return m.MessageDeleteSwitchPage()
			}
		case tabConsumers:
			if names := m.getSelectedConsumers(); len(names) > 0 {
				m.state.consumerDelete.consumers = names
				m.state.consumerDelete.fromDetails = false
				return m.ConsumerDeleteSwitchPage()
			}
		}
	case key.Matches(keyMsg, m.keys.Purge):
		m.state.streamPurge.stream = stream
		m.state.streamPurge.fromOverview = false
		m, cmd = m.StreamPurgeSwitchPage()
		// On the subjects tab the purge starts from the subject under the cursor.
		if s, ok := m.currentSubject(); ok && d.tab == tabSubjects && m.page == streamPurge {
			m.state.streamPurge.subjectInput.SetValue(s.Subject)
		}
		return m, cmd
	case key.Matches(keyMsg, m.keys.CopyToClipboard):
		switch d.tab {
		case tabSubjects:
			if s, ok := m.currentSubject(); ok {
				return m, commands.CopyToClipboard(s.Subject)
			}
		case tabMessages:
			if message, ok := m.currentMessage(); ok {
				return m, commands.CopyToClipboard(string(message.Data))
			}
		case tabConsumers:
			if c, ok := m.currentConsumer(); ok {
				return m, commands.CopyToClipboard(c.Name)
			}
		}
	case key.Matches(keyMsg, m.keys.Quit, m.keys.Back):
		if d.filterText != "" {
			back := d.drillFrom != "" && d.filterText == d.drillFrom
			subject := d.drillFrom
			d.drillFrom = ""
			m = m.setDetailsFilter("")
			if back {
				d := &m.state.streamDetails
				d.tab = tabSubjects
				d.selectedSubject = follow(m.getFilteredSubjects(), 0, func(s tsui.SubjectCount) bool { return s.Subject == subject })
				m = m.focusDetailsTab().updateSubjectsTable()
			}
			return m.setMessageSource("")
		}
		if m.detailsSelectionCount() > 0 {
			if d.tab == tabMessages {
				d.selectedMessages = make(map[uint64]bool)
			} else {
				d.selectedConsumers = make(map[string]bool)
			}
			return m.updateDetailsTables(), nil
		}
		return m.StreamOverviewGoBack()
	default:
		t := d.activeTable()
		*t = t.Update(keyMsg)
		switch d.tab {
		case tabMessages:
			d.selectedMessage = t.Cursor()
			if movesDown(keyMsg) && t.Cursor() >= len(t.rows)-1 {
				return m.moreMessages()
			}
		case tabConsumers:
			d.selectedConsumer = t.Cursor()
		case tabSubjects:
			d.selectedSubject = t.Cursor()
		}
	}

	return m, cmd
}

func (m model) renderTabBar() string {
	d := m.state.streamDetails

	// A tab whose items are not loaded yet goes without a count.
	tab := func(t detailsTab, name string, n int, loaded bool) string {
		num := ""
		if loaded {
			num = formatCount(uint64(n)) + " "
		}
		if t == d.tab {
			bg := styles.P.Selection
			return styles.S(" "+name+" ", styles.ToneText).On(bg) + styles.B(num, styles.ToneAccent).On(bg)
		}
		return styles.Render(styles.S(" "+name+" ", styles.ToneMuted), styles.S(num, styles.ToneFaint))
	}

	// Until the subjects are listed, or when there are too many to list, the stream counts them,
	// though not the ones a filter shows.
	subjects, subjectsKnown := len(d.subjectsTable.rows), d.subjectsLoaded
	if !d.subjectsLoaded || d.stream.NumSubjects > tsui.MaxListedSubjects {
		subjects, subjectsKnown = int(d.stream.NumSubjects), d.attributesTable != "" && d.filterText == ""
	}
	tabs := tab(tabMessages, "messages", len(d.messagesTable.rows), d.messagesLoaded) + " " +
		tab(tabConsumers, "consumers", len(d.consumersTable.rows), d.consumersLoaded) + " " +
		tab(tabSubjects, "subjects", subjects, subjectsKnown)

	var hints []string
	switch d.tab {
	case tabMessages:
		hints = m.messagesHint()
	case tabSubjects:
		hints = []string{"enter shows the messages on a subject", "enter shows its messages"}
	}
	// The tabs always show in full; the hint takes the room left, in the longest wording that fits.
	width := contentWidth - 4
	room := width - lipgloss.Width(tabs) - 4
	for _, hint := range hints {
		if ansi.StringWidth(hint) <= room {
			return spread(tabs, styles.Faint(styles.Clean(hint)), width)
		}
	}
	return spread(tabs, "", width)
}

func (m model) StreamDetailsView() string {
	d := m.state.streamDetails

	attributes := d.attributesTable
	if attributes == "" {
		attributes = styles.Muted("loading stream attributes…")
	}

	tableView := d.activeTable().View()

	return lipgloss.PlaceHorizontal(contentWidth-4, lipgloss.Left, attributes) + "\n\n" +
		m.renderTabBar() + "\n\n" +
		lipgloss.PlaceHorizontal(contentWidth-4, lipgloss.Left, tableView)
}
