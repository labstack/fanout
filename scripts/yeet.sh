#!/bin/bash
set -e

# Deploy the fanout.run marketing + docs site.
#
# Deploys the root docker-compose.yaml stack (nginx-proxy + acme-companion +
# fanout-site) to the target host. The demo stack at demo.fanout.run is
# deployed separately via ./demo/yeet.sh and joins the same `webproxy`
# network stood up here — run this first on a fresh host; the demo
# deploy depends on nginx-proxy + acme-companion + the webproxy network
# created here.

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
      echo "Deploy fanout.run (site only)"
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

[[ -n "$LETSENCRYPT_EMAIL" ]] && EMAIL="$LETSENCRYPT_EMAIL"
VERSION="${VERSION#v}"
[[ -z "$SERVER" ]] && SERVER="$DEFAULT_SERVER"

echo "Deploying fanout.run site to $SERVER"
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
    # Download the installer to a file first; `curl -fsSL | sh` hides a
    # mid-body curl failure behind sh's exit code even with pipefail.
    tmp=$(mktemp)
    curl -fsSL https://get.docker.com -o "$tmp"
    sudo sh "$tmp"
    rm -f "$tmp"
    sudo systemctl enable docker
    sudo systemctl start docker
    sudo usermod -aG docker $USER
fi

sudo mkdir -p /data/ssl /data/nginx /data/html /data/acme
sudo chown -R $USER:$USER /data
sudo mkdir -p /opt/fanout
sudo chown -R $USER:$USER /opt/fanout
SETUP_EOF

echo "Copying compose..."
REPO_DIR="$(dirname "$0")/.."
scp "$REPO_DIR/docker-compose.yaml" "$SERVER:/opt/fanout/docker-compose.yaml"

# Minimal .env — nginx-proxy + acme-companion need LETSENCRYPT_EMAIL;
# fanout-site is self-contained. Written server-side (no repo secrets).
printf 'LETSENCRYPT_EMAIL=%s\nVERSION=%s\n' "$EMAIL" "${VERSION:-latest}" \
  | ssh "$SERVER" 'cat > /opt/fanout/.env && chmod 600 /opt/fanout/.env'

echo "Deploying..."
ssh "$SERVER" "
set -e
cd /opt/fanout

docker network create webproxy 2>/dev/null || true

docker compose pull
# --remove-orphans cleans up the old fanout admin container from the
# previous compose shape; without this, nginx-proxy sees two containers
# claiming fanout.run and routing is non-deterministic.
docker compose up -d --remove-orphans

echo ''
docker compose ps
"

echo ""
echo "Deployed: https://fanout.run"
echo "Status:   ssh $SERVER 'cd /opt/fanout && docker compose ps'"
echo "Logs:     ssh $SERVER 'cd /opt/fanout && docker compose logs -f fanout-site'"
