// b439_s6_test.go —— B439 S6：模板声明的 target 是旧别名（local/本机/hostname）
// 且本机已配自名时，ViaTemplate 前置拒发并点名模板；未配自名/非别名/省略放行。
package ledgerstep

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// s6RejectAlias 是组装点 config.LegacyLocalAliasError 在测试里的同构：旧别名返回
// 可行动错误，其余（含省略空串）放行。
func s6RejectAlias(target string) error {
	switch target {
	case "local", "本机", "host":
		return errors.New(`机器名 "` + target + `" 是本机旧别名（local/本机/hostname），不再按本机解释；请改成 config.yaml 的 name: 值 "linux-01"`)
	default:
		return nil
	}
}

func TestB439S6TemplateTargetLegacyAliasGate(t *testing.T) {
	cases := []struct {
		name    string
		target  string
		reject  func(string) error
		wantErr bool
	}{
		{"有自名 模板 target=local 拒", "local", s6RejectAlias, true},
		{"有自名 模板 target=本机 拒", "本机", s6RejectAlias, true},
		{"有自名 模板 target=host 拒", "host", s6RejectAlias, true},
		{"有自名 模板 target 省略放行", "", s6RejectAlias, false},
		{"有自名 模板 target 远端放行", "mac-02", s6RejectAlias, false},
		{"无自名 模板 target=local 放行(S5)", "local", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st, card := dispatchTestCard(t)
			tpl, err := st.GetTemplate("feature-impl", 0)
			if err != nil {
				t.Fatalf("取模板: %v", err)
			}
			tpl.Def.Target = c.target
			if _, err := st.PutTemplate("feature-impl", tpl.Def); err != nil {
				t.Fatalf("写带 target 的模板: %v", err)
			}
			called := false
			d := &Dispatcher{St: st, Actor: "tester", RejectLegacyAlias: c.reject,
				Transport: func(ctx context.Context, opts DispatchOpts) (string, string, error) {
					called = true
					return "T-s6", "", nil
				}}
			_, err = d.ViaTemplate(context.Background(), card, TemplateDispatch{Template: "feature-impl"})
			if !c.wantErr {
				if err != nil {
					t.Fatalf("不应拒发: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("模板旧别名 target 应拒发")
			}
			if !strings.Contains(err.Error(), "feature-impl") || !strings.Contains(err.Error(), c.target) {
				t.Fatalf("拒发文案应点名模板与别名，实得: %v", err)
			}
			if called {
				t.Fatal("拒发不得调用 Transport")
			}
		})
	}
}
