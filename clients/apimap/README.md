# clients/apimap

Declarative outbound HTTP layer symmetric to `fibermap` for inbound. Describe upstream APIs in YAML (clients, endpoints, methods, paths, encoding/decoding, per-endpoint timeout/retry overrides, per-client auth); call them by name at runtime via typed `Decode[T]` / `Exchange[Req, Resp]` dispatch.

**Import:** `github.com/theizzatbek/gokit/clients/apimap`
**Depends on:** `gopkg.in/yaml.v3` + `github.com/theizzatbek/gokit/errs` + `github.com/theizzatbek/gokit/clients/httpc`

## Why use it

`clients/httpc` gives you a `*http.Client` with retry. It does NOT solve **endpoint definition** — every project still hand-codes the same per-endpoint URL building, header setting, error-mapping, body-decoding boilerplate. That fragment is repeated, per-endpoint, across every service. `apimap` is the missing layer: endpoints live in YAML; the code calls them by name. One grep across `*.yaml` answers "what external APIs does this service call?"

## Quickstart

`clients.yaml`:

```yaml
clients:
  - name: github
    base_url: https://api.github.com
    timeout: 10s
    max_retries: 3
    default_headers:
      Accept: application/vnd.github+json
    auth:
      type: bearer
      token: ${GITHUB_TOKEN}
    endpoints:
      - name: get_user
        method: GET
        path: /users/{username}
        decode: json
      - name: create_issue
        method: POST
        path: /repos/{owner}/{repo}/issues
        encode: json
        decode: json
```

`main.go`:

```go
eng := apimap.New()
if err := eng.LoadFile("clients.yaml"); err != nil { return err }
apimap.RegisterResponse[User](eng, "github.get_user")
apimap.RegisterRequest[NewIssue](eng, "github.create_issue")
apimap.RegisterResponse[Issue](eng, "github.create_issue")

client, err := eng.Build(apimap.WithLogger(logger), apimap.WithMetrics(promReg))

user, err := apimap.Decode[User](ctx, client, "github.get_user",
    apimap.Call{Path: map[string]string{"username": "torvalds"}})
```

## YAML schema

```yaml
clients:
  - name: <string>                          # required, unique within engine
    base_url: <absolute URL>                # optional; omit for "open client" mode (caller passes Call.URL)
    timeout: <duration>                     # optional → httpc.Config.Timeout
    max_retries: <int>                      # optional → httpc.Config.MaxRetries
    backoff_base: <duration>                # optional → httpc.Config.BackoffBase
    backoff_max: <duration>                 # optional → httpc.Config.BackoffMax
    default_headers:                        # optional, applied to every endpoint
      <Header-Name>: <value>
    auth:                                   # optional; one of basic|bearer|header|custom|none
      type: basic
      username: <string>
      password: <string>
    # — or —
    #   type: bearer
    #   token: <string>
    # — or —
    #   type: header
    #   name: <Header-Name>
    #   value: <string>
    # — or —
    #   type: custom
    #   name: <signer-id>   # must match a RegisterAuth registration
    # — or —
    #   type: none
    endpoints:
      - name: <string>                      # required, unique within client
        method: GET|HEAD|POST|PUT|PATCH|DELETE|OPTIONS
        path: <string with {var} segments>  # e.g. /users/{username}
        encode: json|form|raw|none          # default "none"
        decode: json|raw|none               # default "none"
        headers:                            # optional, merged over default_headers
          <Header-Name>: <value>
        timeout: <duration>                 # optional, overrides client-level
        max_retries: <int>                  # optional, overrides client-level
        backoff_base: <duration>            # optional
        backoff_max: <duration>             # optional
```

### Env-var substitution

`${VAR_NAME}` anywhere in the YAML is replaced from `os.Getenv` at LoadFile time (regex `[A-Z_][A-Z0-9_]*` — uppercase only). Missing env → `*errs.Error{Code: "apimap_env_var_unset"}`. Use this for secrets — never literal-write tokens.

### Explicit env values via `WithEnv`

If your service already has typed config, pass values explicitly instead of relying on process env:

```go
e := apimap.New(apimap.WithEnv(map[string]string{
    "MICROLINK_BASE_URL": cfg.MicrolinkBaseURL,
}))
e.LoadFile("clients.yaml")
```

`WithEnv` map is consulted first; on miss, falls back to `os.LookupEnv`. Both miss → `apimap_env_var_unset`. Useful when typed config is the source of truth and you don't want apimap-only values polluting process env.

## Public API

```go
type Engine struct{ /* unexported */ }
type Client struct{ /* unexported */ }

// Engine lifecycle (build-once)
func New() *Engine
func (e *Engine) LoadFile(path string) error
func (e *Engine) LoadBytes(b []byte) error
func RegisterRequest[T any](e *Engine, endpoint string)       // optional — enforces Exchange[T,_]
func RegisterResponse[T any](e *Engine, endpoint string)      // optional — enforces Decode[T] / Exchange[_,T]
func (e *Engine) Build(opts ...Option) (*Client, error)

// Options
func WithLogger(*slog.Logger) Option        // → httpc.WithLogger
func WithMetrics(prometheus.Registerer) Option  // → httpc.WithMetrics
func WithBaseTransport(http.RoundTripper) Option // → httpc.WithBaseTransport

// Runtime calls
type Call struct {
    Path    map[string]string  // {var} substitution; URL-escaped
    Query   url.Values         // merged over endpoint defaults (per-key override)
    Headers http.Header        // merged last over default + auth + endpoint headers
    Body    any                // used by Do(); Exchange() takes body as separate arg
}

// Untyped — returns stdlib *http.Response, caller decodes + closes Body
func (c *Client) Do(ctx context.Context, endpoint string, call Call) (*http.Response, error)

// Typed — uses endpoint.decode, maps non-2xx to *errs.Error, closes Body
func Decode[Resp any](ctx context.Context, c *Client, endpoint string, call Call) (Resp, error)

// Typed with request body — encodes per endpoint.encode, decodes per endpoint.decode
func Exchange[Req, Resp any](ctx context.Context, c *Client, endpoint string, body Req, call Call) (Resp, error)
```

## Common patterns

### Headers precedence

When multiple sources set the same header, last wins:

1. `client.default_headers` (YAML)
2. **Auth-derived header** (`Authorization` from `auth:` block)
3. `endpoint.headers` (YAML)
4. `Call.Headers` (per-call)

Endpoint can override auth (rare; useful for debugging). `Call.Headers` always wins so tests + per-call overrides remain possible.

### Per-endpoint timeout/retry override

```yaml
endpoints:
  - name: list_repos
    method: GET
    path: /users/{user}/repos
    # uses client-level timeout / max_retries
  - name: bulk_index
    method: POST
    path: /search/index
    timeout: 60s       # this one is slow
    max_retries: 0     # this one must not retry
    encode: json
    decode: json
```

At Build, endpoints with overrides get their own `*http.Client` (via `httpc.New`); endpoints without overrides share the per-API-client `*http.Client`. Free per-endpoint retry/timeout policy.

### Open client (ad-hoc URLs, multi-host fetcher)

When the upstream isn't one stable origin — e.g. a metadata fetcher that pulls
arbitrary user-supplied URLs, a webhook responder that posts to caller-provided
endpoints, a sandbox/prod switchboard — omit `base_url` and pass the full URL
per request via `Call.URL`:

```yaml
clients:
  - name: web
    # base_url omitted → "open client"
    timeout: 10s
    max_retries: 2
    default_headers:
      User-Agent: urlshort/1.0
    endpoints:
      - name: fetch
        method: GET
        decode: raw       # most open-client uses are "give me the body bytes"
```

```go
body, err := apimap.Decode[[]byte](ctx, client, "web.fetch", apimap.Call{
    URL: "https://nytimes.com/some-article",
})
```

**Rules:**
- The two URL sources are mutually exclusive — declaring `base_url` AND passing `Call.URL` returns `*errs.Error{Code: "apimap_url_conflict"}` at request time.
- Open client + empty `Call.URL` → `apimap_missing_request_url`.
- Open client + `Call.Path` → `apimap_unknown_path_var` (no template to substitute against).
- `Call.Query` still merges over the URL's existing query string.
- All client-level knobs (timeout, max_retries, backoff, default_headers, auth, custom signers) apply as usual.

**When to prefer open client over a raw `*http.Client`:** if you want the kit's unified observability (slog + Prometheus), the `*errs.Error` mapping on non-2xx, the typed `Decode[T] / Exchange[Req,Resp]` API, or YAML-level retry/timeout knobs — even when the URL is dynamic. If none of that matters, the bare `httpc.New(...)` is one less indirection.

**Connection pooling caveat:** open clients commonly call many different hosts. Go's `http.DefaultTransport` pools per-host, so this is fine for moderate traffic; if you're hitting thousands of distinct hosts/sec, tune `MaxIdleConnsPerHost` via a custom transport passed through `WithBaseTransport`.

### Auth declared in YAML

Three header-style shapes plus one extensible shape:

```yaml
auth: {type: basic,  username: ${BASIC_USER}, password: ${BASIC_PASS}}
auth: {type: bearer, token: ${API_TOKEN}}
auth: {type: header, name: X-API-Key, value: ${API_KEY}}
auth: {type: custom, name: payments_hmac}    # see below
auth: {type: none}                            # or omit auth entirely
```

For `basic` / `bearer` / `header` the resolved header is applied automatically before sending. `Call.Headers["Authorization"]` can still override per-call.

### Custom signing (HMAC, mTLS-signed payloads, request-bound signatures)

When the upstream needs a signature that depends on the per-request method, path, body, timestamp or nonce — anything that cannot precompute into a static header — declare `auth.type=custom` and register a request-mutating function under the same `name`:

```yaml
clients:
  - name: payments
    base_url: ${PAYMENTS_URL}
    auth:
      type: custom
      name: payments_hmac
    endpoints:
      - {name: charge, method: POST, path: /v1/charges, encode: json, decode: json}
```

```go
eng := apimap.New()
_ = eng.LoadFile("clients.yaml")
apimap.RegisterAuth(eng, "payments_hmac", func(req *http.Request) error {
    ts := strconv.FormatInt(time.Now().Unix(), 10)
    // Compute HMAC over method + path + ts + body (read via GetBody so the
    // body stream stays available for the actual send + future retries).
    var bodyBytes []byte
    if req.GetBody != nil {
        b, _ := req.GetBody()
        if b != nil {
            defer b.Close()
            bodyBytes, _ = io.ReadAll(b)
        }
    }
    mac := hmac.New(sha256.New, []byte(os.Getenv("PAYMENTS_SECRET")))
    fmt.Fprintf(mac, "%s\n%s\n%s\n", req.Method, req.URL.Path, ts)
    mac.Write(bodyBytes)
    req.Header.Set("X-Timestamp", ts)
    req.Header.Set("X-Signature", hex.EncodeToString(mac.Sum(nil)))
    return nil
})
client, err := eng.Build(...)
```

**Layering and retries.** The signer sits *below* httpc's retry layer — it runs once per network attempt. If the server returns a transient 5xx/429 and httpc retries, the signer re-fires with a fresh `*http.Request` whose body is restored from `req.GetBody`, producing a fresh signature/timestamp. Timestamp-bearing schemes with tight clock-skew windows survive retries.

**Reading the body.** Use `req.GetBody()` (returns a fresh `io.ReadCloser`) — never `req.Body` directly, otherwise the stream is consumed before the upstream sees it. `httpc` populates `GetBody` for all body-carrying methods.

**Errors.** If `fn` returns an error, the request never leaves; the error surfaces as the `Do` / `Decode` / `Exchange` return value. Wrap with `*errs.Error{KindInternal}` if you want a stable Code.

**Build-time validation.** If YAML references `auth.name=foo` but `RegisterAuth(eng, "foo", ...)` was never called, `Build` returns `*errs.Error{Code: "apimap_unknown_custom_auth"}`. Duplicate `RegisterAuth` for the same name panics at registration time (programmer error).

**Per-client only.** Each client picks its own signer; endpoints inside a client all share that client's signer. If you need different signing schemes for different endpoints of the same API, split them into separate clients.

### Typed Register* (optional, runtime-checked)

`RegisterRequest[T]` / `RegisterResponse[T]` are optional but, when set,
they bind the endpoint to a specific Go type. `Decode[U]` / `Exchange[U,V]`
then check that the call's generics match the registration at runtime:

```go
type IssueResp struct { Number int }
apimap.RegisterResponse[IssueResp](eng, "gh.get_issue")
client, _ := eng.Build(...)

// OK:
out, _ := apimap.Decode[IssueResp](ctx, client, "gh.get_issue", apimap.Call{})

// PANICS at call time with *errs.Error{Code: "apimap_type_mismatch"}:
_, _ = apimap.Decode[OtherShape](ctx, client, "gh.get_issue", apimap.Call{})
```

Same check on the Req side for `Exchange`. Endpoints without a
registration accept any generic — registration is opt-in. Build still
validates that every registered name exists in the YAML
(`apimap_registered_endpoint_missing`).

Why panic and not return an error? Because Decode[Wrong] is a programmer
mistake — silent JSON-decode of the wrong shape leads to nil zeros in
production. Panic surfaces it in the first test run.

### Body encoding modes

| `encode:` | Body type accepted | Content-Type set |
|---|---|---|
| `none` (default) | any (ignored) | — |
| `json` | any `json.Marshal`-able | `application/json` |
| `form` | `url.Values` or `map[string]string` | `application/x-www-form-urlencoded` |
| `raw` | `io.Reader` | — (caller sets via Call.Headers) |

Type mismatches return `*errs.Error{Code: "apimap_unsupported_body_type"}`.

### Response decoding modes

| `decode:` | What `Decode[T]` returns |
|---|---|
| `none` (default) | `Decode[T]` returns zero T; body drained |
| `json` | `json.NewDecoder(resp.Body).Decode(&out)` |
| `raw` | T must be `[]byte` (whole body) or `io.ReadCloser` (caller closes) |

### Error mapping (non-2xx in `Decode` / `Exchange`)

| Status | `errs.Kind` | `errs.Error.Code` |
|---|---|---|
| 400 | Validation | `apimap_<client>_<endpoint>_bad_request` |
| 401 | Unauthorized | `apimap_<client>_<endpoint>_unauthorized` |
| 403 | Permission | `apimap_<client>_<endpoint>_forbidden` |
| 404 | NotFound | `apimap_<client>_<endpoint>_not_found` |
| 409 | Conflict | `apimap_<client>_<endpoint>_conflict` |
| 429 | RateLimited | `apimap_<client>_<endpoint>_rate_limited` |
| other 4xx | Validation | `apimap_<client>_<endpoint>_client_error` |
| 5xx | Internal | `apimap_<client>_<endpoint>_server_error` |

`*errs.Error.Details` carries `[]FieldError` entries: `{status, url, body (truncated to 4KB)}`.

`Do()` does NOT convert non-2xx to error — it passes `*http.Response` through unchanged. That's the escape hatch for streaming downloads and custom decoding.

## Build-time validation

`Engine.Build()` aggregates every validation failure via `errors.Join`. Codes:

| Code | When |
|---|---|
| `apimap_no_clients` | YAML loaded but `clients:` empty |
| `apimap_duplicate_client` | two clients share `name` |
| `apimap_duplicate_endpoint` | two endpoints share `name` within one client |
| `apimap_missing_client_name` | client without `name` |
| `apimap_invalid_base_url` | non-absolute or unparseable URL |
| `apimap_invalid_method` | HTTP method outside the allowed set |
| `apimap_invalid_path_var` | bad `{var}` shape or duplicate in one path |
| `apimap_invalid_encode` / `apimap_invalid_decode` | unknown mode |
| `apimap_auth_invalid_type` | type not in basic/bearer/header/none |
| `apimap_auth_missing_field` | required field for the chosen type missing |
| `apimap_env_var_unset` / `apimap_env_var_malformed` | `${VAR}` resolution failed |
| `apimap_registered_endpoint_missing` | `Register*` named an endpoint not in YAML |
| `apimap_already_built` | `Build()` called twice |

Runtime codes from `Do`/`Decode`/`Exchange`: `apimap_unknown_endpoint`, `apimap_missing_path_var`, `apimap_unknown_path_var`, `apimap_encode_failed`, `apimap_decode_failed`, `apimap_unsupported_body_type`, `apimap_unsupported_decode_type`, plus the dynamic per-endpoint status codes above.

## Observability

Pass-through to the underlying `clients/httpc`. apimap itself does not log or expose Prometheus collectors — duplication across layers is noise. If you want per-endpoint metrics, wrap your own middleware around `Decode`/`Exchange`.

## Testing

Override `${MICROLINK_BASE_URL}` (or your env var) to point at a `httptest.NewServer`:

```go
srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")
    io.WriteString(w, `{"status":"success","data":{"title":"…"}}`)
}))
t.Cleanup(srv.Close)

t.Setenv("MICROLINK_BASE_URL", srv.URL)
eng := apimap.New()
_ = eng.LoadBytes([]byte(`clients:
  - name: ml
    base_url: ${MICROLINK_BASE_URL}
    endpoints: [{name: get, method: GET, path: /, decode: json}]
`))
apimap.RegisterResponse[Resp](eng, "ml.get")
client, _ := eng.Build()
out, _ := apimap.Decode[Resp](context.Background(), client, "ml.get", apimap.Call{})
```

## Limitations

- **No OpenAPI ingest.** Manual YAML; future tool could derive from a remote API spec.
- **No codegen.** Runtime dispatch only — types are registered at startup, not generated.
- **No hot-reload.** YAML loaded once at startup.
- **No per-endpoint metrics out of the box** (handled by `clients/httpc` at the underlying level).
- **OAuth2/refresh-token flows out of scope.** Use `auth:` for one static credential; for dynamic-secret refresh on each call (e.g. periodic token rotation) declare `auth.type=custom` and have your signer fetch the current token, or wrap `http.RoundTripper` via `WithBaseTransport`.
- **Per-endpoint `auth:` blocks not supported.** Auth is a property of the upstream API as a whole; override per-call via `Call.Headers`.
- **Streaming uploads** beyond `encode: raw` with an `io.Reader` are out of scope.

## See also

- [`clients/httpc`](../httpc/README.md) — the underlying `*http.Client` builder
- [`errs`](../../errs/README.md) — error contract
- [`examples/urlshort`](../../examples/urlshort/README.md) — apimap calling MicroLink in the enrich package
