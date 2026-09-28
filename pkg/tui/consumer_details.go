package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kontrolplane/tsui/pkg/tsui"
	"github.com/kontrolplane/tsui/pkg/tui/commands"
	"github.com/kontrolplane/tsui/pkg/tui/styles"
)

type consumerDetailsState struct {
	consumer tsui.Consumer
}

func (m model) ConsumerDetailsSwitchPage() (model, tea.Cmd) {
	m.error = ""
	m = m.SwitchPage(consumerDetails)
	return m.scheduleRefresh()
}

// refreshConsumer reloads the consumer shown in the details view, and its stream so the sequence
// rails follow new messages.
func (m model) refreshConsumer() tea.Cmd {
	stream := m.state.streamDetails.stream.Name
	return tea.Batch(
		commands.RefreshStream(m.context, m.js, stream),
		commands.LoadConsumer(m.context, m.js, stream, m.state.consumerDetails.consumer.Name),
	)
}

// syncConsumerDetails picks up refreshed state for the consumer shown in the details view. It
// reports false when the consumer is gone, deleted elsewhere or an ephemeral that expired.
func (m model) syncConsumerDetails() (model, bool) {
	name := m.state.consumerDetails.consumer.Name
	for _, c := range m.state.streamDetails.consumers {
		if c.Name == name {
			m.state.consumerDetails.consumer = c
			return m, true
		}
	}
	return m, false
}

func (m model) ConsumerDetailsUpdate(msg tea.Msg) (model, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}

	c := m.state.consumerDetails.consumer
	switch {
	case key.Matches(keyMsg, m.keys.CopyToClipboard):
		return m, commands.CopyToClipboard(c.Name)
	case key.Matches(keyMsg, m.keys.Refresh):
		if m.disconnected() {
			return m.setStatus(offlineStatus, styles.ToneWarning)
		}
		return m, m.refreshConsumer()
	case key.Matches(keyMsg, m.keys.Delete):
		m.state.consumerDelete.consumers = []string{c.Name}
		m.state.consumerDelete.fromDetails = true
		return m.ConsumerDeleteSwitchPage()
	case key.Matches(keyMsg, m.keys.Quit, m.keys.Back):
		m.state.streamDetails.tab = tabConsumers
		m = m.focusDetailsTab()
		return m.StreamDetailsGoBack()
	}
	return m, nil
}

// consumerStatus tells whether a consumer is being read: a push consumer while a client is bound
// to it, a pull consumer while a client waits on it or was served in the last minute.
func consumerStatus(c tsui.Consumer) cell {
	switch {
	case c.Paused:
		return text("⏸ paused", styles.ToneWarning)
	case c.Push && c.PushBound, !c.Push && c.NumWaiting > 0,
		!c.LastActive.IsZero() && time.Since(c.LastActive) < time.Minute:
		return cell{styles.S("● ", styles.ToneSuccess), styles.S("active", styles.ToneBody)}
	case c.Push:
		return cell{styles.S("○ ", styles.ToneFaint), styles.S("idle, no client bound", styles.ToneMuted)}
	}
	return cell{styles.S("○ ", styles.ToneFaint), styles.S("idle, no client pulling", styles.ToneMuted)}
}

func formatOptionalInt(n int) string {
	if n <= 0 {
		return "unlimited"
	}
	return strconv.Itoa(n)
}

func (m model) ConsumerDetailsView() string {
	c := m.state.consumerDetails.consumer
	stream := m.state.streamDetails.stream

	kind := "ephemeral"
	if c.Durable {
		kind = "durable"
	}
	description := c.Description
	if description == "" {
		description = "-"
	}

	left := lipgloss.JoinVertical(lipgloss.Left,
		panelSection("configuration", true, leftContentWidth),
		panelRowSpans("name", styles.B(c.Name, styles.ToneText)),
		panelRowSpans("type", append(cell{styles.S(kind, styles.ToneBody), sep()}, consumerMode(c)...)...),
		panelRowLines("description", description, 2),
		panelRowSpans("filter", consumerFilter(c)...),
		panelRow("deliver policy", c.DeliverPolicy),
		panelRow("ack policy", c.AckPolicy),
		panelRow("ack wait", compactDuration(c.AckWait)),
		panelRow("max deliver", formatOptionalInt(c.MaxDeliver)),
		panelRow("max ack pending", formatOptionalInt(c.MaxAckPending)),
		panelRow("created", formatTime(c.Created)),
	)

	status := consumerStatus(c)

	right := lipgloss.JoinVertical(lipgloss.Left,
		panelSection("state", true, rightContentWidth),
		panelRowSpans("status", status...),
		panelRowSpans("delivery", consumerDelivery(c, stream)...),
		panelRowSpans("pending", count(c.NumPending, styles.ToneWarning)...),
		panelRowSpans("in flight", count(uint64(c.NumAckPending), styles.ToneInfo)...),
		panelRowSpans("redelivered", count(uint64(c.NumRedelivered), styles.ToneWarning)...),
		panelRowSpans("waiting pulls", count(uint64(c.NumWaiting), styles.ToneBody)...),
		panelRowSpans("last active", lastActivity(c.LastActive)...),
		"",
		panelSection("sequence", true, rightContentWidth),
		sequenceRail("stream", stream.FirstSeq, stream.LastSeq, stream.LastSeq, styles.ToneFaint),
		sequenceRail("delivered", stream.FirstSeq, stream.LastSeq, c.Delivered, styles.ToneInfo),
		sequenceRail("ack floor", stream.FirstSeq, stream.LastSeq, c.AckFloor, styles.ToneAccent),
		"",
		styles.Fg(styles.ToneFaint).PaddingLeft(panelLabelWidth).Width(rightContentWidth).Render("between ack floor and delivered: in flight"),
	)

	return splitPanels(left, right)
}

// sequenceRail draws where a sequence sits within the stream, from its first to its last message.
// Every rail takes the width the stream's own needs for its numbers, so the rails line up. Numbers
// too long to leave the bar its room are shortened.
func sequenceRail(label string, first, last, to uint64, tone styles.Tone) string {
	seq := func(n uint64) string { return "#" + strconv.FormatUint(n, 10) }
	span := fmt.Sprintf(" %s … %s", seq(first), seq(last))
	if ansi.StringWidth(span) > panelValueWidth-8 {
		seq = func(n uint64) string { return "#" + compactCount(n) }
		span = fmt.Sprintf(" %s … %s", seq(first), seq(last))
	}
	width := max(8, min(24, panelValueWidth-ansi.StringWidth(span)))
	if label == "stream" {
		spans := []styles.Span{styles.S(strings.Repeat("━", width), styles.ToneMuted)}
		return panelRowSpans(label, append(spans, styles.S(span, styles.ToneMuted))...)
	}
	spans := styles.Bar(sequencePosition(to, first, last), width, tone)
	return panelRowSpans(label, append(spans, styles.S(" "+seq(to), styles.ToneBody))...)
}
