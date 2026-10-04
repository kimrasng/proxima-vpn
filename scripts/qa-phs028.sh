#!/usr/bin/env bash
# Usage: bash scripts/qa-phs028.sh [--output NEW_DIR] [package:TestName ...]
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
output="$root/.omo/start-work/evidence/PHS-028/runs/$(date +%s)-$$"
if [[ "${1:-}" == '--output' ]]; then
  [[ $# -ge 2 ]] || { printf 'missing output directory\n' >&2; exit 2; }
  output="$2"
  shift 2
fi
if [[ $# -eq 0 ]]; then
  set -- 'github.com/proximavpn/proxima-vpn/api-server/internal/database:Test_Migrate_completes_for_concurrent_callers' \
    'github.com/proximavpn/proxima-vpn/api-server/internal/services:TestOnlineUUIDsComeFromThePerIPReport'
fi
[[ ! -e "$output" ]] || { printf 'evidence exists: %s\n' "$output" >&2; exit 2; }
for spec in "$@"; do
  [[ "$spec" =~ ^[A-Za-z0-9_./-]+:Test[A-Za-z0-9_]+$ ]] || { printf 'invalid package:TestName: %s\n' "$spec" >&2; exit 2; }
done
command -v docker >/dev/null
command -v node >/dev/null
docker info >/dev/null 2>&1 || { printf 'Docker daemon unavailable\n' >&2; exit 1; }
mkdir -p "$output"

id="phs028-$(date +%s)-$$-$RANDOM"
network="$id-net"
postgres="$id-postgres"
redis="$id-redis"
runner="$id-go"
modcache="$id-modcache"
buildcache="$id-buildcache"
password='phs028-disposable-only'
cleanup() {
  local status=$?
  trap - EXIT INT TERM
  for resource in "$runner" "$redis" "$postgres"; do
    if [[ "$(docker inspect -f '{{index .Config.Labels "proxima.qa.owner"}}' "$resource" 2>/dev/null || true)" == "$id" ]]; then
      docker rm -f "$resource" >/dev/null 2>&1 || true
    fi
  done
  for resource in "$modcache" "$buildcache"; do
    if [[ "$(docker volume inspect -f '{{index .Labels "proxima.qa.owner"}}' "$resource" 2>/dev/null || true)" == "$id" ]]; then
      docker volume rm "$resource" >/dev/null 2>&1 || true
    fi
  done
  if [[ "$(docker network inspect -f '{{index .Labels "proxima.qa.owner"}}' "$network" 2>/dev/null || true)" == "$id" ]]; then
    docker network rm "$network" >/dev/null 2>&1 || true
  fi
  printf 'PHS-028 cleanup: %s (exit %s)\n' "$id" "$status"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

docker network create --label "proxima.qa.owner=$id" "$network" >/dev/null
docker volume create --label "proxima.qa.owner=$id" "$modcache" >/dev/null
docker volume create --label "proxima.qa.owner=$id" "$buildcache" >/dev/null
docker run -d --name "$postgres" --label "proxima.qa.owner=$id" --network "$network" \
  -e POSTGRES_USER=vpnuser -e "POSTGRES_PASSWORD=$password" -e POSTGRES_DB=vpnpanel \
  postgres:16-alpine >/dev/null
docker run -d --name "$redis" --label "proxima.qa.owner=$id" --network "$network" \
  redis:7-alpine >/dev/null

ready=0
for _ in {1..60}; do
  if docker exec "$postgres" pg_isready -U vpnuser -d vpnpanel >/dev/null 2>&1 && \
    [[ "$(docker exec "$redis" redis-cli ping 2>/dev/null || true)" == PONG ]]; then
    ready=1
    break
  fi
  sleep 1
done
[[ "$ready" -eq 1 ]] || { printf 'Postgres/Redis not ready\n' >&2; exit 1; }
printf 'Disposable Postgres and Redis ready\n'

go_run() {
  docker run --rm --name "$runner" --label "proxima.qa.owner=$id" --network "$network" \
    -v "$root:/src:ro" -v "$modcache:/go/pkg/mod" -v "$buildcache:/root/.cache/go-build" \
    -w /src/api-server -e "TEST_DATABASE_URL=postgres://vpnuser:$password@$postgres:5432/vpnpanel" \
    -e "DATABASE_URL=postgres://vpnuser:$password@$postgres:5432/vpnpanel" \
    -e "TEST_REDIS_ADDR=$redis:6379" -e GOTOOLCHAIN=local golang:1.25 "$@"
}

# The migration output may contain connection diagnostics; record the exit only.
migration_exit=0
go_run go run ./cmd/migrate >/dev/null 2>&1 || migration_exit=$?
node -e 'const fs=require("fs"); fs.writeFileSync(process.argv[1], JSON.stringify({command:"go run ./cmd/migrate",exitCode:Number(process.argv[2])})+"\n", {flag:"wx"})' \
  "$output/migration.json" "$migration_exit"
[[ "$migration_exit" -eq 0 ]] || { printf 'Migration failed (exit %s)\n' "$migration_exit" >&2; exit 1; }

entries=()
index=0
for spec in "$@"; do
  index=$((index + 1))
  pkg="${spec%:*}"
  test="${spec##*:}"
  file="test-$index.jsonl"
  code=0
  go_run go test -p 1 -race -count=1 -json -run "^${test}\$" "$pkg" > "$output/$file" 2>/dev/null || code=$?
  entries+=("$index|$pkg|$test|$file|$code")
  printf 'Go race test %s: exit %s\n' "$test" "$code"
done

node - "$output/results.json" "${entries[@]}" <<'JS'
const fs = require('node:fs');
const [, , path, ...entries] = process.argv;
const commands = entries.map((entry) => {
  const [index, pkg, test, file, code] = entry.split('|');
  return { name: `go-test-${index}`, file, exitCode: Number(code), required: [{ package: pkg, test }] };
});
fs.writeFileSync(path, `${JSON.stringify({ commands }, null, 2)}\n`, { flag: 'wx' });
JS
node "$root/scripts/check-phs028-results.mjs" "$output/results.json"
