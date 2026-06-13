package uploadguard

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/gofiber/fiber/v2"

	s3client "github.com/theizzatbek/gokit/clients/s3"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// Result captures what the middleware validated. Handlers read it via
// [ResultFrom] (or use c.Locals(LocalsKey)). FileHeader is forwarded
// unchanged so the handler can re-open the file if more processing
// is needed (image resizing, virus scan beyond what WithStreamToS3
// did).
type Result struct {
	// Field is the form-field name that carried the upload.
	Field string

	// Size is the uploaded byte count.
	Size int64

	// MIMEType is the sniffed Content-Type (NOT the client-supplied
	// header).
	MIMEType string

	// FileHeader is the underlying multipart entry. nil when the
	// upload was streamed to S3 and the middleware consumed the
	// reader.
	FileHeader *multipart.FileHeader

	// S3Key is the destination object key. Empty unless
	// WithStreamToS3 was configured.
	S3Key string
}

// LocalsKey is the Fiber Locals slot where the middleware stores the
// [Result] for the handler to read.
const LocalsKey = "uploadguard.result"

// Option tunes [Guard].
type Option func(*config)

type config struct {
	maxSize     int64
	allowedMIME []string
	required    bool
	s3          *s3client.Client
	keyFn       func(*fiber.Ctx) string
	contentType string
}

// WithMaxSize caps the uploaded body. Default 10 MiB.
func WithMaxSize(bytes int64) Option {
	return func(c *config) { c.maxSize = bytes }
}

// WithAllowedMIME restricts the sniffed content type to the given
// allowlist. Wildcards are supported via the trailing "/*"
// convention: "image/*" matches every image/<subtype>. Empty list
// (default) skips the MIME check entirely.
func WithAllowedMIME(types ...string) Option {
	return func(c *config) { c.allowedMIME = types }
}

// WithRequiredField (default true) returns 400 when the field is
// absent. Use [WithOptionalField] to invert.
func WithRequiredField() Option {
	return func(c *config) { c.required = true }
}

// WithOptionalField allows the request to pass through without
// validation when the field is missing. Useful for endpoints where
// the upload is one of several allowed inputs.
func WithOptionalField() Option {
	return func(c *config) { c.required = false }
}

// WithStreamToS3 enables direct upload of the validated file to the
// supplied S3 client. keyFn picks the object key per-request — pull
// the user ID from the auth context, the URL slug, etc.
//
// When enabled, the handler does NOT receive the raw file (the
// middleware consumes the reader) — read [Result.S3Key] to know
// where it landed.
func WithStreamToS3(s3 *s3client.Client, keyFn func(*fiber.Ctx) string) Option {
	return func(c *config) {
		c.s3 = s3
		c.keyFn = keyFn
	}
}

// WithS3ContentType overrides the Content-Type written on the S3
// object. Default: the sniffed MIME. Pass to standardise (e.g.
// `binary/octet-stream` for opaque uploads).
func WithS3ContentType(ct string) Option {
	return func(c *config) { c.contentType = ct }
}

// Guard returns the Fiber middleware. fieldName is the form-field
// the upload lives under. Returns the middleware unconditionally —
// configuration mistakes (empty fieldName, nil S3 with stream-on)
// surface as a 500 at request time. Pre-flight callers can build
// the config struct and check it themselves; the kit deliberately
// keeps Guard side-effect-free.
//
// fieldName "" panics — programmer error at startup, not runtime.
func Guard(fieldName string, opts ...Option) fiber.Handler {
	if fieldName == "" {
		panic("uploadguard.Guard: fieldName is required")
	}
	cfg := config{
		maxSize:  10 << 20, // 10 MiB
		required: true,
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	return func(c *fiber.Ctx) error {
		fh, err := c.FormFile(fieldName)
		if err != nil {
			if !cfg.required {
				return c.Next()
			}
			return xerrs.Validationf(CodeFieldMissing,
				"upload field %q missing", fieldName)
		}
		if cfg.maxSize > 0 && fh.Size > cfg.maxSize {
			return xerrs.Validationf(CodeSizeExceeded,
				"upload %q exceeds %d bytes", fieldName, cfg.maxSize)
		}

		f, err := fh.Open()
		if err != nil {
			return xerrs.Wrapf(err, xerrs.KindInternal, CodeOpenFailed,
				"upload %q: open failed", fieldName)
		}
		defer func() { _ = f.Close() }()

		// Sniff the first 512 bytes — http.DetectContentType caps
		// internally so a shorter file is fine.
		head := make([]byte, 512)
		n, _ := io.ReadFull(f, head)
		head = head[:n]
		sniffed := http.DetectContentType(head)
		// Drop the optional `; charset=…` suffix so the allowlist
		// can write "text/plain" without worrying about UTF-8.
		base := sniffed
		if i := strings.Index(base, ";"); i > 0 {
			base = strings.TrimSpace(base[:i])
		}

		if len(cfg.allowedMIME) > 0 && !mimeMatches(base, cfg.allowedMIME) {
			return xerrs.Validationf(CodeMIMENotAllowed,
				"upload %q: type %q not in allowlist", fieldName, base)
		}

		result := Result{
			Field:      fieldName,
			Size:       fh.Size,
			MIMEType:   base,
			FileHeader: fh,
		}

		if cfg.s3 != nil {
			if cfg.keyFn == nil {
				return xerrs.Validation(CodeInvalidConfig,
					"upload: WithStreamToS3 requires keyFn")
			}
			key := cfg.keyFn(c)
			if key == "" {
				return xerrs.Validation(CodeInvalidConfig,
					"upload: keyFn returned empty key")
			}
			ct := cfg.contentType
			if ct == "" {
				ct = base
			}
			combined := io.MultiReader(bytes.NewReader(head), f)
			if err := cfg.s3.Put(c.UserContext(), key, combined,
				s3client.WithPutContentType(ct)); err != nil {
				return xerrs.Wrap(err, xerrs.KindUnavailable, CodeUploadFailed,
					"upload: S3 Put failed")
			}
			result.S3Key = key
			result.FileHeader = nil
		}
		c.Locals(LocalsKey, result)
		return c.Next()
	}
}

// ResultFrom returns the validated upload result the middleware
// stashed in Locals. Returns (zero, false) when the middleware was
// not on this route OR the field was optional + absent.
func ResultFrom(c *fiber.Ctx) (Result, bool) {
	v := c.Locals(LocalsKey)
	if v == nil {
		return Result{}, false
	}
	r, ok := v.(Result)
	return r, ok
}

// mimeMatches reports whether sniffed is in allowed, supporting the
// trailing "/*" wildcard.
func mimeMatches(sniffed string, allowed []string) bool {
	for _, p := range allowed {
		if p == sniffed {
			return true
		}
		if strings.HasSuffix(p, "/*") {
			prefix := strings.TrimSuffix(p, "/*") + "/"
			if strings.HasPrefix(sniffed, prefix) {
				return true
			}
		}
	}
	return false
}

// Silence unused-import warnings when builds drop optional paths.
var _ = context.Background
