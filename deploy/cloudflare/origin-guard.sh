#!/bin/sh
set -eu

CF_IPV4_URL=${CF_IPV4_URL:-https://www.cloudflare.com/ips-v4}
CF_IPV6_URL=${CF_IPV6_URL:-https://www.cloudflare.com/ips-v6}
STATE_DIR=${STATE_DIR:-/var/lib/sub2api-origin-guard}
NFT_BIN=${NFT_BIN:-nft}
CURL_BIN=${CURL_BIN:-curl}
PYTHON_BIN=${PYTHON_BIN:-python3}
NFT_FAMILY=${NFT_FAMILY:-inet}
NFT_TABLE=${NFT_TABLE:-sub2api_edge}
MIN_IPV4_RANGES=${MIN_IPV4_RANGES:-10}
MIN_IPV6_RANGES=${MIN_IPV6_RANGES:-5}
MAX_RANGES=${MAX_RANGES:-1000}
MAX_LIST_BYTES=${MAX_LIST_BYTES:-1048576}
LOCK_DIR=${LOCK_DIR:-/run/sub2api-origin-guard.lock}

die() {
	printf '%s\n' "$*" >&2
	exit 1
}

require_command() {
	command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

acquire_lock() {
	if ! mkdir "$LOCK_DIR" 2>/dev/null; then
		die "origin guard is already running: $LOCK_DIR"
	fi
	trap 'rmdir "$LOCK_DIR" 2>/dev/null || true' EXIT HUP INT TERM
}

release_lock() {
	rmdir "$LOCK_DIR" 2>/dev/null || true
	trap - EXIT HUP INT TERM
}

validate_ranges() {
	file=$1
	version=$2
	minimum=$3
	[ "$(wc -c <"$file")" -le "$MAX_LIST_BYTES" ] ||
		die "IPv${version} range list exceeds ${MAX_LIST_BYTES} bytes"
	"$PYTHON_BIN" - "$file" "$version" "$minimum" "$MAX_RANGES" <<'PY'
import ipaddress
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
version = int(sys.argv[2])
minimum = int(sys.argv[3])
maximum = int(sys.argv[4])
lines = [line.strip() for line in path.read_text(encoding="ascii").splitlines() if line.strip()]
if len(lines) < minimum:
    raise SystemExit(f"expected at least {minimum} IPv{version} ranges, got {len(lines)}")
if len(lines) > maximum:
    raise SystemExit(f"expected at most {maximum} IPv{version} ranges, got {len(lines)}")
if len(lines) != len(set(lines)):
    raise SystemExit(f"duplicate IPv{version} ranges are not allowed")
for line in lines:
    try:
        network = ipaddress.ip_network(line, strict=True)
    except ValueError as exc:
        raise SystemExit(f"invalid CIDR {line!r}: {exc}") from exc
    if network.version != version:
        raise SystemExit(f"expected IPv{version} range, got {line!r}")
PY
}

render_elements() {
	awk '
		NF {
			if (seen++) printf ", "
			printf "%s", $0
		}
		END { printf "\n" }
	' "$1"
}

render_rules() {
	mode=$1
	ipv4_file=${2:-}
	ipv6_file=${3:-}
	output=$4
	{
		if "$NFT_BIN" list table "$NFT_FAMILY" "$NFT_TABLE" >/dev/null 2>&1; then
			printf 'delete table %s %s\n' "$NFT_FAMILY" "$NFT_TABLE"
		fi
		printf 'table %s %s {\n' "$NFT_FAMILY" "$NFT_TABLE"
		if [ "$mode" = allow-cloudflare ]; then
			printf '  set cloudflare_v4 {\n    type ipv4_addr\n    flags interval\n    elements = { '
			render_elements "$ipv4_file"
			printf '    }\n  }\n'
			printf '  set cloudflare_v6 {\n    type ipv6_addr\n    flags interval\n    elements = { '
			render_elements "$ipv6_file"
			printf '    }\n  }\n'
		fi
		printf '  chain input {\n'
		printf '    type filter hook input priority -10; policy accept;\n'
		if [ "$mode" = allow-cloudflare ]; then
			printf '    tcp dport { 80, 443 } ip saddr @cloudflare_v4 counter accept\n'
			printf '    tcp dport { 80, 443 } ip6 saddr @cloudflare_v6 counter accept\n'
		fi
		printf '    tcp dport { 80, 443 } counter drop\n'
		printf '  }\n}\n'
	} >"$output"
}

apply_rules() {
	rules=$1
	"$NFT_BIN" -c -f "$rules"
	"$NFT_BIN" -f "$rules"
}

refresh() {
	require_command "$CURL_BIN"
	require_command "$NFT_BIN"
	require_command "$PYTHON_BIN"
	acquire_lock
	umask 077
	mkdir -p "$STATE_DIR"
	tmp_dir=$(mktemp -d "${STATE_DIR}/refresh.XXXXXX")
	trap 'rm -rf "$tmp_dir"; rmdir "$LOCK_DIR" 2>/dev/null || true' EXIT HUP INT TERM

	"$CURL_BIN" -q --fail --silent --show-error --location --proto '=https' --tlsv1.2 \
		--connect-timeout 10 --max-time 30 --retry 3 --retry-all-errors \
		--max-filesize "$MAX_LIST_BYTES" \
		--output "$tmp_dir/ips-v4" "$CF_IPV4_URL"
	"$CURL_BIN" -q --fail --silent --show-error --location --proto '=https' --tlsv1.2 \
		--connect-timeout 10 --max-time 30 --retry 3 --retry-all-errors \
		--max-filesize "$MAX_LIST_BYTES" \
		--output "$tmp_dir/ips-v6" "$CF_IPV6_URL"
	validate_ranges "$tmp_dir/ips-v4" 4 "$MIN_IPV4_RANGES"
	validate_ranges "$tmp_dir/ips-v6" 6 "$MIN_IPV6_RANGES"
	render_rules allow-cloudflare "$tmp_dir/ips-v4" "$tmp_dir/ips-v6" "$tmp_dir/rules.nft"
	apply_rules "$tmp_dir/rules.nft"
	install -m 0600 "$tmp_dir/ips-v4" "$STATE_DIR/ips-v4"
	install -m 0600 "$tmp_dir/ips-v6" "$STATE_DIR/ips-v6"
	install -m 0600 "$tmp_dir/rules.nft" "$STATE_DIR/rules.nft"
	printf '%s\n' allow-cloudflare >"$tmp_dir/mode"
	install -m 0600 "$tmp_dir/mode" "$STATE_DIR/mode"
	rm -rf "$tmp_dir"
	release_lock
	printf 'Cloudflare origin allowlist applied successfully\n'
}

apply_cache() {
	require_command "$NFT_BIN"
	require_command "$PYTHON_BIN"
	acquire_lock
	[ -s "$STATE_DIR/ips-v4" ] || die "cached IPv4 list is missing"
	[ -s "$STATE_DIR/ips-v6" ] || die "cached IPv6 list is missing"
	validate_ranges "$STATE_DIR/ips-v4" 4 "$MIN_IPV4_RANGES"
	validate_ranges "$STATE_DIR/ips-v6" 6 "$MIN_IPV6_RANGES"
	tmp_rules=$(mktemp "${STATE_DIR}/rules.XXXXXX")
	trap 'rm -f "$tmp_rules" "$tmp_rules.mode"; rmdir "$LOCK_DIR" 2>/dev/null || true' EXIT HUP INT TERM
	render_rules allow-cloudflare "$STATE_DIR/ips-v4" "$STATE_DIR/ips-v6" "$tmp_rules"
	apply_rules "$tmp_rules"
	install -m 0600 "$tmp_rules" "$STATE_DIR/rules.nft"
	printf '%s\n' allow-cloudflare >"$tmp_rules.mode"
	install -m 0600 "$tmp_rules.mode" "$STATE_DIR/mode"
	rm -f "$tmp_rules" "$tmp_rules.mode"
	release_lock
}

apply_cache_if_present() {
	if [ -s "$STATE_DIR/ips-v4" ] && [ -s "$STATE_DIR/ips-v6" ]; then
		apply_cache
	fi
}

deny_all() {
	persist_mode=${1:-yes}
	require_command "$NFT_BIN"
	acquire_lock
	umask 077
	mkdir -p "$STATE_DIR"
	tmp_rules=$(mktemp "${STATE_DIR}/deny-all.XXXXXX")
	trap 'rm -f "$tmp_rules" "$tmp_rules.mode"; rmdir "$LOCK_DIR" 2>/dev/null || true' EXIT HUP INT TERM
	render_rules deny-all '' '' "$tmp_rules"
	apply_rules "$tmp_rules"
	install -m 0600 "$tmp_rules" "$STATE_DIR/rules.nft"
	if [ "$persist_mode" = yes ]; then
		printf '%s\n' deny-all >"$tmp_rules.mode"
		install -m 0600 "$tmp_rules.mode" "$STATE_DIR/mode"
	fi
	rm -f "$tmp_rules" "$tmp_rules.mode"
	release_lock
	printf 'Public HTTP and HTTPS ingress denied\n'
}

service_refresh() {
	mode=
	if [ -f "$STATE_DIR/mode" ]; then
		mode=$(sed -n '1p' "$STATE_DIR/mode")
	fi
	case $mode in
		deny-all)
			deny_all
			;;
		allow-cloudflare|'')
			if ! "$NFT_BIN" list table "$NFT_FAMILY" "$NFT_TABLE" >/dev/null 2>&1; then
				deny_all no
			fi
			apply_cache_if_present
			refresh
			;;
		*)
			die "invalid cached origin guard mode: $mode"
			;;
	esac
}

remove_guard() {
	require_command "$NFT_BIN"
	if "$NFT_BIN" list table "$NFT_FAMILY" "$NFT_TABLE" >/dev/null 2>&1; then
		"$NFT_BIN" delete table "$NFT_FAMILY" "$NFT_TABLE"
	fi
}

case ${1:-refresh} in
	refresh) refresh ;;
	apply-cache) apply_cache ;;
	apply-cache-if-present) apply_cache_if_present ;;
	deny-all) deny_all ;;
	service-refresh) service_refresh ;;
	status) exec "$NFT_BIN" list table "$NFT_FAMILY" "$NFT_TABLE" ;;
	remove) remove_guard ;;
	*) die "usage: $0 {refresh|apply-cache|apply-cache-if-present|deny-all|service-refresh|status|remove}" ;;
esac
