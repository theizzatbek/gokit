// Package principalkey exposes the unexported Locals key that auth
// middleware (Bearer, API-key, session-bridge) uses to store the
// Principal on each request, and that auth/From / auth/MustFrom
// read it back from.
//
// Lives under auth/internal/ so only the auth/... subtree (and its
// declared test-helper sibling auth/authtest) can construct the key.
// External callers must go through auth.From[C] / auth.MustFrom[C]
// to access the Principal — never inspect Locals directly.
package principalkey

// Key is the typed empty-struct used as the fiber.Locals key for
// the Principal. Declared as a struct (not a string constant) so two
// unrelated packages storing under the same string slot cannot collide.
type Key struct{}
