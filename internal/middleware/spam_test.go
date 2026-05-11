package middleware

import "testing"

func TestCountURLs(t *testing.T) {
	cases := []struct {
		name string
		text string
		want int
	}{
		{"empty", "", 0},
		{"plain text", "no links here", 0},
		{"single http", "check out http://example.com for info", 1},
		{"single https", "see https://foo.com/path?a=1", 1},
		{"two links", "https://foo.com and http://bar.org/x", 2},
		{"ftp not matched", "ftp://files.example.com", 0},
		{"link in parens", "(see https://example.com/a)", 1},
		{"markdown link", "[name](https://example.com)", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CountURLs(c.text); got != c.want {
				t.Errorf("CountURLs(%q) = %d, want %d", c.text, got, c.want)
			}
		})
	}
}

func TestCheckKeywords(t *testing.T) {
	blocklist := []string{"spam", "casino", "buy now", ""}
	cases := []struct {
		name string
		text string
		want string
	}{
		{"clean", "this is a clean post", ""},
		{"case-insensitive", "Click here for CASINO Deals", "casino"},
		{"multi-word", "buy now while stocks last", "buy now"},
		{"substring", "the word spammy is here", "spam"},
		{"empty blocklist entry skipped", "nothing to see", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CheckKeywords(c.text, blocklist); got != c.want {
				t.Errorf("CheckKeywords(%q) = %q, want %q", c.text, got, c.want)
			}
		})
	}

	t.Run("empty blocklist", func(t *testing.T) {
		if got := CheckKeywords("any text", nil); got != "" {
			t.Errorf("CheckKeywords with nil blocklist = %q, want empty", got)
		}
	})
}

func TestNeedsCaptcha(t *testing.T) {
	cases := []struct {
		name      string
		provider  string
		postCount int
		threshold int
		want      bool
	}{
		{"provider none", "none", 0, 5, false},
		{"provider empty", "", 0, 5, false},
		{"new user requires captcha", "hcaptcha", 0, 5, true},
		{"under threshold", "hcaptcha", 4, 5, true},
		{"at threshold", "hcaptcha", 5, 5, false},
		{"established user", "hcaptcha", 10, 5, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := NeedsCaptcha(c.provider, c.postCount, c.threshold)
			if got != c.want {
				t.Errorf("NeedsCaptcha(%q, %d, %d) = %v, want %v",
					c.provider, c.postCount, c.threshold, got, c.want)
			}
		})
	}
}

func TestValidateCaptchaNoOp(t *testing.T) {
	if err := ValidateCaptcha("none", "", "", ""); err != nil {
		t.Errorf("ValidateCaptcha none should be no-op, got %v", err)
	}
	if err := ValidateCaptcha("", "", "", ""); err != nil {
		t.Errorf("ValidateCaptcha empty provider should be no-op, got %v", err)
	}
}
