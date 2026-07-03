#!/bin/bash
set -euo pipefail

# Deploy fanout.run (marketing + docs site), demo.fanout.run (live demo
# fed by otel-demo), and fanout.labstack.com (own production instance) to
# the target host. Single Docker Compose project — Traefik at the edge
# handles TLS for four hostnames (fanout.run, demo.fanout.run,
# fanout.labstack.com web, ingest.fanout.labstack.com OTLP gRPC) and
# reverse-proxies to the `site`, `demo`, and `fanout` containers.

DEFAULT_SERVER="root@fanout.labstack.net"

SERVER=""
SITE_VERSION=""
FANOUT_VERSION=""

while [[ $# -gt 0 ]]; do
  case $1 in
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
      echo "  --site-version VERSION       site image tag (default: latest site/v* tag)"
      echo "  --fanout-version VERSION     fanout + demo image tag (default: latest fanout/v* tag)"
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

[[ -z "$SERVER" ]] && SERVER="$DEFAULT_SERVER"

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REMOTE_DIR="/opt/fanout"

# Refresh tags from origin so the resolver sees what teammates have pushed —
# otherwise a stale local repo silently deploys yesterday's release. If the
# fetch fails (no origin, auth, offline), print a warning and fall through to
# whatever's local: the banner will still show which tag was resolved, so the
# operator can compare against `git ls-remote --tags origin` and abort.
if ! fetch_err=$(git -C "$REPO_DIR" fetch --tags --quiet --force 2>&1); then
  echo "WARN: git fetch --tags failed; resolver will use local tags only." >&2
  echo "  $fetch_err" >&2
fi

# resolve_version PREFIX EXPLICIT — print "<source>\t<image-tag>" for a service.
# - If EXPLICIT is set, validate against the stable-release regex (same shape
#   the auto-resolve path uses, so an explicit pre-release tag is rejected
#   too). Strips a leading `v`. Source becomes the literal string "explicit".
# - Otherwise, return the highest stable <PREFIX>/v* git tag, excluding
#   pre-release suffixes (-rc/-beta/etc.) — same filter `just release` uses
#   when minting new tag numbers (see justfile `next_tag()`).
#   Source is the matching git tag (e.g. "fanout/v2026.05.2").
# Both fields land on the same line, tab-separated, so a single call captures
# them via `IFS=$'\t' read -r SRC VER < <(resolve_version ...)`.
resolve_version() {
  local prefix="$1" explicit="${2:-}" stripped tag
  if [[ -n "$explicit" ]]; then
    stripped="${explicit#v}"
    # Reject pre-release suffixes on the explicit path too. An operator who
    # truly wants to ship an rc/beta to prod must add a flag later (we don't
    # have one yet — the symmetric refusal is the safer default).
    if [[ ! "$stripped" =~ ^[0-9]{4}\.[0-9]{2}\.[0-9]+$ ]]; then
      echo "ERROR: --${prefix}-version value '$explicit' doesn't look like a stable release (e.g. 2026.05.1)." >&2
      echo "  Pre-release tags (-rc/-beta/etc.) are not accepted." >&2
      exit 1
    fi
    printf '%s\t%s\n' "explicit" "$stripped"
    return
  fi
  # Note: { grep || true; } in the pipeline so grep's exit 1 on "no match"
  # doesn't trip set -e/pipefail before the friendly error below can fire.
  tag=$(git -C "$REPO_DIR" tag --list "${prefix}/v*" --sort=-v:refname \
          | { grep -E "^${prefix}/v[0-9]{4}\.[0-9]{2}\.[0-9]+$" || true; } \
          | head -1)
  if [[ -z "$tag" ]]; then
    echo "ERROR: no stable ${prefix}/v* tag found locally; run 'just release' or pass --${prefix}-version <value>." >&2
    exit 1
  fi
  printf '%s\t%s\n' "$tag" "${tag#${prefix}/v}"
}

IFS=$'\t' read -r SITE_SOURCE   SITE_VERSION   < <(resolve_version site   "$SITE_VERSION")
IFS=$'\t' read -r FANOUT_SOURCE FANOUT_VERSION < <(resolve_version fanout "$FANOUT_VERSION")

# Preflight — three .env files (gitignored) hold per-service config +
# credentials. scp would silently skip a missing file and the failure
# would only surface on the server as a cryptic "env file not found"
# during docker compose up. Fail fast here with a pointer.
for dir in demo fanout traefik; do
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
# CF token preflight — file exists, key non-empty. lego (Traefik's bundled
# ACME client) validates the token + its scope on first ACME run; errors
# land cleanly in `docker compose logs -f traefik` if anything's wrong.
if ! grep -qE '^CF_DNS_API_TOKEN=.+' "$REPO_DIR/traefik/.env"; then
  echo "ERROR: CF_DNS_API_TOKEN missing or empty in traefik/.env." >&2
  exit 1
fi

echo "Deploying to $SERVER"
echo "  site:    $SITE_VERSION  ($SITE_SOURCE)"
echo "  fanout:  $FANOUT_VERSION  ($FANOUT_SOURCE)"
echo ""

echo "Testing SSH..."
# Capture stderr instead of discarding it: with CI's pinned known_hosts, a
# host-key mismatch (stale DEPLOY_KNOWN_HOSTS after a host rebuild — or a
# MITM) lands here, and "REMOTE HOST IDENTIFICATION HAS CHANGED" needs a
# different remediation than a network blip or a revoked deploy key.
if ! err=$(ssh -o ConnectTimeout=10 -o StrictHostKeyChecking=accept-new "$SERVER" true 2>&1 >/dev/null); then
    echo "ERROR: SSH connection to $SERVER failed:" >&2
    printf '%s\n' "$err" | tail -5 >&2
    echo "  (in CI, a host-key mismatch means DEPLOY_KNOWN_HOSTS is stale — refresh: ssh-keyscan fanout.labstack.net | gh secret set DEPLOY_KNOWN_HOSTS)" >&2
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

sudo mkdir -p /data/traefik/letsencrypt /data/fanout-demo /data/fanout /opt/fanout
sudo touch /data/traefik/letsencrypt/acme.json
sudo chmod 600 /data/traefik/letsencrypt/acme.json
sudo chown -R "$(id -un):$(id -un)" /data /opt/fanout
SETUP_EOF

echo "Copying compose + traefik/ + demo/ + fanout/..."
scp "$REPO_DIR/docker-compose.yaml" "$SERVER:$REMOTE_DIR/docker-compose.yaml"
# Recursive copy of each service tree — scp without -r on a glob would
# silently skip subdirectories. Each tree carries its own .env (gitignored,
# loaded by env_file: in compose) and .env.example (committed template).
scp -r "$REPO_DIR/traefik" "$SERVER:$REMOTE_DIR/"
scp -r "$REPO_DIR/demo"    "$SERVER:$REMOTE_DIR/"
scp -r "$REPO_DIR/fanout"  "$SERVER:$REMOTE_DIR/"

# Root .env on the host — image tags for docker-compose.yaml interpolation.
# Per-service secrets live in <service>/.env (scp'd above) and are loaded
# via env_file: in compose, not from this root file. SITE_VERSION and
# FANOUT_VERSION resolve from their own git-tag namespaces (site/v* and
# fanout/v*); compose enforces non-empty via ${VAR:?...} so a missing one
# fails parse, not later as a confusing "manifest unknown" pull error.
printf 'SITE_VERSION=%s\nFANOUT_VERSION=%s\n' \
    "$SITE_VERSION" "$FANOUT_VERSION" \
  | ssh "$SERVER" "cat > $REMOTE_DIR/.env && chmod 600 $REMOTE_DIR/.env"

echo "Deploying..."
# NOTE: the SSH command is double-quoted, so backticks inside this heredoc
# would trigger LOCAL command substitution. Phrase comments without backticks.
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
# Traefik's first-boot cert issuance can take 30-60s after it comes up.
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
# TLS-only handshake check (no HTTP) — for the OTLP gRPC ingest host on :443.
# openssl completes the TLS handshake before any gRPC frames are exchanged,
# so it catches the failure modes that matter (DNS, port reachability,
# Traefik routing for the SNI host, LE cert issued for the right name).
# Asserts:
#   - cert verifies (chain back to LE)
#   - served cert CN matches the expected hostname (not Traefik's default)
tls_smoke() {
  local host="$1" port="$2" out
  for _ in 1 2 3 4 5 6; do
    out=$(echo | openssl s_client -connect "$host:$port" -servername "$host" -verify_return_error 2>&1) || true
    # Subject formatting is toolchain-dependent: macOS LibreSSL prints
    # "subject=CN=host", the ubuntu runner's OpenSSL 3 prints
    # "subject=CN = host" (first CI deploy false-FAILed on this). Allow
    # optional spaces around '=' so both pass.
    if echo "$out" | grep -q "Verify return code: 0 (ok)" \
       && echo "$out" | grep -Eq "subject=.*CN *= *$host"; then
      echo "  OK  tls $host:$port"
      return 0
    fi
    sleep 10
  done
  echo "  FAIL tls $host:$port" >&2
  echo "$out" | grep -E "Verify return code|subject=" | head -3 >&2
  return 1
}
# Aggregate failures so one bad URL doesn't mask the others. Three hosts
# share one Traefik + one ACME process, so they tend to fail together —
# reporting the first and quitting paints a misleading partial-outage
# picture exactly when the operator needs the full view.
failed=0
smoke https://fanout.run                          || failed=1
smoke https://demo.fanout.run                     || failed=1
smoke https://fanout.labstack.com/healthz         || failed=1
tls_smoke ingest.fanout.labstack.com 443          || failed=1
if (( failed )); then
  echo "ERROR: one or more smoke checks failed; inspect 'docker compose logs -f traefik' on the host." >&2
  exit 1
fi

echo ""
echo "Pruning old images..."
# Only after the smoke tests pass — a failed deploy keeps every image around
# for rollback. until=168h keeps the last week of releases for the same
# reason (the filter is on image CREATION time, so an unused third-party pin
# whose upstream build is older than a week gets pruned right after a pin
# bump — rollback of a pin then means a re-pull). Build cache goes entirely:
# this host pulls, it never builds.
# Non-fatal: the deploy is already live and smoke-tested at this point, so a
# prune hiccup is a housekeeping problem, not a deploy failure — don't let
# set -e turn it into a red deploy. The next deploy retries the prune.
if ! ssh "$SERVER" 'docker image prune -af --filter "until=168h" && docker builder prune -af' | tail -2; then
  prune_warn="image/builder prune failed — deploy is live; disk housekeeping skipped this run"
  echo "WARN: $prune_warn" >&2
  # In CI a stderr WARN inside a green run is never seen — surface it as a
  # run annotation so a prune that fails every deploy eventually gets noticed.
  if [[ -n "${GITHUB_ACTIONS:-}" ]]; then
    echo "::warning::$prune_warn"
  fi
fi

echo ""
echo "Deployed:"
echo "  https://fanout.run"
echo "  https://demo.fanout.run"
echo "  https://fanout.labstack.com"
echo "  https://ingest.fanout.labstack.com  (OTLP gRPC, TLS on 443)"
echo ""
echo "Status: ssh $SERVER 'cd $REMOTE_DIR && docker compose ps'"
echo "Logs:   ssh $SERVER 'cd $REMOTE_DIR && docker compose logs -f traefik'"
