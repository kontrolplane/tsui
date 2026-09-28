package tui

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/kontrolplane/tsui/pkg/tsui"
	"github.com/kontrolplane/tsui/pkg/tui/commands"
	"github.com/kontrolplane/tsui/pkg/tui/messages"
	"github.com/kontrolplane/tsui/pkg/tui/styles"
)

// maxAutoPages bounds the older pages loaded one after another without a key press, when pages come
// back short because the stream has long gaps or few messages on the subject.
const maxAutoPages = 20

// resetMessages forgets the loaded messages, before loading those of another stream or subject.
func (m model) resetMessages(subject string) model {
	d := &m.state.streamDetails
	d.subject = subject
	d.messages = nil
	d.messagesLoaded = false
	d.selectedMessage = 0
	d.olderNext = 0
	d.paged = false
	d.fillTo = commands.MessageLimit
	d.autoPages = 0
	return m.cancelOlder()
}

// cancelOlder drops the older page in flight, which no longer continues what is loaded.
func (m model) cancelOlder() model {
	m.state.streamDetails.loadingOlder = false
	m.olderGen++
	return m
}

// loadMessages loads the newest messages of the details stream, or of its subject. Every load is
// numbered, so a slower response to an older one cannot overwrite a newer one. Once older pages
// are loaded, the load reaches down to the newest message on screen so they can stay.
func (m model) loadMessages() (model, tea.Cmd) {
	m.msgsGen++
	d := m.state.streamDetails
	var after uint64
	if d.paged && len(d.messages) > 0 {
		after = d.messages[0].Sequence
	}
	return m, commands.LoadMessages(m.context, m.js, d.stream.Name, d.subject, after, m.msgsGen)
}

// setMessageSource loads the messages on subject from the server, or the newest of the whole
// stream when subject is empty.
func (m model) setMessageSource(subject string) (model, tea.Cmd) {
	if subject == m.state.streamDetails.subject {
		return m, nil
	}
	m = m.resetMessages(subject).updateMessagesTable()
	return m.loadMessages()
}

// subjectLike reports whether a filter names subjects of the stream, so their messages are better
// looked up by the server than searched for among the loaded ones. A plain word only does when it
// is a subject of the stream, or one known to hold messages: on a stream taking `>` or `*` every
// word could be a subject, and is far more likely a search. A plain word is searched for in the
// loaded subjects and payloads instead.
func subjectLike(filter string, s tsui.Stream, known []tsui.SubjectCount) bool {
	if !tsui.ValidFilterSubject(filter) {
		return false
	}
	named := tsui.HasWildcard(filter) || strings.Contains(filter, ".") || slices.Contains(s.Subjects, filter) ||
		slices.ContainsFunc(known, func(k tsui.SubjectCount) bool { return k.Subject == filter })
	if !named {
		return false
	}
	if len(s.Subjects) == 0 {
		return true
	}
	return slices.ContainsFunc(s.Subjects, func(sub string) bool { return tsui.SubjectsOverlap(sub, filter) })
}

// subjectFilter reports whether filter names subjects of the details stream, see subjectLike.
func (m model) subjectFilter(filter string) bool {
	d := m.state.streamDetails
	return subjectLike(filter, d.stream, d.subjects)
}

// applyFilterSource loads the messages matching the applied filter from the server when it names
// subjects, and returns to the newest messages of the stream when it no longer does.
func (m model) applyFilterSource() (model, tea.Cmd) {
	d := m.state.streamDetails
	if m.subjectFilter(d.filterText) {
		return m.setMessageSource(d.filterText)
	}
	return m.setMessageSource("")
}

// mergeNewer puts fresh, the newest messages down to next (0 for all of them), on top of loaded,
// keeping the loaded ones older than what fresh covers. What fresh covers and does not hold was
// deleted.
func mergeNewer(loaded, fresh []tsui.Message, next uint64) []tsui.Message {
	merged := slices.Clone(fresh)
	if next == 0 {
		return merged
	}
	for _, msg := range loaded {
		if msg.Sequence < next {
			merged = append(merged, msg)
		}
	}
	return merged
}

// applyMessages shows a load of the newest messages. Without older pages it replaces what is
// loaded; with them it is put on top, unless too many messages arrived to reach the loaded ones.
func (m model) applyMessages(msg messages.MessagesLoadedMsg) (model, tea.Cmd) {
	d := &m.state.streamDetails
	prev, ok := m.currentMessage()
	var cmd tea.Cmd

	var top uint64
	if len(d.messages) > 0 {
		top = d.messages[0].Sequence
	}
	connected := msg.Next == 0 || msg.Next <= top+1
	switch {
	case !d.paged || len(d.messages) == 0:
		d.messages, d.olderNext, d.paged = msg.Messages, msg.Next, false
		m = m.cancelOlder()
	case !connected && msg.After == 0:
		// Asked for before older pages were added, and it does not reach them.
		return m.loadMessages()
	case !connected:
		d.messages, d.olderNext, d.paged = msg.Messages, msg.Next, false
		d.fillTo, d.autoPages = commands.MessageLimit, 0
		m = m.cancelOlder()
		m, cmd = m.setStatus(fmt.Sprintf("over %d new messages, older pages were let go", len(msg.Messages)), styles.ToneWarning)
	default:
		olderNext := d.olderNext
		if msg.Next == 0 {
			olderNext = 0
		} else if olderNext != 0 {
			olderNext = min(olderNext, msg.Next)
		}
		d.messages = mergeNewer(d.messages, msg.Messages, msg.Next)
		if first := d.stream.FirstSeq; first > 0 {
			d.messages = slices.DeleteFunc(d.messages, func(msg tsui.Message) bool { return msg.Sequence < first })
			if olderNext != 0 && olderNext <= first {
				olderNext = 0
			}
		}
		if olderNext != d.olderNext {
			d.olderNext = olderNext
			m = m.cancelOlder()
		}
	}
	d = &m.state.streamDetails
	d.messagesLoaded, d.messagesStale = true, false
	prune(d.selectedMessages, d.messages, func(msg tsui.Message) uint64 { return msg.Sequence })
	// Following, the top row stays on the newest message; any other row stays on its message.
	if ok && (!d.follow || d.selectedMessage > 0) {
		d.selectedMessage = follow(m.getFilteredMessages(), d.selectedMessage, func(msg tsui.Message) bool { return msg.Sequence == prev.Sequence })
	}
	m = m.updateMessagesTable()
	m, fill := m.fillOlder()
	return m, tea.Batch(cmd, fill)
}

// applyOlderMessages adds a page of older messages below the loaded ones.
func (m model) applyOlderMessages(msg messages.OlderMessagesLoadedMsg) (model, tea.Cmd) {
	d := &m.state.streamDetails
	d.loadingOlder = false
	if msg.Err != nil {
		if errors.Is(msg.Err, jetstream.ErrStreamNotFound) {
			return m, nil
		}
		return m.loadError("could not load older messages", msg.Err, true)
	}
	lowest := msg.Before
	if n := len(d.messages); n > 0 {
		lowest = min(lowest, d.messages[n-1].Sequence)
	}
	for _, older := range msg.Messages {
		if older.Sequence < lowest {
			d.messages = append(d.messages, older)
		}
	}
	d.olderNext = msg.Next
	d.paged = true
	m = m.updateMessagesTable()
	return m.fillOlder()
}

// loadOlder loads the page of messages below the loaded ones.
func (m model) loadOlder() (model, tea.Cmd) {
	d := &m.state.streamDetails
	if d.olderNext == 0 || d.loadingOlder || !d.messagesLoaded {
		return m, nil
	}
	d.loadingOlder = true
	m.olderGen++
	return m, commands.LoadOlderMessages(m.context, m.js, d.stream.Name, d.subject, d.olderNext, m.olderGen)
}

// fillOlder keeps loading older pages while fewer messages than wanted are loaded, within
// maxAutoPages.
func (m model) fillOlder() (model, tea.Cmd) {
	d := &m.state.streamDetails
	if d.olderNext == 0 || d.loadingOlder || len(d.messages) >= d.fillTo || d.autoPages >= maxAutoPages {
		return m, nil
	}
	d.autoPages++
	return m.loadOlder()
}

// moreMessages loads older pages until at least one more message is loaded.
func (m model) moreMessages() (model, tea.Cmd) {
	d := &m.state.streamDetails
	if d.olderNext == 0 || d.loadingOlder {
		return m, nil
	}
	d.fillTo, d.autoPages = len(d.messages)+1, 0
	return m.loadOlder()
}

// movesDown reports whether a key moves the cursor of a table down.
func movesDown(msg tea.KeyPressMsg) bool {
	switch msg.String() {
	case "down", "j", "pgdown", "end", "G":
		return true
	}
	return false
}

// messageTotal is the number of messages the messages tab could hold: in the stream, or on its
// subject when the subjects are known.
func (m model) messageTotal() (uint64, bool) {
	d := m.state.streamDetails
	if d.subject == "" {
		return d.stream.Messages, true
	}
	if !d.subjectsLoaded {
		return 0, false
	}
	var n uint64
	for _, s := range d.subjects {
		if tsui.SubjectMatches(d.subject, s.Subject) {
			n += s.Messages
		}
	}
	return n, true
}

// messagesHint describes how much of the stream the messages tab holds, from the longest wording
// to the shortest.
func (m model) messagesHint() []string {
	d := m.state.streamDetails
	n := len(d.messages)
	if !d.messagesLoaded || n == 0 {
		return nil
	}
	total, known := m.messageTotal()
	count := formatCount(uint64(n))
	if known && total > uint64(n) {
		count += " of " + formatCount(total)
	}
	more := d.olderNext != 0 || (known && total > uint64(n))
	var head string
	switch {
	case d.paged || d.loadingOlder:
		head = count + " loaded"
	case more:
		head = "newest " + count
	case d.subject != "":
		head = plural(n, "message")
	default:
		return nil
	}
	var tail string
	switch {
	case d.loadingOlder:
		tail = "loading older…"
	case d.paged && d.olderNext == 0:
		tail = "start of stream"
	case d.olderNext != 0 && !d.paged:
		tail = "read by sequence without consuming"
	}
	on := ""
	if d.subject != "" {
		on = " on " + d.subject
	}
	var hints []string
	if tail != "" {
		sep := " · "
		if !d.paged && !d.loadingOlder {
			sep = ", "
		}
		hints = append(hints, head+on+sep+tail, head+sep+tail)
	}
	return append(hints, head+on, head)
}

// messagesEmpty explains an empty messages table.
func (m model) messagesEmpty() string {
	d := m.state.streamDetails
	switch {
	case !d.messagesLoaded && d.subject != "":
		return fmt.Sprintf("loading the messages on %s…", d.subject)
	case !d.messagesLoaded:
		return "loading messages…"
	case d.filtering && m.subjectFilter(d.filterText) && d.filterText != d.subject:
		return fmt.Sprintf("enter loads the messages on %s", d.filterText)
	case d.subject != "" && len(d.messages) == 0 && (d.loadingOlder || d.olderNext != 0):
		return fmt.Sprintf("looking for older messages on %s…", d.subject)
	case d.subject != "" && len(d.messages) == 0:
		if tsui.HasWildcard(d.subject) {
			return fmt.Sprintf("no messages on %s", d.subject)
		}
		return fmt.Sprintf("no messages on %s, try %s.>", d.subject, d.subject)
	case len(d.messages) == 0:
		return fmt.Sprintf("no messages in %s%s, press ctrl+n to publish one", d.stream.Name, yet(d.stream))
	case d.subject != "":
		return "no messages match the filter"
	case d.loadingOlder:
		return fmt.Sprintf("no match among the %d loaded messages, loading older…", len(d.messages))
	case d.olderNext != 0 && d.paged:
		return fmt.Sprintf("no match among the %d loaded messages, ↓ loads older", len(d.messages))
	case d.olderNext != 0:
		return fmt.Sprintf("no match among the newest %d messages, ↓ loads older", len(d.messages))
	case d.stream.Messages > uint64(len(d.messages)):
		return fmt.Sprintf("no match among the newest %d messages", len(d.messages))
	}
	return "no messages match the filter"
}

// jumpUpdate handles the go to sequence input: enter opens the message at the typed sequence,
// esc closes it, and only digits are taken.
func (m model) jumpUpdate(msg tea.Msg) (model, tea.Cmd) {
	d := &m.state.streamDetails
	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		var cmd tea.Cmd
		d.jumpInput, cmd = d.jumpInput.Update(msg)
		return m, cmd
	}
	switch {
	case key.Matches(keyMsg, m.keys.Back):
		d.jumping = false
		d.jumpInput.Blur()
		return m, nil
	case key.Matches(keyMsg, m.keys.View):
		seq, err := strconv.ParseUint(strings.TrimSpace(d.jumpInput.Value()), 10, 64)
		if err != nil || seq == 0 {
			return m.setStatus("enter a sequence number", styles.ToneWarning)
		}
		d.jumping = false
		d.jumpInput.Blur()
		return m, commands.FetchMessage(m.context, m.js, d.stream.Name, seq)
	}
	if keyMsg.Text != "" && strings.Trim(keyMsg.Text, "0123456789") != "" {
		return m, nil
	}
	var cmd tea.Cmd
	d.jumpInput, cmd = d.jumpInput.Update(msg)
	return m, cmd
}

// yet words an empty stream that never held a message as waiting for its first one, which a
// stream emptied by a purge or by its limits is not.
func yet(s tsui.Stream) string {
	if s.LastSeq == 0 {
		return " yet"
	}
	return ""
}

// missingMessage explains why there is no message at seq: it was deleted, or the stream holds
// other sequences.
func missingMessage(s tsui.Stream, seq uint64) string {
	switch {
	case s.Messages > 0 && seq >= s.FirstSeq && seq <= s.LastSeq:
		return fmt.Sprintf("message #%d was deleted", seq)
	case s.Messages > 0:
		return fmt.Sprintf("no message #%d (stream holds #%d–#%d)", seq, s.FirstSeq, s.LastSeq)
	}
	return fmt.Sprintf("no message #%d, %s is empty", seq, s.Name)
}

// openFetchedMessage opens a message fetched by its sequence.
func (m model) openFetchedMessage(msg messages.MessageFetchedMsg) (model, tea.Cmd) {
	switch {
	case errors.Is(msg.Err, tsui.ErrMessageNotFound):
		return m.setStatus(missingMessage(m.state.streamDetails.stream, msg.Sequence), styles.ToneWarning)
	case msg.Err != nil:
		return m.loadError(fmt.Sprintf("could not load message #%d", msg.Sequence), msg.Err, true)
	}
	m.state.messageDetails.message = msg.Message
	return m.MessageDetailsSwitchPage()
}

// refreshes reports whether the current page refreshes on its own.
func (m model) refreshes() bool {
	switch m.page {
	case streamOverview, streamDetails, consumerDetails:
		return true
	}
	return false
}

// refreshPage reloads what the current page shows.
func (m model) refreshPage() (model, tea.Cmd) {
	switch m.page {
	case streamOverview:
		return m, commands.LoadStreams(m.context, m.js)
	case streamDetails:
		m.consGen++
		return m, commands.RefreshStreamDetails(m.context, m.js, m.state.streamDetails.stream.Name, m.consGen)
	case consumerDetails:
		return m, m.refreshConsumer()
	}
	return m, nil
}

// togglePause stops the refresh ticks, or starts them again with a refresh.
func (m model) togglePause() (model, tea.Cmd) {
	m.paused = !m.paused
	if m.paused {
		return m, nil
	}
	return m.refreshPage()
}

// clockVisible reports whether the time since the last refresh is on screen.
func (m model) clockVisible() bool {
	return !m.refreshedAt.IsZero() && m.refreshes() && !m.loading && m.error == "" && !m.tooSmall()
}

// keepClock ticks the time since the last refresh while it is on screen, once each time its
// wording changes.
func (m model) keepClock() (model, tea.Cmd) {
	if m.clocking || !m.clockVisible() {
		return m, nil
	}
	m.clocking = true
	return m, commands.ScheduleClock(untilAgoChanges(time.Since(m.refreshedAt)))
}

// untilAgoChanges returns how long until formatAgo words an age of d differently.
func untilAgoChanges(d time.Duration) time.Duration {
	unit := time.Second
	switch {
	case d >= 24*time.Hour:
		unit = 24 * time.Hour
	case d >= time.Hour:
		unit = time.Hour
	case d >= time.Minute:
		unit = time.Minute
	}
	if d < 0 {
		return unit
	}
	return unit - d%unit
}

// formatRefreshed words the time since the last refresh.
func formatRefreshed(d time.Duration) string {
	if d < time.Second {
		return "refreshed just now"
	}
	return "refreshed " + formatAgo(time.Now().Add(-d))
}
