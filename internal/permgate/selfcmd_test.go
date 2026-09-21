package permgate

import "testing"

func TestIsSelfCommand(t *testing.T) {
	cases := []struct {
		name string
		in   string
		hit  bool
		sub  string
	}{
		// 真调用：三种可执行文件形态
		{"裸调用", "handoff dispatch plan.md", true, "dispatch"},
		{"相对路径", "./handoff run T1 ls", true, "run"},
		{"绝对路径", "/usr/local/bin/handoff done T1", true, "done"},
		{"Windows 后缀", `C:\bin\handoff.exe stop T1`, true, "stop"},

		// flag 插在中间：flag 与它的值都不得干扰候选判定
		{"持久 flag 后接变更命令", "handoff --agentd http://x:1 dispatch plan.md", true, "dispatch"},
		{"持久 flag 后接只读命令", "handoff --agentd http://x:1 tasks", false, ""},

		// 自己批自己的工单
		{"自批工单", "handoff reply T1 --ticket X --approve", true, "reply"},

		// 白名单放行；已删除的 graph 入口必须 fail-closed
		{"tasks", "handoff tasks", false, ""},
		{"show", "handoff show T1", false, ""},
		{"diff 带 flag", "handoff diff T1 --base main", false, ""},
		{"graph resolve 已移除", "handoff graph resolve --doc docs/spec.md", true, "graph"},
		{"graph 未知子命令 fail-closed", "handoff graph inspect --doc docs/spec.md", true, "graph"},

		// 切段：管道后的词元不参与本段判定
		{"管道隔段", "handoff tasks | grep done", false, ""},
		{"与号隔段", "cd handoff && make", false, ""},

		// 变更词优先于白名单词
		{"白名单词塞进变更命令参数", "handoff run T1 handoff show", true, "run"},

		// 安全默认：两个名单都不认识的子命令一律拦
		{"未知子命令", "handoff foo", true, "foo"},

		// 候选为空 → 不命中
		{"纯 flag", "handoff --help", false, ""},
		{"裸二进制名", "handoff", false, ""},
		{"cd 到同名目录", "cd ~/handoff", false, ""},
		{"删同名目录", "rm -rf handoff", false, ""},

		// basename 不是 handoff → 不定位
		{"同名前缀路径", "go test ./handoff/...", false, ""},
		{"同名日志文件", "cat handoff.log", false, ""},

		// 前缀不得被当成白名单词
		{"showoff 不是 show", "handoff showoff", true, "showoff"},

		// B383 第 3 项之 (c)：判据收紧为「handoff 处于命令位置才认」之后，
		// echo 参数位上的 handoff 不再误伤（改前这里是「已知代价」= true）。
		{"echo 参数位不再误伤", "echo handoff dispatch", false, ""},

		// 完全无关
		{"无关命令", "go test ./...", false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hit, sub := IsSelfCommand(c.in)
			if hit != c.hit || sub != c.sub {
				t.Fatalf("IsSelfCommand(%q) = (%v, %q)，期望 (%v, %q)",
					c.in, hit, sub, c.hit, c.sub)
			}
		})
	}
}

// TestIsSelfCommandCommandPosition 是 B383 第 3 项之 (c) 的回归表。
//
// 背景：旧判据是「任何 basename 为 handoff 的词元，其后跟着非 flag 词元就
// 当候选；两个名单都不认识就拦」。它把**普通参数位**上的 handoff（搜索词、
// 构建输出名、源路径）也当成变更子命令，评审实测出三条误报。
//
// 新判据把识别面收到「命令位置」：段首词元，或执行包装器之后的词元。本表
// 分两组断言：
//   - 误报回归：普通参数位上的 handoff。改前为红，改后必须放行。
//   - 安全底线：段首 handoff 与包装器之后的 handoff。改前改后都必须命中，
//     收紧识别面不得把它们一起放过。
func TestIsSelfCommandCommandPosition(t *testing.T) {
	const (
		falsePositive = "误报回归"
		safetyFloor   = "安全底线"
	)
	cases := []struct {
		name     string
		in       string
		hit      bool
		sub      string
		kind     string
		tokenize bool // 先经 selfCmdTokenText 恢复被引号粘连的词元（包装器路径）
	}{
		// —— 误报回归（改前为红）：三条实测误报，handoff 全在普通参数位 ——
		// 段首分别是 rg / grep / go，handoff 是搜索词 / 输出名 / 源路径。
		{"rg 把 handoff 当搜索词", `rg handoff internal/`, false, "", falsePositive, false},
		{"grep 把 handoff 当搜索词", `grep -rn handoff docs/`, false, "", falsePositive, false},
		{"go build -o 输出名与源路径含 handoff", `go build -o ./handoff ./cmd/handoff`, false, "", falsePositive, false},
		// 现状已放行，回归表一并钉住不得回退。
		{"grep 搜引号里的 handoff 包路径", `grep -rn "import \"github.com/Xsxdot/handoff\"" internal/`, false, "", falsePositive, false},

		// —— 安全底线（改前改后都必须命中）——
		{"段首 dispatch", `handoff dispatch T1`, true, "dispatch", safetyFloor, false},
		{"段首 run", `handoff run T1`, true, "run", safetyFloor, false},
		{"段首 done", `handoff done T1`, true, "done", safetyFloor, false},
		{"段首未知子命令 graph（安全默认拦）", `handoff graph X`, true, "graph", safetyFloor, false},
		{"sh -c 包装器里的 handoff", `sh -c "handoff dispatch"`, true, "dispatch", safetyFloor, true},
		{"xargs 包装器后的 handoff", `xargs handoff dispatch`, true, "dispatch", safetyFloor, false},
		{"分号后段首的 handoff", `; handoff run T1`, true, "run", safetyFloor, false},
	}
	for _, c := range cases {
		t.Run(c.kind+"/"+c.name, func(t *testing.T) {
			in := c.in
			if c.tokenize {
				in = selfCmdTokenText(in)
			}
			hit, sub := IsSelfCommand(in)
			if hit != c.hit || sub != c.sub {
				t.Fatalf("IsSelfCommand(%q) = (%v, %q)，期望 (%v, %q)",
					in, hit, sub, c.hit, c.sub)
			}
		})
	}
}
