package svckit

import "testing"

func TestClose_TeardownIsLIFO(t *testing.T) {
	var journal []string
	s := &Service[struct{}, struct{}]{opts: &options{}}
	s.OnShutdown(func() error { journal = append(journal, "first"); return nil })
	s.OnShutdown(func() error { journal = append(journal, "second"); return nil })

	s.Close()

	want := []string{"second", "first"}
	if len(journal) != len(want) || journal[0] != want[0] || journal[1] != want[1] {
		t.Fatalf("teardown: want %v, got %v", want, journal)
	}
}

func TestClose_IsIdempotent(t *testing.T) {
	calls := 0
	s := &Service[struct{}, struct{}]{opts: &options{}}
	s.OnShutdown(func() error { calls++; return nil })

	s.Close()
	s.Close()

	if calls != 1 {
		t.Fatalf("callback must fire exactly once, got %d", calls)
	}
}

func TestClose_NilReceiverIsSafe(t *testing.T) {
	var s *Service[struct{}, struct{}]
	s.Close() // must not panic
}

func TestOnShutdown_AfterCloseIsDropped(t *testing.T) {
	called := false
	s := &Service[struct{}, struct{}]{opts: &options{}}

	s.Close()
	s.OnShutdown(func() error { called = true; return nil })

	if called {
		t.Error("callback registered after Close must not be invoked")
	}
}
