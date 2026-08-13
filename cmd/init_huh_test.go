// huh 选择控件的渲染契约：预选项不是第一项时，上面的选项仍必须看得见。
//
// 真机上 huh v1 会把 viewport.YOffset 直接设成 selected，三项里预选第二项
// 时「执行机」被卷出视口，必须再按一次上键。本文件钉住这个回归。
package cmd

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// 预选第二项时，三项必须同时出现在 View 里。v1 的 YOffset=selected 会裁掉第一项。
func TestHuhSelectShowsAllOptionsWhenDefaultIsNotFirst(t *testing.T) {
	view := renderHuhSelect(t, "这台机器的角色", []promptOption{
		{Value: "executor", Label: "执行机"},
		{Value: "coordinator", Label: "协调者"},
		{Value: "both", Label: "两者"},
	}, "coordinator")

	plain := ansi.Strip(view)
	for _, label := range []string{"执行机", "协调者", "两者"} {
		if !strings.Contains(plain, label) {
			t.Fatalf("预选第二项时仍应看见 %q，得到:\n%s", label, plain)
		}
	}
}

// renderHuhSelect 走与生产 Select 相同的构造，再取 View。不 Run，避免要真终端。
func renderHuhSelect(t *testing.T, title string, options []promptOption, def string) string {
	t.Helper()
	value := def
	sel := newHuhSelect(title, options, &value)
	sel.WithWidth(80)
	return sel.View()
}
