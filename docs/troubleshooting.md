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

## Managed Entry/Exit Profile Fails

If a managed profile is missing or connects to the wrong Exit, check in this
order before replacing any working listener or DNS record:

1. Check the profile's managed Entry hostname and Cloudflare DNS state. The A
   record must point to the Entry address in DNS-only mode, not to an Exit.
   `pending` means reconciliation is still due or retrying; `ready` means the
   owned record was observed, not that every resolver has updated. `conflict`
   calls for an ownership or record check, not overwriting an unrelated record.
   For `error`, inspect the error code and API reconciliation logs and correct
   configuration or provider permission. `deleting` means cleanup is in
   progress; `deleted` means the owned record has been removed. DNS caches and
   previously issued profiles may persist after deletion.
2. Check that the client's TCP destination is the Entry hostname on `24443`
   or `24444`, as intended. Verify Entry port reachability, upstream firewall,
   IP forwarding, DNAT/masquerade policy, and connectivity from Entry to the
   selected Exit. Do not test this as native UDP forwarding.
3. Check the selected Exit's upstream firewall and nft source admission. The
   protected listener port should accept the Entry's source address and drop
   other sources. `publish_direct=false` hides direct profiles but doesn't
   enforce that network restriction.
4. Verify the selected Exit listener is running with the profile's VLESS
   credentials, Reality public key and short ID, and that its accepted server
   names include the Exit's exact canonical client SNI. The Entry routing
   hostname is not the SNI; check all published listeners, including speed
   tiers, if only some clients fail.
5. Check fresh Entry and Exit agent heartbeats, policy fetch/apply logs, and
   configuration acknowledgement. An unsafe or incompatible SNI/listener
   combination withholds managed rollout or profile publication. It does not
   forcibly terminate all existing Xray sessions. Do not distribute a profile
   until the listeners and policies are applied and acknowledged.

If policy fetch or apply fails, inspect the agent logs and restore API access
or correct the invalid policy. The agent retains the last successfully applied
policy on failure; that is not instant revocation of an old allowlist or
profile. For a failed SNI cutover, restore the known-good canonical SNI and
listener settings, apply them, wait for fresh agent acknowledgement, then
republish profiles. Keep the expanded schema and stored data intact during an
application rollback; verify both the old traffic path and DNS state before
retiring the new path.

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
