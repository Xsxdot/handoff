// workbench_test.go —— 工作台状态存储的 CRUD 与淘汰边界测试。
//
// 职责：验证 workbench_bases / workbench_singletons 的读写、覆盖、删除和行数上限。
// 边界：不验证 HTTP 参数校验或前端 payload 形状；那些由 agentd 与 Web 层测试负责。
package store

import (
	"path/filepath"
	"testing"
)

// newWorkbenchStore 开一个临时库，供本文件的用例共用。
func newWorkbenchStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// TestWorkbenchBaseCRUD 覆盖基准行的写、覆盖写、列、删。
func TestWorkbenchBaseCRUD(t *testing.T) {
	st := newWorkbenchStore(t)

	if err := st.PutWorkbenchBase("/repo/a", `{"v":1}`, 1000); err != nil {
		t.Fatalf("PutWorkbenchBase: %v", err)
	}
	if err := st.PutWorkbenchBase("/repo/b", `{"v":2}`, 2000); err != nil {
		t.Fatalf("PutWorkbenchBase: %v", err)
	}

	bases, singles, err := st.ListWorkbench()
	if err != nil {
		t.Fatalf("ListWorkbench: %v", err)
	}
	if len(bases) != 2 {
		t.Fatalf("bases = %d，期望 2", len(bases))
	}
	if len(singles) != 0 {
		t.Fatalf("singles = %v，期望空", singles)
	}
	// 列出顺序按 updated_at 倒序：最近动过的排前面
	if bases[0].BaseKey != "/repo/b" {
		t.Fatalf("bases[0] = %s，期望最近写入的 /repo/b", bases[0].BaseKey)
	}
	if bases[0].Payload != `{"v":2}` || bases[0].UpdatedAt != 2000 {
		t.Fatalf("bases[0] = %+v", bases[0])
	}

	// 同 key 覆盖写
	if err := st.PutWorkbenchBase("/repo/a", `{"v":9}`, 3000); err != nil {
		t.Fatalf("覆盖写: %v", err)
	}
	bases, _, _ = st.ListWorkbench()
	if bases[0].BaseKey != "/repo/a" || bases[0].Payload != `{"v":9}` {
		t.Fatalf("覆盖写后 bases[0] = %+v", bases[0])
	}

	if err := st.DeleteWorkbenchBase("/repo/a"); err != nil {
		t.Fatalf("DeleteWorkbenchBase: %v", err)
	}
	bases, _, _ = st.ListWorkbench()
	if len(bases) != 1 || bases[0].BaseKey != "/repo/b" {
		t.Fatalf("删除后 bases = %+v", bases)
	}
	// 删不存在的行是幂等的，不报错
	if err := st.DeleteWorkbenchBase("/repo/nope"); err != nil {
		t.Fatalf("删不存在的行应幂等: %v", err)
	}
}

// TestWorkbenchBaseLimit 钉住 50 行上限：写第 51 行时最旧的那条消失。
func TestWorkbenchBaseLimit(t *testing.T) {
	st := newWorkbenchStore(t)
	for i := 0; i < WorkbenchBaseLimit; i++ {
		key := "/repo/" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		if err := st.PutWorkbenchBase(key, `{}`, int64(1000+i)); err != nil {
			t.Fatalf("第 %d 行: %v", i, err)
		}
	}
	bases, _, _ := st.ListWorkbench()
	if len(bases) != WorkbenchBaseLimit {
		t.Fatalf("满额时 bases = %d，期望 %d", len(bases), WorkbenchBaseLimit)
	}
	oldest := bases[len(bases)-1].BaseKey

	// 再写一行，最旧的那条必须被裁掉
	if err := st.PutWorkbenchBase("/repo/newest", `{}`, 9999); err != nil {
		t.Fatalf("第 51 行: %v", err)
	}
	bases, _, _ = st.ListWorkbench()
	if len(bases) != WorkbenchBaseLimit {
		t.Fatalf("超额后 bases = %d，期望仍为 %d", len(bases), WorkbenchBaseLimit)
	}
	for _, b := range bases {
		if b.BaseKey == oldest {
			t.Fatalf("最旧的 %s 应被裁掉，实际仍在", oldest)
		}
	}
	if bases[0].BaseKey != "/repo/newest" {
		t.Fatalf("bases[0] = %s，期望刚写入的 /repo/newest", bases[0].BaseKey)
	}
}

// TestWorkbenchSingletons 覆盖单例的写、覆盖、列、删。
func TestWorkbenchSingletons(t *testing.T) {
	st := newWorkbenchStore(t)

	if err := st.PutWorkbenchSingleton(WorkbenchKeySelected, "/repo/a", 1000); err != nil {
		t.Fatalf("PutWorkbenchSingleton: %v", err)
	}
	if err := st.PutWorkbenchSingleton(WorkbenchKeyDock, `{"v":1}`, 1000); err != nil {
		t.Fatalf("PutWorkbenchSingleton dock: %v", err)
	}
	_, singles, err := st.ListWorkbench()
	if err != nil {
		t.Fatalf("ListWorkbench: %v", err)
	}
	if singles[WorkbenchKeySelected] != "/repo/a" || singles[WorkbenchKeyDock] != `{"v":1}` {
		t.Fatalf("singles = %v", singles)
	}

	if err := st.PutWorkbenchSingleton(WorkbenchKeySelected, "/repo/b", 2000); err != nil {
		t.Fatalf("覆盖单例: %v", err)
	}
	_, singles, _ = st.ListWorkbench()
	if singles[WorkbenchKeySelected] != "/repo/b" {
		t.Fatalf("覆盖后 selected = %q", singles[WorkbenchKeySelected])
	}

	if err := st.DeleteWorkbenchSingleton(WorkbenchKeyDock); err != nil {
		t.Fatalf("DeleteWorkbenchSingleton: %v", err)
	}
	_, singles, _ = st.ListWorkbench()
	if _, ok := singles[WorkbenchKeyDock]; ok {
		t.Fatalf("dock 应已删除，singles = %v", singles)
	}
	// 单例行不参与基准行的淘汰
	if err := st.DeleteWorkbenchSingleton("nope"); err != nil {
		t.Fatalf("删不存在的单例应幂等: %v", err)
	}
}
