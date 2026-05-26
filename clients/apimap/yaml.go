package apimap

import (
	"fmt"
	"os"
	"regexp"

	xerrs "github.com/theizzatbek/gokit/errs"
	"gopkg.in/yaml.v3"
)

// envVarPattern matches a single valid ${VAR_NAME} reference (uppercase,
// digits, underscore; must start with letter or underscore).
var envVarPattern = regexp.MustCompile(`\$\{([A-Z_][A-Z0-9_]*)\}`)

// envVarCatchAll matches any remaining ${...} sequence after the valid
// ones are substituted — used to flag malformed references.
var envVarCatchAll = regexp.MustCompile(`\$\{[^}]*\}`)

// substituteEnv replaces every ${VAR} reference in b. lookup is the
// resolver; if nil, falls back to os.LookupEnv. Unset variables produce
// CodeEnvVarUnset; malformed ${...} produce CodeEnvVarMalformed.
// Runs on raw bytes BEFORE yaml.Unmarshal so any string field in the
// YAML can use it.
func substituteEnv(b []byte, lookup func(string) (string, bool)) ([]byte, error) {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	var firstErr error
	out := envVarPattern.ReplaceAllFunc(b, func(match []byte) []byte {
		name := envVarPattern.FindSubmatch(match)[1]
		val, ok := lookup(string(name))
		if !ok {
			if firstErr == nil {
				firstErr = xerrs.Validationf(CodeEnvVarUnset,
					"apimap: env var %s is not set", string(name))
			}
			return match
		}
		return []byte(val)
	})
	if firstErr != nil {
		return nil, firstErr
	}
	if leftover := envVarCatchAll.Find(out); leftover != nil {
		return nil, xerrs.Validationf(CodeEnvVarMalformed,
			"apimap: malformed env var reference %s (must match ${[A-Z_][A-Z0-9_]*})", string(leftover))
	}
	return out, nil
}

// parseBytes runs env-var substitution and decodes the YAML document
// into rawConfig. Enforces at least one client. Per-field validation
// happens later in spec.go. lookup is the env resolver; nil falls back
// to os.LookupEnv.
func parseBytes(b []byte, lookup func(string) (string, bool)) (*rawConfig, error) {
	substituted, err := substituteEnv(b, lookup)
	if err != nil {
		return nil, err
	}
	var cfg rawConfig
	if err := yaml.Unmarshal(substituted, &cfg); err != nil {
		return nil, fmt.Errorf("apimap: parse yaml: %w", err)
	}
	if len(cfg.Clients) == 0 {
		return nil, xerrs.Validation(CodeNoClients, "apimap: yaml has no clients")
	}
	return &cfg, nil
}

// parsePathTemplate returns the ordered list of placeholder names found
// in path. Validates name shape (identifier rules) and uniqueness, and
// flags unmatched braces.
func parsePathTemplate(path string) ([]string, error) {
	var (
		out  []string
		seen = map[string]struct{}{}
		i    int
	)
	for i < len(path) {
		switch path[i] {
		case '{':
			end := indexClose(path, i+1)
			if end < 0 {
				return nil, xerrs.Validation(CodeInvalidPathVar,
					"apimap: unmatched '{' in path "+path)
			}
			name := path[i+1 : end]
			if !isValidVarName(name) {
				return nil, xerrs.Validationf(CodeInvalidPathVar,
					"apimap: invalid path variable %q in %q", name, path)
			}
			if _, dup := seen[name]; dup {
				return nil, xerrs.Validationf(CodeInvalidPathVar,
					"apimap: duplicate path variable %q in %q", name, path)
			}
			seen[name] = struct{}{}
			out = append(out, name)
			i = end + 1
		case '}':
			return nil, xerrs.Validation(CodeInvalidPathVar,
				"apimap: unmatched '}' in path "+path)
		default:
			i++
		}
	}
	return out, nil
}

func indexClose(s string, from int) int {
	for j := from; j < len(s); j++ {
		switch s[j] {
		case '}':
			return j
		case '{':
			return -1
		}
	}
	return -1
}

// isValidVarName matches the identifier rule:
// [A-Za-z_][A-Za-z0-9_]* (no empty names, no leading digit, no spaces).
func isValidVarName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		first := i == 0
		switch {
		case r == '_':
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case !first && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}
