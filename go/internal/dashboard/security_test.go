package dashboard

import (
	"fzsmbot/internal/client"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSecurityHeaders(t *testing.T) {
	h := withSecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	for _, k := range []string{"Content-Security-Policy", "X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy"} {
		if rr.Header().Get(k) == "" {
			t.Fatalf("missing %s", k)
		}
	}
}
func TestCookieValidationRejectsUntrustedDomainAndInvalidName(t *testing.T) {
	if _, err := parseCookieItems([]any{map[string]any{"name": "session", "value": "abcdefghijklmnop", "domain": "evil.example", "path": "/"}}); err == nil {
		t.Fatal("untrusted cookie domain accepted")
	}
	if _, err := parseCookieItems([]any{map[string]any{"name": "bad;name", "value": "abcdefghijklmnop", "domain": "fanzisima.xyz", "path": "/"}}); err == nil {
		t.Fatal("invalid cookie name accepted")
	}
}
func TestCookieFilesUseRestrictedPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose POSIX permission bits reliably")
	}
	path := filepath.Join(t.TempDir(), "auth", "cookies.json")
	if err := writeCookieFile(path, []client.CookieItem{{Name: "fz_lottery", Value: "abcdefghijklmnop", Domain: "fanzisima.xyz", Path: "/"}}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("cookie file is group/world accessible: %o", info.Mode().Perm())
	}
}

func TestSanitizeRedactsNestedSecrets(t *testing.T) {
	got := asMap(sanitize(map[string]any{"room": map[string]any{"_password": "secret-room", "name": "ok"}, "token": "secret-token"}))
	room := asMap(got["room"])
	if room["_password"] != "[REDACTED]" || got["token"] != "[REDACTED]" || room["name"] != "ok" {
		t.Fatalf("secret redaction failed: %+v", got)
	}
}
