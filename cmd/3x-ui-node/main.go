package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
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
	if filepath.Base(os.Args[0]) == "x-ui" && len(args) == 0 {
		command = "menu"
	}
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
		if err = node.SaveConfig(*path, c); err != nil {
			return err
		}
		fmt.Println("Token rotated. Restart 3x-ui-node, then update the master using credentials.")
		return nil
	case "default-client":
		return defaultClient(*path, c, f.Args(), os.Stdout)
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
	case "menu":
		return runMenu(*path, c, f.Args(), os.Stdin, os.Stdout)
	case "serve":
	default:
		return fmt.Errorf("commands: init, serve, check, status, menu, credentials, rotate-token, default-client, version")
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

func defaultClient(path string, c node.Config, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] == "show" {
		if c.DefaultClientUUID == "" {
			fmt.Fprintln(out, "Default client: not configured")
			return nil
		}
		fmt.Fprintf(out, "Default client UUID: %s\nDefault client email: %s\n", c.DefaultClientUUID, c.DefaultClientEmail)
		return nil
	}
	switch args[0] {
	case "set":
		if len(args) != 3 {
			return fmt.Errorf("usage: 3x-ui-node default-client set UUID EMAIL")
		}
		c.DefaultClientUUID, c.DefaultClientEmail = args[1], args[2]
		if err := node.ValidateDefaultClient(c.DefaultClientUUID, c.DefaultClientEmail); err != nil {
			return err
		}
		if err := node.SaveConfig(path, c); err != nil {
			return err
		}
		fmt.Fprintln(out, "Default client saved. Run: rc-service 3x-ui-node restart")
		return nil
	case "clear":
		if len(args) != 1 {
			return fmt.Errorf("usage: 3x-ui-node default-client clear")
		}
		c.DefaultClientUUID, c.DefaultClientEmail = "", ""
		if err := node.SaveConfig(path, c); err != nil {
			return err
		}
		fmt.Fprintln(out, "Default client cleared. Run: rc-service 3x-ui-node restart")
		return nil
	default:
		return fmt.Errorf("usage: 3x-ui-node default-client [show|set UUID EMAIL|clear]")
	}
}

func status(c node.Config) error {
	client, endpoint, err := apiClient(c, "server/status")
	if err != nil {
		return err
	}
	defer client.CloseIdleConnections()
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("API returned %s", resp.Status)
	}
	_, err = io.Copy(os.Stdout, io.LimitReader(resp.Body, node.MaxBody))
	return err
}

func apiClient(c node.Config, path string) (*http.Client, string, error) {
	_, port, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return nil, "", err
	}
	b, err := os.ReadFile(c.CertFile)
	if err != nil {
		return nil, "", err
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(b)
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: "localhost", MinVersion: tls.VersionTLS12}}}
	endpoint := "https://127.0.0.1:" + port + c.BasePath + "panel/api/" + path
	return client, endpoint, nil
}
