package modules

import (
        "fmt"
        "strings"
        "time"

        "fzsmbot/internal/client"
        "fzsmbot/internal/config"
        "fzsmbot/internal/flags"
        "fzsmbot/internal/storage"
)

func now() float64 { return float64(time.Now().UnixNano()) / 1e9 }

func asMap(v any) map[string]any {
        if m, ok := v.(map[string]any); ok && m != nil {
                return m
        }
        return map[string]any{}
}

func asSlice(v any) []any {
        if s, ok := v.([]any); ok {
                return s
        }
        return []any{}
}

func asFloat(v any) float64 {
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
                _, _ = fmt.Sscanf(t, "%f", &f)
                return f
        default:
                return 0
        }
}

func asBool(v any, def bool) bool {
        switch t := v.(type) {
        case bool:
                return t
        case string:
                switch strings.ToLower(strings.TrimSpace(t)) {
                case "1", "true", "yes", "on":
                        return true
                case "0", "false", "no", "off":
                        return false
                }
        case float64:
                return t != 0
        case int:
                return t != 0
        }
        return def
}

func result(moduleID, title, status string, actions []map[string]any, analysis map[string]any, errors []string, ev any, extra map[string]any) map[string]any {
        if actions == nil {
                actions = []map[string]any{}
        }
        if analysis == nil {
                analysis = map[string]any{}
        }
        if errors == nil {
                errors = []string{}
        }
        out := map[string]any{
                "module_id": moduleID,
                "title":     title,
                "enabled":   true,
                "status":    status,
                "skipped":   status == "skip" || status == "disabled",
                "reason":    nil,
                "ts":        now(),
                "actions":   actions,
                "analysis":  analysis,
                "errors":    errors,
                "ev":        ev,
                "impl":      "go",
        }
        for k, v := range extra {
                out[k] = v
        }
        return out
}

func plotState(p map[string]any) string {
        status := strings.ToLower(fmt.Sprint(p["status"]))
        state := strings.ToLower(fmt.Sprint(p["state"]))
        crop := strings.TrimSpace(fmt.Sprint(p["crop_key"]))
        if crop == "<nil>" {
                crop = ""
        }
        if status == "empty" || (crop == "" && (state == "" || state == "empty" || state == "<nil>")) {
                return "empty"
        }
        if status == "ready" || status == "mature" || status == "harvestable" || state == "ready" || state == "mature" || state == "harvestable" {
                return "ready"
        }
        remain := asFloat(p["remain_sec"])
        if p["remain_sec"] == nil {
                readyAt := asFloat(p["ready_at"])
                if readyAt > 0 {
                        remain = readyAt - now()
                        if remain < 0 {
                                remain = 0
                        }
                }
        }
        if crop == "" {
                return "empty"
        }
        if remain <= 0 {
                return "ready"
        }
        return "growing"
}

func lotteryCfg(cfg *config.Config) map[string]any {
        if cfg != nil && cfg.Lottery != nil {
                return cfg.Lottery
        }
        return map[string]any{}
}

func flagOn(values map[string]any, id string, def bool) bool {
        if values == nil {
                return def
        }
        if v, ok := values[id]; ok {
                return asBool(v, def)
        }
        return def
}

func runFarmInfo(farm map[string]any) map[string]any {
        fs := asMap(farm)
        status := "idle"
        if len(fs) > 0 {
                if errs := asSlice(fs["errors"]); len(errs) > 0 {
                        status = "error"
                } else {
                        status = "ok"
                }
        }
        actions := []map[string]any{{
                "status":    "ok",
                "planted":   len(asSlice(fs["planted"])),
                "harvested": len(asSlice(fs["harvested"])),
                "stolen":    len(asSlice(fs["stolen"])),
                "crop":      fs["crop_key"],
        }}
        analysis := map[string]any{
                "crop_reason": fs["crop_reason"],
                "plots":       fs["plots"],
                "day_ev_12":   fs["day_ev_12"],
        }
        var errors []string
        for _, e := range asSlice(fs["errors"]) {
                errors = append(errors, fmt.Sprint(e))
        }
        return result("farm", "farm", status, actions, analysis, errors, fs["day_ev_12"], map[string]any{
                "raw_status": map[string]any{"enabled": fs["enabled"], "skipped": fs["skipped"], "crop_key": fs["crop_key"]},
        })
}

func runSpot(account map[string]any) map[string]any {
        pos := asSlice(account["positions"])
        return result("spot", "spot", "ok", []map[string]any{{
                "status": "ok", "positions": len(pos), "cash": account["cash"], "equity": account["equity"],
        }}, map[string]any{
                "cash": account["cash"], "equity": account["equity"], "stock_value": account["stock_value"], "position_count": len(pos),
        }, nil, nil, nil)
}

func runSide(st *storage.Storage, c *client.Client) map[string]any {
        ss := st.GetStateMap("side_hustle")
        analysis := asMap(ss["analysis"])
        errors := []string{}
        ipoN := len(asSlice(ss["ipo"]))
        betN := len(asSlice(ss["bets"]))
        fundN := len(asSlice(ss["funds"]))
        // live refresh list sizes from verified endpoints
        if c != nil {
                if arr, code, err := c.StocksList("/invest/ipos"); err != nil {
                        errors = append(errors, "invest/ipos: "+err.Error())
                } else if code >= 400 {
                        errors = append(errors, fmt.Sprintf("invest/ipos status=%d", code))
                } else {
                        ipoN = len(arr)
                        analysis["ipo_live_count"] = ipoN
                        if len(arr) > 0 {
                                top := []map[string]any{}
                                for i, it := range arr {
                                        if i >= 3 {
                                                break
                                        }
                                        m := asMap(it)
                                        top = append(top, map[string]any{
                                                "name": firstNonEmptyStr(fmt.Sprint(m["name"]), fmt.Sprint(m["code"])),
                                                "code": m["code"],
                                                "progress": m["progress"],
                                        })
                                }
                                analysis["ipo_live_top"] = top
                        }
                }
                if m, code, err := c.StocksMap("/bet/list"); err != nil {
                        errors = append(errors, "bet/list: "+err.Error())
                } else if code >= 400 {
                        errors = append(errors, fmt.Sprintf("bet/list status=%d", code))
                } else {
                        // bet list may be object with open/items
                        open := asSlice(firstNonNil(m["open"], m["list"], m["items"], m["bets"]))
                        if len(open) == 0 {
                                // sometimes data itself is list via StocksList better; try size fields
                                if n := int(asFloat(m["count"])); n > 0 {
                                        betN = n
                                }
                        } else {
                                betN = len(open)
                        }
                        analysis["bets_live_count"] = betN
                }
                if arr, code, err := c.StocksList("/funds/list"); err != nil {
                        errors = append(errors, "funds/list: "+err.Error())
                } else if code >= 400 {
                        errors = append(errors, fmt.Sprintf("funds/list status=%d", code))
                } else {
                        fundN = len(arr)
                        analysis["funds_live_count"] = fundN
                }
        }
        if len(ss) == 0 && ipoN+betN+fundN == 0 {
                return result("side_hustle", "IPO/对赌/基金", "idle", nil, map[string]any{"note": "waiting side runner"}, errors, nil, nil)
        }
        status := "ok"
        if len(errors) > 0 && ipoN+betN+fundN == 0 {
                status = "error"
        }
        return result("side_hustle", "IPO/对赌/基金", status, []map[string]any{{
                "status": status,
                "action": "side_lists_loaded",
                "ipo":    ipoN,
                "bets":   betN,
                "funds":  fundN,
        }}, analysis, errors, nil, map[string]any{"source": "side_cache+live_lists"})
}

func asSliceMaps(v any) []map[string]any {
        out := []map[string]any{}
        for _, item := range asSlice(v) {
                if m := asMap(item); len(m) > 0 {
                        out = append(out, m)
                }
        }
        return out
}

func runDerivatives(st *storage.Storage, values map[string]any) map[string]any {
        ds := st.GetStateMap("derivatives")
        tradeEnabled := flagOn(values, "derivatives.trade_enabled", false)
        if len(ds) == 0 {
                status := "analyze_only"
                if tradeEnabled {
                        status = "idle"
                }
                return result("derivatives", "derivatives", status, []map[string]any{{
                        "status": status, "action": "plan", "reason": "derivatives_trade_disabled_analyze_only",
                }}, map[string]any{"trade_enabled": tradeEnabled}, nil, nil, nil)
        }
        status := "ok"
        if !tradeEnabled {
                status = "analyze_only"
        }
        return result("derivatives", "derivatives", status, asSliceMaps(ds["actions"]), asMap(ds["analysis"]), nil, nil, map[string]any{
                "trade_enabled": tradeEnabled, "open_margin": ds["open_margin"], "cash": ds["cash"],
        })
}

func runBrokers(st *storage.Storage, c *client.Client) map[string]any {
        errors := []string{}
        me := map[string]any{}
        list := []any{}
        candidates := []any{}
        underwriters := []any{}
        if c != nil {
                if m, code, err := c.StocksMap("/broker/me"); err != nil {
                        errors = append(errors, "broker/me: "+err.Error())
                } else if code >= 400 {
                        errors = append(errors, fmt.Sprintf("broker/me status=%d", code))
                } else {
                        me = m
                        delete(me, "_http_status")
                }
                if arr, code, err := c.StocksList("/broker/list"); err != nil {
                        errors = append(errors, "broker/list: "+err.Error())
                } else if code >= 400 {
                        errors = append(errors, fmt.Sprintf("broker/list status=%d", code))
                } else {
                        list = arr
                }
                if arr, code, err := c.StocksList("/broker/candidates"); err != nil {
                        errors = append(errors, "broker/candidates: "+err.Error())
                } else if code >= 400 {
                        errors = append(errors, fmt.Sprintf("broker/candidates status=%d", code))
                } else {
                        candidates = arr
                }
                if arr, code, err := c.StocksList("/broker/underwriter/list"); err != nil {
                        // keep soft: endpoint may be empty list only
                        errors = append(errors, "broker/underwriter/list: "+err.Error())
                } else if code >= 400 {
                        errors = append(errors, fmt.Sprintf("broker/underwriter/list status=%d", code))
                } else {
                        underwriters = arr
                }
        }
        top := []map[string]any{}
        for i, it := range list {
                if i >= 5 {
                        break
                }
                m := asMap(it)
                top = append(top, map[string]any{
                        "id":         m["id"],
                        "name":       firstNonEmptyStr(fmt.Sprint(m["name"]), fmt.Sprint(m["owner_name"])),
                        "owner_name": m["owner_name"],
                        "clients":    firstNonZeroF(asFloat(m["client_count_cache"]), asFloat(m["clients"])),
                })
        }
        signed := me["signed_broker"]
        myCa := me["my_candidate"]
        actions := []map[string]any{
                {"status": "ok", "action": "broker_me_loaded", "signed": signed != nil && fmt.Sprint(signed) != "<nil>" && fmt.Sprint(signed) != "map[]"},
                {"status": "ok", "action": "broker_list_loaded", "count": len(list)},
                {"status": "ok", "action": "candidates_loaded", "count": len(candidates)},
        }
        if len(underwriters) == 0 {
                actions = append(actions, map[string]any{"status": "idle", "action": "underwriter_list", "reason": "empty"})
        } else {
                actions = append(actions, map[string]any{"status": "ok", "action": "underwriter_list", "count": len(underwriters)})
        }
        status := "ok"
        if len(list) == 0 && len(errors) > 0 && len(me) == 0 {
                status = "error"
        }
        analysis := map[string]any{
                "me":                me,
                "brokers_count":     len(list),
                "candidates_count":  len(candidates),
                "underwriter_count": len(underwriters),
                "top_brokers":       top,
                "signed_broker":     signed,
                "my_candidate":      myCa,
                "source":            "broker/me+list+candidates+underwriter/list",
        }
        return result("brokers", "券商/选举", status, actions, analysis, errors, map[string]any{
                "brokers_count":    len(list),
                "candidates_count": len(candidates),
        }, nil)
}

func runCalendar(st *storage.Storage, c *client.Client) map[string]any {
        errors := []string{}
        eventsActive := []any{}
        eventsRecent := []any{}
        news := []any{}
        if c != nil {
                if m, code, err := c.StocksMap("/events"); err != nil {
                        errors = append(errors, "events: "+err.Error())
                } else if code >= 400 {
                        errors = append(errors, fmt.Sprintf("events status=%d", code))
                } else {
                        eventsActive = asSlice(m["active"])
                        eventsRecent = asSlice(m["recent"])
                }
                if arr, code, err := c.StocksList("/news"); err != nil {
                        errors = append(errors, "news: "+err.Error())
                } else if code >= 400 {
                        errors = append(errors, fmt.Sprintf("news status=%d", code))
                } else {
                        news = arr
                }
                // dedicated calendar endpoint is known missing (404); do not hard-fail.
        }

        eventMoods := []float64{}
        eventTitles := []string{}
        for i, it := range eventsRecent {
                m := asMap(it)
                if v := asFloat(m["market_mood"]); m["market_mood"] != nil {
                        eventMoods = append(eventMoods, v)
                }
                title := firstNonEmptyStr(fmt.Sprint(m["title"]), fmt.Sprint(m["body"]))
                if title != "" && i < 5 {
                        eventTitles = append(eventTitles, title)
                }
        }
        newsSents := []float64{}
        newsTitles := []string{}
        for i, it := range news {
                m := asMap(it)
                if m["sentiment"] != nil {
                        newsSents = append(newsSents, asFloat(m["sentiment"]))
                }
                title := firstNonEmptyStr(fmt.Sprint(m["title"]), fmt.Sprint(m["body"]))
                if title != "" && i < 5 {
                        newsTitles = append(newsTitles, title)
                }
        }
        avgEvent := avgFloats(eventMoods)
        avgNews := avgFloats(newsSents)
        // combine: events mood [-n,n] + news sentiment
        combined := avgEvent
        if len(newsSents) > 0 && len(eventMoods) > 0 {
                combined = (avgEvent + avgNews) / 2
        } else if len(newsSents) > 0 {
                combined = avgNews
        }
        enter := 0.0
        riskOff := 0.0
        notes := []string{"calendar_use_events_news"}
        if combined >= 3 {
                enter = 0.03
                notes = append(notes, "mood_bullish")
        } else if combined <= -3 {
                riskOff = 0.03
                enter = -0.02
                notes = append(notes, "mood_bearish")
        } else if combined <= -1 {
                riskOff = 0.01
                notes = append(notes, "mood_soft_risk_off")
        } else {
                notes = append(notes, "mood_neutral")
        }
        bias := map[string]any{
                "enter_boost":    enter,
                "risk_off_boost": riskOff,
                "notes":          notes,
                "avg_market_mood": combined,
                "events_count":   len(eventsRecent) + len(eventsActive),
                "news_count":     len(news),
        }
        _ = st.SetState("bias.calendar", bias)

        actions := []map[string]any{
                {"status": "analyze_only", "action": "events_loaded", "count": len(eventsRecent) + len(eventsActive)},
                {"status": "analyze_only", "action": "news_loaded", "count": len(news)},
                {"status": "analyze_only", "action": "bias_analyze", "reason": "calendar_use_events_news", "avg_mood": combined},
        }
        status := "analyze_only"
        if len(eventsRecent)+len(eventsActive)+len(news) == 0 && len(errors) > 0 {
                status = "error"
        }
        analysis := map[string]any{
                "source":             "events+news",
                "mode":               "analyze_only",
                "bias":               bias,
                "avg_market_mood":    combined,
                "avg_event_mood":     avgEvent,
                "avg_news_sentiment": avgNews,
                "events_active":      len(eventsActive),
                "events_recent":      len(eventsRecent),
                "news_count":         len(news),
                "event_titles":       eventTitles,
                "news_titles":        newsTitles,
                "dedicated_calendar": false,
        }
        return result("calendar", "日历/市场情绪", status, actions, analysis, errors, bias, nil)
}

func avgFloats(xs []float64) float64 {
        if len(xs) == 0 {
                return 0
        }
        s := 0.0
        for _, v := range xs {
                s += v
        }
        return s / float64(len(xs))
}

func runLeaderboard(st *storage.Storage, c *client.Client, account map[string]any, userName string) map[string]any {
        errors := []string{}
        assetRows := []any{}
        farmRows := []any{}
        lotToday := map[string]any{}
        lotWeek := map[string]any{}
        lotAll := map[string]any{}
        vipLB := map[string]any{}
        if c != nil {
                if arr, code, err := c.StocksList("/leaderboard"); err != nil {
                        errors = append(errors, "asset_leaderboard: "+err.Error())
                } else if code >= 400 {
                        errors = append(errors, fmt.Sprintf("asset_leaderboard status=%d", code))
                } else {
                        assetRows = arr
                }
                if arr, code, err := c.StocksList("/farm/rankings"); err != nil {
                        errors = append(errors, "farm_rankings: "+err.Error())
                } else if code >= 400 {
                        errors = append(errors, fmt.Sprintf("farm_rankings status=%d", code))
                } else {
                        farmRows = arr
                }
                if m, code, err := c.LotteryMap("/lottery/api/leaderboard?range=today"); err != nil {
                        errors = append(errors, "lottery_lb_today: "+err.Error())
                } else if code >= 400 {
                        errors = append(errors, fmt.Sprintf("lottery_lb_today status=%d", code))
                } else {
                        lotToday = m
                        delete(lotToday, "_http_status")
                }
                if m, code, err := c.LotteryMap("/lottery/api/leaderboard?range=week"); err != nil {
                        errors = append(errors, "lottery_lb_week: "+err.Error())
                } else if code >= 400 {
                        errors = append(errors, fmt.Sprintf("lottery_lb_week status=%d", code))
                } else {
                        lotWeek = m
                        delete(lotWeek, "_http_status")
                }
                if m, code, err := c.LotteryMap("/lottery/api/leaderboard?range=all"); err != nil {
                        errors = append(errors, "lottery_lb_all: "+err.Error())
                } else if code >= 400 {
                        errors = append(errors, fmt.Sprintf("lottery_lb_all status=%d", code))
                } else {
                        lotAll = m
                        delete(lotAll, "_http_status")
                }
                if m, code, err := c.LotteryMap("/lottery/api/vip/leaderboard"); err != nil {
                        errors = append(errors, "vip_leaderboard: "+err.Error())
                } else if code >= 400 {
                        errors = append(errors, fmt.Sprintf("vip_leaderboard status=%d", code))
                } else {
                        vipLB = m
                        delete(vipLB, "_http_status")
                }
        }

        myEquity := asFloat(account["equity"])
        if myEquity == 0 {
                myEquity = asFloat(account["cash"]) + asFloat(account["stock_value"])
        }
        myName := strings.TrimSpace(userName)
        myID := 0
        for _, it := range farmRows {
                m := asMap(it)
                n := firstNonEmptyStr(fmt.Sprint(m["name"]), fmt.Sprint(m["display_name"]))
                if myName == "" || myName == "<nil>" {
                        myName = n
                }
                if id := int(asFloat(m["user_id"])); id > 0 {
                        myID = id
                }
        }

        assetTop := []map[string]any{}
        assetTop1 := map[string]any{}
        assets := []float64{}
        assetRank := 0
        for i, it := range assetRows {
                m := asMap(it)
                asset := firstNonZeroF(asFloat(m["total_asset"]), asFloat(m["total_asset_lobster"]), asFloat(m["balance"]), asFloat(m["balance_lobster"]))
                assets = append(assets, asset)
                name := firstNonEmptyStr(fmt.Sprint(m["name"]), fmt.Sprint(m["display_name"]), fmt.Sprint(m["username"]))
                uid := int(asFloat(m["user_id"]))
                row := map[string]any{
                        "rank": i + 1, "name": name, "user_id": uid,
                        "total_asset": asset,
                        "balance": firstNonZeroF(asFloat(m["balance"]), asFloat(m["balance_lobster"])),
                        "market_value": asFloat(m["market_value"]),
                }
                if i == 0 {
                        assetTop1 = row
                }
                if i < 5 {
                        assetTop = append(assetTop, row)
                }
                if assetRank == 0 {
                        if (myID > 0 && uid == myID) || (myName != "" && myName != "<nil>" && strings.EqualFold(name, myName)) {
                                assetRank = i + 1
                                if myEquity == 0 {
                                        myEquity = asset
                                }
                        }
                }
        }
        median := 0.0
        if len(assets) > 0 {
                sorted := append([]float64{}, assets...)
                for i := 1; i < len(sorted); i++ {
                        j := i
                        for j > 0 && sorted[j-1] > sorted[j] {
                                sorted[j-1], sorted[j] = sorted[j], sorted[j-1]
                                j--
                        }
                }
                median = sorted[len(sorted)/2]
        }
        notes := []string{}
        aggression := 0.0
        assetRankLabel := "100+"
        var assetRankEst any = "100+"
        if assetRank > 0 {
                assetRankLabel = fmt.Sprintf("%d", assetRank)
                assetRankEst = assetRank
                if assetRank <= 10 {
                        aggression = 0.05
                        notes = append(notes, "top10_aggressive")
                } else if assetRank <= 30 {
                        aggression = 0.02
                        notes = append(notes, "top30_mild")
                } else {
                        notes = append(notes, "in_top100")
                }
        } else if median > 0 && myEquity > 0 && myEquity < median*0.01 {
                aggression = -0.03
                notes = append(notes, "far_below_leaderboard_median")
        } else if median > 0 && myEquity > 0 && myEquity < median {
                aggression = -0.02
                notes = append(notes, "below_leaderboard_median")
        } else {
                aggression = -0.01
                notes = append(notes, "outside_top100")
        }
        bias := map[string]any{
                "aggression": aggression, "notes": notes, "rank_est": assetRankEst,
                "my_equity": myEquity, "median_top": median, "top_count": len(assetRows),
        }
        _ = st.SetState("bias.leaderboard", bias)

        farmTop := []map[string]any{}
        farmMe := map[string]any{}
        for i, it := range farmRows {
                m := asMap(it)
                row := map[string]any{
                        "rank": i + 1,
                        "name": firstNonEmptyStr(fmt.Sprint(m["name"]), fmt.Sprint(m["display_name"])),
                        "user_id": int(asFloat(m["user_id"])),
                        "total_lobster": firstNonZeroF(asFloat(m["total_lobster"]), asFloat(m["harvested_lobster"])),
                        "harvested_lobster": asFloat(m["harvested_lobster"]),
                        "stolen_lobster": asFloat(m["stolen_lobster"]),
                }
                if i < 5 {
                        farmTop = append(farmTop, row)
                }
                if (myID > 0 && int(asFloat(m["user_id"])) == myID) || (myName != "" && strings.EqualFold(fmt.Sprint(row["name"]), myName)) {
                        farmMe = row
                }
        }
        if len(farmMe) == 0 && len(farmRows) > 0 {
                farmMe = farmTop[0]
        }

        mkLot := func(raw map[string]any, rangeName string) map[string]any {
                lucky := asSlice(raw["lucky"])
                unlucky := asSlice(raw["unlucky"])
                topLucky := []map[string]any{}
                topUnlucky := []map[string]any{}
                var lucky1 any
                var unlucky1 any
                for i, it := range lucky {
                        if i >= 5 {
                                break
                        }
                        m := asMap(it)
                        row := map[string]any{
                                "rank": firstNonZeroF(asFloat(m["rank"]), float64(i+1)),
                                "name": firstNonEmptyStr(fmt.Sprint(m["name"]), fmt.Sprint(m["user_id"])),
                                "user_id": m["user_id"],
                                "lottery_lobster": asFloat(m["lottery_lobster"]),
                                "is_me": asBool(m["is_me"], false),
                        }
                        topLucky = append(topLucky, row)
                        if i == 0 {
                                lucky1 = row
                        }
                }
                for i, it := range unlucky {
                        if i >= 5 {
                                break
                        }
                        m := asMap(it)
                        row := map[string]any{
                                "rank": firstNonZeroF(asFloat(m["rank"]), float64(i+1)),
                                "name": firstNonEmptyStr(fmt.Sprint(m["name"]), fmt.Sprint(m["user_id"])),
                                "user_id": m["user_id"],
                                "lottery_lobster": asFloat(m["lottery_lobster"]),
                                "is_me": asBool(m["is_me"], false),
                        }
                        topUnlucky = append(topUnlucky, row)
                        if i == 0 {
                                unlucky1 = row
                        }
                }
                return map[string]any{
                        "range": rangeName,
                        "lucky_count": len(lucky),
                        "unlucky_count": len(unlucky),
                        "lucky_top": topLucky,
                        "unlucky_top": topUnlucky,
                        "mine": asMap(raw["mine"]),
                        "lucky1": lucky1,
                        "unlucky1": unlucky1,
                }
        }
        lotTodayP := mkLot(lotToday, "today")
        lotWeekP := mkLot(lotWeek, "week")
        lotAllP := mkLot(lotAll, "all")

        vipItems := asSlice(vipLB["items"])
        vipTop := []map[string]any{}
        for i, it := range vipItems {
                if i >= 5 {
                        break
                }
                m := asMap(it)
                vipTop = append(vipTop, map[string]any{
                        "rank": firstNonZeroF(asFloat(m["rank"]), float64(i+1)),
                        "name": firstNonEmptyStr(fmt.Sprint(m["name"]), fmt.Sprint(m["user_id"])),
                        "score": firstNonZeroF(asFloat(m["score"]), asFloat(m["value"]), asFloat(m["lottery_lobster"])),
                })
        }

        boards := map[string]any{
                "asset": map[string]any{
                        "name": "资产榜", "count": len(assetRows), "rank_label": assetRankLabel, "rank_est": assetRankEst,
                        "my_equity": myEquity, "top1": assetTop1, "top": assetTop, "median_top": median,
                },
                "farm": map[string]any{
                        "name": "农场榜",
                        "count": len(farmRows),
                        "me": farmMe,
                        "top": farmTop,
                        "complete": len(farmRows) > 1,
                        "scope": func() string {
                                if len(farmRows) <= 1 {
                                        return "self_or_partial"
                                }
                                return "list"
                        }(),
                        "note": func() string {
                                if len(farmRows) <= 1 {
                                        return "远端 /farm/rankings 当前仅返回个人或少量记录，不是完整全服榜"
                                }
                                return "farm rankings list"
                        }(),
                },
                "lottery_today": map[string]any{"name": "抽奖榜·今日", "data": lotTodayP},
                "lottery_week":  map[string]any{"name": "抽奖榜·本周", "data": lotWeekP},
                "lottery_all":   map[string]any{"name": "抽奖榜·全部", "data": lotAllP},
                "vip": map[string]any{"name": "VIP榜", "period": vipLB["period"], "count": len(vipItems), "top": vipTop},
        }

        actions := []map[string]any{
                {"status": "ok", "action": "asset_leaderboard_loaded", "count": len(assetRows), "top_name": assetTop1["name"], "rank_label": assetRankLabel},
                {"status": "ok", "action": "farm_rankings_loaded", "count": len(farmRows), "name": firstNonEmptyStr(fmt.Sprint(farmMe["name"]), myName), "total_lobster": firstNonZeroF(asFloat(farmMe["total_lobster"]), asFloat(farmMe["harvested_lobster"]))},
                {"status": "ok", "action": "lottery_leaderboard_today_loaded", "lucky": lotTodayP["lucky_count"], "unlucky": lotTodayP["unlucky_count"]},
                {"status": "ok", "action": "lottery_leaderboard_week_loaded", "lucky": lotWeekP["lucky_count"], "unlucky": lotWeekP["unlucky_count"]},
                {"status": "ok", "action": "lottery_leaderboard_all_loaded", "lucky": lotAllP["lucky_count"], "unlucky": lotAllP["unlucky_count"]},
                {"status": "ok", "action": "vip_leaderboard_loaded", "count": len(vipItems)},
        }
        status := "ok"
        if len(assetRows) == 0 && len(errors) > 0 {
                status = "error"
        }
        analysis := map[string]any{
                "count": len(assetRows), "top1": assetTop1, "top": assetTop,
                "rank_est": assetRankEst, "rank_label": assetRankLabel,
                "my_equity": myEquity, "my_name": myName, "my_user_id": myID,
                "median_top": median, "farm_rank_count": len(farmRows), "farm_me": farmMe,
                "bias": bias, "source": "asset+farm+lottery(today/week/all)+vip",
                "boards": boards,
        }
        ev := map[string]any{"aggression": aggression, "rank_est": assetRankEst, "my_equity": myEquity, "boards": 6}
        return result("leaderboard", "排行榜", status, actions, analysis, errors, ev, nil)
}


func firstNonEmptyStr(xs ...string) string {
        for _, x := range xs {
                x = strings.TrimSpace(x)
                if x != "" && x != "<nil>" && x != "null" {
                        return x
                }
        }
        return ""
}

func firstNonZeroF(xs ...float64) float64 {
        for _, x := range xs {
                if x != 0 {
                        return x
                }
        }
        if len(xs) > 0 {
                return xs[0]
        }
        return 0
}

func runHonors(st *storage.Storage) map[string]any {
        prev := st.GetStateMap("modules.honors")
        if len(prev) > 0 {
                prev["impl"] = "go-reuse"
                prev["ts"] = now()
                return prev
        }
        return result("honors", "honors", "idle", nil, map[string]any{"note": "waiting data"}, nil, nil, nil)
}

func runMeeting(st *storage.Storage, c *client.Client, account map[string]any) map[string]any {
        errors := []string{}
        publicOK := false
        publicNote := "no_public_meeting_list_api"
        // Probe known public list paths; currently commonly 404/400.
        if c != nil {
                for _, path := range []string{"/meeting", "/meeting/list", "/meetings", "/meetings/list"} {
                        code, _, err := c.StocksGet(path)
                        if err != nil {
                                errors = append(errors, path+": "+err.Error())
                                continue
                        }
                        if code >= 200 && code < 300 {
                                publicOK = true
                                publicNote = "public_list_available"
                                break
                        }
                        errors = append(errors, fmt.Sprintf("%s status=%d", path, code))
                }
        }
        scan := st.GetStateMap("meeting.scan")
        positions := asSlice(account["positions"])
        holdings := len(positions)
        if holdings == 0 {
                // fallback to scan holdings_scanned
                holdings = int(asFloat(scan["holdings_scanned"]))
        }
        active := asSlice(scan["active_meetings"])
        missing := asSlice(scan["missing"])
        analysis := map[string]any{
                "public_list_available": publicOK,
                "public_list_note":      publicNote,
                "holdings_scanned":      holdings,
                "active_meetings":       active,
                "missing":               missing,
                "with_meeting_ts":       scan["with_meeting_ts"],
                "source":                "holdings_scan+public_probe",
        }
        actions := []map[string]any{}
        if publicOK {
                actions = append(actions, map[string]any{"status": "ok", "action": "public_list_loaded"})
        } else {
                actions = append(actions, map[string]any{"status": "analyze_only", "action": "public_list_unavailable", "reason": "no_public_meeting_list_api"})
        }
        if len(scan) > 0 {
                actions = append(actions, map[string]any{"status": "ok", "action": "scan_cached", "holdings": holdings, "active": len(active)})
        } else {
                actions = append(actions, map[string]any{"status": "idle", "action": "holdings_scan_pending", "reason": "waiting_position_meeting_scan"})
        }
        status := "ok"
        if !publicOK && len(scan) == 0 {
                status = "analyze_only"
        }
        return result("meeting", "股东大会", status, actions, analysis, errors, map[string]any{"active": len(active), "holdings": holdings}, nil)
}

func runGovernance(st *storage.Storage) map[string]any {
        prev := st.GetStateMap("modules.governance")
        if len(prev) > 0 {
                prev["impl"] = "go-reuse"
                prev["ts"] = now()
                return prev
        }
        return result("governance", "governance", "idle", nil, nil, nil, nil, nil)
}

func runAdmin(st *storage.Storage) map[string]any {
        prev := st.GetStateMap("modules.admin")
        if len(prev) > 0 {
                prev["impl"] = "go-reuse"
                prev["ts"] = now()
                return prev
        }
        return result("admin", "admin", "forbidden", []map[string]any{{"status": "forbidden", "action": "probe", "reason": "admin_probe_only"}}, map[string]any{"note": "probe_only"}, nil, nil, nil)
}

// BuildAccountFromLive pulls /me + /portfolio into last_loop.account shape.
func BuildAccountFromLive(c *client.Client) (map[string]any, string, error) {
        me, err := c.StocksMe()
        if err != nil {
                return nil, "", err
        }
        user := asMap(me["user"])
        name := strings.TrimSpace(fmt.Sprint(user["display_name"]))
        if name == "" || name == "<nil>" {
                name = strings.TrimSpace(fmt.Sprint(user["username"]))
        }
        cash := asFloat(me["balance_lobster"])
        equity := asFloat(me["total_asset_lobster"])
        positions := []any{}
        stockValue := 0.0
        if pf, err := c.Portfolio(); err == nil {
                positions = asSlice(pf["positions"])
                summary := asMap(pf["summary"])
                if v := asFloat(summary["stock_value"]); v != 0 {
                        stockValue = v
                } else if v := asFloat(summary["holdings_value"]); v != 0 {
                        stockValue = v
                } else if v := asFloat(summary["market_value"]); v != 0 {
                        stockValue = v
                }
                if stockValue == 0 {
                        for _, item := range positions {
                                p := asMap(item)
                                mv := asFloat(p["market_value"])
                                if mv == 0 {
                                        mv = asFloat(p["value"])
                                }
                                if mv == 0 {
                                        mv = asFloat(p["shares"]) * asFloat(p["price"])
                                }
                                stockValue += mv
                        }
                }
        }
        if stockValue == 0 && equity != 0 {
                stockValue = equity - cash
        }
        account := map[string]any{
                "mode":        "live",
                "cash":        cash,
                "equity":      equity,
                "stock_value": stockValue,
                "pnl":         me["pnl"],
                "pnl_pct":     me["pnl_pct"],
                "positions":   positions,
        }
        return account, name, nil
}

// RunAll executes a Go modules bus pass and persists modules / farm / last_loop / service fields.
func RunAll(cfg *config.Config, st *storage.Storage, c *client.Client, cycle int, primary bool) map[string]any {
        fl := flags.Get(cfg, st)
        values := asMap(fl["values"])

        farm := RunFarm(cfg, st, c)
        _ = st.SetState("farm", farm)

        account, userName, accErr := BuildAccountFromLive(c)
        if accErr != nil {
                prevLoop := st.GetStateMap("last_loop")
                account = asMap(prevLoop["account"])
                if userName == "" {
                        userName = fmt.Sprint(st.GetStateMap("service")["user_name"])
                }
        }

        order := []string{"spot", "farm", "lottery", "side_hustle", "brokers", "derivatives", "calendar", "leaderboard", "honors", "meeting", "governance", "admin"}
        results := map[string]any{}
        results["spot"] = runSpot(account)
        results["farm"] = runFarmInfo(farm)
        results["lottery"] = RunLottery(cfg, c, values)
        results["side_hustle"] = runSide(st, c)
        results["brokers"] = runBrokers(st, c)
        results["derivatives"] = runDerivatives(st, values)
        results["calendar"] = runCalendar(st, c)
        results["leaderboard"] = runLeaderboard(st, c, account, userName)
        results["honors"] = runHonors(st)
        results["meeting"] = runMeeting(st, c, account)
        results["governance"] = runGovernance(st)
        results["admin"] = runAdmin(st)

        errN, okN := 0, 0
        for _, mid := range order {
                m := asMap(results[mid])
                _ = st.SetState("modules."+mid, m)
                switch fmt.Sprint(m["status"]) {
                case "error":
                        errN++
                case "ok", "analyze_only", "forbidden", "unauthorized":
                        okN++
                }
        }
        bundle := map[string]any{
                "ts":      now(),
                "order":   order,
                "modules": results,
                "counts":  map[string]any{"total": len(order), "error": errN, "ok": okN},
                "impl":    "go",
        }
        _ = st.SetState("modules", bundle)

        prev := st.GetStateMap("last_loop")
        profile := "balanced"
        if cfg != nil && cfg.Strategy != nil {
                if p := strings.TrimSpace(fmt.Sprint(cfg.Strategy["profile"])); p != "" && p != "<nil>" {
                        profile = p
                }
        }
        if p := strings.TrimSpace(fmt.Sprint(prev["profile"])); p != "" && p != "<nil>" {
                profile = p
        }
        last := map[string]any{
                "ts":            now(),
                "index":         prev["index"],
                "account":       account,
                "buy_count":     prev["buy_count"],
                "sell_count":    prev["sell_count"],
                "trade_count":   prev["trade_count"],
                "top_signals":   prev["top_signals"],
                "recent_trades": prev["recent_trades"],
                "control":       st.GetStateMap("control"),
                "profile":       profile,
                "regime":        prev["regime"],
                "farm":          farm,
                "impl":          "go",
                "cycle":         cycle,
        }
        if last["control"] == nil || len(asMap(last["control"])) == 0 {
                last["control"] = map[string]any{"trade_mode": "auto"}
        }
        _ = st.SetState("last_loop", last)

        service := map[string]any{
                "status":        "running",
                "mode":          cfg.Mode,
                "profile":       profile,
                "cycle":         cycle,
                "last_cycle_at": now(),
                "impl":          "go",
                "primary":       primary,
                "user_name":     userName,
                "modules_total": len(order),
                "modules_error": errN,
                "farm_crop":     farm["crop_key"],
                "farm_plots":    farm["plots"],
                "lottery": map[string]any{
                        "free_draws": asMap(results["lottery"])["free_draws"],
                        "drawn":      asMap(results["lottery"])["drawn"],
                        "checked":    asMap(results["lottery"])["checked_today"],
                },
        }
        if accErr != nil {
                service["account_error"] = accErr.Error()
        }
        _ = st.SetState("service_go", service)
        if primary {
                _ = st.SetState("service", service)
        }

        return map[string]any{
                "ok":         true,
                "cycle":      cycle,
                "modules":    bundle["counts"],
                "user_name":  userName,
                "account":    account,
                "farm_plots": farm["plots"],
                "farm_crop":  farm["crop_key"],
                "impl":       "go",
        }
}