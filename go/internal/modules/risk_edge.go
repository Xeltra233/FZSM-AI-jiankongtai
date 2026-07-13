package modules

import (
	"fmt"
	"time"

	"fzsmbot/internal/storage"
)

// Generic high-risk edge tracking (same Plan-B style as slot):
// analyze first, auto-execute only when edge_ok.

func loadRiskEdge(st *storage.Storage, key string) map[string]any {
	if st == nil || key == "" {
		return map[string]any{}
	}
	return st.GetStateMap(key)
}

func saveRiskEdge(st *storage.Storage, key string, edge map[string]any) {
	if st == nil || key == "" || edge == nil {
		return
	}
	_ = st.SetState(key, edge)
}

func riskNum(m map[string]any, key string, def float64) float64 {
	if m == nil {
		return def
	}
	if v, ok := m[key]; ok {
		x := asFloat(v)
		// allow explicit zero
		return x
	}
	return def
}

func buildRiskEdge(kind string, theoryOK bool, theoryRTP, theoryEV, minRTP, minEV float64, samples int, sumDelta float64, wins int, extra map[string]any) map[string]any {
	obsRTP := 0.0
	obsEV := 0.0
	if samples > 0 {
		obsEV = sumDelta / float64(samples)
		// if we know unit stake in extra.bet, derive obs rtp
		bet := riskNum(extra, "bet", 0)
		if bet > 0 {
			obsRTP = 1.0 + obsEV/bet
		}
	}
	useRTP := theoryRTP
	useEV := theoryEV
	source := "theory"
	minSamples := int(riskNum(extra, "min_samples", 30))
	if minSamples < 1 {
		minSamples = 30
	}
	if samples >= minSamples {
		useRTP = 0.7*theoryRTP + 0.3*obsRTP
		useEV = 0.7*theoryEV + 0.3*obsEV
		source = "theory+observed"
	}
	edgeOK := theoryOK && useRTP >= minRTP && useEV >= minEV
	// hard block on negative theory
	if theoryOK && (theoryRTP < minRTP || theoryEV < minEV) {
		edgeOK = false
	}
	edge := map[string]any{
		"ts":            float64(time.Now().UnixNano()) / 1e9,
		"kind":          kind,
		"theory_ok":     theoryOK,
		"theory_rtp":    theoryRTP,
		"theory_ev":     theoryEV,
		"samples":       samples,
		"wins":          wins,
		"sum_delta":     sumDelta,
		"obs_rtp":       obsRTP,
		"obs_ev":        obsEV,
		"min_rtp":       minRTP,
		"min_ev":        minEV,
		"min_samples":   minSamples,
		"sample_target": minSamples,
		"use_rtp":       useRTP,
		"use_ev":        useEV,
		"source":        source,
		"edge_ok":       edgeOK,
	}
	for k, v := range extra {
		if _, exists := edge[k]; !exists {
			edge[k] = v
		}
	}
	if samples > 0 {
		edge["win_rate"] = float64(wins) / float64(samples)
	} else {
		edge["win_rate"] = 0.0
	}
	if !theoryOK {
		edge["gate"] = "need_config"
		edge["probe_status"] = "blocked"
		edge["message"] = kind + "：缺少可分析配置/行情"
	} else if theoryRTP < minRTP || theoryEV < minEV {
		edge["gate"] = "theory_negative"
		edge["probe_status"] = "blocked"
		edge["message"] = fmt.Sprintf("%s：理论负期望 RTP=%.2f%% EV=%.0f，自动执行保持关闭", kind, theoryRTP*100, theoryEV)
	} else if !edgeOK {
		edge["gate"] = "no_positive_edge"
		edge["probe_status"] = "collecting"
		edge["message"] = fmt.Sprintf("%s：未达正EV门槛 RTP=%.2f%% EV=%.0f", kind, useRTP*100, useEV)
	} else {
		edge["gate"] = "ready"
		edge["probe_status"] = "ready"
		edge["message"] = fmt.Sprintf("%s：门槛通过 RTP=%.2f%% EV=%.0f，样本 %d/%d", kind, useRTP*100, useEV, samples, minSamples)
	}
	return edge
}

func recordRiskSample(st *storage.Storage, key string, delta float64, win bool) map[string]any {
	edge := loadRiskEdge(st, key)
	samples := int(asFloat(edge["samples"])) + 1
	sumDelta := asFloat(edge["sum_delta"]) + delta
	wins := int(asFloat(edge["wins"]))
	if win {
		wins++
	}
	edge["samples"] = samples
	edge["sum_delta"] = sumDelta
	edge["wins"] = wins
	edge["last_delta"] = delta
	ts := float64(time.Now().UnixNano()) / 1e9
	edge["last_ts"] = ts
	item := map[string]any{"ts": ts, "delta": delta, "win": win}
	hist := []any{item}
	if old := asSlice(edge["history"]); len(old) > 0 {
		hist = append(hist, old...)
	}
	if len(hist) > 20 {
		hist = hist[:20]
	}
	edge["history"] = hist
	if samples > 0 {
		edge["obs_ev"] = sumDelta / float64(samples)
		edge["win_rate"] = float64(wins) / float64(samples)
		bet := asFloat(edge["bet"])
		if bet > 0 {
			edge["obs_rtp"] = 1.0 + (sumDelta/float64(samples))/bet
		}
	}
	saveRiskEdge(st, key, edge)
	return edge
}

// YOLO: all-in dice style paid game. Without published house-edge proof, treat as negative EV.
func evaluateYoloEdge(st *storage.Storage, lcfg map[string]any, balance float64) map[string]any {
	prev := loadRiskEdge(st, "risk.edge.yolo")
	// Conservative theory: unknown exact table, assume house edge ~5% on stake=balance.
	// This keeps auto off until config/samples prove otherwise.
	minRTP := riskNum(lcfg, "yolo_min_rtp", 1.0)
	minEV := riskNum(lcfg, "yolo_min_ev", 0)
	minSamples := riskNum(lcfg, "yolo_min_samples", 50)
	theoryRTP := riskNum(lcfg, "yolo_theory_rtp", 0.95) // default 95% RTP assumption (negative)
	if theoryRTP <= 0 {
		theoryRTP = 0.95
	}
	bet := balance
	if bet < 0 {
		bet = 0
	}
	theoryEV := (theoryRTP - 1.0) * bet
	edge := buildRiskEdge("yolo", true, theoryRTP, theoryEV, minRTP, minEV,
		int(asFloat(prev["samples"])), asFloat(prev["sum_delta"]), int(asFloat(prev["wins"])),
		map[string]any{
			"bet":         bet,
			"min_samples": minSamples,
			"note":        "搏一搏按全额高波动处理；默认理论RTP=95%（可配置 yolo_theory_rtp）",
		},
	)
	saveRiskEdge(st, "risk.edge.yolo", edge)
	return edge
}

// VIP bet: no free lunch assumed; require configured positive edge.
func evaluateVipBetEdge(st *storage.Storage, lcfg map[string]any, vipState map[string]any) map[string]any {
	prev := loadRiskEdge(st, "risk.edge.vip_bet")
	minRTP := riskNum(lcfg, "vip_bet_min_rtp", 1.0)
	minEV := riskNum(lcfg, "vip_bet_min_ev", 0)
	minSamples := riskNum(lcfg, "vip_bet_min_samples", 30)
	// default theory negative unless config provides vip_bet_theory_rtp/ev
	theoryRTP := riskNum(lcfg, "vip_bet_theory_rtp", 0.97)
	theoryEV := riskNum(lcfg, "vip_bet_theory_ev", -1)
	canEnter := asBool(vipState["can_enter"], false)
	edge := buildRiskEdge("vip_bet", true, theoryRTP, theoryEV, minRTP, minEV,
		int(asFloat(prev["samples"])), asFloat(prev["sum_delta"]), int(asFloat(prev["wins"])),
		map[string]any{
			"min_samples": minSamples,
			"can_enter":   canEnter,
			"note":        "VIP下注默认负期望门槛；仅当配置/样本证明正EV才自动",
		},
	)
	if !canEnter {
		edge["gate"] = "vip_gate_not_met"
		edge["probe_status"] = "blocked"
		edge["edge_ok"] = false
		edge["message"] = "VIP下注：当前不可进房/门槛未满足"
	}
	saveRiskEdge(st, "risk.edge.vip_bet", edge)
	return edge
}

// Borrow zero-rate: edge only when zero-rate offer exists.
func evaluateBorrowEdge(st *storage.Storage, lcfg map[string]any, loanOffers map[string]any) map[string]any {
	prev := loadRiskEdge(st, "risk.edge.borrow")
	offers := asSlice(firstNonNil(loanOffers["offers"], loanOffers["data"], loanOffers["items"], loanOffers["list"]))
	bestRate := 1e9
	zeroN := 0
	for _, it := range offers {
		m := asMap(it)
		rate := asFloat(firstNonNil(m["daily_rate"], m["rate"], m["interest_rate"]))
		if rate == 0 {
			// maybe percent style 0.0
			zeroN++
		}
		if rate < bestRate {
			bestRate = rate
		}
	}
	if len(offers) == 0 {
		bestRate = -1
	}
	// Treat zero-rate as non-negative EV utility (not profit, but not costly). Still require explicit auto + edge_ok.
	theoryRTP := 1.0
	theoryEV := 0.0
	theoryOK := len(offers) > 0
	if zeroN == 0 && len(offers) > 0 {
		// positive interest => negative edge by default
		theoryRTP = 0.9
		theoryEV = -1
	}
	if zeroN > 0 {
		theoryRTP = 1.0
		theoryEV = 0.0
	}
	minRTP := riskNum(lcfg, "borrow_min_rtp", 1.0)
	minEV := riskNum(lcfg, "borrow_min_ev", 0)
	edge := buildRiskEdge("borrow", theoryOK, theoryRTP, theoryEV, minRTP, minEV,
		int(asFloat(prev["samples"])), asFloat(prev["sum_delta"]), int(asFloat(prev["wins"])),
		map[string]any{
			"offers":      len(offers),
			"zero_offers": zeroN,
			"best_rate":   bestRate,
			"min_samples": riskNum(lcfg, "borrow_min_samples", 1),
			"note":        "仅零息挂单视为非负成本；有息默认拦截",
		},
	)
	if !theoryOK {
		edge["message"] = "借贷：暂无挂单可分析"
	} else if zeroN == 0 {
		edge["edge_ok"] = false
		edge["gate"] = "theory_negative"
		edge["probe_status"] = "blocked"
		edge["message"] = "借贷：无零息挂单，自动借保持关闭"
	} else {
		edge["message"] = fmt.Sprintf("借贷：发现零息挂单 %d 条（仍需开关开启才借）", zeroN)
	}
	saveRiskEdge(st, "risk.edge.borrow", edge)
	return edge
}

// Derivatives / leverage: use planner edge if present; otherwise blocked.
func evaluateDerivativesEdge(st *storage.Storage, dcfg map[string]any, ds map[string]any, tradeEnabled bool) map[string]any {
	prev := loadRiskEdge(st, "risk.edge.derivatives")
	analysis := asMap(ds["analysis"])
	actions := asSliceMaps(ds["actions"])
	best := asMap(analysis["best"])
	if len(best) == 0 && len(actions) > 0 {
		best = actions[0]
	}
	// net edge fields used by UI/planner
	theoryEV := asFloat(firstNonNil(best["net_edge"], best["best_net_edge"], analysis["best_net_edge"], best["ev"], best["expected_edge"]))
	// map EV to pseudo RTP around 1.0 for display (not true RTP)
	theoryRTP := 1.0
	if theoryEV < 0 {
		theoryRTP = 0.9
	} else if theoryEV > 0 {
		theoryRTP = 1.05
	}
	theoryOK := len(best) > 0 || len(actions) > 0 || len(analysis) > 0
	minRTP := riskNum(dcfg, "min_rtp", 1.0)
	minEV := riskNum(dcfg, "min_net_edge", riskNum(dcfg, "min_ev", 0))
	edge := buildRiskEdge("derivatives", theoryOK, theoryRTP, theoryEV, minRTP, minEV,
		int(asFloat(prev["samples"])), asFloat(prev["sum_delta"]), int(asFloat(prev["wins"])),
		map[string]any{
			"trade_enabled": tradeEnabled,
			"leverage":      firstNonNil(best["leverage"], analysis["leverage"], dcfg["max_leverage"]),
			"code":          firstNonNil(best["code"], best["symbol"]),
			"notional":      firstNonNil(best["notional"], analysis["notional"]),
			"min_samples":   riskNum(dcfg, "min_samples", 20),
			"note":          "期货/杠杆：仅当计划净边(EV)为正且实盘开关开启才允许",
		},
	)
	if !tradeEnabled {
		edge["edge_ok"] = false
		if edge["gate"] == "ready" {
			edge["gate"] = "trade_disabled"
		}
		edge["probe_status"] = "blocked"
		edge["message"] = "期货/杠杆：实盘开关关闭，仅分析"
	}
	if !theoryOK {
		edge["message"] = "期货/杠杆：暂无计划/边数据"
	}
	saveRiskEdge(st, "risk.edge.derivatives", edge)
	return edge
}

// Underwrite: high risk; default blocked unless configured positive fee edge.
func evaluateUnderwriteEdge(st *storage.Storage, bcfg map[string]any, underwriteCount int) map[string]any {
	prev := loadRiskEdge(st, "risk.edge.underwrite")
	theoryRTP := riskNum(bcfg, "underwrite_theory_rtp", 0.98)
	theoryEV := riskNum(bcfg, "underwrite_theory_ev", -1)
	minRTP := riskNum(bcfg, "underwrite_min_rtp", 1.0)
	minEV := riskNum(bcfg, "underwrite_min_ev", 0)
	edge := buildRiskEdge("underwrite", true, theoryRTP, theoryEV, minRTP, minEV,
		int(asFloat(prev["samples"])), asFloat(prev["sum_delta"]), int(asFloat(prev["wins"])),
		map[string]any{
			"candidates":  underwriteCount,
			"min_samples": riskNum(bcfg, "underwrite_min_samples", 10),
			"note":        "承销默认高门槛；需配置正EV才自动",
		},
	)
	if underwriteCount <= 0 {
		edge["edge_ok"] = false
		edge["gate"] = "no_candidates"
		edge["probe_status"] = "blocked"
		edge["message"] = "承销：当前无候选单"
	}
	saveRiskEdge(st, "risk.edge.underwrite", edge)
	return edge
}
