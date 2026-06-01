package email

import "os"

// readFile is a tiny wrapper for testability; tests stub it via the
// fs.FS-based loader instead, so this stays a plain os.ReadFile.
func readFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
