package node

import (
	"bytes"
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

func TestValidateXHTTPInbound(t *testing.T) {
	cases := []struct {
		name, stream, err string
	}{
		{"panel defaults", `{"network":"xhttp","security":"none","xhttpSettings":{"path":"/x","host":"","mode":"auto","xPaddingBytes":"100-1000","scMaxBufferedPosts":30,"scStreamUpServerSecs":"20-80","headers":{},"xmux":{"maxConcurrency":"16-32"},"enableXmux":false}}`, ""},
		{"reality", `{"network":"xhttp","security":"reality","realitySettings":{"privateKey":"k","target":"example.org:443","serverNames":["example.org"],"shortIds":["ab"]},"xhttpSettings":{"mode":"stream-one"}}`, ""},
		{"unknown field", `{"network":"xhttp","xhttpSettings":{"downloadSettings":{"address":"x"}}}`, "unsupported XHTTP setting downloadSettings"},
		{"relative path", `{"network":"xhttp","xhttpSettings":{"path":"x"}}`, "XHTTP path must start with /"},
		{"bad mode", `{"network":"xhttp","xhttpSettings":{"mode":"stream-down"}}`, "XHTTP mode must be auto, packet-up, stream-up or stream-one"},
		{"settings without transport", `{"network":"tcp","xhttpSettings":{"path":"/x"}}`, "XHTTP settings require XHTTP transport"},
		{"other transport", `{"network":"ws"}`, "only TCP/RAW or XHTTP transport is supported"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ib := testInbound()
			ib.StreamSettings = Object(tc.stream)
			err := ValidateInbound(&ib)
			if got := ""; err != nil {
				got = err.Error()
				if got != tc.err {
					t.Fatalf("error %q, want %q", got, tc.err)
				}
			} else if tc.err != "" {
				t.Fatalf("accepted, want %q", tc.err)
			}
		})
	}
}

func TestXHTTPConfigDropsVisionAndClientOnlyFields(t *testing.T) {
	n, _ := testNode(t)
	ib := n.state.Inbounds[0]
	ib.StreamSettings = Object(`{"network":"xhttp","security":"none","xhttpSettings":{"path":"/x","xmux":{"maxConcurrency":"16-32","maxConnections":3},"enableXmux":true,"noGRPCHeader":true}}`)
	if _, err := n.PutInbound(ib, ib.ID); err != nil {
		t.Fatal(err)
	}
	live := n.desired(n.state)
	if flow := live[0].Users["alice"].Flow; flow != "" {
		t.Fatalf("XHTTP account kept flow %q", flow)
	}
	b, _ := json.Marshal(NewCore(n.Config).xrayConfig(live)["inbounds"].([]any)[1])
	if bytes.Contains(b, []byte("xmux")) || bytes.Contains(b, []byte("enableXmux")) || bytes.Contains(b, []byte("noGRPCHeader")) || !bytes.Contains(b, []byte(`"path":"/x"`)) {
		t.Fatalf("Xray inbound %s", b)
	}
	if stored, _ := n.state.Inbounds[0].Clients(); stored[0].Text("flow") != "xtls-rprx-vision" {
		t.Fatal("stored client flow was not kept for a lossless round trip")
	}
}

func TestXHTTPConnection(t *testing.T) {
	for _, security := range []string{"reality", "none"} {
		for _, mode := range []string{"auto", "packet-up", "stream-up", "stream-one"} {
			t.Run(security+"/"+mode, func(t *testing.T) { testXHTTPConnection(t, security, mode) })
		}
	}
}

func testXHTTPConnection(t *testing.T, security, mode string) {
	binary := os.Getenv("NODE_XRAY_TEST_BINARY")
	if binary == "" {
		t.Skip("set NODE_XRAY_TEST_BINARY to the pinned Xray binary")
	}
	dir := t.TempDir()
	payload := bytes.Repeat([]byte("x"), 256<<10)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(payload) }))
	defer origin.Close()
	cfg := Config{StateFile: filepath.Join(dir, "state.json"), XrayBinary: binary, APIPort: freePort(t), Token: strings.Repeat("a", 64), BasePath: "/"}
	core := NewCore(cfg)
	core.Log = &RotatingLog{Path: filepath.Join(dir, "server.log")}
	n, err := New(cfg, core)
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
	core.configForTest = func(config map[string]any) {
		_, originPort, _ := net.SplitHostPort(origin.Listener.Addr().String())
		config["outbounds"] = []any{map[string]any{"tag": "direct", "protocol": "freedom", "settings": map[string]any{"finalRules": []any{map[string]any{"action": "allow", "network": "tcp", "ip": []string{"127.0.0.1/32"}, "port": originPort}}}}}
	}

	// The stored client keeps Vision as a full panel sharing it with a TCP inbound would.
	xhttp := map[string]any{"path": "/xh", "host": "", "mode": mode, "xPaddingBytes": "100-1000", "noSSEHeader": false, "scMaxBufferedPosts": 30, "scStreamUpServerSecs": "20-80", "headers": map[string]any{}, "scMinPostsIntervalMs": "", "uplinkChunkSize": 0, "noGRPCHeader": false, "enableXmux": false, "xmux": map[string]any{"maxConcurrency": "16-32", "maxConnections": 0}}
	serverStream := map[string]any{"network": "xhttp", "security": security, "xhttpSettings": xhttp}
	clientStream := map[string]any{"network": "xhttp", "security": security, "xhttpSettings": map[string]any{"path": "/xh", "mode": mode}}
	if security == "reality" {
		target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
		target.EnableHTTP2 = true
		target.TLS = &tls.Config{MinVersion: tls.VersionTLS13, CurvePreferences: []tls.CurveID{tls.X25519}}
		target.StartTLS()
		defer target.Close()
		key, err := ecdh.X25519().GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		serverStream["realitySettings"] = map[string]any{"target": target.Listener.Addr().String(), "serverNames": []string{"example.org"}, "privateKey": base64.RawURLEncoding.EncodeToString(key.Bytes()), "shortIds": []string{"ab"}}
		clientStream["realitySettings"] = map[string]any{"fingerprint": "chrome", "serverName": "example.org", "publicKey": base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()), "shortId": "ab"}
	}
	ib := testInbound()
	ib.Port = freePort(t)
	ib.Listen = "127.0.0.1"
	ib.StreamSettings, _ = json.Marshal(serverStream)
	if _, err = n.PutInbound(ib, 0); err != nil {
		t.Fatal(err)
	}

	socksPort := freePort(t)
	clientConfig := map[string]any{
		"log":      map[string]any{"loglevel": "warning"},
		"inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": socksPort, "protocol": "socks", "settings": map[string]any{"auth": "noauth"}}},
		"outbounds": []any{map[string]any{
			"protocol":       "vless",
			"settings":       map[string]any{"vnext": []any{map[string]any{"address": "127.0.0.1", "port": ib.Port, "users": []any{map[string]any{"id": "00000000-0000-4000-8000-000000000001", "encryption": "none"}}}}},
			"streamSettings": clientStream,
		}},
	}
	clientPath := filepath.Join(dir, "client.json")
	b, _ := json.Marshal(clientConfig)
	if err = AtomicWrite(clientPath, b, false); err != nil {
		t.Fatal(err)
	}
	client := exec.Command(binary, "run", "-config", clientPath)
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
	address := "127.0.0.1:" + strconv.Itoa(socksPort)
	for deadline := time.Now().Add(3 * time.Second); ; {
		conn, dialErr := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(dialErr)
		}
		time.Sleep(30 * time.Millisecond)
	}
	dialer, err := proxy.SOCKS5("tcp", address, nil, &net.Dialer{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return dialer.(proxy.ContextDialer).DialContext(ctx, network, address)
	}}
	httpClient := &http.Client{Transport: transport, Timeout: 8 * time.Second}
	defer transport.CloseIdleConnections()
	get := func() {
		t.Helper()
		resp, err := httpClient.Get(origin.URL)
		if err != nil {
			t.Fatal(err)
		}
		got, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil || !bytes.Equal(got, payload) {
			t.Fatalf("XHTTP response %d bytes: %v", len(got), readErr)
		}
	}
	get()

	pid := core.PID()
	bob := testClient("bob", "00000000-0000-4000-8000-000000000002")
	if _, err = n.ChangeClient("", bob, []int{1}, "add"); err != nil {
		t.Fatal(err)
	}
	if core.PID() != pid {
		t.Fatal("client add restarted core")
	}
	get()
	stats, err := core.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if down := stats["user>>>alice>>>traffic>>>downlink"]; down < int64(2*len(payload)) {
		t.Fatalf("alice downlink %d, want at least %d", down, 2*len(payload))
	}
	if down := stats["user>>>bob>>>traffic>>>downlink"]; down != 0 {
		t.Fatalf("idle bob downlink %d", down)
	}
}
