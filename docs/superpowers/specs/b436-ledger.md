# B436 台账：删除代码图（codegraph）消费侧功能

> 边干边落。每确立一个事实追加一行；提交事实只记当时命令与输出，随后 amend 一次收进同批，不回写新 hash。

## 2026-10-18 spec 节点（协调者会话 `local_efafb41a-f958-4cbb-b795-0e00a1ed5b29`，桌面端）

### 卡操作

- 14:55 `handoff card show B436` → status `待办`，note 一条（来源：用户裁决「直接把代码图彻底删除，handoff 里面的功能也删除，几乎没看过代码图」）。
- 14:55 `handoff card move B436 spec` → `{"status":"spec"}`，exit 0。
- 14:55 `handoff card bind B436` → `{"ok":true}`（会话身份取自 `CLAUDE_CODE_SESSION_ID`）。
- `handoff card seat desktop set B436 --desktop-session local_efafb41a-…` → `Error: unknown flag: --desktop-session`；本机 `~/.local/bin/handoff` 的 `card seat` 只有 `bearing` 子命令。结论：已安装二进制落后于 skill 文本与源码（B434 刚合入 main `18061f2c`），不属本卡；信箱唤醒本会话不可用，由父会话消息驱动。
- 共享笔记夹 `side-session-notes/local_4e712e78-…` 为空。

### 事实调查（grep 实况，原始命令与关键输出）

- `grep -rn --include='*.go' 'charter/graph' .`（排除 `codegraph/`）→ 7 处：`cmd/graph_gate_test.go:6`、`internal/agentd/codegraph.go:22`、`internal/agentd/codegraph_test.go:18`、`internal/agentd/server.go:38,958`、`internal/collab/service_test.go:14`（注释）、`internal/discipline/platform_test.go:29`（断言不得出现 go run 入口）。
- `grep -rli --include='*.go' codegraph .` → 19 个 Go 文件：cmd 3（`graph_gate_test.go`、`launcher.go`、`squad.go`）、agentd 8、client 1、discipline 2、permgate 2、scripts 3。
- 非 Go / 非 docs 命中：`.gitignore:24`、`.zcodeignore:24`、`mobile/android/README.md:24`、web 7 文件（`api/client.ts`、`api/types.ts`、`app/codegraph/CodegraphFrame.tsx`、`app/shell/Shell.tsx`、`Shell.test.tsx`、`app/tree/ProjectTree.tsx`、`ProjectTree.test.tsx`），web 共 46 行。
- `图对账|recon`（排除 docs/codegraph，过滤 reconnect/reconcil）→ `web/src/app/cards/CardsPage.test.tsx:556-683` 夹具列名、`web/src/app/flows/NodeEditor.purpose.test.tsx:34-41` 自建用途示例值（`NodeEditor.tsx:41` `knownPurposes = ['implement','review']`，`recon` 只是测试里的任意未知值）、`deploy/workflows/charter-v4.json:125-133`、`deploy/workflows/README.md:50`、`internal/executor/turn/protocol_test.go:86`（断言 ProtocolRules 不点名角色，含 `recon` 字样属否定断言，保留）、`internal/executor/opencode/adapter_test.go:1452` `task-recon-0001`（任务 id 字面量）、`internal/client/execution_test.go:135` 注释「B380 recon 补记」、`internal/agentd/coordinator_sessionref.go:9` 注释。
- `cmd/` 无 `Use: "graph"`；`cmd/root_test.go:291` `TestRootRejectsDeletedGraphCommand` 断言 `graph --help` 报 unknown command。结论：命令族早已删除。
- `du -sh codegraph` → 7.2M；`find codegraph -maxdepth 2`：`baseline.json`、`best.json`、`target.json`、`diffs/cards-B383-charter.json`、`diffs/cards/`、`domains/*.json` 23 份。
- `.github/workflows/ci.yml` / `release.yml` / `release_workflow_test.go` grep `graph` → 0 命中。ci.yml 步骤：go build/vet/test、gofmt、desktop `go mod tidy` 对齐门、Windows 交叉编译 + vet、install.sh 单测、web `npm ci && npm run build && test -f dist/index.html && npm test`。
- `grep charter desktop/go.sum` → 两行 `charter/graph v0.10.2`；`desktop/go.mod` 无直接 require。
- `handoff workflow show charter` → Version 14，states 13 列（待办→spec→contract→breakdown→plan→implement→review→阶段验收→待批次→integrate→最终验收→finish→已完成），无「图对账」。
- `handoff discipline list` → 14 块，含 `charter-recon v3`、`charter-story-recon v1`；`handoff discipline --help` 子命令只有 `get / list / put`。
- `handoff discipline get charter-story-{plan,implement,review,contract,breakdown,integrate} | grep -ci 'codegraph|代码图|图对账|recon'` → 全部 0；`handoff template get charter-story-default` 同样 0。
- `web/package.json` scripts：`build = tsc -b && vite build`、`test = vitest run`、`typecheck = tsc -b`。
- `prototypes/base` 命中：`README.md:26,69-70`、`index.html:280`（dock 钮 `pages/codegraph.html`）、`pages/codegraph.html`、`data/graph.json`（102 KB）、`data/graph.js`（1.7 MB）；`.gitignore` 的 `prototypes/codegraph/` 目录在盘上不存在。
- `docs/roadmap.md` 988 行；以代码图为主语的节/条见 spec 面 G 列表（`grep -n -i 'codegraph|recon|图对账|代码图'` 共 30 余行）。
- `internal/permgate/permgate.go`：`SafeCommandIDs()` :325-334 含 `"codegraph"`；`codegraphReadSubcommands` :341-345、`codegraphValueFlags` :351-353、`codegraphReadSubcommand` :362-392、`safeCommandID` 分支 :483-495。全仓无对 `SafeCommandIDs` 结果的 golden 哈希断言（`grep -rn SafeCommandIDs --include='*_test.go'` 0 命中）。
- `internal/agentd/b23317_boundary_test.go` 六个测试：`GatewayProductionDoesNotImportNestedInternals`、`NestedInternalGuardHasTeeth`、`AssemblyFreezeExcludesHandlerFile`（读 target.json）、`FacadeBudgetsStayRatchet`（读 target.json）、`AssemblyFilesHaveNoHandlerDefs`（经 `b23317AssemblyFiles` 读 target.json 的 assembly 名单）、`HandlerGuardHasTeeth`。
- `internal/agentd/pointer_gate_test.go:58-69`：白名单六条 `scripts/codegraph-rescan/main.go`（`methodNodeOf`/`namedOf`/`isCobraCommand`/`verifyLifecycle`/`sameNamedPtr`/`sameNamed`）；测试要求每条至少命中一次。
- `internal/agentd/b23328_source_guard_test.go:23` 扫描清单含 `"codegraph.go"`（文件不存在时 `ParseFile` 会失败）。
- `internal/agentd/webroute_test.go:116-200` `TestCodegraphRoute`：锁 `/codegraph/app/` 200 + 深路径回落 + 资产 immutable 缓存头 + POST 405 + 未登录 401。
- `internal/discipline/platform.go:13` 平台不变量第 2 条全文已读；`platform_test.go:26-37` 四条断言（无 `handoff graph`、无 go run、有「已安装的 codegraph 二进制」、有 ask 工单 JSON）。

### 工作树

- `git worktree list` → main 在仓根（工作树有一处未提交改动 `docs/superpowers/specs/b434-ledger.md`，属父会话，本卡不碰）；已有 `.claude/worktrees/cards+B435-charter-1`（locked）。
- `git worktree add .claude/worktrees/cards+B436-charter-1 -b cards/B436-charter-1 main` → HEAD `c944b6b7`，工作树干净。

### 裁决（写回 spec 正文，这里只记时点）

- 定级 L2（理由见 spec「定级两问」）；`deploy/workflows/` 整删；`docs/codegraph-scan-recipe.md` 删、另两份 docs 保留；roadmap 按「主语是代码图」裁剪；原型基准站代码图页在本卡删；夹具文案改名；注释改写不删架构论断。全部待用户批准。

### 提交

- `git commit`（spec r0 + 台账）→ `d97af78e docs(b436): spec r0 删除代码图消费侧功能，待用户批准`

### 批准

- 2026-10-18 用户经主会话（local_4e712e78）回复「你推进吧，不用再问我了」：spec r0 与六项自主决定一并批准；授权 plan→implement→review→finish 一路推进、finish 本地 `--no-ff` 合 main；只有破坏性不可逆或超出 B436 范围的决定才回问。批准判决已回写 spec 头部状态行。
- 账本流 v14 已缩为六列；B436 钉 v13，按 v13 列序走到底，不 migrate。

## 2026-10-18 plan 节点（协调者本会话出稿，L2）

- 补核 `internal/agentd/b23317_boundary_test.go:264-300`：`TestB23317AssemblyFilesHaveNoHandlerDefs` 经 `b23317AssemblyFiles` 读 `codegraph/target.json` 的 `assembly`，现值 `['main.go','internal/agentd/server.go','cmd/agentd.go']`（`python3 -c "json.load(open('codegraph/target.json'))['assembly']"`）。plan 决定名单改由测试持有。
- `grep -n -i -E 'codegraph|recon|图对账|代码图|graph check|baseline\.json|best\.json|target\.json|absorb|图覆盖债|重扫' docs/roadmap.md` → 60 余行，逐条分类进 plan 第 7 步表格（删节 3、删条 29、改词 3、保留其余）。
- plan 落盘 `docs/superpowers/plans/b436-plan.md`；相对 spec 的偏离四条写在 plan 首节（不加含 `codegraph` 字面量的锁测试；S5 例外补 `CHANGELOG.md`；平台不变量测试改断「查图」；roadmap 裁剪逐条点名）。

## 2026-10-18 implement 节点（subagent）

### 偏离与自主决定

（占位，随进度补全；交回前完整）

### 第 1 步：拔依赖

- `sed -i '' '/github.com\/Xsxdot\/charter\/graph v0.10.2/d' go.mod && git rm -r -q codegraph/` → exit 0；`git status --short` 列出 `codegraph/` 下 baseline/best/target/diffs/domains 全部 D。
- `go build ./...` → exit 1；`go vet ./...` → exit 1。两者输出相同、只有两条（go 在模块解析阶段即停，不再往下列）：`internal/agentd/codegraph.go:22:2: no required module provides package github.com/Xsxdot/charter/graph/codegraph`、`internal/agentd/server.go:38:2: … charter/graph/webui`。`grep -ln 'charter/graph' cmd/*.go scripts/*/*.go` → 仅 `cmd/graph_gate_test.go`（同一死模块，编译器未到测试阶段）。`scripts/codegraph-rescan/` 有自己的 `go.mod`（不在主模块 `./...` 内）；`scripts/codegraph-flows/` 在主模块内但不 import charter/graph。清单未出现 plan 未列文件，不触发升级。

### 第 2 步：agentd 网关

- `git rm internal/agentd/codegraph.go internal/agentd/codegraph_test.go && git rm -r internal/agentd/testdata/codegraph-repo` → exit 0。
- `server.go`：删 `charterwebui` import（第 38 行）、两条 `api.HandleFunc(... codegraph ...)`（899-900）、`mux.Handle("/codegraph/app/", ...)` 与两段注释、`s.log.Info("代码图 viewer 静态资源已挂载", ...)`；`newSPAHandler` 仍由 `webui.FS()` 使用，保留。删 import 后留下空行，`gofmt -l` 报 server.go，`gofmt -w` 收口。
- `webroute_test.go`：删 `TestCodegraphRoute` 整函数及仅被它用的 `bytes`、`regexp` import。
- `b23328_source_guard_test.go:23` 清单去掉 `"codegraph.go"`。
- `handlers.go:4-5` 注释改为「请求处理不享受开机插线豁免，跨域直调由源码级守卫 `TestB23317AssemblyFilesHaveNoHandlerDefs` 看见」；`coordinator_sessionref.go:9` 删「图对账（刀3）按实然登记其容器」半句。
- `go build ./internal/agentd/` → exit 0。`go test ./internal/agentd/ -run 'Route|B23328|B23317' -count=1` → exit 1，FAIL 恰为三条 b23317：`TestB23317AssemblyFreezeExcludesHandlerFile`、`TestB23317FacadeBudgetsStayRatchet`、`TestB23317AssemblyFilesHaveNoHandlerDefs`，报错均为 `open .../codegraph/target.json: no such file or directory`（plan 预期，第 3 步修）。`-run 'Route|B23328'` 单独 → ok。

### 第 3 步：契约闸与守卫

- `git rm cmd/graph_gate_test.go && git rm -r scripts/codegraph-flows scripts/codegraph-rescan` → exit 0。
- `b23317_boundary_test.go`：删 `TestB23317AssemblyFreezeExcludesHandlerFile`、`b23317FrozenContract` 类型与 `b23317FrozenContracts` 表、`TestB23317FacadeBudgetsStayRatchet`、仅被它用的 `containsStr`（`grep -rn containsStr internal/agentd/` 别处 0 命中）；`b23317AssemblyFiles` 改为不读文件，返回 `[]string{"main.go", "internal/agentd/server.go", "cmd/agentd.go"}`，注释写明名单自 B436 起由本测试持有；删 `encoding/json` import。保留四条守卫。
- `pointer_gate_test.go`：删白名单六条 `scripts/codegraph-rescan/main.go`；文件头 6-10 行改为「图闸已随 B436 删除；本测试从一开始就是读源码的守卫」一句。另把 keystone 条目 `why` 里的「target.json assembly 登记点」字样去掉（指向已删对象的注释，见「偏离与自主决定」）。
- `gofmt -l internal/agentd/ cmd/` → 空。`go test ./internal/agentd/ -run 'B23317|Pointer' -count=1 -v` → exit 0，PASS：`TestB23317GatewayProductionDoesNotImportNestedInternals`、`TestB23317NestedInternalGuardHasTeeth`、`TestB23317AssemblyFilesHaveNoHandlerDefs`、`TestB23317HandlerGuardHasTeeth`、`TestPointerRouteAbsentFromSource`。

### 第 4 步：纪律块与权限门

- `internal/discipline/platform.go`：删 `platformInvariantHead` 第 2 条，余三条重编号 1-3；文件头职责注释「两条底线」→「三条底线」（原注释与条目数本就不符，顺手对齐）。`platform_test.go:29-37`：删 go run 入口、「已安装的 codegraph 二进制」、ask 工单三条断言，改为否定断言「不含『查图』」「不含『二进制』」；`handoff graph` 否定断言保留。
- `internal/permgate/permgate.go`：`SafeCommandIDs` 删 `"codegraph"`；删 `codegraphReadSubcommands`、`codegraphValueFlags`、`codegraphReadSubcommand` 与 `safeCommandID` 的 `fields[0] == "codegraph"` 分支（含 B376 注释）。`permgate_test.go`：删 `:74`、`:112-115`、`:248` 四处用例；`:40` `{"which", "which codegraph"}` 改为 `{"which", "which gofmt"}`（见「偏离与自主决定」）；无表因此为空。
- `grep -rn codegraph internal/discipline internal/permgate` → exit 1（零命中）。`gofmt -l` → 空。`go vet ./internal/discipline/ ./internal/permgate/ && go test ./internal/discipline/ ./internal/permgate/ -count=1` → exit 0，两包 ok。

### 第 5 步：散落字面量

- `cmd/launcher.go:14`、`cmd/squad.go:5-6`、`internal/client/wakegate.go:17`：论据从「`codegraph/target.json` 已声明 / best.json 容器归属」改为「按架构法依赖方向」，架构论断本身不动。`internal/collab/service_test.go:14` 删「（charter/graph edgegate.go:190）」与「图边采集排除测试文件」半句。
- `internal/client/execution_test.go:135` 「B380 recon 补记」→「B380 对账补记」；`internal/executor/opencode/adapter_test.go:1452` `task-recon-0001` → `task-audit-0001`；`internal/executor/turn/protocol_test.go:86` 删 `|| strings.Contains(lower, "recon")`，review 半句保留。
- `.gitignore:24`、`.zcodeignore:24` 删 `prototypes/codegraph/`；`mobile/android/README.md:24` 排除清单删 `codegraph/**`。
- `go vet ./...` → exit 0；`gofmt -l $(git ls-files '*.go')` → 空，exit 0。

### 第 6 步：控制台前端

- `npm --prefix web ci` → exit 0（后台跑，输出 `$TMPDIR/b436-npm-ci.txt`）。
- `git rm web/src/app/codegraph/CodegraphFrame.tsx` → exit 0。
- `Shell.tsx`：删 import、`fullPageRoute` 的 `'/codegraph'`、`openProjectCodegraph`、两个传参、`/codegraph` Route 与注释；B369.7 注释（`:1274`）与 B426 注释（`:1633`）去掉代码图字样。
- `ProjectTree.tsx`：删两个 prop（类型、解构）、项目行「代码图」子钮、dock「代码图」钮、右键「项目代码图」；`rowActionZone`、`onContextMenu`/`onKeyDown` 守卫、compact 行簇条件去掉对应项；注释 `:149,:924,:1421,:1463,:1514` 去掉代码图提法；`GitBranch` 图标 import 随之无引用，删除。mobile-nav-note 文案「流程与代码图暂未适配移动端」→「流程暂未适配移动端」（见「偏离与自主决定」）。
- `api/client.ts` 删 `fetchCodegraph`、`fetchCodegraphSource` 与两个类型 import；`api/types.ts` 删「代码图」整块（`CgTestRef`…`CgSourceResp`），`DesktopState` 注释块保留。
- 测试：`Shell.test.tsx` 删「代码图 iframe 对项目名只做 query 编码」（it.each 三例）与「/codegraph 同时隐藏 Breadcrumb/FileTree」两条；plan 未列的另三处同文件断言一并处理（见「偏离与自主决定」）：`:698` 账本关闭用例的肯定对照从「项目代码图」菜单项改为「编辑项目位置」；`:2500-2502` compact 用例删「代码图」钮为 null 断言；`:2515-2523` 桌面零漂移用例删两条代码图在场断言并改名「流程钮在场」。`ProjectTree.test.tsx` 删 prop 透传与 `:1494-1503` 用例里的 graph 回调，用例改为只验 cards/edit。`CardsPage.test.tsx` `图对账`×6 → `最终验收`；`NodeEditor.purpose.test.tsx` `'recon'`×3 → `'audit'`。
- `grep -rn -i 'codegraph\|代码图' web/src` → exit 1（零命中）。`npm --prefix web run typecheck` → exit 0。`npm --prefix web test` → exit 0，`Test Files 146 passed (146)`，`Tests 1943 passed (1943)`。
