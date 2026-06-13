package email

import "os"

// readFile is a tiny wrapper for testability; tests stub it via the
// fs.FS-based loader instead, so this stays a plain os.ReadFile.
func readFile(path string) (string, error) {
	// #nosec G304 -- path is a caller-supplied template/attachment file
	// from trusted service config, not request-derived input.
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
