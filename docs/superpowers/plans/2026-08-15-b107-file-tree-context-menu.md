# 执行纪律（先读这段，再读 plan）

你收到的是一份完整实现计划。用你自己的 subagent 机制按以下纪律执行，不要单上下文从头写到尾：

1. 逐 task 派全新 subagent 实现。每个 subagent 只给三样东西：该 task 的完整需求原文（含精确值、签名、测试用例）、它要接触的接口、全局约束。不要把会话历史或前序 task 总结灌进去。
2. 实现 subagent 不并行（避免改动冲突）。
3. 每个 task 完成后，派一个独立审查 subagent 做双裁决：spec 符合性（要求全实现、没有多做）+ 代码质量。输入是该 task 的需求原文 + 完整 diff。缺任一裁决不算过。
4. 审查不过进修复回路：一轮 = 一次修复 + 一次只看修复 diff 的复审，最多 5 轮。前 3 轮回原实现者，4-5 轮换全新实现者接手。5 轮后仍有未决项：非承重的记账搁置；承重的（后续 task 依赖它、或暴露 plan 缺陷）停下上报 BLOCKED。
5. 进度落盘到 ledger 文件：每 task 完成、每轮修复各追加一行，含 commit 范围。恢复现场以 ledger + git log 为准，不信记忆。
6. Minor 发现记账不进回路，留给终审统一 triage。
7. 全部 task 完成后做一次整分支终审（相对分支起点的完整 diff）。有发现项就一次性派一个修复 subagent 全量修，再做一次范围复审；不搞逐项派发，也没有第二轮修复波。
8. 协调上下文保持干净：你自己不亲自改代码，所有改动经 subagent 产出且经审查。
9. 每个 task 完成即 commit，提交信息说清做了什么。
10. 不停下来问「要不要继续」。只在 BLOCKED、真歧义、全部完成三种情况停；需求取舍拿不准就发工单问，等审核者裁决。

---

# B107 控制台文件树右键菜单 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 给控制台文件树加上 Orca 那套右键菜单：新建/复制/重命名/删除/文件夹内查找/在终端中打开，外加三条纯前端项。

**Architecture:** 服务端在 `workspace.go` 里新增五个纯函数（全部经 `os.OpenRoot` 完成），`workspacefiles.go` 加四个 HTTP handler + 一个搜索 handler，全部复用既有的 `forwardIfRequested` 跨机转发；PTY 请求体加一个 `rel` 让终端能起在子目录；前端把 B95 留下的 `ContextMenu` 挪到共享位置并扩展出分隔线与置灰态，再在 `FileTree` 上接线。

**Tech Stack:** Go 1.26（`os.Root` 全套 jail 方法）、React + TypeScript + Tailwind、vitest + RTL。

**Spec:** `docs/superpowers/specs/2026-08-15-b107-file-tree-context-menu-design.md`

## Global Constraints

- **路径遏制红线**：所有文件系统写操作**必须**经 `os.OpenRoot` 返回的 `*os.Root` 的方法完成（`Create`/`Mkdir`/`Remove`/`RemoveAll`/`Rename`/`Stat`/`Open`/`OpenRoot`/`FS`）。**禁止** `filepath.Join(root, rel)` 之后调用包级 `os.Remove`/`os.Rename`/`os.Create`/`os.Mkdir`/`os.MkdirAll`。理由见 `workspace.go:1196-1202`（TOCTOU）。这条不会被任何功能测试咬住，只能靠审查，**每个 task 的审查都必须逐个动作确认**。
- **错误原文原样透传**，不吞成「操作失败」（`workspacefiles.go:205-206` 立的规矩）。
- **日志**：每个新增导出函数在入口打 Info（带 repo + rel）、每个错误分支打 Warn/Error 带 cause、成功路径打 Info 带结果。用 `log()`（`workspace.go` 内既有写法），**禁止** `fmt.Printf`。
- **注释**：新增导出函数写清参数/返回/注意事项；`workspacefiles.go` 与 `FileTree.tsx` 的**文件头边界注释必须同步改**（它们现在都写着「只读/不删任何东西」，本期正是推翻它）。
- **不要在任何注释里写「防止弱凭据提权」**——该前提已被证伪（spec §1 错前提二）。删除要确认层的理由只是**不可逆**。
- 名字校验统一：空、`.`、`..`、含 `/` 或 `\` 一律 `ErrBadEntryName`。

---

### Task 1: 服务端 —— 新建 / 重命名 / 删除

**Files:**
- Modify: `internal/agentd/workspace.go`（新增哨兵 + 三个函数）
- Test: `internal/agentd/workspace_test.go`

**Interfaces:**
- Consumes: 既有 `ErrPathEscape`、`ErrGitDirWrite`、`isGitPath`（`workspace.go:1008`）
- Produces:
  ```go
  var (
      ErrEntryExists   = errors.New("目标已存在")
      ErrEntryNotFound = errors.New("目标不存在")
      ErrBadEntryName  = errors.New("名字不合法")
  )
  // kind 取 "file" 或 "dir"；parentRel 为空串表示工作树根
  func CreateEntry(repo, parentRel, name, kind string) (proto.DirEntry, error)
  func RenameEntry(repo, rel, newName string) (proto.DirEntry, error)
  func DeleteEntry(repo, rel string) error
  ```

- [ ] **Step 1: 写失败的测试**

```go
func TestCreateEntryFileAndDir(t *testing.T) {
	repo := t.TempDir()
	got, err := CreateEntry(repo, "", "handler.go", "file")
	if err != nil {
		t.Fatalf("建文件: %v", err)
	}
	if got.Name != "handler.go" || got.IsDir {
		t.Fatalf("返回项不对: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(repo, "handler.go")); err != nil {
		t.Fatalf("文件没落盘: %v", err)
	}
	if _, err := CreateEntry(repo, "", "internal", "dir"); err != nil {
		t.Fatalf("建目录: %v", err)
	}
	fi, err := os.Stat(filepath.Join(repo, "internal"))
	if err != nil || !fi.IsDir() {
		t.Fatalf("目录没落盘: %v", err)
	}
}

func TestCreateEntryRejects(t *testing.T) {
	repo := t.TempDir()
	if _, err := CreateEntry(repo, "", "a.go", "file"); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, parent, entry, kind string
		want                      error
	}{
		{"同名", "", "a.go", "file", ErrEntryExists},
		{"名字含斜杠", "", "x/y.go", "file", ErrBadEntryName},
		{"名字为空", "", "", "file", ErrBadEntryName},
		{"名字是点点", "", "..", "dir", ErrBadEntryName},
		{"父目录逃逸", "..", "a.go", "file", ErrPathEscape},
		{"命中 git 目录", ".git", "config", "file", ErrGitDirWrite},
		{"父目录不存在", "nope", "a.go", "file", ErrEntryNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := CreateEntry(repo, c.parent, c.entry, c.kind)
			if !errors.Is(err, c.want) {
				t.Fatalf("要 %v，得到 %v", c.want, err)
			}
		})
	}
}

func TestRenameEntry(t *testing.T) {
	repo := t.TempDir()
	if _, err := CreateEntry(repo, "", "old.go", "file"); err != nil {
		t.Fatal(err)
	}
	if _, err := RenameEntry(repo, "old.go", "new.go"); err != nil {
		t.Fatalf("改名: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "new.go")); err != nil {
		t.Fatalf("新名字不在: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "old.go")); !os.IsNotExist(err) {
		t.Fatal("旧名字还在")
	}
	if _, err := CreateEntry(repo, "", "taken.go", "file"); err != nil {
		t.Fatal(err)
	}
	if _, err := RenameEntry(repo, "new.go", "taken.go"); !errors.Is(err, ErrEntryExists) {
		t.Fatal("撞名应当被拒")
	}
	if _, err := RenameEntry(repo, "new.go", "a/b.go"); !errors.Is(err, ErrBadEntryName) {
		t.Fatal("新名字含斜杠应当被拒（本期不做跨目录移动）")
	}
	if _, err := RenameEntry(repo, ".git", "x"); !errors.Is(err, ErrGitDirWrite) {
		t.Fatal("改名 .git 应当被拒")
	}
}

func TestDeleteEntry(t *testing.T) {
	repo := t.TempDir()
	if _, err := CreateEntry(repo, "", "gone.go", "file"); err != nil {
		t.Fatal(err)
	}
	if err := DeleteEntry(repo, "gone.go"); err != nil {
		t.Fatalf("删文件: %v", err)
	}
	// 非空目录也要能删
	if _, err := CreateEntry(repo, "", "d", "dir"); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateEntry(repo, "d", "inner.go", "file"); err != nil {
		t.Fatal(err)
	}
	if err := DeleteEntry(repo, "d"); err != nil {
		t.Fatalf("删非空目录: %v", err)
	}
	if err := DeleteEntry(repo, "nope"); !errors.Is(err, ErrEntryNotFound) {
		t.Fatal("删不存在的应当 ErrEntryNotFound")
	}
	if err := DeleteEntry(repo, ".git"); !errors.Is(err, ErrGitDirWrite) {
		t.Fatal("删 .git 应当被拒")
	}
	if err := DeleteEntry(repo, ""); !errors.Is(err, ErrBadEntryName) {
		t.Fatal("删工作树根本身应当被拒")
	}
}

func TestEntryOpsSymlinkEscape(t *testing.T) {
	// 与 TestReadFileSymlinkEscape 同款手法：仓库内放一个指向仓库外的链接，
	// 三个动作都必须被 os.OpenRoot 挡下，而不是顺着链接操作到仓库外
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "victim.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(repo, "link")); err != nil {
		t.Skipf("本平台建不了符号链接: %v", err)
	}
	if _, err := CreateEntry(repo, "link", "new.txt", "file"); err == nil {
		t.Fatal("经链接在仓库外建文件竟然成功了")
	}
	if err := DeleteEntry(repo, "link/victim.txt"); err == nil {
		t.Fatal("经链接删仓库外文件竟然成功了")
	}
	if _, err := os.Stat(filepath.Join(outside, "victim.txt")); err != nil {
		t.Fatal("仓库外的文件被动了")
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/agentd/ -run 'TestCreateEntry|TestRenameEntry|TestDeleteEntry|TestEntryOps' -v`
Expected: FAIL，`undefined: CreateEntry`

- [ ] **Step 3: 实现**

要点（**逐条都是硬要求**）：

1. 三个函数**都**先 `root, err := os.OpenRoot(repo)`，`defer root.Close()`，之后**只**用 `root.*` 方法；
2. `rel` / `parentRel` 先 `filepath.Clean`，绝对路径或以 `..` 开头 → `ErrPathEscape`；`"."` 归一成 `""`；
3. `isGitPath` 对**最终目标路径**判定（`CreateEntry` 判 `parentRel + name`，另两个判 `rel`）→ `ErrGitDirWrite`；
4. `name` 走统一校验：空 / `.` / `..` / 含 `/` 或 `\` → `ErrBadEntryName`；`DeleteEntry` 的 `rel` 为空串也给 `ErrBadEntryName`（不许删工作树根）；
5. 存在性：`root.Stat(target)` 成功 → `ErrEntryExists`（建与改名）；失败且 `os.IsNotExist` → `ErrEntryNotFound`（改名与删）；
6. `CreateEntry` 的父目录：`root.Stat(parentRel)` 不存在 → `ErrEntryNotFound`；不是目录 → `ErrBadEntryName`；
7. 建文件用 `root.Create` 后**立刻 `Close`**（建的是空文件）；建目录用 `root.Mkdir(target, 0o755)`；
8. 删除：`root.Stat` 判是不是目录，目录用 `root.RemoveAll`、文件用 `root.Remove`；
9. 返回的 `proto.DirEntry` 从 `root.Stat` 现取，不要凭入参拼。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run 'TestCreateEntry|TestRenameEntry|TestDeleteEntry|TestEntryOps' -v`
Expected: PASS，且**每条子用例都真跑**（输出里不得有 `no tests to run`）

- [ ] **Step 5: 加关键节点日志**

- 三个函数入口各一条 Info：`log().Info("新建工作树条目", "repo", repo, "parent", parentRel, "name", name, "kind", kind)`（另两个同款）
- 每个错误分支一条 Warn，带 repo + rel + cause，措辞照既有写法（如 `log().Warn("新建条目路径逃逸被拒绝", "repo", repo, "path", rel)`）
- 成功路径各一条 Info，带最终路径与类型——**不许静默成功**

- [ ] **Step 6: 加注释**

- 三个导出函数各写 doc comment：参数、返回、哨兵错误清单
- 在第一个函数上方写一段「为什么全部经 `os.Root`」，**指向** `workspace.go:1196-1202` 已有的 TOCTOU 论证，不要重复抄
- `DeleteEntry` 上注明「不做回收站」及其理由（git 能救已跟踪文件，救不了未跟踪文件），并注明**这不是凭据强弱问题**

- [ ] **Step 7: 提交**

```bash
git add internal/agentd/workspace.go internal/agentd/workspace_test.go
git commit -m "feat(workspace): 新增条目的建/改名/删，全部经 os.Root jail"
```

---

### Task 2: 服务端 —— 复制

**Files:**
- Modify: `internal/agentd/workspace.go`
- Test: `internal/agentd/workspace_test.go`

**Interfaces:**
- Consumes: Task 1 的哨兵与路径校验辅助
- Produces: `func CopyEntry(repo, rel string) (proto.DirEntry, error)`

- [ ] **Step 1: 写失败的测试**

```go
func TestCopyEntryNaming(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "foo.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := CopyEntry(repo, "foo.go")
	if err != nil {
		t.Fatalf("第一次复制: %v", err)
	}
	if first.Name != "foo copy.go" {
		t.Fatalf("第一份副本要叫 %q，得到 %q", "foo copy.go", first.Name)
	}
	second, err := CopyEntry(repo, "foo.go")
	if err != nil {
		t.Fatalf("第二次复制: %v", err)
	}
	if second.Name != "foo copy 2.go" {
		t.Fatalf("第二份副本要叫 %q，得到 %q", "foo copy 2.go", second.Name)
	}
	// 内容要真的复制过去
	b, err := os.ReadFile(filepath.Join(repo, "foo copy.go"))
	if err != nil || string(b) != "package main" {
		t.Fatalf("副本内容不对: %q %v", b, err)
	}
	// 无扩展名
	if err := os.WriteFile(filepath.Join(repo, "Makefile"), []byte("all:"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := CopyEntry(repo, "Makefile")
	if err != nil || got.Name != "Makefile copy" {
		t.Fatalf("无扩展名副本要叫 %q，得到 %q（err=%v）", "Makefile copy", got.Name, err)
	}
}

func TestCopyEntryDirRecursive(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "d", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "d", "sub", "x.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := CopyEntry(repo, "d")
	if err != nil {
		t.Fatalf("复制目录: %v", err)
	}
	if got.Name != "d copy" || !got.IsDir {
		t.Fatalf("目录副本不对: %+v", got)
	}
	b, err := os.ReadFile(filepath.Join(repo, "d copy", "sub", "x.go"))
	if err != nil || string(b) != "x" {
		t.Fatalf("递归内容没复制过去: %q %v", b, err)
	}
}

func TestCopyEntryRejects(t *testing.T) {
	repo := t.TempDir()
	if err := CopyEntryRejectHelper(repo); err != nil {
		t.Fatal(err)
	}
	if _, err := CopyEntry(repo, "nope"); !errors.Is(err, ErrEntryNotFound) {
		t.Fatal("复制不存在的应当 ErrEntryNotFound")
	}
	if _, err := CopyEntry(repo, ".git"); !errors.Is(err, ErrGitDirWrite) {
		t.Fatal("复制 .git 应当被拒")
	}
	if _, err := CopyEntry(repo, "../x"); !errors.Is(err, ErrPathEscape) {
		t.Fatal("逃逸路径应当被拒")
	}
}

// CopyEntryRejectHelper 建一个 .git 目录，让上面的 .git 用例有东西可撞。
func CopyEntryRejectHelper(repo string) error {
	return os.MkdirAll(filepath.Join(repo, ".git"), 0o755)
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/agentd/ -run TestCopyEntry -v`
Expected: FAIL，`undefined: CopyEntry`

- [ ] **Step 3: 实现**

命名规则（spec §3.4）：把 `base` 与 `ext` 用 `filepath.Ext` 拆开（**目录不拆扩展名**，整体当 base），
候选依次是 `base copy<ext>`、`base copy 2<ext>` …… 到 `base copy 99<ext>`；
每个候选用 `root.Stat` 探测，第一个不存在的就是目标；全部占用 → `ErrEntryExists`。

复制实现：
- 文件：`src, _ := root.Open(rel)` → `dst, _ := root.Create(target)` → `io.Copy`，两个都 `defer Close`；权限用源文件的 `Stat().Mode()` 经 `root.Chmod` 补上；
- 目录：`fs.WalkDir(root.FS(), rel, ...)`，对每个条目算出目标相对路径后 `root.Mkdir` / 文件同上。**中途不得落回绝对路径**；
- 符号链接：`fs.WalkDir` 不跟随链接，遇到非普通文件且非目录的条目**跳过并 Warn**（不复制链接，也不因此整体失败）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestCopyEntry -v`
Expected: PASS

- [ ] **Step 5: 加关键节点日志**

入口 Info（repo + rel）；选定目标名后 Info（带最终名与试了几次）；目录复制在**每 200 个条目**打一条 Debug 进度（大目录不要刷屏）；跳过非普通文件时 Warn 带路径；成功 Info 带条目数与字节数；每个错误分支 Warn 带 cause。

- [ ] **Step 6: 加注释**

doc comment 写清命名规则与 99 上限的理由；在递归那段写一句「为什么整段都在 `root.FS()` 上走」。

- [ ] **Step 7: 提交**

```bash
git add internal/agentd/workspace.go internal/agentd/workspace_test.go
git commit -m "feat(workspace): 条目复制，含目录递归与 foo copy N 命名"
```

---

### Task 3: 服务端 —— 文件夹内查找

**Files:**
- Modify: `internal/agentd/workspace.go`
- Modify: `internal/proto/`（新增响应类型，放进现有文件类 proto 文件）
- Test: `internal/agentd/workspace_test.go`

**Interfaces:**
- Produces:
  ```go
  type SearchHit struct {
      Rel  string `json:"rel"`
      Line int    `json:"line"`
      Text string `json:"text"`
  }
  type SearchResult struct {
      Hits      []SearchHit `json:"hits"`
      Truncated bool        `json:"truncated"`
  }
  // limit <= 0 时取 searchDefaultLimit；超过 searchMaxLimit 时收敛到它
  func SearchInDir(ctx context.Context, repo, rel, query string, limit int) (proto.SearchResult, error)
  ```

- [ ] **Step 1: 写失败的测试**

```go
func TestSearchInDirHitsAndSkips(t *testing.T) {
	repo := t.TempDir()
	mk := func(rel, body string) {
		p := filepath.Join(repo, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("a.go", "package main\nfunc needle() {}\n")
	mk("sub/b.go", "// needle 在注释里\n")
	mk(".git/config", "needle\n")
	mk("node_modules/c.js", "needle\n")

	got, err := SearchInDir(context.Background(), repo, "", "needle", 0)
	if err != nil {
		t.Fatalf("搜索: %v", err)
	}
	rels := map[string]bool{}
	for _, h := range got.Hits {
		rels[h.Rel] = true
	}
	if !rels["a.go"] || !rels["sub/b.go"] {
		t.Fatalf("正常文件没命中: %+v", got.Hits)
	}
	if rels[".git/config"] || rels["node_modules/c.js"] {
		t.Fatalf(".git / node_modules 必须被跳过: %+v", got.Hits)
	}
	// 行号从 1 起
	for _, h := range got.Hits {
		if h.Rel == "a.go" && h.Line != 2 {
			t.Fatalf("行号要从 1 起算，needle 在第 2 行，得到 %d", h.Line)
		}
	}
}

func TestSearchInDirLimit(t *testing.T) {
	repo := t.TempDir()
	var sb strings.Builder
	for i := 0; i < 50; i++ {
		sb.WriteString("needle\n")
	}
	if err := os.WriteFile(filepath.Join(repo, "many.txt"), []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := SearchInDir(context.Background(), repo, "", "needle", 10)
	if err != nil {
		t.Fatalf("搜索: %v", err)
	}
	if len(got.Hits) != 10 {
		t.Fatalf("limit=10 要恰好 10 条，得到 %d", len(got.Hits))
	}
	if !got.Truncated {
		t.Fatal("撞到上限必须标 Truncated——否则「10 条」会被读成「只有 10 处」")
	}
}

func TestSearchInDirScopeAndRejects(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "only"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "only", "in.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "out.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := SearchInDir(context.Background(), repo, "only", "needle", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Hits) != 1 || got.Hits[0].Rel != "only/in.txt" {
		t.Fatalf("范围没生效: %+v", got.Hits)
	}
	if _, err := SearchInDir(context.Background(), repo, "", "", 0); err == nil {
		t.Fatal("空关键词应当被拒")
	}
	if _, err := SearchInDir(context.Background(), repo, "../x", "needle", 0); !errors.Is(err, ErrPathEscape) {
		t.Fatal("逃逸范围应当被拒")
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/agentd/ -run TestSearchInDir -v`
Expected: FAIL，`undefined: SearchInDir`

- [ ] **Step 3: 实现**

常量：
```go
const (
	searchDefaultLimit = 200
	searchMaxLimit     = 1000
	searchTimeout      = 10 * time.Second
)
// searchSkipDirs 是不进入的目录名（任意层级命中即跳过整棵子树）。
var searchSkipDirs = map[string]bool{".git": true, "node_modules": true, "vendor": true, "dist": true, "target": true}
```

实现取向：**本期只做 Go 自己遍历这一条路，不接 ripgrep**（spec 说「优先 rg」，但接外部进程要处理不存在、版本差异、输出解析三件事，而收益只是快——**本 task 明确收敛为纯 Go**，rg 留待真嫌慢再说）。
- `os.OpenRoot(repo)` → `fs.WalkDir(root.FS(), scope, ...)`；
- 目录名命中 `searchSkipDirs` → `return fs.SkipDir`；
- 文件逐行扫（`bufio.Scanner`，行长上限设 1 MiB 防超长行）；
- **二进制文件跳过**：读头部 512 字节含 `\x00` 即跳过；
- 单行文本超过 300 字符时截断到 300（结果面板放不下，且长行多半是压缩产物）；
- `ctx` 超时用 `context.WithTimeout(ctx, searchTimeout)`，每处理 100 个文件检查一次 `ctx.Err()`，到点就带 `Truncated: true` 返回**已有结果**（不返回错误）；
- 命中数达到 limit 立刻停止并 `Truncated: true`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestSearchInDir -v`
Expected: PASS

- [ ] **Step 5: 加关键节点日志**

入口 Info（repo/scope/query/limit）；跳过的目录打 Debug；超时返回时打 **Warn**（这是护栏被撞到，要看得见）；撞 limit 打 Info；结束 Info 带命中数、扫描文件数、耗时。

- [ ] **Step 6: 加注释**

三条护栏各写一句「为什么」；`searchSkipDirs` 上写明「任意层级命中即跳整棵子树」；文件头如已有清单则补上本函数。

- [ ] **Step 7: 提交**

```bash
git add internal/agentd/workspace.go internal/proto/ internal/agentd/workspace_test.go
git commit -m "feat(workspace): 文件夹内查找，带条数/超时/跳过生成物三条护栏"
```

---

### Task 4: HTTP 端点接线

**Files:**
- Modify: `internal/agentd/workspacefiles.go`（四个 handler + **文件头边界注释**）
- Modify: `internal/agentd/server.go`（注册 5 条路由 + 顶部清单注释）
- Modify: `internal/proto/`（请求体类型）
- Test: `internal/agentd/workspacefiles_test.go`

**Interfaces:**
- Consumes: Task 1/2/3 的五个函数
- Produces: 5 条路由，全部支持 `?machine=`：
  ```
  POST   /api/workspaces/entry?path=&rel=       body {"name":"...","kind":"file"|"dir"}
  POST   /api/workspaces/entry/copy?path=&rel=
  PATCH  /api/workspaces/entry?path=&rel=       body {"new_name":"..."}
  DELETE /api/workspaces/entry?path=&rel=
  GET    /api/workspaces/search?path=&rel=&q=&limit=
  ```

- [ ] **Step 1: 写失败的测试**

```go
func TestWorkspaceEntryEndpoints(t *testing.T) {
	// 照 workspacefiles_test.go 已有的建站手法起测试服务器与一个白名单工作树
	srv, repo := newWorkspaceTestServer(t)

	// 建
	code, body := doJSON(t, srv, http.MethodPost,
		"/api/workspaces/entry?path="+url.QueryEscape(repo)+"&rel=",
		`{"name":"a.go","kind":"file"}`)
	if code != http.StatusOK {
		t.Fatalf("建文件要 200，得到 %d: %s", code, body)
	}
	// 撞名 → 409
	code, _ = doJSON(t, srv, http.MethodPost,
		"/api/workspaces/entry?path="+url.QueryEscape(repo)+"&rel=",
		`{"name":"a.go","kind":"file"}`)
	if code != http.StatusConflict {
		t.Fatalf("撞名要 409，得到 %d", code)
	}
	// 名字含斜杠 → 400
	code, _ = doJSON(t, srv, http.MethodPost,
		"/api/workspaces/entry?path="+url.QueryEscape(repo)+"&rel=",
		`{"name":"x/y.go","kind":"file"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("非法名字要 400，得到 %d", code)
	}
	// 改名
	code, _ = doJSON(t, srv, http.MethodPatch,
		"/api/workspaces/entry?path="+url.QueryEscape(repo)+"&rel=a.go",
		`{"new_name":"b.go"}`)
	if code != http.StatusOK {
		t.Fatalf("改名要 200，得到 %d", code)
	}
	// 复制
	code, _ = doJSON(t, srv, http.MethodPost,
		"/api/workspaces/entry/copy?path="+url.QueryEscape(repo)+"&rel=b.go", "")
	if code != http.StatusOK {
		t.Fatalf("复制要 200，得到 %d", code)
	}
	// 删
	code, _ = doJSON(t, srv, http.MethodDelete,
		"/api/workspaces/entry?path="+url.QueryEscape(repo)+"&rel=b.go", "")
	if code != http.StatusOK {
		t.Fatalf("删除要 200，得到 %d", code)
	}
	// 删不存在的 → 404
	code, _ = doJSON(t, srv, http.MethodDelete,
		"/api/workspaces/entry?path="+url.QueryEscape(repo)+"&rel=b.go", "")
	if code != http.StatusNotFound {
		t.Fatalf("删不存在的要 404，得到 %d", code)
	}
}

func TestWorkspaceEntryRejectsUnlistedPath(t *testing.T) {
	srv, _ := newWorkspaceTestServer(t)
	other := t.TempDir()
	code, _ := doJSON(t, srv, http.MethodDelete,
		"/api/workspaces/entry?path="+url.QueryEscape(other)+"&rel=x", "")
	if code != http.StatusBadRequest {
		t.Fatalf("非白名单工作树要 400，得到 %d", code)
	}
}

func TestWorkspaceSearchEndpoint(t *testing.T) {
	srv, repo := newWorkspaceTestServer(t)
	if err := os.WriteFile(filepath.Join(repo, "s.go"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, body := doJSON(t, srv, http.MethodGet,
		"/api/workspaces/search?path="+url.QueryEscape(repo)+"&rel=&q=needle", "")
	if code != http.StatusOK {
		t.Fatalf("搜索要 200，得到 %d: %s", code, body)
	}
	if !strings.Contains(body, "s.go") {
		t.Fatalf("响应里没有命中项: %s", body)
	}
	code, _ = doJSON(t, srv, http.MethodGet,
		"/api/workspaces/search?path="+url.QueryEscape(repo)+"&rel=&q=", "")
	if code != http.StatusBadRequest {
		t.Fatalf("空关键词要 400，得到 %d", code)
	}
}

func TestWorkspaceEntryErrorTextPassthrough(t *testing.T) {
	// 中文原文必须透传，不许吞成「操作失败」
	srv, repo := newWorkspaceTestServer(t)
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, body := doJSON(t, srv, http.MethodDelete,
		"/api/workspaces/entry?path="+url.QueryEscape(repo)+"&rel=.git", "")
	if !strings.Contains(body, ".git") {
		t.Fatalf("错误原文没透传: %s", body)
	}
}
```

> `newWorkspaceTestServer` / `doJSON`：`workspacefiles_test.go` 里如已有等价辅助就复用，
> 没有就照该文件现有的建站方式写一份，**不要另起一套测试脚手架**。

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/agentd/ -run 'TestWorkspaceEntry|TestWorkspaceSearch' -v`
Expected: FAIL（404 或编译失败）

- [ ] **Step 3: 实现**

- 每个 handler 第一句 `if s.forwardIfRequested(w, r) { return }`，与三个既有端点一致；
- 用 `s.workspaceRootOrErr(w, r)` 取并校验 `path`；
- 错误映射：`ErrEntryExists`→409、`ErrEntryNotFound`→404、`ErrBadEntryName`/`ErrPathEscape`/`ErrGitDirWrite`→400、其余→500；**错误文案原样透传**；
- `server.go` 注册 5 条路由，并在顶部那份路由清单注释里补上这 5 行（**清单漏了等于没做**）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run 'TestWorkspaceEntry|TestWorkspaceSearch' -v`
Expected: PASS

- [ ] **Step 5: 加关键节点日志**

每个 handler 入口 Info（method + path + rel + machine）；每个错误分支 Warn 带映射后的状态码；成功 Info。

- [ ] **Step 6: 加注释**

**`workspacefiles.go` 文件头必须改**：现在写着「写接口只在单个已存在文件上做原子替换：**不建目录、不删任何东西**」——这句话本 task 之后就不成立了。改成如实描述新的边界（能建、能删、能改名、能复制、能搜；仍然只在已探测到的工作树内）。

- [ ] **Step 7: 提交**

```bash
git add internal/agentd/workspacefiles.go internal/agentd/server.go internal/proto/ internal/agentd/workspacefiles_test.go
git commit -m "feat(agentd): 工作树条目操作与查找的 5 条端点，含跨机转发"
```

---

### Task 5: PTY 支持子目录

**Files:**
- Modify: `internal/proto/pty.go`
- Modify: `internal/agentd/pty_api.go`
- Test: `internal/agentd/pty_api_test.go`

**Interfaces:**
- Produces: `proto.CreatePtySessionReq.Rel string \`json:"rel"\``；`resolvePtyBase` 支持它

- [ ] **Step 1: 写失败的测试**

```go
func TestResolvePtyBaseWithRel(t *testing.T) {
	s, repo := newPtyTestServer(t) // 复用该文件已有的建站辅助
	if err := os.MkdirAll(filepath.Join(repo, "internal", "agentd"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/pty/sessions", nil)

	got, kind, err := s.resolvePtyBase(r, proto.CreatePtySessionReq{BasePath: repo, Rel: "internal/agentd"})
	if err != nil {
		t.Fatalf("子目录应当可用: %v", err)
	}
	if got != filepath.Join(repo, "internal", "agentd") || kind != "workspace" {
		t.Fatalf("cwd 不对: %q kind=%q", got, kind)
	}

	// rel 为空 = 工作树根，保持既有行为
	got, _, err = s.resolvePtyBase(r, proto.CreatePtySessionReq{BasePath: repo})
	if err != nil || got != repo {
		t.Fatalf("空 rel 要回到工作树根: %q %v", got, err)
	}

	if _, _, err := s.resolvePtyBase(r, proto.CreatePtySessionReq{BasePath: repo, Rel: "../.."}); err == nil {
		t.Fatal("逃逸的 rel 应当被拒")
	}
	if err := os.WriteFile(filepath.Join(repo, "f.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.resolvePtyBase(r, proto.CreatePtySessionReq{BasePath: repo, Rel: "f.go"}); err == nil {
		t.Fatal("rel 指向文件应当被拒——终端的 cwd 必须是目录")
	}
	if _, _, err := s.resolvePtyBase(r, proto.CreatePtySessionReq{BasePath: repo, Rel: "nope"}); err == nil {
		t.Fatal("不存在的 rel 应当被拒")
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/agentd/ -run TestResolvePtyBaseWithRel -v`
Expected: FAIL，`unknown field Rel`

- [ ] **Step 3: 实现**

在 `resolvePtyBase` 拿到 `root` 之后、`return root, "workspace", nil` 之前插入：`Rel` 非空时
`filepath.Clean`，绝对路径或以 `..` 开头 → 报错；`filepath.Join(root, rel)` 后 `os.Stat` 确认存在且是目录；返回它。

**注意这里不用 `os.OpenRoot`**：本函数的注释已写明「这是参数校验，不是安全边界」（终端里一条 `cd ~` 就出去了），
失败给的是 400 语义的错误。**但仍然要校验**，理由与原注释一致：防止 shell 起在莫名其妙的角落。
**实现时要在注释里点明这条与 Task 1-3 的判据不同**，否则下一个人会以为是漏了 jail。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestResolvePtyBase -v`
Expected: PASS（含既有的 base 用例，不得回归）

- [ ] **Step 5: 加关键节点日志**

`handleCreatePtySession` 已有的入口 Info 加上 `"rel", req.Rel`；校验失败的 Warn 带 rel。

- [ ] **Step 6: 加注释**

`CreatePtySessionReq.Rel` 写清「相对 BasePath 的子目录，空串=工作树根；`BaseKind=home` 时忽略」；
`resolvePtyBase` 的 doc comment 补上 rel 的语义与「为什么这里不用 OpenRoot」。

- [ ] **Step 7: 提交**

```bash
git add internal/proto/pty.go internal/agentd/pty_api.go internal/agentd/pty_api_test.go
git commit -m "feat(pty): 建终端会话支持 rel，终端可起在工作树子目录"
```

---

### Task 6: 前端 —— ContextMenu 迁移与扩展

**Files:**
- Move: `web/src/app/tree/ContextMenu.tsx` → `web/src/app/shared/ContextMenu.tsx`
- Move: `web/src/app/tree/ContextMenu.test.tsx` → `web/src/app/shared/ContextMenu.test.tsx`
- Modify: `web/src/app/tree/ProjectTree.tsx`（改 import）
- Test: `web/src/app/shared/ContextMenu.test.tsx`

**Interfaces:**
- Produces:
  ```ts
  export interface ContextMenuItem {
    label: string
    onSelect: () => void
    danger?: boolean
    disabled?: boolean
    disabledReason?: string
    separator?: never
  }
  export type ContextMenuEntry = ContextMenuItem | { separator: true }
  export interface ContextMenuProps { x: number; y: number; items: ContextMenuEntry[]; onClose: () => void }
  ```

- [ ] **Step 1: 写失败的测试**

```tsx
it('分隔线渲染成 separator 且不可聚焦', async () => {
  render(<ContextMenu x={10} y={10} onClose={() => {}} items={[
    { label: '甲', onSelect: () => {} },
    { separator: true },
    { label: '乙', onSelect: () => {} },
  ]} />)
  expect(screen.getByRole('separator')).toBeInTheDocument()
  expect(screen.getAllByRole('menuitem')).toHaveLength(2)
})

it('置灰项不可点，并把理由挂在 title 上', async () => {
  const onSelect = vi.fn()
  render(<ContextMenu x={10} y={10} onClose={() => {}} items={[
    { label: '甲', onSelect: () => {} },
    { label: 'Reveal in Finder', onSelect, disabled: true, disabledReason: '远程目录无法在本机的访达中打开' },
  ]} />)
  const item = screen.getByRole('menuitem', { name: /Reveal in Finder/ })
  expect(item).toBeDisabled()
  expect(item).toHaveAttribute('title', '远程目录无法在本机的访达中打开')
  await userEvent.click(item)
  expect(onSelect).not.toHaveBeenCalled()
})

it('初始焦点落在首个可用项上（首项置灰时跳过它）', () => {
  render(<ContextMenu x={10} y={10} onClose={() => {}} items={[
    { label: '灰的', onSelect: () => {}, disabled: true, disabledReason: 'x' },
    { label: '能点的', onSelect: () => {} },
  ]} />)
  expect(screen.getByRole('menuitem', { name: '能点的' })).toHaveFocus()
})

it('上下键在可用项之间循环，跳过分隔线与置灰项', async () => {
  render(<ContextMenu x={10} y={10} onClose={() => {}} items={[
    { label: '甲', onSelect: () => {} },
    { separator: true },
    { label: '灰的', onSelect: () => {}, disabled: true, disabledReason: 'x' },
    { label: '乙', onSelect: () => {} },
  ]} />)
  expect(screen.getByRole('menuitem', { name: '甲' })).toHaveFocus()
  await userEvent.keyboard('{ArrowDown}')
  expect(screen.getByRole('menuitem', { name: '乙' })).toHaveFocus()
  await userEvent.keyboard('{ArrowDown}')
  expect(screen.getByRole('menuitem', { name: '甲' })).toHaveFocus()
})
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npx vitest run src/app/shared/ContextMenu.test.tsx`
Expected: FAIL

- [ ] **Step 3: 实现**

- 文件用 `git mv` 移动（保留历史），改 `ProjectTree.tsx` 的 import；
- 分隔线渲染成 `<div role="separator" className="my-1 h-px bg-border" />`；
- 置灰项：`disabled` + `title={disabledReason}` + `className` 加 `disabled:opacity-50 disabled:cursor-not-allowed`；
- 初始焦点：现在是 `el.querySelector('[role="menuitem"]')`，改成 `'[role="menuitem"]:not(:disabled)'`；
- 上下键：在既有的 `onKey` 里加 `ArrowDown`/`ArrowUp` 分支，在 `:not(:disabled)` 的 menuitem 列表里循环，`preventDefault`；
- **既有行为一条都不许回归**：定位翻转、点外部关闭、Esc、`onSelect` 先执行后关闭。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/shared/ContextMenu.test.tsx src/app/tree/ProjectTree.test.tsx`
Expected: PASS（ProjectTree 的既有用例一并绿）

- [ ] **Step 5: 加注释**

文件头补上「本组件同时服务项目树与文件树」，并把「为什么自己写而不是引依赖」那段保留；
`disabledReason` 上写一句「置灰**必须**给理由，否则用户只会以为是 bug」。

- [ ] **Step 6: 提交**

```bash
git add -A web/src/app
git commit -m "refactor(web): ContextMenu 迁到 shared 并加分隔线与置灰态"
```

---

### Task 7: 前端 —— FileTree 接线与弹层

**Files:**
- Modify: `web/src/api/client.ts`
- Create: `web/src/app/files/EntryNameDialog.tsx`
- Create: `web/src/app/files/DeleteEntryDialog.tsx`
- Modify: `web/src/app/files/FileTree.tsx`（**含文件头边界注释**）
- Test: `web/src/app/files/FileTree.test.tsx`、`web/src/app/files/EntryDialogs.test.tsx`

**Interfaces:**
- Consumes: Task 4 的 5 条端点、Task 6 的 `ContextMenu`
- Produces（`client.ts`，全部带 `machine?: string`）：
  ```ts
  export function createWorkspaceEntry(path: string, rel: string, name: string, kind: 'file' | 'dir', machine?: string): Promise<DirEntry>
  export function copyWorkspaceEntry(path: string, rel: string, machine?: string): Promise<DirEntry>
  export function renameWorkspaceEntry(path: string, rel: string, newName: string, machine?: string): Promise<DirEntry>
  export function deleteWorkspaceEntry(path: string, rel: string, machine?: string): Promise<{ ok: boolean }>
  export function searchWorkspace(path: string, rel: string, q: string, machine?: string): Promise<SearchResult>
  ```

- [ ] **Step 1: 写失败的测试**

```tsx
it('目录行的菜单有「折叠文件夹」，文件行没有', async () => {
  renderTree()
  await userEvent.pointer({ target: await screen.findByText('internal'), keys: '[MouseRight]' })
  expect(screen.getByRole('menuitem', { name: '折叠文件夹' })).toBeInTheDocument()
  await userEvent.keyboard('{Escape}')
  await userEvent.pointer({ target: await screen.findByText('go.mod'), keys: '[MouseRight]' })
  expect(screen.queryByRole('menuitem', { name: '折叠文件夹' })).not.toBeInTheDocument()
})

it('Reveal in Finder 恒置灰，远程与本机的理由不同', async () => {
  renderTree({ machine: 'mac-02' })
  await userEvent.pointer({ target: await screen.findByText('go.mod'), keys: '[MouseRight]' })
  const item = screen.getByRole('menuitem', { name: /Reveal in Finder/ })
  expect(item).toBeDisabled()
  expect(item.getAttribute('title')).toContain('mac-02')
})

it('删除确认必须点名「未跟踪的文件删除后无法恢复」', async () => {
  renderTree()
  await userEvent.pointer({ target: await screen.findByText('go.mod'), keys: '[MouseRight]' })
  await userEvent.click(screen.getByRole('menuitem', { name: '删除' }))
  expect(screen.getByText(/未被 git 跟踪的文件删除后无法恢复/)).toBeInTheDocument()
})

it('服务端的中文错误原文被显示出来，不吞成「操作失败」', async () => {
  server.use(http.delete('*/api/workspaces/entry', () =>
    HttpResponse.json({ error: '不允许写入 .git 目录' }, { status: 400 })))
  renderTree()
  await userEvent.pointer({ target: await screen.findByText('go.mod'), keys: '[MouseRight]' })
  await userEvent.click(screen.getByRole('menuitem', { name: '删除' }))
  await userEvent.click(screen.getByRole('button', { name: '删除' }))
  expect(await screen.findByText(/不允许写入 \.git 目录/)).toBeInTheDocument()
})

it('新建成功后只刷新该层目录', async () => {
  const calls: string[] = []
  server.use(http.get('*/api/workspaces/dir', ({ request }) => {
    calls.push(new URL(request.url).searchParams.get('rel') ?? '')
    return HttpResponse.json({ entries: [] })
  }))
  renderTree()
  await userEvent.pointer({ target: await screen.findByText('internal'), keys: '[MouseRight]' })
  await userEvent.click(screen.getByRole('menuitem', { name: '新文件' }))
  await userEvent.type(screen.getByLabelText('名称'), 'x.go')
  await userEvent.click(screen.getByRole('button', { name: '创建' }))
  await waitFor(() => expect(calls.filter((r) => r === 'internal')).toHaveLength(1))
})

it('名字含 / 时保存按钮禁用并给出理由', async () => {
  renderTree()
  await userEvent.pointer({ target: await screen.findByText('internal'), keys: '[MouseRight]' })
  await userEvent.click(screen.getByRole('menuitem', { name: '新文件' }))
  await userEvent.type(screen.getByLabelText('名称'), 'a/b.go')
  expect(screen.getByRole('button', { name: '创建' })).toBeDisabled()
  expect(screen.getByText(/名字不能包含/)).toBeInTheDocument()
})
```

> `renderTree` 照 `FileTree.test.tsx` 已有的渲染辅助扩展（多接一个 `machine`），**不要另起脚手架**。
> 若既有测试用 msw，沿用它；没有就照该文件已有的 mock 方式。

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npx vitest run src/app/files`
Expected: FAIL

- [ ] **Step 3: 实现**

菜单项与分组照 spec §6.2 那张表。要点：

- **`dirOf`**：目录行用自身 rel，文件行用父 rel（`rel.split('/').slice(0,-1).join('/')`）。四个「文件夹类」动作（新文件/新建文件夹/在终端中打开/在文件夹中查找）全部用它；
- **`Reveal in Finder` 恒 `disabled`**，理由文案本机与远程不同（远程那条要带上机器名）；
- **`复制路径`** 用 `base.path + '/' + rel`，**`复制相对路径`** 用 `rel`，都走 `navigator.clipboard.writeText`；
- **`在终端中打开`** 调既有的建终端能力并传 `rel`（`useWorkbench` 那套；找不到现成入口就把 `rel` 透到 `createPtySession` 的调用点）；
- 弹层用两个新组件，`aria-label="名称"` 的输入框（测试按它取）；
- 操作成功后 `dirs.ensure(dirOf)` 重取该层，**不整树刷新**；
- 失败时把服务端 `error` 字段原文渲染出来。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/files src/app/shared`
Expected: PASS

- [ ] **Step 5: 加注释**

**`FileTree.tsx` 文件头第 10 行必须改**：现在写着「**只读**：不发写请求，不提供新建/重命名/删除」——本 task 正是推翻它。
改成如实的新边界。两个新组件各写文件头（职责 + 边界）；`Reveal in Finder` 恒置灰处写一句「为什么本机也灰」（本期不做单边形态，且依赖 B108 未决的前提）。

- [ ] **Step 6: 前端三件套**

Run: `cd web && npx vitest run && npx tsc -b --force && npx vite build`
Expected: 全绿、0 error

- [ ] **Step 7: 提交**

```bash
git add -A web/src
git commit -m "feat(web): 文件树右键菜单接线，含新建/改名/复制/删除/查找与置灰项"
```

---

## 终审前的机械判据（ledger 必须贴出输出）

```bash
# 路径遏制红线：新增的写操作不得出现包级 os.* 调用
git diff <分支起点>..HEAD -- internal/agentd/workspace.go | grep -n '^+' | grep -E 'os\.(Remove|RemoveAll|Rename|Create|Mkdir|MkdirAll|WriteFile)\(' || echo "红线通过：零命中"

# 被证伪的前提不得复活
git diff <分支起点>..HEAD | grep -n '弱凭据\|弱于主令牌\|提权' || echo "通过：没有引用已被证伪的论证"

# 两处必须改的边界注释
grep -n '不删任何东西\|不建目录' internal/agentd/workspacefiles.go || echo "workspacefiles.go 边界注释已更新"
grep -n '只读：不发写请求' web/src/app/files/FileTree.tsx || echo "FileTree.tsx 边界注释已更新"
```

四条命令的输出**逐条贴进 ledger**。第一条与第二条有命中即为未完成。
