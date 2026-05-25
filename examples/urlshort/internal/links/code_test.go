package links

import (
	"regexp"
	"testing"
)

var codeFormat = regexp.MustCompile(`^[A-Za-z0-9]{6}$`)

func TestGenerateCode_Format(t *testing.T) {
	for i := 0; i < 100; i++ {
		c, err := generateCode()
		if err != nil {
			t.Fatalf("generateCode(): %v", err)
		}
		if !codeFormat.MatchString(c) {
			t.Errorf("code %q does not match [A-Za-z0-9]{6}", c)
		}
	}
}

func TestGenerateCode_Distribution(t *testing.T) {
	seen := map[string]struct{}{}
	const n = 1000
	for i := 0; i < n; i++ {
		c, err := generateCode()
		if err != nil {
			t.Fatal(err)
		}
		seen[c] = struct{}{}
	}
	// 62^6 ≈ 5.7e10; 1000 samples produce essentially zero expected collisions.
	if len(seen) < 990 {
		t.Errorf("got %d unique codes out of %d — distribution too narrow", len(seen), n)
	}
}
