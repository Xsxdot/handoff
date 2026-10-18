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

1. **`permgate_test.go:40` 改值不删行**：plan 写「删 `:40`」，但该行 `{"which", "which codegraph"}` 是 `which` id 的唯一用例；改为 `{"which", "which gofmt"}` 保住覆盖、去掉字面量。plan 授权「helper 的具体重构方式」之内，偏离的是删/改之别。
2. **`pointer_gate_test.go` keystone 白名单条 `why` 去掉「target.json assembly 登记点」**：plan 只点了文件头 6-10 行；该 `why` 指向已删对象，按纪律 6「不留指向已删对象的注释」顺手改，注释措辞授权内。
3. **`internal/discipline/platform.go:3` 职责注释「两条底线」→「三条底线」**：plan 未列；原注释与条目数本就不符（原四条写「两条」），删第 2 条后余三条，顺手对齐。注释措辞授权内。
4. **`ProjectTree.tsx` 用户可见文案 mobile-nav-note「流程与代码图暂未适配移动端」→「流程暂未适配移动端」**（连带注释 `:1421,:1514`、`Shell.tsx:1633` 注释、`GitBranch` 图标 import 随之无引用删除）：plan 只点注释 `:149,:924,:1463`。该文案 B369.7 写「措辞逐字定死」，但保留会让控制台解释一个已不存在的入口；spec「控制台不新增任何提示」约束的是新增，这里是删词。**请审查确认**。
5. **`Shell.test.tsx` 另三处断言 plan 未列**（`:698` 账本关闭用例、`:2500-2502` compact 用例、`:2515-2523` 桌面零漂移用例）：同文件内对「代码图」钮/菜单项的在场/缺席断言，删代码图断言、肯定对照改为相邻入口（「编辑项目位置」/「流程」），用例名同步。属 S1 判据「文件内无 `代码图` 字样」的必要动作。
6. **roadmap `## 代码图数据债` 节内第四条「被依赖子系统的实现迁进标准结构」保留**：plan 写「删整节」，但该条主语是实现迁移/直调债清理，全文不提代码图，来源是 B156.3 spec §7.0；按 spec 判据「主语非代码图 → 留」。删掉标题与前三条后它自然落在紧邻其上的 `## 来自 B156.3 spec 定稿` 节末（即它引用的来源节），未另加标题。**请审查确认**。
7. **roadmap plan 表「B383 节里的 diff 烙基线指纹 / codegraph CLI 内建扫描命令 / 重扫扫描配方」三条实际位于 `## 来自 B379 spec（2026-10-02）` 节**：按首行关键词定位命中即删，该节随之只剩标题，标题一并删（plan 规则）。plan 节名笔误，条目处置一致。
8. **`b23317_boundary_test.go` 新注释初稿含字面量 `codegraph/target.json`**，第 9 步 S5 grep 命中后改为「代码图 target.json」。注释措辞授权内。
9. **`mobile/go.sum` 去掉 `charter/graph v0.10.0` 两行**（plan 未列；spec 面 A 只点了 `desktop/go.sum`）：S5 grep 命中后 `cd mobile && go mod tidy`，diff 恰为这两行、无其它连带。性质与 `desktop/go.sum` 同；plan 禁区写「`mobile/` 源码（只改 README 一行）」，go.sum 不是源码，且是 tidy 产物而非手改。**请审查确认**。
10. **S5 grep 用 `/usr/bin/grep`（BSD）而非 shell 里的 `grep`**：本会话 shell 的 `grep` 是 Claude Code 注入的 ugrep 垫片（`ARGV0=ugrep claude -G --ignore-files --hidden -I …`，见 shell snapshot），`--ignore-files` 在本仓会**整个跳过 `prototypes/*`**（加 `--no-ignore-files` 即与 BSD 一致），且输出路径无 `./` 前缀，plan 的 `grep -v '^\./(docs/|CHANGELOG\.md)'` 过滤因此失效。spec/plan 阶段的 grep 读数同样经此垫片，故漏掉 `prototypes/*` 各 fork 副本（见「待裁决」1）。两种口径的结果都记在第 9 步。
11. **CMD1 第二次用 `env -u CLAUDE_CODE_SESSION_ID` 跑**：字面第一跑 `TestSessionSendActorResolution` 红——`cmd/card_seat.go:68` 读环境变量 `CLAUDE_CODE_SESSION_ID`，本 subagent 运行在 Claude Code 会话内该变量恒在；单测 `env -u` 后 ok、不 `env -u` 必红；B436 未动 `cmd/session*.go` / `card_seat.go`。未改测试。按 plan 这是「非 0 且根因不在本卡范围」，列入「待裁决」4 而非静默放过。
12. **第 9 步两次 CMD1 / CMD5 重跑**：首轮三条重命令并发（go test ./... + Windows 交叉编译 + vitest），vitest 两条超时/时序断言红（`Shell.test.tsx` 5187ms>5000ms、`FileTab.test.tsx`）；单独重跑 146/1943 全绿。首轮 CMD1 另因第 8 项注释与第 9 项 go.sum 在其启动后才改，重跑一次作权威读数。
13. **曾清理 `prototypes/mobile-app/` 后回退**：S5 先命中该目录 6 文件，当作孤立遗漏清掉；随后 BSD grep 全仓分类发现是 8 个 fork 目录的一类问题，判断超出 plan 范围，`git checkout HEAD -- prototypes/mobile-app` 回退，净变化为零，整类列「待裁决」1。
14. **遇「待裁决」未结束回合**：三项待裁决（roadmap 两条、prototypes 一类、env 敏感测试）均为纯文档/判据层面、不影响第 8、9 步机械执行，故记台账后继续到收口；所有待裁决对象原样未动。
15. **提交粒度**：第 1-5 步合一提交（第 1、2 步单独不可编译/不过测），第 6、7、8 步各一，最后一提交收第 9 步修正（注释、mobile/go.sum）与台账。plan 允许合理归并。
16. **协调者裁决后补做（见「协调者裁决后的补做」小节）**：待裁决 1-4 全部按裁决执行。裁决之外的自主决定三处：(a) b264 / b285 / b288 fork 的 `index.html` 除 dock 钮外另有项目行 hover 簇的 JS 字符串 `'<a href="pages/codegraph.html?project=…" title="代码图">◈</a>'`，裁决未点名，按「与 base 同法」删该行；(b) b275 / b279 fork README 的代码图表行首列是 `（未建页）` 而非 `pages/codegraph.html`，按行内容定位删除；(c) b358 README `:16` 列举镜像页的括号去掉 `pages/codegraph.html` 一项、`:58` 「未复制 data/ … 与 pages/codegraph.html」一句整句删（对象已不存在）。mobile-task.html 的 `go vet` 输出行按裁决改命令后、原 mock 输出 `fails=1 over-budget…` 换成 `(无输出)` 以与命令自洽。

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

### 第 7 步：文档、原型与 roadmap

- `git rm docs/codegraph-scan-recipe.md && git rm -r deploy/workflows && git rm prototypes/base/pages/codegraph.html prototypes/base/data/graph.json prototypes/base/data/graph.js` → exit 0；`deploy/` 剩 `handoff-agentd.service`、`install-redirect/`。
- `prototypes/base/index.html:280` 删 dock 钮一行；`README.md:26` 删表格 `pages/codegraph.html` 行，`:69-79` 删「代码图页的形态基准…」整段。`grep -n -i 'codegraph\|代码图\|graph\.js' prototypes/base/{README.md,index.html,design-qa.md}` → exit 1（零命中）。
- `docs/roadmap.md` 按 plan 第 7 步表格逐条处置（以 main 现状行号为锚，脚本逐行 assert 首词后再删）：
  - 删整节：`## 代码图数据债` 的标题与三条图数据项（`baseline.json 逐符号真重扫`、`真重扫的验收判据要逐容器点数`、`清掉陈旧域声明 d_orchestration.json`）；`## 来自 C1.7 finish 的基准回灌` 全节（两条）；`## 来自 B432 真机验收（图覆盖债）` 全节（两条）。
  - 删单条 29 处：`mobilecore 图覆盖债`、`go run … codegraph 认成 codegraph 查询`、`图覆盖债：Store.OpenTickets`、`契约预算未棘轮`、`codegraph check 仍 6 红`、`codegraph validate ~130`、`其余入传输边的 entries 复核`、`d_workspace→d_orchestration 预算清零`、`5 条 over-budget 待棘轮`、`cards-B233.4 视图失效边`、`完整图重扫未做 / absorb 拒收`、`taskProfileHome 图覆盖债`、`TestRepoContractGate dead-interface`、B231 节四条（`符号级漏建残余 33 个`、`6 个零节点容器`、`6 个非 card 族 CLI 父入口仍标 unscanned`、`基线合并即滞后`；保留 `终态事件丢失第二次实测`）、C1.9 节 `原型基准站 codegraph 页…漂移`（该节只剩标题，标题一并删）、C1.11 节三条（`声明覆盖停在 2/23`、`d_workspace.json 职责句`、`前端消费 decls`）、B227 节 `执行机上装 codegraph`、B249 执行过程节 `contract 节点给 target.json 写 entries 用了函数符号名`、B282 finish 节 `best.json 未声明 k_web_api_scheduling`、B393/B395 节 `图视图命名与分支不一致`、`diff 烙基线指纹` / `codegraph CLI 内建扫描命令` / `重扫扫描配方的边提取保真度` 三条——plan 标为「B383 节」，实际位于 `## 来自 B379 spec（2026-10-02）` 节（按首行关键词定位命中，节随之只剩标题，标题一并删）。
  - 改词 3 处：`推平 … 五份纪律块` → 去 `recon`、「五份」「这五份」→「四份」；`跨节点事实不继承` 条示例改为「本机工具链事实『某二进制在 PATH』」；C1.11 `两条真机项由 CI 结论兜底` 删 (a) 保留 (b)，标题去「两条」。
  - 新增 `## 来自 B436 finish（2026-10-18，代码图消费侧删除）` 节于文末，首条 `charter-recon v3 / charter-story-recon v1 待下架`。
  - 文件 988 行 → 878 行。空节检查（awk 扫 `##` 之间无非空行）→ 无。
  - 残余命中（均属 plan「保留」：主语非代码图或代码图仅作佐证）：`:180,:186` `全仓 internal/ 按 best.json 重排`；`:188,:231` 验收节标题里的「合线 absorb」；`:762,:768` B395 条目里的「图对账轮」时点；`:863` 2026-10-15 条目尾句「代码图已回灌基线」。
- **待裁决（plan 决策权限：上表没点名、但疑似应删的条目——列出来，不动）**：
  1. `docs/roadmap.md:185` `- **非 12–16 方向的老债配额**：d_gateway → d_ledger 17、d_cli → d_policy 32 等本卡不清零。棘轮只覆盖 12–16 交出的有门面方向。`——主语是 `target.json` 的 legacyBudget 配额，与被删的「预算棘轮」族同源；plan 表未点名，按表保留。
  2. `docs/roadmap.md` `## 来自「修图」实测（2026-08-24，B233 前置排查）` 整节（现 `:398-420` 一带，五条：容器粒度假读数 / 阴性结果一二 / 探测器不能当裁判 / …）——全节讨论的是代码图自身的粒度与归属读数（`best.json 不该动`），但 plan 表「保留」且其结论「B232 是 B233 的前置」仍是架构排序依据；按表保留。
  以上两项不影响第 8、9 步（纯文档），故继续执行而非中断回合；交回报告中原文列出。
- `CHANGELOG.md` `[Unreleased]` 在 `### 变更` 之后、`### 修复` 之前新增 `### 移除` 小节一条（plan 原文）。

### 第 8 步：依赖收口

- `go mod tidy` → exit 0；`(cd desktop && go mod tidy)` → exit 0。`git diff --stat go.mod go.sum desktop/go.mod desktop/go.sum` → `desktop/go.sum | 2 --`、`go.sum | 2 --`，共 4 行删除：恰为两个文件里 `github.com/Xsxdot/charter/graph v0.10.2` 的 h1 与 go.mod 两行；`go.mod` / `desktop/go.mod` 无变化，无 indirect 连带。

### 第 9 步：全量与取证

- `npm --prefix web ci` 已在第 6 步完成。执行环境：macOS，Go 1.26.1，shell 内 `grep` 为 Claude Code ugrep 垫片（见偏离 10），`CLAUDE_CODE_SESSION_ID` 在环境中（见偏离 11）。
- **CMD1** `go build ./... && go vet ./... && go test ./... -count=1`
  - 字面第一跑（与 CMD3/CMD5 并发）→ **exit 1**；63 包 ok，唯一 FAIL：`--- FAIL: TestSessionSendActorResolution` / `session_test.go:1252: 席位形态: got="" err=当前会话出示失败：--cli/--session 与当前环境会话不一致，请使用当前会话的一对或去掉环境来源`（`FAIL github.com/Xsxdot/handoff/cmd 56.532s`）。
  - 单测复现：`go test ./cmd/ -run TestSessionSendActorResolution -count=1` → FAIL 同上；`env -u CLAUDE_CODE_SESSION_ID go test ./cmd/ -run TestSessionSendActorResolution -count=1` → `ok 0.398s`。
  - 权威重跑（无并发）`env -u CLAUDE_CODE_SESSION_ID sh -c 'go build ./... && go vet ./... && go test ./... -count=1'` → **exit 0**，64 包 ok、7 包 no test files、零 FAIL。原始输出 `$TMPDIR/b436-s9-cmd1.txt`、`b436-s9-cmd1-rerun.txt`。
- **CMD2** `test -z "$(gofmt -l $(git ls-files '*.go'))"` → **exit 0**（第 8 项注释改后再跑一次仍 0）。
- **CMD3** `GOOS=windows GOARCH=amd64 go build ./... && GOOS=windows GOARCH=arm64 go build ./... && GOOS=windows GOARCH=amd64 go vet ./...` → **exit 0**。
- **CMD4** `(cd desktop && go mod tidy && git diff --exit-code -- go.mod go.sum)` → **exit 0**。
- **CMD5** `npm --prefix web run typecheck && npm --prefix web test && npm --prefix web run build && test -f web/dist/index.html`
  - 第一跑（与 CMD1/CMD3 并发）→ **exit 1**：`Test Files 2 failed | 144 passed`、`Tests 2 failed | 1941 passed`——`Shell.test.tsx > 仅新鲜完整 latest 过滤 task…` `Test timed out in 5000ms`（5187ms）、`FileTab.test.tsx > 冲突（保存 409）报 conflict…` 时序断言；build 未执行。
  - 单独重跑 → **exit 0**：`Test Files 146 passed (146)`、`Tests 1943 passed (1943)`、`✓ built in 1.55s`、`web/dist/index.html` 1062 字节在场。原始输出 `$TMPDIR/b436-s9-cmd5.txt`、`b436-s9-cmd5-rerun.txt`。
- **CMD6** `grep -rIn -E 'codegraph|charter/graph|图对账|\brecon\b' --exclude-dir=.git --exclude-dir=node_modules --exclude-dir=.claude --exclude-dir=dist . | grep -v -E '^\./(docs/|CHANGELOG\.md)'`
  - 用 shell `grep`（ugrep 垫片）字面跑 → 输出非空，但全部是 `docs/**` 与 `CHANGELOG.md`（路径无 `./` 前缀致过滤失效）+ 两处真命中：`internal/agentd/b23317_boundary_test.go:115`（偏离 8，已改）、`mobile/go.sum:1-2`（偏离 9，已 tidy）。改后以 `grep -v -E '^(\./)?(docs/|CHANGELOG\.md)'` 过滤 → **零输出**（管道 exit 1）。
  - 用 `/usr/bin/grep`（BSD）字面跑（plan 过滤正则按 `./` 前缀生效）→ **非空**：`docs/**`、`CHANGELOG.md` 之外剩 **24 个文件，全部在 `prototypes/` 八个 fork 目录**（见待裁决 1 清单）。`internal/`、`cmd/`、`web/`、`scripts/`、`deploy/`、`mobile/`、`desktop/`、`prototypes/base/` 零命中。
- **S2 真机替代**：`go build -o "$TMPDIR/handoff" . && "$TMPDIR/handoff" agentd --help` → exit 0，打印「启动 agentd 服务（HTTP API + WS 事件流）」用法。`/api/projects/x/codegraph` 404 与启动日志归协调者最终验收。
- **反例对照**（CMD1 权威跑内全绿）：`TestRootRejectsDeletedGraphCommand`、`TestB23317NestedInternalGuardHasTeeth`、`TestB23317HandlerGuardHasTeeth`、`TestPointerRouteAbsentFromSource`；`ProjectTree.test.tsx` cards/edit 菜单用例在 CMD5 权威跑内绿。

### 待裁决（原样未动，交协调者）

1. **`prototypes/` 八个 fork 目录的代码图残留（S5 判据未闭合）**：`b264-tab-groups`（README.md、index.html dock 钮、`pages/codegraph.html`）、`b275-frontend-proto`（README.md）、`b279-automation-proto`（README.md）、`b285-workbench-polish`（README.md、index.html、`pages/codegraph.html`）、`b288-workbench-ux`（index.html 两处、`pages/codegraph.html`）、`b292-squad-concurrency`（`data/graph.js`、`data/graph.json`、index.html、`pages/codegraph.html`）、`b358-session-groups`（README.md、index.html、`pages/sessions.html` 与 `sessions-v2.html` 里 mock 聊天文本提到 `internal/agentd/codegraph.go`）、`mobile-app`（README.md 表行与形态段、index.html dock 钮、`data/graph.js`、`data/graph.json`、`pages/codegraph.html`、`pages/mobile-task.html` mock 终端 `codegraph check` 与 `codegraph/target.json`）。其中 9 份是 base 代码图页/数据的拷贝，合计 4.3 MB。spec/plan 阶段的 grep 经 ugrep 垫片未见到这些目录。两条路：(a) 扩 B436 范围照 `prototypes/base` 同法清理（删拷贝页/数据、dock 钮、README 行段、mock 文本改词）；(b) 视为与 `docs/` 同类的历史形态权威副本，把 `prototypes/` 加进 S5 允许清单。按协调者本机 shell `grep` 跑 S5 会直接通过（垫片跳过 `prototypes/*`），按 `/usr/bin/grep` 跑则 24 文件命中。
2. **`docs/roadmap.md:185`** `- **非 12–16 方向的老债配额**：d_gateway → d_ledger 17、d_cli → d_policy 32 等本卡不清零。棘轮只覆盖 12–16 交出的有门面方向。`——主语是 `target.json` legacyBudget 配额，与已删「预算棘轮」族同源；plan 表未点名，按表保留。
3. **`docs/roadmap.md` `## 来自「修图」实测（2026-08-24，B233 前置排查）` 整节**（五条：容器粒度假读数 / 阴性结果一、二（`best.json 不该动`）/ 探测器不能当裁判 / …）——全节是代码图自身粒度与归属的读数；plan 表「保留」，且其结论「B232 是 B233 的前置」仍是架构排序依据。按表保留。
4. **`TestSessionSendActorResolution` 对 `CLAUDE_CODE_SESSION_ID` 环境敏感**（`cmd/card_seat.go:68`）：在 Claude Code 会话内跑 `go test ./cmd/` 必红，CI 与裸 shell 不受影响。非 B436 引入、未改。是否记 roadmap「测试隔离」项、以及 CMD1 的权威读数是否以 `env -u` 跑为准，归协调者。

### 协调者裁决后的补做（2026-10-18）

裁决原文四条：(1) prototypes 八个 fork 选 (a) 同法清理；(2) roadmap「非 12–16 方向的老债配额」删；(3) roadmap「来自『修图』实测」整节删；(4) `TestSessionSendActorResolution` 不修、CMD1 以 `env -u` 读数为准、roadmap「来自 B436 finish」追加一条。

- (1) `git rm` 9 份拷贝：`prototypes/{b264-tab-groups,b285-workbench-polish,b288-workbench-ux,b292-squad-concurrency,mobile-app}/pages/codegraph.html`、`prototypes/{b292-squad-concurrency,mobile-app}/data/graph.{js,json}` → exit 0。六个 fork `index.html` 删 dock「代码图」钮行（mobile-app / b358 / b288 / b292 / b264 / b285）；b264 / b285 / b288 另删项目行 JS 字符串钮（偏离 16a）。五个 fork README 删表格行与「代码图页的形态基准…」整段（mobile-app / b279 / b275 / b264 / b285；b275 / b279 的表行首列为 `（未建页）`，偏离 16b）；b358 README 两句上下文（偏离 16c）。b358 `sessions.html` / `sessions-v2.html`：`internal/agentd/codegraph.go` → `internal/agentd/handlers.go`、「A 保留 codegraph.go 为第二组装点」「codegraph.go 同为组装点」同步改名。mobile-app `mobile-task.html`：`codegraph check 2>&1 | head -8` → `go vet ./... 2>&1 | head -8`（mock 输出改 `(无输出)`）、`📄 codegraph/target.json — 冲突现场` → `📄 go.mod — 冲突现场`。
  `/usr/bin/grep -rIl -E 'codegraph|charter/graph|图对账|\brecon\b' prototypes` → exit 1，**零输出**；`/usr/bin/grep -rIn -E '代码图|graph\.js|graph\.json' prototypes` 亦零输出。
- (2) `docs/roadmap.md` 删 `- **非 12–16 方向的老债配额**…` 一行（B233.17 spec 节）。
- (3) `docs/roadmap.md` 删 `## 来自「修图」实测（2026-08-24，B233 前置排查）` 整节（标题至下一 `## ` 前，含五条）。空节检查 → 无；878 行 → 858 行。
- (4) 测试未动；`## 来自 B436 finish` 节追加裁决原文一条。
- 第 9 步重跑（补做后，S5 用 `/usr/bin/grep`）：
  - CMD2 `test -z "$(gofmt -l $(git ls-files '*.go'))"` → **exit 0**。
  - CMD3 `GOOS=windows GOARCH=amd64 go build ./... && GOOS=windows GOARCH=arm64 go build ./... && GOOS=windows GOARCH=amd64 go vet ./...` → **exit 0**。
  - CMD4 `(cd desktop && go mod tidy && git diff --exit-code -- go.mod go.sum)` → **exit 0**。
  - CMD6 `/usr/bin/grep -rIn -E 'codegraph|charter/graph|图对账|\brecon\b' --exclude-dir=.git --exclude-dir=node_modules --exclude-dir=.claude --exclude-dir=dist . | /usr/bin/grep -v -E '^\./(docs/|CHANGELOG\.md)'` → **零输出（0 行）**。
  - CMD1 `env -u CLAUDE_CODE_SESSION_ID sh -c 'go build ./... && go vet ./... && go test ./... -count=1'`（裁决 4：以 `env -u` 读数为准）→ **exit 0**，64 包 ok、零 FAIL。原始输出 `$TMPDIR/b436-s9b-cmd1.txt`。
  - CMD5 `npm --prefix web run typecheck && npm --prefix web test && npm --prefix web run build && test -f web/dist/index.html`（与 CMD1 串行、不并发）→ **exit 0**，`Test Files 146 passed (146)`、`Tests 1943 passed (1943)`、`✓ built in 1.61s`、`web/dist/index.html` 1062 字节。原始输出 `$TMPDIR/b436-s9b-cmd5.txt`。
- 六条全 0，S5 零行；`git status --short` 干净后提交。

## 2026-10-18 review 与最终验收（协调者会话）

### review（独立 subagent，只读）

- 裁决 **pass（附条件）**：五故事复跑全绿（CMD1 `env -u CLAUDE_CODE_SESSION_ID` 64 包 ok；gofmt 0；Windows amd64/arm64 build + vet 0；desktop tidy 0；web 146 文件 / 1943 用例、dist 1062 B；S5 `/usr/bin/grep` 零输出；go.mod/go.sum×4 零命中）。变异复验：`b23317AssemblyFiles` 名单加入 `internal/agentd/handlers.go` → `TestB23317AssemblyFilesHaveNoHandlerDefs` 转红（报 21 个 handle*），还原回绿；对照 `TestRootRejectsDeletedGraphCommand` PASS、ProjectTree cards/edit 菜单用例 ✓。禁区 `git diff --exit-code` 三份历史文档 exit 0。
- 三条已披露越权交协调者：④ mobile-nav-note 文案去「代码图」、⑥ roadmap「被依赖子系统的实现迁进标准结构」条保留、⑨ `mobile/go.sum` tidy。协调者裁决：三条全部接受（入口没了文案必须跟；主语非代码图；go.sum 非源码），已落卡 timeline（seq 24795）。
- Minor 1：`internal/hostapi/hostapi.go:6`、`internal/ledgerstep/discipline_wiring_test.go:3`、`discipline_passthrough_test.go:4` 以「代码图扫描 / 不进代码图」为论据——finish 文档对齐改写（本提交），`go build`/`go vet` 两包 ok、gofmt 空。Minor 2：执行者遇待裁决未按 plan 结束回合，无实害，记此。
- 旁证：`charter:acceptance` skill 已不在本机 skill 列表（Skill 工具报 Unknown skill），验收纪律按 `charter:review`「验收：行为事实只能跑出来」节执行。

### 最终验收真机（协调者，被验对象身份先钉）

- 二进制：`go build -tags embedweb -o $TMPDIR/handoff-b436e .`（worktree HEAD `9011105a` + 本节三处注释改动；`internal/webui/dist` 临时从 `web/dist` 拷入后删除，不入库），sha256 前 16 位 `33b9ed1616fd9e6e`；`/usr/bin/grep -c codegraph` 该二进制 = **0**，对照本机旧 `~/.local/bin/handoff` = 32。
- 隔离 agentd：config `listen 127.0.0.1:7799`、独立 datadir/repo_root、`--executor fake`、无 ledger、无 targets。`GET /api/status`（Bearer）200。
- **S2**：`GET /api/projects/x/codegraph`（Bearer）→ `HTTP/1.1 404 Not Found`，`Content-Type: text/plain`，body `404 page not found`（ServeMux 原生裁决）；对照既有路由 `GET /api/projects/x/branches` → 404 `application/json`（业务 404）。无 Bearer → 401。`/codegraph/source` → 404。`GET /codegraph/app/` 与 `GET /` 响应体 `cmp` **IDENTICAL**（回落 handoff 根 SPA）。`data/agentd.log` 2 行，`grep -c -E '代码图|viewer'` = **0**。
- **S1**：内置浏览器打开 `handoff console --print-url` 的兑换 URL；1400×900 桌面宽度下 `find "代码图"` 无匹配，dock 按钮枚举为 工作项 / 任务看板 / 流程 / 工单 / 设置 / home 基准终端；直访 `/codegraph?project=x` → `find "iframe"` 无匹配，页面文本为工作台「请从左栏选择项目或目录」。截图两张见会话。
- 收尾：`pkill -f handoff-b436e`，隔离 datadir 留在 `$TMPDIR/b436-agentd`。
