package webhooks

import (
	"github.com/google/uuid"
)

// Event is the input to Fanout.HandleEvent. EventID dedupes
// fan-outs via UNIQUE (subscription_id, event_id) in
// webhook_deliveries — passing the same Event.ID twice creates
// no duplicate rows.
type Event struct {
	ID        uuid.UUID
	EventType string
	Payload   []byte
	Headers   map[string][]string
}
