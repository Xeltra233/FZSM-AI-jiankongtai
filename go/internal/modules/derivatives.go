package modules

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"fzsmbot/internal/config"
	"fzsmbot/internal/storage"
)

type derivativesAPI interface {
	StocksFutures() ([]any, int, error)
	StocksMarginPositions() ([]any, int, error)
	StocksMarginOpen(map[string]any) (map[string]any, error)
	StocksMarginClose(map[string]any) (map[string]any, error)
}

type derivativePlan struct {
	StockID           int
	Code              string
	Side              string
	Price             float64
	UnderlyingPrice   float64
	BasisPct          float64
	TimeToExpirySec   float64
	Convergence       float64
	ExpectedMove      float64
	NetEdge           float64
	Leverage          int
	Margin            float64
	Notional          float64
	Shares            int
	LiquidationBuffer float64
}

func derivativeCfg(cfg *config.Config) map[string]any {
	if cfg == nil || cfg.Derivatives == nil {
		return map[string]any{}
	}
	return cfg.Derivatives
}

func dnum(m map[string]any, key string, def float64) float64 {
	if m == nil {
		return def
	}
	if _, ok := m[key]; !ok {
		return def
	}
	return asFloat(m[key])
}

func dbool(m map[string]any, key string, def bool) bool {
	if m == nil {
		return def
	}
	return asBool(m[key], def)
}

func derivativeContracts(groups []any) []map[string]any {
	out := []map[string]any{}
	for _, group0 := range groups {
		group := asMap(group0)
		contracts := asSlice(group["contracts"])
		if len(contracts) == 0 && len(group) > 0 && firstNonNil(group["id"], group["stock_id"]) != nil {
			contracts = []any{group}
		}
		for _, item := range contracts {
			if contract := asMap(item); len(contract) > 0 {
				out = append(out, contract)
			}
		}
	}
	return out
}

func normalizedBasis(contract map[string]any) float64 {
	basisPct := asFloat(firstNonNil(contract["basis_pct"], contract["premium_pct"]))
	// Live /futures returns percentage points (e.g. -19.5 means -19.5%).
	if math.Abs(basisPct) > 1 {
		basisPct /= 100
	}
	if basisPct == 0 {
		future := asFloat(firstNonNil(contract["current_price"], contract["price"], contract["mark_price"]))
		underlying := asFloat(firstNonNil(contract["underlying_price"], contract["index_price"]))
		if future > 0 && underlying > 0 {
			basisPct = (future - underlying) / underlying
		}
	}
	return basisPct
}

func convergenceWeight(tte, maxTTE, floor float64) float64 {
	if maxTTE <= 0 {
		maxTTE = 21600
	}
	if floor < 0 {
		floor = 0
	}
	if floor > 1 {
		floor = 1
	}
	w := 1 - math.Max(tte, 0)/maxTTE
	if w < floor {
		w = floor
	}
	if w > 1 {
		w = 1
	}
	return w
}

func planDerivative(contracts []map[string]any, account map[string]any, cfg map[string]any) (derivativePlan, []map[string]any) {
	equity := asFloat(account["equity"])
	cash := asFloat(account["cash"])
	fee := dnum(cfg, "fee_rate", 0.001)
	slippage := dnum(cfg, "slippage_rate", 0.001)
	minTTE := dnum(cfg, "min_time_to_expiry_sec", 300)
	maxTTE := dnum(cfg, "max_time_to_expiry_sec", 21600)
	minConv := dnum(cfg, "min_convergence_capture", 0.20)
	minAbsBasis := dnum(cfg, "min_abs_basis_pct", 0.008)
	minEdge := dnum(cfg, "min_net_edge", 0.005)
	maxLeverage := int(dnum(cfg, "max_leverage", 3))
	if maxLeverage < 1 {
		maxLeverage = 1
	}
	maintenance := dnum(cfg, "maintenance_margin_pct", 0.08)
	minLiqBuffer := dnum(cfg, "min_liquidation_buffer_pct", 0.18)
	liqPenaltyWeight := dnum(cfg, "liquidation_penalty_weight", 0.25)
	marginCashPct := dnum(cfg, "max_margin_cash_pct", 0.05)
	marginEquityPct := dnum(cfg, "max_margin_equity_pct", 0.05)
	maxNotional := dnum(cfg, "max_notional", 5000000)
	maxMarginAbsolute := dnum(cfg, "max_margin_absolute", -1)
	rows := []map[string]any{}
	best := derivativePlan{}

	for _, contract := range contracts {
		price := asFloat(firstNonNil(contract["current_price"], contract["mark_price"], contract["price"]))
		underlying := asFloat(firstNonNil(contract["underlying_price"], contract["index_price"]))
		tte := asFloat(firstNonNil(contract["time_to_expiry_sec"], contract["tte_sec"]))
		basis := normalizedBasis(contract)
		if price <= 0 || underlying <= 0 || tte < minTTE || tte > maxTTE || math.Abs(basis) < minAbsBasis {
			continue
		}
		conv := convergenceWeight(tte, maxTTE, minConv)
		expectedMove := math.Abs(basis) * conv
		leverage := 2
		if expectedMove >= 0.05 && maxLeverage >= 3 {
			leverage = 3
		}
		if leverage > maxLeverage {
			leverage = maxLeverage
		}
		liqBuffer := 1/float64(leverage) - maintenance
		liqPenalty := math.Max(minLiqBuffer-liqBuffer, 0) * liqPenaltyWeight
		netEdge := expectedMove - 2*fee - slippage - liqPenalty
		side := "short"
		if basis < 0 {
			side = "long"
		}
		margin := math.Min(cash*marginCashPct, equity*marginEquityPct)
		if maxMarginAbsolute >= 0 {
			margin = math.Min(margin, maxMarginAbsolute)
		}
		if maxNotional > 0 {
			margin = math.Min(margin, maxNotional/float64(leverage))
		}
		notional := margin * float64(leverage)
		shares := int(math.Floor(notional / price))
		eligible := netEdge >= minEdge && liqBuffer >= minLiqBuffer && shares > 0 && margin > 0
		row := map[string]any{
			"stock_id": int(asFloat(firstNonNil(contract["id"], contract["stock_id"]))),
			"code":     firstNonNil(contract["code"], contract["symbol"]), "side": side,
			"price": price, "underlying_price": underlying, "basis_pct": basis,
			"time_to_expiry_sec": tte, "convergence": conv, "expected_move": expectedMove,
			"net_edge": netEdge, "leverage": leverage, "liquidation_buffer_pct": liqBuffer,
			"margin": margin, "notional": notional, "shares": shares, "eligible": eligible,
		}
		rows = append(rows, row)
		if eligible && (best.StockID == 0 || netEdge > best.NetEdge) {
			best = derivativePlan{
				StockID: int(asFloat(row["stock_id"])), Code: fmt.Sprint(row["code"]), Side: side,
				Price: price, UnderlyingPrice: underlying, BasisPct: basis, TimeToExpirySec: tte,
				Convergence: conv, ExpectedMove: expectedMove, NetEdge: netEdge, Leverage: leverage,
				Margin: margin, Notional: notional, Shares: shares, LiquidationBuffer: liqBuffer,
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool { return asFloat(rows[i]["net_edge"]) > asFloat(rows[j]["net_edge"]) })
	if len(rows) > 12 {
		rows = rows[:12]
	}
	return best, rows
}

func derivativePositionID(p map[string]any) int {
	return int(asFloat(firstNonNil(p["id"], p["position_id"], p["margin_position_id"])))
}

func derivativePositionPnLPct(p map[string]any) float64 {
	pct := asFloat(firstNonNil(p["pnl_pct"], p["profit_pct"], p["return_pct"], p["roe"]))
	if math.Abs(pct) > 1 {
		pct /= 100
	}
	if pct == 0 {
		entry := asFloat(firstNonNil(p["entry_price"], p["avg_price"], p["open_price"]))
		mark := asFloat(firstNonNil(p["mark_price"], p["current_price"], p["price"]))
		if entry > 0 && mark > 0 {
			pct = (mark - entry) / entry
			if strings.EqualFold(fmt.Sprint(p["side"]), "short") {
				pct = -pct
			}
		}
	}
	return pct
}

func shouldCloseDerivative(p map[string]any, cfg map[string]any) (bool, string) {
	riskLevel := strings.ToLower(strings.TrimSpace(fmt.Sprint(firstNonNil(p["risk_level"], p["status_level"]))))
	if riskLevel == "danger" || riskLevel == "liquidation" {
		return true, "liquidation_risk"
	}
	marginRatio := asFloat(firstNonNil(p["margin_ratio"], p["margin_level"]))
	if math.Abs(marginRatio) > 1 {
		marginRatio /= 100
	}
	if minRatio := dnum(cfg, "min_margin_ratio", 0.12); marginRatio > 0 && marginRatio <= minRatio {
		return true, "margin_ratio_low"
	}
	pnlPct := derivativePositionPnLPct(p)
	if dbool(cfg, "auto_close_loss", true) && pnlPct <= -dnum(cfg, "close_loss_pct", 0.08) {
		return true, "stop_loss"
	}
	if take := dnum(cfg, "close_profit_pct", 0.15); take > 0 && pnlPct >= take {
		return true, "take_profit"
	}
	tte := asFloat(firstNonNil(p["time_to_expiry_sec"], p["tte_sec"]))
	if closeBefore := dnum(cfg, "close_before_expiry_sec", 180); tte > 0 && tte <= closeBefore {
		return true, "near_expiry"
	}
	return false, ""
}

func derivativePlanMap(p derivativePlan) map[string]any {
	if p.StockID <= 0 {
		return map[string]any{}
	}
	return map[string]any{
		"stock_id": p.StockID, "code": p.Code, "side": p.Side, "price": p.Price,
		"underlying_price": p.UnderlyingPrice, "basis_pct": p.BasisPct,
		"time_to_expiry_sec": p.TimeToExpirySec, "convergence": p.Convergence,
		"expected_move": p.ExpectedMove, "net_edge": p.NetEdge, "leverage": p.Leverage,
		"margin": p.Margin, "notional": p.Notional, "shares": p.Shares,
		"liquidation_buffer_pct": p.LiquidationBuffer,
	}
}

func executeDerivatives(cfg *config.Config, st *storage.Storage, c derivativesAPI, values map[string]any, account map[string]any) map[string]any {
	dcfg := map[string]any{}
	for k, v := range derivativeCfg(cfg) {
		dcfg[k] = v
	}
	if cap, managed := capitalAllocationCap(values, "derivatives"); managed {
		dcfg["max_margin_absolute"] = cap
	}
	enabled := dbool(dcfg, "enabled", true)
	tradeEnabled := flagOn(values, "derivatives.trade_enabled", dbool(dcfg, "trade_enabled", false))
	if !enabled || c == nil {
		return result("derivatives", "期货/保证金", "disabled", nil, nil, nil, nil, map[string]any{"enabled": false})
	}

	groups, futuresCode, futuresErr := c.StocksFutures()
	positions, positionsCode, positionsErr := c.StocksMarginPositions()
	futuresHealthy := futuresErr == nil && futuresCode < 400
	positionsHealthy := positionsErr == nil && positionsCode < 400
	errors := []string{}
	if futuresErr != nil || futuresCode >= 400 {
		errors = append(errors, fmt.Sprintf("futures status=%d err=%v", futuresCode, futuresErr))
	}
	if positionsErr != nil || positionsCode >= 400 {
		errors = append(errors, fmt.Sprintf("margin/positions status=%d err=%v", positionsCode, positionsErr))
	}
	contracts := derivativeContracts(groups)
	positionMaps := asSliceMaps(positions)
	best, candidates := planDerivative(contracts, account, dcfg)
	bestMap := derivativePlanMap(best)
	cash := asFloat(account["cash"])
	equity := asFloat(account["equity"])
	openMargin := 0.0
	for _, p := range positionMaps {
		margin := asFloat(firstNonNil(p["margin"], p["deposit"], p["margin_lobster"]))
		if margin == 0 {
			notional := asFloat(firstNonNil(p["notional"], p["market_value"]))
			lev := asFloat(p["leverage"])
			if lev > 0 {
				margin = notional / lev
			}
		}
		openMargin += margin
	}

	actions := []map[string]any{}
	writeAction := false
	writeFailed := false
	lastState := st.GetStateMap("derivatives")
	lastTradeAt := asFloat(lastState["last_trade_at"])
	minInterval := dnum(dcfg, "min_interval_sec", 60)
	protectWhenOff := dbool(dcfg, "protective_close_when_disabled", true)
	maxClose := int(dnum(dcfg, "max_close_per_cycle", 1))
	closed := 0
	closeAttempts := 0
	for _, p := range positionMaps {
		closeIt, why := shouldCloseDerivative(p, dcfg)
		if !closeIt || closeAttempts >= maxClose {
			continue
		}
		pid := derivativePositionID(p)
		if pid <= 0 {
			actions = append(actions, map[string]any{"status": "skip", "action": "margin_close", "reason": "position_id_missing", "position": p})
			continue
		}
		if !tradeEnabled && !protectWhenOff {
			actions = append(actions, map[string]any{"status": "analyze_only", "action": "margin_close", "reason": why, "position_id": pid})
			continue
		}
		raw, err := c.StocksMarginClose(map[string]any{"position_id": pid})
		writeAction = true
		closeAttempts++
		lastTradeAt = now()
		if err != nil {
			recordTraceSample(st, "risk.exec.derivatives", "derivatives_execution", 0, false, map[string]any{"source": "self_exec", "operation": "close"})
			writeFailed = true
			errors = append(errors, "margin close: "+err.Error())
			actions = append(actions, map[string]any{"status": "error", "action": "margin_close", "reason": why, "position_id": pid, "raw": raw})
		} else {
			actions = append(actions, map[string]any{"status": "submitted", "action": "margin_close", "reason": why, "position_id": pid, "raw": raw})
			closed++
		}
	}

	edge := applyEdgeGate(values, evaluateDerivativesEdge(st, dcfg, map[string]any{"analysis": map[string]any{"best": bestMap}}, tradeEnabled))
	maxOpen := int(dnum(dcfg, "max_open_positions", 2))
	cooldownOK := now()-lastTradeAt >= minInterval
	canOpen := tradeEnabled && futuresHealthy && positionsHealthy && !writeAction && len(positionMaps) < maxOpen && best.StockID > 0 && best.Shares > 0 && asBool(edge["edge_ok"], false) && cooldownOK
	if canOpen {
		body := map[string]any{"stock_id": best.StockID, "side": best.Side, "leverage": best.Leverage, "shares": best.Shares}
		raw, err := c.StocksMarginOpen(body)
		writeAction = true
		lastTradeAt = now()
		if err != nil {
			recordTraceSample(st, "risk.exec.derivatives", "derivatives_execution", 0, false, map[string]any{"source": "self_exec", "operation": "open"})
			writeFailed = true
			errors = append(errors, "margin open: "+err.Error())
			actions = append(actions, map[string]any{"status": "error", "action": "margin_order", "operation": "open", "body": body, "raw": raw, "reason": err.Error()})
		} else {
			recordTraceSample(st, "risk.exec.derivatives", "derivatives_execution", best.NetEdge, true, map[string]any{"source": "self_exec", "operation": "open"})
			actions = append(actions, map[string]any{"status": "submitted", "action": "margin_order", "operation": "open", "body": body, "raw": raw, "net_edge": best.NetEdge})
		}
	} else if !tradeEnabled {
		actions = append(actions, map[string]any{"status": "analyze_only", "action": "margin_order", "operation": "open", "reason": "derivatives_trade_disabled", "plan": bestMap})
	} else if !futuresHealthy || !positionsHealthy {
		actions = append(actions, map[string]any{"status": "skip", "action": "margin_order", "operation": "open", "reason": "derivatives_state_unavailable", "futures_healthy": futuresHealthy, "positions_healthy": positionsHealthy})
	} else if len(positionMaps) >= maxOpen {
		actions = append(actions, map[string]any{"status": "skip", "action": "margin_order", "operation": "open", "reason": "max_open_positions", "open_positions": len(positionMaps)})
	} else if !cooldownOK {
		actions = append(actions, map[string]any{"status": "skip", "action": "margin_order", "operation": "open", "reason": "derivatives_cooldown", "retry_after_sec": math.Max(minInterval-(now()-lastTradeAt), 0)})
	} else {
		actions = append(actions, map[string]any{"status": "skip", "action": "margin_order", "operation": "open", "reason": "no_positive_executable_plan", "plan": bestMap, "edge": edge})
	}

	status := "ok"
	if writeFailed || (len(errors) > 0 && len(contracts) == 0 && len(positionMaps) == 0) {
		status = "error"
	} else if writeAction {
		status = "ok"
	} else if !tradeEnabled {
		status = "analyze_only"
	} else if !writeAction {
		status = "idle"
	}
	analysis := map[string]any{
		"best": bestMap, "candidates": candidates, "contracts_count": len(contracts),
		"positions": positionMaps, "position_count": len(positionMaps), "trade_enabled": tradeEnabled,
		"cash": cash, "equity": equity, "open_margin": openMargin,
		"margin_budget": firstNonNil(bestMap["margin"], 0), "risk_edge": edge,
		"plan_executable": canOpen, "executable": canOpen && !writeAction, "protective_close_when_disabled": protectWhenOff,
		"formula":    "E[move]=abs(basis)*convergence; net=E[move]-2*fee-slippage-liquidation_penalty",
		"live_paths": map[string]any{"futures_get": true, "margin_positions_get": true, "margin_open_post": true, "margin_close_post": true},
	}
	out := result("derivatives", "期货/保证金", status, actions, analysis, errors, firstNonNil(bestMap["net_edge"], nil), map[string]any{
		"trade_enabled": tradeEnabled, "cash": cash, "equity": equity, "open_margin": openMargin,
		"margin_budget": analysis["margin_budget"], "risk_edge": edge, "executable": analysis["executable"],
		"contracts_count": len(contracts), "position_count": len(positionMaps), "impl": "go",
	})
	out["last_trade_at"] = lastTradeAt
	out["ts"] = float64(time.Now().UnixNano()) / 1e9
	_ = st.SetState("derivatives", out)
	return out
}
