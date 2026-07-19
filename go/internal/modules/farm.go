package modules

import (
	"fmt"
	"math"
	"strings"
	"time"

	"fzsmbot/internal/client"
	"fzsmbot/internal/config"
	"fzsmbot/internal/storage"
)

func farmCfg(cfg *config.Config) map[string]any {
	if cfg != nil && cfg.Farm != nil {
		return cfg.Farm
	}
	return map[string]any{}
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func farmStealPressureDynamic(feed []any) map[string]float64 {
	stealBy := map[string]float64{}
	plantBy := map[string]float64{}
	for _, e := range feed {
		m := asMap(e)
		k := strings.TrimSpace(fmt.Sprint(m["crop_key"]))
		if k == "" || k == "<nil>" {
			continue
		}
		kind := strings.ToLower(fmt.Sprint(m["kind"]))
		if kind == "steal" {
			stealBy[k]++
		} else if kind == "plant" {
			plantBy[k]++
		}
	}
	out := map[string]float64{}
	keys := map[string]bool{}
	for k := range stealBy {
		keys[k] = true
	}
	for k := range plantBy {
		keys[k] = true
	}
	for k := range keys {
		plants := plantBy[k]
		steals := stealBy[k]
		rate := steals / math.Max(1.0, plants+steals+2.0)
		out[k] = clamp(rate*1.4, 0, 0.7)
	}
	return out
}

func farmStealPressure(crop map[string]any, live map[string]float64) float64 {
	key := strings.ToLower(fmt.Sprint(crop["key"]))
	yld := math.Max(0, asFloat(crop["yield_lobster"]))
	grow := math.Max(1, asFloat(crop["grow_sec"]))
	hint := fmt.Sprint(crop["steal_hint"])
	base := math.Min(0.55, 0.05+yld/900.0+math.Log1p(grow/600.0)*0.04)
	switch {
	case key == "lobster" || strings.Contains(hint, "重点") || strings.Contains(hint, "高收益"):
		base += 0.18
	case key == "berry" || strings.Contains(hint, "偷菜") || strings.Contains(hint, "发光"):
		base += 0.12
	case key == "carrot" || strings.Contains(hint, "香甜"):
		base += 0.07
	case key == "sprout" || strings.Contains(hint, "新手") || strings.Contains(hint, "顺手"):
		base += 0.10
	case key == "corn" || strings.Contains(hint, "稳"):
		base += 0.04
	}
	base = clamp(base, 0, 0.70)
	if live != nil {
		if v, ok := live[fmt.Sprint(crop["key"])]; ok {
			base = clamp(0.55*base+0.45*v, 0, 0.75)
		}
	}
	return base
}

func farmCropEV(crop map[string]any, harvestLatency, offlineGap float64, plots int, live map[string]float64) map[string]any {
	key := fmt.Sprint(crop["key"])
	name := fmt.Sprint(crop["name"])
	if name == "" || name == "<nil>" {
		name = key
	}
	grow := math.Max(1, asFloat(crop["grow_sec"]))
	yld := math.Max(0, asFloat(crop["yield_lobster"]))
	pressure := farmStealPressure(crop, live)
	exposure := math.Max(0, harvestLatency) + math.Max(0, offlineGap)*0.5
	lossP := 1.0 - math.Exp(-pressure*exposure/90.0)
	lossP = clamp(lossP, 0, 0.85)
	retained := 1.0 - lossP
	eYield := yld * retained
	lobPerHour := eYield / grow * 3600.0
	rawPerHour := yld / grow * 3600.0
	cyclesPerDay := 86400.0 / grow
	day1 := eYield * cyclesPerDay
	dayN := day1 * float64(maxInt(1, plots))
	return map[string]any{
		"key":                   key,
		"name":                  name,
		"emoji":                 crop["emoji"],
		"grow_sec":              grow,
		"yield_lobster":         yld,
		"steal_pressure":        round4(pressure),
		"ready_exposure_sec":    round2(exposure),
		"expected_steal_loss_p": round4(lossP),
		"retention":             round4(retained),
		"expected_yield":        round4(eYield),
		"raw_lob_per_hour":      round4(rawPerHour),
		"ev_lob_per_hour":       round4(lobPerHour),
		"day_1plot_ev":          round2(day1),
		"day_plots_ev":          round2(dayN),
		"score":                 lobPerHour,
		"steal_hint":            crop["steal_hint"],
		"formula":               "E[y]=yield*(1-p_steal); score=E[y]/grow*3600",
	}
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }
func round4(v float64) float64 { return math.Round(v*10000) / 10000 }
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func analyzeFarmCrops(crops []any, harvestLatency, offlineGap float64, plots int, feed []any) map[string]any {
	live := farmStealPressureDynamic(feed)
	rows := []map[string]any{}
	for _, item := range crops {
		c := asMap(item)
		if strings.TrimSpace(fmt.Sprint(c["key"])) == "" || fmt.Sprint(c["key"]) == "<nil>" {
			continue
		}
		rows = append(rows, farmCropEV(c, harvestLatency, offlineGap, plots, live))
	}
	// sort by score desc, grow asc, key asc
	for i := 0; i < len(rows); i++ {
		for j := i + 1; j < len(rows); j++ {
			si, sj := asFloat(rows[i]["score"]), asFloat(rows[j]["score"])
			gi, gj := asFloat(rows[i]["grow_sec"]), asFloat(rows[j]["grow_sec"])
			ki, kj := fmt.Sprint(rows[i]["key"]), fmt.Sprint(rows[j]["key"])
			if sj > si || (sj == si && (gj < gi || (gj == gi && kj < ki))) {
				rows[i], rows[j] = rows[j], rows[i]
			}
		}
	}
	var best, alt map[string]any
	if len(rows) > 0 {
		best = rows[0]
	}
	if len(rows) > 1 {
		alt = rows[1]
	}
	var edge any
	reason := ""
	if best != nil {
		if alt != nil {
			edge = asFloat(best["score"]) - asFloat(alt["score"])
		}
		reason = fmt.Sprintf(
			"EV最优 %v/%v: %.2f lob/h (raw %.2f, 保留率 %.2f%%, 12地日EV %.0f)",
			best["name"], best["key"], asFloat(best["ev_lob_per_hour"]), asFloat(best["raw_lob_per_hour"]),
			asFloat(best["retention"])*100, asFloat(best["day_plots_ev"]),
		)
		if edge != nil {
			reason += fmt.Sprintf("; 领先第二名 %.2f lob/h", asFloat(edge))
		}
	}
	top := rows
	if len(top) > 5 {
		top = top[:5]
	}
	return map[string]any{
		"mode":                "max_ev_hourly",
		"plots":               plots,
		"harvest_latency_sec": harvestLatency,
		"offline_gap_sec":     offlineGap,
		"rows":                rows,
		"best_key":            mapGetStr(best, "key"),
		"best":                best,
		"edge_vs_second":      edge,
		"live_steal_pressure": live,
		"reason":              reason,
		"top":                 top,
	}
}

func mapGetStr(m map[string]any, k string) any {
	if m == nil {
		return nil
	}
	return m[k]
}

func cfgFloat(m map[string]any, k string, def float64) float64 {
	if m == nil {
		return def
	}
	if v, ok := m[k]; ok && v != nil {
		return asFloat(v)
	}
	return def
}

func cfgInt(m map[string]any, k string, def int) int {
	return int(cfgFloat(m, k, float64(def)))
}

func cfgBool(m map[string]any, k string, def bool) bool {
	if m == nil {
		return def
	}
	if v, ok := m[k]; ok {
		return asBool(v, def)
	}
	return def
}

func cfgStr(m map[string]any, k, def string) string {
	if m == nil {
		return def
	}
	if v, ok := m[k]; ok {
		s := strings.TrimSpace(fmt.Sprint(v))
		if s != "" && s != "<nil>" {
			return s
		}
	}
	return def
}

// RunFarm executes harvest/plant (and optional steal) with EV crop selection.
func RunFarm(cfg *config.Config, st *storage.Storage, c *client.Client) map[string]any {
	fcfg := farmCfg(cfg)
	out := map[string]any{
		"enabled":    cfgBool(fcfg, "enabled", true),
		"ts":         now(),
		"planted":    []any{},
		"harvested":  []any{},
		"stolen":     []any{},
		"errors":     []any{},
		"plots":      map[string]any{"empty": 0, "growing": 0, "ready": 0},
		"steal_left": nil,
		"roi_table":  []any{},
		"analysis":   map[string]any{},
		"impl":       "go",
		"mode":       "execute",
	}
	if !cfgBool(fcfg, "enabled", true) {
		out["skipped"] = true
		out["status"] = "disabled"
		return out
	}
	// throttle by min_interval_sec using previous farm.ts
	minInterval := cfgFloat(fcfg, "min_interval_sec", 20)
	if prev := st.GetStateMap("farm"); len(prev) > 0 {
		lastTS := asFloat(prev["ts"])
		if lastTS > 0 && now()-lastTS < minInterval && fmt.Sprint(prev["impl"]) == "go" {
			// keep previous executable summary if fresh
			prev["skipped"] = true
			prev["reason"] = "throttle"
			prev["impl"] = "go"
			return prev
		}
	}

	me, err := c.FarmMe()
	if err != nil {
		out["errors"] = []any{fmt.Sprintf("farm_me: %v", err)}
		out["status"] = "error"
		return out
	}
	plots := asSlice(me["plots"])
	crops := asSlice(me["crops"])
	plotN := maxInt(1, len(plots))
	if plotN == 0 {
		plotN = 12
	}
	feed, _ := c.FarmFeed()
	harvestLatency := cfgFloat(fcfg, "harvest_latency_sec", 25)
	offlineGap := cfgFloat(fcfg, "offline_gap_sec", 0)
	analysis := analyzeFarmCrops(crops, harvestLatency, offlineGap, plotN, feed)
	cropMode := cfgStr(fcfg, "crop_mode", "max_ev_hourly")
	cropKey := cfgStr(fcfg, "crop_key", "auto")
	preferred := ""
	if cropMode == "fixed" || (cropKey != "" && cropKey != "auto" && cropKey != "best" && cropKey != "roi") {
		preferred = cropKey
	}
	bestKey := fmt.Sprint(analysis["best_key"])
	if preferred != "" {
		bestKey = preferred
	}
	if bestKey == "" || bestKey == "<nil>" {
		// fallback first crop or lobster
		if len(crops) > 0 {
			bestKey = fmt.Sprint(asMap(crops[0])["key"])
		} else {
			bestKey = "lobster"
		}
	}
	out["crop_key"] = bestKey
	out["crop_mode"] = cropMode
	out["crop_reason"] = analysis["reason"]
	if best := asMap(analysis["best"]); len(best) > 0 {
		out["day_ev_12"] = best["day_plots_ev"]
	}
	top := asSlice(analysis["top"])
	if len(top) == 0 {
		// convert rows if needed
		for _, r := range asSlice(analysis["rows"]) {
			top = append(top, r)
			if len(top) >= 5 {
				break
			}
		}
	}
	out["roi_table"] = top
	out["analysis"] = map[string]any{
		"best":           analysis["best"],
		"edge_vs_second": analysis["edge_vs_second"],
		"reason":         analysis["reason"],
		"top":            top,
		"crop_reason":    analysis["reason"],
		"day_ev_12":      out["day_ev_12"],
		"params": map[string]any{
			"harvest_latency_sec": harvestLatency,
			"offline_gap_sec":     offlineGap,
			"plots":               plotN,
		},
	}

	dailyCount := int(asFloat(me["daily_steal_count"]))
	dailyLimit := int(asFloat(me["daily_steal_limit"]))
	if dailyLimit > 0 {
		left := dailyLimit - dailyCount
		if left < 0 {
			left = 0
		}
		out["steal_left"] = left
	}
	out["balance_lobster"] = me["balance_lobster"]

	maxHarvest := cfgInt(fcfg, "max_harvest_per_cycle", 12)
	maxPlant := cfgInt(fcfg, "max_plant_per_cycle", 12)
	maxSteal := cfgInt(fcfg, "max_steal_per_cycle", 5)
	stealEnabled := cfgBool(fcfg, "steal_enabled", true)

	harvested := []any{}
	planted := []any{}
	stolen := []any{}
	errors := []any{}

	harvestN := 0
	harvestBalanceBefore := asFloat(me["balance_lobster"])
	for _, item := range plots {
		if harvestN >= maxHarvest {
			break
		}
		p := asMap(item)
		if plotState(p) != "ready" {
			continue
		}
		plotNo := int(asFloat(p["plot_no"]))
		if plotNo <= 0 {
			continue
		}
		raw, err := c.FarmHarvest(plotNo)
		if err != nil {
			errors = append(errors, fmt.Sprintf("harvest#%d: %v", plotNo, err))
			continue
		}
		delta := asFloat(firstNonNil(asMap(raw)["net_lobster"], asMap(raw)["delta_lobster"], asMap(raw)["reward"], asMap(raw)["yield"], asMap(raw)["amount"], asMap(raw)["lobster"]))
		harvested = append(harvested, map[string]any{"plot_no": plotNo, "raw": raw, "delta": delta})
		harvestN++
	}
	if harvestN > 0 {
		if me2, err := c.FarmMe(); err == nil {
			totalDelta := asFloat(me2["balance_lobster"]) - harvestBalanceBefore
			missing := 0
			known := 0.0
			for _, item := range harvested {
				row := asMap(item)
				if asFloat(row["delta"]) == 0 {
					missing++
				} else {
					known += asFloat(row["delta"])
				}
			}
			fallback := 0.0
			if missing > 0 && totalDelta > known {
				fallback = (totalDelta - known) / float64(missing)
			}
			for _, item := range harvested {
				row := asMap(item)
				if asFloat(row["delta"]) == 0 && fallback > 0 {
					row["delta"] = fallback
					row["delta_source"] = "balance_reconciliation"
				}
				delta := asFloat(row["delta"])
				recordTraceSample(st, "risk.obs.farm_harvest", "farm_harvest", delta, delta > 0, map[string]any{"source": "self_exec", "plot_no": row["plot_no"], "batch_balance_delta": totalDelta})
			}
			me = me2
			plots = asSlice(me["plots"])
			out["balance_lobster"] = me["balance_lobster"]
		}
	}

	plantN := 0
	for _, item := range plots {
		if plantN >= maxPlant {
			break
		}
		p := asMap(item)
		if plotState(p) != "empty" {
			continue
		}
		plotNo := int(asFloat(p["plot_no"]))
		if plotNo <= 0 {
			continue
		}
		raw, err := c.FarmPlant(plotNo, bestKey)
		if err != nil {
			errors = append(errors, fmt.Sprintf("plant#%d: %v", plotNo, err))
			break
		}
		planted = append(planted, map[string]any{
			"plot_no": plotNo, "crop_key": bestKey, "reason": out["crop_reason"], "raw": raw,
		})
		plantN++
	}

	if stealEnabled {
		stealLeft := out["steal_left"]
		canSteal := true
		quotaState := st.GetStateMap("farm.steal_quota")
		today := time.Now().Format("2006-01-02")
		if fmt.Sprint(quotaState["day"]) == today && asBool(quotaState["exhausted"], false) {
			canSteal = false
			out["steal_left"] = 0
			out["steal_skip_reason"] = "daily_quota_cached"
		}
		if stealLeft != nil && asFloat(stealLeft) <= 0 {
			canSteal = false
		}
		if canSteal {
			targets, err := c.FarmTargets()
			if err != nil {
				errors = append(errors, fmt.Sprintf("targets: %v", err))
			} else {
				stealN := 0
				quotaExhausted := false
				for _, tItem := range targets {
					if quotaExhausted {
						break
					}
					if stealN >= maxSteal {
						break
					}
					if stealLeft != nil && stealN >= int(asFloat(stealLeft)) {
						break
					}
					t := asMap(tItem)
					uid := int(asFloat(firstNonZero(t["user_id"], t["id"], t["uid"])))
					nested := asSlice(firstNonNil(t["plots"], t["ready_plots"]))
					if len(nested) > 0 {
						for _, np := range nested {
							if quotaExhausted {
								break
							}
							if stealN >= maxSteal {
								break
							}
							pm := asMap(np)
							pno := int(asFloat(pm["plot_no"]))
							if uid <= 0 || pno <= 0 {
								continue
							}
							// only ready if marked
							plotSt := strings.ToLower(fmt.Sprint(firstNonNil(pm["status"], pm["state"])))
							if plotSt != "" && plotSt != "ready" && plotSt != "mature" && plotSt != "harvestable" && asFloat(pm["remain_sec"]) > 0 {
								continue
							}
							raw, err := c.FarmSteal(uid, pno)
							if err != nil {
								errors = append(errors, fmt.Sprintf("steal@%d#%d: %v", uid, pno, err))
								if strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "额度用完") || strings.Contains(err.Error(), "quota") {
									quotaExhausted = true
									out["steal_left"] = 0
									_ = st.SetState("farm.steal_quota", map[string]any{"day": today, "exhausted": true, "ts": now()})
								}
								continue
							}
							deltaSteal := asFloat(firstNonNil(asMap(raw)["delta_lobster"], asMap(raw)["reward"], asMap(raw)["amount"], asMap(raw)["lobster"]))
							recordTraceSample(st, "risk.obs.farm_steal", "farm_steal", deltaSteal, deltaSteal > 0, map[string]any{"source": "self_exec", "user_id": uid, "plot_no": pno})
							stolen = append(stolen, map[string]any{"user_id": uid, "plot_no": pno, "raw": raw, "delta": deltaSteal})
							stealN++
						}
					} else {
						pno := int(asFloat(t["plot_no"]))
						if uid > 0 && pno > 0 {
							raw, err := c.FarmSteal(uid, pno)
							if err != nil {
								errors = append(errors, fmt.Sprintf("steal@%d#%d: %v", uid, pno, err))
								if strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "额度用完") || strings.Contains(err.Error(), "quota") {
									quotaExhausted = true
									out["steal_left"] = 0
									_ = st.SetState("farm.steal_quota", map[string]any{"day": today, "exhausted": true, "ts": now()})
								}
							} else {
								stolen = append(stolen, map[string]any{"user_id": uid, "plot_no": pno, "raw": raw})
								stealN++
							}
						}
					}
				}
			}
		}
	}

	// final plot counts
	if me3, err := c.FarmMe(); err == nil {
		me = me3
		plots = asSlice(me["plots"])
		out["balance_lobster"] = me["balance_lobster"]
		dailyCount = int(asFloat(me["daily_steal_count"]))
		dailyLimit = int(asFloat(me["daily_steal_limit"]))
		if dailyLimit > 0 {
			left := dailyLimit - dailyCount
			if left < 0 {
				left = 0
			}
			out["steal_left"] = left
		}
	}
	counts := map[string]int{"empty": 0, "growing": 0, "ready": 0}
	for _, item := range plots {
		counts[plotState(asMap(item))]++
	}
	out["plots"] = map[string]any{"empty": counts["empty"], "growing": counts["growing"], "ready": counts["ready"]}
	out["planted"] = planted
	out["harvested"] = harvested
	out["stolen"] = stolen
	out["errors"] = errors
	out["status"] = "ok"
	if len(errors) > 0 && len(planted)+len(harvested)+len(stolen) == 0 {
		out["status"] = "error"
	}
	out["ts"] = now()
	return out
}

func firstNonZero(xs ...any) any {
	for _, x := range xs {
		if asFloat(x) != 0 {
			return x
		}
	}
	if len(xs) > 0 {
		return xs[0]
	}
	return nil
}

func firstNonNil(xs ...any) any {
	for _, x := range xs {
		if x != nil {
			return x
		}
	}
	return nil
}

// silence unused import if time needed later
var _ = time.Now
