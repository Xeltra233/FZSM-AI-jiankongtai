package collector

import (
        "fmt"
        "sort"
        "strings"

        "fzsmbot/internal/client"
)

type Collector struct {
        Client   *client.Client
        Universe map[string]any
        Loop     map[string]any
}

func New(c *client.Client, universe, loop map[string]any) *Collector {
        return &Collector{Client: c, Universe: universe, Loop: loop}
}

func (c *Collector) FetchMarket() (map[string]any, error) {
        return c.Client.Market()
}

func (c *Collector) FilterUniverse(market map[string]any) []map[string]any {
        stocks := asSliceMap(market["stocks"])
        allowed := map[string]bool{}
        rawAllowed, _ := c.Universe["asset_types"].([]any)
        if len(rawAllowed) == 0 {
                allowed["stock"] = true
                allowed["crypto"] = true
        } else {
                for _, a := range rawAllowed {
                        s := strings.ToLower(fmt.Sprint(a))
                        if s == "all" {
                                allowed["stock"] = true
                                allowed["crypto"] = true
                                allowed["futures"] = true
                        } else {
                                allowed[s] = true
                        }
                }
        }
        excludeSuspended := asBool(c.Universe["exclude_suspended"], true)
        excludeDelisted := asBool(c.Universe["exclude_delisted"], true)
        minPrice := asF(c.Universe["min_price"])
        if minPrice <= 0 {
                minPrice = 0.01
        }
        out := []map[string]any{}
        for _, s := range stocks {
                asset := strings.ToLower(fmt.Sprint(first(s, "asset_type")))
                if asset == "" || asset == "<nil>" {
                        asset = "stock"
                }
                if !allowed[asset] {
                        continue
                }
                if excludeSuspended && int(asF(s["is_suspended"])) == 1 {
                        continue
                }
                if excludeDelisted && int(asF(s["is_delisted"])) == 1 {
                        continue
                }
                status := strings.ToLower(fmt.Sprint(s["status"]))
                if status == "suspended" || status == "halt" || status == "halted" || status == "delisted" {
                        continue
                }
                if asF(s["price"]) < minPrice {
                        continue
                }
                out = append(out, s)
        }
        sort.Slice(out, func(i, j int) bool {
                ai := abs(asF(out[i]["change_pct"]))
                aj := abs(asF(out[j]["change_pct"]))
                if ai == aj {
                        return asF(out[i]["float_shares"]) > asF(out[j]["float_shares"])
                }
                return ai > aj
        })
        maxN := int(asF(c.Loop["max_candidates"]))
        if maxN <= 0 {
                maxN = 12
        }
        if len(out) > maxN {
                out = out[:maxN]
        }
        return out
}

func (c *Collector) FetchKlines(stockID int) ([]map[string]any, error) {
        period := fmt.Sprint(c.Loop["kline_period"])
        if period == "" || period == "<nil>" {
                period = "1m"
        }
        limit := int(asF(c.Loop["kline_limit"]))
        if limit <= 0 {
                limit = 120
        }
        code, data, err := c.Client.StocksGet(fmt.Sprintf("/klines?stock_id=%d&period=%s&limit=%d", stockID, period, limit))
        if err != nil {
                return nil, err
        }
        if code >= 400 {
                return nil, fmt.Errorf("klines status=%d", code)
        }
        arr := []map[string]any{}
        switch t := data.(type) {
        case []any:
                for _, it := range t {
                        if m, ok := it.(map[string]any); ok {
                                arr = append(arr, m)
                        }
                }
        case map[string]any:
                if d, ok := t["data"].([]any); ok {
                        for _, it := range d {
                                if m, ok := it.(map[string]any); ok {
                                        arr = append(arr, m)
                                }
                        }
                }
        }
        sort.Slice(arr, func(i, j int) bool { return asF(arr[i]["ts"]) < asF(arr[j]["ts"]) })
        return arr, nil
}

func (c *Collector) FetchNews(market map[string]any) []any {
        if arr, ok := market["news"].([]any); ok {
                return arr
        }
        return []any{}
}

func asSliceMap(v any) []map[string]any {
        out := []map[string]any{}
        arr, _ := v.([]any)
        for _, it := range arr {
                if m, ok := it.(map[string]any); ok {
                        out = append(out, m)
                }
        }
        return out
}
func asF(v any) float64 {
        switch t := v.(type) {
        case float64:
                return t
        case int:
                return float64(t)
        case int64:
                return float64(t)
        default:
                return 0
        }
}
func asBool(v any, def bool) bool {
        if v == nil {
                return def
        }
        if b, ok := v.(bool); ok {
                return b
        }
        return def
}
func abs(v float64) float64 {
        if v < 0 {
                return -v
        }
        return v
}
func first(m map[string]any, keys ...string) any {
        for _, k := range keys {
                if v, ok := m[k]; ok {
                        return v
                }
        }
        return nil
}