package apimap

import (
	"os"
	"reflect"

	xerrs "github.com/theizzatbek/fibermap/errs"
)

// Engine is the build-once configurator. New → LoadFile/LoadBytes (n) →
// RegisterRequest/RegisterResponse (n) → Build (once).
type Engine struct {
	clients []rawClient

	reqTypes  map[string]reflect.Type // endpoint full-name → registered request type
	respTypes map[string]reflect.Type // endpoint full-name → registered response type

	built bool
}

// New returns an empty Engine.
func New() *Engine {
	return &Engine{
		reqTypes:  map[string]reflect.Type{},
		respTypes: map[string]reflect.Type{},
	}
}

// LoadFile reads a YAML file and appends its clients to the engine.
// May be called multiple times (one file per upstream API is the
// expected pattern).
func (e *Engine) LoadFile(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return xerrs.Wrap(err, xerrs.KindValidation, CodeInvalidBaseURL,
			"apimap: read yaml file: "+path)
	}
	return e.LoadBytes(b)
}

// LoadBytes parses and appends YAML content.
func (e *Engine) LoadBytes(b []byte) error {
	cfg, err := parseBytes(b)
	if err != nil {
		return err
	}
	e.clients = append(e.clients, cfg.Clients...)
	return nil
}

// RegisterRequest records the Go type used by Exchange for the given
// endpoint. Panics with *errs.Error if called after Build (matches
// fibermap's RegisterHandler convention — duplicate or post-Build
// registration is a programmer error at startup).
func RegisterRequest[T any](e *Engine, endpoint string) {
	registerType(e, endpoint, e.reqTypes, reflect.TypeOf((*T)(nil)).Elem(), "request")
}

// RegisterResponse records the Go type used by Decode/Exchange for the
// given endpoint. Same panic semantics as RegisterRequest.
func RegisterResponse[T any](e *Engine, endpoint string) {
	registerType(e, endpoint, e.respTypes, reflect.TypeOf((*T)(nil)).Elem(), "response")
}

func registerType(e *Engine, endpoint string, store map[string]reflect.Type, t reflect.Type, role string) {
	if e.built {
		panic(xerrs.Validationf(CodeAlreadyBuilt,
			"apimap: cannot register %s type for %q after Build", role, endpoint))
	}
	if _, exists := store[endpoint]; exists {
		panic(xerrs.Validationf(CodeDuplicateEndpoint,
			"apimap: duplicate %s registration for endpoint %q", role, endpoint))
	}
	store[endpoint] = t
}

// registrationSet returns the union of req+resp registration keys, used
// by validate() in Build (T8).
func (e *Engine) registrationSet() map[string]struct{} {
	out := map[string]struct{}{}
	for k := range e.reqTypes {
		out[k] = struct{}{}
	}
	for k := range e.respTypes {
		out[k] = struct{}{}
	}
	return out
}
