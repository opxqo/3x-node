#!/bin/sh
# Local isolated test lab. Does not contact the VPS or consume its bandwidth.
set -eu
root=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$root"
duration=${1:-60s}
go_bin=${GO_BIN:-go}
case "$(docker info --format '{{.Architecture}}')" in aarch64|arm64) arch=arm64;; x86_64|amd64) arch=amd64;; *) exit 1;; esac
archive="$root/dist/node/3x-ui-node-0.1.20-linux-$arch.tar.gz"
[ -f "$archive" ] || { echo 'Build packages first.' >&2; exit 1; }
mkdir -p "$root/.cache"
lab=$(mktemp -d "$root/.cache/node-lab.XXXXXX")
suffix=${lab##*.}
service="node-service-$suffix"
driver="node-driver-$suffix"
network="node-network-$suffix"
mkdir "$lab/tools" "$lab/settings" "$lab/state" "$lab/tools/xray-linux"
tar -xzf "$archive" -C "$lab/tools"
mv "$lab/tools/xray" "$lab/tools/xray-linux/xray"
CGO_ENABLED=0 GOOS=linux GOARCH="$arch" "$go_bin" build -trimpath -ldflags='-s -w' -o "$lab/tools/nodebench" ./tools/nodebench
docker network create --internal --subnet 11.233.0.0/24 "$network"
printf 'service=%s\ndriver=%s\nnetwork=%s\nduration=%s\n' "$service" "$driver" "$network" "$duration" > "$lab/lab.txt"
echo "Lab artifacts: $lab"
docker run --rm --network none -v "$lab/tools:/tools:ro" -v "$lab/settings:/settings" -v "$lab/state:/state" alpine:3.24 /tools/3x-ui-node init -config /settings/config.json -state /state/state.json -xray /tools/xray-linux/xray
docker run -d --name "$service" --network "$network" --ip 11.233.0.2 --cpus=1 --memory=96m --memory-swap=96m -v "$lab/tools:/tools:ro" -v "$lab/settings:/settings" -v "$lab/state:/state" alpine:3.24 /tools/3x-ui-node serve -config /settings/config.json
tries=0
until docker exec "$service" /tools/3x-ui-node status -config /settings/config.json >/dev/null 2>&1; do
    tries=$((tries+1)); [ "$tries" -lt 15 ] || { echo 'Node startup failed'; exit 1; }; sleep 1
done
docker run -d --name "$driver" --network "$network" --ip 11.233.0.3 -v "$lab/tools:/tools:ro" -v "$lab/settings:/settings:ro" alpine:3.24 /tools/nodebench -duration "$duration"
while [ "$(docker inspect -f '{{.State.Running}}' "$driver")" = true ]; do
    sh scripts/node/resource-sample.sh "$service" >> "$lab/resources.txt"
    sleep 30
done
docker logs "$driver" > "$lab/load.jsonl" 2> "$lab/driver.log"
sh scripts/node/resource-sample.sh "$service" >> "$lab/resources.txt"
code=$(docker inspect -f '{{.State.ExitCode}}' "$driver")
docker inspect -f '{{json .State}}' "$service" > "$lab/service-state.json"
docker stop "$service"
echo "Finished: driver exit=$code; artifacts=$lab (containers and network retained for inspection)."
exit "$code"
