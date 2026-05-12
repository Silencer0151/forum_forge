// Command seed populates a forum database with realistic demo content for
// manual smoke-testing. It writes through the SQLite store directly (no HTTP
// or CSRF layer) so it can run against a stopped server.
//
//	go run ./cmd/seed                   # seed into ./data/forum.db
//	go run ./cmd/seed -db ./other.db    # custom DB path
//	go run ./cmd/seed -force            # wipe and re-seed an existing DB
//
// Every demo account has the password "password123". Log in as admin@forum.test
// for the full admin tour, moderator@forum.test for the mod queue, or any of
// the member accounts (alice/bob/carol/dave/eve/mallory) for the member view.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"time"

	"golang.org/x/crypto/bcrypt"

	forum "github.com/nitro/forum_forge"
	"github.com/nitro/forum_forge/internal/model"
	"github.com/nitro/forum_forge/internal/store"
	sqlitestore "github.com/nitro/forum_forge/internal/store/sqlite"
)

const demoPassword = "password123"

func main() {
	dbPath := flag.String("db", "./data/forum.db", "path to the SQLite database file")
	force := flag.Bool("force", false, "wipe existing data before seeding")
	flag.Parse()

	if err := run(*dbPath, *force); err != nil {
		log.Fatal(err)
	}
}

func run(dbPath string, force bool) error {
	if err := os.MkdirAll(parentDir(dbPath), 0o755); err != nil {
		return fmt.Errorf("create db directory: %w", err)
	}

	migrationsFS, err := fs.Sub(forum.MigrationsFS(), "migrations")
	if err != nil {
		return fmt.Errorf("sub migrations: %w", err)
	}

	st, err := sqlitestore.New(dbPath, migrationsFS)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}

	ctx := context.Background()

	existing, err := dataExists(ctx, st)
	if err != nil {
		st.Close()
		return err
	}
	if existing && !force {
		st.Close()
		return errors.New("database already has content; pass -force to wipe and re-seed")
	}

	if existing && force {
		// SQLite holds an exclusive lock on the file while open. On Windows,
		// os.Remove fails for locked files, so close before wiping and re-open
		// afterwards.
		if err := st.Close(); err != nil {
			return fmt.Errorf("close before wipe: %w", err)
		}
		if err := wipe(dbPath); err != nil {
			return fmt.Errorf("wipe: %w", err)
		}
		st, err = sqlitestore.New(dbPath, migrationsFS)
		if err != nil {
			return fmt.Errorf("reopen store after wipe: %w", err)
		}
	}
	defer st.Close()

	users, err := seedUsers(ctx, st)
	if err != nil {
		return fmt.Errorf("seed users: %w", err)
	}

	cats, subs, err := seedCategories(ctx, st)
	if err != nil {
		return fmt.Errorf("seed categories: %w", err)
	}

	if err := seedThreadsAndPosts(ctx, st, users, cats, subs); err != nil {
		return fmt.Errorf("seed threads: %w", err)
	}

	printSummary(users)
	return nil
}

// ── User seeding ─────────────────────────────────────────────────────────────

type seedUsers_ struct {
	Admin    *model.User
	Mod      *model.User
	Alice    *model.User // established member (PostCount=50)
	Bob      *model.User // mid-tenure member (PostCount=15)
	Carol    *model.User // new member, hits the link limit (PostCount=5)
	Dave     *model.User // very new member (PostCount=1)
	Eve      *model.User // banned account
	Mallory  *model.User // zero posts, CAPTCHA-required
}

func seedUsers(ctx context.Context, st *sqlitestore.Store) (*seedUsers_, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(demoPassword), bcrypt.MinCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}
	pw := string(hash)

	users := &seedUsers_{
		Admin: &model.User{
			Username: "admin", Email: "admin@forum.test", PasswordHash: pw,
			DisplayName: "Site Admin", Role: model.RoleAdmin, EmailVerified: true,
		},
		Mod: &model.User{
			Username: "moderator", Email: "moderator@forum.test", PasswordHash: pw,
			DisplayName: "The Moderator", Role: model.RoleModerator,
			Signature: "Reports go through me.", PostCount: 40, EmailVerified: true,
		},
		Alice: &model.User{
			Username: "alice", Email: "alice@forum.test", PasswordHash: pw,
			DisplayName: "Alice Anderson", Role: model.RoleMember,
			Signature: "— sent from my workstation", PostCount: 50, EmailVerified: true,
		},
		Bob: &model.User{
			Username: "bob", Email: "bob@forum.test", PasswordHash: pw,
			Role: model.RoleMember, PostCount: 15, EmailVerified: true,
		},
		Carol: &model.User{
			Username: "carol", Email: "carol@forum.test", PasswordHash: pw,
			Role: model.RoleMember, PostCount: 5, EmailVerified: true,
		},
		Dave: &model.User{
			Username: "dave", Email: "dave@forum.test", PasswordHash: pw,
			Role: model.RoleMember, PostCount: 1, EmailVerified: false,
		},
		Eve: &model.User{
			Username: "eve", Email: "eve@forum.test", PasswordHash: pw,
			Role: model.RoleMember, PostCount: 8, EmailVerified: true,
			Banned: true, BanReason: "Repeated rule violations.",
		},
		Mallory: &model.User{
			Username: "mallory", Email: "mallory@forum.test", PasswordHash: pw,
			Role: model.RoleMember, PostCount: 0, EmailVerified: false,
		},
	}

	all := []*model.User{users.Admin, users.Mod, users.Alice, users.Bob, users.Carol, users.Dave, users.Eve, users.Mallory}
	for _, u := range all {
		if err := st.CreateUser(ctx, u); err != nil {
			return nil, fmt.Errorf("create user %s: %w", u.Username, err)
		}
		// CreateUser ignores Role/PostCount/Banned/EmailVerified on insert
		// (the schema has defaults for those columns), so write them back via
		// UpdateUser after the row exists.
		if err := st.UpdateUser(ctx, u); err != nil {
			return nil, fmt.Errorf("update user %s: %w", u.Username, err)
		}
	}
	return users, nil
}

// ── Category seeding ─────────────────────────────────────────────────────────

type seedCats struct {
	General *model.Category
	Tech    *model.Category
}
type seedSubs struct {
	Announcements *model.Subcategory
	GeneralDisc   *model.Subcategory
	Software      *model.Subcategory
	Hardware      *model.Subcategory
}

func seedCategories(ctx context.Context, st *sqlitestore.Store) (*seedCats, *seedSubs, error) {
	cats := &seedCats{
		General: &model.Category{Name: "General", Slug: "general", Description: "Site-wide discussion and announcements.", DisplayOrder: 1, IsPublic: true},
		Tech:    &model.Category{Name: "Tech", Slug: "tech", Description: "Software, hardware, and the rest.", DisplayOrder: 2, IsPublic: true},
	}
	for _, c := range []*model.Category{cats.General, cats.Tech} {
		if err := st.CreateCategory(ctx, c); err != nil {
			return nil, nil, fmt.Errorf("create category %s: %w", c.Slug, err)
		}
	}

	subs := &seedSubs{
		Announcements: &model.Subcategory{CategoryID: cats.General.ID, Name: "Announcements", Slug: "announcements", Description: "Official updates from the team.", DisplayOrder: 1},
		GeneralDisc:   &model.Subcategory{CategoryID: cats.General.ID, Name: "General Discussion", Slug: "general-discussion", Description: "Talk about anything.", DisplayOrder: 2},
		Software:      &model.Subcategory{CategoryID: cats.Tech.ID, Name: "Software", Slug: "software", Description: "Languages, frameworks, tools.", DisplayOrder: 1},
		Hardware:      &model.Subcategory{CategoryID: cats.Tech.ID, Name: "Hardware", Slug: "hardware", Description: "Builds, peripherals, repairs.", DisplayOrder: 2},
	}
	for _, s := range []*model.Subcategory{subs.Announcements, subs.GeneralDisc, subs.Software, subs.Hardware} {
		if err := st.CreateSubcategory(ctx, s); err != nil {
			return nil, nil, fmt.Errorf("create subcategory %s: %w", s.Slug, err)
		}
	}
	return cats, subs, nil
}

// ── Thread / post seeding ────────────────────────────────────────────────────

// makeThread inserts a thread + OP post and bumps reply_count for each reply.
// The handler-level rules are duplicated here intentionally: CreatePost on its
// own no longer touches the thread row, so the caller (this seed) must invoke
// UpdateLastPost once per reply to keep reply_count in sync.
type replySpec struct {
	Author *model.User
	Body   string
	Format model.BodyFormat
	Held   bool
}

type threadSpec struct {
	Sub     *model.Subcategory
	Author  *model.User
	Title   string
	Body    string
	Format  model.BodyFormat
	Age     time.Duration // how far back the thread was created
	Pinned  bool
	Locked  bool
	Replies []replySpec
}

func seedThreadsAndPosts(ctx context.Context, st *sqlitestore.Store, u *seedUsers_, c *seedCats, s *seedSubs) error {
	_ = c

	specs := []threadSpec{
		// ── Announcements ─────────────────────────────────────────────────
		{
			Sub: s.Announcements, Author: u.Admin, Pinned: true,
			Title:  "Welcome to ForumForge — please read",
			Body:   "Hi everyone, and welcome.\n\n- Be kind.\n- Stay on topic.\n- Report rather than retaliate.\n\nThanks!",
			Format: model.BodyFormatMarkdown,
			Age:    7 * 24 * time.Hour,
			Replies: []replySpec{
				{Author: u.Alice, Body: "Glad to be here. Excited to see what people build.", Format: model.BodyFormatMarkdown},
				{Author: u.Bob, Body: "+1, looking forward to it.", Format: model.BodyFormatPlaintext},
			},
		},
		// ── General Discussion (enough threads to make pagination real) ───
		{
			Sub: s.GeneralDisc, Author: u.Alice,
			Title:  "What are you working on this week?",
			Body:   "I'm rewriting an old Go service to use `slog` and channels everywhere. What about you?",
			Format: model.BodyFormatMarkdown,
			Age:    2 * 24 * time.Hour,
			Replies: []replySpec{
				{Author: u.Bob, Body: "Migrating a Postgres schema. Going slower than I'd like.", Format: model.BodyFormatMarkdown},
				{Author: u.Carol, Body: "Just learning! Working through *Effective Go*.", Format: model.BodyFormatMarkdown},
				{Author: u.Alice, Body: "@carol that's a great resource. Ask anything in here.", Format: model.BodyFormatMarkdown},
				{Author: u.Mod, Body: "Same. Big refactor week.", Format: model.BodyFormatPlaintext},
			},
		},
		{
			Sub: s.GeneralDisc, Author: u.Bob,
			Title:  "Favorite keyboard shortcuts you can't live without?",
			Body:   "I'll start: `Ctrl-R` in the terminal. What's yours?",
			Format: model.BodyFormatMarkdown,
			Age:    36 * time.Hour,
			Replies: []replySpec{
				{Author: u.Alice, Body: "`Ctrl-W` to delete words. Saves so much time.", Format: model.BodyFormatMarkdown},
				{Author: u.Dave, Body: "vim's `ciw`!", Format: model.BodyFormatMarkdown},
			},
		},
		{
			Sub: s.GeneralDisc, Author: u.Carol,
			Title:  "Beginner question: when to use a goroutine?",
			Body:   "Reading about concurrency. When is it actually worth using `go` vs just calling the function?",
			Format: model.BodyFormatMarkdown,
			Age:    18 * time.Hour,
			Replies: []replySpec{
				{Author: u.Alice, Body: "Rule of thumb: use one when the work is genuinely independent and you want to do something else while it runs. If you're calling `go f()` and then immediately waiting for the result, you probably don't need it.", Format: model.BodyFormatMarkdown},
			},
		},
		{
			Sub: s.GeneralDisc, Author: u.Mallory,
			Title:  "Hi everyone! New here",
			Body:   "Just joined the forum, looking forward to learning.",
			Format: model.BodyFormatPlaintext,
			Age:    2 * time.Hour,
		},
		{
			Sub: s.GeneralDisc, Author: u.Mod,
			Title:  "Reminder: respect off-topic boundaries",
			Body:   "Quick note that **General Discussion** is for general topics. Tech questions belong in the Tech category. Thanks!",
			Format: model.BodyFormatMarkdown,
			Age:    4 * 24 * time.Hour,
		},
		// ── Software ──────────────────────────────────────────────────────
		{
			Sub: s.Software, Author: u.Alice,
			Title:  "Best practices for structuring a Go web app?",
			Body:   "I've seen `internal/handler`, `internal/server`, `internal/store` layouts — what does your repo look like?",
			Format: model.BodyFormatMarkdown,
			Age:    3 * 24 * time.Hour,
			Replies: []replySpec{
				{Author: u.Bob, Body: "I lean on `cmd/` for entrypoints and put everything else in `internal/`. Keeps the public API surface zero.", Format: model.BodyFormatMarkdown},
				{Author: u.Dave, Body: "Same. The `internal/` convention is great.", Format: model.BodyFormatMarkdown},
				{Author: u.Mod, Body: "+1. Don't over-package early; let things grow into directories.", Format: model.BodyFormatMarkdown},
				{Author: u.Alice, Body: "Good point about not over-packaging. I keep falling into that trap.", Format: model.BodyFormatMarkdown},
			},
		},
		{
			Sub: s.Software, Author: u.Bob, Locked: true,
			Title:  "[LOCKED] SQLite vs Postgres for small forums",
			Body:   "Locked because this gets re-litigated every month. Search the archive first.\n\nShort answer: SQLite is fine until you need concurrent writes from multiple processes.",
			Format: model.BodyFormatMarkdown,
			Age:    10 * 24 * time.Hour,
			Replies: []replySpec{
				{Author: u.Alice, Body: "Agree. Single-node, SQLite. Multi-node or heavy write contention, Postgres.", Format: model.BodyFormatMarkdown},
			},
		},
		{
			Sub: s.Software, Author: u.Carol,
			Title:  "How do I learn HTMX?",
			Body:   "The htmx.org docs are dense. Are there gentler intros?",
			Format: model.BodyFormatMarkdown,
			Age:    12 * time.Hour,
		},
		// ── Hardware ──────────────────────────────────────────────────────
		{
			Sub: s.Hardware, Author: u.Dave,
			Title:  "Anyone using a split keyboard?",
			Body:   "Considering a ZSA Voyager. Worth it?",
			Format: model.BodyFormatMarkdown,
			Age:    20 * time.Hour,
			Replies: []replySpec{
				{Author: u.Alice, Body: "Been on a Moonlander for two years. Shoulders thank me daily.", Format: model.BodyFormatMarkdown},
				{Author: u.Bob, Body: "Wrist pain disappeared within a month. Recommend.", Format: model.BodyFormatMarkdown},
			},
		},
		{
			Sub: s.Hardware, Author: u.Bob,
			Title:  "Old ThinkPad refurb questions",
			Body:   "Picked up a T480 for cheap. Worth upgrading to 32GB RAM, or save the money?",
			Format: model.BodyFormatMarkdown,
			Age:    5 * 24 * time.Hour,
			Replies: []replySpec{
				{Author: u.Dave, Body: "I'd do 32. Browsers eat 8 by themselves.", Format: model.BodyFormatMarkdown},
			},
		},
	}

	// Fill General Discussion with extra throwaway threads so pagination is
	// real (defaultThreadsPerPage is 25; we want ≥30 in this sub).
	for i := 1; i <= 24; i++ {
		specs = append(specs, threadSpec{
			Sub: s.GeneralDisc, Author: u.Alice,
			Title:  fmt.Sprintf("Open thread #%d", i),
			Body:   fmt.Sprintf("Catch-all conversation #%d. Anything goes.", i),
			Format: model.BodyFormatMarkdown,
			Age:    time.Duration(i*6) * time.Hour,
		})
	}

	// One held-for-review post (matches keyword blocklist if admin adds one,
	// otherwise visible only via the held_for_review flag in the DB).
	specs = append(specs, threadSpec{
		Sub: s.GeneralDisc, Author: u.Mallory,
		Title:  "Check out my totally legit affiliate links!",
		Body:   "Hey friends, buy this thing at https://example.com/buy-now and use my referral code SPAMME for 90% off!!!",
		Format: model.BodyFormatPlaintext,
		Age:    30 * time.Minute,
		Replies: []replySpec{
			{Author: u.Bob, Body: "Reported.", Format: model.BodyFormatPlaintext, Held: false},
		},
	})

	var spamThreadOPID int64
	for i, spec := range specs {
		opID, err := createThreadWithReplies(ctx, st, spec)
		if err != nil {
			return fmt.Errorf("thread %d (%q): %w", i, spec.Title, err)
		}
		if spec.Title == "Check out my totally legit affiliate links!" {
			spamThreadOPID = opID
		}
	}

	// Reactions on a couple of posts.
	if err := seedReactions(ctx, st, u); err != nil {
		return fmt.Errorf("seed reactions: %w", err)
	}

	// One open report on the spam OP so the mod queue has something to show.
	if spamThreadOPID != 0 {
		report := &model.Report{
			ReporterID: u.Bob.ID,
			PostID:     spamThreadOPID,
			Reason:     "Looks like affiliate spam.",
			Status:     model.ReportStatusOpen,
		}
		if err := st.CreateReport(ctx, report); err != nil {
			return fmt.Errorf("create report: %w", err)
		}
	}
	return nil
}

func createThreadWithReplies(ctx context.Context, st *sqlitestore.Store, spec threadSpec) (int64, error) {
	createdAt := time.Now().Add(-spec.Age).UTC()

	thread := &model.Thread{
		SubcategoryID: spec.Sub.ID,
		AuthorID:      spec.Author.ID,
		Title:         spec.Title,
		IsPinned:      spec.Pinned,
		IsLocked:      spec.Locked,
		LastPostAt:    createdAt,
		LastPostBy:    &spec.Author.ID,
	}
	if err := st.CreateThread(ctx, thread); err != nil {
		return 0, fmt.Errorf("create thread: %w", err)
	}

	op := &model.Post{
		ThreadID: thread.ID, AuthorID: spec.Author.ID,
		Body: spec.Body, BodyFormat: spec.Format,
	}
	if err := st.CreatePost(ctx, op); err != nil {
		return 0, fmt.Errorf("create OP post: %w", err)
	}

	// Spread replies evenly between the OP timestamp and "now" so the most
	// recent reply is always slightly older than wall-clock time. Without the
	// clamp, short-Age threads with replies end up with last_post_at in the
	// future, which pushes them above newly-created threads in the sort
	// order — a confusing failure mode when you're testing the new-thread
	// flow against seeded data.
	now := time.Now().UTC()
	replyCount := len(spec.Replies)
	for i, r := range spec.Replies {
		reply := &model.Post{
			ThreadID: thread.ID, AuthorID: r.Author.ID,
			Body: r.Body, BodyFormat: r.Format, HeldForReview: r.Held,
		}
		if err := st.CreatePost(ctx, reply); err != nil {
			return 0, fmt.Errorf("create reply: %w", err)
		}
		// Evenly space replies across (createdAt, now], so the Nth reply
		// lands at createdAt + N * (now - createdAt) / (replyCount + 1).
		offset := time.Duration(i+1) * (now.Sub(createdAt)) / time.Duration(replyCount+1)
		replyAt := createdAt.Add(offset)
		if replyAt.After(now) {
			replyAt = now
		}
		if err := st.UpdateLastPost(ctx, thread.ID, r.Author.ID, replyAt); err != nil {
			return 0, fmt.Errorf("update last post: %w", err)
		}
	}
	return op.ID, nil
}

func seedReactions(ctx context.Context, st *sqlitestore.Store, u *seedUsers_) error {
	// React to the first few posts so reaction counts are visible in the UI.
	// PostIDs are monotonic from 1: OP of the welcome thread is #1.
	reactions := []struct {
		PostID int64
		User   *model.User
	}{
		{1, u.Alice},
		{1, u.Bob},
		{1, u.Carol},
		{1, u.Dave},
		{2, u.Bob}, // first reply on welcome
		{4, u.Alice},
		{4, u.Mod},
	}
	for _, r := range reactions {
		if _, err := st.ToggleReaction(ctx, r.PostID, r.User.ID, model.ReactionTypeLike); err != nil {
			return fmt.Errorf("react post %d as %s: %w", r.PostID, r.User.Username, err)
		}
	}
	return nil
}

// ── Utilities ────────────────────────────────────────────────────────────────

func dataExists(ctx context.Context, st *sqlitestore.Store) (bool, error) {
	// Any pre-existing user count > 0 means we'd be writing into a live DB.
	if _, err := st.GetUserByID(ctx, 1); err == nil {
		return true, nil
	} else if errors.Is(err, store.ErrNotFound) {
		return false, nil
	} else {
		return false, fmt.Errorf("probe existing data: %w", err)
	}
}

// wipe removes the SQLite file plus its WAL/SHM sidecars. This is the simplest
// way to get a truly clean schema without dancing around foreign-key drops.
func wipe(dbPath string) error {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		path := dbPath + suffix
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", path, err)
		}
	}
	return nil
}

func parentDir(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[:i]
		}
	}
	return "."
}

func printSummary(u *seedUsers_) {
	fmt.Println()
	fmt.Println("Seed complete. Demo accounts (password for all: " + demoPassword + "):")
	fmt.Println()
	fmt.Println("  Role        Email                    Notes")
	fmt.Println("  ----        -----                    -----")
	fmt.Printf("  admin       %-24s full site control, can create categories\n", u.Admin.Email)
	fmt.Printf("  moderator   %-24s mod queue + ban/lock/pin\n", u.Mod.Email)
	fmt.Printf("  member      %-24s established (50 posts, verified)\n", u.Alice.Email)
	fmt.Printf("  member      %-24s mid-tenure (15 posts)\n", u.Bob.Email)
	fmt.Printf("  member      %-24s new (5 posts — link-limited)\n", u.Carol.Email)
	fmt.Printf("  member      %-24s very new (unverified email banner)\n", u.Dave.Email)
	fmt.Printf("  member      %-24s BANNED — login should be blocked\n", u.Eve.Email)
	fmt.Printf("  member      %-24s 0 posts (would hit CAPTCHA if enabled)\n", u.Mallory.Email)
	fmt.Println()
	fmt.Println("Start the server with `go run ./cmd/forum` and visit http://localhost:8080.")
}
