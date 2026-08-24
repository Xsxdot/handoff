// dispatch_test.go —— B229 缝 1 的单元判据：三态边界表、组装顺序、拒发语义。
// 账本依赖以 fake lookup 注入（本包不 import 账本）；真实 SQLite 的直通竖切
// 在 internal/ledgerstep/discipline_passthrough_test.go。
package discipline

import (
	"errors"
	"strings"
	"testing"
)

var errUnknown = errors.New("ledger: 记录不存在")

func fakeLookup(rows map[string]string) DisciplineLookup {
	return func(name string) (int, string, error) {
		body, ok := rows[name]
		if !ok {
			return 0, "", errUnknown
		}
		return 2, body, nil
	}
}

func yesPtr() *bool { v := true; return &v }
func noPtr() *bool  { v := false; return &v }

const (
	headMark = "# 平台不变量（恒在层）"
	tailMark = "收口前逐条自查"
)

func TestResolveDispatchTriState(t *testing.T) {
	lookup := fakeLookup(map[string]string{"review": "只写台账，不碰代码。"})
	cases := []struct {
		name      string
		targetCap *bool
		wantErr   bool
	}{
		{"nil 按不支持处置", nil, true},
		{"false 不支持", noPtr(), true},
		{"true 支持", yesPtr(), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveDispatch(lookup, DisciplineRef{Name: "review"}, true, tc.targetCap)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("要拒发，却返回了 %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("要放行，却拒绝: %v", err)
			}
			if !strings.Contains(got.Text, "只写台账") {
				t.Fatalf("角色层正文不在场: %q", got.Text)
			}
		})
	}
}

func TestResolveDispatchAssembly(t *testing.T) {
	lookup := fakeLookup(map[string]string{"review": "只写台账，不碰代码。"})

	got, err := ResolveDispatch(lookup, DisciplineRef{Name: "review"}, true, yesPtr())
	if err != nil {
		t.Fatalf("named: %v", err)
	}
	if got.Version != 2 || got.Name != "review" {
		t.Fatalf("产物引用不对: %+v", got)
	}
	head, role, tail := strings.Index(got.Text, headMark),
		strings.Index(got.Text, "只写台账"), strings.Index(got.Text, tailMark)
	if head < 0 || role < 0 || tail < 0 || !(head < role && role < tail) {
		t.Fatalf("三层组装顺序不对（头/角色/尾 = %d/%d/%d）: %q", head, role, tail, got.Text)
	}
	if !strings.Contains(got.Source, "review") {
		t.Fatalf("来源标注缺角色名: %q", got.Source)
	}

	bare, err := ResolveDispatch(lookup, DisciplineRef{}, true, yesPtr())
	if err != nil {
		t.Fatalf("未点名: %v", err)
	}
	if !strings.Contains(bare.Text, headMark) || strings.Contains(bare.Text, "不碰代码") {
		t.Fatalf("未点名应只有平台层: %q", bare.Text)
	}
	if bare.Name != "" || bare.Version != 0 {
		t.Fatalf("未点名应零引用: %+v", bare)
	}

	adHoc, err := ResolveDispatch(lookup, DisciplineRef{RawText: "本次临时纪律。"}, true, yesPtr())
	if err != nil {
		t.Fatalf("raw: %v", err)
	}
	if adHoc.Version != 0 || !strings.Contains(adHoc.Text, "本次临时纪律。") {
		t.Fatalf("临时正文的产物不对: %+v", adHoc)
	}

	off, err := ResolveDispatch(lookup, DisciplineRef{Name: "review"}, false, yesPtr())
	if err != nil {
		t.Fatalf("platform off: %v", err)
	}
	if strings.Contains(off.Text, headMark) || !strings.Contains(off.Text, "不碰代码") {
		t.Fatalf("关闭平台层后应只剩 base: %q", off.Text)
	}
}

func TestResolveDispatchRefusal(t *testing.T) {
	lookup := fakeLookup(map[string]string{})
	if _, err := ResolveDispatch(lookup, DisciplineRef{Name: "charter-must-override"}, true, yesPtr()); !errors.Is(err, errUnknown) {
		t.Fatalf("未知名字要原样上抛拒发，得到: %v", err)
	}
	if _, err := ResolveDispatch(nil, DisciplineRef{Name: "review"}, true, yesPtr()); err == nil {
		t.Fatal("点名但无 lookup 要报错，却返回了成功")
	}
	if _, err := ResolveDispatch(fakeLookup(map[string]string{"a": "x"}), DisciplineRef{Name: " a ", RawText: ""}, true, yesPtr()); err == nil {
		t.Fatal("名字带首尾空白要报参数错误，却返回了成功")
	}
	if _, err := ResolveDispatch(fakeLookup(map[string]string{"a": "x"}), DisciplineRef{Name: "a", RawText: "b"}, true, yesPtr()); err == nil {
		t.Fatal("Name+RawText 同填要报参数错误，却返回了成功")
	}
	if _, err := ResolveDispatch(fakeLookup(map[string]string{"a": "x"}), DisciplineRef{Name: "a"}, true, nil); !errors.Is(err, ErrUnsupportedTarget) {
		t.Fatalf("能力位缺席要拒发且错误可辨，得到: %v", err)
	}
}
