// Package messages defines all the custom tea.Msg types for the TUI application.
package messages

import (
	"github.com/kontrolplane/tsui/pkg/tsui"
)

type StreamsLoadedMsg struct {
	Streams []tsui.Stream
	Err     error
}

// StreamLoadedMsg carries the info of a single stream, used by the stream details view.
// Refresh marks a background refresh, as opposed to the initial load of the view.
type StreamLoadedMsg struct {
	Name    string
	Stream  tsui.Stream
	Refresh bool
	Err     error
}

// MessagesLoadedMsg carries the newest messages of a stream, or of those on Subject. Gen is echoed
// from the request so responses to outdated requests can be dropped. Next is the sequence to page
// older messages from, 0 when there are none. After echoes the newest message already loaded, 0
// when none was: the load then reaches down to it unless Gap reports it could not.
type MessagesLoadedMsg struct {
	Stream   string
	Subject  string
	Gen      uint64
	After    uint64
	Messages []tsui.Message
	Next     uint64
	Gap      bool
	Err      error
}

// OlderMessagesLoadedMsg carries a page of messages older than Before. Gen is echoed from the
// request, a page no longer asked for is dropped.
type OlderMessagesLoadedMsg struct {
	Stream   string
	Subject  string
	Gen      uint64
	Before   uint64
	Messages []tsui.Message
	Next     uint64
	Err      error
}

// MessageFetchedMsg carries the message at Sequence, fetched to be opened.
type MessageFetchedMsg struct {
	Stream   string
	Sequence uint64
	Message  tsui.Message
	Err      error
}

// ClockTickMsg redraws the time since the last refresh.
type ClockTickMsg struct{}

// ConsumersLoadedMsg carries the consumers of a stream. Gen is echoed from the request, a list
// older than the one shown is dropped.
type ConsumersLoadedMsg struct {
	Stream    string
	Gen       uint64
	Consumers []tsui.Consumer
	Err       error
}

type ConsumerLoadedMsg struct {
	Stream   string
	Name     string
	Consumer tsui.Consumer
	Err      error
}

type StreamCreatedMsg struct {
	Stream tsui.Stream
	Err    error
}

// StreamsDeletedMsg lists the streams that were deleted; Err joins the failures of the others.
type StreamsDeletedMsg struct {
	Names []string
	Err   error
}

type StreamPurgedMsg struct {
	Stream string
	Err    error
}

// MessagePublishedMsg reports where a message was stored. Duplicate is set when the stream
// already held a message with the same Nats-Msg-Id and did not store it again.
type MessagePublishedMsg struct {
	Stream    string
	Sequence  uint64
	Duplicate bool
	Err       error
}

type MessagesDeletedMsg struct {
	Stream    string
	Sequences []uint64
	Err       error
}

type ConsumersDeletedMsg struct {
	Stream string
	Names  []string
	Err    error
}

// RefreshTickMsg triggers a background refresh. Ticks whose generation no longer
// matches the model's are dropped, so at most one refresh loop is active.
type RefreshTickMsg struct {
	Gen int
}

type ClipboardCopiedMsg struct {
	Text string
	Err  error
}

type StatusClearMsg struct {
	Gen int
}

type SubjectsLoadedMsg struct {
	Stream   string
	Subjects []tsui.SubjectCount
	Err      error
}

type ServerLoadedMsg struct {
	Server tsui.Server
	Err    error
}

// ServerTickMsg triggers a refresh of the server and account info shown in the header.
type ServerTickMsg struct{}
