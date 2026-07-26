package svckit_test

import (
	"os/exec"
	"strings"
	"testing"
)

// forbidden — packages that svckit exists to keep out of the core.
// If any of these show up in the core's dependency graph, the 15 MB
// binary-size savings the package was built for are gone.
var forbidden = []string{
	"github.com/theizzatbek/gokit/clients/s3",
	"github.com/theizzatbek/gokit/clients/redis",
	"github.com/theizzatbek/gokit/clients/nats",
	"github.com/theizzatbek/gokit/clients/natsmap",
	"github.com/theizzatbek/gokit/clients/apimap",
	"github.com/theizzatbek/gokit/clients/ratelimit",
	"github.com/theizzatbek/gokit/clients/webhooks",
	"github.com/theizzatbek/gokit/cronmap",
	"github.com/theizzatbek/gokit/db/outbox",
	"github.com/theizzatbek/gokit/otelkit",
	"github.com/theizzatbek/gokit/sentrykit",
	"github.com/aws/aws-sdk-go-v2",
	"github.com/redis/go-redis",
	"github.com/nats-io/nats.go",
	"go.opentelemetry.io/otel/sdk",
	"github.com/getsentry/sentry-go",
}

func TestCoreDoesNotImportOptionalSubsystems(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps",
		"github.com/theizzatbek/gokit/svckit").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}

	for _, dep := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		for _, bad := range forbidden {
			if dep == bad || strings.HasPrefix(dep, bad+"/") {
				t.Errorf("svckit core pulls in %q — that subsystem belongs in a mod.\n"+
					"If the import is intentional, that changes what this package means: discuss it, don't relax the test", dep)
			}
		}
	}
}
