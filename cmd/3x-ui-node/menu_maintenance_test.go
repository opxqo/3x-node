package main

import (
	"bufio"
	stdbytes "bytes"
	"strings"
	"testing"
)

func TestUpdateNodeCancelledWithoutConfirmation(t *testing.T) {
	for _, answer := range []string{"", "n", "no", "maybe"} {
		var out stdbytes.Buffer
		reader := bufio.NewReader(strings.NewReader(answer + "\n"))
		if err := updateNode(reader, &out); err != nil {
			t.Fatalf("answer %q: unexpected error: %v", answer, err)
		}
		if !strings.Contains(out.String(), "已取消") {
			t.Fatalf("answer %q should cancel the update, got %q", answer, out.String())
		}
	}
}

// A partial or case-mismatched answer must not trigger the irreversible
// delete path; only the exact word "uninstall" (case-insensitive) may.
func TestUninstallNodeRequiresExactConfirmation(t *testing.T) {
	for _, answer := range []string{"", "y", "yes", "Uninstal", "uninstall!"} {
		var out stdbytes.Buffer
		reader := bufio.NewReader(strings.NewReader(answer + "\n"))
		if err := uninstallNode(reader, &out); err != nil {
			t.Fatalf("answer %q: unexpected error: %v", answer, err)
		}
		if !strings.Contains(out.String(), "已取消") {
			t.Fatalf("answer %q should cancel the uninstall, got %q", answer, out.String())
		}
	}
}
