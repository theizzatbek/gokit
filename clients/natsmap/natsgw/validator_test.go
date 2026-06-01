package natsgw_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/clients/natsmap/natsgw"
	xerrs "github.com/theizzatbek/gokit/errs"
)

func TestValidator_GlobalRejectsAllSubjects(t *testing.T) {
	rt, cleanup := buildRuntime(t, "gwtest.alpha", "gwtest.beta")
	defer cleanup()

	app := mountApp(rt,
		natsgw.WithValidator(func(_ context.Context, _ string, _ []byte) error {
			return errors.New("nope")
		}),
	)
	for _, sub := range []string{"gwtest.alpha", "gwtest.beta"} {
		req := httptest.NewRequest("POST", "/publish/"+sub, bytes.NewReader([]byte(`{}`)))
		resp, _ := app.Test(req)
		if resp.StatusCode != 400 {
			t.Errorf("subject %s status = %d, want 400", sub, resp.StatusCode)
		}
	}
}

func TestValidator_PreservesCustomCode(t *testing.T) {
	rt, cleanup := buildRuntime(t, "gwtest.alpha")
	defer cleanup()

	app := mountApp(rt,
		natsgw.WithValidator(func(_ context.Context, _ string, _ []byte) error {
			return xerrs.Validation("urlshort_bad_code", "code field missing")
		}),
	)
	req := httptest.NewRequest("POST", "/publish/gwtest.alpha", bytes.NewReader([]byte(`{}`)))
	resp, _ := app.Test(req)
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "urlshort_bad_code") {
		t.Errorf("custom Code not surfaced: %s", body)
	}
}

func TestValidator_SubjectScopedOnlyAppliesToMatch(t *testing.T) {
	rt, cleanup := buildRuntime(t, "gwtest.alpha", "gwtest.beta")
	defer cleanup()

	app := mountApp(rt,
		natsgw.WithSubjectValidator("gwtest.alpha",
			func(_ context.Context, _ string, _ []byte) error {
				return errors.New("alpha rejected")
			}),
	)
	// alpha: validator fires
	req := httptest.NewRequest("POST", "/publish/gwtest.alpha", bytes.NewReader([]byte(`{}`)))
	resp, _ := app.Test(req)
	if resp.StatusCode != 400 {
		t.Errorf("alpha status = %d, want 400", resp.StatusCode)
	}
	// beta: validator skipped, request goes through
	req = httptest.NewRequest("POST", "/publish/gwtest.beta", bytes.NewReader([]byte(`{}`)))
	resp, _ = app.Test(req)
	if resp.StatusCode != fiber.StatusAccepted {
		t.Errorf("beta status = %d, want 202", resp.StatusCode)
	}
}

func TestValidator_MultipleStackInRegistrationOrder(t *testing.T) {
	rt, cleanup := buildRuntime(t, "gwtest.alpha")
	defer cleanup()

	var calls []string
	app := mountApp(rt,
		natsgw.WithValidator(func(_ context.Context, _ string, _ []byte) error {
			calls = append(calls, "first")
			return nil
		}),
		natsgw.WithValidator(func(_ context.Context, _ string, _ []byte) error {
			calls = append(calls, "second")
			return errors.New("rejected by second")
		}),
		natsgw.WithValidator(func(_ context.Context, _ string, _ []byte) error {
			calls = append(calls, "third (should not run)")
			return nil
		}),
	)
	req := httptest.NewRequest("POST", "/publish/gwtest.alpha", bytes.NewReader([]byte(`{}`)))
	_, _ = app.Test(req)

	if len(calls) != 2 || calls[0] != "first" || calls[1] != "second" {
		t.Errorf("call order = %v, want [first second]", calls)
	}
}

func TestValidJSON_AcceptsWellFormed(t *testing.T) {
	v := natsgw.ValidJSON()
	if err := v(context.Background(), "x", []byte(`{"a":1}`)); err != nil {
		t.Errorf("good JSON rejected: %v", err)
	}
}

func TestValidJSON_RejectsMalformed(t *testing.T) {
	v := natsgw.ValidJSON()
	err := v(context.Background(), "x", []byte(`{`))
	if err == nil {
		t.Error("malformed JSON accepted")
	}
}

func TestUnmarshalAs_RejectsTypeMismatch(t *testing.T) {
	type sample struct {
		Code string `json:"code"`
	}
	v := natsgw.UnmarshalAs[sample]()
	// Wrong type for field — decode error.
	err := v(context.Background(), "x", []byte(`{"code":123}`))
	if err == nil {
		t.Error("type-mismatch accepted")
	}
}

func TestUnmarshalAs_AcceptsExpectedShape(t *testing.T) {
	type sample struct {
		Code string `json:"code"`
	}
	v := natsgw.UnmarshalAs[sample]()
	if err := v(context.Background(), "x", []byte(`{"code":"abc"}`)); err != nil {
		t.Errorf("good shape rejected: %v", err)
	}
}
