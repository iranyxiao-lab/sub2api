#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
[ "$(id -u)" -eq 0 ] || { echo "run as root" >&2; exit 1; }

install -d -m 0750 /var/lib/sub2api-origin-guard
install -m 0755 "$script_dir/origin-guard.sh" /usr/local/sbin/sub2api-origin-guard
install -m 0644 "$script_dir/sub2api-origin-guard.service" /etc/systemd/system/sub2api-origin-guard.service
install -m 0644 "$script_dir/sub2api-origin-guard.timer" /etc/systemd/system/sub2api-origin-guard.timer
systemctl daemon-reload
/usr/local/sbin/sub2api-origin-guard refresh
systemctl enable sub2api-origin-guard.service
systemctl enable --now sub2api-origin-guard.timer
systemctl --no-pager status sub2api-origin-guard.timer
