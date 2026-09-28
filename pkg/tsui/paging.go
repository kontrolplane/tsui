package tsui

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/nats-io/nats.go/jetstream"
)

// ErrMessageNotFound is returned by GetMessage when the stream holds no message at the sequence.
var ErrMessageNotFound = fmt.Errorf("no such message: %w", jetstream.ErrMsgNotFound)

// maxSubjectCountSubjects bounds the distinct subjects a stream may hold for a wildcard filter to
// still be counted up front.
const maxSubjectCountSubjects = 1000

// subjectRequestBase and subjectRequestPerMsg bound the requests one ListSubjectMessages call makes
// once it has read at least one window.
const (
	subjectRequestBase   = 64
	subjectRequestPerMsg = 4
)

// ListMessagesBefore returns up to limit of the newest messages with a sequence below before, newest
// first; before 0 starts from the last message. next is the before to pass for the following, older
// page, or 0 when nothing older remains. A page may hold fewer than limit messages, or none, while
// next is not 0: the lookup stops after a bounded amount of work on streams with long gaps.
func ListMessagesBefore(ctx context.Context, js jetstream.JetStream, name string, before uint64, limit int) ([]Message, uint64, error) {
	s, err := js.Stream(ctx, name)
	if err != nil {
		return nil, 0, err
	}
	info := s.CachedInfo()
	upTo, ok := upperBound(info.State, before)
	if !ok || limit <= 0 {
		return nil, 0, nil
	}
	if useDirect(js, info) {
		return listDirectBefore(ctx, js, name, info.State, upTo, limit)
	}
	return listFallbackBefore(ctx, s, name, info.State, upTo, limit)
}

// ListSubjectMessages returns up to limit of the newest messages whose subject matches subject, which
// may contain wildcards, with a sequence below before (0 for no bound), newest first. next is the
// before to pass for the following, older page, or 0 when no older match remains. The work per call
// is bounded, so a page may hold fewer than limit messages, or none, while next is not 0.
func ListSubjectMessages(ctx context.Context, js jetstream.JetStream, name, subject string, before uint64, limit int) ([]Message, uint64, error) {
	s, err := js.Stream(ctx, name)
	if err != nil {
		return nil, 0, err
	}
	info := s.CachedInfo()
	if _, ok := upperBound(info.State, before); !ok || limit <= 0 || subject == "" {
		return nil, 0, nil
	}
	count, known, err := countSubject(ctx, s, info, subject)
	if err != nil {
		return nil, 0, err
	}
	if known && count == 0 {
		return nil, 0, nil
	}
	read, overRead := getMsgWindowReader(s, name, subject), 0
	if useDirect(js, info) {
		read, overRead = directWindowReader(js, name, subject), directBatchMax
	}
	msgs, next, _, err := listSubject(ctx, read, info.State, before, limit, count, overRead)
	return msgs, next, err
}

// GetMessage returns the message at seq, or an error wrapping ErrMessageNotFound when there is none.
func GetMessage(ctx context.Context, js jetstream.JetStream, name string, seq uint64) (Message, error) {
	s, err := js.Stream(ctx, name)
	if err != nil {
		return Message{}, err
	}
	if seq == 0 {
		return Message{}, fmt.Errorf("message %d: %w", seq, ErrMessageNotFound)
	}
	// GetMsg uses a direct get when the stream allows it.
	raw, err := s.GetMsg(ctx, seq)
	if errors.Is(err, jetstream.ErrMsgNotFound) {
		return Message{}, fmt.Errorf("message %d: %w", seq, ErrMessageNotFound)
	}
	if err != nil {
		return Message{}, err
	}
	return toMessage(name, raw), nil
}

func useDirect(js jetstream.JetStream, info *jetstream.StreamInfo) bool {
	return info.Config.AllowDirect && serverAtLeast(js.Conn().ConnectedServerVersion(), 2, 11)
}

// upperBound returns the highest sequence below before, reporting false when there is none to read.
func upperBound(state jetstream.StreamState, before uint64) (uint64, bool) {
	upTo := state.LastSeq
	if before != 0 {
		upTo = min(upTo, before-1)
	}
	return upTo, state.Msgs > 0 && upTo >= state.FirstSeq && upTo > 0
}

// countSubject returns how many messages in the stream match subject, when that is cheap to ask:
// for a literal subject, or a wildcard in a stream with few distinct subjects.
func countSubject(ctx context.Context, s jetstream.Stream, info *jetstream.StreamInfo, subject string) (uint64, bool, error) {
	if HasWildcard(subject) && info.State.NumSubjects > maxSubjectCountSubjects {
		return 0, false, nil
	}
	fi, err := s.Info(ctx, jetstream.WithSubjectFilter(subject))
	if err != nil {
		return 0, false, err
	}
	var n uint64
	for _, c := range fi.State.Subjects {
		n += c
	}
	return n, true, nil
}

// windowReader reads up to max messages matching the filter with a sequence in [start, upTo], oldest
// first. complete reports that the window held no further matches; requests counts round trips.
type windowReader func(ctx context.Context, start, upTo uint64, max int) (msgs []Message, complete bool, requests int, err error)

// directWindowReader reads windows with batched direct gets filtered by next_by_subj (nats-server 2.11+).
func directWindowReader(js jetstream.JetStream, name, subject string) windowReader {
	nc, api := js.Conn(), directSubject(js, name)
	return func(ctx context.Context, start, upTo uint64, max int) ([]Message, bool, int, error) {
		ctx, cancel := withDirectTimeout(ctx)
		defer cancel()
		var (
			msgs     []Message
			requests int
		)
		for start <= upTo && len(msgs) < max {
			batch := min(max-len(msgs), directBatchMax)
			got, more, err := directBatch(ctx, nc, api, name, subject, start, batch, upTo)
			requests++
			if err != nil {
				return nil, false, requests, err
			}
			msgs = append(msgs, got...)
			if !more || len(got) == 0 {
				return msgs, true, requests, nil
			}
			start = got[len(got)-1].Sequence + 1
		}
		return msgs, start > upTo, requests, nil
	}
}

// getMsgWindowReader reads windows one next-by-subject lookup at a time, each skipping any gap and
// any message on another subject.
func getMsgWindowReader(s jetstream.Stream, name, subject string) windowReader {
	return func(ctx context.Context, start, upTo uint64, max int) ([]Message, bool, int, error) {
		var (
			msgs     []Message
			requests int
		)
		for start <= upTo && len(msgs) < max {
			raw, err := s.GetMsg(ctx, start, jetstream.WithGetMsgSubject(subject))
			requests++
			if errors.Is(err, jetstream.ErrMsgNotFound) {
				return msgs, true, requests, nil
			}
			if err != nil {
				return nil, false, requests, err
			}
			if raw.Sequence > upTo {
				return msgs, true, requests, nil
			}
			msgs = append(msgs, toMessage(name, raw))
			start = raw.Sequence + 1
		}
		return msgs, start > upTo, requests, nil
	}
}

// listSubject walks back from before in windows, reading each forward with read and keeping the
// newest matches. count is the number of matches in the whole stream, or 0 when unknown, and sizes the
// first window. overRead lets a read return that many matches beyond those still needed, which is
// worth it when reads are batched. Windows grow while they come up short and are halved when they hold more matches than
// one read may return, so a sparse subject in a large stream costs a logarithmic number of windows.
func listSubject(ctx context.Context, read windowReader, state jetstream.StreamState, before uint64, limit int, count uint64, overRead int) (msgs []Message, next uint64, requests int, err error) {
	hi, ok := upperBound(state, before)
	if !ok || limit <= 0 {
		return nil, 0, 0, nil
	}
	lo := state.FirstSeq
	span := uint64(limit)
	if count > 0 {
		// Size the first window to hold about limit matches if they were spread evenly.
		span = max(span, (state.LastSeq-lo+1)/count*uint64(limit))
	}
	budget := subjectRequestBase + subjectRequestPerMsg*limit

	var (
		upTo    = hi
		covered = hi + 1 // every sequence in [covered, hi] has been examined
	)
	for len(msgs) < limit && covered > lo {
		if requests >= budget && covered <= hi {
			break
		}
		need := limit - len(msgs)
		start := lo
		if upTo-lo+1 > span {
			start = upTo - span + 1
		}
		window, complete, n, err := read(ctx, start, upTo, need+overRead)
		requests += n
		if err != nil {
			return nil, 0, requests, err
		}
		if !complete {
			// More matches than one read returns: only the newest are wanted, so narrow the window.
			span = max(1, (upTo-start+1)/2)
			continue
		}
		if len(window) > need {
			window = window[len(window)-need:]
			start = window[0].Sequence
		}
		covered = start
		slices.Reverse(window)
		msgs = append(msgs, window...)
		if start == lo {
			break
		}
		upTo = start - 1
		if len(window) < need {
			span = min(span*4, 1<<62)
		}
	}
	if count > 0 && before == 0 && uint64(len(msgs)) >= count {
		return msgs, 0, requests, nil
	}
	return msgs, nextBefore(covered, lo), requests, nil
}
