package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/node"
)

const (
	testRealityPrivate = "0Av0A8BdPDmPHbPDdo1PpY6MccPrNHnSnPS6OUJvkV0"
	testRealityPublic  = "hUTPcmfVkhhao9QGBJyaiZNFMCMDkIeSum0ujSBkqQg"
	otherRealityPublic = "y0aLu0AEdKyVRRoTrLj2WiXIxnf-K7hmiBXJGbxm0BI"
)

func TestDerivePublicKey(t *testing.T) {
	tests := []struct {
		name, private, want, wantErr string
	}{
		{name: "x25519 pair", private: testRealityPrivate, want: testRealityPublic},
		{name: "surrounding space", private: " " + testRealityPrivate + "\n", want: testRealityPublic},
		{name: "not base64url", private: "not a key!", wantErr: "illegal base64 data"},
		{name: "wrong length", private: "AAAA", wantErr: "invalid private key size"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := derivePublicKey(tt.private)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("derivePublicKey(%q) error = %v, want containing %q", tt.private, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("derivePublicKey(%q) = %v", tt.private, err)
			}
			if got != tt.want {
				t.Fatalf("derivePublicKey(%q) = %q, want %q", tt.private, got, tt.want)
			}
		})
	}
}

func realityInbound(t *testing.T, storedPublicKey string) node.Inbound {
	t.Helper()
	stream := map[string]any{
		"network":  "tcp",
		"security": "reality",
		"realitySettings": map[string]any{
			"target":       "www.microsoft.com:443",
			"serverNames":  []string{"wwwqa.microsoft.com", "www.microsoft.com"},
			"privateKey":   testRealityPrivate,
			"shortIds":     []string{"b642", "30"},
			"minClientVer": "0.0.1",
			"settings":     map[string]any{"publicKey": storedPublicKey, "fingerprint": "chrome"},
		},
	}
	settings := map[string]any{"clients": []map[string]any{
		{"id": "6a98a2bc-68cd-4562-b8ef-542201195f1c", "email": "hk-user", "enable": true, "flow": "xtls-rprx-vision"},
	}}
	in := node.Inbound{ID: 3, Port: 52696, Protocol: "vless", Tag: "in-52696-tcp", Remark: "Fuckip-HK", Enable: true}
	raw, err := json.Marshal(stream)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &in.StreamSettings); err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &in.Settings); err != nil {
		t.Fatal(err)
	}
	return in
}

// The stored settings.publicKey is what the panel hands to clients, but xray
// only ever uses privateKey — a drifted pair must be visible in the pane.
func TestInboundDetailReportsRealityKeyMismatch(t *testing.T) {
	body := strings.Join(inboundDetail(realityInbound(t, otherRealityPublic)), "\n")
	if !strings.Contains(body, "公钥          "+testRealityPublic) {
		t.Fatalf("derived public key missing:\n%s", body)
	}
	if !strings.Contains(body, "⚠ 配置里存的是 "+otherRealityPublic) {
		t.Fatalf("mismatch not reported:\n%s", body)
	}
	matched := strings.Join(inboundDetail(realityInbound(t, testRealityPublic)), "\n")
	if strings.Contains(matched, "⚠") {
		t.Fatalf("a matching pair must not warn:\n%s", matched)
	}
}

func TestInboundDetailShowsRealityAndClientFields(t *testing.T) {
	body := strings.Join(inboundDetail(realityInbound(t, testRealityPublic)), "\n")
	for _, want := range []string{
		"监听          0.0.0.0:52696",
		"目标          www.microsoft.com:443",
		"SNI           wwwqa.microsoft.com, www.microsoft.com",
		"Short IDs     b642, 30",
		"私钥          " + testRealityPrivate,
		"客户端版本    0.0.1 ~ —",
		"客户端 (1)",
		"6a98a2bc-68cd-4562-b8ef-542201195f1c  flow=xtls-rprx-vision",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("detail is missing %q:\n%s", want, body)
		}
	}
}

func TestInboundBrowserNavigation(t *testing.T) {
	b := &inboundBrowser{inbounds: []node.Inbound{
		{ID: 1, Port: 26700, Protocol: "vless", Tag: "in-26700-tcp", Enable: true},
		realityInbound(t, testRealityPublic),
	}}
	if got := b.lines()[1]; !strings.HasPrefix(got, inboundCursor) {
		t.Fatalf("cursor starts off the first row: %q", got)
	}
	if !b.key('j') || b.cursor != 1 {
		t.Fatalf("j did not move the cursor: %d", b.cursor)
	}
	if !b.key('j') || b.cursor != 1 {
		t.Fatalf("cursor ran past the last inbound: %d", b.cursor)
	}
	if got := b.lines()[2]; !strings.HasPrefix(got, inboundCursor) {
		t.Fatalf("cursor row not marked: %q", got)
	}
	if !b.key('\r') || b.detail == nil {
		t.Fatal("Enter did not open the detail view")
	}
	if got, want := b.title(), "入站 #3 · in-52696-tcp"; got != want {
		t.Fatalf("detail title = %q, want %q", got, want)
	}
	if b.key('j') {
		t.Fatal("detail view must let j fall through to scrolling")
	}
	if !b.key(27) || b.detail != nil {
		t.Fatal("Esc did not return to the list")
	}
	if b.key(27) {
		t.Fatal("Esc on the list must fall through and close the pane")
	}
}

func TestInboundBrowserFollowKeepsCursorVisible(t *testing.T) {
	b := &inboundBrowser{inbounds: make([]node.Inbound, 8)}
	b.cursor = 7
	if got := b.follow(4); got != 5 {
		t.Fatalf("follow(4) = %d, want 5", got)
	}
	b.cursor = 0
	if got := b.follow(4); got != 0 {
		t.Fatalf("follow(4) after moving to the top = %d, want 0", got)
	}
	b.detail = []string{"x"}
	if got := b.follow(4); got != 0 {
		t.Fatalf("detail view must start at the top, got %d", got)
	}
}

// The pane renderer picks the highlighted row out of plain text by its marker,
// so a browser row and the renderer must keep agreeing on it.
func TestInboundCursorRowRendersHighlighted(t *testing.T) {
	b := &inboundBrowser{inbounds: []node.Inbound{
		{ID: 1, Port: 26700, Protocol: "vless", Tag: "in-26700-tcp", Enable: true},
		{ID: 3, Port: 52696, Protocol: "vless", Tag: "in-52696-tcp", Enable: true},
	}}
	b.cursor = 1
	var highlighted []string
	for _, row := range renderRows(t, 100, 40, 0, b.follow(20), b.lines(), b.title()) {
		if _, after, ok := strings.Cut(row, "\x1b[7m"); ok {
			text, _, _ := strings.Cut(after, "\x1b[0m")
			highlighted = append(highlighted, strings.TrimRight(text, " "))
		}
	}
	if len(highlighted) != 1 {
		t.Fatalf("want exactly one highlighted row, got %d: %q", len(highlighted), highlighted)
	}
	if !strings.Contains(highlighted[0], "52696") || !strings.HasPrefix(highlighted[0], "  "+inboundCursor) {
		t.Fatalf("highlighted row is not the cursor row: %q", highlighted[0])
	}
}
