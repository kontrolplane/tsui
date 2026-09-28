package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/kontrolplane/tsui/pkg/tsui"
	"github.com/kontrolplane/tsui/pkg/tui/commands"
	"github.com/kontrolplane/tsui/pkg/tui/styles"
)

type streamOverviewState struct {
	selected      int
	streams       []tsui.Stream
	loaded        bool
	table         dataTable
	selectedItems map[string]bool // stream names selected for bulk operations
	filtering     bool
	filterInput   textinput.Model
	filterText    string
}

var streamOverviewColumns = []column{
	{title: "stream", width: 22, grow: 1, min: 16},
	{title: "subjects", width: 40, grow: 2, min: 16},
	{title: "storage", width: 9},
	{title: "retention", width: 9, drop: 2},
	{title: "messages", width: 11, right: true},
	{title: "size", width: 10, right: true},
	{title: "consumers", width: 9, right: true, drop: 1},
	{title: "last message", width: 12, right: true},
}

// streamMatches reports whether the name, description or a subject of a stream contains filter,
// ignoring case, or whether one of its subjects overlaps filter, wildcards included.
func streamMatches(s tsui.Stream, filter string) bool {
	filter = strings.ToLower(filter)
	if strings.Contains(strings.ToLower(s.Name), filter) ||
		strings.Contains(strings.ToLower(s.Description), filter) {
		return true
	}
	for _, subject := range s.Subjects {
		subject = strings.ToLower(subject)
		if strings.Contains(subject, filter) || tsui.SubjectsOverlap(subject, filter) {
			return true
		}
	}
	return false
}

func (m model) getFilteredStreams() []tsui.Stream {
	o := m.state.streamOverview
	return filterBy(o.streams, o.filterText, streamMatches)
}

// streamSubjectSpans renders what a stream listens on: its subjects, or where a mirror or
// sourced stream takes its messages from.
func streamSubjectSpans(s tsui.Stream) cell {
	switch {
	case len(s.Subjects) > 0:
		return styles.Subjects(s.Subjects, styles.ToneBody)
	case s.Mirror != "":
		return cell{styles.S("mirror of ", styles.ToneFaint), styles.S(s.Mirror, styles.ToneBody)}
	case len(s.Sources) > 0:
		return cell{styles.S("sources ", styles.ToneFaint), styles.S(strings.Join(s.Sources, ", "), styles.ToneBody)}
	default:
		return text("-", styles.ToneFaint)
	}
}

// lastActivity renders how long ago something happened, lit up while it is still fresh.
func lastActivity(t time.Time) cell {
	switch {
	case t.IsZero():
		return text("-", styles.ToneFaint)
	case time.Since(t) < time.Minute:
		return cell{styles.S("● ", styles.ToneSuccess), styles.S(formatAgo(t), styles.ToneBody)}
	default:
		return text(formatAgo(t), styles.ToneMuted)
	}
}

func (m model) updateStreamOverviewTable() model {
	o := &m.state.streamOverview
	var rows []tableRow
	for _, s := range m.getFilteredStreams() {
		name := cell{styles.B(s.Name, styles.ToneText)}
		if o.selectedItems[s.Name] {
			name = cell{styles.S("● ", styles.ToneAccent), styles.B(s.Name, styles.ToneText)}
		}
		rows = append(rows, tableRow{
			name,
			streamSubjectSpans(s),
			{styles.S(s.Storage, styles.ToneBody), styles.S(fmt.Sprintf(" R%d", max(s.Replicas, 1)), styles.ToneFaint)},
			text(s.Retention, styles.ToneBody),
			tableCount(s.Messages, styles.ToneText),
			text(formatBytes(s.Bytes), styles.ToneMuted),
			tableCount(uint64(s.Consumers), styles.ToneBody),
			lastActivity(s.LastTime),
		})
	}
	o.table.empty = "no streams yet, press ctrl+n to create one"
	if len(o.streams) > 0 {
		o.table.empty = "no streams match the filter"
	}
	o.selected = setRows(&o.table, rows, o.selected)
	return m
}

func (m model) currentStream() (tsui.Stream, bool) {
	streams := m.getFilteredStreams()
	if len(streams) == 0 {
		return tsui.Stream{}, false
	}
	return streams[m.state.streamOverview.selected], true
}

func (m model) toggleStreamSelection() model {
	if s, ok := m.currentStream(); ok {
		toggle(m.state.streamOverview.selectedItems, s.Name)
	}
	return m.updateStreamOverviewTable()
}

// getSelectedStreams returns the selected streams the filter shows, or the stream under the
// cursor when nothing is selected. hidden counts the selected streams the filter hides, which an
// action on the selection leaves alone.
func (m model) getSelectedStreams() (names []string, hidden int) {
	o := m.state.streamOverview
	names = selectedOr(o.selectedItems, m.getFilteredStreams(), func(s tsui.Stream) string { return s.Name }, m.currentStream)
	if len(o.selectedItems) > 0 {
		hidden = len(o.selectedItems) - len(names)
	}
	return names, hidden
}

// clearFilterOrSelection clears the filter, or else the selection, and reports whether there
// was either to clear.
func (m model) clearFilterOrSelection() (model, bool) {
	o := &m.state.streamOverview
	switch {
	case o.filterText != "":
		o.filterText = ""
		o.filterInput.SetValue("")
		o.selected = 0
	case len(o.selectedItems) > 0:
		o.selectedItems = make(map[string]bool)
	default:
		return m, false
	}
	return m.updateStreamOverviewTable(), true
}

func (m model) StreamOverviewUpdate(msg tea.Msg) (model, tea.Cmd) {
	var cmd tea.Cmd
	o := &m.state.streamOverview

	if o.filtering {
		if msg, ok := msg.(tea.KeyPressMsg); ok {
			switch {
			case key.Matches(msg, m.keys.Back):
				o.filtering = false
				o.filterText = ""
				o.filterInput.SetValue("")
				o.filterInput.Blur()
				return m.updateStreamOverviewTable(), nil
			case key.Matches(msg, m.keys.View):
				o.filtering = false
				o.filterInput.Blur()
				return m, nil
			}
		}
		o.filterInput, cmd = o.filterInput.Update(msg)
		if o.filterText != o.filterInput.Value() {
			o.filterText = o.filterInput.Value()
			o.selected = 0
		}
		return m.updateStreamOverviewTable(), cmd
	}

	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}

	switch {
	case key.Matches(keyMsg, m.keys.Filter):
		o.filtering = true
		return m, o.filterInput.Focus()
	case key.Matches(keyMsg, m.keys.Select):
		return m.toggleStreamSelection(), nil
	case key.Matches(keyMsg, m.keys.Refresh):
		if m.disconnected() {
			return m.setStatus(offlineStatus, styles.ToneWarning)
		}
		return m, commands.LoadStreams(m.context, m.js)
	case key.Matches(keyMsg, m.keys.View):
		if s, ok := m.currentStream(); ok {
			m.state.streamDetails.stream = s
			return m.StreamDetailsSwitchPage()
		}
	case key.Matches(keyMsg, m.keys.Create):
		return m.StreamCreateSwitchPage()
	case key.Matches(keyMsg, m.keys.Purge):
		if s, ok := m.currentStream(); ok {
			m.state.streamPurge.stream = s
			m.state.streamPurge.fromOverview = true
			return m.StreamPurgeSwitchPage()
		}
	case key.Matches(keyMsg, m.keys.Delete):
		names, hidden := m.getSelectedStreams()
		if len(names) == 0 {
			if hidden > 0 {
				return m.setStatus("the selected streams are hidden by the filter", styles.ToneWarning)
			}
			return m, nil
		}
		m.state.streamDelete.streams = names
		m.state.streamDelete.hidden = hidden
		return m.StreamDeleteSwitchPage()
	case key.Matches(keyMsg, m.keys.Back):
		m, _ = m.clearFilterOrSelection()
		return m, nil
	case key.Matches(keyMsg, m.keys.Quit):
		if m, ok := m.clearFilterOrSelection(); ok {
			return m, nil
		}
		return m, tea.Quit
	default:
		o.table = o.table.Update(keyMsg)
		o.selected = o.table.Cursor()
	}

	return m, cmd
}

func (m model) StreamOverviewView() string {
	return m.state.streamOverview.table.View()
}
