package enrich

import (
	"io"
	"regexp"
)

const maxTitleScan = 64 * 1024 // first 64KB of body is plenty for <title>

var titleRe = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

// parseTitle scans up to maxTitleScan bytes of r looking for the first
// <title>...</title>. Returns "" if not found.
func parseTitle(r io.Reader) string {
	buf := make([]byte, maxTitleScan)
	n, _ := io.ReadFull(r, buf)
	if n == 0 {
		return ""
	}
	m := titleRe.FindSubmatch(buf[:n])
	if len(m) < 2 {
		return ""
	}
	return string(m[1])
}
