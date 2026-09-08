#!/bin/sh
# One-command bootstrap for the Alpine/OpenRC node edition.
set -eu
umask 077
die() { echo "ERROR: $*" >&2; exit 1; }
action=${1:-install}
[ "$#" -le 1 ] || die 'Usage: sh install-node.sh [install|upgrade]'
case "$action" in install|upgrade) ;; *) die 'Usage: sh install-node.sh [install|upgrade]';; esac
[ "$(id -u)" = 0 ] || die 'Run as root.'
[ -f /etc/alpine-release ] && command -v rc-service >/dev/null || die 'Alpine Linux with OpenRC is required.'
# Pin the node release: the repository also publishes full-panel releases.
version=0.1.19
case "$(uname -m)" in
    x86_64) arch=amd64; expected=a3615f1f5047a209b6ba9ffbe3882133c37ada1fe733dfaec002abc153b202b7;;
    aarch64) arch=arm64; expected=b00bd8e9d75f8943707d69e0417c63b3dbbc904d3ef1b7fd377df453d2dd76dd;;
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
url="https://github.com/opxqo/3x-ui/releases/download/v$version-node/$asset"
echo "Downloading 3x-ui-node $version ($arch)..."
curl --fail --location --show-error --retry 3 --connect-timeout 15 --max-time 600 \
    --proto '=https' --proto-redir '=https' --output "$package" "$url"
actual=$(sha256sum "$package" | awk '{print $1}')
[ "$actual" = "$expected" ] || die 'Package checksum mismatch; installation aborted.'
# Execute only after verification; the package installer validates its contents.
tar -xzf "$package" -C "$work" install.sh
sh "$work/install.sh" "$action" "$package" "$expected"
echo 'Run 3x-ui-node credentials to view the node Token and TLS fingerprint.'
