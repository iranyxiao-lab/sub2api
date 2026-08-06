#!/bin/bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
SCRIPT="$ROOT_DIR/deploy/cloudflare/configure-trusted-proxy.sh"
TEMP_DIR=$(mktemp -d)
trap 'rm -rf "$TEMP_DIR"' EXIT

mkdir -p "$TEMP_DIR/bin" "$TEMP_DIR/deploy"
printf '%s\n' 'services:' >"$TEMP_DIR/deploy/docker-compose.yml"
printf '%s\n' 'SERVER_MODE=release' >"$TEMP_DIR/deploy/.env"
chmod 0600 "$TEMP_DIR/deploy/.env"

cat >"$TEMP_DIR/bin/id" <<'EOF'
#!/bin/sh
printf '%s\n' 0
EOF

cat >"$TEMP_DIR/bin/docker" <<'EOF'
#!/bin/sh
set -eu
case ${1:-} in
	compose) printf '%s\n' mock-container ;;
	inspect)
		case ${MOCK_GATEWAY_MODE:-ok} in
			ok) printf '%s\n' 172.23.0.1 ;;
			duplicate) printf '%s\n' 172.23.0.1 172.23.0.1 ;;
			multiple) printf '%s\n' 172.23.0.1 172.24.0.1 ;;
			invalid) printf '%s\n' not-an-ip ;;
			empty) : ;;
		esac
		;;
	*) exit 1 ;;
esac
EOF

chmod +x "$TEMP_DIR/bin/id" "$TEMP_DIR/bin/docker"

run_configure() {
	PATH="$TEMP_DIR/bin:$PATH" \
	DEPLOY_DIR="$TEMP_DIR/deploy" \
	MOCK_GATEWAY_MODE="${MOCK_GATEWAY_MODE:-ok}" \
	sh "$SCRIPT"
}

run_configure >/dev/null
test "$(grep -Ec '^SERVER_TRUSTED_PROXIES=172\.23\.0\.1/32$' "$TEMP_DIR/deploy/.env")" -eq 1
test "$(grep -Ec '^SERVER_TRUSTED_PROXIES=' "$TEMP_DIR/deploy/.env")" -eq 1

MOCK_GATEWAY_MODE=duplicate run_configure >/dev/null
test "$(grep -Ec '^SERVER_TRUSTED_PROXIES=172\.23\.0\.1/32$' "$TEMP_DIR/deploy/.env")" -eq 1

for mode in multiple invalid empty; do
	if MOCK_GATEWAY_MODE=$mode run_configure >/dev/null 2>&1; then
		echo "trusted proxy configuration accepted gateway mode: $mode" >&2
		exit 1
	fi
	test "$(grep -Ec '^SERVER_TRUSTED_PROXIES=172\.23\.0\.1/32$' "$TEMP_DIR/deploy/.env")" -eq 1
done

printf '%s\n' 'SERVER_TRUSTED_PROXIES=172.24.0.1/32' >>"$TEMP_DIR/deploy/.env"
if run_configure >/dev/null 2>&1; then
	echo 'trusted proxy configuration accepted duplicate environment entries' >&2
	exit 1
fi

echo "Cloudflare trusted proxy configuration tests passed"
