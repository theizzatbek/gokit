// Package uploadguard is a Fiber middleware that validates an inbound
// multipart-form file field BEFORE the handler sees it. Checks:
//
//   - Size cap                  — content larger than [WithMaxSize] is rejected.
//   - MIME-type allowlist       — sniffed from the first 512 bytes
//     (http.DetectContentType), NOT the client-supplied Content-Type.
//   - Optional streaming-to-S3  — when [WithStreamToS3] is set, the
//     validated file is uploaded directly and the resulting object key
//     is stashed in Locals for the handler to read.
//
// Why sniff: clients lie. An attacker uploading `evil.php` with
// `Content-Type: image/png` would bypass any header-only check. The
// kit reads the actual magic bytes via http.DetectContentType which
// resolves the real type.
//
// Mount via the fibermount factory (`upload_guard`) for YAML-driven
// routes, or directly via Guard(field, opts...) for Go-only routes:
//
//	app.Post("/avatar",
//	    uploadguard.Guard("file",
//	        uploadguard.WithMaxSize(5<<20),
//	        uploadguard.WithAllowedMIME("image/png", "image/jpeg"),
//	        uploadguard.WithStreamToS3(svc.S3, func(c *fiber.Ctx) string {
//	            return "avatars/" + auth.Subject[Claims](c) + ".bin"
//	        }),
//	    ),
//	    handler)
//
// On success the middleware stashes [Result] under Locals key
// `uploadguard.result`. Handlers read it via [ResultFrom].
//
// Failure modes:
//
//   - missing field       → 400  CodeFieldMissing
//   - size exceeded       → 413  CodeSizeExceeded
//   - MIME not allowlisted → 415  CodeMIMENotAllowed
//   - S3 upload error     → 500  CodeUploadFailed
package uploadguard
