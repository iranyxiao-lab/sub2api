#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
CADDYFILE=${CADDYFILE:-/etc/caddy/Caddyfile}
BACKUP_DIR=${BACKUP_DIR:-/var/lib/sub2api-edge/caddy-backups}
CADDY_BIN=${CADDY_BIN:-caddy}

die() {
	printf '%s\n' "$*" >&2
	exit 1
}

[ "$(id -u)" -eq 0 ] || die "run as root"
mode=${1:-}
case $mode in
	cloudflare) source_file=$script_dir/Caddyfile.cloudflare ;;
	tunnel) source_file=$script_dir/Caddyfile.tunnel ;;
	*) die "usage: $0 {cloudflare|tunnel}" ;;
esac

command -v "$CADDY_BIN" >/dev/null 2>&1 || die "caddy is required"
command -v curl >/dev/null 2>&1 || die "curl is required"
[ -f "$source_file" ] || die "Caddy template is missing: $source_file"
[ -f "$CADDYFILE" ] || die "active Caddyfile is missing: $CADDYFILE"
curl --fail --silent --show-error --max-time 10 http://127.0.0.1:18082/health >/dev/null ||
	die "application health check failed before Caddy reload"

"$CADDY_BIN" adapt --config "$source_file" --adapter caddyfile >/dev/null
"$CADDY_BIN" validate --config "$source_file" --adapter caddyfile

install -d -m 0700 "$BACKUP_DIR"
timestamp=$(date -u +%Y%m%dT%H%M%SZ)
backup=$BACKUP_DIR/Caddyfile.$timestamp
install -m 0600 "$CADDYFILE" "$backup"

tmp=$(mktemp "$(dirname "$CADDYFILE")/.Caddyfile.XXXXXX")
trap 'rm -f "$tmp"' EXIT HUP INT TERM
install -m 0644 "$source_file" "$tmp"
mv -f "$tmp" "$CADDYFILE"
trap - EXIT HUP INT TERM

if ! systemctl reload caddy; then
	install -m 0644 "$backup" "$CADDYFILE"
	systemctl reload caddy || true
	die "Caddy reload failed; the previous configuration was restored"
fi

curl --fail --silent --show-error --max-time 10 \
	--header 'Host: heytoken.net' http://127.0.0.1:18081/health >/dev/null || {
		install -m 0644 "$backup" "$CADDYFILE"
		systemctl reload caddy || true
	die "loopback Tunnel origin health check failed; the previous configuration was restored"
}

printf 'Caddy switched to %s mode; rollback backup: %s\n' "$mode" "$backup"
