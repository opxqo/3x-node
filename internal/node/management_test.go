package node

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestManagementIgnoresLocalAndUnauthorizedRequests(t *testing.T) {
	n, _ := testNode(t)
	h := n.Handler()
	for _, tt := range []struct{ peer, token string }{{"127.0.0.1:1234", n.Config.Token}, {"[::1]:1234", n.Config.Token}, {"198.51.100.4:1234", "invalid"}} {
		r := httptest.NewRequest("GET", "/panel/api/server/status", nil)
		r.RemoteAddr = tt.peer
		r.Header.Set("Authorization", "Bearer "+tt.token)
		r.Header.Set("X-Forwarded-For", "203.0.113.5")
		h.ServeHTTP(httptest.NewRecorder(), r)
		if n.management.LastRequest != 0 {
			t.Fatal("local or unauthorized request reported as management")
		}
	}
	r := httptest.NewRequest("GET", "/panel/api/server/status", nil)
	r.RemoteAddr = "198.51.100.4:1234"
	r.Header.Set("Authorization", "Bearer "+n.Config.Token)
	r.Header.Set("X-Forwarded-For", "203.0.113.5")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if n.management.RequestPeer != "198.51.100.4" || n.management.LastRequest == 0 || n.management.LastConfig != 0 {
		t.Fatal(n.management)
	}
	r = httptest.NewRequest("POST", "/panel/api/inbounds/add", strings.NewReader("invalid"))
	r.RemoteAddr = "198.51.100.4:1234"
	r.Header.Set("Authorization", "Bearer "+n.Config.Token)
	h.ServeHTTP(httptest.NewRecorder(), r)
	if n.management.LastConfig != 0 {
		t.Fatal("failed mutation reported as success")
	}
}

func TestANManagedModeRestrictsRemoteManagement(t *testing.T) {
	n, _ := testNode(t)
	n.Config.ManagedANURL = "https://an.example/api/node/enroll"
	h := n.Handler()
	for _, tt := range []struct {
		method, path string
		want         int
	}{
		{"GET", "server/status", 200},
		{"GET", "inbounds/list", 200},
		{"POST", "clients/add", 200},
		{"POST", "clients/update/test", 200},
		{"POST", "clients/test/detach", 200},
		{"POST", "clients/del/test", 200},
		{"POST", "inbounds/add", 200},
		{"POST", "server/restartXrayService", 403},
		{"POST", "clients/resetTraffic/test", 403},
		{"POST", "inbounds/resetAllTraffics", 403},
		{"POST", "inbounds/del/1", 403},
		{"GET", "server/getWebCertFiles", 403},
	} {
		r := httptest.NewRequest(tt.method, "/panel/api/"+tt.path, nil)
		r.Header.Set("Authorization", "Bearer "+n.Config.Token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tt.want {
			t.Errorf("%s %s returned HTTP %d, want %d", tt.method, tt.path, w.Code, tt.want)
		}
	}
	r := httptest.NewRequest("POST", "/panel/api/server/restartXrayService", nil)
	r.Header.Set("Authorization", "Bearer invalid")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("invalid token returned HTTP %d", w.Code)
	}
}

func TestManagementSeparatesQueriesAndConfigSources(t *testing.T) {
	n, _ := testNode(t)
	r := httptest.NewRequest("POST", "/", nil)
	r.RemoteAddr = "198.51.100.8:1234"
	n.recordManagement(r, "clients/onlines", true)
	if n.management.LastConfig != 0 {
		t.Fatal("POST query counted as config")
	}
	n.recordManagement(r, "clients/add", true)
	if n.management.LastConfig == 0 || n.management.ConfigPeer != "198.51.100.8" {
		t.Fatal(n.management)
	}
	r.RemoteAddr = "198.51.100.9:1234"
	r.Method = "GET"
	n.recordManagement(r, "server/status", true)
	if n.management.ConfigPeer != "198.51.100.8" || n.management.RequestPeer != "198.51.100.9" {
		t.Fatal("sources conflated")
	}
}
