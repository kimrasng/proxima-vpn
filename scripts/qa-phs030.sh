#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
id="phs030-$(date +%s)-$$-$RANDOM"
output="$root/.omo/start-work/evidence/PHS-030/runs/$id"
if [[ "${1:-}" == '--output' ]]; then
  [[ $# -eq 2 ]] || { printf 'Usage: %s [--output NEW_DIR]\n' "$0" >&2; exit 2; }
  output="$2"
elif [[ $# -ne 0 ]]; then
  printf 'Usage: %s [--output NEW_DIR]\n' "$0" >&2
  exit 2
fi
[[ ! -e "$output" ]] || { printf 'evidence directory already exists\n' >&2; exit 2; }
for tool in docker node npm npx git curl; do command -v "$tool" >/dev/null; done
docker info >/dev/null 2>&1 || { printf 'Docker daemon unavailable\n' >&2; exit 1; }
mkdir -p "$output"
scratch="$(mktemp -d "${TMPDIR:-/tmp}/phs030-XXXXXXXX")"
network="$id-net"
postgres="$id-postgres"
redis="$id-redis"
runner="$id-go"
modcache="$id-modcache"
buildcache="$id-buildcache"
password='phs030-disposable-only'
preview_pid=''
cleanup() {
  local status=$?
  trap - EXIT INT TERM
  if [[ -n "$preview_pid" ]]; then
    kill "$preview_pid" 2>/dev/null || true
    wait "$preview_pid" 2>/dev/null || true
  fi
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
  printf 'PHS-030 cleanup: %s (exit %s)\n' "$id" "$status"
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

packages=(./internal/reality ./internal/database ./internal/services ./internal/handlers)
migration=0
go_run go run ./cmd/migrate >"$scratch/migrate.log" 2>&1 || migration=$?
printf 'Migration: exit %s\n' "$migration"
tests=125 checker=125 vet=125 build=125
if [[ "$migration" -eq 0 ]]; then
  tests=0
  go_run go test -p 1 -race -shuffle=on -count=1 -json "${packages[@]}" \
    >"$scratch/test.jsonl" 2>"$scratch/test.stderr" || tests=$?
  checker=0
  node "$root/scripts/check-phs030-results.mjs" "$scratch/test.jsonl" "$output/tests.json" || checker=$?
  vet=0
  go_run go vet "${packages[@]}" >"$scratch/vet.log" 2>&1 || vet=$?
  build=0
  go_run go build ./... >"$scratch/build.log" 2>&1 || build=$?
fi

locales=0 lint=0 frontend_build=0
(cd "$root/web" && npm run check:locales) >"$scratch/locales.log" 2>&1 || locales=$?
(cd "$root/web" && npm run lint) >"$scratch/lint.log" 2>&1 || lint=$?
(cd "$root/web" && npm run build) >"$scratch/frontend-build.log" 2>&1 || frontend_build=$?

preview=125 playwright=125 browser_check=125
if [[ "$frontend_build" -eq 0 ]]; then
  port="$(node -e 'const s=require("node:net").createServer();s.listen(0,"127.0.0.1",()=>{console.log(s.address().port);s.close()})')"
  (cd "$root/web" && exec node node_modules/vite/bin/vite.js preview --host 127.0.0.1 --port "$port" --strictPort) \
    >"$scratch/preview.log" 2>&1 &
  preview_pid=$!
  preview=1
  for _ in {1..60}; do
    if ! kill -0 "$preview_pid" 2>/dev/null; then break; fi
    if curl --fail --silent --output /dev/null "http://127.0.0.1:$port/admin/nodes"; then preview=0; break; fi
    sleep 1
  done
  if [[ "$preview" -eq 0 ]]; then
    playwright=0
    (cd "$root/web" && E2E_NO_WEBSERVER=1 E2E_BASE_URL="http://127.0.0.1:$port" \
      PLAYWRIGHT_JSON_OUTPUT_FILE="$scratch/playwright.json" npx playwright test --project=admin --no-deps \
      --reporter=json --output="$scratch/playwright-results" \
      managed-chain-lifecycle.admin.spec.ts managed-node-chains.admin.spec.ts node-endpoints.admin.spec.ts) \
      >"$scratch/playwright.log" 2>&1 || playwright=$?
    browser_check=0
    node - "$scratch/playwright.json" "$output/browser.json" <<'JS' || browser_check=$?
const fs = require('node:fs');
const path = process.argv[2];
try {
  const report = JSON.parse(fs.readFileSync(path, 'utf8'));
  const expected = ['managed-chain-lifecycle.admin.spec.ts', 'managed-node-chains.admin.spec.ts', 'node-endpoints.admin.spec.ts'];
  const specs = [];
  function collect(suite, parentFile) {
    const file = suite.file ?? parentFile;
    for (const spec of suite.specs ?? []) specs.push({ file, spec });
    for (const child of suite.suites ?? []) collect(child, file);
  }
  for (const suite of report.suites ?? []) collect(suite);
  const counts = { passed: 0, failed: 0, skipped: 0, total: 0 };
  for (const { spec } of specs) for (const test of spec.tests ?? []) {
    counts.total++;
    const status = test.status;
    if (status === 'expected' && test.results?.some((result) => result.status === 'passed')) counts.passed++;
    else if (status === 'skipped') counts.skipped++;
    else counts.failed++;
  }
  const present = expected.filter((name) => specs.some(({ file }) => file?.endsWith(name)));
  const passed = present.length === expected.length && counts.total > 0 && counts.passed === counts.total && report.stats?.unexpected === 0 && report.stats?.skipped === 0;
  fs.writeFileSync(process.argv[3], JSON.stringify({ ...counts, files: present.length, requiredFiles: expected.length, passed }, null, 2) + '\n', { flag: 'wx' });
  console.log(`Playwright: ${counts.passed}/${counts.total} passed, ${counts.skipped} skipped, ${present.length}/${expected.length} files`);
  if (!passed) process.exitCode = 1;
} catch (error) {
  console.error(`Playwright report unavailable or invalid: ${error.message}`);
  process.exitCode = 1;
}
JS
  fi
  kill "$preview_pid" 2>/dev/null || true
  wait "$preview_pid" 2>/dev/null || true
  preview_pid=''
fi

diff=0
GIT_MASTER=1 git -C "$root" diff --check >"$scratch/diff.log" 2>&1 || diff=$?
secret=0
node - "$root" "$output" <<'JS' >"$scratch/secret.log" 2>&1 || secret=$?
const { execFileSync } = require('node:child_process');
const { readFileSync, readdirSync } = require('node:fs');
const { join } = require('node:path');
const root = process.argv[2];
const output = process.argv[3];
const relevant = /^(?:api-server\/(?:internal|config)|web\/(?:src|e2e)|docker-compose|\.env\.example|scripts\/qa-phs030|scripts\/check-phs030)/;
const git = (...args) => execFileSync('git', args, { cwd: root, encoding: 'utf8', maxBuffer: 32 * 1024 * 1024 });
const changed = git('diff', '--name-only').split('\n');
const untracked = git('ls-files', '--others', '--exclude-standard').split('\n');
let scanned = 0;
let findings = 0;
for (const file of new Set([...changed, ...untracked].filter((name) => relevant.test(name)))) {
  let lines;
  if (untracked.includes(file)) lines = readFileSync(join(root, file), 'utf8').split('\n');
  else lines = git('diff', '--unified=0', '--', file).split('\n').filter((line) => line.startsWith('+') && !line.startsWith('+++')).map((line) => line.slice(1));
  scanned++;
  for (const line of lines) {
    // Only concrete credentials, not field names, placeholders, test tokens or env references.
    if (/(?:cf_[a-z0-9]{30,}|cloudflare[_-]?(?:api[_-]?)?token\s*[:=]\s*["']?[a-z0-9_-]{30,})/i.test(line) ||
        /(?:password|secret|credential|api[_-]?token)\s*[:=]\s*["'][a-z0-9_+\/=.-]{32,}["']/i.test(line)) findings++;
  }
}
for (const file of readdirSync(output)) {
  if (!file.endsWith('.json')) continue;
  const text = readFileSync(join(output, file), 'utf8');
  if (/cf_[a-z0-9]{30,}|cloudflare[_-]?(?:api[_-]?)?token\s*[:=]\s*["']?[a-z0-9_-]{30,}/i.test(text)) findings++;
}
console.log(`Secret scan: ${scanned} changed relevant files, ${findings} suspected credentials (values suppressed)`);
if (findings) process.exitCode = 1;
JS

node - "$output/summary.json" "$output/tests.json" "$output/browser.json" \
  "$migration" "$tests" "$checker" "$vet" "$build" "$locales" "$lint" "$frontend_build" \
  "$preview" "$playwright" "$browser_check" "$diff" "$secret" <<'JS'
const fs = require('node:fs');
const [, , path, testsPath, browserPath, ...codes] = process.argv;
const names = ['migration', 'Go race tests (reality/database/services/handlers)', 'Go JSON inventory checker',
  'go vet (same packages)', 'go build ./...', 'frontend locale parity', 'frontend lint', 'frontend build',
  'production Vite preview readiness', 'setup-free Playwright admin specs', 'Playwright JSON checker',
  'git diff --check', 'relevant additions secret scan'];
const commands = names.map((command, index) => ({ command, exitCode: Number(codes[index]) }));
const tests = fs.existsSync(testsPath) ? JSON.parse(fs.readFileSync(testsPath, 'utf8')) : null;
const browser = fs.existsSync(browserPath) ? JSON.parse(fs.readFileSync(browserPath, 'utf8')) : null;
fs.writeFileSync(path, JSON.stringify({ passed: commands.every((item) => item.exitCode === 0), commands, tests, browser }, null, 2) + '\n', { flag: 'wx' });
JS
printf 'Evidence: %s\n' "${output#"$root"/}/summary.json"
for code in "$migration" "$tests" "$checker" "$vet" "$build" "$locales" "$lint" "$frontend_build" "$preview" "$playwright" "$browser_check" "$diff" "$secret"; do
  [[ "$code" -eq 0 ]] || { printf 'PHS-030 gate failed; exit codes in summary.json\n' >&2; exit 1; }
done
printf 'PHS-030 gate PASS\n'
