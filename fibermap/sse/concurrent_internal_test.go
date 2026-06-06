package sse

import (
	"bufio"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// signalWriter is an io.Writer that signals once when the first
// Write happens and blocks every Write until release is closed.
// Lets the test deterministically park goroutine 1 inside Send so
// goroutine 2 races into the guard.
type signalWriter struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *signalWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	<-w.release
	return len(p), nil
}

func TestStream_PanicsOnConcurrentSend(t *testing.T) {
	w := &signalWriter{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	// bufio.NewWriter with a tiny buffer so even a short payload
	// flushes through to the underlying signalWriter.Write — that is
	// what parks goroutine 1.
	s := &Stream{w: bufio.NewWriterSize(w, 8)}

	var wg sync.WaitGroup
	var gotPanic atomic.Bool
	var panicMsg atomic.Value

	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = s.Send("evt-1", strings.Repeat("x", 32))
	}()

	// Wait until goroutine 1 is inside the underlying writer — at
	// this point s.inUse is already CAS'd to true and goroutine 1
	// is parked, holding the guard.
	<-w.entered

	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				gotPanic.Store(true)
				if msg, ok := r.(string); ok {
					panicMsg.Store(msg)
				}
			}
		}()
		_ = s.Send("evt-2", "y")
	}()

	// Spin a short while to let goroutine 2 reach the CAS. We
	// can't observe its progress directly, but yielding ~1000
	// times is plenty for a single CAS + recover().
	for i := 0; i < 1000 && !gotPanic.Load(); i++ {
		runtime.Gosched()
	}

	close(w.release)
	wg.Wait()

	if !gotPanic.Load() {
		t.Fatal("second concurrent Send did not panic — CAS guard missing or ineffective")
	}
	if msg, _ := panicMsg.Load().(string); msg == "" || !strings.Contains(msg, "concurrent Send") {
		t.Errorf("panic message missing 'concurrent Send': %q", msg)
	}
}

func TestStream_PanicsOnConcurrentMixed(t *testing.T) {
	// Goroutine 1 enters Send and blocks; goroutine 2 attempts
	// Comment and must panic — the guard is keyed on the Stream,
	// not on the method name.
	w := &signalWriter{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	s := &Stream{w: bufio.NewWriterSize(w, 8)}

	var wg sync.WaitGroup
	var gotPanic atomic.Bool
	var panicMsg atomic.Value

	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = s.Send("e", strings.Repeat("x", 32))
	}()
	<-w.entered

	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				gotPanic.Store(true)
				if msg, ok := r.(string); ok {
					panicMsg.Store(msg)
				}
			}
		}()
		_ = s.Comment("keepalive")
	}()
	for i := 0; i < 1000 && !gotPanic.Load(); i++ {
		runtime.Gosched()
	}

	close(w.release)
	wg.Wait()

	if !gotPanic.Load() {
		t.Fatal("concurrent Comment vs Send did not panic")
	}
	if msg, _ := panicMsg.Load().(string); !strings.Contains(msg, "concurrent Comment") {
		t.Errorf("panic message did not name the offending method: %q", msg)
	}
}

func TestStream_SequentialSendsDoNotPanic(t *testing.T) {
	// Sanity: the guard must not false-positive on serial reuse.
	// A handler that calls Send 10 times in a row from one
	// goroutine is the canonical happy path.
	var sb strings.Builder
	s := &Stream{w: bufio.NewWriter(&sb)}
	for i := 0; i < 10; i++ {
		if err := s.Send("ping", "tick"); err != nil {
			t.Fatalf("Send #%d: %v", i, err)
		}
	}
	if got := s.Err(); got != nil {
		t.Fatalf("Err = %v, want nil", got)
	}
}
