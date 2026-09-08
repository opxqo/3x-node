package main

import (
	stdbytes "bytes"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestServiceDialogRequiresExplicitConfirmation(t *testing.T) {
	for _, key := range []byte{'\r', '\n', 27, 'q'} {
		d := &serviceDialog{}
		if execute, dismiss := d.key(key); execute || !dismiss {
			t.Fatalf("default key %d did not cancel", key)
		}
	}
	d := &serviceDialog{}
	d.key('l')
	d.key('l')
	if execute, dismiss := d.key('\r'); !execute || dismiss || !d.running {
		t.Fatal("focused confirmation did not start action")
	}
	for _, key := range []byte{'\r', 'l', '\t', 27, 'q'} {
		if execute, dismiss := d.key(key); execute || dismiss {
			t.Fatal("running dialog permits duplicate action or dismissal")
		}
	}
	d.running, d.finished = false, true
	if execute, dismiss := d.key('\r'); execute || !dismiss {
		t.Fatal("completed dialog does not close")
	}
}

func TestServiceDialogFitsAndRetainsBackground(t *testing.T) {
	for _, size := range [][2]int{{120, 30}, {80, 24}, {44, 12}, {30, 8}} {
		for _, state := range []string{"confirm", "running", "success", "error"} {
			d := &serviceDialog{action: "restart", label: "重启服务", running: state == "running", finished: state == "success" || state == "error"}
			if state == "error" {
				d.result.err = fmt.Errorf("service failed")
			}
			var b stdbytes.Buffer
			renderServiceDialog(&b, size[0], size[1], 15, d, time.Now())
			plain := terminalSGR.ReplaceAllString(strings.TrimPrefix(b.String(), "\x1b[H\x1b[2J"), "")
			rows := strings.Split(strings.TrimSuffix(plain, "\r\n"), "\r\n")
			if len(rows) >= size[1] {
				t.Fatalf("%v/%s scrolls terminal", size, state)
			}
			for _, row := range rows {
				if terminalDisplayWidth(row) >= size[0] {
					t.Fatalf("%v/%s wraps row: %q", size, state, row)
				}
			}
			if size[0] >= 44 && !strings.Contains(plain, "╭") {
				t.Fatal("dialog missing")
			}
			if size[0] == 120 && !strings.Contains(plain, "卸载节点") {
				t.Fatal("background menu missing")
			}
		}
	}
}

func TestTerminalCellSlicePreservesOverlayColumns(t *testing.T) {
	for start := 0; start < 12; start++ {
		for width := 1; width < 12; width++ {
			if got := terminalDisplayWidth(terminalCellSlice("中文测试 abc", start, width)); got != width {
				t.Fatalf("start=%d width=%d got=%d", start, width, got)
			}
		}
	}
}

func TestCredentialDialogWrapsWithoutLosingData(t *testing.T) {
	token := strings.Repeat("sample-token-", 16)
	for _, size := range [][2]int{{44, 12}, {80, 24}, {120, 30}} {
		d := &serviceDialog{label: "连接凭据", info: []string{"API token: " + token, "TLS SHA256: sample-fingerprint"}}
		var seen strings.Builder
		for {
			rows := d.infoRows(min(88, size[0]-4), size[1]-1)
			if len(rows) > size[1]-2 {
				t.Fatal("dialog too tall")
			}
			for _, row := range rows {
				if terminalDisplayWidth(row) > size[0]-4 {
					t.Fatal("dialog too wide")
				}
			}
			for _, row := range rows[3 : len(rows)-3] {
				seen.WriteString(strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(row, "│ "), " │")))
			}
			if d.offset+d.view >= d.total {
				break
			}
			d.offset += d.view
		}
		if !strings.Contains(seen.String(), token) {
			t.Fatal("token was clipped")
		}
		if execute, dismiss := d.key('\r'); execute || !dismiss {
			t.Fatal("information dialog must only close")
		}
	}
}

func TestUpdateDialogExplainsUpgradeAndDefaultsToCancel(t *testing.T) {
	d := &serviceDialog{action: "update", label: "更新节点"}
	text := strings.Join(d.rows(56, time.Now()), "\n")
	if !strings.Contains(text, "下载并升级") || !strings.Contains(text, "中断服务") {
		t.Fatal("update effects missing")
	}
	if execute, dismiss := d.key('\r'); execute || !dismiss {
		t.Fatal("update must default to cancellation")
	}
}
