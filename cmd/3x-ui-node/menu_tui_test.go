package main

import (
	stdbytes "bytes"
	"fmt"
	"strings"
	"testing"
)

var sgr = strings.NewReplacer("\x1b[1;38;2;12;245;184m", "", "\x1b[2m", "", "\x1b[7m", "", "\x1b[0m", "")

func renderRows(t *testing.T, width, height, selected, offset int, lines []string, title string) []string {
	t.Helper()
	var b stdbytes.Buffer
	renderTerminalMenu(&b, width, height, selected, offset, lines, title)
	body, ok := strings.CutPrefix(b.String(), "\x1b[H\x1b[2J")
	if !ok {
		t.Fatalf("frame does not start by clearing the screen: %q", b.String())
	}
	rows := strings.Split(body, "\r\n")
	return rows[:len(rows)-1]
}

func TestTerminalMenuFallback(t *testing.T) {
	if terminalMenuAvailable(strings.NewReader("0\n"), &stdbytes.Buffer{}) {
		t.Fatal("non-terminal input must use plain menu")
	}
}

func TestTerminalMenuFitsEveryViewport(t *testing.T) {
	sizes := [][2]int{{80, 24}, {120, 50}, {60, 14}, {40, 10}, {24, 6}}
	for _, size := range sizes {
		width, height := size[0], size[1]
		for _, selected := range []int{0, len(menuEntries) / 2, len(menuEntries) - 1} {
			t.Run(fmt.Sprintf("%dx%d/%d", width, height, selected), func(t *testing.T) {
				rows := renderRows(t, width, height, selected, 0, nil, "")
				if len(rows) > height {
					t.Fatalf("%d rows rendered in a %d-row terminal", len(rows), height)
				}
				plain := sgr.Replace(strings.Join(rows, "\n"))
				if !strings.Contains(plain, menuEntries[selected].title) {
					t.Fatalf("selected entry %q scrolled out of view:\n%s", menuEntries[selected].title, plain)
				}
				for _, row := range rows {
					if w := terminalDisplayWidth(sgr.Replace(row)); w > width {
						t.Fatalf("row width %d exceeds terminal width %d: %q", w, width, row)
					}
				}
			})
		}
	}
}

func TestSelectionHighlightCoversOnlyTheContentBlock(t *testing.T) {
	rows := renderRows(t, 80, 24, 0, 0, nil, "")
	var highlighted, rule string
	for _, row := range rows {
		if before, after, ok := strings.Cut(row, "\x1b[7m"); ok {
			if strings.TrimLeft(before, " ") != "" {
				t.Fatalf("highlight starts after non-blank text %q", before)
			}
			highlighted, _, _ = strings.Cut(after, "\x1b[0m")
		}
		if strings.Contains(row, "─") && rule == "" {
			rule = sgr.Replace(row)
		}
	}
	if highlighted == "" {
		t.Fatal("no selected row rendered")
	}
	if !strings.HasPrefix(highlighted, "  ▸ ") {
		t.Fatalf("highlight bleeds into the left margin: %q", highlighted)
	}
	if got, want := terminalDisplayWidth(highlighted), terminalDisplayWidth(strings.TrimLeft(rule, " ")); got != want {
		t.Fatalf("highlight width %d does not match the content block width %d", got, want)
	}
}

func TestTerminalMenuCentersContentBlock(t *testing.T) {
	rows := renderRows(t, 100, 40, 0, 0, nil, "")
	for _, row := range rows {
		plain := sgr.Replace(row)
		if !strings.Contains(plain, menuEntries[0].title) {
			continue
		}
		left := len(plain) - len(strings.TrimLeft(plain, " "))
		right := 99 - terminalDisplayWidth(plain)
		if left < 20 || left-right > 2 {
			t.Fatalf("item row is not centered: left=%d right=%d %q", left, right, plain)
		}
		return
	}
	t.Fatal("menu items not rendered")
}

func TestResultScrollReachesTheLastLine(t *testing.T) {
	lines := make([]string, 80)
	for i := range lines {
		lines[i] = fmt.Sprintf("log-%02d", i)
	}
	width, height := 80, 24
	last := len(lines) - resultViewport(max(20, width-1), height, len(lines))
	for _, offset := range []int{last, last + 30} {
		plain := sgr.Replace(strings.Join(renderRows(t, width, height, 0, offset, lines, "运行日志"), "\n"))
		if !strings.Contains(plain, "log-79") {
			t.Fatalf("offset %d cannot reach the last line:\n%s", offset, plain)
		}
	}
	plain := sgr.Replace(strings.Join(renderRows(t, width, height, 0, 0, lines, "运行日志"), "\n"))
	if !strings.Contains(plain, "log-00") || strings.Contains(plain, "log-79") {
		t.Fatalf("first page is wrong:\n%s", plain)
	}
}

func TestWordmarkRowsAlign(t *testing.T) {
	want := terminalDisplayWidth(wordmark[0])
	for i, row := range wordmark {
		if got := terminalDisplayWidth(row); got != want {
			t.Fatalf("wordmark row %d is %d columns, row 0 is %d", i, got, want)
		}
	}
	plain := sgr.Replace(strings.Join(renderRows(t, 100, 40, 0, 0, nil, ""), "\n"))
	if !strings.Contains(plain, wordmark[0]) {
		t.Fatalf("wordmark missing on a roomy terminal:\n%s", plain)
	}
}

// The footer hint changes with the selected entry, but the content block
// (group dividers, item indent) must not resize or the whole menu jiggles
// left/right as the cursor moves.
func TestFooterHintAlignsWithoutShiftingTheContentBlock(t *testing.T) {
	width, height := 100, 32
	blockWidth := -1
	for i, entry := range menuEntries {
		rows := renderRows(t, width, height, i, 0, nil, "")
		dividerIndent, dividerWidth := -1, -1
		hintIndent, hintLine := -1, ""
		for _, row := range rows {
			plain := sgr.Replace(row)
			if dividerIndent < 0 && strings.Contains(plain, "─") {
				trimmed := strings.TrimLeft(plain, " ")
				dividerIndent = len(plain) - len(trimmed)
				dividerWidth = terminalDisplayWidth(trimmed)
			}
			if strings.Contains(plain, entry.hint) {
				trimmed := strings.TrimLeft(plain, " ")
				hintIndent = len(plain) - len(trimmed)
				hintLine = strings.TrimRight(trimmed, " ")
			}
		}
		if dividerIndent < 0 {
			t.Fatalf("entry %d: no group divider rendered", i)
		}
		if hintLine != entry.hint {
			t.Fatalf("entry %d: hint truncated or missing: got %q want %q", i, hintLine, entry.hint)
		}
		if hintIndent != dividerIndent {
			t.Fatalf("entry %d: hint indent %d does not match content block indent %d", i, hintIndent, dividerIndent)
		}
		if blockWidth == -1 {
			blockWidth = dividerWidth
		} else if dividerWidth != blockWidth {
			t.Fatalf("entry %d: content block width changed from %d to %d as the selection moved", i, blockWidth, dividerWidth)
		}
	}
}

func TestTerminalTextCannotInjectControls(t *testing.T) {
	s := terminalClip("name\x1b[2J\r\x07中文", 80)
	if strings.ContainsAny(s, "\x1b\r\x07") {
		t.Fatal("untrusted text contains terminal controls")
	}
	if got := terminalClip("中文abc", 5); got != "中文a" {
		t.Fatalf("clip = %q", got)
	}
}
