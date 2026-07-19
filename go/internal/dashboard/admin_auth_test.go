package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLoginRateLimitLock(t *testing.T) {
	ip := "203.0.113.9"
	loginAttemptMu.Lock()
	delete(loginAttempts, ip)
	loginAttemptMu.Unlock()

	for i := 1; i <= loginRateMax-1; i++ {
		locked, _, fails := recordLoginFailure(ip)
		if locked {
			t.Fatalf("unexpected lock at fail=%d", fails)
		}
	}
	locked, retry, fails := recordLoginFailure(ip)
	if !locked {
		t.Fatalf("expected lock after %d fails, fails=%d", loginRateMax, fails)
	}
	if retry <= 0 {
		t.Fatalf("expected positive retry, got %v", retry)
	}
	ok, wait := loginAllowed(ip)
	if ok {
		t.Fatal("loginAllowed should be false while locked")
	}
	if wait <= 0 {
		t.Fatalf("expected wait > 0, got %v", wait)
	}

	loginAttemptMu.Lock()
	if st := loginAttempts[ip]; st != nil {
		st.LockedUntil = time.Now().Add(-time.Second)
	}
	loginAttemptMu.Unlock()
	ok, _ = loginAllowed(ip)
	if !ok {
		t.Fatal("expected allow after lock expiry")
	}
	recordLoginSuccess(ip)
	ok, _ = loginAllowed(ip)
	if !ok {
		t.Fatal("expected allow after success reset")
	}
}

func TestAuthStatusHidesUsernameWhenLoggedOut(t *testing.T) {
	t.Setenv(adminPasswordEnv, "unit-test-pass-not-real")
	t.Setenv(adminUsernameEnv, "unit-admin")

	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/admin/auth/status", nil)
	rr := httptest.NewRecorder()
	s.handleAdminAuthStatus(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status code=%d body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if strings.Contains(body, "\"username\"") {
		t.Fatalf("username should be hidden when logged out, body=%s", body)
	}
}

func TestClientIPRejectsSpoofedForwardedHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/admin/login", nil)
	req.RemoteAddr = "198.51.100.8:4321"
	req.Header.Set("X-Forwarded-For", "203.0.113.77")
	if got := clientIP(req); got != "198.51.100.8" {
		t.Fatalf("untrusted forwarded header accepted: %q", got)
	}

	req.RemoteAddr = "127.0.0.1:4321"
	if got := clientIP(req); got != "203.0.113.77" {
		t.Fatalf("loopback reverse proxy header ignored: %q", got)
	}
}

func TestRequestSecureOnlyTrustsLocalProxyHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/admin/login", nil)
	req.RemoteAddr = "198.51.100.8:4321"
	req.Header.Set("X-Forwarded-Proto", "https")
	if requestSecure(req) {
		t.Fatal("external client spoofed forwarded proto")
	}
	req.RemoteAddr = "127.0.0.1:4321"
	if !requestSecure(req) {
		t.Fatal("local reverse proxy https header not trusted")
	}
}
