// Package migrations exports the urlshort schema migrations as an
// fs.FS. urlshort-api (the schema owner) embeds them at compile time
// and feeds them to gokit/service.WithMigrations. The counter +
// enricher binaries don't apply migrations themselves; they trust
// the api to be reachable + done before they start.
//
// embed.FS is local to this package so urlshort-api's main.go can
// import + embed without the cross-tree go:embed `../` restriction.
package migrations

import (
	"embed"
	"io/fs"
)

//go:embed *.sql
var migrationsFS embed.FS

// FS returns an fs.FS rooted at the migrations directory. Suitable
// for gokit/service.WithMigrations(...).
func FS() fs.FS { return migrationsFS }
