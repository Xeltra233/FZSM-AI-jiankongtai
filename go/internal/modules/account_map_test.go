package modules

import "testing"

func TestNormalizeAccountMaps_LobsterFields(t *testing.T) {
	me := map[string]any{
		"balance_lobster":      388200.0,
		"total_asset_lobster":  1483387.84,
		"stock_value_lobster":  1095187.84,
		"pnl_lobster":          -229636.16,
		"pnl_pct":              -0.17333333333333328,
		"user": map[string]any{
			"display_name": "Xeltra",
			"username":     "xeltra233_22",
		},
		"positions": []any{
			map[string]any{"code": "SK-PEPE", "shares": 441608.0, "price": 2.48, "market_value": 1095187.84},
		},
	}
	pf := map[string]any{
		"positions": []any{
			map[string]any{"code": "SK-PEPE", "shares": 441608.0, "price": 2.48, "market_value": 1095187.84},
		},
		"summary": map[string]any{
			"balance_lobster":     388200.0,
			"total_asset_lobster": 1483387.84,
			"stock_value_lobster": 1095187.84,
			"pnl_lobster":         -229636.16,
			"pnl_pct":             -0.17333333333333328,
		},
	}
	acc, name := NormalizeAccountMaps(me, pf)
	if name != "Xeltra" {
		t.Fatalf("name=%v", name)
	}
	if asFloat(acc["cash"]) != 388200 {
		t.Fatalf("cash=%v", acc["cash"])
	}
	if asFloat(acc["equity"]) != 1483387.84 {
		t.Fatalf("equity=%v", acc["equity"])
	}
	if asFloat(acc["stock_value"]) != 1095187.84 {
		t.Fatalf("stock_value=%v", acc["stock_value"])
	}
	if asFloat(acc["pnl"]) != -229636.16 {
		t.Fatalf("pnl=%v", acc["pnl"])
	}
	if len(asSlice(acc["positions"])) != 1 {
		t.Fatalf("positions=%v", acc["positions"])
	}
}

func TestNormalizeAccountMaps_FallbackFromPositions(t *testing.T) {
	me := map[string]any{
		"balance_lobster": 1000.0,
		"user":            map[string]any{"username": "u1"},
	}
	pf := map[string]any{
		"positions": []any{
			map[string]any{"shares": 10.0, "price": 2.5},
		},
		"summary": map[string]any{},
	}
	acc, _ := NormalizeAccountMaps(me, pf)
	if asFloat(acc["stock_value"]) != 25 {
		t.Fatalf("stock_value=%v", acc["stock_value"])
	}
	if asFloat(acc["equity"]) != 1025 {
		t.Fatalf("equity=%v want 1025", acc["equity"])
	}
}

func TestPlotStateReadyGrowingEmpty(t *testing.T) {
	if plotState(map[string]any{"status": "empty"}) != "empty" {
		t.Fatal("empty")
	}
	if plotState(map[string]any{"status": "growing", "crop_key": "lobster", "ready_at": now() + 1000}) != "growing" {
		t.Fatal("growing")
	}
	if plotState(map[string]any{"status": "ready", "crop_key": "lobster"}) != "ready" {
		t.Fatal("ready")
	}
}
