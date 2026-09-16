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
