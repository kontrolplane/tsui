package tsui

import (
	"time"

	"github.com/nats-io/nats.go"
)

// docs: https://docs.nats.io/nats-concepts/jetstream/streams#configuration
type Stream struct {
	Name              string
	Description       string
	Subjects          []string
	Storage           string
	Retention         string
	Discard           string
	Replicas          int
	MaxMsgs           int64
	MaxBytes          int64
	MaxAge            time.Duration
	MaxMsgsPerSubject int64
	MaxMsgSize        int32
	Duplicates        time.Duration
	AllowDirect       bool
	DenyDelete        bool
	DenyPurge         bool
	Sealed            bool
	Mirror            string
	Sources           []string
	Created           time.Time

	Messages    uint64
	Bytes       uint64
	FirstSeq    uint64
	FirstTime   time.Time
	LastSeq     uint64
	LastTime    time.Time
	NumDeleted  int
	NumSubjects uint64
	Consumers   int
	Leader      string
}

// StreamConfig holds the settings exposed in the stream creation form.
type StreamConfig struct {
	Name              string
	Description       string
	Subjects          []string
	Storage           string
	Retention         string
	Discard           string
	Replicas          int
	MaxMsgs           int64
	MaxBytes          int64
	MaxAge            time.Duration
	MaxMsgsPerSubject int64
	MaxMsgSize        int32
	Duplicates        time.Duration
	AllowDirect       bool
	DenyDelete        bool
	DenyPurge         bool
}

type Message struct {
	Stream   string
	Subject  string
	Sequence uint64
	Time     time.Time
	Header   nats.Header
	Data     []byte
}

// PubAck is the stream's acknowledgement of a published message.
type PubAck struct {
	Stream    string
	Sequence  uint64
	Duplicate bool
}

// docs: https://docs.nats.io/nats-concepts/jetstream/consumers#configuration
type Consumer struct {
	Stream         string
	Name           string
	Durable        bool
	Description    string
	FilterSubjects []string
	DeliverPolicy  string
	AckPolicy      string
	AckWait        time.Duration
	MaxDeliver     int
	MaxAckPending  int
	Push           bool
	PushBound      bool
	Paused         bool
	Created        time.Time

	Delivered      uint64
	AckFloor       uint64
	NumPending     uint64
	NumAckPending  int
	NumRedelivered int
	NumWaiting     int
	LastActive     time.Time
}

// SubjectCount is the number of messages a stream holds on one subject.
type SubjectCount struct {
	Subject  string
	Messages uint64
}
