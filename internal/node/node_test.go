package node

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fakeEngine struct {
	live     []LiveInbound
	values   map[string]int64
	running  bool
	fail     bool
	restarts int
}

func (e *fakeEngine) Apply(v []LiveInbound) error {
	if e.fail {
		return errors.New("injected core failure")
	}
	if !reflect.DeepEqual(skeleton(v), skeleton(e.live)) {
		e.restarts++
	}
	e.live = cloneLive(v)
	e.running = true
	return nil
}
func (e *fakeEngine) Stats() (map[string]int64, error) {
	v := map[string]int64{}
	for k, n := range e.values {
		v[k] = n
	}
	return v, nil
}
func (e *fakeEngine) Running() bool { return e.running }
func (e *fakeEngine) Stop() error   { e.running = false; return nil }
func (e *fakeEngine) PID() int      { return 0 }

func testClient(email, id string) Client {
	c := Client{}
	c.Set("email", email)
	c.Set("id", id)
	c.Set("enable", true)
	c.Set("flow", "xtls-rprx-vision")
	c.Set("subId", "test-sub")
	c.Set("totalGB", int64(0))
	c.Set("expiryTime", int64(0))
	return c
}
func testInbound() Inbound {
	ib := Inbound{Enable: true, Port: 18443, Protocol: "vless", Tag: "in-18443", SubSortIndex: 1, Settings: Object(`{"decryption":"none"}`), StreamSettings: Object(`{"network":"tcp","security":"reality","realitySettings":{"privateKey":"test","target":"example.org:443","serverNames":["example.org"],"shortIds":["ab"]}}`), Sniffing: Object(`{"enabled":false}`)}
	ib.SetClients([]Client{testClient("alice", "00000000-0000-4000-8000-000000000001")})
	return ib
}
func testNode(t *testing.T) (*Node, *fakeEngine) {
	t.Helper()
	e := &fakeEngine{values: map[string]int64{}}
	n, err := New(Config{StateFile: filepath.Join(t.TempDir(), "state.json"), Token: strings.Repeat("x", 64), BasePath: "/", APIPort: 62789}, e)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = n.PutInbound(testInbound(), 0); err != nil {
		t.Fatal(err)
	}
	return n, e
}

func TestCumulativeStatsGlobalResetAndRestart(t *testing.T) {
	n, e := testNode(t)
	key := "user>>>alice>>>traffic>>>uplink"
	e.values[key] = 100
	if err := n.sampleLocked(); err != nil {
		t.Fatal(err)
	}
	if err := n.sampleLocked(); err != nil {
		t.Fatal(err)
	}
	if got := n.Inbounds()[0].ClientStats[0].Up; got != 100 {
		t.Fatalf("local %d, want 100", got)
	}
	if _, err := n.PushGlobals("master", []Traffic{{Email: "alice", Up: 10000}}); err != nil {
		t.Fatal(err)
	}
	if got := n.Inbounds()[0].ClientStats[0].Up; got != 100 {
		t.Fatalf("global fed back into local: %d", got)
	}
	if _, err := n.ResetTraffic("alice", 0); err != nil {
		t.Fatal(err)
	}
	e.values[key] = 125
	if err := n.sampleLocked(); err != nil {
		t.Fatal(err)
	}
	if got := n.state.Traffic["alice"].Up; got != 25 {
		t.Fatalf("reset delta %d, want 25", got)
	}
	fresh := &fakeEngine{values: map[string]int64{key: 7}}
	restarted, err := New(n.Config, fresh)
	if err != nil {
		t.Fatal(err)
	}
	if err = restarted.sampleLocked(); err != nil {
		t.Fatal(err)
	}
	if got := restarted.state.Traffic["alice"].Up; got != 32 {
		t.Fatalf("restart cumulative %d, want 32", got)
	}
}

func TestInboundResetDoesNotResetClientQuota(t *testing.T) {
	n, e := testNode(t)
	e.values["user>>>alice>>>traffic>>>uplink"] = 100
	e.values["inbound>>>in-18443>>>traffic>>>uplink"] = 150
	if _, err := n.ResetTraffic("", 1); err != nil {
		t.Fatal(err)
	}
	ib := n.Inbounds()[0]
	if ib.Up != 0 || ib.ClientStats[0].Up != 100 {
		t.Fatalf("inbound reset changed client counters: %+v", ib)
	}
}

func TestQuotaExpiryAndHotUpdate(t *testing.T) {
	n, e := testNode(t)
	ib := n.Inbounds()[0]
	cs, _ := ib.Clients()
	cs[0].Set("totalGB", 100)
	ib.SetClients(cs)
	if _, err := n.PutInbound(ib, ib.ID); err != nil {
		t.Fatal(err)
	}
	restarts := e.restarts
	e.values["user>>>alice>>>traffic>>>downlink"] = 101
	if err := n.sampleLocked(); err != nil {
		t.Fatal(err)
	}
	if len(e.live[0].Users) != 0 || n.state.Traffic["alice"].Enable {
		t.Fatal("exhausted user was not removed")
	}
	if e.restarts != restarts {
		t.Fatal("quota disable restarted inbound")
	}
	c := testClient("alice", "00000000-0000-4000-8000-000000000001")
	c.Set("totalGB", 1000)
	if _, err := n.ChangeClient("alice", c, []int{ib.ID}, "update"); err != nil {
		t.Fatal(err)
	}
	if len(e.live[0].Users) != 1 {
		t.Fatal("quota increase did not restore user")
	}
	c.Set("expiryTime", time.Now().Add(-time.Minute).UnixMilli())
	if _, err := n.ChangeClient("alice", c, nil, "update"); err != nil {
		t.Fatal(err)
	}
	if len(e.live[0].Users) != 0 {
		t.Fatal("expired user still in runtime")
	}
}

func TestDelayedExpiryAndStaleGlobal(t *testing.T) {
	n, e := testNode(t)
	c := testClient("alice", "00000000-0000-4000-8000-000000000001")
	c.Set("expiryTime", -60000)
	c.Set("totalGB", 100)
	if _, err := n.ChangeClient("alice", c, nil, "update"); err != nil {
		t.Fatal(err)
	}
	if n.state.Traffic["alice"].ExpiryTime != -60000 {
		t.Fatal("unused client activated")
	}
	n.state.Globals["old"] = map[string]Global{"alice": {Up: 9999, UpdatedAt: time.Now().Add(-25 * time.Hour).UnixMilli()}}
	e.values["user>>>alice>>>traffic>>>uplink"] = 1
	if err := n.sampleLocked(); err != nil {
		t.Fatal(err)
	}
	if x := n.state.Traffic["alice"].ExpiryTime; x < time.Now().UnixMilli()+55000 {
		t.Fatalf("activation deadline %d", x)
	}
	if !n.state.Traffic["alice"].Enable {
		t.Fatal("stale global depleted client")
	}
}

func TestWriteFailureRestoresRuntimeAndPreservesDisk(t *testing.T) {
	n, e := testNode(t)
	before, _ := os.ReadFile(n.Config.StateFile)
	old := cloneLive(e.live)
	n.save = func(string, *State) error { return errors.New("no space left on device") }
	c := testClient("bob", "00000000-0000-4000-8000-000000000002")
	if _, err := n.ChangeClient("", c, []int{1}, "add"); err == nil {
		t.Fatal("write failure accepted")
	}
	after, _ := os.ReadFile(n.Config.StateFile)
	if !bytes.Equal(before, after) || !reflect.DeepEqual(e.live, old) {
		t.Fatal("write failure changed committed state or runtime")
	}
}

func TestCoreFailureDoesNotCommit(t *testing.T) {
	n, e := testNode(t)
	before, _ := os.ReadFile(n.Config.StateFile)
	e.fail = true
	if _, err := n.DeleteInbound(1); err == nil {
		t.Fatal("core failure accepted")
	}
	after, _ := os.ReadFile(n.Config.StateFile)
	if !bytes.Equal(before, after) {
		t.Fatal("core failure committed state")
	}
}

func TestSharedClientDetachRenameAndRepeatedAdd(t *testing.T) {
	n, _ := testNode(t)
	ib := testInbound()
	ib.Port = 18444
	ib.Tag = "second"
	if _, err := n.PutInbound(ib, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := n.PutInbound(ib, 0); err != nil {
		t.Fatal(err)
	}
	if len(n.Inbounds()) != 2 {
		t.Fatal("reconcile duplicated inbound")
	}
	c := testClient("renamed", "00000000-0000-4000-8000-000000000001")
	if _, err := n.ChangeClient("alice", c, []int{1}, "update"); err != nil {
		t.Fatal(err)
	}
	if n.state.Traffic["alice"] != nil || n.state.Traffic["renamed"] == nil {
		t.Fatal("rename did not migrate traffic identity")
	}
	if _, err := n.ChangeClient("renamed", nil, []int{1}, "detach"); err != nil {
		t.Fatal(err)
	}
	if n.state.Traffic["renamed"] == nil {
		t.Fatal("detach deleted shared identity")
	}
}

func TestStateCorruptionFailsClosed(t *testing.T) {
	n, _ := testNode(t)
	if err := AtomicWrite(n.Config.StateFile, []byte("broken"), true); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadState(n.Config.StateFile); err == nil {
		t.Fatal("corrupt state accepted")
	}
	b, err := os.ReadFile(n.Config.StateFile + ".previous")
	if err != nil || !json.Valid(b) {
		t.Fatal("last good snapshot missing")
	}
}

func TestAPIContractFormObjectsAuthLimitsAndUnsupported(t *testing.T) {
	n, _ := testNode(t)
	handler := n.Handler()
	call := func(method, path, body, typ, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", typ)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := call("GET", "/panel/api/inbounds/list", "", "", "bad"); w.Code != 401 {
		t.Fatalf("auth status %d", w.Code)
	}
	ib := testInbound()
	f := url.Values{"port": {"18444"}, "protocol": {"vless"}, "tag": {"form"}, "enable": {"true"}, "settings": {string(ib.Settings)}, "streamSettings": {string(ib.StreamSettings)}, "sniffing": {string(ib.Sniffing)}}
	w := call("POST", "/panel/api/inbounds/add", f.Encode(), "application/x-www-form-urlencoded", n.Config.Token)
	var e Envelope
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil || !e.Success {
		t.Fatalf("form add: %s", w.Body.String())
	}
	w = call("GET", "/panel/api/inbounds/list", "", "", n.Config.Token)
	var list struct {
		Obj []struct {
			Settings map[string]any `json:"settings"`
		} `json:"obj"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list.Obj) != 2 {
		t.Fatalf("nested JSON contract: %s", w.Body.String())
	}
	for _, path := range []string{"server/updatePanel", "inbounds/add"} {
		w = call("POST", "/panel/api/"+path, `{"protocol":"vmess"}`, "application/json", n.Config.Token)
		_ = json.Unmarshal(w.Body.Bytes(), &e)
		if e.Success {
			t.Fatalf("unsupported %s accepted", path)
		}
	}
	w = call("POST", "/panel/api/inbounds/add", strings.Repeat("x", MaxBody+1), "application/json", n.Config.Token)
	if w.Code != 413 {
		t.Fatalf("oversize status %d", w.Code)
	}
	for _, path := range []string{"clients/onlinesByGuid", "clients/activeInbounds", "clients/clientIpsByGuid"} {
		w = call("POST", "/panel/api/"+path, "", "", n.Config.Token)
		if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil || !e.Success {
			t.Fatalf("query %s: %s", path, w.Body.String())
		}
	}
}

func TestRejectUnsupportedConfiguration(t *testing.T) {
	for _, field := range []string{"limitIp", "reset", "resetDay", "hwidLimit"} {
		t.Run(field, func(t *testing.T) {
			ib := testInbound()
			cs, _ := ib.Clients()
			cs[0].Set(field, 1)
			ib.SetClients(cs)
			if err := ValidateInbound(&ib); err == nil {
				t.Fatalf("accepted %s", field)
			}
		})
	}
	ib := testInbound()
	ib.Sniffing = Object(`{"enabled":true}`)
	if err := ValidateInbound(&ib); err == nil {
		t.Fatal("sniffing silently accepted")
	}
}

func TestValidatePlainVLESSTCP(t *testing.T) {
	ib := testInbound()
	ib.StreamSettings = Object(`{"network":"tcp","security":"none","tcpSettings":{"header":{"type":"none"}}}`)
	clients, err := ib.Clients()
	if err != nil {
		t.Fatal(err)
	}
	clients[0].Set("flow", "")
	ib.SetClients(clients)
	if err = ValidateInbound(&ib); err != nil {
		t.Fatalf("plain VLESS TCP rejected: %v", err)
	}

	withRealityFields := ib
	withRealityFields.StreamSettings = Object(`{"network":"tcp","security":"none","realitySettings":{"privateKey":"must-not-be-ignored"}}`)
	if err = ValidateInbound(&withRealityFields); err == nil {
		t.Fatal("plain VLESS silently accepted REALITY-only settings")
	}

	withVision := ib
	clients, _ = withVision.Clients()
	clients[0].Set("flow", "xtls-rprx-vision")
	withVision.SetClients(clients)
	if err = ValidateInbound(&withVision); err == nil {
		t.Fatal("plain VLESS silently accepted XTLS Vision flow")
	}
}

func TestFullMasterRemoteRouteCoverage(t *testing.T) {
	b, err := os.ReadFile("../web/runtime/remote.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"server/restartXrayService", "server/status", "inbounds/list", "clients/onlinesByGuid", "clients/lastOnline", "clients/activeInbounds", "inbounds/pushClientTraffics", "server/clientIps", "clients/clientIpsByGuid"} {
		if path != "server/status" && !strings.Contains(string(b), "panel/api/"+path) {
			t.Fatalf("master route changed: %s", path)
		}
	}
	n, _ := testNode(t)
	for _, path := range []string{"server/descendants", "hosts/list", "server/getWebCertFiles", "server/status"} {
		r := httptest.NewRequest(http.MethodGet, "/panel/api/"+path, nil)
		r.Header.Set("Authorization", "Bearer "+n.Config.Token)
		w := httptest.NewRecorder()
		n.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("route %s status %d", path, w.Code)
		}
	}
}
