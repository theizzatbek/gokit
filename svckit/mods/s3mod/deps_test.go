package s3mod_test

import (
	"os/exec"
	"strings"
	"testing"
)

// The mod must pull in its own SDK and must not pull in anyone
// else's. Catches copy-paste mistakes when adding the next mods.
func TestModPullsOwnSDKOnly(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps",
		"github.com/theizzatbek/gokit/svckit/mods/s3mod").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	deps := string(out)

	if !strings.Contains(deps, "github.com/aws/aws-sdk-go-v2") {
		t.Error("s3mod doesn't pull in aws-sdk — is the mod empty?")
	}
	for _, foreign := range []string{
		"github.com/redis/go-redis",
		"github.com/nats-io/nats.go",
		"go.opentelemetry.io/otel/sdk",
		"github.com/getsentry/sentry-go",
	} {
		if strings.Contains(deps, foreign) {
			t.Errorf("s3mod pulls in a foreign dependency %q", foreign)
		}
	}
}
