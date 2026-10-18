# B436 plan：删除代码图（codegraph）消费侧功能

- **卡**：B436，L2；spec `docs/superpowers/specs/b436.md`（已批准 r0，2026-10-18）；台账 `docs/superpowers/specs/b436-ledger.md`（本轮续写，不另开）。
- **分支**：`cards/B436-charter-1`（worktree `.claude/worktrees/cards+B436-charter-1`），基线 `main` @ `c944b6b7`。
- **执行形态**：本地 subagent 出稿，协调者（本会话）审；不走 handoff 派发。

## 偏离与自主决定（相对 spec）

1. **不新增含 `codegraph` 字面量的锁测试。** spec 面 B 说「补一条锁：`GET /api/projects/{name}/codegraph` 必须 404」，面 E 说权限门测试「改为 `codegraph sym X` 判 Consult」。两条锁都要在 `_test.go` 里写下 `codegraph` 字面量，与 S5「`docs/` 之外零命中」自相矛盾。取舍：S5 判据优先；S2 的 404 由 implement 收尾时 `curl` 本机 agentd 取证进台账，S3 的「不再放行」由 `SafeCommandIDs` 不含该 id（grep 可证）加全量绿承担。CLI 既有锁 `TestRootRejectsDeletedGraphCommand` 名字里只有 `Graph`，不触 S5，保留。
2. **S5 的例外清单补 `CHANGELOG.md`**：spec 要求的「### 移除」条目本身会含 `codegraph` 字样，它在仓根不在 `docs/`。S5 的允许命中 = `docs/**` + `CHANGELOG.md`。
3. **平台不变量测试的替代断言**用「正文不含『查图』」而非「不含 codegraph」，同为否定断言、不触 S5。
4. **roadmap 裁剪在本 plan 逐条点名**（见 G.7），不留给 implement 自由判断——这是整卡唯一的判断型改动，点名后 review 才有对照基准。

## 交付目标

本轮一次交付 spec 全部五条故事 S1–S5，无后续轮次。做到：worktree 内 `go build ./... && go vet ./... && go test ./... -count=1`、gofmt、Windows 交叉编译 + vet、`desktop/` tidy 对齐、`web` typecheck / test / build 全绿；S5 grep 判据只剩 `docs/**` 与 `CHANGELOG.md` 命中；控制台无代码图入口。留待后续的只有 Out of Scope 已列项（账本两块 recon 纪律块下架）。

## 改动边界

只动 handoff 仓本分支。**禁区**：`docs/` 下既有 spec / ledger / `backlog.md` / `docs/codegraph-best-rationale.md` / `docs/2026-08-22-codegraph-batch-handover.md` 一字不改；`.github/`、`release.yml`、`desktop/` 源码（只允许 `go.mod`/`go.sum` 因 tidy 变化）、`mobile/` 源码（只改 README 一行）、账本（任何 `handoff card/discipline/workflow` 写命令）不碰。

真实代码入口（现状读数，工作树复核）：agentd 路由注册 `internal/agentd/server.go:899-900,950-958`；控制台壳 `web/src/app/shell/Shell.tsx` 的 Route 表与 `ProjectTree` 的 prop 面；权限门 `internal/permgate/permgate.go#safeCommandID`；纪律块 `internal/discipline/platform.go#Compose`。

架构方向：删除不新增任何依赖方向；保留的三处注释（`cmd/launcher.go:14`、`cmd/squad.go:5-6`、`internal/client/wakegate.go:17`）里关于依赖方向的论断照旧成立，只把论据从「`codegraph/target.json` 已声明」改为「架构法（`charter:architecture-law`）依赖方向」。

## 工作顺序（每步末跑一次该步的局部验证；第 9 步全量）

### 1. 拔依赖，让编译器列清单（面 A）
- `go.mod` 删 `github.com/Xsxdot/charter/graph v0.10.2`；`git rm -r codegraph/`。
- `go build ./... 2>&1 | head -40` 把全部 import 失败处记进台账（预期：`cmd/graph_gate_test.go`、`internal/agentd/codegraph.go`、`codegraph_test.go`、`server.go`、`scripts/codegraph-*`）。

### 2. agentd 网关（面 B）
- `git rm internal/agentd/codegraph.go internal/agentd/codegraph_test.go`；`git rm -r internal/agentd/testdata/codegraph-repo`。
- `server.go`：删 `charterwebui` import、两条 `api.HandleFunc(... codegraph ...)`、`mux.Handle("/codegraph/app/", ...)` 与其注释、`s.log.Info("代码图 viewer 静态资源已挂载", ...)`。`newSPAHandler` 仍被 `webui.FS()` 使用，保留。
- `webroute_test.go`：删 `TestCodegraphRoute` 整函数及其专用 import（`regexp`、`bytes` 若不再被引用）。
- `b23328_source_guard_test.go:23` 文件清单去掉 `"codegraph.go"`。
- 注释改写：`handlers.go:4-5`（删「codegraph assembly 免检名单 / codegraph check 看见」两句，改为「请求处理不享受开机插线豁免，跨域直调由源码级守卫 `TestB23317AssemblyFilesHaveNoHandlerDefs` 看见」）；`coordinator_sessionref.go:9` 删「图对账（刀3）按实然登记其容器」半句。
- 局部验证：`go build ./internal/agentd/ && go test ./internal/agentd/ -run 'Route|B23328|B23317' -count=1`（此时 b23317 仍红，第 3 步修）。

### 3. 契约闸与守卫（面 C）
- `git rm cmd/graph_gate_test.go`。
- `internal/agentd/b23317_boundary_test.go`：删 `TestB23317AssemblyFreezeExcludesHandlerFile`、`TestB23317FacadeBudgetsStayRatchet`、类型 `b23317FrozenContract` 及其它只被这两条用到的 helper；`b23317AssemblyFiles` 改为**不读文件**，返回测试内显式名单 `[]string{"main.go", "internal/agentd/server.go", "cmd/agentd.go"}`（即被删 `target.json` 的 `assembly` 现值），并在注释写明「名单自 B436 起由本测试持有」。保留 `TestB23317GatewayProductionDoesNotImportNestedInternals`、`TestB23317NestedInternalGuardHasTeeth`、`TestB23317AssemblyFilesHaveNoHandlerDefs`、`TestB23317HandlerGuardHasTeeth`。
- `pointer_gate_test.go`：删白名单里六条 `scripts/codegraph-rescan/main.go` 条目；文件头注释第 6-10 行关于 `cmd/graph_gate_test.go:36` 与 `graph check` 的论证改为一句「图闸已随 B436 删除；本测试从一开始就是读源码的守卫」。
- `git rm -r scripts/codegraph-flows scripts/codegraph-rescan`。
- 局部验证：`go test ./internal/agentd/ -run 'B23317|Pointer' -count=1`。

### 4. 纪律块与权限门（面 E）
- `internal/discipline/platform.go`：删 `platformInvariantHead` 第 2 条，余三条重编号 1-3；`platform_test.go:26-37` 改为：不含 `handoff graph`（保留）、不含「查图」、不含「二进制」；删对 go run 入口与 ask 工单的两条断言（断言对象已不存在）。
- `internal/permgate/permgate.go`：`SafeCommandIDs` 删 `"codegraph"`；删 `codegraphReadSubcommands`、`codegraphValueFlags`、`codegraphReadSubcommand` 与 `safeCommandID` 的 `fields[0] == "codegraph"` 分支（含 B376 注释）。`permgate_test.go` 删 `:40,74,112-115,248` 五处用例行；若某表驱动因此为空则整表删。
- 局部验证：`go test ./internal/discipline/ ./internal/permgate/ -count=1`。

### 5. 散落字面量（面 J 与测试夹具）
- `cmd/launcher.go:14`、`cmd/squad.go:5-6`、`internal/client/wakegate.go:17`：按「改动边界」改写论据。
- `internal/collab/service_test.go:14` 注释删「（charter/graph edgegate.go:190）」。
- `internal/client/execution_test.go:135` 「B380 recon 补记」→「B380 对账补记」；`internal/executor/opencode/adapter_test.go:1452` `task-recon-0001` → `task-audit-0001`；`internal/executor/turn/protocol_test.go:86` 删 `|| strings.Contains(lower, "recon")`，保留 review 半句。
- `.gitignore:24`、`.zcodeignore:24` 删 `prototypes/codegraph/`；`mobile/android/README.md:24` 排除清单删 `codegraph/**`。
- 局部验证：`go vet ./... && gofmt -l $(git ls-files '*.go')` 为空。

### 6. 控制台前端（面 F）
- `git rm web/src/app/codegraph/CodegraphFrame.tsx`。
- `Shell.tsx`：删 import、`fullPageRoute` 白名单里的 `'/codegraph'`、`openProjectCodegraph` 函数、`onOpenCodegraph` / `onOpenProjectCodegraph` 两个传参、`/codegraph` Route 与其注释（`:1274` B369.7 注释里去掉 `/codegraph` 字样，保留 `/flows` 的说法）。
- `ProjectTree.tsx`：删 `onOpenCodegraph` / `onOpenProjectCodegraph` 两个 prop（类型、解构、默认值）与以它们为门控的全部渲染分支——项目行「代码图」子钮（`:949-954`）、dock「代码图」钮（`:1491-1496`）、右键菜单「项目代码图」（`:1578`）；`rowActionZone`（`:707`）与键盘菜单守卫（`:806-809`）的条件去掉对应项；注释 `:149,:924,:1463` 去掉代码图提法。
- `api/client.ts`：删 `fetchCodegraph`、`fetchCodegraphSource` 与 `CodegraphResp` import；`api/types.ts:831-895` 删 `CgTestRef…CgSourceResp` 整块（保留其后 `DesktopState` 注释块）。
- 测试：`Shell.test.tsx` 删「代码图 iframe src」与「/codegraph 隐藏 Breadcrumb/FileTree」两条用例；`ProjectTree.test.tsx` 删 `onOpenProjectCodegraph` prop 透传与 `:1494-1496` 用例里的 graph 回调（用例改为只验 cards/edit 两项仍在菜单里，名字同步去掉 codegraph）；`CardsPage.test.tsx:556-683` 夹具 `图对账` → `最终验收`；`NodeEditor.purpose.test.tsx:34-41` `recon` → `audit`。
- 局部验证：`npm --prefix web run typecheck && npm --prefix web test`。

### 7. 文档、原型与 roadmap（面 G）
- `git rm docs/codegraph-scan-recipe.md`；`git rm -r deploy/workflows`（目录只剩这两份文件，整目录删）。
- 原型基准站：`git rm prototypes/base/pages/codegraph.html prototypes/base/data/graph.json prototypes/base/data/graph.js`；`prototypes/base/index.html:280` 删 dock 钮一行；`prototypes/base/README.md:26` 删表格该行，`:69-78` 删「代码图页的形态基准…」整段（到该段落结束的空行）。
- `docs/roadmap.md` 裁剪（行号为 `main` 现状，按内容定位）：

  | 处置 | 条目（以首行关键词定位） |
  |---|---|
  | 删整节 | `## 代码图数据债（2026-08-26 B156.3 contract 轮实测）` 全节；`## 来自 C1.7 finish 的基准回灌（2026-08-25）` 全节；`## 来自 B432 真机验收（2026-10-11，图覆盖债）` 全节 |
  | 删单条 | `mobilecore 图覆盖债`；`go run github.com/…/codegraph 认成 codegraph 查询`；`图覆盖债：新增导出 Store.OpenTickets`；`契约预算未棘轮`；`codegraph check 仍 6 红（P1-A）`；`codegraph validate 完整性问题 ~130`；`其余入传输边的 entries 复核`；`d_workspace→d_orchestration 预算清零`；`本卡带来的 5 条 over-budget 待棘轮`；`cards-B233.4 视图失效边`；`完整图重扫未做 / absorb 拒收`；`taskProfileHome 图覆盖债`；`TestRepoContractGate dead-interface OrchestrationClient`；B231 节里的 `符号级漏建残余 33 个`、`6 个零节点容器`、`6 个非 card 族 CLI 父入口仍标 unscanned`、`基线合并即滞后`；C1.9 节里的 `原型基准站 codegraph 页与真实前端在下钻形态上漂移`；C1.11 节里的 `声明覆盖停在 2/23`、`d_workspace.json 的职责句可能有跨域残留`、`前端消费 decls 未实现`；`执行机上装 codegraph（B227 图对账暴露）`；`contract 节点给 codegraph/target.json 写 entries 时用了函数符号名`；`best.json 未声明 k_web_api_scheduling`；`图视图命名与分支不一致`；B383 节里的 `diff 烙基线指纹`、`codegraph CLI 内建扫描命令`、`重扫扫描配方的边提取保真度` |
  | 改一词 | `推平 breakdown / implement / integrate / contract / recon 五份纪律块的两机差异` → 去掉 `recon`、「五份」改「四份」；`跨节点事实不继承（流程改进）` 条里的示例「本机 codegraph 在 PATH，勿用 go run」换成「本机工具链事实（如某二进制在 PATH）」；C1.11 节 `两条真机项由 CI 结论兜底` 条删 (a) 子项、保留 (b) |
  | 保留 | 其余一切，含 `全仓 internal/ 按 best.json 重排` 两条（主语是物理搬家）、`终态事件丢失第二次实测`（主语是 executor 静默）、B185/B228 等以代码图为佐证的条目 |

  删完一节后若该节只剩标题，标题一并删。新增节（放在文件末尾现有最后一节之后）：

  ```
  ## 来自 B436 finish（2026-10-18，代码图消费侧删除）

  - **账本纪律块 `charter-recon v3` / `charter-story-recon v1` 待下架**：`handoff discipline` 只有 `get / list / put`，没有删除或下架命令；流 v14 已不引用它们，但 `discipline list` 仍列出。需要给 `discipline` 加下架能力后再清。来源：B436 spec Out of Scope。
  ```
- `CHANGELOG.md` `[Unreleased]` 下新增 `### 移除` 小节（放在 `### 变更` 之后）一条：「**代码图（codegraph）消费侧整体移除（B436）。** 控制台不再有「代码图」入口与 `/codegraph` 页；agentd 删除 `GET /api/projects/{name}/codegraph[/source]` 与 charter viewer 挂载；权限门不再静默放行 `codegraph` 命令，执行者纪律块不再要求本机装 codegraph 二进制；仓内 `codegraph/` 数据目录、契约闸 `TestRepoContractGate`、扫描脚本与配方一并删除，`go.mod` 去掉 `github.com/Xsxdot/charter/graph`。用户 2026-10-18 裁决：「直接把代码图彻底删除，handoff 里面的功能也删除」。」

### 8. 依赖收口
- 仓根 `go mod tidy`；`cd desktop && go mod tidy`；`git diff --stat go.mod go.sum desktop/go.mod desktop/go.sum` 记台账（预期 `charter/graph` 两行从三个文件消失，可能连带若干 `// indirect`）。

### 9. 全量与取证（三段律的全量段）
按顺序跑并把**命令原文 + 退出码 + 关键输出**追加进台账：
```
go build ./... && go vet ./... && go test ./... -count=1
test -z "$(gofmt -l $(git ls-files '*.go'))"
GOOS=windows GOARCH=amd64 go build ./... && GOOS=windows GOARCH=arm64 go build ./... && GOOS=windows GOARCH=amd64 go vet ./...
(cd desktop && go mod tidy && git diff --exit-code -- go.mod go.sum)
npm --prefix web run typecheck && npm --prefix web test && npm --prefix web run build && test -f web/dist/index.html
grep -rIn -E 'codegraph|charter/graph|图对账|\brecon\b' --exclude-dir=.git --exclude-dir=node_modules --exclude-dir=.claude --exclude-dir=dist . | grep -v -E '^\./(docs/|CHANGELOG\.md)'
```
最后一条必须**零输出**。`web/node_modules` 缺失时先 `npm --prefix web ci`。
S2 真机：`go run . agentd` 不便时，用 `go build -o "$TMPDIR/handoff" . && "$TMPDIR/handoff" agentd --help` 证明二进制可起；`/api/projects/x/codegraph` 的 404 由协调者在最终验收用本机 agentd 取证，implement 不起常驻进程。

## 验收方式

- S1：`Shell.test.tsx` / `ProjectTree.test.tsx` 全绿且文件内无 `代码图` / `codegraph` 字样；`npm run build` 产物 `web/dist/index.html` 存在。真机一眼归最终验收（协调者）。
- S2：`go test ./internal/agentd/ -count=1` 绿；`grep -rn codegraph internal/agentd` 零命中；agentd 启动日志不含「代码图 viewer」归最终验收。
- S3：`go test ./internal/discipline/ ./internal/permgate/ -count=1` 绿；`grep -n codegraph internal/discipline internal/permgate` 零命中。
- S4：第 9 步前五条命令全部退出 0。
- S5：第 9 步 grep 零输出。
- 反例（必须仍红/仍绿的对照）：`TestRootRejectsDeletedGraphCommand` 仍绿；`TestB23317NestedInternalGuardHasTeeth`、`TestB23317HandlerGuardHasTeeth`、`TestPointerRouteAbsentFromSource` 仍绿（守卫没被一起删掉）；`ProjectTree.test.tsx` 中 cards / edit 菜单项用例仍绿（删钮没把邻居删掉）。
- 缺陷族对照：本卡是删除，相关族只有「假绿」（删光测试也全绿）与「残留」（死代码 / 死注释）。假绿靠上面的反例对照与 S5 grep；残留靠 `go vet` 的 unused import 与 S5。

## 决策权限

**执行者可自决**：helper 的具体重构方式、注释措辞、roadmap 单条内部的删句边界（只要条目留存与否与上表一致）、提交拆分粒度（建议按步骤 1-8 分提交，每条提交 footer 带 `B436` 与裁决原话）、是否把 `go mod tidy` 带出的 indirect 变化合进第 8 步提交。

**必须停下升级给协调者**（在台账记「待裁决」并结束回合，不猜）：任何对「改动边界」禁区的触碰；步骤 1 的编译失败清单出现 plan 未列的文件；步骤 9 任一命令在清理完本卡范围后仍非 0 且根因不在本卡范围；roadmap 里发现上表没点名、但疑似应删的条目（列出来，不动）；`desktop/` 或 `mobile/` 的 tidy / 构建牵出源码改动。

**交回物**：台账续写（第一节「偏离与自主决定」）、分支上的提交、第 9 步六条命令的原始输出。
