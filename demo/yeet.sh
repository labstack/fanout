#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
DEFAULT_SERVER="ubuntu@fanout.run"
REMOTE_DIR="/opt/fanout/demo"

# Parse args
SERVER=""
while [[ $# -gt 0 ]]; do
  case $1 in
    --help)
      echo "Deploy fanout-demo (otel-demo + fanout)"
      echo ""
      echo "Usage: $0 [user@server]"
      echo ""
      echo "Default server: $DEFAULT_SERVER"
      exit 0
      ;;
    --*)
      echo "Unknown option: $1"
      echo "Run '$0 --help' for usage."
      exit 1
      ;;
    *)
      if [[ -n "$SERVER" ]]; then
        echo "Multiple servers specified: '$SERVER' and '$1'"
        exit 1
      fi
      SERVER="$1"
      shift
      ;;
  esac
done

[[ -z "$SERVER" ]] && SERVER="$DEFAULT_SERVER"

# Verify required files exist
for f in docker-compose.yaml .env otelcol-config.yaml; do
  if [[ ! -f "$SCRIPT_DIR/$f" ]]; then
    echo "Required file not found: $SCRIPT_DIR/$f"
    exit 1
  fi
done
for d in flagd products; do
  if [[ ! -d "$SCRIPT_DIR/$d" ]]; then
    echo "Required directory not found: $SCRIPT_DIR/$d"
    exit 1
  fi
done

echo "Deploying fanout-demo to $SERVER..."

# Test SSH
echo "Testing SSH..."
if ! ssh -o ConnectTimeout=10 -o StrictHostKeyChecking=accept-new "$SERVER" "echo 'OK'" > /dev/null; then
  echo "SSH connection to $SERVER failed (see error above)"
  exit 1
fi

# Setup directories
echo "Setting up server..."
ssh "$SERVER" "sudo mkdir -p /data/fanout-demo $REMOTE_DIR && sudo chown -R \$USER:\$USER /data/fanout-demo $REMOTE_DIR"

# Copy files
echo "Copying config..."
scp "$SCRIPT_DIR/docker-compose.yaml" "$SERVER:$REMOTE_DIR/docker-compose.yaml"
scp "$SCRIPT_DIR/.env" "$SERVER:$REMOTE_DIR/.env"
scp "$SCRIPT_DIR/otelcol-config.yaml" "$SERVER:$REMOTE_DIR/otelcol-config.yaml"

echo "Copying data..."
ssh "$SERVER" "mkdir -p $REMOTE_DIR/flagd $REMOTE_DIR/products"
scp "$SCRIPT_DIR/flagd/"* "$SERVER:$REMOTE_DIR/flagd/"
scp "$SCRIPT_DIR/products/"* "$SERVER:$REMOTE_DIR/products/"

# Deploy
echo "Deploying..."
ssh "$SERVER" "
set -e
cd '$REMOTE_DIR'

if ! docker network inspect webproxy >/dev/null 2>&1; then
  docker network create webproxy
fi

docker compose pull
docker compose up -d --wait --wait-timeout 120

echo ''
echo 'Deployment complete!'
docker compose ps
"

echo ""
echo "YEET SUCCESSFUL!"
echo "https://demo.fanout.run"
echo "Status: ssh $SERVER 'cd $REMOTE_DIR && docker compose ps'"
echo "Logs:   ssh $SERVER 'cd $REMOTE_DIR && docker compose logs -f fanout'"
