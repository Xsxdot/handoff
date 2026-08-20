# 工作台状态同步实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把中央工作台的 tab / 分屏布局与右下角悬浮窗的现场落到协调者 agentd 的 SQLite，
让切换目录、刷新页面、退出并重开桌面端之后，工作现场原样回来。

**Architecture:** 后端是一个**不解释内容的键值存储**——`payload` 在 agentd 眼里就是一个
JSON 字符串，它不解析、不校验结构。前端是唯一懂布局形状的一方，负责编解码与逐字段校验。
按基准目录分行存储（不是整份一个大 blob），冲突策略是最后写入者赢。恢复时不做任何探活，
只用「本来就要拉的那份 PTY 会话列表」把已死的 `sessionId` 抹掉。

**Tech Stack:** Go 1.x + modernc.org/sqlite（纯 Go，无 cgo）；React 18 + TypeScript + Vite + vitest。

**Spec:** `docs/superpowers/specs/2026-08-20-workbench-state-sync-design.md`

## Global Constraints

以下要求适用于**每一个** task，不再逐条重复：

1. **注释**：每个新建文件顶部写「职责 + 边界（它**不**做什么）」；每个导出函数/方法写
   参数、返回、注意事项；复杂逻辑与边界条件用中文注释解释**为什么**，不复述代码在做什么。
2. **日志**：Go 侧一律用 `s.log`（`*slog.Logger`），**禁止 `fmt.Printf`**。
   `internal/store` 沿本包既有叶子层纪律——方法错误 return 前**不打日志**，由调用方带上下文记录。
   TS 侧用 `console.debug` / `console.warn`，不用 `console.log`。
3. **错误分支必须带上下文**：`fmt.Errorf("写工作台状态 %s: %w", key, err)`，不写裸 `err`。
4. **成功路径不许静默**：每个 HTTP handler 在返回前打一条结果日志（含 key 与 payload 字节数）。
5. **gofmt**：每个 Go 提交前跑 `gofmt -l internal/ | head`，输出必须为空。测试全绿 ≠ 格式干净。
6. **提交信息**：中文，`type(scope): 摘要` 格式；结尾附
   `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`。
7. **不要改动本计划范围之外的文件**。文件草稿（`fileDraft.ts`）、左栏偏好（`treePrefs.ts`）
   一律**不动**，它们继续留在 localStorage（spec §1.2 的明确决定）。
8. **测试命令**：
   - Go：`go test ./internal/store/ ./internal/agentd/ ./internal/proto/`
   - Web：`cd web && npm test`（vitest run）；类型检查 `cd web && npm run typecheck`
9. **Task 执行顺序**：**先 Task 2（proto 类型），再 Task 1（store）**——Task 1 的
   store 方法引用 `proto.WorkbenchBase`。其余一律按编号顺序执行。
10. **常量取值**（逐字照抄，不要自行调整）：
   - 基准目录行数上限 `50`
   - `payload` 字节上限 `256 * 1024`
   - 前端写回去抖 `500` ms
   - 持久化格式版本 `PERSIST_VERSION = 1`
   - 单例键名：`"selected"`、`"dock"`

---

### Task 1: store 层——两张表与它们的读写

**Files:**
- Create: `internal/store/workbench.go`
- Create: `internal/store/workbench_test.go`
- Modify: `internal/store/store.go`（在 `Open` 的 DDL 切片里追加两条建表语句）

**Interfaces:**
- Consumes: `store.Store`（已有）、`proto.WorkbenchBase`（由 Task 2 定义——
  见 Global Constraints 第 9 条，Task 2 必须先做）
- Produces:
  - `const WorkbenchBaseLimit = 50`
  - `const WorkbenchKeySelected = "selected"`
  - `const WorkbenchKeyDock = "dock"`
  - `func (s *Store) ListWorkbench() ([]proto.WorkbenchBase, map[string]string, error)`
  - `func (s *Store) PutWorkbenchBase(key, payload string, now int64) error`
  - `func (s *Store) DeleteWorkbenchBase(key string) error`
  - `func (s *Store) PutWorkbenchSingleton(key, value string, now int64) error`
  - `func (s *Store) DeleteWorkbenchSingleton(key string) error`

- [ ] **Step 1: 在 `Open` 的 DDL 切片里加两张表**

在 `internal/store/store.go` 的 `for _, ddl := range []string{ ... }` 切片末尾（`mirror_tasks`
那条之后）追加：

```go
		// 工作台状态两表（2026-08-20 状态同步 spec §4.1）。
		//
		// 为什么分两张而不是一张：workbench_bases 是「多行、有 50 行上限、按 key 索引」
		// 的那一类；workbench_singletons 装的是整个控制台只有一份的东西（当前选中目录、
		// 悬浮窗现场），永远两行封顶、不参与淘汰。形状不同，合表会让淘汰 SQL 必须
		// 额外排除单例行——那是一句迟早有人写漏的 WHERE。
		//
		// payload / value 一律是**前端序列化好的 JSON 字符串**，agentd 不解析它。
		// 这条分界是有意的：布局里加字段时后端一行都不用改。
		`CREATE TABLE IF NOT EXISTS workbench_bases (
  base_key   TEXT PRIMARY KEY,
  payload    TEXT NOT NULL,
  -- updated_at 是毫秒时间戳。用毫秒而不是秒：淘汰按它排序，秒级精度下
  -- 同一秒内写入的多行并列，被裁掉哪一条就成了随机的
  updated_at INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS workbench_singletons (
  key        TEXT PRIMARY KEY,   -- 'selected' | 'dock'
  value      TEXT NOT NULL,
  updated_at INTEGER NOT NULL)`,
```

- [ ] **Step 2: 写失败的测试**

创建 `internal/store/workbench_test.go`：

```go
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
```

- [ ] **Step 3: 跑测试确认它失败**

Run: `go test ./internal/store/ -run TestWorkbench -v`
Expected: FAIL，`undefined: WorkbenchBaseLimit` / `st.PutWorkbenchBase undefined`

- [ ] **Step 4: 写实现**

创建 `internal/store/workbench.go`：

```go
// 本文件是工作台状态两表（workbench_bases / workbench_singletons）的持久化实现。
//
// 职责：
//   - 基准目录行的写（PutWorkbenchBase，含 50 行上限的就地淘汰）、列、删
//   - 单例（当前选中目录、悬浮窗现场）的写、列、删
//
// 边界：
//   - **不解释 payload / value**：它们是前端序列化好的 JSON 字符串，本层原样搬运。
//     这条分界是有意的（spec §3）——布局形状将来加字段时后端一行都不用改
//   - 不做长度校验：payload 上限属于接口层的参数校验，在 agentd 侧做
//   - 不产生时间：now 由调用方传入，测试才能钉住淘汰顺序
//   - 与 store.go 一致的叶子层纪律：方法错误 return 前不打日志，由调用方带上下文记录
package store

import (
	"context"
	"fmt"

	"github.com/Xsxdot/handoff/internal/proto"
)

// WorkbenchBaseLimit 是 workbench_bases 的行数上限。
//
// 为什么是 50：每个 worktree 都会留一行，跑久了会攒到几百行。50 个目录远超
// 任何人同时在手的工作面，而每行 payload 只有 1–2 KiB，总量可以忽略。
// 不做「路径还在不在」的 GC——那要遍历文件系统、还要跨机器，成本远高于一行 JSON。
const WorkbenchBaseLimit = 50

// 单例键名。只有这两个，agentd 侧的接口层据此白名单校验。
const (
	WorkbenchKeySelected = "selected"
	WorkbenchKeyDock     = "dock"
)

// ListWorkbench 一次读出全部基准行与全部单例。
//
// 返回：
//   - bases: 按 updated_at 倒序（最近动过的在前）。恢复时顺序不重要，
//     但倒序让「淘汰谁」在读取结果里一眼可见，调试时省一次排序
//   - singles: 键为 WorkbenchKeySelected / WorkbenchKeyDock；不存在的键**不出现**
//   - 错误：查询失败
//
// 注意：两张表分两次查询，不在事务里。工作台状态没有跨表不变式——
// selected 指向一个已被淘汰的 base 是完全合法的（前端会退回未选中态）。
func (s *Store) ListWorkbench() ([]proto.WorkbenchBase, map[string]string, error) {
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT base_key, payload, updated_at FROM workbench_bases ORDER BY updated_at DESC`)
	if err != nil {
		return nil, nil, fmt.Errorf("查询工作台基准行: %w", err)
	}
	defer rows.Close()
	bases := []proto.WorkbenchBase{}
	for rows.Next() {
		var b proto.WorkbenchBase
		if err := rows.Scan(&b.BaseKey, &b.Payload, &b.UpdatedAt); err != nil {
			return nil, nil, fmt.Errorf("扫描工作台基准行: %w", err)
		}
		bases = append(bases, b)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("遍历工作台基准行: %w", err)
	}

	srows, err := s.db.QueryContext(context.Background(),
		`SELECT key, value FROM workbench_singletons`)
	if err != nil {
		return nil, nil, fmt.Errorf("查询工作台单例: %w", err)
	}
	defer srows.Close()
	singles := map[string]string{}
	for srows.Next() {
		var k, v string
		if err := srows.Scan(&k, &v); err != nil {
			return nil, nil, fmt.Errorf("扫描工作台单例: %w", err)
		}
		singles[k] = v
	}
	if err := srows.Err(); err != nil {
		return nil, nil, fmt.Errorf("遍历工作台单例: %w", err)
	}
	return bases, singles, nil
}

// PutWorkbenchBase 写入或覆盖一行基准状态，并就地把总行数裁到 WorkbenchBaseLimit。
//
// 参数：
//   - key: 基准目录的身份（工作树是 path 或 path@machine，见前端 workspaceBase）
//   - payload: 前端序列化好的 JSON 字符串，本层不解析
//   - now: 毫秒时间戳，由调用方给定
//
// 返回：错误为写库故障。
//
// 注意：淘汰做在写入路径而不是后台定时任务——省一个 goroutine，而且「刚写完
// 立刻裁」的时机最准（此刻的 updated_at 排序就是最终排序）。
func (s *Store) PutWorkbenchBase(key, payload string, now int64) error {
	if _, err := s.db.ExecContext(context.Background(),
		`INSERT INTO workbench_bases (base_key, payload, updated_at) VALUES (?, ?, ?)
     ON CONFLICT(base_key) DO UPDATE SET payload = excluded.payload, updated_at = excluded.updated_at`,
		key, payload, now); err != nil {
		return fmt.Errorf("写工作台基准行 %s: %w", key, err)
	}
	// 裁到上限：保留 updated_at 最大的 N 行，其余删。
	// 用子查询挑「要留下的」而不是「要删的」，因为 SQLite 的 DELETE 不支持
	// ORDER BY + LIMIT（需要编译期开关），这个写法在任何构建里都成立。
	if _, err := s.db.ExecContext(context.Background(),
		`DELETE FROM workbench_bases WHERE base_key NOT IN (
       SELECT base_key FROM workbench_bases ORDER BY updated_at DESC LIMIT ?)`,
		WorkbenchBaseLimit); err != nil {
		return fmt.Errorf("裁剪工作台基准行至 %d: %w", WorkbenchBaseLimit, err)
	}
	return nil
}

// DeleteWorkbenchBase 删除一行基准状态。行不存在时是空操作，不报错。
//
// 幂等的理由：前端在「一个目录的 tab 全关光了」时发删除，而它可能因为去抖
// 合并发两次。让第二次报错只会在控制台留下一条无意义的红。
func (s *Store) DeleteWorkbenchBase(key string) error {
	if _, err := s.db.ExecContext(context.Background(),
		`DELETE FROM workbench_bases WHERE base_key = ?`, key); err != nil {
		return fmt.Errorf("删除工作台基准行 %s: %w", key, err)
	}
	return nil
}

// PutWorkbenchSingleton 写入或覆盖一个单例。
//
// 参数：key 必须是 WorkbenchKeySelected / WorkbenchKeyDock 之一（白名单在接口层校验）；
// value 是字符串，语义由前端定义；now 是毫秒时间戳。
//
// 返回：错误为写库故障。
func (s *Store) PutWorkbenchSingleton(key, value string, now int64) error {
	if _, err := s.db.ExecContext(context.Background(),
		`INSERT INTO workbench_singletons (key, value, updated_at) VALUES (?, ?, ?)
     ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, now); err != nil {
		return fmt.Errorf("写工作台单例 %s: %w", key, err)
	}
	return nil
}

// DeleteWorkbenchSingleton 删除一个单例。不存在时是空操作，不报错（同 DeleteWorkbenchBase）。
func (s *Store) DeleteWorkbenchSingleton(key string) error {
	if _, err := s.db.ExecContext(context.Background(),
		`DELETE FROM workbench_singletons WHERE key = ?`, key); err != nil {
		return fmt.Errorf("删除工作台单例 %s: %w", key, err)
	}
	return nil
}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/store/ -run TestWorkbench -v`
Expected: PASS（三个用例全绿）

- [ ] **Step 6: 跑全量 store 测试，确认没打破既有行为**

Run: `go test ./internal/store/`
Expected: ok

- [ ] **Step 7: 检查注释与日志覆盖**

对照 Global Constraints 逐条自查：
- 新文件有「职责 + 边界」文件头注释 ✓
- 五个导出方法都有参数/返回/注意事项注释 ✓
- 三处非显然决定有「为什么」注释：50 这个数字、毫秒而非秒、`NOT IN` 子查询的写法 ✓
- 本层**不打日志**（叶子层纪律），错误全部带 key 上下文包装 ✓

- [ ] **Step 8: gofmt 与提交**

```bash
gofmt -l internal/ | head
git add internal/store/workbench.go internal/store/workbench_test.go internal/store/store.go
git commit -m "feat(store): 工作台状态两表与读写

workbench_bases 按基准目录分行、50 行上限就地淘汰；workbench_singletons
装当前选中目录与悬浮窗现场。payload 一律当不透明字符串搬运，不解析。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 2: proto 线格式类型与契约 fixture

**注意：本 task 必须在 Task 1 之前完成**（Task 1 的 store 方法引用 `proto.WorkbenchBase`）。

**Files:**
- Create: `internal/proto/workbench.go`
- Modify: `internal/proto/contract_fixture_test.go`（加 5 个样本函数与 5 个 case）
- Create（由 `-update` 生成）：`web/src/api/testdata/WorkbenchBase.json`、
  `WorkbenchStateResp.json`、`WorkbenchBaseReq.json`、`WorkbenchSelectedReq.json`、
  `WorkbenchDockReq.json`

**Interfaces:**
- Produces: `proto.WorkbenchBase` / `proto.WorkbenchStateResp` / `proto.WorkbenchBaseReq` /
  `proto.WorkbenchSelectedReq` / `proto.WorkbenchDockReq`

- [ ] **Step 1: 写类型**

创建 `internal/proto/workbench.go`：

```go
// 工作台状态同步的线格式类型（2026-08-20 状态同步 spec §4.2）。
//
// 职责：定义 /api/workbench/state 四个端点的请求/响应形状。
// 边界：
//   - 不含任何行为
//   - **Payload 一律是字符串**，内容是前端序列化好的 JSON。agentd 不解析它，
//     所以也不该让 JSON 解码器替它解析一遍（spec §4.2）
package proto

// WorkbenchBase 是一个基准目录的持久化状态行。
type WorkbenchBase struct {
	BaseKey string `json:"base_key"`
	// Payload 是前端序列化好的 JSON 字符串（布局 + 基准目录元数据）。
	Payload string `json:"payload"`
	// UpdatedAt 是毫秒时间戳。毫秒而非秒：淘汰按它排序，秒级精度下同秒写入的
	// 多行并列，裁掉哪一条就成了随机的。
	UpdatedAt int64 `json:"updated_at"`
}

// WorkbenchStateResp 是 GET /api/workbench/state 的响应。
//
// Selected / Dock 没有内容时是**空串**而不是缺键：两者都是「当前没有」这个
// 明确结论，缺键会让前端分不清它和「这版服务端还不认识这个字段」。
type WorkbenchStateResp struct {
	Selected string          `json:"selected"`
	Dock     string          `json:"dock"`
	Bases    []WorkbenchBase `json:"bases"`
}

// WorkbenchBaseReq 是 PUT /api/workbench/state/base 的请求体。
//
// Payload 用指针表达三态里的两态：**取 null = 删除该行**，否则是要写入的内容。
// 为什么不用空串当删除信号：空串是一个合法但无意义的 payload，用它当信号会让
// 「前端 bug 发了个空串」静默变成「删掉用户的布局」。
type WorkbenchBaseReq struct {
	BaseKey string  `json:"base_key"`
	Payload *string `json:"payload"`
}

// WorkbenchSelectedReq 是 PUT /api/workbench/state/selected 的请求体。
// BaseKey 为空串表示「当前没有选中任何目录」，这是合法状态，会落库成空串。
type WorkbenchSelectedReq struct {
	BaseKey string `json:"base_key"`
}

// WorkbenchDockReq 是 PUT /api/workbench/state/dock 的请求体。
// Payload 取 null = 清空悬浮窗现场，语义同 WorkbenchBaseReq.Payload。
type WorkbenchDockReq struct {
	Payload *string `json:"payload"`
}
```

- [ ] **Step 2: 加契约样本函数**

在 `internal/proto/contract_fixture_test.go` 末尾追加：

```go
// workbenchBaseSample 是一行基准状态的代表性样本。
func workbenchBaseSample() WorkbenchBase {
	return WorkbenchBase{
		BaseKey:   "/Users/dev/repo@linux-01",
		Payload:   `{"v":1,"base":{"kind":"workspace"},"wb":{"active":0}}`,
		UpdatedAt: 1755648000000,
	}
}

// workbenchStateRespSample 覆盖三个字段同时有值的情形。
func workbenchStateRespSample() WorkbenchStateResp {
	return WorkbenchStateResp{
		Selected: "/Users/dev/repo@linux-01",
		Dock:     `{"v":1,"windowOpen":true}`,
		Bases:    []WorkbenchBase{workbenchBaseSample()},
	}
}

// workbenchBaseReqSample 取「有 payload」那一支；null 那一支由 agentd 侧用例覆盖。
func workbenchBaseReqSample() WorkbenchBaseReq {
	p := `{"v":1}`
	return WorkbenchBaseReq{BaseKey: "/Users/dev/repo", Payload: &p}
}

func workbenchSelectedReqSample() WorkbenchSelectedReq {
	return WorkbenchSelectedReq{BaseKey: "/Users/dev/repo"}
}

func workbenchDockReqSample() WorkbenchDockReq {
	p := `{"v":1,"tabs":[]}`
	return WorkbenchDockReq{Payload: &p}
}
```

在 `TestContractFixtures` 的 `cases` 切片末尾（`{"ExecutorDefaultReq", ...}` 之后）追加：

```go
		{"WorkbenchBase", workbenchBaseSample()},
		{"WorkbenchStateResp", workbenchStateRespSample()},
		{"WorkbenchBaseReq", workbenchBaseReqSample()},
		{"WorkbenchSelectedReq", workbenchSelectedReqSample()},
		{"WorkbenchDockReq", workbenchDockReqSample()},
```

- [ ] **Step 3: 跑测试确认它失败**

Run: `go test ./internal/proto/ -run TestContractFixtures`
Expected: FAIL，五个新 fixture 文件不存在（读文件报 no such file）

- [ ] **Step 4: 生成 fixture**

Run: `go test ./internal/proto/ -run TestContractFixtures -update`
Expected: PASS，并在日志里看到「已重写 .../WorkbenchBase.json」等五行

- [ ] **Step 5: 不带 -update 再跑一次，确认已钉死**

Run: `go test ./internal/proto/`
Expected: ok

- [ ] **Step 6: 人眼过一遍生成的 fixture**

```bash
cat web/src/api/testdata/WorkbenchStateResp.json
```
确认 `payload` 确实是**字符串**（值被转义成 `"{\"v\":1,...}"`），而不是嵌套对象。
如果是嵌套对象，说明结构体字段类型写错了，回 Step 1 改。

- [ ] **Step 7: gofmt 与提交**

```bash
gofmt -l internal/ | head
git add internal/proto/workbench.go internal/proto/contract_fixture_test.go web/src/api/testdata/
git commit -m "feat(proto): 工作台状态同步的线格式类型与契约 fixture

payload 一律是字符串（前端序列化好的 JSON），null 表示删除该行。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 3: agentd 的四个 HTTP 端点

**Files:**
- Create: `internal/agentd/workbench_api.go`
- Create: `internal/agentd/workbench_api_test.go`
- Modify: `internal/agentd/server.go`（在路由表里加四行，位置紧跟 `GET /api/machines` 那一组）

**Interfaces:**
- Consumes: Task 1 的 `store` 五个方法；Task 2 的五个 `proto` 类型；
  已有的 `writeJSON(w, status, v)`、`s.st`（`*store.Store`）、`s.log`（`*slog.Logger`）
- Produces: 四个路由
  - `GET  /api/workbench/state`
  - `PUT  /api/workbench/state/base`
  - `PUT  /api/workbench/state/selected`
  - `PUT  /api/workbench/state/dock`

**这里刻意不接 `forwardIfRequested`**：工作台状态是**协调者本机**的东西，不按 `?machine=`
转发。布局里可以有指向远程机器目录的 tab，但那只是 payload 里的一个字段，不需要去远程机
存任何东西（spec §1.1）。

- [ ] **Step 1: 写失败的测试**

创建 `internal/agentd/workbench_api_test.go`：

```go
// workbench_api_test.go —— 工作台状态四个端点的测试（白盒包：要直接读 store 对账）。
package agentd

import (
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

// strptr 把字面量取址，供 *string 字段使用。
func strptr(s string) *string { return &s }

// TestWorkbenchStateEmpty 空库时三个字段都必须是「明确的空」而不是缺席。
func TestWorkbenchStateEmpty(t *testing.T) {
	env := newTestAgentdEnv(t)
	var resp proto.WorkbenchStateResp
	if code := env.getJSON(t, "/api/workbench/state", &resp); code != 200 {
		t.Fatalf("code = %d, want 200", code)
	}
	if resp.Selected != "" || resp.Dock != "" {
		t.Fatalf("空库时 selected/dock 应为空串，得到 %+v", resp)
	}
	if resp.Bases == nil {
		t.Fatal("bases 应是空数组而不是 null——null 会让前端多一条判空分支")
	}
	if len(resp.Bases) != 0 {
		t.Fatalf("bases = %d，期望 0", len(resp.Bases))
	}
}

// TestWorkbenchBaseRoundTrip 写一行、读回来、再删掉。
func TestWorkbenchBaseRoundTrip(t *testing.T) {
	env := newTestAgentdEnv(t)

	if code := env.putJSON(t, "/api/workbench/state/base",
		proto.WorkbenchBaseReq{BaseKey: "/repo/a", Payload: strptr(`{"v":1}`)}, nil); code != 200 {
		t.Fatalf("PUT base code = %d, want 200", code)
	}
	var resp proto.WorkbenchStateResp
	env.getJSON(t, "/api/workbench/state", &resp)
	if len(resp.Bases) != 1 || resp.Bases[0].BaseKey != "/repo/a" || resp.Bases[0].Payload != `{"v":1}` {
		t.Fatalf("读回 = %+v", resp.Bases)
	}
	if resp.Bases[0].UpdatedAt <= 0 {
		t.Fatalf("updated_at = %d，服务端必须盖上时间戳", resp.Bases[0].UpdatedAt)
	}

	// payload 取 null = 删除该行
	if code := env.putJSON(t, "/api/workbench/state/base",
		proto.WorkbenchBaseReq{BaseKey: "/repo/a", Payload: nil}, nil); code != 200 {
		t.Fatalf("PUT null code = %d, want 200", code)
	}
	resp = proto.WorkbenchStateResp{}
	env.getJSON(t, "/api/workbench/state", &resp)
	if len(resp.Bases) != 0 {
		t.Fatalf("payload=null 应删除该行，实际 = %+v", resp.Bases)
	}
}

// TestWorkbenchSelectedAndDock 覆盖两个单例，含「空串 = 清空」。
func TestWorkbenchSelectedAndDock(t *testing.T) {
	env := newTestAgentdEnv(t)

	env.putJSON(t, "/api/workbench/state/selected", proto.WorkbenchSelectedReq{BaseKey: "/repo/a"}, nil)
	env.putJSON(t, "/api/workbench/state/dock", proto.WorkbenchDockReq{Payload: strptr(`{"v":1}`)}, nil)

	var resp proto.WorkbenchStateResp
	env.getJSON(t, "/api/workbench/state", &resp)
	if resp.Selected != "/repo/a" || resp.Dock != `{"v":1}` {
		t.Fatalf("resp = %+v", resp)
	}

	// selected 写空串 = 没有选中任何目录（合法状态，不是删除信号）
	env.putJSON(t, "/api/workbench/state/selected", proto.WorkbenchSelectedReq{BaseKey: ""}, nil)
	// dock 写 null = 清空现场
	env.putJSON(t, "/api/workbench/state/dock", proto.WorkbenchDockReq{Payload: nil}, nil)

	resp = proto.WorkbenchStateResp{}
	env.getJSON(t, "/api/workbench/state", &resp)
	if resp.Selected != "" || resp.Dock != "" {
		t.Fatalf("清空后 resp = %+v", resp)
	}
}

// TestWorkbenchBaseRejects 覆盖三种 400。
func TestWorkbenchBaseRejects(t *testing.T) {
	env := newTestAgentdEnv(t)

	// ① base_key 为空
	if code := env.putJSON(t, "/api/workbench/state/base",
		proto.WorkbenchBaseReq{BaseKey: "", Payload: strptr("{}")}, nil); code != 400 {
		t.Fatalf("空 base_key code = %d, want 400", code)
	}

	// ② payload 超长
	big := strings.Repeat("x", maxWorkbenchPayload+1)
	if code := env.putJSON(t, "/api/workbench/state/base",
		proto.WorkbenchBaseReq{BaseKey: "/repo/a", Payload: &big}, nil); code != 400 {
		t.Fatalf("超长 payload code = %d, want 400", code)
	}

	// ③ dock payload 超长走同一条闸
	if code := env.putJSON(t, "/api/workbench/state/dock",
		proto.WorkbenchDockReq{Payload: &big}, nil); code != 400 {
		t.Fatalf("超长 dock code = %d, want 400", code)
	}

	// 三次拒绝之后库里必须什么都没有——400 不能有副作用
	bases, singles, err := env.st.ListWorkbench()
	if err != nil {
		t.Fatalf("ListWorkbench: %v", err)
	}
	if len(bases) != 0 || len(singles) != 0 {
		t.Fatalf("400 不该落库，bases=%v singles=%v", bases, singles)
	}
	_ = store.WorkbenchKeySelected // 引用一次，确认常量在本包可见
}

// TestWorkbenchBadJSON 坏 JSON 一律 400。
func TestWorkbenchBadJSON(t *testing.T) {
	env := newTestAgentdEnv(t)
	for _, path := range []string{
		"/api/workbench/state/base",
		"/api/workbench/state/selected",
		"/api/workbench/state/dock",
	} {
		// 传一个类型对不上的 body（base_key 应是 string，这里给数字）
		if code := env.putJSON(t, path, map[string]any{"base_key": 42, "payload": 42}, nil); code != 400 {
			t.Fatalf("%s 坏 JSON code = %d, want 400", path, code)
		}
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/agentd/ -run TestWorkbench -v`
Expected: FAIL，`undefined: maxWorkbenchPayload`，且四个路由返回 404

- [ ] **Step 3: 写实现**

创建 `internal/agentd/workbench_api.go`：

```go
// workbench_api.go —— 控制台工作台状态的 HTTP 面（2026-08-20 状态同步 spec §4.2）。
//
// 职责：
//   - GET  /api/workbench/state          一次读出全部基准行与两个单例
//   - PUT  /api/workbench/state/base     写/删一行基准状态
//   - PUT  /api/workbench/state/selected 写当前选中目录
//   - PUT  /api/workbench/state/dock     写/清空悬浮窗现场
//
// 边界：
//   - **不解释 payload**：它是前端序列化好的 JSON 字符串，本层只做长度校验。
//     后端不认识什么叫「分屏」，布局形状改了这里一行都不用动（spec §3）
//   - **不接 forwardIfRequested**：工作台状态是协调者本机的东西，不按 ?machine= 转发。
//     布局里可以有远程机器的目录，但那只是 payload 里的一个字段
//   - 不做鉴权：走 /api 既有的那一套
package agentd

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

// maxWorkbenchPayload 是单个 payload 的字节上限。
//
// 这不是防攻击——控制台会话在能力上本就等价于主令牌（POST /api/tasks/{id}/run 就是 sh -c）。
// 它防的是前端 bug：万一哪天有人把文件草稿塞进 TabContent，希望它当场 400，
// 而不是把库悄悄撑大到几百 MB。正常一行布局是 1–2 KiB，256 KiB 有两个数量级余量。
const maxWorkbenchPayload = 256 * 1024

// nowMilli 返回当前毫秒时间戳。单独抽出来是为了让将来注入假时钟只改一处。
func nowMilli() int64 { return time.Now().UnixMilli() }

// handleWorkbenchStateGet 处理 GET /api/workbench/state。
//
// 响应：200 proto.WorkbenchStateResp / 500 读库失败。
//
// 注意：Selected 与 Dock 缺席时返回**空串**而不是缺键——两者都是「当前没有」
// 这个明确结论，缺键会让前端分不清它和「这版服务端还不认识这个字段」。
// Bases 恒为数组（可能为空），不返回 null，省掉前端一条判空分支。
func (s *Server) handleWorkbenchStateGet(w http.ResponseWriter, r *http.Request) {
	bases, singles, err := s.st.ListWorkbench()
	if err != nil {
		s.log.Error("读取工作台状态失败", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	resp := proto.WorkbenchStateResp{
		Selected: singles[store.WorkbenchKeySelected],
		Dock:     singles[store.WorkbenchKeyDock],
		Bases:    bases,
	}
	s.log.Debug("工作台状态查询完成",
		"bases", len(resp.Bases), "has_selected", resp.Selected != "", "dock_bytes", len(resp.Dock))
	writeJSON(w, http.StatusOK, resp)
}

// handleWorkbenchBasePut 处理 PUT /api/workbench/state/base。
//
// 请求体 proto.WorkbenchBaseReq：Payload 取 null 表示删除该行。
// 响应：200 空对象 / 400 参数错（坏 JSON、空 base_key、payload 超长）/ 500 写库失败。
func (s *Server) handleWorkbenchBasePut(w http.ResponseWriter, r *http.Request) {
	var req proto.WorkbenchBaseReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.log.Warn("工作台基准行写入：请求体无法解码", "err", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if req.BaseKey == "" {
		s.log.Warn("工作台基准行写入：base_key 为空")
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "base_key 不能为空"})
		return
	}
	if req.Payload == nil {
		if err := s.st.DeleteWorkbenchBase(req.BaseKey); err != nil {
			s.log.Error("删除工作台基准行失败", "base_key", req.BaseKey, "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		s.log.Debug("工作台基准行已删除", "base_key", req.BaseKey)
		writeJSON(w, http.StatusOK, map[string]string{})
		return
	}
	if len(*req.Payload) > maxWorkbenchPayload {
		s.log.Warn("工作台基准行写入：payload 超长",
			"base_key", req.BaseKey, "bytes", len(*req.Payload), "limit", maxWorkbenchPayload)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "payload 超过长度上限"})
		return
	}
	if err := s.st.PutWorkbenchBase(req.BaseKey, *req.Payload, nowMilli()); err != nil {
		s.log.Error("写工作台基准行失败", "base_key", req.BaseKey, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.log.Debug("工作台基准行已写入", "base_key", req.BaseKey, "bytes", len(*req.Payload))
	writeJSON(w, http.StatusOK, map[string]string{})
}

// handleWorkbenchSelectedPut 处理 PUT /api/workbench/state/selected。
//
// 请求体 proto.WorkbenchSelectedReq。BaseKey 为空串是**合法状态**（当前没选中任何目录），
// 落库成空串而不是删行——删行与存空串在读取端等价，但存空串让「用户确实取消了选中」
// 与「从来没写过」在库里可区分，排障时有用。
//
// 响应：200 空对象 / 400 坏 JSON / 500 写库失败。
func (s *Server) handleWorkbenchSelectedPut(w http.ResponseWriter, r *http.Request) {
	var req proto.WorkbenchSelectedReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.log.Warn("选中目录写入：请求体无法解码", "err", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if len(req.BaseKey) > maxWorkbenchPayload {
		s.log.Warn("选中目录写入：base_key 超长", "bytes", len(req.BaseKey))
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "base_key 超过长度上限"})
		return
	}
	if err := s.st.PutWorkbenchSingleton(store.WorkbenchKeySelected, req.BaseKey, nowMilli()); err != nil {
		s.log.Error("写选中目录失败", "base_key", req.BaseKey, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.log.Debug("选中目录已写入", "base_key", req.BaseKey)
	writeJSON(w, http.StatusOK, map[string]string{})
}

// handleWorkbenchDockPut 处理 PUT /api/workbench/state/dock。
//
// 请求体 proto.WorkbenchDockReq：Payload 取 null 表示清空悬浮窗现场。
// 响应：200 空对象 / 400 参数错 / 500 写库失败。
func (s *Server) handleWorkbenchDockPut(w http.ResponseWriter, r *http.Request) {
	var req proto.WorkbenchDockReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.log.Warn("悬浮窗现场写入：请求体无法解码", "err", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if req.Payload == nil {
		if err := s.st.DeleteWorkbenchSingleton(store.WorkbenchKeyDock); err != nil {
			s.log.Error("清空悬浮窗现场失败", "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		s.log.Debug("悬浮窗现场已清空")
		writeJSON(w, http.StatusOK, map[string]string{})
		return
	}
	if len(*req.Payload) > maxWorkbenchPayload {
		s.log.Warn("悬浮窗现场写入：payload 超长", "bytes", len(*req.Payload), "limit", maxWorkbenchPayload)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "payload 超过长度上限"})
		return
	}
	if err := s.st.PutWorkbenchSingleton(store.WorkbenchKeyDock, *req.Payload, nowMilli()); err != nil {
		s.log.Error("写悬浮窗现场失败", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.log.Debug("悬浮窗现场已写入", "bytes", len(*req.Payload))
	writeJSON(w, http.StatusOK, map[string]string{})
}
```

- [ ] **Step 4: 注册四个路由**

在 `internal/agentd/server.go` 的路由表里，紧跟
`api.HandleFunc("GET /api/machines", s.handleMachines)` 那一行之后插入：

```go
	api.HandleFunc("GET /api/workbench/state", s.handleWorkbenchStateGet)
	api.HandleFunc("PUT /api/workbench/state/base", s.handleWorkbenchBasePut)
	api.HandleFunc("PUT /api/workbench/state/selected", s.handleWorkbenchSelectedPut)
	api.HandleFunc("PUT /api/workbench/state/dock", s.handleWorkbenchDockPut)
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestWorkbench -v`
Expected: PASS（五个用例全绿）

- [ ] **Step 6: 跑全量 agentd 测试**

Run: `go test ./internal/agentd/`
Expected: ok（这个包测试较多，耗时可能几十秒）

- [ ] **Step 7: 日志与注释自查**

- 四个 handler 的**成功路径**都打了 Debug（含 key 与字节数）✓
- 每个 400 分支打 Warn 带原因，每个 500 分支打 Error 带 `err` ✓
- 没有 `fmt.Printf` ✓
- 文件头有职责 + 三条边界；`maxWorkbenchPayload` 有「为什么不是防攻击」的说明 ✓

- [ ] **Step 8: gofmt 与提交**

```bash
gofmt -l internal/ | head
git add internal/agentd/workbench_api.go internal/agentd/workbench_api_test.go internal/agentd/server.go
git commit -m "feat(agentd): 工作台状态的四个 HTTP 端点

GET 一次读全量，三个 PUT 分别写基准行/选中目录/悬浮窗现场。
payload 只做 256 KiB 长度校验，不解析内容；不接 ?machine= 转发。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 4: 前端 API 客户端与契约接线

**Files:**
- Modify: `web/src/api/types.ts`（追加三个接口）
- Modify: `web/src/api/client.ts`（追加四个函数）
- Modify: `web/src/api/contract.test.ts`（追加五个 fixture 的断言）

**Interfaces:**
- Consumes: Task 2 生成的 `web/src/api/testdata/Workbench*.json`
- Produces:
  - `types.ts`: `WorkbenchBaseRow` / `WorkbenchStateResp`
  - `client.ts`: `fetchWorkbenchState()` / `putWorkbenchBase(baseKey, payload)` /
    `putWorkbenchSelected(baseKey)` / `putWorkbenchDock(payload)`

- [ ] **Step 1: 加类型**

在 `web/src/api/types.ts` 末尾追加：

```ts
// WorkbenchBaseRow / WorkbenchStateResp 与 internal/proto/workbench.go 对应，两边一起改。
//
// payload 是**字符串**而不是嵌套对象：agentd 不解析它，所以线上就是一段序列化好的
// JSON 文本。解析与逐字段校验全部在 app/workbench/persist.ts 里做。

// WorkbenchBaseRow 是一个基准目录的持久化状态行。
export interface WorkbenchBaseRow {
  base_key: string
  payload: string
  updated_at: number // 毫秒时间戳
}

// WorkbenchStateResp 是 GET /api/workbench/state 的响应。
// selected / dock 没有内容时是空串（不是缺键）。
export interface WorkbenchStateResp {
  selected: string
  dock: string
  bases: WorkbenchBaseRow[]
}
```

- [ ] **Step 2: 加客户端函数**

在 `web/src/api/client.ts` 里 `deletePtySession` 之后追加（并在文件顶部的 `import type`
清单里加上 `WorkbenchStateResp`）：

```ts
// fetchWorkbenchState 一次拉全工作台状态（GET /api/workbench/state）。
//
// **只在应用启动时调一次。** 不做前台唤醒时重拉：那一刻本端内存里的那份才是
// 用户刚才的现场，从服务端拉一份回来盖掉它是纯粹的坏（spec §1.6）。
export function fetchWorkbenchState(): Promise<WorkbenchStateResp> {
  return request<WorkbenchStateResp>('/api/workbench/state')
}

// putWorkbenchBase 写一行基准状态（PUT /api/workbench/state/base）。
//
// payload 传 null = 删除该行（一个目录的 tab 全关光了就该删，不存空记录）。
// 400 = base_key 为空或 payload 超过 256 KiB。
export async function putWorkbenchBase(baseKey: string, payload: string | null): Promise<void> {
  await putJSON<unknown>('/api/workbench/state/base', { base_key: baseKey, payload })
}

// putWorkbenchSelected 写「当前选中的基准目录」（PUT /api/workbench/state/selected）。
// 空串是合法值，表示当前没有选中任何目录。
export async function putWorkbenchSelected(baseKey: string): Promise<void> {
  await putJSON<unknown>('/api/workbench/state/selected', { base_key: baseKey })
}

// putWorkbenchDock 写悬浮窗现场（PUT /api/workbench/state/dock）。
// payload 传 null = 清空。
export async function putWorkbenchDock(payload: string | null): Promise<void> {
  await putJSON<unknown>('/api/workbench/state/dock', { payload })
}
```

- [ ] **Step 3: 写失败的契约测试**

在 `web/src/api/contract.test.ts` 的 import 区加：

```ts
import workbenchBaseFixture from './testdata/WorkbenchBase.json'
import workbenchStateFixture from './testdata/WorkbenchStateResp.json'
```

在 `import { type ActiveTask, ... }` 那个类型清单里加上 `WorkbenchBaseRow`、`WorkbenchStateResp`。

在文件末尾的 `describe` 里追加一个用例：

```ts
  it('WorkbenchStateResp 的 payload 是字符串而不是嵌套对象', () => {
    const base: WorkbenchBaseRow = workbenchBaseFixture
    expect(typeof base.base_key).toBe('string')
    // 这条断言是本用例存在的理由：payload 一旦被写成嵌套对象，
    // persist.ts 里的 JSON.parse 会在运行时炸，而 typecheck 未必拦得住
    expect(typeof base.payload).toBe('string')
    expect(typeof base.updated_at).toBe('number')

    const state: WorkbenchStateResp = workbenchStateFixture
    expect(typeof state.selected).toBe('string')
    expect(typeof state.dock).toBe('string')
    expect(Array.isArray(state.bases)).toBe(true)
    expect(typeof state.bases[0].payload).toBe('string')
  })
```

- [ ] **Step 4: 跑测试**

Run: `cd web && npm test -- contract`
Expected: PASS

如果报「找不到 testdata/WorkbenchBase.json」，说明 Task 2 的 `-update` 没跑或没提交，回去补。

- [ ] **Step 5: 类型检查**

Run: `cd web && npm run typecheck`
Expected: 无输出（通过）

- [ ] **Step 6: 提交**

```bash
git add web/src/api/types.ts web/src/api/client.ts web/src/api/contract.test.ts
git commit -m "feat(web/api): 工作台状态的客户端函数与契约断言

payload 在线上是字符串，契约测试把这一点钉住——写成嵌套对象会让
persist.ts 的 JSON.parse 在运行时炸。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 5: `persist.ts`——工作台侧的编解码与纯函数

**Files:**
- Create: `web/src/app/workbench/persist.ts`
- Create: `web/src/app/workbench/persist.test.ts`

**Interfaces:**
- Consumes: `./tabs` 的 `Workbench` / `TabGroup` / `Tab` / `TabContent` / `MAX_GROUPS`；
  `./useWorkbench` 的 `BaseDir`
- Produces:
  - `const PERSIST_VERSION = 1`
  - `function encodeBase(base: BaseDir, wb: Workbench): string`
  - `function decodeBase(baseKey: string, raw: string): { base: BaseDir; wb: Workbench } | null`
  - `function isEmptyWorkbench(wb: Workbench): boolean`
  - `function pruneDeadSessions(wb: Workbench, liveIds: Set<string>): Workbench`
  - `function diffPayloads(prev: Record<string, string>, next: Record<string, string>): { changed: string[]; removed: string[] }`

- [ ] **Step 1: 写失败的测试**

创建 `web/src/app/workbench/persist.test.ts`：

```ts
import { describe, expect, it } from 'vitest'
import { EMPTY_WORKBENCH, type Workbench } from './tabs'
import type { BaseDir } from './useWorkbench'
import {
  PERSIST_VERSION,
  decodeBase,
  diffPayloads,
  encodeBase,
  isEmptyWorkbench,
  pruneDeadSessions,
} from './persist'

const base: BaseDir = {
  key: '/repo/a@linux-01',
  kind: 'workspace',
  path: '/repo/a',
  label: 'feature/x',
  projectName: 'handoff',
  machine: 'linux-01',
}

// wbSample 造一个两栏、含三种 tab 的工作台。
function wbSample(): Workbench {
  return {
    groups: [
      {
        tabs: [
          { id: 't1', content: { kind: 'terminal', seq: 1, sessionId: 'S1' } },
          { id: 't2', content: { kind: 'file', rel: 'src/a.ts' } },
        ],
        activeId: 't2',
      },
      {
        tabs: [
          { id: 't3', content: { kind: 'tui', taskId: 'TASK-1' } },
          { id: 't4', content: { kind: 'blank' } },
        ],
        activeId: 't3',
      },
    ],
    active: 1,
    sizes: [2, 1],
  }
}

describe('encodeBase / decodeBase', () => {
  it('往返之后逐字段相等', () => {
    const raw = encodeBase(base, wbSample())
    expect(typeof raw).toBe('string')
    const out = decodeBase(base.key, raw)
    expect(out).not.toBeNull()
    expect(out!.base).toEqual(base)
    expect(out!.wb).toEqual(wbSample())
  })

  it('key 由行本身提供，不从 payload 里读', () => {
    const raw = encodeBase(base, wbSample())
    const out = decodeBase('/somewhere/else', raw)
    // 存下来的 base 元数据照用，但 key 必须是调用方给的那个——
    // key 是行的身份，payload 里再存一份就有了两个真相
    expect(out!.base.key).toBe('/somewhere/else')
    expect(out!.base.path).toBe('/repo/a')
  })

  it('文件 tab 的草稿不落盘', () => {
    const wb: Workbench = {
      groups: [{ tabs: [{ id: 't1', content: { kind: 'file', rel: 'a.ts', draft: '改了一半', baseSha: 'abc' } }], activeId: 't1' }],
      active: 0,
      sizes: [1],
    }
    const out = decodeBase(base.key, encodeBase(base, wb))
    const c = out!.wb.groups[0].tabs[0].content
    expect(c).toEqual({ kind: 'file', rel: 'a.ts' })
  })

  it.each([
    ['不是 JSON', 'not json at all'],
    ['版本号不认识', JSON.stringify({ v: 99, base: {}, wb: EMPTY_WORKBENCH })],
    ['缺 wb', JSON.stringify({ v: PERSIST_VERSION, base: { kind: 'workspace', path: '/a', label: 'a', projectName: '', machine: '' } })],
    ['kind 不是三种之一', JSON.stringify({ v: PERSIST_VERSION, base: { kind: 'bogus', path: '/a', label: 'a', projectName: '', machine: '' }, wb: EMPTY_WORKBENCH })],
    ['sizes 与 groups 不等长', JSON.stringify({ v: PERSIST_VERSION, base: { kind: 'workspace', path: '/a', label: 'a', projectName: '', machine: '' }, wb: { groups: [{ tabs: [], activeId: null }], active: 0, sizes: [1, 1] } })],
    ['active 越界', JSON.stringify({ v: PERSIST_VERSION, base: { kind: 'workspace', path: '/a', label: 'a', projectName: '', machine: '' }, wb: { groups: [{ tabs: [], activeId: null }], active: 5, sizes: [1] } })],
    ['tab content 种类不认识', JSON.stringify({ v: PERSIST_VERSION, base: { kind: 'workspace', path: '/a', label: 'a', projectName: '', machine: '' }, wb: { groups: [{ tabs: [{ id: 'x', content: { kind: 'video' } }], activeId: 'x' }], active: 0, sizes: [1] } })],
  ])('坏数据「%s」整行丢弃', (_name, raw) => {
    expect(decodeBase('/k', raw as string)).toBeNull()
  })
})

describe('isEmptyWorkbench', () => {
  it('所有组都没有 tab 才算空', () => {
    expect(isEmptyWorkbench(EMPTY_WORKBENCH)).toBe(true)
    expect(isEmptyWorkbench({ groups: [{ tabs: [], activeId: null }, { tabs: [], activeId: null }], active: 0, sizes: [1, 1] })).toBe(true)
    expect(isEmptyWorkbench(wbSample())).toBe(false)
  })
})

describe('pruneDeadSessions', () => {
  it('死会话的 id 被抹掉，tab 留在原位', () => {
    const out = pruneDeadSessions(wbSample(), new Set<string>())
    expect(out.groups[0].tabs[0].content).toEqual({ kind: 'terminal', seq: 1 })
    expect(out.groups[0].tabs).toHaveLength(2)
    expect(out.groups[0].activeId).toBe('t2')
  })

  it('活会话原样保留', () => {
    const out = pruneDeadSessions(wbSample(), new Set(['S1']))
    expect(out.groups[0].tabs[0].content).toEqual({ kind: 'terminal', seq: 1, sessionId: 'S1' })
  })

  it('没有 sessionId 的 tab 与其它种类不受影响', () => {
    const out = pruneDeadSessions(wbSample(), new Set<string>())
    expect(out.groups[0].tabs[1].content).toEqual({ kind: 'file', rel: 'src/a.ts' })
    expect(out.groups[1].tabs[0].content).toEqual({ kind: 'tui', taskId: 'TASK-1' })
    expect(out.groups[1].tabs[1].content).toEqual({ kind: 'blank' })
  })
})

describe('diffPayloads', () => {
  it('分出新增、变更、删除三类', () => {
    const prev = { a: '1', b: '2', c: '3' }
    const next = { a: '1', b: '9', d: '4' }
    const { changed, removed } = diffPayloads(prev, next)
    expect(changed.sort()).toEqual(['b', 'd'])
    expect(removed).toEqual(['c'])
  })

  it('完全相同时两边都是空数组', () => {
    const same = { a: '1' }
    expect(diffPayloads(same, { ...same })).toEqual({ changed: [], removed: [] })
  })
})
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npm test -- persist`
Expected: FAIL，`Failed to resolve import "./persist"`

- [ ] **Step 3: 写实现**

创建 `web/src/app/workbench/persist.ts`：

```ts
// persist.ts —— 工作台状态的编解码层（2026-08-20 状态同步 spec §5.1）。
//
// 职责：
//   - Workbench + BaseDir ↔ 落盘用的 JSON 字符串
//   - 读回来时逐字段校验，坏数据整行丢弃
//   - 规则二（抹掉已死的 sessionId）与写回时的差分，两个纯函数
//
// 边界：
//   - 不碰 React、不发请求、不认识 localStorage
//   - **不落草稿**：file tab 的 draft / baseSha 在编码时被剥掉（spec §1.2 的明确决定）
//   - 不认识「哪些会话是活的」：liveIds 由调用方给
//
// 为什么逐字段查类型而不是信 `as`：这份数据来自服务端，而服务端只是原样搬运
// 前端**上一个版本**写进去的东西。字段改名、结构变形、用户手改数据库，
// 三条路径都会让 `as` 在运行时炸在离现场很远的地方。这与 treePrefs.isPrefs 同款纪律。
import { MAX_GROUPS, type Tab, type TabContent, type TabGroup, type Workbench } from './tabs'
import type { BaseDir } from './useWorkbench'

// PERSIST_VERSION 是落盘格式版本。形状将来不兼容地变了就 +1，
// 老数据在 decodeBase 里整份丢弃——迁移一份「工作现场」不值得，重开一下就有。
export const PERSIST_VERSION = 1

// PersistedBase 是落在 payload 里的完整结构。
//
// 它同时装 BaseDir 元数据与 Workbench：只存 Workbench 的话，恢复时拿着一个 key
// 却不知道面包屑该写什么——key 本身（path@machine）还原不出 label 与 projectName。
// **不存 key**：key 是行的身份，由行本身提供；payload 里再存一份就有了两个真相。
interface PersistedBase {
  v: number
  base: Omit<BaseDir, 'key'>
  wb: Workbench
}

// encodeBase 把一个基准目录的现场序列化成 payload 字符串。
//
// 参数：base 是该目录的元数据；wb 是它的 tab 组。
// 返回：JSON 字符串，直接作为 PUT 的 payload 发出。
// 注意：file tab 的 draft / baseSha 会被剥掉，草稿继续留在 localStorage。
export function encodeBase(base: BaseDir, wb: Workbench): string {
  const payload: PersistedBase = {
    v: PERSIST_VERSION,
    base: { kind: base.kind, path: base.path, label: base.label, projectName: base.projectName, machine: base.machine },
    wb: {
      groups: wb.groups.map((g) => ({ tabs: g.tabs.map(stripTab), activeId: g.activeId })),
      active: wb.active,
      sizes: [...wb.sizes],
    },
  }
  return JSON.stringify(payload)
}

// stripTab 去掉一个 tab 里不该落盘的部分。
//
// 目前只有 file tab 的草稿两字段。写成一个独立函数而不是内联三元，
// 是为了将来再多一种「不落盘字段」时只有一处要改。
function stripTab(t: Tab): Tab {
  if (t.content.kind === 'file') {
    return { id: t.id, content: { kind: 'file', rel: t.content.rel } }
  }
  return { id: t.id, content: t.content }
}

// decodeBase 把一行 payload 解回基准目录与它的 tab 组。
//
// 参数：
//   - baseKey: 这一行的 key，直接作为返回 BaseDir 的 key
//   - raw: 服务端存的 payload 字符串
//
// 返回：解析并校验通过时返回 { base, wb }；**任何一处不对就返回 null**
//（调用方丢弃整行并 warn，绝不半信半疑地用一部分）。
export function decodeBase(baseKey: string, raw: string): { base: BaseDir; wb: Workbench } | null {
  let parsed: unknown
  try {
    parsed = JSON.parse(raw)
  } catch {
    return null
  }
  if (!isObject(parsed)) return null
  if (parsed.v !== PERSIST_VERSION) return null

  const b = parsed.base
  if (!isObject(b)) return null
  if (b.kind !== 'workspace' && b.kind !== 'home' && b.kind !== 'scratch') return null
  if (!isStr(b.path) || !isStr(b.label) || !isStr(b.projectName) || !isStr(b.machine)) return null

  const wb = parseWorkbench(parsed.wb)
  if (wb === null) return null

  return {
    base: { key: baseKey, kind: b.kind, path: b.path, label: b.label, projectName: b.projectName, machine: b.machine },
    wb,
  }
}

// parseWorkbench 校验并归一化一个 Workbench。返回 null = 形状不对。
function parseWorkbench(raw: unknown): Workbench | null {
  if (!isObject(raw)) return null
  if (!Array.isArray(raw.groups) || raw.groups.length === 0 || raw.groups.length > MAX_GROUPS) return null
  if (!Array.isArray(raw.sizes) || raw.sizes.length !== raw.groups.length) return null
  if (!raw.sizes.every((n) => typeof n === 'number' && Number.isFinite(n) && n > 0)) return null
  // active 越界会让渲染层去取 groups[5] —— 那是一次静默的 undefined，
  // 表现为「中央区一片空白但左栏是选中的」，比整行丢弃难查得多
  if (typeof raw.active !== 'number' || !Number.isInteger(raw.active)) return null
  if (raw.active < 0 || raw.active >= raw.groups.length) return null

  const groups: TabGroup[] = []
  for (const g of raw.groups) {
    if (!isObject(g) || !Array.isArray(g.tabs)) return null
    if (g.activeId !== null && !isStr(g.activeId)) return null
    const tabs: Tab[] = []
    for (const t of g.tabs) {
      if (!isObject(t) || !isStr(t.id)) return null
      const content = parseContent(t.content)
      if (content === null) return null
      tabs.push({ id: t.id, content })
    }
    // activeId 指向一个已经不在列表里的 tab 是坏数据：渲染层会显示空面板
    if (g.activeId !== null && !tabs.some((t) => t.id === g.activeId)) return null
    // 反过来，有 tab 却没有 activeId 也不成立（closeTab 保证了这条不变式）
    if (g.activeId === null && tabs.length > 0) return null
    groups.push({ tabs, activeId: g.activeId })
  }
  return { groups, active: raw.active, sizes: raw.sizes as number[] }
}

// parseContent 校验一个 tab 的内容。返回 null = 种类不认识或字段不对。
function parseContent(raw: unknown): TabContent | null {
  if (!isObject(raw)) return null
  switch (raw.kind) {
    case 'blank':
      return { kind: 'blank' }
    case 'terminal': {
      if (typeof raw.seq !== 'number' || !Number.isFinite(raw.seq)) return null
      const out: TabContent = { kind: 'terminal', seq: raw.seq }
      if (raw.sessionId !== undefined) {
        if (!isStr(raw.sessionId)) return null
        out.sessionId = raw.sessionId
      }
      if (raw.rel !== undefined) {
        if (!isStr(raw.rel)) return null
        out.rel = raw.rel
      }
      return out
    }
    case 'file':
      if (!isStr(raw.rel)) return null
      // 草稿即使被塞进来了也不采信：编码时剥掉的东西，解码时也不认
      return { kind: 'file', rel: raw.rel }
    case 'tui':
      if (!isStr(raw.taskId)) return null
      return { kind: 'tui', taskId: raw.taskId }
    default:
      return null
  }
}

function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

function isStr(v: unknown): v is string {
  return typeof v === 'string'
}

// isEmptyWorkbench 判断一个工作台是不是一个 tab 都没有。
//
// 用途：空的工作台**编码为删除**（PUT payload: null），不存一行空记录——
// 用户把一个目录的 tab 全关掉就是不想再看见它，存空记录只会白占 50 行配额里的一格。
export function isEmptyWorkbench(wb: Workbench): boolean {
  return wb.groups.every((g) => g.tabs.length === 0)
}

// pruneDeadSessions 抹掉不在 liveIds 里的 sessionId（spec §2 规则二）。
//
// 参数：wb 是刚恢复出来的工作台；liveIds 是服务端会话列表里**还活着**的那些 id。
// 返回：新的 Workbench；tab 一个都不删，只把死掉的 sessionId 字段去掉。
//
// 为什么留着 tab 而不是删掉：「我在这一栏放了个终端」本身就是布局的一部分。
// 抹掉 id 之后 TerminalTab 挂载时会原地建一个新会话，位置不变。
export function pruneDeadSessions(wb: Workbench, liveIds: Set<string>): Workbench {
  return {
    ...wb,
    groups: wb.groups.map((g) => ({
      ...g,
      tabs: g.tabs.map((t) => {
        if (t.content.kind !== 'terminal') return t
        const id = t.content.sessionId
        if (id === undefined || liveIds.has(id)) return t
        const next: TabContent = { kind: 'terminal', seq: t.content.seq }
        if (t.content.rel !== undefined) next.rel = t.content.rel
        return { id: t.id, content: next }
      }),
    })),
  }
}

// diffPayloads 比较两份「key → payload 字符串」，分出要写的与要删的。
//
// 参数：prev 是上次已落盘的快照；next 是当前应该落盘的内容。
// 返回：changed 是新增或内容变了的 key；removed 是 prev 有而 next 没有的 key。
//
// 为什么比字符串而不是比对象：payload 本来就要序列化成字符串才能发出去，
// 顺手拿它当比较依据，就不必写一个深比较，也不会因为对象字段顺序不同而误判。
export function diffPayloads(
  prev: Record<string, string>,
  next: Record<string, string>,
): { changed: string[]; removed: string[] } {
  const changed: string[] = []
  for (const [k, v] of Object.entries(next)) {
    if (prev[k] !== v) changed.push(k)
  }
  const removed: string[] = []
  for (const k of Object.keys(prev)) {
    if (!(k in next)) removed.push(k)
  }
  return { changed, removed }
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npm test -- persist`
Expected: PASS（全部用例，含 7 条坏数据参数化用例）

- [ ] **Step 5: 类型检查**

Run: `cd web && npm run typecheck`
Expected: 通过

- [ ] **Step 6: 注释自查**

- 文件头有职责 + 三条边界 + 「为什么逐字段查类型」的理由 ✓
- 六个导出符号都有注释；`PersistedBase` 说明了「为什么不存 key」✓
- 三处非显然判断有「为什么」：`active` 越界、`activeId` 指向不存在的 tab、
  `diffPayloads` 比字符串 ✓
- 本文件是纯函数层，**不打日志**——日志在调用方（Task 10）打，那里才知道是哪一行 ✓

- [ ] **Step 7: 提交**

```bash
git add web/src/app/workbench/persist.ts web/src/app/workbench/persist.test.ts
git commit -m "feat(web): 工作台状态的编解码与三个纯函数

逐字段校验，坏数据整行丢弃；草稿不落盘；死 sessionId 就地抹掉但保留 tab。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 6: `dockPersist.ts`——悬浮窗侧的编解码与几何夹紧

**Files:**
- Create: `web/src/app/homedock/dockPersist.ts`
- Create: `web/src/app/homedock/dockPersist.test.ts`

**放在 `homedock/` 而不是 `workbench/`**：悬浮窗与工作台是两套互不认识的状态
（`useHomeDock` 的注释写明了这条分界）。把它的编解码塞进 `workbench/persist.ts`
会让工作台反过来依赖 `HomeTab`，正是那条分界要避免的耦合。

**Interfaces:**
- Consumes: `./useHomeDock` 的 `HomeTab`
- Produces:
  - `const DOCK_PERSIST_VERSION = 1`
  - `interface DockSnapshot { tabs: HomeTab[]; activeId: string | null; windowOpen: boolean; geom: Geom; maximized: boolean }`
  - `interface Geom { x: number; y: number; w: number; h: number }`
  - `function encodeDock(d: DockSnapshot): string`
  - `function decodeDock(raw: string): DockSnapshot | null`
  - `function pruneDeadDockSessions(tabs: HomeTab[], liveIds: Set<string>): HomeTab[]`
  - `function clampGeom(g: Geom, vw: number, vh: number, inset: number): Geom`

- [ ] **Step 1: 写失败的测试**

创建 `web/src/app/homedock/dockPersist.test.ts`：

```ts
import { describe, expect, it } from 'vitest'
import type { HomeTab } from './useHomeDock'
import {
  DOCK_PERSIST_VERSION,
  clampGeom,
  decodeDock,
  encodeDock,
  pruneDeadDockSessions,
  type DockSnapshot,
} from './dockPersist'

function snap(): DockSnapshot {
  return {
    tabs: [
      { id: 'h1', kind: 'terminal', seq: 1, sessionId: 'S1', machine: '' },
      { id: 'h2', kind: 'file', seq: 2, machine: '', rel: 'notes.md', draft: '改了一半', baseSha: 'abc' },
    ],
    activeId: 'h2',
    windowOpen: true,
    geom: { x: 100, y: 80, w: 620, h: 340 },
    maximized: false,
  }
}

describe('encodeDock / decodeDock', () => {
  it('往返之后相等，但草稿被剥掉', () => {
    const out = decodeDock(encodeDock(snap()))
    expect(out).not.toBeNull()
    expect(out!.tabs[1]).toEqual({ id: 'h2', kind: 'file', seq: 2, machine: '', rel: 'notes.md' })
    expect(out!.tabs[0]).toEqual({ id: 'h1', kind: 'terminal', seq: 1, sessionId: 'S1', machine: '' })
    expect(out!.activeId).toBe('h2')
    expect(out!.windowOpen).toBe(true)
    expect(out!.geom).toEqual({ x: 100, y: 80, w: 620, h: 340 })
    expect(out!.maximized).toBe(false)
  })

  it.each([
    ['不是 JSON', 'nope'],
    ['版本不认识', JSON.stringify({ v: 99, tabs: [], activeId: null, windowOpen: false, geom: { x: 0, y: 0, w: 1, h: 1 }, maximized: false })],
    ['tabs 不是数组', JSON.stringify({ v: DOCK_PERSIST_VERSION, tabs: {}, activeId: null, windowOpen: false, geom: { x: 0, y: 0, w: 1, h: 1 }, maximized: false })],
    ['kind 不认识', JSON.stringify({ v: DOCK_PERSIST_VERSION, tabs: [{ id: 'h1', kind: 'video', seq: 1, machine: '' }], activeId: 'h1', windowOpen: false, geom: { x: 0, y: 0, w: 1, h: 1 }, maximized: false })],
    ['activeId 指向不存在的 tab', JSON.stringify({ v: DOCK_PERSIST_VERSION, tabs: [], activeId: 'h9', windowOpen: false, geom: { x: 0, y: 0, w: 1, h: 1 }, maximized: false })],
    ['geom 有非数字', JSON.stringify({ v: DOCK_PERSIST_VERSION, tabs: [], activeId: null, windowOpen: false, geom: { x: 0, y: 0, w: 'wide', h: 1 }, maximized: false })],
  ])('坏数据「%s」整份丢弃', (_n, raw) => {
    expect(decodeDock(raw as string)).toBeNull()
  })
})

describe('pruneDeadDockSessions', () => {
  it('死 sessionId 被抹掉，tab 留着', () => {
    const out = pruneDeadDockSessions(snap().tabs, new Set<string>())
    expect(out[0]).toEqual({ id: 'h1', kind: 'terminal', seq: 1, machine: '' })
    expect(out).toHaveLength(2)
  })

  it('活会话与非终端 tab 原样保留', () => {
    const tabs = snap().tabs
    const out = pruneDeadDockSessions(tabs, new Set(['S1']))
    expect(out[0].sessionId).toBe('S1')
    expect(out[1]).toEqual(tabs[1])
  })
})

describe('clampGeom', () => {
  it('上次在大屏、这次在小屏时把浮窗拉回视口内', () => {
    const out = clampGeom({ x: 2000, y: 1400, w: 900, h: 700 }, 1280, 800, 28)
    expect(out.x + out.w).toBeLessThanOrEqual(1280)
    expect(out.y + out.h).toBeLessThanOrEqual(800)
    expect(out.x).toBeGreaterThanOrEqual(8)
    expect(out.y).toBeGreaterThanOrEqual(28 + 8)
  })

  it('本来就在视口内的几何原样返回', () => {
    const g = { x: 100, y: 100, w: 620, h: 340 }
    expect(clampGeom(g, 1280, 800, 28)).toEqual(g)
  })

  it('尺寸不小于下界', () => {
    const out = clampGeom({ x: 0, y: 0, w: 10, h: 10 }, 1280, 800, 0)
    expect(out.w).toBeGreaterThanOrEqual(360)
    expect(out.h).toBeGreaterThanOrEqual(200)
  })

  it('视口比最小尺寸还小时，保证不出屏优先于保证最小尺寸', () => {
    const out = clampGeom({ x: 0, y: 0, w: 620, h: 340 }, 300, 150, 0)
    expect(out.x).toBe(8)
    expect(out.y).toBe(8)
    // 视口装不下 360×200 时不再强行放大，宽高被视口夹住
    expect(out.w).toBeLessThanOrEqual(300 - 8)
    expect(out.h).toBeLessThanOrEqual(150 - 8)
  })
})
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npm test -- dockPersist`
Expected: FAIL，`Failed to resolve import "./dockPersist"`

- [ ] **Step 3: 写实现**

创建 `web/src/app/homedock/dockPersist.ts`：

```ts
// dockPersist.ts —— 悬浮窗现场的编解码层（2026-08-20 状态同步 spec §5.1、§5.5）。
//
// 职责：
//   - DockSnapshot ↔ 落盘用的 JSON 字符串，读回时逐字段校验
//   - 规则二用在悬浮窗 tab 上（抹掉已死的 sessionId）
//   - 恢复几何时按**当前**视口夹紧
//
// 边界：
//   - 不碰 React、不发请求
//   - **不落草稿**：file tab 的 draft / baseSha 编码时剥掉（同工作台侧的决定）
//   - 不认识 useHomeDock 的内部 ref（seq / tabId 计数器的播种在那边做）
//
// 为什么与 workbench/persist.ts 分开：悬浮窗与工作台是两套互不认识的状态
//（见 useHomeDock 的边界注释）。合并会让工作台反过来依赖 HomeTab。
import type { HomeTab } from './useHomeDock'

// DOCK_PERSIST_VERSION 与工作台侧的 PERSIST_VERSION 各自独立：
// 两份数据形状无关，一边改了不该让另一边的老数据一起作废。
export const DOCK_PERSIST_VERSION = 1

// Geom 是浮窗在视口里的位置与尺寸，单位 px。
export interface Geom {
  x: number
  y: number
  w: number
  h: number
}

// DockSnapshot 是悬浮窗的完整现场。
export interface DockSnapshot {
  tabs: HomeTab[]
  activeId: string | null
  windowOpen: boolean
  geom: Geom
  maximized: boolean
}

// 几何夹紧用的下界，与 useHomeDock 里那四个常量一一对应。
//
// 为什么在这里重复一遍而不是从 useHomeDock 导出：那边它们是模块私有常量，
// 导出会把「浮窗内部尺寸约定」变成公开接口。四个数字重复的代价，小于
// 多一个跨模块耦合点；两边同时要改的场景（改浮窗最小尺寸）本来就要一起看。
const MIN_W = 360
const MIN_H = 200
const MARGIN = 8

// encodeDock 把悬浮窗现场序列化成 payload 字符串。
//
// 参数：d 是当前现场。
// 返回：JSON 字符串。file tab 的 draft / baseSha 被剥掉。
export function encodeDock(d: DockSnapshot): string {
  return JSON.stringify({
    v: DOCK_PERSIST_VERSION,
    tabs: d.tabs.map(stripDockTab),
    activeId: d.activeId,
    windowOpen: d.windowOpen,
    geom: { x: d.geom.x, y: d.geom.y, w: d.geom.w, h: d.geom.h },
    maximized: d.maximized,
  })
}

// stripDockTab 去掉一个悬浮窗 tab 里不该落盘的部分（目前只有草稿两字段）。
function stripDockTab(t: HomeTab): HomeTab {
  const out: HomeTab = { id: t.id, kind: t.kind, seq: t.seq, machine: t.machine }
  if (t.sessionId !== undefined) out.sessionId = t.sessionId
  if (t.rel !== undefined) out.rel = t.rel
  return out
}

// decodeDock 把 payload 解回悬浮窗现场。
//
// 参数：raw 是服务端存的字符串。
// 返回：校验通过时返回 DockSnapshot；**任何一处不对就返回 null**，调用方整份丢弃。
export function decodeDock(raw: string): DockSnapshot | null {
  let parsed: unknown
  try {
    parsed = JSON.parse(raw)
  } catch {
    return null
  }
  if (!isObject(parsed)) return null
  if (parsed.v !== DOCK_PERSIST_VERSION) return null
  if (typeof parsed.windowOpen !== 'boolean' || typeof parsed.maximized !== 'boolean') return null
  if (parsed.activeId !== null && typeof parsed.activeId !== 'string') return null
  if (!Array.isArray(parsed.tabs)) return null

  const geom = parseGeom(parsed.geom)
  if (geom === null) return null

  const tabs: HomeTab[] = []
  for (const t of parsed.tabs) {
    if (!isObject(t)) return null
    if (typeof t.id !== 'string' || typeof t.machine !== 'string') return null
    if (typeof t.seq !== 'number' || !Number.isFinite(t.seq)) return null
    if (t.kind !== 'terminal' && t.kind !== 'file') return null
    const tab: HomeTab = { id: t.id, kind: t.kind, seq: t.seq, machine: t.machine }
    if (t.sessionId !== undefined) {
      if (typeof t.sessionId !== 'string') return null
      tab.sessionId = t.sessionId
    }
    if (t.rel !== undefined) {
      if (typeof t.rel !== 'string') return null
      tab.rel = t.rel
    }
    tabs.push(tab)
  }
  // activeId 指向一个不存在的 tab，浮窗会显示一片空白且没人能解释为什么
  if (parsed.activeId !== null && !tabs.some((t) => t.id === parsed.activeId)) return null

  return { tabs, activeId: parsed.activeId, windowOpen: parsed.windowOpen, geom, maximized: parsed.maximized }
}

function parseGeom(raw: unknown): Geom | null {
  if (!isObject(raw)) return null
  const nums = [raw.x, raw.y, raw.w, raw.h]
  if (!nums.every((n) => typeof n === 'number' && Number.isFinite(n))) return null
  return { x: raw.x as number, y: raw.y as number, w: raw.w as number, h: raw.h as number }
}

function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

// pruneDeadDockSessions 抹掉不在 liveIds 里的 sessionId（spec §2 规则二）。
//
// 参数：tabs 是刚恢复出来的悬浮窗 tab；liveIds 是还活着的会话 id。
// 返回：新数组；tab 一个都不删，只去掉死掉的 sessionId 字段。
export function pruneDeadDockSessions(tabs: HomeTab[], liveIds: Set<string>): HomeTab[] {
  return tabs.map((t) => {
    if (t.kind !== 'terminal' || t.sessionId === undefined) return t
    if (liveIds.has(t.sessionId)) return t
    const out: HomeTab = { id: t.id, kind: t.kind, seq: t.seq, machine: t.machine }
    if (t.rel !== undefined) out.rel = t.rel
    return out
  })
}

// clampGeom 把恢复出来的几何夹进当前视口。
//
// 参数：
//   - g: 上次落盘的几何
//   - vw / vh: 当前视口宽高
//   - inset: 页面顶部要让出的高度（桌面薄壳的拖动区，浏览器里为 0）
//
// 返回：夹紧后的几何。
//
// 为什么必须夹：上次在 27 寸屏上摆到 x=2000，这次在笔记本上打开，不夹就是一个
// 看不见的浮窗——用户会以为悬浮窗坏了。
//
// 夹紧次序是**先尺寸后位置**，且视口装不下最小尺寸时「不出屏」优先于「不小于下界」：
// 一个比最小尺寸还小、但看得见的浮窗，好过一个尺寸达标却在屏幕外的浮窗。
export function clampGeom(g: Geom, vw: number, vh: number, inset: number): Geom {
  const topLimit = inset + MARGIN
  // 可用区：扣掉四周边距与顶部让位
  const maxW = Math.max(1, vw - MARGIN * 2)
  const maxH = Math.max(1, vh - topLimit - MARGIN)

  const w = Math.min(Math.max(MIN_W, g.w), maxW)
  const h = Math.min(Math.max(MIN_H, g.h), maxH)
  const x = Math.min(Math.max(MARGIN, g.x), Math.max(MARGIN, vw - MARGIN - w))
  const y = Math.min(Math.max(topLimit, g.y), Math.max(topLimit, vh - MARGIN - h))
  return { x, y, w, h }
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npm test -- dockPersist`
Expected: PASS

如果「视口比最小尺寸还小」那条用例失败，检查 `maxW` / `maxH` 的算法：
`Math.min(Math.max(MIN_W, g.w), maxW)` 的次序决定了「视口优先」，
写成 `Math.max(MIN_W, Math.min(g.w, maxW))` 就变成「最小尺寸优先」，会出屏。

- [ ] **Step 5: 类型检查**

Run: `cd web && npm run typecheck`
Expected: 通过

- [ ] **Step 6: 注释自查**

- 文件头有职责 + 三条边界 + 「为什么与 workbench/persist.ts 分开」✓
- 常量重复那三行有「为什么不从 useHomeDock 导出」的说明 ✓
- `clampGeom` 说明了「为什么必须夹」与「视口优先于最小尺寸」的次序理由 ✓
- 纯函数层不打日志 ✓

- [ ] **Step 7: 提交**

```bash
git add web/src/app/homedock/dockPersist.ts web/src/app/homedock/dockPersist.test.ts
git commit -m "feat(web): 悬浮窗现场的编解码与几何夹紧

草稿不落盘；死 sessionId 抹掉但保留 tab；恢复几何按当前视口夹紧，
视口装不下时不出屏优先于最小尺寸。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 7: 基准目录的 key 加上机器维度

**Files:**
- Modify: `web/src/app/tree/ProjectTree.tsx`（`workspaceBase` 的 key；`selectedKey === ws.path` 那一处比较）
- Modify: `web/src/app/workbench/usePtyRestore.ts`（`baseOfSession` 的 workspace 分支）
- Modify: `web/src/app/tree/ProjectTree.test.tsx` 或新建断言（见 Step 1）
- Create: `web/src/app/workbench/baseKey.test.ts`

**为什么现在做**：`workspaceBase()` 返回的 `key` 就是 `ws.path`，不带机器名；而
`baseOfSession()` 里 home 基准是明确按 `~@machine` 分开的（注释：「远端 home 与本机 home
必须分开：路径都叫『~』，但它们是两台机器上的两个目录」）。同一条道理对工作树成立，
工作树这边却没做——两台机器上出现同路径的工作树时，它们的 tab 组会撞进同一个 key。
今天这是内存态、影响面小；**一旦落盘就被固化成主键**，以后再改要迁移数据。

**Interfaces:**
- Produces: `workspaceBase()` 返回的 `key` 变为 `machine ? \`${path}@${machine}\` : path`；
  `baseOfSession()` 的 workspace 分支产出同一个 key

- [ ] **Step 1: 写失败的测试**

创建 `web/src/app/workbench/baseKey.test.ts`：

```ts
// 两处 key 生成必须产出同一个字符串。对不上就会出现「左栏点进这个目录，
// 恢复出来的终端却在另一个组里」——这是 usePtyRestore 里早就写下的告诫。
import { describe, expect, it } from 'vitest'
import { workspaceBase } from '../tree/ProjectTree'
import { baseOfSession } from './usePtyRestore'
import type { PtySession } from '../../api/types'

const project = { project_id: 'p1', name: 'handoff', locations: [] } as never
const ws = { path: '/repo/a', branch: 'feature/x', is_main: false } as never

function session(machine: string): PtySession {
  return {
    id: 'S1', machine, base_path: '/repo/a', base_kind: 'workspace', shell: '/bin/bash',
    created_at: '2026-08-20T10:00:00+08:00', cols: 80, rows: 24, attached: 0,
    foreground: false, pid: 1, bytes_out: 0,
  }
}

describe('工作树基准的 key 带机器维度', () => {
  it('本机（machine 为空串）时 key 逐字节等于 path', () => {
    expect(workspaceBase(project, '', ws).key).toBe('/repo/a')
    expect(baseOfSession(session('')).key).toBe('/repo/a')
  })

  it('远端机器时 key 是 path@machine', () => {
    expect(workspaceBase(project, 'linux-01', ws).key).toBe('/repo/a@linux-01')
    expect(baseOfSession(session('linux-01')).key).toBe('/repo/a@linux-01')
  })

  it('两台机器上同路径的工作树不再撞 key', () => {
    const a = workspaceBase(project, 'linux-01', ws).key
    const b = workspaceBase(project, 'win-b37', ws).key
    expect(a).not.toBe(b)
  })

  it('两处生成器对同一台机器产出同一个 key', () => {
    for (const m of ['', 'linux-01', 'win-b37']) {
      expect(workspaceBase(project, m, ws).key).toBe(baseOfSession(session(m)).key)
    }
  })
})
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npm test -- baseKey`
Expected: FAIL，远端机器那两条断言拿到 `/repo/a` 而不是 `/repo/a@linux-01`

- [ ] **Step 3: 改 `workspaceBase`**

在 `web/src/app/tree/ProjectTree.tsx` 里，把 `workspaceBase` 改成：

```tsx
// workspaceBase 把树上的一个目录节点做成中央工作台的基准。
//
// key 必须带机器维度：两台机器上完全可能出现同路径的工作树（同一个项目
// 在两台开发机上 clone 到同一个位置），不带机器名它们的 tab 组会撞进同一个
// key 里混在一起。形状与 home 基准的 `~` / `~@machine` 同构。
//
// machine 为空串（本机）时 key **逐字节等于 path**，与改动前完全一致——
// 单机用户的既有行为不受影响。
export function workspaceBase(project: ProjectNode, machine: string, ws: Workspace): BaseDir {
  return {
    key: machine ? `${ws.path}@${machine}` : ws.path,
    kind: 'workspace',
    path: ws.path,
    label: dirLabel(ws),
    projectName: project.name,
    machine,
  }
}
```

- [ ] **Step 4: 修掉那处绕过 `workspaceBase` 的比较**

同文件里 `splitIdleWorkspaces` 的回调中有一行 `selected: selectedKey === ws.path`
（约在 524 行）。它**绕过了 `workspaceBase`** 直接拿 path 比，改 key 之后远端目录
会永远判为「未选中」，于是被当成空闲目录折叠掉——用户选中的那一行会自己消失。

改成：

```tsx
                            // 必须走 workspaceBase 而不是直接比 path：key 带机器维度之后
                            // 拿 path 比会让远端目录永远判为未选中，进而被当成空闲折叠掉
                            selected: selectedKey === workspaceBase(project, loc.machine, ws).key,
```

- [ ] **Step 5: 改 `baseOfSession`**

在 `web/src/app/workbench/usePtyRestore.ts` 里，把 workspace 分支改成：

```ts
  const name = s.base_path.split('/').filter(Boolean).pop() ?? s.base_path
  // key 与 ProjectTree.workspaceBase 必须逐字节一致，含机器维度——
  // 两边对不上就会出现「左栏点进这个目录，恢复出来的终端却在另一个组里」
  return {
    key: s.machine ? `${s.base_path}@${s.machine}` : s.base_path,
    kind: 'workspace',
    path: s.base_path,
    label: name,
    projectName: '',
    machine: s.machine,
  }
```

- [ ] **Step 6: 跑测试确认通过**

Run: `cd web && npm test -- baseKey`
Expected: PASS

- [ ] **Step 7: 跑全量前端测试，抓出别处对 key 的隐含假设**

Run: `cd web && npm test`
Expected: 全绿。若 `ProjectTree.test.tsx` 或 `usePtyRestore.test.ts` 里有断言写死了
`key: '/repo/a'` 之类的期望值且用例给了非空 machine，按新规则更新期望值——
**不要**为了让测试变绿而把实现改回去。

- [ ] **Step 8: 类型检查与提交**

```bash
cd web && npm run typecheck && cd ..
git add web/src/app/tree/ProjectTree.tsx web/src/app/workbench/usePtyRestore.ts web/src/app/workbench/baseKey.test.ts
git commit -m "fix(web): 工作树基准的 key 加上机器维度

两台机器上同路径的工作树原本会撞进同一个 key。与 home 的 ~@machine 同构；
machine 为空时 key 逐字节不变。顺带修掉左栏一处绕过 workspaceBase 直接比
path 的判断——它会让远端选中目录被当成空闲折叠掉。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 8: `useWorkbench` 暴露 `byBase` / `baseDirs` 与 `hydrate`

**Files:**
- Modify: `web/src/app/workbench/useWorkbench.ts`
- Modify: `web/src/app/workbench/useWorkbench.test.ts`

**Interfaces:**
- Consumes: `./tabs` 的 `Workbench`、`EMPTY_WORKBENCH`
- Produces（`WorkbenchApi` 新增三项，既有十几个动作签名**一个都不动**）：
  - `byBase: Record<string, Workbench>`
  - `baseDirs: Record<string, BaseDir>`
  - `hydrate: (entries: Array<{ base: BaseDir; wb: Workbench }>) => void`

**为什么要 `baseDirs`**：`byBase` 只有 `key → Workbench`，没有 `BaseDir`。写回时要把
基准元数据（`label` / `projectName` / `machine` / `path`）一起编码进 payload，而当前
基准之外的那些目录，`useWorkbench` 现在根本没留它们的 `BaseDir`。不加这张表就没法编码。

**`hydrate` 刻意不管选中**：`selected` 的恢复要等项目树加载完（spec §6），
在这里 select 会让水合直接把用户丢进一个树还没到、面包屑是空的界面。

- [ ] **Step 1: 写失败的测试**

在 `web/src/app/workbench/useWorkbench.test.ts` 末尾追加（沿用该文件已有的
`renderHook` / `act` 引入方式；若文件用的是别的测试工具，照它的写法改）：

```ts
  it('hydrate 灌入多个目录的 tab 组，且不改变当前选中', () => {
    const { result } = renderHook(() => useWorkbench())
    const a: BaseDir = { key: '/repo/a', kind: 'workspace', path: '/repo/a', label: 'a', projectName: 'p', machine: '' }
    const b: BaseDir = { key: '/repo/b@m1', kind: 'workspace', path: '/repo/b', label: 'b', projectName: 'p', machine: 'm1' }
    const wbA: Workbench = { groups: [{ tabs: [{ id: 't1', content: { kind: 'blank' } }], activeId: 't1' }], active: 0, sizes: [1] }
    const wbB: Workbench = { groups: [{ tabs: [{ id: 't2', content: { kind: 'tui', taskId: 'T' } }], activeId: 't2' }], active: 0, sizes: [1] }

    act(() => result.current.hydrate([{ base: a, wb: wbA }, { base: b, wb: wbB }]))

    // 水合不选中任何目录——那要等项目树到位（spec §6）
    expect(result.current.base).toBeNull()
    expect(result.current.byBase['/repo/a']).toEqual(wbA)
    expect(result.current.byBase['/repo/b@m1']).toEqual(wbB)
    expect(result.current.baseDirs['/repo/b@m1']).toEqual(b)
  })

  it('baseDirs 会随 select / open 记住每个基准的元数据', () => {
    const { result } = renderHook(() => useWorkbench())
    const a: BaseDir = { key: '/repo/a', kind: 'workspace', path: '/repo/a', label: 'a', projectName: 'p', machine: '' }
    act(() => result.current.select(a))
    expect(result.current.baseDirs['/repo/a']).toEqual(a)
  })

  it('restoreTerminal 也会登记 baseDirs（它写的是非当前基准）', () => {
    const { result } = renderHook(() => useWorkbench())
    const b: BaseDir = { key: '/repo/b@m1', kind: 'workspace', path: '/repo/b', label: 'b', projectName: '', machine: 'm1' }
    act(() => result.current.restoreTerminal(b, 'S1'))
    expect(result.current.base).toBeNull()
    expect(result.current.baseDirs['/repo/b@m1']).toEqual(b)
    expect(result.current.byBase['/repo/b@m1'].groups[0].tabs).toHaveLength(1)
  })
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npm test -- useWorkbench`
Expected: FAIL，`result.current.hydrate is not a function`

- [ ] **Step 3: 改实现**

在 `web/src/app/workbench/useWorkbench.ts` 里：

① 在 `WorkbenchApi` 接口里追加三项（放在 `restoreTerminal` 之后）：

```ts
  // byBase 是全部基准目录的 tab 组，**只读**。持久化层要监听它整体做差分，
  // 只盯当前基准是不够的——restoreTerminal 会写非当前基准的行。
  byBase: Record<string, Workbench>
  // baseDirs 是每个 key 对应的基准元数据。
  //
  // 为什么必须单独存一张：byBase 只有 key → Workbench，而落盘时要把 label /
  // projectName / machine / path 一起编码进 payload（恢复时 key 还原不出它们）。
  // 当前基准之外的那些目录，除了这张表没有别的地方留着它们的 BaseDir。
  baseDirs: Record<string, BaseDir>
  // hydrate 一次性灌入落盘恢复出来的全部 tab 组。
  //
  // **刻意不管选中**：selected 的恢复要等项目树加载完（spec §6），在这里 select
  // 会把用户丢进一个树还没到、面包屑是空的界面。
  hydrate: (entries: Array<{ base: BaseDir; wb: Workbench }>) => void
```

② 在 hook 体里，`byBase` 的 `useState` 之后加一个平行的状态：

```ts
  // baseDirs 与 byBase 同生命周期：凡是写进 byBase 的 key，元数据都在这里
  const [baseDirs, setBaseDirs] = useState<Record<string, BaseDir>>({})
```

③ 在 `select` 里登记元数据：

```ts
  const select = useCallback((b: BaseDir) => {
    baseRef.current = b
    setBase(b)
    // 登记元数据：落盘时要用它编码 payload。用 updater 里的浅比较避免
    // 每次 select 都产生一个新对象引发无谓的重渲染
    setBaseDirs((prev) => (prev[b.key] === b ? prev : { ...prev, [b.key]: b }))
  }, [])
```

④ 在 `mutate` 里，显式给了基准时也登记（`select` 已覆盖切基准那一路，
但 `mutate` 传的 `b` 与当前基准同 key 时不会走 `select`）：

```ts
  const mutate = useCallback(
    (fn: (w: Workbench) => Workbench, b?: BaseDir) => {
      const target = b ?? baseRef.current
      if (!target) return
      if (b && b.key !== baseRef.current?.key) select(b)
      setBaseDirs((prev) => (prev[target.key] ? prev : { ...prev, [target.key]: target }))
      setByBase((prev) => ({ ...prev, [target.key]: fn(prev[target.key] ?? EMPTY_WORKBENCH) }))
    },
    [select],
  )
```

⑤ 在 `restoreTerminal` 里登记（它不走 `mutate`）：

```ts
  const restoreTerminal = useCallback((b: BaseDir, sessionId: string) => {
    setBaseDirs((prev) => (prev[b.key] ? prev : { ...prev, [b.key]: b }))
    setByBase((prev) => {
      const w = prev[b.key] ?? EMPTY_WORKBENCH
      return { ...prev, [b.key]: openTab(w, { kind: 'terminal', seq: nextTerminalSeq(w), sessionId }) }
    })
  }, [])
```

⑥ 新增 `hydrate`：

```ts
  // hydrate 一次性灌入恢复出来的全部 tab 组。见接口注释：不碰选中态。
  //
  // 用整体替换而不是逐条合并：水合只在应用启动时发生一次，那时 byBase 必然是空的。
  // 写成合并会让「重复调用」看起来是安全的，而它其实会把用户启动后新开的 tab
  // 与一份陈旧快照混在一起——那种状态没人能解释。
  const hydrate = useCallback((entries: Array<{ base: BaseDir; wb: Workbench }>) => {
    const nextBases: Record<string, Workbench> = {}
    const nextDirs: Record<string, BaseDir> = {}
    for (const e of entries) {
      nextBases[e.base.key] = e.wb
      nextDirs[e.base.key] = e.base
    }
    setByBase(nextBases)
    setBaseDirs(nextDirs)
  }, [])
```

⑦ 在 return 里加上三项：

```ts
  return { base, wb, byBase, baseDirs, select, open, openTerminal, close, closeById, activate, setContent, split, splitAt, openInNewPane, resize, restoreTerminal, hydrate }
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npm test -- useWorkbench`
Expected: PASS（含既有全部用例）

- [ ] **Step 5: 类型检查与全量测试**

Run: `cd web && npm run typecheck && npm test`
Expected: 全绿

- [ ] **Step 6: 注释自查**

- 三个新接口成员都有注释，`baseDirs` 说明了「为什么必须单独存一张」✓
- `hydrate` 说明了「为什么整体替换而不是合并」✓
- 本文件是状态容器，不打日志（日志在 Task 10 的 sync hook 里打）✓

- [ ] **Step 7: 提交**

```bash
git add web/src/app/workbench/useWorkbench.ts web/src/app/workbench/useWorkbench.test.ts
git commit -m "feat(web): useWorkbench 暴露 byBase/baseDirs 与 hydrate

持久化层要监听 byBase 整体做差分；baseDirs 补上「非当前基准的元数据」
这个原本没人留着的东西。hydrate 不碰选中态——那要等项目树。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 9: `useHomeDock` 的 `hydrate` 与计数器播种

**Files:**
- Modify: `web/src/app/homedock/useHomeDock.ts`
- Modify: `web/src/app/homedock/useHomeDock.test.ts`

**Interfaces:**
- Consumes: Task 6 的 `DockSnapshot`
- Produces: `HomeDockApi` 新增 `hydrate: (s: DockSnapshot) => void`

**这个 task 最容易被做成「看起来是好的」**：不播种两个计数器，功能在恢复后
**第一次新建 tab 时**才炸——`tabIdCounter` 从 0 起，恢复出 `h1..h5` 之后
`newTerminal` 会生成 `h1`，与已存在的 tab 撞 id；`seqCounter` 同理，会出现
两个 `bash · home 3`。测试必须直接钉住这一条。

- [ ] **Step 1: 写失败的测试**

在 `web/src/app/homedock/useHomeDock.test.ts` 末尾追加：

```ts
  it('hydrate 之后新建 tab 不与恢复出来的撞 id / seq', () => {
    const { result } = renderHook(() => useHomeDock())
    act(() =>
      result.current.hydrate({
        tabs: [
          { id: 'h1', kind: 'terminal', seq: 1, sessionId: 'S1', machine: '' },
          { id: 'h5', kind: 'terminal', seq: 7, machine: '' },
        ],
        activeId: 'h5',
        windowOpen: true,
        geom: { x: 100, y: 100, w: 620, h: 340 },
        maximized: false,
      }),
    )
    expect(result.current.tabs).toHaveLength(2)
    expect(result.current.windowOpen).toBe(true)
    expect(result.current.activeId).toBe('h5')

    act(() => result.current.newTerminal())
    const fresh = result.current.tabs[2]
    // id 必须跳过已恢复的 h5
    expect(result.current.tabs.map((t) => t.id)).toHaveLength(new Set(result.current.tabs.map((t) => t.id)).size)
    expect(fresh.id).toBe('h6')
    // seq 必须跳过已恢复的 7
    expect(fresh.seq).toBe(8)
  })

  it('adopt 进来的 sessionId 形 id 不参与播种', () => {
    const { result } = renderHook(() => useHomeDock())
    act(() =>
      result.current.hydrate({
        // 孤儿会话被 adopt 时 id 就是 sessionId（见 Shell 的调用），不是 h<n> 形状
        tabs: [{ id: '7ec762e7-3bd2-412c-a39c-e4cf8b4057ad', kind: 'terminal', seq: 3, sessionId: 'S9', machine: '' }],
        activeId: '7ec762e7-3bd2-412c-a39c-e4cf8b4057ad',
        windowOpen: false,
        geom: { x: 10, y: 40, w: 620, h: 340 },
        maximized: false,
      }),
    )
    act(() => result.current.newTerminal())
    expect(result.current.tabs[1].id).toBe('h1')
    expect(result.current.tabs[1].seq).toBe(4)
  })

  it('hydrate 之后再打开浮窗不会把恢复的位置冲掉', () => {
    const { result } = renderHook(() => useHomeDock())
    const geom = { x: 123, y: 234, w: 620, h: 340 }
    act(() => result.current.hydrate({ tabs: [], activeId: null, windowOpen: false, geom, maximized: false }))
    // newTerminal 内部会 openWindow；placed 已被 hydrate 置 true，不该重摆
    act(() => result.current.newTerminal())
    expect(result.current.geom).toEqual(geom)
  })
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npm test -- useHomeDock`
Expected: FAIL，`result.current.hydrate is not a function`

- [ ] **Step 3: 改实现**

在 `web/src/app/homedock/useHomeDock.ts` 里：

① 顶部加 import：

```ts
import type { DockSnapshot } from './dockPersist'
```

② 在 `HomeDockApi` 接口里追加：

```ts
  // hydrate 一次性灌入落盘恢复出来的悬浮窗现场。
  //
  // 与 adopt 的关键差别：adopt 收编的是「用户不知道存在的孤儿会话」，所以刻意
  // 不打开浮窗；hydrate 恢复的是「用户上次亲手开着的窗」，windowOpen 照实还原。
  hydrate: (s: DockSnapshot) => void
```

③ 在 hook 体里、`adopt` 之前加实现：

```ts
  // hydrate 恢复整份现场，并**播种两个计数器**。
  //
  // 播种这一步是承重的：tabIdCounter / seqCounter 都从 0 起，恢复出 h1..h5 之后
  // 再点「新建终端」会生成 h1——与已存在的 tab 撞 id，React 的 key 与 activate
  // 全部错乱；seq 同理会出现两个 'bash · home 3'。不播种的话功能看起来是好的，
  // 只在用户恢复后新建第一个 tab 时炸。
  //
  // id 只从 /^h(\d+)$/ 里取最大值：adopt 收编的孤儿会话其 id 是 **sessionId**
  //（见 Shell 里 dock.adopt 的调用），不是 h<n> 形状，拿它去 parseInt 会得到 NaN。
  const hydrate = useCallback((s: DockSnapshot) => {
    setTabs(s.tabs)
    setActiveId(s.activeId)
    setWindowOpen(s.windowOpen)
    setGeomState(s.geom)
    setMaximized(s.maximized)
    // 恢复出来的几何就是用户上次亲手摆的位置，不能被「第一次打开时按视口重摆」冲掉
    placed.current = true

    let maxTabId = 0
    let maxSeq = 0
    for (const t of s.tabs) {
      const m = /^h(\d+)$/.exec(t.id)
      if (m) maxTabId = Math.max(maxTabId, Number(m[1]))
      if (Number.isFinite(t.seq)) maxSeq = Math.max(maxSeq, t.seq)
    }
    tabIdCounter.current = Math.max(tabIdCounter.current, maxTabId)
    seqCounter.current = Math.max(seqCounter.current, maxSeq)
  }, [])
```

④ 在 return 里加上 `hydrate`。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npm test -- useHomeDock`
Expected: PASS（含既有全部用例）

- [ ] **Step 5: 类型检查与全量测试**

Run: `cd web && npm run typecheck && npm test`
Expected: 全绿

- [ ] **Step 6: 注释自查**

- `hydrate` 的接口注释说清了与 `adopt` 的差别 ✓
- 实现处的注释说清了「播种是承重的、不做会在什么时候炸」与「为什么只认 h<n>」✓
- `placed.current = true` 那一行有「为什么」✓

- [ ] **Step 7: 提交**

```bash
git add web/src/app/homedock/useHomeDock.ts web/src/app/homedock/useHomeDock.test.ts
git commit -m "feat(web): useHomeDock 支持 hydrate，并播种 id/seq 两个计数器

不播种的话恢复后新建的第一个 tab 会与已恢复的撞 id——功能看起来是好的，
只在那一刻炸。adopt 进来的 sessionId 形 id 不参与播种。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 10: `restore.ts`——把落盘状态与会话列表合成恢复结果

**Files:**
- Create: `web/src/app/workbench/restore.ts`
- Create: `web/src/app/workbench/restore.test.ts`

**为什么单独一层纯函数**：恢复逻辑里全部的判断（哪些 sessionId 死了、哪些会话是孤儿、
孤儿该进工作台还是悬浮窗、几何要不要夹）都不需要 React、不需要网络。把它们留在 hook 里
就只能靠 mock fetch 去测，而这些判断恰恰是最需要用表驱动一条条钉住的部分。

**Interfaces:**
- Consumes: `./persist` 的 `decodeBase` / `pruneDeadSessions`；
  `../homedock/dockPersist` 的 `decodeDock` / `pruneDeadDockSessions` / `clampGeom` / `DockSnapshot`；
  `./tabs` 的 `openTab` / `nextTerminalSeq` / `EMPTY_WORKBENCH` / `Workbench`；
  `./usePtyRestore` 的 `baseOfSession`（Task 12 会把它搬到本文件，见下）；
  `../../api/types` 的 `PtySession` / `WorkbenchStateResp`
- Produces:
  - `function baseOfSession(s: PtySession): BaseDir`（**从 `usePtyRestore.ts` 搬过来**，
    连同它的注释；`usePtyRestore.ts` 在 Task 12 被删除）
  - `interface RestoreInput { state, sessions, vw, vh, inset }`
  - `interface RestoreResult { entries, dock, dockOrphans, selected, dropped, pruned, adopted }`
  - `function buildRestore(input: RestoreInput): RestoreResult`

- [ ] **Step 1: 写失败的测试**

创建 `web/src/app/workbench/restore.test.ts`：

```ts
import { describe, expect, it } from 'vitest'
import type { PtySession, WorkbenchStateResp } from '../../api/types'
import { encodeBase } from './persist'
import { encodeDock } from '../homedock/dockPersist'
import type { BaseDir } from './useWorkbench'
import type { Workbench } from './tabs'
import { buildRestore } from './restore'

const baseA: BaseDir = { key: '/repo/a', kind: 'workspace', path: '/repo/a', label: 'a', projectName: 'p', machine: '' }

function wbWith(sessionId?: string): Workbench {
  return {
    groups: [{ tabs: [{ id: 't1', content: sessionId ? { kind: 'terminal', seq: 1, sessionId } : { kind: 'terminal', seq: 1 } }], activeId: 't1' }],
    active: 0,
    sizes: [1],
  }
}

function sess(id: string, over: Partial<PtySession> = {}): PtySession {
  return {
    id, machine: '', base_path: '/repo/a', base_kind: 'workspace', shell: '/bin/bash',
    created_at: '2026-08-20T10:00:00+08:00', cols: 80, rows: 24, attached: 0,
    foreground: false, pid: 1, bytes_out: 0, ...over,
  }
}

function state(over: Partial<WorkbenchStateResp> = {}): WorkbenchStateResp {
  return { selected: '', dock: '', bases: [], ...over }
}

const VIEW = { vw: 1280, vh: 800, inset: 0 }

describe('buildRestore', () => {
  it('活着的会话原样保留', () => {
    const r = buildRestore({
      state: state({ bases: [{ base_key: '/repo/a', payload: encodeBase(baseA, wbWith('S1')), updated_at: 1 }] }),
      sessions: [sess('S1')],
      ...VIEW,
    })
    expect(r.entries).toHaveLength(1)
    expect(r.entries[0].wb.groups[0].tabs[0].content).toEqual({ kind: 'terminal', seq: 1, sessionId: 'S1' })
    expect(r.pruned).toBe(0)
    expect(r.adopted).toBe(0)
  })

  it('已退出的会话被抹掉 id，tab 留在原位', () => {
    const r = buildRestore({
      state: state({ bases: [{ base_key: '/repo/a', payload: encodeBase(baseA, wbWith('S1')), updated_at: 1 }] }),
      sessions: [sess('S1', { exit_code: 0 })],
      ...VIEW,
    })
    expect(r.entries[0].wb.groups[0].tabs[0].content).toEqual({ kind: 'terminal', seq: 1 })
    expect(r.pruned).toBe(1)
  })

  it('列表里完全没有的会话同样被抹掉', () => {
    const r = buildRestore({
      state: state({ bases: [{ base_key: '/repo/a', payload: encodeBase(baseA, wbWith('S1')), updated_at: 1 }] }),
      sessions: [],
      ...VIEW,
    })
    expect(r.entries[0].wb.groups[0].tabs[0].content).toEqual({ kind: 'terminal', seq: 1 })
    expect(r.pruned).toBe(1)
  })

  it('孤儿工作树会话被补进对应目录', () => {
    const r = buildRestore({ state: state(), sessions: [sess('S9')], ...VIEW })
    expect(r.entries).toHaveLength(1)
    expect(r.entries[0].base.key).toBe('/repo/a')
    expect(r.entries[0].wb.groups[0].tabs[0].content).toMatchObject({ kind: 'terminal', sessionId: 'S9' })
    expect(r.adopted).toBe(1)
  })

  it('孤儿会话补进**已有**目录时不覆盖既有 tab', () => {
    const r = buildRestore({
      state: state({ bases: [{ base_key: '/repo/a', payload: encodeBase(baseA, wbWith('S1')), updated_at: 1 }] }),
      sessions: [sess('S1'), sess('S9')],
      ...VIEW,
    })
    expect(r.entries[0].wb.groups[0].tabs).toHaveLength(2)
    expect(r.adopted).toBe(1)
  })

  it('home 会话不进工作台，落到悬浮窗', () => {
    const dockRaw = encodeDock({ tabs: [], activeId: null, windowOpen: true, geom: { x: 10, y: 10, w: 620, h: 340 }, maximized: false })
    const r = buildRestore({
      state: state({ dock: dockRaw }),
      sessions: [sess('H1', { base_kind: 'home', base_path: '' })],
      ...VIEW,
    })
    expect(r.entries).toHaveLength(0)
    expect(r.dock).not.toBeNull()
    expect(r.dock!.tabs).toHaveLength(1)
    expect(r.dock!.tabs[0]).toMatchObject({ kind: 'terminal', sessionId: 'H1' })
    // 有 tab 就必须有激活项，否则浮窗一片空白
    expect(r.dock!.activeId).toBe(r.dock!.tabs[0].id)
    expect(r.dockOrphans).toHaveLength(0)
  })

  it('没有落盘的悬浮窗现场时，孤儿 home 会话走 dockOrphans（不 hydrate、不开窗）', () => {
    const r = buildRestore({
      state: state(),
      sessions: [sess('H1', { base_kind: 'home', base_path: '' })],
      ...VIEW,
    })
    expect(r.dock).toBeNull()
    expect(r.dockOrphans).toHaveLength(1)
    expect(r.dockOrphans[0].sessionId).toBe('H1')
  })

  it('悬浮窗几何按当前视口夹紧', () => {
    const dockRaw = encodeDock({ tabs: [], activeId: null, windowOpen: true, geom: { x: 2000, y: 1400, w: 900, h: 700 }, maximized: false })
    const r = buildRestore({ state: state({ dock: dockRaw }), sessions: [], ...VIEW })
    expect(r.dock!.geom.x + r.dock!.geom.w).toBeLessThanOrEqual(1280)
    expect(r.dock!.geom.y + r.dock!.geom.h).toBeLessThanOrEqual(800)
  })

  it('坏行整行丢弃，其余行照常恢复', () => {
    const r = buildRestore({
      state: state({
        bases: [
          { base_key: '/repo/bad', payload: 'not json', updated_at: 1 },
          { base_key: '/repo/a', payload: encodeBase(baseA, wbWith()), updated_at: 2 },
        ],
      }),
      sessions: [],
      ...VIEW,
    })
    expect(r.dropped).toEqual(['/repo/bad'])
    expect(r.entries).toHaveLength(1)
    expect(r.entries[0].base.key).toBe('/repo/a')
  })

  it('坏的悬浮窗现场整份丢弃，不影响工作台', () => {
    const r = buildRestore({
      state: state({ dock: '{{{', bases: [{ base_key: '/repo/a', payload: encodeBase(baseA, wbWith()), updated_at: 1 }] }),
      sessions: [],
      ...VIEW,
    })
    expect(r.dock).toBeNull()
    expect(r.entries).toHaveLength(1)
  })

  it('selected 原样透传', () => {
    const r = buildRestore({ state: state({ selected: '/repo/a' }), sessions: [], ...VIEW })
    expect(r.selected).toBe('/repo/a')
  })
})
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npm test -- restore`
Expected: FAIL，`Failed to resolve import "./restore"`

- [ ] **Step 3: 写实现**

创建 `web/src/app/workbench/restore.ts`：

```ts
// restore.ts —— 把「落盘状态」与「服务端会话列表」合成一次可直接灌入的恢复结果。
//
// 职责：
//   - 解码每一行基准状态，坏行整行丢弃
//   - 抹掉已死的 sessionId（spec §2 规则二），tab 留在原位
//   - 把「列表里有、状态里没有」的孤儿会话补进去：工作树进对应目录，home 进悬浮窗
//   - 悬浮窗几何按当前视口夹紧
//
// 边界：
//   - **纯函数，不碰 React、不发请求、不读 window**：视口尺寸由调用方传进来。
//     这一层全部的判断都用表驱动测试一条条钉住，靠 mock fetch 是测不动的
//   - 不决定「选中哪个目录」：selected 原样透传，校验它还在不在树上是 Shell 的事
//     （要等项目树，spec §6）
//   - 不打日志：它不知道自己跑在什么上下文里；统计量以返回值给出，由调用方记录
import type { PtySession, WorkbenchStateResp } from '../../api/types'
import { clampGeom, decodeDock, pruneDeadDockSessions, type DockSnapshot } from '../homedock/dockPersist'
import type { HomeTab } from '../homedock/useHomeDock'
import { decodeBase, pruneDeadSessions } from './persist'
import { EMPTY_WORKBENCH, nextTerminalSeq, openTab, type Workbench } from './tabs'
import { HOME_BASE, type BaseDir } from './useWorkbench'

// baseOfSession 把一个会话反解成它所属的基准目录。
//
// 工作树的 key 必须与 ProjectTree.workspaceBase 完全一致（含机器维度）——
// 两边对不上就会出现「左栏点进这个目录，恢复出来的终端却在另一个组里」。
//
// label 退回目录名：会话不带分支信息，而树上的 label 优先用分支名。这只影响
// 标题文字，**不影响归组**（key 相同），用户点一下左栏就会换成带分支名的那个。
export function baseOfSession(s: PtySession): BaseDir {
  if (s.base_kind === 'home') {
    // 远端 home 与本机 home 必须分开：路径都叫「~」，但它们是两台机器上的两个目录
    if (s.machine !== '') {
      return { key: `~@${s.machine}`, kind: 'home', path: '~', label: `home@${s.machine}`, projectName: '', machine: s.machine }
    }
    return HOME_BASE
  }
  const name = s.base_path.split('/').filter(Boolean).pop() ?? s.base_path
  return {
    key: s.machine ? `${s.base_path}@${s.machine}` : s.base_path,
    kind: 'workspace',
    path: s.base_path,
    label: name,
    projectName: '',
    machine: s.machine,
  }
}

// RestoreInput 是合成恢复结果所需的全部输入。
export interface RestoreInput {
  state: WorkbenchStateResp
  sessions: PtySession[]
  // 视口宽高与顶部让位，用于夹紧悬浮窗几何。由调用方读 window 后传进来
  vw: number
  vh: number
  inset: number
}

// RestoreResult 是可以直接灌进两个 hook 的恢复结果。
export interface RestoreResult {
  entries: Array<{ base: BaseDir; wb: Workbench }>
  // dock 为 null = 没有可用的落盘现场（从没存过，或存的那份是坏数据）。
  // 此时**不该** hydrate 悬浮窗，让它保持自己的默认几何。
  dock: DockSnapshot | null
  // dockOrphans 只在 dock 为 null 时非空：这些孤儿 home 会话要走 adopt
  //（不开窗、不改几何）。dock 非 null 时它们已被并进 dock.tabs，这里恒为空数组
  dockOrphans: HomeTab[]
  selected: string
  // 下面三个是给日志用的统计量，不参与渲染
  dropped: string[]
  pruned: number
  adopted: number
}

// liveSessionIds 挑出还活着的会话 id。exit_code 缺席 = 还活着，出现 = 已退出。
//
// 为什么用 `!= null` 而不是 `!== undefined`：它同时挡住 undefined 与 null。
// 今天 Go 侧 `ExitCode *int` 带 omitempty，nil 是**缺键**而不是 null，所以两种写法
// 等价；但这条断言的正确性不该依赖某个 json tag 上的 omitempty 还在不在。
function liveSessionIds(sessions: PtySession[]): Set<string> {
  const live = new Set<string>()
  for (const s of sessions) {
    if (s.exit_code != null) continue
    live.add(s.id)
  }
  return live
}

// collectUsedSessionIds 收集恢复结果里已经被某个 tab 占用的会话 id。
// 孤儿判定就是「活着但不在这个集合里」。
function collectUsedSessionIds(entries: Array<{ base: BaseDir; wb: Workbench }>, dockTabs: HomeTab[]): Set<string> {
  const used = new Set<string>()
  for (const e of entries) {
    for (const g of e.wb.groups) {
      for (const t of g.tabs) {
        if (t.content.kind === 'terminal' && t.content.sessionId) used.add(t.content.sessionId)
      }
    }
  }
  for (const t of dockTabs) {
    if (t.sessionId) used.add(t.sessionId)
  }
  return used
}

// countTerminalsWithSession 数一个工作台里带会话的终端 tab 数，用于统计被抹掉多少个。
function countTerminalsWithSession(wb: Workbench): number {
  let n = 0
  for (const g of wb.groups) {
    for (const t of g.tabs) {
      if (t.content.kind === 'terminal' && t.content.sessionId) n++
    }
  }
  return n
}

// buildRestore 合成恢复结果。
//
// 参数：见 RestoreInput。
// 返回：见 RestoreResult。**不抛异常**——任何坏数据都降级为「丢掉那一份」，
// 因为这条路径失败意味着用户看到一个空界面，而空界面不该由一次 JSON.parse 决定。
export function buildRestore(input: RestoreInput): RestoreResult {
  const live = liveSessionIds(input.sessions)

  // ① 解码基准行，坏行整行丢弃；顺手抹掉死会话
  const entries: Array<{ base: BaseDir; wb: Workbench }> = []
  const dropped: string[] = []
  let pruned = 0
  for (const row of input.state.bases) {
    const decoded = decodeBase(row.base_key, row.payload)
    if (decoded === null) {
      dropped.push(row.base_key)
      continue
    }
    const before = countTerminalsWithSession(decoded.wb)
    const wb = pruneDeadSessions(decoded.wb, live)
    pruned += before - countTerminalsWithSession(wb)
    entries.push({ base: decoded.base, wb })
  }

  // ② 解码悬浮窗现场：坏数据或从没存过都得到 null
  let dock: DockSnapshot | null = null
  if (input.state.dock !== '') {
    const d = decodeDock(input.state.dock)
    if (d !== null) {
      const beforeTabs = d.tabs.filter((t) => t.sessionId).length
      const tabs = pruneDeadDockSessions(d.tabs, live)
      pruned += beforeTabs - tabs.filter((t) => t.sessionId).length
      dock = { ...d, tabs, geom: clampGeom(d.geom, input.vw, input.vh, input.inset) }
    }
  }

  // ③ 补孤儿会话
  const used = collectUsedSessionIds(entries, dock?.tabs ?? [])
  const dockOrphans: HomeTab[] = []
  let adopted = 0
  // 悬浮窗 tab 的 seq 接着现有最大值往下发，避免出现两个 'bash · home 3'
  let dockSeq = Math.max(0, ...(dock?.tabs ?? []).map((t) => t.seq))
  for (const s of input.sessions) {
    if (!live.has(s.id) || used.has(s.id)) continue
    adopted++
    const b = baseOfSession(s)
    if (b.kind === 'home') {
      // 孤儿 home 会话的 tab id 直接用 sessionId：与 Shell 里 dock.adopt 的既有
      // 调用一致，且天然唯一。它不是 h<n> 形状，不参与 useHomeDock 的计数器播种
      const tab: HomeTab = { id: s.id, kind: 'terminal', seq: ++dockSeq, sessionId: s.id, machine: s.machine }
      if (dock === null) dockOrphans.push(tab)
      else dock.tabs = [...dock.tabs, tab]
      continue
    }
    const found = entries.find((e) => e.base.key === b.key)
    if (found) {
      found.wb = openTab(found.wb, { kind: 'terminal', seq: nextTerminalSeq(found.wb), sessionId: s.id })
    } else {
      entries.push({ base: b, wb: openTab(EMPTY_WORKBENCH, { kind: 'terminal', seq: 1, sessionId: s.id }) })
    }
  }

  // 有 tab 却没有激活项时，浮窗会显示一片空白且没人能解释为什么。
  // 这个状态只可能由「往空现场里补了孤儿」产生，就地补上
  if (dock !== null && dock.activeId === null && dock.tabs.length > 0) {
    dock.activeId = dock.tabs[0].id
  }

  return { entries, dock, dockOrphans, selected: input.state.selected, dropped, pruned, adopted }
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npm test -- restore`
Expected: PASS（11 条用例全绿）

- [ ] **Step 5: 类型检查**

Run: `cd web && npm run typecheck`
Expected: 通过（此时 `usePtyRestore.ts` 里仍有一份 `baseOfSession`，两处并存是暂时的，
Task 12 会删掉旧的那份）

- [ ] **Step 6: 注释自查**

- 文件头有职责 + 三条边界，含「为什么要单独一层纯函数」✓
- `liveSessionIds` 说明了「为什么用 `!= null`」✓
- `dockOrphans` 的接口注释说清了它何时非空 ✓
- 补 `activeId` 那一段说明了「这个状态只可能怎么产生」✓
- 纯函数层不打日志，统计量以返回值给出 ✓

- [ ] **Step 7: 提交**

```bash
git add web/src/app/workbench/restore.ts web/src/app/workbench/restore.test.ts
git commit -m "feat(web): 落盘状态与会话列表的合成层

抹死会话、补孤儿、夹紧几何全部收进一个纯函数，用表驱动测试逐条钉住。
baseOfSession 一并搬进来（usePtyRestore 下一步删除）。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 11: `useWorkbenchSync`——水合与去抖写回

**Files:**
- Create: `web/src/app/workbench/useWorkbenchSync.ts`
- Create: `web/src/app/workbench/useWorkbenchSync.test.ts`

**Interfaces:**
- Consumes: `../../api/client` 的 `fetchWorkbenchState` / `fetchPtySessions` /
  `putWorkbenchBase` / `putWorkbenchSelected` / `putWorkbenchDock`；
  Task 10 的 `buildRestore`；Task 5 的 `encodeBase` / `isEmptyWorkbench` / `diffPayloads`；
  Task 6 的 `encodeDock` / `DockSnapshot`；`../lib/desktopShell` 的 `topInset`；
  `../lib/format` 的 `errorMessage`
- Produces:
  - `interface WorkbenchSyncDeps { byBase, baseDirs, selectedKey, dockSnapshot, hydrateWorkbench, hydrateDock, adoptDockTab }`
  - `function useWorkbenchSync(deps: WorkbenchSyncDeps): { error: string; restoredSelected: string }`

**三条承重规则，做错任何一条都会丢用户的数据：**

1. **拉取失败时永久禁用写回。** 拉不到就等于不知道服务端有什么，此时把本端那份
   空布局写回去 = 一次启动失败清空用户所有现场。宁可这一整个会话都不落盘，
   并把错误摆在界面上。
2. **先播种「已落盘快照」，再灌入。** 次序颠倒的话，写回 effect 会看到「本地有一堆、
   快照是空的」，于是把刚恢复的整份重推一遍——N 次无谓的 PUT，还把全部行的
   `updated_at` 刷成同一时刻，50 行淘汰的先后就全乱了。
3. **快照种子用服务端返回的原文，不用重新编码的结果。** 这样只有**内容真的变了**
   的行（被抹了死会话、被补了孤儿）才会重写；解码失败被丢弃的坏行会因为不在
   `next` 里而被判为 removed，顺带清理掉。

- [ ] **Step 1: 写失败的测试**

创建 `web/src/app/workbench/useWorkbenchSync.test.ts`：

```ts
import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Workbench } from './tabs'
import type { BaseDir } from './useWorkbench'
import type { DockSnapshot } from '../homedock/dockPersist'
import { encodeBase } from './persist'

vi.mock('../../api/client', () => ({
  fetchWorkbenchState: vi.fn(),
  fetchPtySessions: vi.fn(),
  putWorkbenchBase: vi.fn(() => Promise.resolve()),
  putWorkbenchSelected: vi.fn(() => Promise.resolve()),
  putWorkbenchDock: vi.fn(() => Promise.resolve()),
}))

import {
  fetchPtySessions,
  fetchWorkbenchState,
  putWorkbenchBase,
  putWorkbenchDock,
} from '../../api/client'
import { useWorkbenchSync, type WorkbenchSyncDeps } from './useWorkbenchSync'

const baseA: BaseDir = { key: '/repo/a', kind: 'workspace', path: '/repo/a', label: 'a', projectName: 'p', machine: '' }
const wbA: Workbench = {
  groups: [{ tabs: [{ id: 't1', content: { kind: 'blank' } }], activeId: 't1' }],
  active: 0,
  sizes: [1],
}
const emptyDock: DockSnapshot = { tabs: [], activeId: null, windowOpen: false, geom: { x: 1, y: 1, w: 620, h: 340 }, maximized: false }

function deps(over: Partial<WorkbenchSyncDeps> = {}): WorkbenchSyncDeps {
  return {
    byBase: {},
    baseDirs: {},
    selectedKey: '',
    dockSnapshot: emptyDock,
    hydrateWorkbench: vi.fn(),
    hydrateDock: vi.fn(),
    adoptDockTab: vi.fn(),
    ...over,
  }
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.useFakeTimers({ shouldAdvanceTime: true })
})
afterEach(() => {
  vi.useRealTimers()
})

describe('useWorkbenchSync 水合', () => {
  it('两个请求都到齐之后才灌入一次', async () => {
    let resolveSessions: (v: unknown) => void = () => {}
    vi.mocked(fetchWorkbenchState).mockResolvedValue({ selected: '/repo/a', dock: '', bases: [] })
    vi.mocked(fetchPtySessions).mockReturnValue(new Promise((res) => { resolveSessions = res as never }) as never)

    const d = deps()
    const { result } = renderHook(() => useWorkbenchSync(d))

    // 只有布局到了，会话列表还没到——此时绝不能灌入，否则终端 tab 会闪一下
    await act(async () => { await Promise.resolve() })
    expect(d.hydrateWorkbench).not.toHaveBeenCalled()

    await act(async () => { resolveSessions({ sessions: [] }); await Promise.resolve() })
    await waitFor(() => expect(d.hydrateWorkbench).toHaveBeenCalledTimes(1))
    expect(result.current.restoredSelected).toBe('/repo/a')
    expect(result.current.error).toBe('')
  })

  it('拉取失败时报错、不灌入，且此后永不写回', async () => {
    vi.mocked(fetchWorkbenchState).mockRejectedValue(new Error('boom'))
    vi.mocked(fetchPtySessions).mockResolvedValue({ sessions: [] } as never)

    const d = deps()
    const { result, rerender } = renderHook((p: WorkbenchSyncDeps) => useWorkbenchSync(p), { initialProps: d })
    await waitFor(() => expect(result.current.error).toContain('boom'))
    expect(d.hydrateWorkbench).not.toHaveBeenCalled()

    // 用户照常开 tab；这一整个会话都不该有任何写回——
    // 拉不到就等于不知道服务端有什么，写回去就是清空用户的现场
    rerender(deps({ byBase: { '/repo/a': wbA }, baseDirs: { '/repo/a': baseA } }))
    await act(async () => { vi.advanceTimersByTime(2000) })
    expect(putWorkbenchBase).not.toHaveBeenCalled()
  })
})

describe('useWorkbenchSync 写回', () => {
  async function mounted(initial: WorkbenchSyncDeps) {
    vi.mocked(fetchWorkbenchState).mockResolvedValue({ selected: '', dock: '', bases: [] })
    vi.mocked(fetchPtySessions).mockResolvedValue({ sessions: [] } as never)
    const h = renderHook((p: WorkbenchSyncDeps) => useWorkbenchSync(p), { initialProps: initial })
    await waitFor(() => expect(initial.hydrateWorkbench).toHaveBeenCalled())
    return h
  }

  it('水合本身不触发写回', async () => {
    const { rerender } = await mounted(deps())
    rerender(deps())
    await act(async () => { vi.advanceTimersByTime(2000) })
    expect(putWorkbenchBase).not.toHaveBeenCalled()
  })

  it('连续多次变更只发一次 PUT（去抖）', async () => {
    const { rerender } = await mounted(deps())
    const mk = (label: string) => deps({ byBase: { '/repo/a': wbA }, baseDirs: { '/repo/a': { ...baseA, label } } })
    rerender(mk('a1'))
    rerender(mk('a2'))
    rerender(mk('a3'))
    await act(async () => { vi.advanceTimersByTime(2000) })
    expect(putWorkbenchBase).toHaveBeenCalledTimes(1)
    expect(vi.mocked(putWorkbenchBase).mock.calls[0][0]).toBe('/repo/a')
    expect(vi.mocked(putWorkbenchBase).mock.calls[0][1]).toBe(encodeBase({ ...baseA, label: 'a3' }, wbA))
  })

  it('tab 全关光的目录 PUT null（删除该行）', async () => {
    const { rerender } = await mounted(deps())
    rerender(deps({ byBase: { '/repo/a': wbA }, baseDirs: { '/repo/a': baseA } }))
    await act(async () => { vi.advanceTimersByTime(2000) })
    vi.mocked(putWorkbenchBase).mockClear()

    rerender(deps({ byBase: { '/repo/a': { groups: [{ tabs: [], activeId: null }], active: 0, sizes: [1] } }, baseDirs: { '/repo/a': baseA } }))
    await act(async () => { vi.advanceTimersByTime(2000) })
    expect(putWorkbenchBase).toHaveBeenCalledWith('/repo/a', null)
  })

  it('悬浮窗现场变了就写回', async () => {
    const { rerender } = await mounted(deps())
    rerender(deps({ dockSnapshot: { ...emptyDock, windowOpen: true } }))
    await act(async () => { vi.advanceTimersByTime(2000) })
    expect(putWorkbenchDock).toHaveBeenCalledTimes(1)
  })
})
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npm test -- useWorkbenchSync`
Expected: FAIL，`Failed to resolve import "./useWorkbenchSync"`

- [ ] **Step 3: 写实现**

创建 `web/src/app/workbench/useWorkbenchSync.ts`：

```ts
// useWorkbenchSync —— 工作台状态的水合与写回（2026-08-20 状态同步 spec §5.3）。
//
// 职责：
//   - 启动时拉一次「落盘状态 + PTY 会话列表」，合成后一次性灌入两个 hook
//   - 之后监听状态变化，把变了的行去抖 500ms 各自写回
//
// 边界：
//   - **不认识布局形状**：解码、校验、合成全在 persist.ts / dockPersist.ts / restore.ts
//   - **不管选中目录**：restoredSelected 只是把服务端存的那个 key 交出去，
//     校验它还在不在树上、要不要 select，都是 Shell 的事（要等项目树，spec §6）
//   - **只在启动时拉一次**，不做前台唤醒重拉：那一刻本端内存里的那份才是用户
//     刚才的现场，从服务端拉一份回来盖掉它是纯粹的坏（spec §1.6）
//
// 它取代了原来的 usePtyRestore：布局恢复与会话恢复本来就是同一件事的两半，
// 留两个入口必然会有人只改一边。
import { useEffect, useRef, useState } from 'react'
import {
  fetchPtySessions,
  fetchWorkbenchState,
  putWorkbenchBase,
  putWorkbenchDock,
  putWorkbenchSelected,
} from '../../api/client'
import { encodeDock, type DockSnapshot } from '../homedock/dockPersist'
import type { HomeTab } from '../homedock/useHomeDock'
import { topInset } from '../lib/desktopShell'
import { errorMessage } from '../lib/format'
import { diffPayloads, encodeBase, isEmptyWorkbench } from './persist'
import { buildRestore } from './restore'
import type { Workbench } from './tabs'
import type { BaseDir } from './useWorkbench'

// WRITE_DEBOUNCE_MS 是写回的去抖窗口。
//
// 500ms 照着 FileTab 草稿层的既有取舍：拖分屏分隔条会连发几十次 resize，
// 每次都 PUT 是自找的。**不挂 beforeunload 做 flush**——窗口内丢掉的最坏
// 情况是一次栏宽微调没存上，为它挂一个卸载钩子不划算。
const WRITE_DEBOUNCE_MS = 500

// WorkbenchSyncDeps 是本 hook 需要读到的状态与要调用的写入口。
//
// 全部由调用方（Shell）提供，本 hook 不自己持有任何工作台状态——
// 它是两个既有 hook 之间的一条管道，不是第三个真相。
export interface WorkbenchSyncDeps {
  byBase: Record<string, Workbench>
  baseDirs: Record<string, BaseDir>
  selectedKey: string
  dockSnapshot: DockSnapshot
  hydrateWorkbench: (entries: Array<{ base: BaseDir; wb: Workbench }>) => void
  hydrateDock: (s: DockSnapshot) => void
  adoptDockTab: (t: HomeTab) => void
}

// useWorkbenchSync 在挂载时恢复一次，并在此后持续写回。
//
// 返回：
//   - error: 拉取失败的原文（空串 = 没出错）。**不吞**：拉不到意味着用户会看到
//     一个「什么都没了」的界面，必须说清是为什么
//   - restoredSelected: 服务端存的「上次选中目录」的 key（空串 = 没有）。
//     调用方要在项目树到位后校验它还在不在，再决定 select
export function useWorkbenchSync(deps: WorkbenchSyncDeps): { error: string; restoredSelected: string } {
  const [error, setError] = useState('')
  const [restoredSelected, setRestoredSelected] = useState('')

  // ranRef 让恢复严格只跑一次：React 18 的 StrictMode 会把 effect 跑两遍，
  // 空依赖数组挡不住，而这里跑两遍就是两次跨机探活。
  // cancelledRef 与它配对：ranRef 管「只跑一次」，cancelledRef 管「结果还要不要」，
  // 两者都必须跨 effect run。用局部变量是错的——上一轮 cleanup 会取消掉这一轮
  // 仍有效的请求，StrictMode 下开发端 100% 恢复不出任何 tab。
  //（这两条纪律原样承接自它取代的 usePtyRestore。）
  const ranRef = useRef(false)
  const cancelledRef = useRef(false)

  // readyRef 是写回的总闸。**只有恢复成功才打开**：拉取失败时我们不知道服务端
  // 有什么，此时把本端那份空布局写回去 = 一次启动失败清空用户所有现场。
  // 宁可这一整个会话都不落盘，并把错误摆在界面上。
  const readyRef = useRef(false)

  // depsRef 让去抖回调读到**触发时**的最新状态，而不是排期那一刻闭包里的旧值
  const depsRef = useRef(deps)
  depsRef.current = deps

  // sentRef / dockSentRef / selectedSentRef 是「已经落盘的那一份」的快照，
  // 差分的基准。种子在恢复成功时播下，用的是**服务端返回的原文**而不是重新
  // 编码的结果——这样只有内容真的变了的行才会重写，而解码失败被丢弃的坏行会
  // 因为不在 next 里被判为 removed，顺带清理掉。
  const sentRef = useRef<Record<string, string>>({})
  const dockSentRef = useRef('')
  const selectedSentRef = useRef('')

  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  // ① 恢复：两个请求都到齐才灌入一次
  useEffect(() => {
    cancelledRef.current = false
    if (!ranRef.current) {
      ranRef.current = true
      Promise.all([fetchWorkbenchState(), fetchPtySessions('all')])
        .then(([state, sessResp]) => {
          if (cancelledRef.current) return
          const vw = window.innerWidth || document.documentElement.clientWidth
          const vh = window.innerHeight || document.documentElement.clientHeight
          const r = buildRestore({
            state,
            sessions: sessResp.sessions,
            vw: vw > 0 ? vw : 1280,
            vh: vh > 0 ? vh : 800,
            inset: topInset(),
          })

          // 承重次序：先播种快照，再灌入。颠倒的话写回 effect 会看到
          // 「本地有一堆、快照是空的」，把刚恢复的整份重推一遍——N 次无谓的 PUT，
          // 还会把全部行的 updated_at 刷成同一时刻，50 行淘汰的先后全乱
          sentRef.current = Object.fromEntries(state.bases.map((b) => [b.base_key, b.payload]))
          dockSentRef.current = state.dock
          selectedSentRef.current = state.selected

          const d = depsRef.current
          d.hydrateWorkbench(r.entries)
          if (r.dock !== null) d.hydrateDock(r.dock)
          for (const t of r.dockOrphans) d.adoptDockTab(t)
          setRestoredSelected(r.selected)
          readyRef.current = true

          if (r.dropped.length > 0) {
            console.warn('丢弃了无法解析的工作台状态行，这些目录的布局不会恢复', r.dropped)
          }
          console.debug('工作台状态恢复完成', {
            目录数: r.entries.length,
            抹掉的死会话: r.pruned,
            补进来的孤儿会话: r.adopted,
            丢弃的坏行: r.dropped.length,
            悬浮窗: r.dock !== null ? '已恢复' : '无落盘现场',
          })
        })
        .catch((err: unknown) => {
          if (cancelledRef.current) return
          // 不吞：用户会看到「什么都没了」，必须说清为什么。
          // readyRef 保持 false —— 这一整个会话都不写回
          console.warn('恢复工作台状态失败，本次不恢复任何 tab，且本会话不会写回', err)
          setError(errorMessage(err))
        })
    }
    return () => {
      cancelledRef.current = true
    }
  }, [])

  // ② 写回：状态一变就重排去抖
  useEffect(() => {
    if (!readyRef.current) return
    if (timerRef.current !== null) clearTimeout(timerRef.current)
    timerRef.current = setTimeout(() => {
      timerRef.current = null
      flush(depsRef.current, sentRef, dockSentRef, selectedSentRef)
    }, WRITE_DEBOUNCE_MS)
    return () => {
      if (timerRef.current !== null) {
        clearTimeout(timerRef.current)
        timerRef.current = null
      }
    }
  }, [deps.byBase, deps.baseDirs, deps.selectedKey, deps.dockSnapshot])

  return { error, restoredSelected }
}

// flush 把当前状态与「已落盘快照」做差分，逐项写回。
//
// 参数：d 是触发时的最新状态；三个 ref 是快照，写成功后就地更新。
// 返回：无。**不抛**——任一项失败只 warn，下一次状态变动会自然重试。
//
// 为什么成功之后才更新快照（而不是乐观更新）：失败的那一项留在旧快照里，
// 下次差分仍会判为「变了」，于是自动重试。乐观更新等于一次网络抖动
// 就永久丢掉那一行。
function flush(
  d: WorkbenchSyncDeps,
  sentRef: React.MutableRefObject<Record<string, string>>,
  dockSentRef: React.MutableRefObject<string>,
  selectedSentRef: React.MutableRefObject<string>,
) {
  const next: Record<string, string> = {}
  for (const [key, wb] of Object.entries(d.byBase)) {
    // 空工作台编码为「删除」：用户把一个目录的 tab 全关掉就是不想再看见它，
    // 存一行空记录只会白占 50 行配额里的一格
    if (isEmptyWorkbench(wb)) continue
    const base = d.baseDirs[key]
    if (base === undefined) {
      // 有 tab 组却没有元数据，编码不出来。正常不会发生（useWorkbench 的四个
      // 写入口都会登记），出现了就是那边漏了一处，必须能被搜到
      console.warn('工作台状态写回：缺少基准元数据，跳过该行', key)
      continue
    }
    next[key] = encodeBase(base, wb)
  }

  const { changed, removed } = diffPayloads(sentRef.current, next)
  for (const key of changed) {
    const payload = next[key]
    putWorkbenchBase(key, payload)
      .then(() => {
        sentRef.current[key] = payload
      })
      .catch((err: unknown) => console.warn('工作台状态写回失败，下次变动会重试', key, err))
  }
  for (const key of removed) {
    putWorkbenchBase(key, null)
      .then(() => {
        delete sentRef.current[key]
      })
      .catch((err: unknown) => console.warn('工作台状态删除失败，下次变动会重试', key, err))
  }

  const dockRaw = encodeDock(d.dockSnapshot)
  if (dockRaw !== dockSentRef.current) {
    putWorkbenchDock(dockRaw)
      .then(() => {
        dockSentRef.current = dockRaw
      })
      .catch((err: unknown) => console.warn('悬浮窗现场写回失败，下次变动会重试', err))
  }

  if (d.selectedKey !== selectedSentRef.current) {
    const key = d.selectedKey
    putWorkbenchSelected(key)
      .then(() => {
        selectedSentRef.current = key
      })
      .catch((err: unknown) => console.warn('选中目录写回失败，下次变动会重试', err))
  }

  if (changed.length > 0 || removed.length > 0) {
    console.debug('工作台状态写回', { 写入: changed.length, 删除: removed.length })
  }
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npm test -- useWorkbenchSync`
Expected: PASS（六条用例全绿）

若「连续多次变更只发一次 PUT」失败且发了三次，检查写回 effect 的依赖数组——
`deps` 整个对象每次渲染都是新引用，依赖必须列**它的四个字段**而不是 `deps` 本身。

- [ ] **Step 5: 类型检查与全量测试**

Run: `cd web && npm run typecheck && npm test`
Expected: 全绿

- [ ] **Step 6: 日志与注释自查**

- 恢复成功路径打了 `console.debug`（目录数、抹掉的死会话、补的孤儿、丢弃的坏行、悬浮窗）✓
- 恢复失败打 `console.warn` 并把原文交给界面 ✓
- 丢弃坏行、缺元数据、四种写回失败各有一条 warn，且都带 key ✓
- 三条承重规则各自在代码处有「为什么」注释 ✓
- 没有 `console.log` ✓

- [ ] **Step 7: 提交**

```bash
git add web/src/app/workbench/useWorkbenchSync.ts web/src/app/workbench/useWorkbenchSync.test.ts
git commit -m "feat(web): 工作台状态的水合与去抖写回

两个请求都到齐才灌入；拉取失败则整个会话禁用写回（避免用空布局覆盖服务端）；
快照种子用服务端原文，只有内容真变了的行才重写，坏行顺带清理。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 12: Shell 接线，删除 `usePtyRestore`

**Files:**
- Modify: `web/src/app/tree/ProjectTree.tsx`（新增 `findBaseByKey`）
- Modify: `web/src/app/shell/Shell.tsx`
- Delete: `web/src/app/workbench/usePtyRestore.ts`
- Delete: `web/src/app/workbench/usePtyRestore.test.ts`
- Modify: `web/src/app/workbench/baseKey.test.ts`（`baseOfSession` 的 import 改指 `./restore`）
- Modify: `web/src/app/shell/Shell.test.tsx`（既有用例里对 `usePtyRestore` 的 mock 改成 mock api client；见 Step 6）

**Interfaces:**
- Consumes: Task 11 的 `useWorkbenchSync`；Task 8 的 `wb.byBase` / `wb.baseDirs` / `wb.hydrate`；
  Task 9 的 `dock.hydrate`
- Produces: `findBaseByKey(tree: ProjectTreeResp, key: string): BaseDir | null`

- [ ] **Step 1: 写失败的测试（`findBaseByKey`）**

在 `web/src/app/tree/ProjectTree.test.tsx` 末尾追加（若该文件是组件测试、不便加纯函数用例，
新建 `web/src/app/tree/findBaseByKey.test.ts` 放这段）：

```ts
import { describe, expect, it } from 'vitest'
import { findBaseByKey, workspaceBase } from './ProjectTree'
import type { ProjectTreeResp } from '../../api/types'

const tree = {
  projects: [
    {
      project_id: 'p1',
      name: 'handoff',
      locations: [
        { machine: '', workspaces: [{ path: '/repo/a', branch: 'main', is_main: true }] },
        { machine: 'linux-01', workspaces: [{ path: '/repo/a', branch: 'feature/x', is_main: false }] },
      ],
    },
  ],
} as unknown as ProjectTreeResp

describe('findBaseByKey', () => {
  it('按 key 反查得到与 workspaceBase 完全一致的基准', () => {
    const p = tree.projects[0]
    const local = workspaceBase(p as never, '', p.locations[0].workspaces[0] as never)
    expect(findBaseByKey(tree, local.key)).toEqual(local)
  })

  it('同路径不同机器不会认错', () => {
    const p = tree.projects[0]
    const remote = workspaceBase(p as never, 'linux-01', p.locations[1].workspaces[0] as never)
    const got = findBaseByKey(tree, remote.key)
    expect(got).toEqual(remote)
    expect(got!.machine).toBe('linux-01')
  })

  it('目录已经不在树上时返回 null', () => {
    expect(findBaseByKey(tree, '/repo/gone')).toBeNull()
  })
})
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npm test -- findBaseByKey`
Expected: FAIL，`findBaseByKey is not exported`

- [ ] **Step 3: 实现 `findBaseByKey`**

在 `web/src/app/tree/ProjectTree.tsx` 里，紧跟 `findBaseOfTask` 之后加：

```tsx
// findBaseByKey 在树上按 key 反查一个目录基准。
//
// 用途：恢复「上次选中的目录」（spec §6 规则三）。必须走 workspaceBase 生成 key
// 再比对，而不是直接比 path——key 带机器维度，两台机器上同路径的工作树只有
// 连机器一起比才分得开。
//
// 返回 null 是正常情形，不要当异常处理：那个目录已经不在树上了（worktree 被
// done 回收、项目被注销）。调用方据此退回「未选中」态。
export function findBaseByKey(tree: ProjectTreeResp, key: string): BaseDir | null {
  for (const project of tree.projects) {
    for (const loc of project.locations) {
      for (const ws of loc.workspaces) {
        const base = workspaceBase(project, loc.machine, ws)
        if (base.key === key) return base
      }
    }
  }
  return null
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npm test -- findBaseByKey`
Expected: PASS

- [ ] **Step 5: 改 Shell**

在 `web/src/app/shell/Shell.tsx` 里：

① import 调整——删掉 `usePtyRestore` 的 import，加上：

```tsx
import { useWorkbenchSync } from '../workbench/useWorkbenchSync'
import type { DockSnapshot } from '../homedock/dockPersist'
import { findBaseByKey, findBaseOfTask, ProjectTree, workspaceBase } from '../tree/ProjectTree'
```

② 把第 106–121 行那整段 `const ptyRestore = usePtyRestore(...)` 替换成：

```tsx
  // dockSnapshot 把悬浮窗的五份状态收成一个对象，供落盘层做差分。
  // 必须 useMemo：不 memo 的话每次渲染都是新引用，写回 effect 会每帧重排一次去抖
  const dockSnapshot: DockSnapshot = useMemo(
    () => ({ tabs: dock.tabs, activeId: dock.activeId, windowOpen: dock.windowOpen, geom: dock.geom, maximized: dock.maximized }),
    [dock.tabs, dock.activeId, dock.windowOpen, dock.geom, dock.maximized],
  )

  // 工作台状态的水合与写回（2026-08-20 状态同步 spec §5.3）。
  // 它取代了原来的 usePtyRestore：布局恢复与会话恢复是同一件事的两半。
  //
  // adoptDockTab 仍用 dock.adopt 而不是别的入口：adopt 不打开浮窗、不抢焦点——
  // 页面一加载就弹出浮窗，等于替用户点了一下
  const sync = useWorkbenchSync({
    byBase: wb.byBase,
    baseDirs: wb.baseDirs,
    selectedKey: wb.base?.key ?? '',
    dockSnapshot,
    hydrateWorkbench: wb.hydrate,
    hydrateDock: dock.hydrate,
    adoptDockTab: dock.adopt,
  })

  // 恢复「上次选中的目录」：要等项目树到位才能校验它还在不在（spec §6 规则三）。
  //
  // 三个条件缺一不可：
  //   - 树已加载（没树就无从校验）
  //   - 服务端确实存了一个（空串 = 上次就没选中）
  //   - 用户还没自己选过（wb.base 非空说明他已经点过左栏了，别抢方向盘）
  // selectedRestoredRef 保证只做一次：树刷新会让这个 effect 重跑，
  // 而用户此时可能已经切到别的目录了
  const selectedRestoredRef = useRef(false)
  useEffect(() => {
    if (selectedRestoredRef.current) return
    if (!treeState.data || sync.restoredSelected === '' || wb.base !== null) return
    selectedRestoredRef.current = true
    const found = findBaseByKey(treeState.data, sync.restoredSelected)
    if (found === null) {
      // 目录已经不在树上了（worktree 被回收、项目被注销）。退回未选中态，
      // 而不是摆出一栏点什么都报错的 tab
      console.debug('上次选中的目录已不在树上，退回未选中态', sync.restoredSelected)
      return
    }
    // 用树上重新构造的那份，而不是 payload 里的快照：树上的 label 会跟着
    // 分支改名一起变，用快照会让面包屑显示一个已经改掉的旧分支名
    wb.select(found)
  }, [treeState.data, sync.restoredSelected, wb.base, wb.select])
```

（`useMemo` / `useRef` / `useEffect` 如果还没在本文件 import，补进 react 的 import 清单。）

③ 把第 308–310 行的错误横幅改成用新的 error：

```tsx
        {sync.error !== '' && (
          <DisconnectedBanner message={`工作台状态恢复失败，本次不会保存布局：${sync.error}`} compact />
        )}
```

文案要说清「本次不会保存布局」——这是拉取失败时写回被永久禁用的直接后果，
不说的话用户会以为只是这一次没恢复，然后一整天的布局都白摆了。

- [ ] **Step 6: 删掉 `usePtyRestore`，修好引用**

```bash
git rm web/src/app/workbench/usePtyRestore.ts web/src/app/workbench/usePtyRestore.test.ts
```

- `web/src/app/workbench/baseKey.test.ts` 里 `import { baseOfSession } from './usePtyRestore'`
  改成 `from './restore'`
- 跑一次全量搜索，确认没有别处还引用它：

```bash
grep -rn "usePtyRestore" web/src || echo "无残留引用"
```

- `web/src/app/shell/Shell.test.tsx` 里如果 mock 了 `usePtyRestore`，改成 mock
  `../../api/client` 的 `fetchWorkbenchState` / `fetchPtySessions`（返回空状态与空会话列表），
  其余断言不动。

- [ ] **Step 7: 跑全量测试与类型检查**

Run: `cd web && npm run typecheck && npm test`
Expected: 全绿

- [ ] **Step 8: 跑全量 Go 测试，确认后端没被碰坏**

Run: `go test ./...`
Expected: ok

- [ ] **Step 9: 注释与日志自查**

- `dockSnapshot` 的 `useMemo` 有「为什么必须 memo」✓
- 选中恢复的 effect 说清了三个条件与「为什么只做一次」✓
- 目录不在树上时打了 debug ✓
- 错误横幅文案包含「本次不会保存布局」✓

- [ ] **Step 10: 提交**

```bash
git add -A
git commit -m "feat(web): Shell 接上状态同步，删除 usePtyRestore

悬浮窗状态收成 dockSnapshot 供差分；选中目录等项目树到位后校验再恢复，
不在树上就退回未选中态。恢复失败的横幅明说本次不会保存布局。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 13: 真机走查与验收

> **本 task 由审核者在本地执行，不派发。**
> 它要起 agentd、开桌面端、反复退出重进，属于交互式真机操作；派发出去的执行者
> 既没有桌面环境，也不该被要求驱动这些。执行者做完 Task 12 即交回。

**Files:**
- Modify: `docs/superpowers/specs/2026-08-20-workbench-state-sync-design.md`（如走查发现设计缺口）
- Modify: `CHANGELOG.md`（记下本次改动）

- [ ] **Step 1: 起一个隔离的 agentd 实例**

**不要重启本机常驻的那个 agentd**——launchd 会用旧二进制把它拉回来。
起一个独立 DataDir + 独立端口的实例来验收。

- [ ] **Step 2: 走查清单（逐条勾）**

- [ ] 在目录 A 开两个终端 + 一个文件 tab，分成两栏，拖一下分隔条
- [ ] 切到目录 B，开一个 TUI tab
- [ ] 切回目录 A：两栏、三个 tab、栏宽、激活项**全部原样**
- [ ] 刷新页面：同上，且终端里之前的输出还在（会话没死）
- [ ] 完全退出桌面端再打开：同上
- [ ] 打开右下角悬浮窗，开两个 home 终端，把窗口拖到左上角并调小；退出重进：
      tab、激活项、窗口位置与大小**全部原样**，且窗口是打开着的
- [ ] 悬浮窗恢复后点「新建终端」：新 tab 的编号不与已恢复的重复（这是 Task 9 那条最容易漏的）
- [ ] 重启 agentd（此时 PTY 会话必然全死，A 还没做）：布局完整回来，终端 tab
      还在原来那一栏，点进去原地起一个新 shell
- [ ] 把某个目录的 tab 全关掉，退出重进：那个目录不再有残留 tab
- [ ] 断开 agentd（停掉进程）后刷新页面：界面上有「工作台状态恢复失败，本次不会保存布局」
      的横幅，且恢复 agentd 后**本会话不再写回**（这是有意的，见 Task 11 承重规则 1）

- [ ] **Step 3: 直接查库对账**

```bash
sqlite3 <隔离实例的 DataDir>/handoff.db "SELECT base_key, length(payload), updated_at FROM workbench_bases ORDER BY updated_at DESC;"
sqlite3 <隔离实例的 DataDir>/handoff.db "SELECT key, length(value) FROM workbench_singletons;"
```

确认：行数与你开过的目录数一致、单例有两行、payload 长度在 1–3 KiB 量级。

- [ ] **Step 4: 记 CHANGELOG 并提交**

在 `CHANGELOG.md` 的 `[Unreleased]` 小节下记：工作台 tab 与分屏布局、悬浮窗现场
落到 agentd，切目录/刷新/重开桌面端后原样恢复；工作树基准 key 加上机器维度。

```bash
git add CHANGELOG.md docs/superpowers/specs/
git commit -m "docs(changelog): 工作台状态同步落地

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## 自查清单（每个 task 完成后逐条确认）

| 检查项 | 要求 |
|--------|------|
| 完成目标 | 实现了本 task 定义的全部内容，没有跳步 |
| 测试先红后绿 | 每个 task 都先跑到失败，再实现，再跑到通过 |
| 文件头注释 | 每个新建文件顶部写了职责和**边界**（它不做什么） |
| 导出注释 | 每个导出函数/方法写了参数、返回、注意事项 |
| 中文「为什么」 | 复杂逻辑和边界条件说明了理由，不复述代码 |
| 日志 | 关键节点、错误分支、**成功路径**都有；Go 用 `s.log`，TS 用 `console.debug/warn` |
| 无 print | 没有 `fmt.Printf` / `console.log` |
| gofmt | `gofmt -l internal/` 输出为空 |
| 类型检查 | `cd web && npm run typecheck` 通过 |
| 全量测试 | `go test ./...` 与 `cd web && npm test` 全绿 |
| 范围 | 没有改动本计划之外的文件；`fileDraft.ts` / `treePrefs.ts` 一行未动 |
