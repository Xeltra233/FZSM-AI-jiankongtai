package main

import (
        "encoding/json"
        "flag"
        "fmt"
        "os"
        "strings"
        "time"

        "fzsmbot/internal/client"
        "fzsmbot/internal/config"
        "fzsmbot/internal/storage"
)

type check struct {
        Name   string `json:"name"`
        OK     bool   `json:"ok"`
        Detail string `json:"detail"`
}

func add(checks *[]check, name string, ok bool, detail string) {
        mark := "OK  "
        if !ok {
                mark = "FAIL"
        }
        fmt.Printf("%s %s: %s\n", mark, name, detail)
        *checks = append(*checks, check{Name: name, OK: ok, Detail: detail})
}

func main() {
        cfgPath := flag.String("c", "config/config.yaml", "config path")
        jsonOut := flag.Bool("json", false, "print json summary")
        mapOnly := flag.Bool("map", false, "print feature map only")
        flag.Parse()

        if *mapOnly {
                printMap()
                return
        }

        checks := []check{}
        cfg, err := config.Load(*cfgPath)
        add(&checks, "config/config.yaml", err == nil, *cfgPath)
        if err != nil {
                failSummary(checks, *jsonOut)
                os.Exit(1)
        }
        cookie := cfg.CookieFile
        add(&checks, "cookie_file", fileExists(cookie), cookie)

        names := []string{}
        if b, err := os.ReadFile(cookie); err == nil {
                var raw any
                if json.Unmarshal(b, &raw) == nil {
                        switch t := raw.(type) {
                        case []any:
                                for _, it := range t {
                                        if m, ok := it.(map[string]any); ok {
                                                names = append(names, fmt.Sprint(m["name"]))
                                        }
                                }
                        case map[string]any:
                                if arr, ok := t["cookies"].([]any); ok {
                                        for _, it := range arr {
                                                if m, ok := it.(map[string]any); ok {
                                                        names = append(names, fmt.Sprint(m["name"]))
                                                }
                                        }
                                }
                        }
                }
                add(&checks, "cookies.loaded", len(names) > 0, fmt.Sprintf("names=%v", names))
                has := false
                for _, n := range names {
                        if n == "fz_lottery" {
                                has = true
                                break
                        }
                }
                add(&checks, "cookies.fz_lottery", has, map[bool]string{true: "present", false: "missing fz_lottery"}[has])
        } else {
                add(&checks, "cookies.loaded", false, err.Error())
        }

        st, err := storage.Open(cfg.Storage.DBPath)
        add(&checks, "storage.open", err == nil, cfg.Storage.DBPath)
        if err != nil {
                failSummary(checks, *jsonOut)
                os.Exit(1)
        }
        defer st.Close()

        cli, err := client.New(cfg.APIBase, "https://api.fanzisima.xyz", cfg.CookieFile)
        add(&checks, "client.new", err == nil, cfg.APIBase)
        if err != nil {
                failSummary(checks, *jsonOut)
                os.Exit(1)
        }
        probe := cli.AuthProbe()
        authOK := asBool(probe["ok"])
        me, _ := probe["me"].(map[string]any)
        bal := any(nil)
        if me != nil {
                bal = me["balance_lobster"]
        }
        add(&checks, "auth.probe /me", authOK, fmt.Sprintf("ok=%v balance=%v", authOK, bal))

        ka := st.GetStateMap("auth_keepalive")
        if len(ka) == 0 {
                add(&checks, "auth.keepalive", true, "no runtime record yet")
        } else {
                ok := ka["ok"] != false
                add(&checks, "auth.keepalive", ok, fmt.Sprintf("ok=%v impl=%v msg=%v", ka["ok"], ka["impl"], trim(fmt.Sprint(ka["message"]), 80)))
        }

        if authOK {
                type p struct {
                        name string
                        fn   func() (any, error)
                }
                probes := []p{
                        {"market", func() (any, error) { return cli.Market() }},
                        {"portfolio", func() (any, error) { return cli.Portfolio() }},
                        {"farm.me", func() (any, error) { return cli.FarmMe() }},
                        {"lottery.me", func() (any, error) { return cli.LotteryMe() }},
                }
                for _, pr := range probes {
                        data, err := pr.fn()
                        if err != nil {
                                add(&checks, "api."+pr.name, false, trim(err.Error(), 160))
                                continue
                        }
                        n := sizeOf(data)
                        add(&checks, "api."+pr.name, true, fmt.Sprintf("type=%T size=%v", data, n))
                }
        }

        // service / modules
        svc := st.GetStateMap("service")
        sg := st.GetStateMap("service_go")
        add(&checks, "service.python_or_go", len(svc) > 0 || len(sg) > 0, fmt.Sprintf("service.impl=%v service_go.impl=%v", svc["impl"], sg["impl"]))
        add(&checks, "service_go.present", len(sg) > 0, fmt.Sprintf("cycle=%v user=%v", sg["cycle"], sg["user_name"]))

        mods := st.GetStateMap("modules")
        order, _ := mods["order"].([]any)
        counts := mods["counts"]
        add(&checks, "modules.bundle", len(order) > 0, fmt.Sprintf("order=%d counts=%v impl=%v", len(order), counts, mods["impl"]))
        need := []string{"spot", "farm", "lottery", "side_hustle", "brokers", "derivatives", "calendar", "leaderboard", "honors", "meeting", "governance", "admin"}
        have := map[string]bool{}
        for _, x := range order {
                have[fmt.Sprint(x)] = true
        }
        missing := []string{}
        for _, n := range need {
                if !have[n] {
                        missing = append(missing, n)
                }
        }
        add(&checks, "modules.coverage", len(missing) == 0, map[bool]string{true: "12 core modules present", false: "missing=" + strings.Join(missing, ",")}[len(missing) == 0])

        farm := st.GetStateMap("farm")
        add(&checks, "farm.state", len(farm) > 0, fmt.Sprintf("impl=%v crop=%v plots=%v", farm["impl"], farm["crop_key"], farm["plots"]))
        ll := st.GetStateMap("last_loop")
        add(&checks, "last_loop", len(ll) > 0, fmt.Sprintf("impl=%v buy=%v sell=%v trade=%v", ll["impl"], ll["buy_count"], ll["sell_count"], ll["trade_count"]))
        reg := st.GetStateMap("regime")
        add(&checks, "regime", len(reg) > 0, fmt.Sprintf("name=%v force_sell_only=%v scale=%v", reg["name"], reg["force_sell_only"], reg["position_scale"]))

        for _, rel := range []string{"docs/FEATURE_MATRIX.md", "docs/STARTUP.md", "docs/PROFIT_MODELS.md", "README.md", "web/dashboard.html"} {
                add(&checks, "doc."+rel, fileExists(rel) && fileSize(rel) > 100, rel)
        }

        fmt.Println("\nFeature coverage:")
        printMap()

        failed := 0
        for _, c := range checks {
                if !c.OK {
                        failed++
                }
        }
        fmt.Printf("\nDoctor summary: %d/%d passed\n", len(checks)-failed, len(checks))
        if failed > 0 {
                fmt.Println("Failed:")
                for _, c := range checks {
                        if !c.OK {
                                fmt.Printf(" - %s => %s\n", c.Name, c.Detail)
                        }
                }
                fmt.Println("\nRepair hints:")
                fmt.Println(" - Cookie 登录：在 Dashboard 控制页导入并探测 fz_lottery")
                fmt.Println(" - Go bot: bin\\fzsm-bot.exe -c config/config.yaml --once")
                fmt.Println(" - Go dashboard: bin\\fzsm-dashboard.exe -c config/config.yaml -port 8787")
                if *jsonOut {
                        b, _ := json.MarshalIndent(map[string]any{"ok": false, "failed": failed, "checks": checks, "ts": time.Now().Unix()}, "", "  ")
                        fmt.Println(string(b))
                }
                os.Exit(1)
        }
        fmt.Println("All critical checks passed.")
        if *jsonOut {
                b, _ := json.MarshalIndent(map[string]any{"ok": true, "failed": 0, "checks": checks, "ts": time.Now().Unix()}, "", "  ")
                fmt.Println(string(b))
        }
}

func printMap() {
        rows := [][]string{
                {"active", "/stocks", "spot,trader,strategy", "行情交易"},
                {"active", "/farm", "farm", "农场"},
                {"active", "/lottery/page", "lottery", "签到/抽奖/VIP"},
                {"active", "/invest|/bet|/funds", "side_hustle", "IPO/对赌/基金"},
                {"active", "/broker", "brokers", "券商"},
                {"active", "/futures|/margin", "derivatives", "期货/保证金"},
                {"active", "events/news", "calendar", "日历/事件偏置"},
                {"active", "/leaderboard", "leaderboard", "排行榜"},
                {"active", "/honors", "honors", "荣誉"},
                {"active", "/meeting", "meeting", "股东大会"},
                {"active", "/governance", "governance", "公司治理"},
                {"probe_only", "/admin", "admin", "管理探测"},
                {"active", "dashboard funds", "funds_breakdown,llm_usage", "资金统计"},
                {"active", "auth keepalive", "keepalive", "Cookie 保活"},
                {"active", "feature flags", "flags", "功能开关"},
        }
        fmt.Printf("%-12s %-28s %-28s %s\n", "status", "route", "local", "title")
        fmt.Println(strings.Repeat("-", 100))
        for _, r := range rows {
                fmt.Printf("%-12s %-28s %-28s %s\n", r[0], r[1], r[2], r[3])
        }
}

func fileExists(p string) bool {
        _, err := os.Stat(p)
        return err == nil
}
func fileSize(p string) int64 {
        st, err := os.Stat(p)
        if err != nil {
                return 0
        }
        return st.Size()
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
func sizeOf(v any) any {
        switch t := v.(type) {
        case map[string]any:
                return len(t)
        case []any:
                return len(t)
        default:
                return fmt.Sprintf("%T", v)
        }
}
func trim(s string, n int) string {
        if len(s) <= n {
                return s
        }
        return s[:n]
}
func failSummary(checks []check, jsonOut bool) {
        failed := 0
        for _, c := range checks {
                if !c.OK {
                        failed++
                }
        }
        fmt.Printf("\nDoctor summary: %d/%d passed\n", len(checks)-failed, len(checks))
        if jsonOut {
                b, _ := json.MarshalIndent(map[string]any{"ok": false, "failed": failed, "checks": checks}, "", "  ")
                fmt.Println(string(b))
        }
}
