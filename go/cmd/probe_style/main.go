package main

import (
        "encoding/json"
        "fmt"
        "os"

        "fzsmbot/internal/client"
        "fzsmbot/internal/risk"
        "fzsmbot/internal/storage"
        "fzsmbot/internal/strategy"
        "fzsmbot/internal/trader"
)

func main() {
        _ = os.Remove("data/bot_style_probe.db")
        st, err := storage.Open("data/bot_style_probe.db")
        if err != nil { panic(err) }
        defer st.Close()
        cfgRisk := map[string]any{"cash_reserve_pct": 0.12, "position_pct": 0.12, "max_position_pct": 0.2, "max_positions": 6, "max_new_entries_per_cycle": 2, "cooldown_sec": 0}
        c, err := client.New("https://fanzisima.xyz/stocks/api", "https://api.fanzisima.xyz", "auth/cookies.json")
        if err != nil { panic(err) }
        rm := risk.New(cfgRisk)
        tr := trader.New("paper", c, rm, st, 1000000)
        tr.SetControl(map[string]any{"trade_mode": "auto", "capital_style": "prefer_hold"})
        prices := map[int]float64{1: 10}
        buy := strategy.Signal{StockID: 1, Code: "T1", Name: "T1", Price: 10, Action: "buy", Score: 0.8, Reason: "probe-buy", TradeEV: map[string]any{"net_edge": 0.03, "target_position_pct": 0.1, "eligible": true}}
        fmt.Println("buy", j(tr.Execute(buy, prices, 0)))
        sell := strategy.Signal{StockID: 1, Code: "T1", Name: "T1", Price: 10.1, Action: "sell", Score: -0.1, Reason: "probe-weak-sell"}
        fmt.Println("hold_weak_sell", j(tr.Execute(sell, map[int]float64{1: 10.1}, 0)))
        tr.SetControl(map[string]any{"trade_mode": "auto", "capital_style": "prefer_cash"})
        sell2 := strategy.Signal{StockID: 1, Code: "T1", Name: "T1", Price: 10.2, Action: "sell", Score: -0.2, Reason: "probe-cash-sell"}
        fmt.Println("cash_sell", j(tr.Execute(sell2, map[int]float64{1: 10.2}, 0)))
        fmt.Println("account", j(tr.AccountSnapshot(map[int]float64{1: 10.2})))
}

func j(v any) string { b, _ := json.Marshal(v); return string(b) }
