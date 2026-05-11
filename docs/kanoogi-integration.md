# Kanoogi Integration Guide

This guide covers the concrete steps to embed Forum Forge into Kanoogi (or any existing Go web application). It expands on Appendix B of `spec.md` with working code examples for the auth adapter, CSS overrides, and route mounting.

The forum is designed for this use case: the `AuthAdapter` interface, `--ff-*` CSS custom properties, and the subpath-mount pattern all exist specifically to make this integration clean.

---

## Overview

There are three ways to run the forum alongside Kanoogi. They differ in operational complexity and coupling:

| Approach | TLS | Auth | Coupling |
|---|---|---|---|
| **Separate service behind proxy** | Kanoogi's proxy terminates TLS, routes `/forum/*` to forum service | Adapter calls Kanoogi API to validate session | Low — processes are independent |
| **Library import** | Entirely Kanoogi's concern | Adapter reads session from request context | High — forum is a Go package |
| **Separate domain** | Forum manages its own TLS (autocert) | Adapter calls Kanoogi API; cross-domain cookies | None — fully decoupled |

The **separate service behind proxy** approach is recommended for a first integration: it requires no changes to Kanoogi's Go code, decouples deployment, and the auth adapter calling the Kanoogi API is a well-defined boundary.

---

## Step 1: Implement the Auth Adapter

The forum defines the interface in `internal/auth/adapter.go`. A stub Kanoogi implementation lives in `internal/auth/kanoogi.go` — all methods return `ErrNotImplemented`. Your task is to fill them in.

### The interface

```go
type AuthAdapter interface {
    // Authenticate returns the logged-in user for this request, or nil for guests.
    Authenticate(r *http.Request) (*ExternalUser, error)

    // GetUser fetches a user by Kanoogi user ID.
    GetUser(externalID string) (*ExternalUser, error)

    // GetAvatarURL returns the avatar URL for a Kanoogi user.
    GetAvatarURL(externalID string) (string, error)
}

type ExternalUser struct {
    ExternalID  string // Kanoogi's internal user ID
    Username    string
    DisplayName string
    AvatarURL   string
    Email       string
}
```

### KanoogiConfig

The adapter is configured via `KanoogiConfig` (see `internal/auth/kanoogi.go`):

```go
type KanoogiConfig struct {
    SessionCookie string     // name of the cookie Kanoogi sets, e.g. "kanoogi_session"
    SessionHeader string     // optional service-to-service header, e.g. "X-Kanoogi-Token"
    APIBaseURL    string     // e.g. "https://api.kanoogi.com"
    APIKey        string     // shared secret for forum→Kanoogi calls
    HTTPClient    *http.Client
}
```

### Implementing Authenticate

The `Authenticate` method reads the session cookie (or header), sends it to the Kanoogi validation endpoint, and returns the user.

```go
func (a *KanoogiAdapter) Authenticate(r *http.Request) (*auth.ExternalUser, error) {
    raw := ""
    if c, err := r.Cookie(a.cfg.SessionCookie); err == nil {
        raw = c.Value
    }
    if raw == "" {
        raw = r.Header.Get(a.cfg.SessionHeader)
    }
    if raw == "" {
        return nil, nil // guest
    }

    resp, err := a.callValidate(r.Context(), raw)
    if err != nil {
        return nil, fmt.Errorf("kanoogi validate: %w", err)
    }
    return &auth.ExternalUser{
        ExternalID:  resp.UserID,
        Username:    resp.Username,
        DisplayName: resp.DisplayName,
        AvatarURL:   resp.AvatarURL,
        Email:       resp.Email,
    }, nil
}

type validateResponse struct {
    UserID      string `json:"user_id"`
    Username    string `json:"username"`
    DisplayName string `json:"display_name"`
    AvatarURL   string `json:"avatar_url"`
    Email       string `json:"email"`
}

func (a *KanoogiAdapter) callValidate(ctx context.Context, token string) (*validateResponse, error) {
    req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
        a.cfg.APIBaseURL+"/auth/validate", nil)
    req.Header.Set("Authorization", "Bearer "+a.cfg.APIKey)
    req.Header.Set("X-Session-Token", token)

    res, err := a.cfg.HTTPClient.Do(req)
    if err != nil {
        return nil, err
    }
    defer res.Body.Close()

    if res.StatusCode == http.StatusUnauthorized {
        return nil, auth.ErrSessionInvalid // triggers session cookie clear
    }
    if res.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("unexpected status %d", res.StatusCode)
    }

    var out validateResponse
    if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
        return nil, err
    }
    return &out, nil
}
```

### Implementing AuthenticateRequest (auto-provisioning)

The auth middleware calls `AuthenticateRequest` rather than `Authenticate` so it can get a fully-loaded local `*model.User`. The implementation should:

1. Call `Authenticate` to get the Kanoogi user.
2. Look up the local user row by `(external_id, external_source="kanoogi")`.
3. If not found, create one (`role = member`).
4. Return the local user.

```go
func (a *KanoogiAdapter) AuthenticateRequest(r *http.Request) (*model.User, *model.Session, error) {
    ext, err := a.Authenticate(r)
    if err != nil {
        return nil, nil, err
    }
    if ext == nil {
        return nil, nil, nil // guest
    }

    user, err := a.store.GetUserByExternalID(r.Context(), ext.ExternalID, auth.SourceKanoogi)
    if errors.Is(err, store.ErrNotFound) {
        user = &model.User{
            Username:       ext.Username,
            DisplayName:    ext.DisplayName,
            AvatarURL:      ext.AvatarURL,
            Email:          ext.Email,
            Role:           model.RoleMember,
            ExternalID:     ext.ExternalID,
            ExternalSource: auth.SourceKanoogi,
        }
        if err := a.store.CreateUser(r.Context(), user); err != nil {
            return nil, nil, fmt.Errorf("auto-provision: %w", err)
        }
    } else if err != nil {
        return nil, nil, err
    }

    // Sessions are owned by Kanoogi; no local session row needed.
    return user, nil, nil
}
```

### Wiring the adapter

In `cmd/forum/main.go`, the adapter is selected based on `FORUM_AUTH_MODE`:

```go
var authenticator auth.Authenticator
switch cfg.AuthMode {
case "integrated":
    authenticator = auth.NewKanoogi(auth.KanoogiConfig{
        SessionCookie: os.Getenv("KANOOGI_SESSION_COOKIE"),
        APIBaseURL:    os.Getenv("KANOOGI_API_BASE_URL"),
        APIKey:        os.Getenv("KANOOGI_API_KEY"),
    }, sqliteStore)
default:
    authenticator = standaloneService
}
```

Set these additional environment variables in your deployment:

```sh
FORUM_AUTH_MODE=integrated
KANOOGI_SESSION_COOKIE=kanoogi_session
KANOOGI_API_BASE_URL=https://api.kanoogi.com
KANOOGI_API_KEY=<shared secret>
```

---

## Step 2: Override the CSS Theme

The forum's entire colour palette is expressed as CSS custom properties with the `--ff-` prefix, declared in `static/css/style.css`. Kanoogi integration would override these to match Kanoogi's exact palette.

### Method A: Host platform injects an override stylesheet

Add a `<link>` after the forum's own stylesheet in Kanoogi's layout (or inject via the reverse proxy):

```html
<link rel="stylesheet" href="/static/css/style.css">
<link rel="stylesheet" href="/static/css/kanoogi-theme.css">
```

`kanoogi-theme.css`:

```css
:root {
    /* Exact values observed from kanoogi.com — adjust once design system is confirmed */
    --ff-bg-primary:      #1a1125;   /* deep purple-black background */
    --ff-bg-secondary:    #241934;   /* slightly lighter card/panel background */
    --ff-bg-tertiary:     #2e2240;   /* hover/selected states */

    --ff-text-primary:    #e8e0f0;   /* main body text */
    --ff-text-secondary:  #a090b8;   /* muted labels, metadata */
    --ff-text-muted:      #6a5a80;   /* timestamps, de-emphasised content */

    --ff-accent:          #d4940a;   /* orange/gold — headings, active links, CTAs */
    --ff-accent-hover:    #f0aa12;   /* brighter on hover */
    --ff-accent-dim:      #8a6008;   /* pressed/disabled state */

    --ff-border:          #3a2a50;   /* card and table borders */
    --ff-border-subtle:   #2a1e3a;   /* very subtle separators */

    --ff-avatar-radius:   6px;       /* slightly rounded square, matching Kanoogi comments */

    --ff-font-sans: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
    --ff-font-mono: "SFMono-Regular", Consolas, "Liberation Mono", monospace;
}
```

### Method B: Library import (custom template base)

If the forum is imported as a Go package, replace `templates/layout/base.html` with a Kanoogi-specific base template that includes Kanoogi's own `<head>` (fonts, global CSS, analytics) before the forum's stylesheet.

---

## Step 3: Mount Forum Routes

### Separate service behind a proxy

Run the forum on an internal port (`FORUM_LISTEN_ADDR=:8081`) with `FORUM_TLS_MODE=none` and `FORUM_TRUST_PROXY=true`. Proxy `/forum/` to it:

```nginx
location /forum/ {
    proxy_pass         http://127.0.0.1:8081/forum/;
    proxy_set_header   Host              $host;
    proxy_set_header   X-Forwarded-For   $proxy_add_x_forwarded_for;
    proxy_set_header   X-Forwarded-Proto $scheme;
}
```

Set `FORUM_BASE_URL=https://kanoogi.com/forum` so all generated links and redirects use the correct root.

### Library import (Go module)

The forum's `Server` is an `http.Handler`. Mount it under a prefix in Kanoogi's router:

```go
import "github.com/nitro/forum_forge/internal/server"

forumServer, err := server.New(forumCfg, forumStore, forumRenderer, authDeps)
if err != nil {
    log.Fatal(err)
}

// Mount under /forum/ in Kanoogi's mux
http.Handle("/forum/", http.StripPrefix("/forum", forumServer))
```

The forum's own router already has all routes registered relative to its root, so `http.StripPrefix` is sufficient.

---

## Step 4: Add "Forum" to Kanoogi's Navigation

Add a `Forum` entry to Kanoogi's horizontal nav bar alongside Home, About, Join Beta, Updates, and Library. The link target is the forum root (`/forum/` or `https://forum.kanoogi.com/`).

If using integrated mode, the nav item can highlight the logged-in user's unread-thread count once the forum exposes a lightweight count API endpoint.

---

## Step 5: Map Existing Kanoogi Users

If Kanoogi already has registered users, they will be auto-provisioned in the forum's local database the first time they make a request in integrated mode (see `AuthenticateRequest` above). No pre-migration step is required.

If you want to pre-populate the forum users table (for example to assign moderator roles before launch), run a one-off script against the forum's SQLite database:

```sql
INSERT INTO users (username, display_name, email, role, external_id, external_source, created_at, updated_at)
SELECT
    k.username,
    k.display_name,
    k.email,
    CASE WHEN k.is_admin THEN 'admin' ELSE 'member' END,
    k.id,
    'kanoogi',
    CURRENT_TIMESTAMP,
    CURRENT_TIMESTAMP
FROM kanoogi_users k
ON CONFLICT (external_id, external_source) DO NOTHING;
```

---

## Step 6: Configure Avatar Sync

Implement `GetAvatarURL` in the Kanoogi adapter to return the URL for a user's avatar on Kanoogi's CDN. The simplest approach is a URL template:

```go
func (a *KanoogiAdapter) GetAvatarURL(externalID string) (string, error) {
    return fmt.Sprintf("https://cdn.kanoogi.com/avatars/%s.png", externalID), nil
}
```

If Kanoogi uses signed URLs or a more complex addressing scheme, call the Kanoogi API here instead.

The forum stores the avatar URL returned by `AuthenticateRequest` in the local user row and updates it on each login, so avatars stay current without a separate sync job.

---

## Step 7 (Optional): Migrate Existing Comments

If Kanoogi's existing blog-post comment threads should become forum threads, write a one-off migration script:

1. Create a dedicated subcategory (e.g. "Kanoogi Updates") in the forum.
2. For each Kanoogi blog post that has comments:
   - Insert a thread with the blog post title, `user_id` of the original author (or an admin bot account).
   - Insert posts for each comment, mapping `kanoogi_user_id` → forum `user_id` via the `external_id` column.
3. Set the `created_at` timestamps to the original comment timestamps to preserve chronological order.

This is a one-time operation; after migration, the forum is the source of truth for discussion.

---

## Integration Checklist

- [ ] `Authenticate` implemented and validated against real Kanoogi session tokens
- [ ] `AuthenticateRequest` auto-provisions local user rows correctly
- [ ] `GetAvatarURL` returns valid avatar URLs
- [ ] `FORUM_AUTH_MODE=integrated` and Kanoogi env vars set in production
- [ ] CSS override stylesheet deployed and `--ff-*` values match Kanoogi's palette
- [ ] Forum routes proxied or mounted under the correct subpath
- [ ] `FORUM_BASE_URL` set to the full public URL (including subpath if applicable)
- [ ] "Forum" link added to Kanoogi's navigation bar
- [ ] Existing Kanoogi users pre-provisioned (or confirmed to auto-provision on first visit)
- [ ] Avatar sync confirmed working (avatars visible in forum posts)
- [ ] SMTP configured so forum notification emails use Kanoogi's sending domain
- [ ] Rate limits reviewed for Kanoogi's user volume (adjust via admin settings panel)
