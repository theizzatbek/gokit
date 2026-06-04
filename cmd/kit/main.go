// Command kit is the kit's operator CLI: migrations, auth key
// minting, outbox inspection.
//
// Subcommands:
//
//	kit version
//	kit migrate up   [--dir migrations/]
//	kit migrate down [--steps N]   [--dir migrations/]
//	kit migrate status              [--dir migrations/]
//	kit auth keygen  [--kid k1]
//	kit auth apikey new --subject S [--scopes a,b] [--role r] [--expires-in 90d]
//	kit outbox status
//
// DB connection: --dsn flag OR `DATABASE_URL` env (PostgreSQL URL
// format). API_KEY_HASH_SECRET env supplies the kit-side HMAC
// secret for `auth apikey` operations.
//
// Stdlib `flag` only — no external CLI framework. The tool is a
// thin operator-side shell around the kit's library entry points.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
)

const usageRoot = `kit — gokit operator CLI

Usage:
  kit version
  kit migrate up|down|status [flags]
  kit auth keygen|apikey [flags]
  kit outbox status [flags]
  kit doctor --url URL [flags]
  kit init <name> --module PATH [flags]
  kit add-endpoint METHOD PATH HANDLER [flags]
  kit gen migration <name> [flags]
  kit gen k8s --name svc --image IMG [flags]
  kit gen db-cluster [--replicas N] [flags]

Pass --help on any subcommand for its flags.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usageRoot)
		os.Exit(2)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var err error
	switch os.Args[1] {
	case "version", "--version", "-v":
		printVersion()
	case "migrate":
		err = runMigrate(ctx, os.Args[2:])
	case "auth":
		err = runAuth(ctx, os.Args[2:])
	case "outbox":
		err = runOutbox(ctx, os.Args[2:])
	case "doctor":
		err = runDoctor(ctx, os.Args[2:])
	case "init":
		err = runInit(ctx, os.Args[2:])
	case "add-endpoint":
		err = runAddEndpoint(ctx, os.Args[2:])
	case "gen":
		err = runGen(ctx, os.Args[2:])
	case "help", "--help", "-h":
		fmt.Fprint(os.Stdout, usageRoot)
	default:
		fmt.Fprintf(os.Stderr, "kit: unknown command %q\n\n", os.Args[1])
		fmt.Fprint(os.Stderr, usageRoot)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "kit: %s\n", err.Error())
		os.Exit(1)
	}
}

func printVersion() {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		fmt.Println("kit: build info unavailable")
		return
	}
	fmt.Println("kit", info.Main.Version)
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision", "vcs.time":
			fmt.Printf("  %s: %s\n", s.Key, s.Value)
		}
	}
}
