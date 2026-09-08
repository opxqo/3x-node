package node

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Metrics struct {
	at       time.Time
	cpuTotal uint64
	cpuIdle  uint64
	netSent  uint64
	netRecv  uint64
	cached   map[string]any
}

func readUint(path string) uint64 {
	b, _ := os.ReadFile(path)
	n, _ := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	return n
}

func (n *Node) Status() map[string]any {
	n.mu.Lock()
	defer n.mu.Unlock()
	m := &n.metrics
	if time.Since(m.at) < time.Second && m.cached != nil {
		return m.cached
	}
	total, current := memoryUsage()
	cpu := 0.0
	if b, err := os.ReadFile("/proc/stat"); err == nil {
		line := strings.SplitN(string(b), "\n", 2)[0]
		f := strings.Fields(line)
		var sum, idle uint64
		for j, v := range f[1:] {
			x, _ := strconv.ParseUint(v, 10, 64)
			sum += x
			if j == 3 || j == 4 {
				idle += x
			}
		}
		if sum > m.cpuTotal {
			cpu = 100 * (1 - float64(idle-m.cpuIdle)/float64(sum-m.cpuTotal))
		}
		m.cpuTotal, m.cpuIdle = sum, idle
	}
	sent, recv := netBytes()
	var up, down uint64
	if !m.at.IsZero() {
		dt := time.Since(m.at).Seconds()
		if sent >= m.netSent {
			up = uint64(float64(sent-m.netSent) / dt)
		}
		if recv >= m.netRecv {
			down = uint64(float64(recv-m.netRecv) / dt)
		}
	}
	xstate := "running"
	xerr := ""
	if !n.engine.Running() {
		xstate = "error"
		xerr = "Xray is not running"
	}
	m.cached = map[string]any{"cpu": cpu, "mem": map[string]uint64{"current": current, "total": total}, "xray": map[string]string{"state": xstate, "errorMsg": xerr, "version": XrayVersion}, "panelVersion": Version, "panelGuid": n.state.GUID, "uptime": uint64(time.Since(n.started).Seconds()), "netIO": map[string]uint64{"up": up, "down": down}, "nodeAgent": map[string]any{"version": Version, "compatibility": "ed6bc1d8", "xrayPid": n.engine.PID()}}
	m.cached["managementActivity"] = n.management
	m.cached["memoryDetail"] = memoryDetail("/sys/fs/cgroup")
	m.at = time.Now()
	m.netSent, m.netRecv = sent, recv
	return m.cached
}

func memoryUsage() (uint64, uint64) {
	if limit := readUint("/sys/fs/cgroup/memory.max"); limit > 0 && limit < 1<<60 {
		return limit, readUint("/sys/fs/cgroup/memory.current")
	}
	if limit := readUint("/sys/fs/cgroup/memory/memory.limit_in_bytes"); limit > 0 && limit < 1<<60 {
		return limit, readUint("/sys/fs/cgroup/memory/memory.usage_in_bytes")
	}
	b, _ := os.ReadFile("/proc/meminfo")
	var total, available uint64
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) > 1 {
			v, _ := strconv.ParseUint(f[1], 10, 64)
			if f[0] == "MemTotal:" {
				total = v * 1024
			}
			if f[0] == "MemAvailable:" {
				available = v * 1024
			}
		}
	}
	if available > total {
		available = total
	}
	return total, total - available
}

func netBytes() (uint64, uint64) {
	b, _ := os.ReadFile("/proc/net/dev")
	var sent, recv uint64
	for _, line := range strings.Split(string(b), "\n") {
		name, rest, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(name) == "lo" {
			continue
		}
		f := strings.Fields(rest)
		if len(f) >= 9 {
			r, _ := strconv.ParseUint(f[0], 10, 64)
			s, _ := strconv.ParseUint(f[8], 10, 64)
			recv += r
			sent += s
		}
	}
	return sent, recv
}
