#!/usr/bin/env bash
# AWS multi-VM real-node E2E: provision -> register nodes -> checks -> evidence -> destroy.
# Creates billable AWS resources. Refuses to run without AWS_E2E_APPROVE=yes.
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
root=$(cd "${here}/../.." && pwd)
state="${here}/.state"
evidence="${here}/evidence/$(date +%Y%m%d-%H%M%S)"
export TF_DATA_DIR="${state}/tfdata"
export TF_IN_AUTOMATION=1
tf=(terraform -chdir="${here}")
ssh_opts=(-i "${state}/key" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o ConnectTimeout=10)

[[ "${AWS_E2E_APPROVE:-}" == yes ]] || { echo 'Refusing: set AWS_E2E_APPROVE=yes to create AWS resources (costs money).' >&2; exit 2; }
for c in terraform aws ssh ssh-keygen curl jq go; do command -v "$c" >/dev/null || { echo "missing: $c" >&2; exit 2; }; done
aws sts get-caller-identity >/dev/null || { echo 'AWS auth missing: run aws configure / aws sso login' >&2; exit 2; }

mkdir -p "${state}" "${evidence}"
chmod 700 "${state}"
[[ -f "${state}/key" ]] || ssh-keygen -q -t ed25519 -N '' -f "${state}/key"
my_ip=$(curl -fsS https://checkip.amazonaws.com | tr -d '[:space:]')

destroyed=0
cleanup() {
  status=$?
  if (( destroyed == 0 )); then
    echo "== terraform destroy (exit ${status}) =="
    "${tf[@]}" destroy -auto-approve -input=false \
      -state="${state}/terraform.tfstate" \
      -var "my_ip_cidr=${my_ip}/32" -var "ssh_public_key=$(cat "${state}/key.pub")" || {
      echo "DESTROY FAILED - resources may still exist. Re-run: terraform -chdir=${here} destroy -state=${state}/terraform.tfstate" >&2
      status=1
    }
    destroyed=1
  fi
  exit "${status}"
}
trap cleanup EXIT

"${tf[@]}" init -input=false >/dev/null
"${tf[@]}" apply -auto-approve -input=false -state="${state}/terraform.tfstate" \
  -var "my_ip_cidr=${my_ip}/32" -var "ssh_public_key=$(cat "${state}/key.pub")"
out=$("${tf[@]}" output -json -state="${state}/terraform.tfstate")
pub() { jq -er --arg k "$1" '.public_ip.value[$k]' <<<"$out"; }
priv() { jq -er --arg k "$1" '.private_ip.value[$k]' <<<"$out"; }
rsh() { local vm=$1; shift; ssh "${ssh_opts[@]}" "ubuntu@$(pub "$vm")" "$@"; }
rcp() { scp "${ssh_opts[@]}" -q "$1" "ubuntu@$(pub "$2"):$3"; }

for vm in panel exit1 exit2 entry client1 client2; do
  for _ in $(seq 1 60); do rsh "$vm" true 2>/dev/null && break; sleep 3; done
  rsh "$vm" 'cloud-init status --wait >/dev/null 2>&1 || true'
  echo "PASS: ssh to $vm ($(pub "$vm"))"
done

# Build linux/amd64 binaries locally from the current checkout.
bin="${state}/bin"; mkdir -p "${bin}"
(cd "${root}/api-server" && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o "${bin}/proxima-api" ./cmd)
(cd "${root}/node-agent" && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o "${bin}/node-agent-linux-amd64" ./cmd)

# --- Panel: Postgres + Redis (Docker) and the API binary ---------------------
admin_pw=$(openssl rand -hex 16)
jwt=$(openssl rand -hex 32)
dbpw=$(openssl rand -hex 16)
rcp "${bin}/proxima-api" panel /tmp/proxima-api
rcp "${bin}/node-agent-linux-amd64" panel /tmp/node-agent-linux-amd64
rsh panel 'sudo bash -s' <<EOF
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq && apt-get install -y -qq docker.io jq curl >/dev/null
docker run -d --name pg --restart always -p 127.0.0.1:5432:5432 -e POSTGRES_PASSWORD=$dbpw -e POSTGRES_DB=proxima postgres:16-alpine
docker run -d --name redis --restart always -p 127.0.0.1:6379:6379 redis:7-alpine
mkdir -p /app/downloads
install -m 755 /tmp/proxima-api /usr/local/bin/proxima-api
install -m 755 /tmp/node-agent-linux-amd64 /app/downloads/node-agent-linux-amd64
cat >/etc/proxima-api.env <<ENV
DATABASE_URL=postgres://postgres:$dbpw@127.0.0.1:5432/proxima?sslmode=disable
REDIS_URL=redis://127.0.0.1:6379
JWT_SECRET=$jwt
ADMIN_EMAIL=admin@aws-e2e.test
ADMIN_PASSWORD=$admin_pw
PANEL_URL=http://$(priv panel):2053
SWAGGER_ENABLED=false
ENV
chmod 600 /etc/proxima-api.env
systemd-run --unit proxima-api --property=EnvironmentFile=/etc/proxima-api.env --property=Restart=always /usr/local/bin/proxima-api
for i in \$(seq 1 60); do curl -fsS http://127.0.0.1:2053/health >/dev/null && exit 0; sleep 2; done
journalctl -u proxima-api --no-pager | tail -40; exit 1
EOF
echo 'PASS: panel API healthy'

panel_url="http://$(priv panel):2053"
api() { # api METHOD PATH [TOKEN] [BODY]  (runs curl on the panel; API is VPC-only)
  local m=$1 p=$2 t=${3:-} b=${4:-} auth=''
  [[ -n "$t" ]] && auth="-H 'Authorization: Bearer $t'"
  # The body travels on stdin so it needs no shell quoting on the remote side.
  printf '%s' "$b" | rsh panel "curl -fsS -X $m $auth -H 'Content-Type: application/json' --data-binary @- 'http://127.0.0.1:2053$p'"
}
admin_token=$(api POST /api/v1/admin/auth/login '' "$(jq -nc --arg p "$admin_pw" '{email:"admin@aws-e2e.test",password:$p}')" | jq -er .token)

# --- Register exits through the real install.sh ------------------------------
register_node() {
  local vm=$1 name=$2 role=$3 port=$4 tok
  tok=$(api POST /api/v1/admin/nodes/token "$admin_token" "{\"role\":\"$role\",\"name\":\"$name\",\"port\":$port}" | jq -er .token)
  rcp "${root}/scripts/install.sh" "$vm" /tmp/install.sh
  rsh "$vm" "sudo bash /tmp/install.sh --server $panel_url --token $tok --name $name --port $port" >"${evidence}/install-$name.log" 2>&1 \
    || { tail -30 "${evidence}/install-$name.log" >&2; return 1; }
}
pget() { rsh panel "curl -fsS 'http://127.0.0.1:2053$1'"; }
register_node exit1 aws-exit-1 exit 8443
register_node exit2 aws-exit-2 exit 8443

nodes_ok() {
  api GET /api/v1/admin/nodes "$admin_token" | jq -e '[.[] | select(.name|startswith("aws-exit-")) | select(.status=="online")] | length == 2' >/dev/null
}
for _ in $(seq 1 60); do nodes_ok && break; sleep 3; done
nodes_ok || { api GET /api/v1/admin/nodes "$admin_token" >&2 || true; echo 'FAIL: both exits did not come online' >&2; exit 1; }
echo 'PASS: test 1 - both exit nodes registered and online'

api GET /api/v1/admin/nodes "$admin_token" >"${evidence}/nodes.json"
api GET /api/v1/admin/concurrency-enforcement "$admin_token" >"${evidence}/concurrency-enforcement.json"
jq -e '.enforced == true' "${evidence}/concurrency-enforcement.json" >/dev/null \
  && echo 'PASS: concurrency enforcement is on by default' \
  || { echo 'FAIL: concurrency enforcement is not on by default' >&2; exit 1; }

source "${here}/tests.sh"
failed=()
run_test() { echo "== $1 =="; "$1" || failed+=("$1"); }
setup_topology
run_test test_connectivity
run_test test_address_cap
run_test test_uuid_eviction
run_test test_stuck_pending
for vm in panel exit1 exit2 entry; do
  rsh "$vm" 'sudo journalctl --no-pager -u proxima-api -u node-agent 2>/dev/null | tail -300' >"${evidence}/journal-$vm.log" 2>&1 || true
done
if (( ${#failed[@]} > 0 )); then
  echo "FAILED: ${failed[*]}" >&2
  exit 1
fi
echo 'PASS: all AWS E2E tests passed'
