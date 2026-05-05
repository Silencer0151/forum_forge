package config

import (
	"os"
	"strings"
	"testing"
)

// allForumEnvVars is used to clear the environment before each test.
var allForumEnvVars = []string{
	"FORUM_DB_DRIVER", "FORUM_DB_PATH", "FORUM_DB_DSN",
	"FORUM_LISTEN_ADDR", "FORUM_BASE_URL", "FORUM_UPLOAD_PATH",
	"FORUM_AUTH_MODE", "FORUM_SESSION_SECRET", "FORUM_TLS_MODE",
	"FORUM_TLS_DOMAINS", "FORUM_TLS_CERT_DIR", "FORUM_TLS_CERT_FILE",
	"FORUM_TLS_KEY_FILE", "FORUM_REDIRECT_HTTP", "FORUM_TRUST_PROXY",
	"FORUM_SMTP_HOST", "FORUM_SMTP_PORT", "FORUM_SMTP_USER",
	"FORUM_SMTP_PASS", "FORUM_SMTP_FROM", "FORUM_CAPTCHA_PROVIDER",
	"FORUM_CAPTCHA_SITEKEY", "FORUM_CAPTCHA_SECRET",
	"FORUM_LOG_LEVEL", "FORUM_LOG_FORMAT",
}

func setenv(t *testing.T, key, value string) {
	t.Helper()
	old, hadOld := os.LookupEnv(key)
	os.Setenv(key, value)
	t.Cleanup(func() {
		if hadOld {
			os.Setenv(key, old)
		} else {
			os.Unsetenv(key)
		}
	})
}

func clearAllForumEnv(t *testing.T) {
	t.Helper()
	for _, k := range allForumEnvVars {
		old, hadOld := os.LookupEnv(k)
		os.Unsetenv(k)
		k, old, hadOld := k, old, hadOld
		t.Cleanup(func() {
			if hadOld {
				os.Setenv(k, old)
			}
		})
	}
}

func TestLoad_Defaults(t *testing.T) {
	clearAllForumEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"DBDriver", cfg.DBDriver, "sqlite"},
		{"DBPath", cfg.DBPath, "./data/forum.db"},
		{"ListenAddr", cfg.ListenAddr, ":8080"},
		{"BaseURL", cfg.BaseURL, "http://localhost:8080"},
		{"UploadPath", cfg.UploadPath, "./uploads"},
		{"AuthMode", cfg.AuthMode, "standalone"},
		{"TLSMode", cfg.TLSMode, "none"},
		{"TLSCertDir", cfg.TLSCertDir, "./certs"},
		{"SMTPPort", cfg.SMTPPort, 587},
		{"CaptchaProvider", cfg.CaptchaProvider, "none"},
		{"LogLevel", cfg.LogLevel, "info"},
		{"LogFormat", cfg.LogFormat, "text"},
		{"TrustProxy", cfg.TrustProxy, false},
		{"RedirectHTTP", cfg.RedirectHTTP, false},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestLoad_TLSModeAutoSetsListenAddr(t *testing.T) {
	clearAllForumEnv(t)
	setenv(t, "FORUM_TLS_MODE", "autocert")
	setenv(t, "FORUM_TLS_DOMAINS", "forum.example.com")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.ListenAddr != ":443" {
		t.Errorf("ListenAddr = %q, want :443 when TLS enabled", cfg.ListenAddr)
	}
	if !cfg.RedirectHTTP {
		t.Error("RedirectHTTP should default to true when TLS enabled")
	}
}

func TestLoad_TLSDomainsParseCommaSeparated(t *testing.T) {
	clearAllForumEnv(t)
	setenv(t, "FORUM_TLS_MODE", "autocert")
	setenv(t, "FORUM_TLS_DOMAINS", "forum.example.com, api.example.com , extra.example.com")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(cfg.TLSDomains) != 3 {
		t.Errorf("TLSDomains len = %d, want 3", len(cfg.TLSDomains))
	}
}

func TestLoad_Validation(t *testing.T) {
	// Base valid env — tests override specific keys to trigger errors.
	baseEnv := map[string]string{
		"FORUM_DB_DRIVER": "sqlite",
		"FORUM_BASE_URL":  "http://localhost:8080",
		"FORUM_TLS_MODE":  "none",
		"FORUM_AUTH_MODE": "standalone",
	}

	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{
			name:    "invalid BASE_URL — no scheme",
			env:     map[string]string{"FORUM_BASE_URL": "localhost:8080"},
			wantErr: "FORUM_BASE_URL",
		},
		{
			name:    "invalid BASE_URL — plain string",
			env:     map[string]string{"FORUM_BASE_URL": "not-a-url"},
			wantErr: "FORUM_BASE_URL",
		},
		{
			name:    "invalid TLS_MODE",
			env:     map[string]string{"FORUM_TLS_MODE": "letsencrypt"},
			wantErr: "FORUM_TLS_MODE",
		},
		{
			name:    "invalid AUTH_MODE",
			env:     map[string]string{"FORUM_AUTH_MODE": "oauth"},
			wantErr: "FORUM_AUTH_MODE",
		},
		{
			name:    "invalid DB_DRIVER",
			env:     map[string]string{"FORUM_DB_DRIVER": "mysql"},
			wantErr: "FORUM_DB_DRIVER",
		},
		{
			name:    "autocert requires TLS_DOMAINS",
			env:     map[string]string{"FORUM_TLS_MODE": "autocert"},
			wantErr: "FORUM_TLS_DOMAINS",
		},
		{
			name:    "manual requires CERT_FILE",
			env:     map[string]string{"FORUM_TLS_MODE": "manual", "FORUM_TLS_KEY_FILE": "/key.pem"},
			wantErr: "FORUM_TLS_CERT_FILE",
		},
		{
			name:    "manual requires KEY_FILE",
			env:     map[string]string{"FORUM_TLS_MODE": "manual", "FORUM_TLS_CERT_FILE": "/cert.pem"},
			wantErr: "FORUM_TLS_KEY_FILE",
		},
		// Valid cases
		{
			name: "autocert with domains",
			env:  map[string]string{"FORUM_TLS_MODE": "autocert", "FORUM_TLS_DOMAINS": "forum.example.com"},
		},
		{
			name: "manual with both files",
			env:  map[string]string{"FORUM_TLS_MODE": "manual", "FORUM_TLS_CERT_FILE": "/cert.pem", "FORUM_TLS_KEY_FILE": "/key.pem"},
		},
		{
			name: "postgres driver",
			env:  map[string]string{"FORUM_DB_DRIVER": "postgres"},
		},
		{
			name: "integrated auth mode",
			env:  map[string]string{"FORUM_AUTH_MODE": "integrated"},
		},
		{
			name: "https BASE_URL",
			env:  map[string]string{"FORUM_BASE_URL": "https://forum.example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearAllForumEnv(t)
			for k, v := range baseEnv {
				setenv(t, k, v)
			}
			for k, v := range tt.env {
				setenv(t, k, v)
			}

			_, err := Load()
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("Load() expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("Load() error = %q, want it to contain %q", err.Error(), tt.wantErr)
				}
			} else {
				if err != nil {
					t.Fatalf("Load() unexpected error: %v", err)
				}
			}
		})
	}
}
