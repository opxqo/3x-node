#!/bin/sh
# Usage: sh install.sh install|upgrade PACKAGE SHA256
# The SHA256 must come from a trusted release channel; it is not a signature.
set -eu
umask 077
die() { echo "$*" >&2; exit 1; }
[ "$(id -u)" = 0 ] || die 'Run as root on Alpine.'
[ -f /etc/alpine-release ] && command -v rc-service >/dev/null || die 'Alpine with OpenRC is required.'
case "$(uname -m)" in x86_64) arch=amd64;; aarch64) arch=arm64;; *) die 'Only amd64 and arm64 supported.';; esac
action=${1:-}
case "$action" in install|upgrade) ;; *) die 'Usage: install.sh install|upgrade PACKAGE SHA256';; esac
[ "$#" = 3 ] || die 'Package and trusted SHA256 required.'
package=$2
expected=$3
[ "${#expected}" = 64 ] || die 'Expected SHA256 must have 64 hex digits.'
case "$expected" in *[!0-9a-f]*) die 'Invalid SHA256';; esac
[ -f "$package" ] || die 'Package not found.'
actual=$(sha256sum "$package" | awk '{print $1}')
[ "$actual" = "$expected" ] || die 'Package checksum mismatch.'
# Check container limits, never trust host MemTotal alone.
mem=$(awk '/MemTotal:/ {print $2*1024}' /proc/meminfo)
for file in /sys/fs/cgroup/memory.max /sys/fs/cgroup/memory/memory.limit_in_bytes; do
    if [ -r "$file" ]; then
        limit=$(head -n 1 "$file")
        case "$limit" in max|'') ;; *) mem=$(awk -v a="$mem" -v b="$limit" 'BEGIN {printf "%.0f", (a<b?a:b)}');; esac
    fi
done
awk -v m="$mem" 'BEGIN {exit (m<100663296)}' || die 'Effective memory below 96MiB; installation refused.'
echo "Effective container memory: $mem bytes (128MiB qualification is pending)."
free_kb=$(df -Pk /usr/local | awk 'END {print $4}')
[ "$free_kb" -ge 307200 ] || die 'At least 300MiB free needed for safe staging and rollback.'
command -v netstat >/dev/null || die 'BusyBox netstat is required for port checks.'
base=/usr/local/lib/3x-ui-node
mkdir -p "$base" /etc/3x-ui-node /var/lib/3x-ui-node
release="$base/release-$expected"
[ ! -e "$release" ] || die 'This release already exists; no change made.'

if [ "$action" = install ]; then
    [ ! -e "$base/current" ] && [ ! -e /etc/3x-ui-node/config.json ] || die 'Existing installation: use upgrade.'
    netstat -lnt | awk '$4 ~ /:(2053|62789)$/ {found=1} END {exit !found}' && die 'Default API port 2053 or private port 62789 occupied.'
else
    [ -L "$base/current" ] && [ -f /etc/3x-ui-node/config.json ] || die 'No managed node installation found.'
fi

# Keep only the archive index in the temporary directory. The previous
# installer unpacked the whole package there and then copied the large Xray
# binary into the release directory, creating an avoidable memory/page-cache
# spike on 128MiB containers.
stage=$(mktemp -d /usr/local/lib/3x-ui-node-stage.XXXXXX)
tmp_release="$base/.release-$expected.$$"
cleanup() {
    rm -rf -- "$stage" 2>/dev/null || true
    if [ -n "${tmp_release:-}" ]; then
        rm -rf -- "$tmp_release" 2>/dev/null || true
    fi
}
trap cleanup EXIT HUP INT TERM
# Only named plain files are accepted; forbid traversal, links and special files.
tar -tzf "$package" | sort > "$stage/list"
printf '%s\n' 3x-ui-node LICENSE XRAY-LICENSE README.md VALIDATION.md install.sh manifest service xray | sort > "$stage/expected"
cmp -s "$stage/list" "$stage/expected" || die 'Not a node-only package.'
tar -tvzf "$package" | awk 'substr($0,1,1)!="-" {bad=1} END {exit bad}' || die 'Package contains links or special files.'
mkdir "$tmp_release"
tar -xzf "$package" -C "$tmp_release"
[ "$(sed -n '1p' "$tmp_release/manifest")" = 3x-ui-node ] || die 'Wrong package kind.'
[ "$(sed -n '3p' "$tmp_release/manifest")" = "$arch" ] || die 'Wrong architecture.'
[ "$(sed -n '4p' "$tmp_release/manifest")" = 26.7.28 ] || die 'Wrong Xray version.'
chown 0:0 "$tmp_release"/*
chmod 755 "$tmp_release/3x-ui-node" "$tmp_release/xray"
"$tmp_release/3x-ui-node" version
"$tmp_release/xray" version | head -n 1
old=''
if [ "$action" = upgrade ]; then
    old=$(readlink "$base/current")
    rc-service 3x-ui-node stop
    [ ! -f /var/lib/3x-ui-node/state.json ] || cp -p /var/lib/3x-ui-node/state.json /var/lib/3x-ui-node/state.pre-upgrade.json
fi
mv "$tmp_release" "$release"
tmp_release=''
ln -s "$release" "$base/current.next"
mv -Tf "$base/current.next" "$base/current"
ln -sf "$base/current/3x-ui-node" /usr/local/bin/3x-ui-node
if [ ! -e /usr/local/bin/x-ui ] && [ ! -L /usr/local/bin/x-ui ]; then
    ln -s "$base/current/3x-ui-node" /usr/local/bin/x-ui
fi
cp "$release/service" /etc/init.d/3x-ui-node
chmod 755 /etc/init.d/3x-ui-node
if [ "$action" = install ]; then
    3x-ui-node init -xray "$base/current/xray"
fi
ready() {
    tries=0
    while [ "$tries" -lt 10 ]; do
        if 3x-ui-node status >/dev/null 2>&1; then return 0; fi
        tries=$((tries + 1))
        sleep 1
    done
    return 1
}
if ! 3x-ui-node check || ! rc-service 3x-ui-node start || ! ready; then
    rc-service 3x-ui-node stop || true
    if [ -n "$old" ]; then
        ln -s "$old" "$base/current.next"
        mv -Tf "$base/current.next" "$base/current"
        cp "$old/service" /etc/init.d/3x-ui-node
        rc-service 3x-ui-node start || true
    fi
    die 'Activation failed; previous binary restored if available. Inspect node.log.'
fi
rc-update add 3x-ui-node default
if [ -n "$old" ]; then
    obsolete=$(readlink "$base/previous" || true)
    ln -s "$old" "$base/previous.next"
    mv -Tf "$base/previous.next" "$base/previous"
    if [ -n "$obsolete" ] && [ "$obsolete" != "$old" ] && [ "$obsolete" != "$release" ]; then
        suffix=${obsolete#"$base/release-"}
        case "$suffix" in *[!0-9a-f]*|'') die 'Unexpected older release path; retained for inspection.';; esac
        [ "${#suffix}" = 64 ] && [ ! -L "$obsolete" ] && [ "$(head -n 1 "$obsolete/manifest")" = 3x-ui-node ] || die 'Unexpected older release; retained.'
        rm -r -- "$obsolete"
        echo "Removed superseded rollback release: $obsolete (not recoverable locally)."
    fi
fi
echo 'Installed. Run 3x-ui-node credentials locally; do not publish its token.'
echo 'The immediately previous release is retained for rollback.'
