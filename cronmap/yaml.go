package cronmap

import (
	"fmt"
	"os"
	"regexp"

	xerrs "github.com/theizzatbek/gokit/errs"
	"gopkg.in/yaml.v3"
)

// envVarPattern matches a single valid ${VAR_NAME} reference
// (uppercase letters / digits / underscore; must start with letter
// or underscore). Matches the apimap / natsmap convention so the
// YAML feel is consistent across kit subsystems.
var envVarPattern = regexp.MustCompile(`\$\{([A-Z_][A-Z0-9_]*)\}`)

// envVarCatchAll matches any remaining ${...} sequence after the
// valid ones are substituted — used to flag malformed references.
var envVarCatchAll = regexp.MustCompile(`\$\{[^}]*\}`)

// substituteEnv replaces every ${VAR} reference in b. lookup is the
// resolver; if nil, falls back to os.LookupEnv. Unset variables
// produce CodeEnvVarUnset; malformed ${...} produce
// CodeEnvVarMalformed. Runs on raw bytes BEFORE yaml.Unmarshal so any
// string field in the YAML can use it (schedule, sentry_slug, etc.).
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
					"cronmap: env var %s is not set", string(name))
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
			"cronmap: malformed env var reference %s (must match ${[A-Z_][A-Z0-9_]*})",
			string(leftover))
	}
	return out, nil
}

// parseBytes runs env-var substitution and decodes the YAML document
// into rawConfig. Per-field validation happens later in spec.go /
// engine.Build through errors.Join. lookup is the env resolver; nil
// falls back to os.LookupEnv.
//
// An empty `jobs:` list is allowed at parse time — Build returns a
// usable (no-op) Runtime so callers can keep an empty crons.yaml
// during early dev without forcing a build error.
func parseBytes(b []byte, lookup func(string) (string, bool)) (*rawConfig, error) {
	substituted, err := substituteEnv(b, lookup)
	if err != nil {
		return nil, err
	}
	var cfg rawConfig
	if err := yaml.Unmarshal(substituted, &cfg); err != nil {
		return nil, fmt.Errorf("cronmap: parse yaml: %w", err)
	}
	return &cfg, nil
}
