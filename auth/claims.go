package auth

import (
	"bytes"
	"encoding/json"
)

// Claims[C] is the JWT body. Registered claims sit alongside generic Custom C
// in a flat JSON object — `tenant_id` lives next to `sub`, not nested.
type Claims[C any] struct {
	Issuer    string   `json:"iss,omitempty"`
	Subject   string   `json:"sub"`
	Audience  []string `json:"aud,omitempty"`
	ExpiresAt int64    `json:"exp"`
	NotBefore int64    `json:"nbf,omitempty"`
	IssuedAt  int64    `json:"iat"`
	JTI       string   `json:"jti,omitempty"`

	Scopes []string `json:"scopes,omitempty"`
	Roles  []string `json:"roles,omitempty"`

	Custom C `json:"-"`
}

// registeredKeys lists JSON tags that belong to the registered fields and must
// shadow any Custom field of the same name on Marshal.
var registeredKeys = map[string]struct{}{
	"iss": {}, "sub": {}, "aud": {}, "exp": {}, "nbf": {},
	"iat": {}, "jti": {}, "scopes": {}, "roles": {},
}

// MarshalJSON produces a flat object: registered claims rendered through the
// struct's natural encoding, then Custom merged in (skipping keys already taken
// by registered fields).
func (c Claims[C]) MarshalJSON() ([]byte, error) {
	type alias Claims[C]
	regBytes, err := json.Marshal(alias(c))
	if err != nil {
		return nil, err
	}
	customBytes, err := json.Marshal(c.Custom)
	if err != nil {
		return nil, err
	}
	if bytes.Equal(customBytes, []byte("null")) || bytes.Equal(customBytes, []byte("{}")) {
		return regBytes, nil
	}
	var regMap, customMap map[string]json.RawMessage
	if err := json.Unmarshal(regBytes, &regMap); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(customBytes, &customMap); err != nil {
		// Custom marshalled to a non-object (Custom is e.g. a string). Drop it —
		// callers should use a struct type for C if they want flat merging.
		return regBytes, nil
	}
	for k, v := range customMap {
		if _, isRegistered := registeredKeys[k]; isRegistered {
			continue
		}
		regMap[k] = v
	}
	return json.Marshal(regMap)
}

// UnmarshalJSON populates registered fields normally, then re-decodes the same
// bytes into Custom so plain projection of unknown fields lands there.
func (c *Claims[C]) UnmarshalJSON(b []byte) error {
	type alias Claims[C]
	var inner alias
	if err := json.Unmarshal(b, &inner); err != nil {
		return err
	}
	*c = Claims[C](inner)
	// Re-decode into Custom — encoding/json silently ignores unknown fields, so
	// only the keys C cares about get populated.
	return json.Unmarshal(b, &c.Custom)
}
