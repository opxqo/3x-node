#!/bin/sh
# Read-only sampling; tiny docker-exec helpers are included in cgroup totals.
set -eu
container=${1:?container name required}
docker exec "$container" sh -c '
date -u +%Y-%m-%dT%H:%M:%SZ
for file in memory.current memory.peak memory.max memory.swap.max cpu.max memory.events; do
    echo "$file"; cat "/sys/fs/cgroup/$file"
done
awk "/^(anon|file|sock) /" /sys/fs/cgroup/memory.stat
for path in /proc/[0-9]*/status; do
    awk "/^(Name|VmRSS):/" "$path"
done
'
