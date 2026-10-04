# Proxima VPN Panel

Multi-protocol VPN management panel with web UI, Telegram bot, and multi-node support.

## Features

- Multi-protocol: VLESS Reality, VMess, Trojan, Shadowsocks, Hysteria2, WireGuard
- Multi-format subscription: V2Ray, Clash, Sing-box, Surfboard, Quantumult (Hysteria2/WireGuard는 Clash/Sing-box만 지원 — Surfboard/Quantumult는 두 앱의 제한적인 문법 탓에 미지원. WireGuard는 전용 `.conf` 다운로드도 지원)
- Dynamic inbound management per node
- Telegram Bot for full user management
- Real-time monitoring with Prometheus
- Dark mode, multi-language (EN/KO/ZH)

## Quick Start (Development)

```bash
cp .env.example .env
docker compose up -d --build
```

Panel: http://localhost:8080
API: http://localhost:2053

To work on the frontend with hot reload at the same panel URL after starting the stack:

```bash
docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d --build --no-deps web
```

Changes under `web/` then update in the browser without rebuilding. The development
container proxies API requests to the existing `api` service. To return to the
production nginx container, run `docker compose up -d --build --no-deps --force-recreate web`.

## Production Deployment

See [docs/installation.md](docs/installation.md)

## Adding Nodes

See [docs/node-setup.md](docs/node-setup.md)

## Troubleshooting

See [docs/troubleshooting.md](docs/troubleshooting.md)

## Architecture

```
┌─────────┐     ┌─────────┐     ┌──────────┐
│  Web UI │────▶│   API   │────▶│ Postgres │
│  :8080  │     │  :2053  │     └──────────┘
└─────────┘     │         │────▶┌──────────┐
                │         │     │  Redis   │
┌─────────┐     │         │     └──────────┘
│Telegram │────▶│         │
│   Bot   │     └─────────┘
└─────────┘         │
                    ▼
              ┌───────────┐
              │Node Agents│
              └───────────┘
```

## Utilities

### ChatGPT Checkout Script

One-click script for testing ChatGPT checkout API calls from Chrome Console.

**File:** `scripts/chatgpt-checkout.js`

**Usage:**
```bash
# Generate the console script
node scripts/chatgpt-checkout.js

# Copy the output and paste into Chrome Console on chatgpt.com
# Script auto-fetches auth token - no manual steps needed!
```

**What it does:**
- Auto-fetches auth token from `https://chatgpt.com/api/auth/session`
- Uses token to make checkout request with `plan_name=chatgptpromax`
- Only includes essential headers (`authorization`, `content-type`)
- Browser automatically injects other headers

**Features:**
- No manual token replacement needed
- One-click execution
- Error handling included
- Logs all steps for debugging

## License

MIT
