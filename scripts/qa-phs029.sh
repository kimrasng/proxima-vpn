#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
id="phs029-$(date +%s)-$$-$RANDOM"
output="$root/.omo/start-work/evidence/PHS-029/runs/$id"
if [[ "${1:-}" == '--output' ]]; then
  [[ $# -eq 2 ]] || { printf 'Usage: %s [--output NEW_DIR]\n' "$0" >&2; exit 2; }
  output="$2"
elif [[ $# -ne 0 ]]; then
  printf 'Usage: %s [--output NEW_DIR]\n' "$0" >&2
  exit 2
fi
[[ ! -e "$output" ]] || { printf 'evidence directory already exists\n' >&2; exit 2; }
for tool in docker node git; do command -v "$tool" >/dev/null; done
docker info >/dev/null 2>&1 || { printf 'Docker daemon unavailable\n' >&2; exit 1; }
mkdir -p "$output"
scratch="$(mktemp -d "${TMPDIR:-/tmp}/phs029-XXXXXXXX")"
network="$id-net"
postgres="$id-postgres"
redis="$id-redis"
runner="$id-go"
modcache="$id-modcache"
buildcache="$id-buildcache"
password='phs029-disposable-only'
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
  rm -rf -- "$scratch"
  printf 'PHS-029 cleanup: %s (exit %s)\n' "$id" "$status"
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
docker run -d --name "$redis" --label "proxima.qa.owner=$id" --network "$network" redis:7-alpine >/dev/null
ready=0
for _ in {1..60}; do
  if docker exec "$postgres" pg_isready -U vpnuser -d vpnpanel >/dev/null 2>&1 && \
    [[ "$(docker exec "$redis" redis-cli ping 2>/dev/null || true)" == PONG ]]; then
    ready=1
    break
  fi
  sleep 1
done
[[ "$ready" -eq 1 ]] || { printf 'Disposable Postgres/Redis unavailable\n' >&2; exit 1; }

go_run() {
  docker run --rm --name "$runner" --label "proxima.qa.owner=$id" --network "$network" \
    -v "$root:/src:ro" -v "$modcache:/go/pkg/mod" -v "$buildcache:/root/.cache/go-build" \
    -w /src/api-server -e "TEST_DATABASE_URL=postgres://vpnuser:$password@$postgres:5432/vpnpanel" \
    -e "DATABASE_URL=postgres://vpnuser:$password@$postgres:5432/vpnpanel" \
    -e "TEST_REDIS_ADDR=$redis:6379" -e GOTOOLCHAIN=local golang:1.25 "$@"
}

migration=0
go_run go run ./cmd/migrate >"$scratch/migrate.log" 2>&1 || migration=$?
printf 'go run ./cmd/migrate: exit %s\n' "$migration"
tests=125
checker=125
vet=125
build=125
if [[ "$migration" -eq 0 ]]; then
  tests=0
  go_run go test -p 1 -race -shuffle=on -count=1 -json \
    ./internal/config ./internal/database ./internal/services ./internal/handlers ./internal/scheduler \
    >"$scratch/test.jsonl" 2>"$scratch/test.stderr" || tests=$?
  checker=0
  node "$root/scripts/check-phs029-results.mjs" "$scratch/test.jsonl" "$output/tests.json" || checker=$?
  vet=0
  go_run go vet ./internal/config ./internal/database ./internal/services ./internal/handlers ./internal/scheduler \
    >"$scratch/vet.log" 2>&1 || vet=$?
  build=0
  go_run go build ./... >"$scratch/build.log" 2>&1 || build=$?
fi
diff=0
git -C "$root" diff --check >"$scratch/diff.log" 2>&1 || diff=$?
compose=0
(cd "$root" && docker compose config -q) >"$scratch/compose.log" 2>&1 || compose=$?

# Deployment YAML is scanned without printing matching lines; unrelated E2E fixtures are outside this gate.
yaml=0
node - "$root" <<'JS' || yaml=$?
const { execFileSync } = require('node:child_process');
const { existsSync, readFileSync } = require('node:fs');
const { join } = require('node:path');
const root = process.argv[2];
const paths = ['api-server/config.example.yaml', 'docker-compose.yml', 'docker-compose.dev.yml'];
for (const path of paths) {
  const diff = execFileSync('git', ['diff', '--unified=0', '--', path], { cwd: root, encoding: 'utf8' });
  const added = diff.split('\n').filter((line) => line.startsWith('+') && !line.startsWith('+++')).map((line) => line.slice(1));
  const untracked = execFileSync('git', ['ls-files', '--others', '--exclude-standard', '--', path], { cwd: root, encoding: 'utf8' }).trim();
  if (untracked && existsSync(join(root, path))) added.push(...readFileSync(join(root, path), 'utf8').split('\n'));
  for (const line of added) {
    const field = line.match(/^\s*(?:-\s*)?([\w-]+)\s*:\s*(.*)$/);
    const env = line.match(/^\s*-\s*([\w-]+)=(.*)$/);
    const key = field?.[1] ?? env?.[1] ?? '';
    const value = (field?.[2] ?? env?.[2] ?? '').trim().replace(/^['"]/, '');
    if (path === 'api-server/config.example.yaml' && /token/i.test(key) && field) process.exitCode = 1;
    if (/(?:token|password|secret|credential|api_key)/i.test(key) && value && !value.startsWith('$')) process.exitCode = 1;
  }
}
JS

node - "$output/summary.json" "$output/tests.json" "$migration" "$tests" "$checker" "$vet" "$build" "$diff" "$compose" "$yaml" <<'JS'
const fs = require('node:fs');
const [, , path, testsPath, ...codes] = process.argv;
const names = ['go run ./cmd/migrate', 'go test -p 1 -race -shuffle=on -count=1 -json ./internal/{config,database,services,handlers,scheduler}', 'Go JSON checker', 'go vet ./internal/{config,database,services,handlers,scheduler}', 'go build ./...', 'git diff --check', 'docker compose config -q', 'deployment YAML token-field/literal-credential scan'];
const commands = names.map((name, index) => ({ command: name, exitCode: Number(codes[index]) }));
const tests = fs.existsSync(testsPath) ? JSON.parse(fs.readFileSync(testsPath, 'utf8')) : null;
fs.writeFileSync(path, `${JSON.stringify({ commands, tests }, null, 2)}\n`, { flag: 'wx' });
JS
printf 'Evidence: %s\n' "${output#"$root"/}/summary.json"
for code in "$migration" "$tests" "$checker" "$vet" "$build" "$diff" "$compose" "$yaml"; do
  [[ "$code" -eq 0 ]] || { printf 'PHS-029 gate failed; command exit codes in summary.json\n' >&2; exit 1; }
done
printf 'PHS-029 gate PASS: race tests, coverage, vet, build, diff, Compose, YAML scan\n'
