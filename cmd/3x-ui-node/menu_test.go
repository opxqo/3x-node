package main

import (
	"bufio"
	stdbytes "bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
	if err := runMenu("/tmp/config.json", node.Config{}, []string{"help"}, nil, &output); err != nil {
		t.Fatal(err)
	}
	if output.Len() == 0 {
		t.Fatal("expected help output")
	}
}

func TestManualClientBuildsUsableDefaults(t *testing.T) {
	client, err := manualClient("", "alice", true)
	if err != nil {
		t.Fatal(err)
	}
	if client.Text("email") != "alice" || !client.Enabled() || client.Text("flow") != "" {
		t.Fatalf("unexpected client: %#v", client)
	}
	id := client.Text("id")
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		t.Fatalf("generated UUID is invalid: %q", id)
	}
}

func TestInboundIDsAcceptsCommaSeparatedPositiveIDs(t *testing.T) {
	ids, err := inboundIDs("5, 12")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != 5 || ids[1] != 12 {
		t.Fatalf("IDs = %#v, want [5 12]", ids)
	}
	if _, err := inboundIDs("5,zero"); err == nil {
		t.Fatal("invalid inbound IDs were accepted")
	}
}

func TestConfigureSyncStoresTokenSeparately(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	secret := "master-token-must-not-be-printed"
	input := stdbytes.NewBufferString("https://master.example/panel/api-docs\n" + secret + "\n" +
		"0000000000000000000000000000000000000000000000000000000000000000\n60\n1\nhk\n77\n1\ny\n")
	var output stdbytes.Buffer
	if err := configureSync(configPath, node.Config{}, bufio.NewReader(input), &output, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), secret) {
		t.Fatal("master token was printed during configuration")
	}
	configBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(configBytes), secret) {
		t.Fatal("master token was stored in config JSON")
	}
	var saved map[string]any
	if err = json.Unmarshal(configBytes, &saved); err != nil {
		t.Fatal(err)
	}
	tokenPath := filepath.Join(dir, "master.token")
	info, err := os.Stat(tokenPath)
	if err != nil {
		t.Fatalf("token file missing: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("token file permissions = %v", info.Mode().Perm())
	}
	if got, err := os.ReadFile(tokenPath); err != nil || strings.TrimSpace(string(got)) != secret {
		t.Fatalf("token file contents invalid: %q, err=%v", got, err)
	}
}
