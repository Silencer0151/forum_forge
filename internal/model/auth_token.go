package model

import "time"

// TokenType identifies what an AuthToken is good for.
type TokenType string

const (
	// TokenTypeVerifyEmail tokens validate the email address used at registration.
	TokenTypeVerifyEmail TokenType = "verify"
	// TokenTypeResetPassword tokens authorise a password reset.
	TokenTypeResetPassword TokenType = "reset"
)

// IsValid reports whether t is a known token type.
func (t TokenType) IsValid() bool {
	switch t {
	case TokenTypeVerifyEmail, TokenTypeResetPassword:
		return true
	}
	return false
}

// AuthToken is a single-use credential issued by the standalone auth flow for
// email verification or password reset. Only the SHA-256 hash of the raw token
// is persisted — the raw value is delivered to the user via email and never
// stored at rest.
type AuthToken struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"user_id"`
	Type      TokenType `json:"type"`
	TokenHash string    `json:"-"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

// IsExpired reports whether the token's expiry has passed.
func (a *AuthToken) IsExpired() bool {
	return time.Now().After(a.ExpiresAt)
}
