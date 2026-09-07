package node

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemovedPullRoutes(t *testing.T) {
	n, _ := testNode(t)
	for _, path := range []string{"sync/status", "sync/preview", "sync/now"} {
		method := "POST"
		if path == "sync/status" {
			method = "GET"
		}
		r := httptest.NewRequest(method, "/panel/api/"+path, nil)
		r.RemoteAddr = "127.0.0.1:12345"
		r.Header.Set("Authorization", "Bearer "+n.Config.Token)
		w := httptest.NewRecorder()
		n.Handler().ServeHTTP(w, r)
		if w.Code != 404 {
			t.Fatalf("removed route %s returned %d", path, w.Code)
		}
	}
}
func TestLegacyPullConfigIgnoredAndStatePreserved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg, err := InitConfig(path, "127.0.0.1:2053", filepath.Join(dir, "state.json"), "/unused/xray")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(cfg)
	var obj map[string]any
	_ = json.Unmarshal(b, &obj)
	obj["masterSync"] = map[string]any{"enabled": true, "baseURL": "invalid", "tokenFile": "/does-not-exist"}
	b, _ = json.Marshal(obj)
	if err = os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(loaded)
	if strings.Contains(string(b), "masterSync") {
		t.Fatal("legacy pull config retained")
	}
	n, _ := testNode(t)
	state := n.state.Clone()
	b, _ = json.Marshal(state)
	_ = json.Unmarshal(b, &obj)
	obj["masterSync"] = []any{map[string]any{"mappingId": "old", "managed": []any{}}}
	b, _ = json.Marshal(obj)
	path = filepath.Join(dir, "legacy-state.json")
	_ = os.WriteFile(path, b, 0600)
	restored, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if restored.GUID != state.GUID || len(restored.Inbounds) != len(state.Inbounds) || len(restored.Traffic) != len(state.Traffic) {
		t.Fatal("lost existing state")
	}
	b, _ = json.Marshal(restored)
	if strings.Contains(string(b), "masterSync") {
		t.Fatal("legacy mappings retained")
	}
}
