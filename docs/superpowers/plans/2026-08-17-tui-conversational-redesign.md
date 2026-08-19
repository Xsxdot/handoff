# TUI tab 对话式重构 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 TUI tab 从「四个盒子的仪表盘」重构为「一个 agent 会话」：会话流当主角（唯一滚动区）、事件人话化内联、审阅取证右滑分栏、推进动作变对话式 composer、页头补回 ctx/累计用量。

**Architecture:** spec 见 `docs/superpowers/specs/2026-08-17-tui-conversational-redesign-design.md`；形态验收基准是 `prototypes/tui-redesign/`（确认记录在 `prototypes/base/README.md`）。前端重构集中在 `web/src/app/task/` 与 `workbench/TuiTab.tsx`；两项后端小改：turn_start 帧带指令原文（proto + FrameWriter + 4 adapter）、任务分支列表接口。数据钩子 `useTaskSession` / `useFramesStream` / `useRenderStream` 不动。

**Tech Stack:** React 19 + TypeScript + Tailwind（shadcn token）+ vitest；Go 1.x（agentd / executor），`log/slog` 风格 logger。

## Global Constraints

- 注释用中文，解释「为什么」；新文件必须有文件头注释（职责 + 边界）；导出函数必须有 doc 注释（CLAUDE.md §2）
- Go 侧日志用现有 logger（`a.log` / `s.log` / `w.log`），**禁止 `fmt.Printf`**；前端**禁止 `console.log`** 当日志（错误一律 UI 透出，与现状一致）
- 不引入任何新 npm/Go 依赖（diff 解析自写）
- 视觉全部用现有 shadcn token（`text-muted-foreground`、`bg-muted`、`border` 等），不写死色值；amber/green/red 系沿用现有组件的写法（如 `text-amber-600 dark:text-amber-500`）
- 前端验证命令：`cd web && npm run typecheck && npm run lint && npm test`；后端：`go test ./...`（仓库根）
- 每个 task 完成即 commit；坏行/截断/未知类型「不吞数据」纪律不得回退

## 文件结构总览

```
web/src/app/task/
  diff.ts / diff.test.ts                 新增：unified diff 解析（纯函数）
  eventPhrase.ts / eventPhrase.test.ts   新增：事件 → 人话（纯函数）
  delivery.ts / delivery.test.ts         新增：报工 trailer 提取（纯函数）
  meta.tsx                               新增：元数据行共用样式/容器
  EventChip.tsx                          新增：生命周期事件行（替代 EventMark）
  UserInstructionBlock.tsx               新增：审核者指令右对齐气泡
  DeliverySummaryCard.tsx                新增：交付摘要卡
  ConversationStream.tsx / .test.tsx     新增：会话流（替代 TimelinePanel）
  TuiHeader.tsx / TuiHeader.test.tsx     新增：两行页头
  UsageChip.tsx                          新增：ctx 小表 + 账目弹出
  ReviewSidePanel.tsx / .test.tsx        新增：审阅右滑栏（替代 ReviewPanel）
  DiffView.tsx                           新增：diff 按文件分组渲染
  Composer.tsx / Composer.test.tsx       新增：对话式收口（替代 AdvanceActions）
  DebugDrawer.tsx / DebugDrawer.test.tsx 新增：调试抽屉（收纳原始事件 + RenderPanel）
  ThinkingBlock.tsx / ToolCard.tsx       改造：元数据行样式
  frames.ts                              小改：turn 块带 instructions
  删除：TimelinePanel(.test)、EventsPanel、EventMark、ReviewPanel、AdvanceActions
web/src/app/workbench/TuiTab.tsx         重排总装
web/src/api/types.ts                     Frame.instructions、BranchesResult
web/src/api/client.ts                    fetchTaskBranches
internal/proto/frames.go                 Frame.Instructions
internal/executor/turn/frames.go         BeginTurn(reason, instructions)
internal/executor/{claudecode,codex,grok,opencode}/adapter.go  8 处调用点
internal/agentd/workspace.go             Branches()
internal/agentd/server.go                GET /api/tasks/{id}/branches
```

---

### Task 1: turn_start 帧带指令原文（后端 + 类型）

**Files:**
- Modify: `internal/proto/frames.go`（Frame 结构，~L84 Reason 字段后）
- Modify: `internal/executor/turn/frames.go:139`（BeginTurn）
- Modify: `internal/executor/turn/frames_test.go`
- Modify: `internal/executor/claudecode/adapter.go:199,346`
- Modify: `internal/executor/codex/adapter.go:268,427`
- Modify: `internal/executor/grok/adapter.go:199,294`
- Modify: `internal/executor/opencode/adapter.go:369,469`
- Modify: `web/src/api/types.ts`（Frame 接口，~L407 reason 后）

**Interfaces:**
- Consumes: `proto.Frame`、`FrameWriter.appendLocked`（现有）
- Produces: `Frame.Instructions string`（JSON `instructions,omitempty`）；`func (w *FrameWriter) BeginTurn(reason, instructions string) error`；TS `Frame.instructions?: string`。Task 5/6 靠 `instructions` 渲染审核者气泡。

- [ ] **Step 1: 写失败测试**——`internal/executor/turn/frames_test.go` 追加：

```go
// TestBeginTurnCarriesInstructions 验证 send 回合的 turn_start 帧携带指令原文，
// dispatch 回合不带（omitempty 缺席）。
func TestBeginTurnCarriesInstructions(t *testing.T) {
	dir := t.TempDir()
	w, err := NewFrameWriter(dir, testLogger(t))
	if err != nil {
		t.Fatalf("NewFrameWriter: %v", err)
	}
	if err := w.BeginTurn("dispatch", ""); err != nil {
		t.Fatalf("BeginTurn dispatch: %v", err)
	}
	if err := w.BeginTurn("send", "把审批理由改为 deny，再跑一遍测试"); err != nil {
		t.Fatalf("BeginTurn send: %v", err)
	}
	frames := readFrames(t, dir) // 用本文件已有的读回帧辅助函数；若命名不同，沿用现有的
	if len(frames) != 2 {
		t.Fatalf("期望 2 帧，得到 %d", len(frames))
	}
	if frames[0].Instructions != "" {
		t.Errorf("dispatch 帧不应带 instructions，得到 %q", frames[0].Instructions)
	}
	if frames[1].Instructions != "把审批理由改为 deny，再跑一遍测试" {
		t.Errorf("send 帧 instructions 不符：%q", frames[1].Instructions)
	}
}
```

注意：`readFrames` / `testLogger` 以 `frames_test.go` 内**已有的**辅助函数为准（该文件已有多个读回帧的测试，如 `TestBeginTurnOrdersTurnStartBeforeConcurrentWrites`）；名字不同就用现成的，不新造重复辅助。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/turn/ -run TestBeginTurnCarriesInstructions -v`
Expected: 编译失败（BeginTurn 参数个数不符）——这正是本任务要改的签名。

- [ ] **Step 3: proto.Frame 加字段**——`internal/proto/frames.go` 在 `Reason` 字段之后：

```go
	// Instructions 是 turn_start（reason=send）携带的审核者指令原文——
	// continue 的修改指令或 reply 的应答文本。前端靠它渲染「审核者气泡」。
	// dispatch 回合恒为空；旧帧无此字段（前端按缺席处理，向后兼容）。
	// 不截断：这是人写的指令，长度天然有限；截了反而丢审阅依据。
	Instructions string `json:"instructions,omitempty"`
```

同时把文件头部字段注释表（~L43）的 `turn_start: Reason` 行更新为 `turn_start: Reason + Instructions（send 时）`。

- [ ] **Step 4: BeginTurn 改签名**——`internal/executor/turn/frames.go:139`：

```go
// BeginTurn 开启新回合：turn 自增、part 计数归零，并写一条 turn_start 帧。
//
// reason 只应是 "dispatch"（Adapter.Start）或 "send"（Adapter.Send）。
// instructions 是 send 时的指令/应答原文（dispatch 传 ""）——写进帧供前端
// 渲染审核者气泡；日志里只记长度不记原文，避免把长指令刷进日志。
func (w *FrameWriter) BeginTurn(reason, instructions string) error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.turn++
	w.nextPart = 0
	w.log.Info("回合开始", "turn", w.turn, "reason", reason, "instructions_len", len(instructions))
	return w.appendLocked(proto.Frame{Type: proto.FrameTurnStart, Reason: reason, Instructions: instructions})
}
```

- [ ] **Step 5: 更新 8 处调用点**——4 个 adapter 各 2 处：
  - dispatch 路径（claudecode:199、codex:268、grok:199、opencode:369）：`BeginTurn("dispatch")` → `BeginTurn("dispatch", "")`
  - send 路径（claudecode:346、codex:427、grok:294、opencode:469）：`BeginTurn("send")` → `BeginTurn("send", text)`——`text` 用该 Send 方法作用域里的指令参数名（claudecode 是 `text`；其余 adapter 以各自 Send 签名的文本参数为准，编译器会指路）。
  - `frames_test.go` 里所有旧的 `BeginTurn("...")` 调用同步补第二参（dispatch 补 `""`）。

- [ ] **Step 6: 跑测试确认通过**

Run: `go test ./internal/executor/... -v -run 'TestBeginTurn'`
Expected: PASS（含并发顺序测试不回归）

- [ ] **Step 7: TS 类型对齐**——`web/src/api/types.ts` Frame 接口 `reason?: string` 之后：

```ts
  // turn_start（send）携带的审核者指令原文；dispatch 与旧帧缺席
  instructions?: string
```

- [ ] **Step 8: 自检日志与注释**（instrumenting-code 清单）：BeginTurn 的 Info 日志带 turn/reason/instructions_len；新字段注释说明为什么不截断、旧帧兼容语义。无新增错误分支。

- [ ] **Step 9: 全量回归 + Commit**

Run: `go test ./... && cd web && npm run typecheck`
Expected: 全绿

```bash
git add internal/proto/frames.go internal/executor/turn/ internal/executor/claudecode/ internal/executor/codex/ internal/executor/grok/ internal/executor/opencode/ web/src/api/types.ts
git commit -m "feat(frames): turn_start 帧携带审核者指令原文"
```

---

### Task 2: 任务分支列表接口（后端 + client）

**Files:**
- Modify: `internal/agentd/workspace.go`（Diff 函数附近，~L972 后）
- Modify: `internal/agentd/workspace_test.go`（跟随现有 Diff 测试的建仓辅助）
- Modify: `internal/agentd/server.go`（路由 ~L336 diff 行后；handler 放 handleTaskDiff 后 ~L1395）
- Modify: `web/src/api/types.ts`、`web/src/api/client.ts`（fetchTaskDiff ~L172 后）

**Interfaces:**
- Consumes: `gitRun(ctx, repo, args...)`、`resolveBaseBranch(repo)`、`s.taskRepoOrErr(w, taskID)`、`writeJSON`（全部现有）
- Produces: `func Branches(repo string) ([]string, error)`；`GET /api/tasks/{id}/branches` → `{"branches": string[], "default": string}`；TS `BranchesResult { branches: string[]; default: string }`；`fetchTaskBranches(id): Promise<BranchesResult>`。Task 8 的基准下拉消费。

- [ ] **Step 1: 写失败测试**——`internal/agentd/workspace_test.go` 追加（建临时 git 仓库的辅助以该文件现有 Diff/ReadFile 测试用的为准，直接复用）：

```go
// TestBranches 验证分支列表按名称返回本地分支，且不含 HEAD 指针。
func TestBranches(t *testing.T) {
	repo := initTestRepo(t) // 复用本文件现有建仓辅助；已有 main/初始提交
	// 加一个特性分支
	mustGit(t, repo, "branch", "feature/x")
	got, err := Branches(repo)
	if err != nil {
		t.Fatalf("Branches: %v", err)
	}
	want := map[string]bool{"feature/x": false}
	for _, b := range got {
		if _, ok := want[b]; ok {
			want[b] = true
		}
		if strings.HasPrefix(b, "-") || b == "" {
			t.Errorf("非法分支名混入：%q", b)
		}
	}
	for b, seen := range want {
		if !seen {
			t.Errorf("缺少分支 %s（得到 %v）", b, got)
		}
	}
}
```

若本文件没有 `initTestRepo`/`mustGit` 同义辅助，就照现有 Diff 测试的建仓写法内联一份（`git init` + 配置 user + 空提交），不要发明新的全局辅助。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestBranches -v`
Expected: 编译失败（Branches 未定义）

- [ ] **Step 3: 实现 Branches**——`internal/agentd/workspace.go`，紧跟 `resolveBaseBranch` 之后：

```go
// Branches 列出仓库的本地分支名（refname:short，字母序）。
//
// 供审阅栏「改动」的基准分支下拉用：协调者从列表里选，不手填。
// 只列本地分支——diff 的 base 语义是本地 rev，远端跟踪分支由默认推导覆盖。
//
// 返回：分支名切片（可能为空，如空仓库）；git 失败返回错误（stderr 在 err 里）。
func Branches(repo string) ([]string, error) {
	out, _, err := gitRun(context.Background(), repo, "for-each-ref", "--format=%(refname:short)", "refs/heads")
	if err != nil {
		log().Error("列分支失败", "repo", repo, "cause", err)
		return nil, fmt.Errorf("git for-each-ref: %w", err)
	}
	var branches []string
	for _, line := range strings.Split(out, "\n") {
		if b := strings.TrimSpace(line); b != "" {
			branches = append(branches, b)
		}
	}
	log().Debug("列分支完成", "repo", repo, "count", len(branches))
	return branches, nil
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestBranches -v`
Expected: PASS

- [ ] **Step 5: 路由 + handler**——`internal/agentd/server.go`：路由块 L336（diff 行）后加：

```go
	api.HandleFunc("GET /api/tasks/{id}/branches", s.byTask(s.handleTaskBranches))
```

handler 放在 `handleTaskDiff` 之后（错误映射与日志风格与它对齐）：

```go
// handleTaskBranches 返回任务仓库的本地分支名列表与推导出的默认基准分支。
//
// 供前端审阅栏的基准下拉用（spec 2026-08-17 §6.2）。只读，不做状态门禁。
// default 为空表示推导不出（前端下拉退化为仅「自动推导」项）。
func (s *Server) handleTaskBranches(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	s.log.Info("branches 请求", "method", r.Method, "path", r.URL.Path, "task", taskID)
	repo, ok := s.taskRepoOrErr(w, taskID)
	if !ok {
		return
	}
	branches, err := Branches(repo)
	if err != nil {
		s.log.Error("列分支失败", "task", taskID, "repo", repo, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": truncateRunes(err.Error(), 200)})
		return
	}
	s.log.Info("branches 完成", "task", taskID, "count", len(branches))
	writeJSON(w, http.StatusOK, map[string]any{"branches": branches, "default": resolveBaseBranch(repo)})
}
```

- [ ] **Step 6: 前端 client + 类型**——`web/src/api/types.ts`（DiffResult 附近）：

```ts
// BranchesResult 是 GET /api/tasks/{id}/branches 的响应：本地分支名 + 推导默认。
// default 为空串 = 推导不出，前端下拉退化为仅「自动推导」项。
export interface BranchesResult {
  branches: string[]
  default: string
}
```

`web/src/api/client.ts`（fetchTaskDiff 后）：

```ts
// fetchTaskBranches 取任务仓库的本地分支列表（审阅栏基准下拉的数据源）。
export function fetchTaskBranches(id: string): Promise<BranchesResult> {
  return request(`/api/tasks/${id}/branches`)
}
```

（`request` 与相邻函数的封装保持一致；import 处补 `BranchesResult`。）

- [ ] **Step 7: 自检日志与注释**：handler 入口/成功/失败三点日志齐（入口带 task、成功带 count、失败带 cause）；Branches 的 doc 注释说明「只列本地分支」的边界。

- [ ] **Step 8: 全量回归 + Commit**

Run: `go test ./internal/agentd/ && cd web && npm run typecheck`
Expected: 全绿

```bash
git add internal/agentd/workspace.go internal/agentd/workspace_test.go internal/agentd/server.go web/src/api/types.ts web/src/api/client.ts
git commit -m "feat(agentd): 任务分支列表接口——审阅栏基准下拉的数据源"
```

---

### Task 3: parseUnifiedDiff 纯函数

**Files:**
- Create: `web/src/app/task/diff.ts`
- Create: `web/src/app/task/diff.test.ts`

**Interfaces:**
- Consumes: 无（纯函数，输入是 `fetchTaskDiff` 返回的文本）
- Produces:

```ts
export interface DiffLine { kind: 'add' | 'del' | 'ctx' | 'hunk'; text: string }
export interface FileDiff { path: string; adds: number; dels: number; lines: DiffLine[] }
export interface ParsedDiff { files: FileDiff[]; trailer: string }
export function parseUnifiedDiff(text: string): ParsedDiff | null
```

Task 8 的 DiffView 消费。**返回 null = 不是可解析的 unified diff，调用方整体回退裸文本**。

- [ ] **Step 1: 写失败测试**——`web/src/app/task/diff.test.ts`：

```ts
// diff.test.ts —— parseUnifiedDiff 的穷举测试：多文件/新增删除/二进制/trailer/非法输入。
import { describe, expect, it } from 'vitest'
import { parseUnifiedDiff } from './diff'

const TWO_FILES = `diff --git a/README.md b/README.md
index 1457913..99c4d50 100644
--- a/README.md
+++ b/README.md
@@ -247,7 +247,8 @@ Task state machine
 retry with continue.
-old line
+new line one
+new line two
diff --git a/internal/agentd/task.go b/internal/agentd/task.go
index aaa..bbb 100644
--- a/internal/agentd/task.go
+++ b/internal/agentd/task.go
@@ -118,3 +118,4 @@ func handleDone
 ctx line
+added
`

// agentd 的 Diff() 会在 diff 后拼 "\n\n" + git log --oneline 提交列表
const WITH_TRAILER = TWO_FILES + `
4e3de5e feat: done 幂等
a41f8c2 fix: watchdog`

describe('parseUnifiedDiff', () => {
  it('多文件分组与 ± 统计', () => {
    const r = parseUnifiedDiff(TWO_FILES)
    expect(r).not.toBeNull()
    expect(r!.files.map((f) => f.path)).toEqual(['README.md', 'internal/agentd/task.go'])
    expect(r!.files[0].adds).toBe(2)
    expect(r!.files[0].dels).toBe(1)
    expect(r!.files[1].adds).toBe(1)
    expect(r!.files[1].dels).toBe(0)
  })

  it('行类型标注正确', () => {
    const lines = parseUnifiedDiff(TWO_FILES)!.files[0].lines
    expect(lines[0]).toEqual({ kind: 'hunk', text: '@@ -247,7 +247,8 @@ Task state machine' })
    expect(lines.find((l) => l.kind === 'del')!.text).toBe('-old line')
    expect(lines.filter((l) => l.kind === 'add')).toHaveLength(2)
    expect(lines.find((l) => l.kind === 'ctx')!.text).toBe(' retry with continue.')
  })

  it('diff 后的提交列表进 trailer，不混进文件行', () => {
    const r = parseUnifiedDiff(WITH_TRAILER)!
    expect(r.files).toHaveLength(2)
    expect(r.trailer).toContain('4e3de5e feat: done 幂等')
    expect(r.files[1].lines.some((l) => l.text.includes('4e3de5e'))).toBe(false)
  })

  it('新文件与二进制文件不炸：头部行归为 ctx', () => {
    const t = `diff --git a/new.bin b/new.bin
new file mode 100644
Binary files /dev/null and b/new.bin differ`
    const r = parseUnifiedDiff(t)!
    expect(r.files[0].path).toBe('new.bin')
    expect(r.files[0].adds).toBe(0)
    expect(r.files[0].lines.every((l) => l.kind === 'ctx')).toBe(true)
  })

  it('非 diff 文本返回 null（调用方回退裸文本）', () => {
    expect(parseUnifiedDiff('随便一段话')).toBeNull()
    expect(parseUnifiedDiff('')).toBeNull()
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/task/diff.test.ts`
Expected: FAIL（模块不存在）

- [ ] **Step 3: 实现**——`web/src/app/task/diff.ts`：

```ts
// diff.ts —— unified diff 的解析（纯函数）。
//
// 职责：把 agentd Diff() 返回的文本（git diff + 空行 + git log --oneline）切成
// 按文件分组的行级结构，供 DiffView 着色渲染。
//
// 边界：
//   - 不碰 DOM、不发请求：必须能脱离浏览器被穷举测试（与 frames.ts 同一纪律）
//   - 只认标准 `diff --git` 输出；认不出返回 null，由调用方整体回退裸文本——
//     解析失败绝不能吞掉内容，diff 是审阅的核心证据
//   - 不做语法高亮、不做并排视图（spec §10 范围外）

// DiffLine 是 diff 的一行：add/del 着色，hunk 是 @@ 头，其余（上下文、
// index/mode/Binary 等头部行）一律 ctx——审阅者要看得到它们，但不着色。
export interface DiffLine {
  kind: 'add' | 'del' | 'ctx' | 'hunk'
  text: string
}

// FileDiff 是一个文件的改动组。
export interface FileDiff {
  path: string
  adds: number
  dels: number
  lines: DiffLine[]
}

// ParsedDiff 是整份 diff 的解析产物；trailer 是 diff 之后的非 diff 尾巴
// （agentd 拼上的提交列表），原样保留展示。
export interface ParsedDiff {
  files: FileDiff[]
  trailer: string
}

// FILE_HEAD 匹配 `diff --git a/<path> b/<path>`，取 b 侧路径（新路径为准，
// 重命名时审阅者关心改成了什么名字）。
const FILE_HEAD = /^diff --git a\/.* b\/(.*)$/

// parseUnifiedDiff 解析一份 unified diff 文本。
//
// 返回：
//   - ParsedDiff: 至少解析出一个文件
//   - null: 文本里没有任何 `diff --git` 头（含空串）——不是 diff，调用方回退
//
// 判定 trailer 的规则：最后一个文件块结束后、且与 diff 隔了空行的剩余文本。
// 实现上：遇到空行且当前不在 hunk 连续行里，即认为 diff 部分结束。
export function parseUnifiedDiff(text: string): ParsedDiff | null {
  const lines = text.split('\n')
  const files: FileDiff[] = []
  let cur: FileDiff | null = null
  let trailerStart = -1

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]
    const head = FILE_HEAD.exec(line)
    if (head) {
      cur = { path: head[1], adds: 0, dels: 0, lines: [] }
      files.push(cur)
      continue
    }
    if (!cur) continue // 还没遇到第一个文件头，跳过（防御前导杂音）

    // diff 与提交列表之间由空行分隔（agentd Diff() 的拼接约定）；
    // 空行后如果再没有文件头，剩余全是 trailer
    if (line === '' ) {
      const rest = lines.slice(i + 1)
      if (rest.length > 0 && !rest.some((l) => FILE_HEAD.test(l))) {
        trailerStart = i + 1
        break
      }
      continue
    }

    if (line.startsWith('@@')) {
      cur.lines.push({ kind: 'hunk', text: line })
    } else if (line.startsWith('+') && !line.startsWith('+++')) {
      cur.adds++
      cur.lines.push({ kind: 'add', text: line })
    } else if (line.startsWith('-') && !line.startsWith('---')) {
      cur.dels++
      cur.lines.push({ kind: 'del', text: line })
    } else {
      cur.lines.push({ kind: 'ctx', text: line })
    }
  }

  if (files.length === 0) return null
  const trailer = trailerStart >= 0 ? lines.slice(trailerStart).join('\n').trim() : ''
  return { files, trailer }
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/task/diff.test.ts`
Expected: PASS（5 个用例全绿）

- [ ] **Step 5: 自检注释**：文件头职责/边界齐；导出类型与函数 doc 注释齐；`+++`/`---` 排除的 why 在代码里可读出（条件自身已表达，无需赘注）。纯函数无 I/O，无日志点。

- [ ] **Step 6: Commit**

```bash
git add web/src/app/task/diff.ts web/src/app/task/diff.test.ts
git commit -m "feat(web): unified diff 解析纯函数——按文件分组与行级分类"
```

---

### Task 4: eventPhrase 与 delivery trailer 提取纯函数

**Files:**
- Create: `web/src/app/task/eventPhrase.ts` / `eventPhrase.test.ts`
- Create: `web/src/app/task/delivery.ts` / `delivery.test.ts`

**Interfaces:**
- Consumes: 无
- Produces:

```ts
// eventPhrase.ts
export interface EventPhrase { text: string; tone: 'info' | 'warn' }
export function eventPhrase(event: string): EventPhrase
// delivery.ts
export interface Delivery { branch?: string; commit?: string; summary?: string }
export function extractDelivery(text: string): { delivery: Delivery; body: string } | null
```

Task 5 的 EventChip / DeliverySummaryCard 消费。

- [ ] **Step 1: 写失败测试**——`web/src/app/task/eventPhrase.test.ts`：

```ts
// eventPhrase.test.ts —— 事件人话化映射：白名单内中文短语，白名单外原样透出。
import { describe, expect, it } from 'vitest'
import { eventPhrase } from './eventPhrase'

describe('eventPhrase', () => {
  it('工单类事件是 warn 并指向工单面板', () => {
    const p = eventPhrase('permission_request')
    expect(p.tone).toBe('warn')
    expect(p.text).toContain('权限工单')
    expect(p.text).toContain('工单面板')
    expect(eventPhrase('question').tone).toBe('warn')
  })
  it('生命周期事件是 info 中文短语', () => {
    expect(eventPhrase('completed')).toEqual({ text: '一轮结束，进入待审', tone: 'info' })
    expect(eventPhrase('turn_failed').text).toBe('回合失败')
    expect(eventPhrase('failed').tone).toBe('warn')
    expect(eventPhrase('stalled').text).toContain('看门狗')
  })
  it('未知类型原样透出，不吞', () => {
    expect(eventPhrase('brand_new_event')).toEqual({ text: 'brand_new_event', tone: 'info' })
  })
})
```

`web/src/app/task/delivery.test.ts`：

```ts
// delivery.test.ts —— 报工 trailer 提取：尾部 JSON → 交付摘要，其余情况返回 null。
import { describe, expect, it } from 'vitest'
import { extractDelivery } from './delivery'

const TRAILER = '{"branch":"bench/b93","commit":"4e3de5e","summary":"三缺口全落地"}'

describe('extractDelivery', () => {
  it('正文 + 尾部 trailer：拆出摘要与剩余正文', () => {
    const r = extractDelivery(`B93 全部完成。全量回归 0 FAIL。\n\n${TRAILER}`)
    expect(r).not.toBeNull()
    expect(r!.delivery).toEqual({ branch: 'bench/b93', commit: '4e3de5e', summary: '三缺口全落地' })
    expect(r!.body).toBe('B93 全部完成。全量回归 0 FAIL。')
  })
  it('纯 trailer（无正文）也能提取，body 为空串', () => {
    const r = extractDelivery(TRAILER)!
    expect(r.delivery.commit).toBe('4e3de5e')
    expect(r.body).toBe('')
  })
  it('JSON 不在末尾 / 不是对象 / 无已知字段 → null（原样当正文）', () => {
    expect(extractDelivery(`${TRAILER}\n后面还有话`)).toBeNull()
    expect(extractDelivery('末尾是 [1,2,3]')).toBeNull()
    expect(extractDelivery('末尾 {"foo":"bar"}')).toBeNull()
    expect(extractDelivery('没有任何 JSON')).toBeNull()
  })
  it('非法 JSON → null，不抛异常', () => {
    expect(extractDelivery('话 {"branch": 断掉了')).toBeNull()
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/task/eventPhrase.test.ts src/app/task/delivery.test.ts`
Expected: FAIL（模块不存在）

- [ ] **Step 3: 实现**——`web/src/app/task/eventPhrase.ts`：

```ts
// eventPhrase.ts —— 帧 event 类型 → 人话短语（纯函数）。
//
// 职责：会话流内联事件行（EventChip）的文案与色调。
// 边界：
//   - 输入是帧的 event 类型名（W4a 刻意冗余在帧里），不查 events 表
//   - 白名单外的类型**原样透出**，不吞——契约会演进，前端比后端旧是常态
//   - 文案沿自原 EventMark 的 EVENT_LABEL（B100 的 failed/turn_failed 区分保留）

// EventPhrase 是一条事件的展示：text 文案 + tone 色调（warn 用琥珀）。
export interface EventPhrase {
  text: string
  tone: 'info' | 'warn'
}

// PHRASES 是已知事件类型的映射。可裁决类（permission_request/question）把人
// 指向工单面板；completed/failed 等没有可裁决物，不指（指了会让人扑空——
// 原 EventMark 的 ADJUDICABLE 纪律）。
const PHRASES: Record<string, EventPhrase> = {
  permission_request: { text: '权限工单：等待裁决——入口在左栏底部的工单面板', tone: 'warn' },
  question: { text: '提问工单：等待回答——入口在左栏底部的工单面板', tone: 'warn' },
  completed: { text: '一轮结束，进入待审', tone: 'info' },
  failed: { text: '任务失败', tone: 'warn' },
  turn_failed: { text: '回合失败', tone: 'warn' },
  delivery_failed: { text: '裁决已落库但没送到 executor', tone: 'warn' },
  stalled: { text: '看门狗：长时间无产出', tone: 'warn' },
}

// eventPhrase 返回事件的人话展示；未知类型原样透出（info 色调）。
export function eventPhrase(event: string): EventPhrase {
  return PHRASES[event] ?? { text: event, tone: 'info' }
}
```

`web/src/app/task/delivery.ts`：

```ts
// delivery.ts —— 模型报工 trailer 的提取（纯函数，best-effort）。
//
// 职责：从回合正文块的**末尾**探测报工 JSON（branch/commit/summary），
// 拆成交付摘要 + 剩余正文，供 DeliverySummaryCard 渲染成卡片。
//
// 边界：
//   - 不改协议、不假设 trailer 一定存在：提取失败返回 null，正文原样展示——
//     宁可少画一张卡，不能把正文吃掉
//   - 只认末尾的 JSON 对象：JSON 后面还有正文说明它不是 trailer

// Delivery 是报工摘要的已知字段（全部可选，至少命中一个才算 trailer）。
export interface Delivery {
  branch?: string
  commit?: string
  summary?: string
}

// extractDelivery 从文本末尾提取报工 trailer。
//
// 返回：
//   - { delivery, body }: 提取成功；body 是去掉 trailer 后的正文（已 trim）
//   - null: 末尾不是 JSON 对象 / 解析失败 / 无任何已知字段
export function extractDelivery(text: string): { delivery: Delivery; body: string } | null {
  const trimmed = text.trimEnd()
  if (!trimmed.endsWith('}')) return null
  // 从最后一个 '{' 往前逐个尝试：trailer 是扁平对象，正常一次命中；
  // 正文里出现 '{' 时多试几次也只是常数开销
  for (let i = trimmed.lastIndexOf('{'); i >= 0; i = trimmed.lastIndexOf('{', i - 1)) {
    const candidate = trimmed.slice(i)
    let parsed: unknown
    try {
      parsed = JSON.parse(candidate)
    } catch {
      continue
    }
    if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) return null
    const o = parsed as Record<string, unknown>
    const delivery: Delivery = {}
    if (typeof o.branch === 'string') delivery.branch = o.branch
    if (typeof o.commit === 'string') delivery.commit = o.commit
    if (typeof o.summary === 'string') delivery.summary = o.summary
    if (!delivery.branch && !delivery.commit && !delivery.summary) return null
    return { delivery, body: trimmed.slice(0, i).trimEnd() }
  }
  return null
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/task/eventPhrase.test.ts src/app/task/delivery.test.ts`
Expected: PASS

- [ ] **Step 5: 自检注释**：两个文件头职责/边界齐（不吞数据的 why 都写了）；导出全部有 doc 注释。纯函数无日志点。

- [ ] **Step 6: Commit**

```bash
git add web/src/app/task/eventPhrase.ts web/src/app/task/eventPhrase.test.ts web/src/app/task/delivery.ts web/src/app/task/delivery.test.ts
git commit -m "feat(web): 事件人话化与报工 trailer 提取纯函数"
```

---

### Task 5: 会话流块组件——统一元数据行语言

**Files:**
- Create: `web/src/app/task/meta.tsx`
- Create: `web/src/app/task/EventChip.tsx`
- Create: `web/src/app/task/UserInstructionBlock.tsx`
- Create: `web/src/app/task/DeliverySummaryCard.tsx`
- Modify: `web/src/app/task/ThinkingBlock.tsx`（整文件重写样式）
- Modify: `web/src/app/task/ToolCard.tsx`（折叠头重写为元数据行；展开区保留）
- Modify: `web/src/app/task/frames.ts`（turn 块带 instructions，~L86 与 ~L156）
- Modify: `web/src/app/task/blocks.test.tsx`、`web/src/app/task/frames.test.ts`

**Interfaces:**
- Consumes: `eventPhrase`（Task 4）、`extractDelivery`（Task 4）、`toolState` / `ToolBlock`（现有 frames.ts）、`formatFull`（现有 format.ts）
- Produces（Task 6 消费）:

```ts
// meta.tsx
export function MetaRow(props: { glyph: ReactNode; tone?: 'info' | 'warn'; children: ReactNode }): JSX.Element
// EventChip.tsx
export function EventChip(props: { event: string; ts: string }): JSX.Element
// UserInstructionBlock.tsx
export function UserInstructionBlock(props: { text: string; ts: string }): JSX.Element
// DeliverySummaryCard.tsx
export function DeliverySummaryCard(props: { delivery: Delivery }): JSX.Element
// frames.ts turn 块新增字段
{ kind: 'turn'; key: string; turn: number; reason: string; ts: string; instructions: string }
```

- [ ] **Step 1: 写失败测试**——`web/src/app/task/frames.test.ts` 追加（跟随该文件现有 buildBlocks 测试风格）：

```ts
it('turn_start 的 instructions 进 turn 块（缺席时为空串）', () => {
  const blocks = buildBlocks([
    { seq: 1, ts: T, turn: 1, type: 'turn_start', reason: 'dispatch' },
    { seq: 2, ts: T, turn: 2, type: 'turn_start', reason: 'send', instructions: '补测试' },
  ] as Frame[])
  expect(blocks[0]).toMatchObject({ kind: 'turn', instructions: '' })
  expect(blocks[1]).toMatchObject({ kind: 'turn', reason: 'send', instructions: '补测试' })
})
```

（`T` 用该文件现有的时间常量；没有就 `const T = '2026-08-17T10:00:00Z'`。）

`web/src/app/task/blocks.test.tsx` 追加：

```tsx
describe('EventChip', () => {
  it('白名单事件渲染人话短语', () => {
    render(<EventChip event="completed" ts="2026-08-17T10:00:00Z" />)
    expect(screen.getByText(/一轮结束，进入待审/)).toBeInTheDocument()
  })
  it('未知事件原样透出', () => {
    render(<EventChip event="mystery_event" ts="2026-08-17T10:00:00Z" />)
    expect(screen.getByText(/mystery_event/)).toBeInTheDocument()
  })
})

describe('UserInstructionBlock', () => {
  it('渲染审核者身份行与指令原文', () => {
    render(<UserInstructionBlock text="补上变异测试记录" ts="2026-08-17T14:20:00Z" />)
    expect(screen.getByText(/审核者/)).toBeInTheDocument()
    expect(screen.getByText('补上变异测试记录')).toBeInTheDocument()
  })
})

describe('DeliverySummaryCard', () => {
  it('渲染命中的字段，缺席字段不渲染行', () => {
    render(<DeliverySummaryCard delivery={{ branch: 'bench/b93', summary: '全落地' }} />)
    expect(screen.getByText('bench/b93')).toBeInTheDocument()
    expect(screen.getByText('全落地')).toBeInTheDocument()
    expect(screen.queryByText('commit')).not.toBeInTheDocument()
  })
})
```

（import 跟随该文件现有写法：`render`/`screen` 来自 `@testing-library/react`，新组件从各自文件 import。）

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/task/frames.test.ts src/app/task/blocks.test.tsx`
Expected: FAIL（instructions 字段缺失；组件不存在）

- [ ] **Step 3: frames.ts 小改**——turn 块类型（L86）：

```ts
  | { kind: 'turn'; key: string; turn: number; reason: string; ts: string; instructions: string }
```

buildBlocks 的 turn_start 分支（L156）：

```ts
      case 'turn_start':
        blocks.push({ kind: 'turn', key, turn, reason: fr.reason ?? '', ts: fr.ts, instructions: fr.instructions ?? '' })
        break
```

- [ ] **Step 4: 实现 meta.tsx**：

```tsx
// meta.tsx —— 会话流「元数据行」的统一容器。
//
// 职责：思维链/工具行/事件行共用的一套视觉语言（左对齐、12px、muted、同一左轨、
// 行首小符号），保证非正文元素只有一种形态——主次靠这个约定成立（spec §2.2）。
// 边界：只管容器样式，不认识任何具体块类型；tone=warn 只换文字颜色，不加底色。
import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'

// MetaRow 渲染一行元数据。glyph 是行首符号位（宽度固定对齐左轨）。
export function MetaRow({ glyph, tone = 'info', children }: {
  glyph: ReactNode
  tone?: 'info' | 'warn'
  children: ReactNode
}) {
  return (
    <div
      className={cn(
        'my-1 flex items-center gap-2 py-0.5 text-xs',
        tone === 'warn' ? 'text-amber-600 dark:text-amber-500' : 'text-muted-foreground',
      )}
    >
      <span className="w-3.5 shrink-0 text-center">{glyph}</span>
      {children}
    </div>
  )
}
```

- [ ] **Step 5: 实现 EventChip.tsx**：

```tsx
// EventChip —— 会话流的生命周期事件行（EventMark 的继任者）。
//
// 职责：一行人话说明因果（派发/工单/回合结束/失败…），文案与色调来自 eventPhrase。
// 边界：
//   - **不可操作**（沿 EventMark 纪律）：裁决入口只在全局工单弹层，这里只指路
//   - 不显示 payload：权限/提问全文只在工单面板
import { formatFull } from '../lib/format'
import { eventPhrase } from './eventPhrase'
import { MetaRow } from './meta'

// EventChip 渲染一行事件。event 是帧的事件类型名，ts 是帧时间戳（RFC3339）。
export function EventChip({ event, ts }: { event: string; ts: string }) {
  const p = eventPhrase(event)
  return (
    <MetaRow glyph={p.tone === 'warn' ? '⚠' : '◇'} tone={p.tone}>
      <span className="min-w-0 flex-1 break-words">
        {p.text}
        <span className="ml-2 text-[11px] opacity-70">{formatFull(ts)}</span>
      </span>
    </MetaRow>
  )
}
```

- [ ] **Step 6: 重写 ThinkingBlock.tsx**（整文件替换）：

```tsx
// ThinkingBlock —— 思维链的折叠行（元数据行语言）。
//
// 职责：默认一行「思维链 · N 字」，点开是左边线引文块。
// 边界：思维链绝不混入正文（W4a 纪律）；不做 markdown 渲染，原文展示。
import { useState } from 'react'
import { cn } from '@/lib/utils'

// ThinkingBlock 渲染一段已合并的思维链增量。text 为完整思维链文本。
export function ThinkingBlock({ text }: { text: string }) {
  const [open, setOpen] = useState(false)
  return (
    <div className="my-1 text-xs text-muted-foreground">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex items-center gap-2 py-0.5 hover:text-foreground"
      >
        <span className={cn('w-3.5 shrink-0 text-center transition-transform', open && 'rotate-90')}>▸</span>
        思维链 · {[...text].length} 字
      </button>
      {open && (
        <div className="ml-[7px] whitespace-pre-wrap break-words border-l-2 border-border py-1 pl-3 leading-relaxed">
          {text}
        </div>
      )}
    </div>
  )}
```

- [ ] **Step 7: 改造 ToolCard.tsx 折叠头**——保留 `argSummary`/`truncNote`/展开区结构与全部截断、未返回逻辑，仅把外框与折叠头换成元数据行形态。折叠头替换为：

```tsx
  const DOT_CLS: Record<ToolState, string> = {
    ok: 'bg-green-600',
    error: 'bg-destructive',
    running: 'bg-amber-500 animate-pulse',
    gone: 'border border-amber-500 bg-transparent',
  }
  // 外层去掉 rounded border 卡片壳，改为元数据行 + 展开块：
  return (
    <div className="my-1 text-xs">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-2 py-0.5 text-left text-muted-foreground hover:text-foreground"
      >
        <span className="flex w-3.5 shrink-0 justify-center">
          <span className={cn('size-[7px] rounded-full', DOT_CLS[st])} />
        </span>
        <span className="shrink-0 font-medium text-foreground">{block.tool || '(未知工具)'}</span>
        <span className="min-w-0 flex-1 truncate font-mono">{argSummary(block.input)}</span>
        <span className="shrink-0 text-[11px]">{STATE_LABEL[st]}</span>
      </button>
      {open && (
        /* 原展开区整块保留，容器类名改为：
           "ml-[7px] border-l-2 border-border pl-3" 的引文式块 */
      )}
    </div>
  )
```

（`cn` 从 `@/lib/utils` import；Badge/lucide 图标 import 移除；`STATE_LABEL`、未返回文案、truncNote 等逻辑一律不动。）

- [ ] **Step 8: 实现 UserInstructionBlock.tsx**：

```tsx
// UserInstructionBlock —— 审核者指令的右对齐气泡（对话感的关键件）。
//
// 职责：send 回合起点展示 continue 指令 / reply 应答的原文。
// 边界：数据来自 turn_start 帧的 instructions（Task 1）；旧帧无此字段时
// 本组件不渲染（由 ConversationStream 判空决定），不在这里造假数据。
import { formatFull } from '../lib/format'

// UserInstructionBlock 渲染一条审核者消息。text 为指令原文，ts 为回合时刻。
export function UserInstructionBlock({ text, ts }: { text: string; ts: string }) {
  return (
    <div className="my-3 ml-auto w-fit max-w-[78%] rounded-xl rounded-br-sm bg-muted px-3 py-2 text-sm leading-relaxed">
      <div className="mb-0.5 text-right text-[11px] text-muted-foreground">审核者 · {formatFull(ts)}</div>
      <div className="whitespace-pre-wrap break-words">{text}</div>
    </div>
  )
}
```

- [ ] **Step 9: 实现 DeliverySummaryCard.tsx**：

```tsx
// DeliverySummaryCard —— 模型报工 trailer 的交付摘要卡。
//
// 职责：把 extractDelivery 命中的字段（分支/commit/摘要）渲染成结构化卡片。
// 边界：只渲染命中的字段；提取与判定在 delivery.ts，本组件不碰原始文本。
import { shortCommit } from '../lib/format'
import type { Delivery } from './delivery'

// DeliverySummaryCard 渲染一张交付摘要卡。delivery 至少含一个字段（调用方保证）。
export function DeliverySummaryCard({ delivery }: { delivery: Delivery }) {
  return (
    <div className="my-3 rounded-lg border bg-sidebar p-3 text-sm">
      <div className="mb-1.5 font-semibold">✅ 交付摘要</div>
      <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 text-[13px]">
        {delivery.branch && (
          <>
            <dt className="text-muted-foreground">分支</dt>
            <dd className="break-all font-mono text-xs">{delivery.branch}</dd>
          </>
        )}
        {delivery.commit && (
          <>
            <dt className="text-muted-foreground">commit</dt>
            <dd className="font-mono text-xs">{shortCommit(delivery.commit)}</dd>
          </>
        )}
        {delivery.summary && (
          <>
            <dt className="text-muted-foreground">摘要</dt>
            <dd className="break-words">{delivery.summary}</dd>
          </>
        )}
      </dl>
    </div>
  )
}
```

（`bg-sidebar` 若项目 Tailwind 无此 token，用 `bg-muted/40`——以 `web/src/index.css` 实际 token 为准。）

- [ ] **Step 10: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/task/`
Expected: PASS（新用例全绿，ToolCard/frames 既有用例不回归；ToolCard 既有测试若断言旧卡片类名/Badge 文案，按新结构更新断言——行为断言「状态文案/截断提示/未返回文案」必须保留）

- [ ] **Step 11: 自检注释**：四个新文件头注释齐；ToolCard/ThinkingBlock 文件头更新为新形态描述；组件无日志点（错误路径不存在，纯展示）。

- [ ] **Step 12: Commit**

```bash
git add web/src/app/task/
git commit -m "feat(web): 会话流块组件统一元数据行语言，新增指令气泡与交付摘要卡"
```

---

### Task 6: ConversationStream——唯一滚动区的会话流

**Files:**
- Create: `web/src/app/task/ConversationStream.tsx`
- Create: `web/src/app/task/ConversationStream.test.tsx`
- Delete（本 task 只创建新件，删除旧件在 Task 10 总装时做，避免中间态编译破）

**Interfaces:**
- Consumes: `Block`/`ToolBlock`（frames.ts）、`TextBlock`/`ThinkingBlock`/`ToolCard`/`EventChip`/`UserInstructionBlock`/`DeliverySummaryCard`/`UnknownBlock`（Task 5 与现有）、`extractDelivery`（Task 4）
- Produces（Task 10 的 TuiTab 消费）:

```ts
export interface ConversationStreamProps {
  taskId: string
  taskState: string
  blocks: Block[]
  badLines: number
  startOffset: number   // >0 表示还有更早的帧未加载
  atCap: boolean
  error: string | null
  loadingEarlier: boolean
  onLoadEarlier: () => void
  onRetry: () => void
}
export function ConversationStream(props: ConversationStreamProps): JSX.Element
// 回合锚点 id 约定（TuiHeader 跳转用）：`turn-${taskId}-${turn}`
```

- [ ] **Step 1: 写失败测试**——`web/src/app/task/ConversationStream.test.tsx`（迁移 TimelinePanel.test.tsx 中「渲染块」类断言的精神，按新 props 直供 blocks 重写；TimelinePanel.test 的取数/开关类断言不迁——取数已上移）：

```tsx
// ConversationStream.test.tsx —— 会话流渲染：回合分隔/指令气泡/交付卡/提示行。
import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { ConversationStream } from './ConversationStream'
import type { Block } from './frames'

const noop = () => {}
const base = {
  taskId: 't1', taskState: 'waiting_review',
  badLines: 0, startOffset: 0, atCap: false, error: null,
  loadingEarlier: false, onLoadEarlier: noop, onRetry: noop,
}

describe('ConversationStream', () => {
  it('回合分隔线带序号与起因；send 回合渲染审核者气泡', () => {
    const blocks: Block[] = [
      { kind: 'turn', key: 'f1', turn: 1, reason: 'dispatch', ts: '2026-08-17T11:16:00Z', instructions: '' },
      { kind: 'turn', key: 'f2', turn: 2, reason: 'send', ts: '2026-08-17T14:20:00Z', instructions: '补测试' },
    ]
    render(<ConversationStream {...base} blocks={blocks} />)
    expect(screen.getByText(/回合 1/)).toBeInTheDocument()
    expect(screen.getByText(/派发/)).toBeInTheDocument()
    expect(screen.getByText(/续发指令/)).toBeInTheDocument()
    expect(screen.getByText('补测试')).toBeInTheDocument()
    expect(screen.getByText(/审核者/)).toBeInTheDocument()
  })

  it('text 块末尾的报工 trailer 拆成交付摘要卡', () => {
    const blocks: Block[] = [
      { kind: 'text', key: 'f3', turn: 1, text: '全部完成。\n\n{"branch":"bench/x","summary":"落地"}' },
    ]
    render(<ConversationStream {...base} blocks={blocks} />)
    expect(screen.getByText('全部完成。')).toBeInTheDocument()
    expect(screen.getByText('交付摘要')).toBeInTheDocument()
    expect(screen.getByText('bench/x')).toBeInTheDocument()
  })

  it('坏行与帧上限提示为流内元数据行；空流显示等待文案', () => {
    const { rerender } = render(<ConversationStream {...base} blocks={[]} badLines={3} atCap />)
    expect(screen.getByText(/3 行无法解析/)).toBeInTheDocument()
    expect(screen.getByText(/handoff frames/)).toBeInTheDocument()
    rerender(<ConversationStream {...base} blocks={[]} />)
    expect(screen.getByText(/等待模型输出/)).toBeInTheDocument()
  })

  it('startOffset>0 显示加载更早，点击回调', () => {
    const onLoadEarlier = vi.fn()
    render(<ConversationStream {...base} blocks={[]} startOffset={100} onLoadEarlier={onLoadEarlier} />)
    screen.getByRole('button', { name: /加载更早/ }).click()
    expect(onLoadEarlier).toHaveBeenCalled()
  })

  it('error 显示原文与重试按钮', () => {
    render(<ConversationStream {...base} blocks={[]} error="连接断了" />)
    expect(screen.getByRole('alert')).toHaveTextContent('连接断了')
    expect(screen.getByRole('button', { name: /重试/ })).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/task/ConversationStream.test.tsx`
Expected: FAIL（组件不存在）

- [ ] **Step 3: 实现**——`web/src/app/task/ConversationStream.tsx`：

```tsx
// ConversationStream —— 会话流（TimelinePanel 的继任者，任务 TUI 的主角）。
//
// 职责：
//   - 渲染块序列：回合分隔（send 回合接审核者气泡）、正文（末尾 trailer 拆
//     交付卡）、思维链、工具行、事件行、未知块
//   - 唯一滚动区：跟随滚动（stickBottom）、加载更早 + prepend 补偿
//   - 坏行/帧上限/错误提示以流内元数据行呈现
//
// 边界：
//   - 不取数：frames 流由 TuiTab 持有（页头回合下拉与本组件共享 turns），
//     本组件只吃 blocks 与流状态 props
//   - 不含原始视图切换：原始 render.log 在调试抽屉（spec §2.5）
//   - 回合锚点 id 约定 `turn-${taskId}-${turn}`，TuiHeader 跳转靠它
import { useLayoutEffect, useRef } from 'react'
import { Button } from '@/components/ui/button'
import type { Block } from './frames'
import { extractDelivery } from './delivery'
import { TextBlock } from './TextBlock'
import { ThinkingBlock } from './ThinkingBlock'
import { ToolCard } from './ToolCard'
import { EventChip } from './EventChip'
import { UserInstructionBlock } from './UserInstructionBlock'
import { DeliverySummaryCard } from './DeliverySummaryCard'
import { UnknownBlock } from './UnknownBlock'
import { MetaRow } from './meta'
import { formatFull } from '../lib/format'

// TURN_REASON 沿自 TimelinePanel：dispatch/send 的中文映射，未知原样显示。
const TURN_REASON: Record<string, string> = { dispatch: '派发', send: '续发指令' }

// stickThreshold 是「算作在底部」的像素阈值（与原 TimelinePanel 一致）。
const stickThreshold = 40

export interface ConversationStreamProps {
  taskId: string
  taskState: string
  blocks: Block[]
  badLines: number
  startOffset: number
  atCap: boolean
  error: string | null
  loadingEarlier: boolean
  onLoadEarlier: () => void
  onRetry: () => void
}

// ConversationStream 渲染一个任务的会话流。滚动补偿与跟随逻辑整体平移自
// TimelinePanel（useLayoutEffect + prependRef 的实现原样保留，注释见彼处 git 史）。
export function ConversationStream({
  taskId, taskState, blocks, badLines, startOffset, atCap, error,
  loadingEarlier, onLoadEarlier, onRetry,
}: ConversationStreamProps) {
  const scrollRef = useRef<HTMLDivElement>(null)
  const stickBottom = useRef(true)
  const prependRef = useRef<number | null>(null)

  useLayoutEffect(() => {
    const el = scrollRef.current
    if (!el) return
    if (prependRef.current !== null) {
      el.scrollTop += el.scrollHeight - prependRef.current
      prependRef.current = null
      return
    }
    const nearBottom = el.scrollHeight - el.scrollTop - el.clientHeight < stickThreshold
    if (stickBottom.current || nearBottom) {
      el.scrollTop = el.scrollHeight
      stickBottom.current = true
    }
  }, [blocks])

  const onScroll = () => {
    const el = scrollRef.current
    if (!el) return
    stickBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < stickThreshold
  }

  const handleLoadEarlier = () => {
    prependRef.current = scrollRef.current?.scrollHeight ?? 0
    onLoadEarlier()
  }

  return (
    <div ref={scrollRef} onScroll={onScroll} className="h-full min-h-0 overflow-y-auto">
      <div className="mx-auto max-w-[760px] px-6 py-5">
        {startOffset > 0 && !atCap && (
          <div className="mb-3 flex justify-center">
            <Button variant="ghost" size="sm" disabled={loadingEarlier} onClick={handleLoadEarlier}>
              {loadingEarlier ? '加载中…' : '↑ 加载更早'}
            </Button>
          </div>
        )}

        {badLines > 0 && (
          <MetaRow glyph="⚠" tone="warn">
            {badLines} 行无法解析，已跳过（其余帧不受影响；帧文件可能被截断或采集侧有 bug）
          </MetaRow>
        )}
        {atCap && (
          <MetaRow glyph="◇">
            已加载帧数到上限，不再往前加载——更早的内容请用 <span className="font-mono">handoff frames</span> 回看
          </MetaRow>
        )}
        {error && (
          <p role="alert" className="my-2 flex flex-wrap items-center gap-2 break-words text-sm text-destructive">
            {error}
            <Button variant="outline" size="sm" onClick={onRetry}>重试</Button>
          </p>
        )}

        {blocks.length === 0 && error === null ? (
          <p className="text-sm text-muted-foreground">等待模型输出…（frames.jsonl 尚为空属正常）</p>
        ) : (
          blocks.map((b) => {
            switch (b.kind) {
              case 'turn':
                return (
                  <div key={b.key}>
                    <div
                      id={`turn-${taskId}-${b.turn}`}
                      className="mb-2 mt-5 flex items-center gap-2 text-xs text-muted-foreground first:mt-0"
                    >
                      <span className="h-px flex-1 bg-border" />
                      <span>
                        <b className="font-semibold text-foreground">回合 {b.turn}</b>
                        {' · '}{TURN_REASON[b.reason] ?? b.reason}{' · '}{formatFull(b.ts)}
                      </span>
                      <span className="h-px flex-1 bg-border" />
                    </div>
                    {/* send 回合带指令原文时渲染审核者气泡；旧帧缺席则只有分隔线 */}
                    {b.instructions !== '' && <UserInstructionBlock text={b.instructions} ts={b.ts} />}
                  </div>
                )
              case 'text': {
                // 末尾报工 trailer 拆成交付卡（best-effort，见 delivery.ts）
                const d = extractDelivery(b.text)
                if (d) {
                  return (
                    <div key={b.key} className="my-2">
                      {d.body !== '' && <TextBlock text={d.body} />}
                      <DeliverySummaryCard delivery={d.delivery} />
                    </div>
                  )
                }
                return <div key={b.key} className="my-2"><TextBlock text={b.text} /></div>
              }
              case 'thinking':
                return <ThinkingBlock key={b.key} text={b.text} />
              case 'tool':
                return <ToolCard key={b.key} block={b} taskState={taskState} />
              case 'event':
                return <EventChip key={b.key} event={b.event} ts={b.ts} />
              case 'unknown':
                return <UnknownBlock key={b.key} type={b.type} raw={b.raw} />
            }
          })
        )}
      </div>
    </div>
  )
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/task/ConversationStream.test.tsx`
Expected: PASS

- [ ] **Step 5: 自检注释**：文件头职责/边界（不取数的 why、锚点约定）齐；TURN_REASON/stickThreshold 注释保留出处。

- [ ] **Step 6: Commit**

```bash
git add web/src/app/task/ConversationStream.tsx web/src/app/task/ConversationStream.test.tsx
git commit -m "feat(web): ConversationStream 会话流——唯一滚动区与限宽正文"
```

---

### Task 7: TuiHeader 两行页头 + UsageChip

**Files:**
- Create: `web/src/app/task/UsageChip.tsx`
- Create: `web/src/app/task/TuiHeader.tsx`
- Create: `web/src/app/task/TuiHeader.test.tsx`

**Interfaces:**
- Consumes: `Task`/`Usage`/`Cumulative`（api/types）、`stateLabel`/`stateBadgeVariant`（board/columns.ts）、`formatTokens`/`formatCost`/`formatRelative`/`shortID`（lib/format.ts）、`WsStatus`（api/ws）
- Produces（Task 10 消费）:

```ts
export function UsageChip(props: { usage?: Usage; cumulative?: Cumulative }): JSX.Element | null
export interface TuiHeaderProps {
  task: Task
  turns: number[]
  turnsPartial: boolean            // startOffset>0：下拉底部注「仅覆盖已加载范围」
  onJumpTurn: (turn: number) => void
  reviewAvailable: boolean         // waiting_review 才显示审阅栏开关
  reviewOpen: boolean
  onToggleReview: () => void
  onOpenDebug: () => void
  wsStatus: WsStatus
  disconnected: boolean
}
export function TuiHeader(props: TuiHeaderProps): JSX.Element
```

- [ ] **Step 1: 写失败测试**——`web/src/app/task/TuiHeader.test.tsx`：

```tsx
// TuiHeader.test.tsx —— 两行页头：身份/动作行 + 遥测行（模型、回合下拉、ctx）。
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { TuiHeader } from './TuiHeader'
import type { Task } from '../../api/types'

const task = {
  id: '7a8334f4-0000-0000-0000-000000000000',
  name: 'B93 基准评测', state: 'waiting_review', executor: 'opencode',
  actual_model: 'qwen3-coder',
  created_at: '2026-08-17T11:16:00Z', updated_at: '2026-08-17T14:31:00Z',
  usage: { context_tokens: 41236, context_window: 200000 },
  cumulative: { input_tokens: 182400, cached_tokens: 1210000, output_tokens: 96800, total_tokens: 1489200 },
} as unknown as Task

const base = {
  task, turns: [1, 2], turnsPartial: false, onJumpTurn: vi.fn(),
  reviewAvailable: true, reviewOpen: false, onToggleReview: vi.fn(), onOpenDebug: vi.fn(),
  wsStatus: 'open' as const, disconnected: false,
}

describe('TuiHeader', () => {
  it('第一行：任务名、状态徽章、审阅栏与调试按钮', () => {
    render(<TuiHeader {...base} />)
    expect(screen.getByText('B93 基准评测')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /审阅栏/ })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /调试/ })).toBeInTheDocument()
  })
  it('遥测行：executor、实际模型、ctx 读数', () => {
    render(<TuiHeader {...base} />)
    expect(screen.getByText(/opencode/)).toBeInTheDocument()
    expect(screen.getByText(/qwen3-coder/)).toBeInTheDocument()
    expect(screen.getByText(/41\.2k/)).toBeInTheDocument()
    expect(screen.getByText(/200k/)).toBeInTheDocument()
  })
  it('回合下拉列出回合并回调跳转', () => {
    const onJumpTurn = vi.fn()
    render(<TuiHeader {...base} onJumpTurn={onJumpTurn} />)
    fireEvent.click(screen.getByRole('button', { name: /回合 2/ }))
    fireEvent.click(screen.getByRole('button', { name: /回合 1/ }))
    expect(onJumpTurn).toHaveBeenCalledWith(1)
  })
  it('非 review 态不显示审阅栏按钮；无 usage 不渲染 ctx', () => {
    render(<TuiHeader {...base} reviewAvailable={false} task={{ ...task, usage: undefined } as Task} />)
    expect(screen.queryByRole('button', { name: /审阅栏/ })).not.toBeInTheDocument()
    expect(screen.queryByText(/ctx/)).not.toBeInTheDocument()
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/task/TuiHeader.test.tsx`
Expected: FAIL

- [ ] **Step 3: 实现 UsageChip.tsx**：

```tsx
// UsageChip —— 页头 ctx 小表 + 两口径账目弹出（usage/cumulative 展示回归 TUI）。
//
// 职责：一眼读数（迷你条 + "ctx 41.2k / 200k"），点开完整账目。
// 边界：
//   - executor 没报 context_window 时只显绝对值——前端**不猜分母**（现有纪律）
//   - usage 与 cumulative 都缺席时整体不渲染（返回 null）：没有账目不画空表
import { useState } from 'react'
import type { Cumulative, Usage } from '../../api/types'
import { formatCost, formatTokens } from '../lib/format'

// UsageChip 渲染 ctx 读数。usage=当前占用，cumulative=累计消耗，均可缺席。
export function UsageChip({ usage, cumulative }: { usage?: Usage; cumulative?: Cumulative }) {
  const [open, setOpen] = useState(false)
  if (!usage && !cumulative) return null

  const pct = usage?.context_window
    ? Math.min(100, Math.round((usage.context_tokens / usage.context_window) * 100))
    : null

  return (
    <span className="relative">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="inline-flex items-center gap-1.5 hover:text-foreground"
      >
        {pct !== null && (
          <span className="h-[5px] w-14 overflow-hidden rounded-full bg-muted">
            <span className="block h-full rounded-full bg-green-600" style={{ width: `${pct}%` }} />
          </span>
        )}
        {usage && (
          <span>
            ctx {formatTokens(usage.context_tokens)}
            {usage.context_window ? ` / ${formatTokens(usage.context_window)}` : ''}
          </span>
        )}
        {!usage && <span>累计 {formatTokens(cumulative!.total_tokens)}</span>}
      </button>
      {open && (
        <div className="absolute left-0 top-6 z-10 w-64 rounded-lg border bg-background p-3 text-xs shadow-lg">
          {usage && (
            <>
              <div className="mb-1 font-semibold">当前占用</div>
              <dl className="mb-2 grid grid-cols-[auto_1fr] gap-x-3">
                <dt className="text-muted-foreground">context</dt>
                <dd className="text-right font-mono">
                  {usage.context_tokens.toLocaleString()}
                  {usage.context_window ? ` / ${usage.context_window.toLocaleString()}（${pct}%）` : ''}
                </dd>
              </dl>
            </>
          )}
          {cumulative && (
            <>
              <div className="mb-1 font-semibold">累计消耗</div>
              <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5">
                <dt className="text-muted-foreground">输入</dt>
                <dd className="text-right font-mono">{formatTokens(cumulative.input_tokens)}</dd>
                <dt className="text-muted-foreground">缓存命中</dt>
                <dd className="text-right font-mono">{formatTokens(cumulative.cached_tokens)}</dd>
                <dt className="text-muted-foreground">输出</dt>
                <dd className="text-right font-mono">{formatTokens(cumulative.output_tokens)}</dd>
                <dt className="text-muted-foreground">合计</dt>
                <dd className="text-right font-mono">{formatTokens(cumulative.total_tokens)}</dd>
                {cumulative.cost && (
                  <>
                    <dt className="text-muted-foreground">花费</dt>
                    <dd className="text-right font-mono">
                      {formatCost(cumulative.cost).text}
                      <span className="ml-1 font-sans text-muted-foreground">{formatCost(cumulative.cost).hint}</span>
                    </dd>
                  </>
                )}
              </dl>
            </>
          )}
        </div>
      )}
    </span>
  )
}
```

（`formatTokens`/`formatCost` 的确切返回形态以 `lib/format.ts` 为准，断言若与 41.2k 格式不符，以 formatTokens 的实际输出改测试断言——**格式跟随现有函数，不新造格式化**。）

- [ ] **Step 4: 实现 TuiHeader.tsx**：

```tsx
// TuiHeader —— TUI tab 的两行页头。
//
// 职责：第一行身份 + 动作（审阅栏开关/调试）；第二行遥测（executor·模型·回合
// 下拉·运行时长·ctx）。回合下拉的数据由 TuiTab 从 frames 派生传入。
// 边界：不取数、不持流；「回合下拉只覆盖已加载范围」必须写在下拉里（turnsPartial）。
import { useState } from 'react'
import type { Task } from '../../api/types'
import type { WsStatus } from '../../api/ws'
import { Badge } from '@/components/ui/badge'
import { stateBadgeVariant, stateLabel } from '../board/columns'
import { formatRelative, shortID } from '../lib/format'
import { UsageChip } from './UsageChip'
import { cn } from '@/lib/utils'

export interface TuiHeaderProps {
  task: Task
  turns: number[]
  turnsPartial: boolean
  onJumpTurn: (turn: number) => void
  reviewAvailable: boolean
  reviewOpen: boolean
  onToggleReview: () => void
  onOpenDebug: () => void
  wsStatus: WsStatus
  disconnected: boolean
}

// TuiHeader 渲染页头。动作按钮的可见性由父级传入的状态决定，这里不判状态机。
export function TuiHeader({
  task, turns, turnsPartial, onJumpTurn,
  reviewAvailable, reviewOpen, onToggleReview, onOpenDebug,
  wsStatus, disconnected,
}: TuiHeaderProps) {
  const [turnsOpen, setTurnsOpen] = useState(false)
  const latestTurn = turns.length > 0 ? turns[turns.length - 1] : null

  return (
    <div className="flex flex-col gap-0.5 border-b px-3.5 py-2">
      <div className="flex min-w-0 items-center gap-2.5">
        <span className="truncate text-sm font-semibold">
          {task.name || task.plan_summary || '（无名称）'}
        </span>
        <Badge variant={stateBadgeVariant(task.state)}>{stateLabel(task.state)}</Badge>
        {disconnected && <Badge variant="destructive">已断开</Badge>}
        {!disconnected && wsStatus === 'open' && <Badge variant="outline">实时</Badge>}
        <span className="ml-auto" />
        {reviewAvailable && (
          <button
            type="button"
            onClick={onToggleReview}
            className={cn(
              'rounded-md border px-2.5 py-0.5 text-xs',
              reviewOpen ? 'border-primary bg-primary text-primary-foreground' : 'hover:bg-muted',
            )}
          >
            审阅栏
          </button>
        )}
        <button type="button" onClick={onOpenDebug} className="rounded-md border px-2.5 py-0.5 text-xs hover:bg-muted">
          调试
        </button>
      </div>

      <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-xs text-muted-foreground">
        <span>
          {task.executor}
          {task.actual_model ? ` · ${task.actual_model}` : ''}
        </span>
        <span className="opacity-50">·</span>
        {latestTurn !== null && (
          <span className="relative">
            <button type="button" onClick={() => setTurnsOpen((v) => !v)} className="hover:text-foreground">
              回合 {latestTurn} ▾
            </button>
            {turnsOpen && (
              <div className="absolute left-0 top-6 z-10 min-w-40 rounded-lg border bg-background p-1 shadow-lg">
                {turns.map((t) => (
                  <button
                    key={t}
                    type="button"
                    onClick={() => { setTurnsOpen(false); onJumpTurn(t) }}
                    className="block w-full rounded px-2.5 py-1 text-left hover:bg-muted"
                  >
                    回合 {t}
                  </button>
                ))}
                {/* 锚点只覆盖已加载范围，必须写出来——不假装是全量目录 */}
                {turnsPartial && (
                  <p className="px-2.5 py-1 text-[11px]">仅覆盖已加载范围，更早的需先加载</p>
                )}
              </div>
            )}
          </span>
        )}
        <span className="opacity-50">·</span>
        <span>派发于 {formatRelative(task.created_at)}</span>
        <span className="opacity-50">·</span>
        <UsageChip usage={task.usage} cumulative={task.cumulative} />
        <span className="ml-auto font-mono text-[11px]">handoff-{shortID(task.id)}</span>
      </div>
    </div>
  )
}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/task/TuiHeader.test.tsx`
Expected: PASS（formatTokens 输出格式若与断言不符，改测试断言对齐真实输出）

- [ ] **Step 6: 自检注释**：两个文件头齐；UsageChip「不猜分母」「无账目不画」的 why 都写了。

- [ ] **Step 7: Commit**

```bash
git add web/src/app/task/UsageChip.tsx web/src/app/task/TuiHeader.tsx web/src/app/task/TuiHeader.test.tsx
git commit -m "feat(web): TUI 两行页头与 ctx/累计用量小表"
```

---

### Task 8: ReviewSidePanel + DiffView——审阅右滑栏

**Files:**
- Create: `web/src/app/task/DiffView.tsx`
- Create: `web/src/app/task/ReviewSidePanel.tsx`
- Create: `web/src/app/task/ReviewSidePanel.test.tsx`

**Interfaces:**
- Consumes: `parseUnifiedDiff`（Task 3）、`fetchTaskDiff`/`fetchTaskFile`/`runTaskCommand`（现有 client）、`fetchTaskBranches`（Task 2）、`errorMessage`（lib/format）
- Produces（Task 10 消费）:

```ts
export function ReviewSidePanel(props: { taskId: string; onClose: () => void }): JSX.Element
export function DiffView(props: { text: string }): JSX.Element   // 内部自行 parse，null 回退裸 pre
```

- [ ] **Step 1: 写失败测试**——`web/src/app/task/ReviewSidePanel.test.tsx`（mock client 模块，风格跟随现有测试的 `vi.mock`）：

```tsx
// ReviewSidePanel.test.tsx —— 审阅栏：diff 自动加载/基准下拉/裸文本回退/跑命令。
import { render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ReviewSidePanel } from './ReviewSidePanel'

vi.mock('../../api/client', () => ({
  fetchTaskDiff: vi.fn(),
  fetchTaskBranches: vi.fn(),
  fetchTaskFile: vi.fn(),
  runTaskCommand: vi.fn(),
}))
import { fetchTaskBranches, fetchTaskDiff } from '../../api/client'

const DIFF = `diff --git a/a.md b/a.md
index 1..2 100644
--- a/a.md
+++ b/a.md
@@ -1 +1,2 @@
 x
+y
`

beforeEach(() => {
  vi.mocked(fetchTaskDiff).mockResolvedValue({ diff: DIFF })
  vi.mocked(fetchTaskBranches).mockResolvedValue({ branches: ['main', 'dev'], default: 'main' })
})

describe('ReviewSidePanel', () => {
  it('进栏自动加载 diff 并按文件分组展示 ± 统计', async () => {
    render(<ReviewSidePanel taskId="t1" onClose={() => {}} />)
    await waitFor(() => expect(screen.getByText('a.md')).toBeInTheDocument())
    expect(screen.getByText(/\+1/)).toBeInTheDocument()
    expect(fetchTaskDiff).toHaveBeenCalledWith('t1', undefined)
  })
  it('基准下拉列出分支；选择后带 base 重取', async () => {
    render(<ReviewSidePanel taskId="t1" onClose={() => {}} />)
    await waitFor(() => expect(screen.getByRole('combobox')).toBeInTheDocument())
    const sel = screen.getByRole('combobox') as HTMLSelectElement
    expect(screen.getByRole('option', { name: /dev/ })).toBeInTheDocument()
    sel.value = 'dev'
    sel.dispatchEvent(new Event('change', { bubbles: true }))
    await waitFor(() => expect(fetchTaskDiff).toHaveBeenCalledWith('t1', 'dev'))
  })
  it('不可解析的 diff 整体回退裸文本', async () => {
    vi.mocked(fetchTaskDiff).mockResolvedValue({ diff: '一段解析不了的输出' })
    render(<ReviewSidePanel taskId="t1" onClose={() => {}} />)
    await waitFor(() => expect(screen.getByText('一段解析不了的输出')).toBeInTheDocument())
  })
  it('分支接口失败：下拉退化为仅自动推导，diff 不受影响', async () => {
    vi.mocked(fetchTaskBranches).mockRejectedValue(new Error('探活失败'))
    render(<ReviewSidePanel taskId="t1" onClose={() => {}} />)
    await waitFor(() => expect(screen.getByText('a.md')).toBeInTheDocument())
    expect(screen.getByRole('option', { name: /自动推导/ })).toBeInTheDocument()
    expect(screen.getAllByRole('option')).toHaveLength(1)
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/task/ReviewSidePanel.test.tsx`
Expected: FAIL

- [ ] **Step 3: 实现 DiffView.tsx**：

```tsx
// DiffView —— diff 的按文件分组着色渲染。
//
// 职责：parseUnifiedDiff 的产物 → 可折叠文件组（±统计 + 行级着色）+ trailer。
// 边界：解析失败（null）整体回退裸 <pre>——diff 是审阅核心证据，绝不吞内容。
import { useMemo, useState } from 'react'
import { cn } from '@/lib/utils'
import { type FileDiff, parseUnifiedDiff } from './diff'

// LINE_CLS 是四种行的着色。绿/红沿用项目 diff 惯例色阶，深浅模式各自可读。
const LINE_CLS = {
  add: 'bg-green-500/10 text-green-800 dark:text-green-300',
  del: 'bg-red-500/10 text-red-800 dark:text-red-300',
  hunk: 'bg-muted text-muted-foreground',
  ctx: '',
} as const

// FileGroup 渲染一个文件的可折叠改动组；默认第一组展开由父级控制。
function FileGroup({ file, defaultOpen }: { file: FileDiff; defaultOpen: boolean }) {
  const [open, setOpen] = useState(defaultOpen)
  return (
    <div className="mb-2 overflow-hidden rounded-lg border">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-2 bg-muted/40 px-2.5 py-1.5 text-left font-mono text-xs hover:bg-muted"
      >
        <span className="min-w-0 flex-1 truncate">{file.path}</span>
        <span className="shrink-0 text-green-700 dark:text-green-400">+{file.adds}</span>
        <span className="shrink-0 text-red-700 dark:text-red-400">−{file.dels}</span>
      </button>
      {open && (
        <div className="overflow-x-auto font-mono text-xs leading-normal">
          {file.lines.map((l, i) => (
            <div key={i} className={cn('whitespace-pre px-2.5', LINE_CLS[l.kind])}>{l.text}</div>
          ))}
        </div>
      )}
    </div>
  )
}

// DiffView 渲染整份 diff 文本。text 为 fetchTaskDiff 返回的原文。
export function DiffView({ text }: { text: string }) {
  const parsed = useMemo(() => parseUnifiedDiff(text), [text])
  if (text.trim() === '') {
    return <p className="text-sm text-muted-foreground">没有差异（分支与基准一致）。</p>
  }
  if (parsed === null) {
    // 解析不出：整体回退裸文本，一个字都不能丢
    return <pre className="overflow-auto rounded-md bg-muted/30 p-3 font-mono text-xs leading-relaxed whitespace-pre-wrap break-words">{text}</pre>
  }
  const totalAdds = parsed.files.reduce((n, f) => n + f.adds, 0)
  const totalDels = parsed.files.reduce((n, f) => n + f.dels, 0)
  return (
    <div>
      <p className="mb-2 text-xs text-muted-foreground">
        {parsed.files.length} 个文件
        {' · '}<span className="text-green-700 dark:text-green-400">+{totalAdds}</span>
        {' '}<span className="text-red-700 dark:text-red-400">−{totalDels}</span>
      </p>
      {parsed.files.map((f, i) => (
        <FileGroup key={f.path + i} file={f} defaultOpen={i === 0} />
      ))}
      {parsed.trailer !== '' && (
        <div className="mt-2">
          <p className="mb-1 text-xs text-muted-foreground">提交列表</p>
          <pre className="overflow-auto rounded-md bg-muted/30 p-2.5 font-mono text-xs">{parsed.trailer}</pre>
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 4: 实现 ReviewSidePanel.tsx**——骨架自 ReviewPanel 平移（RunSection/FileSection 的取数与错误透出逻辑**原样搬运**，仅容器样式换），Diff 区重写：

```tsx
// ReviewSidePanel —— 审阅取证右滑栏（ReviewPanel 的继任者）。
//
// 职责：改动（DiffView + 基准下拉）/ 跑命令 / 读文件 三个子 tab；栏内自滚。
// 边界：
//   - 全部只读取证，不改任务状态（与 ReviewPanel 相同）
//   - 分支接口失败退化为仅「自动推导」，diff 不受影响（spec §6.2）
//   - 何时可见由 TuiTab 决定（waiting_review），本组件不判状态机
import { useCallback, useEffect, useState } from 'react'
import type { BranchesResult, RunResult } from '../../api/types'
import { fetchTaskBranches, fetchTaskDiff, fetchTaskFile, runTaskCommand } from '../../api/client'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { errorMessage } from '../lib/format'
import { cn } from '@/lib/utils'
import { DiffView } from './DiffView'

type ReviewTab = 'diff' | 'run' | 'file'

// ReviewSidePanel 渲染审阅栏。onClose 由页头的开关与栏内 ✕ 共用。
export function ReviewSidePanel({ taskId, onClose }: { taskId: string; onClose: () => void }) {
  const [tab, setTab] = useState<ReviewTab>('diff')
  return (
    <aside className="flex h-full min-h-0 w-[44%] min-w-[400px] max-w-[620px] flex-col border-l bg-background">
      <div className="flex items-center gap-1.5 border-b px-3 py-2">
        {(['diff', 'run', 'file'] as const).map((t) => (
          <button
            key={t}
            type="button"
            onClick={() => setTab(t)}
            className={cn(
              'rounded-md px-3 py-1 text-xs',
              tab === t ? 'bg-muted font-medium' : 'text-muted-foreground hover:bg-muted/50',
            )}
          >
            {t === 'diff' ? '改动' : t === 'run' ? '跑命令' : '读文件'}
          </button>
        ))}
        <button type="button" onClick={onClose} title="收起" className="ml-auto px-1 text-muted-foreground hover:text-foreground">✕</button>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto p-3">
        {tab === 'diff' && <DiffSection taskId={taskId} />}
        {tab === 'run' && <RunSection taskId={taskId} />}
        {tab === 'file' && <FileSection taskId={taskId} />}
      </div>
    </aside>
  )
}

// DiffSection：基准下拉（fetchTaskBranches，失败退化）+ DiffView。
function DiffSection({ taskId }: { taskId: string }) {
  const [branches, setBranches] = useState<BranchesResult | null>(null)
  const [base, setBase] = useState('')
  const [diff, setDiff] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async (b: string) => {
    setLoading(true)
    setError(null)
    try {
      const r = await fetchTaskDiff(taskId, b || undefined)
      setDiff(r.diff)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setLoading(false)
    }
  }, [taskId])

  // 进栏自动加载默认基准的 diff；分支列表失败不拦 diff（退化下拉）
  useEffect(() => {
    setDiff(null)
    setBase('')
    void load('')
    fetchTaskBranches(taskId)
      .then(setBranches)
      .catch(() => setBranches(null))
  }, [taskId, load])

  return (
    <div className="flex flex-col gap-2">
      <label className="flex items-center gap-2 text-xs text-muted-foreground">
        基准
        <select
          className="flex-1 rounded-md border border-input bg-background px-2 py-1.5 font-mono text-xs"
          value={base}
          onChange={(e) => { setBase(e.target.value); void load(e.target.value) }}
        >
          <option value="">自动推导{branches?.default ? `（${branches.default}）` : ''}</option>
          {branches?.branches.map((b) => <option key={b} value={b}>{b}</option>)}
        </select>
        {loading && <span>加载中…</span>}
      </label>
      {error && <p role="alert" className="break-words text-sm text-destructive">{error}</p>}
      {diff !== null && <DiffView text={diff} />}
    </div>
  )
}

// RunSection / FileSection：逻辑自 ReviewPanel 原样平移（输入 + 请求 + 退出码
// 徽章/错误透出），仅类名对齐新栏样式——不改任何请求语义。
```

（RunSection/FileSection 的完整函数体从 `ReviewPanel.tsx` L126-228 复制过来，input 类名沿用其 `inputCls`。）

- [ ] **Step 5: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/task/ReviewSidePanel.test.tsx`
Expected: PASS

- [ ] **Step 6: 自检注释**：DiffView「回退不吞内容」、ReviewSidePanel「分支退化」「不判状态机」的 why 都在文件头/关键行；错误路径全部 UI 透出（errorMessage），无静默失败。

- [ ] **Step 7: Commit**

```bash
git add web/src/app/task/DiffView.tsx web/src/app/task/ReviewSidePanel.tsx web/src/app/task/ReviewSidePanel.test.tsx
git commit -m "feat(web): 审阅右滑栏——diff 按文件着色分组与基准分支下拉"
```

---

### Task 9: Composer——对话式收口

**Files:**
- Create: `web/src/app/task/Composer.tsx`
- Create: `web/src/app/task/Composer.test.tsx`

**Interfaces:**
- Consumes: `continueTask`/`doneTask`/`stopTask`/`resumeTask`（现有 client）、`ConfirmDialog`（lib）、`errorMessage`（lib/format）、`Task`（types）
- Produces（Task 10 消费）:

```ts
export function Composer(props: { task: Task; disabled: boolean; onChanged: () => void }): JSX.Element
```

行为契约（AdvanceActions 的状态机对齐视图不变）：continue/done 仅 waiting_review；stop 非终态可用；resume + force 仅 waiting_answer；done/stop 二次确认；断线禁用但保留已填内容；Enter 发送 / Shift+Enter 换行。

- [ ] **Step 1: 写失败测试**——`web/src/app/task/Composer.test.tsx`：

```tsx
// Composer.test.tsx —— 对话式收口的状态联动与动作契约。
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { Composer } from './Composer'
import type { Task } from '../../api/types'

vi.mock('../../api/client', () => ({
  continueTask: vi.fn().mockResolvedValue({ ok: true }),
  doneTask: vi.fn().mockResolvedValue({ ok: true }),
  stopTask: vi.fn().mockResolvedValue({ worktree_removed: true }),
  resumeTask: vi.fn().mockResolvedValue({ forced: false, note: '' }),
}))
import { continueTask } from '../../api/client'

const task = (state: string) => ({ id: 't1', state } as Task)

describe('Composer', () => {
  beforeEach(() => vi.clearAllMocks())

  it('waiting_review：可输入，Enter 发送 continue，发送后清空', async () => {
    render(<Composer task={task('waiting_review')} disabled={false} onChanged={() => {}} />)
    const box = screen.getByRole('textbox')
    fireEvent.change(box, { target: { value: '补测试' } })
    fireEvent.keyDown(box, { key: 'Enter' })
    await waitFor(() => expect(continueTask).toHaveBeenCalledWith('t1', '补测试'))
    expect((box as HTMLTextAreaElement).value).toBe('')
  })

  it('running：输入禁用并说明原因；停止仍可用', () => {
    render(<Composer task={task('running')} disabled={false} onChanged={() => {}} />)
    expect(screen.getByRole('textbox')).toBeDisabled()
    expect(screen.getByText(/回合结束/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /停止任务/ })).toBeEnabled()
    expect(screen.queryByRole('button', { name: /完成任务/ })).not.toBeInTheDocument()
  })

  it('done 需二次确认才调接口', async () => {
    render(<Composer task={task('waiting_review')} disabled={false} onChanged={() => {}} />)
    fireEvent.click(screen.getByRole('button', { name: /完成任务/ }))
    expect(screen.getByText(/不可撤销/)).toBeInTheDocument()
  })

  it('waiting_answer：显示恢复执行与强制收口选项', () => {
    render(<Composer task={task('waiting_answer')} disabled={false} onChanged={() => {}} />)
    expect(screen.getByRole('button', { name: /恢复执行/ })).toBeInTheDocument()
    expect(screen.getByText(/强制收口/)).toBeInTheDocument()
  })

  it('终态：只读说明，无任何动作', () => {
    render(<Composer task={task('completed')} disabled={false} onChanged={() => {}} />)
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /停止任务/ })).not.toBeInTheDocument()
    expect(screen.getByText(/已归档|已终结|终态/)).toBeInTheDocument()
  })

  it('断线：全部禁用但保留已填内容', () => {
    render(<Composer task={task('waiting_review')} disabled onChanged={() => {}} />)
    const box = screen.getByRole('textbox')
    fireEvent.change(box, { target: { value: '还没发的话' } })
    expect(screen.getByRole('button', { name: /续发修改/ })).toBeDisabled()
    expect((box as HTMLTextAreaElement).value).toBe('还没发的话')
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/task/Composer.test.tsx`
Expected: FAIL

- [ ] **Step 3: 实现**——`web/src/app/task/Composer.tsx`（动作执行骨架 runAction、ConfirmDialog 两个弹窗、resume/force 逻辑自 AdvanceActions **平移**，措辞与文案不变）：

```tsx
// Composer —— 任务推进的对话式收口（AdvanceActions 的继任者）。
//
// 可用性按状态机（proto.transitTable 对齐视图，与 AdvanceActions 相同）：
//   - 输入框 + 续发修改（continue）/ 完成任务（done）：仅 waiting_review
//   - 停止任务：非终态可用，弱化为红字按钮（不可逆，二次确认）
//   - 恢复执行 + 强制收口（resume/force）：仅 waiting_answer
//   - 终态：只读说明；断线：禁用但保留已填内容
//
// 交互：Enter 发送，Shift+Enter 换行——「对话」的形态判据之一（spec §2.4）。
import { useState } from 'react'
import type { Task } from '../../api/types'
import { continueTask, doneTask, resumeTask, stopTask } from '../../api/client'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '../lib/ConfirmDialog'
import { errorMessage } from '../lib/format'

type Busy = 'continue' | 'done' | 'stop' | 'resume' | null

// isTerminal 判断任务是否已到终态（completed / failed）。
function isTerminal(state: string): boolean {
  return state === 'completed' || state === 'failed'
}

// HINTS 是各状态下 composer 上方的提示语（人话说明当前能做什么）。
const HINTS: Record<string, string> = {
  running: '任务运行中——回合结束进入待审后才能下指令；停止任务随时可用。',
  waiting_review: '这一轮已干完，等你裁决——下修改指令让它继续，或完成归档。',
  waiting_answer: '任务在等一张工单的应答——裁决入口在左栏底部的工单面板。',
}

// Composer 渲染推进区。disabled=断线；onChanged 在任何动作成功后回调刷新。
export function Composer({ task, disabled, onChanged }: { task: Task; disabled: boolean; onChanged: () => void }) {
  const [instructions, setInstructions] = useState('')
  const [force, setForce] = useState(false)
  const [busy, setBusy] = useState<Busy>(null)
  const [message, setMessage] = useState<{ text: string; kind: 'info' | 'error' } | null>(null)
  const [confirming, setConfirming] = useState<'done' | 'stop' | null>(null)

  const canReview = task.state === 'waiting_review'
  const canStop = !isTerminal(task.state)
  const canResume = task.state === 'waiting_answer'
  const blocked = disabled || busy !== null

  const runAction = async (op: NonNullable<Busy>, action: () => Promise<void>) => {
    setBusy(op)
    setMessage(null)
    try {
      await action()
      onChanged()
    } catch (err) {
      setMessage({ text: errorMessage(err), kind: 'error' })
    } finally {
      setBusy(null)
    }
  }

  const send = () => {
    if (instructions.trim() === '' || blocked || !canReview) return
    void runAction('continue', async () => {
      await continueTask(task.id, instructions.trim())
      setMessage({ text: '已续发指令，任务回到 running。', kind: 'info' })
      setInstructions('')
    })
  }

  if (isTerminal(task.state)) {
    return (
      <div className="border-t px-3.5 py-2.5">
        <p className="mx-auto max-w-[760px] text-xs text-muted-foreground">
          任务已{task.state === 'completed' ? '归档（completed）' : '终结（failed）'}
          {task.done_note ? `——完成说明：${task.done_note}` : '，没有可用的推进动作。'}
        </p>
      </div>
    )
  }

  return (
    <div className="border-t px-3.5 py-2.5">
      <div className="mx-auto max-w-[760px]">
        <p className="mb-1.5 text-xs text-muted-foreground">
          {disabled ? '已断开，推进动作已禁用（保留已填内容）' : (HINTS[task.state] ?? '')}
        </p>
        {message && (
          <p role="alert" className={`mb-1.5 break-words text-sm ${message.kind === 'error' ? 'text-destructive' : 'text-foreground/80'}`}>
            {message.text}
          </p>
        )}

        {canResume && (
          <div className="mb-1.5 flex flex-wrap items-center gap-3 text-xs">
            <label className="flex items-center gap-1.5">
              <input type="checkbox" className="size-3.5" checked={force} onChange={(e) => setForce(e.target.checked)} />
              强制收口（绕过对账，直接推到 Review；会留下「人工强制」事件）
            </label>
            <Button
              size="sm" variant="outline" disabled={blocked}
              onClick={() => void runAction('resume', async () => {
                const rep = await resumeTask(task.id, force)
                setMessage({ text: rep.note || (rep.forced ? '已强制收口。' : '已恢复执行。'), kind: 'info' })
              })}
            >
              {busy === 'resume' ? '恢复中…' : '恢复执行'}
            </Button>
          </div>
        )}

        <div className="flex flex-col gap-1.5 rounded-xl border bg-background p-2 focus-within:border-muted-foreground/50">
          <textarea
            value={instructions}
            onChange={(e) => setInstructions(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send() }
            }}
            rows={2}
            disabled={blocked || !canReview}
            className="resize-none bg-transparent text-sm leading-relaxed outline-none disabled:opacity-60"
            placeholder={canReview ? '下修改指令，回给模型让它继续改…（Enter 发送，Shift+Enter 换行）' : '任务运行中，暂不能下指令'}
          />
          <div className="flex items-center gap-2">
            {canStop && (
              <button type="button" disabled={blocked} onClick={() => setConfirming('stop')} className="px-1 text-xs text-destructive hover:underline disabled:opacity-50">
                停止任务
              </button>
            )}
            <span className="flex-1" />
            {canReview && (
              <>
                <Button size="sm" variant="outline" disabled={blocked} onClick={() => setConfirming('done')}>
                  ✓ 完成任务
                </Button>
                <Button size="sm" disabled={blocked || instructions.trim() === ''} onClick={send}>
                  {busy === 'continue' ? '提交中…' : '↑ 续发修改'}
                </Button>
              </>
            )}
          </div>
        </div>
      </div>

      <ConfirmDialog
        open={confirming === 'done'}
        title="完成任务？"
        description="任务将被置为 completed 并回收执行器。worktree 由 agentd 管理时会被删除。此操作不可撤销。"
        confirmLabel="完成任务"
        busy={busy === 'done'}
        onConfirm={() => {
          setConfirming(null)
          void runAction('done', async () => {
            await doneTask(task.id)
            setMessage({ text: '任务已归档为 completed。', kind: 'info' })
          })
        }}
        onCancel={() => setConfirming(null)}
      />
      <ConfirmDialog
        open={confirming === 'stop'}
        title="停止任务？"
        description="将停止执行器、作废全部挂起工单，并把任务置为 failed。此操作不可撤销。"
        confirmLabel="停止任务"
        destructive
        busy={busy === 'stop'}
        onConfirm={() => {
          setConfirming(null)
          void runAction('stop', async () => {
            const r = await stopTask(task.id)
            setMessage({
              text: r.worktree_removed ? '已停止；agentd 创建的工作树已删除。' : '已停止（工作树保留：用户自带工作树 / 原地模式，或清理失败）。',
              kind: 'info',
            })
          })
        }}
        onCancel={() => setConfirming(null)}
      />
    </div>
  )
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/task/Composer.test.tsx`
Expected: PASS

- [ ] **Step 5: 自检注释**：文件头写清状态机对齐视图与 Enter 语义；错误全部 UI 透出（runAction 骨架保留）；无静默成功（每个动作成功都 setMessage）。

- [ ] **Step 6: Commit**

```bash
git add web/src/app/task/Composer.tsx web/src/app/task/Composer.test.tsx
git commit -m "feat(web): 对话式 composer——Enter 续发，完成/停止收进工具条"
```

---

### Task 10: DebugDrawer + TuiTab 总装 + 删旧件 + 全量回归

**Files:**
- Create: `web/src/app/task/DebugDrawer.tsx` / `DebugDrawer.test.tsx`
- Modify: `web/src/app/workbench/TuiTab.tsx`（整文件重写）
- Modify: `web/src/app/workbench/WorkbenchPage.test.tsx`、`web/src/app/workbench/TuiTab` 相关既有断言（若有）
- Delete: `web/src/app/task/TimelinePanel.tsx`、`TimelinePanel.test.tsx`、`EventsPanel.tsx`、`EventMark.tsx`、`ReviewPanel.tsx`、`AdvanceActions.tsx`

**Interfaces:**
- Consumes: Task 5-9 全部产物 + `useTaskSession`/`useFramesStream`（现有）+ `RenderPanel`（现有，移入抽屉）+ `buildBlocks`/`turnsOf`（frames.ts）
- Produces: `TuiTab({ taskId })` 对外签名不变（Shell 零改动）

- [ ] **Step 1: 写失败测试**——`web/src/app/task/DebugDrawer.test.tsx`：

```tsx
// DebugDrawer.test.tsx —— 调试抽屉：原始事件列表 + 原始正文两个子 tab。
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { DebugDrawer } from './DebugDrawer'
import type { Event } from '../../api/types'

// RenderPanel 挂常驻流，测试里 mock 掉（按需连接语义由 RenderPanel 自己的实现保证）
vi.mock('./RenderPanel', () => ({ RenderPanel: () => <p>render-log-stream</p> }))

const events = [
  { seq: 23, type: 'progress', created_at: '2026-08-17T11:16:46Z', payload: { text: '会话就绪' } },
] as unknown as Event[]

describe('DebugDrawer', () => {
  it('默认展示原始事件（#seq/type/摘要）', () => {
    render(<DebugDrawer taskId="t1" events={events} status="open" error={null} onClose={() => {}} />)
    expect(screen.getByText(/#23/)).toBeInTheDocument()
    expect(screen.getByText('progress')).toBeInTheDocument()
    expect(screen.getByText('会话就绪')).toBeInTheDocument()
  })
  it('切到原始正文才挂 RenderPanel（按需连接）', () => {
    render(<DebugDrawer taskId="t1" events={events} status="open" error={null} onClose={() => {}} />)
    expect(screen.queryByText('render-log-stream')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /原始正文/ }))
    expect(screen.getByText('render-log-stream')).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/task/DebugDrawer.test.tsx`
Expected: FAIL

- [ ] **Step 3: 实现 DebugDrawer.tsx**（原始事件列表的行渲染与 `eventSummary` 自 EventsPanel **平移**，含封顶丢旧与「payload 是截断摘要」的纪律注释）：

```tsx
// DebugDrawer —— 调试抽屉：原始事件流 + 原始正文（render.log）。
//
// 职责：帧渲染出问题时，用原始数据区分「渲染错了」还是「采集错了」（W4b 保留
// 原始视图的同一条理由，收进抽屉后日常不可见）。
// 边界：
//   - 事件 payload 是截断摘要，全文只在工单面板（EventsPanel 的既有纪律）
//   - 原始正文按需连接：切到该 tab 才挂 RenderPanel，关闭即卸载（不留常驻流）
//   - 列表封顶丢最旧（maxShownEvents，沿 EventsPanel）
import { useState } from 'react'
import type { Event } from '../../api/types'
import type { WsStatus } from '../../api/ws'
import { Badge } from '@/components/ui/badge'
import { formatFull } from '../lib/format'
import { RenderPanel } from './RenderPanel'
import { cn } from '@/lib/utils'

const maxShownEvents = 200

// eventSummary 从事件 payload 提取一行可读简览（自 EventsPanel 平移）。
function eventSummary(ev: Event): string {
  const p = ev.payload
  if (p !== null && typeof p === 'object') {
    const obj = p as Record<string, unknown>
    for (const key of ['question', 'permission', 'text', 'reason', 'hint', 'fail_reason', 'note']) {
      const v = obj[key]
      if (typeof v === 'string' && v !== '') return v
    }
    const s = JSON.stringify(obj)
    return s.length > 120 ? `${s.slice(0, 120)}…` : s
  }
  return String(p ?? '')
}

// DebugDrawer 渲染为右侧覆盖层（fixed）；onClose 关闭。
export function DebugDrawer({ taskId, events, status, error, onClose }: {
  taskId: string
  events: Event[]
  status: WsStatus
  error: string | null
  onClose: () => void
}) {
  const [tab, setTab] = useState<'events' | 'render'>('events')
  return (
    <div className="fixed inset-0 z-40 flex justify-end bg-black/30" onClick={onClose}>
      <div className="flex h-full w-[560px] max-w-[92vw] flex-col bg-background shadow-xl" onClick={(e) => e.stopPropagation()}>
        <div className="flex items-center gap-2 border-b px-3 py-2">
          <span className="text-sm font-medium">调试数据</span>
          <span className="text-xs text-muted-foreground">区分「渲染错了」还是「采集错了」</span>
          <button type="button" onClick={onClose} className="ml-auto px-1 text-muted-foreground hover:text-foreground">✕</button>
        </div>
        <div className="flex items-center gap-1.5 border-b px-3 py-1.5">
          <button type="button" onClick={() => setTab('events')} className={cn('rounded-md px-2.5 py-1 text-xs', tab === 'events' ? 'bg-muted font-medium' : 'text-muted-foreground')}>
            原始事件（{events.length} 条）
          </button>
          <button type="button" onClick={() => setTab('render')} className={cn('rounded-md px-2.5 py-1 text-xs', tab === 'render' ? 'bg-muted font-medium' : 'text-muted-foreground')}>
            原始正文
          </button>
          {tab === 'events' && <Badge variant={status === 'open' ? 'default' : status === 'connecting' ? 'secondary' : 'destructive'} className="ml-auto">{status}</Badge>}
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto p-3">
          {tab === 'events' ? (
            <>
              {error && <p role="alert" className="mb-2 break-words text-sm text-destructive">{error}（将自动重连；事件全部落库，重连后可凭游标补拉）</p>}
              {events.length === 0 ? (
                <p className="text-sm text-muted-foreground">还没有事件。</p>
              ) : (
                <ul className="flex flex-col gap-1 font-mono text-xs">
                  {events.slice(-maxShownEvents).map((ev) => (
                    <li key={ev.seq} className="border-b border-border/60 py-1 last:border-b-0">
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="text-muted-foreground">#{ev.seq}</span>
                        <span>{ev.type}</span>
                        <span className="ml-auto text-muted-foreground">{formatFull(ev.created_at)}</span>
                      </div>
                      <p className="break-words text-foreground/80">{eventSummary(ev)}</p>
                    </li>
                  ))}
                </ul>
              )}
            </>
          ) : (
            // 切进来才挂流；RenderPanel 自带卸载即断连（AbortController）
            <RenderPanel taskId={taskId} />
          )}
        </div>
      </div>
    </div>
  )
}
```

- [ ] **Step 4: 重写 TuiTab.tsx（总装）**：

```tsx
// TuiTab —— 桌面端 TUI（spec 2026-08-17 对话式重构）。
//
// 职责：总装页头（TuiHeader）/ 主区（ConversationStream + ReviewSidePanel）/
// Composer / DebugDrawer；持有 frames 流（页头回合下拉与会话流共享）与
// 审阅栏开合状态。
//
// 状态联动（spec §5）：
//   - waiting_review 进入时审阅栏自动滑出；人手动收起后本 tab 内记住不再自动开
//   - running / waiting_answer / 终态：审阅栏隐藏
// 边界（沿旧 TuiTab）：不含 TicketsPanel（全局工单弹层）；不含面包屑；
// 会话数据全部经 useTaskSession / useFramesStream。
import { useEffect, useMemo, useRef, useState } from 'react'
import { DisconnectedBanner, LoadFailed, SessionExpiredBanner } from '../lib/Banners'
import { useTaskSession } from '../task/useTaskSession'
import { useFramesStream } from '../task/useFramesStream'
import { buildBlocks, turnsOf } from '../task/frames'
import { TuiHeader } from '../task/TuiHeader'
import { ConversationStream } from '../task/ConversationStream'
import { ReviewSidePanel } from '../task/ReviewSidePanel'
import { Composer } from '../task/Composer'
import { DebugDrawer } from '../task/DebugDrawer'

export function TuiTab({ taskId }: { taskId: string }) {
  const s = useTaskSession(taskId)
  const { frames, badLines, startOffset, error, atCap, loadingEarlier, loadEarlier, retry } =
    useFramesStream(taskId)
  const blocks = useMemo(() => buildBlocks(frames), [frames])
  const turns = useMemo(() => turnsOf(frames), [frames])

  const [reviewOpen, setReviewOpen] = useState(false)
  const [debugOpen, setDebugOpen] = useState(false)
  // reviewDismissed 记「人手动收起过」：waiting_review 里自动开一次，人收起后
  // 不再抢开；离开 review 态重置，下次进入再自动开
  const reviewDismissed = useRef(false)

  const state = s.detail?.task.state
  useEffect(() => {
    if (state === 'waiting_review') {
      if (!reviewDismissed.current) setReviewOpen(true)
    } else {
      setReviewOpen(false)
      reviewDismissed.current = false
    }
  }, [state])

  if (s.detail === null) {
    if (s.loadError) return <LoadFailed message={s.loadError} onRetry={s.refresh} />
    if (s.sessionExpired) return <SessionExpiredBanner />
    return <p className="p-4 text-sm text-muted-foreground">正在加载任务…</p>
  }

  const inReview = s.detail.task.state === 'waiting_review'
  const closeReview = () => { setReviewOpen(false); reviewDismissed.current = true }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <TuiHeader
        task={s.detail.task}
        turns={turns}
        turnsPartial={startOffset > 0}
        onJumpTurn={(t) => document.getElementById(`turn-${taskId}-${t}`)?.scrollIntoView({ block: 'start' })}
        reviewAvailable={inReview}
        reviewOpen={reviewOpen}
        onToggleReview={() => (reviewOpen ? closeReview() : setReviewOpen(true))}
        onOpenDebug={() => setDebugOpen(true)}
        wsStatus={s.wsStatus}
        disconnected={s.disconnected}
      />

      {s.sessionExpired && <SessionExpiredBanner />}
      {s.disconnected && !s.sessionExpired && <DisconnectedBanner message={s.disconnectReason} />}

      <div className="flex min-h-0 flex-1">
        <div className="min-w-0 flex-1">
          <ConversationStream
            taskId={taskId}
            taskState={s.detail.task.state}
            blocks={blocks}
            badLines={badLines}
            startOffset={startOffset}
            atCap={atCap}
            error={error}
            loadingEarlier={loadingEarlier}
            onLoadEarlier={loadEarlier}
            onRetry={retry}
          />
        </div>
        {inReview && reviewOpen && <ReviewSidePanel taskId={taskId} onClose={closeReview} />}
      </div>

      <Composer task={s.detail.task} disabled={s.disconnected} onChanged={s.refresh} />

      {debugOpen && (
        <DebugDrawer taskId={taskId} events={s.events} status={s.wsStatus} error={s.wsError} onClose={() => setDebugOpen(false)} />
      )}
    </div>
  )
}
```

（注意：`useFramesStream` 原签名是 `useFramesStream(raw ? undefined : taskId)`——总装后不再有 raw 切换，恒传 `taskId`；hook 本身不动。）

- [ ] **Step 5: 删除旧件**：

```bash
git rm web/src/app/task/TimelinePanel.tsx web/src/app/task/TimelinePanel.test.tsx web/src/app/task/EventsPanel.tsx web/src/app/task/EventMark.tsx web/src/app/task/ReviewPanel.tsx web/src/app/task/AdvanceActions.tsx
```

随后 `cd web && npm run typecheck`，凡引用旧件的残留 import（TuiTab 已重写；blocks.test.tsx 若还测 EventMark 则删那段断言）逐一清掉。`TicketsPanel` 与其测试**不动**（不在 TUI tab 范围）。

- [ ] **Step 6: 状态联动手测断言写进测试**——`WorkbenchPage.test.tsx` 或既有 TuiTab 相关测试若断言了「回合时间线/事件流」等旧文案，更新为新结构断言（页头任务名 + composer 存在）。

- [ ] **Step 7: 全量回归**

Run: `cd web && npm run typecheck && npm run lint && npm test && cd .. && go test ./...`
Expected: 全绿；lint 无未用 import

- [ ] **Step 8: 自检日志与注释**（instrumenting-code 终审清单）：TuiTab/DebugDrawer 文件头齐；reviewDismissed 的 why 注释在；前端无 console.log；后端两处新日志（Task 1/2）已验。

- [ ] **Step 9: Commit**

```bash
git add -A web/src/app/
git commit -m "feat(web): TUI tab 对话式总装——会话流主角、审阅右滑、调试抽屉，删除四个旧面板"
```

---

### Task 11: 真机走查 + 原型对照验收

**Files:**
- Modify: `prototypes/base/README.md`（走查通过后不改状态——「已确认」由本 task 的对照通过驱动，见 Step 3）

- [ ] **Step 1: 起 dev server 走查**——按项目惯例起 web dev server 与本机 agentd（若 SuperDev 已接管服务用 SuperDev 起），打开一个真实任务的 TUI tab。

- [ ] **Step 2: 对照原型逐项核对**（验收基准 = `prototypes/tui-redesign/index.html`，spec §9）：
  - [ ] 整 tab 只有会话流一个滚动区；正文限宽居中
  - [ ] 页头两行：身份 + 动作 / executor·模型·回合下拉·ctx 小表（点开两口径账目）
  - [ ] 思维链/工具/事件行是统一元数据行；表面只有气泡/正文/交付卡
  - [ ] send 回合显示审核者气泡（新派发的任务；旧任务旧帧只有分隔线，属预期）
  - [ ] waiting_review：审阅栏自动滑出；diff 按文件分组着色；基准下拉列出分支
  - [ ] composer：Enter 续发；完成/停止二次确认；running 态禁用有说明
  - [ ] 调试抽屉：原始事件与原始正文都在，关闭抽屉流断开
- [ ] **Step 3: 通过后把 `prototypes/base/README.md` 对应行改为「已确认」并 commit**：

```bash
git add prototypes/base/README.md
git commit -m "docs(prototypes): TUI 对话式重构真机对照通过，转已确认"
```

- [ ] **Step 4: 收尾**——调 `finishing-a-development-branch`（含「原型改动回流 base」提示：把 tui-redesign 副本的 TUI 形态并进 `prototypes/base/index.html`）。

---

## Self-Review 记录

- **Spec 覆盖**：§2.1 页头→Task 7；§2.2 会话流→Task 5/6；§2.3 审阅栏→Task 2/3/8；§2.4 composer→Task 9；§2.5 调试抽屉→Task 10；§4 纯函数→Task 3/4；§5 状态联动→Task 9/10；§6.1→Task 1；§6.2→Task 2；§8 测试→各 task 内嵌；§9 验收→Task 11。无缺口。
- **占位扫描**：无 TBD；Task 8 RunSection/FileSection 与 Task 9 弹窗为「平移自现有文件 + 明确行号/位置」，源码在仓库内，不属占位。
- **类型一致性**：`instructions`（Frame/turn 块/UserInstructionBlock）、`BranchesResult`、`ConversationStreamProps`、`TuiHeaderProps`、锚点 id `turn-${taskId}-${turn}` 已跨任务核对一致。
