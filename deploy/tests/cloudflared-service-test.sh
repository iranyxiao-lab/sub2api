#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
service="$repo_root/deploy/cloudflare/cloudflared.service"

grep -Fxq 'Environment=TUNNEL_TRANSPORT_PROTOCOL=http2' "$service"
grep -Fq -- '--metrics 127.0.0.1:20241' "$service"
grep -Fq -- '--token-file /etc/cloudflared/heytoken.token' "$service"

if grep -Eq '(^|[[:space:]])--token([=[:space:]])' "$service"; then
	echo 'cloudflared token must not be stored in the systemd command line' >&2
	exit 1
fi

if grep -Eq 'Environment=.*(TOKEN|token)' "$service"; then
	echo 'cloudflared token must not be stored in the systemd environment' >&2
	exit 1
fi

echo "cloudflared service tests passed"
