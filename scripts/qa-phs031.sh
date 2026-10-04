#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
id="phs031-$(date -u +%Y%m%dT%H%M%S)-$$-$RANDOM"
output="$root/tests/evidence/entry-dns-sni/$id"
if [[ $# -eq 2 && "$1" == --output ]]; then
  output=$2
elif [[ $# -ne 0 ]]; then
  printf 'Usage: bash scripts/qa-phs031.sh [--output NEW_DIR]\n' >&2
  exit 2
fi
[[ ! -e "$output" && ! -L "$output" ]] || { printf 'Output already exists or is a symlink\n' >&2; exit 2; }
output=$(node -e 'console.log(require("node:path").resolve(process.argv[1]))' "$output")
for tool in docker npm npx node curl git spec-graph; do command -v "$tool" >/dev/null; done
docker info >/dev/null
scratch=$(mktemp -d "${TMPDIR:-/tmp}/$id-XXXXXXXX")
network="$id-net"
postgres="$id-postgres"
redis="$id-redis"
runner="$id-runner"
api="$id-api"
modcache="$id-modcache"
buildcache="$id-buildcache"
password='disposable-phs031-local-only'
admin_password='DisposableAdmin031!'
commands="$scratch/commands.jsonl"
mkdir -p "$output"
finish() {
  local status=$?
  trap - EXIT INT TERM
  if [[ -n "${preview_pid:-}" ]]; then kill "$preview_pid" 2>/dev/null || true; wait "$preview_pid" 2>/dev/null || true; fi
  for resource in "$runner" "$api" "$redis" "$postgres"; do
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
  node - "$commands" "$output" "$status" <<'JS'
const fs = require('node:fs');
const path = require('node:path');
const [commandsPath, output, exit] = process.argv.slice(2);
const commands = fs.existsSync(commandsPath) ? fs.readFileSync(commandsPath, 'utf8').trim().split('\n').filter(Boolean).map(JSON.parse) : [];
const read = (file) => fs.existsSync(path.join(output, file)) ? JSON.parse(fs.readFileSync(path.join(output, file))) : null;
const relay = read('relay/summary.json');
const modules = Object.fromEntries(['api', 'pkg', 'node'].map(lane => [lane, read(`${lane}.json`)]));
const browser = read('browser.json');
const passed = Number(exit) === 0 && commands.every(command => command.exitCode === 0) &&
  relay?.assertions === 24 && Object.values(modules).every(module => module?.passed) && browser?.passed;
const summary = { passed: !!passed, commands, relay: relay && { assertions: relay.assertions, exitCode: commands.find(c => c.command === 'relay')?.exitCode }, modules, browser };
fs.writeFileSync(path.join(output, 'summary.json'), JSON.stringify(summary, null, 2) + '\n');
const lines = ['# PHS-031 final regression gate', '', `Status: ${passed ? 'PASS' : 'FAIL'}`, '',
  `Relay: ${relay?.assertions ?? 0}/24 assertions; exit ${summary.relay?.exitCode ?? 'not run'}.`,
  ...Object.entries(modules).map(([lane, value]) => `${lane}: ${value?.testPassCount ?? 0} test/subtest passes, ${value?.skipCount ?? 0} skips, ${value?.packagePassCount ?? 0}/${value?.expectedPackages ?? 0} package passes${value?.nft ? `; nft ${value.nft.testPassCount} pass` : ''}.`),
  `Browser: ${browser?.passedTests ?? 0}/${browser?.total ?? 0} passes; ${browser?.skipped ?? 0} skips; ${browser?.files ?? 0}/${browser?.requiredFiles ?? 0} spec files.`, '',
  'Fixture boundaries: local DNS/DB fixture and syntactically valid test-only managed DNS; no live Cloudflare. Legacy pool compatibility is DB-seeded in the isolated relay harness.', '',
  'Command exit codes:', ...commands.map(c => `- ${c.command}: ${c.exitCode}`), ''];
fs.writeFileSync(path.join(output, 'summary.md'), lines.join('\n'));
JS
  rm -rf -- "$scratch"
  printf 'PHS-031 %s: %s (raw logs removed)\n' "$([[ "$status" -eq 0 ]] && printf PASS || printf FAIL)" "$output"
  exit "$status"
}
trap finish EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

record() {
  node - "$commands" "$1" "$2" <<'JS'
const fs = require('node:fs');
fs.appendFileSync(process.argv[2], JSON.stringify({ command: process.argv[3], exitCode: Number(process.argv[4]) }) + '\n');
JS
}
stage() {
  local name=$1 code=0
  shift
  "$@" >"$scratch/$name.log" 2>&1 || code=$?
  record "$name" "$code"
  printf '%s: exit %s\n' "$name" "$code"
  if [[ "$code" -ne 0 && "$name" == browser-real-* && -f "$scratch/${name#browser-}-report.json" ]]; then
    node - "$scratch/${name#browser-}-report.json" <<'JS'
const fs = require('node:fs');
const report = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'));
function visit(suite) {
  for (const spec of suite.specs ?? []) for (const test of spec.tests ?? [])
    if (test.status !== 'expected') {
      console.error(`Browser ${test.status}: ${suite.file ?? 'nested'} ${spec.title}`);
      for (const result of test.results ?? []) {
        const message = (result.error?.message ?? '').replace(/\x1b\[[0-9;]*m/g, '')
          .replace(/[0-9a-f]{8}-[0-9a-f-]{27,}/gi, '[redacted]')
          .replace(/(?:Bearer\s+|(?:password|token|secret)[=:]\s*)\S+/gi, '[redacted]');
        console.error(message.slice(0, 1000));
      }
    }
  for (const child of suite.suites ?? []) visit(child);
}
for (const suite of report.suites ?? []) visit(suite);
JS
  fi
  [[ "$code" -eq 0 ]] || { printf '%s failed; raw diagnostics retained only until cleanup\n' "$name" >&2; exit 1; }
}
go_run() {
  local module=$1
  shift
  docker run --rm --name "$runner" --label "proxima.qa.owner=$id" --network "$network" \
    -v "$root:/src:ro" -v "$scratch:/qa" -v "$modcache:/go/pkg/mod" \
    -v "$buildcache:/root/.cache/go-build" -w "/src/$module" \
    -e "TEST_DATABASE_URL=postgres://vpnuser:$password@$postgres:5432/vpnpanel" \
    -e "DATABASE_URL=postgres://vpnuser:$password@$postgres:5432/vpnpanel" \
    -e "TEST_REDIS_ADDR=$redis:6379" -e GOTOOLCHAIN=local golang:1.25-bookworm "$@"
}
go_test() {
  local lane=$1 module=$2 code=0
  shift 2
  go_run "$module" "$@" >"$scratch/$lane.jsonl" 2>"$scratch/$lane.stderr" || code=$?
  record "$lane-race-tests" "$code"
  printf '%s race tests: exit %s\n' "$lane" "$code"
  [[ "$code" -eq 0 ]] || exit 1
}

stage relay bash "$root/tests/relay-e2e/run.sh" --output "$output/relay"
stage relay-check node "$root/tests/relay-e2e/check-results.js" "$output/relay/assertions.jsonl"
node - "$output/relay/summary.json" <<'JS'
const s = JSON.parse(require('node:fs').readFileSync(process.argv[2], 'utf8'));
if (s.assertions !== 24 || s.status !== 'pass') process.exit(1);
JS

docker network create --label "proxima.qa.owner=$id" "$network" >/dev/null
docker volume create --label "proxima.qa.owner=$id" "$modcache" >/dev/null
docker volume create --label "proxima.qa.owner=$id" "$buildcache" >/dev/null
docker run -d --name "$postgres" --label "proxima.qa.owner=$id" --network "$network" \
  -e POSTGRES_USER=vpnuser -e "POSTGRES_PASSWORD=$password" -e POSTGRES_DB=vpnpanel postgres:16-alpine >/dev/null
docker run -d --name "$redis" --label "proxima.qa.owner=$id" --network "$network" redis:7-alpine >/dev/null
ready=0
for _ in {1..60}; do
  if docker exec "$postgres" pg_isready -U vpnuser -d vpnpanel >/dev/null 2>&1 &&
    [[ "$(docker exec "$redis" redis-cli ping 2>/dev/null || true)" == PONG ]]; then ready=1; break; fi
  sleep 1
done
[[ "$ready" -eq 1 ]] || { record fixtures 1; exit 1; }
stage migration go_run api-server go run ./cmd/migrate
for lane in api pkg node; do
  case "$lane" in api) module=api-server;; pkg) module=pkg;; node) module=node-agent;; esac
  go_run "$module" go list ./... >"$scratch/$lane.packages" 2>"$scratch/$lane.list.stderr" || { record "$lane-list" 1; exit 1; }
  record "$lane-list" 0
  if [[ "$lane" == node ]]; then
    go_test "$lane" "$module" sh -c 'apt-get update -qq && apt-get install -y -qq iproute2 >/dev/null && exec go test -race -shuffle=on -count=1 -json ./...'
    code=0
    docker run --rm --name "$runner" --label "proxima.qa.owner=$id" --network "$network" --privileged \
      -v "$root:/src:ro" -v "$scratch:/qa" -v "$modcache:/go/pkg/mod" \
      -v "$buildcache:/root/.cache/go-build" -w /src/node-agent -e NFT_CHECK=1 \
      -e GOTOOLCHAIN=local golang:1.25-bookworm sh -c \
      'apt-get update -qq && apt-get install -y -qq nftables >/dev/null && exec go test -race -count=1 -json ./internal/relay -run '\''^TestRenderNftablesSyntax$'\''' \
      >"$scratch/node-nft.jsonl" 2>"$scratch/node-nft.stderr" || code=$?
    record node-nft "$code"
    [[ "$code" -eq 0 ]] || exit 1
    stage "$lane-check" node "$root/scripts/check-phs031-results.mjs" "$lane" "$scratch/$lane.jsonl" "$scratch/$lane.packages" "$output/$lane.json" "$scratch/node-nft.jsonl"
  else
    if [[ "$lane" == api ]]; then go_test "$lane" "$module" go test -p 1 -race -shuffle=on -count=1 -json ./...
    else go_test "$lane" "$module" go test -race -shuffle=on -count=1 -json ./...; fi
    stage "$lane-check" node "$root/scripts/check-phs031-results.mjs" "$lane" "$scratch/$lane.jsonl" "$scratch/$lane.packages" "$output/$lane.json"
  fi
  stage "$lane-vet" go_run "$module" go vet ./...
  stage "$lane-build" go_run "$module" go build ./...
done

stage api-binary go_run api-server go build -o /qa/api-server ./cmd
port=$(node -e 'const s=require("node:net").createServer();s.listen(0,"127.0.0.1",()=>{console.log(s.address().port);s.close()})')
start_api() {
  local batch=$1 api_ready=1
  if [[ "$(docker inspect -f '{{index .Config.Labels "proxima.qa.owner"}}' "$api" 2>/dev/null || true)" == "$id" ]]; then
    docker rm -f "$api" >/dev/null
  fi
  docker run -d --name "$api" --label "proxima.qa.owner=$id" --network "$network" \
    --add-host api.cloudflare.com:127.0.0.1 -p "127.0.0.1:$port:2053" \
    -v "$scratch:/qa:ro" -e "DATABASE_URL=postgres://vpnuser:$password@$postgres:5432/vpnpanel" \
    -e "REDIS_URL=redis://$redis:6379" -e "JWT_SECRET=disposable-phs031-jwt" \
    -e ADMIN_EMAIL=admin@e2e.test -e "ADMIN_PASSWORD=$admin_password" \
    -e CLOUDFLARE_API_TOKEN=disposable-test-only -e CLOUDFLARE_ZONE_ID=00000000000000000000000000000000 \
    -e ENTRY_DNS_BASE_DOMAIN=entry.qa.test -e SWAGGER_ENABLED=false \
    golang:1.25-bookworm /qa/api-server >/dev/null
  for _ in {1..60}; do
    if curl -fsS "http://127.0.0.1:$port/health" >"$scratch/$batch-health.log" 2>&1; then api_ready=0; break; fi
    sleep 1
  done
  record "$batch-api-ready" "$api_ready"
  [[ "$api_ready" -eq 0 ]] || exit 1
}
start_api real-admin
stage order-fixture docker exec -i "$postgres" psql -v ON_ERROR_STOP=1 -U vpnuser -d vpnpanel <<'SQL'
WITH g AS (INSERT INTO node_groups(name) VALUES ('qa-order-group') RETURNING id),
p AS (INSERT INTO plans(name, duration_days, node_group_id) SELECT 'qa-order-plan', 30, id FROM g RETURNING id),
u AS (INSERT INTO users(email, password_hash, sub_token) VALUES ('qa-order@e2e.test', 'unused', encode(gen_random_bytes(16), 'hex')) RETURNING id)
INSERT INTO plan_orders(user_id, plan_id, duration_days, price_cents, status, origin, user_agent, device_fingerprint)
SELECT u.id, p.id, 30, 1000, 'pending', 'web', 'QA fixture', 'qa-fixture' FROM u CROSS JOIN p;
SQL

mkdir -p "$scratch/web"
cp -R "$root/web/e2e" "$scratch/web/e2e"
for file in package.json package-lock.json playwright.config.ts vite.config.ts tsconfig.json index.html eslint.config.js; do
  [[ ! -e "$root/web/$file" ]] || cp "$root/web/$file" "$scratch/web/$file"
done
for dir in src public scripts node_modules; do
  [[ ! -e "$root/web/$dir" ]] || ln -s "$root/web/$dir" "$scratch/web/$dir"
done
mkdir -p "$scratch/web/e2e/.auth"
rm -f "$scratch/web/e2e/.auth/admin.json"
web_run() { (cd "$scratch/web" && "$@"); }
stage locales web_run npm run check:locales
stage lint web_run npm run lint
stage web-build web_run npm run build
preview_port=$(node -e 'const s=require("node:net").createServer();s.listen(0,"127.0.0.1",()=>{console.log(s.address().port);s.close()})')
(cd "$scratch/web" && VITE_API_PROXY_TARGET="http://127.0.0.1:$port" exec node node_modules/vite/bin/vite.js preview --host 127.0.0.1 --port "$preview_port" --strictPort) >"$scratch/preview.log" 2>&1 &
preview_pid=$!
preview_ready=1
for _ in {1..60}; do
  if curl -fsS "http://127.0.0.1:$preview_port/admin/login" >/dev/null 2>&1; then preview_ready=0; break; fi
  sleep 1
done
record preview-ready "$preview_ready"
[[ "$preview_ready" -eq 0 ]] || exit 1
browser_lane() {
  local lane=$1
  shift
  (cd "$scratch/web" && E2E_NO_WEBSERVER=1 E2E_BASE_URL="http://127.0.0.1:$preview_port" \
    E2E_ADMIN_EMAIL=admin@e2e.test E2E_ADMIN_PASSWORD="$admin_password" \
    PLAYWRIGHT_JSON_OUTPUT_FILE="$scratch/$lane-report.json" \
    npx playwright test --reporter=json --output="$scratch/$lane-results" "$@")
}
stage browser-real-admin browser_lane real-admin --project=admin 'e2e/admin\.spec\.ts$'
mv "$scratch/web/e2e/.auth/admin.json" "$scratch/real-admin-auth.json"
start_api real-node-chains
stage browser-real-node-chains browser_lane real-node-chains --project=admin node-chains.spec.ts
mv "$scratch/web/e2e/.auth/admin.json" "$scratch/real-node-chains-auth.json"
start_api real-admin-subscription-domains
stage browser-real-admin-subscription-domains browser_lane real-admin-subscription-domains --project=admin admin-subscription-domains.spec.ts
mv "$scratch/web/e2e/.auth/admin.json" "$scratch/real-admin-subscription-domains-auth.json"
start_api real-user-portal
stage browser-real-user-portal browser_lane real-user-portal --project=user-portal user-portal.spec.ts
start_api real-user-subscription-domains
stage browser-real-user-subscription-domains browser_lane real-user-subscription-domains --project=user-portal user-subscription-domains.spec.ts
stage browser-mock browser_lane mock --project=admin --no-deps \
  managed-chain-lifecycle.admin.spec.ts managed-node-chains.admin.spec.ts node-endpoints.admin.spec.ts
stage browser-check node "$root/scripts/check-phs031-results.mjs" browser "$root/web/e2e" "$output/browser.json" \
  "$scratch/real-admin-report.json" "$scratch/real-node-chains-report.json" \
  "$scratch/real-admin-subscription-domains-report.json" "$scratch/real-user-portal-report.json" \
  "$scratch/real-user-subscription-domains-report.json" "$scratch/mock-report.json"

stage diff-check env GIT_MASTER=1 git -C "$root" diff --check
stage graph-validate spec-graph validate
stage credential-scan node - "$root" "$output" <<'JS'
const fs = require('node:fs');
const path = require('node:path');
const { execFileSync } = require('node:child_process');
const [root, evidence] = process.argv.slice(2);
const forbidden = /(?:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|Bearer\s+\S+|(?:vless|postgres|postgresql|redis):\/\/|(?:private|public)[_-]?key|short[_-]?id|subscription[_-]?url|password|cloudflare[_-]?(?:api[_-]?)?token|browser[_-]?storage|"(?:cookies|origins)"\s*:)/i;
let findings = 0;
function scan(dir) {
  for (const item of fs.readdirSync(dir, { withFileTypes: true })) {
    const file = path.join(dir, item.name);
    if (item.isSymbolicLink()) { findings++; continue; }
    if (item.isDirectory()) scan(file);
    else if (forbidden.test(fs.readFileSync(file, 'utf8'))) findings++;
  }
}
scan(evidence);
const git = (...args) => execFileSync('git', args, { cwd: root, encoding: 'utf8' });
for (const file of git('diff', '--name-only').split('\n').filter(name => /^(api-server|node-agent|pkg|web|scripts)\//.test(name))) {
  const additions = git('diff', '--unified=0', '--', file).split('\n').filter(line => line.startsWith('+') && !line.startsWith('+++')).join('\n');
  if (/(?:cf_[a-z0-9]{30,}|(?:password|secret|token)\s*[:=]\s*["'][a-z0-9+/_=-]{32,}["'])/i.test(additions)) findings++;
}
console.log(`Credential scan: ${findings} findings (values suppressed)`);
if (findings) process.exitCode = 1;
JS
