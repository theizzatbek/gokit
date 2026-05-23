package fibermap

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

var validHTTPMethods = map[string]struct{}{
	http.MethodGet:     {},
	http.MethodPost:    {},
	http.MethodPut:     {},
	http.MethodPatch:   {},
	http.MethodDelete:  {},
	http.MethodHead:    {},
	http.MethodOptions: {},
}

// UnmarshalYAML decodes a middleware reference. Two accepted shapes:
//   - scalar string         → mwRef{Name: "x"}
//   - single-key mapping    → mwRef{Name: "x", Args: ["a", "b"]}
//     where the value must be a sequence of strings (may be empty).
func (m *mwRef) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		m.Name = node.Value
		return nil
	case yaml.MappingNode:
		if len(node.Content) != 2 {
			return &Error{Stage: "parse", Code: CodeInvalidYAML,
				Message: "middleware mapping must have exactly one key", Line: node.Line}
		}
		key, val := node.Content[0], node.Content[1]
		if key.Kind != yaml.ScalarNode {
			return &Error{Stage: "parse", Code: CodeInvalidYAML,
				Message: "middleware mapping key must be a scalar", Line: key.Line}
		}
		m.Name = key.Value
		if val.Kind != yaml.SequenceNode {
			return &Error{Stage: "parse", Code: CodeInvalidYAML,
				Message: "middleware factory args must be a sequence of strings", Line: val.Line}
		}
		m.Args = make([]string, 0, len(val.Content))
		for _, item := range val.Content {
			if item.Kind != yaml.ScalarNode {
				return &Error{Stage: "parse", Code: CodeInvalidYAML,
					Message: "middleware factory arg must be a scalar string", Line: item.Line}
			}
			m.Args = append(m.Args, item.Value)
		}
		return nil
	default:
		return &Error{Stage: "parse", Code: CodeInvalidYAML,
			Message: "middleware entry must be a string or single-key mapping", Line: node.Line}
	}
}

// rawGroupAlias and rawRouteAlias break the recursive UnmarshalYAML call
// (alias trick: aliases drop the method set, so node.Decode hits the
// default reflection path).
type rawGroupAlias rawGroup
type rawRouteAlias rawRoute

// UnmarshalYAML captures the source line for rawGroup so parse/mount
// errors can point the user at the offending YAML row.
func (g *rawGroup) UnmarshalYAML(node *yaml.Node) error {
	var a rawGroupAlias
	if err := node.Decode(&a); err != nil {
		return err
	}
	*g = rawGroup(a)
	g.Line = node.Line
	return nil
}

// UnmarshalYAML — same as rawGroup, for routes.
func (r *rawRoute) UnmarshalYAML(node *yaml.Node) error {
	var a rawRouteAlias
	if err := node.Decode(&a); err != nil {
		return err
	}
	*r = rawRoute(a)
	r.Line = node.Line
	return nil
}

// UnmarshalYAML for rawCache accepts two shapes:
//
//	cache: 30s                          # scalar string → TTL only
//	cache:                              # mapping → full config
//	  ttl: 30s
//	  control: true
//	  headers: true
//	  vary_header: [Accept-Language]
func (rc *rawCache) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		rc.TTL = node.Value
		return nil
	case yaml.MappingNode:
		type rawCacheAlias rawCache
		var a rawCacheAlias
		if err := node.Decode(&a); err != nil {
			return err
		}
		*rc = rawCache(a)
		return nil
	default:
		return &Error{Stage: "parse", Code: CodeInvalidYAML,
			Message: "cache must be a duration string or a mapping", Line: node.Line}
	}
}

// parseBytes parses YAML data into rawConfig and runs syntactic validation:
// required fields, valid HTTP methods, middleware_set cycle detection.
// `file` is used only for error reporting; pass "" if loading from memory.
func parseBytes(data []byte, file string) (*rawConfig, error) {
	var cfg rawConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, &Error{Stage: "parse", Code: CodeInvalidYAML, Message: err.Error(), File: file}
	}

	if err := validateGroups(cfg.Groups, "groups", file); err != nil {
		return nil, err
	}
	if err := detectSetCycles(cfg.MiddlewareSets, file); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func validateGroups(groups []rawGroup, path, file string) error {
	for i, g := range groups {
		gPath := fmt.Sprintf("%s[%d]", path, i)
		for j, r := range g.Routes {
			rPath := fmt.Sprintf("%s.routes[%d]", gPath, j)
			if r.Method == "" {
				return &Error{Stage: "parse", Code: CodeMissingField, Message: "method is required", File: file, Path: rPath + ".method", Line: r.Line}
			}
			if r.Handler == "" {
				return &Error{Stage: "parse", Code: CodeMissingField, Message: "handler is required", File: file, Path: rPath + ".handler", Line: r.Line}
			}
			if _, ok := validHTTPMethods[r.Method]; !ok {
				return &Error{Stage: "parse", Code: CodeInvalidHTTPMethod, Message: fmt.Sprintf("unknown HTTP method %q", r.Method), File: file, Path: rPath + ".method", Line: r.Line}
			}
			if r.Timeout != "" {
				d, err := time.ParseDuration(r.Timeout)
				if err != nil {
					return &Error{Stage: "parse", Code: CodeInvalidTimeout, Message: fmt.Sprintf("timeout %q: %s", r.Timeout, err.Error()), File: file, Path: rPath + ".timeout", Line: r.Line}
				}
				if d <= 0 {
					return &Error{Stage: "parse", Code: CodeInvalidTimeout, Message: fmt.Sprintf("timeout %q must be > 0", r.Timeout), File: file, Path: rPath + ".timeout", Line: r.Line}
				}
			}
			if r.Cache != nil {
				if r.Cache.TTL == "" {
					return &Error{Stage: "parse", Code: CodeInvalidCache, Message: "cache.ttl is required", File: file, Path: rPath + ".cache", Line: r.Line}
				}
				d, err := time.ParseDuration(r.Cache.TTL)
				if err != nil {
					return &Error{Stage: "parse", Code: CodeInvalidCache, Message: fmt.Sprintf("cache.ttl %q: %s", r.Cache.TTL, err.Error()), File: file, Path: rPath + ".cache.ttl", Line: r.Line}
				}
				if d <= 0 {
					return &Error{Stage: "parse", Code: CodeInvalidCache, Message: fmt.Sprintf("cache.ttl %q must be > 0", r.Cache.TTL), File: file, Path: rPath + ".cache.ttl", Line: r.Line}
				}
				for i, h := range r.Cache.VaryHeader {
					if h == "" {
						return &Error{Stage: "parse", Code: CodeInvalidCache, Message: fmt.Sprintf("cache.vary_header[%d] is empty", i), File: file, Path: rPath + ".cache.vary_header", Line: r.Line}
					}
				}
			}
		}
		if err := validateGroups(g.Groups, gPath+".groups", file); err != nil {
			return err
		}
	}
	return nil
}

// detectSetCycles runs DFS over middleware_sets and reports the first cycle
// it finds, including the offending names.
func detectSetCycles(sets map[string][]mwRef, file string) error {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(sets))
	var dfs func(name string, stack []string) error
	dfs = func(name string, stack []string) error {
		switch color[name] {
		case gray:
			cycle := append([]string{}, stack...)
			cycle = append(cycle, name)
			return &Error{Stage: "parse", Code: CodeMiddlewareCycle, Message: fmt.Sprintf("middleware_set cycle: %v", cycle), File: file}
		case black:
			return nil
		}
		color[name] = gray
		for _, child := range sets[name] {
			if _, isSet := sets[child.Name]; isSet {
				if err := dfs(child.Name, append(stack, name)); err != nil {
					return err
				}
			}
		}
		color[name] = black
		return nil
	}
	for name := range sets {
		if err := dfs(name, nil); err != nil {
			return err
		}
	}
	return nil
}

func loadFileToConfig(path string) (*rawConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, &Error{Stage: "parse", Code: CodeFileNotFound, Message: err.Error(), File: path}
	}
	return parseBytes(data, path)
}

// Lint runs schema-level validation on a routes.yaml document: required
// fields, valid HTTP methods, middleware_set cycle detection, and
// middleware-entry shape. It does NOT verify that referenced handler,
// middleware, or factory names are registered — for that, use
// Engine.Validate() after registering everything.
//
// Intended for CI tools and pre-commit hooks that want to flag bad YAML
// without instantiating an Engine.
func Lint(data []byte) error {
	_, err := parseBytes(data, "")
	return err
}

// LintFile is Lint over the contents of a file. File-not-found is
// surfaced as *Error / CodeFileNotFound.
func LintFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return &Error{Stage: "parse", Code: CodeFileNotFound, Message: err.Error(), File: path}
	}
	_, err = parseBytes(data, path)
	return err
}
