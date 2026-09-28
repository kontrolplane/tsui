package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/kontrolplane/tsui/pkg/tsui"
	"github.com/kontrolplane/tsui/pkg/tui/commands"
	"github.com/kontrolplane/tsui/pkg/tui/styles"
)

type purgeFocus int

const (
	purgeFocusButtons purgeFocus = iota
	purgeFocusSubject
)

type purgeStep int

const (
	purgeStepAsk  purgeStep = iota // choose the subject and confirm
	purgeStepSure                  // confirm a subject purge of unknown or large size once more
	purgeStepType                  // type the stream name to purge all of it
)

// secondPromptThreshold is the message count above which a subject purge asks for confirmation twice.
const secondPromptThreshold = 10

type streamPurgeState struct {
	stream       tsui.Stream
	selected     int // 0 = no, 1 = yes
	step         purgeStep
	fromOverview bool
	focus        purgeFocus
	subjectInput textinput.Model
	subject      string // the subject being purged once confirmed, empty for the whole stream
	confirm      typedConfirm
}

func (m model) StreamPurgeSwitchPage() (model, tea.Cmd) {
	m.error = ""
	stream := m.state.streamPurge.stream
	if stream.DenyPurge {
		m.error = fmt.Sprintf("stream %s does not allow purging (deny_purge).", stream.Name)
		return m, nil
	}

	p := &m.state.streamPurge
	p.selected = 0
	p.step = purgeStepAsk
	p.subject = ""
	p.focus = purgeFocusButtons
	p.subjectInput = textinput.New()
	p.subjectInput.Placeholder = "all subjects"
	p.subjectInput.CharLimit = 255
	p.subjectInput.SetWidth(40)
	p.subjectInput.Prompt = "› "
	p.subjectInput.SetStyles(styles.TextInput())
	return m.SwitchPage(streamPurge), nil
}

func (m model) streamPurgeGoBack() (model, tea.Cmd) {
	if m.state.streamPurge.fromOverview {
		return m.StreamOverviewGoBack()
	}
	return m.StreamDetailsGoBack()
}

func (m model) purgeSubject() string {
	return strings.TrimSpace(m.state.streamPurge.subjectInput.Value())
}

// validSubjectFilter reports whether s is a subject a purge or filter can take: no empty tokens,
// no whitespace, and `>` only as the last token.
func validSubjectFilter(s string) bool {
	if s == "" || strings.ContainsAny(s, " \t\r\n") {
		return false
	}
	tokens := strings.Split(s, ".")
	for i, t := range tokens {
		if t == "" || (t == ">" && i != len(tokens)-1) {
			return false
		}
	}
	return true
}

// subjectInStream reports whether the (possibly wildcard) subject overlaps with one of the stream subjects.
func subjectInStream(s tsui.Stream, subject string) bool {
	if len(s.Subjects) == 0 {
		return true
	}
	for _, pattern := range s.Subjects {
		if tsui.SubjectsOverlap(pattern, subject) {
			return true
		}
	}
	return false
}

// covers reports whether every subject matching pattern also matches filter.
func covers(filter, pattern string) bool {
	ft, pt := strings.Split(filter, "."), strings.Split(pattern, ".")
	for i, f := range ft {
		switch {
		case f == ">":
			return i < len(pt)
		case i >= len(pt), pt[i] == ">":
			return false
		case f != "*" && f != pt[i]:
			return false
		}
	}
	return len(ft) == len(pt)
}

// coversStream reports whether purging subject empties the whole stream.
func coversStream(s tsui.Stream, subject string) bool {
	if subject == "" || subject == ">" {
		return true
	}
	if len(s.Subjects) == 0 {
		return false
	}
	for _, pattern := range s.Subjects {
		if !covers(subject, pattern) {
			return false
		}
	}
	return true
}

// purgeEstimate counts the messages a purge of subject would remove, when the subjects of the
// stream are known.
func (m model) purgeEstimate(subject string) (uint64, bool) {
	p := m.state.streamPurge
	if coversStream(p.stream, subject) {
		return p.stream.Messages, true
	}
	d := m.state.streamDetails
	if d.stream.Name != p.stream.Name || !d.subjectsLoaded || d.stream.NumSubjects > tsui.MaxListedSubjects {
		return 0, false
	}
	var n uint64
	for _, s := range d.subjects {
		if tsui.SubjectMatches(subject, s.Subject) {
			n += s.Messages
		}
	}
	return n, true
}

func (m model) purgeAmount(subject string) string {
	if n, ok := m.purgeEstimate(subject); ok {
		return plural(int(n), "message")
	}
	return "an unknown number of messages"
}

func (m model) StreamPurgeView() string {
	p := m.state.streamPurge
	name := styles.B(dialogName(p.stream.Name), styles.ToneText)
	note := styles.Faint("consumers keep their state and continue from the next message published.")

	switch p.step {
	case purgeStepType:
		return dialog("purge stream", styles.ToneDanger,
			styles.Render(styles.S("this removes ", styles.ToneBody), styles.B("all "+plural(int(p.stream.Messages), "message"), styles.ToneDanger),
				styles.S(" from ", styles.ToneBody), name, styles.S(".", styles.ToneBody)),
			note,
			"",
			p.confirm.view(),
		)
	case purgeStepSure:
		prompt := append([]styles.Span{styles.S("this removes ", styles.ToneBody), styles.B(m.purgeAmount(p.subject), styles.ToneWarning),
			styles.S(" on ", styles.ToneBody)}, styles.Subject(p.subject, styles.ToneText)...)
		prompt = append(prompt, styles.S(", are you sure?", styles.ToneBody))
		return dialog("purge stream", styles.ToneWarning,
			styles.Render(prompt...),
			note,
			"",
			confirmButtons(p.selected, styles.ToneWarning),
		)
	}

	subject := m.purgeSubject()
	tone := styles.ToneWarning
	var prompt []styles.Span
	if coversStream(p.stream, subject) {
		tone = styles.ToneDanger
		prompt = []styles.Span{styles.S("purge all messages from ", styles.ToneBody), name, styles.S("?", styles.ToneBody)}
	} else {
		prompt = append([]styles.Span{styles.S("purge messages on ", styles.ToneBody)}, styles.Subject(subject, styles.ToneText)...)
		prompt = append(prompt, styles.S(" from ", styles.ToneBody), name, styles.S("?", styles.ToneBody))
	}

	labelTone := styles.ToneFaint
	if p.focus == purgeFocusSubject {
		labelTone = styles.ToneAccent
	}
	subjectRow := styles.Fg(labelTone).Render("subject ") + p.subjectInput.View()

	buttons := confirmButtons(p.selected, tone)
	if p.focus == purgeFocusSubject {
		buttons = confirmButtons(-1, tone)
	}

	return dialog("purge stream", tone,
		styles.Render(prompt...),
		styles.Faint("removes "+m.purgeAmount(subject)+". ")+note,
		"",
		subjectRow,
		styles.Faint("wildcards allowed, empty purges every subject"),
		"",
		buttons,
	)
}

func (m model) StreamPurgeUpdate(msg tea.Msg) (model, tea.Cmd) {
	p := &m.state.streamPurge
	var cmd tea.Cmd

	keyMsg, isKey := msg.(tea.KeyPressMsg)

	switch {
	case p.step == purgeStepType:
		if isKey {
			switch {
			case key.Matches(keyMsg, m.keys.Back):
				return m.streamPurgeGoBack()
			case key.Matches(keyMsg, m.keys.View):
				if !p.confirm.ok() {
					return m, nil
				}
				return m.purge()
			}
		}
		p.confirm, cmd = p.confirm.update(msg)
		return m, cmd

	case p.focus == purgeFocusSubject:
		if isKey {
			switch {
			case key.Matches(keyMsg, m.keys.NextField, m.keys.PrevField, m.keys.View):
				p.focus = purgeFocusButtons
				p.subjectInput.Blur()
				return m, nil
			case key.Matches(keyMsg, m.keys.Back):
				return m.streamPurgeGoBack()
			}
		}
		p.subjectInput, cmd = p.subjectInput.Update(msg)
		return m, cmd

	case p.step == purgeStepAsk && isKey && key.Matches(keyMsg, m.keys.NextField, m.keys.PrevField):
		p.focus = purgeFocusSubject
		return m, p.subjectInput.Focus()
	}

	switch m.confirmKey(msg, &p.selected) {
	case confirmNo:
		return m.streamPurgeGoBack()
	case confirmYes:
		if p.step == purgeStepSure {
			return m.purge()
		}
		return m.confirmPurge()
	}
	return m, nil
}

// confirmPurge checks the subject and asks once more where the purge warrants it: a purge of the
// whole stream asks to type its name, one of a subject whose size is unknown or large asks twice.
func (m model) confirmPurge() (model, tea.Cmd) {
	p := &m.state.streamPurge
	subject := m.purgeSubject()
	switch {
	case subject != "" && !validSubjectFilter(subject):
		m.error = fmt.Sprintf("%q is not a valid subject.", subject)
		return m, nil
	case subject != "" && !subjectInStream(p.stream, subject):
		m.error = fmt.Sprintf("subject %s is not part of stream %s.", subject, p.stream.Name)
		return m, nil
	case coversStream(p.stream, subject):
		p.subject = ""
		p.step = purgeStepType
		var cmd tea.Cmd
		p.confirm, cmd = newTypedConfirm(p.stream.Name)
		return m, cmd
	}
	p.subject = subject
	if n, ok := m.purgeEstimate(subject); !ok || n > secondPromptThreshold {
		p.step = purgeStepSure
		return m, nil
	}
	return m.purge()
}

func (m model) purge() (model, tea.Cmd) {
	p := m.state.streamPurge
	if m.disconnected() {
		m.error = notConnected("purged")
		return m, nil
	}
	m.busy, m.loading, m.loadingMsg = true, true, "purging "+p.stream.Name+"…"
	return m, commands.PurgeStream(m.context, m.js, p.stream.Name, p.subject)
}
