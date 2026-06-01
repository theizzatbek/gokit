package uploadguard_test

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/fibermap"
	"github.com/theizzatbek/gokit/fibermap/uploadguard"
)

func buildMultipart(t *testing.T, field, filename, contentType string, body []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	hdr := make(map[string][]string)
	hdr["Content-Disposition"] = []string{`form-data; name="` + field + `"; filename="` + filename + `"`}
	if contentType != "" {
		hdr["Content-Type"] = []string{contentType}
	}
	part, err := w.CreatePart(hdr)
	if err != nil {
		t.Fatalf("CreatePart: %v", err)
	}
	if _, err := part.Write(body); err != nil {
		t.Fatalf("part.Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return &buf, w.FormDataContentType()
}

func newApp() *fiber.App {
	return fiber.New(fiber.Config{ErrorHandler: fibermap.ErrorHandler(nil)})
}

// 8x8 PNG (89 50 4E 47 ...).
var validPNG = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
	0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x08, 0x00, 0x00, 0x00, 0x08,
	0x08, 0x06, 0x00, 0x00, 0x00, 0xC4, 0x0F, 0xBE,
	0x8B, 0x00, 0x00, 0x00, 0x0A, 0x49, 0x44, 0x41,
	0x54, 0x78, 0xDA, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00,
	0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE,
	0x42, 0x60, 0x82,
}

func TestGuard_AcceptsValidPNG(t *testing.T) {
	app := newApp()
	app.Post("/u",
		uploadguard.Guard("file", uploadguard.WithAllowedMIME("image/png")),
		func(c *fiber.Ctx) error {
			r, ok := uploadguard.ResultFrom(c)
			if !ok {
				return c.Status(500).SendString("no result")
			}
			return c.JSON(map[string]any{"size": r.Size, "mime": r.MIMEType})
		})

	body, ctype := buildMultipart(t, "file", "x.png", "image/png", validPNG)
	req := httptest.NewRequest("POST", "/u", body)
	req.Header.Set("Content-Type", ctype)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if resp.StatusCode != 200 {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, string(raw))
	}
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), `"mime":"image/png"`) {
		t.Errorf("body missing mime field: %s", raw)
	}
}

func TestGuard_RejectsSpoofedMIME(t *testing.T) {
	// Client claims image/png but the actual content is plain text.
	app := newApp()
	app.Post("/u",
		uploadguard.Guard("file", uploadguard.WithAllowedMIME("image/png")),
		func(c *fiber.Ctx) error { return c.SendString("ok") })

	body, ctype := buildMultipart(t, "file", "evil.png", "image/png",
		[]byte("not actually a png"))
	req := httptest.NewRequest("POST", "/u", body)
	req.Header.Set("Content-Type", ctype)
	resp, _ := app.Test(req)
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400 (MIME not allowed)", resp.StatusCode)
	}
}

func TestGuard_SizeCap(t *testing.T) {
	app := newApp()
	app.Post("/u",
		uploadguard.Guard("file", uploadguard.WithMaxSize(10)),
		func(c *fiber.Ctx) error { return c.SendString("ok") })

	body, ctype := buildMultipart(t, "file", "big.bin", "application/octet-stream",
		bytes.Repeat([]byte("X"), 100))
	req := httptest.NewRequest("POST", "/u", body)
	req.Header.Set("Content-Type", ctype)
	resp, _ := app.Test(req)
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400 (size cap)", resp.StatusCode)
	}
}

func TestGuard_MissingFieldRequired(t *testing.T) {
	app := newApp()
	app.Post("/u",
		uploadguard.Guard("file"),
		func(c *fiber.Ctx) error { return c.SendString("ok") })

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("other", "value")
	w.Close()
	req := httptest.NewRequest("POST", "/u", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, _ := app.Test(req)
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400 (field missing)", resp.StatusCode)
	}
}

func TestGuard_MissingFieldOptionalPassthrough(t *testing.T) {
	app := newApp()
	app.Post("/u",
		uploadguard.Guard("file", uploadguard.WithOptionalField()),
		func(c *fiber.Ctx) error { return c.SendString("ok") })

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	w.Close()
	req := httptest.NewRequest("POST", "/u", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, _ := app.Test(req)
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200 (optional passthrough)", resp.StatusCode)
	}
}

func TestGuard_WildcardMIME(t *testing.T) {
	app := newApp()
	app.Post("/u",
		uploadguard.Guard("file", uploadguard.WithAllowedMIME("image/*")),
		func(c *fiber.Ctx) error { return c.SendString("ok") })

	body, ctype := buildMultipart(t, "file", "x.png", "image/png", validPNG)
	req := httptest.NewRequest("POST", "/u", body)
	req.Header.Set("Content-Type", ctype)
	resp, _ := app.Test(req)
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200 (image/* matches image/png)", resp.StatusCode)
	}
}

func TestGuard_EmptyFieldNamePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on empty fieldName")
		}
	}()
	uploadguard.Guard("")
}
