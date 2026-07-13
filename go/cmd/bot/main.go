package main

import (
	"flag"
	"log"
	"time"

	"fzsmbot/internal/client"
	"fzsmbot/internal/config"
	"fzsmbot/internal/flags"
	"fzsmbot/internal/keepalive"
	"fzsmbot/internal/logutil"
	"fzsmbot/internal/loop"
	"fzsmbot/internal/storage"
)

// Go bot:
// - cookie keepalive
// - feature flags snapshot
// - spot trade skeleton (market/klines/strategy/risk/execute)
// - modules earn path (farm harvest/plant/steal + free lottery)
//
// Default sidecar mode writes service_go only.
// Pass -primary to own service/last_loop as main process.
func main() {
	cfgPath := flag.String("c", "config/config.yaml", "config path")
	once := flag.Bool("once", false, "run one cycle then exit")
	every := flag.Int("every", 18, "loop seconds")
	primary := flag.Bool("primary", false, "write service as primary")
	mode := flag.String("mode", "", "override mode: paper|live")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	logCloser, err := logutil.Setup(logutil.Options{
		Dir:        cfg.Storage.LogDir,
		Name:       "bot.log",
		AlsoStdout: true,
	})
	if err != nil {
		log.Fatalf("log setup: %v", err)
	}
	defer logCloser.Close()

	if *mode == "paper" || *mode == "live" {
		cfg.Mode = *mode
	}
	st, err := storage.Open(cfg.Storage.DBPath)
	if err != nil {
		log.Fatalf("storage: %v", err)
	}
	defer st.Close()
	cli, err := client.New(cfg.APIBase, "https://api.fanzisima.xyz", cfg.CookieFile)
	if err != nil {
		log.Fatalf("client: %v", err)
	}

	cycle := 0
	beat := func(force bool) {
		cycle++
		fl := flags.Get(cfg, st)
		ka := keepalive.Run(cfg, st, cli, force, cycle)
		out := loop.Once(cfg, st, cli, cycle, *primary)
		n := 0
		if items, ok := fl["items"].([]map[string]any); ok {
			n = len(items)
		} else if arr, ok := fl["items"].([]any); ok {
			n = len(arr)
		}
		acc := map[string]any{}
		if m, ok := out["account"].(map[string]any); ok {
			acc = m
		}
		mods := map[string]any{}
		if m, ok := out["modules"].(map[string]any); ok {
			mods = m
		}
		log.Printf(
			"cycle=%d primary=%v flags=%d ka=%v candidates=%v trades=%v buy=%v sell=%v modules_total=%v modules_error=%v user=%v farm=%v equity=%v",
			cycle, *primary, n, ka["ok"], out["candidates"], out["trade_count"], out["buy_count"], out["sell_count"],
			mods["total"], mods["error"], out["user_name"], out["farm_crop"], acc["equity"],
		)
	}
	beat(true)
	if *once {
		return
	}
	t := time.NewTicker(time.Duration(*every) * time.Second)
	defer t.Stop()
	for range t.C {
		beat(false)
	}
}
