package natsmap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	natsclient "github.com/theizzatbek/gokit/clients/nats"
	xerrs "github.com/theizzatbek/gokit/errs"
	"github.com/theizzatbek/gokit/reqctx"
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

	// batchedHandlerFns wraps a typed *batched* user handler — the
	// one whose signature is func(ctx, []natsclient.Msg[T]) error.
	// Each entry receives a slice of decoded *T pointers paired with
	// their metas; the shim assembles the []Msg[T] and dispatches.
	// Registered via RegisterBatchedHandler. Subscribers in
	// batched-mode (s.BatchSize > 0) MUST have a batched handler;
	// regular-mode subscribers MUST have a handlerFns entry.
	batchedHandlerFns map[string]func(ctx context.Context, ptrs []any, metas []msgMeta) error

	// publisherTypes maps publisher name → reflect.Type of the registered T.
	publisherTypes map[string]reflect.Type

	envMap map[string]string // nil = no overrides; LoadBytes builds composite lookup

	streams     rawStreamsBlock
	serverGroup string

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

// EngineOption configures Engine at construction time.
type EngineOption func(*Engine)

// WithEnv supplies explicit values for ${VAR} substitution at
// LoadBytes/LoadFile time. The map is consulted first; on miss the
// lookup falls back to os.LookupEnv. Pass to New(...). nil or empty
// map is a no-op (falls back to os.LookupEnv for every key).
func WithEnv(m map[string]string) EngineOption {
	return func(e *Engine) { e.envMap = m }
}

// WithServerGroup sets the server group label used for auto-suffixing
// subscriber queue groups. When set, an auto-derived queue group
// (empty durable + empty queue_group in YAML) becomes
// "<subscriber-name>-<server-group>" instead of just
// "<subscriber-name>". Lets the same service deployed across N regions
// process events independently per region.
//
// Explicit YAML queue_group values are NOT suffixed.
func WithServerGroup(group string) EngineOption {
	return func(e *Engine) { e.serverGroup = group }
}

// New returns an empty Engine.
func New(opts ...EngineOption) *Engine {
	e := &Engine{
		handlerTypes:      map[string]reflect.Type{},
		handlerFns:        map[string]func(ctx context.Context, ptr any, meta msgMeta) error{},
		batchedHandlerFns: map[string]func(ctx context.Context, ptrs []any, metas []msgMeta) error{},
		publisherTypes:    map[string]reflect.Type{},
	}
	for _, fn := range opts {
		fn(e)
	}
	return e
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
	cfg, err := parseBytes(b, e.envLookup())
	if err != nil {
		return err
	}
	e.subscribers = append(e.subscribers, cfg.Subscribers...)
	e.publishers = append(e.publishers, cfg.Publishers...)
	if cfg.Streams.Auto {
		e.streams.Auto = true
	}
	e.streams.List = append(e.streams.List, cfg.Streams.List...)
	return nil
}

// envLookup returns the composite ${VAR} resolver: engine map first,
// then os.LookupEnv. Returns nil when no map is set, letting
// substituteEnv use its default (os.LookupEnv).
func (e *Engine) envLookup() func(string) (string, bool) {
	if e.envMap == nil {
		return nil
	}
	return func(name string) (string, bool) {
		if v, ok := e.envMap[name]; ok {
			return v, true
		}
		return os.LookupEnv(name)
	}
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

// RegisterBatchedHandler records a batched handler whose signature
// is `func(ctx, []natsclient.Msg[T]) error`. Targets subscribers
// declared with `batch_size: N` in the YAML — at Build the kit
// switches them onto JetStream Pull subscription, fetches up to N
// messages with a deadline of batch_interval, hands them to the
// handler as one slice, then Acks all (on nil) or Naks all (on err)
// atomically.
//
// Pairs by name with the YAML subscriber entry; mismatched modes
// (regular handler against batched subscriber, or vice versa) fail
// at Build with CodeBatchHandlerRequired / CodeRegularHandlerRequired.
//
// Panics with *errs.Error on duplicate or post-Build registration.
func RegisterBatchedHandler[T any](e *Engine, name string,
	h func(ctx context.Context, batch []natsclient.Msg[T]) error) {
	if e.built {
		panic(xerrs.Validationf(CodeAlreadyBuilt,
			"natsmap: cannot register batched handler for %q after Build", name))
	}
	if _, exists := e.handlerTypes[name]; exists {
		panic(xerrs.Validationf(CodeDuplicateSubscriber,
			"natsmap: duplicate handler registration for %q", name))
	}
	t := reflect.TypeOf((*T)(nil)).Elem()
	e.handlerTypes[name] = t
	e.batchedHandlerFns[name] = func(ctx context.Context, ptrs []any, metas []msgMeta) error {
		msgs := make([]natsclient.Msg[T], len(ptrs))
		for i, ptr := range ptrs {
			data, ok := ptr.(*T)
			if !ok {
				return xerrs.Internalf(CodePublisherTypeMismatch,
					"natsmap: batched handler %q got wrong payload type %T at index %d", name, ptr, i)
			}
			meta := metas[i]
			msgs[i] = natsclient.Msg[T]{
				Data:         *data,
				Subject:      meta.Subject,
				Headers:      meta.Headers,
				Sequence:     meta.Sequence,
				Redeliveries: meta.Redeliveries,
				Reply:        meta.Reply,
				Timestamp:    meta.Timestamp,
			}
		}
		return h(ctx, msgs)
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

// Build opens every subscription and prepares every publisher. Returns
// *errs.Error (errors.Join when multiple problems co-occur). Calling
// Build twice returns CodeAlreadyBuilt.
func (e *Engine) Build(ctx context.Context, c *natsclient.Client, opts ...Option) (*Runtime, error) {
	if e.built {
		return nil, xerrs.Validation(CodeAlreadyBuilt,
			"natsmap: Engine.Build called twice")
	}
	cfg := &rawConfig{Subscribers: e.subscribers, Publishers: e.publishers, Streams: e.streams}
	if err := cfg.validate(e.handlerNameSet(), e.publisherNameSet()); err != nil {
		return nil, err
	}

	o := &options{}
	for _, fn := range opts {
		fn(o)
	}

	rt := &Runtime{
		publishers: map[string]publishShim{},
	}

	// Resolve streams: explicit list OR auto-derived from subjects.
	streamsToEnsure := e.streams.List
	if e.streams.Auto {
		streamsToEnsure = deriveStreamsFromSubjects(e.subscribers, e.publishers)
	}
	for i := range streamsToEnsure {
		s := &streamsToEnsure[i]
		streamCfg, err := buildStreamConfig(s)
		if err != nil {
			return nil, err
		}
		if err := c.EnsureStream(ctx, streamCfg); err != nil {
			return nil, xerrs.Wrapf(err, xerrs.KindUnavailable,
				CodeEnsureStreamFailed, "natsmap: ensure stream %q", s.Name)
		}
	}

	// Subscribers
	codec := c.Codec()
	var buildErrs []error
	for i := range e.subscribers {
		s := &e.subscribers[i]
		handlerType := e.handlerTypes[s.Name]
		_, hasRegular := e.handlerFns[s.Name]
		batchedFn, hasBatched := e.batchedHandlerFns[s.Name]
		durable, queueGroup := resolveDurableQueueGroup(s, e.serverGroup)

		// Mode cross-check: YAML batch_size declares the subscriber's
		// mode. A regular handler against a batched subscriber (or
		// vice versa) is a programmer error caught at Build.
		if s.BatchSize > 0 && !hasBatched {
			buildErrs = append(buildErrs, xerrs.Validationf(CodeBatchHandlerRequired,
				"natsmap: subscriber %q has batch_size=%d but no batched handler "+
					"(call RegisterBatchedHandler[T] instead of RegisterHandler[T])",
				s.Name, s.BatchSize))
			continue
		}
		if s.BatchSize == 0 && hasBatched {
			buildErrs = append(buildErrs, xerrs.Validationf(CodeRegularHandlerRequired,
				"natsmap: subscriber %q has no batch_size but a batched handler was registered "+
					"(set batch_size > 0 in YAML or call RegisterHandler[T])",
				s.Name))
			continue
		}

		if s.BatchSize > 0 {
			// Batched mode: JetStream Pull subscription + manual ack
			// per batch via the batchedHandlerFns shim.
			sub, err := openBatchedSubscriber(ctx, c, s, durable, handlerType, codec, batchedFn)
			if err != nil {
				buildErrs = append(buildErrs, err)
				continue
			}
			rt.subs = append(rt.subs, sub)
			rt.subscriberNames = append(rt.subscriberNames, s.Name)
			continue
		}

		// Regular mode: existing push-subscribe + auto-ack path.
		_ = hasRegular
		handlerFn := e.handlerFns[s.Name]
		subOpts, sferr := buildSubscribeOptions(s, durable, queueGroup)
		if sferr != nil {
			buildErrs = append(buildErrs, sferr)
			continue
		}
		raw := makeRawHandler(handlerType, codec, handlerFn)
		sub, err := natsclient.SubscribeRaw(ctx, c, s.Subject, raw, subOpts...)
		if err != nil {
			buildErrs = append(buildErrs, xerrs.Wrapf(err, xerrs.KindUnavailable,
				CodeSubscribeFailed, "natsmap: subscribe %q on %q", s.Name, s.Subject))
			continue
		}
		rt.subs = append(rt.subs, sub)
		rt.subscriberNames = append(rt.subscriberNames, s.Name)
	}

	// Publishers: expand map[string]string → map[string][]string for shim.
	for i := range e.publishers {
		p := &e.publishers[i]
		payloadType := e.publisherTypes[p.Name]
		subject := p.Subject
		staticHdrs := expandHeaders(p.Headers)
		rt.publishers[p.Name] = publishShim{
			subject:     subject,
			staticHdrs:  staticHdrs,
			payloadType: payloadType,
			publish: func(ctx context.Context, payload any, callHdrs map[string][]string) error {
				return natsclient.PublishViaCodec(ctx, c, subject, payload, callHdrs)
			},
			publishRaw: func(ctx context.Context, body []byte, callHdrs map[string][]string) error {
				return natsclient.PublishRaw(ctx, c, subject, body, callHdrs)
			},
		}
	}

	if err := errors.Join(buildErrs...); err != nil {
		return nil, err
	}
	e.built = true
	return rt, nil
}

// expandHeaders turns YAML's scalar map[string]string into the
// map[string][]string shape natsclient (and nats.Header) expects.
// Returns nil for an empty/absent map.
func expandHeaders(in map[string]string) map[string][]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]string, len(in))
	for k, v := range in {
		out[k] = []string{v}
	}
	return out
}

// makeRawHandler returns a natsclient.RawHandler that decodes the
// payload into a fresh *T (via reflect.New(handlerType)) and invokes
// the typed handler fn registered for the subscriber. Decode failures
// are wrapped with natsclient.ErrPoison so the dispatcher Terms the
// message instead of Nak'ing.
func makeRawHandler(t reflect.Type, codec natsclient.Codec,
	fn func(ctx context.Context, ptr any, meta msgMeta) error) natsclient.RawHandler {
	return func(ctx context.Context, m *natsclient.RawMsg) error {
		ptr := reflect.New(t)
		if err := codec.Unmarshal(m.Body, ptr.Interface()); err != nil {
			return fmt.Errorf("natsmap: decode failed: %w: %w", natsclient.ErrPoison, err)
		}
		if hdrs := m.Headers[reqctx.HeaderRequestID]; len(hdrs) > 0 && hdrs[0] != "" {
			ctx = reqctx.WithRequestID(ctx, hdrs[0])
		}
		meta := msgMeta{
			Subject:      m.Subject,
			Headers:      m.Headers,
			Sequence:     m.Sequence,
			Redeliveries: m.Redeliveries,
			Reply:        m.Reply,
			Timestamp:    m.Timestamp,
		}
		return fn(ctx, ptr.Interface(), meta)
	}
}

// resolveDurableQueueGroup applies the auto-default rules described in
// the multi-node README section:
//
//	durable=""           → durable = sub.Name
//	durable="ephemeral"  → durable = "" (true ephemeral)
//	durable=other        → durable = other (unchanged)
//
//	if durable was auto-derived AND queue_group is empty:
//	    queue_group = sub.Name (+ "-" + serverGroup if non-empty)
//
// Explicit queue_group is never suffixed; explicit durable does NOT
// trigger queue_group auto-derive (user controls durable → user
// controls qg).
func resolveDurableQueueGroup(s *rawSubscriber, serverGroup string) (durable, queueGroup string) {
	durable = s.Durable
	queueGroup = s.QueueGroup

	autoDurable := false
	if durable == "" {
		durable = s.Name
		autoDurable = true
	} else if durable == "ephemeral" {
		durable = ""
	}

	if autoDurable && queueGroup == "" {
		queueGroup = s.Name
		if serverGroup != "" {
			queueGroup += "-" + serverGroup
		}
	}
	return durable, queueGroup
}

// buildSubscribeOptions translates a rawSubscriber into the
// natsclient.SubscribeOption list. durable and queueGroup are
// pre-resolved by resolveDurableQueueGroup (applying auto-default and
// ServerGroup-suffix rules); buildSubscribeOptions reads neither
// s.Durable nor s.QueueGroup directly.
func buildSubscribeOptions(s *rawSubscriber, durable, queueGroup string) ([]natsclient.SubscribeOption, error) {
	var opts []natsclient.SubscribeOption
	if durable != "" {
		opts = append(opts, natsclient.WithDurable(durable))
	}
	if s.MaxInFlight > 0 {
		opts = append(opts, natsclient.WithMaxInFlight(s.MaxInFlight))
	}
	if s.MaxDeliver > 0 {
		opts = append(opts, natsclient.WithMaxDeliver(s.MaxDeliver))
	}
	if s.AckWait > 0 {
		opts = append(opts, natsclient.WithAckWait(s.AckWait))
	}
	if queueGroup != "" {
		opts = append(opts, natsclient.WithQueueGroup(queueGroup))
	}
	if s.FilterSubject != "" {
		opts = append(opts, natsclient.WithFilterSubject(s.FilterSubject))
	}
	if s.Backoff != nil {
		bo := buildBackoffFn(s.Backoff)
		opts = append(opts, natsclient.WithBackoff(bo))
	}
	if s.StartFrom != "" {
		policy, err := parseStartPolicy(s.StartFrom)
		if err != nil {
			return nil, err
		}
		if policy != nil {
			opts = append(opts, natsclient.WithStartFrom(policy))
		}
	}
	return opts, nil
}

func buildBackoffFn(b *rawBackoff) func(int) time.Duration {
	if strings.ToLower(b.Type) == "fixed" {
		base := b.Base
		return func(int) time.Duration { return base }
	}
	base := b.Base
	maxBackoff := b.Max
	if maxBackoff <= 0 {
		maxBackoff = base * 32
	}
	return func(redeliveries int) time.Duration {
		if redeliveries < 1 {
			return base
		}
		d := base << (redeliveries - 1)
		if d > maxBackoff || d <= 0 {
			return maxBackoff
		}
		return d
	}
}

func parseStartPolicy(s string) (natsclient.StartPolicy, error) {
	switch s {
	case "", "new":
		return natsclient.StartNew(), nil
	case "all":
		return natsclient.StartAll(), nil
	}
	if rest, ok := strings.CutPrefix(s, "from_seq:"); ok {
		seq, err := strconv.ParseUint(rest, 10, 64)
		if err != nil {
			return nil, xerrs.Wrapf(err, xerrs.KindValidation, CodeInvalidStartFrom,
				"natsmap: from_seq:%s — invalid sequence", rest)
		}
		return natsclient.StartFromSequence(seq), nil
	}
	if rest, ok := strings.CutPrefix(s, "from_time:"); ok {
		t, err := time.Parse(time.RFC3339, rest)
		if err != nil {
			return nil, xerrs.Wrapf(err, xerrs.KindValidation, CodeInvalidStartFrom,
				"natsmap: from_time:%s — must be RFC3339", rest)
		}
		return natsclient.StartFromTime(t), nil
	}
	return nil, xerrs.Validationf(CodeInvalidStartFrom,
		"natsmap: start_from %q invalid", s)
}

// buildStreamConfig translates rawStream → natsclient.StreamConfig.
// Returns *errs.Error for invalid storage / retention values.
func buildStreamConfig(s *rawStream) (natsclient.StreamConfig, error) {
	cfg := natsclient.StreamConfig{
		Name:     s.Name,
		Subjects: s.Subjects,
		MaxAge:   s.MaxAge,
		MaxBytes: s.MaxBytes,
		MaxMsgs:  s.MaxMsgs,
		Replicas: s.Replicas,
		Dedup:    s.Dedup,
	}
	switch strings.ToLower(s.Storage) {
	case "", "file":
		cfg.Storage = natsclient.StorageFile
	case "memory":
		cfg.Storage = natsclient.StorageMemory
	default:
		return cfg, xerrs.Validationf(CodeStreamInvalidStorage,
			"natsmap: stream %q storage %q invalid", s.Name, s.Storage)
	}
	switch strings.ToLower(s.Retention) {
	case "", "limits":
		cfg.Retention = natsclient.RetentionLimits
	case "interest":
		cfg.Retention = natsclient.RetentionInterest
	case "work_queue":
		cfg.Retention = natsclient.RetentionWorkQueue
	default:
		return cfg, xerrs.Validationf(CodeStreamInvalidRetention,
			"natsmap: stream %q retention %q invalid", s.Name, s.Retention)
	}
	return cfg, nil
}

// deriveStreamsFromSubjects walks subscriber + publisher subjects, groups
// by the first segment (text before the first dot), and returns one
// rawStream per group. Used by Engine.Build when `streams: auto` is set.
//
// Conventions:
//   - Group key = first segment (e.g. "orders.created" → "orders").
//   - Stream name = uppercase group key.
//   - Subjects = ["<group>.>"] for dotted inputs; for inputs without
//     dots, the literal subject is used.
//   - Defaults: Storage=File, Retention=Limits, MaxAge=0 (set by
//     buildStreamConfig from zero values).
func deriveStreamsFromSubjects(subs []rawSubscriber, pubs []rawPublisher) []rawStream {
	groups := map[string]string{} // group key (lowercase first segment) → subject pattern
	collect := func(subject string) {
		if subject == "" {
			return
		}
		dot := strings.IndexByte(subject, '.')
		if dot < 0 {
			groups[subject] = subject // literal fallback
			return
		}
		segment := subject[:dot]
		groups[segment] = segment + ".>"
	}
	for _, s := range subs {
		collect(s.Subject)
	}
	for _, p := range pubs {
		collect(p.Subject)
	}
	if len(groups) == 0 {
		return nil
	}
	out := make([]rawStream, 0, len(groups))
	for segment, subject := range groups {
		out = append(out, rawStream{
			Name:     strings.ToUpper(segment),
			Subjects: []string{subject},
		})
	}
	return out
}
