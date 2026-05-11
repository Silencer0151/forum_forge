package model

// Settings holds all admin-configurable forum values, persisted as key-value
// pairs in the settings table and cached in-memory at runtime.
type Settings struct {
	EditWindowMinutes              int      `json:"edit_window_minutes"`
	PostsPerPage                   int      `json:"posts_per_page"`
	ThreadsPerPage                 int      `json:"threads_per_page"`
	MaxAttachmentSizeBytes         int64    `json:"max_attachment_size_bytes"`
	AllowedAttachmentTypes         []string `json:"allowed_attachment_types"`
	RateLimitPostsPerMinute        int      `json:"rate_limit_posts_per_minute"`
	RateLimitThreadsPerHour        int      `json:"rate_limit_threads_per_hour"`
	RequireCaptchaUntilPostCount   int      `json:"require_captcha_until_post_count"`
	// MaxLinksForNewUsers caps the number of URLs allowed in a post body for
	// users with PostCount < NewUserLinkPostCount. Existing/established users
	// are not subject to this cap.
	MaxLinksForNewUsers  int `json:"max_links_for_new_users"`
	NewUserLinkPostCount int `json:"new_user_link_post_count"`
	// KeywordBlocklist is a case-insensitive list of substrings that cause a
	// post to be held for moderator review when matched in the body.
	KeywordBlocklist []string `json:"keyword_blocklist"`
}

func DefaultSettings() Settings {
	return Settings{
		EditWindowMinutes:            30,
		PostsPerPage:                 20,
		ThreadsPerPage:               25,
		MaxAttachmentSizeBytes:       5 * 1024 * 1024, // 5MB
		AllowedAttachmentTypes:       []string{"image/jpeg", "image/png", "image/gif", "image/webp", "application/pdf", "application/zip"},
		RateLimitPostsPerMinute:      3,
		RateLimitThreadsPerHour:      5,
		RequireCaptchaUntilPostCount: 5,
		MaxLinksForNewUsers:          2,
		NewUserLinkPostCount:         10,
		KeywordBlocklist:             []string{},
	}
}
