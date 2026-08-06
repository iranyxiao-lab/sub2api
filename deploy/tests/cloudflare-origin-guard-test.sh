#!/bin/bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
SCRIPT="$ROOT_DIR/deploy/cloudflare/origin-guard.sh"
TEMP_DIR=$(mktemp -d)
trap 'rm -rf "$TEMP_DIR"' EXIT

mkdir -p "$TEMP_DIR/bin" "$TEMP_DIR/state"

cat >"$TEMP_DIR/bin/curl" <<'EOF'
#!/bin/sh
set -eu
output=
url=
while [ "$#" -gt 0 ]; do
	case $1 in
		--output) output=$2; shift 2 ;;
		https://*) url=$1; shift ;;
		*) shift ;;
	esac
done
[ "${MOCK_CURL_MODE:-ok}" != fail ] || exit 22
case ${MOCK_CURL_MODE:-ok}:$url in
	empty:*) : >"$output" ;;
	bad:*ips-v4) printf '%s\n' 'not-a-cidr' >"$output" ;;
	duplicate:*ips-v4) awk 'BEGIN { for (n = 1; n <= 10; n++) printf "198.51.%d.0/24\n", n; print "198.51.1.0/24" }' >"$output" ;;
	*:/*ips-v4) awk 'BEGIN { for (n = 1; n <= 10; n++) printf "198.51.%d.0/24\n", n }' >"$output" ;;
	*:/*ips-v6) awk 'BEGIN { for (n = 1; n <= 5; n++) printf "2001:db8:%x::/48\n", n }' >"$output" ;;
	*) exit 22 ;;
esac
EOF

cat >"$TEMP_DIR/bin/nft" <<'EOF'
#!/bin/sh
set -eu
if [ "${1:-}" = list ]; then
	[ "${NFT_TABLE_EXISTS:-0}" = 1 ]
	exit
fi
if [ "${1:-}" = -c ]; then
	[ "${NFT_CHECK_FAIL:-0}" != 1 ] || exit 1
	cp "$3" "$NFT_CHECKED"
	exit
fi
if [ "${1:-}" = -f ]; then
	cp "$2" "$NFT_APPLIED"
	exit
fi
exit 0
EOF
chmod +x "$TEMP_DIR/bin/curl" "$TEMP_DIR/bin/nft"

run_guard() {
	PATH="$TEMP_DIR/bin:$PATH" \
	STATE_DIR="$TEMP_DIR/state" \
	LOCK_DIR="$TEMP_DIR/lock" \
	NFT_CHECKED="$TEMP_DIR/checked.nft" \
	NFT_APPLIED="$TEMP_DIR/applied.nft" \
	CF_IPV4_URL=https://example.test/ips-v4 \
	CF_IPV6_URL=https://example.test/ips-v6 \
	MOCK_CURL_MODE="${MOCK_CURL_MODE:-ok}" \
	NFT_CHECK_FAIL="${NFT_CHECK_FAIL:-0}" \
	NFT_TABLE_EXISTS="${NFT_TABLE_EXISTS:-0}" \
	sh "$SCRIPT" "$@"
}

file_checksum() {
	cksum "$1" | awk '{print $1 ":" $2}'
}

run_guard apply-cache-if-present
run_guard refresh
grep -Fq 'table inet sub2api_edge' "$TEMP_DIR/applied.nft"
grep -Fq 'ip saddr @cloudflare_v4 counter accept' "$TEMP_DIR/applied.nft"
grep -Fq 'ip6 saddr @cloudflare_v6 counter accept' "$TEMP_DIR/applied.nft"
grep -Fq 'tcp dport { 80, 443 } counter drop' "$TEMP_DIR/applied.nft"
test -s "$TEMP_DIR/state/ips-v4"
test -s "$TEMP_DIR/state/ips-v6"
baseline=$(file_checksum "$TEMP_DIR/applied.nft")
cached_v4=$(file_checksum "$TEMP_DIR/state/ips-v4")
cached_v6=$(file_checksum "$TEMP_DIR/state/ips-v6")

for mode in empty bad duplicate fail; do
	if MOCK_CURL_MODE=$mode run_guard refresh >/dev/null 2>&1; then
		echo "origin guard accepted invalid curl mode: $mode" >&2
		exit 1
	fi
	test "$(file_checksum "$TEMP_DIR/applied.nft")" = "$baseline"
	test "$(file_checksum "$TEMP_DIR/state/ips-v4")" = "$cached_v4"
	test "$(file_checksum "$TEMP_DIR/state/ips-v6")" = "$cached_v6"
done

if NFT_CHECK_FAIL=1 run_guard refresh >/dev/null 2>&1; then
	echo "origin guard ignored nft syntax failure" >&2
	exit 1
fi
test "$(file_checksum "$TEMP_DIR/applied.nft")" = "$baseline"

NFT_TABLE_EXISTS=1 run_guard apply-cache
grep -Fq 'delete table inet sub2api_edge' "$TEMP_DIR/applied.nft"

run_guard apply-cache
run_guard deny-all
grep -Fq 'tcp dport { 80, 443 } counter drop' "$TEMP_DIR/applied.nft"
if grep -Fq 'cloudflare_v4 counter accept' "$TEMP_DIR/applied.nft"; then
	echo "deny-all rules unexpectedly allow Cloudflare ingress" >&2
	exit 1
fi
test "$(cat "$TEMP_DIR/state/mode")" = deny-all
run_guard service-refresh
if grep -Fq 'cloudflare_v4 counter accept' "$TEMP_DIR/applied.nft"; then
	echo "service refresh reopened a deny-all origin" >&2
	exit 1
fi

rm -f "$TEMP_DIR/state/ips-v4" "$TEMP_DIR/state/ips-v6"
printf '%s\n' allow-cloudflare >"$TEMP_DIR/state/mode"
if MOCK_CURL_MODE=fail NFT_TABLE_EXISTS=0 run_guard service-refresh >/dev/null 2>&1; then
	echo "service refresh unexpectedly succeeded without a table, cache, or download" >&2
	exit 1
fi
grep -Fq 'tcp dport { 80, 443 } counter drop' "$TEMP_DIR/applied.nft"
if grep -Fq 'cloudflare_v4 counter accept' "$TEMP_DIR/applied.nft"; then
	echo "service refresh failed open without a table, cache, or download" >&2
	exit 1
fi
test "$(cat "$TEMP_DIR/state/mode")" = allow-cloudflare

bash -n "$ROOT_DIR/deploy/cloudflare/install-origin-guard.sh"
bash -n "$ROOT_DIR/deploy/cloudflare/install-cloudflared.sh"
bash -n "$ROOT_DIR/deploy/cloudflare/install-cloudflared-token.sh"
bash -n "$ROOT_DIR/deploy/cloudflare/rollback-to-proxied-origin.sh"
bash -n "$ROOT_DIR/deploy/cloudflare/configure-trusted-proxy.sh"
bash -n "$ROOT_DIR/deploy/cloudflare/switch-caddy-mode.sh"

echo "Cloudflare origin guard tests passed"
