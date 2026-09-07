package claudecode_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/claudecode"
)

func TestProfilePrepareNotesKeychain(t *testing.T) {
	home := t.TempDir()
	var prof executor.Profile = claudecode.New(nil).Profile()
	req := executor.ProfileReq{
		HomeDir:  home,
		Isolated: true,
		Rules: []executor.ProfileFile{
			{Name: "CLAUDE.md", Content: "carrier rules"},
		},
	}
	rep, err := prof.Prepare(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, n := range append(rep.Notes, rep.Missing...) {
		if strings.Contains(n, "Keychain") || strings.Contains(n, "无可靠文件") || strings.Contains(n, "未拷贝登录态") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("claude Prepare 必须在 Notes 或 Missing 中说明凭据不足/Keychain: %+v", rep)
	}
}
