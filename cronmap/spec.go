package cronmap

import (
	"errors"
	"strings"
	"time"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// rawConfig mirrors the top-level YAML document. Multi-file loads
// (LoadFile called multiple times) append rawConfig.Jobs into the
// engine's flat list, validated together at Build.
type rawConfig struct {
	Jobs []rawJob `yaml:"jobs"`
}

// rawJob is one cron declaration. Fields map 1:1 to the YAML schema
// documented in 2026-06-02-cronmap-design.md.
type rawJob struct {
	Name       string        `yaml:"name"`
	Handler    string        `yaml:"handler"`
	Schedule   string        `yaml:"schedule"`
	Timeout    time.Duration `yaml:"timeout,omitempty"`
	Singleton  bool          `yaml:"singleton,omitempty"`
	SentrySlug string        `yaml:"sentry_slug,omitempty"`

	// MaxRetries (0 = no retry — back-compat) caps how many times
	// the runtime re-invokes the handler when it returns a non-nil
	// error. A timeout outcome ALSO counts as failure for retry
	// purposes; a panic likewise (the kit recovers and the next
	// retry runs against a fresh ctx). Successful retries surface as
	// `success` in metrics.
	MaxRetries int `yaml:"max_retries,omitempty"`

	// RetryBackoff is the initial delay between attempts. The
	// runtime doubles it on each attempt up to RetryBackoff × 8 as a
	// soft cap. 0 (default) = no wait between retries (use only on
	// idempotent + cheap upstreams).
	RetryBackoff time.Duration `yaml:"retry_backoff,omitempty"`
}

// validate runs per-job validation that doesn't depend on engine
// state (schedule parser, registered handlers, locker presence).
// Engine-level cross-checks (uniqueness, unknown handler refs,
// singleton without locker) happen in Engine.Build through
// errors.Join so callers see every problem at once.
func (j rawJob) validate(idx int) error {
	var errsAcc []error
	if strings.TrimSpace(j.Name) == "" {
		errsAcc = append(errsAcc, xerrs.Validationf(CodeMissingName,
			"cronmap: jobs[%d] missing name", idx))
	}
	if strings.TrimSpace(j.Handler) == "" {
		errsAcc = append(errsAcc, xerrs.Validationf(CodeMissingHandler,
			"cronmap: jobs[%d] (%q) missing handler", idx, j.Name))
	}
	if strings.TrimSpace(j.Schedule) == "" {
		errsAcc = append(errsAcc, xerrs.Validationf(CodeMissingSchedule,
			"cronmap: jobs[%d] (%q) missing schedule", idx, j.Name))
	}
	if j.Timeout < 0 {
		errsAcc = append(errsAcc, xerrs.Validationf(CodeInvalidTimeout,
			"cronmap: jobs[%d] (%q) timeout must be >= 0, got %v", idx, j.Name, j.Timeout))
	}
	if j.MaxRetries < 0 {
		errsAcc = append(errsAcc, xerrs.Validationf(CodeInvalidTimeout,
			"cronmap: jobs[%d] (%q) max_retries must be >= 0, got %d", idx, j.Name, j.MaxRetries))
	}
	if j.RetryBackoff < 0 {
		errsAcc = append(errsAcc, xerrs.Validationf(CodeInvalidTimeout,
			"cronmap: jobs[%d] (%q) retry_backoff must be >= 0, got %v", idx, j.Name, j.RetryBackoff))
	}
	return errors.Join(errsAcc...)
}
