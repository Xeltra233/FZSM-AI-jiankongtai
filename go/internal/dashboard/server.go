package dashboard

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"fzsmbot/internal/config"
	"fzsmbot/internal/flags"
	"fzsmbot/internal/storage"
)

type Server struct {
	cfg     *config.Config
	st      *storage.Storage
	html    string
	refresh int
}

func New(cfg *config.Config, st *storage.Storage, htmlPath string) (*Server, error) {
	b, err := os.ReadFile(htmlPath)
	if err != nil {
		return nil, err
	}
	return &Server{cfg: cfg, st: st, html: string(b), refresh: cfg.Dashboard.RefreshSec}, nil
}

func normalize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = normalize(val)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[fmt.Sprint(k)] = normalize(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = normalize(val)
		}
		return out
	default:
		// also handle map[interface{}]interface{} via reflection-like type assert fallback
		return v
	}
}

func sanitize(v any) any {
	v = normalize(v)
	b, err := json.Marshal(v)
	if err != nil {
		// last resort string form
		return map[string]any{"_error": err.Error(), "_type": fmt.Sprintf("%T", v)}
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return map[string]any{"_error": err.Error()}
	}
	return out
}

func (s *Server) Overview() map[string]any {
	last := asMap(sanitize(s.st.GetStateMap("last_loop")))
	service := asMap(sanitize(s.st.GetStateMap("service")))
	control := asMap(sanitize(s.st.GetStateMap("control")))
	if len(control) == 0 {
		control = map[string]any{"trade_mode": "auto", "capital_style": "prefer_hold"}
	}
	account := asMap(last["account"])
	if account == nil {
		account = map[string]any{}
	}
	// if last_loop empty, still surface farm/modules from runtime_state
	profile := firstNonEmpty(
		asString(last["profile"]),
		asString(service["profile"]),
		asString(mapGet(s.cfg.Strategy, "profile")),
		"balanced",
	)
	regime := asMap(last["regime"])
	if len(regime) == 0 {
		regime = asMap(sanitize(s.st.GetStateMap("regime")))
	}
	risk := asMap(sanitize(s.st.GetStateMap("risk")))
	if len(risk) == 0 {
		risk = asMap(sanitize(s.cfg.Risk))
	}
	ka := asMap(sanitize(s.st.GetStateMap("auth_keepalive")))
	riskEdges := map[string]any{
		"slot":        asMap(sanitize(s.st.GetStateMap("lottery.slot_edge"))),
		"yolo":        asMap(sanitize(s.st.GetStateMap("risk.edge.yolo"))),
		"vip_bet":     asMap(sanitize(s.st.GetStateMap("risk.edge.vip_bet"))),
		"borrow":      asMap(sanitize(s.st.GetStateMap("risk.edge.borrow"))),
		"derivatives": asMap(sanitize(s.st.GetStateMap("risk.edge.derivatives"))),
		"underwrite":  asMap(sanitize(s.st.GetStateMap("risk.edge.underwrite"))),
	}

	authOK := true
	if v, ok := ka["ok"].(bool); ok {
		authOK = v
	}
	userName := firstNonEmpty(asString(service["user_name"]), asString(asMap(ka["stocks"])["user"]), "-")
	return map[string]any{
		"ts":            firstNum(last["ts"], float64(time.Now().Unix())),
		"mode":          s.cfg.Mode,
		"profile":       profile,
		"auth_ok":       authOK,
		"user_name":     userName,
		"account":       account,
		"index":         asMap(last["index"]),
		"buy_count":     last["buy_count"],
		"sell_count":    last["sell_count"],
		"trade_count":   last["trade_count"],
		"top_signals":   asSlice(last["top_signals"]),
		"recent_trades": sanitize(s.st.RecentTrades(30)),
		"trade_stats":   s.st.TradeStats(),
		"service":       service,
		"control":       control,
		"feature_flags": sanitize(flags.Get(s.cfg, s.st)),
		"regime":        regime,
		"risk":          risk,
		"log_tail":      tailLog(filepath.Join(s.cfg.Storage.LogDir, "bot.log"), 30),
		"equity_series": s.equitySeries(240),
		"farm":          asMap(sanitize(s.st.GetStateMap("farm"))),
		"side_hustle":   asMap(sanitize(s.st.GetStateMap("side_hustle"))),
		"derivatives":   asMap(sanitize(s.st.GetStateMap("derivatives"))),
		"modules":       asMap(sanitize(s.st.GetStateMap("modules"))),
		"bias": map[string]any{
			"calendar":    asMap(sanitize(s.st.GetStateMap("bias.calendar"))),
			"leaderboard": asMap(sanitize(s.st.GetStateMap("bias.leaderboard"))),
		},
		"funds":          s.fundsBreakdown(account),
		"auth_keepalive": ka,
		"risk_edges":     riskEdges,
		"llm_usage":      asMap(sanitize(s.st.GetStateMap("llm_usage"))),
		"notes":          []string{"go-dashboard"},
	}
}

func (s *Server) fundsBreakdown(account map[string]any) map[string]any {
	cash := asFloat(account["cash"])
	equity := asFloat(account["equity"])
	stock := asFloat(account["stock_value"])
	positions := asSlice(account["positions"])
	if stock == 0 {
		for _, p := range positions {
			if m, ok := p.(map[string]any); ok {
				mv := asFloat(m["market_value"])
				if mv == 0 {
					mv = asFloat(m["shares"]) * asFloat(firstNonEmptyNum(m["price"], m["current_price"], m["avg_price"]))
				}
				stock += mv
			}
		}
	}
	if equity == 0 {
		equity = cash + stock
	}
	gap := equity - (cash + stock)
	denom := equity
	if denom == 0 {
		denom = 1
	}
	composition := []map[string]any{
		{"key": "cash", "name": "\u73b0\u91d1", "amount": cash, "pct": cash / denom},
		{"key": "stock", "name": "\u6301\u4ed3\u5e02\u503c", "amount": stock, "pct": stock / denom},
	}
	prevCash, prevEquity, prevStock := 0.0, 0.0, 0.0
	snaps := s.st.RecentSnapshots("loop", 2)
	if len(snaps) >= 2 {
		acc := asMap(asMap(snaps[1]["payload"])["account"])
		prevCash = asFloat(acc["cash"])
		prevEquity = asFloat(acc["equity"])
		prevStock = asFloat(acc["stock_value"])
	} else {
		prevCash, prevEquity, prevStock = cash, equity, stock
	}
	delta := map[string]any{
		"cash": cash - prevCash, "equity": equity - prevEquity, "stock_value": stock - prevStock,
	}
	changes := []map[string]any{
		{"metric": "\u73b0\u91d1", "delta": cash - prevCash, "explain": "\u76f8\u5bf9\u4e0a\u4e00\u5faa\u73af\u5feb\u7167"},
		{"metric": "\u6301\u4ed3\u5e02\u503c", "delta": stock - prevStock, "explain": "\u76f8\u5bf9\u4e0a\u4e00\u5faa\u73af\u5feb\u7167"},
		{"metric": "\u603b\u8d44\u4ea7", "delta": equity - prevEquity, "explain": "\u76f8\u5bf9\u4e0a\u4e00\u5faa\u73af\u5feb\u7167"},
	}
	posRows := []map[string]any{}
	pnlSum := 0.0
	for _, p0 := range positions {
		m := asMap(p0)
		if len(m) == 0 {
			continue
		}
		mv := asFloat(m["market_value"])
		shares := asFloat(firstNonEmptyNum(m["shares"], m["quantity"]))
		avg := asFloat(firstNonEmptyNum(m["avg_price"], m["cost_price"], m["avg_cost"]))
		price := asFloat(firstNonEmptyNum(m["price"], m["current_price"]))
		if mv == 0 && shares != 0 && price != 0 {
			mv = shares * price
		}
		pnl := asFloat(m["pnl"])
		if pnl == 0 && shares != 0 && avg != 0 && price != 0 {
			pnl = (price - avg) * shares
		}
		if pnl == 0 && shares != 0 && avg != 0 && mv != 0 {
			pnl = mv - avg*shares
		}
		pnlSum += pnl
		posRows = append(posRows, map[string]any{
			"code":         firstNonEmpty(asString(m["code"]), asString(m["symbol"]), fmt.Sprint(m["stock_id"])),
			"name":         firstNonEmpty(asString(m["name"]), asString(m["code"])),
			"market_value": mv, "pnl": pnl, "shares": shares,
		})
	}
	trades := s.st.RecentTrades(30)
	buyN, sellN := 0.0, 0.0
	events := []map[string]any{}
	for _, tr := range trades {
		side := strings.ToLower(asString(tr["side"]))
		shares := asFloat(tr["shares"])
		price := asFloat(tr["price"])
		notional := shares * price
		if side == "buy" {
			buyN += notional
		} else if side == "sell" {
			sellN += notional
		}
		if len(events) < 12 {
			events = append(events, map[string]any{"side": side, "code": tr["code"], "status": tr["status"], "shares": shares, "price": price, "notional": notional, "reason": tr["reason"]})
		}
	}
	llm := asMap(sanitize(s.st.GetStateMap("llm_usage")))
	moduleImpacts := []map[string]any{}
	if len(llm) > 0 {
		moduleImpacts = append(moduleImpacts, map[string]any{"module": "llm", "title": "LLM API \u6d88\u8017", "kind": "\u6210\u672c", "amount": llm["total_cost"], "detail": fmt.Sprintf("tokens=%v calls=%v", llm["total_tokens"], llm["call_count"])})
	}
	farm := asMap(sanitize(s.st.GetStateMap("farm")))
	if len(farm) > 0 {
		plots := asMap(farm["plots"])
		crop := cropNameCN(asString(farm["crop_key"]))
		detail := fmt.Sprintf("作物=%s 地块=空%g/种%g/可收%g", crop, asFloat(plots["empty"]), asFloat(plots["growing"]), asFloat(plots["ready"]))
		moduleImpacts = append(moduleImpacts, map[string]any{"module": "farm", "title": "农场", "kind": "日EV", "amount": farm["day_ev_12"], "detail": detail})
	}
	return map[string]any{
		"ts":                    firstNum(account["ts"], float64(time.Now().Unix())),
		"totals":                map[string]any{"cash": cash, "equity": equity, "stock_value": stock, "positions_pnl_sum": pnlSum},
		"identity":              map[string]any{"formula": "\u603b\u8d44\u4ea7 \u2248 \u73b0\u91d1 + \u6301\u4ed3\u5e02\u503c", "gap": gap},
		"delta_vs_prev":         delta,
		"composition":           composition,
		"position_contributors": posRows,
		"trade_cashflow":        map[string]any{"buy_notional": buyN, "sell_notional": sellN, "net": sellN - buyN},
		"module_impacts":        moduleImpacts,
		"changes":               changes,
		"recent_events":         events,
		"llm_usage":             llm,
	}
}

func (s *Server) equitySeries(limit int) []map[string]any {
	if limit <= 0 {
		limit = 120
	}
	snaps := s.st.RecentSnapshots("loop", limit)
	out := make([]map[string]any, 0, len(snaps))
	// RecentSnapshots is newest-first; reverse to chronological for chart.
	for i := len(snaps) - 1; i >= 0; i-- {
		sn := snaps[i]
		payload := asMap(sn["payload"])
		acc := asMap(payload["account"])
		if len(acc) == 0 {
			continue
		}
		equity := asFloat(acc["equity"])
		cash := asFloat(acc["cash"])
		stock := asFloat(acc["stock_value"])
		if stock == 0 {
			for _, p0 := range asSlice(acc["positions"]) {
				m := asMap(p0)
				mv := asFloat(m["market_value"])
				if mv == 0 {
					mv = asFloat(m["shares"]) * asFloat(firstNonEmptyNum(m["price"], m["current_price"], m["avg_price"]))
				}
				stock += mv
			}
		}
		if equity == 0 {
			equity = cash + stock
		}
		out = append(out, map[string]any{
			"ts":          firstNum(sn["ts"], float64(time.Now().Unix())),
			"equity":      equity,
			"cash":        cash,
			"stock_value": stock,
		})
	}
	return out
}

func cropNameCN(key string) string {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "lobster":
		return "龙虾藤"
	case "berry":
		return "玻璃草莓"
	case "carrot":
		return "甜胡萝卜"
	case "sprout":
		return "青芽菜"
	case "corn":
		return "黄金玉米"
	default:
		if key == "" {
			return "-"
		}
		return key
	}
}

func firstNonEmptyNum(xs ...any) any {
	for _, x := range xs {
		if asFloat(x) != 0 {
			return x
		}
	}
	if len(xs) > 0 {
		return xs[0]
	}
	return 0
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/index.html" {
			http.NotFound(w, r)
			return
		}
		page := strings.ReplaceAll(s.html, "%%REFRESH_SEC%%", strconv.Itoa(s.refresh))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(page))
	})
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true, "ts": time.Now().Unix(), "impl": "go"})
	})
	mux.HandleFunc("/api/overview", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, s.Overview())
	})
	mux.HandleFunc("/api/control", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			c := s.st.GetStateMap("control")
			if len(c) == 0 {
				c = map[string]any{"trade_mode": "auto", "capital_style": "prefer_hold"}
			}
			if strings.TrimSpace(fmt.Sprint(c["capital_style"])) == "" || fmt.Sprint(c["capital_style"]) == "<nil>" {
				c["capital_style"] = "prefer_hold"
			}
			writeJSON(w, 200, c)
			return
		}
		if r.Method == http.MethodPost {
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			cur := s.st.GetStateMap("control")
			mode := strings.ToLower(strings.TrimSpace(fmt.Sprint(payload["trade_mode"])))
			if mode == "" || mode == "<nil>" {
				mode = strings.ToLower(fmt.Sprint(cur["trade_mode"]))
			}
			if mode == "" || mode == "<nil>" {
				mode = "auto"
			}
			if mode != "auto" && mode != "sell_only" && mode != "paused" {
				writeJSON(w, 400, map[string]any{"ok": false, "error": "trade_mode must be auto/sell_only/paused"})
				return
			}
			style := strings.ToLower(strings.TrimSpace(fmt.Sprint(payload["capital_style"])))
			if style == "" || style == "<nil>" {
				style = strings.ToLower(fmt.Sprint(cur["capital_style"]))
			}
			switch style {
			case "cash", "prefer_cash":
				style = "prefer_cash"
			case "all", "all_in", "full":
				style = "all_in"
			case "hold", "prefer_hold", "", "<nil>":
				style = "prefer_hold"
			default:
				writeJSON(w, 400, map[string]any{"ok": false, "error": "capital_style must be prefer_cash/prefer_hold/all_in"})
				return
			}
			data := map[string]any{
				"trade_mode":    mode,
				"capital_style": style,
				"updated_at":    float64(time.Now().UnixNano()) / 1e9,
			}
			if err := s.st.SetState("control", data); err != nil {
				writeJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
				return
			}
			writeJSON(w, 200, map[string]any{"ok": true, "control": data})
			return
		}
		writeJSON(w, 405, map[string]any{"error": "method not allowed"})
	})
	mux.HandleFunc("/api/admin/auth/status", s.handleAdminAuthStatus)
	mux.HandleFunc("/api/admin/login", s.handleAdminLogin)
	mux.HandleFunc("/api/admin/logout", s.handleAdminLogout)
	mux.HandleFunc("/api/auth/cookies/status", s.handleCookieStatus)
	mux.HandleFunc("/api/auth/cookies/export", s.handleCookieExport)
	mux.HandleFunc("/api/auth/cookies/import", s.handleCookieImport)
	mux.HandleFunc("/api/auth/cookies/probe", s.handleCookieProbe)
	mux.HandleFunc("/api/auth/cookies/clear", s.handleCookieClear)
	mux.HandleFunc("/api/auth/cookies", s.handleCookieList)
	mux.HandleFunc("/api/feature-flags", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeJSON(w, 200, flags.Get(s.cfg, s.st))
			return
		}
		if r.Method == http.MethodPost {
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			id := strings.TrimSpace(fmt.Sprint(payload["id"]))
			if id == "" || id == "<nil>" {
				id = strings.TrimSpace(fmt.Sprint(payload["flag"]))
			}
			val := payload["value"]
			b := false
			switch t := val.(type) {
			case bool:
				b = t
			case string:
				b = t == "1" || strings.EqualFold(t, "true") || t == "on" || t == "开"
			case float64:
				b = t != 0
			default:
				writeJSON(w, 400, map[string]any{"ok": false, "error": "value required"})
				return
			}
			out, err := flags.Set(s.cfg, s.st, id, b)
			if err != nil {
				writeJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
				return
			}
			writeJSON(w, 200, map[string]any{"ok": true, "feature_flags": out})
			return
		}
		writeJSON(w, 405, map[string]any{"error": "method not allowed"})
	})
	return s.withAdminAPIGate(mux)
}

func writeJSON(w http.ResponseWriter, code int, payload any) {
	b, err := json.Marshal(payload)
	if err != nil {
		b, _ = json.Marshal(map[string]any{"error": err.Error(), "impl": "go"})
		code = 500
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_, _ = w.Write(b)
}

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok && m != nil {
		return m
	}
	return map[string]any{}
}
func asSlice(v any) []any {
	if s, ok := v.([]any); ok {
		return s
	}
	return []any{}
}
func asString(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}
func asFloat(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case float32:
		return float64(t)
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case json.Number:
		f, _ := t.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(t, 64)
		return f
	default:
		return 0
	}
}
func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if strings.TrimSpace(x) != "" && x != "<nil>" {
			return x
		}
	}
	return ""
}
func firstNum(v any, def float64) float64 {
	if v == nil {
		return def
	}
	f := asFloat(v)
	if f == 0 {
		return def
	}
	return f
}
func mapGet(m map[string]any, k string) any {
	if m == nil {
		return nil
	}
	return m[k]
}
func tailLog(path string, n int) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return []string{}
	}
	lines := strings.Split(string(b), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}
