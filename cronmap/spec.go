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
	return errors.Join(errsAcc...)
}
