package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
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
	client, endpoint, err := apiClient(c, path)
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
	printMenuHeader(w, "客户端流量")
	fmt.Fprintln(w, "入站  名称                      上传        下载        最近上线")
	rows := make([]node.Traffic, 0)
	for _, in := range inbounds {
		rows = append(rows, in.ClientStats...)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Email < rows[j].Email })
	for _, client := range rows {
		lastOnline := "从未"
		if client.LastOnline > 0 {
			lastOnline = time.UnixMilli(client.LastOnline).Local().Format("2006-01-02 15:04")
		}
		fmt.Fprintf(w, "%-5d %-25s %-11s %-11s %s\n", client.InboundID, trim(client.Email, 24), bytes(uint64(client.Up)), bytes(uint64(client.Down)), lastOnline)
	}
	if len(rows) == 0 {
		fmt.Fprintln(w, "没有客户端流量记录。")
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

func menuHelp(w io.Writer) {
	fmt.Fprintln(w, "用法: 3x-ui-node menu [status|inbounds|clients|ports|errors]")
	fmt.Fprintln(w, "不带子命令时进入交互菜单。所有菜单项仅读取信息，不会改配置或重启服务。")
}

func runMenu(c node.Config, args []string, in io.Reader, out io.Writer) error {
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
		fmt.Fprintln(out, "\n3x-ui-node · 只读快捷查询")
		fmt.Fprintln(out, "1) 状态  2) 入站  3) 客户端流量  4) 监听端口  5) Xray 错误  0) 退出")
		fmt.Fprint(out, "选择: ")
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
