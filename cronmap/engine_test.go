package cronmap

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/robfig/cron/v3"

	xerrs "github.com/theizzatbek/gokit/errs"
)

func nopHandler(context.Context) error { return nil }

func TestBuild_HappyMinimal(t *testing.T) {
	t.Parallel()
	e := New()
	if err := e.LoadBytes([]byte(`
jobs:
  - name: hourly-ping
    handler: ping
    schedule: "@hourly"
`)); err != nil {
		t.Fatal(err)
	}
	RegisterHandler(e, "ping", nopHandler)
	rt, err := e.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := rt.JobNames(); len(got) != 1 || got[0] != "hourly-ping" {
		t.Errorf("JobNames = %v, want [hourly-ping]", got)
	}
}

func TestBuild_EmptyJobs_AllowsBuild(t *testing.T) {
	t.Parallel()
	e := New()
	if err := e.LoadBytes([]byte(`jobs: []`)); err != nil {
		t.Fatal(err)
	}
	rt, err := e.Build()
	if err != nil {
		t.Fatalf("Build empty: %v", err)
	}
	if got := rt.JobNames(); len(got) != 0 {
		t.Errorf("empty Build returned %d jobs, want 0", len(got))
	}
}

func TestBuild_ValidationErrorsAggregate(t *testing.T) {
	t.Parallel()
	e := New()
	if err := e.LoadBytes([]byte(`
jobs:
  - name: ""
    handler: ""
    schedule: ""
  - name: dup-a
    handler: known
    schedule: "@hourly"
  - name: dup-a
    handler: known
    schedule: "@hourly"
  - name: orphan
    handler: missing-fn
    schedule: "@hourly"
  - name: bad-schedule
    handler: known
    schedule: "obviously not a cron expr"
`)); err != nil {
		t.Fatal(err)
	}
	RegisterHandler(e, "known", nopHandler)

	_, err := e.Build()
	if err == nil {
		t.Fatal("expected aggregated errors")
	}
	// All six buckets should surface in one pass — assert by Code substring.
	for _, code := range []string{
		CodeMissingName,
		CodeMissingHandler,
		CodeMissingSchedule,
		CodeDuplicateJob,
		CodeUnknownHandler,
		CodeInvalidSchedule,
	} {
		if !strings.Contains(err.Error(), code) {
			t.Errorf("missing %q in aggregated err: %v", code, err)
		}
	}
}

func TestBuild_NegativeTimeoutRejected(t *testing.T) {
	t.Parallel()
	e := New()
	if err := e.LoadBytes([]byte(`
jobs:
  - name: neg
    handler: ping
    schedule: "@hourly"
    timeout: -5s
`)); err != nil {
		t.Fatal(err)
	}
	RegisterHandler(e, "ping", nopHandler)
	_, err := e.Build()
	if err == nil {
		t.Fatal("expected error for negative timeout")
	}
	if !strings.Contains(err.Error(), CodeInvalidTimeout) {
		t.Errorf("err = %v, want %q", err, CodeInvalidTimeout)
	}
}

func TestBuild_SingletonWithoutLockerRejected(t *testing.T) {
	t.Parallel()
	e := New()
	if err := e.LoadBytes([]byte(`
jobs:
  - name: solo
    handler: ping
    schedule: "@hourly"
    singleton: true
`)); err != nil {
		t.Fatal(err)
	}
	RegisterHandler(e, "ping", nopHandler)
	_, err := e.Build()
	var xe *xerrs.Error
	if !errors.As(err, &xe) {
		t.Fatalf("want *errs.Error, got %T (%v)", err, err)
	}
	if !strings.Contains(err.Error(), CodeSingletonNeedsLocker) {
		t.Errorf("want %q, got %v", CodeSingletonNeedsLocker, err)
	}
}

func TestBuild_SingletonWithLocker_OK(t *testing.T) {
	t.Parallel()
	e := New()
	if err := e.LoadBytes([]byte(`
jobs:
  - name: solo
    handler: ping
    schedule: "@hourly"
    singleton: true
`)); err != nil {
		t.Fatal(err)
	}
	RegisterHandler(e, "ping", nopHandler)
	if _, err := e.Build(WithSingletonLocker(fakeLocker{})); err != nil {
		t.Fatalf("Build with locker: %v", err)
	}
}

func TestBuild_TwiceRejected(t *testing.T) {
	t.Parallel()
	e := New()
	_ = e.LoadBytes([]byte(`jobs: []`))
	if _, err := e.Build(); err != nil {
		t.Fatal(err)
	}
	_, err := e.Build()
	if err == nil || !strings.Contains(err.Error(), CodeAlreadyBuilt) {
		t.Errorf("second Build err = %v, want %q", err, CodeAlreadyBuilt)
	}
}

func TestRegisterHandler_DuplicatePanics(t *testing.T) {
	t.Parallel()
	e := New()
	RegisterHandler(e, "x", nopHandler)
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on duplicate RegisterHandler")
		}
		if !strings.Contains(any2err(r).Error(), CodeAlreadyRegistered) {
			t.Errorf("panic = %v, want %q", r, CodeAlreadyRegistered)
		}
	}()
	RegisterHandler(e, "x", nopHandler)
}

func TestRegisterHandler_NilFnPanics(t *testing.T) {
	t.Parallel()
	e := New()
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on nil fn")
		}
	}()
	RegisterHandler(e, "x", nil)
}

func TestRegisterHandler_AfterBuildPanics(t *testing.T) {
	t.Parallel()
	e := New()
	_ = e.LoadBytes([]byte(`jobs: []`))
	if _, err := e.Build(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on post-Build RegisterHandler")
		}
		if !strings.Contains(any2err(r).Error(), CodeAlreadyBuilt) {
			t.Errorf("panic = %v, want %q", r, CodeAlreadyBuilt)
		}
	}()
	RegisterHandler(e, "x", nopHandler)
}

func TestLoadBytes_EnvSubstitution(t *testing.T) {
	t.Parallel()
	e := New(WithEnv(map[string]string{
		"DAILY_HOUR": "3",
	}))
	if err := e.LoadBytes([]byte(`
jobs:
  - name: daily
    handler: ping
    schedule: "0 ${DAILY_HOUR} * * *"
`)); err != nil {
		t.Fatal(err)
	}
	RegisterHandler(e, "ping", nopHandler)
	if _, err := e.Build(); err != nil {
		t.Fatalf("Build after env subst: %v", err)
	}
}

func TestLoadBytes_EnvVarUnset(t *testing.T) {
	t.Parallel()
	e := New()
	err := e.LoadBytes([]byte(`
jobs:
  - name: x
    handler: ping
    schedule: "0 ${UNSET_VAR} * * *"
`))
	if err == nil {
		t.Fatal("expected error for unset env var")
	}
	if !strings.Contains(err.Error(), CodeEnvVarUnset) {
		t.Errorf("err = %v, want %q", err, CodeEnvVarUnset)
	}
}

func TestSlugify_CommonShapes(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"Daily Rollup":  "daily-rollup",
		"orders.sync":   "orders-sync",
		"daily/rollup":  "daily-rollup",
		"a__b__c":       "a-b-c",
		"!!!":           "job",
		"  spaces  ":    "spaces",
		"Weekly_Report": "weekly-report",
	}
	for in, want := range cases {
		got := slugify(in)
		if got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParser_SecondsPrecisionAccepted(t *testing.T) {
	t.Parallel()
	e := New()
	_ = e.LoadBytes([]byte(`
jobs:
  - name: tick
    handler: ping
    schedule: "* * * * * *"
`))
	RegisterHandler(e, "ping", nopHandler)
	parser := cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	if _, err := e.Build(WithParser(parser)); err != nil {
		t.Fatalf("seconds parser Build: %v", err)
	}
}

// fakeLocker satisfies SingletonLocker without doing anything. Used
// when the test only needs Build to accept singleton: true.
type fakeLocker struct{}

func (fakeLocker) TryLock(context.Context, string) (func(), bool, error) {
	return func() {}, true, nil
}

func any2err(v any) error {
	if e, ok := v.(error); ok {
		return e
	}
	return errors.New("non-error panic")
}

// Compile-time check: time package import is exercised somewhere so
// the engine test file compiles even before runtime tests land.
var _ = time.Second
