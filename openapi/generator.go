package openapi

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/invopop/jsonschema"
	"github.com/theizzatbek/fibermap"
)

// Generator builds an OpenAPI 3.0 spec from a fibermap engine.
// Construct via [NewGenerator]; attach per-handler request/response
// schemas with [Generator.OnHandler]; produce JSON bytes with
// [Generator.Generate].
//
// Generator is generic over T (the engine's per-request payload
// type) — solely so it can hold a typed [fibermap.Engine] pointer.
// None of T's fields are read; the engine's introspection API is the
// only thing that's used.
type Generator[T any] struct {
	eng *fibermap.Engine[T]
	cfg *config

	handlerSchemas map[string]*HandlerSchemaBuilder

	reflector *jsonschema.Reflector
}

// NewGenerator constructs a Generator for `eng`, applying the given
// options. Use the returned Generator's `OnHandler` to attach typed
// request/response schemas, then call `Generate` to produce JSON.
func NewGenerator[T any](eng *fibermap.Engine[T], opts ...Option) *Generator[T] {
	cfg := newConfig()
	for _, opt := range opts {
		opt(cfg)
	}
	return &Generator[T]{
		eng:            eng,
		cfg:            cfg,
		handlerSchemas: map[string]*HandlerSchemaBuilder{},
		reflector: &jsonschema.Reflector{
			// Do NOT use ref-by-id for anonymous structs — keep schemas
			// inline so the generated spec is portable.
			DoNotReference: false,
			// Inline definitions under #/components/schemas instead of
			// the JSON-Schema-default #/$defs path. We rewrite paths in
			// the final document.
			ExpandedStruct: false,
		},
	}
}

// HandlerSchemaBuilder collects request and response schemas for one
// handler (by registered name). Methods chain so wiring is one
// expression per handler:
//
//	gen.OnHandler("tasks.create").
//	    Body(CreateTaskReq{}).
//	    Query(ListQuery{}).
//	    Response(201, Task{}).
//	    Response(400, ErrorResponse{})
//
// Operation metadata — `summary`, `description`, `tags` — is taken
// from `routes.yaml` (the `summary:`, `description:`, `tags:` fields
// on each route). Keep documentation declarative alongside the
// route definition; the builder is reserved for typed Go schemas.
type HandlerSchemaBuilder struct {
	name      string
	body      any
	query     any
	headers   any
	responses map[int]any
}

// OnHandler returns (or creates) the schema builder for the handler
// registered under `name`. Multiple calls return the same builder,
// so configuration can be split across files / packages if needed.
func (g *Generator[T]) OnHandler(name string) *HandlerSchemaBuilder {
	b, ok := g.handlerSchemas[name]
	if !ok {
		b = &HandlerSchemaBuilder{name: name, responses: map[int]any{}}
		g.handlerSchemas[name] = b
	}
	return b
}

// Body attaches a JSON request-body schema. Pass the zero value of
// the request struct: `b.Body(CreateTaskReq{})`. The struct is
// reflected via invopop/jsonschema, honouring `json:` and `validate:`
// tags.
func (b *HandlerSchemaBuilder) Body(model any) *HandlerSchemaBuilder {
	b.body = model
	return b
}

// Query attaches a schema for query parameters. Fields use the
// `query:` tag (Fiber's convention) to name each parameter.
func (b *HandlerSchemaBuilder) Query(model any) *HandlerSchemaBuilder {
	b.query = model
	return b
}

// Headers attaches a schema for request headers. Fields use the
// `reqHeader:` tag.
func (b *HandlerSchemaBuilder) Headers(model any) *HandlerSchemaBuilder {
	b.headers = model
	return b
}

// Response attaches the schema for one response status code. The
// schema describes the JSON body; status `204` (No Content) should
// pass `nil` to advertise an empty body.
func (b *HandlerSchemaBuilder) Response(status int, model any) *HandlerSchemaBuilder {
	b.responses[status] = model
	return b
}

// Generate returns the OpenAPI 3.0 document as JSON. Errors:
//
//   - a middleware mapped via MapMiddlewareToSecurity points to a
//     security scheme name that wasn't registered via WithSecurity;
//   - a reflection failure in a registered Body/Query/Headers/Response
//     model (unlikely with Go structs).
func (g *Generator[T]) Generate() ([]byte, error) {
	if err := g.validateConfig(); err != nil {
		return nil, err
	}

	components := &Components{
		Schemas:         map[string]map[string]any{},
		SecuritySchemes: g.cfg.securitySchemes,
	}

	paths := map[string]PathItem{}
	for r := range g.eng.All() {
		opPath := toOpenAPIPath(r.Path)
		method := strings.ToUpper(r.Method)
		op, err := g.buildOperation(r, components)
		if err != nil {
			return nil, fmt.Errorf("openapi: building operation %s %s: %w", method, r.Path, err)
		}
		item := paths[opPath]
		attachOperation(&item, method, op)
		paths[opPath] = item
	}

	if len(components.Schemas) == 0 {
		components.Schemas = nil
	}
	if len(components.SecuritySchemes) == 0 {
		components.SecuritySchemes = nil
	}
	var comp *Components
	if components.Schemas != nil || components.SecuritySchemes != nil {
		comp = components
	}

	spec := Spec{
		OpenAPI:    "3.0.3",
		Info:       g.cfg.info,
		Servers:    g.cfg.servers,
		Paths:      paths,
		Components: comp,
	}
	return marshalSorted(spec)
}

func (g *Generator[T]) validateConfig() error {
	for mw, scheme := range g.cfg.middlewareSecurity {
		if _, ok := g.cfg.securitySchemes[scheme]; !ok {
			return fmt.Errorf("openapi: middleware %q mapped to security scheme %q, but the scheme is not registered (use WithSecurity)", mw, scheme)
		}
	}
	return nil
}

// buildOperation converts one fibermap RouteInfo into an OpenAPI
// Operation, registering any referenced schemas in `components`.
func (g *Generator[T]) buildOperation(r fibermap.RouteInfo, components *Components) (*Operation, error) {
	op := &Operation{
		OperationID: r.Name,
		Summary:     r.Summary,
		Description: r.Description,
		Tags:        append([]string(nil), r.Tags...),
		Responses:   map[string]Response{},
	}

	// Path params from the original Fiber-style route ("/users/:id").
	for _, p := range extractPathParams(r.Path) {
		op.Parameters = append(op.Parameters, Parameter{
			Name:     p,
			In:       "path",
			Required: true,
			Schema:   map[string]any{"type": "string"},
		})
	}

	// Security: any middleware in this route's chain that's mapped
	// to a scheme contributes to the security requirement.
	var sec []map[string][]string
	for _, mw := range r.Middleware {
		if scheme, ok := g.cfg.middlewareSecurity[mw.Name]; ok {
			sec = append(sec, map[string][]string{scheme: {}})
		}
	}
	op.Security = sec

	// Optional typed schemas for the handler.
	if b, ok := g.handlerSchemas[r.Handler]; ok {
		if b.body != nil {
			schema, err := g.reflectSchema(b.body, components)
			if err != nil {
				return nil, fmt.Errorf("body: %w", err)
			}
			op.RequestBody = &RequestBody{
				Required: true,
				Content:  map[string]MediaType{"application/json": {Schema: schema}},
			}
		}
		if b.query != nil {
			params, err := g.reflectParams(b.query, "query", "query")
			if err != nil {
				return nil, fmt.Errorf("query: %w", err)
			}
			op.Parameters = append(op.Parameters, params...)
		}
		if b.headers != nil {
			params, err := g.reflectParams(b.headers, "reqHeader", "header")
			if err != nil {
				return nil, fmt.Errorf("headers: %w", err)
			}
			op.Parameters = append(op.Parameters, params...)
		}
		for status, model := range b.responses {
			resp := Response{Description: defaultStatusDescription(status)}
			if model != nil {
				schema, err := g.reflectSchema(model, components)
				if err != nil {
					return nil, fmt.Errorf("response %d: %w", status, err)
				}
				resp.Content = map[string]MediaType{"application/json": {Schema: schema}}
			}
			op.Responses[fmt.Sprintf("%d", status)] = resp
		}
	}

	// If no responses were declared, fall back to a generic 200.
	if len(op.Responses) == 0 {
		op.Responses["200"] = Response{Description: "OK"}
	}

	return op, nil
}

// reflectSchema runs invopop/jsonschema on `model`, hoists every
// referenced definition into components/schemas under a stable name,
// and returns either an inline $ref or the inline schema body.
func (g *Generator[T]) reflectSchema(model any, components *Components) (map[string]any, error) {
	s := g.reflector.Reflect(model)
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	var asMap map[string]any
	if err := json.Unmarshal(raw, &asMap); err != nil {
		return nil, err
	}
	// invopop's schemas use $defs and refer via "$ref": "#/$defs/Foo".
	// OpenAPI 3 expects "#/components/schemas/Foo". Rewrite both.
	if defs, ok := asMap["$defs"].(map[string]any); ok {
		for name, def := range defs {
			if dm, ok := def.(map[string]any); ok {
				rewriteRefs(dm, "#/$defs/", "#/components/schemas/")
				components.Schemas[name] = dm
			}
		}
		delete(asMap, "$defs")
	}
	rewriteRefs(asMap, "#/$defs/", "#/components/schemas/")

	// If the top-level schema is a $ref, return just that ref.
	if ref, ok := asMap["$ref"].(string); ok && len(asMap) == 1 {
		return map[string]any{"$ref": ref}, nil
	}
	return asMap, nil
}

// reflectParams turns a struct's fields into a slice of OpenAPI
// Parameters. `tagName` is the struct-tag key fibermap uses
// (`query`, `reqHeader`); `in` is the OpenAPI parameter location
// (`query`, `header`).
func (g *Generator[T]) reflectParams(model any, tagName, in string) ([]Parameter, error) {
	s := g.reflector.Reflect(model)
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	var asMap map[string]any
	if err := json.Unmarshal(raw, &asMap); err != nil {
		return nil, err
	}
	props, _ := asMap["properties"].(map[string]any)
	required := map[string]bool{}
	if r, ok := asMap["required"].([]any); ok {
		for _, name := range r {
			if s, ok := name.(string); ok {
				required[s] = true
			}
		}
	}

	var params []Parameter
	for name, raw := range props {
		schemaMap, _ := raw.(map[string]any)
		params = append(params, Parameter{
			Name:     name,
			In:       in,
			Required: required[name],
			Schema:   schemaMap,
		})
	}
	// Deterministic order.
	sort.Slice(params, func(i, j int) bool { return params[i].Name < params[j].Name })
	_ = tagName // not consumed — reflector reads JSON tags by default; both query and reqHeader produce property names verbatim.
	return params, nil
}

// rewriteRefs walks `m` recursively, replacing the prefix of every
// "$ref" string from `from` to `to`.
func rewriteRefs(m map[string]any, from, to string) {
	for k, v := range m {
		switch tv := v.(type) {
		case string:
			if k == "$ref" && strings.HasPrefix(tv, from) {
				m[k] = to + strings.TrimPrefix(tv, from)
			}
		case map[string]any:
			rewriteRefs(tv, from, to)
		case []any:
			for _, item := range tv {
				if inner, ok := item.(map[string]any); ok {
					rewriteRefs(inner, from, to)
				}
			}
		}
	}
}
