package trader

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

const bankruptcyStateKey = "risk.bankruptcy_cooldown"

var (
	reBankruptcyHourMin = regexp.MustCompile("\u7834\u4ea7\u51b7\u5374\u4e2d[\uff0c,]?\\s*\u8fd8\u6709\\s*(\\d+)\\s*\u5c0f\u65f6\\s*(\\d+)\\s*\u5206\u949f")
	reBankruptcyHour    = regexp.MustCompile("\u7834\u4ea7\u51b7\u5374\u4e2d[\uff0c,]?\\s*\u8fd8\u6709\\s*(\\d+)\\s*\u5c0f\u65f6")
	reBankruptcyMin     = regexp.MustCompile("\u7834\u4ea7\u51b7\u5374\u4e2d[\uff0c,]?\\s*\u8fd8\u6709\\s*(\\d+)\\s*\u5206\u949f")
	reBankruptcyAny     = regexp.MustCompile("\u7834\u4ea7\u51b7\u5374")
)

// BankruptcyBlocked returns true when live stock trading should wait until cooldown ends.
func (t *Trader) BankruptcyBlocked() (bool, string, time.Time) {
	if t == nil || t.Storage == nil || t.Mode != "live" {
		return false, "", time.Time{}
	}
	st := t.Storage.GetStateMap(bankruptcyStateKey)
	until := asF(st["until_unix"])
	if until <= 0 {
		// bootstrap from recent trade errors so restart does not hammer buy/sell
		t.bootstrapBankruptcyFromTrades()
		st = t.Storage.GetStateMap(bankruptcyStateKey)
		until = asF(st["until_unix"])
		if until <= 0 {
			return false, "", time.Time{}
		}
	}
	untilT := time.Unix(int64(until), 0)
	now := time.Now()
	if now.Before(untilT) {
		remain := untilT.Sub(now).Round(time.Minute)
		msg := fmt.Sprintf("\u7834\u4ea7\u51b7\u5374\u4e2d\uff0c\u7ea6 %s \u540e\u6062\u590d\u4ea4\u6613", formatDurationCN(remain))
		if s := strings.TrimSpace(fmt.Sprint(st["message"])); s != "" && s != "<nil>" {
			msg = s
		}
		return true, msg, untilT
	}
	// expired - clear sticky state
	_ = t.Storage.SetState(bankruptcyStateKey, map[string]any{
		"active":     false,
		"until_unix": 0,
		"cleared_at": float64(now.Unix()),
		"message":    "\u7834\u4ea7\u51b7\u5374\u5df2\u7ed3\u675f\uff0c\u6062\u590d\u4ea4\u6613",
	})
	return false, "", untilT
}

// BankruptcyStatus exposes cooldown state for dashboard/overview.
func BankruptcyStatus(st interface{ GetStateMap(string) map[string]any }) map[string]any {
	if st == nil {
		return map[string]any{"active": false}
	}
	m := st.GetStateMap(bankruptcyStateKey)
	until := asF(m["until_unix"])
	if until <= 0 {
		return map[string]any{"active": false, "until_unix": 0, "message": ""}
	}
	untilT := time.Unix(int64(until), 0)
	now := time.Now()
	if !now.Before(untilT) {
		return map[string]any{"active": false, "until_unix": until, "message": "\u7834\u4ea7\u51b7\u5374\u5df2\u7ed3\u675f", "until": untilT.Format(time.RFC3339)}
	}
	msg := strings.TrimSpace(fmt.Sprint(m["message"]))
	if msg == "" || msg == "<nil>" {
		msg = fmt.Sprintf("\u7834\u4ea7\u51b7\u5374\u4e2d\uff0c\u7ea6 %s \u540e\u6062\u590d\u4ea4\u6613", formatDurationCN(untilT.Sub(now).Round(time.Minute)))
	}
	return map[string]any{
		"active":       true,
		"until_unix":   until,
		"until":        untilT.Format(time.RFC3339),
		"remain_sec":   int(untilT.Sub(now).Seconds()),
		"message":      msg,
		"source":       m["source"],
		"updated_at":   m["updated_at"],
	}
}

func (t *Trader) rememberBankruptcyData(raw any) {
	if t == nil || t.Storage == nil {
		return
	}
	text := fmt.Sprint(raw)
	if !reBankruptcyAny.MatchString(text) {
		return
	}
	until, msg := parseBankruptcyUntil(text, time.Now())
	if until.IsZero() {
		// unknown remaining time: block for 30 minutes to avoid hammering, refresh on next error
		until = time.Now().Add(30 * time.Minute)
		msg = "\u7834\u4ea7\u51b7\u5374\u4e2d\uff08\u5269\u4f59\u65f6\u95f4\u672a\u77e5\uff0c\u4e34\u65f6\u62e6\u622a 30 \u5206\u949f\uff09"
	}
	_ = t.Storage.SetState(bankruptcyStateKey, map[string]any{
		"active":     true,
		"until_unix": float64(until.Unix()),
		"message":    msg,
		"updated_at": float64(time.Now().Unix()),
		"source":     "trade_error",
	})
}

func (t *Trader) bootstrapBankruptcyFromTrades() {
	if t == nil || t.Storage == nil {
		return
	}
	for _, tr := range t.Storage.RecentTrades(40) {
		blob := strings.TrimSpace(fmt.Sprint(tr["reason"])) + " " + strings.TrimSpace(fmt.Sprint(tr["raw"]))
		if !reBankruptcyAny.MatchString(blob) {
			continue
		}
		// Reconstruct absolute until from historical trade timestamp + remaining text.
		base := time.Now()
		if ts := asF(tr["ts"]); ts > 0 {
			if ts > 1e12 {
				ts = ts / 1000
			}
			base = time.Unix(int64(ts), 0)
		}
		until, msg := parseBankruptcyUntil(blob, base)
		if until.IsZero() {
			continue
		}
		if time.Now().After(until) {
			// already expired
			_ = t.Storage.SetState(bankruptcyStateKey, map[string]any{
				"active":     false,
				"until_unix": 0,
				"message":    "破产冷却已结束，恢复交易",
				"cleared_at": float64(time.Now().Unix()),
				"source":     "bootstrap_expired",
			})
			return
		}
		_ = t.Storage.SetState(bankruptcyStateKey, map[string]any{
			"active":     true,
			"until_unix": float64(until.Unix()),
			"message":    msg,
			"updated_at": float64(time.Now().Unix()),
			"source":     "bootstrap_trades",
		})
		return
	}
}

func parseBankruptcyUntil(text string, now time.Time) (time.Time, string) {
	text = strings.TrimSpace(text)
	if m := reBankruptcyHourMin.FindStringSubmatch(text); len(m) == 3 {
		h := atoiLocal(m[1])
		min := atoiLocal(m[2])
		d := time.Duration(h)*time.Hour + time.Duration(min)*time.Minute
		// add 30s buffer so we don't fire slightly early
		d += 30 * time.Second
		until := now.Add(d)
		return until, fmt.Sprintf("\u7834\u4ea7\u51b7\u5374\u4e2d\uff0c\u8fd8\u6709 %d \u5c0f\u65f6 %d \u5206\u949f\u624d\u80fd\u4ea4\u6613", h, min)
	}
	if m := reBankruptcyHour.FindStringSubmatch(text); len(m) == 2 {
		h := atoiLocal(m[1])
		d := time.Duration(h)*time.Hour + 30*time.Second
		until := now.Add(d)
		return until, fmt.Sprintf("\u7834\u4ea7\u51b7\u5374\u4e2d\uff0c\u8fd8\u6709 %d \u5c0f\u65f6\u624d\u80fd\u4ea4\u6613", h)
	}
	if m := reBankruptcyMin.FindStringSubmatch(text); len(m) == 2 {
		min := atoiLocal(m[1])
		d := time.Duration(min)*time.Minute + 30*time.Second
		until := now.Add(d)
		return until, fmt.Sprintf("\u7834\u4ea7\u51b7\u5374\u4e2d\uff0c\u8fd8\u6709 %d \u5206\u949f\u624d\u80fd\u4ea4\u6613", min)
	}
	if reBankruptcyAny.MatchString(text) {
		return time.Time{}, strings.TrimSpace(text)
	}
	return time.Time{}, ""
}

func formatDurationCN(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf("%d\u5c0f\u65f6%d\u5206\u949f", h, m)
	}
	if m <= 0 {
		return "\u4e0d\u52301\u5206\u949f"
	}
	return fmt.Sprintf("%d\u5206\u949f", m)
}

func atoiLocal(s string) int {
	n := 0
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			break
		}
		n = n*10 + int(ch-'0')
	}
	return n
}
