package main

import (
        "flag"
        "fmt"
        "log"
        "net/http"
        "path/filepath"

        "fzsmbot/internal/config"
        "fzsmbot/internal/dashboard"
        "fzsmbot/internal/storage"
)

func main() {
        cfgPath := flag.String("c", "config.yaml", "config path")
        host := flag.String("host", "", "override host")
        port := flag.Int("port", 0, "override port")
        htmlPath := flag.String("html", "web/dashboard.html", "dashboard html path")
        flag.Parse()

        cfg, err := config.Load(*cfgPath)
        if err != nil {
                log.Fatalf("load config: %v", err)
        }
        if *host != "" {
                cfg.Dashboard.Host = *host
        }
        if *port > 0 {
                cfg.Dashboard.Port = *port
        }
        // use config dashboard.port as-is (Go-only)

        st, err := storage.Open(cfg.Storage.DBPath)
        if err != nil {
                log.Fatalf("storage: %v", err)
        }
        defer st.Close()

        html := *htmlPath
        if !filepath.IsAbs(html) {
                // keep relative to cwd
        }
        srv, err := dashboard.New(cfg, st, html)
        if err != nil {
                log.Fatalf("dashboard: %v", err)
        }
        addr := fmt.Sprintf("%s:%d", cfg.Dashboard.Host, cfg.Dashboard.Port)
        log.Printf("go dashboard listening on http://%s", addr)
        if err := http.ListenAndServe(addr, srv.Handler()); err != nil {
                log.Fatal(err)
        }
}
