package main

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/node"
)

func syncCommand(configPath string, c node.Config, args []string, in io.Reader, out io.Writer, terminal bool) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: 3x-ui-node sync [configure|status|preview|now|disable]")
	}
	switch args[0] {
	case "configure":
		reader, ok := in.(*bufio.Reader)
		if !ok {
			reader = bufio.NewReader(in)
		}
		return configureSync(configPath, c, reader, out, terminal)
	case "disable":
		if !c.MasterSync.Enabled {
			fmt.Fprintln(out, "主面板同步已经停用")
			return nil
		}
		c.MasterSync.Enabled = false
		if err := node.SaveConfig(configPath, c); err != nil {
			return err
		}
		fmt.Fprintln(out, "主面板同步已停用；Token 文件保留，重启服务后生效")
		return nil
	case "status":
		if !c.MasterSync.Enabled {
			fmt.Fprintln(out, "主面板同步：未启用")
			return nil
		}
		var status node.MasterSyncStatus
		if err := requestJSON(c, http.MethodGet, "sync/status", nil, &status); err != nil {
			return err
		}
		printSyncStatus(out, status)
		return nil
	case "preview", "now":
		if !c.MasterSync.Enabled {
			return fmt.Errorf("master sync is disabled; configure masterSync first")
		}
		var preview node.MasterSyncPreview
		path := "sync/preview"
		if args[0] == "now" {
			path = "sync/now"
		}
		if err := requestJSON(c, http.MethodPost, path, nil, &preview); err != nil {
			return err
		}
		if args[0] == "preview" {
			fmt.Fprintln(out, "主面板同步预览（只读，不修改副面板）")
		} else {
			fmt.Fprintln(out, "主面板同步已完成")
		}
		printSyncPreview(out, preview)
		return nil
	default:
		return fmt.Errorf("usage: 3x-ui-node sync [configure|status|preview|now|disable]")
	}
}

func isTerminalInput(in io.Reader) bool {
	f, ok := in.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0 && exec.Command("test", "-t", strconv.Itoa(int(f.Fd()))).Run() == nil
}

func promptSecret(reader *bufio.Reader, out io.Writer, label string, terminal bool) (string, error) {
	fmt.Fprintf(out, "%s: ", label)
	if terminal {
		_ = exec.Command("stty", "-echo").Run()
		defer func() {
			_ = exec.Command("stty", "echo").Run()
			fmt.Fprintln(out)
		}()
	}
	v, err := reader.ReadString('\n')
	if err != nil && len(v) == 0 {
		return "", err
	}
	return strings.TrimSpace(v), nil
}

func promptInt(reader *bufio.Reader, out io.Writer, label string, fallback int) (int, error) {
	value, err := prompt(reader, out, label, strconv.Itoa(fallback))
	if err != nil {
		return 0, err
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s 必须是正整数", label)
	}
	return parsed, nil
}

func configureSync(configPath string, old node.Config, reader *bufio.Reader, out io.Writer, terminal bool) error {
	fmt.Fprintln(out, "主面板同步配置向导（只配置副面板，不修改主面板）")
	base, err := prompt(reader, out, "主面板地址或 /panel/api-docs 地址", old.MasterSync.BaseURL)
	if err != nil {
		return err
	}
	base, err = node.NormalizeMasterBaseURL(base)
	if err != nil {
		return err
	}
	token, err := promptSecret(reader, out, "主面板 API Token", terminal)
	if err != nil {
		return err
	}
	if token == "" {
		return fmt.Errorf("主面板 API Token 不能为空")
	}
	fingerprint, err := prompt(reader, out, "主面板 TLS SHA256 指纹", old.MasterSync.CertSHA256)
	if err != nil {
		return err
	}
	if err = node.ValidateSHA256Fingerprint(fingerprint); err != nil {
		return err
	}
	interval, err := promptInt(reader, out, "同步间隔秒数（30-3600）", defaultSyncInterval(old.MasterSync.IntervalSeconds))
	if err != nil {
		return err
	}
	count, err := promptInt(reader, out, "映射数量（1-8）", len(old.MasterSync.Mappings))
	if err != nil || count < 1 || count > node.MaxInbounds {
		return fmt.Errorf("映射数量必须是 1-%d", node.MaxInbounds)
	}
	mappings := make([]node.MasterSyncMapping, 0, count)
	for j := 0; j < count; j++ {
		var previous node.MasterSyncMapping
		if j < len(old.MasterSync.Mappings) {
			previous = old.MasterSync.Mappings[j]
		}
		id, err := prompt(reader, out, fmt.Sprintf("映射 %d 名称", j+1), previous.ID)
		if err != nil {
			return err
		}
		masterID, err := promptInt(reader, out, fmt.Sprintf("映射 %d 主面板入站 ID", j+1), previous.MasterInboundID)
		if err != nil {
			return err
		}
		localID, err := promptInt(reader, out, fmt.Sprintf("映射 %d 副面板入站 ID", j+1), previous.LocalInboundID)
		if err != nil {
			return err
		}
		mappings = append(mappings, node.MasterSyncMapping{ID: id, MasterInboundID: masterID, LocalInboundID: localID})
	}
	candidate := old
	candidate.MasterSync = node.MasterSyncConfig{Enabled: true, BaseURL: base, TokenFile: filepath.Join(filepath.Dir(configPath), "master.token"), CertSHA256: fingerprint, IntervalSeconds: interval, Mappings: mappings}
	if err = node.ValidateMasterSync(candidate.MasterSync); err != nil {
		return err
	}
	fmt.Fprintf(out, "\n将启用 %d 个映射，同步间隔 %d 秒。不会显示 Token。\n", len(mappings), interval)
	confirm, err := prompt(reader, out, "确认保存并启用？y/n", "n")
	if err != nil {
		return err
	}
	if !strings.EqualFold(confirm, "y") && !strings.EqualFold(confirm, "yes") {
		fmt.Fprintln(out, "已取消，未修改配置。")
		return nil
	}
	tokenPath := candidate.MasterSync.TokenFile
	oldToken, oldTokenErr := os.ReadFile(tokenPath)
	tokenInfo, tokenStatErr := os.Lstat(tokenPath)
	if tokenStatErr == nil && tokenInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("Token 文件不能是符号链接")
	}
	if err = node.AtomicWrite(tokenPath, []byte(strings.TrimSpace(token)+"\n"), false); err != nil {
		return err
	}
	if err = node.SaveConfig(configPath, candidate); err != nil {
		if oldTokenErr == nil {
			_ = node.AtomicWrite(tokenPath, oldToken, false)
		} else {
			_ = os.Remove(tokenPath)
		}
		return err
	}
	fmt.Fprintln(out, "主面板同步配置已保存；重启服务后生效。")
	return nil
}

func defaultSyncInterval(value int) int {
	if value == 0 {
		return node.DefaultMasterSyncInterval
	}
	return value
}

func printSyncStatus(out io.Writer, status node.MasterSyncStatus) {
	printMenuHeader(out, "主面板同步状态")
	state := "空闲"
	if status.InProgress {
		state = "同步中"
	}
	fmt.Fprintf(out, "状态              %s\n", state)
	fmt.Fprintf(out, "连续失败          %d\n", status.ConsecutiveFailures)
	if status.LastAttemptUnix > 0 {
		fmt.Fprintf(out, "最近尝试          %s\n", time.Unix(status.LastAttemptUnix, 0).Format(time.RFC3339))
	}
	if status.LastSuccessUnix > 0 {
		fmt.Fprintf(out, "最近成功          %s\n", time.Unix(status.LastSuccessUnix, 0).Format(time.RFC3339))
	}
	if status.LastError != "" {
		fmt.Fprintf(out, "最近错误          %s\n", status.LastError)
	}
}

func printSyncPreview(out io.Writer, preview node.MasterSyncPreview) {
	fmt.Fprintln(out, "映射                 远端客户端  新增  更新  不变  待清理")
	for _, row := range preview.Mappings {
		fmt.Fprintf(out, "%-20s %-10d %-5d %-5d %-5d %-6d\n", row.MappingID, row.RemoteClients, row.Added, row.Updated, row.Unchanged, row.PendingRemoval)
		for _, conflict := range row.Conflict {
			fmt.Fprintf(out, "  冲突：%s\n", conflict)
		}
	}
}
