#!/bin/sh
# Run on a development machine, never on the VPS.
set -eu
root=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$root"
go_bin=${GO_BIN:-go}
out=${NODE_DIST:-"$root/dist/node"}
mkdir -p "$out"
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT HUP INT TERM
for arch in amd64 arm64; do
    case "$arch" in
        amd64) asset=Xray-linux-64.zip; sha=8195d909f1109b8f3d99eefe401a3c451d7bf4af71f24d3815420f77e5dd2a40 ;;
        arm64) asset=Xray-linux-arm64-v8a.zip; sha=f5698bb218ada3b4022db26fafc39601c5f53b46b19eb76c9616325985807501 ;;
    esac
    dir="$stage/$arch"
    mkdir "$dir"
    curl --fail --location --retry 3 "https://github.com/XTLS/Xray-core/releases/download/v26.7.28/$asset" -o "$stage/$asset"
    actual=$(openssl dgst -sha256 "$stage/$asset" | awk '{print $NF}')
    [ "$actual" = "$sha" ] || { echo 'Xray checksum mismatch' >&2; exit 1; }
    unzip -p "$stage/$asset" xray > "$dir/xray"
    chmod 755 "$dir/xray"
    CGO_ENABLED=0 GOOS=linux GOARCH="$arch" "$go_bin" build -trimpath -ldflags='-s -w' -o "$dir/3x-ui-node" ./cmd/3x-ui-node
    cp scripts/node/3x-ui-node.initd "$dir/service"
    cp scripts/node/install.sh "$dir/install.sh"
    cp LICENSE "$dir/LICENSE"
    unzip -p "$stage/$asset" LICENSE > "$dir/XRAY-LICENSE"
    cp docs/node/README.md "$dir/README.md"
    cp docs/node/VALIDATION.md "$dir/VALIDATION.md"
	printf '3x-ui-node\n0.1.8-node\n%s\n26.7.28\n' "$arch" > "$dir/manifest"
	archive="$out/3x-ui-node-0.1.8-linux-$arch.tar.gz"
    COPYFILE_DISABLE=1 tar -czf "$archive" -C "$dir" 3x-ui-node xray service install.sh LICENSE XRAY-LICENSE README.md VALIDATION.md manifest
    openssl dgst -sha256 "$archive" > "$archive.sha256"
done
echo "Packages: $out"
