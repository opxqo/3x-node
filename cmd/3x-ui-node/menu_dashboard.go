package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/node"
)

type statusUpdate struct {
	generation int
	status     serverStatus
	err        error
}

func fetchDashboardStatus(ctx context.Context, c node.Config) (serverStatus, error) {
	var result apiResult[serverStatus]
	client, endpoint, err := apiClient(c, "server/status")
	if err != nil {
		return result.Obj, err
	}
	defer client.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return result.Obj, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := client.Do(req)
	if err != nil {
		return result.Obj, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return result.Obj, fmt.Errorf("status HTTP %d", resp.StatusCode)
	}
	err = json.NewDecoder(io.LimitReader(resp.Body, node.MaxBody)).Decode(&result)
	if err == nil && !result.Success {
		err = fmt.Errorf("status query failed")
	}
	return result.Obj, err
}

type statusDashboard struct {
	status         serverStatus
	updated        time.Time
	failed, paused bool
	cpu, memory    []float64
	oomBaseline    *uint64
}

func (d *statusDashboard) accept(s serverStatus, err error, now time.Time) {
	d.failed = err != nil
	if err != nil {
		return
	}
	d.status, d.updated = s, now
	if m := s.MemoryDetail; m != nil && m.OOMKills != nil {
		if d.oomBaseline == nil || *m.OOMKills < *d.oomBaseline {
			v := *m.OOMKills
			d.oomBaseline = &v
		}
	}
	d.cpu = append(d.cpu, s.CPU)
	mem := 0.0
	if s.Mem.Total > 0 {
		mem = 100 * float64(s.Mem.Current) / float64(s.Mem.Total)
	}
	if m := s.MemoryDetail; m != nil && m.ProbeUsed != nil && m.Limit > 0 {
		mem = 100 * float64(*m.ProbeUsed) / float64(m.Limit)
	}
	d.memory = append(d.memory, mem)
	if len(d.cpu) > 30 {
		d.cpu = d.cpu[1:]
		d.memory = d.memory[1:]
	}
}

func percentBar(value float64, width int) string {
	value = math.Max(0, math.Min(100, value))
	if math.IsNaN(value) {
		value = 0
	}
	filled := int(math.Round(value * float64(width) / 100))
	return strings.Repeat("━", filled) + strings.Repeat("─", width-filled)
}

func percentTrend(values []float64, width int) string {
	if len(values) > width {
		values = values[len(values)-width:]
	}
	runes := []rune("▁▂▃▄▅▆▇█")
	var b strings.Builder
	b.WriteString(strings.Repeat("·", width-len(values)))
	for _, v := range values {
		if math.IsNaN(v) {
			v = 0
		}
		b.WriteRune(runes[int(math.Round(math.Max(0, math.Min(100, v))*7/100))])
	}
	return b.String()
}

func dashboardPad(s string, width int) string {
	s = terminalClip(s, width)
	return s + strings.Repeat(" ", max(0, width-terminalDisplayWidth(s)))
}

func (d *statusDashboard) lines(width int, busy bool, now time.Time) []string {
	block := min(64, max(18, width-8))
	mode := "LIVE · 2 秒刷新"
	if d.paused {
		mode = "PAUSED · 已暂停"
	} else if busy {
		mode = []string{"◐", "◓", "◑", "◒"}[now.UnixMilli()/250%4] + " 正在更新"
	}
	if d.failed {
		mode = "连接失败 · 保留上次数据"
	}
	lines := []string{mode, ""}
	if d.updated.IsZero() {
		rows := 30
		if block < 56 {
			rows = 39
		}
		if d.failed {
			lines = append(lines, "无法读取节点状态", "按 r 重试，或 Esc 返回后运行系统体检")
		} else {
			lines = append(lines, "正在建立本地安全连接…", "CPU / 内存 / 网络数据将在采样后显示")
		}
		for len(lines) < rows {
			lines = append(lines, "")
		}
		lines[rows-1] = strings.Repeat(" ", block)
		return lines
	}
	s := d.status
	state := "● Xray 运行中"
	if s.Xray.State != "running" {
		state = "! Xray 未运行"
	}
	lines = append(lines, state+" · "+safeTerminalText(s.Xray.Version), "运行时间  "+(time.Duration(s.Uptime)*time.Second).Round(time.Second).String(), "")
	column := block
	wide := block >= 56
	if wide {
		column = (block - 4) / 2
	}
	mem := 0.0
	memValue := "不可用"
	if s.Mem.Total > 0 {
		mem = 100 * float64(s.Mem.Current) / float64(s.Mem.Total)
		memValue = fmt.Sprintf("%.1f%%", mem)
	}
	cpu := []string{"CPU · 系统采样", fmt.Sprintf("%.1f%%", s.CPU), percentBar(s.CPU, column), percentTrend(d.cpu, column), "固定刻度 0–100%"}
	memory := []string{"内存 · 容器/系统", memValue, percentBar(mem, column), percentTrend(d.memory, column), bytes(s.Mem.Current) + " / " + bytes(s.Mem.Total)}
	if m := s.MemoryDetail; m != nil && m.ProbeUsed != nil && m.Limit > 0 {
		mem = 100 * float64(*m.ProbeUsed) / float64(m.Limit)
		memory = []string{"内存 · 探针口径", fmt.Sprintf("%.1f%%", mem), percentBar(mem, column), percentTrend(d.memory, column), bytes(*m.ProbeUsed) + " / " + bytes(m.Limit)}
	} else {
		memory[0] = "内存 · 总占用含缓存"
	}
	pair := func(left, right []string) {
		if wide {
			for i := range left {
				lines = append(lines, dashboardPad(left[i], column)+"    "+dashboardPad(right[i], column))
			}
		} else {
			lines = append(lines, left...)
			lines = append(lines, "")
			lines = append(lines, right...)
		}
	}
	pair(cpu, memory)
	lines = append(lines, memoryDetailLines(s.MemoryDetail, d.oomBaseline)...)
	lines = append(lines, "", strings.Repeat("─", block))
	pair([]string{"↑ 发送速率", bytes(s.NetIO.Up) + "/s"}, []string{"↓ 接收速率", bytes(s.NetIO.Down) + "/s"})
	lines = append(lines, "", fmt.Sprintf("更新 %s · %d 秒前", d.updated.Format("15:04:05"), max(0, int(now.Sub(d.updated).Seconds()))), "趋势：本页最近 30 次采样；非累计流量")
	lines = append(lines, "")
	lines = append(lines, managementLines(s.ManagementActivity, now)...)
	if d.failed {
		lines = append(lines, "数据已过期；检查服务或按 r 重试")
	}
	return lines
}

func memoryDetailLines(m *node.MemoryDetail, baseline *uint64) []string {
	if m == nil {
		return []string{"", "内存细分不可用；总占用不等于程序用量", "", "", "", ""}
	}
	value := func(v *uint64) string {
		if v == nil {
			return "不可用"
		}
		return bytes(*v)
	}
	pressure := "不可用"
	if m.PressureSome10 != nil {
		pressure = fmt.Sprintf("%.2f%%", *m.PressureSome10)
	}
	oom := "不可用"
	if m.OOMKills != nil {
		oom = fmt.Sprintf("历史 %d", *m.OOMKills)
		if baseline != nil && *m.OOMKills >= *baseline {
			oom += fmt.Sprintf(" · 本页新增 %d", *m.OOMKills-*baseline)
		}
	}
	probe := "不可用"
	if m.ProbeUsed != nil {
		probe = bytes(*m.ProbeUsed) + "（总内存−MemAvailable）"
	}
	return []string{"", "探针口径 " + probe, "总占用 " + bytes(m.Current) + " / " + bytes(m.Limit) + "（含缓存）", "文件缓存 " + value(m.File) + " · 匿名 " + value(m.Anon), "Swap " + value(m.Swap) + " · 内存等待10秒均值 " + pressure, "OOM 杀进程：" + oom}
}

func managementLines(activity *node.ManagementActivity, now time.Time) []string {
	lines := []string{"主面板接入 · 远程管理反馈"}
	if activity == nil {
		return append(lines, "节点程序未提供接入记录；需更新服务进程", "", "", "", "共享 Token 无法证明主面板身份或唯一绑定")
	}
	age := func(stamp int64) string {
		return fmt.Sprintf("%s · %d 秒前", time.Unix(stamp, 0).Format("01-02 15:04:05"), max(0, int(now.Unix()-stamp)))
	}
	if activity.LastRequest == 0 {
		lines = append(lines, "本次启动尚未观察到远程认证请求", "请求来源  —", "配置下发  尚无成功记录", "下发来源  —")
	} else {
		lines = append(lines, "最近请求  "+age(activity.LastRequest), "请求来源  "+safeTerminalText(activity.RequestPeer))
		if activity.LastConfig == 0 {
			lines = append(lines, "配置下发  尚无成功记录", "下发来源  —")
		} else {
			lines = append(lines, "配置下发  "+age(activity.LastConfig), "下发来源  "+safeTerminalText(activity.ConfigPeer))
		}
	}
	return append(lines, "本次启动记录；来源为直连 IP，非绑定证明")
}

// Update only changed terminal rows; avoid a full clear on every data tick.
type terminalFrameWriter struct {
	out      io.Writer
	pending  strings.Builder
	previous []string
}

func (w *terminalFrameWriter) Write(p []byte) (int, error) { return w.pending.Write(p) }
func (w *terminalFrameWriter) flush() {
	frame := strings.TrimPrefix(w.pending.String(), "\x1b[H\x1b[2J")
	w.pending.Reset()
	rows := strings.Split(strings.TrimSuffix(frame, "\r\n"), "\r\n")
	if w.previous == nil {
		fmt.Fprint(w.out, "\x1b[H\x1b[2J")
	}
	for i := 0; i < max(len(rows), len(w.previous)); i++ {
		row := ""
		if i < len(rows) {
			row = rows[i]
		}
		if i >= len(w.previous) || w.previous[i] != row {
			fmt.Fprintf(w.out, "\x1b[%d;1H\x1b[2K%s", i+1, row)
		}
	}
	w.previous = rows
}
