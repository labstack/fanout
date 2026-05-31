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
SITE_VERSION=""
FANOUT_VERSION=""

while [[ $# -gt 0 ]]; do
  case $1 in
    --email)
      EMAIL="$2"
      shift 2
      ;;
    --site-version)
      SITE_VERSION="$2"
      shift 2
      ;;
    --fanout-version)
      FANOUT_VERSION="$2"
      shift 2
      ;;
    --help)
      echo "Deploy fanout.run + demo.fanout.run + fanout.labstack.com"
      echo ""
      echo "Usage: $0 [user@]server [options]"
      echo ""
      echo "Options:"
      echo "  --email EMAIL                Let's Encrypt contact (default: $EMAIL)"
      echo "  --site-version VERSION       fanout-site image tag (default: latest site/v* tag)"
      echo "  --fanout-version VERSION     fanout + fanout-demo image tag (default: latest fanout/v* tag)"
      echo "  --help                       Show this help"
      echo ""
      echo "Examples:"
      echo "  $0                                                # Auto-resolve both versions from git tags"
      echo "  $0 --fanout-version 2026.05.2                     # Pin fanout, auto-resolve site"
      echo "  $0 root@other.server --site-version 2026.05.1     # Deploy to a different host"
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
[[ -z "$SERVER" ]] && SERVER="$DEFAULT_SERVER"

# Auto-resolve per-service versions from git tags when not passed (mirrors
# monk's pattern). `<service>/v*` tags are cut by `just release`. The leading
# `v` and the `<service>/` prefix are both stripped so the value matches the
# Docker image tag exactly.
latest_tag_version() {
  local prefix="$1"
  local tag
  tag=$(git -C "$(dirname "$0")/.." tag --list "${prefix}/v*" --sort=-v:refname | head -1)
  if [[ -z "$tag" ]]; then
    echo "ERROR: no ${prefix}/v* tag found locally; pass --${prefix}-version explicitly or run 'just release'." >&2
    exit 1
  fi
  echo "${tag#${prefix}/v}"
}

[[ -z "$SITE_VERSION"   ]] && SITE_VERSION=$(latest_tag_version site)
[[ -z "$FANOUT_VERSION" ]] && FANOUT_VERSION=$(latest_tag_version fanout)
# Tolerate explicit `--site-version v2026.05.1` (strip leading v).
SITE_VERSION="${SITE_VERSION#v}"
FANOUT_VERSION="${FANOUT_VERSION#v}"

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
echo "  site:    $SITE_VERSION"
echo "  fanout:  $FANOUT_VERSION"
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
# SITE_VERSION (fanout-site) and FANOUT_VERSION (fanout + fanout-demo)
# are resolved separately above — each from its own git-tag namespace
# (site/v* and fanout/v*). docker-compose.yaml + demo/docker-compose.yaml
# enforce non-empty via ${VAR:?...} so a missing one fails compose parse,
# not later as a confusing "manifest unknown" pull error.
printf 'LETSENCRYPT_EMAIL=%s\nSITE_VERSION=%s\nFANOUT_VERSION=%s\n' \
    "$EMAIL" "$SITE_VERSION" "$FANOUT_VERSION" \
  | ssh "$SERVER" "cat > $REMOTE_DIR/.env && chmod 600 $REMOTE_DIR/.env"

echo "Deploying..."
# NOTE: the SSH command is double-quoted, so backticks inside this heredoc
# would trigger LOCAL command substitution. Phrase comments without backticks.
ssh "$SERVER" "
set -euo pipefail
cd $REMOTE_DIR

# Rebuild caddy from Dockerfile.caddy on every deploy; plain pull alone
# would not catch local context changes (ghcr image-only services pull;
# the build-context caddy service must be rebuilt explicitly). The
# build --pull flag refreshes the caddy:NN-builder base too, so upstream
# security fixes flow in without bumping the pinned tag.
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
