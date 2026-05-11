# Deployment Guide

This guide covers all supported deployment configurations: single binary with automatic TLS, manual TLS certificates, behind a reverse proxy, Docker, and Postgres.

---

## Option 1: Single Binary + Autocert (Recommended)

The simplest production setup. The forum obtains and renews TLS certificates automatically from Let's Encrypt. You need a domain name with DNS already pointing at your server.

### Requirements

- A Linux server with ports 80 and 443 open
- A domain name (e.g. `forum.example.com`) pointing at the server's public IP
- No other process listening on ports 80 or 443

### Setup

```sh
# Download or build the binary
make build
scp forum_forge user@your-server:/opt/forum/forum

# On the server, create the data directories
mkdir -p /opt/forum/{data,uploads,certs}

# Create a minimal systemd service
cat > /etc/systemd/system/forum.service << 'EOF'
[Unit]
Description=Forum Forge
After=network.target

[Service]
User=forum
WorkingDirectory=/opt/forum
ExecStart=/opt/forum/forum
Restart=always
RestartSec=5

Environment=FORUM_BASE_URL=https://forum.example.com
Environment=FORUM_TLS_MODE=autocert
Environment=FORUM_TLS_DOMAINS=forum.example.com
Environment=FORUM_TLS_CERT_DIR=/opt/forum/certs
Environment=FORUM_DB_PATH=/opt/forum/data/forum.db
Environment=FORUM_UPLOAD_PATH=/opt/forum/uploads
Environment=FORUM_SESSION_SECRET=<generate with: openssl rand -hex 32>
Environment=FORUM_LOG_FORMAT=json

[Install]
WantedBy=multi-user.target
EOF

useradd -r -s /bin/false forum
chown -R forum:forum /opt/forum

systemctl daemon-reload
systemctl enable --now forum
```

The server listens on `:443` for HTTPS traffic and `:80` to handle ACME HTTP-01 challenges (and redirect non-HTTPS requests). The certificate cache is stored in `FORUM_TLS_CERT_DIR`.

---

## Option 2: Manual TLS Certificates

Use this when you already have certificates (from a CA, internal PKI, or a wildcard cert).

```sh
Environment=FORUM_TLS_MODE=manual
Environment=FORUM_TLS_CERT_FILE=/etc/ssl/forum/fullchain.pem
Environment=FORUM_TLS_KEY_FILE=/etc/ssl/forum/privkey.pem
Environment=FORUM_BASE_URL=https://forum.example.com
```

The forum calls `ListenAndServeTLS` directly with the supplied cert and key. Certificate rotation requires a service restart (or a `SIGHUP` handler if you add one).

For Let's Encrypt with Certbot managing the files manually:

```sh
certbot certonly --standalone -d forum.example.com
# Certificates land in /etc/letsencrypt/live/forum.example.com/
```

Add a Certbot renewal hook to restart the service after renewal:

```sh
cat > /etc/letsencrypt/renewal-hooks/deploy/forum.sh << 'EOF'
#!/bin/sh
systemctl restart forum
EOF
chmod +x /etc/letsencrypt/renewal-hooks/deploy/forum.sh
```

---

## Option 3: Behind a Reverse Proxy

Run the forum on an internal port with TLS terminated by nginx, Caddy, or a cloud load balancer. This is the right setup when you are hosting multiple services on one machine, or when a platform (like Kanoogi) routes `/forum/` traffic to the forum service.

### Forum config

```sh
Environment=FORUM_TLS_MODE=none
Environment=FORUM_LISTEN_ADDR=:8080
Environment=FORUM_BASE_URL=https://forum.example.com
Environment=FORUM_TRUST_PROXY=true   # trust X-Forwarded-For / X-Forwarded-Proto
```

`FORUM_TRUST_PROXY=true` causes the middleware to read the real client IP from `X-Forwarded-For` and the scheme from `X-Forwarded-Proto`. Only enable this if your proxy is the only thing that can reach the forum's listen port (use a firewall or private network).

### nginx example

```nginx
server {
    listen 443 ssl;
    server_name forum.example.com;

    ssl_certificate     /etc/ssl/forum/fullchain.pem;
    ssl_certificate_key /etc/ssl/forum/privkey.pem;

    location / {
        proxy_pass         http://127.0.0.1:8080;
        proxy_set_header   Host              $host;
        proxy_set_header   X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header   X-Forwarded-Proto $scheme;
        proxy_read_timeout 60s;
    }
}

server {
    listen 80;
    server_name forum.example.com;
    return 301 https://$host$request_uri;
}
```

### Caddy example

```caddyfile
forum.example.com {
    reverse_proxy localhost:8080
}
```

Caddy automatically obtains and renews TLS certificates. The forum itself runs with `FORUM_TLS_MODE=none`.

### Subpath mount (e.g. `/forum/`)

If the proxy routes `/forum/*` to the forum, set `FORUM_BASE_URL` to the full subpath URL:

```sh
Environment=FORUM_BASE_URL=https://example.com/forum
```

All generated links, redirects, and cookie paths use `BASE_URL` as their root, so this is the only change required on the forum side.

nginx subpath config:

```nginx
location /forum/ {
    proxy_pass         http://127.0.0.1:8080/forum/;
    proxy_set_header   Host              $host;
    proxy_set_header   X-Forwarded-For   $proxy_add_x_forwarded_for;
    proxy_set_header   X-Forwarded-Proto $scheme;
}
```

---

## Option 4: Docker

### SQLite (quick start)

```sh
docker compose up -d
```

The default `docker-compose.yml` runs the forum with SQLite. Data is persisted in Docker named volumes (`forum_data`, `forum_uploads`, `forum_certs`).

Edit the `environment:` block in `docker-compose.yml` to configure the forum:

```yaml
environment:
  FORUM_BASE_URL: "https://forum.example.com"
  FORUM_SESSION_SECRET: "replace-with-a-real-secret"
  FORUM_TLS_MODE: none          # set to autocert if running without a proxy
  FORUM_TLS_DOMAINS: forum.example.com
  FORUM_SMTP_HOST: smtp.example.com
  FORUM_SMTP_PORT: "587"
  FORUM_SMTP_USER: forum@example.com
  FORUM_SMTP_PASS: "smtp-password"
  FORUM_SMTP_FROM: forum@example.com
```

### Postgres

```sh
docker compose -f docker-compose.postgres.yml up -d
```

Starts a Postgres 16 container alongside the forum. The forum waits for Postgres to be healthy before starting. Data is persisted in the `pgdata` named volume.

### Building the image

```sh
docker build -t forum_forge:latest .
```

The Dockerfile uses a two-stage build: Go builder → Alpine runtime. The final image contains only the compiled binary, templates, static files, and CA certificates.

---

## Postgres Migration Path

To switch an existing SQLite deployment to Postgres:

1. **Stop the forum** — prevents writes during migration.

2. **Export SQLite data** using a tool such as [`pgloader`](https://pgloader.io/) or a manual dump:
   ```sh
   # pgloader example
   pgloader sqlite:///opt/forum/data/forum.db \
            postgresql://forum:password@localhost/forum
   ```

3. **Update config** to point at Postgres:
   ```sh
   FORUM_DB_DRIVER=postgres
   FORUM_DB_DSN=postgres://forum:password@localhost/forum?sslmode=require
   ```

4. **Start the forum** — migrations run automatically on startup.

5. Verify with `GET /health`.

The forum's migrations are idempotent (`IF NOT EXISTS` guards), so re-running them against an already-migrated Postgres database is safe.

---

## Health Check

The forum exposes `GET /health` which returns `200 OK` with body `ok` when the server is ready and the database is reachable. Use this for load balancer health checks, Docker `HEALTHCHECK`, and uptime monitoring.

---

## Email (SMTP)

Without SMTP configured, the forum still works — it logs verification and password reset URLs to stdout instead of emailing them. This is fine for development or private communities that skip email verification.

For production, configure SMTP with STARTTLS on port 587:

```sh
FORUM_SMTP_HOST=smtp.example.com
FORUM_SMTP_PORT=587
FORUM_SMTP_USER=forum@example.com
FORUM_SMTP_PASS=secret
FORUM_SMTP_FROM="Forum Forge <forum@example.com>"
```

---

## Security Hardening Checklist

- [ ] `FORUM_SESSION_SECRET` set to a random 32+ byte hex string (`openssl rand -hex 32`)
- [ ] `FORUM_BASE_URL` uses `https://` so session cookies get the `Secure` flag
- [ ] Firewall blocks direct access to the forum's internal port if behind a proxy
- [ ] `FORUM_TRUST_PROXY=true` only set when a trusted proxy is in front
- [ ] SQLite database file (`data/forum.db`) not readable by other system users
- [ ] `uploads/` directory not directly served (the forum serves files through `/attachments/{id}/*` with access checks)
- [ ] SMTP credentials stored as environment variables or secrets, not in compose files committed to version control
