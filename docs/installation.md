# Installation Guide

## System Requirements

- Linux (Ubuntu 20.04+, Debian 11+, CentOS 8+)
- 2 GB RAM minimum (4 GB recommended)
- 10 GB disk space
- Docker 24+ and Docker Compose v2
- A domain name (recommended for TLS)

## Step 1: Install Docker

If Docker isn't installed yet:

```bash
curl -fsSL https://get.docker.com | sh
systemctl enable --now docker
```

Verify:

```bash
docker --version
docker compose version
```

## Step 2: Clone the Repository

```bash
git clone https://github.com/proximavpn/proxima-vpn.git
cd proxima-vpn
```

## Step 3: Configure Environment

```bash
cp .env.example .env
```

Edit `.env` and set these values:

| Variable | Description |
|----------|-------------|
| `JWT_SECRET` | Random 64-character string. Generate with `openssl rand -hex 32` |
| `POSTGRES_PASSWORD` | Strong database password |
| `REDIS_PASSWORD` | Strong Redis password |
| `PANEL_URL` | Public URL of your panel (e.g. `https://panel.example.com`) |
| `ADMIN_EMAIL` | Admin login email |
| `ADMIN_PASSWORD` | Leave blank for auto-generated password |

Optional:

| Variable | Description |
|----------|-------------|
| `TELEGRAM_BOT_TOKEN` | Bot token from @BotFather |
| `TELEGRAM_CHAT_ID` | Your Telegram chat ID for notifications |
| `TELEGRAM_ENABLED` | Set to `true` to activate the bot |

## Step 4: Start Services

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d --build
```

This starts all services: PostgreSQL, Redis, API server, Web UI, and Prometheus.

## Step 5: First Login

If you left `ADMIN_PASSWORD` blank, the system generates credentials on first run:

```bash
docker compose exec api cat /tmp/proxima-initial-credentials.txt
```

Use those credentials to log in at your `PANEL_URL`.

## Firewall Setup

Open these ports:

| Port | Service | Required |
|------|---------|----------|
| 8080 | Web Panel | Yes |
| 2053 | API Server | Yes |
| 443 | Node traffic (on node servers) | Yes |
| 9090 | Prometheus | Optional (internal only recommended) |

Example with `ufw`:

```bash
ufw allow 8080/tcp
ufw allow 2053/tcp
ufw reload
```

## Security Checklist

- [ ] Changed all default passwords in `.env`
- [ ] Generated a strong random `JWT_SECRET`
- [ ] Set `SWAGGER_ENABLED=false` in production
- [ ] Restricted port 9090 (Prometheus) to internal access
- [ ] Set up a reverse proxy (nginx/Caddy) with TLS for the panel
- [ ] Configured firewall to only expose necessary ports
- [ ] Enabled Telegram notifications for monitoring

## Updating

```bash
git pull
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d --build
```

The database migrations run automatically on API startup.
