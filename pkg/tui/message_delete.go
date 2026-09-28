package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/kontrolplane/tsui/pkg/tui/commands"
	"github.com/kontrolplane/tsui/pkg/tui/styles"
)

type messageDeleteState struct {
	sequences   []uint64
	selected    int  // 0 = no, 1 = yes
	fromDetails bool // true when triggered from the message details view
}

func (m model) MessageDeleteSwitchPage() (model, tea.Cmd) {
	m.error = ""
	m.state.messageDelete.selected = 0
	return m.SwitchPage(messageDelete), nil
}

func (m model) messageDeleteGoBack() (model, tea.Cmd) {
	if m.state.messageDelete.fromDetails {
		m.error = ""
		return m.SwitchPage(messageDetails), nil
	}
	return m.StreamDetailsGoBack()
}

func (m model) MessageDeleteView() string {
	d := m.state.messageDelete
	names := make([]string, len(d.sequences))
	for i, seq := range d.sequences {
		names[i] = fmt.Sprintf("#%d", seq)
	}

	target := styles.B(plural(len(names), "message"), styles.ToneText)
	if len(names) == 1 {
		target = styles.B("message "+names[0], styles.ToneText)
	}
	body := listBody(names, true)
	note := "the sequence is removed, consumers that have not received it yet will skip it."
	if len(names) > 1 {
		note = "the sequences are removed, consumers that have not received them yet will skip them."
	}
	body = append(body, "", styles.Faint(note))
	return confirmDialog(
		"delete message",
		[]styles.Span{styles.S("delete ", styles.ToneBody), target, styles.S(" from ", styles.ToneBody),
			styles.B(dialogName(m.state.streamDetails.stream.Name), styles.ToneText), styles.S("?", styles.ToneBody)},
		body,
		d.selected,
	)
}

func (m model) MessageDeleteUpdate(msg tea.Msg) (model, tea.Cmd) {
	d := &m.state.messageDelete
	switch m.confirmKey(msg, &d.selected) {
	case confirmNo:
		return m.messageDeleteGoBack()
	case confirmYes:
		if m.disconnected() {
			m.error = notConnected("deleted")
			return m, nil
		}
		m.busy, m.loading, m.loadingMsg = true, true, deletingMsg(len(d.sequences), "message")
		return m, commands.DeleteMessages(m.context, m.js, m.state.streamDetails.stream.Name, d.sequences)
	}
	return m, nil
}
