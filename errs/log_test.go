package errs_test

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/theizzatbek/gokit/errs"
)

func TestLogValueIncludesKindCodeMessage(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	logger.Error("oops", "err", errs.NotFound("user_not_found", "user 42"))
	out := buf.String()
	for _, want := range []string{"err.kind=not_found", "err.code=user_not_found", `err.message="user 42"`} {
		if !strings.Contains(out, want) {
			t.Errorf("log output %q missing %q", out, want)
		}
	}
}

func TestLogValueIncludesCause(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	cause := errors.New("sql: no rows")
	logger.Error("oops", "err", errs.Wrap(cause, errs.KindNotFound, "user_not_found", "u"))
	out := buf.String()
	if !strings.Contains(out, `err.cause="sql: no rows"`) {
		t.Errorf("missing err.cause in %q", out)
	}
}

func TestLogValueOmitsEmptyCauseAndDetails(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	logger.Error("oops", "err", errs.NotFound("x", "y"))
	out := buf.String()
	if strings.Contains(out, "err.cause") {
		t.Errorf("err.cause should be absent: %s", out)
	}
	if strings.Contains(out, "err.details") {
		t.Errorf("err.details should be absent: %s", out)
	}
}
