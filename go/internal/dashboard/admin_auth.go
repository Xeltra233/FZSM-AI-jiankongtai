package dashboard

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	adminUsernameEnv   = "FZSM_ADMIN_USERNAME"
	adminPasswordEnv   = "FZSM_ADMIN_PASSWORD"
	adminSessionCookie = "fzsm_admin_session"
	adminSessionTTL    = 12 * time.Hour

	// loginRateWindow / loginRateMax: same IP max attempts in window
	loginRateWindow = 10 * time.Minute
	loginRateMax    = 8
	// loginLockTTL: lock duration after threshold
	loginLockTTL = 15 * time.Minute
)

type adminSession struct {
	ExpiresAt time.Time
}

type loginAttemptState struct {
	Fails       int
	WindowStart time.Time
	LockedUntil time.Time
	LastAt      time.Time
}

var (
	adminSessionMu sync.Mutex
	adminSessions  = map[string]adminSession{}

	loginAttemptMu sync.Mutex
	loginAttempts  = map[string]*loginAttemptState{}
)

func adminPassword() string {
	if p := strings.TrimSpace(os.Getenv(adminPasswordEnv)); p != "" {
		return p
	}
	// Compat: reuse FZSM_ADMIN_TOKEN as admin password when dedicated password missing.
	return strings.TrimSpace(os.Getenv(adminTokenEnv))
}

func adminUsername() string {
	u := strings.TrimSpace(os.Getenv(adminUsernameEnv))
	if u == "" {
		return "admin"
	}
	return u
}

func usernameOK(got string) bool {
	want := adminUsername()
	got = strings.TrimSpace(got)
	if got == "" || want == "" {
		return false
	}
	wh := sha256.Sum256([]byte(want))
	gh := sha256.Sum256([]byte(got))
	return subtle.ConstantTimeCompare(wh[:], gh[:]) == 1
}

func adminAuthRequired() bool {
	return adminPassword() != ""
}

func adminAuthMode() string {
	if !adminAuthRequired() {
		return "open_local"
	}
	return "login_required"
}

func requestClientLoopback(r *http.Request) bool {
	ip := net.ParseIP(strings.TrimSpace(clientIP(r)))
	return ip != nil && ip.IsLoopback()
}

func requestOriginAllowed(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	wantHost := strings.TrimSpace(r.Host)
	if requestFromLoopback(r) {
		if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Host")); forwarded != "" {
			wantHost = strings.TrimSpace(strings.Split(forwarded, ",")[0])
		}
	}
	return strings.EqualFold(u.Host, wantHost)
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func hashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

func purgeExpiredSessionsLocked() {
	now := time.Now()
	for k, s := range adminSessions {
		if now.After(s.ExpiresAt) {
			delete(adminSessions, k)
		}
	}
}

func createAdminSession() (string, time.Time, error) {
	tok, err := randomToken(24)
	if err != nil {
		return "", time.Time{}, err
	}
	exp := time.Now().Add(adminSessionTTL)
	adminSessionMu.Lock()
	defer adminSessionMu.Unlock()
	purgeExpiredSessionsLocked()
	adminSessions[hashToken(tok)] = adminSession{ExpiresAt: exp}
	return tok, exp, nil
}

func destroyAdminSession(tok string) {
	if strings.TrimSpace(tok) == "" {
		return
	}
	adminSessionMu.Lock()
	defer adminSessionMu.Unlock()
	delete(adminSessions, hashToken(tok))
}

func sessionValid(tok string) bool {
	tok = strings.TrimSpace(tok)
	if tok == "" {
		return false
	}
	adminSessionMu.Lock()
	defer adminSessionMu.Unlock()
	purgeExpiredSessionsLocked()
	s, ok := adminSessions[hashToken(tok)]
	if !ok {
		return false
	}
	if time.Now().After(s.ExpiresAt) {
		delete(adminSessions, hashToken(tok))
		return false
	}
	return true
}

func readSessionToken(r *http.Request) string {
	if c, err := r.Cookie(adminSessionCookie); err == nil && c != nil {
		return strings.TrimSpace(c.Value)
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(auth) > 7 && strings.EqualFold(auth[:7], "bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	return strings.TrimSpace(r.Header.Get("X-Admin-Session"))
}

func passwordOK(got string) bool {
	want := adminPassword()
	if want == "" {
		return true
	}
	got = strings.TrimSpace(got)
	if got == "" {
		return false
	}
	wh := sha256.Sum256([]byte(want))
	gh := sha256.Sum256([]byte(got))
	return subtle.ConstantTimeCompare(wh[:], gh[:]) == 1
}

func requestSecure(r *http.Request) bool {
	if r == nil {
		return false
	}
	if r.TLS != nil {
		return true
	}
	if !requestFromLoopback(r) {
		return false
	}
	proto := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")))
	if proto == "https" {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Ssl")), "on") {
		return true
	}
	return false
}

func requestFromLoopback(r *http.Request) bool {
	if r == nil {
		return false
	}
	remote := strings.TrimSpace(r.RemoteAddr)
	host, _, err := net.SplitHostPort(remote)
	if err != nil || host == "" {
		host = remote
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	return ip != nil && ip.IsLoopback()
}

func clientIP(r *http.Request) string {
	if r == nil {
		return "unknown"
	}
	remote := strings.TrimSpace(r.RemoteAddr)
	host, _, err := net.SplitHostPort(remote)
	if err != nil || host == "" {
		host = remote
	}
	parsed := net.ParseIP(strings.TrimSpace(host))
	trustedProxy := requestFromLoopback(r)
	if trustedProxy {
		if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
			parts := strings.Split(xff, ",")
			ip := strings.TrimSpace(parts[0])
			if net.ParseIP(ip) != nil {
				return ip
			}
		}
		if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); net.ParseIP(xri) != nil {
			return xri
		}
	}
	if parsed != nil {
		return host
	}
	if remote != "" {
		return remote
	}
	return "unknown"
}

func purgeLoginAttemptsLocked(now time.Time) {
	for k, st := range loginAttempts {
		if st == nil {
			delete(loginAttempts, k)
			continue
		}
		if !st.LockedUntil.IsZero() && now.After(st.LockedUntil.Add(30*time.Minute)) {
			delete(loginAttempts, k)
			continue
		}
		if st.LockedUntil.IsZero() && !st.LastAt.IsZero() && now.Sub(st.LastAt) > loginRateWindow*3 {
			delete(loginAttempts, k)
		}
	}
}

func loginAllowed(ip string) (bool, time.Duration) {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		ip = "unknown"
	}
	now := time.Now()
	loginAttemptMu.Lock()
	defer loginAttemptMu.Unlock()
	purgeLoginAttemptsLocked(now)
	st := loginAttempts[ip]
	if st == nil {
		return true, 0
	}
	if !st.LockedUntil.IsZero() && now.Before(st.LockedUntil) {
		return false, st.LockedUntil.Sub(now)
	}
	if !st.LockedUntil.IsZero() && !now.Before(st.LockedUntil) {
		st.Fails = 0
		st.LockedUntil = time.Time{}
		st.WindowStart = now
	}
	return true, 0
}

func recordLoginFailure(ip string) (locked bool, retryAfter time.Duration, fails int) {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		ip = "unknown"
	}
	now := time.Now()
	loginAttemptMu.Lock()
	defer loginAttemptMu.Unlock()
	purgeLoginAttemptsLocked(now)
	st := loginAttempts[ip]
	if st == nil {
		st = &loginAttemptState{WindowStart: now}
		loginAttempts[ip] = st
	}
	if st.WindowStart.IsZero() || now.Sub(st.WindowStart) > loginRateWindow {
		st.WindowStart = now
		st.Fails = 0
		st.LockedUntil = time.Time{}
	}
	st.Fails++
	st.LastAt = now
	if st.Fails >= loginRateMax {
		st.LockedUntil = now.Add(loginLockTTL)
		return true, loginLockTTL, st.Fails
	}
	return false, 0, st.Fails
}

func recordLoginSuccess(ip string) {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return
	}
	loginAttemptMu.Lock()
	defer loginAttemptMu.Unlock()
	delete(loginAttempts, ip)
}

func auditAuth(event, ip, detail string) {
	clean := func(s string) string {
		s = strings.ReplaceAll(s, "\r", " ")
		s = strings.ReplaceAll(s, "\n", " ")
		return strings.TrimSpace(s)
	}
	ip = clean(ip)
	if ip == "" {
		ip = "-"
	}
	detail = clean(detail)
	if detail == "" {
		detail = "-"
	}
	log.Printf("auth_audit event=%s ip=%s detail=%s", event, ip, detail)
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, tok string, exp time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     adminSessionCookie,
		Value:    tok,
		Path:     "/",
		HttpOnly: true,
		Secure:   requestSecure(r),
		SameSite: http.SameSiteStrictMode,
		Expires:  exp,
		MaxAge:   int(time.Until(exp).Seconds()),
	})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     adminSessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   requestSecure(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	})
}

// requireAdmin:
// 1) no password configured => open_local allow
// 2) valid session => allow
// 3) X-Admin-Token header == admin password => allow (query admin_token removed)
func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if !adminAuthRequired() {
		if requestClientLoopback(r) {
			return true
		}
		writeJSON(w, 403, map[string]any{"ok": false, "error": "未配置管理密码时仅允许本机访问", "auth_mode": adminAuthMode()})
		return false
	}
	if sessionValid(readSessionToken(r)) {
		return true
	}
	got := strings.TrimSpace(r.Header.Get("X-Admin-Token"))
	if passwordOK(got) {
		return true
	}
	writeJSON(w, 401, map[string]any{
		"ok":             false,
		"error":          "需要管理员登录",
		"message":        "未授权：请先登录管理账号",
		"auth_mode":      adminAuthMode(),
		"login_required": true,
	})
	return false
}

func (s *Server) handleAdminAuthStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, 405, map[string]any{"ok": false, "error": "方法不允许"})
		return
	}
	required := adminAuthRequired()
	loggedIn := requestClientLoopback(r)
	if required {
		loggedIn = sessionValid(readSessionToken(r))
	}
	msg := "本机开放：未配置管理密码"
	if required && loggedIn {
		msg = "已登录管理账号"
	} else if required {
		msg = "需要管理员登录"
	}
	out := map[string]any{
		"ok":               true,
		"auth_mode":        adminAuthMode(),
		"login_required":   required,
		"logged_in":        loggedIn,
		"admin_configured": required,
		"message":          msg,
	}
	// Do not leak username before login.
	if loggedIn {
		out["username"] = adminUsername()
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]any{"ok": false, "error": "方法不允许"})
		return
	}
	ip := clientIP(r)
	if !adminAuthRequired() {
		if !requestClientLoopback(r) {
			auditAuth("login_open_local_denied", ip, "non_loopback")
			writeJSON(w, 403, map[string]any{"ok": false, "logged_in": false, "error": "未配置管理密码时仅允许本机访问"})
			return
		}
		auditAuth("login_open_local", ip, "no_password_configured")
		writeJSON(w, 200, map[string]any{
			"ok": true, "logged_in": true, "auth_mode": "open_local",
			"message": "本机开放，无需登录",
		})
		return
	}

	if ok, wait := loginAllowed(ip); !ok {
		sec := int(wait.Seconds())
		if sec < 1 {
			sec = 1
		}
		w.Header().Set("Retry-After", fmt.Sprintf("%d", sec))
		auditAuth("login_locked", ip, fmt.Sprintf("retry_after_sec=%d", sec))
		writeJSON(w, 429, map[string]any{
			"ok":              false,
			"error":           "登录尝试过多，请稍后再试",
			"message":         fmt.Sprintf("登录已暂时锁定，请约 %d 秒后再试", sec),
			"retry_after_sec": sec,
		})
		return
	}

	var payload map[string]any
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&payload); err != nil {
		writeJSON(w, 400, map[string]any{"ok": false, "error": "登录请求格式无效"})
		return
	}
	user := strings.TrimSpace(fmt.Sprint(payload["username"]))
	if user == "<nil>" {
		user = ""
	}
	pass := strings.TrimSpace(fmt.Sprint(payload["password"]))
	if pass == "<nil>" {
		pass = ""
	}

	// Unified failure message: no username/password distinction.
	if !usernameOK(user) || !passwordOK(pass) {
		locked, retryAfter, fails := recordLoginFailure(ip)
		if locked {
			sec := int(retryAfter.Seconds())
			if sec < 1 {
				sec = 1
			}
			w.Header().Set("Retry-After", fmt.Sprintf("%d", sec))
			auditAuth("login_failed_lock", ip, fmt.Sprintf("fails=%d lock_sec=%d", fails, sec))
			writeJSON(w, 429, map[string]any{
				"ok":              false,
				"error":           "登录尝试过多，请稍后再试",
				"message":         fmt.Sprintf("登录失败次数过多，请约 %d 秒后再试", sec),
				"retry_after_sec": sec,
			})
			return
		}
		auditAuth("login_failed", ip, fmt.Sprintf("fails=%d", fails))
		writeJSON(w, 401, map[string]any{
			"ok":      false,
			"error":   "用户名或密码错误",
			"message": "用户名或密码错误",
		})
		return
	}

	tok, exp, err := createAdminSession()
	if err != nil {
		auditAuth("login_session_error", ip, "create_session_failed")
		writeJSON(w, 500, map[string]any{"ok": false, "error": err.Error(), "message": "创建会话失败"})
		return
	}
	recordLoginSuccess(ip)
	setSessionCookie(w, r, tok, exp)
	auditAuth("login_ok", ip, "session_created")
	writeJSON(w, 200, map[string]any{
		"ok":         true,
		"logged_in":  true,
		"auth_mode":  adminAuthMode(),
		"username":   adminUsername(),
		"expires_at": exp.Unix(),
		"message":    "登录成功",
	})
}

func (s *Server) handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]any{"ok": false, "error": "方法不允许"})
		return
	}
	ip := clientIP(r)
	destroyAdminSession(readSessionToken(r))
	clearSessionCookie(w, r)
	auditAuth("logout", ip, "session_cleared")
	writeJSON(w, 200, map[string]any{"ok": true, "logged_in": false, "message": "已退出登录"})
}

// withAdminAPIGate protects panel APIs: unauthenticated clients cannot access overview/control/feature-flags/cookie management.
func (s *Server) withAdminAPIGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		// public paths
		if path == "/" || path == "/index.html" || path == "/api/health" ||
			path == "/api/admin/auth/status" || path == "/api/admin/login" || path == "/api/admin/logout" {
			next.ServeHTTP(w, r)
			return
		}
		// no password configured: open only to the actual client loopback address.
		if !adminAuthRequired() {
			if requestClientLoopback(r) {
				next.ServeHTTP(w, r)
				return
			}
			writeJSON(w, 403, map[string]any{"ok": false, "error": "未配置管理密码时仅允许本机访问", "auth_mode": adminAuthMode()})
			return
		}
		// authenticated APIs
		if strings.HasPrefix(path, "/api/") {
			if sessionValid(readSessionToken(r)) {
				if r.Method != http.MethodGet && r.Method != http.MethodHead && !requestOriginAllowed(r) {
					writeJSON(w, 403, map[string]any{"ok": false, "error": "请求来源校验失败"})
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			// header token only; query admin_token no longer accepted
			got := strings.TrimSpace(r.Header.Get("X-Admin-Token"))
			if passwordOK(got) {
				next.ServeHTTP(w, r)
				return
			}
			writeJSON(w, 401, map[string]any{
				"ok":             false,
				"error":          "需要管理员登录",
				"message":        "未登录，无法访问面板接口",
				"auth_mode":      adminAuthMode(),
				"login_required": true,
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}
