package main

import (
	stdbytes "bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/opxqo/3x-node/v3/internal/node"
)

const testEnrollCode = "enroll-code-0123456789"

func testAccessInfo(t *testing.T) accessInfo {
	t.Helper()
	dir := t.TempDir()
	c, err := node.InitConfig(filepath.Join(dir, "config.json"), "0.0.0.0:2053", filepath.Join(dir, "state.json"), "/usr/local/lib/3x-ui-node/xray")
	if err != nil {
		t.Fatal(err)
	}
	info, err := loadAccessInfo(c)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func TestEnrollPostsAccessInfoWithoutPrintingToken(t *testing.T) {
	info := testAccessInfo(t)
	var got accessInfo
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		_, _ = w.Write([]byte(`{"success":true,"msg":"node #7 pending approval","obj":{"managedMode":"an-v1"}}`))
	}))
	defer srv.Close()
	var out stdbytes.Buffer
	managed, err := enroll(srv.Client(), srv.URL+"/api/node/enroll", testEnrollCode, info, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !managed {
		t.Fatal("AN enrollment did not enable managed mode")
	}
	want := info
	want.Code = testEnrollCode
	if got != want {
		t.Fatalf("posted %+v, want %+v", got, want)
	}
	if got.ListenPort != "2053" || got.BasePath != "/" || got.Version != node.Version || got.GUID == "" {
		t.Fatalf("unexpected access fields: %+v", got)
	}
	if strings.Contains(out.String(), info.Token) || !strings.Contains(out.String(), "node #7 pending approval") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestGenericEnrollmentDoesNotEnableANMode(t *testing.T) {
	info := testAccessInfo(t)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"obj":{"connectionId":7}}`))
	}))
	defer srv.Close()
	managed, err := enroll(srv.Client(), srv.URL+"/enroll", testEnrollCode, info, &stdbytes.Buffer{})
	if err != nil || managed {
		t.Fatalf("generic enrollment: managed=%t, err=%v", managed, err)
	}
}

func TestManagedCredentialsHideToken(t *testing.T) {
	dir := t.TempDir()
	c, err := node.InitConfig(filepath.Join(dir, "config.json"), "0.0.0.0:2053", filepath.Join(dir, "state.json"), "/usr/local/lib/3x-ui-node/xray")
	if err != nil {
		t.Fatal(err)
	}
	c.ManagedANURL = "https://an.example/api/node/enroll"
	if err = node.SaveConfig(filepath.Join(dir, "config.json"), c); err != nil {
		t.Fatal(err)
	}
	loaded, err := node.LoadConfig(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var out stdbytes.Buffer
	if err = showCredentials(&out, loaded); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), c.Token) || !strings.Contains(out.String(), "由 AN 管理") {
		t.Fatalf("managed credentials output: %q", out.String())
	}
}

func TestEnrollRejectsUnsafeInputBeforeSending(t *testing.T) {
	info := testAccessInfo(t)
	var calls atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "https://")
	for _, tt := range []struct{ name, url, code, want string }{
		{"plain http", "http://" + host + "/enroll", testEnrollCode, "-url must be an https URL"},
		{"credentials in url", "https://user:pass@" + host + "/enroll", testEnrollCode, "-url must be an https URL"},
		{"query in url", srv.URL + "/enroll?token=unsafe", testEnrollCode, "-url must be an https URL"},
		{"short code", srv.URL + "/enroll", "short", "-code must be 16-128"},
		{"code with slash", srv.URL + "/enroll", "enroll/code/0123456789", "-code must be 16-128"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := enroll(srv.Client(), tt.url, tt.code, info, &stdbytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("server received %d requests", n)
	}
}

func TestEnrollDoesNotFollowRedirects(t *testing.T) {
	info := testAccessInfo(t)
	var followed atomic.Bool
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/elsewhere" {
			followed.Store(true)
			_, _ = w.Write([]byte(`{"success":true}`))
			return
		}
		http.Redirect(w, r, "/elsewhere", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	_, err := enroll(srv.Client(), srv.URL+"/enroll", testEnrollCode, info, &stdbytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "307") {
		t.Fatalf("err = %v", err)
	}
	if followed.Load() {
		t.Fatal("redirect was followed, resending the token")
	}
}

func TestEnrollReportsMasterRejection(t *testing.T) {
	info := testAccessInfo(t)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":false,"msg":"code expired"}`))
	}))
	defer srv.Close()
	_, err := enroll(srv.Client(), srv.URL+"/enroll", testEnrollCode, info, &stdbytes.Buffer{})
	if err == nil || err.Error() != "enroll rejected: code expired" {
		t.Fatalf("err = %v", err)
	}
}

func TestAccessBlobRoundTrip(t *testing.T) {
	info := testAccessInfo(t)
	s, err := accessBlob(info, "https://203.0.113.9:36749")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(s, accessBlobPrefix))
	if err != nil || !strings.HasPrefix(s, accessBlobPrefix) {
		t.Fatalf("blob %q: %v", s, err)
	}
	var got accessInfo
	if err = json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	want := info
	want.URL = "https://203.0.113.9:36749"
	if got != want {
		t.Fatalf("decoded %+v, want %+v", got, want)
	}
	if _, err = accessBlob(info, "http://203.0.113.9:36749"); err == nil || !strings.Contains(err.Error(), "-public-url") {
		t.Fatalf("plain http public URL accepted: %v", err)
	}
}
