package natsclient

import (
	"time"

	"github.com/nats-io/nats.go"
)

// Msg is what a Subscribe handler receives — the decoded payload plus JetStream
// metadata. Use Raw() to escape into the underlying *nats.Msg for cases the
// typed API doesn't cover (manual Ack/Nak/Term, custom header parsing).
type Msg[T any] struct {
	Data         T
	Subject      string
	Headers      map[string][]string
	Sequence     uint64
	Redeliveries int
	Reply        string
	Timestamp    time.Time
	raw          *nats.Msg
}

// Raw returns the underlying *nats.Msg.
func (m Msg[T]) Raw() *nats.Msg { return m.raw }
