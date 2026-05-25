# Troubleshooting

## Container Won't Start

**Symptoms:** `docker compose up` fails or containers keep restarting.

**Check logs:**

```bash
docker compose logs api
docker compose logs db
docker compose logs web
```

**Common causes:**

- Missing `.env` file. Copy from example: `cp .env.example .env`
- `JWT_SECRET` not set. The API refuses to start without it.
- Database password mismatch. Ensure `POSTGRES_PASSWORD` in `.env` matches what the database was initialized with. If you changed it after first run, remove the volume: `docker compose down -v` (this deletes data).
- Port already in use. Check with `ss -tlnp | grep 8080` or `ss -tlnp | grep 2053`.

## Can't Log In

**Symptoms:** Login page rejects credentials.

**Fixes:**

- On first run with blank `ADMIN_PASSWORD`, check auto-generated credentials:
  ```bash
  docker compose exec api cat /tmp/proxima-initial-credentials.txt
  ```
- If `JWT_SECRET` changed after users were created, all existing sessions become invalid. Users need to log in again with their passwords. If the admin password is lost, reset it via:
  ```bash
  docker compose exec api node-agent admin reset-password --email admin@example.com
  ```

## Node Shows Offline

**Symptoms:** Node appears in panel but status is "Offline".

**On the node server:**

1. Check agent service:
   ```bash
   systemctl status node-agent
   ```
2. Check agent logs:
   ```bash
   journalctl -u node-agent -f
   ```
3. Test connectivity to panel:
   ```bash
   curl -s https://your-panel.com/health
   ```
4. Verify DNS resolution works and firewall allows outbound on port 2053.

**On the panel server:**

- Check API logs for connection attempts: `docker compose logs api | grep "node"`

## Subscription Not Working

**Symptoms:** VPN client can't import subscription link, or shows no servers.

**Check:**

1. Verify the subscription URL is accessible from outside:
   ```bash
   curl -I https://your-panel.com/api/sub/YOUR_TOKEN
   ```
2. Ensure `PANEL_URL` in `.env` is set to the public URL (not `localhost`).
3. Check the user has active traffic and hasn't expired.
4. Try a different subscription format. Some clients only support specific formats:
   - iOS Shadowrocket: V2Ray format
   - Clash for Android: Clash format
   - Sing-box clients: Sing-box format

## Database Connection Errors

**Symptoms:** API logs show "connection refused" or "authentication failed" for PostgreSQL.

**Fixes:**

- Ensure the `db` container is healthy: `docker compose ps`
- If you changed `POSTGRES_PASSWORD` after initial setup, the database still has the old password. Either revert the password or recreate the volume:
  ```bash
  docker compose down
  docker volume rm proxima-vpn_pg_data
  docker compose up -d
  ```
  Warning: this deletes all data. Back up first.

## Redis Connection Errors

**Symptoms:** API logs show Redis connection failures.

**Fixes:**

- Check Redis is running: `docker compose ps redis`
- Verify `REDIS_PASSWORD` matches between `.env` and what Redis was started with.
- Test connection:
  ```bash
  docker compose exec redis redis-cli -a YOUR_REDIS_PASSWORD ping
  ```

## High Memory Usage

The production compose file sets memory limits:

| Service | Limit |
|---------|-------|
| API | 512 MB |
| Database | 512 MB |
| Redis | 256 MB |
| Web | 128 MB |
| Prometheus | 256 MB |

If a container is OOM-killed, check `docker compose logs <service>` and consider increasing limits in `docker-compose.prod.yml`.

## Prometheus Not Collecting Metrics

- Verify Prometheus is running: `docker compose ps prometheus`
- Check the config is mounted: the `prometheus.yml` file must exist in the project root.
- Access Prometheus UI at `http://your-server:9090/targets` to see scrape status.

## Common Docker Issues

**"Permission denied" errors:**

```bash
sudo usermod -aG docker $USER
# Log out and back in
```

**Disk space full:**

```bash
docker system prune -a --volumes
```

Warning: this removes all unused images and volumes.

**Compose version mismatch:**

If you see errors about compose file format, ensure you're using Docker Compose v2:

```bash
docker compose version
# Should show v2.x.x
```
