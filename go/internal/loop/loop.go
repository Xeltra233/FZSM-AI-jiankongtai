package loop

import (
        "fmt"
        "sort"
        "time"

        "fzsmbot/internal/client"
        "fzsmbot/internal/collector"
        "fzsmbot/internal/config"
        "fzsmbot/internal/modules"
        "fzsmbot/internal/regime"
        "fzsmbot/internal/risk"
        "fzsmbot/internal/storage"
        "fzsmbot/internal/strategy"
        "fzsmbot/internal/trader"
)

func asMap(v any) map[string]any {
        if m, ok := v.(map[string]any); ok && m != nil {
                return m
        }
        return map[string]any{}
}

// Once runs one spot cycle + modules earn path and persists last_loop compatible fields.
func Once(cfg *config.Config, st *storage.Storage, cli *client.Client, cycle int, primary bool) map[string]any {
        control := st.GetStateMap("control")
        if len(control) == 0 {
                control = map[string]any{"trade_mode": "auto"}
        }

        // strategy/risk cfg with EV defaults from risk section
        stratCfg := map[string]any{}
        for k, v := range cfg.Strategy {
                stratCfg[k] = v
        }
        riskCfg := map[string]any{}
        for k, v := range cfg.Risk {
                riskCfg[k] = v
        }
        setDef := func(dst map[string]any, k string, v any) {
                if _, ok := dst[k]; !ok {
                        dst[k] = v
                }
        }
        setDef(stratCfg, "ev_stop_loss_pct", riskCfg["stop_loss_pct"])
        setDef(stratCfg, "ev_take_profit_pct", riskCfg["take_profit_pct"])
        setDef(stratCfg, "ev_max_position_pct", riskCfg["max_position_pct"])
        setDef(stratCfg, "ev_base_position_pct", riskCfg["position_pct"])
        setDef(stratCfg, "kelly_fraction", riskCfg["kelly_fraction"])
        setDef(stratCfg, "fee_rate", riskCfg["fee_rate"])

        uni := map[string]any{"asset_types": []any{"stock", "crypto"}, "exclude_suspended": true, "exclude_delisted": true, "min_price": 0.01}
        if cfg.Universe != nil {
                for k, v := range cfg.Universe {
                        uni[k] = v
                }
        }
        loopCfg := map[string]any{"max_candidates": 8, "kline_period": "1m", "kline_limit": 90, "top_n_trade": 3}
        if cfg.Loop != nil {
                for k, v := range cfg.Loop {
                        loopCfg[k] = v
                }
        }
        if asF(loopCfg["max_candidates"]) > 10 {
                loopCfg["max_candidates"] = 10
        }
        col := collector.New(cli, uni, loopCfg)

        market, err := col.FetchMarket()
        if err != nil {
                market = map[string]any{}
        }
        regEngine := regime.New(cfg.Regime, riskCfg)
        regimeState := regEngine.Detect(market)
        _ = st.SetState("regime", regimeState)

        eng := strategy.New(stratCfg)
        rm := risk.New(riskCfg)
        paperCash := asF(riskCfg["paper_cash"])
        if paperCash <= 0 {
                paperCash = 1000000
        }
        tr := trader.New(cfg.Mode, cli, rm, st, paperCash)
        tr.SetControl(control)
        tr.SetRegime(regimeState)
        tr.ResetCycle()

        news := col.FetchNews(market)
        universe := col.FilterUniverse(market)
        index := asMap(market["index"])

        prices := map[int]float64{}
        for _, s0 := range asSlice(market["stocks"]) {
                s := asMap(s0)
                prices[int(asF(s["id"]))] = asF(s["price"])
        }

        type row struct {
                stock  map[string]any
                klines []map[string]any
                sig    strategy.Signal
        }
        rows := []row{}
        for _, stock := range universe {
                sid := int(asF(stock["id"]))
                kl, _ := col.FetchKlines(sid)
                sig := eng.Analyze(stock, kl, news, regimeState)
                rows = append(rows, row{stock: stock, klines: kl, sig: sig})
                _ = st.LogSignal(map[string]any{
                        "stock_id": sig.StockID, "code": sig.Code, "name": sig.Name, "action": sig.Action,
                        "score": sig.Score, "confidence": sig.Confidence, "price": sig.Price, "reason": sig.Reason,
                        "indicators": sig.Indicators, "trade_ev": sig.TradeEV,
                })
        }

        var buys, sells, holds []strategy.Signal
        for _, r := range rows {
                switch r.sig.Action {
                case "buy":
                        buys = append(buys, r.sig)
                case "sell":
                        sells = append(sells, r.sig)
                default:
                        holds = append(holds, r.sig)
                }
        }
        sort.Slice(buys, func(i, j int) bool { return buys[i].Score > buys[j].Score })
        sort.Slice(sells, func(i, j int) bool { return sells[i].Score < sells[j].Score })
        topN := 3
        if v := asF(col.Loop["top_n_trade"]); v > 0 {
                topN = int(v)
        }
        if rm.CapitalStyle() == "all_in" {
                if fast := int(rm.CfgF("all_in_max_new_entries_per_cycle", 3)); fast > topN {
                        topN = fast
                }
        }
        if topN > len(buys) {
                topN = len(buys)
        }
        ordered := append([]strategy.Signal{}, sells...)
        ordered = append(ordered, buys[:topN]...)
        ordered = append(ordered, holds...)

        trades := []map[string]any{}
        acted := map[int]bool{}
        for _, sig := range ordered {
                if sig.Action != "buy" && sig.Action != "sell" {
                        continue
                }
                res := tr.Execute(sig, prices, 0)
                acted[sig.StockID] = true
                for _, r := range res {
                        trades = append(trades, r)
                }
        }
        // risk patrol holdings
        acc := tr.AccountSnapshot(prices)
        for _, p0 := range asSlice(acc["positions"]) {
                p := asMap(p0)
                sid := int(asF(first(p, "stock_id", "id")))
                if sid <= 0 || acted[sid] {
                        continue
                }
                px := prices[sid]
                if px <= 0 {
                        px = asF(first(p, "price", "current_price"))
                }
                if px <= 0 {
                        continue
                }
                sig := strategy.Signal{
                        StockID: sid, Code: fmt.Sprint(first(p, "code", "symbol", "id")), Name: fmt.Sprint(p["name"]),
                        AssetType: fmt.Sprint(first(p, "asset_type")), Price: px, Action: "hold", Reason: "持仓风控巡检",
                }
                held := asF(first(p, "shares", "quantity"))
                for _, r := range tr.Execute(sig, prices, held) {
                        if stt := fmt.Sprint(r["status"]); stt != "" && stt != "skip" {
                                trades = append(trades, r)
                        }
                }
        }

        // modules earn path (farm/lottery/...) after spot decisions
        bus := modules.RunAll(cfg, st, cli, cycle, primary)
        // paper mode must keep paper account in last_loop (modules bus account is always live)
        if cfg.Mode == "paper" {
                bus["account"] = tr.AccountSnapshot(prices)
        }

        // top signals payload
        allSigs := []strategy.Signal{}
        for _, r := range rows {
                allSigs = append(allSigs, r.sig)
        }
        sort.Slice(allSigs, func(i, j int) bool {
                ai := allSigs[i].Score
                if ai < 0 {
                        ai = -ai
                }
                aj := allSigs[j].Score
                if aj < 0 {
                        aj = -aj
                }
                return ai > aj
        })
        topSignals := []map[string]any{}
        for i, s := range allSigs {
                if i >= 12 {
                        break
                }
                topSignals = append(topSignals, map[string]any{
                        "stock_id": s.StockID, "code": s.Code, "name": s.Name, "action": s.Action,
                        "score": s.Score, "confidence": s.Confidence, "price": s.Price, "reason": s.Reason,
                        "trade_ev": s.TradeEV,
                })
        }
        recentTrades := trades
        if len(recentTrades) > 10 {
                recentTrades = recentTrades[len(recentTrades)-10:]
        }

        profile := "balanced"
        if p := fmt.Sprint(cfg.Strategy["profile"]); p != "" && p != "<nil>" {
                profile = p
        }
        account := asMap(bus["account"])
        if len(account) == 0 || cfg.Mode == "paper" {
                account = tr.AccountSnapshot(prices)
        }
        last := map[string]any{
                "ts":    float64(time.Now().UnixNano()) / 1e9,
                "index": index,
                "account": map[string]any{
                        "mode": account["mode"], "cash": account["cash"], "equity": account["equity"],
                        "stock_value": account["stock_value"], "pnl": account["pnl"], "pnl_pct": account["pnl_pct"],
                        "positions": account["positions"],
                },
                "buy_count": len(buys), "sell_count": len(sells), "trade_count": len(trades),
                "top_signals": topSignals, "recent_trades": recentTrades,
                "control": control, "profile": profile, "regime": regimeState,
                "farm": st.GetStateMap("farm"), "impl": "go", "cycle": cycle,
        }
        _ = st.SetState("last_loop", last)
        _ = st.Snapshot("loop", map[string]any{
                "ts": last["ts"], "index": index, "account": last["account"], "signals": topSignals,
                "trades": trades, "buy_count": len(buys), "sell_count": len(sells), "trade_count": len(trades),
                "control": control, "profile": profile, "regime": regimeState, "impl": "go",
        })
        if primary {
                _ = st.SetState("service", map[string]any{
                        "status": "running", "mode": cfg.Mode, "profile": profile, "cycle": cycle,
                        "last_cycle_at": last["ts"], "impl": "go", "primary": true,
                        "user_name": bus["user_name"], "trade_count": len(trades),
                        "buy_count": len(buys), "sell_count": len(sells), "farm_crop": bus["farm_crop"], "regime": regimeState["name"],
                })
        }
        _ = st.SetState("service_go", map[string]any{
                "status": "running", "mode": cfg.Mode, "profile": profile, "cycle": cycle,
                "last_cycle_at": last["ts"], "impl": "go", "primary": primary,
                "user_name": bus["user_name"], "trade_count": len(trades),
                "buy_count": len(buys), "sell_count": len(sells), "modules": bus["modules"],
                "farm_crop": bus["farm_crop"], "regime": regimeState["name"], "farm_plots": bus["farm_plots"],
        })

        return map[string]any{
                "ok": true, "cycle": cycle, "buy_count": len(buys), "sell_count": len(sells),
                "trade_count": len(trades), "candidates": len(universe), "account": last["account"],
                "modules": bus["modules"], "farm_crop": bus["farm_crop"], "regime": regimeState["name"], "user_name": bus["user_name"],
                "impl": "go",
        }
}

func asSlice(v any) []any {
        if s, ok := v.([]any); ok {
                return s
        }
        return []any{}
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
func first(m map[string]any, keys ...string) any {
        for _, k := range keys {
                if v, ok := m[k]; ok && v != nil {
                        return v
                }
        }
        return nil
}
