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

		// 白名单放行
		{"tasks", "handoff tasks", false, ""},
		{"show", "handoff show T1", false, ""},
		{"diff 带 flag", "handoff diff T1 --base main", false, ""},
		{"graph resolve 只读", "handoff graph resolve --doc docs/spec.md", false, ""},

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

		// 已知误伤：echo 之后的词元同样进候选。代价只是一次人工点击，
		// 不值得为它引入命令语义解析。钉住它是为了让这个取舍显式可见
		{"echo 误伤（已知代价）", "echo handoff dispatch", true, "dispatch"},

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
