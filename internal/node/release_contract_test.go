//go:build mastercontract

package node_test

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/node"
	master "github.com/mhsanaei/3x-ui/v3/internal/web/runtime"
)

// Exercise the actual released executable, not the in-process fake engine.
// All credentials, target endpoints and identities are disposable local data.
func TestReleasedNodeMasterREALITY(t *testing.T) {
	binary, xray := os.Getenv("NODE_RELEASE_TEST_BINARY"), os.Getenv("NODE_XRAY_TEST_BINARY")
	if binary == "" || xray == "" {
		t.Skip("set NODE_RELEASE_TEST_BINARY and NODE_XRAY_TEST_BINARY")
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	freePort := func() int {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		must(err)
		p := l.Addr().(*net.TCPAddr).Port
		must(l.Close())
		return p
	}
	dir := t.TempDir()
	plainPort, realityPort := freePort(), freePort()
	dockerImage := os.Getenv("NODE_RELEASE_TEST_DOCKER")
	containerName := "node-release-check-" + strconv.Itoa(os.Getpid())
	path := filepath.Join(dir, "config.json")
	bind := "127.0.0.1:"
	if dockerImage != "" {
		bind = "0.0.0.0:"
	}
	c, err := node.InitConfig(path, bind+strconv.Itoa(freePort()), filepath.Join(dir, "state.json"), xray)
	must(err)
	c.APIPort = freePort()
	must(node.SaveConfig(path, c))
	pin, err := node.Fingerprint(c)
	must(err)
	_, portText, err := net.SplitHostPort(c.Listen)
	must(err)
	port, err := strconv.Atoi(portText)
	must(err)
	r := master.NewRemote(&model.Node{Id: 6, Name: "release-test", Scheme: "https", Address: "127.0.0.1", Port: port, BasePath: c.BasePath, ApiToken: c.Token, Enable: true, AllowPrivateAddress: true, TlsVerifyMode: "pin", PinnedCertSha256: pin}, nil)
	ctx := context.Background()
	start := func() *exec.Cmd {
		p := exec.Command(binary, "serve", "-config", path)
		if dockerImage != "" {
			args := []string{"run", "--rm", "--name", containerName, "--memory=128m", "--memory-swap=256m", "-v", dir + ":" + dir, "-v", filepath.Dir(binary) + ":" + filepath.Dir(binary) + ":ro"}
			if platform := os.Getenv("NODE_RELEASE_TEST_PLATFORM"); platform != "" {
				args = append(args, "--platform", platform)
			}
			for _, v := range []int{port, plainPort, realityPort} {
				mapping := strconv.Itoa(v)
				args = append(args, "-p", "127.0.0.1:"+mapping+":"+mapping)
			}
			args = append(args, dockerImage, binary, "serve", "-config", path)
			p = exec.Command("docker", args...)
		}
		must(p.Start())
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			if _, e := r.ListInboundOptions(ctx); e == nil {
				return p
			}
			time.Sleep(100 * time.Millisecond)
		}
		_ = p.Process.Kill()
		_ = p.Wait()
		t.Fatal("released node did not become ready")
		return nil
	}
	stop := func(p *exec.Cmd) {
		if dockerImage != "" {
			must(exec.Command("docker", "stop", "--time", "10", containerName).Run())
		} else {
			_ = p.Process.Signal(os.Interrupt)
		}
		must(p.Wait())
	}
	p := start()
	t.Cleanup(func() {
		if p != nil {
			stop(p)
		}
	})
	target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("target")) }))
	target.EnableHTTP2 = true
	target.TLS = &tls.Config{MinVersion: tls.VersionTLS13, CurvePreferences: []tls.CurveID{tls.X25519}}
	target.StartTLS()
	defer target.Close()
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	must(err)
	names := []string{"example.org"}
	for i := 1; i < 24; i++ {
		names = append(names, "test"+strconv.Itoa(i)+".example.org")
	}
	stream, err := json.Marshal(map[string]any{
		"network": "tcp", "security": "reality", "tcpSettings": map[string]any{"acceptProxyProtocol": false, "header": map[string]any{"type": "none"}},
		"realitySettings": map[string]any{"show": false, "xver": 0, "target": target.Listener.Addr().String(), "serverNames": names, "privateKey": base64.RawURLEncoding.EncodeToString(key.Bytes()), "minClientVer": "0.0.1", "maxClientVer": "", "maxTimediff": 0, "shortIds": []string{"0123456789", "012345", "0123456789abcdef", "0123456789abcd", "0123", "0123456789ab", "ab", "01234567"}, "mldsa65Seed": "", "settings": map[string]any{"publicKey": base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()), "fingerprint": "chrome", "serverName": "", "spiderX": "/", "mldsa65Verify": ""}},
	})
	must(err)
	plain := &model.Inbound{Id: 83, Tag: "n6-in-26700-tcp", Port: plainPort, Protocol: "vless", Enable: true, Settings: `{"clients":[],"decryption":"none","encryption":"none"}`, StreamSettings: `{"network":"tcp","security":"none"}`, Sniffing: `{"enabled":false}`, TrafficReset: "never"}
	ib := &model.Inbound{Id: 84, Tag: "n6-in-52696-tcp", Port: realityPort, Protocol: "vless", Enable: true, Settings: `{"clients":[],"decryption":"none","encryption":"none","testseed":[900,500,900,256]}`, StreamSettings: string(stream), Sniffing: `{"enabled":false}`, TrafficReset: "never"}
	_, err = r.ReconcileInbound(ctx, plain, false)
	must(err)
	_, err = r.ReconcileInbound(ctx, ib, false)
	must(err)
	check := func() {
		t.Helper()
		options, e := r.ListInboundOptions(ctx)
		must(e)
		if len(options) != 2 {
			t.Fatalf("expected both inbounds, got %d", len(options))
		}
		for _, in := range []*model.Inbound{plain, ib} {
			conn, e := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(in.Port)), time.Second)
			must(e)
			must(conn.Close())
		}
	}
	check()
	t.Log("full-default REALITY empty inbound and existing plain inbound both listening")
	client := model.Client{ID: "00000000-0000-4000-8000-000000000001", Email: "release-test", Enable: true, Flow: "xtls-rprx-vision", TrafficReset: "never", TrafficResetDay: 1}
	must(r.AddClient(ctx, ib, client))
	snapshot, err := r.FetchTrafficSnapshot(ctx)
	must(err)
	found := false
	for _, in := range snapshot.Inbounds {
		if in.Port != ib.Port {
			continue
		}
		var settings struct {
			Clients []struct {
				Email string `json:"email"`
			}
			Testseed []int
		}
		must(json.Unmarshal([]byte(in.Settings), &settings))
		found = len(settings.Clients) == 1 && settings.Clients[0].Email == client.Email && len(settings.Testseed) == 4
	}
	if !found {
		t.Fatal("REALITY client or default seed not persisted")
	}
	stop(p)
	p = nil
	p = start()
	check()
	must(r.DeleteUser(ctx, ib, client.Email))
	check()
	t.Log("real master hot client add, released-process restart, persistence and delete passed")
}
