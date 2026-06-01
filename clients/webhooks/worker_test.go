package webhooks

import (
	"testing"
)

func TestClassify_2xx(t *testing.T) {
	if classify(200) != outcomeDelivered {
		t.Fatal("200 must be delivered")
	}
	if classify(204) != outcomeDelivered {
		t.Fatal("204 must be delivered")
	}
}

func TestClassify_RetryableServer(t *testing.T) {
	for _, s := range []int{408, 429, 500, 502, 503, 504} {
		if classify(s) != outcomeRetryable {
			t.Fatalf("%d should be retryable", s)
		}
	}
}

func TestClassify_FatalClient(t *testing.T) {
	for _, s := range []int{400, 401, 403, 404, 410, 422} {
		if classify(s) != outcomeFatal {
			t.Fatalf("%d should be fatal", s)
		}
	}
}

func TestBackoff_Sequence(t *testing.T) {
	b := BackoffConfig{Initial: 1, Max: 100, Multiplier: 2.0, Jitter: 0}
	want := []int{1, 2, 4, 8, 16, 32, 64, 100, 100}
	for i, w := range want {
		got := int(b.attemptDelay(i + 1).Seconds())
		if got != w {
			t.Fatalf("attempt=%d got=%d want=%d", i+1, got, w)
		}
	}
}
