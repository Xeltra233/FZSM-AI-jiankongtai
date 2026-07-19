package modules

import (
	"math"
	"testing"

	"fzsmbot/internal/config"
)

func TestCapitalAllocatorBlocksNegativeAndBoundsPool(t *testing.T) {
	st := lotteryTestStorage(t)
	_ = st.SetState("risk.edge.derivatives", map[string]any{"edge_ok": true, "use_ev": 0.02, "margin": 5000000.0, "samples": 30, "gate": "ready"})
	_ = st.SetState("risk.obs.free_draw_premium", map[string]any{"rolling_lcb_ev": 400000.0, "entry_fee": 500000.0, "rolling_samples": 100, "samples": 100, "win_rate": 0.6})
	cfg := &config.Config{Risk: map[string]any{"cross_module_budget_cash_pct": 0.05, "cross_module_budget_equity_pct": 0.05, "cross_module_budget_max": 10000000.0}}
	got := buildCapitalAllocator(cfg, st, map[string]any{"cash": 100000000.0, "equity": 200000000.0})
	if asFloat(got["pool"]) != 5000000 {
		t.Fatalf("pool mismatch: %+v", got)
	}
	alloc := asMap(got["allocations"])
	d := asMap(alloc["derivatives"])
	p := asMap(alloc["lottery_paid_premium"])
	if asFloat(d["cap"]) <= 0 || asFloat(d["cap"]) > 5000000 {
		t.Fatalf("derivative cap invalid: %+v", d)
	}
	if asFloat(p["cap"]) != 0 || asBool(p["eligible"], true) {
		t.Fatalf("negative premium allocated: %+v", p)
	}
}

func TestCapitalAllocatorAllowsDerivativeDiscoveryWithoutExecutionBypass(t *testing.T) {
	st := lotteryTestStorage(t)
	_ = st.SetState("risk.edge.derivatives", map[string]any{"gate": "need_config", "edge_ok": false})
	got := buildCapitalAllocator(&config.Config{}, st, map[string]any{"cash": 10000000.0, "equity": 10000000.0})
	cap, managed := capitalAllocationCap(map[string]any{"_capital_allocator": got}, "derivatives")
	if !managed || cap <= 0 {
		t.Fatalf("discovery starved: cap=%v got=%+v", cap, got)
	}
}

func TestOpportunityScorePenalizesFailureAndDuration(t *testing.T) {
	fast := opportunityScore(capitalOpportunity{NetEV: 100, Capital: 1000, Success: 1, Confidence: 1, DurationHours: 1, Eligible: true})
	slowFail := opportunityScore(capitalOpportunity{NetEV: 100, Capital: 1000, Success: 0.5, Confidence: 1, DurationHours: 2, Eligible: true})
	if fast <= slowFail || opportunityScore(capitalOpportunity{NetEV: -1, Capital: 1, Success: 1, Confidence: 1, DurationHours: 1, Eligible: true}) != 0 {
		t.Fatalf("bad scores fast=%v slow=%v", fast, slowFail)
	}
}

func TestExecutionDegradeStopsFailureStormAndAllowsProbe(t *testing.T) {
	edge := map[string]any{"last_ts": now(), "history": []any{map[string]any{"win": false}, map[string]any{"win": false}, map[string]any{"win": false}}}
	if scale, degraded := executionDegrade(edge, 900); scale != 0 || !degraded {
		t.Fatalf("failure storm not stopped: scale=%v degraded=%v", scale, degraded)
	}
	edge["last_ts"] = now() - 901
	if scale, degraded := executionDegrade(edge, 900); scale != 0.1 || !degraded {
		t.Fatalf("recovery probe mismatch: scale=%v degraded=%v", scale, degraded)
	}
}

func TestDerivativePlanRespectsAbsoluteAllocatorCap(t *testing.T) {
	cfg := map[string]any{"max_margin_cash_pct": 0.5, "max_margin_equity_pct": 0.5, "max_margin_absolute": 100000.0, "max_notional": 5000000.0, "min_net_edge": 0.0}
	best, _ := planDerivative(asSliceMaps(positiveFutureGroups()), map[string]any{"cash": 10000000.0, "equity": 10000000.0}, cfg)
	if best.Margin > 100000.0001 {
		t.Fatalf("allocator cap exceeded: %+v", best)
	}
}

func TestAllocatorRedistributesUnusedShare(t *testing.T) {
	ops := []capitalOpportunity{
		{ID: "small", NetEV: 100, Capital: 100, Success: 1, Confidence: 1, DurationHours: 1, Eligible: true},
		{ID: "large", NetEV: 1000, Capital: 1000, Success: 1, Confidence: 1, DurationHours: 1, Eligible: true},
	}
	caps, remaining := allocateOpportunityCaps(ops, 1000)
	if math.Abs(caps["small"]-100) > 0.001 || math.Abs(caps["large"]-900) > 0.001 || math.Abs(remaining) > 0.001 {
		t.Fatalf("unused share not redistributed: caps=%+v remaining=%v", caps, remaining)
	}
}
