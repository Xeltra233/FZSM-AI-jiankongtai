package modules

import (
	"math"
	"path/filepath"
	"testing"

	"fzsmbot/internal/storage"
)

func lotteryTestStorage(t *testing.T) *storage.Storage {
	t.Helper()
	st, err := storage.Open(filepath.Join(t.TempDir(), "lottery.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestRollingObservationStatsAndConfidence(t *testing.T) {
	h := []any{map[string]any{"delta": 10.0}, map[string]any{"delta": 20.0}, map[string]any{"delta": 30.0}}
	got := rollingObservationStats(h, 100, 1.96)
	if asFloat(got["mean"]) != 20 || !asBool(got["confidence_ready"], false) {
		t.Fatalf("unexpected stats: %+v", got)
	}
	if !(asFloat(got["lcb"]) < 20 && asFloat(got["ucb"]) > 20) {
		t.Fatalf("bad interval: %+v", got)
	}
}

func TestVersionedObservationArchivesAndResets(t *testing.T) {
	st := lotteryTestStorage(t)
	recordVersionedObservation(st, "risk.obs.test", 10, true, map[string]any{"version": "v1"})
	got := recordVersionedObservation(st, "risk.obs.test", 20, true, map[string]any{"version": "v2"})
	if stringValue(got["version"]) != "v2" || int(asFloat(got["samples"])) != 1 || asFloat(got["sum_delta"]) != 20 {
		t.Fatalf("version reset failed: %+v", got)
	}
	if len(asSlice(got["previous_versions"])) != 1 {
		t.Fatalf("archive missing: %+v", got)
	}
}

func TestDrawNetDeltaPrefersNet(t *testing.T) {
	got := drawNetDelta(map[string]any{"net_lobster": 400.0, "delta_lobster": 900.0, "win_lobster": 1000.0})
	if got != 400 {
		t.Fatalf("gross value used instead of net: %v", got)
	}
}

func TestPaidPremiumDecisionBoundaries(t *testing.T) {
	edge := map[string]any{"version": "v1", "rolling_samples": 20, "rolling_ev": 900000.0, "rolling_lcb_ev": 700000.0, "confidence_ready": true}
	good := paidPremiumDecision(edge, "v1", 500000, map[string]any{"paid_premium_min_samples": 20, "paid_premium_min_net_ev": 0})
	if !asBool(good["ready"], false) {
		t.Fatalf("positive lower bound rejected: %+v", good)
	}
	edge["rolling_lcb_ev"] = 400000.0
	bad := paidPremiumDecision(edge, "v1", 500000, map[string]any{"paid_premium_min_samples": 20})
	if asBool(bad["ready"], true) {
		t.Fatalf("negative net lower bound accepted: %+v", bad)
	}
}

func officialSlotConfig() map[string]any {
	weights := []float64{30, 22, 18, 14, 10, 6}
	mult := []float64{3, 5, 8, 15, 30, 100}
	syms := make([]any, 0, 6)
	prizes := make([]any, 0, 8)
	for i := range weights {
		id := string(rune('a' + i))
		syms = append(syms, map[string]any{"id": id, "weight": weights[i]})
		prizes = append(prizes, map[string]any{"match_type": "three_same", "symbol_id": id, "payout_mult": mult[i]})
	}
	prizes = append(prizes, map[string]any{"match_type": "two_same", "payout_mult": 1.0}, map[string]any{"match_type": "three_diff", "payout_mult": 0.0})
	return map[string]any{"settings": map[string]any{"bet_lobster": 1000000.0}, "symbols": syms, "prizes": prizes}
}

func TestOfficialSlotTheoryAndHardBlock(t *testing.T) {
	theory := computeSlotTheory(officialSlotConfig())
	if math.Abs(asFloat(theory["rtp"])-0.743336) > 0.000001 {
		t.Fatalf("RTP mismatch: %+v", theory)
	}
	edge := updateSlotEdge(lotteryTestStorage(t), map[string]any{"slot_min_rtp": 1.0, "slot_min_ev": 0.0}, officialSlotConfig())
	if !asBool(edge["hard_block"], false) {
		t.Fatalf("negative exact theory not hard blocked: %+v", edge)
	}
	bypass := applyEdgeGate(map[string]any{"risk.edge_gate_enabled": false}, edge)
	if asBool(bypass["edge_ok"], true) || asBool(bypass["gate_bypassed"], true) {
		t.Fatalf("hard block bypassed: %+v", bypass)
	}
}
