package risk

import (
        "fmt"
        "math"
        "sort"
        "strings"
        "time"
)

type Position struct {
        StockID      int
        Code         string
        Name         string
        Shares       float64
        AvgPrice     float64
        OpenedAt     float64
        HighestPrice float64
}

type Decision struct {
        Allow  bool
        Shares float64
        Reason string
}

type Manager struct {
        Cfg       map[string]any
        Cooldowns map[int]float64
        Regime    map[string]any
        Peaks     map[int]float64
        Control   map[string]any
}

func New(cfg map[string]any) *Manager {
        return &Manager{Cfg: cfg, Cooldowns: map[int]float64{}, Regime: map[string]any{}, Peaks: map[int]float64{}, Control: map[string]any{}}
}

func (m *Manager) CfgF(k string, def float64) float64 {
        if m.Cfg == nil {
                return def
        }
        if v, ok := m.Cfg[k]; ok {
                switch t := v.(type) {
                case float64:
                        return t
                case int:
                        return float64(t)
                case int64:
                        return float64(t)
                }
        }
        return def
}
func (m *Manager) CfgI(k string, def int) int { return int(m.CfgF(k, float64(def))) }
func (m *Manager) CfgB(k string, def bool) bool {
        if m.Cfg == nil {
                return def
        }
        if v, ok := m.Cfg[k]; ok {
                if b, ok := v.(bool); ok {
                        return b
                }
        }
        return def
}

func (m *Manager) SetRegime(r map[string]any) {
        if r == nil {
                r = map[string]any{}
        }
        m.Regime = r
}
func (m *Manager) SetControl(c map[string]any) {
        if c == nil {
                c = map[string]any{}
        }
        m.Control = c
}
func (m *Manager) CapitalStyle() string {
        style := strings.ToLower(strings.TrimSpace(fmt.Sprint(m.Control["capital_style"])))
        switch style {
        case "cash", "prefer_cash":
                return "prefer_cash"
        case "all", "all_in", "full":
                return "all_in"
        case "hold", "prefer_hold", "", "<nil>":
                return "prefer_hold"
        default:
                return "prefer_hold"
        }
}
func (m *Manager) MarkTrade(stockID int) {
        m.Cooldowns[stockID] = float64(time.Now().Unix()) + m.CfgF("cooldown_sec", 45)
}

// MarkReduce uses a longer cooldown so risk_off 35% partial exits do not chain every cycle.
func (m *Manager) MarkReduce(stockID int) {
        sec := m.CfgF("reduce_cooldown_sec", 300)
        if sec < m.CfgF("cooldown_sec", 45) {
                sec = m.CfgF("cooldown_sec", 45)
        }
        m.Cooldowns[stockID] = float64(time.Now().Unix()) + sec
}

func (m *Manager) InCooldown(stockID int) bool {
        return float64(time.Now().Unix()) < m.Cooldowns[stockID]
}

// LoadCooldowns restores absolute until-unix map from storage.
func (m *Manager) LoadCooldowns(raw map[string]any) {
        if m.Cooldowns == nil {
                m.Cooldowns = map[int]float64{}
        }
        now := float64(time.Now().Unix())
        for k, v := range raw {
                var id int
                fmt.Sscanf(k, "%d", &id)
                if id <= 0 {
                        continue
                }
                until := asF(v)
                if until > now {
                        m.Cooldowns[id] = until
                }
        }
}

// ExportCooldowns returns active until-unix map for persistence.
func (m *Manager) ExportCooldowns() map[string]any {
        out := map[string]any{}
        now := float64(time.Now().Unix())
        for id, until := range m.Cooldowns {
                if until > now && id > 0 {
                        out[fmt.Sprint(id)] = until
                }
        }
        return out
}
func (m *Manager) UpdatePeak(stockID int, price, seed float64) float64 {
        cur, ok := m.Peaks[stockID]
        if !ok {
                if seed > 0 {
                        cur = seed
                } else {
                        cur = price
                }
        }
        if price > cur {
                cur = price
        }
        if seed > cur {
                cur = seed
        }
        m.Peaks[stockID] = cur
        return cur
}
func (m *Manager) ClearPeak(stockID int) { delete(m.Peaks, stockID) }

func (m *Manager) roiTable() [][2]float64 {
        // fallback ladder
        tp := m.CfgF("take_profit_pct", 0.22)
        if v := asF(m.Regime["take_profit_pct"]); v > 0 {
                tp = v
        }
        table := [][2]float64{
                {0, tp},
                {30, math.Max(tp*0.55, 0.05)},
                {90, math.Max(tp*0.30, 0.03)},
                {180, math.Max(tp*0.15, 0.015)},
        }
        sort.Slice(table, func(i, j int) bool { return table[i][0] > table[j][0] })
        return table
}

func (m *Manager) ROIHit(openedAt, avg, price float64) (bool, string) {
        if !m.CfgB("use_roi_ladder", true) || avg <= 0 || price <= 0 || openedAt <= 0 {
                return false, ""
        }
        heldMin := math.Max(0, (float64(time.Now().Unix())-openedAt)/60.0)
        pnl := (price - avg) / avg
        for _, row := range m.roiTable() {
                if heldMin >= row[0] && pnl >= row[1] {
                        return true, fmt.Sprintf("ROI达标 %.0fm>=%.0fm pnl=%.2f%%>=%.2f%%", heldMin, row[0], pnl*100, row[1]*100)
                }
        }
        return false, ""
}

func (m *Manager) SizeBuy(equity, cash, price float64, openPositions int, score float64, targetPct, tradeEV *float64) Decision {
        return m.SizeBuyForPosition(equity, cash, price, openPositions, false, score, targetPct, tradeEV)
}

// SizeBuyForPosition distinguishes a new holding from adding to an existing one.
// A full position count must not strand deployable cash by blocking valid adds.
func (m *Manager) SizeBuyForPosition(equity, cash, price float64, openPositions int, existing bool, score float64, targetPct, tradeEV *float64) Decision {
        if price <= 0 || equity <= 0 {
                return Decision{false, 0, "价格/权益无效"}
        }
        if asBool(m.Regime["force_sell_only"]) || m.Regime["allow_new_entries"] == false {
                return Decision{false, 0, fmt.Sprintf("行情%v:禁止开仓", m.Regime["name"])}
        }
        maxPos := m.CfgI("max_positions", 6)
        if v := int(asF(m.Regime["max_positions"])); v > 0 {
                maxPos = v
        }
        style := m.CapitalStyle()
        if style == "all_in" {
                if allInMax := m.CfgI("all_in_max_positions", maxPos); allInMax > maxPos {
                        maxPos = allInMax
                }
        }
        if !existing && openPositions >= maxPos {
                return Decision{false, 0, fmt.Sprintf("持仓已满(%d/%d)", openPositions, maxPos)}
        }
        reserve := m.CfgF("cash_reserve_pct", 0.12)
        if style == "prefer_cash" {
                if reserve < 0.28 {
                        reserve = 0.28
                }
        } else if style == "all_in" {
                if reserve > 0.02 {
                        reserve = 0.02
                }
        } else if style == "prefer_hold" {
                if reserve < 0.08 {
                        reserve = 0.08
                }
                if reserve > 0.12 {
                        reserve = 0.12
                }
        }
        switch fmt.Sprint(m.Regime["name"]) {
        case "crash":
                if reserve < 0.30 {
                        reserve = 0.30
                }
        case "risk_off":
                if reserve < 0.20 {
                        reserve = 0.20
                }
        }
        spendable := math.Max(cash-equity*reserve, 0)
        if spendable <= 0 {
                return Decision{false, 0, "可用现金不足(保留金)"}
        }
        basePct := m.CfgF("position_pct", 0.12)
        maxPct := m.CfgF("max_position_pct", 0.20)
        scale := asF(m.Regime["position_scale"])
        if scale <= 0 {
                scale = 1
        }
        if style == "prefer_cash" {
                scale *= 0.65
                if maxPct > 0.14 {
                        maxPct = 0.14
                }
        } else if style == "all_in" {
                scale *= 1.25
                if maxPct < 0.28 {
                        maxPct = 0.28
                }
        } else if style == "prefer_hold" {
                scale *= 1.05
        }
        var pct float64
        if targetPct != nil {
                pct = math.Min(maxPct, math.Max(0, *targetPct)) * scale
        } else {
                pct = math.Min(maxPct, basePct*(0.90+math.Abs(score)*0.95)) * scale
        }
        if tradeEV != nil && *tradeEV <= 0 {
                return Decision{false, 0, fmt.Sprintf("EV<=0(%.4f)", *tradeEV)}
        }
        if pct <= 0 {
                return Decision{false, 0, "仓位目标<=0"}
        }
        budget := math.Min(equity*pct, spendable)
        maxN := m.CfgF("max_notional_per_order", 0)
        if style == "all_in" {
                if pctCap := m.CfgF("all_in_max_notional_pct", 0.08) * equity; pctCap > maxN {
                        maxN = pctCap
                }
                if hardCap := m.CfgF("all_in_max_notional_per_order", 0); hardCap > 0 && maxN > hardCap {
                        maxN = hardCap
                }
        }
        if maxN > 0 {
                budget = math.Min(budget, maxN)
        }
        shares := budget / price
        minShares := m.CfgF("min_shares", 1)
        maxShares := m.CfgF("max_shares_per_order", 5000000)
        if shares < minShares {
                if spendable >= price*minShares && cash >= price*minShares {
                        shares = minShares
                } else {
                        return Decision{false, 0, "现金不足以买最小股数"}
                }
        }
        shares = math.Min(shares, maxShares)
        shares = math.Floor(shares)
        if shares <= 0 {
                return Decision{false, 0, "股数=0"}
        }
        evTxt := ""
        if tradeEV != nil {
                evTxt = fmt.Sprintf(" EV=%.3f%%", (*tradeEV)*100)
        }
        return Decision{true, shares, fmt.Sprintf("预算=%.2f 仓位=%.2f%% 模式=%v%s", budget, pct*100, firstNonEmpty(fmt.Sprint(m.Regime["name"]), "neutral"), evTxt)}
}

func (m *Manager) ShouldStop(pos Position, price, peak float64) (bool, string) {
        if pos.AvgPrice <= 0 || price <= 0 {
                return false, ""
        }
        pnl := (price - pos.AvgPrice) / pos.AvgPrice
        highest := peak
        if price > highest {
                highest = price
        }
        sl := m.CfgF("stop_loss_pct", 0.08)
        if v := asF(m.Regime["stop_loss_pct"]); v > 0 {
                sl = v
        }
        tp := m.CfgF("take_profit_pct", 0.22)
        if v := asF(m.Regime["take_profit_pct"]); v > 0 {
                tp = v
        }
        trail := m.CfgF("trailing_stop_pct", 0.10)
        if v := asF(m.Regime["trailing_stop_pct"]); v > 0 {
                trail = v
        }
        trailOffset := m.CfgF("trailing_stop_positive_offset", 0.04)
        if pnl <= -sl {
                return true, fmt.Sprintf("止损 %.2f%%", pnl*100)
        }
        if hit, why := m.ROIHit(pos.OpenedAt, pos.AvgPrice, price); hit {
                return true, why
        }
        if pnl >= tp {
                return true, fmt.Sprintf("止盈 %.2f%%", pnl*100)
        }
        if highest > pos.AvgPrice*(1+trailOffset) {
                dd := (price - highest) / math.Max(highest, 1e-9)
                if dd <= -trail {
                        return true, fmt.Sprintf("移动止盈 %.2f%%", dd*100)
                }
        }
        return false, ""
}

func (m *Manager) AllowAdd(avg, price float64) (bool, string) {
        if asBool(m.Regime["force_sell_only"]) || m.Regime["allow_new_entries"] == false {
                return false, fmt.Sprintf("行情%v:禁止加仓", m.Regime["name"])
        }
        if avg <= 0 || price <= 0 {
                return true, ""
        }
        pnl := (price - avg) / avg
        limit := m.CfgF("no_add_loss_pct", 0.04)
        if pnl <= -limit {
                return false, fmt.Sprintf("浮亏禁止加仓(%.2f%%)", pnl*100)
        }
        return true, ""
}

func (m *Manager) ReduceFraction() float64 {
        v := asF(m.Regime["reduce_fraction"])
        if v < 0 {
                return 0
        }
        if v > 1 {
                return 1
        }
        return v
}

func asF(v any) float64 {
        switch t := v.(type) {
        case float64:
                return t
        case int:
                return float64(t)
        case int64:
                return float64(t)
        default:
                return 0
        }
}
func asBool(v any) bool {
        switch t := v.(type) {
        case bool:
                return t
        case float64:
                return t != 0
        default:
                return false
        }
}
func firstNonEmpty(a, b string) string {
        if a != "" && a != "<nil>" {
                return a
        }
        return b
}
