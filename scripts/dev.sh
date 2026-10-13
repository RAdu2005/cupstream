#!/usr/bin/env bash
# Development environment: MediaMTX in Docker, the Go server and the Vite dev
# server with hot reload. Open http://localhost:5173. Ctrl+C stops everything
# except the MediaMTX container (`docker compose stop mediamtx`).
set -euo pipefail
cd "$(dirname "$0")/.."

docker compose up -d --build mediamtx
[ -d web/node_modules ] || (cd web && npm ci)

# Google/Discord OAuth credentials, if any, come from .env.
set -a
[ -f .env ] && . ./.env
set +a

# A persistent development secret key, so stored Discord tokens survive restarts.
mkdir -p data
[ -f data/dev-secret-key ] || head -c 32 /dev/urandom | base64 > data/dev-secret-key
export CUPSTREAM_SECRET_KEY="${CUPSTREAM_SECRET_KEY:-$(cat data/dev-secret-key)}"

export CUPSTREAM_ADDR=127.0.0.1:8080
export CUPSTREAM_PUBLIC_URL=http://localhost:5173
export CUPSTREAM_DB="$PWD/data/dev.db"
export CUPSTREAM_DEV_LOGIN=true
export CUPSTREAM_LOG_FORMAT=text
export CUPSTREAM_LOG_LEVEL="${CUPSTREAM_LOG_LEVEL:-debug}"

trap 'trap - EXIT; kill 0' EXIT INT TERM
(cd server && go run ./cmd/cupstream) &
(cd web && npm run dev -- --clearScreen false) &
wait -n
