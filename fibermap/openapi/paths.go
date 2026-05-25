package openapi

import (
	"encoding/json"
	"strings"
)

// toOpenAPIPath converts a Fiber-style route ("/users/:id/posts/:postId")
// to OpenAPI-style ("/users/{id}/posts/{postId}").
//
// Wildcard segments (`*`) are not standardised by OpenAPI; we leave
// them alone so the user can post-process if they need to.
func toOpenAPIPath(path string) string {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		if strings.HasPrefix(p, ":") {
			name := strings.TrimPrefix(p, ":")
			// Optional params in fiber use `:name?` — strip the `?`
			// for OpenAPI (we'd advertise this via `required: false`
			// on the parameter, but for the path-template translation
			// only the name matters).
			name = strings.TrimSuffix(name, "?")
			parts[i] = "{" + name + "}"
		}
	}
	return strings.Join(parts, "/")
}

// extractPathParams returns each `:name` segment in `path`, in the
// order they appear. `:name?` becomes `name` (the optionality info
// is lost — OpenAPI path parameters are always required).
func extractPathParams(path string) []string {
	var out []string
	for _, p := range strings.Split(path, "/") {
		if !strings.HasPrefix(p, ":") {
			continue
		}
		name := strings.TrimPrefix(p, ":")
		name = strings.TrimSuffix(name, "?")
		out = append(out, name)
	}
	return out
}

// attachOperation sets `item.<Method>` to `op`, indexed by the
// uppercase HTTP method name (matching what fibermap stores in
// RouteInfo.Method).
func attachOperation(item *PathItem, method string, op *Operation) {
	switch method {
	case "GET":
		item.Get = op
	case "POST":
		item.Post = op
	case "PUT":
		item.Put = op
	case "PATCH":
		item.Patch = op
	case "DELETE":
		item.Delete = op
	case "HEAD":
		item.Head = op
	case "OPTIONS":
		item.Options = op
	}
}

// defaultStatusDescription returns a one-line description for common
// HTTP status codes — better than an empty Description, which
// OpenAPI 3.0 forbids.
func defaultStatusDescription(status int) string {
	switch status {
	case 200:
		return "OK"
	case 201:
		return "Created"
	case 202:
		return "Accepted"
	case 204:
		return "No Content"
	case 301:
		return "Moved Permanently"
	case 302:
		return "Found"
	case 304:
		return "Not Modified"
	case 400:
		return "Bad Request"
	case 401:
		return "Unauthorized"
	case 403:
		return "Forbidden"
	case 404:
		return "Not Found"
	case 405:
		return "Method Not Allowed"
	case 408:
		return "Request Timeout"
	case 409:
		return "Conflict"
	case 422:
		return "Unprocessable Entity"
	case 429:
		return "Too Many Requests"
	case 500:
		return "Internal Server Error"
	case 502:
		return "Bad Gateway"
	case 503:
		return "Service Unavailable"
	case 504:
		return "Gateway Timeout"
	default:
		return "Response"
	}
}

// marshalSorted MarshalIndent's `v` with deterministic key ordering.
// Go's encoding/json already emits map keys in sorted order, so this
// is just a thin alias plus a 2-space indent.
func marshalSorted(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}
