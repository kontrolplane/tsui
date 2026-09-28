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
	kind     string // what the payload is and its size, for the section header
	viewport viewport.Model
}

func (m model) MessageDetailsSwitchPage() (model, tea.Cmd) {
	m.error = ""
	d := &m.state.messageDetails
	d.payload = newPayloadText(d.message.Data)
	d.kind = payloadKind(d.message.Data)
	d.viewport = viewport.New(viewport.WithWidth(rightContentWidth), viewport.WithHeight(detailsViewportHeight()))
	d.viewport.SetContent(d.payload.render(rightContentWidth))
	return m.SwitchPage(messageDetails), nil
}

// resizePayload wraps the payload again at the current width, keeping the scroll position.
func (d *messageDetailsState) resizePayload() {
	offset := d.viewport.YOffset()
	d.viewport.SetWidth(rightContentWidth)
	d.viewport.SetHeight(detailsViewportHeight())
	d.viewport.SetContent(d.payload.render(rightContentWidth))
	d.viewport.SetYOffset(offset)
}

func (m model) MessageDetailsUpdate(msg tea.Msg) (model, tea.Cmd) {
	var cmd tea.Cmd
	message := m.state.messageDetails.message

	if keyMsg, ok := msg.(tea.KeyPressMsg); ok {
		switch {
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
			m.state.messageDetails.viewport.GotoTop()
			return m, nil
		case key.Matches(keyMsg, m.keys.Bottom):
			m.state.messageDetails.viewport.GotoBottom()
			return m, nil
		}
	}

	m.state.messageDetails.viewport, cmd = m.state.messageDetails.viewport.Update(msg)
	return m, cmd
}

func (m model) MessageDetailsView() string {
	msg := m.state.messageDetails.message
	stream := m.state.streamDetails.stream

	// The last sequence and the age are left out rather than cut short when the panel is narrow.
	sequence := []styles.Span{styles.S(fmt.Sprintf("#%d", msg.Sequence), styles.ToneText)}
	sequence = appendIfFits(sequence, stream.LastSeq > 0, styles.S(fmt.Sprintf("  of #%d", stream.LastSeq), styles.ToneFaint))
	published := []styles.Span{styles.S(formatTime(msg.Time), styles.ToneBody)}
	published = appendIfFits(published, !msg.Time.IsZero(), styles.S("  "+formatAgo(msg.Time), styles.ToneFaint))

	left := []string{
		panelSection("message", true, leftContentWidth),
		panelRowSpans("stream", styles.S(msg.Stream, styles.ToneText)),
		panelRowSpans("subject", styles.Subject(msg.Subject, styles.ToneText)...),
		panelRowSpans("sequence", sequence...),
		panelRowSpans("published", published...),
		panelRow("size", formatBytes(uint64(len(msg.Data)))),
	}

	left = append(left, "", styles.SectionHeaderWith(styles.Bold("headers"), styles.Faint(strconv.Itoa(len(msg.Header))), leftContentWidth))
	left = append(left, headerRows(msg.Header, contentHeight-len(left))...)

	vp := m.state.messageDetails.viewport
	kind := m.state.messageDetails.kind
	if vp.TotalLineCount() > vp.Height() {
		kind += fmt.Sprintf(" · %d%%", int(vp.ScrollPercent()*100))
	}
	right := lipgloss.JoinVertical(lipgloss.Left,
		styles.SectionHeaderWith(styles.Bold("payload"), styles.Faint(kind), rightContentWidth),
		"",
		vp.View(),
	)

	return splitPanels(lipgloss.JoinVertical(lipgloss.Left, left...), right)
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
// past them counted. Labels take the width the longest name needs, up to half the panel, so long
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
