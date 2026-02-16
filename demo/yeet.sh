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
    *)
      SERVER="$1"
      shift
      ;;
  esac
done

[[ -z "$SERVER" ]] && SERVER="$DEFAULT_SERVER"

echo "Deploying fanout-demo to $SERVER..."

# Test SSH
echo "Testing SSH..."
if ! ssh -o ConnectTimeout=10 -o StrictHostKeyChecking=accept-new "$SERVER" "echo 'OK'" > /dev/null 2>&1; then
  echo "SSH connection failed"
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
scp -r "$SCRIPT_DIR/flagd" "$SERVER:$REMOTE_DIR/flagd"
scp -r "$SCRIPT_DIR/products" "$SERVER:$REMOTE_DIR/products"

# Deploy
echo "Deploying..."
ssh "$SERVER" "
set -e
cd $REMOTE_DIR

docker network create webproxy 2>/dev/null || true

docker compose pull
docker compose up -d

echo ''
echo 'Deployment complete!'
docker compose ps
"

echo ""
echo "YEET SUCCESSFUL!"
echo "https://demo.fanout.run"
echo "Status: ssh $SERVER 'cd $REMOTE_DIR && docker compose ps'"
echo "Logs:   ssh $SERVER 'cd $REMOTE_DIR && docker compose logs -f fanout'"
