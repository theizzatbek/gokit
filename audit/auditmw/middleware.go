package auditmw

import (
	"fmt"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/audit"
)

// Options tunes [Middleware].
type Options struct {
	// IncludeMethods lists HTTP verbs to audit. Default: POST,
	// PUT, PATCH, DELETE. Add "GET" via [WithIncludeMethods] when
	// compliance regime requires reads to be logged.
	IncludeMethods map[string]bool

	// SkipPaths is a set of exact-match paths that are never
	// audited. Default empty; typical use:
	// /healthz, /readyz, /metrics, /preflight.
	SkipPaths map[string]bool

	// SubjectFn extracts the actor subject from the request. nil
	// leaves Actor.Subject empty (anonymous events). Typical:
	// pull from auth.From[C].
	SubjectFn func(*fiber.Ctx) string

	// ActionFn overrides the default verb-routing. Default:
	// "<METHOD>.<route-pattern>" — POST./api/tasks.
	ActionFn func(*fiber.Ctx) string

	// TargetFn extracts the target Actor was acting on. Default:
	// Target.Type = first path-segment, Target.ID = the most
	// specific path-param.
	TargetFn func(*fiber.Ctx) audit.Target

	// MetadataFn returns extra free-form metadata stamped onto
	// every event. Useful for: tenant id, feature flags active,
	// request id.
	MetadataFn func(*fiber.Ctx) map[string]any
}

// Option mutates Options. Compose via the variadic argument of
// [Middleware].
type Option func(*Options)

// WithIncludeMethods sets the audited HTTP verbs. Pass the verbs
// uppercase. Replaces the default set entirely.
func WithIncludeMethods(verbs ...string) Option {
	return func(o *Options) {
		o.IncludeMethods = make(map[string]bool, len(verbs))
		for _, v := range verbs {
			o.IncludeMethods[strings.ToUpper(v)] = true
		}
	}
}

// WithSkipPaths excludes exact-match paths from auditing. Pass
// multiple times to append; each call APPENDS, not replaces, so
// kit-supplied defaults can coexist with user additions.
func WithSkipPaths(paths ...string) Option {
	return func(o *Options) {
		if o.SkipPaths == nil {
			o.SkipPaths = map[string]bool{}
		}
		for _, p := range paths {
			o.SkipPaths[p] = true
		}
	}
}

// WithSubject wires the actor-subject extractor.
func WithSubject(fn func(*fiber.Ctx) string) Option {
	return func(o *Options) { o.SubjectFn = fn }
}

// WithAction wires a custom Action-verb extractor.
func WithAction(fn func(*fiber.Ctx) string) Option {
	return func(o *Options) { o.ActionFn = fn }
}

// WithTarget wires a custom Target extractor.
func WithTarget(fn func(*fiber.Ctx) audit.Target) Option {
	return func(o *Options) { o.TargetFn = fn }
}

// WithMetadata wires a per-event metadata stamper.
func WithMetadata(fn func(*fiber.Ctx) map[string]any) Option {
	return func(o *Options) { o.MetadataFn = fn }
}

// Middleware returns a Fiber handler that emits one audit event per
// matching request. logger MUST be non-nil; pass [audit.NewMemoryStore]
// when wiring in unit tests, [audit.New(auditpg.New(db), ...)] in
// production.
//
// Audit failures are logged but never propagate — an audit-store
// blip cannot turn a write into a 500 (HIPAA / SOC2 audit logs
// should be reliable, but availability of the audited workload
// matters more during an audit-backend outage).
func Middleware(logger *audit.Logger, opts ...Option) fiber.Handler {
	o := Options{
		IncludeMethods: map[string]bool{
			fiber.MethodPost:   true,
			fiber.MethodPut:    true,
			fiber.MethodPatch:  true,
			fiber.MethodDelete: true,
		},
	}
	for _, opt := range opts {
		opt(&o)
	}
	return func(c *fiber.Ctx) error {
		next := c.Next()
		if logger == nil {
			return next
		}
		method := c.Method()
		if !o.IncludeMethods[method] {
			return next
		}
		path := c.OriginalURL()
		if i := strings.IndexByte(path, '?'); i > 0 {
			path = path[:i]
		}
		if o.SkipPaths[path] {
			return next
		}

		actor := audit.Actor{IP: c.IP(), UA: c.Get(fiber.HeaderUserAgent)}
		if o.SubjectFn != nil {
			actor.Subject = o.SubjectFn(c)
		}

		action := defaultAction(c)
		if o.ActionFn != nil {
			action = o.ActionFn(c)
		}

		target := defaultTarget(c)
		if o.TargetFn != nil {
			target = o.TargetFn(c)
		}

		status := c.Response().StatusCode()
		outcome := outcomeFromStatus(status)
		var meta map[string]any
		if o.MetadataFn != nil {
			meta = o.MetadataFn(c)
		}
		if meta == nil {
			meta = map[string]any{}
		}
		meta["status"] = status

		_, _ = logger.Log(c.UserContext(), audit.Event{
			Actor:    actor,
			Action:   action,
			Target:   target,
			Outcome:  outcome,
			Metadata: meta,
		})
		return next
	}
}

// defaultAction returns "<METHOD>.<route-pattern>" — the
// route-pattern is Fiber's resolved pattern (e.g. "/tasks/:id") so
// individual IDs don't blow up the action cardinality.
func defaultAction(c *fiber.Ctx) string {
	pattern := c.Route().Path
	if pattern == "" {
		pattern = c.OriginalURL()
	}
	return fmt.Sprintf("%s.%s", c.Method(), pattern)
}

// defaultTarget pulls the first path-segment as Target.Type and
// the most specific :param as Target.ID.
//
// Example: route `/tasks/:id/comments/:commentID`, request
// `/tasks/42/comments/9` → Target{Type:"tasks", ID:"9"}.
func defaultTarget(c *fiber.Ctx) audit.Target {
	path := c.Path()
	if i := strings.IndexByte(path[1:], '/'); i >= 0 {
		t := audit.Target{Type: path[1 : 1+i]}
		// Most-specific param == last AllParams entry.
		params := c.AllParams()
		if len(params) > 0 {
			// Prefer "id"-like keys for stable Target.ID.
			for _, k := range []string{"id", "ID", "uuid", "UUID"} {
				if v, ok := params[k]; ok {
					t.ID = v
					return t
				}
			}
			// Otherwise — last param wins. Map iteration is
			// non-deterministic, so collect keys + take last.
			var lastVal string
			for _, v := range params {
				lastVal = v
			}
			t.ID = lastVal
		}
		return t
	}
	return audit.Target{Type: strings.TrimPrefix(path, "/")}
}

// outcomeFromStatus maps HTTP status to audit.Outcome.
//
//	2xx       → success
//	401, 403  → denied
//	other 4xx → failure
//	5xx       → failure
func outcomeFromStatus(s int) audit.Outcome {
	switch {
	case s >= 200 && s < 300:
		return audit.Success
	case s == 401 || s == 403:
		return audit.Denied
	default:
		return audit.Failure
	}
}
