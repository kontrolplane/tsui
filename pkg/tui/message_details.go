package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/nats-io/nats.go"

	"github.com/kontrolplane/tsui/pkg/tsui"
	"github.com/kontrolplane/tsui/pkg/tui/commands"
	"github.com/kontrolplane/tsui/pkg/tui/styles"
)

func detailsViewportHeight() int { return contentHeight - 3 } // Account for header and margin

type messageDetailsState struct {
	message  tsui.Message
	payload  payloadText
	kind     string         // what the payload is and its size, for the section header
	viewport viewport.Model // the payload
	fields   viewport.Model // the message and its headers, which can be more than fit
	onFields bool           // the keys scroll the fields rather than the payload
}

func (m model) MessageDetailsSwitchPage() (model, tea.Cmd) {
	m.error = ""
	d := &m.state.messageDetails
	d.payload = newPayloadText(d.message.Data)
	d.kind = payloadKind(d.message.Data)
	d.viewport = viewport.New(viewport.WithWidth(rightContentWidth), viewport.WithHeight(detailsViewportHeight()))
	d.viewport.SetContent(d.payload.render(rightContentWidth))
	d.fields = viewport.New(viewport.WithWidth(leftContentWidth), viewport.WithHeight(contentHeight))
	d.fields.SetContent(m.messageFields())
	d.onFields = false
	return m.SwitchPage(messageDetails), nil
}

// resizeMessageDetails wraps the payload and the fields again at the current width, keeping the
// scroll positions.
func (m *model) resizeMessageDetails() {
	d := &m.state.messageDetails
	offset := d.viewport.YOffset()
	d.viewport.SetWidth(rightContentWidth)
	d.viewport.SetHeight(detailsViewportHeight())
	d.viewport.SetContent(d.payload.render(rightContentWidth))
	d.viewport.SetYOffset(offset)
	offset = d.fields.YOffset()
	d.fields.SetWidth(leftContentWidth)
	d.fields.SetHeight(contentHeight)
	d.fields.SetContent(m.messageFields())
	d.fields.SetYOffset(offset)
	if !d.fieldsOverflow() {
		d.onFields = false
	}
}

// fieldsOverflow reports whether the fields are more than the panel shows, so it scrolls.
func (d messageDetailsState) fieldsOverflow() bool {
	return d.fields.TotalLineCount() > d.fields.Height()
}

func (m model) MessageDetailsUpdate(msg tea.Msg) (model, tea.Cmd) {
	var cmd tea.Cmd
	d := &m.state.messageDetails
	message := d.message
	// The fields hold the age of the message, which changes while it is open.
	d.fields.SetContent(m.messageFields())
	scrolled := &d.viewport
	if d.onFields {
		scrolled = &d.fields
	}

	if keyMsg, ok := msg.(tea.KeyPressMsg); ok {
		switch {
		case key.Matches(keyMsg, m.keys.SwitchTab, m.keys.PrevTab):
			d.onFields = !d.onFields && d.fieldsOverflow()
			return m, nil
		case key.Matches(keyMsg, m.keys.CopyToClipboard):
			return m, commands.CopyToClipboard(string(message.Data))
		case key.Matches(keyMsg, m.keys.Delete):
			if m.state.streamDetails.stream.DenyDelete {
				m.error = fmt.Sprintf("stream %s does not allow deleting messages (deny_delete).", message.Stream)
				return m, nil
			}
			m.state.messageDelete.sequences = []uint64{message.Sequence}
			m.state.messageDelete.fromDetails = true
			return m.MessageDeleteSwitchPage()
		case key.Matches(keyMsg, m.keys.Quit, m.keys.Back):
			return m.StreamDetailsGoBack()
		case key.Matches(keyMsg, m.keys.Top):
			scrolled.GotoTop()
			return m, nil
		case key.Matches(keyMsg, m.keys.Bottom):
			scrolled.GotoBottom()
			return m, nil
		}
	}

	*scrolled, cmd = scrolled.Update(msg)
	return m, cmd
}

// messageFields renders the left panel: where the message is stored, when and how large, and all
// of its headers.
func (m model) messageFields() string {
	d := m.state.messageDetails
	msg := d.message
	stream := m.state.streamDetails.stream

	// The last sequence and the age are left out rather than cut short when the panel is narrow.
	sequence := []styles.Span{styles.S(fmt.Sprintf("#%d", msg.Sequence), styles.ToneText)}
	sequence = appendIfFits(sequence, stream.LastSeq > 0, styles.S(fmt.Sprintf("  of #%d", stream.LastSeq), styles.ToneFaint))
	published := []styles.Span{styles.S(formatTime(msg.Time), styles.ToneBody)}
	published = appendIfFits(published, !msg.Time.IsZero(), styles.S("  "+formatAgo(msg.Time), styles.ToneFaint))

	title := styles.Fg(styles.ToneText).Bold(true)
	if d.onFields {
		title = styles.Fg(styles.ToneAccent).Bold(true)
	}
	meta := ""
	if d.fieldsOverflow() {
		meta = styles.Faint("tab scrolls")
		if d.onFields {
			meta = styles.Faint(fmt.Sprintf("%d%%", int(d.fields.ScrollPercent()*100)))
		}
	}
	left := []string{
		styles.SectionHeaderWith(title.Render("message"), meta, leftContentWidth),
		panelRowSpans("stream", styles.S(msg.Stream, styles.ToneText)),
		panelRowSpans("subject", styles.Subject(msg.Subject, styles.ToneText)...),
		panelRowSpans("sequence", sequence...),
		panelRowSpans("published", published...),
		panelRow("size", formatBytes(uint64(len(msg.Data)))),
	}

	left = append(left, "", styles.SectionHeaderWith(styles.Bold("headers"), styles.Faint(strconv.Itoa(len(msg.Header))), leftContentWidth))
	left = append(left, headerRows(msg.Header, len(msg.Header))...)
	return strings.Join(left, "\n")
}

func (m model) MessageDetailsView() string {
	d := m.state.messageDetails
	fields := d.fields
	fields.SetContent(m.messageFields())

	vp := d.viewport
	kind := d.kind
	if vp.TotalLineCount() > vp.Height() {
		kind += fmt.Sprintf(" · %d%%", int(vp.ScrollPercent()*100))
	}
	title := styles.Fg(styles.ToneText).Bold(true)
	if !d.onFields && d.fieldsOverflow() {
		title = styles.Fg(styles.ToneAccent).Bold(true)
	}
	right := lipgloss.JoinVertical(lipgloss.Left,
		styles.SectionHeaderWith(title.Render("payload"), styles.Faint(kind), rightContentWidth),
		"",
		vp.View(),
	)

	return splitPanels(fields.View(), right)
}

// appendIfFits appends extra to a panel value when ok and the value still fits the panel.
func appendIfFits(value []styles.Span, ok bool, extra styles.Span) []styles.Span {
	w := ansi.StringWidth(extra.Text)
	for _, s := range value {
		w += ansi.StringWidth(s.Text)
	}
	if !ok || w > panelValueWidth {
		return value
	}
	return append(value, extra)
}

// headerRows lists the headers of a message on one line each, in at most rows lines, with the ones
// past them counted. The details view scrolls, so it lists them all. Labels take the width the longest name needs, up to half the panel, so long
// names that share a prefix stay apart.
func headerRows(header nats.Header, rows int) []string {
	if len(header) == 0 {
		return []string{panelRowSpans("", styles.S("no headers", styles.ToneFaint))}
	}
	names := make([]string, 0, len(header))
	labelWidth := panelLabelWidth - 2
	for name := range header {
		names = append(names, name)
		labelWidth = max(labelWidth, min(ansi.StringWidth(styles.Clean(name)), leftContentWidth/2))
	}
	sort.Strings(names)

	shown := names
	if rows = max(rows, 1); len(names) > rows {
		shown = names[:rows-1]
	}
	lines := make([]string, 0, len(shown)+1)
	for _, name := range shown {
		lines = append(lines, headerRow(name, strings.Join(header[name], ", "), labelWidth))
	}
	if more := len(names) - len(shown); more > 0 {
		lines = append(lines, strings.Repeat(" ", labelWidth+2)+styles.Faint(fmt.Sprintf("+%d more headers", more)))
	}
	return lines
}

// headerRow sets the headers NATS itself acts on (Nats-Msg-Id, Nats-Expected-*, Nats-TTL, ...)
// apart from the application's own.
func headerRow(name, value string, labelWidth int) string {
	tone := styles.ToneFaint
	if strings.HasPrefix(strings.ToLower(name), "nats-") {
		tone = styles.ToneAccent
	}
	label := styles.Fg(tone).Width(labelWidth).Align(lipgloss.Right).Render(truncate(styles.Clean(name), labelWidth))
	return label + "  " + renderCell(cell{styles.S(value, styles.ToneBody)}, column{width: leftContentWidth - labelWidth - 2}, nil)
}
