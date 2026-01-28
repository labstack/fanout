#!/bin/bash
set -e

# Configuration
DEFAULT_SERVER="ubuntu@fanout.run"
EMAIL="v@labstack.com"

# Parse command line arguments
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
      echo "🚀 YEET - Deploy Fanout"
      echo ""
      echo "Usage: $0 [user@]server [options]"
      echo ""
      echo "Options:"
      echo "  --email EMAIL       Let's Encrypt email (default: $EMAIL)"
      echo "  --version VERSION   Docker image version (default: latest)"
      echo "  --help              Show this help"
      echo ""
      echo "Examples:"
      echo "  $0                                    # Deploy to $DEFAULT_SERVER"
      echo "  $0 --version v1.0.0                   # Deploy specific version"
      echo "  $0 ubuntu@other.server --version v1.0.0 # Deploy to different server"
      exit 0
      ;;
    *)
      if [[ "$1" =~ ^[^@]+@.+ ]] || [[ "$1" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || [[ "$1" =~ ^[a-zA-Z0-9.-]+$ ]]; then
        SERVER="$1"
        shift
      else
        echo "❌ Unknown option: $1"
        exit 1
      fi
      ;;
  esac
done

# Use environment variable overrides
[[ -n "$LETSENCRYPT_EMAIL" ]] && EMAIL="$LETSENCRYPT_EMAIL"

# Default server
[[ -z "$SERVER" ]] && SERVER="$DEFAULT_SERVER"

echo "🚀 Deploying Fanout to $SERVER..."
echo "📧 Email: $EMAIL"
echo "🏷️  Version: ${VERSION:-latest}"
echo ""

# Test SSH
echo "🔐 Testing SSH..."
if ! ssh -o ConnectTimeout=10 -o StrictHostKeyChecking=accept-new "$SERVER" "echo 'OK'" > /dev/null 2>&1; then
    echo "❌ SSH connection failed"
    exit 1
fi

echo "⚡ Setting up server..."
ssh "$SERVER" 'bash -s' << 'SETUP_EOF'
set -e
sudo apt update && sudo apt install -y curl wget

if ! command -v docker &> /dev/null; then
    echo "🐳 Installing Docker..."
    curl -fsSL https://get.docker.com | sudo sh
    sudo systemctl enable docker
    sudo systemctl start docker
    sudo usermod -aG docker $USER
fi

sudo mkdir -p /data/{fanout,ssl,nginx,html,acme}
sudo chown -R $USER:$USER /data
sudo mkdir -p /app
sudo chown -R $USER:$USER /app
SETUP_EOF

echo "📥 Copying config..."
scp "$(dirname "$0")/../docker-compose.prod.yaml" "$SERVER:/app/docker-compose.yaml"
scp "$(dirname "$0")/grpc.conf" "$SERVER:/data/nginx/grpc.conf"

echo "🚀 Deploying..."
ssh "$SERVER" "
set -e
cd /app

cat > .env << EOF
VERSION=${VERSION:-latest}
LETSENCRYPT_EMAIL=$EMAIL
EOF

docker compose pull
docker compose up -d

echo ''
echo '✅ Deployment complete!'
docker compose ps
"

echo ""
echo "🎯 YEET SUCCESSFUL! 💥"
echo "🌐 https://fanout.run"
echo "📊 Status: ssh $SERVER 'cd /app && docker compose ps'"
echo "📋 Logs: ssh $SERVER 'cd /app && docker compose logs -f fanout'"
