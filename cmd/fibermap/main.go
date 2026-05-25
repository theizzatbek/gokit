// Command fibermap is a small CLI around the fibermap library.
//
// Usage:
//
//	fibermap validate <path>     # schema-lint routes.yaml; non-zero exit on issues
//	fibermap dump-schema         # write the bundled JSON Schema to stdout
//
// `validate` runs the same parse-stage checks the library performs at
// LoadFile time (required fields, valid HTTP methods, middleware_set
// cycle detection, middleware-entry shape). It does NOT verify that
// referenced handler/middleware/factory names are registered — your
// Go binary is the only place where those are known.
package main

import (
	"fmt"
	"os"

	"github.com/theizzatbek/gokit/fibermap"
)

const usage = `usage:
  fibermap validate <path>     schema-lint a routes.yaml file
  fibermap dump-schema         print the bundled JSON Schema (draft-07)
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "validate":
		if len(os.Args) != 3 {
			fmt.Fprint(os.Stderr, usage)
			os.Exit(2)
		}
		if err := fibermap.LintFile(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("OK")
	case "dump-schema":
		os.Stdout.Write(fibermap.Schema())
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
}
