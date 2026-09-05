// nodebench runs outside the measured node's cgroup on an isolated Docker network.
package main

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/node"
	"golang.org/x/net/proxy"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
func run() error {
	duration := flag.Duration("duration", time.Minute, "load duration; 24h for soak")
	flag.Parse()
	c, err := node.LoadConfig("/settings/config.json")
	if err != nil {
		return err
	}
	pem, err := os.ReadFile(c.CertFile)
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(pem)
	api := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "localhost", MinVersion: tls.VersionTLS12}}}
	defer api.CloseIdleConnections()
	call := func(path string, body any) error {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", "https://11.233.0.2:2053/panel/api/"+path, strings.NewReader(string(b)))
		req.Header.Set("Authorization", "Bearer "+c.Token)
		req.Header.Set("Content-Type", "application/json")
		resp, e := api.Do(req)
		if e != nil {
			return e
		}
		defer resp.Body.Close()
		var env node.Envelope
		if e = json.NewDecoder(resp.Body).Decode(&env); e != nil {
			return e
		}
		if !env.Success {
			return fmt.Errorf("%s: %s", path, env.Msg)
		}
		return nil
	}
	listen := func(port string) (net.Listener, error) { return net.Listen("tcp", "0.0.0.0:"+port) }
	origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		chunk := make([]byte, 2500)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				if _, e := w.Write(chunk); e != nil {
					return
				}
				w.(http.Flusher).Flush()
			}
		}
	}))
	origin.Listener, err = listen("18080")
	if err != nil {
		return err
	}
	origin.Start()
	defer origin.Close()
	target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("target")) }))
	target.Listener, err = listen("18443")
	if err != nil {
		return err
	}
	target.EnableHTTP2 = true
	target.TLS = &tls.Config{MinVersion: tls.VersionTLS13, CurvePreferences: []tls.CurveID{tls.X25519}, SessionTicketsDisabled: true}
	target.Config.ErrorLog = log.New(io.Discard, "", 0)
	target.StartTLS()
	defer target.Close()
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	clients := []map[string]any{}
	socks := []any{}
	outs := []any{}
	rules := []any{}
	for i := 0; i < 5; i++ {
		uuid := fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1)
		clients = append(clients, map[string]any{"id": uuid, "email": fmt.Sprintf("user%d", i), "enable": true, "flow": "xtls-rprx-vision", "subId": fmt.Sprintf("sub%d", i)})
		port := 24443
		if i >= 3 {
			port = 24444
		}
		tag := fmt.Sprintf("user%d", i)
		socks = append(socks, map[string]any{"tag": tag, "listen": "127.0.0.1", "port": 19080 + i, "protocol": "socks", "settings": map[string]any{"auth": "noauth"}})
		outs = append(outs, map[string]any{"tag": tag, "protocol": "vless", "settings": map[string]any{"vnext": []any{map[string]any{"address": "11.233.0.2", "port": port, "users": []any{map[string]any{"id": uuid, "encryption": "none", "flow": "xtls-rprx-vision"}}}}}, "streamSettings": map[string]any{"network": "tcp", "security": "reality", "realitySettings": map[string]any{"serverName": "example.org", "fingerprint": "chrome", "publicKey": base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()), "shortId": "ab"}}})
		rules = append(rules, map[string]any{"type": "field", "inboundTag": []string{tag}, "outboundTag": tag})
	}
	for i := 0; i < 2; i++ {
		cs := clients[:3]
		if i == 1 {
			cs = clients[3:]
		}
		ib := map[string]any{"tag": fmt.Sprintf("bench%d", i), "port": 24443 + i, "protocol": "vless", "enable": true, "settings": map[string]any{"decryption": "none", "clients": cs}, "streamSettings": map[string]any{"network": "tcp", "security": "reality", "realitySettings": map[string]any{"privateKey": base64.RawURLEncoding.EncodeToString(key.Bytes()), "target": "11.233.0.3:18443", "serverNames": []string{"example.org"}, "shortIds": []string{"ab"}}}, "sniffing": map[string]any{"enabled": false}}
		if err = call("inbounds/add", ib); err != nil {
			return err
		}
	}
	b, _ := json.Marshal(map[string]any{"log": map[string]any{"loglevel": "warning", "access": "none"}, "inbounds": socks, "outbounds": outs, "routing": map[string]any{"rules": rules}})
	if err = node.AtomicWrite("/tmp/bench-client.json", b, false); err != nil {
		return err
	}
	client := exec.Command("/tools/xray-linux/xray", "run", "-config", "/tmp/bench-client.json")
	client.Stderr = os.Stderr
	client.Stdout = os.Stderr
	if err = client.Start(); err != nil {
		return err
	}
	defer func() { _ = client.Process.Kill(); _ = client.Wait() }()
	for deadline := time.Now().Add(5 * time.Second); ; {
		conn, e := net.DialTimeout("tcp", "127.0.0.1:19080", 100*time.Millisecond)
		if e == nil {
			conn.Close()
			break
		}
		if time.Now().After(deadline) {
			return e
		}
		time.Sleep(50 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *duration)
	defer cancel()
	var bytes atomic.Int64
	var started atomic.Int64
	var failed atomic.Int64
	var wg sync.WaitGroup
	begin := time.Now()
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			dialer, e := proxy.SOCKS5("tcp", fmt.Sprintf("127.0.0.1:%d", 19080+i%5), nil, &net.Dialer{Timeout: 5 * time.Second})
			if e != nil {
				failed.Add(1)
				return
			}
			tr := &http.Transport{DialContext: dialer.(proxy.ContextDialer).DialContext, DisableKeepAlives: true, ResponseHeaderTimeout: 20 * time.Second}
			defer tr.CloseIdleConnections()
			req, _ := http.NewRequestWithContext(ctx, "GET", "http://11.233.0.3:18080/stream", nil)
			resp, e := (&http.Client{Transport: tr}).Do(req)
			if e != nil {
				failed.Add(1)
				log.Print(e)
				return
			}
			defer resp.Body.Close()
			started.Add(1)
			buf := make([]byte, 8192)
			for {
				n, e := resp.Body.Read(buf)
				bytes.Add(int64(n))
				if e != nil {
					if ctx.Err() == nil {
						failed.Add(1)
						log.Print(e)
					}
					return
				}
			}
		}(i)
	}
	wg.Wait()
	result := map[string]any{"connections": 50, "established": started.Load(), "failures": failed.Load(), "bytes": bytes.Load(), "seconds": time.Since(begin).Seconds(), "mbps": float64(bytes.Load()) * 8 / time.Since(begin).Seconds() / 1e6, "inbounds": 2, "users": 5}
	_ = json.NewEncoder(os.Stdout).Encode(result)
	if started.Load() != 50 || failed.Load() != 0 {
		return fmt.Errorf("load failed: %+v", result)
	}
	return nil
}
