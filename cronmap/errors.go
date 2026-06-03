package cronmap

// Stable error Code constants returned in *errs.Error.Code from
// LoadFile / LoadBytes / Build / RegisterHandler. Runtime errors
// (Start, Stop) and per-job invocation errors propagate as their
// underlying type — only configuration/wiring failures get a kit Code.
const (
	// CodeMissingName — a YAML jobs[i] entry has no `name:` field.
	CodeMissingName = "cronmap_missing_name"

	// CodeMissingHandler — a YAML jobs[i] entry has no `handler:` field.
	CodeMissingHandler = "cronmap_missing_handler"

	// CodeMissingSchedule — a YAML jobs[i] entry has no `schedule:` field.
	CodeMissingSchedule = "cronmap_missing_schedule"

	// CodeInvalidSchedule — the configured parser refused `schedule:`.
	// Cause wraps the parser's error so callers can inspect it.
	CodeInvalidSchedule = "cronmap_invalid_schedule"

	// CodeInvalidTimeout — `timeout:` parses but is negative. Zero /
	// missing is fine (no per-run deadline).
	CodeInvalidTimeout = "cronmap_invalid_timeout"

	// CodeDuplicateJob — two jobs share the same name. Names key
	// metrics + the singleton lock; silent duplication would corrupt
	// both.
	CodeDuplicateJob = "cronmap_duplicate_job"

	// CodeUnknownHandler — a job references a handler name that was
	// never passed to RegisterHandler. Surfaced at Build, not at first
	// tick, so misconfiguration fails at boot.
	CodeUnknownHandler = "cronmap_unknown_handler"

	// CodeSingletonNeedsLocker — a job sets singleton: true but Build
	// was not called with [WithSingletonLocker]. Without a locker the
	// "exactly one of N pods runs this" guarantee cannot be made.
	CodeSingletonNeedsLocker = "cronmap_singleton_needs_locker"

	// CodeAlreadyBuilt — Engine.Build was called twice on the same
	// engine. The post-Build engine is sealed (handlers + jobs
	// immutable) so a second Build would silently shadow registrations.
	CodeAlreadyBuilt = "cronmap_already_built"

	// CodeAlreadyRegistered — RegisterHandler was called twice with
	// the same name. Panics (programmer-error convention shared with
	// fibermap.RegisterHandler).
	CodeAlreadyRegistered = "cronmap_already_registered"

	// CodeRuntimeStopped — Runtime.Start called after Runtime.Stop.
	// Runtimes are single-use; build a fresh engine to restart.
	CodeRuntimeStopped = "cronmap_runtime_stopped"

	// CodeEnvVarUnset — ${VAR} substitution at LoadBytes could not
	// resolve VAR via the engine's WithEnv map or os.LookupEnv.
	CodeEnvVarUnset = "cronmap_env_var_unset"

	// CodeEnvVarMalformed — a ${...} block had bad syntax (e.g.
	// unterminated, invalid identifier).
	CodeEnvVarMalformed = "cronmap_env_var_malformed"

	// CodeUnknownJob — Stats / NextRun / PauseJob / ResumeJob /
	// TriggerJob were called with a name that was not in the
	// runtime's plan.
	CodeUnknownJob = "cronmap_unknown_job"
)
