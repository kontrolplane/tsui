package tui

import (
	"fmt"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/kontrolplane/tsui/pkg/tui/commands"
	"github.com/kontrolplane/tsui/pkg/tui/styles"
)

type streamDeleteState struct {
	streams []string
	hidden  int // selected streams the overview filter hides, which are not deleted
	confirm typedConfirm
}

// StreamDeleteSwitchPage asks to type the name of the stream, or how many are deleted, since a
// deleted stream cannot be brought back.
func (m model) StreamDeleteSwitchPage() (model, tea.Cmd) {
	m.error = ""
	d := &m.state.streamDelete
	want := fmt.Sprintf("delete %d streams", len(d.streams))
	if len(d.streams) == 1 {
		want = d.streams[0]
	}
	var cmd tea.Cmd
	d.confirm, cmd = newTypedConfirm(want)
	return m.SwitchPage(streamDelete), cmd
}

// StreamOverviewGoBack returns to the stream overview as it was left and refreshes it in the background.
func (m model) StreamOverviewGoBack() (model, tea.Cmd) {
	m.error = ""
	return m.SwitchPage(streamOverview), commands.LoadStreams(m.context, m.js)
}

func (m model) StreamDeleteView() string {
	d := m.state.streamDelete

	target, their := styles.B(plural(len(d.streams), "stream"), styles.ToneText), "their"
	if len(d.streams) == 1 {
		target, their = styles.B(dialogName(d.streams[0]), styles.ToneText), "its"
	}
	body := []string{styles.Render(styles.S("delete ", styles.ToneBody), target, styles.S(" with all of "+their+" messages and consumers?", styles.ToneBody))}
	body = append(body, listBody(d.streams, false)...)
	body = append(body, "", styles.Faint("this cannot be undone, publishers on its subjects will get no responders."))
	if d.hidden > 0 {
		body = append(body, styles.Faint(fmt.Sprintf("%s hidden by the filter %s not included.", plural(d.hidden, "selected stream"), isAre(d.hidden))))
	}
	body = append(body, "", d.confirm.view())
	return dialog("delete stream", styles.ToneDanger, body...)
}

func isAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

func (m model) StreamDeleteUpdate(msg tea.Msg) (model, tea.Cmd) {
	d := &m.state.streamDelete
	if keyMsg, ok := msg.(tea.KeyPressMsg); ok {
		switch {
		case key.Matches(keyMsg, m.keys.Back):
			return m.StreamOverviewGoBack()
		case key.Matches(keyMsg, m.keys.View):
			if !d.confirm.ok() {
				return m, nil
			}
			if m.disconnected() {
				m.error = notConnected("deleted")
				return m, nil
			}
			m.busy, m.loading, m.loadingMsg = true, true, deletingMsg(len(d.streams), "stream")
			return m, commands.DeleteStreams(m.context, m.js, d.streams)
		}
	}
	var cmd tea.Cmd
	d.confirm, cmd = d.confirm.update(msg)
	return m, cmd
}
