package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/nats-io/nats.go"

	"github.com/kontrolplane/tsui/pkg/tsui"
	"github.com/kontrolplane/tsui/pkg/tui/commands"
	"github.com/kontrolplane/tsui/pkg/tui/styles"
)

type publishFocus int

const (
	publishFocusSubject publishFocus = iota
	publishFocusHeaders
	publishFocusBody
	publishFocusCancel
	publishFocusSubmit
	publishFocusCount
)

const publishHeadersHeight = 4

func publishBodyHeight() int { return contentHeight - 2 }

type messagePublishState struct {
	subject        textinput.Model
	headers        textarea.Model
	body           textarea.Model
	focus          publishFocus
	initialSubject string
}

// dirty reports whether anything was entered that leaving the form would lose.
func (p messagePublishState) dirty() bool {
	return strings.TrimSpace(p.body.Value()) != "" || strings.TrimSpace(p.headers.Value()) != "" ||
		p.subject.Value() != p.initialSubject
}

func (m model) MessagePublishSwitchPage() (model, tea.Cmd) {
	m.error = ""
	stream := m.state.streamDetails.stream

	subject := textinput.New()
	subject.Placeholder = "orders.eu.created"
	subject.CharLimit = 255
	subject.SetWidth(leftContentWidth - 4)
	subject.Prompt = "› "
	subject.SetStyles(styles.TextInput())
	if len(stream.Subjects) > 0 {
		subject.Placeholder = stream.Subjects[0]
		subject.SetValue(tsui.SubjectPrefix(stream.Subjects[0]))
	}

	headers := textarea.New()
	headers.Placeholder = "Nats-Msg-Id: order-123\nContent-Type: application/json"
	headers.ShowLineNumbers = false
	headers.SetWidth(leftContentWidth)
	headers.SetHeight(publishHeadersHeight)
	headers.Prompt = "│ "
	headers.SetStyles(styles.TextArea())

	body := textarea.New()
	body.Placeholder = "message payload, json or plain text…"
	body.SetWidth(rightContentWidth)
	body.SetHeight(publishBodyHeight())
	body.CharLimit = 0
	body.Prompt = "│ "
	body.SetStyles(styles.TextArea())

	m.state.messagePublish = messagePublishState{
		subject:        subject,
		headers:        headers,
		body:           body,
		initialSubject: subject.Value(),
	}
	m.armed = false
	m, cmd := m.focusPublishField(publishFocusSubject)
	return m.SwitchPage(messagePublish), cmd
}

func (m model) focusPublishField(f publishFocus) (model, tea.Cmd) {
	p := &m.state.messagePublish
	p.focus = f
	p.subject.Blur()
	p.headers.Blur()
	p.body.Blur()

	switch f {
	case publishFocusSubject:
		return m, p.subject.Focus()
	case publishFocusHeaders:
		return m, p.headers.Focus()
	case publishFocusBody:
		return m, p.body.Focus()
	}
	return m, nil
}

// natsHeaders are the headers the server acts on. They are matched exactly, so a name typed in
// another case is corrected rather than silently ignored.
var natsHeaders = []string{
	"Nats-Msg-Id",
	"Nats-Expected-Stream",
	"Nats-Expected-Last-Sequence",
	"Nats-Expected-Last-Subject-Sequence",
	"Nats-Expected-Last-Msg-Id",
	"Nats-TTL",
}

// parseHeaders parses one `Key: Value` header per line.
func parseHeaders(s string) (nats.Header, error) {
	header := nats.Header{}
	for i, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		name = strings.TrimSpace(name)
		if !ok || name == "" || strings.ContainsAny(name, " \t") {
			return nil, fmt.Errorf("header line %d must look like `Key: Value`", i+1)
		}
		for _, known := range natsHeaders {
			if strings.EqualFold(name, known) {
				name = known
			}
		}
		header.Add(name, strings.TrimSpace(value))
	}
	return header, nil
}

func (m model) submitPublish() (model, tea.Cmd) {
	p := m.state.messagePublish
	stream := m.state.streamDetails.stream

	subject := strings.TrimSpace(p.subject.Value())
	if !tsui.ValidPublishSubject(subject) {
		m.error = fmt.Sprintf("%q is not a valid subject to publish to (no wildcards, spaces or empty tokens).", subject)
		return m, nil
	}
	matches := false
	for _, pattern := range stream.Subjects {
		if tsui.SubjectMatches(pattern, subject) {
			matches = true
			break
		}
	}
	if !matches {
		m.error = fmt.Sprintf("subject %s is not bound to stream %s (%s).", subject, stream.Name, strings.Join(stream.Subjects, ", "))
		return m, nil
	}

	header, err := parseHeaders(p.headers.Value())
	if err != nil {
		m.error = err.Error()
		return m, nil
	}
	if m.disconnected() {
		m.error = notConnected("published")
		return m, nil
	}

	m.busy, m.loading, m.loadingMsg = true, true, "publishing message…"
	return m, commands.PublishMessage(m.context, m.js, stream.Name, subject, header, []byte(p.body.Value()))
}

func (m model) MessagePublishUpdate(msg tea.Msg) (model, tea.Cmd) {
	var cmd tea.Cmd
	p := &m.state.messagePublish

	if keyMsg, ok := msg.(tea.KeyPressMsg); ok {
		switch {
		case key.Matches(keyMsg, m.keys.Back):
			if p.dirty() && !m.armed {
				m.armed = true
				return m.setStatus(discardPrompt, styles.ToneWarning)
			}
			return m.StreamDetailsGoBack()
		case key.Matches(keyMsg, m.keys.Submit):
			return m.submitPublish()
		case key.Matches(keyMsg, m.keys.NextField):
			return m.focusPublishField((p.focus + 1) % publishFocusCount)
		case key.Matches(keyMsg, m.keys.PrevField):
			return m.focusPublishField((p.focus + publishFocusCount - 1) % publishFocusCount)
		}

		switch p.focus {
		case publishFocusSubject:
			if key.Matches(keyMsg, m.keys.View) {
				return m.focusPublishField(publishFocusHeaders)
			}
		case publishFocusCancel, publishFocusSubmit:
			switch {
			case key.Matches(keyMsg, m.keys.Left), key.Matches(keyMsg, m.keys.Right):
				if p.focus == publishFocusCancel {
					p.focus = publishFocusSubmit
				} else {
					p.focus = publishFocusCancel
				}
			case key.Matches(keyMsg, m.keys.View):
				if p.focus == publishFocusCancel {
					return m.StreamDetailsGoBack()
				}
				return m.submitPublish()
			}
			return m, nil
		}
	}

	switch p.focus {
	case publishFocusSubject:
		p.subject, cmd = p.subject.Update(msg)
	case publishFocusHeaders:
		p.headers, cmd = p.headers.Update(msg)
	case publishFocusBody:
		p.body, cmd = p.body.Update(msg)
	}
	return m, cmd
}

// publishSubjectStatus tells, while typing, whether the subject will be stored by the stream.
func (m model) publishSubjectStatus() []styles.Span {
	stream := m.state.streamDetails.stream
	subject := strings.TrimSpace(m.state.messagePublish.subject.Value())
	switch {
	case subject == "" || strings.HasSuffix(subject, "."):
		return []styles.Span{styles.S("complete the subject to publish on", styles.ToneFaint)}
	case !tsui.ValidPublishSubject(subject):
		return []styles.Span{styles.S("✗ ", styles.ToneDanger), styles.S("no wildcards, spaces or empty tokens when publishing", styles.ToneDanger)}
	}
	for _, pattern := range stream.Subjects {
		if tsui.SubjectMatches(pattern, subject) {
			spans := []styles.Span{styles.S("✓ ", styles.ToneSuccess), styles.S("stored by ", styles.ToneMuted), styles.S(stream.Name, styles.ToneText), styles.S(" via ", styles.ToneMuted)}
			return append(spans, styles.Subject(pattern, styles.ToneBody)...)
		}
	}
	return []styles.Span{styles.S("✗ ", styles.ToneWarning), styles.S("not bound to "+stream.Name, styles.ToneWarning)}
}

func (m model) MessagePublishView() string {
	p := m.state.messagePublish
	stream := m.state.streamDetails.stream

	fieldTitle := func(title string, focused bool) string {
		style := styles.Fg(styles.ToneMuted).MarginTop(1)
		if focused {
			style = styles.Fg(styles.ToneAccent).Bold(true).MarginTop(1)
		}
		return style.Render(title)
	}

	dedupe := "Nats-Msg-Id deduplicates repeated publishes"
	if stream.Duplicates > 0 {
		dedupe += " within " + formatDuration(stream.Duplicates)
	}

	top := lipgloss.JoinVertical(lipgloss.Left,
		panelSection("stream", true, leftContentWidth),
		panelRowSpans("name", styles.S(stream.Name, styles.ToneText)),
		panelRowSpans("subjects", styles.Subjects(stream.Subjects, styles.ToneBody)...),
		fieldTitle("subject", p.focus == publishFocusSubject),
		p.subject.View(),
		renderCell(m.publishSubjectStatus(), column{width: leftContentWidth}, nil),
		fieldTitle("headers", p.focus == publishFocusHeaders),
		p.headers.View(),
		publishHeadersHint(p.headers.Value(), dedupe),
	)

	buttons := lipgloss.JoinHorizontal(lipgloss.Center,
		styles.Button("cancel", p.focus == publishFocusCancel, styles.ToneText),
		"    ",
		styles.Button("publish", p.focus == publishFocusSubmit, styles.ToneText),
	)
	bottom := lipgloss.PlaceHorizontal(leftContentWidth, lipgloss.Center, buttons)

	left := lipgloss.JoinVertical(lipgloss.Left,
		top,
		lipgloss.PlaceVertical(contentHeight-lipgloss.Height(top), lipgloss.Bottom, bottom),
	)

	bodyTitle := styles.Fg(styles.ToneText).Bold(true)
	if p.focus == publishFocusBody {
		bodyTitle = styles.Fg(styles.ToneAccent).Bold(true)
	}
	right := lipgloss.JoinVertical(lipgloss.Left,
		styles.SectionHeaderWith(bodyTitle.Render("payload"), payloadMeta(p.body.Value()), rightContentWidth),
		"",
		p.body.View(),
	)

	return splitPanels(left, right)
}

// publishHeadersHint explains the headers field, or flags the line that is not a header while it
// is typed. Either takes two lines, so the form does not move.
func publishHeadersHint(headers, dedupe string) string {
	if _, err := parseHeaders(headers); err != nil {
		return styles.Render(styles.S("✗ ", styles.ToneDanger), styles.S(truncate(err.Error(), leftContentWidth-2), styles.ToneDanger)) +
			"\n" + styles.Faint(truncate("one Key: Value per line", leftContentWidth))
	}
	return styles.Faint(wrapLines("one Key: Value per line. "+dedupe+".", leftContentWidth, 2))
}

// payloadMeta sizes the payload being written and, when it looks like JSON, says whether it parses.
func payloadMeta(body string) string {
	meta := styles.Faint(formatBytes(uint64(len(body))))
	if trimmed := strings.TrimSpace(body); strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		if json.Valid([]byte(trimmed)) {
			return meta + styles.Faint(" · ") + styles.Render(styles.S("json ✓", styles.ToneSuccess))
		}
		return meta + styles.Faint(" · ") + styles.Render(styles.S("invalid json", styles.ToneWarning))
	}
	return meta
}
