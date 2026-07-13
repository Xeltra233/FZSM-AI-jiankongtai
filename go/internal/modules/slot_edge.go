package modules

import (
	"fmt"
	"time"

	"fzsmbot/internal/client"
	"fzsmbot/internal/config"
	"fzsmbot/internal/storage"
)

// Slot edge state is persisted under storage key lottery.slot_edge.
// Plan B: analyze config RTP + optional observed samples, auto-spin only with proven edge.

func slotNum(cfg map[string]any, key string, def float64) float64 {
	if cfg == nil {
		return def
	}
	if v, ok := cfg[key]; ok {
		x := asFloat(v)
		if x != 0 || fmt.Sprint(v) == "0" {
			return x
		}
	}
	return def
}

func computeSlotTheory(slotCfg map[string]any) map[string]any {
	out := map[string]any{
		"ok": false, "rtp": 0.0, "ev_per_spin": 0.0, "bet": 0.0,
		"reason": "slot_config_missing",
	}
	if slotCfg == nil || len(slotCfg) == 0 {
		return out
	}
	settings, _ := slotCfg["settings"].(map[string]any)
	if settings == nil {
		// maybe nested
		if d, ok := slotCfg["data"].(map[string]any); ok {
			settings, _ = d["settings"].(map[string]any)
			if slotCfg["symbols"] == nil {
				slotCfg = d
			}
		}
	}
	bet := asFloat(settings["bet_lobster"])
	if bet <= 0 {
		bet = 1000000
	}
	syms := asSlice(slotCfg["symbols"])
	prizes := asSlice(slotCfg["prizes"])
	if len(syms) == 0 || len(prizes) == 0 {
		out["reason"] = "slot_symbols_or_prizes_missing"
		out["bet"] = bet
		return out
	}
	type sym struct {
		id string
		w  float64
	}
	arr := make([]sym, 0, len(syms))
	totalW := 0.0
	for _, it := range syms {
		m, _ := it.(map[string]any)
		if m == nil {
			continue
		}
		id := fmt.Sprint(m["id"])
		w := asFloat(m["weight"])
		if id == "" || id == "<nil>" || w <= 0 {
			continue
		}
		arr = append(arr, sym{id: id, w: w})
		totalW += w
	}
	if totalW <= 0 || len(arr) == 0 {
		out["reason"] = "slot_weights_invalid"
		out["bet"] = bet
		return out
	}
	three := map[string]float64{}
	twoMult := 0.0
	diffMult := 0.0
	for _, it := range prizes {
		m, _ := it.(map[string]any)
		if m == nil {
			continue
		}
		mt := fmt.Sprint(m["match_type"])
		mult := asFloat(m["payout_mult"])
		switch mt {
		case "three_same":
			sid := fmt.Sprint(m["symbol_id"])
			if sid != "" && sid != "<nil>" {
				three[sid] = mult
			}
		case "two_same":
			twoMult = mult
		case "three_diff":
			diffMult = mult
		}
	}
	// exact expectation over all symbol triples
	eMult := 0.0
	for _, a := range arr {
		pa := a.w / totalW
		for _, b := range arr {
			pb := b.w / totalW
			for _, c := range arr {
				pc := c.w / totalW
				prob := pa * pb * pc
				var mult float64
				if a.id == b.id && b.id == c.id {
					mult = three[a.id]
				} else if a.id == b.id || a.id == c.id || b.id == c.id {
					mult = twoMult
				} else {
					mult = diffMult
				}
				eMult += prob * mult
			}
		}
	}
	ev := (eMult - 1.0) * bet
	out["ok"] = true
	out["rtp"] = eMult
	out["ev_per_spin"] = ev
	out["bet"] = bet
	out["symbol_count"] = len(arr)
	out["total_weight"] = totalW
	out["reason"] = "theory_from_config"
	return out
}

func loadSlotEdge(st *storage.Storage) map[string]any {
	if st == nil {
		return map[string]any{}
	}
	return st.GetStateMap("lottery.slot_edge")
}

func saveSlotEdge(st *storage.Storage, edge map[string]any) {
	if st == nil || edge == nil {
		return
	}
	_ = st.SetState("lottery.slot_edge", edge)
}

func updateSlotEdge(st *storage.Storage, lcfg map[string]any, slotCfg map[string]any) map[string]any {
	prev := loadSlotEdge(st)
	theory := computeSlotTheory(slotCfg)
	minSamples := int(slotNum(lcfg, "slot_min_samples", 30))
	if minSamples < 1 {
		minSamples = 30
	}
	minRTP := slotNum(lcfg, "slot_min_rtp", 1.0)
	minEV := slotNum(lcfg, "slot_min_ev", 0.0)
	maxSpins := int(slotNum(lcfg, "slot_max_spins_per_cycle", 1))
	if maxSpins < 1 {
		maxSpins = 1
	}
	samples := int(asFloat(prev["samples"]))
	sumDelta := asFloat(prev["sum_delta"])
	wins := int(asFloat(prev["wins"]))
	obsRTP := 0.0
	obsEV := 0.0
	if samples > 0 {
		obsEV = sumDelta / float64(samples)
		// approximate observed RTP from avg delta and bet
		bet := asFloat(theory["bet"])
		if bet <= 0 {
			bet = asFloat(prev["bet"])
		}
		if bet > 0 {
			obsRTP = 1.0 + (obsEV / bet)
		}
	}
	theoryRTP := asFloat(theory["rtp"])
	theoryEV := asFloat(theory["ev_per_spin"])
	// Prefer theory when available; observed only reinforces after enough samples.
	useRTP := theoryRTP
	useEV := theoryEV
	source := "theory"
	if samples >= minSamples {
		// blend lightly toward observed, but never ignore strongly negative theory
		useRTP = 0.7*theoryRTP + 0.3*obsRTP
		useEV = 0.7*theoryEV + 0.3*obsEV
		source = "theory+observed"
	}
	edgeOK := asBool(theory["ok"], false) && useRTP >= minRTP && useEV >= minEV
	edge := map[string]any{
		"ts":                  float64(time.Now().UnixNano()) / 1e9,
		"theory_ok":           theory["ok"],
		"theory_rtp":          theoryRTP,
		"theory_ev_per_spin":  theoryEV,
		"bet":                 theory["bet"],
		"samples":             samples,
		"wins":                wins,
		"sum_delta":           sumDelta,
		"obs_rtp":             obsRTP,
		"obs_ev_per_spin":     obsEV,
		"min_samples":         minSamples,
		"min_rtp":             minRTP,
		"min_ev":              minEV,
		"max_spins_per_cycle": maxSpins,
		"use_rtp":             useRTP,
		"use_ev":              useEV,
		"source":              source,
		"edge_ok":             edgeOK,
		"reason":              theory["reason"],
	}
	if !asBool(theory["ok"], false) {
		edge["gate"] = "need_slot_config"
		edge["message"] = "缺少老虎机配置，无法计算 RTP"
		edge["probe_status"] = "blocked"
	} else if theoryRTP < minRTP || theoryEV < minEV {
		// hard stop on negative theory regardless of samples
		edge["edge_ok"] = false
		edge["gate"] = "theory_negative"
		edge["message"] = fmt.Sprintf("理论负期望：RTP=%.2f%% EV/把=%.0f，自动转保持关闭", theoryRTP*100, theoryEV)
		edge["probe_status"] = "blocked"
	} else if !edgeOK {
		edge["gate"] = "no_positive_edge"
		edge["message"] = fmt.Sprintf("未达正EV：RTP=%.2f%% EV/把=%.0f，门槛 RTP>=%.0f%% 且 EV>=%.0f", useRTP*100, useEV, minRTP*100, minEV)
		edge["probe_status"] = "collecting"
	} else if samples < minSamples {
		// theory already positive; samples are bonus
		edge["gate"] = "ready"
		edge["message"] = fmt.Sprintf("理论优势成立 RTP=%.2f%% EV/把=%.0f，可自动转（样本 %d/%d）", theoryRTP*100, theoryEV, samples, minSamples)
		edge["probe_status"] = "ready"
	} else {
		edge["gate"] = "ready"
		edge["message"] = fmt.Sprintf("样本=%d 综合RTP=%.2f%% EV/把=%.0f，可自动转", samples, useRTP*100, useEV)
		edge["probe_status"] = "ready"
	}
	// keep explicit observed summary fields for UI
	edge["sample_target"] = minSamples
	edge["win_rate"] = 0.0
	if samples > 0 {
		edge["win_rate"] = float64(wins) / float64(samples)
	}
	saveSlotEdge(st, edge)
	return edge
}

func recordSlotSample(st *storage.Storage, delta float64, win bool) map[string]any {
	edge := loadSlotEdge(st)
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
	// rolling history (latest first, keep 20)
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
		edge["obs_ev_per_spin"] = sumDelta / float64(samples)
		edge["win_rate"] = float64(wins) / float64(samples)
		bet := asFloat(edge["bet"])
		if bet > 0 {
			edge["obs_rtp"] = 1.0 + (sumDelta / float64(samples) / bet)
		}
	}
	saveSlotEdge(st, edge)
	return edge
}

func maybeAutoSlot(cfg *config.Config, st *storage.Storage, c *client.Client, values map[string]any, lcfg map[string]any, slotCfg map[string]any, balance float64) (actions []map[string]any, errors []string, edge map[string]any) {
	edge = updateSlotEdge(st, lcfg, slotCfg)
	auto := flagOn(values, "lottery.auto_slot", asBool(lcfg["auto_slot"], false)) ||
		flagOn(values, "lottery.auto_nailong", asBool(lcfg["auto_nailong"], false))
	if !auto {
		actions = append(actions, map[string]any{
			"status": "analyze_only", "action": "slot",
			"reason": "slot_default_off_need_rtp_or_manual",
			"edge":   edge,
		})
		return
	}
	actions = append(actions, map[string]any{
		"status": "ok", "action": "slot_edge_analyzed",
		"rtp": edge["use_rtp"], "ev": edge["use_ev"], "gate": edge["gate"], "message": edge["message"],
	})
	if !asBool(edge["edge_ok"], false) {
		actions = append(actions, map[string]any{
			"status": "skip", "action": "slot_spin",
			"reason": "no_proven_rtp_edge_in_config",
			"detail": edge["message"],
			"edge":   edge,
		})
		return
	}
	bet := asFloat(edge["bet"])
	if bet <= 0 {
		bet = 1000000
	}
	if balance < bet {
		actions = append(actions, map[string]any{
			"status": "skip", "action": "slot_spin",
			"reason": "insufficient_balance_for_slot_bet",
			"need":   bet, "balance": balance,
		})
		return
	}
	maxSpins := int(asFloat(edge["max_spins_per_cycle"]))
	if maxSpins <= 0 {
		maxSpins = 1
	}
	// hard safety: never more than 3 per cycle
	if maxSpins > 3 {
		maxSpins = 3
	}
	spun := 0
	for i := 0; i < maxSpins; i++ {
		if balance < bet {
			break
		}
		raw, err := c.LotteryNailong(1, 1)
		if err != nil {
			errors = append(errors, fmt.Sprintf("slot_spin: %v", err))
			actions = append(actions, map[string]any{"status": "error", "action": "slot_spin", "reason": err.Error()})
			break
		}
		delta := asFloat(firstNonNil(raw["delta_lobster"], raw["net_lobster"]))
		win := asBool(raw["win"], delta > 0)
		edge = recordSlotSample(st, delta, win)
		balance = asFloat(firstNonNil(raw["after_lobster"], balance+delta))
		spun++
		actions = append(actions, map[string]any{
			"status": "ok", "action": "slot_spin",
			"delta": delta, "win": win, "after": balance,
			"label": raw["label"], "symbols": raw["symbols"],
			"samples": edge["samples"],
		})
		// if after a spin edge collapses (shouldn't for theory), stop
		if !asBool(edge["edge_ok"], true) && asFloat(edge["theory_rtp"]) < asFloat(edge["min_rtp"]) {
			break
		}
	}
	if spun == 0 {
		actions = append(actions, map[string]any{"status": "skip", "action": "slot_spin", "reason": "no_spin_executed", "edge": edge})
	}
	return
}
