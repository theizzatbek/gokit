package webhooks

import "time"

// Verifier validates an inbound webhook payload against a partner-
// specific signature scheme. Implementations live in
// clients/webhooks/verifiers (GenericHMAC, GitHub) and can be
// added externally.
//
// headers is the raw http.Header / fasthttp header map (key →
// values); body is the verbatim request body. now is injected so
// tests can pin the clock.
type Verifier interface {
	Verify(headers map[string][]string, body []byte, now time.Time) error
}
