#!/bin/sh
# One-command bootstrap for the Alpine/OpenRC node edition.
set -eu
umask 077
die() { echo "ERROR: $*" >&2; exit 1; }
usage='Usage: sh install-node.sh [install [--enroll-url URL --enroll-code CODE]|upgrade]'
action=${1:-install}
[ "$#" -eq 0 ] || shift
case "$action" in install|upgrade) ;; *) die "$usage";; esac
enroll_url=''
enroll_code=''
while [ "$#" -gt 0 ]; do
    case "$1" in
        --enroll-url) [ "$#" -ge 2 ] || die "$usage"; enroll_url=$2; shift 2;;
        --enroll-code) [ "$#" -ge 2 ] || die "$usage"; enroll_code=$2; shift 2;;
        *) die "$usage";;
    esac
done
if [ -n "$enroll_url$enroll_code" ]; then
    [ "$action" = install ] || die 'Enrollment flags are accepted only with install.'
    [ -n "$enroll_url" ] && [ -n "$enroll_code" ] || die 'Both --enroll-url and --enroll-code are required.'
    case "$enroll_url" in https://?*) ;; *) die 'Enrollment URL must use https.';; esac
    printf '%s' "$enroll_code" | grep -Eq '^[A-Za-z0-9_-]{16,128}$' || die 'Invalid enrollment code.'
fi
[ "$(id -u)" = 0 ] || die 'Run as root.'
[ -f /etc/alpine-release ] && command -v rc-service >/dev/null || die 'Alpine Linux with OpenRC is required.'
# Pin the node release: the repository also publishes full-panel releases.
version=0.1.27
case "$(uname -m)" in
    x86_64) arch=amd64; expected=8aca9b3534ffbb9dbecefb74ad9959c43503ab47e0a234b69f51c5fb95a523b9;;
    aarch64) arch=arm64; expected=4198caeccac6f3f7f233d05e7047159fd7de1be961d1b1e228f4a31d7a98b480;;
    *) die 'Only amd64 and arm64 are supported.';;
esac
base=/usr/local/lib/3x-ui-node
if [ "$action" = install ]; then
    [ ! -e "$base/current" ] && [ ! -L "$base/current" ] && [ ! -e /etc/3x-ui-node/config.json ] || die 'Existing node installation: use upgrade.'
else
    [ -L "$base/current" ] && [ -f /etc/3x-ui-node/config.json ] || die 'No managed node installation found.'
    if [ "$(readlink "$base/current")" = "$base/release-$expected" ]; then
        echo "3x-ui-node $version is already installed."
        exit 0
    fi
fi
apk add --no-cache ca-certificates curl tar
# Stage on disk instead of /tmp, which may be tmpfs on small VPS containers.
mkdir -p /usr/local/lib
work=$(mktemp -d /usr/local/lib/3x-ui-node-download.XXXXXX)
trap 'rm -rf -- "$work"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP
asset="3x-ui-node-$version-linux-$arch.tar.gz"
package="$work/$asset"
url="https://github.com/opxqo/3x-node/releases/download/v$version-node/$asset"
echo "Downloading 3x-ui-node $version ($arch)..."
# Unstable routes to GitHub reset long downloads: resume instead of restarting; the checksum below still decides.
attempt=1
until curl --fail --location --silent --show-error --connect-timeout 15 --max-time 600 --speed-limit 1024 --speed-time 60 \
    --continue-at - --proto '=https' --proto-redir '=https' --output "$package" "$url"; do
    [ "$attempt" -lt 6 ] || die 'Download failed 6 times; check this server can reach github.com, then rerun.'
    attempt=$((attempt + 1))
    echo "Download interrupted; resuming (attempt $attempt/6)..."
    sleep $((attempt * 2))
done
actual=$(sha256sum "$package" | awk '{print $1}')
[ "$actual" = "$expected" ] || die 'Package checksum mismatch; installation aborted.'
# Execute only after verification; the package installer validates its contents.
tar -xzf "$package" -C "$work" install.sh
sh "$work/install.sh" "$action" "$package" "$expected"
if [ -z "$enroll_code" ]; then
    echo 'Run 3x-ui-node credentials to view the node Token and TLS fingerprint.'
elif 3x-ui-node enroll -url "$enroll_url" -code "$enroll_code"; then
    echo 'Node installed and enrolled; enable it in the master once reviewed.'
else
    die 'Node installed, but enrollment failed. Retry 3x-ui-node enroll -url URL -code CODE, or connect manually with 3x-ui-node credentials.'
fi
