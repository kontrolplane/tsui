package tsui

import (
	"context"
	"fmt"
	"sort"

	"github.com/nats-io/nats.go/jetstream"
)

func consumerFromInfo(info *jetstream.ConsumerInfo) Consumer {
	cfg := info.Config
	c := Consumer{
		Stream:         info.Stream,
		Name:           info.Name,
		Durable:        cfg.Durable != "",
		Description:    cfg.Description,
		FilterSubjects: cfg.FilterSubjects,
		DeliverPolicy:  deliverPolicyName(cfg.DeliverPolicy),
		AckPolicy:      ackPolicyName(cfg.AckPolicy),
		AckWait:        cfg.AckWait,
		MaxDeliver:     cfg.MaxDeliver,
		MaxAckPending:  cfg.MaxAckPending,
		Push:           cfg.DeliverSubject != "",
		PushBound:      info.PushBound,
		Paused:         info.Paused,
		Created:        info.Created,

		Delivered:      info.Delivered.Stream,
		AckFloor:       info.AckFloor.Stream,
		NumPending:     info.NumPending,
		NumAckPending:  info.NumAckPending,
		NumRedelivered: info.NumRedelivered,
		NumWaiting:     info.NumWaiting,
	}
	if cfg.FilterSubject != "" {
		c.FilterSubjects = append([]string{cfg.FilterSubject}, c.FilterSubjects...)
	}
	if info.Delivered.Last != nil {
		c.LastActive = *info.Delivered.Last
	}
	return c
}

func deliverPolicyName(p jetstream.DeliverPolicy) string {
	switch p {
	case jetstream.DeliverLastPolicy:
		return "last"
	case jetstream.DeliverNewPolicy:
		return "new"
	case jetstream.DeliverByStartSequencePolicy:
		return "by seq"
	case jetstream.DeliverByStartTimePolicy:
		return "by time"
	case jetstream.DeliverLastPerSubjectPolicy:
		return "last/subj"
	default:
		return "all"
	}
}

func ackPolicyName(p jetstream.AckPolicy) string {
	switch p {
	case jetstream.AckNonePolicy:
		return "none"
	case jetstream.AckAllPolicy:
		return "all"
	default:
		return "explicit"
	}
}

// ListConsumers returns all consumers of a stream, sorted by name.
func ListConsumers(ctx context.Context, js jetstream.JetStream, stream string) ([]Consumer, error) {
	s, err := js.Stream(ctx, stream)
	if err != nil {
		return nil, err
	}
	lister := s.ListConsumers(ctx)
	var consumers []Consumer
	for info := range lister.Info() {
		consumers = append(consumers, consumerFromInfo(info))
	}
	if err := lister.Err(); err != nil {
		return nil, err
	}
	sort.Slice(consumers, func(i, j int) bool { return consumers[i].Name < consumers[j].Name })
	return consumers, nil
}

// GetConsumer returns a single consumer of a stream.
func GetConsumer(ctx context.Context, js jetstream.JetStream, stream, name string) (Consumer, error) {
	c, err := js.Consumer(ctx, stream, name)
	if err != nil {
		return Consumer{}, err
	}
	return consumerFromInfo(c.CachedInfo()), nil
}

func DeleteConsumer(ctx context.Context, js jetstream.JetStream, stream, name string) error {
	return js.DeleteConsumer(ctx, stream, name)
}

// DeleteConsumers deletes the given consumers of a stream, returning the ones that were deleted
// together with an error for each that was not.
func DeleteConsumers(ctx context.Context, js jetstream.JetStream, stream string, names []string) ([]string, error) {
	return forEach(names, func(name string) error {
		if err := js.DeleteConsumer(ctx, stream, name); err != nil {
			return fmt.Errorf("deleting consumer %s: %w", name, err)
		}
		return nil
	})
}
