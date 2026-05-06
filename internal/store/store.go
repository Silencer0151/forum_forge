package store

import (
	"context"
	"errors"
	"time"

	"github.com/nitro/forum_forge/internal/model"
)

// PageRequest carries pagination parameters.
type PageRequest struct {
	Page    int
	PerPage int
}

// PageResult is a generic paginated response.
type PageResult[T any] struct {
	Items      []T
	Total      int
	Page       int
	TotalPages int
}

// ThreadListOptions filters and sorts thread listings.
type ThreadListOptions struct {
	SubcategoryID int64
	PinnedFirst   bool
	Page          PageRequest
}

// ThreadWithMeta joins a thread with its author and last-post information for listing pages.
type ThreadWithMeta struct {
	Thread           *model.Thread
	AuthorUsername   string
	AuthorAvatarURL  string
	LastPostUsername *string
}

// PostWithAuthor joins a post with its full author record for display.
type PostWithAuthor struct {
	Post   *model.Post
	Author *model.User
}

// ReactionCount holds the count of a specific reaction type on a post.
type ReactionCount struct {
	Type  model.ReactionType
	Count int
}

// SearchResult is a single FTS5 search hit with a highlighted snippet.
type SearchResult struct {
	PostID         int64
	ThreadID       int64
	ThreadTitle    string
	Snippet        string
	AuthorUsername string
	CreatedAt      time.Time
}

// UserStore handles user persistence.
type UserStore interface {
	CreateUser(ctx context.Context, u *model.User) error
	GetUserByID(ctx context.Context, id int64) (*model.User, error)
	GetUserByEmail(ctx context.Context, email string) (*model.User, error)
	GetUserByUsername(ctx context.Context, username string) (*model.User, error)
	GetUserByExternalID(ctx context.Context, externalID, source string) (*model.User, error)
	UpdateUser(ctx context.Context, u *model.User) error
	IncrementPostCount(ctx context.Context, userID int64) error
	UpdateLastSeen(ctx context.Context, userID int64, t time.Time) error
	BanUser(ctx context.Context, userID int64, reason string) error
	UnbanUser(ctx context.Context, userID int64) error
}

// SubcategoryStats holds aggregate stats for a subcategory for display on listing pages.
type SubcategoryStats struct {
	SubcategoryID    int64
	ThreadCount      int
	PostCount        int
	LastPostAt       time.Time // zero value means no posts
	LastPostUsername string    // empty means no posts
	LastThreadTitle  string    // empty means no posts
}

// CategoryStore handles categories and subcategories.
type CategoryStore interface {
	CreateCategory(ctx context.Context, c *model.Category) error
	GetCategoryByID(ctx context.Context, id int64) (*model.Category, error)
	GetCategoryBySlug(ctx context.Context, slug string) (*model.Category, error)
	UpdateCategory(ctx context.Context, c *model.Category) error
	DeleteCategory(ctx context.Context, id int64) error
	ListCategories(ctx context.Context) ([]*model.Category, error)
	ReorderCategories(ctx context.Context, ids []int64) error

	CreateSubcategory(ctx context.Context, s *model.Subcategory) error
	GetSubcategoryByID(ctx context.Context, id int64) (*model.Subcategory, error)
	GetSubcategoryBySlug(ctx context.Context, categorySlug, subcategorySlug string) (*model.Subcategory, error)
	UpdateSubcategory(ctx context.Context, s *model.Subcategory) error
	DeleteSubcategory(ctx context.Context, id int64) error
	ListSubcategoriesByCategoryID(ctx context.Context, categoryID int64) ([]*model.Subcategory, error)
	ReorderSubcategories(ctx context.Context, ids []int64) error

	// GetSubcategoryStatsBatch returns thread/post counts and last-post info for
	// multiple subcategories in a single query. Returns an empty map for empty input.
	GetSubcategoryStatsBatch(ctx context.Context, subcategoryIDs []int64) (map[int64]*SubcategoryStats, error)
}

// ThreadStore handles thread persistence and counters.
type ThreadStore interface {
	CreateThread(ctx context.Context, t *model.Thread) error
	GetThreadByID(ctx context.Context, id int64) (*model.Thread, error)
	ListThreads(ctx context.Context, opts ThreadListOptions) (*PageResult[*ThreadWithMeta], error)
	UpdateThread(ctx context.Context, t *model.Thread) error
	IncrementViewCount(ctx context.Context, threadID int64) error
	UpdateLastPost(ctx context.Context, threadID, userID int64, at time.Time) error
	PinThread(ctx context.Context, threadID int64, pinned bool) error
	LockThread(ctx context.Context, threadID int64, locked bool) error
	DeleteThread(ctx context.Context, threadID int64) error
}

// PostStore handles post persistence.
type PostStore interface {
	CreatePost(ctx context.Context, p *model.Post) error
	GetPostByID(ctx context.Context, id int64) (*model.Post, error)
	ListPostsByThread(ctx context.Context, threadID int64, page PageRequest) (*PageResult[*PostWithAuthor], error)
	ListPostsByAuthor(ctx context.Context, userID int64, page PageRequest) (*PageResult[*PostWithAuthor], error)
	UpdatePost(ctx context.Context, p *model.Post) error
	DeletePost(ctx context.Context, postID, deletedBy int64) error
}

// ReactionStore handles post reactions (toggle, counts, batch user-reactions).
type ReactionStore interface {
	ToggleReaction(ctx context.Context, postID, userID int64, reactionType model.ReactionType) (added bool, err error)
	GetReactionCounts(ctx context.Context, postID int64) ([]ReactionCount, error)
	GetUserReactionsForPosts(ctx context.Context, userID int64, postIDs []int64) (map[int64][]model.ReactionType, error)
}

// PMStore handles private messages.
type PMStore interface {
	CreatePM(ctx context.Context, pm *model.PrivateMessage) error
	GetPMByID(ctx context.Context, id int64) (*model.PrivateMessage, error)
	ListInbox(ctx context.Context, recipientID int64, page PageRequest) (*PageResult[*model.PrivateMessage], error)
	MarkPMRead(ctx context.Context, pmID int64) error
	DeletePMForSender(ctx context.Context, pmID int64) error
	DeletePMForRecipient(ctx context.Context, pmID int64) error
}

// ReportStore handles post reports and the moderation queue.
type ReportStore interface {
	CreateReport(ctx context.Context, r *model.Report) error
	GetReportByID(ctx context.Context, id int64) (*model.Report, error)
	ListOpenReports(ctx context.Context, page PageRequest) (*PageResult[*model.Report], error)
	ReviewReport(ctx context.Context, reportID, reviewerID int64, status model.ReportStatus) error
}

// DraftStore handles auto-saved drafts.
type DraftStore interface {
	UpsertDraft(ctx context.Context, d *model.Draft) error
	GetDraftByThread(ctx context.Context, userID, threadID int64) (*model.Draft, error)
	GetDraftBySubcategory(ctx context.Context, userID, subcategoryID int64) (*model.Draft, error)
	DeleteDraft(ctx context.Context, id int64) error
	CleanupOldDrafts(ctx context.Context, olderThan time.Time) error
}

// SessionStore handles session persistence.
type SessionStore interface {
	CreateSession(ctx context.Context, s *model.Session) error
	GetSession(ctx context.Context, id string) (*model.Session, error)
	DeleteSession(ctx context.Context, id string) error
	DeleteUserSessions(ctx context.Context, userID int64) error
}

// SettingsStore handles admin-configurable forum settings.
type SettingsStore interface {
	GetSettings(ctx context.Context) (*model.Settings, error)
	SaveSettings(ctx context.Context, s *model.Settings) error
}

// AttachmentStore handles file attachments on posts.
type AttachmentStore interface {
	CreateAttachment(ctx context.Context, a *model.Attachment) error
	GetAttachmentByID(ctx context.Context, id int64) (*model.Attachment, error)
	ListAttachmentsByPostID(ctx context.Context, postID int64) ([]*model.Attachment, error)
	DeleteAttachment(ctx context.Context, id int64) error
	DeleteOrphanedAttachments(ctx context.Context, olderThan time.Time) error
}

// SearchStore handles full-text search over posts.
type SearchStore interface {
	Search(ctx context.Context, query string, page PageRequest) (*PageResult[SearchResult], error)
}

// ErrNotFound is returned by Get* methods when the requested record does not exist.
var ErrNotFound = errors.New("store: not found")

// Store composes all sub-interfaces into a single dependency.
type Store interface {
	UserStore
	CategoryStore
	ThreadStore
	PostStore
	ReactionStore
	PMStore
	ReportStore
	DraftStore
	SessionStore
	SettingsStore
	AttachmentStore
	SearchStore
}
