package service_test

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/robfig/cron/v3"
	tcpg "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/service"
)

var (
	pgOnce sync.Once
	pgCfg  db.Config
	pgErr  error
)

func TestMain(m *testing.M) { os.Exit(m.Run()) }

func initContainer() {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	c, err := tcpg.Run(ctx, "postgres:16-alpine",
		tcpg.WithDatabase("test"), tcpg.WithUsername("test"), tcpg.WithPassword("test"),
		tcpg.BasicWaitStrategies(),
	)
	if err != nil {
		pgErr = err
		return
	}
	host, _ := c.Host(ctx)
	port, _ := c.MappedPort(ctx, "5432/tcp")
	p, _ := strconv.Atoi(port.Port())
	pgCfg = db.Config{
		Host: host, Port: p, User: "test", Password: "test", Database: "test",
		SSLMode: "disable", ConnectTimeout: 5 * time.Second,
		// Two conns per replica so each replica has a slot for the
		// advisory lock + a slot for the rest of the workload.
		MaxConns: 2, MinConns: 2,
	}
}

// TestSingletonCron_OnlyOneReplicaRuns spins TWO services pointing
// at the same Postgres + registers the same job name in each.
// The advisory lock should hand the job to exactly ONE replica per
// tick.
func TestSingletonCron_OnlyOneReplicaRuns(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Postgres testcontainer")
	}
	pgOnce.Do(initContainer)
	if pgErr != nil {
		t.Fatalf("container: %v", pgErr)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		runsA        int32
		runsB        int32
		startBarrier sync.WaitGroup
	)
	startBarrier.Add(1)

	job := func(label string, counter *int32) service.JobFn {
		return func(jobCtx context.Context) error {
			// Block briefly so both replicas have a chance to attempt
			// the lock at the same time — without this, replica A
			// might finish before replica B even tries.
			startBarrier.Wait()
			atomic.AddInt32(counter, 1)
			time.Sleep(200 * time.Millisecond)
			return nil
		}
	}

	cfg := service.Config{}
	cfg.Service.LogLevel = "error"
	cfg.DB = pgCfg

	parser := cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

	svcA, err := service.New[map[string]any, any](ctx, cfg,
		service.WithCronParser(parser),
		service.WithSingletonCron("test-singleton", "* * * * * *", job("A", &runsA)),
	)
	if err != nil {
		t.Fatalf("svcA: %v", err)
	}
	defer svcA.Close()

	svcB, err := service.New[map[string]any, any](ctx, cfg,
		service.WithCronParser(parser),
		service.WithSingletonCron("test-singleton", "* * * * * *", job("B", &runsB)),
	)
	if err != nil {
		t.Fatalf("svcB: %v", err)
	}
	defer svcB.Close()

	// Release the barrier so jobs race for the lock immediately.
	startBarrier.Done()

	// Wait long enough for ~3 ticks. Expectation: total runs ≈ 3,
	// distribution between A and B is implementation-defined but
	// SUM stays at 3 (never 6).
	time.Sleep(3500 * time.Millisecond)

	total := atomic.LoadInt32(&runsA) + atomic.LoadInt32(&runsB)
	if total < 2 || total > 4 {
		t.Errorf("total runs = %d (A=%d B=%d), want 2..4 (one per tick)",
			total, atomic.LoadInt32(&runsA), atomic.LoadInt32(&runsB))
	}
	// Sanity: neither side should be > total (clearly impossible) AND
	// the runs should reflect SOME serialisation — without the lock
	// we'd see both A and B fire on every tick → total ~6.
	if total >= 5 {
		t.Errorf("total = %d suggests advisory lock failed; without it both replicas would run every tick", total)
	}
}

// TestWithSingletonCron_NoDB_ErrorsAtNew confirms the safeguard:
// a singleton job without DB is a misconfig caught at boot.
func TestWithSingletonCron_NoDB_ErrorsAtNew(t *testing.T) {
	cfg := service.Config{}
	cfg.Service.LogLevel = "error"
	_, err := service.New[map[string]any, any](context.Background(), cfg,
		service.WithSingletonCron("test", "0 0 * * *",
			func(context.Context) error { return nil }))
	if err == nil {
		t.Fatal("expected error from singleton cron without DB")
	}
}

// avoid unused-import warning when fmt goes unused after refactors.
var _ = fmt.Sprintln
