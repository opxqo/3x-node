package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/opxqo/3x-node/v3/internal/node"
	"golang.org/x/sys/unix"
)

type doctorFinding struct {
	level, name, detail string
	permissionPath      string
}

// Command output may contain credentials or terminal controls. Only interpret it,
// never include it verbatim in the report.
func doctorCommand(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func doctorPermission(path string) doctorFinding {
	f := doctorFinding{level: "PASS", name: "敏感文件权限", detail: path}
	i, err := os.Lstat(path)
	if err != nil || !i.Mode().IsRegular() {
		f.level, f.detail = "WARN", path+"：不可读或非普通文件；请人工检查（不自动处理符号链接）"
		return f
	}
	if i.Mode().Perm() != 0600 {
		f.level, f.detail, f.permissionPath = "WARN", path+"：建议限制为 0600", path
	}
	return f
}

func inspectDoctor(path string) []doctorFinding {
	var findings []doctorFinding
	add := func(level, name, detail string) {
		findings = append(findings, doctorFinding{level: level, name: name, detail: detail})
	}
	add("PASS", "平台", runtime.GOOS+"/"+runtime.GOARCH+" · node "+node.Version)
	c, err := node.LoadConfig(path)
	if err != nil {
		add("FAIL", "配置", "无法读取或校验配置；检查文件存在、JSON 格式和必填项。未输出配置内容。")
	} else {
		add("PASS", "配置", "配置结构有效")
		for _, p := range []string{path, c.KeyFile, c.StateFile} {
			findings = append(findings, doctorPermission(p))
		}
		pair, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
		if err != nil {
			add("FAIL", "TLS", "证书与私钥无法加载或不匹配；保留原文件并人工恢复，勿随意更换主面板固定的证书")
		} else if cert, err := x509.ParseCertificate(pair.Certificate[0]); err != nil {
			add("FAIL", "TLS", "证书解析失败")
		} else if now := time.Now(); now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) {
			add("FAIL", "TLS", "证书不在有效期内；检查系统时间与证书有效期")
		} else if time.Until(cert.NotAfter) < 30*24*time.Hour {
			add("WARN", "TLS", "证书将在 30 天内到期；更换后需同步主面板指纹")
		} else {
			add("PASS", "TLS", "证书匹配且在有效期内")
		}
		if _, err := os.Stat(c.StateFile); err != nil {
			add("FAIL", "节点数据", "状态文件不可访问；请检查原数据路径，勿初始化覆盖")
		} else if _, err := node.LoadState(c.StateFile); err != nil {
			add("FAIL", "节点数据", "状态文件校验失败；请从可信备份恢复")
		} else {
			add("PASS", "节点数据", "状态文件可加载")
		}
		var logBytes int64
		logKnown := true
		for _, name := range []string{"node.log", "node.log.1"} {
			i, err := os.Stat(filepath.Join(filepath.Dir(c.StateFile), name))
			if err == nil {
				logBytes += i.Size()
			} else if !os.IsNotExist(err) {
				logKnown = false
			}
		}
		if !logKnown {
			add("WARN", "日志占用", "部分日志不可访问，无法统计")
		} else {
			level := "PASS"
			if logBytes > 4<<20 {
				level = "WARN"
			}
			add(level, "日志占用", fmt.Sprintf("节点轮转日志合计 %d KiB；不自动删除日志", logBytes>>10))
		}
		if i, err := os.Stat(c.XrayBinary); err != nil || !i.Mode().IsRegular() || i.Mode().Perm()&0111 == 0 {
			add("FAIL", "Xray 文件", "二进制缺失或不可执行；使用可信发布包恢复")
		} else {
			add("PASS", "Xray 文件", "二进制存在且可执行")
		}
		var status serverStatus
		if err := requestAPI(c, "server/status", &status); err != nil {
			add("FAIL", "本地 HTTPS API", "认证查询失败；检查服务、管理端口与 TLS（不输出令牌或响应正文）")
		} else {
			add("PASS", "本地 HTTPS API", "本地 TLS 与认证查询通过")
			if status.Xray.State == "running" {
				add("PASS", "Xray 运行状态", "核心报告正在运行")
			} else {
				add("WARN", "Xray 运行状态", "核心未报告 running；检查启用入站与节点日志")
			}
		}
		var disk unix.Statfs_t
		if err := unix.Statfs(filepath.Dir(c.StateFile), &disk); err != nil {
			add("WARN", "磁盘", "无法读取状态目录文件系统容量")
		} else {
			free := uint64(disk.Bavail) * uint64(disk.Bsize)
			level := "PASS"
			if free < 300<<20 {
				level = "WARN"
			}
			add(level, "数据目录磁盘", fmt.Sprintf("剩余 %d MiB；升级暂存目录另需至少 300 MiB", free>>20))
		}
		if runtime.GOOS == "linux" {
			data, err := doctorCommand("netstat", "-lnt")
			if err != nil {
				add("WARN", "监听端口", "netstat 不可用；无法验证内部 API 是否仅监听回环")
			} else {
				if state, err := node.LoadState(c.StateFile); err == nil {
					for _, inbound := range state.Inbounds {
						if !inbound.Enable {
							continue
						}
						found, _ := doctorAPIBindings(string(data), inbound.Port)
						level := "PASS"
						detail := "存在 TCP 监听；不能仅凭端口确定进程归属"
						if !found {
							level, detail = "FAIL", "启用入站未发现 TCP 监听；检查 Xray 日志与端口冲突"
						}
						add(level, fmt.Sprintf("入站端口 %d", inbound.Port), detail)
					}
				}
				found, exposed := doctorAPIBindings(string(data), c.APIPort)
				if exposed {
					add("FAIL", "内部 Xray API", "发现非回环监听；检查核心配置，禁止映射此端口")
				} else if found {
					add("PASS", "内部 Xray API", "观察到的监听地址均为回环")
				} else {
					add("WARN", "内部 Xray API", "未发现监听；检查核心是否启动")
				}
			}
		}
	}
	if runtime.GOOS == "linux" {
		for _, cmd := range []string{"rc-service", "rc-update", "netstat", "tar", "curl", "sha256sum"} {
			if _, err := exec.LookPath(cmd); err != nil {
				add("WARN", "命令依赖", cmd+" 不可用；通过 Alpine 包管理器检查对应软件包")
			}
		}
		if _, err := doctorCommand("rc-service", "3x-ui-node", "status"); err != nil {
			add("WARN", "OpenRC 服务", "服务状态检查失败；结合本地 API 与 PID 检查，不自动重启")
		} else {
			add("PASS", "OpenRC 服务", "服务管理器报告正常")
		}
		if b, err := os.ReadFile("/run/3x-ui-node.pid"); err == nil {
			pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
			if err != nil || pid <= 1 {
				add("WARN", "PID 文件", "内容无效；需人工核实，不自动删除")
			} else if b, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid)); err != nil || strings.TrimSpace(string(b)) != "3x-ui-node" {
				add("WARN", "PID 文件", "未能确认 PID 指向节点进程；可能退出或 PID 被复用，不自动删除")
			} else {
				add("PASS", "PID 文件", "PID 指向名称为 3x-ui-node 的进程")
			}
		} else {
			add("WARN", "PID 文件", "未能读取 OpenRC PID 文件；检查服务是否受 OpenRC 管理")
		}
		if b, err := doctorCommand("rc-depend", "-a"); err != nil || strings.Contains(string(b), "non existent") || strings.Contains(string(b), "does not exist") {
			add("WARN", "OpenRC 依赖", "依赖检查失败或发现缺失依赖；容器可能省略 dev/hostname，需结合发行版配置人工核实")
		} else {
			add("PASS", "OpenRC 依赖", "未检测到缺失依赖提示")
		}
		for _, p := range []string{"/sys/fs/cgroup/memory.max", "/sys/fs/cgroup/memory/memory.limit_in_bytes"} {
			if b, err := os.ReadFile(p); err == nil {
				if limit, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64); err == nil {
					level := "PASS"
					if limit < 96<<20 {
						level = "FAIL"
					}
					add(level, "cgroup 内存上限", fmt.Sprintf("%d MiB；仅为限制，不代表可用内存或峰值安全", limit>>20))
				} else {
					add("WARN", "cgroup 内存上限", "未获得有限数值；需检查实际容器限制")
				}
			}
		}
		if b, err := os.ReadFile("/sys/fs/cgroup/memory.events"); err == nil {
			level := "PASS"
			for _, line := range strings.Split(string(b), "\n") {
				fields := strings.Fields(line)
				if len(fields) == 2 && (fields[0] == "oom" || fields[0] == "oom_kill") && fields[1] != "0" {
					level = "WARN"
				}
			}
			add(level, "OOM 历史", "已检查 cgroup memory.events；WARN 表示存在累计 OOM 事件，不代表当前持续异常")
		} else {
			add("WARN", "OOM 历史", "memory.events 不可读；无法判定容器 OOM 历史")
		}
	} else {
		add("WARN", "Linux 系统检查", "当前平台跳过 OpenRC、PID、cgroup 与监听检查")
	}
	add("MANUAL", "公网/NAT", "需从外部验证公网管理端口和 VLESS 端口映射，本机检查不能证明公网可达")
	return findings
}

func doctorAPIBindings(output string, port int) (found, exposed bool) {
	for _, line := range strings.Split(output, "\n") {
		f := strings.Fields(line)
		if len(f) < 6 || !strings.HasPrefix(f[0], "tcp") || f[len(f)-1] != "LISTEN" {
			continue
		}
		i := strings.LastIndex(f[3], ":")
		if i < 0 || f[3][i+1:] != strconv.Itoa(port) {
			continue
		}
		found = true
		ip := net.ParseIP(strings.Trim(f[3][:i], "[]"))
		if ip == nil || !ip.IsLoopback() {
			exposed = true
		}
	}
	return
}

// Open without following a final symlink, verify inode ownership and save the
// old mode before changing it. File contents are never copied or logged.
func doctorFixPermission(path string) (string, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return "", err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	i, err := f.Stat()
	if err != nil {
		return "", err
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return "", err
	}
	if !i.Mode().IsRegular() || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
		return "", fmt.Errorf("拒绝修复非普通文件、其他所有者文件或硬链接")
	}
	backup, err := os.CreateTemp(filepath.Dir(path), ".doctor-permissions-*.json")
	if err != nil {
		return "", err
	}
	defer backup.Close()
	if err := json.NewEncoder(backup).Encode(struct {
		Path string
		Mode uint32
	}{path, uint32(i.Mode().Perm())}); err != nil {
		return "", err
	}
	if err := backup.Sync(); err != nil {
		return "", err
	}
	return backup.Name(), f.Chmod(0600)
}

func runDoctor(path string, fix bool, in io.Reader, out io.Writer) error {
	report := func() []doctorFinding {
		findings := inspectDoctor(path)
		for _, f := range findings {
			fmt.Fprintf(out, "[%s] %s：%s\n", f.level, f.name, safeTerminalText(f.detail))
		}
		return findings
	}
	fmt.Fprintln(out, "3X NODE Doctor · 只检查节点运行所需环境")
	findings := report()
	repairFailed := false
	if fix {
		reader := bufio.NewReader(in)
		for _, f := range findings {
			if f.permissionPath == "" {
				continue
			}
			fmt.Fprintf(out, "修复方案：将 %s 权限改为 0600；同目录备份原权限，不修改内容、不重启服务。\n", safeTerminalText(f.permissionPath))
			answer, err := prompt(reader, out, "确认执行？y/n", "n")
			if err != nil {
				return err
			}
			if !strings.EqualFold(answer, "y") && !strings.EqualFold(answer, "yes") {
				continue
			}
			backup, err := doctorFixPermission(f.permissionPath)
			if err != nil {
				repairFailed = true
				fmt.Fprintln(out, "修复失败：文件状态或权限不满足安全修复条件；请人工检查。")
			} else {
				fmt.Fprintf(out, "已修复；原权限记录：%s\n", safeTerminalText(backup))
			}
		}
		fmt.Fprintln(out, "复查结果：")
		findings = report()
	}
	fmt.Fprintln(out, "--fix 当前仅自动修复敏感文件权限；服务、PID、系统依赖、网络与证书问题需要人工处理。")
	if repairFailed {
		return fmt.Errorf("doctor 部分修复失败，请检查复查结果")
	}
	for _, f := range findings {
		if f.level == "FAIL" {
			return fmt.Errorf("doctor 检测到失败项，请按报告处理")
		}
	}
	return nil
}
