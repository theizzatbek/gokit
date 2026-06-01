package audit

import (
	"crypto/sha256"
	"encoding/json"
	"sort"
	"time"
)

// Outcome is the result of the audited action.
type Outcome string

const (
	// Success — the action was performed successfully.
	Success Outcome = "success"

	// Failure — the action was attempted and failed at execution
	// (DB error, downstream timeout, etc.). NOT an authorization
	// rejection.
	Failure Outcome = "failure"

	// Denied — the action was rejected at the authorization layer
	// (missing scope/role, ownership check, rate-limit). Often the
	// most security-relevant entries — keep them.
	Denied Outcome = "denied"
)

// Actor is who performed the action. Subject is typically the
// authenticated user-ID; IP / UA capture the network origin for
// forensic-friendly logs.
type Actor struct {
	Subject string `json:"subject,omitempty"`
	Type    string `json:"type,omitempty"` // user|service|system
	IP      string `json:"ip,omitempty"`
	UA      string `json:"ua,omitempty"`
}

// Target is what was acted on. Type is the resource kind
// ("user", "post"), ID the resource identifier, Name an optional
// human-readable label that survives ID renames.
type Target struct {
	Type string `json:"type,omitempty"`
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

// Event is one entry in the audit log. ID + Timestamp + Hash /
// PrevHash are server-set at Append time; callers populate the
// semantic fields (Actor / Action / Target / Outcome / Metadata).
//
// The Hash field is the SHA-256 of the canonical JSON encoding of
// the event (excluding Hash itself) XOR-prepended with PrevHash.
// Stores that don't opt into the hash chain leave Hash + PrevHash
// nil.
type Event struct {
	ID          string         `json:"id,omitempty"`
	OccurredAt  time.Time      `json:"occurred_at"`
	ServiceName string         `json:"service_name,omitempty"`
	Actor       Actor          `json:"actor"`
	Action      string         `json:"action"`
	Target      Target         `json:"target,omitempty"`
	Outcome     Outcome        `json:"outcome"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	PrevHash    []byte         `json:"prev_hash,omitempty"`
	Hash        []byte         `json:"hash,omitempty"`
}

// validate checks the minimum semantic contract. Server-set fields
// are not checked here — those are filled at Append time.
func (e Event) validate() error {
	if e.Action == "" {
		return newInvalidEventError("Action is required")
	}
	if e.Outcome == "" {
		return newInvalidEventError("Outcome is required")
	}
	return nil
}

// canonicalHashInput returns the deterministic byte representation
// of Event minus Hash, used as the SHA-256 input for chaining.
// Map keys are sorted so JSON encoding is reproducible regardless
// of insertion order.
//
// Excluded: Hash (we're computing it). Included: every other field.
func (e Event) canonicalHashInput() ([]byte, error) {
	type alias struct {
		ID          string          `json:"id,omitempty"`
		OccurredAt  string          `json:"occurred_at"` // RFC3339Nano for cross-language reproducibility
		ServiceName string          `json:"service_name,omitempty"`
		Actor       Actor           `json:"actor"`
		Action      string          `json:"action"`
		Target      Target          `json:"target,omitempty"`
		Outcome     Outcome         `json:"outcome"`
		Metadata    json.RawMessage `json:"metadata,omitempty"`
		PrevHash    string          `json:"prev_hash,omitempty"`
	}
	var metaRaw json.RawMessage
	if len(e.Metadata) > 0 {
		raw, err := encodeSortedMap(e.Metadata)
		if err != nil {
			return nil, err
		}
		metaRaw = raw
	}
	a := alias{
		ID:          e.ID,
		OccurredAt:  e.OccurredAt.UTC().Format(time.RFC3339Nano),
		ServiceName: e.ServiceName,
		Actor:       e.Actor,
		Action:      e.Action,
		Target:      e.Target,
		Outcome:     e.Outcome,
		Metadata:    metaRaw,
		PrevHash:    hashHex(e.PrevHash),
	}
	return json.Marshal(a)
}

// computeHash returns SHA-256(canonical-bytes). Chain semantics:
// PrevHash is folded into the canonical input, so flipping any byte
// of any earlier event invalidates every Hash downstream.
func (e *Event) computeHash() ([]byte, error) {
	raw, err := e.canonicalHashInput()
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	return sum[:], nil
}

// encodeSortedMap JSON-encodes m with keys in sorted order so the
// canonical bytes are deterministic.
func encodeSortedMap(m map[string]any) ([]byte, error) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ordered := make([]any, 0, len(keys)*2)
	for _, k := range keys {
		ordered = append(ordered, k, m[k])
	}
	// Use a {k:v,k:v,...} object explicitly so JSON shape stays a
	// map (not an array) — readers can decode straight into
	// map[string]any.
	var b []byte
	b = append(b, '{')
	for i, k := range keys {
		if i > 0 {
			b = append(b, ',')
		}
		kj, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		b = append(b, kj...)
		b = append(b, ':')
		vj, err := json.Marshal(m[k])
		if err != nil {
			return nil, err
		}
		b = append(b, vj...)
	}
	b = append(b, '}')
	return b, nil
}

// hashHex returns the lowercase hex form of h, or "" for nil.
// Hex (not base64) because Postgres bytea round-trips well to hex
// and visual diff'ing chains is easier.
func hashHex(h []byte) string {
	if len(h) == 0 {
		return ""
	}
	const hex = "0123456789abcdef"
	out := make([]byte, len(h)*2)
	for i, b := range h {
		out[i*2] = hex[b>>4]
		out[i*2+1] = hex[b&0x0f]
	}
	return string(out)
}
