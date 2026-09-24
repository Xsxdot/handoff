package cmd

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
)

// TestB382CardWorkBranchCLIEndToEnd 穿过真实 CLI、账本 SQLite 与命令输出边界：
// add → work-branch → WorkBranch 读回；空串清除；有快照时命令成功、stderr 提示、
// WorkBranch 仍返回快照。
func TestB382CardWorkBranchCLIEndToEnd(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runLedgerCLI(t, dir, "card", "add", "B382 人工实现卡", "--project", "demo")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &created); err != nil {
		t.Fatalf("解 add 输出 %q: %v", out, err)
	}

	if _, _, err := runLedgerCLI(t, dir, "card", "work-branch", created.ID, "feat/b382-cli"); err != nil {
		t.Fatalf("work-branch: %v", err)
	}
	func() {
		st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
		if err != nil {
			t.Fatalf("打开账本: %v", err)
		}
		defer st.Close()
		if wb, err := st.WorkBranch(created.ID); err != nil || wb.Branch != "feat/b382-cli" {
			t.Fatalf("登记后 WorkBranch=%+v err=%v", wb, err)
		}
	}()

	if _, _, err := runLedgerCLI(t, dir, "card", "work-branch", created.ID, ""); err != nil {
		t.Fatalf("清除: %v", err)
	}
	func() {
		st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
		if err != nil {
			t.Fatalf("打开账本: %v", err)
		}
		defer st.Close()
		if _, err := st.WorkBranch(created.ID); err == nil {
			t.Fatal("清除后 WorkBranch 应报错")
		}
	}()

	// 有快照时：命令成功（不报错）、stderr 有提示、WorkBranch 仍返回快照。
	func() {
		st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
		if err != nil {
			t.Fatalf("打开账本: %v", err)
		}
		defer st.Close()
		if err := st.RecordDispatch(created.ID, ledger.DispatchSnapshot{
			Template: "feature-impl", Target: "mac-02", TaskID: "T-cli",
			Branch: "cards/" + created.ID + "-implement", Purpose: ledger.PurposeImplement, Actor: "test",
		}); err != nil {
			t.Fatalf("落快照: %v", err)
		}
	}()
	_, errOut, err := runLedgerCLI(t, dir, "card", "work-branch", created.ID, "feat/should-ignore")
	if err != nil {
		t.Fatalf("有快照时登记不应报错: %v", err)
	}
	if !strings.Contains(errOut, "不生效") {
		t.Fatalf("有快照时应在 stderr 提示不生效，实得 %q", errOut)
	}
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatalf("重开账本: %v", err)
	}
	defer st.Close()
	if wb, err := st.WorkBranch(created.ID); err != nil || wb.Branch != "cards/"+created.ID+"-implement" {
		t.Fatalf("快照应胜出，实得 %+v err=%v", wb, err)
	}
}
