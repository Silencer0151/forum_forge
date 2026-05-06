package model

import "time"

type Role string

const (
	RoleAdmin     Role = "admin"
	RoleModerator Role = "moderator"
	RoleMember    Role = "member"
	RoleGuest     Role = "guest"
)

func (r Role) IsValid() bool {
	switch r {
	case RoleAdmin, RoleModerator, RoleMember, RoleGuest:
		return true
	}
	return false
}

func (r Role) CanModerate() bool {
	return r == RoleAdmin || r == RoleModerator
}

func (r Role) CanAdmin() bool {
	return r == RoleAdmin
}

type User struct {
	ID             int64     `json:"id"`
	Username       string    `json:"username"`
	Email          string    `json:"email,omitempty"`
	PasswordHash   string    `json:"-"`
	DisplayName    string    `json:"display_name,omitempty"`
	AvatarURL      string    `json:"avatar_url,omitempty"`
	Signature      string    `json:"signature,omitempty"`
	Role           Role      `json:"role"`
	PostCount      int       `json:"post_count"`
	CreatedAt      time.Time `json:"created_at"`
	LastSeenAt     time.Time `json:"last_seen_at"`
	Banned         bool      `json:"banned"`
	BanReason      string    `json:"ban_reason,omitempty"`
	EmailVerified  bool      `json:"email_verified"`
	ExternalID     string    `json:"external_id,omitempty"`
	ExternalSource string    `json:"external_source,omitempty"`
}

func (u *User) DisplayNameOrUsername() string {
	if u.DisplayName != "" {
		return u.DisplayName
	}
	return u.Username
}
