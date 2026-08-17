// 本文件是 CLI 提问序列的回归金样：把「打印了什么、问什么、按什么顺序」逐行
// 锁进 testdata/golden_askall_*.txt。
//
// 职责：
//   - 在 initflow 改造前录制当前 AskAll 的完整提问/输出序列，作为改造唯一回归防线
//   - -update 重录；不带 -update 时逐字节比对金样
//
// 边界：
//   - **不探测工具链**：探测结果由 goldenFixture 写死，金样不随开发机环境漂移
//   - **不改生产代码**：只消费 AskAll 的现有签名，与 prompter.go 的 Prompter 接口
//   - 只锁提问序列，不校验返回的 (isExec, role)——那由现有测试覆盖
package initflow

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/toolchain"
)

var update = flag.Bool("update", false, "重录金样文件")

// recordingPrompter 把每一问的类型、标题、默认值、选项记进共享日志，
// 答案默认取 def；force 可按标题强制某个答案，用来走遍各角色分支。
type recordingPrompter struct {
	force map[string]string
	log   *[]string
}

func (p *recordingPrompter) answer(title, def string) string {
	if v, ok := p.force[title]; ok {
		return v
	}
	return def
}

func (p *recordingPrompter) Select(title string, options []Option, def string) (string, error) {
	vals := make([]string, len(options))
	for i, o := range options {
		vals[i] = o.Value + "=" + o.Label
	}
	*p.log = append(*p.log, fmt.Sprintf("select|%s|def=%s|opts=%s", title, def, strings.Join(vals, ";")))
	return p.answer(title, def), nil
}

func (p *recordingPrompter) Input(title, def string) (string, error) {
	*p.log = append(*p.log, fmt.Sprintf("input|%s|def=%s", title, def))
	return p.answer(title, def), nil
}

func (p *recordingPrompter) Confirm(title string, def bool) (bool, error) {
	*p.log = append(*p.log, fmt.Sprintf("confirm|%s|def=%v", title, def))
	return def, nil
}

// recordingWriter 把 AskAll 写进 io.Writer 的产品输出记进同一条日志，
// 从而把「打印」与「提问」的**真实交错顺序**一并锁住。
type recordingWriter struct{ log *[]string }

func (w recordingWriter) Write(b []byte) (int, error) {
	for _, line := range strings.Split(string(b), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			*w.log = append(*w.log, "out|"+s)
		}
	}
	return len(b), nil
}

// goldenFixture 造一份确定的输入：固定的 cfg 与固定的探测结果。
// 探测结果写死而不调 toolchain.Detect()，否则金样会随开发机装了什么而变。
func goldenFixture() (*config.Config, []toolchain.Result) {
	cfg := &config.Config{
		Listen:  "127.0.0.1:7777",
		DataDir: "/tmp/handoff",
		Targets: map[string]config.Target{},
	}
	rs := []toolchain.Result{
		{Name: "opencode", Path: "/usr/bin/opencode", State: toolchain.StateReady},
		{Name: "claude", Path: "/usr/bin/claude", State: toolchain.StateAuthUnknown},
		{Name: "grok", State: toolchain.StateMissing},
		{Name: "codex", Path: "/usr/bin/codex", State: toolchain.StateNoCreds},
	}
	return cfg, rs
}

// TestAskAllGolden 锁住 CLI 的提问文本、顺序、默认值与选项集合。
//
// 用 -update 重录：go test ./internal/initflow/ -run TestAskAllGolden -update
// **重录前必须确认差异是有意的**——这个测试的全部价值就在于它会挡住
// 无意的行为漂移。
func TestAskAllGolden(t *testing.T) {
	roles := map[string]string{
		"coordinator": RoleCoordinator,
		"executor":    RoleExecutor,
		"both":        RoleBoth,
	}
	for name, role := range roles {
		t.Run(name, func(t *testing.T) {
			var log []string
			cfg, rs := goldenFixture()
			p := &recordingPrompter{force: map[string]string{"这台机器的角色": role}, log: &log}
			if _, _, err := AskAll(recordingWriter{log: &log}, p, cfg, rs, false); err != nil {
				t.Fatalf("AskAll 不该出错: %v", err)
			}
			got := strings.Join(log, "\n") + "\n"
			path := filepath.Join("testdata", "golden_askall_"+name+".txt")
			if *update {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("读金样失败（先用 -update 录一次）: %v", err)
			}
			if got != string(want) {
				t.Fatalf("提问序列与金样不一致\n--- 期望 ---\n%s\n--- 实际 ---\n%s", want, got)
			}
		})
	}
}
