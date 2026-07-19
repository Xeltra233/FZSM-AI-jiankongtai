package client

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type CookieItem struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Domain   string `json:"domain"`
	Path     string `json:"path"`
	Secure   bool   `json:"secure"`
	HTTPOnly bool   `json:"httpOnly"`
}

type Client struct {
	StocksBase  string
	LotteryBase string
	CookieFile  string
	HTTP        *http.Client
	Timeout     time.Duration
	// primaryCookies always attached to requests (host-independent).
	// This avoids cookiejar domain mismatches for cross-host auth cookies.
	primaryCookies map[string]string
}

const maxResponseBodySize = 8 << 20

func New(apiBase, lotteryBase, cookieFile string) (*Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(apiBase) == "" {
		apiBase = "https://fanzisima.xyz/stocks/api"
	}
	if strings.TrimSpace(lotteryBase) == "" {
		lotteryBase = "https://api.fanzisima.xyz"
	}
	c := &Client{
		StocksBase:  strings.TrimRight(apiBase, "/"),
		LotteryBase: strings.TrimRight(lotteryBase, "/"),
		CookieFile:  cookieFile,
		Timeout:     12 * time.Second,
		HTTP: &http.Client{
			Timeout: 12 * time.Second,
			Jar:     jar,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) == 0 {
					return nil
				}
				first := via[0].URL
				if !strings.EqualFold(req.URL.Scheme, first.Scheme) || !strings.EqualFold(req.URL.Host, first.Host) {
					return fmt.Errorf("cross-origin redirect blocked: %s", req.URL.Redacted())
				}
				return nil
			},
		},
		primaryCookies: map[string]string{},
	}
	if cookieFile != "" {
		_, _ = c.LoadCookies(cookieFile)
	}
	return c, nil
}

func (c *Client) LoadCookies(path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	var raw any
	if err := json.Unmarshal(b, &raw); err != nil {
		return 0, err
	}
	items := []CookieItem{}
	switch t := raw.(type) {
	case []any:
		for _, it := range t {
			if m, ok := it.(map[string]any); ok {
				items = append(items, cookieFromMap(m))
			}
		}
	case map[string]any:
		if arr, ok := t["cookies"].([]any); ok {
			for _, it := range arr {
				if m, ok := it.(map[string]any); ok {
					items = append(items, cookieFromMap(m))
				}
			}
		}
	}
	// Reload means replace, not merge: removed/rotated credentials must stop being sent.
	c.primaryCookies = map[string]string{}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return 0, err
	}
	c.HTTP.Jar = jar
	n := 0
	for _, it := range items {
		if it.Name == "" || it.Value == "" || it.Name == "<nil>" || it.Value == "<nil>" {
			continue
		}
		// always keep auth-bearing cookies for direct Cookie header injection
		ln := strings.ToLower(strings.TrimSpace(it.Name))
		if ln == "fz_lottery" || strings.Contains(ln, "session") || strings.Contains(ln, "token") || strings.Contains(ln, "auth") {
			c.primaryCookies[it.Name] = it.Value
		} else if len(c.primaryCookies) == 0 {
			// fallback: keep first cookie as primary if nothing matched
			c.primaryCookies[it.Name] = it.Value
		}
		domains := []string{"fanzisima.xyz", "api.fanzisima.xyz", "www.fanzisima.xyz"}
		if it.Domain != "" && it.Domain != "<nil>" {
			d0 := strings.TrimPrefix(strings.TrimSpace(it.Domain), ".")
			if d0 != "" {
				domains = append([]string{d0}, domains...)
			}
		}
		// unique domains
		seenDom := map[string]bool{}
		path := it.Path
		if path == "" || path == "<nil>" {
			path = "/"
		}
		for _, d := range domains {
			d = strings.TrimPrefix(strings.TrimSpace(d), ".")
			if d == "" || seenDom[d] {
				continue
			}
			seenDom[d] = true
			u, err := url.Parse("https://" + d + path)
			if err != nil || u == nil {
				continue
			}
			// IMPORTANT: leave Domain empty for cookiejar.SetCookies.
			// Setting Domain explicitly often makes host-only requests miss the cookie.
			c.HTTP.Jar.SetCookies(u, []*http.Cookie{{
				Name:     it.Name,
				Value:    it.Value,
				Path:     path,
				Secure:   true,
				HttpOnly: it.HTTPOnly,
				SameSite: http.SameSiteStrictMode,
			}})
			n++
		}
	}
	return n, nil
}

func cookieFromMap(m map[string]any) CookieItem {
	return CookieItem{
		Name:   fmt.Sprint(m["name"]),
		Value:  fmt.Sprint(m["value"]),
		Domain: fmt.Sprint(m["domain"]),
		Path:   fmt.Sprint(m["path"]),
		Secure: asBool(m["secure"]),
	}
}

func asBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t == "1" || strings.EqualFold(t, "true")
	default:
		return false
	}
}

func (c *Client) SaveCookies(path string) (int, error) {
	if path == "" {
		path = c.CookieFile
	}
	if path == "" {
		path = "auth/cookies.json"
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, err
	}
	_ = os.Chmod(dir, 0o700)
	seen := map[string]CookieItem{}
	for _, host := range []string{"https://fanzisima.xyz/", "https://api.fanzisima.xyz/"} {
		u, _ := url.Parse(host)
		for _, ck := range c.HTTP.Jar.Cookies(u) {
			key := ck.Name
			seen[key] = CookieItem{
				Name: ck.Name, Value: ck.Value,
				Domain: firstNonEmpty(ck.Domain, "fanzisima.xyz"),
				Path:   firstNonEmpty(ck.Path, "/"),
				Secure: ck.Secure,
			}
		}
	}
	jar := make([]CookieItem, 0, len(seen))
	for _, v := range seen {
		jar = append(jar, v)
	}
	b, err := json.MarshalIndent(jar, "", "  ")
	if err != nil {
		return 0, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return 0, err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return 0, err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return 0, err
	}
	if err := f.Close(); err != nil {
		return 0, err
	}
	return len(jar), nil
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func (c *Client) do(method, fullURL string, body any, headers map[string]string) (int, any, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rdr = strings.NewReader(string(b))
	}
	req, err := http.NewRequest(method, fullURL, rdr)
	if err != nil {
		return 0, nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// Force-attach primary auth cookies. Jar alone is unreliable across fanzisima.xyz / api.fanzisima.xyz.
	if len(c.primaryCookies) > 0 {
		parts := make([]string, 0, len(c.primaryCookies))
		for name, val := range c.primaryCookies {
			name = strings.TrimSpace(name)
			val = strings.TrimSpace(val)
			if name == "" || val == "" {
				continue
			}
			parts = append(parts, name+"="+val)
		}
		if len(parts) > 0 {
			// If caller already set Cookie, append.
			if old := strings.TrimSpace(req.Header.Get("Cookie")); old != "" {
				req.Header.Set("Cookie", old+"; "+strings.Join(parts, "; "))
			} else {
				req.Header.Set("Cookie", strings.Join(parts, "; "))
			}
		}
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodySize+1))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	if len(raw) > maxResponseBodySize {
		return resp.StatusCode, nil, fmt.Errorf("response body exceeds %d bytes", maxResponseBodySize)
	}
	var data any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &data); err != nil {
			data = map[string]any{"raw": string(raw), "status_code": resp.StatusCode}
		}
	} else {
		data = map[string]any{}
	}
	return resp.StatusCode, data, nil
}

func resolveEndpointURL(base, path string) (string, error) {
	baseURL, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil || baseURL.Scheme == "" || baseURL.Host == "" {
		return "", fmt.Errorf("invalid API base URL")
	}
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		u, err := url.Parse(path)
		if err != nil || !strings.EqualFold(u.Scheme, baseURL.Scheme) || !strings.EqualFold(u.Host, baseURL.Host) {
			return "", fmt.Errorf("absolute endpoint outside configured API origin")
		}
		return u.String(), nil
	}
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/"), nil
}

func (c *Client) StocksGet(path string) (int, any, error) {
	u, err := resolveEndpointURL(c.StocksBase, path)
	if err != nil {
		return 0, nil, err
	}
	return c.do("GET", u, nil, map[string]string{
		"User-Agent": "fzsm-go-bot/0.1",
		"Accept":     "application/json, text/event-stream, */*",
		"Origin":     "https://fanzisima.xyz",
		"Referer":    "https://fanzisima.xyz/stocks/",
	})
}

func (c *Client) LotteryGet(path string) (int, any, error) {
	u, err := resolveEndpointURL(c.LotteryBase, path)
	if err != nil {
		return 0, nil, err
	}
	return c.do("GET", u, nil, map[string]string{
		"User-Agent": "fzsm-go-bot/0.1 (+lottery)",
		"Accept":     "application/json, text/plain, */*",
		"Origin":     "https://api.fanzisima.xyz",
		"Referer":    "https://api.fanzisima.xyz/lottery/page",
	})
}

func unwrapData(data any) map[string]any {
	if m, ok := data.(map[string]any); ok {
		if d, ok := m["data"]; ok {
			if dm, ok := d.(map[string]any); ok {
				// preserve top-level flags sometimes useful for auth checks
				if _, has := dm["success"]; !has {
					if s, ok := m["success"]; ok {
						dm["success"] = s
					}
				}
				return dm
			}
		}
		return m
	}
	return map[string]any{"raw": data}
}

func (c *Client) StocksList(path string) ([]any, int, error) {
	code, data, err := c.StocksGet(path)
	if err != nil {
		return nil, code, err
	}
	if m, ok := data.(map[string]any); ok {
		if d, ok := m["data"]; ok {
			if arr, ok := d.([]any); ok {
				return arr, code, nil
			}
			if dm, ok := d.(map[string]any); ok {
				// sometimes wrapped list under items/list/rows
				for _, k := range []string{"items", "list", "rows", "leaderboard", "rankings"} {
					if arr, ok := dm[k].([]any); ok {
						return arr, code, nil
					}
				}
			}
		}
	}
	if arr, ok := data.([]any); ok {
		return arr, code, nil
	}
	return []any{}, code, nil
}

// StocksMap GET stocks path and unwrap {data:...} envelope when present.
func (c *Client) StocksMap(path string) (map[string]any, int, error) {
	code, data, err := c.StocksGet(path)
	if err != nil {
		return nil, code, err
	}
	m := unwrapData(data)
	m["_http_status"] = code
	return m, code, nil
}

func (c *Client) StocksPost(path string, body any) (int, any, error) {
	u := path
	if !strings.HasPrefix(path, "http") {
		u = c.StocksBase + "/" + strings.TrimLeft(path, "/")
	}
	return c.do("POST", u, body, map[string]string{
		"User-Agent": "fzsm-go-bot/0.1",
		"Accept":     "application/json, text/event-stream, */*",
		"Origin":     "https://fanzisima.xyz",
		"Referer":    "https://fanzisima.xyz/stocks/",
	})
}

func (c *Client) LotteryPost(path string, body any) (int, any, error) {
	u := path
	if !strings.HasPrefix(path, "http") {
		u = c.LotteryBase + "/" + strings.TrimLeft(path, "/")
	}
	return c.do("POST", u, body, map[string]string{
		"User-Agent": "fzsm-go-bot/0.1 (+lottery)",
		"Accept":     "application/json, text/plain, */*",
		"Origin":     "https://api.fanzisima.xyz",
		"Referer":    "https://api.fanzisima.xyz/lottery/page",
	})
}

func (c *Client) FarmPlant(plotNo int, cropKey string) (map[string]any, error) {
	code, data, err := c.StocksPost("/farm/plant", map[string]any{"plot_no": plotNo, "crop_key": cropKey})
	if err != nil {
		return nil, err
	}
	m := unwrapData(data)
	m["_http_status"] = code
	if code >= 400 {
		return m, fmt.Errorf("farm/plant status=%d data=%v", code, data)
	}
	return m, nil
}

func (c *Client) FarmHarvest(plotNo int) (map[string]any, error) {
	code, data, err := c.StocksPost("/farm/harvest", map[string]any{"plot_no": plotNo})
	if err != nil {
		return nil, err
	}
	m := unwrapData(data)
	m["_http_status"] = code
	if code >= 400 {
		return m, fmt.Errorf("farm/harvest status=%d data=%v", code, data)
	}
	return m, nil
}

func (c *Client) FarmSteal(targetUserID, plotNo int) (map[string]any, error) {
	code, data, err := c.StocksPost("/farm/steal", map[string]any{"target_user_id": targetUserID, "plot_no": plotNo})
	if err != nil {
		return nil, err
	}
	m := unwrapData(data)
	m["_http_status"] = code
	if code >= 400 {
		return m, fmt.Errorf("farm/steal status=%d data=%v", code, data)
	}
	return m, nil
}

func (c *Client) FarmTargets() ([]any, error) {
	code, data, err := c.StocksGet("/farm/targets")
	if err != nil {
		return nil, err
	}
	if code >= 400 {
		return nil, fmt.Errorf("farm/targets status=%d", code)
	}
	// targets may be list or {data:[...]}
	if arr, ok := data.([]any); ok {
		return arr, nil
	}
	if m, ok := data.(map[string]any); ok {
		if d, ok := m["data"]; ok {
			if arr, ok := d.([]any); ok {
				return arr, nil
			}
		}
	}
	return []any{}, nil
}

func (c *Client) FarmFeed() ([]any, error) {
	code, data, err := c.StocksGet("/farm/feed")
	if err != nil {
		return nil, err
	}
	if code >= 400 {
		return nil, fmt.Errorf("farm/feed status=%d", code)
	}
	if arr, ok := data.([]any); ok {
		return arr, nil
	}
	if m, ok := data.(map[string]any); ok {
		if d, ok := m["data"]; ok {
			if arr, ok := d.([]any); ok {
				return arr, nil
			}
		}
	}
	return []any{}, nil
}

func (c *Client) LotteryCheckin() (map[string]any, error) {
	code, data, err := c.LotteryPost("/lottery/api/checkin", map[string]any{})
	if err != nil {
		return nil, err
	}
	m := unwrapData(data)
	m["_http_status"] = code
	if code >= 400 {
		return m, fmt.Errorf("lottery checkin status=%d data=%v", code, data)
	}
	return m, nil
}

func (c *Client) LotteryDraw(premium bool) (map[string]any, error) {
	path := "/lottery/api/draw"
	if premium {
		path = "/lottery/api/draw-premium"
	}
	code, data, err := c.LotteryPost(path, map[string]any{})
	if err != nil {
		return nil, err
	}
	m := unwrapData(data)
	m["_http_status"] = code
	if code >= 400 {
		return m, fmt.Errorf("lottery draw premium=%v status=%d data=%v", premium, code, data)
	}
	return m, nil
}
func (c *Client) FarmMe() (map[string]any, error) {
	m, code, err := c.StocksMap("/farm/me")
	if err != nil {
		return nil, err
	}
	if code >= 400 {
		return m, fmt.Errorf("farm/me status=%d", code)
	}
	return m, nil
}

func (c *Client) Portfolio() (map[string]any, error) {
	m, code, err := c.StocksMap("/portfolio")
	if err != nil {
		return nil, err
	}
	if code >= 400 {
		return m, fmt.Errorf("portfolio status=%d", code)
	}
	return m, nil
}

func (c *Client) Market() (map[string]any, error) {
	m, code, err := c.StocksMap("/market")
	if err != nil {
		return nil, err
	}
	if code >= 400 {
		return m, fmt.Errorf("market status=%d", code)
	}
	return m, nil
}

func (c *Client) StocksMe() (map[string]any, error) {
	code, data, err := c.StocksGet("/me")
	if err != nil {
		return nil, err
	}
	m := unwrapData(data)
	m["_http_status"] = code
	return m, nil
}

func (c *Client) LotteryMap(path string) (map[string]any, int, error) {
	code, data, err := c.LotteryGet(path)
	if err != nil {
		return nil, code, err
	}
	m := unwrapData(data)
	m["_http_status"] = code
	return m, code, nil
}

func (c *Client) LotteryMe() (map[string]any, error) {
	code, data, err := c.LotteryGet("/lottery/api/me")
	if err != nil {
		return nil, err
	}
	m := unwrapData(data)
	m["_http_status"] = code
	return m, nil
}

func (c *Client) AuthProbe() map[string]any {
	// Try multiple stocks endpoints; some sessions accept /portfolio or /farm/me even if /me shape changes.
	type cand struct {
		name string
		fn   func() (map[string]any, error)
	}
	cands := []cand{
		{name: "/me", fn: c.StocksMe},
		{name: "/portfolio", fn: c.Portfolio},
		{name: "/farm/me", fn: c.FarmMe},
	}
	var lastErr string
	var lastStatus any = nil
	var lastMe map[string]any
	for _, cd := range cands {
		me, err := cd.fn()
		if err != nil {
			lastErr = err.Error()
			continue
		}
		if me == nil {
			me = map[string]any{}
		}
		status := int(asFloat(me["_http_status"]))
		lastStatus = status
		lastMe = me
		ok := status > 0 && status < 400 && (me["balance_lobster"] != nil || me["user"] != nil || me["total_asset_lobster"] != nil || me["positions"] != nil || me["holdings"] != nil || len(me) > 1)
		if ok {
			return map[string]any{
				"ok": true, "status": status, "me": me, "error": nil, "endpoint": cd.name,
			}
		}
		lastErr = fmt.Sprintf("stocks %s status=%d", cd.name, status)
	}
	if lastMe == nil {
		lastMe = map[string]any{}
	}
	return map[string]any{"ok": false, "status": lastStatus, "me": lastMe, "error": lastErr}
}

func asFloat(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int:
		return float64(t)
	default:
		return 0
	}
}

func (c *Client) BuyMarket(stockID int, shares int) (map[string]any, error) {
	code, data, err := c.StocksPost("/buy", map[string]any{"stock_id": stockID, "shares": shares})
	if err != nil {
		return nil, err
	}
	m := unwrapData(data)
	m["_http_status"] = code
	if code >= 400 {
		return m, fmt.Errorf("buy status=%d data=%v", code, data)
	}
	return m, nil
}

func (c *Client) SellMarket(stockID int, shares int) (map[string]any, error) {
	code, data, err := c.StocksPost("/sell", map[string]any{"stock_id": stockID, "shares": shares})
	if err != nil {
		return nil, err
	}
	m := unwrapData(data)
	m["_http_status"] = code
	if code >= 400 {
		return m, fmt.Errorf("sell status=%d data=%v", code, data)
	}
	return m, nil
}

func (c *Client) Preview(stockID int, side string, shares int) (map[string]any, error) {
	code, data, err := c.StocksPost("/trades/preview", map[string]any{
		"stock_id": stockID, "order_type": "market", "side": side, "shares": shares,
	})
	if err != nil {
		return nil, err
	}
	m := unwrapData(data)
	m["_http_status"] = code
	if code >= 400 {
		return m, fmt.Errorf("preview status=%d", code)
	}
	return m, nil
}
