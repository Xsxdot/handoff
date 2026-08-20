package ledgernode

import (
	"errors"
	"strings"
	"testing"
)

// TestSyncWorkBranchScriptLadder 阶梯三条腿都要在脚本里（spec §3.3）：
// 本地有就推、本地没有就试 fetch、都没有就打 marker 退 3。
func TestSyncWorkBranchScriptLadder(t *testing.T) {
	script := syncWorkBranchScript("feat/x")
	for _, want := range []string{
		"git rev-parse --verify --quiet 'refs/heads/feat/x'",
		"git push origin 'feat/x':'feat/x'",
		"git fetch origin 'feat/x'",
		workBranchMissingMarker,
		"exit 3",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("脚本缺 %q：\n%s", want, script)
		}
	}
	// 绝不强推：这条是安全红线，写成断言防止以后有人「顺手加个 --force 就好了」
	if strings.Contains(script, "--force") || strings.Contains(script, " -f ") {
		t.Fatalf("脚本不得包含强推：\n%s", script)
	}
}

// TestSyncWorkBranchScriptQuotesBranch 分支名带单引号也不能破坏脚本。
func TestSyncWorkBranchScriptQuotesBranch(t *testing.T) {
	script := syncWorkBranchScript("weird'name")
	if !strings.Contains(script, `'weird'"'"'name'`) {
		t.Fatalf("分支名未被正确转义：\n%s", script)
	}
}

// TestClassifyScriptErrorMarker 命中 marker 时必须能被 errors.Is 认出来，
// 否则 MergeNode 会把「工作分支缺失」误报成「合并冲突」，人看到的原因是错的。
func TestClassifyScriptErrorMarker(t *testing.T) {
	out := []byte("something\n" + workBranchMissingMarker + "\n")
	err := classifyScriptError(out, errors.New("exit status 3"), "合并")
	if !errors.Is(err, ErrWorkBranchMissing) {
		t.Fatalf("应能识别为工作分支缺失，实得: %v", err)
	}
}

// TestClassifyScriptErrorPlain 普通失败保留原始输出，不许吞。
func TestClassifyScriptErrorPlain(t *testing.T) {
	out := []byte("CONFLICT (content): foo.go\n")
	err := classifyScriptError(out, errors.New("exit status 1"), "合并")
	if errors.Is(err, ErrWorkBranchMissing) {
		t.Fatalf("普通失败不该被认成工作分支缺失: %v", err)
	}
	if !strings.Contains(err.Error(), "CONFLICT (content): foo.go") {
		t.Fatalf("错误里必须带脚本原始输出: %v", err)
	}
}

// TestClassifyScriptErrorNil err 为 nil 时不造错误。
func TestClassifyScriptErrorNil(t *testing.T) {
	if err := classifyScriptError([]byte("ok"), nil, "合并"); err != nil {
		t.Fatalf("成功时应返回 nil，实得: %v", err)
	}
}
