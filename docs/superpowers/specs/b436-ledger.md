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
