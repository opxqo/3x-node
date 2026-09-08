package node

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// All counters are read from one visible cgroup scope. Missing metrics remain
// nil, not zero. Working set is an estimate, not unreclaimable memory.
type MemoryDetail struct {
	Source         string   `json:"source"`
	Current        uint64   `json:"current"`
	Limit          uint64   `json:"limit"`
	Available      *uint64  `json:"available"`
	ProbeUsed      *uint64  `json:"probeUsed"`
	WorkingSet     *uint64  `json:"workingSet"`
	File           *uint64  `json:"file"`
	Anon           *uint64  `json:"anon"`
	Swap           *uint64  `json:"swap"`
	OOMKills       *uint64  `json:"oomKills"`
	PressureSome10 *float64 `json:"pressureSome10"`
}

func memoryDetail(root string) *MemoryDetail {
	read := func(name string) (string, bool) {
		b, e := os.ReadFile(filepath.Join(root, name))
		return strings.TrimSpace(string(b)), e == nil
	}
	number := func(name string) *uint64 {
		s, ok := read(name)
		if !ok {
			return nil
		}
		v, e := strconv.ParseUint(s, 10, 64)
		if e != nil {
			return nil
		}
		return &v
	}
	limit, current := number("memory.max"), number("memory.current")
	if limit == nil || current == nil || *limit == 0 || *limit >= 1<<60 {
		return nil
	}
	d := &MemoryDetail{Source: "cgroup v2", Current: *current, Limit: *limit, Swap: number("memory.swap.current")}
	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		var totalKB, availableKB uint64
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) < 2 {
				continue
			}
			v, e := strconv.ParseUint(f[1], 10, 64)
			if e != nil {
				continue
			}
			switch f[0] {
			case "MemTotal:":
				totalKB = v
			case "MemAvailable:":
				availableKB = v
			}
		}
		if totalKB > 0 && availableKB <= totalKB {
			probeTotal := totalKB * 1024
			// Some container runtimes expose the host's /proc/meminfo while
			// cgroup memory.max is container-scoped. In that case applying the
			// probe formula would silently clamp host memory to the cgroup limit
			// and report a meaningless zero or near-zero value. Only publish the
			// probe metric when both views describe roughly the same scope.
			if probeTotal >= *limit/2 && probeTotal <= *limit*2 {
				available := availableKB * 1024
				if available > probeTotal {
					available = probeTotal
				}
				used := probeTotal - available
				d.Available, d.ProbeUsed = &available, &used
			}
		}
	}
	if stat, ok := read("memory.stat"); ok {
		for _, line := range strings.Split(stat, "\n") {
			f := strings.Fields(line)
			if len(f) != 2 {
				continue
			}
			v, e := strconv.ParseUint(f[1], 10, 64)
			if e != nil {
				continue
			}
			switch f[0] {
			case "file":
				d.File = &v
			case "anon":
				d.Anon = &v
			case "inactive_file":
				w := *current - min(*current, v)
				d.WorkingSet = &w
			}
		}
	}
	if events, ok := read("memory.events"); ok {
		for _, line := range strings.Split(events, "\n") {
			f := strings.Fields(line)
			if len(f) == 2 && f[0] == "oom_kill" {
				v, e := strconv.ParseUint(f[1], 10, 64)
				if e == nil {
					d.OOMKills = &v
				}
			}
		}
	}
	if pressure, ok := read("memory.pressure"); ok {
		for _, line := range strings.Split(pressure, "\n") {
			f := strings.Fields(line)
			if len(f) > 1 && f[0] == "some" {
				for _, field := range f[1:] {
					if s, ok := strings.CutPrefix(field, "avg10="); ok {
						v, e := strconv.ParseFloat(s, 64)
						if e == nil && v >= 0 && v <= 100 {
							d.PressureSome10 = &v
						}
					}
				}
			}
		}
	}
	return d
}
