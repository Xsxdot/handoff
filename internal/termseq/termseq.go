// Package termseq 从 PTY 字节里抽出跟 TUI 鼠标/焦点有关的 CSI。
//
// 职责：把 stdin（模拟器 → 应用）和 stdout（应用 → 模拟器）里的
// 1000/1004/1006/1049 与 [I]/[O]/滚轮 SGR 编成短标签，给 [DEBUG-b270] 用。
// 边界：不写 PTY、不改字节；普通按键和清屏一类 CSI 一律忽略，避免把日志打爆。
package termseq

import (
	"fmt"
	"strconv"
	"strings"
)

var watchedModes = map[int]struct{}{
	1000: {},
	1002: {},
	1003: {},
	1004: {},
	1006: {},
	1049: {},
	2004: {},
}

// SummarizeIn 分类终端模拟器送给应用的字节（用户按键 / 焦点 / 鼠标）。
func SummarizeIn(p []byte) []string {
	var out []string
	wheelUp, wheelDown := 0, 0
	flush := func() {
		if wheelUp > 0 {
			out = append(out, fmt.Sprintf("wheel-up×%d", wheelUp))
			wheelUp = 0
		}
		if wheelDown > 0 {
			out = append(out, fmt.Sprintf("wheel-down×%d", wheelDown))
			wheelDown = 0
		}
	}
	i := 0
	for i < len(p) {
		if p[i] != 0x1b {
			i++
			continue
		}
		if i+2 < len(p) && p[i+1] == '[' && p[i+2] == 'O' {
			flush()
			out = append(out, "focus-out")
			i += 3
			continue
		}
		if i+2 < len(p) && p[i+1] == '[' && p[i+2] == 'I' {
			flush()
			out = append(out, "focus-in")
			i += 3
			continue
		}
		if btn, n, ok := sgrMouse(p[i:]); ok {
			if btn == 64 {
				wheelUp++
			} else if btn == 65 {
				wheelDown++
			} else {
				flush()
				out = append(out, fmt.Sprintf("sgr-btn=%d", btn))
			}
			i += n
			continue
		}
		i++
	}
	flush()
	return out
}

// SummarizeOut 分类应用（TUI）写给模拟器的 DECSET/DECRST。
func SummarizeOut(p []byte) []string {
	var out []string
	i := 0
	for i < len(p) {
		if p[i] != 0x1b {
			i++
			continue
		}
		modes, hl, n, ok := decPrivate(p[i:])
		if !ok {
			i++
			continue
		}
		for _, m := range modes {
			if _, watch := watchedModes[m]; watch {
				out = append(out, strconv.Itoa(m)+string(hl))
			}
		}
		i += n
	}
	return out
}

func sgrMouse(p []byte) (btn int, n int, ok bool) {
	// ESC [ < btn ; col ; row M|m
	if len(p) < 8 || p[0] != 0x1b || p[1] != '[' || p[2] != '<' {
		return 0, 0, false
	}
	j := 3
	btn, j, ok = readInt(p, j)
	if !ok || j >= len(p) || p[j] != ';' {
		return 0, 0, false
	}
	j++
	_, j, ok = readInt(p, j)
	if !ok || j >= len(p) || p[j] != ';' {
		return 0, 0, false
	}
	j++
	_, j, ok = readInt(p, j)
	if !ok || j >= len(p) || (p[j] != 'M' && p[j] != 'm') {
		return 0, 0, false
	}
	return btn, j + 1, true
}

func decPrivate(p []byte) (modes []int, hl byte, n int, ok bool) {
	// ESC [ ? 1000 ; 1006 h|l
	if len(p) < 5 || p[0] != 0x1b || p[1] != '[' || p[2] != '?' {
		return nil, 0, 0, false
	}
	j := 3
	for j < len(p) {
		var v int
		v, j, ok = readInt(p, j)
		if !ok {
			return nil, 0, 0, false
		}
		modes = append(modes, v)
		if j >= len(p) {
			return nil, 0, 0, false
		}
		if p[j] == ';' {
			j++
			continue
		}
		if p[j] == 'h' || p[j] == 'l' {
			return modes, p[j], j + 1, true
		}
		return nil, 0, 0, false
	}
	return nil, 0, 0, false
}

func readInt(p []byte, i int) (int, int, bool) {
	if i >= len(p) || p[i] < '0' || p[i] > '9' {
		return 0, i, false
	}
	n := 0
	for i < len(p) && p[i] >= '0' && p[i] <= '9' {
		n = n*10 + int(p[i]-'0')
		i++
	}
	return n, i, true
}

// Join 把标签收成一条日志字段。空输入返回空串。
func Join(notes []string) string {
	return strings.Join(notes, " ")
}
