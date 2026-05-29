# Proxima VPN Panel

Multi-protocol VPN management panel with web UI, Telegram bot, and multi-node support.

## Features

- Multi-protocol: VLESS Reality, VMess, Trojan, Shadowsocks, Hysteria2, WireGuard
- Multi-format subscription: V2Ray, Clash, Sing-box, Surfboard, Quantumult
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

## License

MIT
# vpn-panel
