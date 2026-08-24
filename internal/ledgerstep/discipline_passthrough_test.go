// discipline_passthrough_test.go —— B229 直通竖切（重档法定步骤）：
// 一次真实调用穿过缝 2（真实 SQLite 库 + PutDiscipline 种子）与缝 1
// （discipline.ResolveDispatch 的组装与能力位判定）。库缝按 spec 取义：
// 测试夹具直调。本文件是测试，不进代码图；ledgerstep 生产代码对
// discipline/ledger 保持既有依赖面不变。
package ledgerstep_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/discipline"
	"github.com/Xsxdot/handoff/internal/ledger"
)

func yes() *bool { v := true; return &v }

func lookupOf(st *ledger.Store) discipline.DisciplineLookup {
	return func(name string) (int, string, error) {
		d, err := st.GetDiscipline(name, 0)
		if err != nil {
			return 0, "", err
		}
		return d.Version, d.Body, nil
	}
}

func TestResolveDispatchPassthrough(t *testing.T) {
	st, err := ledger.Open(t.TempDir() + "/ledger.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	if _, err := st.PutDiscipline("review", "只读，不写。"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := st.PutDiscipline("review", "只读，不写。第二轮措辞修正。"); err != nil {
		t.Fatalf("seed v2: %v", err)
	}

	got, err := discipline.ResolveDispatch(lookupOf(st), discipline.DisciplineRef{Name: "review"}, true, yes())
	if err != nil {
		t.Fatalf("竖切失败: %v", err)
	}
	if got.Version != 2 || got.Name != "review" {
		t.Fatalf("要取最新版 v2/review，得到 v%d/%s", got.Version, got.Name)
	}
	for _, mark := range []string{"# 平台不变量（恒在层）", "第二轮措辞修正。", "收口前逐条自查"} {
		if !strings.Contains(got.Text, mark) {
			t.Fatalf("组装产物缺 %q: %q", mark, got.Text)
		}
	}
	if !strings.Contains(got.Source, "review") {
		t.Fatalf("来源标注不对: %q", got.Source)
	}

	if _, err := discipline.ResolveDispatch(lookupOf(st), discipline.DisciplineRef{Name: "nope"}, true, yes()); !errors.Is(err, ledger.ErrNotFound) {
		t.Fatalf("未知名要透传账本 ErrNotFound 拒发，得到: %v", err)
	}
	names, err := st.ListDisciplineNames()
	if err != nil || len(names) != 1 || names[0] != "review" {
		t.Fatalf("list: %v %v", names, err)
	}
}
