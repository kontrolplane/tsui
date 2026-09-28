package tsui

import (
	"context"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Server describes the server the connection is attached to and the JetStream usage of the account.
type Server struct {
	Name      string
	Version   string
	Cluster   string
	RTT       time.Duration
	Domain    string
	Streams   int
	Consumers int
	Memory    uint64
	Store     uint64
	MaxMemory int64
	MaxStore  int64
}

// GetServer measures the round trip to the server and reads the JetStream account info.
func GetServer(ctx context.Context, nc *nats.Conn, js jetstream.JetStream) (Server, error) {
	s := Server{
		Name:    nc.ConnectedServerName(),
		Version: nc.ConnectedServerVersion(),
		Cluster: nc.ConnectedClusterName(),
	}
	rtt, err := nc.RTT()
	if err != nil {
		return s, err
	}
	s.RTT = rtt

	info, err := js.AccountInfo(ctx)
	if err != nil {
		return s, err
	}
	s.Domain = info.Domain
	s.Streams = info.Streams
	s.Consumers = info.Consumers
	s.Memory = info.Memory
	s.Store = info.Store
	s.MaxMemory = info.Limits.MaxMemory
	s.MaxStore = info.Limits.MaxStore
	return s, nil
}
