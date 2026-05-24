package auth

import "testing"

func TestErrorCodes_AreStableStrings(t *testing.T) {
	cases := map[string]string{
		"missing_token":          CodeMissingToken,
		"invalid_token_scheme":   CodeInvalidTokenScheme,
		"invalid_token":          CodeInvalidToken,
		"expired_token":          CodeExpiredToken,
		"missing_refresh":        CodeMissingRefresh,
		"refresh_invalid":        CodeRefreshInvalid,
		"refresh_expired":        CodeRefreshExpired,
		"refresh_reused":         CodeRefreshReused,
		"invalid_credentials":    CodeInvalidCredentials,
		"missing_scope":          CodeMissingScope,
		"missing_role":           CodeMissingRole,
		"invalid_factory_args":   CodeInvalidFactoryArgs,
		"password_hash_corrupt":  CodePasswordHashCorrupt,
		"key_not_loaded":         CodeKeyNotLoaded,
		"store_unavailable":      CodeStoreUnavailable,
	}
	for want, got := range cases {
		if got != want {
			t.Errorf("constant value drift: got %q, want %q", got, want)
		}
	}
}

func TestWWWAuthenticate_FormatsPerRFC6750(t *testing.T) {
	got := wwwAuthenticate("api", CodeInvalidToken)
	want := `Bearer realm="api", error="invalid_token"`
	if got != want {
		t.Fatalf("wwwAuthenticate = %q, want %q", got, want)
	}
}
