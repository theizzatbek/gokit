// Package openapi generates OpenAPI 3.0 specifications from a
// fibermap [fibermap.Engine].
//
// The generator reads routes from `Engine.Routes()`, translates path
// params (`:id` → `{id}`), and emits paths/operations using each
// route's metadata (`Name` → `operationId`, `Description`, `Tags`).
//
// Body / query / response schemas, security schemes, and server URLs
// are supplied through the [Generator] builder API:
//
//	gen := openapi.NewGenerator(eng,
//	    openapi.WithInfo(openapi.Info{Title: "Tasks API", Version: "1.0.0"}),
//	    openapi.WithServer("https://api.example.com", "production"),
//	    openapi.WithSecurity("BearerAuth", openapi.HTTPBearer()),
//	    openapi.MapMiddlewareToSecurity("auth", "BearerAuth"),
//	)
//
//	gen.OnHandler("tasks.create").
//	    Body(CreateTaskReq{}).
//	    Response(201, Task{}).
//	    Response(400, ErrorResponse{})
//
//	spec, err := gen.Generate()           // JSON bytes
//
// Operation metadata — `summary`, `description`, `tags` — is taken
// straight from the route's YAML (`summary:`, `description:`,
// `tags:`). The builder is reserved for typed Go schemas; keeping
// the human-readable text in YAML alongside the route keeps a
// single source of truth.
//
// Schema reflection uses invopop/jsonschema and respects
// `json:` and `validate:` struct tags.
package openapi

// Spec is the top-level OpenAPI 3.0 document.
type Spec struct {
	OpenAPI    string              `json:"openapi"`
	Info       Info                `json:"info"`
	Servers    []Server            `json:"servers,omitempty"`
	Paths      map[string]PathItem `json:"paths"`
	Components *Components         `json:"components,omitempty"`
}

// Info is the OpenAPI document metadata block.
type Info struct {
	Title       string   `json:"title"`
	Version     string   `json:"version"`
	Description string   `json:"description,omitempty"`
	Contact     *Contact `json:"contact,omitempty"`
}

// Contact identifies the maintainer.
type Contact struct {
	Name  string `json:"name,omitempty"`
	URL   string `json:"url,omitempty"`
	Email string `json:"email,omitempty"`
}

// Server declares a base URL.
type Server struct {
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
}

// PathItem groups operations by HTTP method on a single path.
type PathItem struct {
	Get        *Operation  `json:"get,omitempty"`
	Post       *Operation  `json:"post,omitempty"`
	Put        *Operation  `json:"put,omitempty"`
	Patch      *Operation  `json:"patch,omitempty"`
	Delete     *Operation  `json:"delete,omitempty"`
	Head       *Operation  `json:"head,omitempty"`
	Options    *Operation  `json:"options,omitempty"`
	Parameters []Parameter `json:"parameters,omitempty"`
}

// Operation is a single HTTP method on a path.
type Operation struct {
	OperationID string                `json:"operationId,omitempty"`
	Summary     string                `json:"summary,omitempty"`
	Description string                `json:"description,omitempty"`
	Tags        []string              `json:"tags,omitempty"`
	Parameters  []Parameter           `json:"parameters,omitempty"`
	RequestBody *RequestBody          `json:"requestBody,omitempty"`
	Responses   map[string]Response   `json:"responses"`
	Security    []map[string][]string `json:"security,omitempty"`
}

// Parameter is a path/query/header parameter on an operation.
type Parameter struct {
	Name        string         `json:"name"`
	In          string         `json:"in"` // path | query | header | cookie
	Required    bool           `json:"required,omitempty"`
	Description string         `json:"description,omitempty"`
	Schema      map[string]any `json:"schema,omitempty"`
}

// RequestBody describes the body of a request.
type RequestBody struct {
	Required    bool                 `json:"required,omitempty"`
	Description string               `json:"description,omitempty"`
	Content     map[string]MediaType `json:"content"`
}

// Response describes one possible response.
type Response struct {
	Description string               `json:"description"`
	Content     map[string]MediaType `json:"content,omitempty"`
}

// MediaType pairs a content-type with a schema.
type MediaType struct {
	Schema map[string]any `json:"schema,omitempty"`
}

// Components holds reusable parts referenced via `$ref`.
type Components struct {
	Schemas         map[string]map[string]any `json:"schemas,omitempty"`
	SecuritySchemes map[string]SecurityScheme `json:"securitySchemes,omitempty"`
}

// SecurityScheme describes one auth mechanism.
type SecurityScheme struct {
	Type             string `json:"type"` // http | apiKey | oauth2 | openIdConnect
	Description      string `json:"description,omitempty"`
	Scheme           string `json:"scheme,omitempty"` // for http: bearer | basic
	BearerFormat     string `json:"bearerFormat,omitempty"`
	Name             string `json:"name,omitempty"` // for apiKey: header/query/cookie name
	In               string `json:"in,omitempty"`   // for apiKey: header | query | cookie
	OpenIDConnectURL string `json:"openIdConnectUrl,omitempty"`
}

// HTTPBearer returns a Bearer-token HTTP security scheme. Use
// `format` to advertise the token format (e.g. "JWT") — empty means
// unspecified.
func HTTPBearer(format ...string) SecurityScheme {
	s := SecurityScheme{Type: "http", Scheme: "bearer"}
	if len(format) > 0 {
		s.BearerFormat = format[0]
	}
	return s
}

// HTTPBasic returns a Basic-auth HTTP security scheme.
func HTTPBasic() SecurityScheme {
	return SecurityScheme{Type: "http", Scheme: "basic"}
}

// APIKey returns an API-key security scheme. `in` must be one of
// "header", "query", "cookie".
func APIKey(name, in string) SecurityScheme {
	return SecurityScheme{Type: "apiKey", Name: name, In: in}
}
