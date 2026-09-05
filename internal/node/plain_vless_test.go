package node

import (
	"context"
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

func TestPlainVLESSTCPConnection(t *testing.T) {
	binary := os.Getenv("NODE_XRAY_TEST_BINARY")
	if binary == "" {
		t.Skip("set NODE_XRAY_TEST_BINARY to the pinned Xray binary")
	}
	dir := t.TempDir()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("node-plain-vless-ok"))
	}))
	defer origin.Close()

	cfg := Config{StateFile: filepath.Join(dir, "state.json"), XrayBinary: binary, APIPort: freePort(t), Token: strings.Repeat("a", 64), BasePath: "/"}
	core := NewCore(cfg)
	core.Log = &RotatingLog{Path: filepath.Join(dir, "server.log")}
	core.configForTest = func(config map[string]any) {
		_, originPort, _ := net.SplitHostPort(origin.Listener.Addr().String())
		config["outbounds"] = []any{map[string]any{"tag": "direct", "protocol": "freedom", "settings": map[string]any{"finalRules": []any{map[string]any{"action": "allow", "network": "tcp", "ip": []string{"127.0.0.1/32"}, "port": originPort}}}}}
	}
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

	ib := testInbound()
	ib.Port = freePort(t)
	ib.Listen = "127.0.0.1"
	ib.StreamSettings = Object(`{"network":"tcp","security":"none","tcpSettings":{"header":{"type":"none"}}}`)
	clients, _ := ib.Clients()
	clients[0].Set("flow", "")
	ib.SetClients(clients)
	if _, err = n.PutInbound(ib, 0); err != nil {
		t.Fatal(err)
	}

	socksPort := freePort(t)
	clientConfig := map[string]any{
		"log":      map[string]any{"loglevel": "warning"},
		"inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": socksPort, "protocol": "socks", "settings": map[string]any{"auth": "noauth"}}},
		"outbounds": []any{map[string]any{
			"protocol":       "vless",
			"settings":       map[string]any{"vnext": []any{map[string]any{"address": "127.0.0.1", "port": ib.Port, "users": []any{map[string]any{"id": clients[0].Text("id"), "encryption": "none"}}}}},
			"streamSettings": map[string]any{"network": "tcp", "security": "none"},
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
	resp, err := httpClient.Get(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	got, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil || string(got) != "node-plain-vless-ok" {
		t.Fatalf("plain VLESS response %q: %v", got, readErr)
	}
}
