#!/bin/sh
set -eu

[ "$(id -u)" -eq 0 ] || { echo "run as root" >&2; exit 1; }
[ ! -t 0 ] || { echo "provide the tunnel token on standard input" >&2; exit 1; }
getent passwd cloudflared >/dev/null || { echo "cloudflared user is missing" >&2; exit 1; }

umask 077
tmp=$(mktemp /etc/cloudflared/heytoken.token.XXXXXX)
trap 'rm -f "$tmp"' EXIT HUP INT TERM
cat >"$tmp"
[ -s "$tmp" ] || { echo "tunnel token is empty" >&2; exit 1; }
[ "$(wc -l <"$tmp")" -le 1 ] || { echo "tunnel token must be one line" >&2; exit 1; }
LC_ALL=C grep -Eq '^[^[:space:]]+$' "$tmp" || { echo "tunnel token contains whitespace" >&2; exit 1; }
install -o cloudflared -g cloudflared -m 0400 "$tmp" /etc/cloudflared/heytoken.token
printf 'Tunnel token installed at /etc/cloudflared/heytoken.token\n'
