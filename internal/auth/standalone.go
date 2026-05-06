// Package auth implements the forum's authentication primitives. The standalone
// service in this file is the spec.md §4.1 email+password mode: bcrypt password
// hashing, server-side sessions stored in the SessionStore, and a handful of
// validation helpers shared by the HTTP handler layer.
//
// Future tasks add the AuthAdapter interface (3.3) so this can sit behind a
// common interface alongside Kanoogi's session-bridging implementation.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/nitro/forum_forge/internal/model"
	"github.com/nitro/forum_forge/internal/store"
)

const (
	// SessionLifetime matches the 30-day cookie MaxAge in spec.md §4.1.
	SessionLifetime = 30 * 24 * time.Hour

	// MinPasswordLength enforces the spec's "≥8 chars" rule on registration
	// and password reset. Login does not enforce this so existing users
	// with shorter legacy passwords could still sign in if the rule
	// changes — the spec only mandates the floor at write time.
	MinPasswordLength = 8

	// BcryptCost is the work factor for password hashing. 12 is the spec's
	// recommended default; tests override it via NewWithCost to keep
	// hashing under a hundred milliseconds.
	BcryptCost = 12

	sessionTokenBytes = 32
)

// Errors returned by the Service. Handlers map these to flash messages and
// HTTP status codes; nothing in this package should leak internal error text
// to end users.
var (
	ErrInvalidCredentials = errors.New("auth: invalid email or password")
	ErrUserBanned         = errors.New("auth: user is banned")
	ErrEmailTaken         = errors.New("auth: email is already registered")
	ErrUsernameTaken      = errors.New("auth: username is already taken")
)

// SessionStore is the slice of store.Store the standalone service needs.
// Defining it here (rather than depending on the full store.Store) keeps the
// package self-contained and makes tests easy to wire with a fake.
type SessionStore interface {
	CreateUser(ctx context.Context, u *model.User) error
	GetUserByEmail(ctx context.Context, email string) (*model.User, error)
	GetUserByUsername(ctx context.Context, username string) (*model.User, error)
	UpdateUser(ctx context.Context, u *model.User) error

	CreateSession(ctx context.Context, s *model.Session) error
	DeleteSession(ctx context.Context, id string) error
	DeleteUserSessions(ctx context.Context, userID int64) error
}

// Service implements standalone (email+password) authentication.
type Service struct {
	store      SessionStore
	bcryptCost int
}

// New returns a Service backed by the given store, using the default bcrypt cost.
func New(s SessionStore) *Service {
	return NewWithCost(s, BcryptCost)
}

// NewWithCost is the same as New but allows tests to lower bcrypt cost so the
// suite stays fast. Production callers should use New.
func NewWithCost(s SessionStore, cost int) *Service {
	return &Service{store: s, bcryptCost: cost}
}

// RegisterInput is the validated form payload for creating a new account.
type RegisterInput struct {
	Username string
	Email    string
	Password string
}

// LoginInput is the validated form payload for signing in.
type LoginInput struct {
	Email    string
	Password string
}

// Result bundles the user record and freshly created session that the handler
// turns into a Set-Cookie response.
type Result struct {
	User    *model.User
	Session *model.Session
}

// FieldErrors maps form-field name → human-readable validation message.
// A nil/empty map means validation passed.
type FieldErrors map[string]string

// Has reports whether any field error is set.
func (f FieldErrors) Has() bool { return len(f) > 0 }

// Error makes FieldErrors satisfy the error interface so handlers can return
// it directly. The message is intentionally generic — handlers should render
// the per-field messages from the map.
func (f FieldErrors) Error() string {
	if len(f) == 0 {
		return ""
	}
	parts := make([]string, 0, len(f))
	for k, v := range f {
		parts = append(parts, k+": "+v)
	}
	return "validation: " + strings.Join(parts, "; ")
}

// usernameRe enforces spec.md §2.1: 3–32 chars, alphanumeric + underscore.
var usernameRe = regexp.MustCompile(`^[A-Za-z0-9_]{3,32}$`)

// ValidateRegister returns per-field errors for in. The returned map is nil
// when input is valid. Handlers call this before hitting the store so the
// form can be re-rendered with messages and the user's previous values.
func ValidateRegister(in RegisterInput) FieldErrors {
	errs := FieldErrors{}

	if in.Username == "" {
		errs["username"] = "Username is required"
	} else if !usernameRe.MatchString(in.Username) {
		errs["username"] = "Username must be 3–32 characters, letters, numbers, or underscore"
	}

	if in.Email == "" {
		errs["email"] = "Email is required"
	} else if _, err := mail.ParseAddress(in.Email); err != nil {
		errs["email"] = "Email address is not valid"
	}

	if len(in.Password) < MinPasswordLength {
		errs["password"] = fmt.Sprintf("Password must be at least %d characters", MinPasswordLength)
	}

	if len(errs) == 0 {
		return nil
	}
	return errs
}

// Register creates the user account, hashes the password, and opens a session.
// Returns ErrEmailTaken or ErrUsernameTaken when uniqueness checks fail; the
// store-level UNIQUE constraint is a backstop for races between the existence
// check and the INSERT.
func (s *Service) Register(ctx context.Context, in RegisterInput) (*Result, error) {
	in.Username = strings.TrimSpace(in.Username)
	in.Email = strings.TrimSpace(strings.ToLower(in.Email))

	if errs := ValidateRegister(in); errs.Has() {
		return nil, errs
	}

	if _, err := s.store.GetUserByEmail(ctx, in.Email); err == nil {
		return nil, ErrEmailTaken
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, fmt.Errorf("lookup email: %w", err)
	}
	if _, err := s.store.GetUserByUsername(ctx, in.Username); err == nil {
		return nil, ErrUsernameTaken
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, fmt.Errorf("lookup username: %w", err)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), s.bcryptCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	u := &model.User{
		Username:     in.Username,
		Email:        in.Email,
		PasswordHash: string(hash),
		Role:         model.RoleMember,
	}
	if err := s.store.CreateUser(ctx, u); err != nil {
		// The unique-index violation surfaces here when the existence
		// check above raced with another request. Translate it back to
		// the well-known sentinels rather than leaking the SQL error.
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "users.email") {
			return nil, ErrEmailTaken
		}
		if strings.Contains(msg, "users.username") {
			return nil, ErrUsernameTaken
		}
		return nil, fmt.Errorf("create user: %w", err)
	}

	sess, err := s.openSession(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	return &Result{User: u, Session: sess}, nil
}

// Login looks up the user by email, verifies the password, and opens a session.
// Returns ErrInvalidCredentials for both unknown users and bad passwords so an
// attacker cannot enumerate valid email addresses.
func (s *Service) Login(ctx context.Context, in LoginInput) (*Result, error) {
	in.Email = strings.TrimSpace(strings.ToLower(in.Email))
	if in.Email == "" || in.Password == "" {
		return nil, ErrInvalidCredentials
	}

	u, err := s.store.GetUserByEmail(ctx, in.Email)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// Run a dummy bcrypt comparison so the response time for
			// "unknown user" matches "wrong password". Without this,
			// timing reveals which emails are registered.
			_ = bcrypt.CompareHashAndPassword(
				[]byte("$2a$12$00000000000000000000000000000000000000000000000000000"),
				[]byte(in.Password))
			return nil, ErrInvalidCredentials
		}
		return nil, fmt.Errorf("lookup user: %w", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(in.Password)); err != nil {
		return nil, ErrInvalidCredentials
	}

	if u.Banned {
		return nil, ErrUserBanned
	}

	sess, err := s.openSession(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	return &Result{User: u, Session: sess}, nil
}

// HashPassword runs bcrypt at the service's configured cost and returns the
// resulting hash. Exported so the password-reset handler (Task 3.2) can reuse
// the same cost as registration without poking at internal state.
func (s *Service) HashPassword(raw string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(raw), s.bcryptCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(hash), nil
}

// Logout deletes the session record. A missing session is not an error: the
// user is already logged out from the server's point of view, and the handler
// will clear the cookie unconditionally.
func (s *Service) Logout(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return nil
	}
	return s.store.DeleteSession(ctx, sessionID)
}

// openSession generates a random session ID, persists the row, and returns it.
func (s *Service) openSession(ctx context.Context, userID int64) (*model.Session, error) {
	id, err := newSessionID()
	if err != nil {
		return nil, fmt.Errorf("generate session id: %w", err)
	}
	sess := &model.Session{
		ID:        id,
		UserID:    userID,
		ExpiresAt: time.Now().Add(SessionLifetime),
	}
	if err := s.store.CreateSession(ctx, sess); err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	return sess, nil
}

func newSessionID() (string, error) {
	b := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
