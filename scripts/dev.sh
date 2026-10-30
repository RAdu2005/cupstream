#!/usr/bin/env bash
# Development environment: MediaMTX in Docker, the Go server and the Vite dev
# server with hot reload. Open http://localhost:5173. Ctrl+C stops everything
# except the MediaMTX container (`docker compose stop mediamtx`).
#
# To run a second copy from another worktree, pick other ports:
#   DEV_WEB_PORT=5174 DEV_SERVER_PORT=8081 make dev
# Both copies share the MediaMTX container. Google and Discord sign-in only
# work on port 5173, where their redirect URIs are registered.
set -euo pipefail
cd "$(dirname "$0")/.."

docker compose up -d --build mediamtx
[ -d web/node_modules ] || (cd web && npm ci)

# Google/Discord OAuth credentials, if any, come from .env; a git worktree
# without one uses the main checkout's.
env_file=.env
main_checkout=$(dirname "$(git rev-parse --path-format=absolute --git-common-dir 2>/dev/null || echo .git)")
[ -f "$env_file" ] || env_file=$main_checkout/.env
set -a
[ -f "$env_file" ] && . "$env_file"
set +a
web_port=${DEV_WEB_PORT:-5173}
server_port=${DEV_SERVER_PORT:-8080}

# A persistent development secret key, so stored Discord tokens survive restarts.
mkdir -p data
[ -f data/dev-secret-key ] || head -c 32 /dev/urandom | base64 > data/dev-secret-key
export CUPSTREAM_SECRET_KEY="${CUPSTREAM_SECRET_KEY:-$(cat data/dev-secret-key)}"

export CUPSTREAM_ADDR=127.0.0.1:$server_port
export CUPSTREAM_PUBLIC_URL=http://localhost:$web_port
export CUPSTREAM_DB="$PWD/data/dev.db"
export CUPSTREAM_DEV_LOGIN=true
export CUPSTREAM_LOG_FORMAT=text
export CUPSTREAM_LOG_LEVEL="${CUPSTREAM_LOG_LEVEL:-debug}"

trap 'trap - EXIT; kill 0' EXIT INT TERM
(cd server && go run ./cmd/cupstream) &
(cd web && npm run dev -- --clearScreen false --port "$web_port") &
wait -n
