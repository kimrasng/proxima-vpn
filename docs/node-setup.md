# Node Setup Guide

Nodes are remote servers that run VPN protocols. Each node connects back to the panel via the node-agent service.

## Prerequisites

- A Linux server (Ubuntu 20.04+, Debian 11+, CentOS 8+)
- Root access
- Network connectivity to the panel server (port 2053)
- Port 443 open for VPN traffic (or your chosen port)

## Step 1: Generate a Registration Token

1. Log in to the admin panel
2. Go to **Nodes** > **Add Node**
3. Click **Generate Token**
4. Copy the token (it expires after 10 minutes)

## Step 2: Run the Install Script

On the node server, run:

```bash
bash <(curl -s https://your-panel.com/scripts/install.sh) \
  --server https://your-panel.com \
  --token YOUR_REGISTRATION_TOKEN
```

Optional flags:

| Flag | Description | Default |
|------|-------------|---------|
| `--name` | Display name in panel | Server hostname |
| `--country` | Country code (e.g. `US`, `DE`) | Auto-detected |
| `--region` | City or region | Auto-detected |
| `--port` | Service port for VPN traffic | 443 |

Example with all options:

```bash
bash <(curl -s https://your-panel.com/scripts/install.sh) \
  --server https://your-panel.com \
  --token abc123def456 \
  --name "Frankfurt-1" \
  --country DE \
  --region Frankfurt \
  --port 443
```

## Step 3: Verify in Panel

After installation completes:

1. Go to **Nodes** in the admin panel
2. The new node should appear with a green "Online" status
3. If it shows "Offline", wait about 10 seconds - the agent heartbeats on that interval and the list refreshes itself

## Step 4: Configure Inbounds

Once the node is online:

1. Click the node name to open its settings
2. Go to **Inbounds** > **Add Inbound**
3. Select a protocol (VLESS Reality, VMess, Trojan, etc.)
4. Configure the inbound settings
5. Save and apply

The node-agent picks up configuration changes automatically.

## Managing the Node Agent

The agent runs as a systemd service:

```bash
# Check status
systemctl status node-agent

# View logs
journalctl -u node-agent -f

# Restart
systemctl restart node-agent

# Stop
systemctl stop node-agent
```

Configuration lives at `/etc/node-agent/config.json`.

## Updating the Node Agent

The agent heartbeats every 10 seconds and the panel marks a node offline after
40 seconds of silence. An agent older than that change beats every 30 seconds
and will be reported offline between beats, so the panel and the agents must be
updated together.

Build the panel with a version stamp, so agents can tell old from new:

```bash
docker compose build --build-arg AGENT_VERSION=v0.2.0 api
docker compose up -d api
```

Then either roll out automatically, or update each node by hand.

**Automatic.** Set the target version once and every agent picks it up within
five minutes, replaces its own binary, and restarts:

```sql
INSERT INTO settings (key, value) VALUES ('agent_target_version', 'v0.2.0')
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;
```

**By hand**, on each node:

```bash
bash <(curl -s https://your-panel.com/scripts/update.sh) --agent-only
```

Confirm what a node is running with `node-agent version`.

## Uninstalling a Node

```bash
bash <(curl -s https://your-panel.com/scripts/uninstall.sh)
```

This stops the service, removes binaries, and deregisters the node from the panel.

## Troubleshooting Node Connection

**Node shows "Offline" in panel:**

1. Check the agent is running: `systemctl status node-agent`
2. Check logs for errors: `journalctl -u node-agent --no-pager -n 50`
3. Verify the node can reach the panel: `curl -s https://your-panel.com/health`
4. Check firewall allows outbound to panel port 2053

**Registration failed:**

- Token may have expired. Generate a new one from the panel.
- Verify `--server` URL is correct and reachable from the node.

**Xray won't start:**

- Check Xray logs: `journalctl -u node-agent -f`
- Verify port 443 isn't already in use: `ss -tlnp | grep 443`
- Ensure the inbound configuration is valid in the panel.
