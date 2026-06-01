package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const usageDoctor = `kit doctor — preflight check against a running service

Usage:
  kit doctor --url URL [--timeout 10s] [--json]

Examples:
  kit doctor --url http://localhost:8080
  kit doctor --url https://staging.api.io --timeout 30s
  kit doctor --url http://localhost:8080 --json | jq

Hits the service's /preflight endpoint (must be wired via
service.WithPreflightEndpoint) and pretty-prints every check's
status. Exit code 0 on full success, 1 on any failure, 2 on
transport / config error.

Use as a CI gate ("is staging actually ready before running
integration tests") or for on-call smoke-checks.
`

func runDoctor(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	url := fs.String("url", "", "base URL of the kit service (required)")
	timeout := fs.Duration("timeout", 15*time.Second, "request timeout")
	jsonOut := fs.Bool("json", false, "output raw JSON instead of formatted table")
	path := fs.String("path", "/preflight", "path of the preflight endpoint on the service")
	fs.Usage = func() { fmt.Fprint(os.Stderr, usageDoctor) }
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *url == "" {
		fs.Usage()
		return errors.New("--url is required")
	}

	target := strings.TrimRight(*url, "/") + *path

	cctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, target, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("call %s: %w", target, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	if *jsonOut {
		// Stream raw JSON to stdout for `jq` chains.
		fmt.Println(string(body))
	} else {
		if err := printDoctorTable(os.Stdout, body, target, resp.StatusCode); err != nil {
			return err
		}
	}

	// Exit code mirrors the HTTP status — 200 → ok, 503 → at least
	// one failure. Anything else (404, 401) is a CLI-config error
	// (endpoint not mounted, auth-gate, wrong path).
	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusServiceUnavailable:
		return errors.New("one or more preflight checks failed")
	default:
		return fmt.Errorf("unexpected %d from %s — is /preflight wired? "+
			"(service.WithPreflightEndpoint)", resp.StatusCode, target)
	}
}

type doctorCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Error   string `json:"error,omitempty"`
	Latency int64  `json:"latency_ms"`
}

type doctorResult struct {
	Status string        `json:"status"`
	Checks []doctorCheck `json:"checks"`
}

// printDoctorTable renders the structured result as a 4-column
// ASCII table — no third-party tables dep so the CLI stays
// stdlib-only.
func printDoctorTable(w io.Writer, raw []byte, target string, status int) error {
	var res doctorResult
	if err := json.Unmarshal(raw, &res); err != nil {
		// Body isn't shaped like PreflightResult — render
		// status + raw body so the operator sees what came back.
		fmt.Fprintf(w, "doctor: GET %s → %d\n%s\n", target, status, string(raw))
		return nil
	}
	fmt.Fprintf(w, "preflight @ %s\n", target)
	fmt.Fprintf(w, "status:  %s\n\n", res.Status)
	fmt.Fprintln(w, "  CHECK              STATUS    LATENCY  DETAIL")
	for _, c := range res.Checks {
		mark := "OK"
		if c.Status != "ok" {
			mark = "FAIL"
		}
		fmt.Fprintf(w, "  %-18s %-9s %4dms   %s\n",
			c.Name, mark, c.Latency, c.Error)
	}
	fmt.Fprintln(w)
	return nil
}
