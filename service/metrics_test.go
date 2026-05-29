package service

import (
	"context"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

func TestNew_RegistersRuntimeCollectorsByDefault(t *testing.T) {
	reg := prometheus.NewRegistry()
	svc, err := New[testCtx, testClaims](context.Background(), Config{}, WithMetrics(reg))
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	t.Cleanup(svc.Close)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	var names []string
	for _, mf := range mfs {
		names = append(names, mf.GetName())
	}
	if !containsPrefix(names, "go_") {
		t.Errorf("expected go_* runtime collectors, got: %v", names)
	}
	if !containsPrefix(names, "process_") {
		t.Errorf("expected process_* collectors, got: %v", names)
	}
}

func TestNew_WithoutRuntimeMetrics_Skips(t *testing.T) {
	reg := prometheus.NewRegistry()
	svc, err := New[testCtx, testClaims](context.Background(), Config{},
		WithMetrics(reg), WithoutRuntimeMetrics())
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	t.Cleanup(svc.Close)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, mf := range mfs {
		if strings.HasPrefix(mf.GetName(), "go_") || strings.HasPrefix(mf.GetName(), "process_") {
			t.Errorf("WithoutRuntimeMetrics: unexpected %s collector still registered", mf.GetName())
		}
	}
}

func TestNew_RuntimeCollectors_IdempotentOnPreRegistered(t *testing.T) {
	// User pre-registers the Go collector themselves; service.New
	// should silently skip the duplicate and not error.
	reg := prometheus.NewRegistry()
	if err := reg.Register(collectors.NewGoCollector()); err != nil {
		t.Fatalf("pre-register: %v", err)
	}
	svc, err := New[testCtx, testClaims](context.Background(), Config{}, WithMetrics(reg))
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	t.Cleanup(svc.Close)
}

func containsPrefix(names []string, prefix string) bool {
	for _, n := range names {
		if strings.HasPrefix(n, prefix) {
			return true
		}
	}
	return false
}