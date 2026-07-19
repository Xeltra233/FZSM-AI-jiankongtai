package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveEndpointURLRejectsForeignAbsoluteURL(t *testing.T) {
	if _, err := resolveEndpointURL("https://api.example.test/v1", "https://evil.example/steal"); err == nil {
		t.Fatal("foreign absolute endpoint accepted")
	}
	got, err := resolveEndpointURL("https://api.example.test/v1", "/me")
	if err != nil || got != "https://api.example.test/v1/me" {
		t.Fatalf("relative resolution failed: %q %v", got, err)
	}
}

func TestLoadCookiesReplacesRemovedCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.json")
	b, _ := json.Marshal([]CookieItem{{Name: "session", Value: "first-secret", Domain: "fanzisima.xyz", Path: "/"}})
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := New("https://fanzisima.xyz/stocks/api", "https://api.fanzisima.xyz", path)
	if err != nil {
		t.Fatal(err)
	}
	if c.primaryCookies["session"] != "first-secret" {
		t.Fatal("initial cookie missing")
	}
	if err := os.WriteFile(path, []byte(`[]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.LoadCookies(path); err != nil {
		t.Fatal(err)
	}
	if len(c.primaryCookies) != 0 {
		t.Fatalf("removed credential retained: %+v", c.primaryCookies)
	}
}
func TestCrossOriginRedirectDoesNotReceiveCookie(t *testing.T) {
	leaked := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Cookie"), "secret") {
			leaked = true
		}
		w.WriteHeader(200)
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer source.Close()
	c, err := New(source.URL, source.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	c.primaryCookies["session"] = "secret"
	_, _, err = c.StocksGet("/")
	if err == nil {
		t.Fatal("cross-origin redirect was not blocked")
	}
	if leaked {
		t.Fatal("cookie leaked to redirect target")
	}
}
func TestOversizedResponseRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", maxResponseBodySize+1)))
	}))
	defer srv.Close()
	c, err := New(srv.URL, srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.StocksGet("/"); err == nil {
		t.Fatal("oversized response accepted")
	}
}
