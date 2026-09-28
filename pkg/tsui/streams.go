package tsui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/nats-io/nats.go/jetstream"
)

func streamFromInfo(info *jetstream.StreamInfo) Stream {
	cfg := info.Config
	s := Stream{
		Name:              cfg.Name,
		Description:       cfg.Description,
		Subjects:          cfg.Subjects,
		Storage:           strings.ToLower(cfg.Storage.String()),
		Retention:         retentionName(cfg.Retention),
		Discard:           discardName(cfg.Discard),
		Replicas:          cfg.Replicas,
		MaxMsgs:           cfg.MaxMsgs,
		MaxBytes:          cfg.MaxBytes,
		MaxAge:            cfg.MaxAge,
		MaxMsgsPerSubject: cfg.MaxMsgsPerSubject,
		MaxMsgSize:        cfg.MaxMsgSize,
		Duplicates:        cfg.Duplicates,
		AllowDirect:       cfg.AllowDirect,
		DenyDelete:        cfg.DenyDelete,
		DenyPurge:         cfg.DenyPurge,
		Sealed:            cfg.Sealed,
		Created:           info.Created,

		Messages:    info.State.Msgs,
		Bytes:       info.State.Bytes,
		FirstSeq:    info.State.FirstSeq,
		FirstTime:   info.State.FirstTime,
		LastSeq:     info.State.LastSeq,
		LastTime:    info.State.LastTime,
		NumDeleted:  info.State.NumDeleted,
		NumSubjects: info.State.NumSubjects,
		Consumers:   info.State.Consumers,
	}
	if cfg.Mirror != nil {
		s.Mirror = cfg.Mirror.Name
	}
	for _, src := range cfg.Sources {
		if src != nil {
			s.Sources = append(s.Sources, src.Name)
		}
	}
	if info.Cluster != nil {
		s.Leader = info.Cluster.Leader
	}
	return s
}

// retentionName and discardName avoid the verbose String() output of the jetstream enums ("Limits", "DiscardOld").
func retentionName(r jetstream.RetentionPolicy) string {
	switch r {
	case jetstream.InterestPolicy:
		return "interest"
	case jetstream.WorkQueuePolicy:
		return "workqueue"
	default:
		return "limits"
	}
}

func discardName(d jetstream.DiscardPolicy) string {
	if d == jetstream.DiscardNew {
		return "new"
	}
	return "old"
}

// ListStreams returns all streams in the account, sorted by name.
func ListStreams(ctx context.Context, js jetstream.JetStream) ([]Stream, error) {
	lister := js.ListStreams(ctx)
	var streams []Stream
	for info := range lister.Info() {
		streams = append(streams, streamFromInfo(info))
	}
	if err := lister.Err(); err != nil {
		return nil, err
	}
	sort.Slice(streams, func(i, j int) bool { return streams[i].Name < streams[j].Name })
	return streams, nil
}

func GetStream(ctx context.Context, js jetstream.JetStream, name string) (Stream, error) {
	s, err := js.Stream(ctx, name)
	if err != nil {
		return Stream{}, err
	}
	return streamFromInfo(s.CachedInfo()), nil
}

func (c StreamConfig) jetstreamConfig() (jetstream.StreamConfig, error) {
	cfg := jetstream.StreamConfig{
		Name:              c.Name,
		Description:       c.Description,
		Subjects:          c.Subjects,
		Replicas:          c.Replicas,
		MaxMsgs:           c.MaxMsgs,
		MaxBytes:          c.MaxBytes,
		MaxAge:            c.MaxAge,
		MaxMsgsPerSubject: c.MaxMsgsPerSubject,
		MaxMsgSize:        c.MaxMsgSize,
		MaxConsumers:      -1,
		Duplicates:        c.Duplicates,
		AllowDirect:       c.AllowDirect,
		DenyDelete:        c.DenyDelete,
		DenyPurge:         c.DenyPurge,
	}

	switch strings.ToLower(c.Storage) {
	case "", "file":
		cfg.Storage = jetstream.FileStorage
	case "memory":
		cfg.Storage = jetstream.MemoryStorage
	default:
		return cfg, fmt.Errorf("unknown storage type %q", c.Storage)
	}

	switch strings.ToLower(c.Retention) {
	case "", "limits":
		cfg.Retention = jetstream.LimitsPolicy
	case "interest":
		cfg.Retention = jetstream.InterestPolicy
	case "workqueue":
		cfg.Retention = jetstream.WorkQueuePolicy
	default:
		return cfg, fmt.Errorf("unknown retention policy %q", c.Retention)
	}

	switch strings.ToLower(c.Discard) {
	case "", "old":
		cfg.Discard = jetstream.DiscardOld
	case "new":
		cfg.Discard = jetstream.DiscardNew
	default:
		return cfg, fmt.Errorf("unknown discard policy %q", c.Discard)
	}

	return cfg, nil
}

func CreateStream(ctx context.Context, js jetstream.JetStream, c StreamConfig) (Stream, error) {
	cfg, err := c.jetstreamConfig()
	if err != nil {
		return Stream{}, err
	}
	s, err := js.CreateStream(ctx, cfg)
	if err != nil {
		return Stream{}, err
	}
	return streamFromInfo(s.CachedInfo()), nil
}

func DeleteStream(ctx context.Context, js jetstream.JetStream, name string) error {
	return js.DeleteStream(ctx, name)
}

// DeleteStreams deletes the given streams, returning the ones that were deleted together with an
// error for each that was not.
func DeleteStreams(ctx context.Context, js jetstream.JetStream, names []string) ([]string, error) {
	return forEach(names, func(name string) error {
		if err := js.DeleteStream(ctx, name); err != nil {
			return fmt.Errorf("deleting stream %s: %w", name, err)
		}
		return nil
	})
}

// PurgeStream removes all messages from a stream, or only those matching subject when it is set.
func PurgeStream(ctx context.Context, js jetstream.JetStream, name, subject string) error {
	s, err := js.Stream(ctx, name)
	if err != nil {
		return err
	}
	var opts []jetstream.StreamPurgeOpt
	if subject != "" {
		opts = append(opts, jetstream.WithPurgeSubject(subject))
	}
	return s.Purge(ctx, opts...)
}

// MaxListedSubjects bounds the subjects fetched for a stream, the server pages through them otherwise.
const MaxListedSubjects = 10000

// ListSubjects returns the subjects a stream holds messages on, busiest first.
func ListSubjects(ctx context.Context, js jetstream.JetStream, name string) ([]SubjectCount, error) {
	s, err := js.Stream(ctx, name)
	if err != nil {
		return nil, err
	}
	info, err := s.Info(ctx, jetstream.WithSubjectFilter(">"))
	if err != nil {
		return nil, err
	}
	subjects := make([]SubjectCount, 0, len(info.State.Subjects))
	for subject, n := range info.State.Subjects {
		subjects = append(subjects, SubjectCount{Subject: subject, Messages: n})
	}
	sort.Slice(subjects, func(i, j int) bool {
		if subjects[i].Messages != subjects[j].Messages {
			return subjects[i].Messages > subjects[j].Messages
		}
		return subjects[i].Subject < subjects[j].Subject
	})
	return subjects, nil
}
