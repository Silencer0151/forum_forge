package main

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/nitro/forum_forge/internal/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	fmt.Println("forum starting")
	fmt.Printf("  db_driver     = %s\n", cfg.DBDriver)
	fmt.Printf("  db_path       = %s\n", cfg.DBPath)
	fmt.Printf("  listen_addr   = %s\n", cfg.ListenAddr)
	fmt.Printf("  base_url      = %s\n", cfg.BaseURL)
	fmt.Printf("  auth_mode     = %s\n", cfg.AuthMode)
	fmt.Printf("  tls_mode      = %s\n", cfg.TLSMode)
	if len(cfg.TLSDomains) > 0 {
		fmt.Printf("  tls_domains   = %s\n", strings.Join(cfg.TLSDomains, ", "))
	}
	fmt.Printf("  upload_path   = %s\n", cfg.UploadPath)
	fmt.Printf("  trust_proxy   = %v\n", cfg.TrustProxy)
	fmt.Printf("  captcha       = %s\n", cfg.CaptchaProvider)
	fmt.Printf("  log_level     = %s\n", cfg.LogLevel)
	fmt.Printf("  log_format    = %s\n", cfg.LogFormat)
}
