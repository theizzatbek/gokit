package svckit

import "path/filepath"

// resolvePathInDir returns the YAML file path a subsystem should
// load, applying the standard convention with a ConfigsDir layer:
//
//   - explicit userPath wins, passed through unchanged. Operators
//     who set Routes.Path want that literal path used, with no
//     surprise prefixing — supports both absolute paths and
//     CWD-relative ones.
//   - when enabled and userPath empty and configsDir non-empty →
//     filepath.Join(configsDir, defaultName).
//   - when enabled and configsDir empty → defaultName (original
//     CWD-relative behaviour preserved for back-compat with services
//     that drop their YAMLs next to the binary).
//   - otherwise "" (subsystem off).
//
// Backward compat: a non-empty userPath triggers the subsystem even
// when enabled is false, preserving the original "Path-presence is
// the opt-in" behaviour.
//
// The core has exactly one default-named YAML today — routes.yaml —
// but the helper is shared with Host.ResolvePath so a mod's own
// default-named YAML (e.g. a future cronmapmod's crons.yaml) honours
// the same CONFIGS_DIR convention without duplicating this logic.
func resolvePathInDir(configsDir, userPath, defaultName string, enabled bool) string {
	if userPath != "" {
		return userPath
	}
	if !enabled {
		return ""
	}
	if configsDir == "" {
		return defaultName
	}
	return filepath.Join(configsDir, defaultName)
}
