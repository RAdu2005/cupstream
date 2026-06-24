#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

GITHUB_REPO="${GITHUB_REPO:-RAdu2005/cupstream}"
MEDIAMTX_REPO="bluenviron/mediamtx"
ENV_FILE="$PROJECT_DIR/.env"
BUILD_FROM_SOURCE=false
SKIP_FIREWALL=false

# ------------------------------------------------------------------
# Args
# ------------------------------------------------------------------

while [ $# -gt 0 ]; do
	case "$1" in
		--env-file)
			ENV_FILE="$2"
			shift 2
			;;
		--build)
			BUILD_FROM_SOURCE=true
			shift
			;;
		--skip-firewall)
			SKIP_FIREWALL=true
			shift
			;;
		--help|-h)
			echo "Usage: $0 [--env-file <path>] [--build] [--skip-firewall]"
			exit 0
			;;
		*)
			echo "Unknown option: $1" >&2
			exit 1
			;;
	esac
done

# ------------------------------------------------------------------
# Detection
# ------------------------------------------------------------------

ARCH="amd64"
[ "$(uname -m)" = "aarch64" ] && ARCH="arm64"

DISTRO=""
if [ -f /etc/os-release ]; then
	DISTRO="$(. /etc/os-release && echo "$ID")"
fi

case "$DISTRO" in
	ubuntu|debian) DISTRO="debian" ;;
	alpine) DISTRO="alpine" ;;
	*)
		if command -v apt-get >/dev/null 2>&1; then
			DISTRO="debian"
		elif command -v apk >/dev/null 2>&1; then
			DISTRO="alpine"
		else
			echo "Unsupported distro: $DISTRO" >&2
			exit 1
		fi
		;;
esac

echo "Detected distro: $DISTRO, arch: $ARCH"

# ------------------------------------------------------------------
# Preflight
# ------------------------------------------------------------------

if [ ! -f "$ENV_FILE" ]; then
	echo "Missing env file: $ENV_FILE" >&2
	echo "Run scripts/setup-env.sh first." >&2
	exit 1
fi

DOMAIN="$(grep '^DOMAIN=' "$ENV_FILE" | cut -d= -f2-)"
MEDIAMTX_DOMAIN="$(grep '^MEDIAMTX_DOMAIN=' "$ENV_FILE" | cut -d= -f2-)"

if [ -z "$DOMAIN" ] || [ -z "$MEDIAMTX_DOMAIN" ]; then
	echo "DOMAIN and MEDIAMTX_DOMAIN must be set in $ENV_FILE" >&2
	exit 1
fi

# ------------------------------------------------------------------
# Dependencies
# ------------------------------------------------------------------

install_deps() {
	if [ "$DISTRO" = "debian" ]; then
		apt-get update
		apt-get install -y curl openssl ca-certificates
	elif [ "$DISTRO" = "alpine" ]; then
		apk add --no-cache curl openssl ca-certificates
	fi
}

if ! command -v curl >/dev/null 2>&1; then
	echo "Installing dependencies..."
	install_deps
fi

# ------------------------------------------------------------------
# Users
# ------------------------------------------------------------------

create_user() {
	local user="$1"
	if id "$user" >/dev/null 2>&1; then
		echo "User $user already exists"
	else
		if [ "$DISTRO" = "debian" ]; then
			useradd --system --no-create-home --shell /usr/sbin/nologin "$user"
		else
			adduser -S -D -H -s /sbin/nologin "$user"
		fi
		echo "Created user $user"
	fi
}

create_user auth-svc
create_user mediamtx

# ------------------------------------------------------------------
# Directories
# ------------------------------------------------------------------

mkdir -p /usr/local/bin
mkdir -p /etc/auth-svc
mkdir -p /etc/mediamtx
mkdir -p /var/lib/auth-svc/certs
mkdir -p /var/lib/mediamtx
chown auth-svc:auth-svc /var/lib/auth-svc
chown mediamtx:mediamtx /var/lib/mediamtx

# ------------------------------------------------------------------
# Install auth-svc
# ------------------------------------------------------------------

install_auth_svc_binary() {
	if [ "$BUILD_FROM_SOURCE" = true ]; then
		echo "Building auth-svc from source..."
		if ! command -v go >/dev/null 2>&1 || ! command -v npm >/dev/null 2>&1; then
			echo "Go and Node.js are required for --build." >&2
			exit 1
		fi
		cd "$PROJECT_DIR"
		npm ci
		npm run build
		cd auth-svc
		CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" go build -ldflags="-s -w" -trimpath -o auth-svc .
		cp auth-svc /usr/local/bin/auth-svc
	else
		echo "Downloading auth-svc from GitHub releases..."
		local url="https://github.com/$GITHUB_REPO/releases/latest/download/auth-svc-linux-${ARCH}"
		if ! curl -fsSL -o /usr/local/bin/auth-svc "$url"; then
			echo "Failed to download release binary." >&2
			echo "Either create a GitHub release or use --build to compile from source." >&2
			exit 1
		fi
	fi
	chmod +x /usr/local/bin/auth-svc
	echo "Installed /usr/local/bin/auth-svc"
}

install_auth_svc_binary

# ------------------------------------------------------------------
# Install MediaMTX
# ------------------------------------------------------------------

install_mediamtx() {
	echo "Downloading MediaMTX..."
	local tmpdir
	tmpdir="$(mktemp -d)"
	trap "rm -rf '$tmpdir'" EXIT

	# Get latest release tag
	local tag
	tag="$(curl -fsSL "https://api.github.com/repos/$MEDIAMTX_REPO/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')"
	if [ -z "$tag" ]; then
		echo "Failed to fetch latest MediaMTX release tag" >&2
		exit 1
	fi

	local ver="${tag#v}"
	local url="https://github.com/$MEDIAMTX_REPO/releases/download/${tag}/mediamtx_v${ver}_linux_${ARCH}.tar.gz"

	curl -fsSL -o "$tmpdir/mediamtx.tar.gz" "$url"
	tar -xzf "$tmpdir/mediamtx.tar.gz" -C "$tmpdir"
	cp "$tmpdir/mediamtx" /usr/local/bin/mediamtx
	chmod +x /usr/local/bin/mediamtx
	echo "Installed /usr/local/bin/mediamtx ($tag)"
}

install_mediamtx

# ------------------------------------------------------------------
# Install configs
# ------------------------------------------------------------------

cp "$ENV_FILE" /etc/auth-svc/env
chmod 600 /etc/auth-svc/env
chown auth-svc:auth-svc /etc/auth-svc/env
echo "Installed /etc/auth-svc/env"

sed "s|__MEDIAMTX_DOMAIN__|$MEDIAMTX_DOMAIN|g" "$PROJECT_DIR/deploy/mediamtx.yml" > /etc/mediamtx/mediamtx.yml
chmod 644 /etc/mediamtx/mediamtx.yml
echo "Installed /etc/mediamtx/mediamtx.yml"

# ------------------------------------------------------------------
# Install services
# ------------------------------------------------------------------

if [ "$DISTRO" = "debian" ]; then
	cp "$PROJECT_DIR/deploy/auth-svc.service" /etc/systemd/system/auth-svc.service
	cp "$PROJECT_DIR/deploy/mediamtx.service" /etc/systemd/system/mediamtx.service
	chmod 644 /etc/systemd/system/auth-svc.service /etc/systemd/system/mediamtx.service
	systemctl daemon-reload
	systemctl enable mediamtx auth-svc
	echo "Installed systemd services"
else
	cp "$PROJECT_DIR/deploy/auth-svc.openrc" /etc/init.d/auth-svc
	cp "$PROJECT_DIR/deploy/mediamtx.openrc" /etc/init.d/mediamtx
	chmod +x /etc/init.d/auth-svc /etc/init.d/mediamtx
	rc-update add mediamtx default
	rc-update add auth-svc default
	echo "Installed OpenRC services"
fi

# ------------------------------------------------------------------
# Firewall
# ------------------------------------------------------------------

configure_firewall() {
	if [ "$SKIP_FIREWALL" = true ]; then
		echo "Skipping firewall configuration (--skip-firewall)"
		return 0
	fi

	if [ "$DISTRO" = "debian" ]; then
		if command -v ufw >/dev/null 2>&1; then
			ufw allow 80/tcp comment 'HTTP'
			ufw allow 443/tcp comment 'HTTPS'
			ufw allow 8889/tcp comment 'WebRTC signaling'
			ufw allow 8189/udp comment 'WebRTC ICE media'
			echo "Configured ufw rules"
		else
			echo "ufw not installed; skipping firewall setup. Open ports manually: 80/tcp, 443/tcp, 8889/tcp, 8189/udp"
		fi
	else
		if command -v ufw >/dev/null 2>&1; then
			ufw allow 80/tcp
			ufw allow 443/tcp
			ufw allow 8889/tcp
			ufw allow 8189/udp
			echo "Configured ufw rules"
		elif iptables -L >/dev/null 2>&1; then
			iptables -C INPUT -p tcp --dport 80 -j ACCEPT 2>/dev/null || iptables -A INPUT -p tcp --dport 80 -j ACCEPT
			iptables -C INPUT -p tcp --dport 443 -j ACCEPT 2>/dev/null || iptables -A INPUT -p tcp --dport 443 -j ACCEPT
			iptables -C INPUT -p tcp --dport 8889 -j ACCEPT 2>/dev/null || iptables -A INPUT -p tcp --dport 8889 -j ACCEPT
			iptables -C INPUT -p udp --dport 8189 -j ACCEPT 2>/dev/null || iptables -A INPUT -p udp --dport 8189 -j ACCEPT
			echo "Configured iptables rules"
		else
			echo "No firewall tool found; skipping firewall setup. Open ports manually: 80/tcp, 443/tcp, 8889/tcp, 8189/udp"
		fi
	fi
}

configure_firewall

# ------------------------------------------------------------------
# Start
# ------------------------------------------------------------------

if [ "$DISTRO" = "debian" ]; then
	systemctl restart mediamtx
	systemctl restart auth-svc
	echo "Services started via systemd"
else
	rc-service mediamtx restart
	rc-service auth-svc restart
	echo "Services started via OpenRC"
fi

echo
echo "=== Installation complete ==="
echo "Domain:        $DOMAIN"
echo "MediaMTX:      $MEDIAMTX_DOMAIN"
echo "Config:        /etc/auth-svc/env"
echo "MediaMTX cfg:  /etc/mediamtx/mediamtx.yml"
echo
echo "Check status with:"
if [ "$DISTRO" = "debian" ]; then
	echo "  systemctl status auth-svc mediamtx"
else
	echo "  rc-service auth-svc status"
	echo "  rc-service mediamtx status"
fi
