# 纪律块具名化 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把纪律块从「按 executor 名解析的一份」改成「派发方可点名的具名资源」，消除
`card dispatch` 路径上两份纪律块同时注入（且审阅那次互相矛盾）的缺陷。

**Architecture:** `internal/discipline` 增一条 `ByName` 解析路径（文件覆盖 > 内置同名块），
与既有 `For(executor)` 并列、后者一字不改作兜底。派发请求带**名字**（不是路径也不是正文），
名字落盘到 task，`continue` 与 `ResumeTask` 靠它重解析。CLI 不再读文件、不再往 prompt 里
拼纪律块。

**Tech Stack:** Go 1.21+、SQLite（`internal/store`）、`log/slog`。无新依赖。

## Global Constraints

- **`Resolver.For(executor)` 的实现与语义一字不改**——它是所有现存部署的兜底路径。
- **不点名的派发行为必须逐字不变**：同一 executor 拿到同一份块，`纪律块: <来源>` 回显不变。
- 纪律块名字**不是路径**：不含路径分隔符，落盘文件名固定为 `<名字>.md`。
- 日志一律用 `slog`（本仓的 `log()` / `r.log` / `m.log`），**禁止 `fmt.Printf`**。
- 新文件写文件头注释（职责 + 边界）；导出函数写 doc 注释（参数、返回、注意事项）；
  非显然分支写「为什么」的中文注释。
- 本 plan **只动后端**，不碰 `web/`。
- 本 plan **不调用 handoff CLI、不起 agentd 进程**——需要真机驱动 handoff 的验收在附一，
  由审核者执行。

---

### Task 1: `internal/discipline` 加内置 `review` 块与 `ByName` 解析

**Files:**
- Create: `internal/discipline/builtin/review.md`（正文从 `docs/superpowers/discipline/block-review.md` 原样复制，**不要改一个字**）
- Modify: `internal/discipline/discipline.go`（加 name 常量、`builtinByName`、`Builtins()` 追加一条）
- Modify: `internal/discipline/resolver.go`（加 `ByName`）
- Test: `internal/discipline/resolver_test.go`（文件已存在，追加用例）

**Interfaces:**
- Consumes: 同包既有的 `Block`、`builtinFor`、`resolvePath`、`maxBlockSize`、`ErrBadName`、`defaultTier`
- Produces:
  - `const NameImplement = "implement"`、`const NameReview = "review"`
  - `func (r *Resolver) ByName(name, executor string) (Block, error)`
  - `Builtins()` 返回值从 2 条变 3 条，**`review` 追加在最后**

**先读懂再动**：打开 `internal/discipline/resolver.go` 的 `For`（约 96 行）。它的三档语义
（配置非空 → 读文件；显式空串 → 关闭；键不存在 → 内置默认）是**本 task 不许碰**的。
`ByName` 是**另一条独立路径**，不复用它的三档逻辑，只复用 `resolvePath` 与大小校验。

**为什么 `ByName` 要收 executor**：`implement` 这个名字内部仍要按 `defaultTier` 分档
（codex/grok 读到 subagent 版会转而扮协调者，这条有实测）。`review` 与 executor 无关，
参数在那条路径上不被使用。

- [ ] **Step 1: 复制内置 review 正文**

```bash
cp docs/superpowers/discipline/block-review.md internal/discipline/builtin/review.md
```

不要编辑它。`docs/` 下那份在 Task 6 删除，此刻还不能删——Task 1-5 的测试与它无关，
但保留原件方便逐字比对。

- [ ] **Step 2: 写失败的测试**

**先看清两个测试文件不在同一个包**：`resolver_test.go` 是 `package discipline`（内部测试，
能用 `maxBlockSize`、`quietLog()`、不加包名调 `NewResolver`），`files_test.go` 是
`package discipline_test`（外部测试，一律 `discipline.` 前缀）。下面的用例都要放进
**`resolver_test.go`**——它们引用了未导出的 `maxBlockSize`。

`quietLog()` 是该文件已有的静音 logger 辅助（第 12 行），照用，别新造。测试目录用
`t.TempDir()`，**绝不建在仓库内**（仓库内的临时目录会破坏 git 相关测试的前提）：

```go
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
```

`resolver_test.go` 若还没 import `errors`、`os`、`path/filepath`、`strings`，一并补上。

- [ ] **Step 3: 跑测试确认它失败**

Run: `go test ./internal/discipline/ -run 'TestByName|TestBuiltins|TestForUnchanged' -count=1`
Expected: **编译失败**，`r.ByName undefined`、`NameReview undefined`。

- [ ] **Step 4: 实现**

`internal/discipline/discipline.go` 追加（放在 `builtinFor` 之后）：

```go
//go:embed builtin/review.md
var builtinReview string

// 纪律块角色名。名字是「这一轮执行者扮演什么角色」，与 Tier（执行器能力档位）
// 是两条正交的轴：implement 这一个角色内部还要按档位分，review 则与档位无关。
const (
	NameImplement = "implement" // 实现角色；内部按 defaultTier 落到 subagent / single-context
	NameReview    = "review"    // 审阅角色；只读，与执行器能力无关
)

// builtinByName 返回该名字的内置纪律块；名字没有内置对应物时返回 ok=false。
//
// 参数：name 角色名；executor 仅在 name==NameImplement 时被使用（选档位）。
// 返回：Block 与命中标志。
//
// Source 里给 implement 带上档位（如「内置:implement(single-context)」）是刻意的：
// 只写角色名会把「派错档」这个历史上真出过事的信息藏起来——codex/grok 读到
// subagent 版会转而扮协调者，同一份 plan 从「0 推动跑完」退化成「9 次人工推动卡死」。
func builtinByName(name, executor string) (Block, bool) {
	switch name {
	case NameImplement:
		b := builtinFor(executor)
		tier := TierSingleContext
		if defaultTier[executor] == TierSubagent {
			tier = TierSubagent
		}
		return Block{Text: b.Text, Source: "内置:" + NameImplement + "(" + tier + ")"}, true
	case NameReview:
		return Block{Text: builtinReview, Source: "内置:" + NameReview}, true
	}
	return Block{}, false
}
```

`Builtins()` 改为三条，**review 追加在最后**：

```go
func Builtins() []Builtin {
	return []Builtin{
		{Tier: TierSubagent, Content: builtinSubagent},
		{Tier: TierSingleContext, Content: builtinSingleContext},
		// review 追加在末尾而不是插在前面：控制台用 builtins[0] 当默认选中项，
		// 换位置会静默改掉用户打开设置页时看到的内容。
		{Tier: NameReview, Content: builtinReview},
	}
}
```

`internal/discipline/resolver.go` 追加（放在 `For` 之后、`resolvePath` 之前）：

```go
// ByName 按**角色名**解析纪律块，与 For（按 executor 兜底）并列的另一条路径。
//
// 参数：
//   - name: 角色名（如 implement / review）；不是路径，含路径分隔符一律拒绝
//   - executor: 仅在 name==NameImplement 时被使用（选能力档位）
//
// 返回：
//   - Block；名字非法 / 覆盖文件超限或读不到 / 名字既无文件又无同名内置时返回错误
//
// 解析顺序：<dir>/<name>.md 存在 → 用它（Source「配置:<name>」）；
// 否则 → 内置同名块（Source「内置:<name>」）；两者都没有 → 报错。
//
// 为什么未知名字是错误而不是退回 executor 兜底：调用方明确点了名，
// 悄悄换成另一份比失败更危险——它会以为跑的是自己点的那套（与 For 里
// 「配置指向的文件缺失是错误」同一条理由）。
//
// 注意：本方法**不看** executor 的机器级映射，因此机器级的「显式空串关闭」
// 不作用于点名路径。那个开关属 executor 轴（这台机器给这个执行器派任务时不注入），
// 角色轴的点名是正确性需求（审阅必须只读），两者不是同一件事。
func (r *Resolver) ByName(name, executor string) (Block, error) {
	path, err := resolvePath(r.dir, name+".md")
	if err != nil {
		r.log.Error("纪律块名字非法", "name", name, "dir", r.dir, "cause", err)
		return Block{}, fmt.Errorf("%w: 纪律块名字 %q 不合法", ErrBadName, name)
	}
	fi, statErr := os.Stat(path)
	switch {
	case statErr == nil && fi.Size() > maxBlockSize:
		r.log.Error("纪律块覆盖文件超限", "name", name, "path", path, "size", fi.Size())
		return Block{}, fmt.Errorf("纪律块文件 %s 超过 %d 字节上限（实际 %d）", path, maxBlockSize, fi.Size())
	case statErr == nil:
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			r.log.Error("读取纪律块覆盖文件失败", "name", name, "path", path, "cause", readErr)
			return Block{}, fmt.Errorf("读取纪律块文件 %s: %w", path, readErr)
		}
		r.log.Info("纪律块按名字命中覆盖文件", "name", name, "path", path, "bytes", len(data))
		return Block{Text: string(data), Source: "配置:" + name}, nil
	}
	if block, ok := builtinByName(name, executor); ok {
		r.log.Info("纪律块按名字命中内置", "name", name, "executor", executor, "source", block.Source)
		return block, nil
	}
	r.log.Error("未知纪律块名字", "name", name, "dir", r.dir)
	return Block{}, fmt.Errorf("未知纪律块名字 %q：既无 %s 也无同名内置块", name, path)
}
```

注意 `resolvePath(r.dir, name+".md")`：先拼扩展名再校验，这样名字里含分隔符时
（`sub/dir` → `sub/dir.md`）仍然被 `resolvePath` 拒掉。空名字 `""` → `".md"`，
不含分隔符也不是 `.` / `..`，会通过 `resolvePath`——但 `.md` 文件不存在、
`builtinByName("")` 不命中，最终落到「未知名字」错误。`TestByNameIllegalName`
断言的是 `errors.Is(err, ErrBadName)`，所以空名字要在 `ByName` 开头单独挡：

```go
	if strings.TrimSpace(name) == "" {
		r.log.Error("纪律块名字为空", "dir", r.dir)
		return Block{}, fmt.Errorf("%w: 纪律块名字不能为空", ErrBadName)
	}
```

这段放在函数第一行。`.` 与 `..` 拼上 `.md` 变成 `..md` / `...md`，不含分隔符会通过
`resolvePath`，同样落到「未知名字」——所以它们也要在这里一并挡掉：

```go
	if name == "." || name == ".." || strings.ContainsRune(name, filepath.Separator) || strings.ContainsRune(name, '/') {
		r.log.Error("纪律块名字非法", "name", name, "dir", r.dir)
		return Block{}, fmt.Errorf("%w: 纪律块名字 %q 不能是路径", ErrBadName, name)
	}
```

把这两段合并成一个前置校验块放在 `ByName` 开头，后面的 `resolvePath` 调用保留
（双保险，且复用它的错误文案）。

- [ ] **Step 5: 处置转红的既有测试（判定，不是顺手改绿）**

加了第三条内置块后，`internal/discipline/files_test.go:146` 的
`TestBuiltinsAndDefaultTier` 会红在 `if len(bs) != 2`。

**动它之前先回答「它守的语义是什么」**，把答案写进提交信息：

| 它的断言 | 守的语义 | 本次是否仍成立 |
|---|---|---|
| `len(bs) != 2` | 内置块**数量**恰为 2 | **不成立**——本 task 就是要加第三条，这是需求变更 |
| `bs[0]==subagent && bs[1]==single-context` | 顺序是界面契约（控制台用 `builtins[0]` 当默认选中项） | **仍成立**，一字不许动 |
| `bs[i].Content != ""` | 每条内置块都有正文 | **仍成立**，要扩到第三条 |
| `DefaultTierFor` 那张表 | executor→档位映射不变 | **仍成立**，一字不许动 |

所以只改数量那一处，并把顺序与非空断言扩到三条：

```go
	bs := discipline.Builtins()
	if len(bs) != 3 {
		t.Fatalf("len = %d, want 3", len(bs))
	}
	// 顺序是界面契约：控制台用 builtins[0] 当默认选中项，
	// review 只能追加在末尾，不能插到前面
	want := []string{discipline.TierSubagent, discipline.TierSingleContext, discipline.NameReview}
	for i, w := range want {
		if bs[i].Tier != w {
			t.Fatalf("第 %d 条 = %q, want %q", i, bs[i].Tier, w)
		}
		if bs[i].Content == "" {
			t.Fatalf("第 %d 条（%s）内置正文不能为空", i, w)
		}
	}
```

`DefaultTierFor` 那一段保持原样。**注意该文件是 `package discipline_test`**，全部要带
`discipline.` 前缀。

- [ ] **Step 6: 跑测试确认通过**

Run: `go test ./internal/discipline/ -count=1`
Expected: PASS，全包绿（除上面判定过的那一处，既有用例必须原样通过——`For` 一字未改）。

- [ ] **Step 7: 加关键节点日志**

上面的实现里已经包含（这一步是自检，不是补写）：

- 命中覆盖文件：Info，带 name / path / bytes
- 命中内置：Info，带 name / executor / source
- 名字非法 / 超限 / 读不到 / 未知名字：**四条 Error 分支各自带上下文与 cause**
- 成功路径不静默：两条命中分支都有 Info

逐条对照代码确认没有漏掉的分支。

- [ ] **Step 8: 加注释**

同样已包含在实现里，逐条确认：

- `ByName` 的 doc 注释：参数、返回、解析顺序、**两条「为什么」**（未知名字为何是错误、
  为何不看机器级映射）
- `builtinByName` 的 doc 注释：参数语义 + Source 为何带档位
- `NameImplement` / `NameReview` 常量上方：角色轴与能力轴正交的说明
- `Builtins()` 里 review 那行的行内注释：为何追加在末尾

- [ ] **Step 9: 提交**

```bash
git add internal/discipline/
git commit -m "feat(discipline): 加内置 review 块与按名字解析的 ByName"
```

---

### Task 2: `tasks` 表加 `discipline_name` 列

**Files:**
- Modify: `internal/proto/proto.go:237-242`（加 `DisciplineName` 字段；**顺带修正 `Discipline` 那条说谎的注释**）
- Modify: `internal/store/store.go`（CREATE TABLE、迁移 map、INSERT、`taskColumns`、`scanTaskRow`）
- Test: `internal/store/store_test.go`（文件已存在，追加用例）

**Interfaces:**
- Consumes: 无（本 task 只加存储字段）
- Produces: `proto.Task.DisciplineName string`（json tag `discipline_name,omitempty`），
  经 `SaveTask` / `GetTask` 往返不丢

**先读懂再动**：`internal/store/store.go:270` 的 `taskColumns` 注释写着「加列只改这里与
scanTaskRow」——那句话**不完整**，实际要改四处：CREATE TABLE、迁移 map、INSERT 列清单与
占位符、`taskColumns` + `scanTaskRow`。本 task 顺手把那条注释补准。

**为什么要新加一列而不是复用 `Discipline`**：`Discipline` 存的是人可读来源标注
（如「内置:single-context」），是展示用的，拿它反解析名字既脆弱又会在换文案时崩。
而且——**`Discipline` 根本没落盘**：`tasks` 表里没有这一列，迁移 map 里也没有，
`proto.Task.Discipline` 的注释「该列后加、不回填」说的是一件没发生的事。本 task
只加 `discipline_name` 一列（`Discipline` 保持不落盘的现状，它只在派发响应里回显），
并把那条错误注释改对。

- [ ] **Step 1: 写失败的测试**

在 `internal/store/store_test.go` 追加：

```go
// TestSaveTaskRoundTripsDisciplineName 纪律块名字必须落盘：
// resumeForContinue 与 ResumeTask 只拿得到 executor 名，不落盘的话
// 一次 continue 或一次 agentd 重启就会让审阅任务静默退回实现块，
// 且首回合是对的、更难查。
func TestSaveTaskRoundTripsDisciplineName(t *testing.T) {
	st := newTestStore(t)
	task := proto.Task{
		ID: "t-disc", RepoPath: "/tmp/r", State: proto.TaskStateRunning,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
		Executor: "grok", DisciplineName: "review",
	}
	if err := st.SaveTask(task); err != nil {
		t.Fatalf("SaveTask: %v", err)
	}
	got, err := st.GetTask("t-disc")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.DisciplineName != "review" {
		t.Fatalf("纪律块名字未往返，实得 %q", got.DisciplineName)
	}
}

// TestSaveTaskEmptyDisciplineName 不点名的任务存空串，读回也是空串
// （空 = 走 executor 兜底，是有意义的取值，不能变成别的东西）。
func TestSaveTaskEmptyDisciplineName(t *testing.T) {
	st := newTestStore(t)
	task := proto.Task{
		ID: "t-plain", RepoPath: "/tmp/r", State: proto.TaskStateRunning,
		CreatedAt: time.Now(), UpdatedAt: time.Now(), Executor: "codex",
	}
	if err := st.SaveTask(task); err != nil {
		t.Fatalf("SaveTask: %v", err)
	}
	got, err := st.GetTask("t-plain")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.DisciplineName != "" {
		t.Fatalf("未点名的任务应为空串，实得 %q", got.DisciplineName)
	}
}
```

`newTestStore(t)` 是本文件已有的建库辅助；若名字不同就用现成的那个（**先 grep
`func newTestStore` 或同类辅助，别新造**）。

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/store/ -run TestSaveTask -count=1`
Expected: **编译失败**，`proto.Task` 没有 `DisciplineName` 字段。

- [ ] **Step 3: 加 proto 字段并修正说谎的注释**

`internal/proto/proto.go`，把现有 `Discipline` 那段改成：

```go
	// Discipline 是本任务实际注入的纪律块来源标注（如「内置:single-context」）。
	// **不落盘**：它只在派发响应里回显给协调者，agentd 重启后为空。
	//
	// 为什么要回显：配置化把纪律块从 plan 文件里拿走后，写 plan 的人再也看不见它，
	// dispatch 必须当场把「这次注入的是哪块」说出来；CLI 拿到的就是这个对象。
	Discipline string `json:"discipline,omitempty"`
	// DisciplineName 是派发时点名的纪律块角色名（如 review）；空=按 executor 兜底。
	// 该列后加，老任务为空——空是有意义的取值（走兜底），不回填、不编造。
	//
	// 为什么必须落盘：resumeForContinue 与 ResumeTask 只拿得到 executor 名，
	// 不落盘的话一次 continue 或一次 agentd 重启就会让点名的任务静默退回兜底块，
	// 而且首回合是对的，事后极难查。
	DisciplineName string `json:"discipline_name,omitempty"`
```

- [ ] **Step 4: 改 store 的四处**

① CREATE TABLE（`internal/store/store.go` 的 `tasks` 建表语句尾部，与
`usage_context_window` 同段）追加：

```sql
  -- discipline_name：派发时点名的纪律块角色名；空=按 executor 兜底。
  -- continue/resume 靠它重解析，不落盘会让点名任务在第二回合静默换块。
  discipline_name TEXT NOT NULL DEFAULT ''
```

② 迁移 map（约 208 行）加一行：

```go
		"discipline_name":      "TEXT NOT NULL DEFAULT ''",
```

③ INSERT（约 257 行）列清单末尾加 `discipline_name`，`VALUES` 加一个 `?`，
实参末尾加 `t.DisciplineName`。

④ `taskColumns`（约 274 行）末尾加 `, discipline_name`；`scanTaskRow` 的 `Scan`
参数末尾加 `&task.DisciplineName`。**两处必须同时改**——只改一处的表现是运行期
Scan 列数不匹配。

同时把 `taskColumns` 上方那句不完整的注释补准：

```go
// taskColumns 是 tasks 表的完整读取列清单：GetTask / ListTasks /
// ActiveTasksByWorkDir 共用同一份。为什么要共用：这份清单原先在两处各抄一遍，
// 每加一列就得同步四个位置，漏一处的表现是运行期 Scan 列数不匹配。
//
// 加一列要改**四处**：建表 DDL、迁移 map、INSERT（列清单 + 占位符 + 实参）、
// 本常量 + scanTaskRow。原注释只提了后两处，照着做会漏掉前两处。
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/store/ -count=1`
Expected: PASS，全包绿。

再跑一次全包确认没有别处依赖列数：

Run: `go test ./internal/agentd/ -count=1`
Expected: PASS。

- [ ] **Step 6: 加关键节点日志**

本 task 是纯存储字段，**不新增日志**——`SaveTask` / `GetTask` 已有的错误分支
覆盖了新列（列数不匹配会在 Scan 处报错并被现有错误路径带出）。

**这一步不是跳过，是判断**：写下判断依据，别默认「加了就对」。若实现时发现
迁移失败没有独立日志，补一条 Error（带列名与 cause）。

- [ ] **Step 7: 加注释**

- `proto.Task.DisciplineName` 的字段注释（已在 Step 3 给出）：含「为什么必须落盘」
- CREATE TABLE 里那两行 SQL 注释（已在 Step 4 给出）
- `taskColumns` 上方补准的「加一列要改四处」（已在 Step 4 给出）
- `Discipline` 字段注释订正为「不落盘」（已在 Step 3 给出）

- [ ] **Step 8: 提交**

```bash
git add internal/proto/proto.go internal/store/store.go internal/store/store_test.go
git commit -m "feat(store): tasks 表加 discipline_name 列，纪律块名字随任务落盘"
```

---

### Task 3: `DispatchReq.Discipline` 与三处解析点统一

**Files:**
- Modify: `internal/agentd/manager.go`（`DispatchReq` 加字段；新增 `resolveDisciplineFor`；
  三处 `m.discipline.For(execName)` 改为调它：约 684 / 1182 / 3229 行）
- Test: `internal/agentd/manager_test.go`（文件已存在，追加用例）

**Interfaces:**
- Consumes: Task 1 的 `(*discipline.Resolver).ByName(name, executor string) (Block, error)`、
  `discipline.NameReview`；Task 2 的 `proto.Task.DisciplineName`
- Produces:
  - `DispatchReq.Discipline string`（**名字**）
  - `func (m *Manager) resolveDisciplineFor(name, execName string) (discipline.Block, error)`

**先读懂再动**：三处调用点的上下文不一样——

| 位置 | 函数 | 名字从哪来 |
|---|---|---|
| 约 684 行 | `Dispatch` | `req.Discipline`（派发请求） |
| 约 1182 行 | `resumeForContinue` | `task.DisciplineName`（落盘的那份） |
| 约 3229 行 | `ResumeTask` | `task.DisciplineName`（落盘的那份） |

后两处**必须**从 task 读，不能重新算——它们没有派发请求。

- [ ] **Step 1: 写失败的测试**

在 `internal/agentd/manager_test.go` 追加：

```go
// TestDispatchNamedDisciplineInjectsNamedBlock 点名 review 时注入的是只读块，
// 而不是按 executor 兜底的实现块。
func TestDispatchNamedDisciplineInjectsNamedBlock(t *testing.T) {
	clone := initClonedRepo(t, "main")
	m := compensateOnlyManager(t)
	pid := registerTestProject(t, m, clone)

	task, err := m.Dispatch(context.Background(), DispatchReq{
		ProjectID: pid, Prompt: "x", Executor: "fake", NewWorktree: true,
		Discipline: discipline.NameReview,
	})
	if err != nil {
		t.Fatalf("派发应成功: %v", err)
	}
	if task.DisciplineName != discipline.NameReview {
		t.Fatalf("名字应落到 task 上，实得 %q", task.DisciplineName)
	}
	if task.Discipline != "内置:review" {
		t.Fatalf("来源标注应为 内置:review，实得 %q", task.Discipline)
	}
}

// TestDispatchUnnamedDisciplineUnchanged 不点名时行为逐字不变——
// 这是本次重构的兼容底线：所有现存部署走的都是这条路。
func TestDispatchUnnamedDisciplineUnchanged(t *testing.T) {
	clone := initClonedRepo(t, "main")
	m := compensateOnlyManager(t)
	pid := registerTestProject(t, m, clone)

	task, err := m.Dispatch(context.Background(), DispatchReq{
		ProjectID: pid, Prompt: "x", Executor: "fake", NewWorktree: true,
	})
	if err != nil {
		t.Fatalf("派发应成功: %v", err)
	}
	if task.DisciplineName != "" {
		t.Fatalf("不点名时 DisciplineName 应为空，实得 %q", task.DisciplineName)
	}
	if !strings.HasPrefix(task.Discipline, "内置:") {
		t.Fatalf("兜底来源标注变了：%q", task.Discipline)
	}
}

// TestDispatchUnknownDisciplineNameRejected 点了不存在的名字：拒发，不静默兜底。
func TestDispatchUnknownDisciplineNameRejected(t *testing.T) {
	clone := initClonedRepo(t, "main")
	m := compensateOnlyManager(t)
	pid := registerTestProject(t, m, clone)

	_, err := m.Dispatch(context.Background(), DispatchReq{
		ProjectID: pid, Prompt: "x", Executor: "fake", NewWorktree: true,
		Discipline: "no-such-role",
	})
	if err == nil {
		t.Fatal("未知纪律块名字应拒发")
	}
	if !strings.Contains(err.Error(), "no-such-role") {
		t.Fatalf("错误里应带名字：%v", err)
	}
}

// TestResolveDisciplineForPrefersName 直接打 resolveDisciplineFor 的两条分支：
// 有名字走 ByName、无名字走 For。这条守的是三个调用点共用的那段判定。
func TestResolveDisciplineForPrefersName(t *testing.T) {
	m := compensateOnlyManager(t)

	named, err := m.resolveDisciplineFor(discipline.NameReview, "codex")
	if err != nil {
		t.Fatalf("有名字: %v", err)
	}
	if named.Source != "内置:review" {
		t.Fatalf("有名字应走 ByName，实得 %q", named.Source)
	}

	fallback, err := m.resolveDisciplineFor("", "codex")
	if err != nil {
		t.Fatalf("无名字: %v", err)
	}
	if fallback.Source != "内置:"+discipline.TierSingleContext {
		t.Fatalf("无名字应走 For，实得 %q", fallback.Source)
	}
}
```

`initClonedRepo` / `compensateOnlyManager` / `registerTestProject` 是本文件已有的辅助
（`TestDispatchBaseBranchNameYieldsRequestedBranch` 用的就是它们）。若 `manager_test.go`
还没 import `discipline` 与 `strings`，补上。

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/agentd/ -run 'TestDispatch.*Discipline|TestResolveDisciplineFor' -count=1`
Expected: **编译失败**，`DispatchReq` 没有 `Discipline` 字段、`resolveDisciplineFor` 未定义。

- [ ] **Step 3: 实现**

`DispatchReq` 加字段（放在 `Executor` 附近）：

```go
	// Discipline 是本次派发点名的纪律块**角色名**（如 review）；空=按 executor 兜底。
	//
	// 为什么是名字而不是路径或正文：路径要跨机器解析（协调者的仓内相对路径在
	// agentd 上没有意义），正文要跨网络搬运且没法被机器级覆盖。名字让 agentd
	// 成为纪律块的唯一拥有者，调用方只说「我要哪个角色」。
	Discipline string
```

新增共用判定（放在 `Dispatch` 之前）：

```go
// resolveDisciplineFor 按「有名字用名字、无名字按 executor 兜底」裁出纪律块。
//
// 参数：name 角色名（空=不点名）；execName 执行者名。
// 返回：解析出的块；名字非法 / 未知 / 覆盖文件坏掉时返回错误（调用方拒发）。
//
// 为什么要收口成一个函数：三个调用点（Dispatch / resumeForContinue / ResumeTask）
// 必须用同一套判定。分开写的表现是首回合注入了点名的块、continue 之后悄悄换成
// 兜底块——首回合是对的，事后极难查。
func (m *Manager) resolveDisciplineFor(name, execName string) (discipline.Block, error) {
	if strings.TrimSpace(name) != "" {
		return m.discipline.ByName(name, execName)
	}
	return m.discipline.For(execName)
}
```

三处调用点改法：

① `Dispatch`（约 684 行）：

```go
	discBlock, err := m.resolveDisciplineFor(req.Discipline, execName)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errDisciplineResolveFailed, err)
	}
```

并在构造 task 的那两处（约 839 与 897 行，`Discipline: discBlock.Source` /
`task.Discipline = discBlock.Source`）**同时**写上名字：

```go
		Discipline:     discBlock.Source,
		DisciplineName: req.Discipline,
```

```go
	task.Discipline = discBlock.Source
	task.DisciplineName = req.Discipline
```

② `resumeForContinue`（约 1182 行）与 ③ `ResumeTask`（约 3229 行）：两处都从 task 取名字。
先确认该函数作用域里 task 对象叫什么（`resumeForContinue` 里要先 `GetTask`，
`ResumeTask` 里已有 task），然后：

```go
	discBlock, derr := m.resolveDisciplineFor(task.DisciplineName, execName)
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -count=1`
Expected: PASS，全包绿。

- [ ] **Step 5: 加关键节点日志**

在三处调用点各补一条 Info（`resolveDisciplineFor` 内部不打——它是纯判定，
打了会在 resume 循环里刷屏）：

- `Dispatch`：解析成功后 `m.log.Info("纪律块已裁定", "task", taskID, "name", req.Discipline, "source", discBlock.Source)`
  （`name` 为空时值就是空串，一眼看得出走的兜底）
- `resumeForContinue` / `ResumeTask`：各补
  `m.log.Info("续接/恢复重解析纪律块", "task", taskID, "name", task.DisciplineName, "source", discBlock.Source)`
  —— **这两条是排查「第二回合换块」的唯一线索**，不能省

三处的 Error 分支沿用既有的 `errDisciplineResolveFailed` 包装，确认错误里带得出名字
（`ByName` 的错误文案已含名字）。

- [ ] **Step 6: 加注释**

- `DispatchReq.Discipline` 字段注释（已在 Step 3 给出）：含「为什么是名字」
- `resolveDisciplineFor` 的 doc 注释（已在 Step 3 给出）：含「为什么要收口」
- `resumeForContinue` / `ResumeTask` 两处改动上方各加一行行内注释：
  `// 名字必须从落盘的 task 上取：这里没有派发请求，重新按 executor 算会换块`

- [ ] **Step 7: 提交**

```bash
git add internal/agentd/manager.go internal/agentd/manager_test.go
git commit -m "feat(agentd): 派发可点名纪律块，续接与恢复从 task 重解析"
```

---

### Task 4: 模板字段改名与老行映射

**Files:**
- Modify: `internal/ledger/templates.go`（`TemplateDef` 加 `Discipline`、保留 `DisciplinePath` 作废弃字段；
  `GetTemplate` 读取后做映射；出厂模板改用名字）
- Test: `internal/ledger/templates_test.go`（文件已存在，追加用例）

**Interfaces:**
- Consumes: Task 1 的 `discipline.NameImplement` / `discipline.NameReview`
- Produces: `TemplateDef.Discipline string`（json tag `discipline,omitempty`）；
  `TemplateDef.DisciplinePath` 保留但标记废弃

**先读懂再动**：模板 def 存 JSON，`jsonUnmarshal`（`internal/ledger/workflows.go:145`）
是**宽松解码**——直接换字段名，老行会静默解成 `Discipline` 为空 → 退回 executor 兜底 →
审阅模板悄悄拿到实现块，正是本 plan 要修的缺陷换个方式复活。所以必须保留旧字段并映射。

- [ ] **Step 1: 写失败的测试**

在 `internal/ledger/templates_test.go` 追加：

```go
// TestTemplateLegacyDisciplinePathMaps 老模板行用的是 discipline_path，
// 宽松 JSON 解码会把它静默丢掉——必须映射成名字，否则审阅模板会悄悄
// 退回 executor 兜底的实现块（正是本轮要修的缺陷换个方式复活）。
func TestTemplateLegacyDisciplinePathMaps(t *testing.T) {
	for _, tc := range []struct{ path, want string }{
		{"docs/superpowers/discipline/block-review.md", "review"},
		{"docs/superpowers/discipline/block-a.md", "implement"},
		{"docs/superpowers/discipline/block-b.md", "implement"},
	} {
		got := disciplineNameFromLegacyPath(tc.path)
		if got != tc.want {
			t.Fatalf("路径 %s 应映射为 %q，实得 %q", tc.path, tc.want, got)
		}
	}
}

// TestTemplateLegacyUnknownPathMapsEmpty 认不出来的旧值映射为空（退回兜底），
// 但调用方必须打 Warn——猜不出来可以退，不能不出声。
func TestTemplateLegacyUnknownPathMapsEmpty(t *testing.T) {
	if got := disciplineNameFromLegacyPath("some/custom/block.md"); got != "" {
		t.Fatalf("未知路径应映射为空，实得 %q", got)
	}
}

// TestGetTemplateMapsLegacyRow 存了老字段的行读出来要带上名字。
func TestGetTemplateMapsLegacyRow(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.PutTemplate("legacy-review", TemplateDef{
		Executor: "grok", Purpose: PurposeReview, BranchPrefix: "cards",
		DisciplinePath: "docs/superpowers/discipline/block-review.md",
		Prompt:         "审阅",
	}); err != nil {
		t.Fatalf("PutTemplate: %v", err)
	}
	tpl, err := st.GetTemplate("legacy-review", 0)
	if err != nil {
		t.Fatalf("GetTemplate: %v", err)
	}
	if tpl.Def.Discipline != "review" {
		t.Fatalf("老行应映射出 review，实得 %q", tpl.Def.Discipline)
	}
}

// TestGetTemplateNewFieldWins 新字段非空时不看旧字段。
func TestGetTemplateNewFieldWins(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.PutTemplate("both", TemplateDef{
		Executor: "grok", Purpose: PurposeReview, BranchPrefix: "cards",
		Discipline: "review", DisciplinePath: "docs/superpowers/discipline/block-a.md",
		Prompt:     "审阅",
	}); err != nil {
		t.Fatalf("PutTemplate: %v", err)
	}
	tpl, err := st.GetTemplate("both", 0)
	if err != nil {
		t.Fatalf("GetTemplate: %v", err)
	}
	if tpl.Def.Discipline != "review" {
		t.Fatalf("新字段应胜出，实得 %q", tpl.Def.Discipline)
	}
}

// TestDefaultTemplatesUseNames 出厂模板用名字，不再指路径。
func TestDefaultTemplatesUseNames(t *testing.T) {
	st := newTestStore(t)
	if err := st.EnsureDefaultTemplates(); err != nil {
		t.Fatalf("EnsureDefaultTemplates: %v", err)
	}
	for name, want := range map[string]string{
		"feature-impl":   "implement",
		"review-generic": "review",
	} {
		tpl, err := st.GetTemplate(name, 0)
		if err != nil {
			t.Fatalf("GetTemplate(%s): %v", name, err)
		}
		if tpl.Def.Discipline != want {
			t.Fatalf("%s 的纪律块名字应为 %q，实得 %q", name, want, tpl.Def.Discipline)
		}
		if tpl.Def.DisciplinePath != "" {
			t.Fatalf("%s 不该再带旧路径字段，实得 %q", name, tpl.Def.DisciplinePath)
		}
	}
}
```

`newTestStore(t)` 是本文件已有的建库辅助（`templates_test.go:9`），照用别新造。
`PurposeReview` 在 `internal/ledger/types.go:31`。本文件是 `package ledger`（内部测试），
所以 `TemplateDef` / `PurposeReview` 都不带包名前缀。

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/ledger/ -run 'TestTemplate|TestGetTemplate|TestDefaultTemplates' -count=1`
Expected: **编译失败**，`disciplineNameFromLegacyPath` 未定义、`TemplateDef` 没有
`Discipline` 字段。

- [ ] **Step 3: 实现**

`TemplateDef` 改为：

```go
	// Discipline 是派发该模板时点名的纪律块**角色名**（如 implement / review）；
	// 空=按 executor 兜底。
	Discipline string `json:"discipline,omitempty"`
	// DisciplinePath 是**已废弃**的旧字段（仓内相对路径）。
	//
	// 保留它不是为了兼容语义，是为了**不静默降级**：模板 def 存 JSON 且用宽松
	// 解码，直接删字段会让老行解出空 Discipline、退回 executor 兜底——审阅模板
	// 悄悄拿到实现块，正是本次重构要修的缺陷换个方式复活。读取时映射并 Warn，
	// 提示用户 template put 重写；确认线上无残留后再删。
	DisciplinePath string `json:"discipline_path,omitempty"`
```

新增映射函数（放在 `TemplateDef` 之后）：

```go
// legacyDisciplinePaths 是废弃的 discipline_path 取值 → 角色名的映射表。
// 只认这三个出厂过的文件名：认不出来的自定义路径没法猜，映射为空退回兜底。
var legacyDisciplinePaths = map[string]string{
	"block-review.md": "review",
	"block-a.md":      "implement",
	"block-b.md":      "implement",
}

// disciplineNameFromLegacyPath 把废弃的 discipline_path 换算成角色名。
//
// 参数：path 旧字段原值（形如 docs/superpowers/discipline/block-review.md）。
// 返回：角色名；认不出来时返回空串（调用方负责 Warn 并退回兜底）。
//
// 只按 basename 匹配已知的三个出厂文件名：用户自定义的路径指向的是什么纪律
// 我们不知道，猜错比退回兜底更危险。
func disciplineNameFromLegacyPath(path string) string {
	if path == "" {
		return ""
	}
	return legacyDisciplinePaths[filepath.Base(path)]
}
```

`GetTemplate` 在 `jsonUnmarshal` 之后补映射（找到 `templates.go:76` 那行
`if err := jsonUnmarshal(raw, &t.Def); err != nil {` 的后面）：

```go
	// 老行只有废弃的 discipline_path：映射成名字，映不出来就退回兜底，
	// 两种情况都出声——静默降级会让审阅模板悄悄拿到实现块。
	if t.Def.Discipline == "" && t.Def.DisciplinePath != "" {
		if name := disciplineNameFromLegacyPath(t.Def.DisciplinePath); name != "" {
			t.Def.Discipline = name
			log().Warn("模板用了废弃字段 discipline_path，已按文件名映射为角色名；建议 template put 重写",
				"template", t.Name, "legacy_path", t.Def.DisciplinePath, "name", name)
		} else {
			log().Warn("模板用了废弃字段 discipline_path 且认不出对应角色，本次派发将按 executor 兜底；建议 template put 重写",
				"template", t.Name, "legacy_path", t.Def.DisciplinePath)
		}
	}
```

若本包没有 `log()` 入口，用与同包其他文件一致的日志写法（先 grep
`internal/ledger/` 里现有的日志调用，照抄那个形态）。

出厂模板改为用名字：

```go
		"feature-impl": {
			Executor: "opencode", Purpose: "implement", BranchPrefix: "cards",
			Discipline: discipline.NameImplement,
			Prompt:     "实现以下工作项：{{TITLE}}（卡 {{CARD}}）。\n验收判据：{{ACCEPT}}\n完整需求见随附 plan。",
		},
		"review-generic": {
			Executor: "grok", Purpose: "review", BranchPrefix: "cards",
			// 审阅用只读角色：实现纪律写着「每个 task 完成即 commit」，
			// 派给审阅者会让它在审阅分支上真的提交东西（2026-08-19 真机实测出现过一次）
			// ——审阅的产出是裁决报文，不是提交
			Discipline: discipline.NameReview,
			Prompt: "审阅卡 {{CARD}}（{{TITLE}}）对应分支的完整 diff：spec 符合性（要求全实现、没有多做）+ 代码质量双裁决。\n" +
				"验收判据：{{ACCEPT}}\n" + reviewVerdictContract,
		},
```

需要 import `path/filepath` 与 `github.com/Xsxdot/handoff/internal/discipline`。

**检查有没有引入 import 环**：`internal/discipline` 不 import `internal/ledger`
（它只 import 标准库，见 `discipline.go` 与 `resolver.go` 的 import 段），所以
`ledger → discipline` 是安全的。跑 `go build ./...` 确认。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/ledger/ -count=1`
Expected: PASS，全包绿。

- [ ] **Step 5: 加关键节点日志**

已包含在 Step 3 的实现里，逐条确认：

- 老字段映射成功：Warn，带 template / legacy_path / name
- 老字段认不出来：Warn，带 template / legacy_path，并说明「将按 executor 兜底」

**两条都是 Warn 不是 Info**：静默降级是本次重构最大的风险面，这两行是它唯一的
可观测出口。成功路径（新字段直接命中）不打日志——它是常态，打了会刷屏。

- [ ] **Step 6: 加注释**

- `Discipline` / `DisciplinePath` 两个字段注释（已在 Step 3 给出）：后者要写清
  「保留它不是为了兼容语义，是为了不静默降级」
- `legacyDisciplinePaths` 与 `disciplineNameFromLegacyPath`（已在 Step 3 给出）：
  含「为什么只认三个已知文件名」
- `GetTemplate` 里映射块上方的行内注释（已在 Step 3 给出）

- [ ] **Step 7: 提交**

```bash
git add internal/ledger/templates.go internal/ledger/templates_test.go
git commit -m "feat(ledger): 模板改用纪律块角色名，老 discipline_path 映射并告警"
```

---

### Task 5: CLI 与 HTTP 透传名字，停止拼 prompt

**Files:**
- Modify: `cmd/card_dispatch.go`（`dispatchRequest` 加 `discipline`；新增
  `swapDispatchTransportWithOpts` 测试缝；`dispatchViaTemplate` 删掉读文件与 prompt 拼接；
  `--discipline-override` 语义改为名字；文件头注释订正）
- Modify: `internal/ledger/events.go:111-121`（`DispatchSnapshot.DisciplineHash` → `DisciplineName`）
- Modify: `internal/client/client.go:668-688`（`DispatchOpts` 加 `Discipline`）
- Modify: `internal/agentd/server.go:990-1010` 与 `:1028-1034`（wire 结构加字段并透传）
- Test: `cmd/card_dispatch_test.go`（追加用例 + **处置两条转红的既有用例**）
- Test: `internal/ledger/events_test.go:125-143`（处置转红的快照用例）

**Interfaces:**
- Consumes: Task 3 的 `DispatchReq.Discipline`、Task 4 的 `TemplateDef.Discipline`
- Produces:
  - 线上 JSON 键 `discipline`（`DispatchOpts.Discipline` ↔ server 端 `dispatchRequest.Discipline`）
  - `func swapDispatchTransportWithOpts(fn func(dispatchRequest) (string, error)) func()`
  - `DispatchSnapshot.DisciplineName string`（json 键 `discipline_name`）

**先读懂再动**，这个 task 的地形比前四个复杂，三件事必须先看清：

① **靶心在 `cmd/card_dispatch.go:138-150`**：

```go
	disciplinePath := tpl.Def.DisciplinePath
	if disciplineOverride != "" { disciplinePath = disciplineOverride }
	discipline, err := os.ReadFile(disciplinePath)
	...
	prompt := string(discipline) + "\n\n---\n\n" + body
```

整段删除。

② **既有测试缝带不了新字段**。`cmd/card_dispatch.go:85-95` 的 `swapDispatchTransport`
收的是四标量回调，并且在第 89-91 行把 `dispatchTransportWithOpts` **也**降级成那四个标量：

```go
	dispatchTransportWithOpts = func(req dispatchRequest) (string, error) {
		return dispatchTransport(req.prompt, req.branch, req.target, req.project)
	}
```

——`discipline` 走到这里就被丢了，用它写不出本 task 的判据。
**不要去拓宽这条缝**：它的 doc 注释写明「保留这个四参数缝是为了让单测只关心
prompt、分支、目标机与项目四个派发前事实」，拓宽等于把两条既有用例的回调也一起改，
而它们并不关心新字段。**新增第二条缝**，只替换 `dispatchTransportWithOpts`。

③ **`DisciplineHash` 是有据可查的账本字段，不能填空串了事**。
`internal/ledger/events.go:109-110` 写着「『B107 那次派发用的哪版纪律块』从这里答
（蓝图 §3.3 取证文化）」。正文不再经过 CLI，指纹确实算不出来了——但**那个问题依然要答得上**，
新模型下答案就是名字。所以是**换字段**，不是删能力。

- [ ] **Step 1: 加第二条测试缝**

在 `cmd/card_dispatch.go` 的 `swapDispatchTransport` 之后追加：

```go
// swapDispatchTransportWithOpts 只替换携带完整请求的派发段；测试恢复原实现。
//
// 为什么不复用 swapDispatchTransport：那条缝刻意只暴露 prompt/branch/target/project
// 四个标量（见它的注释），纪律块名字这类新字段到不了回调手上。两条缝并存，
// 各测各的关注面，既有用例不必为新字段改回调。
func swapDispatchTransportWithOpts(fn func(dispatchRequest) (string, error)) func() {
	old := dispatchTransportWithOpts
	dispatchTransportWithOpts = fn
	return func() { dispatchTransportWithOpts = old }
}
```

- [ ] **Step 2: 写失败的测试**

在 `cmd/card_dispatch_test.go` 追加。走 `runLedgerCLI`（该文件既有的驱动方式），
不要直接调 `dispatchViaTemplate`：

```go
// TestCardDispatchSendsDisciplineName 模板的角色名要随派发请求上送，
// 而不是被 CLI 读成正文拼进 prompt。
func TestCardDispatchSendsDisciplineName(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runLedgerCLI(t, dir, "card", "add", "要派的卡", "--project", "demo")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &c); err != nil {
		t.Fatal(err)
	}

	var got dispatchRequest
	restore := swapDispatchTransportWithOpts(func(req dispatchRequest) (string, error) {
		got = req
		return "T-fake-1", nil
	})
	defer restore()

	if _, _, err := runLedgerCLI(t, dir, "card", "dispatch", c.ID,
		"--template", "feature-impl", "--target", "mac-02"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if got.discipline != "implement" {
		t.Fatalf("请求里应带角色名 implement，实得 %q", got.discipline)
	}
}

// TestCardDispatchNoDisciplineInPrompt prompt 里不许再出现纪律块正文。
// 这是本次重构的核心判据：两份纪律块同时在场时，审阅那次的「只读，不写」
// 会被实现块的「每个 task 完成即 commit」推翻。
func TestCardDispatchNoDisciplineInPrompt(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runLedgerCLI(t, dir, "card", "add", "要派的卡", "--project", "demo")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &c); err != nil {
		t.Fatal(err)
	}

	var got dispatchRequest
	restore := swapDispatchTransportWithOpts(func(req dispatchRequest) (string, error) {
		got = req
		return "T-fake-2", nil
	})
	defer restore()

	if _, _, err := runLedgerCLI(t, dir, "card", "dispatch", c.ID,
		"--template", "feature-impl", "--target", "mac-02"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	for _, mark := range []string{"# 审阅纪律", "# 执行纪律", "只读，不写", "每个 task 完成即 commit"} {
		if strings.Contains(got.prompt, mark) {
			t.Fatalf("prompt 里不该再有纪律块正文，命中 %q：\n%s", mark, got.prompt)
		}
	}
	if !strings.Contains(got.prompt, "要派的卡") {
		t.Fatalf("模板正文应还在：\n%s", got.prompt)
	}
}

// TestCardDispatchOverrideReplacesName --discipline-override 改的是名字，
// 不再是文件路径。
func TestCardDispatchOverrideReplacesName(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runLedgerCLI(t, dir, "card", "add", "要派的卡", "--project", "demo")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &c); err != nil {
		t.Fatal(err)
	}

	var got dispatchRequest
	restore := swapDispatchTransportWithOpts(func(req dispatchRequest) (string, error) {
		got = req
		return "T-fake-3", nil
	})
	defer restore()

	if _, _, err := runLedgerCLI(t, dir, "card", "dispatch", c.ID,
		"--template", "feature-impl", "--target", "mac-02",
		"--discipline-override", "review"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if got.discipline != "review" {
		t.Fatalf("override 应替换名字，实得 %q", got.discipline)
	}
}

// TestCardDispatchSnapshotRecordsDisciplineName 派发事件快照要答得出
// 「这次用的哪块纪律」——正文不再经过 CLI，指纹算不出来了，
// 但那个问题本身没消失，答案换成名字。
func TestCardDispatchSnapshotRecordsDisciplineName(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runLedgerCLI(t, dir, "card", "add", "要派的卡", "--project", "demo")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &c); err != nil {
		t.Fatal(err)
	}
	restore := swapDispatchTransportWithOpts(func(req dispatchRequest) (string, error) {
		return "T-fake-4", nil
	})
	defer restore()
	if _, _, err := runLedgerCLI(t, dir, "card", "dispatch", c.ID,
		"--template", "feature-impl", "--target", "mac-02"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	show, _, err := runLedgerCLI(t, dir, "card", "show", c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(show, `"discipline_name":"implement"`) {
		t.Fatalf("快照应记下角色名: %q", show)
	}
}
```

- [ ] **Step 3: 跑测试确认它失败**

Run: `go test ./cmd/ -run TestCardDispatch -count=1`
Expected: **编译失败**（`dispatchRequest` 没有 `discipline` 字段、
`swapDispatchTransportWithOpts` 在 Step 1 之后才存在则改为断言失败）。若已编译通过，
预期至少 `TestCardDispatchNoDisciplineInPrompt` 红在命中 `# 执行纪律`。

- [ ] **Step 4: 实现**

① `dispatchRequest` 加字段（放在 `executor` 附近）：

```go
	// discipline 是本次派发点名的纪律块角色名；空=让 agentd 按 executor 兜底。
	// 只传名字不传正文：正文由 agentd 解析注入，CLI 不再是纪律块的搬运工。
	discipline string
```

② `dispatchViaTemplate` 里把①那段整体换成：

```go
	// 纪律块只传名字，正文由 agentd 解析注入。CLI 曾在这里读文件并拼进 prompt，
	// 而 agentd 又会按 executor 注入一份——两份同时在场，审阅那次的「只读，不写」
	// 被实现块的「每个 task 完成即 commit」直接推翻（2026-08-19 真机实测过一次）。
	disciplineName := tpl.Def.Discipline
	if disciplineOverride != "" {
		disciplineName = disciplineOverride
	}
```

prompt 合成改回纯模板正文（原为 `string(discipline) + "\n\n---\n\n" + body`）：

```go
	prompt := body
```

③ `dispatchTransportWithOpts(dispatchRequest{...})` 的实参加 `discipline: disciplineName,`。

④ 落快照那处（`RecordDispatch` 的实参）把 `DisciplineHash: disciplineHash` 换成
`DisciplineName: disciplineName`。

⑤ 删掉不再使用的 import：`crypto/sha256`、`encoding/hex`。
**`os` 别盲删**——`os.ReadFile(planPath)` 还在用。跑 `go build ./cmd/` 确认。

⑥ `--discipline-override` 的帮助文案：

```go
	cardDispatchCmd.Flags().StringVar(&cardDispatchDiscipline, "discipline-override", "",
		"覆盖模板指定的纪律块角色名（如 review；测试/应急）")
```

⑦ `cmd/card_dispatch.go` 的文件头注释第一行现在是
「card dispatch：按模板拼装 prompt + 纪律块，走既有 dispatch 通道；」——**它已经不成立了**，
改成：

```go
// card dispatch：按模板拼装 prompt，带上纪律块**角色名**（正文由 agentd 注入），
// 走既有 dispatch 通道；
```

⑧ `internal/ledger/events.go` 的 `DispatchSnapshot`：

```go
// DispatchSnapshot 派发事件快照：模板版本 + 纪律块角色名 + 落点。
// 「B107 那次派发用的哪块纪律」从这里答（蓝图 §3.3 取证文化）。
//
// 原字段是 discipline_hash（正文指纹）；纪律块改为具名资源后正文不再经过 CLI，
// 指纹无从算起，同一个问题的答案换成名字。老事件里的 discipline_hash 键留在
// 已落盘的 payload 里，只是不再写新的——事件是追加式的，不做回填。
type DispatchSnapshot struct {
	Template        string `json:"template"`
	TemplateVersion int    `json:"template_version"`
	DisciplineName  string `json:"discipline_name"`
	...
```

⑨ `internal/client/client.go` 的 `DispatchOpts` 加字段，并在它拼 HTTP body 处加
`"discipline"` 键（**先看该文件里 body 是怎么拼的**，照现有形态加一项）：

```go
	// Discipline 是本次派发点名的纪律块角色名；空=按 executor 兜底。
	Discipline string
```

⑩ `internal/agentd/server.go` 的 `dispatchRequest` 加字段，并在
`s.mgr.Dispatch(r.Context(), DispatchReq{...})` 的实参里加 `Discipline: req.Discipline,`：

```go
	// Discipline 是派发点名的纪律块角色名；空=按 executor 兜底。
	Discipline string `json:"discipline"`
```

- [ ] **Step 5: 处置转红的既有测试（判定，不是顺手改绿）**

三条既有用例会红。**每一条先回答「它守的语义是什么」**，把答案写进提交信息。
只有确认语义确实不再成立才允许动测试；语义仍成立就得改代码。

**① `cmd/card_dispatch_test.go:48` `TestCardDispatchClaimAndSnapshot`**

| 断言 | 守的语义 | 是否仍成立 |
|---|---|---|
| `HasPrefix(gotPrompt, "# 执行纪律")` | CLI 把纪律块正文拼在 prompt 开头 | **不成立**——本 task 就是要删掉这个行为 |
| `Contains(gotPrompt, "要派的卡"/"测试全绿")` | 模板变量替换生效 | 仍成立，一字不动 |
| `gotProject == "demo"` | 派发带上项目 | 仍成立，一字不动 |
| `Contains(show, "discipline_hash")` | 快照答得出「用的哪块纪律」 | **语义成立、字段变了**→ 改成 `discipline_name` |
| 重复派发报「认领」、`"Status":"进行中"` | 派发即认领 | 仍成立，一字不动 |

改动：把首个断言**反过来**，并顺带把 `--discipline-override` 的实参从文件路径改成名字。

```go
	if strings.HasPrefix(gotPrompt, "# 执行纪律") || strings.Contains(gotPrompt, "# 执行纪律") {
		t.Fatalf("纪律块正文不该再进 prompt: %q", gotPrompt)
	}
	if !strings.Contains(gotPrompt, "要派的卡") || !strings.Contains(gotPrompt, "测试全绿") {
		t.Fatalf("prompt 拼装: %q", gotPrompt)
	}
```

`--discipline-override dp`（`dp` 是 tmpdir 下的绝对路径）在新语义下是**非法名字**，
两处调用都改成 `--discipline-override implement`。测试开头那两行造
`block-a.md` 的 `os.WriteFile` 随之删除（连同不再需要的 import）。
`Contains(show, "discipline_hash")` 改为 `Contains(show, "discipline_name")`。

**② `cmd/card_dispatch_test.go:96,115` `TestCardDispatchFailureReleasesLease`**

它守的语义是「派发失败要连租约一起退」，与纪律块无关，**断言一字不动**。
只把 `--discipline-override dp` 三处改成 `--discipline-override implement`，
并删掉造 `block-a.md` 的那两行。

**③ `internal/ledger/events_test.go:127,141`**

它守的语义是「派发快照能答出用的哪块纪律 + 模板版本」，**仍成立**，只是字段换了名：
`DisciplineHash: "1f3c9d"` → `DisciplineName: "review"`，
`payload["discipline_hash"] != "1f3c9d"` → `payload["discipline_name"] != "review"`。

- [ ] **Step 6: 跑测试确认通过**

Run: `go test ./cmd/ ./internal/client/ ./internal/agentd/ ./internal/ledger/ -count=1`
Expected: PASS。

- [ ] **Step 7: 加关键节点日志**

在 `dispatchViaTemplate` 决定名字之后补一条（用本文件既有的日志形态；先 grep
`cmd/` 里现有的日志调用，照抄那个形态，**不要 `fmt.Printf`**）：

```go
	slog.Default().Info("模板派发已裁定纪律块角色", "card", c.ID, "template", tplName,
		"discipline", disciplineName, "overridden", disciplineOverride != "")
```

**为什么这条不能省**：纪律块正文不再经过 CLI，协调者在本地看不到任何纪律块痕迹；
这行加上 agentd 那边的「纪律块: <来源>」回显，构成点名链路的完整取证。

- [ ] **Step 8: 加注释**

- `dispatchRequest.discipline` 字段注释（Step 4 ①）
- 替换掉读文件那段的行内注释（Step 4 ②）：写清**为什么**删，含真机实测那句
- `cmd/card_dispatch.go` 文件头注释订正（Step 4 ⑦）
- `DispatchSnapshot` 的 doc 注释（Step 4 ⑧）：含「换字段不是删能力」与「老事件不回填」
- `swapDispatchTransportWithOpts` 的 doc 注释（Step 1）：含「为什么不复用既有缝」
- `DispatchOpts.Discipline` 与 server 端 `dispatchRequest.Discipline` 的字段注释

- [ ] **Step 9: 提交**

```bash
git add cmd/card_dispatch.go cmd/card_dispatch_test.go internal/client/client.go \
  internal/agentd/server.go internal/ledger/events.go internal/ledger/events_test.go
git commit -m "refactor(cli): 派发只传纪律块角色名，快照改记名字"
```

---

### Task 6: 删除仓内纪律块副本并做红线审计

**Files:**
- Delete: `docs/superpowers/discipline/block-a.md`、`block-b.md`、`block-review.md`（整个目录）
- Test: 无新测试；本 task 的判据是红线 grep

**Interfaces:**
- Consumes: Task 1-5 的全部产出
- Produces: 无

**先读懂再动**：`block-a.md` / `block-b.md` 与内置 `subagent.md` / `single-context.md`
实测只差 4 行给人看的「适用执行器」提示，正文一字不差；`block-review.md` 的正文已在
Task 1 复制进 `internal/discipline/builtin/review.md`。删除前**再比一次**，确认没漏改。

- [ ] **Step 1: 删除前逐字比对**

```bash
diff docs/superpowers/discipline/block-review.md internal/discipline/builtin/review.md
```

Expected: **无输出**（Task 1 是原样复制）。有输出就停下——说明 Task 1 复制时改了字，
先查清楚再继续。

- [ ] **Step 2: 删除目录**

```bash
git rm -r docs/superpowers/discipline/
```

- [ ] **Step 3: 红线审计**

逐条跑，每条都必须**无输出**：

```bash
grep -rn 'block-a\.md\|block-b\.md\|block-review\.md' --include='*.go' --include='*.md' . | grep -v '^./docs/superpowers/specs/' | grep -v '^./docs/superpowers/plans/'
```
（spec 与 plan 里提到这些文件名是叙述历史，允许保留。）

```bash
grep -rn 'os.ReadFile(disciplinePath)\|DisciplinePath' --include='*.go' cmd/
```
（CLI 侧不该再有任何 `DisciplinePath` 引用。）

```bash
grep -rn 'discipline_hash\|DisciplineHash' --include='*.go' .
```
（Task 5 已把快照字段换成名字，代码里不该再有指纹字段的引用。）

```bash
grep -rn 'fmt.Printf' --include='*.go' internal/discipline/ internal/ledger/templates.go cmd/card_dispatch.go
```
（日志必须走 slog。）

任何一条有输出 → 停下修掉再继续。

- [ ] **Step 4: 全包构建与测试**

Run: `go build ./... && go vet ./... && go test ./... -count=1`
Expected: 构建与 vet 无输出；测试全绿（基线上本来就红的环境敏感项如实记账，
**不改无关模块**）。

- [ ] **Step 5: 加关键节点日志**

本 task 只删文件，无行为变更，**不新增日志**——这是判断不是跳过：删除的是三个
markdown 副本，没有新的执行路径、分支或外部调用可观测。写下这个判断依据再往下走。

- [ ] **Step 6: 加注释**

本 task 只删文件，无代码改动，无注释可加。**这不是跳过**：确认删除后
`internal/discipline/builtin/` 下三个文件都有各自的用途说明——`review.md` 是新增的，
若它开头没有说明它是「审阅角色的内置纪律块」，在 `discipline.go` 的 `//go:embed`
上方补一行注释指明它的角色。

- [ ] **Step 7: 提交**

```bash
git add -A docs/superpowers/discipline internal/discipline
git commit -m "chore(discipline): 删除仓内纪律块副本，正文统一收进内置"
```

---

### Task 7: 全量门与整分支终审

**Files:**
- Create: `docs/superpowers/ledgers/2026-08-20-discipline-naming-execution.md`（执行账本）

**Interfaces:**
- Consumes: Task 1-6 的全部产出
- Produces: 无

- [ ] **Step 1: 格式与静态检查**

```bash
gofmt -l . | grep -v '^web/'
git diff --check
go build ./...
go vet ./...
```
Expected: 四条全部无输出。**`gofmt` 这条必跑**——测试全绿不等于格式干净。

- [ ] **Step 2: 全量测试**

```bash
go test ./... -count=1
```

把**实际结果原文**记进账本：通过的包数、失败的包与用例名。基线上本来就红的
环境敏感项（`internal/client` 的 cursor 根目录用例、`internal/config` 的
`TestLoadStripUpdateDoesNotBlockOnSaveFailure`、`internal/executor/grok` 的权限
相关用例等）如实记「基线即红」，**不改无关模块**。

- [ ] **Step 3: 前端门**

本 plan 不动 `web/`，但 `Builtins()` 变成三条会流到控制台的 API 响应。跑一遍确认：

```bash
cd web && npx tsc --noEmit && npx vitest run
```
Expected: tsc 退出 0；vitest 全绿。

**已在基线上核实过它不会红**：`DisciplinePage.test.tsx:21-23` 用的是**写死在测试里的
两条 fixture**，不打真 API，所以后端加第三条内置块碰不到它。

若它仍然红了 —— **停下来报告，不要改测试**。那说明前端对内置列表的依赖比核实时更深，
本 plan 声明只动后端，越界与否该由审核者裁决。

`node_modules` 不在时 `npx tsc` 会「成功」得很像回事，**别用
`npx tsc --noEmit 2>&1 | tail -3 && echo ok` 这种写法**——`&&` 绑的是 `tail`，
它永远为真。先确认 `web/node_modules` 存在（不存在就 `npm install`），
再直接看 `npx tsc --noEmit` 自己的退出码。

- [ ] **Step 4: 逐条对判据自查**

对照本 plan 开头的 Global Constraints 与下列判据，逐条写下**实际结果**（不是「应该」）：

1. 点名 review 的派发：注入的块含「只读，不写」、不含「每个 task 完成即 commit」
2. 不点名的派发：`Discipline` 来源标注仍是 `内置:<档位>`，与改动前一致
3. `resolveDisciplineFor` 三处调用点共用同一函数（grep 确认无第二份判定）
4. 老模板行映射出 `review` 且有 Warn
5. `docs/superpowers/discipline/` 已删且红线 grep 无输出

**没跑到结果的不许写结论**；跑了但失败就贴原始报错原文。

- [ ] **Step 5: 自我双裁决**

对整分支 diff 做两次裁决并各写结论：

- **spec 符合性**：要求全实现、没有多做。特别检查是否误改了 `Resolver.For` 的语义
  （Global Constraints 第一条）、是否碰了 `web/`（本 plan 声明只动后端）。
- **代码质量**：日志覆盖（每个错误分支带上下文与 cause、成功路径不静默）、
  注释覆盖（新文件头注释、导出函数 doc 注释、非显然分支的「为什么」）、
  无 `fmt.Printf`。

- [ ] **Step 6: 提交账本**

```bash
git add docs/superpowers/ledgers/2026-08-20-discipline-naming-execution.md
git commit -m "docs(ledger): 纪律块具名化全量门与终审"
```

---

## 附一：审核者本地验收清单（**不派发**，协调者执行）

以下要真机驱动 handoff 自身（起 agentd、派发、continue、重启），与执行纪律块的
「不要派发、不要调用 handoff CLI、不要起任何新的 executor 进程」**直接冲突**，
故意留在派发范围之外。

对应 spec 判据 ①③④⑥。

**A. 造靶子** —— 起隔离 agentd 实例（独立 DataDir + 端口、`ledger.enabled: true`、
`executor.default: fake`），**绝不重启 launchd 托管的生产 agentd**。

**B. 判据①（双重注入已消除）** —— 派一张走 `review-generic` 模板的卡，把最终 prompt
抓出来数：「# 审阅纪律」1 次、「# 执行纪律」**0** 次、「只读，不写」1 次、
「每个 task 完成即 commit」**0** 次。基线是 1/1/1/1（见 spec §1.1）。

**C. 判据③（机器级开关不作用于点名路径）** —— 隔离实例配 `discipline: {grok: ""}`，
① 不点名的派发不注入（现行为）；② 点名 `review` 的派发**仍然注入** review 块。

**D. 判据④（continue 与重启后不换块）** —— 点名 `review` 派一张卡，
① `handoff continue` 一轮，确认 `纪律块: 内置:review` 而不是兜底；
② 停掉隔离 agentd 再拉起，触发 `ResumeTask`，确认同上。
**这条是本轮最承重的判据**——单测只能证明函数选对了名字，证明不了名字真的活过了
进程重启。

**E. 判据⑥（文件覆盖生效）** —— 往隔离实例的 `DataDir/discipline/review.md` 丢一份
自定义正文，派发确认 Source 变成 `配置:review` 且注入的是自定义正文。

**F. 控制台走查** —— 打开隔离实例的设置页「执行纪律」分区，确认：内置列表出现三条、
默认选中项仍是 `subagent`（不是 review）、review 的正文能正常展示。

**G. 清理** —— 停隔离实例，删掉临时 DataDir。

## 附二：本 plan 明确不做

- 不改 `Resolver.For(executor)` 的实现与语义。
- 不碰 `web/`（`Builtins()` 变三条会自然流到控制台，但不改前端代码；
  若前端因此变红，停下报告而不是改测试）。
- 不做 A 组三条 UI —— 那是第二份 plan。
- 不改 `RecordAcceptance` / 卡详情接口 / 抽屉 —— 同上。
- 不合 main。
