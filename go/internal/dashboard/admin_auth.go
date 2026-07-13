package dashboard

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	adminPasswordEnv   = "FZSM_ADMIN_PASSWORD"
	adminSessionCookie = "fzsm_admin_session"
	adminSessionTTL    = 12 * time.Hour
)

type adminSession struct {
	ExpiresAt time.Time
}

var (
	adminSessionMu sync.Mutex
	adminSessions  = map[string]adminSession{}
)

func adminPassword() string {
	if p := strings.TrimSpace(os.Getenv(adminPasswordEnv)); p != "" {
		return p
	}
	// 兼容旧环境：没有独立密码时，复用 FZSM_ADMIN_TOKEN 作为管理密码
	return strings.TrimSpace(os.Getenv(adminTokenEnv))
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

func setSessionCookie(w http.ResponseWriter, tok string, exp time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     adminSessionCookie,
		Value:    tok,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  exp,
		MaxAge:   int(time.Until(exp).Seconds()),
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     adminSessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	})
}

// requireAdmin:
// 1) 未配置管理密码 => open_local 放行
// 2) 已登录 session => 放行
// 3) 兼容旧 X-Admin-Token / admin_token == 管理密码 => 放行
func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if !adminAuthRequired() {
		return true
	}
	if sessionValid(readSessionToken(r)) {
		return true
	}
	got := strings.TrimSpace(r.Header.Get("X-Admin-Token"))
	if got == "" {
		got = strings.TrimSpace(r.URL.Query().Get("admin_token"))
	}
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
	loggedIn := true
	if required {
		loggedIn = sessionValid(readSessionToken(r))
	}
	msg := "本机开放：未配置管理密码"
	if required && loggedIn {
		msg = "已登录管理账号"
	} else if required {
		msg = "需要管理员登录"
	}
	writeJSON(w, 200, map[string]any{
		"ok":               true,
		"auth_mode":        adminAuthMode(),
		"login_required":   required,
		"logged_in":        loggedIn,
		"username":         "admin",
		"admin_configured": required,
		"message":          msg,
	})
}

func (s *Server) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]any{"ok": false, "error": "方法不允许"})
		return
	}
	if !adminAuthRequired() {
		writeJSON(w, 200, map[string]any{
			"ok": true, "logged_in": true, "auth_mode": "open_local",
			"message": "本机开放，无需登录",
		})
		return
	}
	var payload map[string]any
	_ = json.NewDecoder(r.Body).Decode(&payload)
	user := strings.TrimSpace(fmt.Sprint(payload["username"]))
	if user == "" || user == "<nil>" {
		user = "admin"
	}
	if user != "admin" {
		writeJSON(w, 401, map[string]any{"ok": false, "error": "用户名或密码错误", "message": "用户名或密码错误"})
		return
	}
	pass := strings.TrimSpace(fmt.Sprint(payload["password"]))
	if pass == "<nil>" {
		pass = ""
	}
	if !passwordOK(pass) {
		writeJSON(w, 401, map[string]any{"ok": false, "error": "用户名或密码错误", "message": "用户名或密码错误"})
		return
	}
	tok, exp, err := createAdminSession()
	if err != nil {
		writeJSON(w, 500, map[string]any{"ok": false, "error": err.Error(), "message": "创建会话失败"})
		return
	}
	setSessionCookie(w, tok, exp)
	writeJSON(w, 200, map[string]any{
		"ok":         true,
		"logged_in":  true,
		"auth_mode":  adminAuthMode(),
		"username":   "admin",
		"expires_at": exp.Unix(),
		"message":    "登录成功",
	})
}

func (s *Server) handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]any{"ok": false, "error": "方法不允许"})
		return
	}
	destroyAdminSession(readSessionToken(r))
	clearSessionCookie(w)
	writeJSON(w, 200, map[string]any{"ok": true, "logged_in": false, "message": "已退出登录"})
}


// withAdminAPIGate 保护面板 API：未登录不可访问 overview/control/feature-flags/cookie 管理
func (s *Server) withAdminAPIGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		// 公开路径
		if path == "/" || path == "/index.html" || path == "/api/health" ||
			path == "/api/admin/auth/status" || path == "/api/admin/login" || path == "/api/admin/logout" {
			next.ServeHTTP(w, r)
			return
		}
		// 未配置密码：本机开放
		if !adminAuthRequired() {
			next.ServeHTTP(w, r)
			return
		}
		// 需要登录的 API
		if strings.HasPrefix(path, "/api/") {
			if sessionValid(readSessionToken(r)) {
				next.ServeHTTP(w, r)
				return
			}
			// 兼容旧 token 头
			got := strings.TrimSpace(r.Header.Get("X-Admin-Token"))
			if got == "" {
				got = strings.TrimSpace(r.URL.Query().Get("admin_token"))
			}
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
