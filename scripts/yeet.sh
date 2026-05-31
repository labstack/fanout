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

# Preflight — three .env files (gitignored) hold per-service config +
# credentials. scp would silently skip a missing file and the failure
# would only surface on the server as a cryptic "env file not found"
# during docker compose up. Fail fast here with a pointer.
for dir in demo fanout caddy; do
  if [[ ! -f "$REPO_DIR/$dir/.env" ]]; then
    echo "ERROR: $dir/.env not found locally." >&2
    echo "  Copy $dir/.env.example to $dir/.env and fill in real values." >&2
    exit 1
  fi
  # Catch the "cp .env.example .env; forgot to edit" case — the templates
  # use `replace-with-` placeholders. Shipping those = known-public JWT
  # signing keys in production. Length checks alone don't catch them
  # (the placeholders are >32 chars).
  if grep -qE 'replace-with-|<your-' "$REPO_DIR/$dir/.env"; then
    echo "ERROR: $dir/.env still contains template placeholders." >&2
    echo "  Open $dir/.env and replace every 'replace-with-…' value." >&2
    exit 1
  fi
done
# Validate the Cloudflare token against the verify endpoint before deploy.
# Without this, a typo'd / revoked / wrong-account / expired token reaches
# Caddy unchecked and the failure mode is "deploy succeeds against a cached
# cert, then breaks weeks later when renewal fails." This catches the empty-
# token's siblings. (Does NOT prove the token has the *right* scope — only
# that it's accepted by Cloudflare's auth layer. Scope errors still surface
# via Caddy logs on the first ACME run.)
CF_API_TOKEN=$(grep -E '^CF_API_TOKEN=' "$REPO_DIR/caddy/.env" | tail -1 | cut -d= -f2-)
# Sanitize common .env quirks before the value lands in a curl Bearer header:
#   - CRLF line endings (file edited on Windows)
#   - Single or double-quoted values
#   - Surrounding whitespace
# Docker Compose's env_file parser handles these silently; grep|cut does not.
CF_API_TOKEN="${CF_API_TOKEN%$'\r'}"
CF_API_TOKEN="${CF_API_TOKEN#\"}"; CF_API_TOKEN="${CF_API_TOKEN%\"}"
CF_API_TOKEN="${CF_API_TOKEN#\'}"; CF_API_TOKEN="${CF_API_TOKEN%\'}"
CF_API_TOKEN="${CF_API_TOKEN## }"; CF_API_TOKEN="${CF_API_TOKEN%% }"
if [[ -z "$CF_API_TOKEN" ]]; then
  echo "ERROR: CF_API_TOKEN not set in caddy/.env." >&2
  exit 1
fi
echo "Validating CF_API_TOKEN against Cloudflare..."
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

echo "Copying compose + Caddyfile + Dockerfile.caddy + caddy/ + demo/ + fanout/..."
scp "$REPO_DIR/docker-compose.yaml" "$SERVER:$REMOTE_DIR/docker-compose.yaml"
scp "$REPO_DIR/Caddyfile"           "$SERVER:$REMOTE_DIR/Caddyfile"
scp "$REPO_DIR/Dockerfile.caddy"    "$SERVER:$REMOTE_DIR/Dockerfile.caddy"
# Recursive copy of each service tree — scp without -r on a glob would
# silently skip subdirectories. Each tree carries its own .env (gitignored,
# loaded by env_file: in compose) and .env.example (committed template).
scp -r "$REPO_DIR/caddy"  "$SERVER:$REMOTE_DIR/"
scp -r "$REPO_DIR/demo"   "$SERVER:$REMOTE_DIR/"
scp -r "$REPO_DIR/fanout" "$SERVER:$REMOTE_DIR/"

# Root .env on the host — Caddy's TLS email + the image tags for compose
# interpolation in docker-compose.yaml. Per-service secrets live in
# <service>/.env (scp'd above) and are loaded via env_file: in compose,
# not from this root file.
# FANOUT_VERSION defaults to VERSION so `--version 2026.05.2` pins both
# the fanout-site image AND the instance + demo containers to the same
# tag. Operators can still override with `FANOUT_VERSION=… ./scripts/yeet.sh`
# if they need to ship a site update without bumping the instance.
printf 'LETSENCRYPT_EMAIL=%s\nVERSION=%s\nFANOUT_VERSION=%s\n' \
    "$EMAIL" "${VERSION:-latest}" "${FANOUT_VERSION:-${VERSION:-latest}}" \
  | ssh "$SERVER" "cat > $REMOTE_DIR/.env && chmod 600 $REMOTE_DIR/.env"

echo "Deploying..."
ssh "$SERVER" "
set -euo pipefail
cd $REMOTE_DIR

# Rebuild caddy from Dockerfile.caddy on every deploy; `pull` alone wouldn't
# catch local context changes. The `--pull` flag on `build` refreshes the
# `caddy:<version>-builder` base too, so we pick up upstream security fixes
# without needing to bump the pinned tag.
docker compose pull
docker compose build --pull caddy
# --wait blocks until all services with healthchecks are healthy (or
# the timeout elapses), so a success exit actually reflects a working
# deploy. Services without healthchecks only need to start.
docker compose up -d --wait --wait-timeout 180 --remove-orphans

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
