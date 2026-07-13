package indicators

import "math"

func SMA(values []float64, period int) []float64 {
        out := make([]float64, len(values))
        for i := range out {
                out[i] = math.NaN()
        }
        if period <= 0 || len(values) < period {
                return out
        }
        sum := 0.0
        for i := 0; i < period; i++ {
                sum += values[i]
        }
        out[period-1] = sum / float64(period)
        for i := period; i < len(values); i++ {
                sum += values[i] - values[i-period]
                out[i] = sum / float64(period)
        }
        return out
}

func EMA(values []float64, period int) []float64 {
        out := make([]float64, len(values))
        for i := range out {
                out[i] = math.NaN()
        }
        if period <= 0 || len(values) == 0 {
                return out
        }
        alpha := 2.0 / (float64(period) + 1.0)
        if len(values) < period {
                prev := values[0]
                out[0] = prev
                for i := 1; i < len(values); i++ {
                        prev = alpha*values[i] + (1-alpha)*prev
                        out[i] = prev
                }
                return out
        }
        sum := 0.0
        for i := 0; i < period; i++ {
                sum += values[i]
        }
        prev := sum / float64(period)
        out[period-1] = prev
        for i := period; i < len(values); i++ {
                prev = alpha*values[i] + (1-alpha)*prev
                out[i] = prev
        }
        return out
}

func RSI(values []float64, period int) []float64 {
        out := make([]float64, len(values))
        for i := range out {
                out[i] = math.NaN()
        }
        if period <= 0 || len(values) <= period {
                return out
        }
        gains, losses := 0.0, 0.0
        for i := 1; i <= period; i++ {
                d := values[i] - values[i-1]
                if d >= 0 {
                        gains += d
                } else {
                        losses -= d
                }
        }
        avgGain := gains / float64(period)
        avgLoss := losses / float64(period)
        if avgLoss == 0 {
                out[period] = 100
        } else {
                rs := avgGain / avgLoss
                out[period] = 100 - (100 / (1 + rs))
        }
        for i := period + 1; i < len(values); i++ {
                d := values[i] - values[i-1]
                gain := math.Max(d, 0)
                loss := math.Max(-d, 0)
                avgGain = (avgGain*float64(period-1) + gain) / float64(period)
                avgLoss = (avgLoss*float64(period-1) + loss) / float64(period)
                if avgLoss == 0 {
                        out[i] = 100
                } else {
                        rs := avgGain / avgLoss
                        out[i] = 100 - (100 / (1 + rs))
                }
        }
        return out
}

func MACD(values []float64, fast, slow, signal int) (line, sig, hist []float64) {
        ef := EMA(values, fast)
        es := EMA(values, slow)
        line = make([]float64, len(values))
        sig = make([]float64, len(values))
        hist = make([]float64, len(values))
        denseIdx := []int{}
        denseVals := []float64{}
        for i := range values {
                line[i], sig[i], hist[i] = math.NaN(), math.NaN(), math.NaN()
                if !math.IsNaN(ef[i]) && !math.IsNaN(es[i]) {
                        line[i] = ef[i] - es[i]
                        denseIdx = append(denseIdx, i)
                        denseVals = append(denseVals, line[i])
                }
        }
        denseSig := EMA(denseVals, signal)
        for j, i := range denseIdx {
                if j < len(denseSig) && !math.IsNaN(denseSig[j]) {
                        sig[i] = denseSig[j]
                        hist[i] = line[i] - sig[i]
                }
        }
        return
}

func ATR(highs, lows, closes []float64, period int) []float64 {
        n := len(closes)
        if len(highs) < n {
                n = len(highs)
        }
        if len(lows) < n {
                n = len(lows)
        }
        trs := make([]float64, n)
        for i := 0; i < n; i++ {
                if i == 0 {
                        trs[i] = highs[i] - lows[i]
                        continue
                }
                tr := highs[i] - lows[i]
                tr = math.Max(tr, math.Abs(highs[i]-closes[i-1]))
                tr = math.Max(tr, math.Abs(lows[i]-closes[i-1]))
                trs[i] = tr
        }
        return EMA(trs, period)
}

func LastValid(series []float64) (float64, bool) {
        for i := len(series) - 1; i >= 0; i-- {
                if !math.IsNaN(series[i]) {
                        return series[i], true
                }
        }
        return 0, false
}

func ExtractOHLCV(klines []map[string]any) (open, high, low, close, volume, ts []float64) {
        for _, k := range klines {
                open = append(open, asF(first(k, "open", "o")))
                high = append(high, asF(first(k, "high", "h")))
                low = append(low, asF(first(k, "low", "l")))
                close = append(close, asF(first(k, "close", "c")))
                volume = append(volume, asF(first(k, "volume", "v")))
                ts = append(ts, asF(first(k, "ts", "time")))
        }
        return
}

func first(m map[string]any, keys ...string) any {
        for _, k := range keys {
                if v, ok := m[k]; ok && v != nil {
                        return v
                }
        }
        return 0
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
        default:
                return 0
        }
}

// avoid importing encoding/json solely for Number; keep local alias unused
type jsonNumber float64