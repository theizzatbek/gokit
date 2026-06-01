// Package webhookguard ships a fiber middleware that verifies the
// signature of an inbound webhook payload against a
// clients/webhooks.Verifier. On signature mismatch the request is
// short-circuited with errs.Unauthorized(CodeSignatureInvalid),
// which the kit ErrorHandler renders as 401.
//
// The middleware reads the full body up to BodyLimit (default
// 1 MiB), passes it to the Verifier, and on success re-injects the
// same body so the downstream handler can read it via c.Body() /
// BodyParser.
package webhookguard
