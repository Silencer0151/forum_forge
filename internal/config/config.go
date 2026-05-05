package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	DBDriver      string
	DBPath        string
	DBDSN         string
	ListenAddr    string
	BaseURL       string
	UploadPath    string
	AuthMode      string
	SessionSecret string
	TLSMode       string
	TLSDomains    []string
	TLSCertDir    string
	TLSCertFile   string
	TLSKeyFile    string
	RedirectHTTP  bool
	TrustProxy    bool
	SMTPHost      string
	SMTPPort      int
	SMTPUser      string
	SMTPPass      string
	SMTPFrom      string
	CaptchaProvider string
	CaptchaSiteKey  string
	CaptchaSecret   string
	LogLevel      string
	LogFormat     string
}

func Load() (*Config, error) {
	tlsMode := getenv("FORUM_TLS_MODE", "none")

	defaultListenAddr := ":8080"
	if tlsMode != "none" {
		defaultListenAddr = ":443"
	}

	defaultRedirectHTTP := "false"
	if tlsMode != "none" {
		defaultRedirectHTTP = "true"
	}

	c := &Config{
		DBDriver:        getenv("FORUM_DB_DRIVER", "sqlite"),
		DBPath:          getenv("FORUM_DB_PATH", "./data/forum.db"),
		DBDSN:           getenv("FORUM_DB_DSN", ""),
		ListenAddr:      getenv("FORUM_LISTEN_ADDR", defaultListenAddr),
		BaseURL:         getenv("FORUM_BASE_URL", "http://localhost:8080"),
		UploadPath:      getenv("FORUM_UPLOAD_PATH", "./uploads"),
		AuthMode:        getenv("FORUM_AUTH_MODE", "standalone"),
		SessionSecret:   getenv("FORUM_SESSION_SECRET", ""),
		TLSMode:         tlsMode,
		TLSCertDir:      getenv("FORUM_TLS_CERT_DIR", "./certs"),
		TLSCertFile:     getenv("FORUM_TLS_CERT_FILE", ""),
		TLSKeyFile:      getenv("FORUM_TLS_KEY_FILE", ""),
		RedirectHTTP:    parseBool(getenv("FORUM_REDIRECT_HTTP", defaultRedirectHTTP)),
		TrustProxy:      parseBool(getenv("FORUM_TRUST_PROXY", "false")),
		SMTPHost:        getenv("FORUM_SMTP_HOST", ""),
		SMTPPort:        parseInt(getenv("FORUM_SMTP_PORT", "587")),
		SMTPUser:        getenv("FORUM_SMTP_USER", ""),
		SMTPPass:        getenv("FORUM_SMTP_PASS", ""),
		SMTPFrom:        getenv("FORUM_SMTP_FROM", ""),
		CaptchaProvider: getenv("FORUM_CAPTCHA_PROVIDER", "none"),
		CaptchaSiteKey:  getenv("FORUM_CAPTCHA_SITEKEY", ""),
		CaptchaSecret:   getenv("FORUM_CAPTCHA_SECRET", ""),
		LogLevel:        getenv("FORUM_LOG_LEVEL", "info"),
		LogFormat:       getenv("FORUM_LOG_FORMAT", "text"),
	}

	if raw := getenv("FORUM_TLS_DOMAINS", ""); raw != "" {
		for _, d := range strings.Split(raw, ",") {
			if d = strings.TrimSpace(d); d != "" {
				c.TLSDomains = append(c.TLSDomains, d)
			}
		}
	}

	if err := c.validate(); err != nil {
		return nil, err
	}

	if c.AuthMode == "standalone" && c.SMTPHost == "" {
		slog.Warn("SMTP not configured — email features (verification, password reset) are disabled")
	}

	return c, nil
}

func (c *Config) validate() error {
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("FORUM_BASE_URL %q is not a valid absolute URL", c.BaseURL)
	}

	switch c.TLSMode {
	case "autocert", "manual", "none":
	default:
		return fmt.Errorf("FORUM_TLS_MODE must be autocert, manual, or none; got %q", c.TLSMode)
	}

	switch c.AuthMode {
	case "standalone", "integrated":
	default:
		return fmt.Errorf("FORUM_AUTH_MODE must be standalone or integrated; got %q", c.AuthMode)
	}

	switch c.DBDriver {
	case "sqlite", "postgres":
	default:
		return fmt.Errorf("FORUM_DB_DRIVER must be sqlite or postgres; got %q", c.DBDriver)
	}

	if c.TLSMode == "autocert" && len(c.TLSDomains) == 0 {
		return fmt.Errorf("FORUM_TLS_DOMAINS is required when FORUM_TLS_MODE=autocert")
	}

	if c.TLSMode == "manual" {
		if c.TLSCertFile == "" {
			return fmt.Errorf("FORUM_TLS_CERT_FILE is required when FORUM_TLS_MODE=manual")
		}
		if c.TLSKeyFile == "" {
			return fmt.Errorf("FORUM_TLS_KEY_FILE is required when FORUM_TLS_MODE=manual")
		}
	}

	return nil
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parseBool(s string) bool {
	b, _ := strconv.ParseBool(s)
	return b
}

func parseInt(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
