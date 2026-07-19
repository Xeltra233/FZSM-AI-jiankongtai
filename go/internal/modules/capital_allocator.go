package modules

import (
	"math"
	"sort"

	"fzsmbot/internal/config"
	"fzsmbot/internal/storage"
)

type capitalOpportunity struct {
	ID            string
	NetEV         float64
	Capital       float64
	Success       float64
	Confidence    float64
	DurationHours float64
	Eligible      bool
	Reason        string
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func opportunityScore(o capitalOpportunity) float64 {
	if !o.Eligible || o.NetEV <= 0 {
		return 0
	}
	capital := math.Max(1, o.Capital)
	hours := math.Max(1.0/60.0, o.DurationHours)
	return o.NetEV / capital / hours * clamp01(o.Success) * clamp01(o.Confidence)
}

func recentFailureStreak(edge map[string]any) int {
	n := 0
	for _, item := range asSlice(edge["history"]) {
		if asBool(asMap(item)["win"], false) {
			break
		}
		n++
	}
	return n
}

func executionDegrade(edge map[string]any, cooldownSec float64) (scale float64, degraded bool) {
	streak := recentFailureStreak(edge)
	if streak < 3 {
		return 1, false
	}
	if now()-asFloat(edge["last_ts"]) < cooldownSec {
		return 0, true
	}
	return 0.1, true
}

func allocatorNum(cfg map[string]any, key string, def float64) float64 {
	if cfg != nil {
		if v, ok := cfg[key]; ok {
			return asFloat(v)
		}
	}
	return def
}

func buildCapitalAllocator(cfg *config.Config, st *storage.Storage, account map[string]any) map[string]any {
	cash, equity := asFloat(account["cash"]), asFloat(account["equity"])
	rcfg := map[string]any{}
	if cfg != nil && cfg.Risk != nil {
		rcfg = cfg.Risk
	}
	pool := math.Min(cash*allocatorNum(rcfg, "cross_module_budget_cash_pct", 0.05), equity*allocatorNum(rcfg, "cross_module_budget_equity_pct", 0.05))
	maxPool := allocatorNum(rcfg, "cross_module_budget_max", 10000000)
	if maxPool > 0 {
		pool = math.Min(pool, maxPool)
	}
	if pool < 0 {
		pool = 0
	}

	deriv := loadRiskEdge(st, "risk.edge.derivatives")
	derivCapital := asFloat(firstNonNil(deriv["margin"], deriv["margin_budget"], pool))
	if derivCapital <= 0 {
		derivCapital = pool
	}
	derivEV := asFloat(firstNonNil(deriv["use_ev"], deriv["theory_ev"], deriv["net_edge"]))
	derivExec := loadRiskEdge(st, "risk.exec.derivatives")
	derivScale, derivDegraded := executionDegrade(derivExec, allocatorNum(rcfg, "cross_module_failure_cooldown_sec", 900))
	derivSuccess := asFloat(derivExec["win_rate"])
	if asFloat(derivExec["samples"]) < 1 {
		derivSuccess = 0.70
	}
	derivConf := math.Min(1, math.Max(0.35, asFloat(deriv["samples"])/30))
	derivDiscovery := len(deriv) == 0 || stringValue(deriv["gate"]) == "need_config"
	if derivDiscovery && derivEV <= 0 {
		derivEV = 0.0001
	}

	premium := loadRiskEdge(st, "risk.obs.free_draw_premium")
	entryFee := asFloat(premium["entry_fee"])
	if entryFee <= 0 && cfg != nil && cfg.Lottery != nil {
		entryFee = allocatorNum(cfg.Lottery, "premium_entry_fee", 500000)
	}
	premiumEV := asFloat(firstNonNil(premium["rolling_lcb_ev"], premium["obs_ev"])) - entryFee
	premiumExec := loadRiskEdge(st, "risk.exec.lottery_paid_premium")
	premiumSuccess := asFloat(premiumExec["win_rate"])
	if asFloat(premiumExec["samples"]) < 1 {
		premiumSuccess = 0.90
	}
	premiumConf := math.Min(1, asFloat(firstNonNil(premium["rolling_samples"], premium["samples"]))/20)

	farmState := map[string]any{}
	if st != nil {
		farmState = st.GetStateMap("farm")
	}
	ops := []capitalOpportunity{
		{ID: "derivatives", NetEV: derivEV * math.Max(1, derivCapital) * derivScale, Capital: derivCapital * derivScale, Success: derivSuccess, Confidence: derivConf, DurationHours: 1, Eligible: derivScale > 0 && (asBool(deriv["edge_ok"], false) || derivDiscovery) && !asBool(deriv["hard_block"], false), Reason: "positive_net_basis_or_discovery"},
		{ID: "lottery_paid_premium", NetEV: premiumEV, Capital: entryFee, Success: premiumSuccess, Confidence: premiumConf, DurationHours: 1.0 / 60.0, Eligible: premiumEV > 0 && premiumConf >= 1, Reason: "current_version_net_lcb"},
		{ID: "farm", NetEV: asFloat(farmState["day_ev_12"]) / 24, Capital: 0, Success: 1, Confidence: 1, DurationHours: 1, Eligible: true, Reason: "zero_capital_recurring"},
		{ID: "lottery_free", NetEV: asFloat(loadRiskEdge(st, "risk.obs.free_draw")["rolling_lcb_ev"]), Capital: 0, Success: 1, Confidence: 1, DurationHours: 1.0 / 60.0, Eligible: true, Reason: "free_ticket"},
	}
	sort.SliceStable(ops, func(i, j int) bool { return opportunityScore(ops[i]) > opportunityScore(ops[j]) })
	totalScore := 0.0
	for _, o := range ops {
		if o.Capital > 0 {
			totalScore += opportunityScore(o)
		}
	}
	allocations := map[string]any{}
	ranked := make([]any, 0, len(ops))
	for rank, o := range ops {
		score := opportunityScore(o)
		cap := 0.0
		if o.Capital > 0 && totalScore > 0 {
			cap = math.Min(o.Capital, pool*score/totalScore)
		}
		allocations[o.ID] = map[string]any{"cap": cap, "score": score, "net_ev": o.NetEV, "capital": o.Capital, "success_prob": o.Success, "confidence": o.Confidence, "eligible": o.Eligible, "reason": o.Reason, "rank": rank + 1}
		ranked = append(ranked, map[string]any{"id": o.ID, "rank": rank + 1, "score": score, "cap": cap, "net_ev": o.NetEV, "capital": o.Capital, "eligible": o.Eligible, "reason": o.Reason})
	}
	out := map[string]any{"ts": now(), "cash": cash, "equity": equity, "pool": pool, "allocations": allocations, "ranking": ranked, "formula": "score=(net_ev/capital)/hours*success*confidence; caps share bounded pool", "negative_ev_blocked": true, "execution_degrade": map[string]any{"derivatives": derivDegraded, "derivatives_scale": derivScale}}
	if st != nil {
		_ = st.SetState("capital.allocator", out)
	}
	return out
}

func capitalAllocationCap(values map[string]any, id string) (float64, bool) {
	alloc := asMap(values["_capital_allocator"])
	if len(alloc) == 0 {
		return 0, false
	}
	row := asMap(asMap(alloc["allocations"])[id])
	if len(row) == 0 {
		return 0, true
	}
	return math.Max(0, asFloat(row["cap"])), true
}
