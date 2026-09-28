package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/kontrolplane/tsui/pkg/tui/commands"
	"github.com/kontrolplane/tsui/pkg/tui/styles"
)

type consumerDeleteState struct {
	consumers   []string
	selected    int  // 0 = no, 1 = yes
	fromDetails bool // true when triggered from the consumer details view
}

func (m model) ConsumerDeleteSwitchPage() (model, tea.Cmd) {
	m.error = ""
	m.state.consumerDelete.selected = 0
	return m.SwitchPage(consumerDelete), nil
}

func (m model) consumerDeleteGoBack() (model, tea.Cmd) {
	if m.state.consumerDelete.fromDetails {
		return m.ConsumerDetailsSwitchPage()
	}
	return m.StreamDetailsGoBack()
}

func (m model) ConsumerDeleteView() string {
	d := m.state.consumerDelete

	target := styles.B(plural(len(d.consumers), "consumer"), styles.ToneText)
	if len(d.consumers) == 1 {
		target = styles.B(dialogName(d.consumers[0]), styles.ToneText)
	}
	body := listBody(d.consumers, false)
	body = append(body, "", styles.Faint("its delivery state and ack floor are lost. on an interest stream, messages only it held are removed."))
	return confirmDialog(
		"delete consumer",
		[]styles.Span{styles.S("delete ", styles.ToneBody), target, styles.S(" from ", styles.ToneBody),
			styles.B(dialogName(m.state.streamDetails.stream.Name), styles.ToneText), styles.S("?", styles.ToneBody)},
		body,
		d.selected,
	)
}

func (m model) ConsumerDeleteUpdate(msg tea.Msg) (model, tea.Cmd) {
	d := &m.state.consumerDelete
	switch m.confirmKey(msg, &d.selected) {
	case confirmNo:
		return m.consumerDeleteGoBack()
	case confirmYes:
		if m.disconnected() {
			m.error = notConnected("deleted")
			return m, nil
		}
		m.busy, m.loading, m.loadingMsg = true, true, deletingMsg(len(d.consumers), "consumer")
		return m, commands.DeleteConsumers(m.context, m.js, m.state.streamDetails.stream.Name, d.consumers)
	}
	return m, nil
}
