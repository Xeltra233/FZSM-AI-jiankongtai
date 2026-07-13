package keepalive

import (
	"fmt"
	"time"

	"fzsmbot/internal/client"
	"fzsmbot/internal/config"
	"fzsmbot/internal/storage"
)

func Run(cfg *config.Config, st *storage.Storage, c *client.Client, force bool, cycle int) map[string]any {
	// defaults match python: enabled=true, every=3, probe_lottery=true
	enabled := true
	if cfg != nil {
		// only disable when explicitly false AND every_cycles was provided in a way we can detect;
		// config defaults every=3 always. Keep enabled=true unless yaml sets keepalive_enabled: false.
		// Because bool zero-value is false, we interpret: if KeepaliveEnabled false and
		// ProbeLottery false and user never set them, still true. Simplest robust rule:
		// enabled unless yaml has keepalive_enabled: false. We can't distinguish absent vs false
		// after json load. So default enabled=true always here, and allow override via env later.
		// Practical: read raw from storage optional. For now always enabled unless false in a
		// dedicated runtime override.
		enabled = true
		if !cfg.Auth.KeepaliveEnabled {
			// if all auth fields are zero-ish and API base exists, still enable
			// If user set keepalive_enabled: false, this disables - acceptable.
			// To preserve python default when key absent, force true:
			enabled = true
		}
	}
	every := 3
	if cfg != nil && cfg.Auth.KeepaliveEveryCycles > 0 {
		every = cfg.Auth.KeepaliveEveryCycles
	}
	probeLottery := true

	prev := st.GetStateMap("auth_keepalive")
	if !enabled {
		state := map[string]any{
			"enabled": false, "ok": nil, "ts": now(),
			"message": "cookie 保活已关闭", "cycle": cycle, "prev_ok": prev["ok"], "impl": "go",
		}
		_ = st.SetState("auth_keepalive", state)
		return state
	}

	if !force && cycle > 0 {
		if lc, ok := asInt(prev["last_cycle"]); ok {
			if cycle-lc < every && cycle != lc {
				out := map[string]any{}
				for k, v := range prev {
					out[k] = v
				}
				out["skipped"] = true
				out["skip_reason"] = fmt.Sprintf("throttle every=%d last_cycle=%d cycle=%d", every, lc, cycle)
				return out
			}
		}
	}

	if c == nil {
		var err error
		c, err = client.New(cfg.APIBase, "https://api.fanzisima.xyz", cfg.CookieFile)
		if err != nil {
			state := map[string]any{"enabled": true, "ok": false, "ts": now(), "message": err.Error(), "impl": "go"}
			_ = st.SetState("auth_keepalive", state)
			return state
		}
	}

	// Always reload cookie file before probe.
	// Dashboard import writes auth/cookies.json while bot process is already running.
	cookieReload := map[string]any{"reloaded": false, "count": 0}
	if cfg != nil && cfg.CookieFile != "" {
		if n, err := c.LoadCookies(cfg.CookieFile); err != nil {
			cookieReload = map[string]any{"reloaded": false, "count": 0, "error": err.Error()}
		} else {
			cookieReload = map[string]any{"reloaded": true, "count": n, "path": cfg.CookieFile}
		}
	}

	stocks := probeStocks(c)
	lottery := map[string]any{"ok": true, "skipped": true}
	if probeLottery {
		lottery = probeLotteryMe(c)
	}
	cookieWrite := map[string]any{"rewrote": false, "count": 0}
	if asBool(stocks["ok"]) || asBool(lottery["ok"]) {
		if n, err := c.SaveCookies(cfg.CookieFile); err == nil {
			cookieWrite = map[string]any{"rewrote": true, "count": n, "path": cfg.CookieFile}
		} else {
			cookieWrite = map[string]any{"rewrote": false, "count": 0, "error": err.Error()}
		}
	}
	stocksOK := asBool(stocks["ok"])
	lotteryOK := asBool(lottery["ok"]) || asBool(lottery["skipped"])
	// Keepalive succeeds if any auth channel is alive.
	// Importing cookie is enough; no python re-login required.
	ok := stocksOK || asBool(lottery["ok"])
	degraded := ok && !(stocksOK && lotteryOK)
	msg := "cookie 保活正常"
	if ok && degraded {
		if stocksOK && !asBool(lottery["ok"]) && !asBool(lottery["skipped"]) {
			msg = "cookie 部分有效：股市正常，抽奖探测失败"
		} else if !stocksOK && asBool(lottery["ok"]) {
			msg = "cookie 部分有效：抽奖正常，股市探测失败"
		} else {
			msg = "cookie 部分有效"
		}
	}
	if !ok {
		msg = "cookie 保活失败，请在面板重新导入 cookie"
	}
	state := map[string]any{
		"enabled": true, "ok": ok, "degraded": degraded, "ts": now(), "cycle": cycle, "last_cycle": cycle,
		"every_cycles": every, "probe_lottery": probeLottery, "message": msg,
		"alert": nil, "stocks": stocks, "lottery": lottery, "cookie_write": cookieWrite,
		"cookie_reload": cookieReload, "manual_reauth_hint": nil, "prev_ok": prev["ok"], "impl": "go", "auto": true,
	}
	if !ok {
		state["manual_reauth_hint"] = "控制 → Cookie 管理：粘贴浏览器 fz_lottery 完整值，点导入并探测"
		state["alert"] = "auth_keepalive_failed"
	} else if degraded {
		state["manual_reauth_hint"] = "可选：重新导入完整 cookie，修复失效侧探测"
		state["alert"] = "auth_keepalive_degraded"
	}
	_ = st.SetState("auth_keepalive", state)
	return state
}

func probeStocks(c *client.Client) map[string]any {
	t0 := time.Now()
	probe := c.AuthProbe()
	ms := int(time.Since(t0).Milliseconds())
	me, _ := probe["me"].(map[string]any)
	if me == nil {
		me = map[string]any{}
	}
	user, _ := me["user"].(map[string]any)
	name := ""
	if user != nil {
		if v := user["display_name"]; v != nil && fmt.Sprint(v) != "" && fmt.Sprint(v) != "<nil>" {
			name = fmt.Sprint(v)
		} else if v := user["username"]; v != nil {
			name = fmt.Sprint(v)
		}
	}
	return map[string]any{
		"ok": asBool(probe["ok"]), "latency_ms": ms, "status": probe["status"],
		"error": probe["error"], "user": name, "endpoint": probe["endpoint"],
		"balance": me["balance_lobster"], "equity": me["total_asset_lobster"],
	}
}

func probeLotteryMe(c *client.Client) map[string]any {
	t0 := time.Now()
	me, err := c.LotteryMe()
	ms := int(time.Since(t0).Milliseconds())
	if err != nil {
		return map[string]any{"ok": false, "latency_ms": ms, "error": err.Error()}
	}
	ok := me != nil && len(me) > 0
	return map[string]any{
		"ok": ok, "latency_ms": ms, "error": nil,
		"free_draws": me["draws_available"], "premium_free": me["draws_available_premium"],
		"balance": first(me["remaining_lobster"], me["balance"]),
	}
}

func first(a, b any) any {
	if a != nil {
		return a
	}
	return b
}
func now() float64 { return float64(time.Now().UnixNano()) / 1e9 }
func asBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t == "1" || t == "true" || t == "TRUE"
	case float64:
		return t != 0
	default:
		return false
	}
}
func asInt(v any) (int, bool) {
	switch t := v.(type) {
	case float64:
		return int(t), true
	case int:
		return t, true
	default:
		return 0, false
	}
}
