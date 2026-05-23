package auth

import (
	"encoding/base64"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func basicHeader(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

// app200 mounts Basic() as the only middleware; the handler echoes
// locals so the test can prove they were set.
func app200(t *testing.T) *fiber.App {
	t.Helper()
	app := fiber.New()
	app.Use(Basic())
	app.Get("/", func(c *fiber.Ctx) error {
		uid, _ := c.Locals("user_id").(string)
		role, _ := c.Locals("role").(string)
		return c.SendString(uid + "|" + role)
	})
	return app
}

func TestBasic_Success(t *testing.T) {
	app := app200(t)
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", basicHeader("alice", "secret"))
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "u-alice|user" {
		t.Errorf("body = %q, want u-alice|user", string(body))
	}
}

func TestBasic_WrongPassword(t *testing.T) {
	app := app200(t)
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", basicHeader("alice", "wrong"))
	resp, _ := app.Test(req)
	if resp.StatusCode != 401 {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

// TestBasic_UnknownUser_SameBodyAsWrongPassword guarantees we don't
// leak whether a username exists. The 401 body for an unknown user
// must be byte-for-byte identical to the 401 body for a wrong
// password.
func TestBasic_UnknownUser_SameBodyAsWrongPassword(t *testing.T) {
	app := app200(t)

	wrong := httptest.NewRequest("GET", "/", nil)
	wrong.Header.Set("Authorization", basicHeader("alice", "wrong"))
	respWrong, _ := app.Test(wrong)
	bodyWrong, _ := io.ReadAll(respWrong.Body)

	unknown := httptest.NewRequest("GET", "/", nil)
	unknown.Header.Set("Authorization", basicHeader("nosuch", "whatever"))
	respUnknown, _ := app.Test(unknown)
	bodyUnknown, _ := io.ReadAll(respUnknown.Body)

	if respUnknown.StatusCode != 401 {
		t.Errorf("unknown status = %d, want 401", respUnknown.StatusCode)
	}
	if string(bodyWrong) != string(bodyUnknown) {
		t.Errorf("enumeration leak:\n wrong-password body = %q\n unknown-user body  = %q",
			string(bodyWrong), string(bodyUnknown))
	}
}

func TestBasic_BadBase64(t *testing.T) {
	app := app200(t)
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Basic !!!not-base64!!!")
	resp, _ := app.Test(req)
	if resp.StatusCode != 401 {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestBasic_NoHeader(t *testing.T) {
	app := app200(t)
	req := httptest.NewRequest("GET", "/", nil)
	resp, _ := app.Test(req)
	if resp.StatusCode != 401 {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}
