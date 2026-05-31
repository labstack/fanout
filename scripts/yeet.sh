#!/bin/bash
set -euo pipefail

# Deploy fanout.run (marketing + docs site), demo.fanout.run (live demo
# fed by otel-demo), and fanout.labstack.com (own production instance) to
# the target host. Single Docker Compose project — Caddy at the edge
# handles TLS for all three hostnames and reverse-proxies to the
# fanout-site, fanout-demo, and fanout containers.

DEFAULT_SERVER="root@fanout.labstack.net"
EMAIL="v@labstack.com"

SERVER=""
VERSION=""

while [[ $# -gt 0 ]]; do
  case $1 in
    --email)
      EMAIL="$2"
      shift 2
      ;;
    --version)
      VERSION="$2"
      shift 2
      ;;
    --help)
      echo "Deploy fanout.run + demo.fanout.run + fanout.labstack.com"
      echo ""
      echo "Usage: $0 [user@]server [options]"
      echo ""
      echo "Options:"
      echo "  --email EMAIL       Let's Encrypt contact (default: $EMAIL)"
      echo "  --version VERSION   fanout-site image tag (default: latest)"
      echo "  --help              Show this help"
      echo ""
      echo "Examples:"
      echo "  $0                                            # Deploy :latest to $DEFAULT_SERVER"
      echo "  $0 --version v2026.04.2                       # Pin a release tag"
      echo "  $0 root@other.server --version v2026.05.2    # Deploy to a different host"
      exit 0
      ;;
    *)
      if [[ "$1" =~ ^[^@]+@.+ ]] || [[ "$1" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || [[ "$1" =~ ^[a-zA-Z0-9.-]+$ ]]; then
        SERVER="$1"
        shift
      else
        echo "Unknown option: $1"
        exit 1
      fi
      ;;
  esac
done

[[ -n "${LETSENCRYPT_EMAIL:-}" ]] && EMAIL="$LETSENCRYPT_EMAIL"
VERSION="${VERSION#v}"
[[ -z "$SERVER" ]] && SERVER="$DEFAULT_SERVER"

REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"
REMOTE_DIR="/opt/fanout"

# Preflight — both .env.secrets files (gitignored) hold per-instance
# JWT/SMTP/AI credentials. scp would silently skip a missing file and
# the failure would only surface on the server as a cryptic "env file
# not found" during docker compose up. Fail fast here with a pointer.
if [[ ! -f "$REPO_DIR/demo/.env.secrets" ]]; then
  echo "ERROR: demo/.env.secrets not found locally." >&2
  echo "  Copy demo/.env.secrets.sample to demo/.env.secrets and fill in real values." >&2
  exit 1
fi
if [[ ! -f "$REPO_DIR/instance/.env.secrets" ]]; then
  echo "ERROR: instance/.env.secrets not found locally." >&2
  echo "  Copy instance/.env.secrets.example to instance/.env.secrets and fill in real values." >&2
  exit 1
fi
# Caddy's ACME DNS-01 challenge needs a Cloudflare API token with
# `Zone.Zone:Read` + `Zone.DNS:Edit` on both fanout.run and labstack.com.
# Without it, cert issuance fails on every site and the deploy comes up
# with no TLS. Fail fast here with the same shape as the .env.secrets check.
if [[ -z "${CF_API_TOKEN:-}" ]]; then
  echo "ERROR: CF_API_TOKEN environment variable not set." >&2
  echo "  Create a scoped Cloudflare API token (Zone.Zone:Read + Zone.DNS:Edit on fanout.run and labstack.com)" >&2
  echo "  at https://dash.cloudflare.com/profile/api-tokens, then:" >&2
  echo "    export CF_API_TOKEN=<token>" >&2
  exit 1
fi
# Validate the token against Cloudflare's verify endpoint. Without this,
# a typo'd / revoked / wrong-scope token deploys "successfully" against
# whatever cached cert Caddy has on disk, then breaks weeks later when
# renewal fails. This catches the empty-token's siblings:
#   - typo, revoked, expired, wrong-account, wrong-zone scope.
# (It does NOT prove the token has the *right* scope — only that it's
# accepted by Cloudflare's auth layer. The first ACME run still surfaces
# scope errors via Caddy logs.)
echo "Validating CF_API_TOKEN..."
verify=$(curl -fsS --max-time 10 \
  -H "Authorization: Bearer $CF_API_TOKEN" \
  https://api.cloudflare.com/client/v4/user/tokens/verify 2>&1) || {
  echo "ERROR: Cloudflare rejected CF_API_TOKEN: $verify" >&2
  exit 1
}
if ! echo "$verify" | grep -q '"status":"active"'; then
  echo "ERROR: CF_API_TOKEN is not active: $verify" >&2
  exit 1
fi

echo "Deploying to $SERVER"
echo "  version: ${VERSION:-latest}"
echo "  email:   $EMAIL"
echo ""

echo "Testing SSH..."
if ! ssh -o ConnectTimeout=10 -o StrictHostKeyChecking=accept-new "$SERVER" "echo 'OK'" > /dev/null 2>&1; then
    echo "SSH connection failed"
    exit 1
fi

echo "Setting up server..."
ssh "$SERVER" 'bash -s' << 'SETUP_EOF'
set -euo pipefail
sudo apt update && sudo apt install -y curl wget

if ! command -v docker &> /dev/null; then
    echo "Installing Docker..."
    tmp=$(mktemp)
    curl -fsSL https://get.docker.com -o "$tmp"
    sudo sh "$tmp"
    rm -f "$tmp"
    sudo systemctl enable docker
    sudo systemctl start docker
    # usermod -aG docker only matters for non-root deploy users — skip it
    # when the deploy SSH user is root (current default: root@fanout.labstack.net).
    if [[ "$(id -un)" != "root" ]]; then
        sudo usermod -aG docker "$(id -un)"
    fi
fi

sudo mkdir -p /data/caddy /data/caddy-config /data/fanout-demo /data/fanout /opt/fanout
sudo chown -R "$(id -un):$(id -un)" /data /opt/fanout
SETUP_EOF

echo "Copying compose + Caddyfile + demo/ + instance/..."
scp "$REPO_DIR/docker-compose.yaml" "$SERVER:$REMOTE_DIR/docker-compose.yaml"
scp "$REPO_DIR/Caddyfile"           "$SERVER:$REMOTE_DIR/Caddyfile"
# Recursive copy of the whole demo/ tree — scp without -r on a glob
# would silently skip any subdirectories that get added later.
scp -r "$REPO_DIR/demo"     "$SERVER:$REMOTE_DIR/"
scp -r "$REPO_DIR/instance" "$SERVER:$REMOTE_DIR/"

# Root .env — Caddy's TLS email and the fanout-site image tag.
# Demo-specific env has two consumers, both pointed at demo/.env:
#   1. Variable interpolation for the included compose file: the
#      root compose declares `include.env_file: demo/.env`, which
#      expands `${COLLECTOR_CONTRIB_IMAGE}` and friends at parse time.
#   2. Container runtime env: the demo `fanout` service has its own
#      `env_file: .env` (relative to demo/), which injects variables
#      into the container at runtime. Both hats live in the same file
#      — don't delete one thinking the other covers it.
printf 'LETSENCRYPT_EMAIL=%s\nVERSION=%s\nFANOUT_VERSION=%s\nCF_API_TOKEN=%s\n' \
    "$EMAIL" "${VERSION:-latest}" "${FANOUT_VERSION:-latest}" "$CF_API_TOKEN" \
  | ssh "$SERVER" "cat > $REMOTE_DIR/.env && chmod 600 $REMOTE_DIR/.env"

echo "Deploying..."
ssh "$SERVER" "
set -euo pipefail
cd $REMOTE_DIR

# --build forces the caddy service to rebuild when Dockerfile.caddy or its
# context changes — without it, docker compose pull only fetches image:
# services and a stale custom-built caddy stays in the local cache silently.
# --pull always also ensures the caddy:<version>-builder base gets refreshed.
docker compose pull
docker compose build --pull caddy
# --wait blocks until all services with healthchecks are healthy (or
# the timeout elapses), so a success exit actually reflects a working
# deploy. Services without healthchecks only need to start.
docker compose up -d --build --wait --wait-timeout 180 --remove-orphans

echo ''
docker compose ps
"

echo ""
echo "Smoke test..."
# Caddy's first-boot cert issuance can take 30-60s after it comes up.
# Retry a handful of times; bail only if it still fails.
smoke() {
  local url="$1"
  for _ in 1 2 3 4 5 6; do
    if curl -fsS --max-time 10 -o /dev/null "$url"; then
      echo "  OK  $url"
      return 0
    fi
    sleep 10
  done
  echo "  FAIL $url" >&2
  return 1
}
smoke https://fanout.run
smoke https://demo.fanout.run
smoke https://fanout.labstack.com/healthz

echo ""
echo "Deployed:"
echo "  https://fanout.run"
echo "  https://demo.fanout.run"
echo "  https://fanout.labstack.com"
echo ""
echo "Status: ssh $SERVER 'cd $REMOTE_DIR && docker compose ps'"
echo "Logs:   ssh $SERVER 'cd $REMOTE_DIR && docker compose logs -f caddy'"
