#!/bin/sh
set -eu

[ "$(id -u)" -eq 0 ] || { echo "run as root" >&2; exit 1; }
[ -s /var/lib/sub2api-origin-guard/ips-v4 ] || { echo "cached Cloudflare IPv4 ranges are missing" >&2; exit 1; }
[ -s /var/lib/sub2api-origin-guard/ips-v6 ] || { echo "cached Cloudflare IPv6 ranges are missing" >&2; exit 1; }

/usr/local/sbin/sub2api-origin-guard apply-cache
systemctl enable sub2api-origin-guard.service
systemctl enable --now sub2api-origin-guard.timer
printf '%s\n' 'Cloudflare allowlist restored. Restore the proxied DNS A record and public Caddyfile before stopping cloudflared.'
