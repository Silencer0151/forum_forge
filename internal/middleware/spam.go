package middleware

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// urlPattern matches http:// and https:// URLs in post bodies. It is intentionally
// loose: spammers care about getting clicks, so any plausible URL counts.
var urlPattern = regexp.MustCompile(`(?i)https?://[^\s"'<>)\]]+`)

// CountURLs returns the number of http(s) URLs found in text.
func CountURLs(text string) int {
	return len(urlPattern.FindAllString(text, -1))
}

// CheckKeywords returns the first keyword from blocklist that appears in text
// (case-insensitive substring match), or "" if none match. Empty entries in
// blocklist are skipped so admins can save the settings form with trailing blanks.
func CheckKeywords(text string, blocklist []string) string {
	if len(blocklist) == 0 {
		return ""
	}
	lower := strings.ToLower(text)
	for _, kw := range blocklist {
		kw = strings.TrimSpace(kw)
		if kw == "" {
			continue
		}
		if strings.Contains(lower, strings.ToLower(kw)) {
			return kw
		}
	}
	return ""
}

// NeedsCaptcha reports whether a user with the given post count must solve a
// CAPTCHA before posting. Returns false when the captcha provider is "none"
// or unset so dev environments don't require a token.
func NeedsCaptcha(captchaProvider string, postCount, threshold int) bool {
	if captchaProvider == "" || captchaProvider == "none" {
		return false
	}
	return postCount < threshold
}

// ValidateCaptcha verifies a CAPTCHA response token against the configured
// provider. It is a no-op when provider is "none" or empty so the call site
// can be unconditional.
func ValidateCaptcha(provider, secret, token, clientIP string) error {
	switch provider {
	case "", "none":
		return nil
	case "hcaptcha":
		return validateHCaptcha(secret, token, clientIP)
	default:
		// Unknown provider: don't lock users out, but note the misconfiguration.
		return nil
	}
}

type hcaptchaResponse struct {
	Success    bool     `json:"success"`
	ErrorCodes []string `json:"error-codes"`
}

func validateHCaptcha(secret, token, clientIP string) error {
	if token == "" {
		return fmt.Errorf("captcha response is required")
	}
	if secret == "" {
		return fmt.Errorf("captcha not configured")
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.PostForm("https://hcaptcha.com/siteverify", url.Values{
		"secret":   {secret},
		"response": {token},
		"remoteip": {clientIP},
	})
	if err != nil {
		return fmt.Errorf("captcha verification request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return fmt.Errorf("read captcha response: %w", err)
	}
	var result hcaptchaResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("parse captcha response: %w", err)
	}
	if !result.Success {
		return fmt.Errorf("captcha verification failed")
	}
	return nil
}
