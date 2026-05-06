package auth

import (
	"errors"
	"net/http"

	"github.com/nitro/forum_forge/internal/model"
)

// Authentication source identifiers stored in model.User.ExternalSource. The
// standalone adapter tags users it manages with SourceStandalone so the
// integrated mode (Kanoogi) can keep its own auto-provisioned rows separate.
const (
	SourceStandalone = "standalone"
	SourceKanoogi    = "kanoogi"
)

// ExternalUser is the platform-agnostic user representation returned by an
// AuthAdapter (spec.md §4.2). Fields are populated from the adapter's source
// of truth: the local user record for standalone, or the upstream identity
// provider for integrated mode.
type ExternalUser struct {
	ExternalID  string
	Username    string
	DisplayName string
	AvatarURL   string
	Email       string
}

// AuthAdapter is the interface defined in spec.md §4.2. It abstracts the
// user-identity layer so the rest of the forum (handlers, templates) stays
// the same regardless of whether authentication is handled in-process
// (standalone) or by an external platform such as Kanoogi (integrated).
type AuthAdapter interface {
	// Authenticate validates an incoming request and returns the external
	// user info if authenticated. Returns (nil, nil) for guests.
	// Implementations may return ErrSessionInvalid to signal the caller
	// that the request carries stale auth state which should be cleared.
	Authenticate(r *http.Request) (*ExternalUser, error)

	// GetUser fetches user info by external platform ID. Returns
	// store.ErrNotFound when the user does not exist.
	GetUser(externalID string) (*ExternalUser, error)

	// GetAvatarURL returns the avatar URL for a given external user.
	GetAvatarURL(externalID string) (string, error)
}

// Authenticator extends AuthAdapter with the request-scoped resolution the
// auth middleware needs: turning the request into a fully-loaded local
// *model.User and, when applicable, the active *model.Session record. The
// spec interface is preserved verbatim; this extension exists so the
// middleware can populate the request context without each adapter having
// to spell out a UserStore lookup at every call site.
//
// Both shipping adapters (StandaloneAdapter, KanoogiAdapter) implement this.
type Authenticator interface {
	AuthAdapter

	// AuthenticateRequest resolves the request's auth state into a local
	// user (auto-provisioning when applicable) and the matching session.
	// Returns (nil, nil, nil) for guests. Returns ErrSessionInvalid when
	// the request carries stale state (expired session, banned user,
	// missing local row); the middleware clears the session cookie in
	// that case and treats the request as a guest.
	AuthenticateRequest(r *http.Request) (*model.User, *model.Session, error)
}

// ErrNotImplemented is returned by adapter operations that need integration-
// specific configuration (the upstream API endpoint, cookie name, signing
// secret) which has not been wired up yet. The Kanoogi stub returns this
// from every operation; production deployments must implement them before
// switching FORUM_AUTH_MODE=integrated.
var ErrNotImplemented = errors.New("auth: adapter operation not implemented")

// ErrSessionInvalid signals to the middleware that the request carries
// auth state which should be cleared (expired session, banned user, missing
// local row). The middleware turns this into a guest passthrough plus a
// past-dated Set-Cookie header.
var ErrSessionInvalid = errors.New("auth: session invalid")
