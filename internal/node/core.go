package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Account struct {
	ID   string
	Flow string
}
type LiveInbound struct {
	Tag    string
	Listen string
	Port   int
	Stream Object
	Users  map[string]Account
}

type Engine interface {
	Apply([]LiveInbound) error
	Stats() (map[string]int64, error)
	Running() bool
	Stop() error
	PID() int
}

type Core struct {
	config         Config
	cmd            *exec.Cmd
	done           chan error
	conn           *grpc.ClientConn
	live           []LiveInbound
	Generation     uint64
	lastError      string
	versionChecked bool
	Log            *RotatingLog
	Events         chan struct{}
	// Tests permit only their ephemeral loopback origin, without weakening production defaults.
	configForTest func(map[string]any)
}

func NewCore(c Config) *Core { return &Core{config: c, Events: make(chan struct{}, 1)} }
func (c *Core) PID() int {
	if c.cmd == nil || !c.Running() {
		return 0
	}
	return c.cmd.Process.Pid
}
func (c *Core) Running() bool {
	if c.cmd == nil {
		return false
	}
	select {
	case err := <-c.done:
		c.cmd = nil
		if c.conn != nil {
			_ = c.conn.Close()
			c.conn = nil
		}
		if err != nil {
			c.lastError = err.Error()
		}
		return false
	default:
		return true
	}
}

func (c *Core) Stop() error {
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
	if c.cmd == nil {
		return nil
	}
	_ = c.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-c.done:
	case <-time.After(5 * time.Second):
		_ = c.cmd.Process.Kill()
		<-c.done
	}
	c.cmd = nil
	return nil
}

func skeleton(in []LiveInbound) []LiveInbound {
	out := make([]LiveInbound, len(in))
	copy(out, in)
	for j := range out {
		out[j].Users = nil
	}
	return out
}

func (c *Core) Apply(next []LiveInbound) error {
	if !c.Running() || !reflect.DeepEqual(skeleton(c.live), skeleton(next)) {
		return c.restart(next)
	}
	old := cloneLive(c.live)
	if err := c.hot(next); err != nil {
		// A timed-out RPC may have applied; rebuild old state rather than trusting the local diff.
		if rollback := c.restart(old); rollback != nil {
			_ = c.Stop()
			return fmt.Errorf("hot update failed; rollback failed, core stopped: %w", errors.Join(err, rollback))
		}
		return err
	}
	return nil
}

func cloneLive(in []LiveInbound) []LiveInbound {
	b, _ := json.Marshal(in)
	var out []LiveInbound
	_ = json.Unmarshal(b, &out)
	return out
}

func (c *Core) hot(next []LiveInbound) error {
	for j := range next {
		for email, old := range c.live[j].Users {
			if want, ok := next[j].Users[email]; ok && want == old {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			err := alterUser(ctx, c.conn, c.live[j].Tag, email, nil)
			cancel()
			if err != nil {
				return fmt.Errorf("remove user: %w", err)
			}
			delete(c.live[j].Users, email)
		}
		for email, want := range next[j].Users {
			if got, ok := c.live[j].Users[email]; ok && got == want {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			err := alterUser(ctx, c.conn, c.live[j].Tag, email, &want)
			cancel()
			if err != nil {
				return fmt.Errorf("add user: %w", err)
			}
			c.live[j].Users[email] = want
		}
	}
	return nil
}

func (c *Core) restart(next []LiveInbound) error {
	old := cloneLive(c.live)
	if err := c.Stop(); err != nil {
		return err
	}
	if err := c.start(next); err != nil {
		_ = c.Stop()
		if rollback := c.start(old); rollback != nil {
			_ = c.Stop()
			return fmt.Errorf("start and rollback failed: %w", errors.Join(err, rollback))
		}
		return fmt.Errorf("invalid replacement; previous core restored: %w", err)
	}
	return nil
}

func (c *Core) start(next []LiveInbound) error {
	if !c.versionChecked {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		cmd := exec.CommandContext(ctx, c.config.XrayBinary, "version")
		cmd.Env = coreEnv()
		b, err := cmd.Output()
		cancel()
		if err != nil {
			return fmt.Errorf("Xray version check: %w", err)
		}
		if !strings.HasPrefix(string(b), "Xray "+XrayVersion+" ") {
			return errors.New("Xray version does not match pinned " + XrayVersion)
		}
		c.versionChecked = true
	}
	probe, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(c.config.APIPort)))
	if err != nil {
		return fmt.Errorf("Xray API port unavailable: %w", err)
	}
	_ = probe.Close()
	config := c.xrayConfig(next)
	if c.configForTest != nil {
		c.configForTest(config)
	}
	b, err := json.Marshal(config)
	if err != nil {
		return err
	}
	path := filepath.Join(filepath.Dir(c.config.StateFile), "xray.json")
	if err = AtomicWrite(path, b, false); err != nil {
		return err
	}
	cmd := exec.Command(c.config.XrayBinary, "run", "-config", path)
	cmd.Env = coreEnv()
	childAttrs(cmd)
	if c.Log != nil {
		cmd.Stdout = c.Log
		cmd.Stderr = c.Log
	}
	if err = cmd.Start(); err != nil {
		return err
	}
	c.cmd = cmd
	c.done = make(chan error, 1)
	done := c.done
	go func() {
		done <- cmd.Wait()
		select {
		case c.Events <- struct{}{}:
		default:
		}
	}()
	c.Generation++
	conn, err := grpc.NewClient(net.JoinHostPort("127.0.0.1", strconv.Itoa(c.config.APIPort)), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(MaxBody)))
	if err != nil {
		return err
	}
	c.conn = conn
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if !c.Running() {
			return errors.New("Xray exited during startup; inspect node log")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		_, err = queryStats(ctx, conn)
		cancel()
		if err == nil {
			c.live = cloneLive(next)
			c.lastError = ""
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("Xray API did not become ready")
}

func coreEnv() []string {
	env := []string{}
	for _, s := range os.Environ() {
		if strings.HasPrefix(s, "GOMEMLIMIT=") || strings.HasPrefix(s, "GOGC=") || strings.HasPrefix(s, "GOMAXPROCS=") {
			continue
		}
		env = append(env, s)
	}
	return append(env, "GOMAXPROCS=1", "GOGC=50", "GOMEMLIMIT=48MiB")
}

func (c *Core) xrayConfig(in []LiveInbound) map[string]any {
	inbounds := []any{map[string]any{"tag": "api", "listen": "127.0.0.1", "port": c.config.APIPort, "protocol": "dokodemo-door", "settings": map[string]any{"address": "127.0.0.1"}}}
	for _, ib := range in {
		if ib.Listen == "" {
			ib.Listen = "0.0.0.0"
		}
		users := []any{}
		for email, a := range ib.Users {
			users = append(users, map[string]any{"email": email, "id": a.ID, "flow": a.Flow, "level": 0})
		}
		var stream map[string]json.RawMessage
		_ = json.Unmarshal(ib.Stream, &stream)
		delete(stream, "externalProxy")
		inbounds = append(inbounds, map[string]any{"tag": ib.Tag, "listen": ib.Listen, "port": ib.Port, "protocol": "vless", "settings": map[string]any{"clients": users, "decryption": "none"}, "streamSettings": stream, "sniffing": map[string]any{"enabled": false}})
	}
	return map[string]any{
		"log": map[string]any{"loglevel": "warning", "access": "none"}, "inbounds": inbounds,
		// Xray's Freedom outbound is deny-by-default when finalRules is omitted.
		// Keep the node's only egress explicit: proxy traffic may leave directly.
		"outbounds": []any{map[string]any{"tag": "direct", "protocol": "freedom", "settings": map[string]any{"finalRules": []any{map[string]any{"action": "allow"}}}}},
		"api":       map[string]any{"tag": "api", "services": []string{"HandlerService", "StatsService"}}, "stats": map[string]any{},
		"routing": map[string]any{"domainStrategy": "AsIs", "rules": []any{map[string]any{"type": "field", "inboundTag": []string{"api"}, "outboundTag": "api"}}},
		"policy":  map[string]any{"levels": map[string]any{"0": map[string]any{"bufferSize": 0, "statsUserUplink": true, "statsUserDownlink": true}}, "system": map[string]any{"statsInboundUplink": true, "statsInboundDownlink": true}},
	}
}

func (c *Core) Stats() (map[string]int64, error) {
	if !c.Running() {
		return nil, errors.New("Xray is not running")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return queryStats(ctx, c.conn)
}
