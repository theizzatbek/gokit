package jobs_test

import (
	"context"
	"errors"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tcpg "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/db/jobs"
)

var (
	pgOnce sync.Once
	pgCfg  db.Config
	pgErr  error
)

func TestMain(m *testing.M) { os.Exit(m.Run()) }

func initPostgresContainer() {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	c, err := tcpg.Run(ctx, "postgres:16-alpine",
		tcpg.WithDatabase("test"),
		tcpg.WithUsername("test"),
		tcpg.WithPassword("test"),
		tcpg.BasicWaitStrategies(),
	)
	if err != nil {
		pgErr = err
		return
	}
	host, err := c.Host(ctx)
	if err != nil {
		pgErr = err
		return
	}
	port, err := c.MappedPort(ctx, "5432/tcp")
	if err != nil {
		pgErr = err
		return
	}
	p, _ := strconv.Atoi(port.Port())
	pgCfg = db.Config{
		Host:     host,
		Port:     p,
		User:     "test",
		Password: "test",
		Database: "test",
		SSLMode:  "disable",
		MaxConns: 4,
		MinConns: 2,
	}
}

func freshDB(t *testing.T) *db.DB {
	t.Helper()
	if testing.Short() {
		t.Skip("requires Postgres container")
	}
	pgOnce.Do(initPostgresContainer)
	if pgErr != nil {
		t.Fatalf("postgres container: %v", pgErr)
	}
	d, err := db.Connect(context.Background(), pgCfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := jobs.ApplySchema(context.Background(), d); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	if _, err := d.Exec(context.Background(), "TRUNCATE jobs"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

type welcomePayload struct {
	UserID string `json:"user_id"`
}

func TestSchedule_InsertsRowReturningID(t *testing.T) {
	d := freshDB(t)
	id, err := jobs.Schedule(context.Background(), d, time.Time{}, "user.welcome",
		welcomePayload{UserID: "u-1"})
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if id == 0 {
		t.Errorf("returned id = 0, want > 0")
	}
}

func TestSchedule_RequiresJobType(t *testing.T) {
	d := freshDB(t)
	_, err := jobs.Schedule(context.Background(), d, time.Time{}, "", welcomePayload{})
	if err == nil {
		t.Fatal("expected error for empty jobType")
	}
}

func TestWorker_RunsHandlerThenMarksDone(t *testing.T) {
	d := freshDB(t)
	w, err := jobs.NewWorker(d, jobs.WithInterval(50*time.Millisecond))
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	var seen atomic.Int32
	jobs.RegisterHandler[welcomePayload](w, "user.welcome", func(_ context.Context, p welcomePayload) error {
		if p.UserID != "u-1" {
			t.Errorf("payload UserID = %q, want u-1", p.UserID)
		}
		seen.Add(1)
		return nil
	})

	if _, err := jobs.Schedule(context.Background(), d, time.Time{}, "user.welcome",
		welcomePayload{UserID: "u-1"}); err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go w.Start(ctx)
	waitFor(t, func() bool { return seen.Load() == 1 }, 2*time.Second, "handler not invoked")
	_ = w.Stop()

	// Row should now be in state=done.
	var state string
	row := d.QueryRow(context.Background(), "SELECT state FROM jobs LIMIT 1")
	if err := row.Scan(&state); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if state != "done" {
		t.Errorf("state = %q, want done", state)
	}
}

func TestWorker_RetriesOnHandlerError(t *testing.T) {
	d := freshDB(t)
	w, _ := jobs.NewWorker(d, jobs.WithInterval(40*time.Millisecond))
	var attempts atomic.Int32
	jobs.RegisterHandler[welcomePayload](w, "flaky", func(_ context.Context, _ welcomePayload) error {
		n := attempts.Add(1)
		if n < 3 {
			return errors.New("transient")
		}
		return nil
	})
	_, _ = jobs.Schedule(context.Background(), d, time.Time{}, "flaky", welcomePayload{})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go w.Start(ctx)
	waitFor(t, func() bool { return attempts.Load() >= 3 }, 8*time.Second, "did not retry to 3 attempts")
	_ = w.Stop()
}

func TestWorker_MaxAttemptsMovesToFailed(t *testing.T) {
	d := freshDB(t)
	w, _ := jobs.NewWorker(d, jobs.WithInterval(30*time.Millisecond))
	jobs.RegisterHandler[welcomePayload](w, "always-fails", func(_ context.Context, _ welcomePayload) error {
		return errors.New("nope")
	})
	_, _ = jobs.Schedule(context.Background(), d, time.Time{}, "always-fails",
		welcomePayload{}, jobs.WithMaxAttempts(2))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go w.Start(ctx)

	waitFor(t, func() bool {
		var s string
		_ = d.QueryRow(context.Background(), "SELECT state FROM jobs LIMIT 1").Scan(&s)
		return s == "failed"
	}, 4*time.Second, "row never reached failed")
	_ = w.Stop()
}

func TestWorker_MissingHandlerMarksFailed(t *testing.T) {
	d := freshDB(t)
	w, _ := jobs.NewWorker(d, jobs.WithInterval(30*time.Millisecond))
	_, _ = jobs.Schedule(context.Background(), d, time.Time{}, "no-handler", welcomePayload{})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go w.Start(ctx)
	waitFor(t, func() bool {
		var s string
		_ = d.QueryRow(context.Background(), "SELECT state FROM jobs LIMIT 1").Scan(&s)
		return s == "failed"
	}, 2*time.Second, "row never reached failed (missing handler)")
	_ = w.Stop()
}

func TestWorker_QueueFilterIsolatesWorkers(t *testing.T) {
	d := freshDB(t)
	// Two workers — one drains "email", other drains "billing".
	emailW, _ := jobs.NewWorker(d, jobs.WithInterval(30*time.Millisecond), jobs.WithQueues("email"))
	billingW, _ := jobs.NewWorker(d, jobs.WithInterval(30*time.Millisecond), jobs.WithQueues("billing"))

	var emailRuns, billingRuns atomic.Int32
	jobs.RegisterHandler[welcomePayload](emailW, "send", func(_ context.Context, _ welcomePayload) error {
		emailRuns.Add(1)
		return nil
	})
	jobs.RegisterHandler[welcomePayload](billingW, "send", func(_ context.Context, _ welcomePayload) error {
		billingRuns.Add(1)
		return nil
	})

	_, _ = jobs.Schedule(context.Background(), d, time.Time{}, "send", welcomePayload{}, jobs.WithQueue("email"))
	_, _ = jobs.Schedule(context.Background(), d, time.Time{}, "send", welcomePayload{}, jobs.WithQueue("billing"))

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	go emailW.Start(ctx)
	go billingW.Start(ctx)
	waitFor(t, func() bool {
		return emailRuns.Load() == 1 && billingRuns.Load() == 1
	}, 3*time.Second, "queue isolation failed")
	_ = emailW.Stop()
	_ = billingW.Stop()
}

func TestWorker_DelayedRunAtRespected(t *testing.T) {
	d := freshDB(t)
	w, _ := jobs.NewWorker(d, jobs.WithInterval(30*time.Millisecond))
	var seen atomic.Int32
	jobs.RegisterHandler[welcomePayload](w, "later", func(_ context.Context, _ welcomePayload) error {
		seen.Add(1)
		return nil
	})

	// Schedule 500ms in the future.
	runAt := time.Now().Add(500 * time.Millisecond)
	_, _ = jobs.Schedule(context.Background(), d, runAt, "later", welcomePayload{})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go w.Start(ctx)

	// Should NOT run within first 200ms.
	time.Sleep(200 * time.Millisecond)
	if seen.Load() != 0 {
		t.Errorf("ran before runAt: seen = %d", seen.Load())
	}
	waitFor(t, func() bool { return seen.Load() == 1 }, 2*time.Second, "delayed job never ran")
	_ = w.Stop()
}

func TestWorker_DuplicateRegistrationPanics(t *testing.T) {
	d := freshDB(t)
	w, _ := jobs.NewWorker(d)
	jobs.RegisterHandler[welcomePayload](w, "dup", func(_ context.Context, _ welcomePayload) error { return nil })
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on duplicate RegisterHandler")
		}
	}()
	jobs.RegisterHandler[welcomePayload](w, "dup", func(_ context.Context, _ welcomePayload) error { return nil })
}

func TestWorker_StartTwiceErrors(t *testing.T) {
	d := freshDB(t)
	w, _ := jobs.NewWorker(d)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Start(ctx)
	time.Sleep(20 * time.Millisecond)
	err := w.Start(ctx)
	if err == nil {
		t.Fatal("second Start expected to error")
	}
	_ = w.Stop()
}

func waitFor(t *testing.T, cond func() bool, timeout time.Duration, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("waitFor timeout: %s", msg)
}
