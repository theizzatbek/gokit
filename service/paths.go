package service

import "path/filepath"

// resolvePath returns the YAML file path a subsystem should load,
// applying the standard convention:
//
//   - userPath if non-empty (explicit override wins)
//   - else defaultPath when enabled is true
//   - else "" (subsystem off)
//
// Backward compat: a non-empty userPath triggers the subsystem even
// when enabled is false, preserving the original "Path-presence is the
// opt-in" behaviour.
//
// For callers honouring ServiceConfig.ConfigsDir use [resolvePathInDir].
func resolvePath(userPath, defaultPath string, enabled bool) string {
	return resolvePathInDir("", userPath, defaultPath, enabled)
}

// resolvePathInDir is resolvePath with a ConfigsDir layer:
//
//   - explicit userPath wins, passed through unchanged. Operators
//     who set Routes.Path / APIMap.Path / NATSMap.*Path want that
//     literal path used, with no surprise prefixing — supports both
//     absolute paths and CWD-relative ones.
//   - when enabled and userPath empty and configsDir non-empty →
//     filepath.Join(configsDir, defaultName).
//   - when enabled and configsDir empty → defaultName (original
//     CWD-relative behaviour preserved for back-compat with services
//     that drop their YAMLs next to the binary).
//   - otherwise "" (subsystem off).
//
// All three default YAMLs (routes/clients/subscribers/publishers)
// route through this helper so a single CONFIGS_DIR env var moves
// the whole bundle into a folder without per-subsystem overrides.
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
