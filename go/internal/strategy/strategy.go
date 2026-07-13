package strategy

import (
        "fmt"
        "math"
        "strings"

        "fzsmbot/internal/indicators"
)

type Signal struct {
        StockID    int            `json:"stock_id"`
        Code       string         `json:"code"`
        Name       string         `json:"name"`
        AssetType  string         `json:"asset_type"`
        Price      float64        `json:"price"`
        Action     string         `json:"action"`
        Score      float64        `json:"score"`
        Confidence float64        `json:"confidence"`
        Reason     string         `json:"reason"`
        Indicators map[string]any `json:"indicators"`
        TradeEV    map[string]any `json:"trade_ev"`
}

type Engine struct {
        Cfg map[string]any
}

func New(cfg map[string]any) *Engine { return &Engine{Cfg: cfg} }

func (e *Engine) cfgF(k string, def float64) float64 {
        if e.Cfg == nil {
                return def
        }
        if v, ok := e.Cfg[k]; ok && v != nil {
                switch t := v.(type) {
                case float64:
                        return t
                case int:
                        return float64(t)
                case int64:
                        return float64(t)
                }
        }
        return def
}
func (e *Engine) cfgI(k string, def int) int { return int(e.cfgF(k, float64(def))) }
func (e *Engine) cfgB(k string, def bool) bool {
        if e.Cfg == nil {
                return def
        }
        if v, ok := e.Cfg[k]; ok {
                switch t := v.(type) {
                case bool:
                        return t
                case string:
                        return t == "1" || strings.EqualFold(t, "true")
                }
        }
        return def
}
func (e *Engine) cfgS(k, def string) string {
        if e.Cfg == nil {
                return def
        }
        if v, ok := e.Cfg[k]; ok {
                s := strings.TrimSpace(fmt.Sprint(v))
                if s != "" && s != "<nil>" {
                        return s
                }
        }
        return def
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

func scoreToExpectedReturn(score float64, atrPct, ret5 *float64) float64 {
        // compact port of python econ.score_to_expected_return spirit
        base := score * 0.04
        if atrPct != nil {
                base += clamp(*atrPct*0.15, -0.02, 0.03)
        }
        if ret5 != nil {
                base += clamp(*ret5*0.25, -0.03, 0.03)
        }
        return clamp(base, -0.2, 0.25)
}

func estimateDownside(atrPct *float64, stopLoss, score float64) float64 {
        d := stopLoss
        if atrPct != nil {
                d = math.Max(d, *atrPct*1.8)
        }
        if score < 0 {
                d *= 1.1
        }
        return clamp(d, 0.02, 0.35)
}

func fractionalKelly(edge, risk, frac float64) float64 {
        if risk <= 0 || edge <= 0 {
                return 0
        }
        k := edge / risk
        return clamp(k*frac, 0, 1)
}

func analyzeTradeEV(score, price float64, atr *float64, ret5 *float64, cfg map[string]any) map[string]any {
        get := func(k string, def float64) float64 {
                if cfg == nil {
                        return def
                }
                if v, ok := cfg[k]; ok {
                        switch t := v.(type) {
                        case float64:
                                return t
                        case int:
                                return float64(t)
                        }
                }
                return def
        }
        stop := get("ev_stop_loss_pct", 0.08)
        tp := get("ev_take_profit_pct", 0.22)
        fee := get("fee_rate", 0.001)
        kf := get("kelly_fraction", 0.35)
        maxPct := get("ev_max_position_pct", 0.20)
        basePct := get("ev_base_position_pct", 0.12)
        var atrPct *float64
        if atr != nil && price > 0 {
                v := *atr / price
                atrPct = &v
        }
        expRet := scoreToExpectedReturn(score, atrPct, ret5)
        downside := estimateDownside(atrPct, stop, score)
        net := expRet - 2*fee
        capped := clamp(net, -0.5, tp)
        kelly := fractionalKelly(math.Max(0, net), downside, kf)
        raw := math.Max(basePct*math.Max(score, 0), 0)
        pct := clamp(math.Max(raw, kelly), 0, maxPct)
        if net <= 0 {
                pct = 0
        }
        out := map[string]any{
                "expected_return":      round6(expRet),
                "net_edge":             round6(net),
                "downside_risk":        round6(downside),
                "trade_ev":             round6(capped),
                "kelly_pct":            round6(kelly),
                "target_position_pct":  round6(pct),
                "fee_rate":             fee,
                "eligible":             net > 0 && pct > 0,
                "reason": fmt.Sprintf("E[r]=%.3f%% net=%.3f%% risk=%.3f%% kelly=%.2f%% target_pct=%.2f%%",
                        expRet*100, net*100, downside*100, kelly*100, pct*100),
        }
        if atrPct != nil {
                out["atr_pct"] = round6(*atrPct)
        }
        return out
}

func round6(v float64) float64 { return math.Round(v*1e6) / 1e6 }

func (e *Engine) Analyze(stock map[string]any, klines []map[string]any, news []any, regime map[string]any) Signal {
        sid := int(asF(first(stock, "id", "stock_id")))
        code := fmt.Sprint(first(stock, "code", "symbol", "id"))
        name := fmt.Sprint(first(stock, "name", "code"))
        asset := fmt.Sprint(first(stock, "asset_type"))
        if asset == "" || asset == "<nil>" {
                asset = "stock"
        }
        price := asF(stock["price"])
        dayChg := asF(stock["change_pct"])
        profile := strings.ToLower(e.cfgS("profile", "balanced"))
        if regime == nil {
                regime = map[string]any{}
        }
        _, highs, lows, closes, volumes, _ := indicators.ExtractOHLCV(klines)
        need := e.cfgI("ema_slow", 21)
        if r := e.cfgI("rsi_period", 14) + 2; r > need {
                need = r
        }
        if len(closes) < need {
                return Signal{StockID: sid, Code: code, Name: name, AssetType: asset, Price: price, Action: "hold", Reason: "kline不足"}
        }
        emaFast := indicators.EMA(closes, e.cfgI("ema_fast", 9))
        emaSlow := indicators.EMA(closes, e.cfgI("ema_slow", 21))
        rsiS := indicators.RSI(closes, e.cfgI("rsi_period", 14))
        _, _, macdHist := indicators.MACD(closes, e.cfgI("macd_fast", 12), e.cfgI("macd_slow", 26), e.cfgI("macd_signal", 9))
        atrS := indicators.ATR(highs, lows, closes, e.cfgI("atr_period", 14))
        volMA := indicators.SMA(volumes, e.cfgI("volume_ma", 20))
        lastClose := closes[len(closes)-1]
        f, _ := indicators.LastValid(emaFast)
        s, _ := indicators.LastValid(emaSlow)
        r, hasR := indicators.LastValid(rsiS)
        mh, hasMH := indicators.LastValid(macdHist)
        a, hasA := indicators.LastValid(atrS)
        v := 0.0
        if len(volumes) > 0 {
                v = volumes[len(volumes)-1]
        }
        vma, _ := indicators.LastValid(volMA)

        score := 0.0
        reasons := []string{}
        if f > s {
                if profile == "balanced" {
                        score += 0.28
                } else {
                        score += 0.30
                }
                reasons = append(reasons, "EMA多头")
        } else {
                if profile == "balanced" {
                        score -= 0.30
                } else {
                        score -= 0.26
                }
                reasons = append(reasons, "EMA空头")
        }
        gap := (f - s) / math.Max(math.Abs(s), 1e-9)
        gapCap := 0.12
        if profile != "balanced" {
                gapCap = 0.16
        }
        score += clamp(gap*(map[bool]float64{true: 2.2, false: 2.8}[profile == "balanced"]), -gapCap, gapCap)

        rsiOS := e.cfgF("rsi_oversold", map[bool]float64{true: 35, false: 40}[profile == "balanced"])
        rsiOB := e.cfgF("rsi_overbought", map[bool]float64{true: 68, false: 72}[profile == "balanced"])
        if hasR {
                if r < rsiOS {
                        score += map[bool]float64{true: 0.26, false: 0.24}[profile == "balanced"]
                        reasons = append(reasons, fmt.Sprintf("RSI超卖(%.1f)", r))
                } else if r > rsiOB {
                        penalty := e.cfgF("chase_penalty", map[bool]float64{true: 0.10, false: 0.06}[profile == "balanced"])
                        if profile == "balanced" {
                                score -= 0.18 + penalty
                        } else {
                                score -= 0.12
                        }
                        reasons = append(reasons, fmt.Sprintf("RSI超买(%.1f)", r))
                } else {
                        if r >= 48 && r <= 62 {
                                score += map[bool]float64{true: 0.10, false: 0.08}[profile == "balanced"]
                        } else if r > 62 && profile == "balanced" {
                                score -= 0.04
                        }
                        reasons = append(reasons, fmt.Sprintf("RSI=%.1f", r))
                }
        }
        if hasMH {
                if mh > 0 {
                        score += map[bool]float64{true: 0.16, false: 0.18}[profile == "balanced"]
                        reasons = append(reasons, "MACD+")
                } else {
                        score -= map[bool]float64{true: 0.18, false: 0.16}[profile == "balanced"]
                        reasons = append(reasons, "MACD-")
                }
        }
        if vma > 0 && v > vma*map[bool]float64{true: 1.25, false: 1.15}[profile == "balanced"] {
                if score >= 0 {
                        score += 0.08
                } else {
                        score -= 0.08
                }
                reasons = append(reasons, "放量放大")
        }
        mw := e.cfgF("momentum_weight", map[bool]float64{true: 0.12, false: 0.18}[profile == "balanced"])
        ret5 := 0.0
        if len(closes) >= 6 {
                ret5 = (closes[len(closes)-1] - closes[len(closes)-6]) / math.Max(closes[len(closes)-6], 1e-9)
                if profile == "balanced" {
                        if ret5 > 0.08 {
                                score -= math.Min(ret5*0.8, mw)
                                reasons = append(reasons, fmt.Sprintf("5m涨超=%.2f%%", ret5*100))
                        } else {
                                score += clamp(ret5*1.6, -mw, mw)
                                reasons = append(reasons, fmt.Sprintf("5m动量=%.2f%%", ret5*100))
                        }
                } else {
                        score += clamp(ret5*2.2, -mw, mw)
                        reasons = append(reasons, fmt.Sprintf("5m动量=%.2f%%", ret5*100))
                }
        }
        bw := e.cfgF("breakout_weight", map[bool]float64{true: 0.08, false: 0.12}[profile == "balanced"])
        nearLow := false
        if len(highs) >= 12 {
                recentHigh := maxSlice(highs[len(highs)-12 : len(highs)-1])
                recentLow := minSlice(lows[len(lows)-12 : len(lows)-1])
                if lastClose >= recentHigh*0.995 {
                        if profile == "balanced" && hasR && r > rsiOB {
                                score -= bw * 0.5
                                reasons = append(reasons, "近高超买")
                        } else {
                                score += bw * map[bool]float64{true: 0.7, false: 1.0}[profile == "balanced"]
                                reasons = append(reasons, "突破新高")
                        }
                } else if lastClose <= recentLow*1.005 {
                        nearLow = true
                        score -= bw * map[bool]float64{true: 1.15, false: 1.0}[profile == "balanced"]
                        reasons = append(reasons, "靠近新低")
                }
        }
        dayCap := map[bool]float64{true: 0.08, false: 0.12}[profile == "balanced"]
        if profile == "balanced" && dayChg > 0.12 {
                score -= math.Min(dayChg*0.5, 0.10)
                reasons = append(reasons, fmt.Sprintf("日涨过度=%.2f%%", dayChg*100))
        } else {
                score += clamp(dayChg*map[bool]float64{true: 0.55, false: 0.8}[profile == "balanced"], -dayCap, dayCap)
                if math.Abs(dayChg) > 0.03 {
                        reasons = append(reasons, fmt.Sprintf("日涨跌=%.2f%%", dayChg*100))
                }
        }
        if hasA && lastClose > 0 {
                volPct := a / lastClose
                thr := map[bool]float64{true: 0.14, false: 0.20}[profile == "balanced"]
                if volPct > thr {
                        damp := e.cfgF("volatility_dampen", map[bool]float64{true: 0.82, false: 0.90}[profile == "balanced"])
                        score *= damp
                        reasons = append(reasons, fmt.Sprintf("波动偏大(%.1f%%)", volPct*100))
                }
        }
        if asset == "crypto" {
                boost := e.cfgF("prefer_crypto_boost", map[bool]float64{true: 0.02, false: 0.05}[profile == "balanced"])
                if score > 0 {
                        score += boost
                        reasons = append(reasons, "加密货加权")
                }
        }
        // news mild
        if ns := newsSentiment(sid, news); ns != nil {
                nw := e.cfgF("news_weight", map[bool]float64{true: 0.15, false: 0.20}[profile == "balanced"])
                score += clamp(*ns/8.0, -1, 1) * nw
                reasons = append(reasons, fmt.Sprintf("新闻=%.1f", *ns))
        }
        enter := e.cfgF("min_score_enter", map[bool]float64{true: 0.48, false: 0.38}[profile == "balanced"])
        enter += asF(regime["enter_score_boost"])
        exitTh := e.cfgF("min_score_exit", map[bool]float64{true: -0.15, false: -0.08}[profile == "balanced"])
        score = clamp(score, -1, 1)

        requireConfirm := e.cfgB("require_entry_confirm", true)
        bullTrend := f > s
        macdOK := hasMH && mh > 0
        rsiOK := hasR && r >= e.cfgF("entry_rsi_min", 42) && r <= e.cfgF("entry_rsi_max", map[bool]float64{true: 66, false: 70}[profile == "balanced"])
        notChasing := !(profile == "balanced" && (dayChg > 0.12 || ret5 > 0.10))
        confirmHits := 0
        for _, b := range []bool{bullTrend, macdOK, rsiOK, notChasing} {
                if b {
                        confirmHits++
                }
        }
        minConfirms := e.cfgI("min_entry_confirms", map[bool]int{true: 3, false: 2}[profile == "balanced"])
        action := "hold"
        if score >= enter {
                if !requireConfirm || confirmHits >= minConfirms {
                        action = "buy"
                } else {
                        reasons = append(reasons, fmt.Sprintf("确认不足(%d/%d)", confirmHits, minConfirms))
                }
        } else if score <= exitTh || nearLow {
                action = "sell"
        }
        if action == "buy" && asBool(regime["force_sell_only"]) {
                action = "hold"
                reasons = append(reasons, fmt.Sprintf("行情%v:只卖", regime["name"]))
        }
        if regime["name"] != nil && fmt.Sprint(regime["name"]) != "" {
                reasons = append(reasons, fmt.Sprintf("行情=%v", regime["name"]))
        }
        var atrPtr *float64
        if hasA {
                atrPtr = &a
        }
        ret5p := ret5
        tev := analyzeTradeEV(score, map[bool]float64{true: price, false: lastClose}[price > 0], atrPtr, &ret5p, e.Cfg)
        if action == "buy" && !asBool(tev["eligible"]) {
                action = "hold"
                reasons = append(reasons, fmt.Sprintf("EV拒绝:%v", tev["reason"]))
        } else if action == "buy" {
                reasons = append(reasons, fmt.Sprintf("EV:%v", tev["reason"]))
        }
        if price <= 0 {
                price = lastClose
        }
        return Signal{
                StockID: sid, Code: code, Name: name, AssetType: asset, Price: price,
                Action: action, Score: score, Confidence: math.Min(math.Abs(score), 1),
                Reason: strings.Join(reasons, "; "),
                Indicators: map[string]any{
                        "ema_fast": f, "ema_slow": s, "rsi": r, "macd_hist": mh, "atr": a,
                        "volume": v, "volume_ma": vma, "close": lastClose, "day_change_pct": dayChg,
                        "ret5": ret5, "confirm_hits": confirmHits, "profile": profile, "regime": regime["name"],
                },
                TradeEV: tev,
        }
}

func newsSentiment(stockID int, news []any) *float64 {
        vals := []float64{}
        for _, n0 := range news {
                n, _ := n0.(map[string]any)
                if n == nil {
                        continue
                }
                ids, _ := n["stock_ids"].([]any)
                hit := false
                for _, id := range ids {
                        if int(asF(id)) == stockID {
                                hit = true
                                break
                        }
                }
                if hit {
                        vals = append(vals, asF(n["sentiment"]))
                }
        }
        if len(vals) == 0 {
                for i, n0 := range news {
                        if i >= 6 {
                                break
                        }
                        if n, ok := n0.(map[string]any); ok {
                                vals = append(vals, asF(n["sentiment"])*0.2)
                        }
                }
        }
        if len(vals) == 0 {
                return nil
        }
        s := 0.0
        for _, v := range vals {
                s += v
        }
        avg := s / float64(len(vals))
        return &avg
}

func first(m map[string]any, keys ...string) any {
        for _, k := range keys {
                if v, ok := m[k]; ok && v != nil {
                        return v
                }
        }
        return nil
}
func asF(v any) float64 {
        switch t := v.(type) {
        case float64:
                return t
        case float32:
                return float64(t)
        case int:
                return float64(t)
        case int64:
                return float64(t)
        case string:
                var f float64
                fmt.Sscanf(t, "%f", &f)
                return f
        default:
                return 0
        }
}
func asBool(v any) bool {
        switch t := v.(type) {
        case bool:
                return t
        case string:
                return t == "1" || strings.EqualFold(t, "true")
        case float64:
                return t != 0
        default:
                return false
        }
}
func maxSlice(xs []float64) float64 {
        m := xs[0]
        for _, v := range xs[1:] {
                if v > m {
                        m = v
                }
        }
        return m
}
func minSlice(xs []float64) float64 {
        m := xs[0]
        for _, v := range xs[1:] {
                if v < m {
                        m = v
                }
        }
        return m
}