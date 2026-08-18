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

- 2026-08-19 **Task 1 变异复验 + 独立审查完成，commit `e6cac7b1`**。变异复验：
  把 clipath.go windows 分支 `handoff.exe`→`handoff`，3 条测试变红（WindowsUsesLocalAppData /
  FallsBackToHome / NamesBinaryPerPlatform），`git checkout --` 还原后恢复绿。
  为跑 desktop 模块全量测试，先 `npm --prefix desktop/frontend ci && npm run build`
  产出 gitignore 的 `frontend/dist`（CI 同序），此后 desktop 模块 `go test ./... -count=1`
  0 FAIL；根模块 0 FAIL；`GOOS=windows GOARCH=amd64 go build -tags production ./...` 退出 0；
  gofmt/vet 干净。
  独立审查 subagent 双裁决：**通过**，无修复项、无多做。
  审查 minor 记账（留终审 triage）：
  - M1: `binpath.go` `ResolveBinPath` 函数级 doc 仍写「空时依次尝试 ~/.local/bin/handoff 和
    PATH 上的 handoff」，在 Windows 上已不准确（第一候选已是 %LOCALAPPDATA%\Programs\...）。
    函数内行内注释已补平台说明，属 doc 漂移，不影响功能。

- 2026-08-19 **Task 2（release.yml 的 build-desktop-windows job）实现完成**。实现 subagent 产出：
  在 `build-desktop-darwin` 与 `release` 之间插入 `build-desktop-windows` job（96 行），
  `release` 的 needs 扩成 5 项、注释「四个构建 job」改「五个构建 job」。
  YAML 经 pyyaml 解析合法，jobs 键含 `build-desktop-windows`。
  **审查通过、已提交 `623a6973`**。独立审查 subagent 双裁决：通过，无修复项、无多做。
  审查 minor 记账（终审 triage）：
  - M2: release job 注释第二句「不把**两个**薄壳 job 列进来」已过期——薄壳 job 现在是三个。
    计划只要求改「四个→五个构建 job」，未提这句，记下待终审统一处理。

- 2026-08-19 **Task 3（契约测试跟上三个薄壳 job）实现完成**。实现 subagent 产出：
  改 `release_workflow_test.go` 的 `TestDesktopJobsCarryLoadBearingFlags`（windows job 存在性 +
  RunsOn 前缀断言；三条计数 2→3；新增 `{"ARCH=amd64", 1}`；needs 循环补 windows job），
  并把 `TestWorkflowInjectsVersionAtModulePath` 的 `wantCount` 4→5（协调者确认的计划缺口，
  注释留了「W5b-4 增了 build-desktop-windows 薄壳 job，构建点从 4 个变 5 个」）。
  四条变异逐条复验全红（GO_FLAGS 替换→计数 3→2；删 ARCH 行→计数 1→0；删 needs→
  needs 断言红；runs-on 换 ubuntu→RunsOn 断言红），还原后恢复绿。
  全量 `go test ./... -count=1` 0 FAIL、gofmt 干净、vet 干净。
  协调者复验：根模块与 desktop 模块全量 0 FAIL、目标测试 PASS、gofmt/vet 干净。
  审查与提交待执行。
