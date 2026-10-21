// b452_skilled_test.go —— B452 S1/S2：熟练工定义的卡派发拒发守卫（V1）与无卡渲染（V2）。
package ledgerstep

import (
	"context"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
)

// TestB452RenderCardlessPrompt：正文三占位符替换为空串，输出 = 正文 + 空行 + 「## 本次任务」+ 空行 + 任务文本。
func TestB452RenderCardlessPrompt(t *testing.T) {
	body := "卡 {{CARD}}《{{TITLE}}》判据：{{ACCEPT}}。照规矩查。"
	got := RenderCardlessPrompt(body, "查 Foo 的调用方")
	want := "卡 《》判据：。照规矩查。\n\n## 本次任务\n\n查 Foo 的调用方"
	if got != want {
		t.Fatalf("无卡渲染:\n got %q\nwant %q", got, want)
	}
}

// TestB452RenderTemplatePlaceholders：卡派发与无卡渲染共用同一个替换函数。
func TestB452RenderTemplatePlaceholders(t *testing.T) {
	got := RenderTemplatePlaceholders("{{CARD}}/{{TITLE}}/{{ACCEPT}}", "B1", "标题", "判据")
	if got != "B1/标题/判据" {
		t.Fatalf("占位符替换 = %q", got)
	}
}

// TestB452ViaTemplateRejectsSkilledDef：卡派发遇熟练工定义（receiver / mode 非空、或 target=here）拒发，
// 错误点名「handoff dispatch --template」，且不调用 Transport。
func TestB452ViaTemplateRejectsSkilledDef(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*ledger.TemplateDef)
	}{
		{"receiver", func(d *ledger.TemplateDef) { d.Receiver = "mimo" }},
		{"mode", func(d *ledger.TemplateDef) { d.Mode = ledger.TemplateModeReadonly }},
		{"target=here", func(d *ledger.TemplateDef) { d.Target = ledger.TemplateTargetHere }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st, card := dispatchTestCard(t)
			tpl, err := st.GetTemplate("feature-impl", 0)
			if err != nil {
				t.Fatalf("取模板: %v", err)
			}
			c.mut(&tpl.Def)
			if _, err := st.PutTemplate("feature-impl", tpl.Def); err != nil {
				t.Fatalf("写熟练工定义: %v", err)
			}
			called := false
			d := &Dispatcher{St: st, Actor: "tester",
				Transport: func(ctx context.Context, opts DispatchOpts) (string, string, error) {
					called = true
					return "T-b452", "", nil
				}}
			_, err = d.ViaTemplate(context.Background(), card, TemplateDispatch{Template: "feature-impl"})
			if err == nil || !strings.Contains(err.Error(), "handoff dispatch --template") ||
				!strings.Contains(err.Error(), "feature-impl") {
				t.Fatalf("应拒发并点名 handoff dispatch --template 与模板名，得 %v", err)
			}
			if called {
				t.Fatal("拒发不得调用 Transport")
			}
		})
	}
}

// TestB452PreflightRejectsSkilledDef：卡派发的前置解析（CLI card dispatch 与 agentd 卡节点共用）同样拒
// 熟练工定义——否则 target=here 会先流进调用方的目标机解析，报出与本守卫无关的错。
func TestB452PreflightRejectsSkilledDef(t *testing.T) {
	st, _ := dispatchTestCard(t)
	tpl, err := st.GetTemplate("feature-impl", 0)
	if err != nil {
		t.Fatalf("取模板: %v", err)
	}
	tpl.Def.Target = ledger.TemplateTargetHere
	if _, err := st.PutTemplate("feature-impl", tpl.Def); err != nil {
		t.Fatalf("写熟练工定义: %v", err)
	}
	_, target, err := PreflightDiscipline(st, "feature-impl", "", "")
	if err == nil || !strings.Contains(err.Error(), "handoff dispatch --template") {
		t.Fatalf("前置解析应拒熟练工定义，得 target=%q err=%v", target, err)
	}
}
