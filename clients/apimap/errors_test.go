package apimap

import (
	"testing"

	xerrs "github.com/theizzatbek/gokit/errs"
)

func TestStatusToKind(t *testing.T) {
	tests := []struct {
		status int
		want   xerrs.Kind
	}{
		{200, xerrs.KindUnknown},
		{301, xerrs.KindUnknown},
		{400, xerrs.KindValidation},
		{401, xerrs.KindUnauthorized},
		{403, xerrs.KindPermission},
		{404, xerrs.KindNotFound},
		{409, xerrs.KindConflict},
		{418, xerrs.KindValidation},
		{429, xerrs.KindRateLimited},
		{500, xerrs.KindInternal},
		{503, xerrs.KindInternal},
		{599, xerrs.KindInternal},
	}
	for _, tt := range tests {
		got := statusToKind(tt.status)
		if got != tt.want {
			t.Errorf("statusToKind(%d) = %v, want %v", tt.status, got, tt.want)
		}
	}
}

func TestCodeForEndpointStatus(t *testing.T) {
	tests := []struct {
		client, endpoint string
		status           int
		want             string
	}{
		{"github", "get_user", 404, "apimap_github_get_user_not_found"},
		{"github", "create_issue", 401, "apimap_github_create_issue_unauthorized"},
		{"stripe", "charge", 409, "apimap_stripe_charge_conflict"},
		{"stripe", "charge", 429, "apimap_stripe_charge_rate_limited"},
		{"x", "y", 500, "apimap_x_y_server_error"},
		{"x", "y", 502, "apimap_x_y_server_error"},
		{"x", "y", 418, "apimap_x_y_client_error"},
		{"x", "y", 301, "apimap_x_y_status_301"},
	}
	for _, tt := range tests {
		got := codeForEndpointStatus(tt.client, tt.endpoint, tt.status)
		if got != tt.want {
			t.Errorf("codeForEndpointStatus(%q, %q, %d) = %q, want %q",
				tt.client, tt.endpoint, tt.status, got, tt.want)
		}
	}
}
