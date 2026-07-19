package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fzsmbot/internal/config"
	"fzsmbot/internal/dashboard"
	"fzsmbot/internal/logutil"
	"fzsmbot/internal/storage"
)

func main() {
	cfgPath := flag.String("c", "config/config.yaml", "config path")
	host := flag.String("host", "", "override host")
	port := flag.Int("port", 0, "override port")
	htmlPath := flag.String("html", "web/dashboard.html", "dashboard html path")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	logCloser, err := logutil.Setup(logutil.Options{
		Dir:        cfg.Storage.LogDir,
		Name:       "dashboard.log",
		AlsoStdout: true,
	})
	if err != nil {
		log.Fatalf("log setup: %v", err)
	}
	defer logCloser.Close()

	if *host != "" {
		cfg.Dashboard.Host = *host
	}
	if *port > 0 {
		cfg.Dashboard.Port = *port
	}
	if dashboardNeedsPassword(cfg.Dashboard.Host) && strings.TrimSpace(os.Getenv("FZSM_ADMIN_PASSWORD")) == "" && strings.TrimSpace(os.Getenv("FZSM_ADMIN_TOKEN")) == "" {
		log.Fatal("refusing non-loopback dashboard listen without FZSM_ADMIN_PASSWORD")
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
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Minute, // large authenticated DB exports may be slow
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	if err := httpServer.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func dashboardNeedsPassword(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" || host == "localhost" {
		return false
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip == nil || !ip.IsLoopback()
}
