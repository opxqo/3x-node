package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

const (
	nodeInstallScriptURL = "https://raw.githubusercontent.com/opxqo/3x-node/main/install-node.sh"
	nodeBaseDir          = "/usr/local/lib/3x-ui-node"
	nodeConfigDir        = "/etc/3x-ui-node"
	nodeDataDir          = "/var/lib/3x-ui-node"
	nodeServiceFile      = "/etc/init.d/3x-ui-node"
	nodeBinLink          = "/usr/local/bin/3x-ui-node"
	nodeCompatBinLink    = "/usr/local/bin/x-ui"
)

func runShell(out io.Writer, script string) error {
	cmd := exec.Command("sh", "-c", script)
	cmd.Stdout, cmd.Stderr = out, out
	return cmd.Run()
}

// updateNode re-runs the same one-command bootstrap the docs tell operators
// to use manually, so the pinned version and per-arch SHA256 always come from
// the latest published install-node.sh instead of being duplicated here.
func updateNode(reader *bufio.Reader, out io.Writer) error {
	confirm, err := prompt(reader, out, "从 GitHub 下载并升级到最新节点版本，会短暂中断服务，确认？y/n", "n")
	if err != nil {
		return err
	}
	if !strings.EqualFold(confirm, "y") && !strings.EqualFold(confirm, "yes") {
		fmt.Fprintln(out, "已取消，未执行升级。")
		return nil
	}
	return performNodeUpdate(out)
}

// Shared executor: callers must obtain confirmation before invoking it.
func performNodeUpdate(out io.Writer) error {
	fmt.Fprintln(out, "正在下载安装脚本并升级，请稍候…")
	script := fmt.Sprintf(`set -e
command -v curl >/dev/null 2>&1 || apk add --no-cache ca-certificates curl
work=$(mktemp -d /usr/local/lib/3x-ui-node-selfupdate.XXXXXX)
trap 'rm -rf -- "$work"' EXIT
curl -fLSs --proto '=https' --proto-redir '=https' %q -o "$work/install-node.sh"
sh "$work/install-node.sh" upgrade`, nodeInstallScriptURL)
	return runShell(out, script)
}

// uninstallNode mirrors the full panel's x-ui uninstall: stop the service,
// then remove the installation, config/token, and data directories.
func uninstallNode(reader *bufio.Reader, out io.Writer) error {
	fmt.Fprintln(out, "卸载将停止服务并删除安装目录、配置、Token、证书与数据；此操作不可恢复。")
	confirm, err := prompt(reader, out, `输入 uninstall 确认卸载`, "")
	if err != nil {
		return err
	}
	if !strings.EqualFold(confirm, "uninstall") {
		fmt.Fprintln(out, "已取消，未卸载。")
		return nil
	}
	if err := runShell(out, "rc-service 3x-ui-node stop"); err != nil {
		fmt.Fprintf(out, "停止服务失败（继续卸载）: %v\n", err)
	}
	if err := runShell(out, "rc-update del 3x-ui-node default"); err != nil {
		fmt.Fprintf(out, "移除开机启动项失败（继续卸载）: %v\n", err)
	}
	if target, e := os.Readlink(nodeCompatBinLink); e == nil && strings.HasPrefix(target, nodeBaseDir) {
		if e := os.Remove(nodeCompatBinLink); e != nil {
			fmt.Fprintf(out, "删除 %s 失败: %v\n", nodeCompatBinLink, e)
		}
	}
	for _, path := range []string{nodeServiceFile, nodeBinLink, nodeBaseDir, nodeConfigDir, nodeDataDir} {
		if e := os.RemoveAll(path); e != nil {
			fmt.Fprintf(out, "删除 %s 失败: %v\n", path, e)
		}
	}
	fmt.Fprintln(out, "3x-ui-node 已卸载。")
	return nil
}
