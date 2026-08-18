# W5b-win 台账：Windows 桌面薄壳

范围：3 个 task + 整分支终审。分支 `feat/w5b-win-desktop-exec`。
恢复现场以本 ledger + git log 为准。

**执行方式**：subagent 驱动（逐 task 实现 subagent → 独立审查 subagent 双裁决 → 修复回路）。
改动全部经 subagent 产出且经审查，协调上下文不亲自改代码。

**基线**：commit `8ba4e333`（本计划提交）之上，`1312731c` 已把 `desktop/build/windows/`
与 Taskfile 钩子就位，本计划不碰它们。

## 派发前发现的计划缺口（协调者已确认）

**Task 2 的 windows job 会新增第 5 处 CLI 构建点**（`-X github.com/Xsxdot/handoff/
internal/buildinfo.releaseVersion=`）。`TestWorkflowInjectsVersionAtModulePath` 的
`wantCount` 写死 4，Task 2 合入后该测试必红。该测试自己的文档注释写明「这个数字是
workflow 里编 CLI 的地方有几处的代理，**加构建点就要同步加**」——协调者确认在
Task 3 里把 `wantCount` 改成 5，并在改动处留一行注释说明原因（避免后人以为是笔误）。

## 进度

- 2026-08-19 **Task 1（平台自适应的 CLI 落点）实现完成**。实现 subagent 产出：
  `desktop/internal/shell/clipath.go`（新建）+ `clipath_test.go`（新建，7 条），
  改 `binpath.go` 第一候选用 `DefaultCLIPath()`（保留 `os`/`filepath`/`slog` import），
  改 `main.go` 释出落点用 `shell.DefaultCLIPath()`（删 `path/filepath` import，`os` 保留）。
  实现 subagent 报告：`TestCLIPathFor` 7 条 PASS、shell 包 Windows 交叉构建 PASS、
  根模块全量测试 PASS、gofmt/vet（shell 包）干净。
  **注意**：desktop 模块 `go test ./...` 被既有基线问题阻断——`desktop/frontend/dist`
  在本工作树不存在（gitignore 且从未构建），main.go:38 `//go:embed all:frontend/dist`
  编译期失败，与本次改动无关。需先跑 `npm --prefix desktop/frontend ci && npm run build`
  产出 dist 才能跑 desktop 模块全量测试（CI 里也是这个顺序）。
  变异复验与独立审查待执行。
