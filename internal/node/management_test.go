package node

import (
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
