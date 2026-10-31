package node

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Replays every request the unmodified 3x-ui v3.11.0 Remote sent while driving
// its full method set (recorded from source/3x-ui; see docs/node/VALIDATION.md).
func TestOfficialMasterV3110RecordedWire(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "official-3x-ui-v3.11.0-remote.json"))
	if err != nil {
		t.Fatal(err)
	}
	var calls []struct{ Method, Path, ContentType, Body string }
	if err := json.Unmarshal(raw, &calls); err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("a", 64)
	n, err := New(Config{StateFile: filepath.Join(t.TempDir(), "state.json"), Token: token, BasePath: "/leaf/", APIPort: 62789}, &fakeEngine{values: map[string]int64{}})
	if err != nil {
		t.Fatal(err)
	}
	h := n.Handler()
	for i, c := range calls {
		req := httptest.NewRequest(c.Method, "/leaf/panel/api/"+c.Path, strings.NewReader(c.Body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("X-3x-Master-Push", "1")
		if c.ContentType != "" {
			req.Header.Set("Content-Type", c.ContentType)
			sum := sha256.Sum256([]byte(c.Body))
			req.Header.Set("X-Config-Sha256", hex.EncodeToString(sum[:]))
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var env Envelope
		_ = json.Unmarshal(rec.Body.Bytes(), &env)
		if rec.Code != http.StatusOK || !env.Success {
			t.Fatalf("call %d %s %s: HTTP %d %q", i, c.Method, c.Path, rec.Code, env.Msg)
		}
		if c.Path == "inbounds/list" && i == 2 {
			list, _ := json.Marshal(env.Obj)
			if !strings.Contains(string(list), `"tag":"in-28762"`) || !strings.Contains(string(list), `"network":"xhttp"`) {
				t.Fatalf("XHTTP inbound missing from list: %s", list)
			}
		}
	}
	if len(n.Inbounds()) != 0 {
		t.Fatalf("inbounds left after recorded deletes: %+v", n.Inbounds())
	}
}
