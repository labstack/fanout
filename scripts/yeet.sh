#!/bin/bash
set -euo pipefail

# Deploy fanout.run (marketing + docs site) and demo.fanout.run (live
# demo fed by otel-demo) to the target host. Single Docker Compose
# project — Caddy at the edge handles TLS for both hostnames and
# reverse-proxies to the fanout-site and fanout-demo containers.

DEFAULT_SERVER="ubuntu@fanout.run"
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
      echo "Deploy fanout.run + demo.fanout.run"
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
      echo "  $0 ubuntu@other.server --version v2026.04.2   # Deploy to a different host"
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
    sudo usermod -aG docker $USER
fi

sudo mkdir -p /data/caddy /data/caddy-config /data/fanout-demo /opt/fanout
sudo chown -R $USER:$USER /data /opt/fanout
SETUP_EOF

echo "Copying compose + Caddyfile + demo/..."
scp "$REPO_DIR/docker-compose.yaml" "$SERVER:$REMOTE_DIR/docker-compose.yaml"
scp "$REPO_DIR/Caddyfile"           "$SERVER:$REMOTE_DIR/Caddyfile"
# Recursive copy of the whole demo/ tree — scp without -r on a glob
# would silently skip any subdirectories that get added later.
scp -r "$REPO_DIR/demo" "$SERVER:$REMOTE_DIR/"

# Root .env — Caddy's TLS email and the fanout-site image tag.
# Demo-specific env has two consumers, both pointed at demo/.env:
#   1. Variable interpolation for the included compose file: the
#      root compose declares `include.env_file: demo/.env`, which
#      expands `${COLLECTOR_CONTRIB_IMAGE}` and friends at parse time.
#   2. Container runtime env: the demo `fanout` service has its own
#      `env_file: .env` (relative to demo/), which injects variables
#      into the container at runtime. Both hats live in the same file
#      — don't delete one thinking the other covers it.
printf 'LETSENCRYPT_EMAIL=%s\nVERSION=%s\n' "$EMAIL" "${VERSION:-latest}" \
  | ssh "$SERVER" "cat > $REMOTE_DIR/.env && chmod 600 $REMOTE_DIR/.env"

echo "Deploying..."
ssh "$SERVER" "
set -euo pipefail
cd $REMOTE_DIR

docker compose pull
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

echo ""
echo "Deployed:"
echo "  https://fanout.run"
echo "  https://demo.fanout.run"
echo ""
echo "Status: ssh $SERVER 'cd $REMOTE_DIR && docker compose ps'"
echo "Logs:   ssh $SERVER 'cd $REMOTE_DIR && docker compose logs -f caddy'"
