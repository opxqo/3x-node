package main

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/node"
)

type apiResult[T any] struct {
	Success bool   `json:"success"`
	Msg     string `json:"msg"`
	Obj     T      `json:"obj"`
}

type serverStatus struct {
	CPU float64 `json:"cpu"`
	Mem struct {
		Current uint64 `json:"current"`
		Total   uint64 `json:"total"`
	} `json:"mem"`
	NetIO struct {
		Up   uint64 `json:"up"`
		Down uint64 `json:"down"`
	} `json:"netIO"`
	Uptime uint64 `json:"uptime"`
	Xray   struct {
		State    string `json:"state"`
		ErrorMsg string `json:"errorMsg"`
		Version  string `json:"version"`
	} `json:"xray"`
}

func requestAPI[T any](c node.Config, path string, target *T) error {
	return requestJSON(c, http.MethodGet, path, nil, target)
}

func requestJSON[T any](c node.Config, method, path string, payload any, target *T) error {
	client, endpoint, err := apiClient(c, path)
	if err != nil {
		return err
	}
	defer client.CloseIdleConnections()
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = strings.NewReader(string(encoded))
	}
	req, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("API returned %s", resp.Status)
	}
	var result apiResult[T]
	if err := json.NewDecoder(io.LimitReader(resp.Body, node.MaxBody)).Decode(&result); err != nil {
		return err
	}
	if !result.Success {
		if result.Msg == "" {
			result.Msg = "unknown API error"
		}
		return fmt.Errorf("API: %s", result.Msg)
	}
	*target = result.Obj
	return nil
}

func printMenuHeader(w io.Writer, title string) {
	fmt.Fprintln(w, "\n3x-ui-node · 只读快捷查询")
	fmt.Fprintln(w, strings.Repeat("-", 56))
	fmt.Fprintln(w, title)
	fmt.Fprintln(w, strings.Repeat("-", 56))
}

func bytes(v uint64) string {
	const unit = 1024
	if v < unit {
		return fmt.Sprintf("%d B", v)
	}
	div, exp := uint64(unit), 0
	for n := v / unit; n >= unit && exp < 4; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %ciB", float64(v)/float64(div), "KMGTPE"[exp])
}

func loadStatus(c node.Config) (serverStatus, error) {
	var result serverStatus
	return result, requestAPI(c, "server/status", &result)
}

func showStatus(w io.Writer, c node.Config) error {
	status, err := loadStatus(c)
	if err != nil {
		return err
	}
	printMenuHeader(w, "服务状态")
	fmt.Fprintf(w, "Xray        %s (%s)\n", status.Xray.State, status.Xray.Version)
	fmt.Fprintf(w, "CPU         %.1f%%\n", status.CPU)
	fmt.Fprintf(w, "内存        %s / %s\n", bytes(status.Mem.Current), bytes(status.Mem.Total))
	fmt.Fprintf(w, "网络累计    ↑ %s  ↓ %s\n", bytes(status.NetIO.Up), bytes(status.NetIO.Down))
	fmt.Fprintf(w, "运行时间    %s\n", (time.Duration(status.Uptime) * time.Second).Round(time.Second))
	if status.Xray.ErrorMsg != "" {
		fmt.Fprintf(w, "Xray 错误   %s\n", status.Xray.ErrorMsg)
	}
	return nil
}

type inboundSummary struct {
	Network  string
	Security string
}

func summarizeInbound(in node.Inbound) inboundSummary {
	var stream struct {
		Network  string `json:"network"`
		Security string `json:"security"`
	}
	_ = json.Unmarshal(in.StreamSettings, &stream)
	if stream.Network == "" {
		stream.Network = "tcp"
	}
	if stream.Security == "" {
		stream.Security = "none"
	}
	return inboundSummary{Network: stream.Network, Security: stream.Security}
}

func loadInbounds(c node.Config) ([]node.Inbound, error) {
	var inbounds []node.Inbound
	return inbounds, requestAPI(c, "inbounds/list", &inbounds)
}

func showInbounds(w io.Writer, c node.Config) error {
	inbounds, err := loadInbounds(c)
	if err != nil {
		return err
	}
	printMenuHeader(w, "入站配置")
	fmt.Fprintln(w, "ID   端口    协议    传输/安全       状态   标签")
	for _, in := range inbounds {
		s := summarizeInbound(in)
		state := "停用"
		if in.Enable {
			state = "运行"
		}
		fmt.Fprintf(w, "%-4d %-7d %-7s %-15s %-6s %s\n", in.ID, in.Port, in.Protocol, s.Network+"/"+s.Security, state, in.Tag)
	}
	if len(inbounds) == 0 {
		fmt.Fprintln(w, "没有已配置的入站。")
	}
	return nil
}

func showClients(w io.Writer, c node.Config) error {
	inbounds, err := loadInbounds(c)
	if err != nil {
		return err
	}
	printMenuHeader(w, "客户端与流量")
	fmt.Fprintln(w, "入站  名称                      UUID          状态   上传        下载")
	rows := make([]node.Traffic, 0)
	seen := map[string]bool{}
	for _, in := range inbounds {
		clients, err := in.Clients()
		if err != nil {
			return err
		}
		stats := map[string]node.Traffic{}
		for _, stat := range in.ClientStats {
			stats[stat.Email] = stat
		}
		for _, client := range clients {
			email := client.Text("email")
			key := strconv.Itoa(in.ID) + ":" + email
			if seen[key] {
				continue
			}
			seen[key] = true
			stat := stats[email]
			state := "停用"
			if client.Enabled() {
				state = "启用"
			}
			fmt.Fprintf(w, "%-5d %-25s %-13s %-6s %-11s %-11s\n", in.ID, trim(email, 24), trim(client.Text("id"), 12), state, bytes(uint64(stat.Up)), bytes(uint64(stat.Down)))
		}
		rows = append(rows, in.ClientStats...)
	}
	if len(rows) == 0 && len(seen) == 0 {
		fmt.Fprintln(w, "没有已配置的客户端。")
	}
	return nil
}

func trim(value string, max int) string {
	if len([]rune(value)) <= max {
		return value
	}
	return string([]rune(value)[:max-1]) + "…"
}

func showPorts(w io.Writer, c node.Config) error {
	inbounds, err := loadInbounds(c)
	if err != nil {
		return err
	}
	printMenuHeader(w, "本机监听检查")
	_, panelPort, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return err
	}
	panelHost, _, _ := net.SplitHostPort(c.Listen)
	checkPort(w, "管理 API", panelHost, panelPort)
	for _, in := range inbounds {
		checkPort(w, in.Tag, in.Listen, strconv.Itoa(in.Port))
	}
	return nil
}

func checkPort(w io.Writer, label, listen, port string) {
	host := listen
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), 1200*time.Millisecond)
	state := "未监听"
	if err == nil {
		state = "已监听"
		_ = conn.Close()
	}
	fmt.Fprintf(w, "%-26s %-7s %s\n", label, port, state)
}

func showErrors(w io.Writer, c node.Config) error {
	status, err := loadStatus(c)
	if err != nil {
		return err
	}
	printMenuHeader(w, "Xray 错误概览")
	if status.Xray.ErrorMsg == "" {
		fmt.Fprintln(w, "当前状态接口未报告 Xray 错误。")
		return nil
	}
	fmt.Fprintln(w, status.Xray.ErrorMsg)
	return nil
}

func newUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

func manualClient(id, email string, enabled bool) (node.Client, error) {
	if id == "" {
		var err error
		id, err = newUUID()
		if err != nil {
			return nil, err
		}
	}
	if err := node.ValidateDefaultClient(id, email); err != nil {
		return nil, err
	}
	client := node.Client{}
	client.Set("id", id)
	client.Set("email", email)
	client.Set("enable", enabled)
	client.Set("flow", "")
	client.Set("totalGB", int64(0))
	client.Set("expiryTime", int64(0))
	client.Set("trafficReset", "never")
	client.Set("trafficResetDay", 1)
	return client, nil
}

func prompt(reader *bufio.Reader, out io.Writer, label, fallback string) (string, error) {
	if fallback == "" {
		fmt.Fprintf(out, "%s: ", label)
	} else {
		fmt.Fprintf(out, "%s [%s]: ", label, fallback)
	}
	v, err := reader.ReadString('\n')
	if err != nil && len(v) == 0 {
		return "", err
	}
	v = strings.TrimSpace(v)
	if v == "" {
		v = fallback
	}
	return v, nil
}

func inboundIDs(value string) ([]int, error) {
	parts := strings.Split(value, ",")
	ids := make([]int, 0, len(parts))
	for _, part := range parts {
		id, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || id < 1 {
			return nil, fmt.Errorf("入站 ID 必须是正整数，例如 5 或 5,6")
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func addClientInteractive(c node.Config, reader *bufio.Reader, out io.Writer) error {
	if err := showInbounds(out, c); err != nil {
		return err
	}
	fmt.Fprintln(out, "\n手动添加客户端：UUID 必须与需要使用该节点的订阅客户端一致。")
	idsRaw, err := prompt(reader, out, "绑定入站 ID（支持逗号分隔）", "")
	if err != nil {
		return err
	}
	ids, err := inboundIDs(idsRaw)
	if err != nil {
		return err
	}
	id, err := prompt(reader, out, "客户端 UUID（留空自动生成）", "")
	if err != nil {
		return err
	}
	email, err := prompt(reader, out, "客户端名称/邮箱", "")
	if err != nil {
		return err
	}
	enabledRaw, err := prompt(reader, out, "立即启用？y/n", "y")
	if err != nil {
		return err
	}
	enabled := strings.EqualFold(enabledRaw, "y") || strings.EqualFold(enabledRaw, "yes")
	client, err := manualClient(id, email, enabled)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "\n将添加：%s\nUUID：%s\n入站：%v\n", client.Text("email"), client.Text("id"), ids)
	confirm, err := prompt(reader, out, "确认保存？y/n", "n")
	if err != nil {
		return err
	}
	if !strings.EqualFold(confirm, "y") && !strings.EqualFold(confirm, "yes") {
		fmt.Fprintln(out, "已取消，未修改配置。")
		return nil
	}
	var ignored json.RawMessage
	if err := requestJSON(c, http.MethodPost, "clients/add", map[string]any{"client": client, "inboundIds": ids}, &ignored); err != nil {
		return err
	}
	fmt.Fprintln(out, "客户端已添加，Xray 配置已应用。")
	return nil
}

func deleteClientInteractive(c node.Config, reader *bufio.Reader, out io.Writer) error {
	if err := showClients(out, c); err != nil {
		return err
	}
	email, err := prompt(reader, out, "要删除的客户端名称/邮箱", "")
	if err != nil {
		return err
	}
	if email == "" {
		return fmt.Errorf("客户端名称不能为空")
	}
	confirm, err := prompt(reader, out, "确认从所有入站删除该客户端？y/n", "n")
	if err != nil {
		return err
	}
	if !strings.EqualFold(confirm, "y") && !strings.EqualFold(confirm, "yes") {
		fmt.Fprintln(out, "已取消，未修改配置。")
		return nil
	}
	var ignored json.RawMessage
	if err := requestJSON(c, http.MethodPost, "clients/del/"+url.PathEscape(email), nil, &ignored); err != nil {
		return err
	}
	fmt.Fprintln(out, "客户端已从副面板所有入站删除。")
	return nil
}

func showDefaultClient(w io.Writer, c node.Config) {
	if c.DefaultClientUUID == "" {
		fmt.Fprintln(w, "未设置默认客户端。")
		return
	}
	fmt.Fprintf(w, "默认客户端 UUID: %s\n默认客户端名称: %s\n", c.DefaultClientUUID, c.DefaultClientEmail)
}

func configureDefaultClient(configPath string, c node.Config, reader *bufio.Reader, out io.Writer) error {
	uuid, err := prompt(reader, out, "默认客户端 UUID（输入 clear 清除）", "")
	if err != nil {
		return err
	}
	if uuid == "clear" {
		c.DefaultClientUUID, c.DefaultClientEmail = "", ""
	} else {
		email, err := prompt(reader, out, "默认客户端名称/邮箱", "")
		if err != nil {
			return err
		}
		if err := node.ValidateDefaultClient(uuid, email); err != nil {
			return err
		}
		c.DefaultClientUUID, c.DefaultClientEmail = uuid, email
	}
	confirm, err := prompt(reader, out, "确认保存默认客户端配置？y/n", "n")
	if err != nil {
		return err
	}
	if !strings.EqualFold(confirm, "y") && !strings.EqualFold(confirm, "yes") {
		fmt.Fprintln(out, "已取消，未修改配置。")
		return nil
	}
	if err := node.SaveConfig(configPath, c); err != nil {
		return err
	}
	fmt.Fprintln(out, "默认客户端配置已保存；重启服务后生效。")
	return nil
}

func serviceAction(action string, out io.Writer) error {
	cmd := exec.Command("rc-service", "3x-ui-node", action)
	cmd.Stdout, cmd.Stderr = out, out
	return cmd.Run()
}

func showLogs(c node.Config, out io.Writer) error {
	cmd := exec.Command("tail", "-n", "80", filepath.Join(filepath.Dir(c.StateFile), "node.log"))
	cmd.Stdout, cmd.Stderr = out, out
	return cmd.Run()
}

func menuHelp(w io.Writer) {
	fmt.Fprintln(w, "用法: 3x-ui-node menu [status|inbounds|clients|ports|errors|add-client]")
	fmt.Fprintln(w, "不带子命令时进入交互菜单；13 为手动添加客户端。")
}

func runMenu(configPath string, c node.Config, args []string, in io.Reader, out io.Writer) error {
	show := func(command string) error {
		switch command {
		case "status":
			return showStatus(out, c)
		case "inbounds":
			return showInbounds(out, c)
		case "clients":
			return showClients(out, c)
		case "ports":
			return showPorts(out, c)
		case "errors":
			return showErrors(out, c)
		case "add-client":
			return addClientInteractive(c, bufio.NewReader(in), out)
		case "help", "-h", "--help":
			menuHelp(out)
			return nil
		default:
			return fmt.Errorf("unknown menu view %q", command)
		}
	}
	if len(args) > 0 {
		return show(args[0])
	}

	reader := bufio.NewReader(in)
	for {
		fmt.Fprintln(out, "\n╔══════════════════════════════════════════════╗")
		fmt.Fprintln(out, "║          3X-UI Node 管理菜单                  ║")
		fmt.Fprintln(out, "║  0) 退出                                      ║")
		fmt.Fprintln(out, "║──────────────────────────────────────────────║")
		fmt.Fprintln(out, "║  1) 服务状态       2) 入站列表                ║")
		fmt.Fprintln(out, "║  3) 客户端列表     4) 监听端口                ║")
		fmt.Fprintln(out, "║  5) Xray 错误      6) 连接凭据                ║")
		fmt.Fprintln(out, "║──────────────────────────────────────────────║")
		fmt.Fprintln(out, "║  7) 启动服务       8) 停止服务                ║")
		fmt.Fprintln(out, "║  9) 重启服务      10) 查看日志                ║")
		fmt.Fprintln(out, "║ 11) 默认客户端    12) 设置默认客户端           ║")
		fmt.Fprintln(out, "║ 13) 手动添加客户端 14) 删除客户端              ║")
		fmt.Fprintln(out, "╚══════════════════════════════════════════════╝")
		fmt.Fprint(out, "请输入选项 [0-14]: ")
		choice, err := reader.ReadString('\n')
		if err != nil && len(choice) == 0 {
			return nil
		}
		switch strings.TrimSpace(choice) {
		case "1":
			err = show("status")
		case "2":
			err = show("inbounds")
		case "3":
			err = show("clients")
		case "4":
			err = show("ports")
		case "5":
			err = show("errors")
		case "6":
			pin, pinErr := node.Fingerprint(c)
			if pinErr != nil {
				err = pinErr
				break
			}
			fmt.Fprintf(out, "\nAPI token: %s\nTLS SHA256: %s\nListen: %s\nBase path: %s\n", c.Token, pin, c.Listen, c.BasePath)
		case "7":
			err = serviceAction("start", out)
		case "8":
			var confirm string
			confirm, err = prompt(reader, out, "停止会中断节点连接，确认停止？y/n", "n")
			if err == nil && (strings.EqualFold(confirm, "y") || strings.EqualFold(confirm, "yes")) {
				err = serviceAction("stop", out)
			}
		case "9":
			err = serviceAction("restart", out)
		case "10":
			err = showLogs(c, out)
		case "11":
			showDefaultClient(out, c)
		case "12":
			err = configureDefaultClient(configPath, c, reader, out)
		case "13":
			err = addClientInteractive(c, reader, out)
		case "14":
			err = deleteClientInteractive(c, reader, out)
		case "0", "q", "Q":
			return nil
		default:
			fmt.Fprintln(out, "无效选择。")
			continue
		}
		if err != nil {
			fmt.Fprintf(out, "查询失败: %v\n", err)
		}
	}
}
