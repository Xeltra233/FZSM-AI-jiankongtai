package modules

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"fzsmbot/internal/storage"
)

const observationHistoryLimit = 100

func stableVersion(kind string, payload any) string {
	b, _ := json.Marshal(map[string]any{"kind": kind, "payload": payload})
	h := sha256.Sum256(b)
	return kind + ":" + hex.EncodeToString(h[:8])
}

func drawObservationVersion(kind string, me map[string]any) string {
	premium := asMap(me["premium"])
	payload := map[string]any{
		"month_start": me["month_start"], "month_end": me["month_end"],
		"entry_fee": premium["entry_fee"], "min": premium["min"], "max": premium["max"],
	}
	return stableVersion(kind, payload)
}

func slotConfigVersion(slotCfg map[string]any) string {
	data := slotCfg
	if nested := asMap(slotCfg["data"]); len(nested) > 0 {
		data = nested
	}
	return stableVersion("slot", map[string]any{
		"symbols": data["symbols"], "prizes": data["prizes"], "settings": data["settings"],
	})
}

func rollingObservationStats(history []any, limit int, z float64) map[string]any {
	if limit <= 0 || limit > len(history) {
		limit = len(history)
	}
	if z <= 0 {
		z = 1.96
	}
	values := make([]float64, 0, limit)
	for _, item := range history[:limit] {
		m := asMap(item)
		if _, ok := m["delta"]; ok {
			values = append(values, asFloat(m["delta"]))
		}
	}
	out := map[string]any{"samples": len(values), "mean": 0.0, "stddev": 0.0, "stderr": 0.0, "lcb": 0.0, "ucb": 0.0, "confidence_ready": false}
	if len(values) == 0 {
		return out
	}
	sum := 0.0
	for _, x := range values {
		sum += x
	}
	mean := sum / float64(len(values))
	out["mean"] = mean
	out["lcb"] = mean
	out["ucb"] = mean
	if len(values) < 2 {
		return out
	}
	ss := 0.0
	for _, x := range values {
		d := x - mean
		ss += d * d
	}
	stddev := math.Sqrt(ss / float64(len(values)-1))
	stderr := stddev / math.Sqrt(float64(len(values)))
	out["stddev"] = stddev
	out["stderr"] = stderr
	out["lcb"] = mean - z*stderr
	out["ucb"] = mean + z*stderr
	out["confidence_ready"] = true
	return out
}

func archiveObservation(prev map[string]any) []any {
	archives := asSlice(prev["previous_versions"])
	if len(prev) > 0 && (asFloat(prev["samples"]) > 0 || prev["version"] != nil) {
		archives = append([]any{map[string]any{
			"version": prev["version"], "samples": prev["samples"], "wins": prev["wins"],
			"sum_delta": prev["sum_delta"], "obs_ev": firstNonNil(prev["obs_ev"], prev["obs_ev_per_spin"]),
			"archived_at": float64(time.Now().UnixNano()) / 1e9,
		}}, archives...)
	}
	if len(archives) > 5 {
		archives = archives[:5]
	}
	return archives
}

func recordVersionedObservation(st *storage.Storage, key string, delta float64, win bool, extra map[string]any) map[string]any {
	prev := loadRiskEdge(st, key)
	version := ""
	if extra != nil {
		version = stringValue(extra["version"])
	}
	edge := prev
	if version != "" && stringValue(prev["version"]) != version {
		edge = map[string]any{"previous_versions": archiveObservation(prev), "version": version, "version_started_at": now()}
	}
	samples := int(asFloat(edge["samples"])) + 1
	sumDelta := asFloat(edge["sum_delta"]) + delta
	sumSq := asFloat(edge["sum_sq_delta"]) + delta*delta
	wins := int(asFloat(edge["wins"]))
	if win {
		wins++
	}
	edge["samples"] = samples
	edge["sum_delta"] = sumDelta
	edge["sum_sq_delta"] = sumSq
	edge["wins"] = wins
	edge["last_delta"] = delta
	edge["last_ts"] = now()
	if samples == 1 || delta < asFloat(edge["min_delta"]) {
		edge["min_delta"] = delta
	}
	if samples == 1 || delta > asFloat(edge["max_delta"]) {
		edge["max_delta"] = delta
	}
	item := map[string]any{"ts": now(), "delta": delta, "win": win, "version": version}
	history := append([]any{item}, asSlice(edge["history"])...)
	if len(history) > observationHistoryLimit {
		history = history[:observationHistoryLimit]
	}
	edge["history"] = history
	edge["obs_ev"] = sumDelta / float64(samples)
	edge["win_rate"] = float64(wins) / float64(samples)
	stats := rollingObservationStats(history, observationHistoryLimit, 1.96)
	edge["rolling_samples"] = stats["samples"]
	edge["rolling_ev"] = stats["mean"]
	edge["rolling_stddev"] = stats["stddev"]
	edge["rolling_stderr"] = stats["stderr"]
	edge["rolling_lcb_ev"] = stats["lcb"]
	edge["rolling_ucb_ev"] = stats["ucb"]
	edge["confidence_ready"] = stats["confidence_ready"]
	if extra != nil {
		for k, v := range extra {
			edge[k] = v
		}
	}
	saveRiskEdge(st, key, edge)
	return edge
}

func stringValue(v any) string {
	s := fmt.Sprint(v)
	if s == "<nil>" || s == "null" {
		return ""
	}
	return s
}

func drawNetDelta(raw map[string]any) float64 {
	return asFloat(firstNonNil(raw["net_lobster"], raw["delta_lobster"], raw["delta"], raw["reward_lobster"], raw["prize_lobster"], raw["win_lobster"]))
}

func freeDrawLimit(edge map[string]any, currentVersion string, configuredMax, minSamples int) int {
	if configuredMax < 1 {
		return 0
	}
	if stringValue(edge["version"]) != currentVersion {
		return 1
	}
	n := int(asFloat(edge["rolling_samples"]))
	mean := asFloat(edge["rolling_ev"])
	lcb := asFloat(edge["rolling_lcb_ev"])
	if n < minSamples {
		return 1
	}
	if mean <= 0 {
		return 0
	}
	if lcb > 0 {
		return configuredMax
	}
	return 1
}

func paidPremiumDecision(edge map[string]any, currentVersion string, entryFee float64, lcfg map[string]any) map[string]any {
	minSamples := int(slotNum(lcfg, "paid_premium_min_samples", 20))
	minNetEV := slotNum(lcfg, "paid_premium_min_net_ev", 0)
	n := int(asFloat(edge["rolling_samples"]))
	grossMean := asFloat(edge["rolling_ev"])
	grossLCB := asFloat(edge["rolling_lcb_ev"])
	versionOK := currentVersion != "" && stringValue(edge["version"]) == currentVersion
	netMean := grossMean - entryFee
	netLCB := grossLCB - entryFee
	ready := versionOK && n >= minSamples && asBool(edge["confidence_ready"], false) && netLCB >= minNetEV
	reason := "ready"
	switch {
	case !versionOK:
		reason = "activity_version_unverified"
	case n < minSamples:
		reason = "insufficient_current_version_samples"
	case !asBool(edge["confidence_ready"], false):
		reason = "confidence_unavailable"
	case netLCB < minNetEV:
		reason = "net_ev_confidence_below_threshold"
	}
	return map[string]any{
		"ready": ready, "reason": reason, "version": currentVersion, "version_ok": versionOK,
		"samples": n, "min_samples": minSamples, "entry_fee": entryFee,
		"gross_mean": grossMean, "gross_lcb": grossLCB, "net_mean": netMean, "net_lcb": netLCB,
		"min_net_ev": minNetEV,
	}
}
