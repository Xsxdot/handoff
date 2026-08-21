package discipline

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestForUnconfiguredUsesBuiltinByTier(t *testing.T) {
	r := NewResolver(t.TempDir(), nil, quietLog())
	for _, c := range []struct{ exec, wantSource, wantSubstr string }{
		{"opencode", "内置:" + TierSubagent, "用你自己的 subagent 机制"},
		{"claude", "内置:" + TierSubagent, "用你自己的 subagent 机制"},
		{"codex", "内置:" + TierSingleContext, "在本会话内自己逐 task 实现"},
		{"grok", "内置:" + TierSingleContext, "在本会话内自己逐 task 实现"},
	} {
		b, err := r.For(c.exec)
		if err != nil {
			t.Fatalf("%s: 意外错误 %v", c.exec, err)
		}
		if b.Source != c.wantSource {
			t.Errorf("%s: Source = %q, want %q", c.exec, b.Source, c.wantSource)
		}
		if !strings.Contains(b.Text, c.wantSubstr) {
			t.Errorf("%s: 正文未含 %q", c.exec, c.wantSubstr)
		}
	}
}

func TestForUnknownExecutorFallsBackToSingleContext(t *testing.T) {
	r := NewResolver(t.TempDir(), nil, quietLog())
	b, err := r.For("某个还没写适配器的执行器")
	if err != nil {
		t.Fatalf("意外错误 %v", err)
	}
	if b.Source != "内置:"+TierSingleContext {
		t.Fatalf("Source = %q，未登记的必须落单上下文版", b.Source)
	}
}

func TestForConfiguredReadsFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mine.md"), []byte("我自己的纪律"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := NewResolver(dir, Static(map[string]string{"codex": "mine.md"}), quietLog())
	b, err := r.For("codex")
	if err != nil {
		t.Fatalf("意外错误 %v", err)
	}
	if b.Text != "我自己的纪律" {
		t.Errorf("Text = %q", b.Text)
	}
	if b.Source != "配置:mine.md" {
		t.Errorf("Source = %q, want 配置:mine.md", b.Source)
	}
}

func TestForEmptyValueDisablesInjection(t *testing.T) {
	r := NewResolver(t.TempDir(), Static(map[string]string{"codex": "  "}), quietLog())
	b, err := r.For("codex")
	if err != nil {
		t.Fatalf("意外错误 %v", err)
	}
	if b.Text != "" || b.Source != "" {
		t.Fatalf("显式关闭却拿到了纪律块：%+v", b)
	}
}

// TestByNameIgnoresExecutorDisable 机器级显式关闭只属于 executor 兜底轴，
// 不得让点名 review 的正确性路径也变成空块。
func TestByNameIgnoresExecutorDisable(t *testing.T) {
	r := NewResolver(t.TempDir(), Static(map[string]string{"grok": ""}), quietLog())
	fallback, err := r.For("grok")
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if fallback.Text != "" || fallback.Source != "" {
		t.Fatalf("显式关闭应返回空块，实得 %+v", fallback)
	}

	named, err := r.ByName(NameReview, "grok")
	if err != nil {
		t.Fatalf("ByName(review): %v", err)
	}
	if !strings.Contains(named.Text, "只读，不写") {
		t.Fatalf("点名 review 应仍返回完整块，实得前 80 字节 %.80q", named.Text)
	}
	if strings.Contains(named.Text, "每个 task 完成即 commit") {
		t.Fatal("点名 review 不应拿到 executor 兜底的实现块")
	}
}

func TestForRejectsPathSeparator(t *testing.T) {
	for _, bad := range []string{"../etc/passwd", "sub/dir.md", "."} {
		r := NewResolver(t.TempDir(), Static(map[string]string{"codex": bad}), quietLog())
		if _, err := r.For("codex"); err == nil {
			t.Errorf("%q 应被拒", bad)
		}
	}
}

func TestForMissingFileErrors(t *testing.T) {
	r := NewResolver(t.TempDir(), Static(map[string]string{"codex": "nope.md"}), quietLog())
	if _, err := r.For("codex"); err == nil {
		t.Fatal("文件缺失应报错")
	}
}

func TestPreflightDoesNotPanicOnBadConfig(t *testing.T) {
	r := NewResolver(t.TempDir(), Static(map[string]string{"codex": "nope.md", "grok": "../x"}), quietLog())
	r.Preflight()
}

func TestForReadsMappingEveryCall(t *testing.T) {
	// 映射是活的：改配置后**不重建 Resolver**，下一次 For 就该看到新值。
	// 这是控制台改映射能「下个任务即生效」的唯一地基。
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mine.md"), []byte("自定义纪律\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	m := map[string]string{}
	r := NewResolver(dir, func() map[string]string { return m }, quietLog())

	if b, err := r.For("codex"); err != nil || b.Source != "内置:single-context" {
		t.Fatalf("改前 Source = %q err=%v", b.Source, err)
	}
	m["codex"] = "mine.md"
	b, err := r.For("codex")
	if err != nil {
		t.Fatalf("改后 For: %v", err)
	}
	if b.Source != "配置:mine.md" || b.Text != "自定义纪律\n" {
		t.Fatalf("改后 = %q / %q", b.Source, b.Text)
	}
}

func TestNilMappingBehavesAsEmpty(t *testing.T) {
	// nil 取值函数不能 panic：测试与早期引导路径都可能不传
	r := NewResolver(t.TempDir(), nil, quietLog())
	if b, err := r.For("opencode"); err != nil || b.Source != "内置:subagent" {
		t.Fatalf("Source = %q err = %v", b.Source, err)
	}
}

// TestByNameBuiltinReview 点名 review 时取内置只读块，与 executor 无关。
func TestByNameBuiltinReview(t *testing.T) {
	r := NewResolver(t.TempDir(), nil, quietLog())
	for _, exec := range []string{"codex", "grok", "opencode", "从未登记的执行器"} {
		block, err := r.ByName(NameReview, exec)
		if err != nil {
			t.Fatalf("ByName(review, %s): %v", exec, err)
		}
		if !strings.Contains(block.Text, "只读，不写") {
			t.Fatalf("review 块正文不对（executor=%s）：%.80s", exec, block.Text)
		}
		if strings.Contains(block.Text, "每个 task 完成即 commit") {
			t.Fatalf("review 块里不该有实现纪律的提交条款（executor=%s）", exec)
		}
		if block.Source != "内置:review" {
			t.Fatalf("Source 应为 内置:review，实得 %q", block.Source)
		}
	}
}

// TestByNameImplementSplitsByTier implement 内部仍按 executor 能力分档，
// 且 Source 要把档位带出来——只写「内置:implement」会把「派错档」这个
// 历史上真出过事的信息藏起来。
func TestByNameImplementSplitsByTier(t *testing.T) {
	r := NewResolver(t.TempDir(), nil, quietLog())
	for _, tc := range []struct{ executor, wantSource, wantMark string }{
		{"opencode", "内置:implement(subagent)", "subagent"},
		{"codex", "内置:implement(single-context)", "在本会话内自己逐 task 实现"},
		{"从未登记的执行器", "内置:implement(single-context)", "在本会话内自己逐 task 实现"},
	} {
		block, err := r.ByName(NameImplement, tc.executor)
		if err != nil {
			t.Fatalf("ByName(implement, %s): %v", tc.executor, err)
		}
		if block.Source != tc.wantSource {
			t.Fatalf("executor=%s Source 应为 %q，实得 %q", tc.executor, tc.wantSource, block.Source)
		}
		if !strings.Contains(block.Text, tc.wantMark) {
			t.Fatalf("executor=%s 正文档位不对：%.80s", tc.executor, block.Text)
		}
	}
}

// TestByNameFileOverridesBuiltin 目录里放同名 .md 即覆盖内置。
func TestByNameFileOverridesBuiltin(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "review.md"), []byte("我自己的审阅纪律"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := NewResolver(dir, nil, quietLog())
	block, err := r.ByName(NameReview, "grok")
	if err != nil {
		t.Fatalf("ByName: %v", err)
	}
	if block.Text != "我自己的审阅纪律" {
		t.Fatalf("应取磁盘文件，实得 %q", block.Text)
	}
	if block.Source != "配置:review" {
		t.Fatalf("Source 应为 配置:review，实得 %q", block.Source)
	}
}

// TestByNameUnknownNameRejected 名字既无文件又无同名内置：拒绝，不退回兜底。
// 悄悄换一份比失败更危险——调用方会以为跑的是它点的那套。
func TestByNameUnknownNameRejected(t *testing.T) {
	r := NewResolver(t.TempDir(), nil, quietLog())
	_, err := r.ByName("bugfix", "codex")
	if err == nil {
		t.Fatal("未知名字应报错")
	}
	if !strings.Contains(err.Error(), "bugfix") {
		t.Fatalf("错误里应带名字：%v", err)
	}
}

// TestByNameIllegalName 名字不是路径：含分隔符一律拒。
func TestByNameIllegalName(t *testing.T) {
	r := NewResolver(t.TempDir(), nil, quietLog())
	for _, bad := range []string{"../etc/passwd", "sub/dir", "", ".", ".."} {
		if _, err := r.ByName(bad, "codex"); !errors.Is(err, ErrBadName) {
			t.Fatalf("名字 %q 应按 ErrBadName 拒绝，实得 %v", bad, err)
		}
	}
}

// TestByNameOversizeFileRejected 覆盖文件超限：拒绝，与 For 同款语义。
func TestByNameOversizeFileRejected(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "review.md"),
		make([]byte, maxBlockSize+1), 0o644); err != nil {
		t.Fatal(err)
	}
	r := NewResolver(dir, nil, quietLog())
	if _, err := r.ByName(NameReview, "grok"); err == nil {
		t.Fatal("超限文件应报错")
	}
}

// TestForUnchangedByNamedPath 兜底路径一字未改：不点名时行为与改动前一致。
func TestForUnchangedByNamedPath(t *testing.T) {
	r := NewResolver(t.TempDir(), nil, quietLog())
	block, err := r.For("codex")
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if block.Source != "内置:"+TierSingleContext {
		t.Fatalf("兜底 Source 变了：%q", block.Source)
	}
}

func TestBuiltinByNameCoversNewRoles(t *testing.T) {
	for _, name := range []string{NameImplement, NameReview, NameSpecDraft, NamePlanWriting, NameFinishing} {
		block, ok := builtinByName(name, "codex")
		if !ok {
			t.Fatalf("角色 %q 没有内置纪律块", name)
		}
		if strings.TrimSpace(block.Text) == "" {
			t.Fatalf("角色 %q 的内置纪律块是空的", name)
		}
		if block.Source == "" {
			t.Fatalf("角色 %q 的 Source 为空", name)
		}
	}
}

func TestBuiltinsListStableAndComplete(t *testing.T) {
	got := Builtins()
	if len(got) != 6 {
		t.Fatalf("内置纪律块应有 6 份（subagent/single-context/review/spec-draft/plan-writing/finishing），得到 %d", len(got))
	}
	// 顺序固定：控制台用 builtins[0] 当默认选中项，换位置会静默改掉用户看到的内容。
	if got[0].Tier != TierSubagent || got[1].Tier != TierSingleContext || got[2].Tier != NameReview {
		t.Fatalf("前三项顺序被改动: %+v", got[:3])
	}
}

func TestFinishingBlockCarriesBaseDiscipline(t *testing.T) {
	block, _ := builtinByName(NameFinishing, "codex")
	for _, want := range []string{"基线", "不要", "裁决"} {
		if !strings.Contains(block.Text, want) {
			t.Fatalf("收尾纪律块缺关键约束 %q", want)
		}
	}
}
