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
version=0.1.25
case "$(uname -m)" in
    x86_64) arch=amd64; expected=4fafda3f5045d077843275c07f75d658fc3a1e90b6ae5456fd91aa2e291586bb;;
    aarch64) arch=arm64; expected=4ea1641402871d3b58e7e309d497be4147584dc42fbea5e3507f7192515ff9a5;;
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
curl --fail --location --show-error --retry 3 --connect-timeout 15 --max-time 600 \
    --proto '=https' --proto-redir '=https' --output "$package" "$url"
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
