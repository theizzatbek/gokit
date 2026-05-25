// Package apimap is the kit's declarative outbound HTTP layer. Upstream
// APIs are described in YAML (clients, endpoints, methods, paths,
// encoding/decoding); the engine loads the YAML, accepts typed request /
// response registrations, validates everything in Build, and returns a
// goroutine-safe *Client. Endpoints are invoked by namespaced name
// (<client>.<endpoint>):
//
//	eng := apimap.New()
//	if err := eng.LoadFile("clients/github.yaml"); err != nil { return err }
//	apimap.RegisterResponse[User](eng, "github.get_user")
//	apimap.RegisterRequest[NewIssue](eng, "github.create_issue")
//	apimap.RegisterResponse[Issue](eng, "github.create_issue")
//	client, err := eng.Build(apimap.WithLogger(logger), apimap.WithMetrics(reg))
//	if err != nil { return err }
//
//	user, err := apimap.Decode[User](ctx, client, "github.get_user",
//	    apimap.Call{Path: map[string]string{"username": "torvalds"}})
//
// Transport is the kit's clients/httpc *http.Client (retry, per-attempt
// timeout, slog + Prometheus). Errors on Decode / Exchange map non-2xx
// status to *errs.Error with Kind derived from the status and a stable
// per-endpoint Code (e.g. apimap_github_get_user_not_found).
//
// See docs/superpowers/specs/2026-05-25-kit-apimap-design.md for the full design.
package apimap
