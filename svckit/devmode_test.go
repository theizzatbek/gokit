package svckit

import "testing"

// TestMountDevTools_IdempotentAcrossRepeatedCalls guards the
// getter-with-a-side-effect shape runOptions() relies on: nothing
// calls mountDevTools twice today, but if it ever did, a second call
// must not append a second copy of the dev routes onto opts.runOpts.
func TestMountDevTools_IdempotentAcrossRepeatedCalls(t *testing.T) {
	s := &Service[struct{}, struct{}]{
		opts: &options{devEnable: true},
	}
	s.cfg.Service.Env = "dev"

	s.mountDevTools()
	afterFirst := len(s.opts.runOpts)
	if afterFirst == 0 {
		t.Fatal("mountDevTools did not append any RunOption on first call")
	}

	s.mountDevTools()
	afterSecond := len(s.opts.runOpts)
	if afterSecond != afterFirst {
		t.Fatalf("second call mounted dev routes again: runOpts grew from %d to %d", afterFirst, afterSecond)
	}
}
