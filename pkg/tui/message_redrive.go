package tui

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/kontrolplane/tsui/pkg/tsui"
	"github.com/kontrolplane/tsui/pkg/tui/commands"
	"github.com/kontrolplane/tsui/pkg/tui/styles"
)

type redriveFocus int

const (
	redriveFocusSubject redriveFocus = iota
	redriveFocusRemove
	redriveFocusButtons
	redriveFocusCount
)

// messageRedriveState publishes stored messages again, to another subject or to their own, which
// moves them into whichever stream listens there: back from a dead letter stream to its origin.
type messageRedriveState struct {
	sequences    []uint64
	subjects     []string // the subjects the messages are stored on, each once
	subjectInput textinput.Model
	remove       bool // delete each message from the stream once the target stored it
	focus        redriveFocus
	selected     int  // 0 = no, 1 = yes
	fromDetails  bool // true when triggered from the message details view
}

func redriveInputWidth() int {
	return min(56, dialogTextWidth()-8)
}

// MessageRedriveSwitchPage asks where to redrive the messages at seqs, stored on subjects.
func (m model) MessageRedriveSwitchPage(seqs []uint64, subjects []string, fromDetails bool) (model, tea.Cmd) {
	m.error = ""
	input := textinput.New()
	input.Placeholder = "each message's own subject"
	if len(subjects) == 1 {
		input.Placeholder = subjects[0]
	}
	input.CharLimit = 255
	input.SetWidth(redriveInputWidth())
	input.Prompt = "› "
	input.SetStyles(styles.TextInput())

	m.state.messageRedrive = messageRedriveState{
		sequences:    seqs,
		subjects:     subjects,
		subjectInput: input,
		fromDetails:  fromDetails,
	}
	m, cmd := m.focusRedriveField(redriveFocusSubject)
	return m.SwitchPage(messageRedrive), cmd
}

// redriveSubjects returns the subjects of the messages at seqs, each once, in list order.
func (m model) redriveSubjects(seqs []uint64) []string {
	var subjects []string
	for _, msg := range m.state.streamDetails.messages {
		if slices.Contains(seqs, msg.Sequence) && !slices.Contains(subjects, msg.Subject) {
			subjects = append(subjects, msg.Subject)
		}
	}
	return subjects
}

func (m model) focusRedriveField(f redriveFocus) (model, tea.Cmd) {
	r := &m.state.messageRedrive
	r.focus = f
	if f == redriveFocusSubject {
		return m, r.subjectInput.Focus()
	}
	r.subjectInput.Blur()
	return m, nil
}

func (m model) messageRedriveGoBack() (model, tea.Cmd) {
	if m.state.messageRedrive.fromDetails {
		m.error = ""
		return m.SwitchPage(messageDetails), nil
	}
	return m.StreamDetailsGoBack()
}

func (m model) redriveSubject() string {
	return strings.TrimSpace(m.state.messageRedrive.subjectInput.Value())
}

// redriveTargets returns the subjects the messages will be published on.
func (m model) redriveTargets() []string {
	if s := m.redriveSubject(); s != "" {
		return []string{s}
	}
	return m.state.messageRedrive.subjects
}

// storingStream returns the stream that stores what is published on subject, among the streams
// listed on the overview. Streams cannot share subjects, so there is at most one.
func (m model) storingStream(subject string) (string, bool) {
	for _, s := range m.state.streamOverview.streams {
		for _, pattern := range s.Subjects {
			if tsui.SubjectMatches(pattern, subject) {
				return s.Name, true
			}
		}
	}
	return "", false
}

// redriveStatus tells, while typing, which streams will store the redriven messages.
func (m model) redriveStatus() []styles.Span {
	subject := m.redriveSubject()
	if subject != "" && !tsui.ValidPublishSubject(subject) {
		return []styles.Span{styles.S("✗ ", styles.ToneDanger), styles.S("no wildcards, spaces or empty tokens when publishing", styles.ToneDanger)}
	}
	if !m.state.streamOverview.loaded {
		return []styles.Span{styles.S("the streams are not known yet", styles.ToneFaint)}
	}
	source := m.state.streamDetails.stream.Name
	var streams []string
	for _, target := range m.redriveTargets() {
		name, ok := m.storingStream(target)
		if !ok {
			return append([]styles.Span{styles.S("▲ ", styles.ToneWarning), styles.S("no stream listens on ", styles.ToneWarning)},
				styles.Subject(target, styles.ToneText)...)
		}
		if !slices.Contains(streams, name) {
			streams = append(streams, name)
		}
	}
	if slices.Contains(streams, source) {
		return []styles.Span{styles.S("▲ ", styles.ToneWarning), styles.S("stored again by "+strings.Join(streams, ", ")+", the stream they are in", styles.ToneWarning)}
	}
	return []styles.Span{styles.S("✓ ", styles.ToneSuccess), styles.S("stored by ", styles.ToneMuted), styles.S(strings.Join(streams, ", "), styles.ToneText)}
}

func (m model) MessageRedriveView() string {
	r := m.state.messageRedrive
	stream := m.state.streamDetails.stream
	names := make([]string, len(r.sequences))
	for i, seq := range r.sequences {
		names[i] = fmt.Sprintf("#%d", seq)
	}

	tone := styles.ToneAccent
	if r.remove {
		tone = styles.ToneWarning
	}
	verb := "redrive "
	if r.remove {
		verb = "move "
	}
	target := styles.B(plural(len(names), "message"), styles.ToneText)
	if len(names) == 1 {
		target = styles.B("message "+names[0], styles.ToneText)
	}
	prompt := []styles.Span{styles.S(verb, styles.ToneBody), target, styles.S(" from ", styles.ToneBody),
		styles.B(dialogName(stream.Name), styles.ToneText), styles.S("?", styles.ToneBody)}

	labelTone := func(f redriveFocus) styles.Tone {
		if r.focus == f {
			return styles.ToneAccent
		}
		return styles.ToneFaint
	}
	subjectRow := styles.Fg(labelTone(redriveFocusSubject)).Render("to ") + r.subjectInput.View()

	box := "[ ]"
	if r.remove {
		box = "[x]"
	}
	removeRow := styles.Render(styles.S(box+" ", labelTone(redriveFocusRemove)),
		styles.S("delete from "+dialogName(stream.Name)+" once stored", styles.ToneBody))
	if stream.DenyDelete {
		removeRow = styles.Faint(fmt.Sprintf("%s does not allow deleting, the messages stay (deny_delete)", dialogName(stream.Name)))
	}

	selected := r.selected
	if r.focus != redriveFocusButtons {
		selected = -1
	}

	body := []string{styles.Render(prompt...)}
	body = append(body, listBody(names, true)...)
	body = append(body,
		styles.Faint("headers and payload are published again, oldest first, without Nats-Expected-* and Nats-Rollup."),
		"",
		subjectRow,
		styles.Render(m.redriveStatus()...),
		styles.Faint("empty publishes each message on the subject it is stored on"),
		"",
		removeRow,
		"",
		confirmButtons(selected, tone),
	)
	return dialog("redrive", tone, body...)
}

func (m model) MessageRedriveUpdate(msg tea.Msg) (model, tea.Cmd) {
	r := &m.state.messageRedrive
	var cmd tea.Cmd

	keyMsg, isKey := msg.(tea.KeyPressMsg)
	if isKey {
		switch {
		case key.Matches(keyMsg, m.keys.Back):
			return m.messageRedriveGoBack()
		case key.Matches(keyMsg, m.keys.NextField):
			return m.focusRedriveField((r.focus + 1) % redriveFocusCount)
		case key.Matches(keyMsg, m.keys.PrevField):
			return m.focusRedriveField((r.focus + redriveFocusCount - 1) % redriveFocusCount)
		}
	}

	switch r.focus {
	case redriveFocusSubject:
		if isKey && key.Matches(keyMsg, m.keys.View) {
			return m.focusRedriveField(redriveFocusRemove)
		}
		r.subjectInput, cmd = r.subjectInput.Update(msg)
		return m, cmd
	case redriveFocusRemove:
		if !isKey {
			return m, nil
		}
		switch {
		case key.Matches(keyMsg, m.keys.Select):
			r.remove = !r.remove && !m.state.streamDetails.stream.DenyDelete
		case key.Matches(keyMsg, m.keys.View):
			return m.focusRedriveField(redriveFocusButtons)
		case key.Matches(keyMsg, m.keys.Quit):
			return m.messageRedriveGoBack()
		}
		return m, nil
	}

	switch m.confirmKey(msg, &r.selected) {
	case confirmNo:
		return m.messageRedriveGoBack()
	case confirmYes:
		return m.redrive()
	}
	return m, nil
}

func (m model) redrive() (model, tea.Cmd) {
	r := m.state.messageRedrive
	subject := m.redriveSubject()
	if subject != "" && !tsui.ValidPublishSubject(subject) {
		m.error = fmt.Sprintf("%q is not a valid subject to publish to (no wildcards, spaces or empty tokens).", subject)
		return m, nil
	}
	if m.disconnected() {
		m.error = notConnected("redriven")
		return m, nil
	}
	m.busy, m.loading = true, true
	m.loadingMsg = "redriving " + plural(len(r.sequences), "message") + "…"
	return m, commands.RedriveMessages(m.context, m.js, m.state.streamDetails.stream.Name, r.sequences, subject, r.remove)
}

// redriveResult describes the outcome of a redrive: what was done, and what to say when it stopped.
func redriveResult(total int, res tsui.RedriveResult) (done, failed string) {
	verb := "redrove "
	if len(res.Deleted) > 0 {
		verb = "moved "
	}
	done = verb + plural(len(res.Redriven), "message")
	if len(res.Streams) > 0 {
		done += " to " + strings.Join(res.Streams, ", ")
	}
	if n := len(res.Duplicates); n > 0 {
		done += fmt.Sprintf(", %s already stored there", plural(n, "duplicate"))
	}
	failed = "could not redrive " + plural(total, "message")
	if n := len(res.Redriven) + len(res.Duplicates); n > 0 {
		failed = fmt.Sprintf("redrove %d of %s, then stopped", n, plural(total, "message"))
	}
	return done, failed
}
