package risk

import "testing"

func allInRisk() *Manager {
	m := New(map[string]any{
		"max_positions":                 6,
		"all_in_max_positions":          10,
		"cash_reserve_pct":              0.12,
		"position_pct":                  0.12,
		"max_position_pct":              0.20,
		"max_notional_per_order":        500000.0,
		"all_in_max_notional_pct":       0.08,
		"all_in_max_notional_per_order": 10000000.0,
		"max_shares_per_order":          5000000.0,
	})
	m.SetControl(map[string]any{"capital_style": "all_in"})
	return m
}

func TestAllInUsesScaledOrderBudget(t *testing.T) {
	m := allInRisk()
	target, edge := 0.20, 0.03
	d := m.SizeBuyForPosition(195000000, 192000000, 1000, 5, false, 0.8, &target, &edge)
	if !d.Allow {
		t.Fatalf("expected buy allowed: %s", d.Reason)
	}
	if d.Shares != 10000 { // 10m hard cap / 1000
		t.Fatalf("expected accelerated 10m budget, shares=%v reason=%s", d.Shares, d.Reason)
	}
}

func TestExistingPositionCanAddAtPositionCountLimit(t *testing.T) {
	m := allInRisk()
	target, edge := 0.10, 0.02
	if d := m.SizeBuyForPosition(1000000, 900000, 100, 10, false, 0.8, &target, &edge); d.Allow {
		t.Fatalf("new position should be blocked at all-in max: %+v", d)
	}
	if d := m.SizeBuyForPosition(1000000, 900000, 100, 10, true, 0.8, &target, &edge); !d.Allow {
		t.Fatalf("existing position add should remain allowed: %+v", d)
	}
}

func TestPreferHoldKeepsStaticOrderCap(t *testing.T) {
	m := allInRisk()
	m.SetControl(map[string]any{"capital_style": "prefer_hold"})
	target, edge := 0.20, 0.03
	d := m.SizeBuyForPosition(195000000, 192000000, 1000, 5, false, 0.8, &target, &edge)
	if !d.Allow || d.Shares != 500 {
		t.Fatalf("prefer_hold should retain 500k cap: %+v", d)
	}
}
