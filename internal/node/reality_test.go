package node

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/proxy"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

func TestREALITYConnectionHotChangesAndRecovery(t *testing.T) {
	for _, network := range []string{"tcp", "raw"} {
		for _, flow := range []string{"", "xtls-rprx-vision"} {
			name := flow
			if name == "" {
				name = "no-vision"
			}
			t.Run(network+"/"+name, func(t *testing.T) {
				testREALITYConnection(t, network, flow)
			})
		}
	}
}

func testREALITYConnection(t *testing.T, network, flow string) {
	binary := os.Getenv("NODE_XRAY_TEST_BINARY")
	if binary == "" {
		t.Skip("set NODE_XRAY_TEST_BINARY to the pinned Xray binary")
	}
	dir := t.TempDir()
	c := Config{StateFile: filepath.Join(dir, "state.json"), XrayBinary: binary, APIPort: freePort(t), Token: strings.Repeat("a", 64), BasePath: "/"}
	core := NewCore(c)
	core.Log = &RotatingLog{Path: filepath.Join(dir, "xray.log")}
	n, err := New(c, core)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = core.Stop()
		if t.Failed() {
			b, _ := os.ReadFile(core.Log.Path)
			t.Log(string(b))
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { n.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	api := httptest.NewServer(n.Handler())
	defer api.Close()
	transport := &http.Client{Timeout: 10 * time.Second}
	defer transport.CloseIdleConnections()
	call := func(method, path string, body any, success bool) {
		t.Helper()
		b, _ := json.Marshal(body)
		r, _ := http.NewRequest(method, api.URL+"/panel/api/"+path, strings.NewReader(string(b)))
		r.Header.Set("Authorization", "Bearer "+c.Token)
		r.Header.Set("Content-Type", "application/json")
		resp, err := transport.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var env Envelope
		if err = json.NewDecoder(resp.Body).Decode(&env); err != nil {
			t.Fatal(err)
		}
		if env.Success != success {
			t.Fatalf("%s: success=%v msg=%s", path, env.Success, env.Msg)
		}
	}

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/stream" {
			for j := 0; j < 150; j++ {
				if _, err := w.Write([]byte("data\n")); err != nil {
					return
				}
				w.(http.Flusher).Flush()
				time.Sleep(40 * time.Millisecond)
			}
			return
		}
		_, _ = w.Write([]byte("node-reality-ok"))
	}))
	defer origin.Close()
	core.configForTest = func(config map[string]any) {
		_, originPort, _ := net.SplitHostPort(origin.Listener.Addr().String())
		config["outbounds"] = []any{map[string]any{"tag": "direct", "protocol": "freedom", "settings": map[string]any{"finalRules": []any{map[string]any{"action": "allow", "network": "tcp", "ip": []string{"127.0.0.1/32"}, "port": originPort}}}}}
	}
	target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("target")) }))
	target.EnableHTTP2 = true
	target.TLS = &tls.Config{MinVersion: tls.VersionTLS13, CurvePreferences: []tls.CurveID{tls.X25519}, SessionTicketsDisabled: true}
	target.StartTLS()
	defer target.Close()
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ib := testInbound()
	clients, err := ib.Clients()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range clients {
		c.Set("flow", flow)
	}
	ib.SetClients(clients)
	// Match the full panel's default server-side Vision padding parameters.
	// Omitting this exact tuple in the generated account is equivalent to
	// Xray's default; that equivalence does not hold for custom four-tuples.
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(ib.Settings, &settings); err != nil {
		t.Fatal(err)
	}
	settings["testseed"] = json.RawMessage(`[900,500,900,256]`)
	ib.Settings, _ = json.Marshal(settings)
	ib.Port = freePort(t)
	ib.Listen = "127.0.0.1"
	ib.StreamSettings, _ = json.Marshal(map[string]any{"network": "tcp", "security": "reality", "realitySettings": map[string]any{"show": false, "target": target.Listener.Addr().String(), "serverNames": []string{"example.org"}, "privateKey": base64.RawURLEncoding.EncodeToString(key.Bytes()), "shortIds": []string{"ab"}}})
	var stream map[string]any
	if err := json.Unmarshal(ib.StreamSettings, &stream); err != nil {
		t.Fatal(err)
	}
	stream["network"] = network
	stream["tcpSettings"] = map[string]any{"acceptProxyProtocol": false, "header": map[string]any{"type": "none"}}
	reality := stream["realitySettings"].(map[string]any)
	reality["xver"] = 0
	reality["minClientVer"] = "0.0.1"
	reality["maxClientVer"] = ""
	reality["maxTimediff"] = 0
	reality["mldsa65Seed"] = ""
	reality["settings"] = map[string]any{"publicKey": base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()), "fingerprint": "chrome", "serverName": "", "spiderX": "/", "mldsa65Verify": ""}
	for i := 1; i < 24; i++ {
		reality["serverNames"] = append(reality["serverNames"].([]any), "test"+strconv.Itoa(i)+".example.org")
	}
	reality["shortIds"] = []string{"0123456789", "012345", "0123456789abcdef", "0123456789abcd", "0123", "0123456789ab", "ab", "01234567"}
	ib.StreamSettings, _ = json.Marshal(stream)
	call("POST", "inbounds/add", ib, true)
	port := freePort(t)
	clientConfig := map[string]any{"log": map[string]any{"loglevel": "warning"}, "inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": port, "protocol": "socks", "settings": map[string]any{"auth": "noauth"}}}, "outbounds": []any{map[string]any{"protocol": "vless", "settings": map[string]any{"vnext": []any{map[string]any{"address": "127.0.0.1", "port": ib.Port, "users": []any{map[string]any{"id": "00000000-0000-4000-8000-000000000001", "encryption": "none", "flow": "xtls-rprx-vision"}}}}}, "streamSettings": map[string]any{"network": "tcp", "security": "reality", "realitySettings": map[string]any{"fingerprint": "chrome", "serverName": "example.org", "publicKey": base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()), "shortId": "ab"}}}}}
	path := filepath.Join(dir, "client.json")
	outbound := clientConfig["outbounds"].([]any)[0].(map[string]any)
	clientStream := outbound["streamSettings"].(map[string]any)
	clientStream["network"] = network
	user := outbound["settings"].(map[string]any)["vnext"].([]any)[0].(map[string]any)["users"].([]any)[0].(map[string]any)
	user["flow"] = flow
	b, _ := json.Marshal(clientConfig)
	if err = AtomicWrite(path, b, false); err != nil {
		t.Fatal(err)
	}
	client := exec.Command(binary, "run", "-config", path)
	clientLog := &RotatingLog{Path: filepath.Join(dir, "client.log")}
	client.Stdout, client.Stderr = clientLog, clientLog
	if err = client.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = client.Process.Kill()
		_ = client.Wait()
		if t.Failed() {
			b, _ := os.ReadFile(clientLog.Path)
			t.Log(string(b))
		}
	})
	for deadline := time.Now().Add(3 * time.Second); ; {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 100*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(30 * time.Millisecond)
	}
	dialer, err := proxy.SOCKS5("tcp", "127.0.0.1:"+strconv.Itoa(port), nil, &net.Dialer{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	httpTransport := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return dialer.(proxy.ContextDialer).DialContext(ctx, network, address)
	}}
	httpClient := &http.Client{Transport: httpTransport, Timeout: 8 * time.Second}
	defer httpTransport.CloseIdleConnections()
	resp, err := httpClient.Get(origin.URL + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	first := make([]byte, 5)
	if _, err = io.ReadFull(resp.Body, first); err != nil {
		t.Fatal(err)
	}
	pid := core.PID()
	bob := testClient("bob", "00000000-0000-4000-8000-000000000002")
	bob.Set("flow", flow)
	call("POST", "clients/add", map[string]any{"client": bob, "inboundIds": []int{1}}, true)
	call("POST", "clients/del/bob", nil, true)
	if core.PID() != pid {
		t.Fatal("client update restarted core")
	}
	if _, err = io.ReadFull(resp.Body, first); err != nil {
		t.Fatalf("unrelated connection interrupted: %v", err)
	}
	resp.Body.Close()
	call("GET", "inbounds/list", nil, true)
	call("POST", "clients/resetTraffic/alice", nil, true)
	invalid := ib
	invalid.ID = 1
	invalid.StreamSettings = Object(`{"network":"tcp","security":"reality","realitySettings":{"privateKey":"invalid","target":"127.0.0.1:1","serverNames":["example.org"],"shortIds":["ab"]}}`)
	call("POST", "inbounds/update/1", invalid, false)
	resp, err = httpClient.Get(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	b, err = io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || string(b) != "node-reality-ok" {
		t.Fatalf("rollback connectivity: %q %v", b, err)
	}
	// A syntactically valid but unrelated REALITY public key must never reach
	// the protected origin. Use a separate client and disable connection reuse.
	wrongKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	wrongPort := freePort(t)
	clientConfig["inbounds"].([]any)[0].(map[string]any)["port"] = wrongPort
	clientStream["realitySettings"].(map[string]any)["publicKey"] = base64.RawURLEncoding.EncodeToString(wrongKey.PublicKey().Bytes())
	wrongPath := filepath.Join(dir, "wrong-key.json")
	b, _ = json.Marshal(clientConfig)
	if err := AtomicWrite(wrongPath, b, false); err != nil {
		t.Fatal(err)
	}
	wrongClient := exec.Command(binary, "run", "-config", wrongPath)
	if err := wrongClient.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = wrongClient.Process.Kill(); _ = wrongClient.Wait() })
	for deadline := time.Now().Add(3 * time.Second); ; {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(wrongPort)), 100*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("wrong-key test client did not start")
		}
		time.Sleep(30 * time.Millisecond)
	}
	wrongDialer, err := proxy.SOCKS5("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(wrongPort)), nil, &net.Dialer{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	wrongTransport := &http.Transport{DialContext: wrongDialer.(proxy.ContextDialer).DialContext, DisableKeepAlives: true}
	defer wrongTransport.CloseIdleConnections()
	wrongHTTP := &http.Client{Transport: wrongTransport, Timeout: 3 * time.Second}
	if resp, err := wrongHTTP.Get(origin.URL); err == nil {
		resp.Body.Close()
		t.Fatal("wrong REALITY public key unexpectedly reached HTTP origin")
	}
	t.Logf("REALITY network=%s flow=%q: transfer, hot changes, failed-config recovery and wrong-key rejection passed; server PID=%d", network, flow, core.PID())
}
