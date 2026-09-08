package node

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMemoryDetailCacheAndUnknown(t *testing.T) {
	dir := t.TempDir()
	for name, data := range map[string]string{"memory.max": "134217728", "memory.current": "134053888", "memory.stat": "anon 29941760\nfile 97546240\ninactive_file 39358464\n", "memory.events": "oom_kill 2\n", "memory.pressure": "some avg10=0.14 avg60=0.03 total=100\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	m := memoryDetail(dir)
	if m == nil || m.WorkingSet == nil || *m.WorkingSet != 94695424 || m.Swap != nil || *m.OOMKills != 2 || *m.PressureSome10 != 0.14 {
		t.Fatalf("bad detail: %+v", m)
	}
	if err := os.WriteFile(filepath.Join(dir, "memory.stat"), []byte("inactive_file 999999999\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if *memoryDetail(dir).WorkingSet != 0 {
		t.Fatal("underflow")
	}
	if memoryDetail(t.TempDir()) != nil {
		t.Fatal("missing metrics fabricated")
	}
}
