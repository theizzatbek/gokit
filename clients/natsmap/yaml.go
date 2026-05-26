package natsmap

import (
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
// resolver; if nil, falls back to os.LookupEnv.
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
					"natsmap: env var %s is not set", string(name))
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
			"natsmap: malformed env var reference %s (must match ${[A-Z_][A-Z0-9_]*})", string(leftover))
	}
	return out, nil
}

// parseBytes runs env-var substitution and decodes the YAML document.
// Returns CodeNoEntries when the parsed document has no subscribers and
// no publishers. lookup is the env resolver; nil falls back to os.LookupEnv.
func parseBytes(b []byte, lookup func(string) (string, bool)) (*rawConfig, error) {
	substituted, err := substituteEnv(b, lookup)
	if err != nil {
		return nil, err
	}
	var cfg rawConfig
	if err := yaml.Unmarshal(substituted, &cfg); err != nil {
		return nil, xerrs.Wrap(err, xerrs.KindValidation, CodeParseYAML,
			"natsmap: parse yaml")
	}
	if len(cfg.Subscribers) == 0 && len(cfg.Publishers) == 0 {
		return nil, xerrs.Validation(CodeNoEntries,
			"natsmap: yaml has no subscribers and no publishers")
	}
	return &cfg, nil
}
