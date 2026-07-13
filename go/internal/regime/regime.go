package regime

import (
        "fmt"
        "math"
)

type Engine struct {
        Cfg     map[string]any
        RiskCfg map[string]any
}

func New(cfg, risk map[string]any) *Engine {
        if cfg == nil {
                cfg = map[string]any{}
        }
        if risk == nil {
                risk = map[string]any{}
        }
        return &Engine{Cfg: cfg, RiskCfg: risk}
}

func asF(v any, def float64) float64 {
        switch t := v.(type) {
        case float64:
                return t
        case float32:
                return float64(t)
        case int:
                return float64(t)
        case int64:
                return float64(t)
        case bool:
                if t {
                        return 1
                }
                return 0
        default:
                return def
        }
}

func asB(v any, def bool) bool {
        switch t := v.(type) {
        case bool:
                return t
        case float64:
                return t != 0
        case int:
                return t != 0
        default:
                return def
        }
}

func asI(v any, def int) int { return int(asF(v, float64(def))) }

func (e *Engine) Enabled() bool { return asB(e.Cfg["enabled"], true) }

func (e *Engine) Detect(market map[string]any) map[string]any {
        if market == nil {
                market = map[string]any{}
        }
        index, _ := market["index"].(map[string]any)
        if index == nil {
                index = map[string]any{}
        }
        stocks, _ := market["stocks"].([]any)
        indexChg := asF(index["change_pct"], 0)
        changes := []float64{}
        down, up := 0, 0
        for _, s0 := range stocks {
                s, _ := s0.(map[string]any)
                if s == nil {
                        continue
                }
                chg := asF(s["change_pct"], 0)
                changes = append(changes, chg)
                if chg < -1e-12 {
                        down++
                } else if chg > 1e-12 {
                        up++
                }
        }
        total := len(changes)
        if total < 1 {
                total = 1
        }
        breadthDown := float64(down) / float64(total)
        avgAbs := math.Abs(indexChg)
        if len(changes) > 0 {
                sum := 0.0
                for _, x := range changes {
                        sum += math.Abs(x)
                }
                avgAbs = sum / float64(len(changes))
        }
        if !e.Enabled() {
                return map[string]any{
                        "name": "neutral", "score": 0.0, "index_change_pct": indexChg, "breadth_down": breadthDown,
                        "avg_abs_change": avgAbs, "down_count": down, "up_count": up, "total": len(changes),
                        "reasons": []any{"regime disabled"}, "allow_new_entries": true, "force_sell_only": false,
                        "position_scale": 1.0, "enter_score_boost": 0.0, "reduce_fraction": 0.0,
                }
        }
        crashIdx := asF(e.Cfg["crash_index_pct"], -0.08)
        riskOffIdx := asF(e.Cfg["risk_off_index_pct"], -0.035)
        riskOnIdx := asF(e.Cfg["risk_on_index_pct"], 0.02)
        breadthCrash := asF(e.Cfg["breadth_crash"], 0.72)
        breadthRiskOff := asF(e.Cfg["breadth_risk_off"], 0.62)
        shockAbs := asF(e.Cfg["shock_avg_abs_change"], 0.09)
        reasons := []any{
                fmt.Sprintf("index=%.2f%%", indexChg*100),
                fmt.Sprintf("down_ratio=%.0f%%", breadthDown*100),
                fmt.Sprintf("avg_move=%.2f%%", avgAbs*100),
        }
        name := "neutral"
        score := 0.0
        if indexChg <= crashIdx || breadthDown >= breadthCrash || (indexChg <= riskOffIdx && avgAbs >= shockAbs) {
                name = "crash"
                score = -1.0
                if indexChg <= crashIdx {
                        reasons = append(reasons, "index_crash")
                }
                if breadthDown >= breadthCrash {
                        reasons = append(reasons, "broad_selloff")
                }
                if avgAbs >= shockAbs {
                        reasons = append(reasons, "volatility_shock")
                }
        } else if indexChg <= riskOffIdx || breadthDown >= breadthRiskOff {
                name = "risk_off"
                score = -0.55
                reasons = append(reasons, "risk_off")
        } else if indexChg >= riskOnIdx && breadthDown <= 0.42 {
                name = "risk_on"
                score = 0.45
                reasons = append(reasons, "risk_on")
        } else {
                reasons = append(reasons, "normal")
        }
        out := map[string]any{
                "name": name, "score": score, "index_change_pct": indexChg, "breadth_down": breadthDown,
                "avg_abs_change": avgAbs, "down_count": down, "up_count": up, "total": len(changes), "reasons": reasons,
        }
        return e.applyPolicy(out)
}

func (e *Engine) applyPolicy(regime map[string]any) map[string]any {
        baseMaxPos := asI(e.RiskCfg["max_positions"], 6)
        baseNew := asI(e.RiskCfg["max_new_entries_per_cycle"], 1)
        baseSL := asF(e.RiskCfg["stop_loss_pct"], 0.08)
        baseTP := asF(e.RiskCfg["take_profit_pct"], 0.22)
        baseTrail := asF(e.RiskCfg["trailing_stop_pct"], 0.10)
        name := fmt.Sprint(regime["name"])
        reasons, _ := regime["reasons"].([]any)
        switch name {
        case "crash":
                regime["allow_new_entries"] = false
                regime["force_sell_only"] = true
                maxPos := asI(e.Cfg["crash_max_positions"], min(2, baseMaxPos))
                if maxPos < 1 {
                        maxPos = 1
                }
                regime["max_positions"] = maxPos
                regime["max_new_entries_per_cycle"] = 0
                regime["position_scale"] = asF(e.Cfg["crash_position_scale"], 0.0)
                regime["enter_score_boost"] = asF(e.Cfg["crash_enter_boost"], 0.30)
                regime["stop_loss_pct"] = asF(e.Cfg["crash_stop_loss_pct"], math.Min(baseSL, 0.05))
                regime["take_profit_pct"] = asF(e.Cfg["crash_take_profit_pct"], math.Min(baseTP, 0.10))
                regime["trailing_stop_pct"] = asF(e.Cfg["crash_trailing_stop_pct"], math.Min(baseTrail, 0.06))
                regime["reduce_fraction"] = asF(e.Cfg["crash_reduce_fraction"], 0.50)
                reasons = append(reasons, "freeze_entries+tighten_stops+force_reduce")
        case "risk_off":
                allow := asB(e.Cfg["risk_off_allow_entries"], false)
                regime["allow_new_entries"] = allow
                regime["force_sell_only"] = !allow
                maxPos := asI(e.Cfg["risk_off_max_positions"], max(2, baseMaxPos/2))
                if maxPos < 1 {
                        maxPos = 1
                }
                regime["max_positions"] = maxPos
                defNew := 0
                if allow {
                        defNew = min(1, baseNew)
                }
                regime["max_new_entries_per_cycle"] = asI(e.Cfg["risk_off_max_new_entries"], defNew)
                regime["position_scale"] = asF(e.Cfg["risk_off_position_scale"], 0.55)
                regime["enter_score_boost"] = asF(e.Cfg["risk_off_enter_boost"], 0.12)
                regime["stop_loss_pct"] = asF(e.Cfg["risk_off_stop_loss_pct"], math.Min(baseSL, 0.06))
                regime["take_profit_pct"] = asF(e.Cfg["risk_off_take_profit_pct"], math.Min(baseTP, 0.14))
                regime["trailing_stop_pct"] = asF(e.Cfg["risk_off_trailing_stop_pct"], math.Min(baseTrail, 0.08))
                regime["reduce_fraction"] = asF(e.Cfg["risk_off_reduce_fraction"], 0.35)
                reasons = append(reasons, "cut_size+higher_entry_bar+prefer_exits")
        case "risk_on":
                regime["allow_new_entries"] = true
                regime["force_sell_only"] = false
                regime["max_positions"] = asI(e.Cfg["risk_on_max_positions"], baseMaxPos)
                regime["max_new_entries_per_cycle"] = asI(e.Cfg["risk_on_max_new_entries"], baseNew)
                regime["position_scale"] = asF(e.Cfg["risk_on_position_scale"], 1.0)
                regime["enter_score_boost"] = asF(e.Cfg["risk_on_enter_boost"], -0.02)
                regime["stop_loss_pct"] = baseSL
                regime["take_profit_pct"] = baseTP
                regime["trailing_stop_pct"] = baseTrail
                regime["reduce_fraction"] = 0.0
                reasons = append(reasons, "balanced_risk_on")
        default:
                regime["allow_new_entries"] = true
                regime["force_sell_only"] = false
                regime["max_positions"] = baseMaxPos
                regime["max_new_entries_per_cycle"] = baseNew
                regime["position_scale"] = 1.0
                regime["enter_score_boost"] = 0.0
                regime["stop_loss_pct"] = baseSL
                regime["take_profit_pct"] = baseTP
                regime["trailing_stop_pct"] = baseTrail
                regime["reduce_fraction"] = 0.0
        }
        regime["reasons"] = reasons
        return regime
}

func min(a, b int) int {
        if a < b {
                return a
        }
        return b
}
func max(a, b int) int {
        if a > b {
                return a
        }
        return b
}
