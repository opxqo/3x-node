package main

import (
	"bufio"
	stdbytes "bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/opxqo/3x-node/v3/internal/node"
	"golang.org/x/term"
)

type menuEntry struct {
	id, group, title, hint string
	interactive            bool
}

var menuEntries = []menuEntry{
	{id: "1", group: "概览", title: "服务状态", hint: "查看 CPU、内存与 Xray 状态"},
	{id: "2", group: "概览", title: "入站列表", hint: "Enter 查看单个入站的完整配置"},
	{id: "3", group: "概览", title: "客户端与流量", hint: "查看客户端及上传下载统计"},
	{id: "4", group: "诊断", title: "监听端口", hint: "检查本机管理与业务监听"},
	{id: "5", group: "诊断", title: "Xray 错误", hint: "查看核心错误信息"},
	{id: "10", group: "诊断", title: "运行日志", hint: "查看最近 80 行日志"},
	{id: "15", group: "诊断", title: "系统体检", hint: "检查节点环境、配置、服务与资源"},
	{id: "16", group: "诊断", title: "体检与修复", hint: "逐项确认权限修复，记录原权限并复查", interactive: true},
	{id: "6", group: "配置", title: "连接凭据", hint: "查看节点 Token 与证书指纹"},
	{id: "11", group: "配置", title: "默认客户端", hint: "查看默认客户端配置"},
	{id: "12", group: "配置", title: "设置默认客户端", hint: "保存默认客户端，重启后生效", interactive: true},
	{id: "13", group: "配置", title: "添加客户端", hint: "交互填写客户端并确认添加", interactive: true},
	{id: "14", group: "配置", title: "删除客户端", hint: "按名称删除，执行前确认", interactive: true},
	{id: "7", group: "服务", title: "启动服务", hint: "启动节点服务"},
	{id: "8", group: "服务", title: "停止服务", hint: "会中断节点连接，执行前确认", interactive: true},
	{id: "9", group: "服务", title: "重启服务", hint: "会中断节点连接，执行前确认", interactive: true},
	{id: "17", group: "服务", title: "检查更新", hint: "下载并升级到最新版本，执行前确认", interactive: true},
	{id: "18", group: "服务", title: "卸载节点", hint: "停止服务并删除全部数据，不可恢复", interactive: true},
}

func widestMenuHint() int {
	width := 0
	for _, e := range menuEntries {
		width = max(width, terminalDisplayWidth(e.hint))
	}
	return width
}

var wordmark = []string{
	"██████╗ ██╗  ██╗   ███╗   ██╗ ██████╗ ██████╗ ███████╗",
	"╚════██╗╚██╗██╔╝   ████╗  ██║██╔═══██╗██╔══██╗██╔════╝",
	" █████╔╝ ╚███╔╝    ██╔██╗ ██║██║   ██║██║  ██║█████╗  ",
	" ╚═══██╗ ██╔██╗    ██║╚██╗██║██║   ██║██║  ██║██╔══╝  ",
	"██████╔╝██╔╝ ██╗   ██║ ╚████║╚██████╔╝██████╔╝███████╗",
	"╚═════╝ ╚═╝  ╚═╝   ╚═╝  ╚═══╝ ╚═════╝ ╚═════╝ ╚══════╝",
}

const (
	menuMinBlock = 34
	menuFooter   = 3
)

func terminalMenuAvailable(in io.Reader, out io.Writer) bool {
	i, iok := in.(*os.File)
	o, ook := out.(*os.File)
	return iok && ook && os.Getenv("TERM") != "dumb" && os.Getenv("TERM") != "" && term.IsTerminal(int(i.Fd())) && term.IsTerminal(int(o.Fd()))
}

// Remove terminal controls from API and log text before rendering it.
func safeTerminalText(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' {
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

func terminalRuneWidth(r rune) int {
	if unicode.Is(unicode.Mn, r) || r == 0 {
		return 0
	}
	if r < 0x1100 {
		return 1
	}
	if r <= 0x115f || r == 0x2329 || r == 0x232a ||
		(r >= 0x2e80 && r <= 0x303e) ||
		(r >= 0x3040 && r <= 0xa4cf) ||
		(r >= 0xac00 && r <= 0xd7a3) ||
		(r >= 0xf900 && r <= 0xfaff) ||
		(r >= 0xfe10 && r <= 0xfe6f) ||
		(r >= 0xff00 && r <= 0xff60) ||
		(r >= 0xffe0 && r <= 0xffe6) ||
		(r >= 0x1f300 && r <= 0x1faff) {
		return 2
	}
	return 1
}

func terminalClip(s string, width int) string {
	var b strings.Builder
	for _, r := range safeTerminalText(s) {
		n := terminalRuneWidth(r)
		if width < n {
			break
		}
		width -= n
		b.WriteRune(r)
	}
	return b.String()
}

func terminalDisplayWidth(s string) int {
	width := 0
	for _, r := range safeTerminalText(s) {
		width += terminalRuneWidth(r)
	}
	return width
}

type rowKind uint8

const (
	kindBlank rowKind = iota
	kindLogo
	kindCaption
	kindGroup
	kindItem
	kindSelected
	kindText
	kindAccent
	kindHint
)

type terminalRow struct {
	text string
	kind rowKind
}

func (r terminalRow) sequence() string {
	switch r.kind {
	case kindLogo, kindAccent:
		return "\x1b[1;38;2;12;245;184m"
	case kindCaption, kindGroup, kindHint:
		return "\x1b[2m"
	case kindSelected:
		return "\x1b[7m"
	}
	return ""
}

// Centered rows follow the screen, block rows follow the shared content column.
func (r terminalRow) centered() bool {
	return r.kind == kindLogo || r.kind == kindCaption
}

func logoTiers(width int) [][]terminalRow {
	caption := terminalRow{text: "节点管理 · " + node.Version, kind: kindCaption}
	tiers := make([][]terminalRow, 0, 4)
	if width >= terminalDisplayWidth(wordmark[0])+2 {
		rows := make([]terminalRow, 0, len(wordmark)+1)
		for _, line := range wordmark {
			rows = append(rows, terminalRow{text: line, kind: kindLogo})
		}
		tiers = append(tiers, append(rows, caption))
	}
	tiers = append(tiers, []terminalRow{{text: "3 X   N O D E", kind: kindLogo}, caption})
	return append(tiers, []terminalRow{{text: "3X NODE · " + node.Version, kind: kindLogo}})
}

// The logo shrinks before the content does, so a short terminal keeps every row.
func terminalLayout(width, height, want int) ([]terminalRow, int) {
	tiers := logoTiers(width)
	for _, tier := range tiers {
		if height-len(tier)-1-menuFooter >= want {
			return tier, want
		}
	}
	// Nothing fits: keep the one-line banner and scroll, unless the terminal is tiny.
	fallback := tiers[len(tiers)-1]
	if height < 10 {
		fallback = nil
	}
	return fallback, max(1, height-len(fallback)-1-menuFooter)
}

type menuRow struct {
	text  string
	entry int
}

// Group headers are rows without an entry, so navigation skips them for free.
func menuRows() []menuRow {
	rows := make([]menuRow, 0, len(menuEntries)+4)
	group := ""
	for i, e := range menuEntries {
		if e.group != group {
			group = e.group
			rows = append(rows, menuRow{text: e.group, entry: -1})
		}
		rows = append(rows, menuRow{text: e.title, entry: i})
	}
	return rows
}

func menuRowOf(selected int) int {
	for i, r := range menuRows() {
		if r.entry == selected {
			return i
		}
	}
	return 0
}

// Both the renderer and the key handler size the result viewport here.
func resultViewport(width, height, lines int) int {
	_, capacity := terminalLayout(width, height, lines+1)
	return max(1, capacity-1)
}

func resultRows(width, height, offset int, lines []string, title string) []terminalRow {
	view := resultViewport(width, height, len(lines))
	start := 0
	if len(lines) > view {
		start = min(max(0, offset), len(lines)-view)
	}
	end := min(len(lines), start+view)
	head := title
	if len(lines) > view {
		head = fmt.Sprintf("%s  %d-%d/%d", title, start+1, end, len(lines))
	}
	rows := []terminalRow{{text: head, kind: kindGroup}}
	for _, s := range lines[start:end] {
		kind := kindText
		if strings.HasPrefix(s, inboundCursor) {
			kind = kindSelected
		}
		rows = append(rows, terminalRow{text: "  " + s, kind: kind})
	}
	return rows
}

func menuBodyRows(width, height, selected int) []terminalRow {
	all := menuRows()
	_, capacity := terminalLayout(width, height, len(all))
	start := 0
	if len(all) > capacity {
		start = min(max(0, menuRowOf(selected)-capacity/2), len(all)-capacity)
	}
	rows := make([]terminalRow, 0, capacity)
	for _, r := range all[start:min(len(all), start+capacity)] {
		switch {
		case r.entry < 0:
			rows = append(rows, terminalRow{text: r.text, kind: kindGroup})
		case r.entry == selected:
			rows = append(rows, terminalRow{text: "  ▸ " + r.text, kind: kindSelected})
		default:
			rows = append(rows, terminalRow{text: "    " + r.text, kind: kindItem})
		}
	}
	return rows
}

func renderTerminalMenu(out io.Writer, width, height, selected, offset int, lines []string, title string) {
	width = max(20, width-1)
	height = max(6, height)
	selected = min(max(0, selected), len(menuEntries)-1)

	var body []terminalRow
	var footer []terminalRow
	if lines != nil {
		body = resultRows(width, height, offset, lines, title)
		footer = []terminalRow{{}, {text: "↑↓ 滚动 · g/G 首尾 · Esc 返回", kind: kindHint}}
		if title == "入站列表" {
			footer = []terminalRow{{}, {text: "↑↓ 选择 · Enter 查看详情 · Esc 返回", kind: kindHint}}
		}
		if title == "实时服务状态" {
			for i := range body {
				if strings.ContainsAny(body[i].text, "━▁▂▃▄▅▆▇█") {
					body[i].kind = kindAccent
				}
			}
			footer = []terminalRow{{}, {text: "p 暂停/继续 · r 刷新 · ↑↓ 滚动 · Esc 返回", kind: kindHint}}
		}
	} else {
		body = menuBodyRows(width, height, selected)
		footer = []terminalRow{
			{},
			{text: menuEntries[selected].hint, kind: kindHint},
			{text: "↑↓ 选择 · Enter 打开 · q 退出", kind: kindHint},
		}
	}

	logo, _ := terminalLayout(width, height, len(body))
	rows := make([]terminalRow, 0, len(logo)+len(body)+len(footer)+1)
	rows = append(rows, logo...)
	if len(logo) > 0 {
		rows = append(rows, terminalRow{})
	}
	rows = append(rows, body...)
	rows = append(rows, footer...)

	// Sized against every hint, not just the selected one, so the block
	// (and the group rules drawn to its width below) hold still as the
	// cursor moves instead of resizing with whichever hint is showing.
	block := menuMinBlock
	if lines == nil {
		block = max(block, widestMenuHint()+2)
	}
	for _, r := range rows {
		if !r.centered() {
			block = max(block, terminalDisplayWidth(r.text)+2)
		}
	}
	block = min(block, width)
	for i, r := range rows {
		if r.kind == kindGroup {
			label := r.text + " "
			rows[i].text = label + strings.Repeat("─", max(0, block-terminalDisplayWidth(label)))
		}
	}

	blockPad := max(0, (width-block)/2)
	fmt.Fprint(out, "\x1b[H\x1b[2J")
	for i := 0; i < max(0, (height-len(rows))/2); i++ {
		fmt.Fprint(out, "\r\n")
	}
	for _, r := range rows {
		text, pad := terminalClip(r.text, block), blockPad
		if r.centered() {
			text = terminalClip(r.text, width)
			pad = max(0, (width-terminalDisplayWidth(text))/2)
		}
		if r.kind == kindSelected {
			text += strings.Repeat(" ", max(0, block-terminalDisplayWidth(text)))
		}
		fmt.Fprint(out, strings.Repeat(" ", pad))
		if seq := r.sequence(); seq != "" {
			fmt.Fprint(out, seq, text, "\x1b[0m")
		} else {
			fmt.Fprint(out, text)
		}
		fmt.Fprint(out, "\r\n")
	}
}

// One input owner is shared by navigation and the existing canonical-mode forms.
type terminalInput struct {
	keys    <-chan byte
	signals <-chan os.Signal
}

func (r terminalInput) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	select {
	case b, ok := <-r.keys:
		if !ok {
			return 0, io.EOF
		}
		p[0] = b
		return 1, nil
	case <-r.signals:
		return 0, io.EOF
	}
}

func runTerminalMenu(configPath string, c node.Config, in io.Reader, out io.Writer) error {
	input := in.(*os.File)
	output := out.(*os.File)
	fd := int(input.Fd())
	original, err := term.MakeRaw(fd)
	if err != nil {
		return err
	}
	defer term.Restore(fd, original)
	fmt.Fprint(out, "\x1b[?1049h\x1b[?25l")
	defer fmt.Fprint(out, "\x1b[0m\x1b[?25h\x1b[?1049l")
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigs)
	keys := make(chan byte, 64)
	done := make(chan struct{})
	defer close(done)
	go func() {
		defer close(keys)
		var b [1]byte
		for {
			n, e := input.Read(b[:])
			if n > 0 {
				select {
				case keys <- b[0]:
				case <-done:
					return
				}
			}
			if e != nil {
				return
			}
		}
	}()
	reader := bufio.NewReader(terminalInput{keys, sigs})
	selected, offset := 0, 0
	var lines []string
	title := ""
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	width, height := 0, 0
	var dashboard *statusDashboard
	var browser *inboundBrowser
	var dialog *serviceDialog
	serviceResults := make(chan serviceDialogResult, 1)
	var cancelStatus context.CancelFunc
	defer func() {
		if cancelStatus != nil {
			cancelStatus()
		}
	}()
	updates := make(chan statusUpdate, 1)
	generation := 0
	busy := false
	var nextUpdate time.Time
	requestStatus := func() {
		if dashboard == nil || busy {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		cancelStatus = cancel
		busy = true
		g, config := generation, c
		go func() {
			defer cancel()
			s, err := fetchDashboardStatus(ctx, config)
			select {
			case updates <- statusUpdate{generation: g, status: s, err: err}:
			case <-done:
			}
		}()
	}
	frame := &terminalFrameWriter{out: out}
	draw := func() {
		oldWidth, oldHeight := width, height
		width, height, _ = term.GetSize(int(output.Fd()))
		if width == 0 {
			width, height = 80, 24
		}
		if width != oldWidth || height != oldHeight {
			frame.previous = nil
		}
		if dashboard != nil {
			lines = dashboard.lines(width, busy, time.Now())
		}
		if dialog != nil {
			renderServiceDialog(frame, width, height, selected, dialog, time.Now())
		} else {
			renderTerminalMenu(frame, width, height, selected, offset, lines, title)
		}
		frame.flush()
	}
	draw()
	for {
		var key byte
		select {
		case result := <-serviceResults:
			if dialog != nil {
				dialog.result = result
				dialog.running, dialog.finished = false, true
				if dialog.action == "update" {
					dialog.info = []string{"升级命令执行完成。"}
					if result.err != nil {
						dialog.info = []string{"升级失败: " + result.err.Error()}
					}
					dialog.info = append(dialog.info, strings.Split(strings.TrimSpace(result.output), "\n")...)
				}
				draw()
			}
			continue
		case update := <-updates:
			if dashboard != nil && update.generation == generation {
				busy = false
				dashboard.accept(update.status, update.err, time.Now())
				nextUpdate = time.Now().Add(2 * time.Second)
				draw()
			}
			continue
		case <-sigs:
			return nil
		case <-ticker.C:
			if dialog != nil && dialog.running {
				draw()
				continue
			}
			if dashboard != nil {
				if !dashboard.paused && !busy && !time.Now().Before(nextUpdate) {
					requestStatus()
				}
				draw()
				continue
			}
			w, h, _ := term.GetSize(int(output.Fd()))
			if w != width || h != height {
				draw()
			}
			continue
		case b, ok := <-keys:
			if !ok {
				return nil
			}
			key = b
		}
		if key == 27 {
			// A standalone Escape returns immediately after a short sequence timeout.
			timer := time.NewTimer(60 * time.Millisecond)
			select {
			case next := <-keys:
				if next == '[' || next == 'O' {
					select {
					case next = <-keys:
						switch next {
						case 'A':
							key = 'k'
						case 'B':
							key = 'j'
						case 'C':
							key = 'l'
						case 'D':
							key = 'h'
						case '5':
							key = 'b'
						case '6':
							key = ' '
						default:
							key = 0
						}
					case <-timer.C:
						key = 27
					}
				}
			case <-timer.C:
			}
			timer.Stop()
		}
		if key == 3 || key == 4 {
			return nil
		}
		if dialog != nil {
			if (width < 44 || height < 12) && key != 27 && key != 'q' {
				continue
			}
			execute, dismiss := dialog.key(key)
			if dismiss {
				if dialog.finished && dialog.result.err != nil && dialog.info == nil {
					title = dialog.label
					lines = strings.Split(strings.TrimSpace(dialog.result.output+"\n操作失败: "+dialog.result.err.Error()), "\n")
					offset = 0
				}
				dialog = nil
			} else if execute {
				action := dialog.action
				go func() {
					var output stdbytes.Buffer
					var err error
					if action == "update" {
						err = performNodeUpdate(&output)
					} else {
						err = serviceAction(action, &output)
					}
					select {
					case serviceResults <- serviceDialogResult{output.String(), err}:
					case <-done:
					}
				}()
			}
			draw()
			continue
		}
		if lines != nil {
			page := resultViewport(max(20, width-1), max(6, height), len(lines))
			last := max(0, len(lines)-page)
			if browser != nil && browser.key(key) {
				title, lines = browser.title(), browser.lines()
				offset = browser.follow(page)
				draw()
				continue
			}
			switch key {
			case 'q', 27, '\r', '\n':
				if cancelStatus != nil {
					cancelStatus()
				}
				dashboard = nil
				browser = nil
				generation++
				busy = false
				lines = nil
				offset = 0
			case 'j':
				offset = min(last, offset+1)
			case 'k':
				offset = max(0, offset-1)
			case ' ':
				offset = min(last, offset+page)
			case 'b':
				offset = max(0, offset-page)
			case 'g':
				offset = 0
			case 'G':
				offset = last
			case 'p':
				if dashboard != nil {
					dashboard.paused = !dashboard.paused
				}
			case 'r':
				requestStatus()
			}
			draw()
			continue
		}
		switch key {
		case 'q':
			return nil
		case 'k':
			selected = (selected + len(menuEntries) - 1) % len(menuEntries)
		case 'j', '\t':
			selected = (selected + 1) % len(menuEntries)
		case '\r', '\n':
			entry := menuEntries[selected]
			title = entry.title
			// Always load current config; editing defaults must not leave stale values.
			fresh, e := node.LoadConfig(configPath)
			if e != nil {
				lines = []string{"读取配置失败: " + e.Error()}
				draw()
				continue
			}
			c = fresh
			if entry.id == "2" {
				b, e := newInboundBrowser(c)
				if e != nil {
					lines = []string{"读取入站失败: " + e.Error()}
					draw()
					continue
				}
				browser = b
				title, lines, offset = browser.title(), browser.lines(), 0
				draw()
				continue
			}
			if entry.id == "1" {
				dashboard = &statusDashboard{}
				title = "实时服务状态"
				generation++
				requestStatus()
				draw()
				continue
			}
			if entry.id == "6" {
				var output stdbytes.Buffer
				if err := menuAction(entry.id, configPath, c, reader, &output); err != nil {
					fmt.Fprintf(&output, "读取凭据失败: %v", err)
				}
				dialog = &serviceDialog{label: entry.title, info: strings.Split(strings.TrimSpace(output.String()), "\n")}
				draw()
				continue
			}
			if entry.id == "17" {
				dialog = &serviceDialog{action: "update", label: "更新节点"}
				draw()
				continue
			}
			if entry.id == "8" || entry.id == "9" {
				action := "restart"
				if entry.id == "8" {
					action = "stop"
				}
				dialog = &serviceDialog{action: action, label: entry.title}
				draw()
				continue
			}
			if entry.interactive {
				frame.previous = nil
				term.Restore(fd, original)
				fmt.Fprint(out, "\x1b[H\x1b[2J\x1b[?25h")
				fmt.Fprintln(out, "3X NODE / "+entry.title)
				fmt.Fprintln(out)
				err = menuAction(entry.id, configPath, c, reader, out)
				if err != nil {
					if errors.Is(err, io.EOF) {
						return nil
					}
					fmt.Fprintf(out, "操作失败: %v\n", err)
				}
				if _, e := prompt(reader, out, "按 Enter 返回菜单", ""); e != nil {
					return nil
				}
				if _, e := term.MakeRaw(fd); e != nil {
					return e
				}
				fmt.Fprint(out, "\x1b[?25l")
			} else {
				frame.previous = nil
				var b stdbytes.Buffer
				fmt.Fprint(out, "\x1b[H\x1b[2J正在读取…\r\n")
				if e := menuAction(entry.id, configPath, c, reader, &b); e != nil {
					fmt.Fprintf(&b, "操作失败: %v\n", e)
				}
				lines = strings.Split(strings.TrimSpace(b.String()), "\n")
				offset = 0
			}
		}
		draw()
	}
}
