package node

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestNetworkTopologyReadPreservesStateAndCore(t *testing.T) {
	n, e := testNode(t)
	before, _ := os.ReadFile(n.Config.StateFile)
	restarts := e.restarts
	e.values["user>>>alice>>>traffic>>>uplink"] = 999
	req := httptest.NewRequest(http.MethodGet, "/panel/api/server/networkTopology", nil)
	req.Header.Set("Authorization", "Bearer "+n.Config.Token)
	n.Config.ManagedANURL = "https://an.example"
	w := httptest.NewRecorder()
	n.Handler().ServeHTTP(w, req)
	after, _ := os.ReadFile(n.Config.StateFile)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"tag":"direct"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if !bytes.Equal(before, after) || restarts != e.restarts {
		t.Fatal("read mutated node")
	}
	if strings.Contains(w.Body.String(), "alice") || strings.Contains(w.Body.String(), "privateKey") {
		t.Fatal("secret leaked")
	}
	bad := httptest.NewRecorder()
	n.Handler().ServeHTTP(bad, httptest.NewRequest(http.MethodGet, "/panel/api/server/networkTopology", nil))
	if bad.Code != 401 {
		t.Fatal("unauthenticated read", bad.Code)
	}
}
