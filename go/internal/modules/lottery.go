package modules

import (
	"fmt"

	"fzsmbot/internal/client"
	"fzsmbot/internal/config"
	"fzsmbot/internal/storage"
)

// RunLottery executes free-positive lottery actions and reports gated high-risk plays.
func RunLottery(cfg *config.Config, st *storage.Storage, c *client.Client, values map[string]any) map[string]any {
	lcfg := lotteryCfg(cfg)
	if !asBool(lcfg["enabled"], true) {
		return result("lottery", "lottery", "disabled", nil, nil, nil, nil, map[string]any{"enabled": false, "reason": "disabled"})
	}
	actions := []map[string]any{}
	errors := []string{}
	analysis := map[string]any{}

	me, err := c.LotteryMe()
	if err != nil {
		return result("lottery", "lottery", "error", nil, nil, []string{err.Error()}, nil, nil)
	}
	analysis["me"] = map[string]any{
		"remaining_lobster":        me["remaining_lobster"],
		"draws_available":          me["draws_available"],
		"draws_available_premium":  me["draws_available_premium"],
		"checked_today":            me["checked_today"],
		"streak":                   me["streak"],
		"draws_exchange_available": me["draws_exchange_available"],
	}

	// 1) free checkin
	if flagOn(values, "lottery.auto_checkin", asBool(lcfg["auto_checkin"], true)) {
		if asBool(me["checked_today"], false) {
			actions = append(actions, map[string]any{"status": "skip", "action": "checkin", "reason": "already_checked_or_disabled"})
		} else {
			raw, err := c.LotteryCheckin()
			if err != nil {
				errors = append(errors, fmt.Sprintf("checkin: %v", err))
				actions = append(actions, map[string]any{"status": "error", "action": "checkin", "reason": err.Error()})
			} else {
				actions = append(actions, map[string]any{"status": "ok", "action": "checkin", "raw": raw, "ev": "free_positive"})
				if me2, err2 := c.LotteryMe(); err2 == nil {
					me = me2
					analysis["me"] = map[string]any{
						"remaining_lobster":        me["remaining_lobster"],
						"draws_available":          me["draws_available"],
						"draws_available_premium":  me["draws_available_premium"],
						"checked_today":            me["checked_today"],
						"streak":                   me["streak"],
						"draws_exchange_available": me["draws_exchange_available"],
					}
				}
			}
		}
	} else {
		actions = append(actions, map[string]any{"status": "skip", "action": "checkin", "reason": "already_checked_or_disabled"})
	}

	// 2) free draws
	freeN := int(asFloat(me["draws_available"]))
	maxDraw := int(asFloat(lcfg["max_free_draws_per_cycle"]))
	if maxDraw <= 0 {
		maxDraw = 10
	}
	drawn := 0
	if flagOn(values, "lottery.auto_draw_free", asBool(lcfg["auto_draw_free"], true)) && freeN > 0 {
		n := freeN
		if n > maxDraw {
			n = maxDraw
		}
		for i := 0; i < n; i++ {
			raw, err := c.LotteryDraw(false)
			if err != nil {
				errors = append(errors, fmt.Sprintf("draw: %v", err))
				actions = append(actions, map[string]any{"status": "error", "action": "draw", "reason": err.Error()})
				break
			}
			actions = append(actions, map[string]any{"status": "ok", "action": "draw", "raw": raw, "ev": "free_ticket"})
			drawn++
		}
	} else {
		actions = append(actions, map[string]any{"status": "skip", "action": "draw", "reason": fmt.Sprintf("free_draws=%d", freeN)})
	}

	// 3) free premium draws
	if me3, err3 := c.LotteryMe(); err3 == nil {
		me = me3
	}
	premN := int(asFloat(me["draws_available_premium"]))
	maxPrem := int(asFloat(lcfg["max_free_premium_draws_per_cycle"]))
	if maxPrem <= 0 {
		maxPrem = 3
	}
	premDrawn := 0
	if flagOn(values, "lottery.auto_draw_premium_free", asBool(lcfg["auto_draw_premium_free"], true)) && premN > 0 {
		n := premN
		if n > maxPrem {
			n = maxPrem
		}
		for i := 0; i < n; i++ {
			raw, err := c.LotteryDraw(true)
			if err != nil {
				errors = append(errors, fmt.Sprintf("draw_premium: %v", err))
				actions = append(actions, map[string]any{"status": "error", "action": "draw_premium", "reason": err.Error()})
				break
			}
			actions = append(actions, map[string]any{"status": "ok", "action": "draw_premium", "raw": raw, "ev": "free_premium_ticket"})
			premDrawn++
		}
	} else {
		actions = append(actions, map[string]any{"status": "skip", "action": "draw_premium", "reason": fmt.Sprintf("premium_free=%d", premN)})
	}

	// 4) high variance remains gated: report only, no blind paid spins
	// Enrich with verified-read endpoints from Task2 probe.
	loanOffers := map[string]any{}
	slotCfg := map[string]any{}
	vipState := map[string]any{}
	if m, code, err := c.LotteryMap("/lottery/api/loan/offers"); err == nil && code < 400 {
		loanOffers = m
		delete(loanOffers, "_http_status")
		actions = append(actions, map[string]any{"status": "ok", "action": "loan_offers_loaded", "count": len(asSlice(firstNonNil(loanOffers["offers"], loanOffers["data"], loanOffers["items"])))})
	} else if err != nil {
		actions = append(actions, map[string]any{"status": "skip", "action": "loan_offers", "reason": "loan_root_unavailable_use_offers"})
	} else {
		actions = append(actions, map[string]any{"status": "skip", "action": "loan_offers", "reason": fmt.Sprintf("loan_offers_status=%d", code)})
	}
	if m, code, err := c.LotteryMap("/lottery/api/slot/config"); err == nil && code < 400 {
		slotCfg = m
		delete(slotCfg, "_http_status")
		actions = append(actions, map[string]any{"status": "ok", "action": "slot_config_loaded"})
	}
	if m, code, err := c.LotteryMap("/lottery/api/vip/state"); err == nil && code < 400 {
		vipState = m
		delete(vipState, "_http_status")
		actions = append(actions, map[string]any{"status": "ok", "action": "vip_state_loaded", "can_enter": vipState["can_enter"]})
	}

	// Plan B slot/nailong: analyze config RTP + samples; auto-spin only with proven edge.
	bal := asFloat(firstNonNil(me["remaining_lobster"], me["balance"]))
	slotActions, slotErrs, slotEdge := maybeAutoSlot(cfg, st, c, values, lcfg, slotCfg, bal)
	slotEdge = applyEdgeGate(values, slotEdge)
	actions = append(actions, slotActions...)
	errors = append(errors, slotErrs...)
	analysis["slot_edge"] = slotEdge
	// keep nailong alias status for UI; actual execution is shared with slot edge gate.
	if flagOn(values, "lottery.auto_nailong", asBool(lcfg["auto_nailong"], false)) && !flagOn(values, "lottery.auto_slot", asBool(lcfg["auto_slot"], false)) {
		actions = append(actions, map[string]any{"status": "ok", "action": "nailong", "reason": "routed_to_slot_edge_gate", "edge": slotEdge})
	}
	// High-risk paid games: same Plan-B edge gate as slot.
	yoloEdge := applyEdgeGate(values, evaluateYoloEdge(st, lcfg, bal))
	analysis["yolo_edge"] = yoloEdge
	if flagOn(values, "lottery.auto_yolo", asBool(lcfg["auto_yolo"], false)) {
		if !asBool(yoloEdge["edge_ok"], false) {
			actions = append(actions, map[string]any{"status": "skip", "action": "yolo", "reason": "paid_high_variance_no_edge", "edge": yoloEdge, "detail": yoloEdge["message"]})
		} else if bal < 100 {
			actions = append(actions, map[string]any{"status": "skip", "action": "yolo", "reason": "insufficient_balance_for_yolo", "balance": bal, "edge": yoloEdge})
		} else {
			raw, err := c.LotteryYolo()
			if err != nil {
				errors = append(errors, fmt.Sprintf("yolo: %v", err))
				actions = append(actions, map[string]any{"status": "error", "action": "yolo", "reason": err.Error(), "edge": yoloEdge})
			} else {
				delta := asFloat(firstNonNil(raw["delta_lobster"], raw["net_lobster"], raw["delta"]))
				win := asBool(raw["win"], delta > 0)
				if edgeHistoryOn(values) {
					yoloEdge = applyEdgeGate(values, recordRiskSample(st, "risk.edge.yolo", delta, win))
				} else {
					yoloEdge["history_skipped"] = true
				}
				analysis["yolo_edge"] = yoloEdge
				bal = asFloat(firstNonNil(raw["after_lobster"], bal+delta))
				actions = append(actions, map[string]any{
					"status": "ok", "action": "yolo",
					"delta": delta, "win": win, "dice": raw["dice"], "after": bal,
					"samples": yoloEdge["samples"], "edge": yoloEdge,
				})
			}
		}
	} else {
		actions = append(actions, map[string]any{"status": "analyze_only", "action": "yolo", "reason": "high_variance_default_off", "edge": yoloEdge})
	}

	vipBetEdge := applyEdgeGate(values, evaluateVipBetEdge(st, lcfg, vipState))
	analysis["vip_bet_edge"] = vipBetEdge
	if flagOn(values, "lottery.auto_vip", asBool(lcfg["auto_vip"], false)) {
		if asBool(vipState["can_enter"], false) {
			actions = append(actions, map[string]any{"status": "skip", "action": "vip", "reason": "vip_enter_ok_but_auto_join_disabled", "edge": vipBetEdge})
		} else {
			actions = append(actions, map[string]any{"status": "skip", "action": "vip", "reason": "vip_balance_or_gate_not_met", "edge": vipBetEdge})
		}
	} else {
		actions = append(actions, map[string]any{"status": "analyze_only", "action": "vip", "reason": "high_variance_default_off", "edge": vipBetEdge})
	}
	if flagOn(values, "lottery.auto_vip_bet", asBool(lcfg["auto_vip_bet"], false)) {
		// VIP bet needs active room/round context; keep explicit non-wiring reason.
		if !asBool(vipBetEdge["edge_ok"], false) {
			actions = append(actions, map[string]any{"status": "skip", "action": "vip_bet", "reason": "paid_high_variance_no_edge", "edge": vipBetEdge, "detail": vipBetEdge["message"]})
		} else {
			actions = append(actions, map[string]any{"status": "skip", "action": "vip_bet", "reason": "needs_active_vip_round_context", "edge": vipBetEdge, "detail": "VIP下注需房间/回合上下文，当前仅做门槛分析"})
		}
	} else {
		actions = append(actions, map[string]any{"status": "analyze_only", "action": "vip_bet", "reason": "high_variance_default_off", "edge": vipBetEdge})
	}

	borrowEdge := applyEdgeGate(values, evaluateBorrowEdge(st, lcfg, loanOffers))
	analysis["borrow_edge"] = borrowEdge
	if flagOn(values, "lottery.auto_borrow_zero_rate", asBool(lcfg["auto_borrow_zero_rate"], false)) {
		if !asBool(borrowEdge["edge_ok"], false) {
			actions = append(actions, map[string]any{"status": "skip", "action": "borrow", "reason": "default_no_auto_borrow", "edge": borrowEdge, "detail": borrowEdge["message"]})
		} else {
			amt := asFloat(lcfg["borrow_amount"])
			if amt <= 0 {
				amt = 1000
			}
			// prefer offer max if smaller/available
			if zmax := asFloat(borrowEdge["best_zero_amount"]); zmax > 0 && zmax < amt {
				amt = zmax
			}
			src := borrowEdge["best_zero_source"]
			if src == nil || fmt.Sprint(src) == "" || fmt.Sprint(src) == "<nil>" {
				actions = append(actions, map[string]any{"status": "skip", "action": "borrow", "reason": "zero_rate_source_missing", "edge": borrowEdge})
			} else {
				raw, err := c.LotteryBorrow(amt, src)
				if err != nil {
					errors = append(errors, fmt.Sprintf("borrow: %v", err))
					actions = append(actions, map[string]any{"status": "error", "action": "borrow", "reason": err.Error(), "edge": borrowEdge, "amount": amt, "source": src})
				} else {
					// treat successful zero-rate borrow as non-loss sample (delta 0 utility)
					if edgeHistoryOn(values) {
						borrowEdge = applyEdgeGate(values, recordRiskSample(st, "risk.edge.borrow", 0, true))
					} else {
						borrowEdge["history_skipped"] = true
					}
					analysis["borrow_edge"] = borrowEdge
					actions = append(actions, map[string]any{
						"status": "ok", "action": "borrow",
						"amount": amt, "source": src, "raw": raw,
						"samples": borrowEdge["samples"], "edge": borrowEdge,
					})
				}
			}
		}
	} else {
		actions = append(actions, map[string]any{"status": "analyze_only", "action": "borrow", "reason": "default_no_auto_borrow", "edge": borrowEdge})
	}

	analysis["loan_offers"] = loanOffers
	analysis["slot_config"] = slotCfg
	analysis["vip_state"] = vipState
	analysis["path_notes"] = []string{
		"loan root /lottery/api/loan = 404; use /lottery/api/loan/offers",
		"deposit root /lottery/api/deposit = 404",
		"vip/rooms = 404; use /lottery/api/vip/state",
	}

	// refresh me after draws
	if me4, err4 := c.LotteryMe(); err4 == nil {
		me = me4
		analysis["me"] = map[string]any{
			"remaining_lobster":        me["remaining_lobster"],
			"draws_available":          me["draws_available"],
			"draws_available_premium":  me["draws_available_premium"],
			"checked_today":            me["checked_today"],
			"streak":                   me["streak"],
			"draws_exchange_available": me["draws_exchange_available"],
		}
	}

	status := "ok"
	if len(errors) > 0 {
		status = "error"
	}
	return result("lottery", "lottery", status, actions, analysis, errors, nil, map[string]any{
		"free_draws":    int(asFloat(me["draws_available"])),
		"premium_free":  int(asFloat(me["draws_available_premium"])),
		"checked_today": me["checked_today"],
		"drawn":         drawn,
		"premium_drawn": premDrawn,
		"balance":       firstNonNil(me["remaining_lobster"], me["balance"]),
		"slot_edge":     analysis["slot_edge"],
		"yolo_edge":     analysis["yolo_edge"],
		"vip_bet_edge":  analysis["vip_bet_edge"],
		"borrow_edge":   analysis["borrow_edge"],
		"impl":          "go",
	})
}
