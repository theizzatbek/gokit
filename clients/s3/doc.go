// Package s3client is the kit's thin AWS S3 wrapper.
//
// Built on `aws-sdk-go-v2/service/s3` — accepts both AWS S3 and
// S3-compatible endpoints (MinIO, Cloudflare R2, DigitalOcean
// Spaces, Backblaze B2) via `Endpoint` + `ForcePathStyle`.
//
// API surface:
//
//   - Put(ctx, key, body, opts...) — upload an io.Reader
//   - Get(ctx, key) — download as io.ReadCloser
//   - Delete(ctx, key) — remove an object
//   - Exists(ctx, key) — HeadObject probe
//   - PresignGet(ctx, key, ttl) — presigned download URL
//   - PresignPut(ctx, key, ttl) — presigned upload URL
//   - List(ctx, prefix) — Go-1.23 range-over-func iterator
//
// Errors map to *errs.Error with stable Code constants
// (s3_not_found, s3_access_denied, s3_unavailable, …) so handlers
// can `return err` and the right HTTP status falls out via
// fibermap.ErrorHandler.
//
// Observability is opt-in via WithLogger + WithMetrics, same
// pattern as the rest of the kit clients.
//
// Package name is `s3client` to avoid colliding with the upstream
// `service/s3` package — same convention as `natsclient` /
// `redisclient`.
package s3client
