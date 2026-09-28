// Package commands provides tea.Cmd factories for async operations.
package commands

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/kontrolplane/tsui/pkg/tsui"
	"github.com/kontrolplane/tsui/pkg/tui/messages"
)

// RefreshInterval is the interval for auto-refresh of the visible data.
const RefreshInterval = 5 * time.Second

// MessageLimit is the number of most recent messages shown for a stream.
const MessageLimit = 100

const requestTimeout = 10 * time.Second

// request runs fn in a tea.Cmd with the request timeout applied to ctx.
func request(ctx context.Context, fn func(context.Context) tea.Msg) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, requestTimeout)
		defer cancel()
		return fn(ctx)
	}
}

func LoadStreams(ctx context.Context, js jetstream.JetStream) tea.Cmd {
	return request(ctx, func(ctx context.Context) tea.Msg {
		streams, err := tsui.ListStreams(ctx, js)
		return messages.StreamsLoadedMsg{Streams: streams, Err: err}
	})
}

func LoadStream(ctx context.Context, js jetstream.JetStream, name string) tea.Cmd {
	return loadStream(ctx, js, name, false)
}

// RefreshStream reloads the stream info in the background, see messages.StreamLoadedMsg.
func RefreshStream(ctx context.Context, js jetstream.JetStream, name string) tea.Cmd {
	return loadStream(ctx, js, name, true)
}

func loadStream(ctx context.Context, js jetstream.JetStream, name string, refresh bool) tea.Cmd {
	return request(ctx, func(ctx context.Context) tea.Msg {
		stream, err := tsui.GetStream(ctx, js, name)
		return messages.StreamLoadedMsg{Name: name, Stream: stream, Refresh: refresh, Err: err}
	})
}

// maxNewerPages bounds the pages a load of new messages reads to reach the ones already loaded.
const maxNewerPages = 10

// listPage reads the page of messages below before, of the whole stream or only those on subject.
func listPage(ctx context.Context, js jetstream.JetStream, stream, subject string, before uint64) ([]tsui.Message, uint64, error) {
	if subject == "" {
		return tsui.ListMessagesBefore(ctx, js, stream, before, MessageLimit)
	}
	return tsui.ListSubjectMessages(ctx, js, stream, subject, before, MessageLimit)
}

// LoadMessages loads the newest messages of a stream, or of those on subject. With after set, it
// keeps reading older pages until it reaches that sequence, so nothing between the newest messages
// and the ones already loaded is left out, and reports a gap when it gives up first.
func LoadMessages(ctx context.Context, js jetstream.JetStream, stream, subject string, after, gen uint64) tea.Cmd {
	return request(ctx, func(ctx context.Context) tea.Msg {
		loaded := messages.MessagesLoadedMsg{Stream: stream, Subject: subject, Gen: gen, After: after}
		var before uint64
		for range maxNewerPages {
			msgs, next, err := listPage(ctx, js, stream, subject, before)
			if err != nil {
				loaded.Err = err
				return loaded
			}
			loaded.Messages = append(loaded.Messages, msgs...)
			loaded.Next = next
			if after == 0 || next == 0 || next <= after+1 {
				return loaded
			}
			before = next
		}
		loaded.Gap = true
		return loaded
	})
}

// LoadOlderMessages loads the page of messages below before.
func LoadOlderMessages(ctx context.Context, js jetstream.JetStream, stream, subject string, before, gen uint64) tea.Cmd {
	return request(ctx, func(ctx context.Context) tea.Msg {
		msgs, next, err := listPage(ctx, js, stream, subject, before)
		return messages.OlderMessagesLoadedMsg{Stream: stream, Subject: subject, Gen: gen, Before: before, Messages: msgs, Next: next, Err: err}
	})
}

// FetchMessage loads the message at seq.
func FetchMessage(ctx context.Context, js jetstream.JetStream, stream string, seq uint64) tea.Cmd {
	return request(ctx, func(ctx context.Context) tea.Msg {
		msg, err := tsui.GetMessage(ctx, js, stream, seq)
		return messages.MessageFetchedMsg{Stream: stream, Sequence: seq, Message: msg, Err: err}
	})
}

func LoadConsumers(ctx context.Context, js jetstream.JetStream, stream string, gen uint64) tea.Cmd {
	return request(ctx, func(ctx context.Context) tea.Msg {
		consumers, err := tsui.ListConsumers(ctx, js, stream)
		return messages.ConsumersLoadedMsg{Stream: stream, Gen: gen, Consumers: consumers, Err: err}
	})
}

func LoadConsumer(ctx context.Context, js jetstream.JetStream, stream, name string) tea.Cmd {
	return request(ctx, func(ctx context.Context) tea.Msg {
		consumer, err := tsui.GetConsumer(ctx, js, stream, name)
		return messages.ConsumerLoadedMsg{Stream: stream, Name: name, Consumer: consumer, Err: err}
	})
}

// LoadStreamDetails loads everything shown on the stream details page, with loadMessages for its
// messages and consumersGen numbering the load of its consumers.
func LoadStreamDetails(ctx context.Context, js jetstream.JetStream, stream string, loadMessages tea.Cmd, consumersGen uint64) tea.Cmd {
	return tea.Batch(
		LoadStream(ctx, js, stream),
		loadMessages,
		LoadConsumers(ctx, js, stream, consumersGen),
	)
}

// RefreshStreamDetails reloads the stream info and consumers, but not the messages.
func RefreshStreamDetails(ctx context.Context, js jetstream.JetStream, stream string, consumersGen uint64) tea.Cmd {
	return tea.Batch(
		RefreshStream(ctx, js, stream),
		LoadConsumers(ctx, js, stream, consumersGen),
	)
}

func LoadSubjects(ctx context.Context, js jetstream.JetStream, stream string) tea.Cmd {
	return request(ctx, func(ctx context.Context) tea.Msg {
		subjects, err := tsui.ListSubjects(ctx, js, stream)
		return messages.SubjectsLoadedMsg{Stream: stream, Subjects: subjects, Err: err}
	})
}

func LoadServer(ctx context.Context, nc *nats.Conn, js jetstream.JetStream) tea.Cmd {
	return request(ctx, func(ctx context.Context) tea.Msg {
		server, err := tsui.GetServer(ctx, nc, js)
		return messages.ServerLoadedMsg{Server: server, Err: err}
	})
}

func ScheduleServerRefresh() tea.Cmd {
	return tea.Tick(RefreshInterval, func(time.Time) tea.Msg {
		return messages.ServerTickMsg{}
	})
}

func CreateStream(ctx context.Context, js jetstream.JetStream, cfg tsui.StreamConfig) tea.Cmd {
	return request(ctx, func(ctx context.Context) tea.Msg {
		stream, err := tsui.CreateStream(ctx, js, cfg)
		return messages.StreamCreatedMsg{Stream: stream, Err: err}
	})
}

func DeleteStreams(ctx context.Context, js jetstream.JetStream, names []string) tea.Cmd {
	return request(ctx, func(ctx context.Context) tea.Msg {
		deleted, err := tsui.DeleteStreams(ctx, js, names)
		return messages.StreamsDeletedMsg{Names: deleted, Err: err}
	})
}

func PurgeStream(ctx context.Context, js jetstream.JetStream, stream, subject string) tea.Cmd {
	return request(ctx, func(ctx context.Context) tea.Msg {
		err := tsui.PurgeStream(ctx, js, stream, subject)
		return messages.StreamPurgedMsg{Stream: stream, Err: err}
	})
}

func PublishMessage(ctx context.Context, js jetstream.JetStream, stream, subject string, header nats.Header, data []byte) tea.Cmd {
	return request(ctx, func(ctx context.Context) tea.Msg {
		ack, err := tsui.PublishMessage(ctx, js, subject, header, data)
		if ack.Stream != "" {
			stream = ack.Stream
		}
		return messages.MessagePublishedMsg{Stream: stream, Sequence: ack.Sequence, Duplicate: ack.Duplicate, Err: err}
	})
}

func DeleteMessages(ctx context.Context, js jetstream.JetStream, stream string, seqs []uint64) tea.Cmd {
	return request(ctx, func(ctx context.Context) tea.Msg {
		deleted, err := tsui.DeleteMessages(ctx, js, stream, seqs)
		return messages.MessagesDeletedMsg{Stream: stream, Sequences: deleted, Err: err}
	})
}

func DeleteConsumers(ctx context.Context, js jetstream.JetStream, stream string, names []string) tea.Cmd {
	return request(ctx, func(ctx context.Context) tea.Msg {
		deleted, err := tsui.DeleteConsumers(ctx, js, stream, names)
		return messages.ConsumersDeletedMsg{Stream: stream, Names: deleted, Err: err}
	})
}

func ScheduleRefresh(gen int) tea.Cmd {
	return tea.Tick(RefreshInterval, func(time.Time) tea.Msg {
		return messages.RefreshTickMsg{Gen: gen}
	})
}

// CopyToClipboard copies text using the system clipboard. On failure the update
// loop falls back to OSC52, which also works over ssh.
func CopyToClipboard(text string) tea.Cmd {
	return func() tea.Msg {
		return messages.ClipboardCopiedMsg{Text: text, Err: clipboard.WriteAll(text)}
	}
}

// ScheduleClock ticks once after d, for a clock on screen that changes then.
func ScheduleClock(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg {
		return messages.ClockTickMsg{}
	})
}

func ClearStatusAfter(d time.Duration, gen int) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg {
		return messages.StatusClearMsg{Gen: gen}
	})
}
