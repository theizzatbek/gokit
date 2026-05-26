package natsmap

import (
	"errors"
	"fmt"
	"strings"
	"time"

	xerrs "github.com/theizzatbek/gokit/errs"
	"gopkg.in/yaml.v3"
)

// rawConfig mirrors the top-level YAML document. Either or both blocks
// may be present; multi-file loads (LoadFile called more than once)
// append into a single engine, validated together at Build.
type rawConfig struct {
	Subscribers []rawSubscriber `yaml:"subscribers"`
	Publishers  []rawPublisher  `yaml:"publishers"`
	Streams     rawStreamsBlock `yaml:"streams"`
}

// rawSubscriber is one declared subscription.
type rawSubscriber struct {
	Name          string        `yaml:"name"`
	Subject       string        `yaml:"subject"`
	Durable       string        `yaml:"durable,omitempty"`
	MaxInFlight   int           `yaml:"max_in_flight,omitempty"`
	MaxDeliver    int           `yaml:"max_deliver,omitempty"`
	AckWait       time.Duration `yaml:"ack_wait,omitempty"`
	QueueGroup    string        `yaml:"queue_group,omitempty"`
	Backoff       *rawBackoff   `yaml:"backoff,omitempty"`
	StartFrom     string        `yaml:"start_from,omitempty"`
	FilterSubject string        `yaml:"filter_subject,omitempty"`
}

// rawPublisher is one declared publisher.
type rawPublisher struct {
	Name    string            `yaml:"name"`
	Subject string            `yaml:"subject"`
	Headers map[string]string `yaml:"headers,omitempty"`
}

// rawBackoff is the per-subscriber backoff override.
type rawBackoff struct {
	Type string        `yaml:"type"` // exponential | fixed
	Base time.Duration `yaml:"base"`
	Max  time.Duration `yaml:"max"`
}

// rawStream is one declared JetStream stream that natsmap.Build will
// EnsureStream against the live connection.
type rawStream struct {
	Name      string        `yaml:"name"`
	Subjects  []string      `yaml:"subjects"`
	Storage   string        `yaml:"storage,omitempty"`   // "file" (default) | "memory"
	Retention string        `yaml:"retention,omitempty"` // "limits" (default) | "interest" | "work_queue"
	MaxAge    time.Duration `yaml:"max_age,omitempty"`
	MaxBytes  int64         `yaml:"max_bytes,omitempty"`
	MaxMsgs   int64         `yaml:"max_msgs,omitempty"`
	Replicas  int           `yaml:"replicas,omitempty"`
	Dedup     time.Duration `yaml:"dedup,omitempty"`
}

// rawStreamsBlock represents the YAML `streams:` field, which may be
// either a scalar `auto` or a list of rawStream entries.
type rawStreamsBlock struct {
	Auto bool
	List []rawStream
}

// UnmarshalYAML accepts either the scalar `auto` or a sequence.
func (b *rawStreamsBlock) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		if n.Value == "auto" {
			b.Auto = true
			return nil
		}
		return fmt.Errorf("natsmap: streams scalar must be `auto`, got %q", n.Value)
	case yaml.SequenceNode:
		return n.Decode(&b.List)
	default:
		return fmt.Errorf("natsmap: streams must be `auto` or a list, got %v", n.Tag)
	}
}

var validStartFromPrefix = map[string]struct{}{
	"":    {},
	"new": {},
	"all": {},
}

// validate aggregates field-level failures, plus cross-checks against
// any handler/publisher type registrations.
//
// handlerNames + publisherNames are the names users wired up via
// RegisterHandler[T] / RegisterPublisher[T]. Pass nil if no
// registrations to check.
func (c *rawConfig) validate(handlerNames, publisherNames map[string]struct{}) error {
	var errsAcc []error
	seenSub := map[string]struct{}{}
	for i := range c.Subscribers {
		s := &c.Subscribers[i]
		if s.Name == "" {
			errsAcc = append(errsAcc, xerrs.Validationf(CodeMissingName,
				"natsmap: subscribers[%d] missing name", i))
		} else if _, dup := seenSub[s.Name]; dup {
			errsAcc = append(errsAcc, xerrs.Validationf(CodeDuplicateSubscriber,
				"natsmap: duplicate subscriber name %q", s.Name))
		} else {
			seenSub[s.Name] = struct{}{}
		}
		if s.Subject == "" {
			errsAcc = append(errsAcc, xerrs.Validationf(CodeMissingSubject,
				"natsmap: subscriber %q missing subject", s.Name))
		}
		if s.MaxInFlight < 0 {
			errsAcc = append(errsAcc, xerrs.Validationf(CodeInvalidMaxInFlight,
				"natsmap: subscriber %q max_in_flight must be >= 0 (got %d)", s.Name, s.MaxInFlight))
		}
		if s.MaxDeliver < 0 {
			errsAcc = append(errsAcc, xerrs.Validationf(CodeInvalidMaxDeliver,
				"natsmap: subscriber %q max_deliver must be >= 0 (got %d)", s.Name, s.MaxDeliver))
		}
		if s.AckWait < 0 {
			errsAcc = append(errsAcc, xerrs.Validationf(CodeInvalidAckWait,
				"natsmap: subscriber %q ack_wait must be >= 0", s.Name))
		}
		if s.Backoff != nil {
			t := strings.ToLower(s.Backoff.Type)
			if t != "exponential" && t != "fixed" {
				errsAcc = append(errsAcc, xerrs.Validationf(CodeInvalidBackoff,
					"natsmap: subscriber %q backoff.type %q not in {exponential, fixed}", s.Name, s.Backoff.Type))
			}
			if s.Backoff.Base <= 0 {
				errsAcc = append(errsAcc, xerrs.Validationf(CodeInvalidBackoff,
					"natsmap: subscriber %q backoff.base must be > 0", s.Name))
			}
			if s.Backoff.Max < s.Backoff.Base {
				errsAcc = append(errsAcc, xerrs.Validationf(CodeInvalidBackoff,
					"natsmap: subscriber %q backoff.max (%s) < base (%s)", s.Name, s.Backoff.Max, s.Backoff.Base))
			}
		}
		if !isValidStartFrom(s.StartFrom) {
			errsAcc = append(errsAcc, xerrs.Validationf(CodeInvalidStartFrom,
				"natsmap: subscriber %q start_from %q invalid (expected new|all|from_seq:<int>|from_time:<RFC3339>)",
				s.Name, s.StartFrom))
		}
	}
	seenPub := map[string]struct{}{}
	for i := range c.Publishers {
		p := &c.Publishers[i]
		if p.Name == "" {
			errsAcc = append(errsAcc, xerrs.Validationf(CodeMissingName,
				"natsmap: publishers[%d] missing name", i))
		} else if _, dup := seenPub[p.Name]; dup {
			errsAcc = append(errsAcc, xerrs.Validationf(CodeDuplicatePublisher,
				"natsmap: duplicate publisher name %q", p.Name))
		} else {
			seenPub[p.Name] = struct{}{}
		}
		if p.Subject == "" {
			errsAcc = append(errsAcc, xerrs.Validationf(CodeMissingSubject,
				"natsmap: publisher %q missing subject", p.Name))
		}
	}

	// Cross-check registrations.
	if handlerNames != nil {
		for name := range seenSub {
			if _, ok := handlerNames[name]; !ok {
				errsAcc = append(errsAcc, xerrs.Validationf(CodeHandlerNotRegistered,
					"natsmap: subscriber %q has no registered handler (call RegisterHandler[T])", name))
			}
		}
		for name := range handlerNames {
			if _, ok := seenSub[name]; !ok {
				errsAcc = append(errsAcc, xerrs.Validationf(CodeHandlerUnknown,
					"natsmap: RegisterHandler for %q has no matching subscriber in YAML", name))
			}
		}
	}
	if publisherNames != nil {
		for name := range seenPub {
			if _, ok := publisherNames[name]; !ok {
				errsAcc = append(errsAcc, xerrs.Validationf(CodePublisherNotRegistered,
					"natsmap: publisher %q has no registered type (call RegisterPublisher[T])", name))
			}
		}
		for name := range publisherNames {
			if _, ok := seenPub[name]; !ok {
				errsAcc = append(errsAcc, xerrs.Validationf(CodePublisherUnknown,
					"natsmap: RegisterPublisher for %q has no matching publisher in YAML", name))
			}
		}
	}

	// streams
	if c.Streams.Auto && len(c.Streams.List) > 0 {
		errsAcc = append(errsAcc, xerrs.Validationf(CodeStreamsAutoConflict,
			"natsmap: streams cannot be both `auto` and an explicit list"))
	}
	seenStream := map[string]struct{}{}
	for i := range c.Streams.List {
		s := &c.Streams.List[i]
		if s.Name == "" {
			errsAcc = append(errsAcc, xerrs.Validationf(CodeStreamMissingName,
				"natsmap: streams[%d] missing name", i))
		} else if _, dup := seenStream[s.Name]; dup {
			errsAcc = append(errsAcc, xerrs.Validationf(CodeStreamDuplicateName,
				"natsmap: duplicate stream name %q", s.Name))
		} else {
			seenStream[s.Name] = struct{}{}
		}
		if len(s.Subjects) == 0 {
			errsAcc = append(errsAcc, xerrs.Validationf(CodeStreamMissingSubjects,
				"natsmap: stream %q has no subjects", s.Name))
		}
		switch strings.ToLower(s.Storage) {
		case "", "file", "memory":
		default:
			errsAcc = append(errsAcc, xerrs.Validationf(CodeStreamInvalidStorage,
				"natsmap: stream %q storage %q not in {file, memory}", s.Name, s.Storage))
		}
		switch strings.ToLower(s.Retention) {
		case "", "limits", "interest", "work_queue":
		default:
			errsAcc = append(errsAcc, xerrs.Validationf(CodeStreamInvalidRetention,
				"natsmap: stream %q retention %q not in {limits, interest, work_queue}", s.Name, s.Retention))
		}
	}
	return errors.Join(errsAcc...)
}

func isValidStartFrom(s string) bool {
	if _, ok := validStartFromPrefix[s]; ok {
		return true
	}
	if strings.HasPrefix(s, "from_seq:") || strings.HasPrefix(s, "from_time:") {
		return true
	}
	return false
}
