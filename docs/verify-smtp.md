# SMTP Smoke-Test Checklist

End-to-end verification of the email/token pipeline. Before this, the only signal that registration and password reset "work" has been `INFO msg="auth: email (smtp disabled, logged to stdout)"` lines in the server log. This walks you through wiring real SMTP, exercising every email-bearing endpoint, and validating that tokens behave correctly (single-use, expiring, scoped to one user).

## Setup — Mailpit on Windows (no Docker)

[Mailpit](https://mailpit.axllent.org) is a single-binary SMTP receiver with a web UI showing every captured email. It does not deliver mail to the real internet — it just receives, parses, and lets you read it.

1. Grab the latest Windows binary from https://github.com/axllent/mailpit/releases (the `mailpit-windows-amd64.zip` asset).
2. Extract `mailpit.exe` somewhere on PATH or into the repo root.
3. Open a second PowerShell window and run:

   ```powershell
   .\mailpit.exe
   ```

   Defaults: SMTP listens on `:1025`, web UI on http://localhost:8025. No auth required. Leave this window open for the duration of the test.

## Forum SMTP configuration

In the PowerShell window where you run the forum, set the SMTP env vars before launching:

```powershell
$env:FORUM_SMTP_HOST = "localhost"
$env:FORUM_SMTP_PORT = "1025"
$env:FORUM_SMTP_FROM = "noreply@forum.test"
# Leave FORUM_SMTP_USER/PASS unset — Mailpit accepts unauthenticated SMTP.

# Optional: ensure verify/reset links point at the right base URL.
$env:FORUM_BASE_URL = "http://localhost:8080"

go run ./cmd/forum
```

You should NOT see `WARN SMTP not configured` in the startup log anymore. If you do, the env vars didn't reach the process — check spelling and that you ran `go run` from the same PowerShell session.

> **Note on `net/smtp`**: Go's standard library refuses PLAIN auth on plaintext connections. Mailpit doesn't require auth, so we leave `FORUM_SMTP_USER` empty — the forum then skips auth entirely ([email.go:136-138](internal/auth/email.go#L136-L138)). If you later test against a real provider (Gmail, SendGrid), the user/pass must be set AND the server must offer STARTTLS, or auth will fail.

---

## 1. Registration → verify email

Visit http://localhost:8080/auth/register, register a new account `smtp-tester@forum.test` (password ≥8 chars).

- [ ] After registration, server log shows `INFO msg="http" method=POST path=/auth/register status=303` (no "smtp disabled" line).
- [ ] Mailpit UI (http://localhost:8025) shows **1 message** addressed to `smtp-tester@forum.test`.
- [ ] Subject reads `Verify your ForumForge email`.
- [ ] From address is `noreply@forum.test` (matches `FORUM_SMTP_FROM`).
- [ ] Body greets you by username and contains a link `http://localhost:8080/auth/verify?token=...`.
- [ ] **Raw source tab** in Mailpit: `Content-Type: text/html; charset="UTF-8"` is present; no malformed headers.

## 2. Click the verify link

Open the verify URL from the captured email in your browser (must be in the session of the registered user, so use the same browser that has the session cookie — or copy-paste into a new tab; it should still work because the cookie is browser-wide).

- [ ] The page renders a "Email verified" confirmation (not an error).
- [ ] Visiting `/u/smtp-tester` afterwards, the user row's `EmailVerified` is now `true`. Visible via:

   ```powershell
   sqlite3 .\data\forum.db "SELECT username, email_verified FROM users WHERE username='smtp-tester';"
   ```

   Should print `smtp-tester|1` (1 = true).
- [ ] Server log: `INFO msg="http" method=GET path=/auth/verify status=200`.

## 3. Verify token is single-use

Reload the verify URL (or open it in a new tab).

- [ ] The second visit renders an error page ("token is invalid or expired" or similar).
- [ ] `auth_tokens` table no longer has a verify row for this user:

   ```powershell
   sqlite3 .\data\forum.db "SELECT id, type FROM auth_tokens WHERE user_id = (SELECT id FROM users WHERE username='smtp-tester');"
   ```

   Returns no rows (the verify token was deleted on first use; see [token.go:116](internal/auth/token.go#L116)).

## 4. Forgot password

Logout (or open a private window), visit http://localhost:8080/auth/forgot-password, submit `smtp-tester@forum.test`.

- [ ] Page shows a "check your email" confirmation regardless of whether the email exists (this is intentional — never confirm or deny account existence to avoid enumeration).
- [ ] Mailpit catches a second email. Subject reads something like `Reset your ForumForge password`.
- [ ] Body contains a link `http://localhost:8080/auth/reset-password?token=...`.

## 5. Forgot password for unknown email

Submit `nosuchuser@forum.test`.

- [ ] You see the SAME success confirmation as in #4 — not an "unknown email" error.
- [ ] Mailpit shows **NO new email** for that address.
- [ ] Server log distinguishes the cases internally but the response is identical.

## 6. Reset link → form → new password

Click the reset URL from email #4. You should land on a "Set a new password" page.

- [ ] The form renders without requiring you to be logged in.
- [ ] Submitting an empty / too-short password (< 8 chars) shows a validation error and DOES NOT consume the token (you can re-submit with a valid password).
- [ ] Submit a valid new password (≥8 chars).
- [ ] You are redirected to `/auth/login`.
- [ ] Logging in with the OLD password fails.
- [ ] Logging in with the NEW password succeeds.

## 7. Reset token is single-use and per-issue

While still logged in as smtp-tester, click the same reset URL again.

- [ ] Error page (token invalid/expired). The same token cannot reset twice.
- [ ] Issue a fresh reset (forgot-password → email arrives → click link). The newest issuance is what works; older tokens for the same user are invalidated by the issue path ([token.go:86](internal/auth/token.go#L86)).

## 8. Reset invalidates other sessions

Setup: login as smtp-tester in two browsers (or Firefox + a Chrome private window). Confirm both can hit `/settings` without redirecting to login.

- [ ] In browser A: trigger forgot-password, follow reset link, set new password.
- [ ] In browser B (the OTHER session): refresh any page. You should be redirected to `/auth/login` because the password reset deleted all sessions for that user ([token.go:149](internal/auth/token.go#L149)).

## 9. Token-expiry behaviour

Hard to exercise interactively (verify is 24h, reset is 1h). Either:

- [ ] Inspect the `expires_at` column directly after issuing a token:

   ```powershell
   sqlite3 .\data\forum.db "SELECT type, datetime(expires_at) FROM auth_tokens ORDER BY id DESC LIMIT 2;"
   ```

   The `verify_email` row should be ~24h from now (UTC); `reset_password` ~1h.
- [ ] Fake an expiry by manually setting `expires_at = '2000-01-01 00:00:00'` for one token, then clicking its link — it should be rejected as invalid.

## 10. Server-side failure paths

Stop Mailpit (close its window), then trigger another registration or forgot-password.

- [ ] Server log shows `ERROR ... smtp send: dial tcp [::1]:1025: connectex: No connection could be made...` or similar.
- [ ] The HTTP response — what does the user see? Check the user-facing copy: ideally either a generic "couldn't send email, try again later" or the registration still succeeds (since the user row is created before the email is sent) with a flash about the email failure. **Whatever you see, note it** — this is the path I'd most expect to surface a bug, because the error-handling here is the least-tested.
- [ ] Restart Mailpit afterwards.

## 11. Manual relaunch test

Stop the forum server (Ctrl+C), unset the SMTP env vars (`Remove-Item Env:FORUM_SMTP_HOST` etc.), restart.

- [ ] Startup log returns to `WARN SMTP not configured — email features ... are disabled`.
- [ ] Registering still works; the body of what would have been the verify email is logged to stdout instead. The dev fallback in [email.go:123-130](internal/auth/email.go#L123-L130) is the safety net for unconfigured deployments.

---

## How to file a "doesn't match" finding

Same as verify-seed.md: cite the section number, what you expected, what you saw, and any log lines printed by the forum server in the second between submitting the form and seeing the result. SMTP-side bugs (auth failure, TLS negotiation) print verbose error messages and are easy to bisect with that one log slice.
