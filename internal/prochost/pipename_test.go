package prochost

import (
	"strings"
	"testing"
)

// TestPipeNameForIsDeterministic 钉住确定性：这是 agentd 与 shim 不共享额外状态
// 就能算出同一个名字的前提，proc.json 与三个 adapter 的零改动全靠它。
func TestPipeNameForIsDeterministic(t *testing.T) {
	const p = `C:\Users\u\.handoff\tasks\3e70fd90-98a4-42b0-be02-c22a357e0ed4\in.fifo`
	a, b := pipeNameFor(p), pipeNameFor(p)
	if a != b {
		t.Fatalf("同一路径推导出两个名字: %q vs %q", a, b)
	}
}

// TestPipeNameForDistinctPerTask 钉住不同任务不撞名——撞名意味着两个任务的
// 执行者共用一根 stdin，指令会投递到错误的模型上。
func TestPipeNameForDistinctPerTask(t *testing.T) {
	a := pipeNameFor(`C:\t\aaaaaaaa-0000-0000-0000-000000000000\in.fifo`)
	b := pipeNameFor(`C:\t\bbbbbbbb-0000-0000-0000-000000000000\in.fifo`)
	if a == b {
		t.Fatalf("不同任务推导出同一个管道名: %q", a)
	}
}

// TestPipeNameForShape 钉住形态与长度：Windows 管道名上限 256 字符，
// 而任务目录路径可以很长——名字必须与输入长度无关。
func TestPipeNameForShape(t *testing.T) {
	long := `C:\` + strings.Repeat("verydeep\\", 30) + `in.fifo`
	name := pipeNameFor(long)
	if !strings.HasPrefix(name, `\\.\pipe\handoff-`) {
		t.Fatalf("管道名前缀不对: %q", name)
	}
	if len(name) > 256 {
		t.Fatalf("管道名 %d 字符，超过 Windows 上限 256: %q", len(name), name)
	}
	hexPart := strings.TrimPrefix(name, `\\.\pipe\handoff-`)
	if len(hexPart) != 16 {
		t.Fatalf("哈希段 %d 字符，想要 16: %q", len(hexPart), hexPart)
	}
	for _, r := range hexPart {
		if !strings.ContainsRune("0123456789abcdef", r) {
			t.Fatalf("哈希段含非十六进制字符 %q: %q", r, hexPart)
		}
	}
}

// TestPipeNameForNormalizesSeparators 钉住路径归一：同一个位置的两种写法
// 必须推出同一个名字，否则 agentd 与 shim 会各算各的。
func TestPipeNameForNormalizesSeparators(t *testing.T) {
	a := pipeNameFor(`C:\t\x\in.fifo`)
	b := pipeNameFor(`C:\t\y\..\x\in.fifo`)
	if a != b {
		t.Fatalf("等价路径推出不同名字: %q vs %q", a, b)
	}
}
