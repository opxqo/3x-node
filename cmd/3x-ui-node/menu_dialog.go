package main

import (
	stdbytes "bytes"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

type serviceDialog struct {
	action, label       string
	confirm             bool
	running             bool
	finished            bool
	result              serviceDialogResult
	info                []string
	offset, view, total int
}

type serviceDialogResult struct {
	output string
	err    error
}

// Modal input is consumed before menu input. Only an explicit focused Enter
// starts the action; cancellation is the default on every fresh dialog.
func (d *serviceDialog) key(key byte) (execute, dismiss bool) {
	if d.info != nil {
		switch key {
		case 'j':
			d.offset = min(max(0, d.total-d.view), d.offset+1)
		case 'k':
			d.offset = max(0, d.offset-1)
		case 'g':
			d.offset = 0
		case 'G':
			d.offset = max(0, d.total-d.view)
		}
		return false, key == 27 || key == 'q' || key == '\r' || key == '\n'
	}
	if d.running {
		return false, false
	}
	if d.finished {
		return false, key == 27 || key == 'q' || key == '\r' || key == '\n'
	}
	switch key {
	case 27, 'q':
		return false, true
	case 'h':
		d.confirm = false
	case 'l':
		d.confirm = true
	case '\t', 'j', 'k':
		d.confirm = !d.confirm
	case '\r', '\n':
		if !d.confirm {
			return false, true
		}
		d.running = true
		return true, false
	}
	return false, false
}

var terminalSGR = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// Slice by display cells, replacing the half of a wide glyph at a boundary
// with a space. This keeps the overlay aligned over Chinese background text.
func terminalCellSlice(s string, start, width int) string {
	var b strings.Builder
	position, used := 0, 0
	for _, r := range safeTerminalText(s) {
		w := terminalRuneWidth(r)
		if position >= start && position+w <= start+width {
			b.WriteRune(r)
			used += w
		} else if position < start+width && position+w > start {
			n := min(position+w, start+width) - max(position, start)
			b.WriteString(strings.Repeat(" ", n))
			used += n
		}
		position += w
		if position >= start+width {
			break
		}
	}
	return b.String() + strings.Repeat(" ", max(0, width-used))
}

func (d *serviceDialog) rows(width int, now time.Time) []string {
	inner := width - 4
	content := func(text string) string {
		return "│ " + dashboardPad(text, inner) + " │"
	}
	heading := "确认" + d.label
	message := "当前节点连接将中断。"
	detail := "确认后执行" + d.label + "，配置与数据保留。"
	if d.action == "update" {
		message = "从 GitHub 下载并升级到最新版本。"
		detail = "升级会短暂中断服务，确认继续？"
	}
	hint := "← → / Tab 选择 · Enter 确认 · Esc 取消"
	buttons := ""
	button := func(text string, active bool) string {
		if active {
			return "\x1b[7m" + text + "\x1b[0m"
		}
		return text
	}
	if d.running {
		heading = "正在" + d.label
		message = []string{"◐", "◓", "◑", "◒"}[now.UnixMilli()/250%4] + " 正在执行，请稍候…"
		detail = "执行期间不会重复提交操作。"
		if d.action == "update" {
			detail = "正在下载安装脚本并升级，请保持终端打开。"
		}
		hint = "请等待执行结果"
	} else if d.finished {
		heading = d.label + " · 已完成"
		message = d.label + "命令已成功执行。"
		detail = "可返回服务状态查看运行情况。"
		if d.result.err != nil {
			heading = d.label + " · 未完成"
			message = "命令执行失败。"
			detail = "关闭弹窗后查看完整错误信息。"
		}
		hint = "Enter / Esc 关闭"
		buttons = button("  返回  ", true)
	} else {
		buttons = button("  取消  ", !d.confirm) + "    " + button("  确认"+d.label+"  ", d.confirm)
	}
	buttonWidth := terminalDisplayWidth(terminalSGR.ReplaceAllString(buttons, ""))
	left := max(0, (inner-buttonWidth)/2)
	buttonRow := "│ " + strings.Repeat(" ", left) + buttons + strings.Repeat(" ", max(0, inner-left-buttonWidth)) + " │"
	return []string{
		"╭" + strings.Repeat("─", width-2) + "╮",
		content(heading), content(""), content(message), content(detail), content(""),
		buttonRow, content(""), content(hint),
		"╰" + strings.Repeat("─", width-2) + "╯",
	}
}

func (d *serviceDialog) infoRows(width, height int) []string {
	inner := width - 4
	var body []string
	for _, line := range d.info {
		line = safeTerminalText(line)
		for terminalDisplayWidth(line) > inner {
			part := terminalClip(line, inner)
			body = append(body, part)
			line = strings.TrimPrefix(line, part)
		}
		body = append(body, line)
	}
	d.total, d.view = len(body), max(1, height-7)
	d.offset = min(max(0, d.offset), max(0, d.total-d.view))
	content := func(s string) string { return "│ " + dashboardPad(s, inner) + " │" }
	rows := []string{"╭" + strings.Repeat("─", width-2) + "╮", content(d.label), content("")}
	for _, line := range body[d.offset:min(d.total, d.offset+d.view)] {
		rows = append(rows, content(line))
	}
	hint := "Enter / Esc 关闭"
	if d.total > d.view {
		hint = fmt.Sprintf("↑↓ 滚动 %d/%d · Esc 关闭", d.offset+1, d.total)
	}
	return append(rows, content(""), content(hint), "╰"+strings.Repeat("─", width-2)+"╯")
}

func renderServiceDialog(out io.Writer, width, height, selected int, d *serviceDialog, now time.Time) {
	// A compact size gate keeps all confirmation controls visible and blocks
	// hidden-button confirmation in a terminal too small for the dialog.
	if width < 44 || height < 12 {
		fmt.Fprint(out, "\x1b[H\x1b[2J", terminalClip("请扩大终端至 44×12 · Esc 取消", max(0, width-1)), "\r\n")
		return
	}
	var base stdbytes.Buffer
	renderTerminalMenu(&base, width, height, selected, 0, nil, "")
	background := strings.Split(strings.TrimPrefix(base.String(), "\x1b[H\x1b[2J"), "\r\n")
	boxWidth := min(56, width-4)
	rows := d.rows(boxWidth, now)
	if d.info != nil {
		boxWidth = min(88, width-4)
		rows = d.infoRows(boxWidth, height-1)
	}
	x, y := (width-1-boxWidth)/2, (height-1-len(rows))/2
	fmt.Fprint(out, "\x1b[H\x1b[2J")
	for i := 0; i < height-1; i++ {
		line := ""
		if i < len(background) {
			line = terminalSGR.ReplaceAllString(background[i], "")
		}
		if i >= y && i < y+len(rows) {
			fmt.Fprint(out, "\x1b[2m", terminalCellSlice(line, 0, x), "\x1b[0m", rows[i-y])
			fmt.Fprint(out, "\x1b[2m", "░", terminalCellSlice(line, x+boxWidth+1, width-2-x-boxWidth), "\x1b[0m")
		} else if i == y+len(rows) {
			fmt.Fprint(out, "\x1b[2m", terminalCellSlice(line, 0, x+1), strings.Repeat("░", boxWidth), terminalCellSlice(line, x+1+boxWidth, width-2-x-boxWidth), "\x1b[0m")
		} else {
			fmt.Fprint(out, "\x1b[2m", terminalCellSlice(line, 0, width-1), "\x1b[0m")
		}
		fmt.Fprint(out, "\r\n")
	}
}
