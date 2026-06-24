#!/usr/bin/env bash
set -euo pipefail

required_vars=("DOMAIN" "PASSWORD_HASH" "JWT_SECRET" "MEDIAMTX_PUBLISH_HASH")
missing=()

for var in "${required_vars[@]}"; do
	if [ -z "${!var:-}" ]; then
		missing+=("$var")
	fi
done

if [ ${#missing[@]} -gt 0 ]; then
	echo "Error: missing required environment variables:" >&2
	printf '  - %s\n' "${missing[@]}" >&2
	echo "Copy deploy/auth-svc.env.template to .env, fill it in, and source it." >&2
	exit 1
fi

exec "$@"
