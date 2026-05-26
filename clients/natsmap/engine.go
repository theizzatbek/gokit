package natsmap

import (
	"context"
	"os"
	"reflect"
	"time"

	natsclient "github.com/theizzatbek/gokit/clients/nats"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// Engine is the build-once configurator. Lifecycle:
//
//	New → LoadFile (n) → RegisterHandler/Publisher (n) → Build → Runtime → Drain
type Engine struct {
	subscribers []rawSubscriber
	publishers  []rawPublisher

	// handlerTypes maps subscriber name → reflect.Type of the registered T.
	handlerTypes map[string]reflect.Type
	// handlerFns wraps the typed user handler as a non-generic dispatcher.
	// ptr is *T (pointer to fresh-allocated T); meta carries the
	// JetStream metadata Task 5's reflection bridge populates.
	handlerFns map[string]func(ctx context.Context, ptr any, meta msgMeta) error

	// publisherTypes maps publisher name → reflect.Type of the registered T.
	publisherTypes map[string]reflect.Type

	built bool
}

// msgMeta is the metadata passed alongside the decoded payload into the
// reflected handler shim — mirrors natsclient.Msg[T] minus Data and Raw.
// natsmap-routed Msg[T].Raw() returns nil; users needing the underlying
// *nats.Msg should use natsclient.Subscribe[T] directly.
type msgMeta struct {
	Subject      string
	Headers      map[string][]string
	Sequence     uint64
	Redeliveries int
	Reply        string
	Timestamp    time.Time
}

// New returns an empty Engine.
func New() *Engine {
	return &Engine{
		handlerTypes:   map[string]reflect.Type{},
		handlerFns:     map[string]func(ctx context.Context, ptr any, meta msgMeta) error{},
		publisherTypes: map[string]reflect.Type{},
	}
}

// LoadFile reads a YAML file (subscribers, publishers, or both) and
// appends its entries.
func (e *Engine) LoadFile(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return xerrs.Wrap(err, xerrs.KindValidation, CodeReadFile,
			"natsmap: read yaml file: "+path)
	}
	return e.LoadBytes(b)
}

// LoadBytes parses and appends YAML content. May be called multiple
// times — entries from each call accumulate into one engine.
func (e *Engine) LoadBytes(b []byte) error {
	cfg, err := parseBytes(b)
	if err != nil {
		return err
	}
	e.subscribers = append(e.subscribers, cfg.Subscribers...)
	e.publishers = append(e.publishers, cfg.Publishers...)
	return nil
}

// RegisterHandler records a typed handler for the subscriber named.
// Panics with *errs.Error on duplicate or post-Build registration.
func RegisterHandler[T any](e *Engine, name string,
	h func(ctx context.Context, m natsclient.Msg[T]) error) {
	if e.built {
		panic(xerrs.Validationf(CodeAlreadyBuilt,
			"natsmap: cannot register handler for %q after Build", name))
	}
	if _, exists := e.handlerTypes[name]; exists {
		panic(xerrs.Validationf(CodeDuplicateSubscriber,
			"natsmap: duplicate handler registration for %q", name))
	}
	t := reflect.TypeOf((*T)(nil)).Elem()
	e.handlerTypes[name] = t
	e.handlerFns[name] = func(ctx context.Context, ptr any, meta msgMeta) error {
		// ptr is *T (pointer to freshly allocated T) from Task 5's reflect.New(T).Interface().
		data, ok := ptr.(*T)
		if !ok {
			return xerrs.Internalf(CodePublisherTypeMismatch,
				"natsmap: handler %q got wrong payload type %T", name, ptr)
		}
		msg := natsclient.Msg[T]{
			Data:         *data,
			Subject:      meta.Subject,
			Headers:      meta.Headers,
			Sequence:     meta.Sequence,
			Redeliveries: meta.Redeliveries,
			Reply:        meta.Reply,
			Timestamp:    meta.Timestamp,
		}
		return h(ctx, msg)
	}
}

// RegisterPublisher records the Go type used by natsmap.Publish[T] for
// the named publisher. Panics on duplicate or post-Build registration.
func RegisterPublisher[T any](e *Engine, name string) {
	if e.built {
		panic(xerrs.Validationf(CodeAlreadyBuilt,
			"natsmap: cannot register publisher for %q after Build", name))
	}
	if _, exists := e.publisherTypes[name]; exists {
		panic(xerrs.Validationf(CodeDuplicatePublisher,
			"natsmap: duplicate publisher registration for %q", name))
	}
	e.publisherTypes[name] = reflect.TypeOf((*T)(nil)).Elem()
}

// handlerNameSet returns the registered handler names as a set —
// used by validate() to cross-check YAML entries.
func (e *Engine) handlerNameSet() map[string]struct{} {
	out := make(map[string]struct{}, len(e.handlerTypes))
	for k := range e.handlerTypes {
		out[k] = struct{}{}
	}
	return out
}

// publisherNameSet returns the registered publisher names as a set.
func (e *Engine) publisherNameSet() map[string]struct{} {
	out := make(map[string]struct{}, len(e.publisherTypes))
	for k := range e.publisherTypes {
		out[k] = struct{}{}
	}
	return out
}
