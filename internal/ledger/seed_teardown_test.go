// 本文件锁定账本初始化边界：Open 只建 schema，不注入工作流或模板种子。
package ledger

import "testing"

// TestOpenInstallsNoSeeds 锁死 B230 的核心结论：打开账本只建 schema，
// 不注入任何工作流与模板。handoff 是通用派发引擎，出厂不预设方法论——
// 谁把 seed 加回来，这条会红。
func TestOpenInstallsNoSeeds(t *testing.T) {
	st, err := Open(t.TempDir() + "/ledger.db")
	if err != nil {
		t.Fatalf("打开账本: %v", err)
	}
	defer st.Close()

	workflows, err := st.ListWorkflowNames()
	if err != nil {
		t.Fatalf("列工作流: %v", err)
	}
	if len(workflows) != 0 {
		t.Fatalf("新账本不该有任何工作流，实得 %v", workflows)
	}

	templates, err := st.ListTemplateNames()
	if err != nil {
		t.Fatalf("列模板: %v", err)
	}
	if len(templates) != 0 {
		t.Fatalf("新账本不该有任何模板，实得 %v", templates)
	}
}
