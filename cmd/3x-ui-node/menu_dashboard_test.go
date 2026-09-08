package main

import (
	stdbytes "bytes"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestDashboardHistoryFailureAndBounds(t *testing.T) {
	d := &statusDashboard{}
	var s serverStatus
	s.CPU = 37
	s.Mem.Current = 72 << 20
	s.Mem.Total = 96 << 20
	s.Xray.State = "running"
	s.NetIO.Up = 2048
	now := time.Now()
	for i := 0; i < 40; i++ {
		d.accept(s, nil, now)
	}
	if len(d.cpu) != 30 || len(d.memory) != 30 {
		t.Fatal("unbounded history")
	}
	d.accept(serverStatus{}, fmt.Errorf("failure"), now.Add(time.Second))
	if d.status.CPU != 37 || !d.updated.Equal(now) {
		t.Fatal("failed query replaced good sample")
	}
	for _, size := range [][2]int{{120, 50}, {80, 24}, {40, 10}, {24, 6}} {
		lines := d.lines(size[0], false, now)
		rows := renderRows(t, size[0], size[1], 0, 0, lines, "实时服务状态")
		if len(rows) > size[1] {
			t.Fatal("vertical overflow")
		}
		for _, row := range rows {
			if terminalDisplayWidth(sgr.Replace(row)) > size[0] {
				t.Fatalf("horizontal overflow: %q", row)
			}
		}
	}
	text := strings.Join(d.lines(100, false, now), "\n")
	if !strings.Contains(text, "2.00 KiB/s") || !strings.Contains(text, "数据已过期") {
		t.Fatal(text)
	}
}

func TestTerminalFrameUpdatesWithoutRepeatedClear(t *testing.T) {
	var out stdbytes.Buffer
	w := &terminalFrameWriter{out: &out}
	fmt.Fprint(w, "\x1b[H\x1b[2Ja\r\nb\r\n")
	w.flush()
	out.Reset()
	fmt.Fprint(w, "\x1b[H\x1b[2Ja\r\nc\r\n")
	w.flush()
	if strings.Contains(out.String(), "\x1b[2J") || out.String() != "\x1b[2;1H\x1b[2Kc" {
		t.Fatalf("unexpected delta %q", out.String())
	}
}
