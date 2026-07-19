package modules

import (
	"errors"
	"path/filepath"
	"testing"

	"fzsmbot/internal/config"
	"fzsmbot/internal/storage"
)

type fakeDerivativesAPI struct {
	groups        []any
	positions     []any
	openBodies    []map[string]any
	closeBodies   []map[string]any
	futuresErr    error
	positionsErr  error
	futuresCode   int
	positionsCode int
}

func (f *fakeDerivativesAPI) StocksFutures() ([]any, int, error) {
	code := f.futuresCode
	if code == 0 {
		code = 200
	}
	return f.groups, code, f.futuresErr
}
func (f *fakeDerivativesAPI) StocksMarginPositions() ([]any, int, error) {
	code := f.positionsCode
	if code == 0 {
		code = 200
	}
	return f.positions, code, f.positionsErr
}
func (f *fakeDerivativesAPI) StocksMarginOpen(body map[string]any) (map[string]any, error) {
	f.openBodies = append(f.openBodies, body)
	return map[string]any{"success": true, "position_id": 99}, nil
}
func (f *fakeDerivativesAPI) StocksMarginClose(body map[string]any) (map[string]any, error) {
	f.closeBodies = append(f.closeBodies, body)
	return map[string]any{"success": true}, nil
}

func derivativeTestStorage(t *testing.T) *storage.Storage {
	t.Helper()
	st, err := storage.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func derivativeTestConfig() *config.Config {
	return &config.Config{Derivatives: map[string]any{
		"enabled": true, "trade_enabled": false, "max_leverage": 3,
		"max_notional": 5000000.0, "max_margin_cash_pct": 0.05,
		"max_margin_equity_pct": 0.05, "min_abs_basis_pct": 0.008,
		"min_net_edge": 0.005, "fee_rate": 0.001, "slippage_rate": 0.001,
		"min_time_to_expiry_sec": 300.0, "max_time_to_expiry_sec": 21600.0,
		"min_convergence_capture": 0.20, "maintenance_margin_pct": 0.08,
		"min_liquidation_buffer_pct": 0.18, "max_open_positions": 2,
		"min_interval_sec": 0.0,
	}}
}

func positiveFutureGroups() []any {
	return []any{map[string]any{"contracts": []any{map[string]any{
		"id": 101, "code": "IF-TEST", "current_price": 900.0,
		"underlying_price": 1000.0, "basis_pct": -10.0,
		"time_to_expiry_sec": 600.0,
	}}}}
}

func TestPlanDerivativeNormalizesPercentAndBuildsLong(t *testing.T) {
	best, rows := planDerivative(derivativeContracts(positiveFutureGroups()), map[string]any{"cash": 100000000.0, "equity": 100000000.0}, derivativeTestConfig().Derivatives)
	if len(rows) != 1 || best.StockID != 101 || best.Side != "long" {
		t.Fatalf("unexpected plan: best=%+v rows=%+v", best, rows)
	}
	if best.BasisPct != -0.10 {
		t.Fatalf("basis percent not normalized: %v", best.BasisPct)
	}
	if best.Notional > 5000000 || best.Leverage > 3 || best.LiquidationBuffer < 0.18 {
		t.Fatalf("hard risk boundary exceeded: %+v", best)
	}
}

func TestDerivativeSwitchOffDoesNotOpen(t *testing.T) {
	api := &fakeDerivativesAPI{groups: positiveFutureGroups()}
	out := executeDerivatives(derivativeTestConfig(), derivativeTestStorage(t), api, map[string]any{"derivatives.trade_enabled": false}, map[string]any{"cash": 100000000.0, "equity": 100000000.0})
	if len(api.openBodies) != 0 {
		t.Fatalf("switch off opened margin: %+v", api.openBodies)
	}
	if out["status"] != "analyze_only" {
		t.Fatalf("status=%v", out["status"])
	}
}

func TestDerivativeSwitchOnOpensBoundedPlan(t *testing.T) {
	api := &fakeDerivativesAPI{groups: positiveFutureGroups()}
	out := executeDerivatives(derivativeTestConfig(), derivativeTestStorage(t), api, map[string]any{"derivatives.trade_enabled": true}, map[string]any{"cash": 100000000.0, "equity": 100000000.0})
	if len(api.openBodies) != 1 {
		t.Fatalf("expected one open: out=%+v bodies=%+v", out, api.openBodies)
	}
	if asBool(out["executable"], true) {
		t.Fatalf("submitted order still reported executable: %+v", out)
	}
	body := api.openBodies[0]
	if body["stock_id"] != 101 || body["side"] != "long" || int(asFloat(body["leverage"])) > 3 || int(asFloat(body["shares"])) <= 0 {
		t.Fatalf("invalid open body: %+v", body)
	}
}

func TestDerivativeDoesNotOpenWhenPositionsStateUnavailable(t *testing.T) {
	api := &fakeDerivativesAPI{groups: positiveFutureGroups(), positionsErr: errors.New("timeout"), positionsCode: 500}
	executeDerivatives(derivativeTestConfig(), derivativeTestStorage(t), api, map[string]any{"derivatives.trade_enabled": true}, map[string]any{"cash": 100000000.0, "equity": 100000000.0})
	if len(api.openBodies) != 0 {
		t.Fatalf("opened with unknown existing exposure: %+v", api.openBodies)
	}
}

func TestDerivativeProtectiveClose(t *testing.T) {
	api := &fakeDerivativesAPI{
		groups:    positiveFutureGroups(),
		positions: []any{map[string]any{"id": 7, "risk_level": "danger", "side": "long", "shares": 2}},
	}
	executeDerivatives(derivativeTestConfig(), derivativeTestStorage(t), api, map[string]any{"derivatives.trade_enabled": false}, map[string]any{"cash": 100000000.0, "equity": 100000000.0})
	if len(api.openBodies) != 0 || len(api.closeBodies) != 1 || api.closeBodies[0]["position_id"] != 7 {
		t.Fatalf("protective close mismatch: open=%+v close=%+v", api.openBodies, api.closeBodies)
	}
}

func TestDerivativeRejectsInsufficientLiquidationBuffer(t *testing.T) {
	cfg := derivativeTestConfig().Derivatives
	cfg["max_leverage"] = 10
	cfg["min_liquidation_buffer_pct"] = 0.30
	best, _ := planDerivative(derivativeContracts(positiveFutureGroups()), map[string]any{"cash": 100000000.0, "equity": 100000000.0}, cfg)
	if best.StockID != 0 {
		t.Fatalf("unsafe liquidation buffer should be rejected: %+v", best)
	}
}
