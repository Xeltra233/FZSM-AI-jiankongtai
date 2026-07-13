package main
import (
  "fmt"
  "fzsmbot/internal/storage"
)
func main(){
  st,err:=storage.Open("../data/bot.db"); if err!=nil {panic(err)}; defer st.Close()
  err=st.LogTrade(map[string]any{"mode":"live","stock_id":99,"code":"GOTEST","side":"buy","shares":1.0,"price":1.23,"status":"submitted","reason":"go-logtrade-test","raw":map[string]any{"ok":true}})
  fmt.Println("logtrade err", err)
  err=st.LogSignal(map[string]any{"stock_id":99,"code":"GOTEST","action":"hold","score":0.1,"confidence":0.1,"price":1.2,"reason":"sig-test"})
  fmt.Println("logsignal err", err)
}