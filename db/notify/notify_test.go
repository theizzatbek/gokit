package notify_test

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
	"github.com/theizzatbek/gokit/db/notify"
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
		// Need at least 2 conns: one held by the notifier, one for
		// the test's pg_notify sender.
		MaxConns: 2, MinConns: 2,
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

func TestNotifier_ReceivesPgNotify(t *testing.T) {
	d := freshDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	got := make(chan notify.Notification, 4)
	n := notify.NewNotifier(d, []string{"my_chan"},
		func(_ context.Context, nn notify.Notification) error {
			got <- nn
			return nil
		})
	if err := n.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Stop() })

	// Give the listen goroutine time to register before pg_notify
	// fires (notifications sent without listeners are silently dropped).
	time.Sleep(150 * time.Millisecond)

	if _, err := d.Exec(ctx, "SELECT pg_notify('my_chan', 'hello')"); err != nil {
		t.Fatal(err)
	}

	select {
	case n := <-got:
		if n.Channel != "my_chan" || n.Payload != "hello" {
			t.Errorf("notification = %+v, want {my_chan, hello}", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("notification not received")
	}
}

func TestNotifier_MultiChannelDispatch(t *testing.T) {
	d := freshDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		mu   sync.Mutex
		seen = map[string]string{}
	)
	n := notify.NewNotifier(d, []string{"chan_a", "chan_b"},
		func(_ context.Context, nn notify.Notification) error {
			mu.Lock()
			seen[nn.Channel] = nn.Payload
			mu.Unlock()
			return nil
		})
	if err := n.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Stop() })

	time.Sleep(150 * time.Millisecond)

	if _, err := d.Exec(ctx, "SELECT pg_notify('chan_a', 'from_a')"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(ctx, "SELECT pg_notify('chan_b', 'from_b')"); err != nil {
		t.Fatal(err)
	}

	waitFor(t, 2*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return seen["chan_a"] == "from_a" && seen["chan_b"] == "from_b"
	})
}

func TestNotifier_HandlerErrorLogsButContinues(t *testing.T) {
	d := freshDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var count int32
	n := notify.NewNotifier(d, []string{"err_chan"},
		func(_ context.Context, _ notify.Notification) error {
			atomic.AddInt32(&count, 1)
			return errors.New("handler failed")
		})
	if err := n.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Stop() })

	time.Sleep(150 * time.Millisecond)
	for i := 0; i < 3; i++ {
		if _, err := d.Exec(ctx, "SELECT pg_notify('err_chan', 'x')"); err != nil {
			t.Fatal(err)
		}
	}

	waitFor(t, 2*time.Second, func() bool { return atomic.LoadInt32(&count) >= 3 })
}

func TestNotifier_StopIsIdempotent(t *testing.T) {
	d := freshDB(t)
	n := notify.NewNotifier(d, []string{"sx"},
		func(context.Context, notify.Notification) error { return nil })
	if err := n.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := n.Stop(); err != nil {
		t.Errorf("second Stop = %v, want nil", err)
	}
}

func TestNotifier_NilSafe(t *testing.T) {
	var n *notify.Notifier
	if err := n.Start(context.Background()); err != nil {
		t.Errorf("Start nil = %v", err)
	}
	if err := n.Stop(); err != nil {
		t.Errorf("Stop nil = %v", err)
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("condition not met within %v", timeout)
}
