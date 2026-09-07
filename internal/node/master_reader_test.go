package node

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func writeMasterSyncToken(t *testing.T, token string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "master.token")
	if err := os.WriteFile(path, []byte(token+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func testMasterSyncConfig(t *testing.T, serverURL, fingerprint string) MasterSyncConfig {
	t.Helper()
	return MasterSyncConfig{
		Enabled:         true,
		BaseURL:         serverURL + "/heacGFSikgp1aH3FXn",
		TokenFile:       writeMasterSyncToken(t, "master-reader-test-token"),
		CertSHA256:      fingerprint,
		IntervalSeconds: DefaultMasterSyncInterval,
		Mappings: []MasterSyncMapping{{
			ID:              "hk",
			MasterInboundID: 77,
			LocalInboundID:  1,
		}},
	}
}

func TestFetchMasterInboundsReadsFullClientPayloadWithPinnedTLS(t *testing.T) {
	var method atomic.Value
	method.Store("")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method.Store(r.Method)
		if r.URL.Path != "/heacGFSikgp1aH3FXn/panel/api/inbounds/list" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer master-reader-test-token" {
			t.Fatalf("unexpected authorization header")
		}
		_, _ = io.WriteString(w, `{"success":true,"msg":"","obj":[{"id":77,"remark":"hk","enable":true,"port":26700,"protocol":"vless","settings":{"clients":[{"id":"00000000-0000-4000-8000-000000000001","email":"alice","enable":true}]},"streamSettings":{"network":"tcp","security":"none"},"sniffing":{"enabled":false}}]}`)
	}))
	defer server.Close()
	sum := sha256.Sum256(server.Certificate().Raw)
	cfg := testMasterSyncConfig(t, server.URL, base64.StdEncoding.EncodeToString(sum[:]))

	inbounds, err := FetchMasterInbounds(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if method.Load() != http.MethodGet || len(inbounds) != 1 || inbounds[0].ID != 77 {
		t.Fatalf("unexpected request/result: method=%v inbounds=%+v", method.Load(), inbounds)
	}
	clients, err := inbounds[0].Clients()
	if err != nil || len(clients) != 1 || clients[0].Text("id") != "00000000-0000-4000-8000-000000000001" || clients[0].Text("email") != "alice" {
		t.Fatalf("full client payload missing: clients=%+v err=%v", clients, err)
	}
}

func TestFetchMasterInboundsRejectsWrongCertificate(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"success":true,"obj":[]}`)
	}))
	defer server.Close()
	cfg := testMasterSyncConfig(t, server.URL, strings.Repeat("00", sha256.Size))
	if _, err := FetchMasterInbounds(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "fingerprint mismatch") {
		t.Fatalf("wrong certificate was accepted: %v", err)
	}
}

func TestFetchMasterInboundsRejectsRedirectAndDoesNotFollowIt(t *testing.T) {
	var redirected atomic.Bool
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirected.Store(true)
		_, _ = io.WriteString(w, `{"success":true,"obj":[]}`)
	}))
	defer target.Close()
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, httptest.NewRequest(http.MethodGet, "/", nil), target.URL, http.StatusFound)
	}))
	defer redirect.Close()
	sum := sha256.Sum256(redirect.Certificate().Raw)
	cfg := testMasterSyncConfig(t, redirect.URL, base64.StdEncoding.EncodeToString(sum[:]))
	if _, err := FetchMasterInbounds(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "redirect refused") {
		t.Fatalf("redirect was accepted: %v", err)
	}
	if redirected.Load() {
		t.Fatal("client followed a redirect to another host")
	}
}

func TestValidateMasterSyncRejectsUnsafeConfiguration(t *testing.T) {
	base := MasterSyncConfig{Enabled: true, BaseURL: "http://example.com", TokenFile: "/tmp/token", CertSHA256: strings.Repeat("00", sha256.Size), Mappings: []MasterSyncMapping{{ID: "x", MasterInboundID: 1, LocalInboundID: 1}}}
	if err := ValidateMasterSync(base); err == nil {
		t.Fatal("accepted non-HTTPS master URL")
	}
	base.BaseURL = "https://example.com"
	base.Mappings = []MasterSyncMapping{{ID: "x", MasterInboundID: 1, LocalInboundID: 1}, {ID: "x", MasterInboundID: 2, LocalInboundID: 2}}
	if err := ValidateMasterSync(base); err == nil {
		t.Fatal("accepted duplicate mapping ID")
	}

	encoded, _ := json.Marshal(base)
	if strings.Contains(string(encoded), "master-reader-test-token") {
		t.Fatal("test config unexpectedly contains plaintext token")
	}
}
