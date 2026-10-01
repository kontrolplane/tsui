package tsui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// pagingStreams creates a direct and a non-direct stream and fills both with the same messages, the
// subject of message i (1-based) given by subjectOf. It returns the stream names.
func pagingStreams(t *testing.T, js jetstream.JetStream, prefix string, n int, subjectOf func(i int) string) (string, string) {
	t.Helper()
	ctx := context.Background()
	names := []string{prefix + "_D", prefix + "_F"}
	for i, name := range names {
		if _, err := js.CreateStream(ctx, jetstream.StreamConfig{
			Name:        name,
			Subjects:    []string{fmt.Sprintf("%s%d.>", prefix, i)},
			Storage:     jetstream.MemoryStorage,
			AllowDirect: i == 0,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= n; i++ {
		for j := range names {
			msg := nats.NewMsg(fmt.Sprintf("%s%d.%s", prefix, j, subjectOf(i)))
			msg.Data = []byte(fmt.Sprint(i))
			msg.Header.Set("N", fmt.Sprint(i))
			// The default 200ms stall wait is too short for slow runners under -race.
			if _, err := js.PublishMsgAsync(msg, jetstream.WithStallWait(10*time.Second)); err != nil {
				t.Fatal(err)
			}
		}
	}
	select {
	case <-js.PublishAsyncComplete():
	case <-ctx.Done():
	}
	return names[0], names[1]
}

func deleteSeqs(t *testing.T, js jetstream.JetStream, seqs []uint64, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, err := DeleteMessages(context.Background(), js, name, seqs); err != nil {
			t.Fatal(err)
		}
	}
}

// pageAll pages through with fetch until next is 0, checking each page is newest first and below its cursor.
func pageAll(t *testing.T, fetch func(before uint64) ([]Message, uint64, error)) []uint64 {
	t.Helper()
	var (
		all    []uint64
		before uint64
	)
	for range 10000 {
		msgs, next, err := fetch(before)
		if err != nil {
			t.Fatal(err)
		}
		for i, m := range msgs {
			if before != 0 && m.Sequence >= before {
				t.Fatalf("page before %d returned %d", before, m.Sequence)
			}
			if i > 0 && m.Sequence >= msgs[i-1].Sequence {
				t.Fatalf("page before %d not newest first: %v", before, sequences(msgs))
			}
			if next != 0 && m.Sequence < next {
				t.Fatalf("page before %d returned %d below its next %d", before, m.Sequence, next)
			}
		}
		all = append(all, sequences(msgs)...)
		if next == 0 {
			return all
		}
		if before != 0 && next >= before {
			t.Fatalf("no progress: before %d next %d", before, next)
		}
		before = next
	}
	t.Fatal("paging did not finish")
	return nil
}

func TestListMessagesBeforePagesGappedStream(t *testing.T) {
	ctx := context.Background()
	js := runServer(t)

	const n = 3000
	direct, fallback := pagingStreams(t, js, "GP", n, func(i int) string {
		if i > 200 && i <= 2500 {
			return "drop"
		}
		return "keep"
	})
	del := []uint64{1, 150, 3000}
	for seq := uint64(2501); seq < 3000; seq += 3 {
		del = append(del, seq)
	}
	for i, name := range []string{direct, fallback} {
		if err := PurgeStream(ctx, js, name, fmt.Sprintf("GP%d.drop", i)); err != nil {
			t.Fatal(err)
		}
	}
	deleteSeqs(t, js, del, direct, fallback)

	var want []uint64
	for seq := uint64(n); seq >= 1; seq-- {
		if (seq <= 200 || seq > 2500) && !slices.Contains(del, seq) {
			want = append(want, seq)
		}
	}

	for _, name := range []string{direct, fallback} {
		for _, limit := range []int{7, 100, 333, 5000} {
			got := pageAll(t, func(before uint64) ([]Message, uint64, error) {
				return ListMessagesBefore(ctx, js, name, before, limit)
			})
			if !slices.Equal(got, want) {
				t.Errorf("%s limit %d: pages covered %d messages, want %d", name, limit, len(got), len(want))
			}
		}
		first, next, err := ListMessagesBefore(ctx, js, name, 0, 10)
		if err != nil {
			t.Fatal(err)
		}
		list, err := ListMessages(ctx, js, name, 10)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(sequences(first), sequences(list)) || next != first[len(first)-1].Sequence {
			t.Errorf("%s: before 0 returned %v next %d, ListMessages %v", name, sequences(first), next, sequences(list))
		}
		if msgs, next, err := ListMessagesBefore(ctx, js, name, 2, 10); err != nil || len(msgs) != 0 || next != 0 {
			t.Errorf("%s: before the first message returned %v, %d, %v", name, sequences(msgs), next, err)
		}
	}
}

func TestListSubjectMessagesInterleaved(t *testing.T) {
	ctx := context.Background()
	js := runServer(t)

	const n = 400
	tokens := []string{"a.x", "b.x", "a.y", "a.x", "c.z", "b.y", "a.x"}
	subjectOf := func(i int) string {
		if i > 100 && i <= 300 && i%50 != 0 {
			return "noise.q"
		}
		return tokens[i%len(tokens)]
	}
	direct, fallback := pagingStreams(t, js, "SP", n, subjectOf)
	var del []uint64
	for seq := uint64(3); seq <= n; seq += 11 {
		del = append(del, seq)
	}
	deleteSeqs(t, js, del, direct, fallback)

	for _, filter := range []string{"a.x", "a.*", "*.x", ">", "b.>", "c.z", "none"} {
		var want []uint64
		for i := n; i >= 1; i-- {
			if !slices.Contains(del, uint64(i)) && SubjectMatches(filter, subjectOf(i)) {
				want = append(want, uint64(i))
			}
		}
		for _, limit := range []int{3, 40, 3000} {
			var pages [2][]uint64
			for j, name := range []string{direct, fallback} {
				subject := fmt.Sprintf("SP%d.%s", j, filter)
				pages[j] = pageAll(t, func(before uint64) ([]Message, uint64, error) {
					msgs, next, err := ListSubjectMessages(ctx, js, name, subject, before, limit)
					for _, m := range msgs {
						if !SubjectMatches(subject, m.Subject) || string(m.Data) != fmt.Sprint(m.Sequence) || m.Header.Get("N") != fmt.Sprint(m.Sequence) || len(m.Header) != 1 {
							t.Fatalf("%s %s: unexpected message %+v", name, subject, m)
						}
					}
					return msgs, next, err
				})
				if !slices.Equal(pages[j], want) {
					t.Errorf("%s %q limit %d: got %d messages, want %d", name, filter, limit, len(pages[j]), len(want))
				}
			}
			if !slices.Equal(pages[0], pages[1]) {
				t.Errorf("%q limit %d: direct and fallback differ", filter, limit)
			}
		}

		// Without an up front count the walk still finds every match.
		for j, name := range []string{direct, fallback} {
			s, err := js.Stream(ctx, name)
			if err != nil {
				t.Fatal(err)
			}
			subject := fmt.Sprintf("SP%d.%s", j, filter)
			read, overRead := getMsgWindowReader(s, name, subject), 0
			if j == 0 {
				read, overRead = directWindowReader(js, name, subject), directBatchMax
			}
			got := pageAll(t, func(before uint64) ([]Message, uint64, error) {
				msgs, next, _, err := listSubject(ctx, read, s.CachedInfo().State, before, 20, 0, overRead)
				return msgs, next, err
			})
			if !slices.Equal(got, want) {
				t.Errorf("%s %q without count: got %d messages, want %d", name, filter, len(got), len(want))
			}
		}
	}
}

func TestListSubjectMessagesSparseInLargeStream(t *testing.T) {
	ctx := context.Background()
	js := runServer(t)

	const n = 20000
	rare := []int{3, 7, 12000}
	direct, fallback := pagingStreams(t, js, "SS", n, func(i int) string {
		if slices.Contains(rare, i) {
			return "rare"
		}
		return "noise"
	})

	for j, name := range []string{direct, fallback} {
		s, err := js.Stream(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		for _, filter := range []string{"rare", "*", "none"} {
			subject := fmt.Sprintf("SS%d.%s", j, filter)
			var want []uint64
			for i := n; i >= 1; i-- {
				if filter == "*" || filter == "rare" && slices.Contains(rare, i) {
					want = append(want, uint64(i))
				}
			}
			if len(want) > 10 {
				want = want[:10]
			}
			for _, count := range []uint64{0, uint64(len(rare))} {
				if filter != "rare" && count != 0 {
					continue
				}
				read, overRead := getMsgWindowReader(s, name, subject), 0
				if j == 0 {
					read, overRead = directWindowReader(js, name, subject), directBatchMax
				}
				msgs, next, requests, err := listSubject(ctx, read, s.CachedInfo().State, 0, 10, count, overRead)
				if err != nil {
					t.Fatal(err)
				}
				if !slices.Equal(sequences(msgs), want) {
					t.Errorf("%s %s count %d: got %v, want %v", name, filter, count, sequences(msgs), want)
				}
				if filter != "*" && next != 0 {
					t.Errorf("%s %s count %d: expected no older page, got next %d", name, filter, count, next)
				}
				if requests > 30 {
					t.Errorf("%s %s count %d: %d requests", name, filter, count, requests)
				}
			}
		}
		msgs, next, err := ListSubjectMessages(ctx, js, name, fmt.Sprintf("SS%d.rare", j), 12000, 10)
		if err != nil || !slices.Equal(sequences(msgs), []uint64{7, 3}) || next != 0 {
			t.Errorf("%s: before 12000 returned %v next %d err %v", name, sequences(msgs), next, err)
		}
	}
}

func TestGetMessage(t *testing.T) {
	ctx := context.Background()
	js := runServer(t)

	direct, fallback := pagingStreams(t, js, "GM", 5, func(int) string { return "m" })
	deleteSeqs(t, js, []uint64{3}, direct, fallback)

	for _, name := range []string{direct, fallback} {
		m, err := GetMessage(ctx, js, name, 4)
		if err != nil {
			t.Fatal(err)
		}
		if m.Sequence != 4 || m.Stream != name || string(m.Data) != "4" || len(m.Header) != 1 || m.Header.Get("N") != "4" || m.Time.IsZero() {
			t.Errorf("%s: unexpected message %+v", name, m)
		}
		for _, seq := range []uint64{0, 3, 6, 1 << 40} {
			_, err := GetMessage(ctx, js, name, seq)
			if !errors.Is(err, ErrMessageNotFound) || !errors.Is(err, jetstream.ErrMsgNotFound) {
				t.Errorf("%s seq %d: expected not found, got %v", name, seq, err)
			}
		}
	}
	if _, err := GetMessage(ctx, js, "MISSING", 1); !errors.Is(err, jetstream.ErrStreamNotFound) {
		t.Errorf("expected stream not found, got %v", err)
	}
}
