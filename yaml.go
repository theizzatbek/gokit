package fibermap

import (
	"fmt"
	"net/http"
	"os"

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
				return &Error{Stage: "parse", Code: CodeMissingField, Message: "method is required", File: file, Path: rPath + ".method"}
			}
			if r.Handler == "" {
				return &Error{Stage: "parse", Code: CodeMissingField, Message: "handler is required", File: file, Path: rPath + ".handler"}
			}
			if _, ok := validHTTPMethods[r.Method]; !ok {
				return &Error{Stage: "parse", Code: CodeInvalidHTTPMethod, Message: fmt.Sprintf("unknown HTTP method %q", r.Method), File: file, Path: rPath + ".method"}
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
func detectSetCycles(sets map[string][]string, file string) error {
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
			if _, isSet := sets[child]; isSet {
				if err := dfs(child, append(stack, name)); err != nil {
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
