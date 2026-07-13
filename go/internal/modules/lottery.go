package modules

import (
	"strings"
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
			delta := asFloat(firstNonNil(raw["delta_lobster"], raw["net_lobster"], raw["delta"], raw["reward_lobster"], raw["prize_lobster"]))
			win := asBool(raw["win"], delta > 0)
			if edgeHistoryOn(values) {
				recordTraceSample(st, "risk.obs.free_draw", "free_draw", delta, win, map[string]any{"source": "self_exec", "premium": false})
			}
			actions = append(actions, map[string]any{"status": "ok", "action": "draw", "raw": raw, "ev": "free_ticket", "delta": delta, "win": win})
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
			delta := asFloat(firstNonNil(raw["delta_lobster"], raw["net_lobster"], raw["delta"], raw["reward_lobster"], raw["prize_lobster"]))
			win := asBool(raw["win"], delta > 0)
			if edgeHistoryOn(values) {
				recordTraceSample(st, "risk.obs.free_draw_premium", "free_draw_premium", delta, win, map[string]any{"source": "self_exec", "premium": true})
			}
			actions = append(actions, map[string]any{"status": "ok", "action": "draw_premium", "raw": raw, "ev": "free_premium_ticket", "delta": delta, "win": win})
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
	vipMyRoom := map[string]any{}
	vipStats := map[string]any{}
	vipHistory := map[string]any{}
	vipCtx := map[string]any{"state_ok": false, "bet_path_available": false}
	if m, code, err := c.LotteryMap("/lottery/api/vip/state"); err == nil && code < 400 {
		vipState = m
		delete(vipState, "_http_status")
		vipCtx["state_ok"] = true
		actions = append(actions, map[string]any{"status": "ok", "action": "vip_state_loaded", "can_enter": vipState["can_enter"]})
	} else if err != nil {
		actions = append(actions, map[string]any{"status": "skip", "action": "vip_state", "reason": "vip_state_unavailable", "detail": err.Error()})
	} else {
		actions = append(actions, map[string]any{"status": "skip", "action": "vip_state", "reason": fmt.Sprintf("vip_state_status=%d", code)})
	}
	if m, code, err := c.LotteryMap("/lottery/api/vip/my-room"); err == nil && code < 400 {
		vipMyRoom = m
		delete(vipMyRoom, "_http_status")
		actions = append(actions, map[string]any{"status": "ok", "action": "vip_my_room_loaded", "room_id": vipMyRoom["room_id"]})
	} else if err != nil {
		actions = append(actions, map[string]any{"status": "skip", "action": "vip_my_room", "reason": "vip_my_room_unavailable", "detail": err.Error()})
	} else {
		actions = append(actions, map[string]any{"status": "skip", "action": "vip_my_room", "reason": fmt.Sprintf("vip_my_room_status=%d", code)})
	}
	if m, code, err := c.LotteryMap("/lottery/api/vip/stats"); err == nil && code < 400 {
		vipStats = m
		delete(vipStats, "_http_status")
		actions = append(actions, map[string]any{"status": "ok", "action": "vip_stats_loaded", "total_rounds": vipStats["total_rounds"]})
	}
	if m, code, err := c.LotteryMap("/lottery/api/vip/history"); err == nil && code < 400 {
		vipHistory = m
		delete(vipHistory, "_http_status")
		hN := len(asSlice(firstNonNil(vipHistory["items"], vipHistory["data"], vipHistory["history"])))
		actions = append(actions, map[string]any{"status": "ok", "action": "vip_history_loaded", "count": hN})
	}
	// nested room/round write paths proven; runtime sets write_paths accordingly.
	vipCtx = buildVipContext(vipState, vipMyRoom, vipStats, vipHistory)
	analysis["vip_context"] = vipCtx
	actions = append(actions, map[string]any{
		"status": "ok", "action": "vip_context_probed",
		"has_room": vipCtx["has_room"], "has_active_round": vipCtx["has_active_round"],
		"context_label": vipCtx["context_label"], "bet_path_available": vipCtx["bet_path_available"],
	})

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

	// Live-proven nested VIP write paths (from official page JS + HTTP probe):
	// POST /vip/rooms/{id}/join|ready|start|leave , POST /vip/rounds/{id}/bet
	// Formal seat still requires min_balance; spectator join works without seat rights.
	vipCtx["join_path_available"] = true
	vipCtx["bet_path_available"] = true
	vipCtx["write_paths"] = map[string]any{
		"room_detail": true,
		"join":        true,
		"ready":       true,
		"start":       true,
		"leave":       true,
		"bet":         true,
		"create":      true,
		"note":        "嵌套写路径已实战确认；正式入座受 min_balance 门槛",
	}
	vipBetEdge := applyEdgeGate(values, evaluateVipBetEdge(st, lcfg, vipState, vipCtx))
	analysis["vip_bet_edge"] = vipBetEdge

	autoVip := flagOn(values, "lottery.auto_vip", asBool(lcfg["auto_vip"], false))
	autoVipBet := flagOn(values, "lottery.auto_vip_bet", asBool(lcfg["auto_vip_bet"], false))
	allowSpectator := asBool(lcfg["auto_vip_spectator"], false)
	selected := pickVipRoom(st, vipState, vipCtx, bal)
	vipCtx["selected_room"] = selected
	analysis["vip_selected_room"] = selected
	analysis["vip_rooms"] = asSlice(firstNonNil(vipState["rooms"], vipState["public_rooms"], vipState["list"]))
	analysis["vip_auto_select"] = map[string]any{
		"enabled_when": "no manual vip.room_pref",
		"rules": []string{
			"仅公开房，跳过密码/满员",
			"优先 waiting/betting/ready",
			"优先底注可负担",
			"同条件优先空位多、更新时间新",
		},
		"selected_source": firstNonNil(asMap(selected)["_source"], "none"),
		"selected_strategy": firstNonNil(asMap(selected)["_strategy"], "none"),
		"note": "无手动选房时自动排序；正式进房仍受 auto_vip 开关、余额门槛与方案B门控",
	}
	if st != nil {
		analysis["vip_room_pref"] = st.GetStateMap("vip.room_pref")
	}

	if autoVip {
		if !asBool(vipState["can_enter"], false) && !allowSpectator {
			actions = append(actions, map[string]any{
				"status": "skip", "action": "vip_join", "reason": "vip_balance_or_gate_not_met",
				"detail": vipBetEdge["message"], "edge": vipBetEdge, "context": vipCtx, "selected": selected,
			})
		} else if selected == nil || firstNonNil(selected["id"], selected["room_id"]) == nil {
			actions = append(actions, map[string]any{
				"status": "skip", "action": "vip_join", "reason": "vip_no_suitable_room",
				"detail": "无可自动加入的公开房间", "edge": vipBetEdge, "context": vipCtx,
			})
		} else {
			rid := firstNonNil(selected["id"], selected["room_id"])
			asSpec := !asBool(vipState["can_enter"], false) && allowSpectator
			raw, err := c.LotteryVipJoin(rid, asSpec, "")
			if err != nil {
				errors = append(errors, fmt.Sprintf("vip_join: %v", err))
				actions = append(actions, map[string]any{
					"status": "error", "action": "vip_join", "reason": err.Error(),
					"room_id": rid, "as_spectator": asSpec, "edge": vipBetEdge, "selected": selected,
				})
			} else {
				actions = append(actions, map[string]any{
					"status": "ok", "action": "vip_join", "room_id": rid, "as_spectator": asSpec,
					"detail": map[string]any{"me_is_spectator": raw["me_is_spectator"], "room": raw["room"]},
					"edge":   vipBetEdge, "selected": selected,
				})
				if det, err2 := c.LotteryVipRoom(rid); err2 == nil {
					vipCtx["room_detail"] = det
					if rm := asMap(det["room"]); len(rm) > 0 {
						vipCtx["room_id"] = rm["id"]
						vipCtx["room_status"] = rm["status"]
						vipCtx["has_room"] = true
					}
					if rnd := asMap(det["round"]); len(rnd) > 0 {
						vipCtx["has_active_round"] = true
						vipCtx["round_id"] = firstNonNil(rnd["id"], rnd["round_id"])
						vipCtx["round_status"] = rnd["status"]
					}
				}
			}
		}
	} else {
		actions = append(actions, map[string]any{
			"status": "analyze_only", "action": "vip_join", "reason": "high_variance_default_off",
			"detail": "进VIP开关关闭；真实路径 /vip/rooms/{id}/join 已确认", "edge": vipBetEdge, "selected": selected, "context": vipCtx,
		})
	}

	if autoVipBet {
		if !asBool(vipState["can_enter"], false) {
			actions = append(actions, map[string]any{
				"status": "skip", "action": "vip_bet", "reason": "vip_balance_or_gate_not_met",
				"detail": "正式入座/下注需要余额达到贵宾厅门槛；观战不能下注",
				"edge":   vipBetEdge, "context": vipCtx, "selected": selected, "executable": false,
			})
		} else if !asBool(vipBetEdge["edge_ok"], false) {
			reason := "paid_high_variance_no_edge"
			switch fmt.Sprint(vipBetEdge["gate"]) {
			case "vip_no_room":
				reason = "vip_no_room"
			case "vip_missing_round":
				reason = "vip_missing_round"
			case "vip_gate_not_met":
				reason = "vip_balance_or_gate_not_met"
			}
			actions = append(actions, map[string]any{
				"status": "skip", "action": "vip_bet", "reason": reason,
				"detail": vipBetEdge["message"], "edge": vipBetEdge, "context": vipCtx, "selected": selected, "executable": false,
			})
		} else {
			rid := firstNonNil(vipCtx["room_id"], selected["id"], selected["room_id"])
			roundID := firstNonNil(vipCtx["round_id"])
			if (roundID == nil || fmt.Sprint(roundID) == "" || fmt.Sprint(roundID) == "<nil>") && rid != nil {
				if det, err := c.LotteryVipRoom(rid); err == nil {
					rnd := asMap(det["round"])
					roundID = firstNonNil(rnd["id"], rnd["round_id"])
					vipCtx["room_detail"] = det
					vipCtx["round_id"] = roundID
					vipCtx["round_status"] = rnd["status"]
				}
			}
			if roundID == nil || fmt.Sprint(roundID) == "" || fmt.Sprint(roundID) == "<nil>" {
				actions = append(actions, map[string]any{
					"status": "skip", "action": "vip_bet", "reason": "vip_missing_round",
					"detail": "真实下注路径 /vip/rounds/{id}/bet 已确认，但当前无活跃回合",
					"edge":   vipBetEdge, "context": vipCtx, "selected": selected,
				})
			} else {
				mult := int(asFloat(lcfg["vip_bet_multiplier"]))
				if mult <= 0 {
					mult = 1
				}
				raw, err := c.LotteryVipBet(roundID, mult)
				if err != nil {
					errors = append(errors, fmt.Sprintf("vip_bet: %v", err))
					actions = append(actions, map[string]any{
						"status": "error", "action": "vip_bet", "reason": err.Error(),
						"round_id": roundID, "bet_multiplier": mult, "edge": vipBetEdge,
					})
				} else {
					if edgeHistoryOn(values) {
						delta := asFloat(firstNonNil(raw["delta_lobster"], raw["net_lobster"], raw["delta"]))
						win := asBool(raw["win"], delta > 0)
						vipBetEdge = applyEdgeGate(values, recordRiskSample(st, "risk.edge.vip_bet", delta, win))
						analysis["vip_bet_edge"] = vipBetEdge
					}
					actions = append(actions, map[string]any{
						"status": "ok", "action": "vip_bet", "round_id": roundID, "bet_multiplier": mult,
						"raw": raw, "edge": vipBetEdge,
					})
				}
			}
		}
	} else {
		actions = append(actions, map[string]any{
			"status": "analyze_only", "action": "vip_bet", "reason": "high_variance_default_off",
			"detail": "VIP下注开关关闭；真实路径 /vip/rounds/{id}/bet 已确认", "edge": vipBetEdge, "context": vipCtx, "selected": selected,
			"executable": false, "write_path_available": true,
		})
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
	analysis["vip_my_room"] = vipMyRoom
	analysis["vip_stats"] = vipStats
	analysis["vip_history"] = vipHistory
	analysis["vip_context"] = vipCtx
	// Observe user manual VIP activity on website even when bot cannot auto join/bet.
	vipManual := recordVipManualActivity(st, vipCtx, vipStats, vipHistory)
	analysis["vip_manual"] = vipManual
	vipObs := recordVipSpectatorSamples(st, c, vipCtx, vipStats, vipHistory, values, lcfg)
	analysis["vip_observe"] = vipObs
	if n := int(asFloat(vipObs["new_samples"])); n > 0 {
		actions = append(actions, map[string]any{"status": "ok", "action": "vip_observe_samples", "count": n, "total": vipObs["samples"], "detail": vipObs["message"]})
	} else {
		actions = append(actions, map[string]any{"status": "ok", "action": "vip_observe_watch", "reason": "waiting_round_outcomes", "detail": vipObs["message"]})
	}
	if n := len(asSlice(vipManual["events"])); n > 0 {
		actions = append(actions, map[string]any{"status": "ok", "action": "vip_manual_observed", "count": n, "latest": vipManual["latest"]})
	} else {
		actions = append(actions, map[string]any{"status": "ok", "action": "vip_manual_watch", "reason": "waiting_user_web_activity", "detail": "bot不自动VIP；网页手动进房/对局会通过只读接口记录"})
	}
	analysis["sample_sources"] = map[string]any{
		"free_draw": "risk.obs.free_draw",
		"free_draw_premium": "risk.obs.free_draw_premium",
		"slot": "lottery.slot_edge",
		"yolo": "risk.edge.yolo",
		"borrow": "risk.edge.borrow",
		"vip_bet": "risk.edge.vip_bet",
		"vip_obs": "risk.edge.vip_obs",
		"farm_harvest": "risk.obs.farm_harvest",
		"farm_steal": "risk.obs.farm_steal",
	}
	analysis["path_notes"] = []string{
		"loan: GET /loan/offers + POST /loan + POST /loan/repay|/loan/repay_all (flat paths exist; 403=业务限制非路由缺失)",
		"deposit: POST /deposit + /deposit/withdraw + /deposit/rollover (存在；冷却/无存单会业务拒绝)",
		"offers: POST /offers 创建; DELETE /offers/mine 取消挂单",
		"vip nested writes: /vip/rooms/{id}/join|ready|start|leave + /vip/rounds/{id}/bet (flat /vip/join|/vip/bet=404)",
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
		"vip_manual":    analysis["vip_manual"],
		"vip_observe":   analysis["vip_observe"],
		"borrow_edge":   analysis["borrow_edge"],
		"impl":          "go",
	})
}

// buildVipContext merges VIP readonly probes into a Chinese-friendly context map.
// No write/bet execution here.
func buildVipContext(state, myRoom, stats, history map[string]any) map[string]any {
	state = asMap(state)
	myRoom = asMap(myRoom)
	stats = asMap(stats)
	history = asMap(history)

	canEnter := asBool(state["can_enter"], false)
	minBal := asFloat(state["min_balance"])
	bal := asFloat(firstNonNil(state["balance_lobster"], state["balance"]))
	rooms := asSlice(firstNonNil(state["rooms"], state["public_rooms"], state["list"]))
	publicN := len(rooms)

	roomID := firstNonNil(myRoom["room_id"], myRoom["id"], myRoom["roomId"])
	hasRoom := roomID != nil && fmt.Sprint(roomID) != "" && fmt.Sprint(roomID) != "<nil>" && fmt.Sprint(roomID) != "null" && fmt.Sprint(roomID) != "0"
	roomStatus := ""
	var roomObj map[string]any
	if hasRoom {
		// if my-room embeds room object
		roomObj = asMap(firstNonNil(myRoom["room"], myRoom["data"]))
		roomStatus = fmt.Sprint(firstNonNil(roomObj["status"], myRoom["status"], ""))
		// also match public rooms list for status
		for _, it := range rooms {
			m := asMap(it)
			id := firstNonNil(m["id"], m["room_id"])
			if fmt.Sprint(id) == fmt.Sprint(roomID) {
				roomObj = m
				roomStatus = fmt.Sprint(firstNonNil(m["status"], roomStatus))
				break
			}
		}
	}

	// Round comes from GET /vip/rooms/{id}.round; also infer from room status when needed.
	hasRound := false
	rs := strings.ToLower(strings.TrimSpace(roomStatus))
	if hasRoom && (rs == "playing" || rs == "betting" || rs == "in_round" || rs == "running" || rs == "active") {
		hasRound = true
	}
	histItems := asSlice(firstNonNil(history["items"], history["data"], history["history"]))
	totalRounds := int(asFloat(firstNonNil(stats["total_rounds"], stats["rounds"])))

	label := "未探测"
	summary := ""
	switch {
	case len(state) == 0:
		label = "状态不可用"
		summary = "VIP状态接口不可用"
	case hasRoom && hasRound:
		label = "有房间有回合"
		summary = fmt.Sprintf("房间%s · 状态%s · 可分析回合", fmt.Sprint(roomID), roomStatus)
	case hasRoom && !hasRound:
		label = "有房间缺回合"
		if roomStatus != "" && roomStatus != "<nil>" {
			summary = fmt.Sprintf("房间%s · 状态%s · 无活跃回合接口", fmt.Sprint(roomID), roomStatus)
		} else {
			summary = fmt.Sprintf("房间%s · 无活跃回合接口", fmt.Sprint(roomID))
		}
	case !hasRoom && canEnter:
		label = "无房间可进房"
		summary = fmt.Sprintf("公开房%d · 门槛%.0f · 尚未加入", publicN, minBal)
	default:
		label = "无房间"
		if minBal > 0 && bal > 0 && bal < minBal {
			summary = fmt.Sprintf("余额%.0f < 门槛%.0f · 公开房%d", bal, minBal, publicN)
		} else {
			summary = fmt.Sprintf("不可进房 · 公开房%d · 门槛%.0f", publicN, minBal)
		}
	}

	return map[string]any{
		"ts":                 now(),
		"state_ok":           len(state) > 0,
		"state":              state,
		"my_room":            myRoom,
		"stats":              stats,
		"history_count":      len(histItems),
		"total_rounds":       totalRounds,
		"can_enter":          canEnter,
		"min_balance":        minBal,
		"balance_lobster":    bal,
		"public_room_count":  publicN,
		"room_id":            roomID,
		"room_status":        roomStatus,
		"has_room":           hasRoom,
		"has_active_round":   hasRound,
		"bet_path_available":  true,
		"join_path_available": true,
		"write_paths": map[string]any{
			"join":  true,
			"ready": true,
			"start": true,
			"bet":   true,
			"leave": true,
			"note":  "嵌套写路径已实战确认；正式入座受 min_balance",
		},
		"context_label":   label,
		"context_summary": summary,
		"probe": map[string]any{
			"state":   len(state) > 0,
			"my_room": len(myRoom) > 0,
			"stats":   len(stats) > 0,
			"history": len(history) > 0,
			"rooms":   false,
			"rounds":  false,
			"bet":     false,
			"join":    false,
		},
	}
}


// recordVipManualActivity diffs readonly VIP probes against previous local snapshot.
// Purpose: user may operate VIP on the official webpage; bot cannot write join/bet,
// but still must persist observed manual activity locally.
func recordVipManualActivity(st *storage.Storage, ctx, stats, history map[string]any) map[string]any {
	ctx = asMap(ctx)
	stats = asMap(stats)
	history = asMap(history)
	prev := map[string]any{}
	if st != nil {
		prev = st.GetStateMap("manual.vip")
	}
	prevSnap := asMap(prev["snapshot"])
	events := asSlice(prev["events"])

	roomID := firstNonNil(ctx["room_id"], asMap(ctx["my_room"])["room_id"])
	prevRoom := firstNonNil(prevSnap["room_id"])
	hasRoom := roomID != nil && fmt.Sprint(roomID) != "" && fmt.Sprint(roomID) != "<nil>" && fmt.Sprint(roomID) != "null" && fmt.Sprint(roomID) != "0"
	prevHas := prevRoom != nil && fmt.Sprint(prevRoom) != "" && fmt.Sprint(prevRoom) != "<nil>" && fmt.Sprint(prevRoom) != "null" && fmt.Sprint(prevRoom) != "0"

	ts := now()
	newEvents := []any{}
	// room join/leave
	if hasRoom && !prevHas {
		newEvents = append(newEvents, map[string]any{
			"ts": ts, "source": "web_manual_or_external", "kind": "enter_room",
			"title": "检测到进房", "detail": fmt.Sprintf("房间 %v · 状态 %v", roomID, ctx["room_status"]),
			"room_id": roomID, "room_status": ctx["room_status"],
		})
	} else if !hasRoom && prevHas {
		newEvents = append(newEvents, map[string]any{
			"ts": ts, "source": "web_manual_or_external", "kind": "leave_room",
			"title": "检测到离房", "detail": fmt.Sprintf("离开房间 %v", prevRoom),
			"room_id": prevRoom,
		})
	} else if hasRoom && prevHas && fmt.Sprint(roomID) != fmt.Sprint(prevRoom) {
		newEvents = append(newEvents, map[string]any{
			"ts": ts, "source": "web_manual_or_external", "kind": "switch_room",
			"title": "检测到换房", "detail": fmt.Sprintf("%v -> %v", prevRoom, roomID),
			"from": prevRoom, "to": roomID,
		})
	}

	// stats deltas
	curRounds := int(asFloat(firstNonNil(stats["total_rounds"], stats["rounds"])))
	prevRounds := int(asFloat(prevSnap["total_rounds"]))
	curWins := int(asFloat(stats["wins"]))
	prevWins := int(asFloat(prevSnap["wins"]))
	curLosses := int(asFloat(stats["losses"]))
	prevLosses := int(asFloat(prevSnap["losses"]))
	curNet := asFloat(stats["net_lobster"])
	prevNet := asFloat(prevSnap["net_lobster"])
	if len(prevSnap) > 0 {
		if curRounds > prevRounds {
			newEvents = append(newEvents, map[string]any{
				"ts": ts, "source": "web_manual_or_external", "kind": "rounds_progress",
				"title": "检测到对局进度", "detail": fmt.Sprintf("总局数 %d -> %d", prevRounds, curRounds),
				"from": prevRounds, "to": curRounds,
			})
		}
		if curWins > prevWins || curLosses > prevLosses {
			newEvents = append(newEvents, map[string]any{
				"ts": ts, "source": "web_manual_or_external", "kind": "match_result_counter",
				"title": "检测到胜负变化", "detail": fmt.Sprintf("胜 %d->%d · 负 %d->%d", prevWins, curWins, prevLosses, curLosses),
				"wins": curWins, "losses": curLosses,
			})
		}
		if curNet != prevNet {
			newEvents = append(newEvents, map[string]any{
				"ts": ts, "source": "web_manual_or_external", "kind": "net_change",
				"title": "检测到VIP净龙虾变化", "detail": fmt.Sprintf("%.0f -> %.0f (Δ%.0f)", prevNet, curNet, curNet-prevNet),
				"from": prevNet, "to": curNet, "delta": curNet - prevNet,
			})
		}
	}

	// history item fingerprints
	histItems := asSlice(firstNonNil(history["items"], history["data"], history["history"]))
	prevHistKeys := map[string]bool{}
	for _, it := range asSlice(prevSnap["history_keys"]) {
		prevHistKeys[fmt.Sprint(it)] = true
	}
	curKeys := []any{}
	added := 0
	for _, it := range histItems {
		m := asMap(it)
		key := fmt.Sprint(firstNonNil(m["id"], m["round_id"], m["ts"], m["created_at"], m["time"], m))
		curKeys = append(curKeys, key)
		if len(prevSnap) > 0 && key != "" && !prevHistKeys[key] {
			added++
			title := "检测到新VIP历史"
			detail := key
			if v := firstNonNil(m["result"], m["outcome"], m["win"]); v != nil {
				detail = fmt.Sprintf("%v · %v", key, v)
			}
			if d := firstNonNil(m["delta_lobster"], m["net_lobster"], m["pnl"], m["profit"]); d != nil {
				detail = fmt.Sprintf("%s · 变动 %v", detail, d)
			}
			newEvents = append(newEvents, map[string]any{
				"ts": ts, "source": "web_manual_or_external", "kind": "history_item",
				"title": title, "detail": detail, "item": m,
			})
		}
	}
	_ = added

	// prepend new events
	if len(newEvents) > 0 {
		events = append(newEvents, events...)
	}
	if len(events) > 50 {
		events = events[:50]
	}
	var latest any
	if len(events) > 0 {
		latest = events[0]
	}
	out := map[string]any{
		"ts":             ts,
		"watch_only":     true,
		"bot_can_write":  false,
		"note":           "bot不自动VIP写操作；网页手动进房/对局通过只读接口差分记录到本地",
		"events":         events,
		"latest":         latest,
		"event_count":    len(events),
		"new_event_count": len(newEvents),
		"snapshot": map[string]any{
			"room_id":       roomID,
			"room_status":   ctx["room_status"],
			"has_room":      hasRoom,
			"total_rounds":  curRounds,
			"wins":          curWins,
			"losses":        curLosses,
			"net_lobster":   curNet,
			"history_keys":  curKeys,
			"history_count": len(histItems),
			"can_enter":     ctx["can_enter"],
			"balance":       ctx["balance_lobster"],
		},
	}
	if st != nil {
		_ = st.SetState("manual.vip", out)
		// also keep a short global manual feed
		feed := st.GetStateMap("manual.activity")
		feedEvents := asSlice(feed["events"])
		if len(newEvents) > 0 {
			// tag module
			tagged := []any{}
			for _, e := range newEvents {
				m := asMap(e)
				m["module"] = "vip"
				tagged = append(tagged, m)
			}
			feedEvents = append(tagged, feedEvents...)
			if len(feedEvents) > 80 {
				feedEvents = feedEvents[:80]
			}
		}
		feed["ts"] = ts
		feed["events"] = feedEvents
		feed["note"] = "跨模块网页手动/外部活动观测（本地持久化）"
		_ = st.SetState("manual.activity", feed)
	}
	return out
}



// pickVipRoom prefers panel manual room preference, otherwise auto-selects a public room.
// Auto strategy (no manual pref):
// 1) public only, no password
// 2) not full
// 3) prefer waiting/betting/ready
// 4) prefer rooms whose base_bet is affordable
// 5) among affordable, prefer more open seats and fresher update time
// 6) if none affordable, still pick best public room for analysis/spectator path, marked unaffordable
func pickVipRoom(st *storage.Storage, state, ctx map[string]any, balance float64) map[string]any {
	state = asMap(state)
	rooms := asSlice(firstNonNil(state["rooms"], state["public_rooms"], state["list"]))
	pref := map[string]any{}
	if st != nil {
		pref = st.GetStateMap("vip.room_pref")
	}
	prefID := strings.TrimSpace(fmt.Sprint(firstNonNil(pref["room_id"], pref["id"], "")))
	if prefID != "" && prefID != "<nil>" && prefID != "null" && prefID != "0" {
		for _, it := range rooms {
			m := asMap(it)
			id := strings.TrimSpace(fmt.Sprint(firstNonNil(m["id"], m["room_id"], "")))
			if id == "" || id != prefID {
				continue
			}
			if asBool(m["has_password"], false) {
				break
			}
			maxP := asFloat(firstNonNil(m["max_players"], 0))
			curP := asFloat(firstNonNil(m["player_count"], m["players"], 0))
			if maxP > 0 && curP >= maxP {
				break
			}
			out := map[string]any{}
			for k, v := range m {
				out[k] = v
			}
			out["_score"] = 999.0
			out["_source"] = "manual_pref"
			out["_pref_mode"] = firstNonNil(pref["mode"], "preferred")
			out["_strategy"] = "manual_pref"
			return out
		}
	}
	var best map[string]any
	bestScore := -1e18
	candN := 0
	for _, it := range rooms {
		m := asMap(it)
		if len(m) == 0 {
			continue
		}
		if asBool(m["has_password"], false) || asBool(m["is_private"], false) {
			continue
		}
		status := strings.ToLower(fmt.Sprint(firstNonNil(m["status"], "")))
		maxP := asFloat(firstNonNil(m["max_players"], 0))
		curP := asFloat(firstNonNil(m["player_count"], m["players"], 0))
		if maxP > 0 && curP >= maxP {
			continue
		}
		candN++
		base := asFloat(firstNonNil(m["base_bet_lobster"], m["base_bet"], 0))
		score := 0.0
		// active/joinable statuses first
		switch status {
		case "waiting":
			score += 8
		case "betting", "ready":
			score += 6
		case "playing":
			score += 2
		}
		// more open seats better
		if maxP > 0 {
			score += (maxP - curP) * 1.5
		} else {
			score += 1
		}
		// affordable base bet strongly preferred
		affordable := base <= 0 || balance >= base
		if affordable {
			score += 20
			if base > 0 {
				// smaller affordable base is easier to enter
				score += 3.0 / (1.0 + base/1e8)
			}
		} else {
			score -= 25
		}
		// fresher room slightly better
		updated := asFloat(firstNonNil(m["updated_at"], m["created_at"], 0))
		if updated > 0 {
			score += updated / 1e12
		}
		if score > bestScore {
			bestScore = score
			best = map[string]any{}
			for k, v := range m {
				best[k] = v
			}
			best["_score"] = score
			best["_source"] = "auto"
			best["_strategy"] = "public_affordable_open_seats"
			best["_affordable"] = affordable
			best["_candidate_count"] = candN
		}
	}
	if best != nil {
		best["_candidate_count"] = candN
	}
	return best
}


// recordVipSpectatorSamples collects observational VIP samples while spectating/watching.
// No bets are placed. Stored under risk.edge.vip_obs, separate from personal vip_bet samples.
func recordVipSpectatorSamples(st *storage.Storage, c *client.Client, ctx, stats, history map[string]any, values, lcfg map[string]any) map[string]any {
	ctx = asMap(ctx)
	stats = asMap(stats)
	history = asMap(history)
	enabled := flagOn(values, "lottery.auto_vip_observe", asBool(lcfg["auto_vip_observe"], true))
	if !enabled {
		return map[string]any{"enabled": false, "new_samples": 0, "samples": 0, "message": "观战样本收集开关关闭"}
	}
	if !edgeHistoryOn(values) {
		return map[string]any{"enabled": true, "new_samples": 0, "samples": 0, "message": "样本历史总开关关闭，观战样本不落盘"}
	}
	prev := map[string]any{}
	if st != nil {
		prev = loadRiskEdge(st, "risk.edge.vip_obs")
	}
	samples := int(asFloat(prev["samples"]))
	wins := int(asFloat(prev["wins"]))
	sumDelta := asFloat(prev["sum_delta"])
	seen := map[string]bool{}
	for _, it := range asSlice(prev["seen_keys"]) {
		seen[fmt.Sprint(it)] = true
	}
	hist := asSlice(prev["history"])
	newN := 0
	ts := now()

	rid := firstNonNil(ctx["room_id"], asMap(ctx["selected_room"])["id"], asMap(ctx["selected_room"])["room_id"])
	if rid != nil && fmt.Sprint(rid) != "" && fmt.Sprint(rid) != "<nil>" && fmt.Sprint(rid) != "null" && c != nil {
		if det, err := c.LotteryVipRoom(rid); err == nil {
			ctx["room_detail"] = det
			rnd := asMap(det["round"])
			hands := asSlice(firstNonNil(det["hands"], rnd["hands"]))
			rst := strings.ToLower(fmt.Sprint(firstNonNil(rnd["status"], "")))
			rk := fmt.Sprintf("room:%v:round:%v:%v", rid, firstNonNil(rnd["id"], rnd["round_id"], ""), rst)
			if len(rnd) > 0 && !seen[rk] && (rst == "settled" || rst == "finished" || rst == "revealed" || rst == "done" || rst == "closed") {
				delta := 0.0
				winN, loseN := 0, 0
				for _, h := range hands {
					hm := asMap(h)
					d := asFloat(firstNonNil(hm["delta_lobster"], hm["pnl"], hm["profit"], hm["net"]))
					delta += d
					if asBool(hm["win"], d > 0) {
						winN++
					} else if d < 0 {
						loseN++
					}
				}
				win := delta > 0 || winN > loseN
				samples++
				sumDelta += delta
				if win {
					wins++
				}
				newN++
				seen[rk] = true
				item := map[string]any{"ts": ts, "delta": delta, "win": win, "source": "spectator_room_round", "room_id": rid, "round": firstNonNil(rnd["id"], rnd["round_id"]), "key": rk}
				hist = append([]any{item}, hist...)
			}
			ctx["round_id"] = firstNonNil(rnd["id"], rnd["round_id"])
			ctx["round_status"] = rst
			ctx["me_is_spectator"] = asBool(det["me_is_spectator"], false)
		}
	}

	for _, it := range asSlice(firstNonNil(history["items"], history["data"], history["history"])) {
		m := asMap(it)
		key := fmt.Sprint(firstNonNil(m["id"], m["round_id"], m["ts"], m["created_at"], m))
		if key == "" || key == "<nil>" || seen[key] {
			continue
		}
		delta := asFloat(firstNonNil(m["delta_lobster"], m["net_lobster"], m["pnl"], m["profit"], m["delta"]))
		win := asBool(firstNonNil(m["win"], m["is_win"]), delta > 0)
		samples++
		sumDelta += delta
		if win {
			wins++
		}
		newN++
		seen[key] = true
		item := map[string]any{"ts": ts, "delta": delta, "win": win, "source": "spectator_history", "key": key}
		hist = append([]any{item}, hist...)
	}

	if newN == 0 && len(stats) > 0 {
		prevSnap := asMap(prev["stats_snap"])
		curRounds := int(asFloat(firstNonNil(stats["total_rounds"], stats["rounds"])))
		prevRounds := int(asFloat(prevSnap["total_rounds"]))
		curNet := asFloat(stats["net_lobster"])
		prevNet := asFloat(prevSnap["net_lobster"])
		if len(prevSnap) > 0 && curRounds > prevRounds {
			delta := curNet - prevNet
			win := delta > 0
			samples++
			sumDelta += delta
			if win {
				wins++
			}
			newN++
			key := fmt.Sprintf("stats:%d:%d:%.0f", prevRounds, curRounds, curNet)
			seen[key] = true
			item := map[string]any{"ts": ts, "delta": delta, "win": win, "source": "spectator_stats_delta", "key": key}
			hist = append([]any{item}, hist...)
		}
	}

	if len(hist) > 30 {
		hist = hist[:30]
	}
	seenKeys := make([]any, 0, len(seen))
	for k := range seen {
		seenKeys = append(seenKeys, k)
		if len(seenKeys) >= 200 {
			break
		}
	}
	obsEV := 0.0
	winRate := 0.0
	if samples > 0 {
		obsEV = sumDelta / float64(samples)
		winRate = float64(wins) / float64(samples)
	}
	msg := "观战/只读观察样本收集中（不下注）"
	if samples > 0 {
		msg = fmt.Sprintf("观战样本 %d（本轮新+%d）· 观察EV %.2f · 胜率 %.1f%%", samples, newN, obsEV, winRate*100)
	}
	edge := map[string]any{
		"ts":          ts,
		"kind":        "vip_obs",
		"source":      "spectator_observe",
		"samples":     samples,
		"wins":        wins,
		"sum_delta":   sumDelta,
		"history":     hist,
		"seen_keys":   seenKeys,
		"new_samples": newN,
		"obs_ev":      obsEV,
		"win_rate":    winRate,
		"message":     msg,
		"stats_snap": map[string]any{
			"total_rounds": int(asFloat(firstNonNil(stats["total_rounds"], stats["rounds"]))),
			"net_lobster":  asFloat(stats["net_lobster"]),
			"wins":         stats["wins"],
			"losses":       stats["losses"],
		},
		"enabled": true,
	}
	if st != nil {
		saveRiskEdge(st, "risk.edge.vip_obs", edge)
	}
	return edge
}

