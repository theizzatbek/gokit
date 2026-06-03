package webhooks

import (
	"net/http"
	"testing"
	"time"
)

func mkResp(status int) *http.Response { return &http.Response{StatusCode: status} }

func TestDefaultClassifier_2xx(t *testing.T) {
	if DefaultClassifier(mkResp(200), nil) != OutcomeDelivered {
		t.Fatal("200 must be delivered")
	}
	if DefaultClassifier(mkResp(204), nil) != OutcomeDelivered {
		t.Fatal("204 must be delivered")
	}
}

func TestDefaultClassifier_RetryableServer(t *testing.T) {
	for _, s := range []int{408, 429, 500, 502, 503, 504} {
		if DefaultClassifier(mkResp(s), nil) != OutcomeRetryable {
			t.Fatalf("%d should be retryable", s)
		}
	}
}

func TestDefaultClassifier_FatalClient(t *testing.T) {
	for _, s := range []int{400, 401, 403, 404, 410, 422} {
		if DefaultClassifier(mkResp(s), nil) != OutcomeFatal {
			t.Fatalf("%d should be fatal", s)
		}
	}
}

func TestDefaultClassifier_NetworkErrAsRetryable(t *testing.T) {
	if DefaultClassifier(nil, http.ErrAbortHandler) != OutcomeRetryable {
		t.Fatal("network err must be retryable (matches legacy fail() reschedule)")
	}
}

func TestBackoff_Sequence(t *testing.T) {
	b := BackoffConfig{
		Initial:    time.Second,
		Max:        100 * time.Second,
		Multiplier: 2.0,
		Jitter:     0,
	}
	want := []int{1, 2, 4, 8, 16, 32, 64, 100, 100}
	for i, w := range want {
		got := int(b.attemptDelay(i + 1).Seconds())
		if got != w {
			t.Fatalf("attempt=%d got=%d want=%d", i+1, got, w)
		}
	}
}
