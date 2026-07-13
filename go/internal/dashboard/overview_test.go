package dashboard_test
import (
  "encoding/json"
  "testing"
  "fzsmbot/internal/config"
  "fzsmbot/internal/dashboard"
  "fzsmbot/internal/storage"
)
func TestOverviewMarshal(t *testing.T){
  root := `C:\project\test\fzsm炒股`
  cfg, err := config.Load(root + `\config.yaml`)
  if err != nil { t.Fatal(err) }
  st, err := storage.Open(root + `\` + cfg.Storage.DBPath)
  if err != nil { t.Fatal(err) }
  defer st.Close()
  s, err := dashboard.New(cfg, st, root + `\web\dashboard.html`)
  if err != nil { t.Fatal(err) }
  ov := s.Overview()
  b, err := json.Marshal(ov)
  if err != nil {
    // try field by field
    for k,v := range ov {
      if _, e := json.Marshal(v); e != nil {
        t.Logf("bad field %s: %v type=%T", k, e, v)
      }
    }
    t.Fatalf("marshal: %v", err)
  }
  t.Logf("len=%d", len(b))
}
