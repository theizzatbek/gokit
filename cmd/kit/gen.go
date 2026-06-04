package main

import (
	"context"
	"errors"
	"fmt"
	"os"
)

const usageGen = `kit gen — code / asset generators

Usage:
  kit gen migration <name> [--dir migrations]
  kit gen k8s --name svc --image IMG [--namespace ns] [--out file]
  kit gen db-cluster [--replicas N] [--out file]

Generators are self-contained — no go.mod parsing, no AST rewrites.
They emit text files (SQL templates, YAML manifests) the operator
reviews and commits.
`

// runGen dispatches the gen-* subcommands. Adding a generator is a
// matter of dropping a runGen<Kind> function in gen_<kind>.go and
// adding a case here.
func runGen(ctx context.Context, args []string) error {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, usageGen)
		return errors.New("subcommand required")
	}
	switch args[0] {
	case "migration":
		return runGenMigration(ctx, args[1:])
	case "k8s":
		return runGenK8s(ctx, args[1:])
	case "db-cluster":
		return runGenDBCluster(ctx, args[1:])
	case "-h", "--help", "help":
		fmt.Fprint(os.Stderr, usageGen)
		return nil
	default:
		fmt.Fprint(os.Stderr, usageGen)
		return fmt.Errorf("unknown gen subcommand %q", args[0])
	}
}
