#!/usr/bin/env bash
# Starts (or restarts) ngrok tunnels for the frontend (4200) and backend
# (8080) dev servers, then rewires the two services to use the new URLs:
#   - frontend/src/environments/environment.development.ts: apiBaseUrl
#   - .env: CORS_ALLOWED_ORIGINS
# and recreates the backend container so it picks up the new CORS origin.
#
# Requires: ngrok configured with tunnels named "frontend" and "backend"
# in ~/.config/ngrok/ngrok.yml (addr 4200 / 8080), docker compose, curl, jq.
#
# Usage: scripts/ngrok-dev.sh

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="$ROOT_DIR/.env"
ENVIRONMENT_TS="$ROOT_DIR/frontend/src/environments/environment.development.ts"
NGROK_API="http://127.0.0.1:4040/api/tunnels"

if ! command -v jq >/dev/null 2>&1; then
  echo "error: jq is required (apt/pacman/brew install jq)" >&2
  exit 1
fi

echo "==> Stopping any running ngrok agent"
pkill -f "^ngrok " 2>/dev/null || true
sleep 1

echo "==> Starting ngrok (frontend + backend tunnels)"
nohup ngrok start --all --config "$HOME/.config/ngrok/ngrok.yml" \
  > "$ROOT_DIR/scripts/.ngrok.log" 2>&1 &
disown

echo "==> Waiting for tunnels to come up"
for _ in $(seq 1 20); do
  if curl -sf "$NGROK_API" >/dev/null 2>&1; then
    TUNNEL_COUNT=$(curl -s "$NGROK_API" | jq '.tunnels | length')
    [ "$TUNNEL_COUNT" = "2" ] && break
  fi
  sleep 0.5
done

TUNNELS_JSON=$(curl -s "$NGROK_API")
FRONTEND_URL=$(echo "$TUNNELS_JSON" | jq -r '.tunnels[] | select(.name=="frontend") | .public_url')
BACKEND_URL=$(echo "$TUNNELS_JSON" | jq -r '.tunnels[] | select(.name=="backend") | .public_url')

if [ -z "$FRONTEND_URL" ] || [ -z "$BACKEND_URL" ]; then
  echo "error: could not read both tunnel URLs from ngrok API" >&2
  echo "$TUNNELS_JSON" >&2
  exit 1
fi

echo "    frontend: $FRONTEND_URL"
echo "    backend:  $BACKEND_URL"

echo "==> Updating $ENVIRONMENT_TS"
sed -i -E "s#apiBaseUrl: '[^']*'#apiBaseUrl: '${BACKEND_URL}/api/v1'#" "$ENVIRONMENT_TS"

echo "==> Updating $ENV_FILE (CORS_ALLOWED_ORIGINS)"
sed -i -E "s#^CORS_ALLOWED_ORIGINS=.*#CORS_ALLOWED_ORIGINS=http://localhost:4200,${FRONTEND_URL}#" "$ENV_FILE"

echo "==> Recreating backend container to pick up new CORS origin"
(cd "$ROOT_DIR" && docker compose up -d --force-recreate backend) >/dev/null

echo "==> Done."
echo ""
echo "Frontend tunnel: $FRONTEND_URL"
echo "Backend tunnel:  $BACKEND_URL"
echo ""
echo "angular.json allowedHosts already covers .ngrok-free.app, and ng serve"
echo "will hot-reload the changed environment.development.ts on its own."
