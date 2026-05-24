package fibermap_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/theizzatbek/fibermap"
	"github.com/theizzatbek/fibermap/errs"
)

func newTestApp(t *testing.T, h fiber.Handler, logger *slog.Logger) *fiber.App {
	t.Helper()
	app := fiber.New(fiber.Config{
		ErrorHandler:          fibermap.ErrorHandler(logger),
		DisableStartupMessage: true,
	})
	app.Get("/x", h)
	return app
}

func doRequest(t *testing.T, app *fiber.App) (int, []byte) {
	t.Helper()
	req := httptest.NewRequest("GET", "/x", nil)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}

func TestErrorHandlerErrsError(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	app := newTestApp(t, func(c *fiber.Ctx) error {
		return errs.NotFound("user_not_found", "user 42 not found")
	}, logger)

	status, body := doRequest(t, app)
	if status != 404 {
		t.Errorf("status = %d, want 404", status)
	}
	var got errs.Response
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, body)
	}
	if got.Code != "user_not_found" || got.Message != "user 42 not found" {
		t.Errorf("body = %+v", got)
	}
	if buf.Len() > 0 {
		t.Errorf("4xx should not log; got %q", buf.String())
	}
}

func TestErrorHandlerInternalLogsAndReturns500(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	app := newTestApp(t, func(c *fiber.Ctx) error {
		return errs.Wrap(errors.New("db connection lost"), errs.KindInternal, "db_failure", "fetch user")
	}, logger)

	status, body := doRequest(t, app)
	if status != 500 {
		t.Errorf("status = %d, want 500", status)
	}
	var got errs.Response
	_ = json.Unmarshal(body, &got)
	if got.Code != "db_failure" {
		t.Errorf("body.Code = %q, want db_failure", got.Code)
	}
	if !strings.Contains(buf.String(), "db_failure") {
		t.Errorf("log should contain code; got %q", buf.String())
	}
}

func TestErrorHandlerUnknownErrorReturns500AndLogs(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	app := newTestApp(t, func(c *fiber.Ctx) error {
		return errors.New("raw")
	}, logger)

	status, body := doRequest(t, app)
	if status != 500 {
		t.Errorf("status = %d, want 500", status)
	}
	var got errs.Response
	_ = json.Unmarshal(body, &got)
	if got.Code != "internal_error" {
		t.Errorf("body.Code = %q, want internal_error", got.Code)
	}
	if buf.Len() == 0 {
		t.Error("unknown error should be logged")
	}
}

func TestErrorHandlerFiberErrorPassesThroughStatus(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	app := newTestApp(t, func(c *fiber.Ctx) error {
		return fiber.NewError(fiber.StatusMethodNotAllowed, "nope")
	}, logger)

	status, body := doRequest(t, app)
	if status != 405 {
		t.Errorf("status = %d, want 405", status)
	}
	var got errs.Response
	_ = json.Unmarshal(body, &got)
	if got.Code != "fiber_error" {
		t.Errorf("body.Code = %q, want fiber_error", got.Code)
	}
	if buf.Len() > 0 {
		t.Errorf("4xx should not log; got %q", buf.String())
	}
}

func TestErrorHandlerNilLoggerUsesDefault(t *testing.T) {
	app := newTestApp(t, func(c *fiber.Ctx) error {
		return errs.NotFound("x", "y")
	}, nil)
	status, _ := doRequest(t, app)
	if status != 404 {
		t.Errorf("status = %d, want 404", status)
	}
}
