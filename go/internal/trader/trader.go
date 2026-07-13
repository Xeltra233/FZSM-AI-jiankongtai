package trader

import (
        "fmt"
        "math"
        "strings"
        "time"

        "fzsmbot/internal/client"
        "fzsmbot/internal/risk"
        "fzsmbot/internal/storage"
        "fzsmbot/internal/strategy"
)

type Trader struct {
        Mode             string
        Client           *client.Client
        Risk             *risk.Manager
        Storage          *storage.Storage
        Control          map[string]any
        Regime           map[string]any
        EntriesThisCycle int
        lastTradeUnix    float64
        Paper            *PaperBroker
        PaperCashSeed    float64
}

type PaperBroker struct {
        Cash      float64
        Positions map[int]*risk.Position
        Storage   *storage.Storage
}

func New(mode string, c *client.Client, rm *risk.Manager, st *storage.Storage, paperCash float64) *Trader {
        t := &Trader{
                Mode: mode, Client: c, Risk: rm, Storage: st,
                Control: map[string]any{"trade_mode": "auto"}, Regime: map[string]any{},
                PaperCashSeed: paperCash,
        }
        peaks := st.GetStateMap("price_peaks")
        for k, v := range peaks {
                var id int
                fmt.Sscanf(k, "%d", &id)
                if id > 0 {
                        rm.Peaks[id] = asF(v)
                }
        }
        if mode == "paper" {
                t.Paper = loadPaper(st, paperCash)
        }
        return t
}

func loadPaper(st *storage.Storage, seed float64) *PaperBroker {
        pb := &PaperBroker{Cash: seed, Positions: map[int]*risk.Position{}, Storage: st}
        cash, positions, ok := st.LoadPaper()
        if ok {
                pb.Cash = cash
                for _, p := range positions {
                        sid := int(asF(first(p, "stock_id", "id")))
                        if sid <= 0 {
                                continue
                        }
                        shares := asF(p["shares"])
                        if shares <= 0 {
                                continue
                        }
                        pb.Positions[sid] = &risk.Position{
                                StockID: sid,
                                Code:    fmt.Sprint(first(p, "code")),
                                Name:    fmt.Sprint(first(p, "name")),
                                Shares:  shares,
                                AvgPrice: asF(first(p, "avg_price", "cost_price", "avg_cost")),
                                OpenedAt: asF(first(p, "opened_at")),
                                HighestPrice: asF(first(p, "highest_price", "avg_price")),
                        }
                        if pb.Positions[sid].OpenedAt <= 0 {
                                pb.Positions[sid].OpenedAt = float64(time.Now().Unix())
                        }
                }
        } else {
                _ = pb.persist()
        }
        return pb
}

func (p *PaperBroker) persist() error {
        arr := []map[string]any{}
        for _, pos := range p.Positions {
                arr = append(arr, map[string]any{
                        "stock_id": pos.StockID, "code": pos.Code, "name": pos.Name,
                        "shares": pos.Shares, "avg_price": pos.AvgPrice, "opened_at": pos.OpenedAt,
                        "highest_price": pos.HighestPrice,
                })
        }
        return p.Storage.SavePaper(p.Cash, arr)
}

func (p *PaperBroker) equity(prices map[int]float64) float64 {
        total := p.Cash
        for sid, pos := range p.Positions {
                px := prices[sid]
                if px <= 0 {
                        px = pos.AvgPrice
                }
                total += pos.Shares * px
        }
        return total
}

func (p *PaperBroker) buy(sig strategy.Signal, shares float64, reason string) map[string]any {
        cost := shares * sig.Price
        if cost > p.Cash+1e-9 {
                return map[string]any{"status": "rejected", "reason": "??????", "shares": shares, "price": sig.Price, "mode": "paper", "stock_id": sig.StockID, "code": sig.Code, "side": "buy"}
        }
        p.Cash -= cost
        if pos := p.Positions[sig.StockID]; pos != nil {
                ns := pos.Shares + shares
                pos.AvgPrice = (pos.AvgPrice*pos.Shares + sig.Price*shares) / math.Max(ns, 1e-9)
                pos.Shares = ns
                if sig.Price > pos.HighestPrice {
                        pos.HighestPrice = sig.Price
                }
        } else {
                p.Positions[sig.StockID] = &risk.Position{
                        StockID: sig.StockID, Code: sig.Code, Name: sig.Name, Shares: shares,
                        AvgPrice: sig.Price, OpenedAt: float64(time.Now().Unix()), HighestPrice: sig.Price,
                }
        }
        _ = p.persist()
        trade := map[string]any{
                "mode": "paper", "stock_id": sig.StockID, "code": sig.Code, "side": "buy",
                "shares": shares, "price": sig.Price, "status": "filled", "reason": reason,
                "raw": map[string]any{"cash": p.Cash},
        }
        _ = p.Storage.LogTrade(trade)
        return trade
}

func (p *PaperBroker) sell(sig strategy.Signal, shares float64, reason string) map[string]any {
        pos := p.Positions[sig.StockID]
        if pos == nil || pos.Shares <= 0 {
                return map[string]any{"status": "rejected", "reason": "?????", "shares": 0, "price": sig.Price, "mode": "paper", "stock_id": sig.StockID, "code": sig.Code, "side": "sell"}
        }
        qty := shares
        if qty <= 0 || qty > pos.Shares {
                qty = pos.Shares
        }
        p.Cash += qty * sig.Price
        pos.Shares -= qty
        if pos.Shares <= 1e-12 {
                delete(p.Positions, sig.StockID)
        }
        _ = p.persist()
        trade := map[string]any{
                "mode": "paper", "stock_id": sig.StockID, "code": sig.Code, "side": "sell",
                "shares": qty, "price": sig.Price, "status": "filled", "reason": reason,
                "raw": map[string]any{"cash": p.Cash},
        }
        _ = p.Storage.LogTrade(trade)
        return trade
}

func (t *Trader) SetControl(c map[string]any) {
        mode := "auto"
        style := "prefer_hold"
        if c != nil {
                mode = strings.ToLower(strings.TrimSpace(fmt.Sprint(c["trade_mode"])))
                style = strings.ToLower(strings.TrimSpace(fmt.Sprint(c["capital_style"])))
        }
        if mode != "auto" && mode != "sell_only" && mode != "paused" {
                mode = "auto"
        }
        switch style {
        case "cash", "prefer_cash":
                style = "prefer_cash"
        case "all", "all_in", "full":
                style = "all_in"
        default:
                style = "prefer_hold"
        }
        t.Control = map[string]any{"trade_mode": mode, "capital_style": style}
        if t.Risk != nil {
                t.Risk.SetControl(t.Control)
        }
}
func (t *Trader) SetRegime(r map[string]any) {
        if r == nil {
                r = map[string]any{}
        }
        t.Regime = r
        t.Risk.SetRegime(r)
}
func (t *Trader) ResetCycle() { t.EntriesThisCycle = 0; t.lastTradeUnix = 0 }
func (t *Trader) persistPeaks() {
        payload := map[string]any{}
        for k, v := range t.Risk.Peaks {
                payload[fmt.Sprint(k)] = v
        }
        _ = t.Storage.SetState("price_peaks", payload)
}

func (t *Trader) AccountSnapshot(prices map[int]float64) map[string]any {
        if t.Mode == "paper" && t.Paper != nil {
                positions := []any{}
                stock := 0.0
                for sid, pos := range t.Paper.Positions {
                        px := prices[sid]
                        if px <= 0 {
                                px = pos.AvgPrice
                        }
                        mv := pos.Shares * px
                        stock += mv
                        positions = append(positions, map[string]any{
                                "stock_id": pos.StockID, "id": pos.StockID, "code": pos.Code, "name": pos.Name,
                                "shares": pos.Shares, "quantity": pos.Shares, "avg_price": pos.AvgPrice,
                                "price": px, "market_value": mv, "opened_at": pos.OpenedAt, "highest_price": pos.HighestPrice,
                        })
                }
                return map[string]any{
                        "mode": "paper", "cash": t.Paper.Cash, "equity": t.Paper.equity(prices),
                        "stock_value": stock, "positions": positions,
                }
        }
        me, _ := t.Client.StocksMe()
        pf, _ := t.Client.Portfolio()
        positions := []any{}
        if arr, ok := pf["positions"].([]any); ok {
                positions = arr
        } else if arr, ok := me["positions"].([]any); ok {
                positions = arr
        }
        cash := asF(me["balance_lobster"])
        equity := asF(me["total_asset_lobster"])
        if equity == 0 {
                equity = cash
        }
        stock := asF(me["stock_value_lobster"])
        if stock == 0 {
                for _, p0 := range positions {
                        p, _ := p0.(map[string]any)
                        stock += asF(p["market_value"])
                }
                if stock == 0 && equity > 0 {
                        stock = equity - cash
                }
        }
        return map[string]any{
                "mode": "live", "cash": cash, "equity": equity, "stock_value": stock,
                "pnl": me["pnl"], "pnl_pct": me["pnl_pct"], "positions": positions, "me": me, "portfolio": pf,
        }
}

func (t *Trader) maxNewEntries() int {
        if v := asF(t.Regime["max_new_entries_per_cycle"]); v > 0 || t.Regime["max_new_entries_per_cycle"] != nil {
                return int(v)
        }
        return int(t.Risk.CfgF("max_new_entries_per_cycle", 1))
}
func (t *Trader) throttle() {
        gap := t.Risk.CfgF("min_trade_gap_sec", 1.2)
        wait := t.lastTradeUnix + gap - float64(time.Now().UnixNano())/1e9
        if wait > 0 {
                time.Sleep(time.Duration(wait * float64(time.Second)))
        }
}
func (t *Trader) markTradeTS() { t.lastTradeUnix = float64(time.Now().UnixNano()) / 1e9 }


func (t *Trader) capitalStyle() string {
        if t.Risk != nil {
                return t.Risk.CapitalStyle()
        }
        style := strings.ToLower(strings.TrimSpace(fmt.Sprint(t.Control["capital_style"])))
        switch style {
        case "cash", "prefer_cash":
                return "prefer_cash"
        case "all", "all_in", "full":
                return "all_in"
        default:
                return "prefer_hold"
        }
}

// styleSellPlan returns sellQty(<=0 means full), reason, skip.
// Hard stops/ROI are handled before this for holdings.
func (t *Trader) styleSellPlan(action string, score, avg, price, held float64, signalReason string) (qty float64, reason string, skip bool) {
        if action != "sell" || held <= 0 {
                return 0, "", true
        }
        style := t.capitalStyle()
        pnl := 0.0
        if avg > 0 && price > 0 {
                pnl = (price - avg) / avg
        }
        switch style {
        case "prefer_hold":
                if pnl < 0.03 {
                        return 0, fmt.Sprintf("????:????(%.2f%%)", pnl*100), true
                }
                if score > -0.25 {
                        return 0, fmt.Sprintf("????:??????(score=%.3f)", score), true
                }
                return 0, fmt.Sprintf("????:???? | %s", signalReason), false // qty 0 => full
        case "prefer_cash":
                if pnl >= 0.015 && score <= -0.05 {
                        q := math.Max(math.Floor(held*0.55), 1)
                        if q >= held {
                                return 0, fmt.Sprintf("????:???? | %s", signalReason), false
                        }
                        return q, fmt.Sprintf("????:????(%.2f%%) | %s", pnl*100, signalReason), false
                }
                if pnl >= 0.04 {
                        q := math.Max(math.Floor(held*0.35), 1)
                        if q > held {
                                q = held
                        }
                        return q, fmt.Sprintf("????:????(%.2f%%)", pnl*100), false
                }
                if score <= -0.35 && pnl > -0.01 {
                        q := math.Max(math.Floor(held*0.45), 1)
                        if q > held {
                                q = held
                        }
                        return q, fmt.Sprintf("????:????? | %s", signalReason), false
                }
                return 0, fmt.Sprintf("????:?????(score=%.3f,pnl=%.2f%%)", score, pnl*100), true
        default: // all_in
                if score > -0.12 && pnl < 0.01 {
                        return 0, fmt.Sprintf("????:???????(score=%.3f)", score), true
                }
                return 0, fmt.Sprintf("????:????? | %s", signalReason), false
        }
}

func (t *Trader) Execute(sig strategy.Signal, prices map[int]float64, heldShares float64) []map[string]any {
        tradeMode := fmt.Sprint(t.Control["trade_mode"])
        if tradeMode == "paused" {
                return []map[string]any{{"status": "skip", "reason": "?????", "stock_id": sig.StockID, "code": sig.Code}}
        }
        if tradeMode == "sell_only" && sig.Action == "buy" {
                return []map[string]any{{"status": "skip", "reason": "?????", "stock_id": sig.StockID, "code": sig.Code}}
        }
        if asBool(t.Regime["force_sell_only"]) && sig.Action == "buy" {
                return []map[string]any{{"status": "skip", "reason": fmt.Sprintf("??%v:??", t.Regime["name"]), "stock_id": sig.StockID, "code": sig.Code}}
        }
        if t.Risk.InCooldown(sig.StockID) && (sig.Action == "buy" || sig.Action == "sell") {
                return []map[string]any{{"status": "skip", "reason": "???", "stock_id": sig.StockID, "code": sig.Code}}
        }
        if t.Mode == "paper" && t.Paper != nil {
                return t.executePaper(sig, prices, tradeMode)
        }
        return t.executeLive(sig, prices, heldShares, tradeMode)
}

func (t *Trader) executePaper(sig strategy.Signal, prices map[int]float64, tradeMode string) []map[string]any {
        pos := t.Paper.Positions[sig.StockID]
        if pos != nil {
                peak := t.Risk.UpdatePeak(pos.StockID, sig.Price, pos.HighestPrice)
                pos.HighestPrice = peak
                stop, why := t.Risk.ShouldStop(*pos, sig.Price, peak)
                if stop && tradeMode != "paused" {
                        tr := t.Paper.sell(sig, 0, why)
                        t.Risk.MarkTrade(sig.StockID)
                        t.Risk.ClearPeak(sig.StockID)
                        t.persistPeaks()
                        return []map[string]any{tr}
                }
                frac := t.Risk.ReduceFraction()
                if frac > 0 && tradeMode != "paused" && sig.Action != "buy" {
                        pnl := (sig.Price - pos.AvgPrice) / math.Max(pos.AvgPrice, 1e-9)
                        if pnl < 0.03 {
                                qty := math.Max(math.Floor(pos.Shares*frac), 1)
                                if qty > 0 && qty < pos.Shares {
                                        tr := t.Paper.sell(sig, qty, fmt.Sprintf("????%.0f%%", frac*100))
                                        t.Risk.MarkTrade(sig.StockID)
                                        t.persistPeaks()
                                        return []map[string]any{tr}
                                }
                        }
                }
        }
        if sig.Action == "buy" {
                maxNew := t.maxNewEntries()
                if t.EntriesThisCycle >= maxNew && pos == nil {
                        return []map[string]any{{"status": "skip", "reason": "????????", "stock_id": sig.StockID}}
                }
                if pos != nil {
                        if ok, why := t.Risk.AllowAdd(pos.AvgPrice, sig.Price); !ok {
                                return []map[string]any{{"status": "skip", "reason": why, "stock_id": sig.StockID, "code": sig.Code}}
                        }
                }
                var targetPct, tradeEV *float64
                if tev := sig.TradeEV; tev != nil {
                        if v, ok := tev["target_position_pct"]; ok {
                                f := asF(v); targetPct = &f
                        }
                        if v, ok := tev["net_edge"]; ok {
                                f := asF(v); tradeEV = &f
                        }
                }
                d := t.Risk.SizeBuy(t.Paper.equity(prices), t.Paper.Cash, sig.Price, len(t.Paper.Positions), sig.Score, targetPct, tradeEV)
                if !d.Allow {
                        return []map[string]any{{"status": "skip", "reason": d.Reason, "stock_id": sig.StockID}}
                }
                tr := t.Paper.buy(sig, d.Shares, sig.Reason+" | "+d.Reason)
                t.Risk.UpdatePeak(sig.StockID, sig.Price, 0)
                t.Risk.MarkTrade(sig.StockID)
                t.EntriesThisCycle++
                t.persistPeaks()
                return []map[string]any{tr}
        }
        if sig.Action == "sell" && pos != nil {
                qty, why, skip := t.styleSellPlan(sig.Action, sig.Score, pos.AvgPrice, sig.Price, pos.Shares, sig.Reason)
                if skip {
                        return []map[string]any{{"status": "skip", "reason": why, "stock_id": sig.StockID, "code": sig.Code}}
                }
                tr := t.Paper.sell(sig, qty, why)
                t.Risk.MarkTrade(sig.StockID)
                if qty <= 0 || qty >= pos.Shares {
                        t.Risk.ClearPeak(sig.StockID)
                }
                t.persistPeaks()
                return []map[string]any{tr}
        }
        return []map[string]any{}
}

func (t *Trader) executeLive(sig strategy.Signal, prices map[int]float64, heldShares float64, tradeMode string) []map[string]any {
        acc := t.AccountSnapshot(prices)
        positions, _ := acc["positions"].([]any)
        cash := asF(acc["cash"])
        equity := asF(acc["equity"])
        held := heldShares
        avg := 0.0
        var rawPos map[string]any
        for _, p0 := range positions {
                p, _ := p0.(map[string]any)
                if p == nil {
                        continue
                }
                psid := int(asF(first(p, "stock_id", "id")))
                if psid != sig.StockID {
                        continue
                }
                rawPos = p
                if held <= 0 {
                        held = asF(first(p, "shares", "quantity"))
                }
                avg = asF(first(p, "avg_price", "cost_price", "avg_cost"))
                break
        }
        if held > 0 {
                seed := avg
                if seed <= 0 {
                        seed = sig.Price
                }
                peak := t.Risk.UpdatePeak(sig.StockID, sig.Price, seed)
                t.persistPeaks()
                if avg > 0 {
                        pos := risk.Position{StockID: sig.StockID, Code: sig.Code, Name: sig.Name, Shares: held, AvgPrice: avg, OpenedAt: openedAt(rawPos), HighestPrice: peak}
                        if stop, why := t.Risk.ShouldStop(pos, sig.Price, peak); stop {
                                sellShares := int(held)
                                if prev, err := t.Client.Preview(sig.StockID, "sell", sellShares); err == nil {
                                        avail := int(asF(prev["available_shares"]))
                                        if avail <= 0 {
                                                return []map[string]any{{"status": "skip", "reason": "??????(??T+1)", "stock_id": sig.StockID, "code": sig.Code}}
                                        }
                                        if avail < sellShares {
                                                sellShares = avail
                                        }
                                }
                                t.throttle()
                                tr := t.liveSell(sig, sellShares, why)
                                t.markTradeTS(); t.Risk.MarkTrade(sig.StockID)
                                if sellShares >= int(held) {
                                        t.Risk.ClearPeak(sig.StockID)
                                }
                                t.persistPeaks()
                                return []map[string]any{tr}
                        }
                        frac := t.Risk.ReduceFraction()
                        if frac > 0 && tradeMode != "paused" && sig.Action != "buy" {
                                pnl := (sig.Price - avg) / math.Max(avg, 1e-9)
                                if pnl < 0.03 {
                                        qty := int(math.Max(math.Floor(held*frac), 1))
                                        if qty > 0 && qty < int(held) {
                                                if prev, err := t.Client.Preview(sig.StockID, "sell", qty); err == nil {
                                                        avail := int(asF(prev["available_shares"]))
                                                        if avail <= 0 {
                                                                return []map[string]any{{"status": "skip", "reason": "??????(??T+1)", "stock_id": sig.StockID, "code": sig.Code}}
                                                        }
                                                        if avail < qty {
                                                                qty = avail
                                                        }
                                                }
                                                if qty > 0 {
                                                        t.throttle()
                                                        tr := t.liveSell(sig, qty, fmt.Sprintf("????%.0f%%", frac*100))
                                                        t.markTradeTS(); t.Risk.MarkTrade(sig.StockID)
                                                        return []map[string]any{tr}
                                                }
                                        }
                                }
                        }
                }
        }
        if sig.Action == "buy" {
                maxNew := t.maxNewEntries()
                already := false
                for _, p0 := range positions {
                        p, _ := p0.(map[string]any)
                        if int(asF(first(p, "stock_id", "id"))) == sig.StockID {
                                already = true
                                posVal := asF(p["market_value"])
                                if posVal == 0 {
                                        posVal = asF(p["shares"]) * sig.Price
                                }
                                maxSingle := t.Risk.CfgF("max_single_position_pct", 0.22) * math.Max(equity, 1)
                                if posVal >= maxSingle {
                                        return []map[string]any{{"status": "skip", "reason": "??????", "stock_id": sig.StockID, "code": sig.Code}}
                                }
                                avgHere := asF(first(p, "avg_price", "cost_price", "avg_cost"))
                                if ok, why := t.Risk.AllowAdd(avgHere, sig.Price); !ok {
                                        return []map[string]any{{"status": "skip", "reason": why, "stock_id": sig.StockID, "code": sig.Code}}
                                }
                        }
                }
                if t.EntriesThisCycle >= maxNew && !already {
                        return []map[string]any{{"status": "skip", "reason": "????????", "stock_id": sig.StockID}}
                }
                var targetPct, tradeEV *float64
                if tev := sig.TradeEV; tev != nil {
                        if v, ok := tev["target_position_pct"]; ok {
                                f := asF(v); targetPct = &f
                        }
                        if v, ok := tev["net_edge"]; ok {
                                f := asF(v); tradeEV = &f
                        }
                }
                d := t.Risk.SizeBuy(max3(equity, cash, 1), cash, sig.Price, len(positions), sig.Score, targetPct, tradeEV)
                if !d.Allow {
                        return []map[string]any{{"status": "skip", "reason": d.Reason, "stock_id": sig.StockID}}
                }
                t.throttle()
                tr := t.liveBuy(sig, int(d.Shares), sig.Reason+" | "+d.Reason)
                t.Risk.UpdatePeak(sig.StockID, sig.Price, 0)
                t.persistPeaks(); t.markTradeTS(); t.Risk.MarkTrade(sig.StockID)
                t.EntriesThisCycle++
                return []map[string]any{tr}
        }
        if sig.Action == "sell" && held > 0 {
                qty, why, skip := t.styleSellPlan(sig.Action, sig.Score, avg, sig.Price, held, sig.Reason)
                if skip {
                        return []map[string]any{{"status": "skip", "reason": why, "stock_id": sig.StockID, "code": sig.Code}}
                }
                sellShares := int(held)
                if qty > 0 {
                        sellShares = int(math.Min(held, qty))
                }
                if prev, err := t.Client.Preview(sig.StockID, "sell", sellShares); err == nil {
                        avail := int(asF(prev["available_shares"]))
                        if avail > 0 && avail < sellShares {
                                sellShares = avail
                        } else if avail <= 0 {
                                return []map[string]any{{"status": "skip", "reason": "??????", "stock_id": sig.StockID, "code": sig.Code}}
                        }
                }
                t.throttle()
                tr := t.liveSell(sig, sellShares, why)
                t.markTradeTS(); t.Risk.MarkTrade(sig.StockID)
                if sellShares >= int(held) {
                        t.Risk.ClearPeak(sig.StockID)
                }
                t.persistPeaks()
                return []map[string]any{tr}
        }
        return []map[string]any{}
}

func (t *Trader) liveBuy(sig strategy.Signal, shares int, reason string) map[string]any {
        var prev any
        if p, err := t.Client.Preview(sig.StockID, "buy", shares); err == nil {
                prev = p
        }
        raw, err := t.Client.BuyMarket(sig.StockID, shares)
        trade := map[string]any{
                "mode": "live", "stock_id": sig.StockID, "code": sig.Code, "side": "buy",
                "shares": shares, "price": sig.Price, "status": "submitted", "reason": reason,
                "raw": map[string]any{"preview": prev, "result": raw},
        }
        if err != nil {
                trade["status"] = "error"
                trade["reason"] = err.Error()
                trade["raw"] = raw
        } else {
                if v := asF(raw["avg_price"]); v > 0 {
                        trade["price"] = v
                }
                if v := asF(raw["filled_shares"]); v > 0 {
                        trade["shares"] = v
                }
        }
        _ = t.Storage.LogTrade(trade)
        return trade
}

func (t *Trader) liveSell(sig strategy.Signal, shares int, reason string) map[string]any {
        var prev any
        if p, err := t.Client.Preview(sig.StockID, "sell", shares); err == nil {
                prev = p
        }
        raw, err := t.Client.SellMarket(sig.StockID, shares)
        trade := map[string]any{
                "mode": "live", "stock_id": sig.StockID, "code": sig.Code, "side": "sell",
                "shares": shares, "price": sig.Price, "status": "submitted", "reason": reason,
                "raw": map[string]any{"preview": prev, "result": raw},
        }
        if err != nil {
                trade["status"] = "error"
                trade["reason"] = err.Error()
                trade["raw"] = raw
        } else {
                if v := asF(raw["avg_price"]); v > 0 {
                        trade["price"] = v
                }
                if v := asF(raw["filled_shares"]); v > 0 {
                        trade["shares"] = v
                }
        }
        _ = t.Storage.LogTrade(trade)
        return trade
}

func openedAt(p map[string]any) float64 {
        if p == nil {
                return float64(time.Now().Unix())
        }
        for _, k := range []string{"opened_at", "open_ts", "created_at", "buy_ts", "entry_ts"} {
                if v := asF(p[k]); v > 0 {
                        if v > 1e12 {
                                return v / 1000
                        }
                        return v
                }
        }
        return float64(time.Now().Unix())
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

func asBool(v any) bool {
        switch t := v.(type) {
        case bool:
                return t
        case float64:
                return t != 0
        default:
                return false
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

func max3(a, b, c float64) float64 {
        if b > a {
                a = b
        }
        if c > a {
                a = c
        }
        return a
}
