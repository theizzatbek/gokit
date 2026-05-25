package fibermap

import _ "embed"

//go:embed schema/routes.schema.json
var schemaJSON []byte

// Schema returns the JSON Schema (draft-07) for routes.yaml as a byte
// slice. Useful for:
//
//   - shipping the schema to your IDE: add the modeline
//     `# yaml-language-server: $schema=https://raw.githubusercontent.com/theizzatbek/gokit/main/fibermap/schema/routes.schema.json`
//     to the top of your routes.yaml and editors with the YAML
//     language server (VS Code, GoLand, Vim with coc-yaml, ...) give
//     you autocomplete, hover docs, and inline diagnostics;
//
//   - serving the schema from your own admin endpoint;
//
//   - the bundled `fibermap dump-schema` CLI command.
//
// The returned slice is a reference to the embedded copy — do not
// mutate it. Take a copy if you need to.
func Schema() []byte { return schemaJSON }
