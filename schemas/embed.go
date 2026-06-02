// Package schemas bundles the JSON Schema (draft-07) documents for
// every kit YAML config. The embedded bytes are exposed verbatim so
// callers can ship them to IDEs, dump them via the kit's `dump-schema`
// CLI, or serve them from custom admin endpoints.
//
// IDE wiring uses the yaml-language-server modeline at the top of
// each YAML file:
//
//	# yaml-language-server: $schema=https://raw.githubusercontent.com/theizzatbek/gokit/main/schemas/routes.schema.json
//
// VS Code (redhat.vscode-yaml), GoLand, Vim (coc-yaml) — every YAML
// editor that speaks the language-server protocol — picks the
// modeline up and gives autocomplete, hover docs, and inline
// diagnostics for free.
//
// Schema → YAML mapping:
//
//	schemas.Routes      ↔ routes.yaml      (fibermap)
//	schemas.Crons       ↔ crons.yaml       (cronmap)
//	schemas.Clients     ↔ clients.yaml     (clients/apimap)
//	schemas.NATSMap     ↔ subscribers.yaml / publishers.yaml (clients/natsmap)
//
// The returned slices are references to the embedded copies — do not
// mutate them. Take a copy if you need to.
package schemas

import _ "embed"

//go:embed routes.schema.json
var routes []byte

//go:embed crons.schema.json
var crons []byte

//go:embed clients.schema.json
var clients []byte

//go:embed natsmap.schema.json
var natsmap []byte

// Routes returns the JSON Schema for fibermap's routes.yaml.
func Routes() []byte { return routes }

// Crons returns the JSON Schema for cronmap's crons.yaml.
func Crons() []byte { return crons }

// Clients returns the JSON Schema for clients/apimap's clients.yaml.
func Clients() []byte { return clients }

// NATSMap returns the JSON Schema for clients/natsmap's
// subscribers.yaml and publishers.yaml. The schema describes the
// union of top-level keys (subscribers, publishers, streams) so a
// single combined YAML or two split files both validate.
func NATSMap() []byte { return natsmap }
