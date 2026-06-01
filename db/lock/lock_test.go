package lock_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tcpg "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/db/lock"
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
		// Two slots: one for the lock-holding conn, one for parallel test ops.
		MaxConns: 3, MinConns: 1,
	}
}

func freshDB(t *testing.T) *db.DB {
	t.Helper()
	if testing.Short() {
		t.Skip("requires Postgres testcontainer")
	}
	pgOnce.Do(initContainer)
	if pgErr != nil {
		t.Fatalf("container: %v", pgErr)
	}
	d, err := db.Connect(context.Background(), pgCfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	return d
}

func TestTryAcquire_HappyPath(t *testing.T) {
	d := freshDB(t)
	lk := lock.New(d, "test.happy")
	ok, release, err := lk.TryAcquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected lock acquired on fresh DB")
	}
	defer release()
}

func TestTryAcquire_AlreadyHeldReturnsFalse(t *testing.T) {
	d := freshDB(t)
	name := fmt.Sprintf("test.held.%d", time.Now().UnixNano())
	lk := lock.New(d, name)

	ok, release, err := lk.TryAcquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("first acquire should succeed")
	}
	defer release()

	// Second TryAcquire on same name → false because the session
	// (different conn) still holds it.
	ok2, release2, err := lk.TryAcquire(context.Background())
	if err != nil {
		t.Fatalf("second TryAcquire err: %v", err)
	}
	if ok2 {
		release2()
		t.Fatal("second TryAcquire should have returned false")
	}
}

func TestRelease_AllowsReacquire(t *testing.T) {
	d := freshDB(t)
	name := fmt.Sprintf("test.release.%d", time.Now().UnixNano())
	lk := lock.New(d, name)

	ok, release, err := lk.TryAcquire(context.Background())
	if err != nil || !ok {
		t.Fatalf("first acquire: %v %v", err, ok)
	}
	release()

	ok2, release2, err := lk.TryAcquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !ok2 {
		t.Fatal("expected reacquire after release")
	}
	defer release2()
}

func TestAcquire_BlocksUntilRelease(t *testing.T) {
	d := freshDB(t)
	name := fmt.Sprintf("test.blocking.%d", time.Now().UnixNano())
	lk := lock.New(d, name)

	// First holder grabs the lock.
	ok, release1, err := lk.TryAcquire(context.Background())
	if err != nil || !ok {
		t.Fatalf("first acquire: %v %v", err, ok)
	}

	// Second Acquire in a goroutine — should block.
	acquired := make(chan struct{})
	go func() {
		release2, err := lk.Acquire(context.Background())
		if err != nil {
			t.Errorf("blocking Acquire: %v", err)
			return
		}
		defer release2()
		close(acquired)
	}()

	// 200ms grace — Acquire must NOT have completed yet.
	select {
	case <-acquired:
		t.Fatal("blocking Acquire returned too early")
	case <-time.After(200 * time.Millisecond):
	}

	// Release first holder; second should proceed.
	release1()
	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("blocking Acquire never completed after release")
	}
}

func TestAcquire_CtxCancellation(t *testing.T) {
	d := freshDB(t)
	name := fmt.Sprintf("test.cancel.%d", time.Now().UnixNano())
	lk := lock.New(d, name)

	ok, release, _ := lk.TryAcquire(context.Background())
	if !ok {
		t.Fatal("first acquire failed")
	}
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := lk.Acquire(ctx)
	if err == nil {
		t.Fatal("expected ctx cancellation error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want DeadlineExceeded", err)
	}
}

func TestRunOnce_OnlyOneOfManyRuns(t *testing.T) {
	d := freshDB(t)
	name := fmt.Sprintf("test.runonce.%d", time.Now().UnixNano())
	var ran int32

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = lock.RunOnce(context.Background(), d, name, func(ctx context.Context) error {
				atomic.AddInt32(&ran, 1)
				time.Sleep(200 * time.Millisecond)
				return nil
			})
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&ran); got != 1 {
		t.Errorf("ran count = %d, want 1 (only one worker should hold lock)", got)
	}
}

func TestRunOnce_PropagatesFnError(t *testing.T) {
	d := freshDB(t)
	name := fmt.Sprintf("test.runonceerr.%d", time.Now().UnixNano())
	want := errors.New("domain failure")

	err := lock.RunOnce(context.Background(), d, name,
		func(ctx context.Context) error { return want })
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
}

func TestNew_NilDBPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on nil *db.DB")
		}
	}()
	_ = lock.New(nil, "x")
}

func TestNew_EmptyNamePanics(t *testing.T) {
	d := freshDB(t)
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on empty name")
		}
	}()
	_ = lock.New(d, "")
}

func TestKey_DeterministicByName(t *testing.T) {
	d := freshDB(t)
	a := lock.New(d, "abc").Key()
	b := lock.New(d, "abc").Key()
	if a != b {
		t.Errorf("Key()s differ: %d vs %d", a, b)
	}
	c := lock.New(d, "def").Key()
	if a == c {
		t.Errorf("different names share key: %d", a)
	}
}
