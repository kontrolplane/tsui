package tsui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// directGetHeaders are added by the server to direct get responses and are not part of the stored message.
var directGetHeaders = []string{
	jetstream.StreamHeader,
	jetstream.SequenceHeader,
	jetstream.TimeStampHeaer,
	jetstream.SubjectHeader,
	jetstream.LastSequenceHeader,
	"Nats-Num-Pending",
}

// maxScanFactor bounds how far past `limit` we walk back through sequence gaps (deleted/acked messages)
// when falling back to single message lookups.
const maxScanFactor = 20

// directBatchMax bounds the messages asked for in one batched direct get. The server does not honor
// up_to_seq for a plain batch, so a window with gaps is answered with messages past its end.
const directBatchMax = 1000

// directTimeout applies to batched direct gets when the caller did not set a deadline.
const directTimeout = 10 * time.Second

// ListMessages returns up to limit of the newest messages in a stream, newest first.
// Messages are read by sequence, so unlike consuming them this does not affect any consumer.
func ListMessages(ctx context.Context, js jetstream.JetStream, name string, limit int) ([]Message, error) {
	msgs, _, err := ListMessagesBefore(ctx, js, name, 0, limit)
	return msgs, err
}

// listFallback reads messages one lookup at a time, for streams without direct get or servers older than 2.11.
func listFallback(ctx context.Context, s jetstream.Stream, name string, state jetstream.StreamState, limit int) ([]Message, error) {
	msgs, _, err := listFallbackBefore(ctx, s, name, state, state.LastSeq, limit)
	return msgs, err
}

// listFallbackBefore reads the newest limit messages with a sequence up to upTo. next is the lowest
// sequence it examined, or 0 once it reached the start of the stream.
func listFallbackBefore(ctx context.Context, s jetstream.Stream, name string, state jetstream.StreamState, upTo uint64, limit int) ([]Message, uint64, error) {
	// A stream holding no more than limit messages is walked forward with next-by-subject lookups,
	// each of which skips any gap. Sparse streams such as KV buckets are mostly gaps.
	if state.Msgs <= uint64(limit) {
		msgs, err := listForward(ctx, s, name, state.FirstSeq, upTo, int(state.Msgs))
		return msgs, 0, err
	}

	var (
		msgs    []Message
		scanned int
		seq     = upTo
	)
	for seq >= state.FirstSeq && seq > 0 && len(msgs) < limit && scanned < limit*maxScanFactor {
		var seqs []uint64
		for len(seqs) < limit-len(msgs) && seq >= state.FirstSeq && seq > 0 {
			seqs = append(seqs, seq)
			seq--
		}
		scanned += len(seqs)

		batch, err := getMessages(ctx, s, name, seqs)
		if err != nil {
			return nil, 0, err
		}
		msgs = append(msgs, batch...)
	}
	return msgs, nextBefore(seq+1, state.FirstSeq), nil
}

// nextBefore turns the lowest examined sequence into the paging cursor: 0 when nothing older remains.
func nextBefore(lowest, first uint64) uint64 {
	if lowest <= first {
		return 0
	}
	return lowest
}

// listForward reads up to n messages from seq through upTo, skipping gaps, and returns them newest first.
func listForward(ctx context.Context, s jetstream.Stream, name string, seq, upTo uint64, n int) ([]Message, error) {
	msgs := make([]Message, 0, n)
	for len(msgs) < n {
		raw, err := s.GetMsg(ctx, seq, jetstream.WithGetMsgSubject(">"))
		if errors.Is(err, jetstream.ErrMsgNotFound) {
			break
		}
		if err != nil {
			return nil, err
		}
		if raw.Sequence > upTo {
			break
		}
		msgs = append(msgs, toMessage(name, raw))
		seq = raw.Sequence + 1
	}
	slices.Reverse(msgs)
	return msgs, nil
}

// getMessages fetches the given sequences concurrently, preserving their order and skipping gaps.
func getMessages(ctx context.Context, s jetstream.Stream, name string, seqs []uint64) ([]Message, error) {
	results := make([]*Message, len(seqs))
	errs := make([]error, len(seqs))

	var wg sync.WaitGroup
	for i, seq := range seqs {
		wg.Go(func() {
			raw, err := s.GetMsg(ctx, seq)
			if err != nil {
				if !errors.Is(err, jetstream.ErrMsgNotFound) {
					errs[i] = err
				}
				return
			}
			msg := toMessage(name, raw)
			results[i] = &msg
		})
	}
	wg.Wait()

	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	var msgs []Message
	for _, m := range results {
		if m != nil {
			msgs = append(msgs, *m)
		}
	}
	return msgs, nil
}

func toMessage(name string, raw *jetstream.RawStreamMsg) Message {
	return Message{
		Stream:   name,
		Subject:  raw.Subject,
		Sequence: raw.Sequence,
		Time:     raw.Time,
		Header:   stripDirectHeaders(raw.Header),
		Data:     raw.Data,
	}
}

func stripDirectHeaders(h nats.Header) nats.Header {
	for _, k := range directGetHeaders {
		h.Del(k)
	}
	if len(h) == 0 {
		return nil
	}
	return h
}

// listDirect walks back from the last sequence in growing windows, reading each window with one
// batched direct get (nats-server 2.11+).
func listDirect(ctx context.Context, js jetstream.JetStream, name string, state jetstream.StreamState, limit int) ([]Message, error) {
	msgs, _, err := listDirectBefore(ctx, js, name, state, state.LastSeq, limit)
	return msgs, err
}

// listDirectBefore reads the newest limit messages with a sequence up to upTo. next is the lowest
// sequence it examined, or 0 once it reached the start of the stream.
func listDirectBefore(ctx context.Context, js jetstream.JetStream, name string, state jetstream.StreamState, upTo uint64, limit int) ([]Message, uint64, error) {
	ctx, cancel := withDirectTimeout(ctx)
	defer cancel()
	subject := directSubject(js, name)

	var (
		msgs []Message
		span = uint64(limit)
	)
	for len(msgs) < limit && upTo >= state.FirstSeq && upTo > 0 {
		need := limit - len(msgs)
		start := state.FirstSeq
		// Once the older part holds no more than we still need, read it in one go instead of
		// stepping through what may be a long run of gaps. state.Msgs also counts messages past
		// upTo, so this only ever errs towards stepping.
		if state.Msgs > uint64(len(msgs)) && state.Msgs-uint64(len(msgs)) > uint64(need) && upTo-state.FirstSeq+1 > span {
			start = upTo - span + 1
		}
		window, err := directRange(ctx, js.Conn(), subject, name, "", start, upTo)
		if err != nil {
			return nil, 0, err
		}
		if len(window) > need {
			window = window[len(window)-need:]
			start = window[0].Sequence
		}
		slices.Reverse(window)
		msgs = append(msgs, window...)

		upTo = start - 1
		if start == state.FirstSeq {
			break
		}
		span *= 4
	}
	return msgs, nextBefore(upTo+1, state.FirstSeq), nil
}

func withDirectTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, directTimeout)
}

func directSubject(js jetstream.JetStream, name string) string {
	return apiPrefix(js) + "DIRECT.GET." + name
}

type directGetRequest struct {
	Seq        uint64 `json:"seq"`
	NextBySubj string `json:"next_by_subj,omitempty"`
	Batch      int    `json:"batch"`
}

// directRange returns the messages with a sequence in [start, upTo], oldest first, limited to those
// matching filter when it is set.
func directRange(ctx context.Context, nc *nats.Conn, subject, name, filter string, start, upTo uint64) ([]Message, error) {
	var msgs []Message
	for start <= upTo {
		batch := min(upTo-start+1, directBatchMax)
		got, more, err := directBatch(ctx, nc, subject, name, filter, start, int(batch), upTo)
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, got...)
		if !more || len(got) == 0 {
			break
		}
		start = got[len(got)-1].Sequence + 1
	}
	return msgs, nil
}

// directBatch sends one batched direct get and reads replies until the end of the batch or a message past upTo.
// more reports that the batch ended before upTo while the stream holds further messages.
func directBatch(ctx context.Context, nc *nats.Conn, subject, name, filter string, seq uint64, batch int, upTo uint64) (msgs []Message, more bool, err error) {
	// A callback subscription, because a sync one turns a stored message with an empty body and a
	// Status: 503 header into a no responders error before it can be told apart from a real one.
	replies := make(chan *nats.Msg, batch+2)
	sub, err := nc.Subscribe(nc.NewRespInbox(), func(m *nats.Msg) {
		select {
		case replies <- m:
		case <-ctx.Done():
		}
	})
	if err != nil {
		return nil, false, err
	}
	defer sub.Unsubscribe()

	req, err := json.Marshal(directGetRequest{Seq: seq, NextBySubj: filter, Batch: batch})
	if err != nil {
		return nil, false, err
	}
	if err := nc.PublishRequest(subject, sub.Subject, req); err != nil {
		return nil, false, err
	}
	for {
		var m *nats.Msg
		select {
		case m = <-replies:
		case <-ctx.Done():
			return nil, false, ctx.Err()
		}
		// Status replies carry no sequence; a stored message may have a Status header of its own.
		if status := m.Header.Get("Status"); status != "" && len(m.Data) == 0 && m.Header.Get(jetstream.SequenceHeader) == "" {
			switch status {
			case "204":
				pending, _ := strconv.ParseUint(m.Header.Get("Nats-Num-Pending"), 10, 64)
				return msgs, pending > 0, nil
			case "404":
				return msgs, false, nil
			case "503":
				return nil, false, nats.ErrNoResponders
			default:
				return nil, false, fmt.Errorf("direct get: %s %s", status, m.Header.Get("Description"))
			}
		}
		sq, err := strconv.ParseUint(m.Header.Get(jetstream.SequenceHeader), 10, 64)
		if err != nil {
			return nil, false, fmt.Errorf("direct get: invalid sequence header: %w", err)
		}
		if sq > upTo {
			return msgs, false, nil
		}
		ts, _ := time.Parse(time.RFC3339Nano, m.Header.Get(jetstream.TimeStampHeaer))
		msgs = append(msgs, Message{
			Stream:   name,
			Subject:  m.Header.Get(jetstream.SubjectHeader),
			Sequence: sq,
			Time:     ts,
			Data:     m.Data,
			Header:   stripDirectHeaders(m.Header),
		})
	}
}

// apiPrefix mirrors the prefix the jetstream client uses for its API requests.
func apiPrefix(js jetstream.JetStream) string {
	opts := js.Options()
	switch {
	case opts.APIPrefix != "":
		if strings.HasSuffix(opts.APIPrefix, ".") {
			return opts.APIPrefix
		}
		return opts.APIPrefix + "."
	case opts.Domain != "":
		return "$JS." + opts.Domain + ".API."
	default:
		return jetstream.DefaultAPIPrefix
	}
}

// serverAtLeast reports whether a "major.minor.patch" server version is at least major.minor.
func serverAtLeast(version string, major, minor int) bool {
	parts := strings.SplitN(strings.TrimPrefix(version, "v"), ".", 3)
	if len(parts) < 2 {
		return false
	}
	ma, err := strconv.Atoi(parts[0])
	if err != nil {
		return false
	}
	mi, err := strconv.Atoi(parts[1])
	if err != nil {
		return false
	}
	return ma > major || ma == major && mi >= minor
}

// PublishMessage publishes a message through JetStream and returns the stream's acknowledgement.
func PublishMessage(ctx context.Context, js jetstream.JetStream, subject string, header nats.Header, data []byte) (PubAck, error) {
	msg := nats.NewMsg(subject)
	msg.Data = data
	for k, values := range header {
		for _, v := range values {
			msg.Header.Add(k, v)
		}
	}
	ack, err := js.PublishMsg(ctx, msg)
	if err != nil {
		if errors.Is(err, nats.ErrNoResponders) || errors.Is(err, jetstream.ErrNoStreamResponse) {
			return PubAck{}, fmt.Errorf("no stream is listening on subject %q", subject)
		}
		return PubAck{}, err
	}
	return PubAck{Stream: ack.Stream, Sequence: ack.Sequence, Duplicate: ack.Duplicate}, nil
}

// DeleteMessages deletes the given sequences from a stream, returning the ones that were deleted
// together with an error for each that was not.
func DeleteMessages(ctx context.Context, js jetstream.JetStream, name string, seqs []uint64) ([]uint64, error) {
	s, err := js.Stream(ctx, name)
	if err != nil {
		return nil, err
	}
	return forEach(seqs, func(seq uint64) error {
		if err := s.DeleteMsg(ctx, seq); err != nil {
			return fmt.Errorf("deleting message %d: %w", seq, err)
		}
		return nil
	})
}
