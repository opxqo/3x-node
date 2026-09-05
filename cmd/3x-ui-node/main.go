package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/node"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	command := "serve"
	args := os.Args[1:]
	if len(args) > 0 {
		command = args[0]
		args = args[1:]
	}
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	path := f.String("config", "/etc/3x-ui-node/config.json", "configuration file")
	listen := f.String("listen", "0.0.0.0:2053", "HTTPS bind address for init")
	state := f.String("state", "/var/lib/3x-ui-node/state.json", "state path for init")
	xray := f.String("xray", "/usr/local/lib/3x-ui-node/xray", "Xray binary for init")
	if err := f.Parse(args); err != nil {
		return err
	}
	if command == "version" {
		fmt.Println(node.Version)
		return nil
	}
	if command == "init" {
		c, err := node.InitConfig(*path, *listen, *state, *xray)
		if err != nil {
			return err
		}
		pin, err := node.Fingerprint(c)
		if err != nil {
			return err
		}
		fmt.Printf("Config: %s\nTLS SHA256: %s\nToken: stored in config.json (0600); use credentials to display\n", *path, pin)
		return nil
	}
	c, err := node.LoadConfig(*path)
	if err != nil {
		return err
	}
	switch command {
	case "credentials":
		pin, err := node.Fingerprint(c)
		if err != nil {
			return err
		}
		fmt.Printf("API token: %s\nTLS SHA256: %s\nListen: %s\nBase path: %s\n", c.Token, pin, c.Listen, c.BasePath)
		return nil
	case "rotate-token":
		c.Token = node.RandomHex(32)
		b, _ := json.MarshalIndent(c, "", "  ")
		if err = node.AtomicWrite(*path, b, true); err != nil {
			return err
		}
		fmt.Println("Token rotated. Restart 3x-ui-node, then update the master using credentials.")
		return nil
	case "check":
		if _, err = tls.LoadX509KeyPair(c.CertFile, c.KeyFile); err != nil {
			return err
		}
		if _, err = node.LoadState(c.StateFile); err != nil {
			return err
		}
		info, err := os.Stat(c.XrayBinary)
		if err != nil {
			return err
		}
		if info.Mode()&0111 == 0 {
			return fmt.Errorf("Xray binary is not executable")
		}
		fmt.Printf("Config valid; platform=%s/%s; inspect cgroup memory and NAT mappings before starting\n", runtime.GOOS, runtime.GOARCH)
		return nil
	case "status":
		return status(c)
	case "serve":
	default:
		return fmt.Errorf("commands: init, serve, check, status, credentials, rotate-token, version")
	}
	runtime.GOMAXPROCS(1)
	debug.SetGCPercent(50)
	debug.SetMemoryLimit(16 << 20)
	if err = os.MkdirAll(filepath.Dir(c.StateFile), 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(c.StateFile+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("node already running: %w", err)
	}
	logger := &node.RotatingLog{Path: filepath.Join(filepath.Dir(c.StateFile), "node.log")}
	log.SetOutput(logger)
	core := node.NewCore(c)
	core.Log = logger
	defer core.Stop()
	n, err := node.New(c, core)
	if err != nil {
		return err
	}
	srv, listener, err := n.Server()
	if err != nil {
		return err
	}
	defer listener.Close()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	runCtx, stopRun := context.WithCancel(context.Background())
	defer stopRun()
	done := make(chan struct{})
	go func() { n.Run(runCtx); close(done) }()
	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.Serve(listener) }()
	select {
	case <-ctx.Done():
	case err = <-serveDone:
		stop()
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
	stopRun()
	<-done
	if err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func status(c node.Config) error {
	_, port, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(c.CertFile)
	if err != nil {
		return err
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(b)
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: "localhost", MinVersion: tls.VersionTLS12}}}
	defer client.CloseIdleConnections()
	r, err := http.NewRequest(http.MethodGet, "https://127.0.0.1:"+port+c.BasePath+"panel/api/server/status", nil)
	if err != nil {
		return err
	}
	r.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := client.Do(r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("API returned %s", resp.Status)
	}
	_, err = io.Copy(os.Stdout, io.LimitReader(resp.Body, node.MaxBody))
	return err
}
