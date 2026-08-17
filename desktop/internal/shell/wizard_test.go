package shell_test

import (
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/desktop/internal/shell"
	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/initflow"
)

// TestBuildFormHasNoTerminalLanguage 钉住「桌面表单不带终端语言」：
// 字段表由 initflow.Form 直接产出，CLI 侧面向终端的措辞（如「回车」）
// 出现在 GUI 里就是文案错误，必须被测试拦截。
func TestBuildFormHasNoTerminalLanguage(t *testing.T) {
	cfg := &config.Config{Listen: "127.0.0.1:7777", Targets: map[string]config.Target{}}
	for _, f := range shell.BuildForm(cfg, nil, "darwin") {
		if strings.Contains(f.Title, "回车") || strings.Contains(f.Notice, "回车") {
			t.Fatalf("字段 %s 含终端语言：%q / %q", f.Key, f.Title, f.Notice)
		}
	}
}

// TestApplyAnswersRejectsBadValue 钉住承重行为：越界答案必须被拒。
// ApplyAnswers 返回错误时调用方绝不落盘——半截答案落盘会造出一份让
// shell.Resolve 判为「已配置」的文件，向导从此再也不会出现（W5b-2 缺陷 A）。
func TestApplyAnswersRejectsBadValue(t *testing.T) {
	cfg := &config.Config{Listen: "127.0.0.1:7777", Targets: map[string]config.Target{}}
	fields := shell.BuildForm(cfg, nil, "darwin")
	if err := shell.ApplyAnswers(cfg, fields, map[string]string{
		"role": initflow.RoleBoth, "executor_default": "不存在",
	}); err == nil {
		t.Fatal("越界答案必须被拒，否则半截配置会落盘")
	}
}
