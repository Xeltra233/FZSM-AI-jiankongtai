package main
import (
  "encoding/json"
  "fmt"
  "fzsmbot/internal/client"
  "fzsmbot/internal/config"
  "fzsmbot/internal/keepalive"
  "fzsmbot/internal/storage"
)
func main(){
  cfg,err:=config.Load("config.yaml"); if err!=nil {panic(err)}
  fmt.Println("api_base=", cfg.APIBase, "cookie=", cfg.CookieFile)
  st,err:=storage.Open(cfg.Storage.DBPath); if err!=nil {panic(err)}; defer st.Close()
  c,err:=client.New(cfg.APIBase,"https://api.fanzisima.xyz",cfg.CookieFile); if err!=nil {panic(err)}
  code,data,err:=c.StocksGet("/me"); fmt.Println("stocks",code,err)
  b,_:=json.Marshal(data); s:=string(b); if len(s)>300 {s=s[:300]}; fmt.Println(s)
  code2,data2,err2:=c.LotteryGet("/lottery/api/me"); fmt.Println("lottery",code2,err2)
  b2,_:=json.Marshal(data2); s2:=string(b2); if len(s2)>300 {s2=s2[:300]}; fmt.Println(s2)
  ka:=keepalive.Run(cfg,st,c,true,1)
  b3,_:=json.Marshal(ka); s3:=string(b3); if len(s3)>600 {s3=s3[:600]}; fmt.Println("ka",s3)
}
