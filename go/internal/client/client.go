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
}

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
		},
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
	n := 0
	for _, it := range items {
		if it.Name == "" || it.Value == "" || it.Name == "<nil>" || it.Value == "<nil>" {
			continue
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
				Name:   it.Name,
				Value:  it.Value,
				Path:   path,
				Secure: true,
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, err
	}
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
	if err := os.WriteFile(path, b, 0o644); err != nil {
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
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
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

func (c *Client) StocksGet(path string) (int, any, error) {
	u := path
	if !strings.HasPrefix(path, "http") {
		u = c.StocksBase + "/" + strings.TrimLeft(path, "/")
	}
	return c.do("GET", u, nil, map[string]string{
		"User-Agent": "fzsm-go-bot/0.1",
		"Accept":     "application/json, text/event-stream, */*",
		"Origin":     "https://fanzisima.xyz",
		"Referer":    "https://fanzisima.xyz/stocks/",
	})
}

func (c *Client) LotteryGet(path string) (int, any, error) {
	u := path
	if !strings.HasPrefix(path, "http") {
		u = c.LotteryBase + "/" + strings.TrimLeft(path, "/")
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
