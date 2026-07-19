package dashboard

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fzsmbot/internal/client"
)

const (
	adminTokenEnv     = "FZSM_ADMIN_TOKEN"
	maxCookieBodySize = 1 << 20 // 1 MiB
	maxCookieItems    = 50
)

func (s *Server) cookiePath() string {
	p := ""
	if s.cfg != nil {
		p = strings.TrimSpace(s.cfg.CookieFile)
	}
	if p == "" {
		p = "auth/cookies.json"
	}
	return p
}

func (s *Server) lotteryBase() string {
	return "https://api.fanzisima.xyz"
}

func adminToken() string {
	return strings.TrimSpace(os.Getenv(adminTokenEnv))
}

func maskCookieValue(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if len(v) <= 8 {
		return "****"
	}
	return v[:4] + "…" + v[len(v)-4:] + fmt.Sprintf("（%d字）", len(v))
}

func parseCookieItems(raw any) ([]client.CookieItem, error) {
	items := []client.CookieItem{}
	switch t := raw.(type) {
	case []any:
		for _, it := range t {
			m, ok := it.(map[string]any)
			if !ok {
				continue
			}
			items = append(items, cookieItemFromMap(m))
		}
	case map[string]any:
		if arr, ok := t["cookies"].([]any); ok {
			for _, it := range arr {
				m, ok := it.(map[string]any)
				if !ok {
					continue
				}
				items = append(items, cookieItemFromMap(m))
			}
		} else if name := strings.TrimSpace(fmt.Sprint(t["name"])); name != "" && name != "<nil>" {
			items = append(items, cookieItemFromMap(t))
		} else {
			return nil, fmt.Errorf("无法识别 cookie JSON：需要数组或 {cookies:[...]}")
		}
	default:
		return nil, fmt.Errorf("无法识别 cookie JSON 类型")
	}
	out := make([]client.CookieItem, 0, len(items))
	for _, it := range items {
		name := strings.TrimSpace(it.Name)
		val := strings.TrimSpace(it.Value)
		if name == "" || val == "" || name == "<nil>" || val == "<nil>" {
			continue
		}
		if it.Path == "" || it.Path == "<nil>" {
			it.Path = "/"
		}
		if it.Domain == "" || it.Domain == "<nil>" {
			it.Domain = "fanzisima.xyz"
		}
		domain := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(it.Domain)), ".")
		if domain != "fanzisima.xyz" && !strings.HasSuffix(domain, ".fanzisima.xyz") {
			return nil, fmt.Errorf("cookie domain 不受信任: %s", it.Domain)
		}
		if !strings.HasPrefix(it.Path, "/") {
			return nil, fmt.Errorf("cookie path 必须以 / 开头")
		}
		if len(val) > 16*1024 {
			return nil, fmt.Errorf("cookie value 过长")
		}
		if err := (&http.Cookie{Name: name, Value: val, Path: it.Path, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode}).Valid(); err != nil {
			return nil, fmt.Errorf("cookie 格式无效: %w", err)
		}
		it.Name = name
		it.Value = val
		out = append(out, it)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("没有有效 cookie（需要 name 与 value）")
	}
	if len(out) > maxCookieItems {
		return nil, fmt.Errorf("cookie 条数过多（最多 %d）", maxCookieItems)
	}
	return out, nil
}

func cookieItemFromMap(m map[string]any) client.CookieItem {
	return client.CookieItem{
		Name:     strings.TrimSpace(fmt.Sprint(m["name"])),
		Value:    strings.TrimSpace(fmt.Sprint(m["value"])),
		Domain:   strings.TrimSpace(fmt.Sprint(m["domain"])),
		Path:     strings.TrimSpace(fmt.Sprint(m["path"])),
		Secure:   asBoolAny(m["secure"]),
		HTTPOnly: asBoolAny(m["httpOnly"]) || asBoolAny(m["http_only"]),
	}
}

func asBoolAny(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t == "1" || strings.EqualFold(t, "true") || strings.EqualFold(t, "on")
	case float64:
		return t != 0
	default:
		return false
	}
}

func readCookieFile(path string) ([]client.CookieItem, map[string]any, error) {
	info := map[string]any{
		"path":   path,
		"exists": false,
		"count":  0,
	}
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []client.CookieItem{}, info, nil
		}
		return nil, info, err
	}
	info["exists"] = true
	info["size"] = st.Size()
	info["mtime"] = st.ModTime().Unix()
	info["mtime_text"] = st.ModTime().Format("2006-01-02 15:04:05")

	b, err := os.ReadFile(path)
	if err != nil {
		return nil, info, err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return []client.CookieItem{}, info, nil
	}
	var raw any
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, info, fmt.Errorf("cookie 文件 JSON 无效: %w", err)
	}
	items, err := parseCookieItems(raw)
	if err != nil {
		if arr, ok := raw.([]any); ok && len(arr) == 0 {
			info["count"] = 0
			return []client.CookieItem{}, info, nil
		}
		return nil, info, err
	}
	info["count"] = len(items)
	return items, info, nil
}

func writeCookieFile(path string, items []client.CookieItem) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(dir, 0o700)
	if items == nil {
		items = []client.CookieItem{}
	}
	b, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	return secureSecretWrite(path, b)
}

func secureSecretWrite(path string, b []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func backupCookieFile(path string) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	if st.Size() == 0 {
		return "", nil
	}
	dir := filepath.Dir(path)
	name := fmt.Sprintf("cookies.backup.%d.json", time.Now().Unix())
	dst := filepath.Join(dir, name)
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if err := secureSecretWrite(dst, b); err != nil {
		return "", err
	}
	return dst, nil
}

func maskedCookieList(items []client.CookieItem) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		out = append(out, map[string]any{
			"name":     it.Name,
			"domain":   it.Domain,
			"path":     it.Path,
			"secure":   it.Secure,
			"httpOnly": it.HTTPOnly,
			"value":    maskCookieValue(it.Value),
			"masked":   true,
		})
	}
	return out
}

func fullCookieList(items []client.CookieItem) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		out = append(out, map[string]any{
			"name":     it.Name,
			"domain":   it.Domain,
			"path":     it.Path,
			"secure":   it.Secure,
			"httpOnly": it.HTTPOnly,
			"value":    it.Value,
			"masked":   false,
		})
	}
	return out
}

func (s *Server) newCookieClient() (*client.Client, error) {
	apiBase := "https://fanzisima.xyz/stocks/api"
	if s.cfg != nil && strings.TrimSpace(s.cfg.APIBase) != "" {
		apiBase = s.cfg.APIBase
	}
	return client.New(apiBase, s.lotteryBase(), s.cookiePath())
}

func (s *Server) persistAuthKeepaliveFromProbe(probe map[string]any) {
	if s == nil || s.st == nil || probe == nil {
		return
	}
	stocks, _ := probe["stocks"].(map[string]any)
	lottery, _ := probe["lottery"].(map[string]any)
	if stocks == nil {
		stocks = map[string]any{}
	}
	if lottery == nil {
		lottery = map[string]any{}
	}
	stocksOK := asBoolAny(stocks["ok"])
	lotteryOK := asBoolAny(lottery["ok"])
	ok := stocksOK || lotteryOK
	degraded := ok && !(stocksOK && lotteryOK)
	msg := "cookie 保活正常"
	if ok && degraded {
		if stocksOK && !lotteryOK {
			msg = "cookie 部分有效：股市正常，抽奖探测失败"
		} else if !stocksOK && lotteryOK {
			msg = "cookie 部分有效：抽奖正常，股市探测失败"
		} else {
			msg = "cookie 部分有效"
		}
	}
	if !ok {
		msg = "cookie 保活失败，请在面板重新导入 cookie"
	}
	state := map[string]any{
		"enabled":            true,
		"ok":                 ok,
		"degraded":           degraded,
		"ts":                 float64(time.Now().UnixNano()) / 1e9,
		"message":            msg,
		"stocks":             stocks,
		"lottery":            lottery,
		"source":             "dashboard_probe",
		"impl":               "go",
		"auto":               true,
		"manual_reauth_hint": nil,
		"alert":              nil,
	}
	if !ok {
		state["manual_reauth_hint"] = "控制 → Cookie 管理：粘贴浏览器 fz_lottery 完整值，点导入并探测"
		state["alert"] = "auth_keepalive_failed"
	} else if degraded {
		state["manual_reauth_hint"] = "可选：重新导入完整 cookie，修复失效侧探测"
		state["alert"] = "auth_keepalive_degraded"
	}
	_ = s.st.SetState("auth_keepalive", state)
}

func probeCookieAuth(c *client.Client) map[string]any {
	out := map[string]any{
		"ts": time.Now().Unix(),
	}
	t0 := time.Now()
	stocks := map[string]any{"ok": false}
	// Use AuthProbe so stocks checks match bot keepalive (/me,/portfolio,/farm/me + forced cookie header)
	probe := c.AuthProbe()
	me, _ := probe["me"].(map[string]any)
	if me == nil {
		me = map[string]any{}
	}
	userName := ""
	if u, okm := me["user"].(map[string]any); okm && u != nil {
		userName = firstNonEmpty(asString(u["display_name"]), asString(u["username"]), asString(u["global_name"]))
	}
	stocks = map[string]any{
		"ok":         asBoolAny(probe["ok"]),
		"status":     probe["status"],
		"latency_ms": int(time.Since(t0).Milliseconds()),
		"user":       userName,
		"balance":    me["balance_lobster"],
		"equity":     me["total_asset_lobster"],
		"endpoint":   probe["endpoint"],
		"error":      probe["error"],
	}
	t1 := time.Now()
	lottery := map[string]any{"ok": false}
	if me, err := c.LotteryMe(); err != nil {
		lottery["ok"] = false
		lottery["error"] = err.Error()
		lottery["latency_ms"] = int(time.Since(t1).Milliseconds())
	} else {
		status := int(asFloat(me["_http_status"]))
		ok := me != nil && len(me) > 0 && status < 400
		lottery = map[string]any{
			"ok":         ok,
			"status":     status,
			"latency_ms": int(time.Since(t1).Milliseconds()),
			"free_draws": me["draws_available"],
			"balance":    firstNonEmptyAny(me["remaining_lobster"], me["balance"]),
		}
		if !ok {
			lottery["error"] = fmt.Sprintf("lottery /me status=%d", status)
		}
	}
	ok := asBoolAny(stocks["ok"]) && asBoolAny(lottery["ok"])
	msg := "探测成功：股市与抽奖均已登录"
	if asBoolAny(stocks["ok"]) && !asBoolAny(lottery["ok"]) {
		msg = "股市已登录，抽奖探测失败"
	} else if !asBoolAny(stocks["ok"]) && asBoolAny(lottery["ok"]) {
		msg = "抽奖已登录，股市探测失败"
	} else if !ok {
		msg = "探测失败：需要重新导入有效 cookie"
	}
	out["ok"] = ok
	out["message"] = msg
	out["stocks"] = stocks
	out["lottery"] = lottery
	return out
}

func firstNonEmptyAny(xs ...any) any {
	for _, x := range xs {
		if x == nil {
			continue
		}
		s := strings.TrimSpace(fmt.Sprint(x))
		if s != "" && s != "<nil>" {
			return x
		}
	}
	return nil
}

func decodeCookieImportBody(r *http.Request) (any, error) {
	defer r.Body.Close()
	limited := io.LimitReader(r.Body, maxCookieBodySize+1)
	b, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if len(b) > maxCookieBodySize {
		return nil, fmt.Errorf("请求体过大（上限 1MB）")
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return nil, fmt.Errorf("请求体为空")
	}

	// 允许 {text|raw|json|content|value: "..."} 包装
	var asMap map[string]any
	if err := json.Unmarshal(b, &asMap); err == nil {
		for _, k := range []string{"raw", "text", "json", "content", "value", "cookie", "cookie_value"} {
			if v, ok := asMap[k]; ok {
				inner := strings.TrimSpace(fmt.Sprint(v))
				if inner != "" && inner != "<nil>" {
					return parseFlexibleCookieInput(inner)
				}
			}
		}
		if _, ok := asMap["cookies"]; ok {
			return asMap, nil
		}
		if _, ok := asMap["name"]; ok {
			return asMap, nil
		}
	}

	return parseFlexibleCookieInput(s)
}

// parseFlexibleCookieInput supports:
// 1) JSON array/object
// 2) name=value; other=...
// 3) multi-line name=value
// 4) bare cookie value (default name=fz_lottery)
func parseFlexibleCookieInput(s string) (any, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("content empty")
	}
	// JSON first
	var raw any
	if err := json.Unmarshal([]byte(s), &raw); err == nil {
		return raw, nil
	}

	if !strings.HasPrefix(s, "{") && !strings.HasPrefix(s, "[") {
		tmp := strings.ReplaceAll(s, "\r\n", "\n")
		tmp = strings.ReplaceAll(tmp, "\r", "\n")
		tmp = strings.ReplaceAll(tmp, ";", "\n")
		lines := strings.Split(tmp, "\n")
		items := make([]any, 0, len(lines))
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if strings.Contains(line, "=") {
				name, val, ok := strings.Cut(line, "=")
				name = strings.TrimSpace(name)
				val = strings.TrimSpace(val)
				if !ok || name == "" || val == "" {
					continue
				}
				ln := strings.ToLower(name)
				if ln == "path" || ln == "domain" || ln == "expires" || ln == "max-age" || ln == "secure" || ln == "httponly" || ln == "samesite" {
					continue
				}
				items = append(items, map[string]any{
					"name": name, "value": val, "domain": "fanzisima.xyz", "path": "/",
				})
				continue
			}
			if looksLikeCookieValue(line) {
				items = append(items, map[string]any{
					"name": "fz_lottery", "value": line, "domain": "fanzisima.xyz", "path": "/",
				})
			}
		}
		if len(items) > 0 {
			return items, nil
		}
	}

	if looksLikeCookieValue(s) {
		return []any{map[string]any{
			"name": "fz_lottery", "value": s, "domain": "fanzisima.xyz", "path": "/",
		}}, nil
	}
	return nil, fmt.Errorf("unrecognized cookie input: use JSON, name=value (multiline ok), or bare value")
}

func looksLikeCookieValue(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 16 {
		return false
	}
	// reject obvious sentences
	if strings.ContainsAny(s, " \t\r\n") {
		return false
	}
	// common jwt/token chars
	for _, ch := range s {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || strings.ContainsRune("._-+=/", ch) {
			continue
		}
		return false
	}
	return true
}

func (s *Server) handleCookieStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, 405, map[string]any{"ok": false, "error": "方法不允许"})
		return
	}
	path := s.cookiePath()
	items, info, err := readCookieFile(path)
	if err != nil {
		writeJSON(w, 500, map[string]any{
			"ok": false, "error": err.Error(), "message": "读取 cookie 失败",
			"path": path, "auth_mode": adminAuthMode(),
		})
		return
	}
	ka := asMap(sanitize(s.st.GetStateMap("auth_keepalive")))
	names := make([]string, 0, len(items))
	for _, it := range items {
		names = append(names, it.Name)
	}
	loggedIn := true
	if adminAuthRequired() {
		loggedIn = sessionValid(readSessionToken(r))
	}
	writeJSON(w, 200, map[string]any{
		"ok":              true,
		"path":            path,
		"exists":          info["exists"],
		"count":           len(items),
		"size":            info["size"],
		"mtime":           info["mtime"],
		"mtime_text":      info["mtime_text"],
		"auth_mode":       adminAuthMode(),
		"admin_token_set": adminAuthRequired(),
		"login_required":  adminAuthRequired(),
		"logged_in":       loggedIn,
		"auth_keepalive": map[string]any{
			"ok":      ka["ok"],
			"ts":      ka["ts"],
			"message": ka["message"],
			"enabled": ka["enabled"],
		},
		"names":   names,
		"message": "cookie 状态已加载",
	})
}

func (s *Server) handleCookieList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, 405, map[string]any{"ok": false, "error": "方法不允许"})
		return
	}
	path := s.cookiePath()
	items, info, err := readCookieFile(path)
	if err != nil {
		writeJSON(w, 500, map[string]any{"ok": false, "error": err.Error(), "message": "读取 cookie 失败"})
		return
	}
	writeJSON(w, 200, map[string]any{
		"ok":        true,
		"path":      path,
		"count":     len(items),
		"exists":    info["exists"],
		"mtime":     info["mtime"],
		"cookies":   maskedCookieList(items),
		"masked":    true,
		"auth_mode": adminAuthMode(),
		"message":   "已返回脱敏 cookie 列表",
	})
}

func (s *Server) handleCookieExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, 405, map[string]any{"ok": false, "error": "方法不允许"})
		return
	}
	if !requireAdmin(w, r) {
		return
	}
	path := s.cookiePath()
	items, _, err := readCookieFile(path)
	if err != nil {
		writeJSON(w, 500, map[string]any{"ok": false, "error": err.Error(), "message": "导出失败"})
		return
	}
	writeJSON(w, 200, map[string]any{
		"ok":        true,
		"path":      path,
		"count":     len(items),
		"cookies":   fullCookieList(items),
		"masked":    false,
		"auth_mode": adminAuthMode(),
		"message":   "已导出完整 cookie（请勿分享或提交到 git）",
		"warning":   "包含明文密钥材料，仅限安全环境使用",
	})
}

func cookieKey(it client.CookieItem) string {
	d := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(it.Domain)), ".")
	if d == "" {
		d = "fanzisima.xyz"
	}
	p := strings.TrimSpace(it.Path)
	if p == "" {
		p = "/"
	}
	return strings.ToLower(strings.TrimSpace(it.Name)) + "|" + d + "|" + p
}

// mergeCookieItems: existing + incoming, same name/domain/path is overwritten by incoming.
func mergeCookieItems(existing, incoming []client.CookieItem) []client.CookieItem {
	order := make([]string, 0, len(existing)+len(incoming))
	m := map[string]client.CookieItem{}
	for _, it := range existing {
		k := cookieKey(it)
		if _, ok := m[k]; !ok {
			order = append(order, k)
		}
		m[k] = it
	}
	for _, it := range incoming {
		k := cookieKey(it)
		if _, ok := m[k]; !ok {
			order = append(order, k)
		}
		m[k] = it
	}
	out := make([]client.CookieItem, 0, len(order))
	for _, k := range order {
		out = append(out, m[k])
	}
	return out
}

func (s *Server) handleCookieImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]any{"ok": false, "error": "方法不允许"})
		return
	}
	if !requireAdmin(w, r) {
		return
	}
	raw, err := decodeCookieImportBody(r)
	if err != nil {
		writeJSON(w, 400, map[string]any{"ok": false, "error": err.Error(), "message": "导入内容无效"})
		return
	}
	incoming, err := parseCookieItems(raw)
	if err != nil {
		writeJSON(w, 400, map[string]any{"ok": false, "error": err.Error(), "message": "导入校验失败"})
		return
	}
	path := s.cookiePath()
	backup, berr := backupCookieFile(path)
	if berr != nil {
		writeJSON(w, 500, map[string]any{"ok": false, "error": berr.Error(), "message": "备份失败，已中止导入"})
		return
	}

	// default: merge. replace=1/true overwrites whole file.
	replace := false
	if q := strings.TrimSpace(r.URL.Query().Get("replace")); q == "1" || strings.EqualFold(q, "true") {
		replace = true
	}
	// also allow body flag via raw map already parsed? keep query only for simplicity
	items := incoming
	mode := "merge"
	if replace {
		mode = "replace"
	} else {
		existing, _, _ := readCookieFile(path)
		items = mergeCookieItems(existing, incoming)
	}
	if len(items) > maxCookieItems {
		writeJSON(w, 400, map[string]any{"ok": false, "error": fmt.Sprintf("合并后 cookie 条数过多（最多 %d）", maxCookieItems), "message": "导入失败"})
		return
	}
	if err := writeCookieFile(path, items); err != nil {
		writeJSON(w, 500, map[string]any{"ok": false, "error": err.Error(), "message": "写入 cookie 失败"})
		return
	}

	doProbe := true
	if q := strings.TrimSpace(r.URL.Query().Get("probe")); q == "0" || strings.EqualFold(q, "false") {
		doProbe = false
	}
	msg := fmt.Sprintf("已%s导入 %d 条（当前共 %d 条）", map[string]string{"merge": "合并", "replace": "覆盖"}[mode], len(incoming), len(items))
	resp := map[string]any{
		"ok":        true,
		"path":      path,
		"count":     len(items),
		"imported":  len(incoming),
		"mode":      mode,
		"backup":    backup,
		"cookies":   maskedCookieList(items),
		"auth_mode": adminAuthMode(),
		"message":   msg,
	}
	if doProbe {
		c, err := s.newCookieClient()
		if err != nil {
			resp["probe"] = map[string]any{"ok": false, "error": err.Error()}
			resp["message"] = msg + "；探测客户端创建失败"
		} else {
			probe := probeCookieAuth(c)
			s.persistAuthKeepaliveFromProbe(probe)
			resp["probe"] = probe
			stocksOK := false
			lotteryOK := false
			if m, ok := probe["stocks"].(map[string]any); ok {
				stocksOK = asBoolAny(m["ok"])
			}
			if m, ok := probe["lottery"].(map[string]any); ok {
				lotteryOK = asBoolAny(m["ok"])
			}
			if asBoolAny(probe["ok"]) {
				resp["message"] = msg + "；探测通过（股市+抽奖）"
			} else if stocksOK || lotteryOK {
				side := "股市"
				if lotteryOK && !stocksOK {
					side = "抽奖"
				} else if stocksOK && !lotteryOK {
					side = "股市"
				} else {
					side = "部分"
				}
				resp["message"] = msg + "；部分有效（" + side + "可用）。通常仍可继续跑，失败侧可稍后重导"
				resp["partial"] = true
			} else {
				resp["message"] = msg + "；探测失败，请确认复制的是完整 fz_lottery 原值"
			}
		}
	}
	writeJSON(w, 200, resp)
}

func (s *Server) handleCookieProbe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]any{"ok": false, "error": "方法不允许"})
		return
	}
	if !requireAdmin(w, r) {
		return
	}
	path := s.cookiePath()
	items, info, err := readCookieFile(path)
	if err != nil {
		writeJSON(w, 500, map[string]any{"ok": false, "error": err.Error(), "message": "读取 cookie 失败"})
		return
	}
	if len(items) == 0 {
		writeJSON(w, 200, map[string]any{
			"ok":        false,
			"path":      path,
			"exists":    info["exists"],
			"count":     0,
			"message":   "没有可探测的 cookie",
			"probe":     map[string]any{"ok": false, "message": "cookie 为空"},
			"auth_mode": adminAuthMode(),
		})
		return
	}
	c, err := s.newCookieClient()
	if err != nil {
		writeJSON(w, 500, map[string]any{"ok": false, "error": err.Error(), "message": "创建客户端失败"})
		return
	}
	probe := probeCookieAuth(c)
	s.persistAuthKeepaliveFromProbe(probe)
	writeJSON(w, 200, map[string]any{
		"ok":        asBoolAny(probe["ok"]),
		"path":      path,
		"count":     len(items),
		"probe":     probe,
		"auth_mode": adminAuthMode(),
		"message":   probe["message"],
	})
}

func (s *Server) handleCookieClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]any{"ok": false, "error": "方法不允许"})
		return
	}
	if !requireAdmin(w, r) {
		return
	}
	path := s.cookiePath()
	backup, err := backupCookieFile(path)
	if err != nil {
		writeJSON(w, 500, map[string]any{"ok": false, "error": err.Error(), "message": "备份失败，已中止清除"})
		return
	}
	if err := writeCookieFile(path, []client.CookieItem{}); err != nil {
		writeJSON(w, 500, map[string]any{"ok": false, "error": err.Error(), "message": "清除失败"})
		return
	}
	writeJSON(w, 200, map[string]any{
		"ok":        true,
		"path":      path,
		"count":     0,
		"backup":    backup,
		"auth_mode": adminAuthMode(),
		"message":   "已清除 cookie（如有原文件已备份）",
		"warning":   "bot 下一轮可能掉登录，请尽快重新导入",
	})
}
