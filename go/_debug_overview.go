package main
import (
  "encoding/json"
  "fmt"
  "fzsmbot/internal/config"
  "fzsmbot/internal/dashboard"
  "fzsmbot/internal/storage"
)
func main(){
  cfg,_ := config.Load("config.yaml")
  st,_ := storage.Open(cfg.Storage.DBPath)
  defer st.Close()
  s,_ := dashboard.New(cfg, st, "web/dashboard.html")
  ov := s.Overview()
  b, err := json.Marshal(ov)
  fmt.Println("marshal err", err)
  fmt.Println("len", len(b))
  if err==nil { fmt.Println(string(b)[:200]) }
}
