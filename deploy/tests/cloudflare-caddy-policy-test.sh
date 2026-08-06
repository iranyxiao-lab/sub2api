#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)

for caddyfile in \
	"$repo_root/deploy/cloudflare/Caddyfile.cloudflare" \
	"$repo_root/deploy/cloudflare/Caddyfile.tunnel"
do
	active=$(sed 's/[[:space:]]*#.*$//' "$caddyfile" | tr -d '\r')
	printf '%s\n' "$active" | grep -Fq 'max_header_size 64KB'
	printf '%s\n' "$active" | grep -Fq 'read_header 10s'
	printf '%s\n' "$active" | grep -Fq 'max_size 256MB'
	printf '%s\n' "$active" | grep -Fq 'header Strict-Transport-Security "max-age=86400"'
	if printf '%s\n' "$active" | grep -Eq 'includeSubDomains|preload'; then
		printf '%s has an unsafe initial HSTS policy\n' "$caddyfile" >&2
		exit 1
	fi
	printf '%s\n' "$active" | grep -Fq 'header_up Host heytoken.net'
	printf '%s\n' "$active" | grep -Fq 'header_up X-Real-IP {http.request.header.CF-Connecting-IP}'
	printf '%s\n' "$active" | grep -Fq 'header_up X-Forwarded-For {http.request.header.CF-Connecting-IP}'
	printf '%s\n' "$active" | grep -Fq 'compression off'
	if printf '%s\n' "$active" | grep -Eq 'flush_interval|text/event-stream|encode[[:space:]]+(gzip|zstd)'; then
		printf '%s has an unsafe SSE buffering/compression directive\n' "$caddyfile" >&2
		exit 1
	fi
done

grep -Fq 'http://:18081' "$repo_root/deploy/cloudflare/Caddyfile.tunnel"
grep -Fq 'bind 127.0.0.1' "$repo_root/deploy/cloudflare/Caddyfile.tunnel"
grep -Fq 'http://:18081' "$repo_root/deploy/cloudflare/Caddyfile.cloudflare"
grep -Fq 'bind 127.0.0.1' "$repo_root/deploy/cloudflare/Caddyfile.cloudflare"
if grep -Eq '(^|[^0-9])(80|443)([^0-9]|$)' "$repo_root/deploy/cloudflare/Caddyfile.tunnel"; then
	echo 'Tunnel Caddyfile must not listen on public HTTP/HTTPS ports' >&2
	exit 1
fi

echo "Cloudflare Caddy policy tests passed"
