# W4 剩余工作交接（可并行线）

写给**另一个会话**。你要接的是 W4「工作区资源」这一期里，**除 PTY 终端之外**
还剩下的活。PTY 终端已经在另一条线上开工了，本文第 2 节讲清楚它锁了哪些文件，
你绕开就行。

---

## 1. 基于哪个分支

```bash
cd /Users/xushixin/workspace/handoff/.claude/worktrees/w4-delivery
git merge-base --is-ancestor 850ae61a HEAD && echo ok   # 必须打印 ok
git switch -c feat/w4-console-polish   # 或按活分别开分支，名字自己定
```

- **基线：`w4-delivery`**（已推 origin）。分支尖端是这份交接文档本身
  （`239f732e`）；PTY 那条线切在它前一个提交 `850ae61a` 上，两者只差一个
  纯文档提交，合并时不构成冲突。
- `w4-delivery` 是 W1–W4 的集成分支，**尚未合回 main**，129 个提交在它上面。
- **不要 `git merge main`。** 它现在落后 main 26 个提交，但 PTY 那条线的
  merge-base 就是 `850ae61a`；你保持同一个 merge-base，两条线回来时才是两个
  干净的合并。追 main 是集成分支持有人的事，不是你的。
- **不要推 main，不要合回 main。** 这条分支要到整块 W4 做完、验过，才谈合并。
- 你自己的分支可以 push（`git push -u origin feat/w4-console-polish`）。

---

## 2. 另一条线在动什么（这是你的避让清单）

任务 `39dbdd6f-570b-49bb-9996-1f1aa8609c12` 已派到 devbox（opencode 执行，
分支 `feat/w4-pty-terminal`，同样基于 `850ae61a`），做的是
[W4 PTY 终端实现计划](../plans/2026-08-12-w4-pty-terminal.md) 的 Task 1–16。

**它会改的文件**（照抄计划的 File Structure，你尽量别碰）：

| 层 | 文件 |
|---|---|
| 新包 | `internal/ptyhost/*` |
| Go 契约 | `internal/proto/pty.go`（新）、`internal/proto/status.go`、`internal/proto/projects.go` |
| 后端 | `internal/agentd/pty_api.go`（新）、`pty_ws.go`（新）、`forward_ws.go`（新）、`server.go` 路由段、`workspacefiles.go` 的**文件头注释** |
| 其它 Go | `internal/config/config.go`、`internal/prochost/footprint.go`、`cmd/status.go`、`cmd/footprint.go` |
| 前端 API | `web/src/api/client.ts`、`web/src/api/types.ts`、`web/src/api/pty.ts`（新） |
| 工作台 | `web/src/app/workbench/{tabs.ts,useWorkbench.ts,WorkbenchPage.tsx,BlankTab.tsx,TerminalTab.tsx,usePtyRestore.ts}` |
| 外框 | `web/src/app/shell/Shell.tsx`、`web/src/app/data/usePtySupport.ts`（新） |

三条尤其要记住：

1. **`WorkbenchPage.tsx` 的 `renderContent` 签名它在改**（从 `(content, base)`
   扩到 `(content, base, group, tabId)`），还给它加了 `onBeforeClose` 与
   `terminalUnavailable` 两个 prop。你**不要**再动这个签名——要往中央区传东西，
   等它合了再说，或者走别的入口。
2. **`Shell.tsx` 的 `<aside>`（左栏，:87-95）它也会碰**——横幅那一段要多插一条
   「终端会话恢复失败」。B74 的搜索框正好也在这几行。同文件相邻行，预期要手工
   合一次，不是灾难，但别以为能自动合。
3. **`internal/proto/` 与 `web/src/api/types.ts` 是纯追加**（各加各的类型），
   冲突概率低；但契约 fixture（`web/src/api/testdata/*.json`）是 `-update`
   生成的，两边都重生成过就会撞。你这边如果动了契约，合并后**重跑一次**
   `go test ./internal/proto/ -run TestContractFixtures -update` 再核对 diff。

---

## 3. 现在就能开工的活（推荐顺序）

四条都还没有 spec。按 `product-backlog` 的规矩：`💡 idea` 不能直接进
`writing-plans`，必须先 `brainstorming` 出 spec，backlog 行转 `📋 specced`，
领取后转 `🔨 doing`。**不要跳过这一步直接开写。**

### P1 · B75 看板干预态橙色标记（推荐先做，零冲突）

- **backlog**：B75，`💡 idea`，中优先级。行里已经把根因和「为什么不能就地修」
  写清楚了，brainstorm 可以直接从那儿起步。
- **是什么**：W4 spec §5.1 要求干预态是**卡片级**橙色标记（`● 等待审批` /
  `● 等待 Review`），计划整个漏了这条，验收项 11 判「部分通过」。顺带钉住一个
  既有缺陷：`app/board/BoardPage.tsx:148-178` 的 `TaskCard` 会在同一张卡上并排
  渲染两个一模一样的红徽章（`waitingAnswer` 那个和 `stateLabel('waiting_answer')`
  那个），做橙色的时候必须一起收掉。
- **要动**：`web/src/components/ui/badge.tsx`（现在只有
  `default|secondary|destructive|outline` 四档，要补橙色档）、
  `web/src/app/board/columns.ts` 的 `stateBadgeVariant`、`app/board/BoardPage.tsx`、
  `web/src/app/task/TaskHeader.tsx`（它也消费 `stateBadgeVariant`，改了两个页面
  一起变，这正是当初判「超出就地修口径」的理由）。
- **与 PTY 的冲突面**：**零**。这些文件 PTY 一个都不碰。
- **为什么排第一**：范围小、判据清楚、完全不用等谁。

### P2 · B74 左栏搜索框与「项目 N」小标题

- **backlog**：B74，`💡 idea`，低优先级。
- **是什么**：原型左栏顶部有 `搜索项目、机器或任务 ⌘K` 输入框，下面一行小标题
  `项目 5`（计数）；实现两样都没有。**这不是实现偏了**——W4 spec 从头到尾没收录
  它们，所以不算 spec §8 那五条「用户裁决过的偏离」，也不该就地糊上去。
- **brainstorm 必须先答的三个问题**（backlog 行里已列）：① 检索面是项目/机器/任务
  三层还是只到任务；② `⌘K` 是就地过滤左栏还是弹命令面板（原型只画了输入框，
  没画交互）；③「项目 N」这个计数在「未归属」分组存在时怎么算。
- **要动**：`web/src/app/tree/ProjectTree.tsx`、`web/src/app/shell/Shell.tsx`
  的 `<aside>` 段。
- **与 PTY 的冲突面**：**低但非零**——`Shell.tsx` 的横幅区相邻。预期一次小的
  手工合并。

### P3 · W4c 文件写入与在线编辑

- **backlog**：**没有行**。这一条只活在 W4 spec §0 的「本轮不做」清单里。
  开工前先按 `product-backlog` 的记录入口给它建一行（下一个可用 ID 从 main 的
  B77 之后取，**别复用**），再 brainstorm。
- **是什么**：W4 总方案 §5 给 W4 划的范围是「文件 REST（浏览、读取、**编辑、
  冲突保护**）」。浏览与读取已经交付（`GET /api/workspaces/dir` /
  `GET /api/workspaces/file`，见 `internal/agentd/workspacefiles.go`），
  写入这一半没做，中央的文件 tab 现在是只读的。
- **brainstorm 要定的**：写入的白名单口径（能不能沿用 `resolveWorkspace`）、
  冲突保护用什么（mtime？内容哈希？）、要不要落自动保存、失败怎么呈现。
  **注意一个前提已经变了**：`workspacefiles.go` 文件头写的那套「控制台会话是
  比主令牌弱的凭据」的安全说辞**已被证伪**（见
  [PTY spec §1](../specs/2026-08-12-w4-pty-terminal-design.md)：控制台会话在能力上
  等价于主令牌，因为它能打到 `POST /api/tasks/{id}/run` → `sh -c`）。PTY 那条线
  的 Task 6 Step 5 会去更正那段注释。所以白名单是**参数校验，不是安全边界**，
  新端点的拒绝码应当是 400 而不是 403。brainstorm 时别再从那段旧说辞出发。
- **要动**：`internal/agentd/workspacefiles.go`（追加写入端点）、
  `internal/proto/`（追加请求响应体）、`web/src/api/{client,types}.ts`（追加）、
  `web/src/app/workbench/FileTab.tsx`。
- **与 PTY 的冲突面**：**中**。`workspacefiles.go` 它只改文件头注释；
  proto 与 api 两边都是追加。**红线：不要改 `WorkbenchPage.tsx` 的
  `renderContent` 签名**（见第 2 节）——`FileTab` 需要往上冒泡什么，先用
  已有的 `api.setContent` 想办法，实在不行就等 PTY 合了再接。

### P4 · 任务 TUI 的补全（模型名、context token 用量）

- **backlog**：**没有行**，同 P3，先建行。
- **是什么**：W4 spec §0 列的「本轮不做」之一——原型 TUI 顶栏有、handoff 现在
  没有的信息（模型名、context token 用量等）。
- **为什么排最后**：它要后端先出得来数——四家 adapter（claudecode / opencode /
  codex / grok）能不能报 context 用量、报的口径一不一致，全是未知。**brainstorm
  的第一步应该是探针**，不是设计。可能的结论是「有的 adapter 报不了，就如实缺席」，
  那也是个结论。
- **要动**：`internal/executor/*/`、`internal/proto/`、`web/src/app/task/*`。
- **与 PTY 的冲突面**：**低**（`app/task/` 与 `internal/executor/` PTY 都不碰），
  只有 proto 是共用的追加面。

---

## 4. 必须等 PTY 合并之后再做的

**终端 tab 相关的一切，最后做。** 这是本次交接的明确安排，别提前动。

- **以用户 home 为基准的文件浏览**（W4 spec §0「本轮不做」的最后一条）。
  它要动 `BlankTab.tsx` 的 home 分支与 `HOME_BASE` 的口径——而 PTY 的 Task 16
  正在改 `BlankTab.tsx`（终端不可用时**不渲染**该项）。两边改同一处判断，
  抢着改一定打架。而且它的前提（§2.6 那条安全边界）已被 PTY spec §1 证伪，
  要重新想，不是照着旧 spec 做。
- **终端 tab 的任何增强**：分屏内多终端、终端历史、字体设置……PTY spec §10
  有一张「本轮不做」表，那里面的条目要等 PTY 落地、真机走查完，看着真东西再谈。
- **PTY 真机走查（计划 Task 17）**：devbox 上那个执行者只会建走查记录骨架、
  每行标「未验」，因为它没有浏览器。真走查是集成分支持有人在本地做的事，
  不是你的活。

---

## 5. 协作纪律

- **提交粒度**：一个 task 一个提交，中文提交信息，跟着计划走。
- **日志与注释是一等公民**（全局 CLAUDE.md + `instrumenting-code`）：每个实现
  类 task 必须带「加关键节点日志」和「加意图注释」两个 step，计划里缺了就是
  plan failure。禁止 `fmt.Printf` / `console.log` 当日志。
- **红线**（沿用本项目一直在守的）：
  - 主令牌、ticket 明文、cookie 明文一律不得进日志；设备名与会话 id 可以。
  - 破坏性 / 不可逆 / 对外可见的操作——删数据、改写历史、改 CI/密钥/生产配置、
    往外部服务发布、装全局依赖——一律不自作主张，升级给用户。
- **平台拆分**一律 `_unix.go` + `_other.go`，不要写 `_windows.go`；仓库根的
  `windows_build_test.go` 会跑 `GOOS=windows go build ./...`，它必须绿。
- **config 新字段必须带 `omitempty`**，且默认值只在使用时解析、**绝不在 `Load`
  时填进结构体**——填了下一次 `Save` 就把默认值写进 `config.yaml`，而
  `yaml.KnownFields(true)` 会让旧版 agentd 直接起不来。
- **回归命令**（交活前必须全绿，红的说清楚是哪条、为什么）：

```bash
go build ./... && go test ./... && go vet ./... && gofmt -l .
```

```bash
cd web && npx vitest run && npm run typecheck && npm run lint && npm run build
```

---

## 6. 交回来的方式

做完一条（或几条）就停下来说明：改了什么、回归结果原文、backlog 行推到哪个状态、
有没有留残留。**不要自己合进 `w4-delivery`**——集成顺序由集成分支持有人排，
PTY 那条线回来的时间不确定，合的顺序会影响冲突量。

有拿不准的（尤其是「这个改动会不会撞上 PTY」），直接问，比合并时再发现便宜得多。
