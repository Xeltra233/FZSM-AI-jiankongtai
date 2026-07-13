package main

import (
        "encoding/json"
        "fmt"
        "os"
        "path/filepath"

        "fzsmbot/internal/client"
        "fzsmbot/internal/config"
        "fzsmbot/internal/risk"
        "fzsmbot/internal/storage"
        "fzsmbot/internal/strategy"
        "fzsmbot/internal/trader"
)

func main() {
        cfgPath := "goal-2/config.paper.go.yaml"
        if _, err := os.Stat(cfgPath); err != nil {
                cfgPath = filepath.Join("..", cfgPath)
        }
        // use fresh db path relative project root
        cfg, err := config.Load(cfgPath)
        if err != nil {
                panic(err)
        }
        cfg.Mode = "paper"
        cfg.Storage.DBPath = "data/bot_paper_probe.db"
        _ = os.Remove(cfg.Storage.DBPath)
        st, err := storage.Open(cfg.Storage.DBPath)
        if err != nil {
                panic(err)
        }
        defer st.Close()
        cli, err := client.New(cfg.APIBase, "https://api.fanzisima.xyz", cfg.CookieFile)
        if err != nil {
                panic(err)
        }
        rm := risk.New(cfg.Risk)
        tr := trader.New("paper", cli, rm, st, 1000000)
        tr.SetControl(map[string]any{"trade_mode": "auto"})
        tr.SetRegime(map[string]any{
                "name": "neutral", "allow_new_entries": true, "force_sell_only": false,
                "position_scale": 1.0, "max_positions": 6, "max_new_entries_per_cycle": 2,
        })
        prices := map[int]float64{101: 10.0, 102: 20.0}
        buy := strategy.Signal{
                StockID: 101, Code: "P-TEST1", Name: "????1", AssetType: "stock", Price: 10,
                Action: "buy", Score: 0.8, Confidence: 0.8, Reason: "paper-probe-buy",
                TradeEV: map[string]any{"net_edge": 0.02, "target_position_pct": 0.1, "eligible": true},
        }
        res1 := tr.Execute(buy, prices, 0)
        // second buy another symbol
        buy2 := buy; buy2.StockID = 102; buy2.Code = "P-TEST2"; buy2.Price = 20; buy2.Reason = "paper-probe-buy2"
        res2 := tr.Execute(buy2, prices, 0)
        // sell first
        sell := buy; sell.Action = "sell"; sell.Reason = "paper-probe-sell"
        res3 := tr.Execute(sell, prices, 0)
        acc := tr.AccountSnapshot(prices)
        out := map[string]any{"buy1": res1, "buy2": res2, "sell1": res3, "account": acc}
        b, _ := json.MarshalIndent(out, "", "  ")
        fmt.Println(string(b))
}
