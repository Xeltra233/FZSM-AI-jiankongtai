package main

import (
  "encoding/json"
  "fmt"
  "os"
  "path/filepath"

  "fzsmbot/internal/client"
  "fzsmbot/internal/config"
  "fzsmbot/internal/modules"
  "fzsmbot/internal/storage"
)

func main() {
  cfgPath := "config/config.yaml"
  if _, err := os.Stat(cfgPath); err != nil {
    cfgPath = filepath.Join("..", "config", "config.yaml")
  }
  cfg, err := config.Load(cfgPath)
  if err != nil { panic(err) }
  // normalize relative storage/cookie paths when launched from go/
  if !filepath.IsAbs(cfg.Storage.DBPath) && filepath.Base(filepath.Dir(cfgPath)) != "." {
    // if config loaded from ../config/config.yaml keep paths relative to project root
    root := filepath.Dir(cfgPath)
    cfg.Storage.DBPath = filepath.Join(root, cfg.Storage.DBPath)
    cfg.CookieFile = filepath.Join(root, cfg.CookieFile)
  }
  fmt.Println("cfg", cfgPath, "db", cfg.Storage.DBPath, "farm_enabled", cfg.Farm["enabled"], "crop", cfg.Farm["crop_key"]) 
  st, err := storage.Open(cfg.Storage.DBPath)
  if err != nil { panic(err) }
  defer st.Close()
  c, err := client.New(cfg.APIBase, "https://api.fanzisima.xyz", cfg.CookieFile)
  if err != nil { panic(err) }
  farm := modules.RunFarm(cfg, st, c)
  b, _ := json.Marshal(map[string]any{
    "impl": farm["impl"], "mode": farm["mode"], "status": farm["status"], "crop": farm["crop_key"],
    "plots": farm["plots"], "planted": len(asSlice(farm["planted"])), "harvested": len(asSlice(farm["harvested"])),
    "stolen": len(asSlice(farm["stolen"])), "errors": farm["errors"], "day_ev": farm["day_ev_12"],
    "reason": farm["crop_reason"], "steal_left": farm["steal_left"],
  })
  fmt.Println(string(b))
  lot := modules.RunLottery(cfg, st, c, map[string]any{})
  b2,_ := json.Marshal(map[string]any{"status": lot["status"], "drawn": lot["drawn"], "actions": lot["actions"], "free": lot["free_draws"], "checked": lot["checked_today"], "impl": lot["impl"]})
  fmt.Println(string(b2))
}

func asSlice(v any) []any {
  if s, ok := v.([]any); ok { return s }
  return nil
}