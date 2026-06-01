package webhooks

// Stable error Code constants returned by clients/webhooks and
// clients/webhooks/storepg. Switch on these in callers / dashboards.
const (
	CodeSubscriptionNotFound = "webhooks_subscription_not_found"
	CodeInvalidURL           = "webhooks_invalid_url"
	CodeInvalidEventTypes    = "webhooks_invalid_event_types"
	CodePayloadTooLarge      = "webhooks_payload_too_large"
	CodeMissingSecret        = "webhooks_missing_secret"
	CodeStorepgNoKey         = "webhooks_storepg_no_key"
	CodeStorepgDecryptFailed = "webhooks_storepg_decrypt_failed"
	CodeSignatureInvalid     = "webhook_signature_invalid" // inbound middleware
)

// MaxPayloadBytes caps the per-event payload size at Fanout time.
// 64 KiB matches typical webhook size limits at popular providers
// (Stripe/GitHub) and prevents bloating webhook_deliveries.
const MaxPayloadBytes = 64 << 10
