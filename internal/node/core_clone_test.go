package node

import (
	"fmt"
	"testing"
)

func cloneFixture() []LiveInbound {
	in := []LiveInbound{{Tag: "test", Stream: Object(`{"network":"tcp","security":"none"}`), Users: map[string]Account{}}}
	for i := 0; i < 64; i++ {
		in[0].Users[fmt.Sprint(i)] = Account{ID: "00000000-0000-4000-8000-000000000020"}
	}
	return in
}
func TestCloneLiveIsolation(t *testing.T) {
	original := cloneFixture()
	copy := cloneLive(original)
	copy[0].Stream[0] = ' '
	delete(copy[0].Users, "0")
	copy[0].Tag = "changed"
	if original[0].Stream[0] != '{' || len(original[0].Users) != 64 || original[0].Tag != "test" {
		t.Fatal("clone aliases source")
	}
	if cloneLive(nil) != nil {
		t.Fatal("nil changed")
	}
}
func BenchmarkCloneLive64(b *testing.B) {
	in := cloneFixture()
	b.ReportAllocs()
	for b.Loop() {
		_ = cloneLive(in)
	}
}
