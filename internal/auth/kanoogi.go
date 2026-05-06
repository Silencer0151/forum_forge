package auth

import (
	"context"
	"net/http"

	"github.com/nitro/forum_forge/internal/model"
)

// KanoogiConfig configures the Kanoogi (integrated-mode) auth adapter. All
// fields are wired from the FORUM_AUTH_* env vars in cmd/forum/main.go once
// the integration is fully implemented; until then the adapter ships as a
// stub that returns ErrNotImplemented from every operation.
//
// TODO: Implement when Kanoogi API details are known.
//   - SessionCookie: name of the cookie Kanoogi sets on its login domain.
//   - SessionHeader: optional header name for service-to-service auth.
//   - APIBaseURL: base URL of the Kanoogi identity API.
//   - APIKey: shared secret for forum→Kanoogi API calls.
//   - HTTPClient: client used to talk to the Kanoogi API; tests inject a fake.
type KanoogiConfig struct {
	SessionCookie string
	SessionHeader string
	APIBaseURL    string
	APIKey        string
	HTTPClient    *http.Client
}

// KanoogiUserStore is the slice of store.Store the adapter needs for
// auto-provisioning local user rows when an authenticated request arrives
// for an unknown external ID (spec.md §4.2). Defining a narrow interface
// here keeps the adapter testable without a full Store fake.
type KanoogiUserStore interface {
	GetUserByExternalID(ctx context.Context, externalID, source string) (*model.User, error)
	CreateUser(ctx context.Context, u *model.User) error
	UpdateUser(ctx context.Context, u *model.User) error
}

// KanoogiAdapter implements AuthAdapter and Authenticator against an external
// Kanoogi identity provider. Per task 3.3, this is a compile-only stub: the
// surface is exhaustive so the rest of the forum can wire against it now,
// but every operation returns ErrNotImplemented until the Kanoogi API
// contract is finalized.
type KanoogiAdapter struct {
	cfg   KanoogiConfig
	store KanoogiUserStore
}

// NewKanoogi builds a stub Kanoogi adapter. Callers must supply a config
// (even if mostly empty) and a store; nil values would only make the
// inevitable production switch-over harder to spot.
func NewKanoogi(cfg KanoogiConfig, st KanoogiUserStore) *KanoogiAdapter {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = http.DefaultClient
	}
	return &KanoogiAdapter{cfg: cfg, store: st}
}

// Authenticate satisfies AuthAdapter. Production behavior:
//  1. Read the Kanoogi session cookie (or service header) from r.
//  2. POST it to {APIBaseURL}/auth/validate with the shared API key.
//  3. Translate the JSON response into an ExternalUser.
//
// TODO: Implement when Kanoogi API details are known.
func (a *KanoogiAdapter) Authenticate(r *http.Request) (*ExternalUser, error) {
	// TODO: Implement when Kanoogi API details are known. The shape will be:
	//   raw := readCookieOrHeader(r, a.cfg.SessionCookie, a.cfg.SessionHeader)
	//   if raw == "" { return nil, nil }
	//   resp, err := a.callValidate(r.Context(), raw)
	//   if err != nil { return nil, err }
	//   return resp.ToExternalUser(), nil
	_ = r
	return nil, ErrNotImplemented
}

// AuthenticateRequest satisfies Authenticator. It will call Authenticate and
// then auto-provision a local *model.User row keyed by ExternalID +
// SourceKanoogi (spec.md §4.2). Sessions are owned by Kanoogi, so the
// returned *model.Session is always nil — the forum doesn't need its own
// row when the upstream platform is the source of truth.
//
// TODO: Implement when Kanoogi API details are known.
func (a *KanoogiAdapter) AuthenticateRequest(r *http.Request) (*model.User, *model.Session, error) {
	ext, err := a.Authenticate(r)
	if err != nil {
		return nil, nil, err
	}
	if ext == nil {
		return nil, nil, nil
	}
	// TODO: Implement when Kanoogi API details are known. The shape will be:
	//   user, err := a.store.GetUserByExternalID(r.Context(), ext.ExternalID, SourceKanoogi)
	//   if errors.Is(err, store.ErrNotFound) {
	//       user = &model.User{
	//           Username:       ext.Username,
	//           DisplayName:    ext.DisplayName,
	//           AvatarURL:      ext.AvatarURL,
	//           Email:          ext.Email,
	//           Role:           model.RoleMember,
	//           ExternalID:     ext.ExternalID,
	//           ExternalSource: SourceKanoogi,
	//       }
	//       if err := a.store.CreateUser(r.Context(), user); err != nil {
	//           return nil, nil, fmt.Errorf("auto-provision: %w", err)
	//       }
	//   } else if err != nil {
	//       return nil, nil, err
	//   }
	//   return user, nil, nil
	return nil, nil, ErrNotImplemented
}

// GetUser satisfies AuthAdapter. Production behavior fetches the user from
// the Kanoogi identity API (with a short TTL cache so profile views don't
// stampede the upstream service).
//
// TODO: Implement when Kanoogi API details are known.
func (a *KanoogiAdapter) GetUser(externalID string) (*ExternalUser, error) {
	_ = externalID
	return nil, ErrNotImplemented
}

// GetAvatarURL satisfies AuthAdapter. May either compose a URL by template
// (e.g., "https://cdn.kanoogi.example/avatars/{id}.png") or call the API;
// the choice depends on Kanoogi's hosting model.
//
// TODO: Implement when Kanoogi API details are known.
func (a *KanoogiAdapter) GetAvatarURL(externalID string) (string, error) {
	_ = externalID
	return "", ErrNotImplemented
}

// _ asserts at compile time that *KanoogiAdapter satisfies both interfaces.
// If the spec interface signatures shift, the missing-method error here
// surfaces the divergence before tests run.
var (
	_ AuthAdapter   = (*KanoogiAdapter)(nil)
	_ Authenticator = (*KanoogiAdapter)(nil)
)

// Compile-time check that the standalone Service still satisfies both
// interfaces. Lives in this file (rather than standalone.go) so a single
// grep finds every adapter implementation.
var (
	_ AuthAdapter   = (*Service)(nil)
	_ Authenticator = (*Service)(nil)
)

