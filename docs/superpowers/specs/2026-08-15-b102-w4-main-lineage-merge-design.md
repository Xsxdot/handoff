# B102 并线：把 w4 控制台线并回 main

**日期**：2026-08-15
**基线**：`main` @ `85f8e825d`（含 B92/B93/B99）与 `origin/w4-delivery` @ `84013dd79`（含 B90/B91/B94 与整个 `web/`）
**分叉点**：`e3bbea230`

> **本 spec 由审核者在用户外出期间独立撰写**，没有走 brainstorming 的逐问确认。
> 下面 §2 的方向选择与 §4 的撞号处置是两处**可推翻的判断**，已在各自小节里写明理由与代价，
> 用户回来后若不同意，改的是这两处，不影响其余部分。

---

## 1. 问题：不是「少个功能」，是两条线各有一半

用户走查报的是「左栏只有本机，没有远程开发机」。查下来根因与前端无关：

本机 agentd 的 `GET /api/projects/tree?scope=all` 会向每台 target 现场扇出
（`projectfanout.go` 的 `buildTreeAll`），mac-02 那一行返回的是：

```json
{"name":"mac-02","ok":false,"error":"项目树: 状态码 405: Method Not Allowed"}
```

**远程 agentd 没有这个端点。** 同时 `/api/machines` 是通的（两台都在、mac-02
`reachable:true`），所以「机器」页看得见远程机、左栏项目树看不见——差别就在这一个端点。

端点不存在的原因是两条线各带一半能力：

| | `main` | `w4-delivery` |
|---|---|---|
| 模块路径 | `github.com/Xsxdot/handoff` | `github.com/xushixin/handoff`（改名前） |
| 领先分叉点 | 239 个提交 | 177 个提交 |
| 独有能力 | B92/B93/B99 修复、release / install / selfupdate、`internal/proxycfg` | 控制台全套：`internal/agentd/` 下 18 个文件（`projecttree` `projectfanout` `machines` `pty_api` `pty_ws` `workspacefiles` `forward` `mirror` `auth` 等）、`internal/ptyhost`、整个 `web/` |

于是眼下必须在两件事里二选一：**远程机跑 main 才有 B92/B93/B99 的修复，跑 w4 才能被控制台看见。**
今天为了做 B92/B93/B99 的真机复验，mac-02 装的是 main 构建——所以左栏至今看不见它。
这个二选一不消除，B95/B96/B97、原型回流、以及任何「在远程机上用控制台」的需求都推不动。

## 2. 方向：把 w4 合进 main，不是反过来

**选 main 作宿主。** 理由三条，按分量排序：

1. **模块路径**。main 的 `github.com/Xsxdot/handoff` 是对外发布用的路径，`install.sh`、
   release workflow、`internal/selfupdate` 的自检全部钉在它上面。反向合并等于把已发布的
   模块路径退回改名前，外部安装路径当场断掉。
2. **w4 的改动以「加文件」为主**。18 个新 `internal/agentd/` 文件 + `internal/ptyhost` +
   `web/` 都是纯新增，天然适合被并入；而 main 的 239 个提交散布在既有文件里，反向合并要
   逐条重放。
3. **试合并的实测数据支持这个方向**（本地 `git merge --no-commit --no-ff w4-delivery` 到 main）：

   - **冲突文件 14 个、冲突块共 23 个**
   - `go.mod` **自动合成正确**（`module github.com/Xsxdot/handoff`）
   - 另有 **57 个文件仍 import `github.com/xushixin/handoff`**，需机械改名

冲突文件清单（这就是本次全部的人工判断面）：

| 文件 | 冲突块 | 预判性质 |
|---|---|---|
| `internal/agentd/server.go` | 4 | 路由表——两边的路由都要在 |
| `internal/config/config.go` | 3 | 配置项——main 的 proc fence 与 w4 的 pty/auth 都要留 |
| `internal/config/config_test.go` | 2 | 跟随 config.go |
| `internal/agentd/workspace.go` | 2 | — |
| `cmd/project.go` | 2 | — |
| `README.md` | 2 | 文档 |
| `internal/prochost/footprint.go` | 1 | **最需要小心**，见 §5 |
| `internal/prochost/footprint_test.go` | 1 | 同上 |
| `internal/client/client.go` | 1 | — |
| `internal/agentd/workspace_test.go` | 1 | — |
| `cmd/agentd.go` | 1 | — |
| `cmd/tasks.go` | 1 | — |
| `cmd/status_test.go` | 1 | — |
| `docs/superpowers/backlog.md` | 1 | 撞号，见 §4 |

**可推翻点**：如果用户其实打算废弃 main 的发布路径、以控制台线为准，那方向要反过来。
本 spec 按「main 是对外发布线」这一既成事实推进。

### 2.1 冲突解决的唯一口径

**两边的能力一个都不能丢。** 冲突不是「选一边」，默认是「都要」——路由表两边的路由都注册，
配置结构体两边的字段都保留，工具函数两边的分支都在。只有在两边**实现同一件事**时才取其一，
且取的那一边必须能通过**另一边的测试**。

测试是唯一裁判：合并后 `go test ./...` 同时跑两条线带进来的全部用例，谁被删了谁就红。

## 3. 交付物

一条 `integration/w4-main` 分支，从 `main` 起，合入 `origin/w4-delivery`，满足 §6 的验收。

**不合并进 `main`。** 按全局纪律，合并进主线是审核者的决定，executor 只交分支。

## 4. 撞号：加一列「线」，不重编号

两条线的 backlog 都用了 B80–B93 这段号，各指不同的事（例：w4 线 B90 是 home 浮窗终端，
main 线 B90 是另一件事）。`docs/superpowers/backlog.md` 的那一处冲突就是这个。

**处置：把两张表并成一张，每行保留原 ID，新增一列「线」标 `main` / `w4`，
并在表头上方写明「B80–B93 这段号两线各有一套，脱离「线」列不唯一；新条目从 B103 起。」**

**为什么不重编号**：ID 被 specs/、plans/、notes/、commit message、以及大量 backlog 备注
交叉引用，重编号要改的地方远超收益，且改漏一处就是一条指向错误条目的死链。加一列的代价是
「B90」这个词单独出现时永远有歧义——这个代价是真实的，但比批量改引用小一个数量级。

**可推翻点**：如果用户宁可承担一次性重编号的代价换取 ID 全局唯一，那就改这一节。

## 5. 风险

**`internal/prochost/footprint.go` 是最危险的一个冲突。** 这个包由 w4 线创建
（`e22827e63 feat(prochost)`），而 main 线的 B93 在它之上加了每任务点名所依赖的
`Footprint`。也就是说两边都动过同一段进程枚举逻辑，且 **B93 的 per-task 预算完全依赖它**。
解错这一处不会编译失败，只会让 B93 的两档点名静默失效——**必须靠 B93 的四条单测钉住**
（见 §6 第 5 条），不能只看编译通过。

**`internal/agentd/server.go` 的路由表漏注册一条就是一个 405。** 本次问题的起因恰恰就是
405，所以合并后必须做端点存在性冒烟（§6 第 6 条），而不是只看单测。

**`internal/config/config.go` 漏一个字段就是一条配置静默失效。** main 侧的
`proc_fence_task_budget` / `proc_fence_task_hard_limit` / `proc_fence_reserve_ratio` 与
w4 侧的 pty/auth 相关项都必须在合并后的结构体里，且 `config_test.go` 两边的用例都要过。

**w4 线的 `web/` 依赖 w4 线的 API 形状。** 合并后若 `internal/proto` 的某个结构体被 main
侧改过字段名，前端的 `api/types.ts` 会对不上。这不会被 Go 测试发现，只会被 `tsc -b` 发现——
所以前端三件套（`vitest` / `tsc -b` / `vite build`）是本次验收的**必需项**而不是加分项。

## 6. 验收

1. **零残留改名**：`grep -rn "github.com/xushixin/handoff" --include="*.go" .` 结果为空。
2. **Go 全绿**：`go build ./... && go vet ./... && go test -count=1 ./...`，0 FAIL。
3. **前端全绿**：`cd web && npm ci && npx vitest run && npx tsc -b && npx vite build`，0 error。
4. **w4 线的能力还在**：`internal/agentd/` 下 18 个 w4 独有文件全部存在，`internal/ptyhost`
   与 `web/` 完整。
5. **main 线的修复还在——点名验证这七条用例存在且通过**（漏任何一条即为合并事故）：
   - `TestTurnFailureKeepsEventChannelOpen`（grok 与 codex 各一条，B92/B99）
   - `TestSendRefusesOnClosedChannel`（grok 与 codex 各一条，B92/B99）
   - `TestHandleResultSweepsProcsOnFail` / `TestHandleResultSweepsProcsOnSuccess`（B93）
   - `TestScanTaskProcsWarnsOnceAtBudget` / `TestScanTaskProcsRearmsAfterFallback`（B93）
   - `TestDoneIsIdempotentOnCompleted`（B93）
6. **端点存在性冒烟**（这是本条 backlog 的直接目标，必须真起进程）：用**临时 datadir 与临时
   端口**起一个 agentd，对 `GET /api/projects/tree`、`GET /api/machines`、`GET /api/tasks`
   各发一次带 token 的请求，断言**都不是 405**。冒烟完把进程停掉、临时目录删掉。
   **不要动 `~/.handoff`**，那是这台机器正在服役的数据目录。
7. **backlog 合表**：两线条目都在，每行有「线」列，表头有歧义说明，新条目起始号写明 B103。

## 7. 明确不做

- **不合并进 `main`**，不 `git push` 到 `main`，不动任何 tag。
- **不改任何功能行为**。本次只做合并与改名；发现的 bug 记进 backlog，不顺手修。
- **不做 B95 / B96 / B97**，不做原型回流——它们等这条并完再排。
- **不升级依赖**、不跑 `go mod tidy` 之外的任何依赖变更；`go mod tidy` 只在编译确实需要时跑，
  且跑完要在 ledger 里说明为什么需要。
- **不删除任何一条 backlog 条目**，包括看起来重复的。
