#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
[ "$(id -u)" -eq 0 ] || { echo "run as root" >&2; exit 1; }

install -d -m 0755 /usr/share/keyrings
curl -q --fail --silent --show-error --location --proto '=https' --tlsv1.2 \
	https://pkg.cloudflare.com/cloudflare-main.gpg \
	--output /usr/share/keyrings/cloudflare-main.gpg
printf '%s\n' 'deb [signed-by=/usr/share/keyrings/cloudflare-main.gpg] https://pkg.cloudflare.com/cloudflared any main' \
	>/etc/apt/sources.list.d/cloudflared.list
apt-get update
DEBIAN_FRONTEND=noninteractive apt-get install -y cloudflared

version=$(dpkg-query -W -f='${Version}' cloudflared)
dpkg --compare-versions "$version" ge 2025.4.0 || {
	echo "cloudflared >= 2025.4.0 is required, installed: $version" >&2
	exit 1
}

if ! getent passwd cloudflared >/dev/null; then
	useradd --system --home-dir /nonexistent --no-create-home --shell /usr/sbin/nologin cloudflared
fi
install -d -o root -g cloudflared -m 0750 /etc/cloudflared
install -m 0644 "$script_dir/cloudflared.service" /etc/systemd/system/cloudflared.service
systemctl daemon-reload

cat <<'EOF'
cloudflared is installed. Install the remotely-managed tunnel token with:
  install-cloudflared-token.sh < token-file
Then start the connector with:
  systemctl enable --now cloudflared
EOF
