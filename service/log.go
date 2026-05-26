package service

import (
	"log/slog"
	"os"
	"strings"

	"github.com/theizzatbek/gokit/reqctx"
)

// newLogger builds a *slog.Logger from the format/level strings in
// ServiceConfig. Unknown level → Info. Unknown format → JSON.
//
// nodeName and serverGroup, when non-empty, are added as default attrs
// (node, server_group) so every log line is identifiable in multi-node
// deployments.
func newLogger(format, level, nodeName, serverGroup string) *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		lvl = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lvl}
	var h slog.Handler
	switch strings.ToLower(format) {
	case "text":
		h = slog.NewTextHandler(os.Stdout, opts)
	default:
		h = slog.NewJSONHandler(os.Stdout, opts)
	}
	l := slog.New(reqctx.SlogHandler(h))
	if nodeName != "" {
		l = l.With("node", nodeName)
	}
	if serverGroup != "" {
		l = l.With("server_group", serverGroup)
	}
	return l
}
