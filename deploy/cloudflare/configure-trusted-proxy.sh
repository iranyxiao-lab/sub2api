#!/bin/sh
set -eu

DEPLOY_DIR=${DEPLOY_DIR:-/opt/sub2api}
COMPOSE_FILE=${COMPOSE_FILE:-docker-compose.yml}
DOCKER_BIN=${DOCKER_BIN:-docker}
PYTHON_BIN=${PYTHON_BIN:-python3}

die() {
	printf '%s\n' "$*" >&2
	exit 1
}

[ "$(id -u)" -eq 0 ] || die "run as root"
command -v "$DOCKER_BIN" >/dev/null 2>&1 || die "docker is required"
command -v "$PYTHON_BIN" >/dev/null 2>&1 || die "python3 is required"
[ -d "$DEPLOY_DIR" ] || die "deployment directory does not exist: $DEPLOY_DIR"
[ -f "$DEPLOY_DIR/$COMPOSE_FILE" ] || die "compose file does not exist: $DEPLOY_DIR/$COMPOSE_FILE"
[ -f "$DEPLOY_DIR/.env" ] || die "environment file does not exist: $DEPLOY_DIR/.env"

container_id=$(
	cd "$DEPLOY_DIR"
	"$DOCKER_BIN" compose -f "$COMPOSE_FILE" ps -q sub2api
)
[ -n "$container_id" ] || die "sub2api container is not running"
[ "$(printf '%s\n' "$container_id" | awk 'NF { count++ } END { print count + 0 }')" -eq 1 ] ||
	die "expected exactly one sub2api container"

gateways=$(
	"$DOCKER_BIN" inspect --format '{{range .NetworkSettings.Networks}}{{println .Gateway}}{{end}}' "$container_id" |
		awk 'NF' | sort -u
)

trusted_proxy=$(
	printf '%s\n' "$gateways" | "$PYTHON_BIN" -c '
import ipaddress
import sys

values = [line.strip() for line in sys.stdin if line.strip()]
valid = []
for value in values:
    try:
        address = ipaddress.ip_address(value)
    except ValueError as exc:
        raise SystemExit(f"invalid Docker network gateway {value!r}: {exc}") from exc
    if address.version != 4 or address.is_unspecified or address.is_multicast:
        raise SystemExit(f"Docker network gateway must be a usable IPv4 address: {value!r}")
    valid.append(str(address))
valid = sorted(set(valid))
if len(valid) != 1:
    raise SystemExit(f"expected one unique Docker IPv4 gateway, got {len(valid)}")
print(valid[0] + "/32")
'
) || die "could not determine a unique Docker IPv4 gateway"

env_file=$DEPLOY_DIR/.env
entry_count=$(grep -Ec '^SERVER_TRUSTED_PROXIES=' "$env_file" || true)
[ "$entry_count" -le 1 ] || die "SERVER_TRUSTED_PROXIES appears more than once in $env_file"

tmp=$(mktemp "$DEPLOY_DIR/.env.trusted-proxy.XXXXXX")
trap 'rm -f "$tmp"' EXIT HUP INT TERM
awk -v replacement="SERVER_TRUSTED_PROXIES=$trusted_proxy" '
	BEGIN { replaced = 0 }
	/^SERVER_TRUSTED_PROXIES=/ {
		print replacement
		replaced = 1
		next
	}
	{ print }
	END {
		if (!replaced) print replacement
	}
' "$env_file" >"$tmp"
chown --reference="$env_file" "$tmp"
chmod --reference="$env_file" "$tmp"
mv -f "$tmp" "$env_file"
trap - EXIT HUP INT TERM

printf 'SERVER_TRUSTED_PROXIES configured as %s; recreate only the sub2api service to apply it.\n' "$trusted_proxy"
