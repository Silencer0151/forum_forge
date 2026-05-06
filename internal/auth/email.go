package auth

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net"
	"net/smtp"
	"path/filepath"
	"strconv"
	"strings"
)

// LoadEmailTemplates reads every "<name>.html" file under "email/" inside
// fsys and returns a name → source map suitable for NewMailer. fsys should be
// rooted at templates/ (so "email/verify.html" resolves correctly). Names in
// the returned map are the file basename without extension ("verify", "reset").
func LoadEmailTemplates(fsys fs.FS) (map[string]string, error) {
	entries, err := fs.Glob(fsys, "email/*.html")
	if err != nil {
		return nil, fmt.Errorf("glob email templates: %w", err)
	}
	out := make(map[string]string, len(entries))
	for _, p := range entries {
		raw, err := fs.ReadFile(fsys, p)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", p, err)
		}
		name := strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
		out[name] = string(raw)
	}
	return out, nil
}

// SMTPConfig is the server-side configuration the email sender needs.
// Mirrors the FORUM_SMTP_* env vars from spec.md §14. When Host is empty the
// sender runs in development mode: instead of dialing an SMTP server, it logs
// the rendered email body (and any links inside it) so a developer can copy
// the verification or reset link from stdout.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
}

// Enabled reports whether an SMTP server is configured. Callers use this to
// surface a "we sent you an email" response even when no server is wired up:
// the link is logged to stdout and recoverable in dev.
func (c SMTPConfig) Enabled() bool { return c.Host != "" }

// Mailer renders templated emails and dispatches them via SMTP. It is safe to
// share across goroutines: net/smtp opens a fresh connection per Send call.
type Mailer struct {
	cfg       SMTPConfig
	templates map[string]*template.Template

	// sendFunc is the actual delivery function — net/smtp by default, but
	// tests can swap in a recorder. The signature mirrors smtp.SendMail.
	sendFunc func(addr string, auth smtp.Auth, from string, to []string, msg []byte) error
}

// MailerOption configures a Mailer at construction time.
type MailerOption func(*Mailer)

// WithSendFunc replaces the network-sending function. Tests use this to
// capture messages without dialing a real SMTP server.
func WithSendFunc(fn func(addr string, auth smtp.Auth, from string, to []string, msg []byte) error) MailerOption {
	return func(m *Mailer) { m.sendFunc = fn }
}

// NewMailer constructs a Mailer with templates parsed from emailTemplates.
// Pass html/template-compatible source for each known template name; a
// missing template surfaces as an error from Send. opts can override the
// SMTP send function (used in tests).
func NewMailer(cfg SMTPConfig, emailTemplates map[string]string, opts ...MailerOption) (*Mailer, error) {
	parsed := make(map[string]*template.Template, len(emailTemplates))
	for name, src := range emailTemplates {
		t, err := template.New(name).Parse(src)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		parsed[name] = t
	}
	m := &Mailer{
		cfg:       cfg,
		templates: parsed,
		sendFunc:  smtp.SendMail,
	}
	for _, o := range opts {
		o(m)
	}
	return m, nil
}

// Email is the rendered output the Mailer dispatches.
type Email struct {
	To      string
	Subject string
	Body    string // HTML body
}

// Send dispatches a templated email. If SMTP is not configured (Host empty),
// it logs the email body to stdout and returns nil — useful in development
// when a developer needs to grab the verification or reset link.
func (m *Mailer) Send(ctx context.Context, templateName, to, subject string, data any) error {
	tmpl, ok := m.templates[templateName]
	if !ok {
		return fmt.Errorf("mailer: template %q not registered", templateName)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("render %s: %w", templateName, err)
	}
	body := buf.String()

	if !m.cfg.Enabled() {
		// Development fallback. Logging at info level so it's visible
		// without flipping the log level — the absence of SMTP_HOST is
		// already a deliberate developer choice.
		slog.Info("auth: email (smtp disabled, logged to stdout)",
			"to", to, "subject", subject, "body", body)
		return nil
	}

	msg := buildMessage(m.cfg.From, to, subject, body)
	addr := net.JoinHostPort(m.cfg.Host, strconv.Itoa(m.cfg.Port))

	var auth smtp.Auth
	if m.cfg.Username != "" {
		auth = smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)
	}
	if err := m.sendFunc(addr, auth, m.cfg.From, []string{to}, msg); err != nil {
		return fmt.Errorf("smtp send: %w", err)
	}
	return nil
}

// buildMessage assembles a minimal RFC 5322 / 2822 HTML message. The body is
// HTML-only; clients without HTML rendering will still display the raw markup
// readably.
func buildMessage(from, to, subject, htmlBody string) []byte {
	headers := []string{
		"From: " + from,
		"To: " + to,
		"Subject: " + subject,
		"MIME-Version: 1.0",
		`Content-Type: text/html; charset="UTF-8"`,
	}
	return []byte(strings.Join(headers, "\r\n") + "\r\n\r\n" + htmlBody)
}

// SendStartTLS is a higher-fidelity sender that explicitly negotiates STARTTLS
// before authenticating. It exists for callers (or future hardening) that want
// to bypass net/smtp.SendMail's default behaviour. Currently unused; kept here
// so tests document the expected contract.
func SendStartTLS(addr, from string, to []string, host, username, password string, msg []byte) error {
	c, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer c.Close()

	if ok, _ := c.Extension("STARTTLS"); ok {
		tlsConfig := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
		if err := c.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("starttls: %w", err)
		}
	} else {
		return errors.New("smtp: server does not advertise STARTTLS")
	}

	if username != "" {
		if err := c.Auth(smtp.PlainAuth("", username, password, host)); err != nil {
			return fmt.Errorf("auth: %w", err)
		}
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("mail from: %w", err)
	}
	for _, addr := range to {
		if err := c.Rcpt(addr); err != nil {
			return fmt.Errorf("rcpt to: %w", err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("data: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("close: %w", err)
	}
	return c.Quit()
}
