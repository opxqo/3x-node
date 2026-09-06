package main

import (
	stdbytes "bytes"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/node"
)

func TestBytes(t *testing.T) {
	tests := map[uint64]string{
		0:       "0 B",
		1023:    "1023 B",
		1024:    "1.00 KiB",
		1048576: "1.00 MiB",
	}
	for input, want := range tests {
		if got := bytes(input); got != want {
			t.Errorf("bytes(%d) = %q, want %q", input, got, want)
		}
	}
}

func TestSummarizeInboundDefaults(t *testing.T) {
	got := summarizeInbound(node.Inbound{StreamSettings: node.Object(`{}`)})
	if got.Network != "tcp" || got.Security != "none" {
		t.Fatalf("unexpected defaults: %#v", got)
	}
}

func TestMenuHelpDoesNotNeedConfig(t *testing.T) {
	var output stdbytes.Buffer
	if err := runMenu(node.Config{}, []string{"help"}, nil, &output); err != nil {
		t.Fatal(err)
	}
	if output.Len() == 0 {
		t.Fatal("expected help output")
	}
}
