package service

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
func resolvePath(userPath, defaultPath string, enabled bool) string {
	if userPath != "" {
		return userPath
	}
	if enabled {
		return defaultPath
	}
	return ""
}
