package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExample_LoginAccessRefreshLogout(t *testing.T) {
	app, err := buildApp(slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	// 1. Login.
	login, err := app.Test(newJSONReq("POST", "/auth/login", `{"login":"alice","password":"hunter2"}`))
	if err != nil {
		t.Fatalf("login send: %v", err)
	}
	if login.StatusCode != http.StatusOK {
		t.Fatalf("login = %d", login.StatusCode)
	}
	var loginBody struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(login.Body).Decode(&loginBody); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	if loginBody.AccessToken == "" {
		t.Fatal("login: empty access_token")
	}
	var rc *http.Cookie
	for _, c := range login.Cookies() {
		if c.Name == "refresh_token" {
			rc = c
		}
	}
	if rc == nil {
		t.Fatal("login: missing refresh_token cookie")
	}

	// 2. GET /api/me with access token.
	req := httptest.NewRequest("GET", "/api/me", nil)
	req.Header.Set("Authorization", "Bearer "+loginBody.AccessToken)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("me send: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("me = %d", resp.StatusCode)
	}

	// 3. POST /api/posts (requires posts:write scope).
	post := httptest.NewRequest("POST", "/api/posts", nil)
	post.Header.Set("Authorization", "Bearer "+loginBody.AccessToken)
	postResp, err := app.Test(post)
	if err != nil {
		t.Fatalf("post send: %v", err)
	}
	if postResp.StatusCode != http.StatusCreated {
		t.Fatalf("create_post = %d", postResp.StatusCode)
	}

	// 4. Refresh.
	rreq := httptest.NewRequest("POST", "/auth/refresh", nil)
	rreq.AddCookie(rc)
	rresp, err := app.Test(rreq)
	if err != nil {
		t.Fatalf("refresh send: %v", err)
	}
	if rresp.StatusCode != http.StatusOK {
		t.Fatalf("refresh = %d", rresp.StatusCode)
	}

	// 5. Logout — carry the freshly-rotated refresh cookie.
	lreq := httptest.NewRequest("POST", "/auth/logout", nil)
	for _, c := range rresp.Cookies() {
		if c.Name == "refresh_token" {
			lreq.AddCookie(c)
		}
	}
	lresp, err := app.Test(lreq)
	if err != nil {
		t.Fatalf("logout send: %v", err)
	}
	if lresp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout = %d", lresp.StatusCode)
	}
}

func newJSONReq(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}
