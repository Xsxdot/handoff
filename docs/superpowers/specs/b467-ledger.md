# B467 台账

## spec

- 2026-10-23 熟练工 code-investigate（基线 8a31ad33）结论：Q1 key 路径只解析 usage 窗口、仓内无 200 样本（需真机）；Q2 只有 `opencode auth logout <provider>` 可按条目删，codex/grok/claude 登出清整份，agy 无登出；Q3 弹层 `.up-main` 376px、内容区 329px、三窗口最小 334px → 月换行；「使用详情」`StatusBar.tsx:238`，底栏名 `:214`。
- 2026-10-23 §探测 mac-pro 主 HOME `auth.json` 条目 `opencode-go`（type api），只打印键名与类型的脚本：
  - 第 1 次 Python 缺省 UA → `status 403`，Cloudflare 错误体（`.cloudflare_error` 等），无效样本。
  - 第 2 次 UA 改 `Go-http-client/1.1`（与 handoff 的 Go 客户端一致，`internal/quota/opencodego.go` 未设 UA）→ `status 200`，键：`.usage.rolling.{status str, percent int, resetsAt str}`、`.usage.weekly.{同}`、`.usage.monthly.{同}`。无账号/组织/邮箱字段 → 第 4 条不做。
- 2026-10-23 原型 fork `prototypes/b467-accounts/`（自 base 8a31ad33，不入库）：账号卡「删除」→「忽略」、页底「已忽略」折叠区与恢复、底栏去名字（title 悬停）、弹层去「使用详情」、`.up-main` 376→392px（原型取值，实现由 plan 定）。
- 2026-10-23 原型浏览器核验（browser-worker，Chrome 经 127.0.0.1 临时静态服务）：8/8 符合——每卡「忽略」无「删除」；已忽略区收起/展开/恢复；被引用账号拒绝文案；忽略后底栏与弹层消失；底栏段内无名字（title 有）；弹层只剩「管理账号…」；.up-main 392px 时 og-team 三窗口 offsetTop 同为 121；settings 页无 error。
- 2026-10-23 用户批准 spec r1（「两条都按推荐，批准，cx-0jr3 删掉」）；B464 残余 cx-0jr3 已删、账号 codex:858e29a5d44e DELETE 200。

## plan

- 2026-10-23 plan r1 出稿（plan subagent，基线 0c71baef；产物 `docs/superpowers/plans/b467-plan.md`）。读数如下，全部本人读码核实。
- 账号定义行写者（定 Q1 的依据）：`internal/scheduling/accounts/accounts.go:68-103` `Service.Put`（`next := cur` 整行 CAS）；`discover.go:194-225` `ensureAccount`（expect=0 新建）；`discover.go:230-258` `backfillLabel`（现值无 label 时整行 CAS，冲突跳过）。`grep 'Put(KindAccount'` 仅此三处。`Account` 结构 `types.go:53-65`，kind 常量 `types.go:20-25`。
- 账本 registry 通用 KV、无 kind 白名单、不解释 body：`internal/ledger/registry.go:1-6,31-77`；门面 `internal/ledger/api/registry.go:29-58` 纯透传。B464 新增 `account_login`/`discovery_run` 两个 kind 时未改账本，先例成立。
- 读面：`accounts.go:217-243` Get、`:246-289` List、`:294-325` newView（无可探登录 → fail/no_login 改写在 `:322-324`）、`:350-366` ProbeLogin、`:584-606` Assessment/Assess。删除 `delete.go:61-98`（引用 → `*ReferencedError`），引用注入 `delete.go:37` ← `admission.go:174 LoginReferrers`，组装 `cmd/agentd.go:850-854`。
- API：路由 `internal/agentd/account_api.go:64-71`；`AccountsClient` `:42-52`；PUT 可改字段 `proto/scheduling.go:371-376` + `account_api.go:260-275`；投影 `account_api.go:379-411` → `proto/scheduling.go:211-234`；立即探测 `account_api.go:284-375`（na 判 `:296-301`，可探登录所在机 `:302-314`）；拒删体 `proto/scheduling.go:342-345`。契约 fixture `internal/proto/contract_fixture_test.go:36,71,1004-` → `web/src/api/testdata/AccountsResp.json` → `web/src/api/contract.test.ts:931-`。
- 探测器：`internal/agentd/accountwork/prober.go:156-197` scan（跳 na、`probeLoginOf` `:200-211`、本机且到期），扫描周期 30s `:36`；`ProbeNow` `:276-328`；哨兵 `:45-55`；`ProberAccounts` `:65-70`。
- 编制域：`PutCarrier` `internal/scheduling/scheduling.go:237-`，`checkCarrierLogin` `:341-372`；唯一调用方 `internal/agentd/schedapi.go:111-148`（`:144`）。准入 `admission.go:102-145`（登录/账号不存在放行 `:111-114,125-130`，先例注释 `:99-100`）。存量迁移 `migrate.go:43-82/85-105`，`writeMigration` `:142-166` 直接 `repo.Put` 不经 `PutCarrier`；每轮发现后调用 `cmd/agentd.go:878-884` → Q7 门禁绕过点。
- CLI：`cmd/account.go:53-61`（--json 直接编码 AccountView）、`:157-159` 状态词表、`:169-189` 表格；`internal/client/accounts.go:35-50` ProbeAccount 非 200 → httpError（F12 退出 1）。
- 前端：`web/src/app/accounts/store.ts:22-30,42-47,53-70,112-114`；`AccountsPage.tsx:101-144` DeleteDialog、`:204` 删除按钮、`:317-343` 分组与对话框挂载；`MobileAccounts.tsx:49-79`；`StatusBar.tsx:11` 头注、`:99` sorted、`:214` 底栏名、`:238` 使用详情、`:239` 管理账号；`shell/Shell.tsx:1842` 唯一 onManage 路由（去「使用详情」不改 Shell）；`settings/SchedulingPage.tsx:191-194` loginsOn、`:492,502-509` 下拉与 keep；`LoginWizard.tsx:216-218` 识别结果行。
- 弹层 CSS：`accounts.css:71` `.up-main` 376px、`:85,87` 行边距 25+8、`:91` `.q-inline` gap 14、`:95-98` `.qi`（label≥12 + bar 50 + pct≥30 + 两段 gap 5 = 102）；内容区 376−2−12−33 = 329 < 334（3×102+2×14）。392 → 345。`.sb-seg .sb-name` `:55`。
- 文档涉及：`README.md:429`、`README.zh-CN.md:245`、`skills/handoff/SKILL.md:131-132`。无原生移动端消费 `/api/accounts`（`mobile/` 是 webview 薄壳，grep 无命中）。
- 生产只读读数（`handoff account list --json`，版本 50333816e846）：13 个账号，定义行全部 version=1；`opencode-go-54ce`（spec 写作 opencode-54ce，SC2）kind opencode-go、state fail、2 条登录（linux-01、macbook-pro）、无载体引用。
- 基线测试：`go test ./internal/scheduling/accounts/ ./internal/agentd/accountwork/ ./internal/proto/` ok；`go test ./internal/agentd/ -run 'Account|Login|B453|B464'` ok；`go test ./cmd/ -run Account` ok；`npx vitest run src/app/accounts src/api/contract.test.ts src/app/settings/SchedulingPage.test.tsx` 7 文件 144 例全过。
- 放弃方案：忽略字段放 `Account` body + 全队同批换版（旧二进制三处写者整行写回会抹掉，无测试能红；三台升级不原子）；字段放登录行（忽略是账号级）；复用 `PUT /api/accounts/{ref}` 带 `ignored`（混两种 CAS、缺席/false 歧义）；新增 `state=ignored`（多处 switch 要登记）；前端逐消费点 filter（漏一处即 bug，改在 store 分区）；收 `.q-inline` gap（余 1px 且被复用）；删除连带删忽略行（删除→重建即抹掉忽略）。

## implement 段 A

- 2026-10-23 段 A 执行（基线 `621c0465`，分支 `cards/B467-charter-1`；只改 plan §6 段 A 允许文件集，未碰 `web/src/app/**`）。
- `web/node_modules` 开工时缺席，先跑 `npm ci --no-audit --no-fund`（290 packages，1s）；未改 package-lock。
- 先红→绿（每步都先看到断言红再实现）：
  - 账号域：`TestIgnoreMarkLifecycleAndDiscoveryNeverUndoesIt` 初跑编译红（缺 `IgnoreMark/KindIgnore/Ignore`），空壳后断言红（`忽略后读面应带 at/by: <nil>`）；实现后绿。锁：忽略行与定义行分离、忽略后 `ProbeLogin` 空且 `State` 不被改成 fail、被引用拒并含「不能忽略」、忽略/恢复幂等、发现（新登录/登录 gone/补 label）、改名、删除重建都不改忽略、坏行按已忽略。
  - 编制域：三支初跑红（`载体 c 不可用：账号 opencode-go-1111 低额度` 等）；实现 `checkCarrierLogin` 拒绝、迁移剔除、准入放行后绿。
  - 探测器：初跑编译红（缺 `ErrIgnored`），加哨兵后断言红（`得 账号没有可用登录…`）；实现后绿（已忽略到期不调 quota、`ProbeNow` 返 `ErrIgnored`、恢复后 scan 探到）。
  - API/wire：初跑编译红（proto 无 `ignored`）；加类型后断言红（`忽略: 404`）；实现两条路由、probe 409、投影后绿。夹具经 `-update` 重生成 `web/src/api/testdata/AccountsResp.json`。
  - 前端契约：`AccountView.ignored 可缺席` 先红（`expected undefined to be defined`），补 TS 类型/调用与夹具后 70/70 绿。
  - CLI：先红（表格无「已忽略」），`renderAccountTable` 覆盖状态列后绿。
- 自主决定（均在 plan §9 授权内）：`ReferencedError` 加 `Action` 字段（删除为空、忽略为「忽略」）复用同形 409；`Assessment.Ignored` 用 bool；`Get` 单读忽略行、`List` 一次 `List(account_ignore)`；迁移剔除后为零且剔除前非零走规则 4 + 原因「同 HOME 的兼容登录属已忽略账号」；探测器 `scan` 显式跳过 `Ignored`（不只依赖 `ProbeLogin` 空）。

段 A 验收命令原始输出（2026-10-23，本工作树）：

```
$ gofmt -l internal cmd
[exit=0]
$ go vet ./...
[exit=0]
$ go test ./internal/scheduling/... ./internal/agentd/... ./internal/proto/... ./cmd/...
ok  	github.com/Xsxdot/handoff/internal/scheduling	277.6s
ok  	github.com/Xsxdot/handoff/internal/scheduling/accounts	0.045s
ok  	github.com/Xsxdot/handoff/internal/scheduling/internal/logging	0.002s
ok  	github.com/Xsxdot/handoff/internal/agentd	277.772s
ok  	github.com/Xsxdot/handoff/internal/agentd/accountwork	0.372s
ok  	github.com/Xsxdot/handoff/internal/proto	0.011s
ok  	github.com/Xsxdot/handoff/cmd	140.416s
[exit=0]
```

```
$ cd web && npx vitest run src/api/contract.test.ts && npx tsc -b
 Test Files  1 passed (1)
      Tests  70 passed (70)
[exit=0]
$ npx tsc -b
[exit=0]
```

```
$ go test ./...
ok  	github.com/Xsxdot/handoff	0.535s
ok  	github.com/Xsxdot/handoff/cmd	(cached)
ok  	github.com/Xsxdot/handoff/internal/agentd	260.200s
ok  	github.com/Xsxdot/handoff/internal/agentd/accountwork	(cached)
ok  	github.com/Xsxdot/handoff/internal/approval	(cached)
ok  	github.com/Xsxdot/handoff/internal/approval/internal/decision	(cached)
ok  	github.com/Xsxdot/handoff/internal/buildinfo	(cached)
ok  	github.com/Xsxdot/handoff/internal/client	(cached)
ok  	github.com/Xsxdot/handoff/internal/collab	(cached)
?   	github.com/Xsxdot/handoff/internal/collab/client	[no test files]
?   	github.com/Xsxdot/handoff/internal/collab/cursor	[no test files]
ok  	github.com/Xsxdot/handoff/internal/collab/room	(cached)
ok  	github.com/Xsxdot/handoff/internal/config	(cached)
ok  	github.com/Xsxdot/handoff/internal/diag	(cached)
ok  	github.com/Xsxdot/handoff/internal/discipline	(cached)
ok  	github.com/Xsxdot/handoff/internal/dropdir	(cached)
ok  	github.com/Xsxdot/handoff/internal/envfile	(cached)
ok  	github.com/Xsxdot/handoff/internal/executor	(cached)
ok  	github.com/Xsxdot/handoff/internal/executor/agy	(cached)
ok  	github.com/Xsxdot/handoff/internal/executor/claudecode	(cached)
ok  	github.com/Xsxdot/handoff/internal/executor/codex	(cached)
ok  	github.com/Xsxdot/handoff/internal/executor/fake	(cached)
ok  	github.com/Xsxdot/handoff/internal/executor/grok	(cached)
ok  	github.com/Xsxdot/handoff/internal/executor/opencode	(cached)
ok  	github.com/Xsxdot/handoff/internal/executor/rawtap	(cached)
ok  	github.com/Xsxdot/handoff/internal/executor/turn	(cached)
ok  	github.com/Xsxdot/handoff/internal/hostapi	(cached)
ok  	github.com/Xsxdot/handoff/internal/initflow	(cached)
?   	github.com/Xsxdot/handoff/internal/keysclient	[no test files]
ok  	github.com/Xsxdot/handoff/internal/keystone	(cached)
ok  	github.com/Xsxdot/handoff/internal/launcher	(cached)
ok  	github.com/Xsxdot/handoff/internal/ledger	(cached)
ok  	github.com/Xsxdot/handoff/internal/ledger/api	(cached)
ok  	github.com/Xsxdot/handoff/internal/ledgermirror	(cached)
ok  	github.com/Xsxdot/handoff/internal/ledgerstep	(cached)
ok  	github.com/Xsxdot/handoff/internal/localsync	(cached)
ok  	github.com/Xsxdot/handoff/internal/logx	(cached)
ok  	github.com/Xsxdot/handoff/internal/mobilecore	(cached)
ok  	github.com/Xsxdot/handoff/internal/orchestration	(cached)
?   	github.com/Xsxdot/handoff/internal/orchestration/internal/cacheplan	[no test files]
ok  	github.com/Xsxdot/handoff/internal/p2p	(cached)
?   	github.com/Xsxdot/handoff/internal/p2p/p2ptest	[no test files]
ok  	github.com/Xsxdot/handoff/internal/pathenv	(cached)
ok  	github.com/Xsxdot/handoff/internal/permgate	(cached)
ok  	github.com/Xsxdot/handoff/internal/prochost	(cached)
ok  	github.com/Xsxdot/handoff/internal/projectid	(cached)
ok  	github.com/Xsxdot/handoff/internal/proto	(cached)
ok  	github.com/Xsxdot/handoff/internal/proxycfg	(cached)
?   	github.com/Xsxdot/handoff/internal/ptyapi	[no test files]
ok  	github.com/Xsxdot/handoff/internal/ptyhost	(cached)
ok  	github.com/Xsxdot/handoff/internal/ptyhost/engine	(cached)
ok  	github.com/Xsxdot/handoff/internal/ptyhost/hostproc	(cached)
ok  	github.com/Xsxdot/handoff/internal/ptyhost/sessdir	(cached)
ok  	github.com/Xsxdot/handoff/internal/ptyhost/wire	(cached)
ok  	github.com/Xsxdot/handoff/internal/ptytestroot	(cached)
ok  	github.com/Xsxdot/handoff/internal/quota	(cached)
ok  	github.com/Xsxdot/handoff/internal/relay	(cached)
ok  	github.com/Xsxdot/handoff/internal/release	(cached)
?   	github.com/Xsxdot/handoff/internal/schedclient	[no test files]
ok  	github.com/Xsxdot/handoff/internal/scheduling	(cached)
ok  	github.com/Xsxdot/handoff/internal/scheduling/accounts	(cached)
ok  	github.com/Xsxdot/handoff/internal/scheduling/internal/logging	(cached)
ok  	github.com/Xsxdot/handoff/internal/selfupdate	(cached)
ok  	github.com/Xsxdot/handoff/internal/service	(cached)
ok  	github.com/Xsxdot/handoff/internal/skill	(cached)
ok  	github.com/Xsxdot/handoff/internal/store	(cached)
ok  	github.com/Xsxdot/handoff/internal/targetclient	(cached)
ok  	github.com/Xsxdot/handoff/internal/termseq	(cached)
ok  	github.com/Xsxdot/handoff/internal/testhttp	(cached)
ok  	github.com/Xsxdot/handoff/internal/testperm	(cached)
ok  	github.com/Xsxdot/handoff/internal/toolchain	(cached)
ok  	github.com/Xsxdot/handoff/internal/upgrade	(cached)
ok  	github.com/Xsxdot/handoff/internal/webui	(cached)
ok  	github.com/Xsxdot/handoff/internal/workspace	(cached)
ok  	github.com/Xsxdot/handoff/internal/workspace/internal/gitproc	(cached)
?   	github.com/Xsxdot/handoff/web/node_modules/flatted/golang/pkg/flatted	[no test files]
[exit=0]
```

- 环境敏感：本次全量绿，未触发 `CLAUDE_CODE_SESSION_ID` / 嵌套工作树敏感用例。


## implement 段 B

- 2026-10-23 段 B 执行（基线 `6d9dccbb`，分支 `cards/B467-charter-2`；只改 plan §6 段 B 允许文件集，未碰 `web/src/api/**` 与 Go 代码）。
- `web/node_modules` 开工时缺席，先跑 `npm ci --no-audit --no-fund`（290 packages，1s）；未改 package-lock。
- 先红→绿：
  - store 分区：新测试首跑红 `TypeError: Cannot read properties of undefined (reading 'map')`（`ignored` 尚不存在）；实现入口 `splitByIgnored` 与回执按 id 移动后绿（快照 accounts 排除已忽略、ignored 单列、忽略/恢复回执在两表之间移动）。
  - 底栏/弹层：首跑红 `AssertionError: expected 'og-main34% 2h41m·62% 4d 2h' not to contain 'og-main'`；去掉 `.sb-name`、段 `title` 放名字、菜单删「使用详情」、`.up-main` 376→392px 后绿。
  - 账号页：首跑红 `Unable to find an accessible element with the role "button" and name "忽略"`；`DeleteDialog`→`IgnoreDialog`（确认/拒绝文案与「去改载体」）、页底 `已忽略（N）` 折叠区与恢复、去掉 store `remove` 后绿。
  - 向导提示：首跑红 `expected ... to contain '已忽略，可在本页底部恢复'`；按 store `ignored` 判后绿。
  - 消费点回归（MobileAccounts / SchedulingPage）在 store 分区落地后补写，首跑即绿；其牙由分区本身支撑（取掉分区即红），按纪律留 review 变异复验，实现侧未自验。
- 自主决定（均在 plan §9 授权内）：`mergeAccount` 按 id（原按 name）移动，忽略/恢复与改名并存不串表；`IgnoreDialog` 不拿快照 `a.carriers` 预拦，一律走 API 的 409 再点名（服务端权威）；「去改载体」用 `<a href="/settings?section=automation">`（jsdom 可测、无需 Router 包裹）；移动端说明「登记、编辑和删除账号」改「登记、编辑和忽略账号」（原文案在界面无删除入口后失真）。
- 未验证：`.up-main` 392px 的实际布局换行与底栏形态只经 CSS/组件断言，真实渲染待主会话浏览器 R7；弹层与底栏未用 jsdom 量宽度（plan §10 假绿族）。

段 B 验收命令原始输出（2026-10-23，本工作树，`cd web && npx vitest run && npx tsc -b && npx eslint .`）：

```

 RUN  v4.1.10 /root/.handoff/worktrees/47973e2e/web

Not implemented: HTMLCanvasElement's getContext() method: without installing the canvas npm package

 Test Files  150 passed (150)
      Tests  2036 passed (2036)
   Start at  04:01:38
   Duration  17.91s (transform 18.29s, setup 17.53s, import 34.50s, tests 80.78s, environment 108.78s)

[vitest exit=0]
[tsc exit=0]

/root/.handoff/worktrees/47973e2e/web/src/app/board/BoardPage.tsx
  55:9  warning  The 'tasks' logical expression could make the dependencies of useMemo Hook (at line 86) change on every render. To fix this, wrap the initialization of 'tasks' in its own useMemo() Hook  react-hooks/exhaustive-deps

/root/.handoff/worktrees/47973e2e/web/src/app/cards/CardsPage.tsx
  176:129  warning  React Hook useMemo has an unnecessary dependency: 'sharedSource'. Either exclude it or remove the dependency array                        react-hooks/exhaustive-deps
  221:6    warning  React Hook useEffect has a missing dependency: 'sharedData'. Either include it or remove the dependency array                             react-hooks/exhaustive-deps
  221:7    warning  React Hook useEffect has a complex expression in the dependency array. Extract it to a separate variable so it can be statically checked  react-hooks/exhaustive-deps
  245:6    warning  React Hook useEffect has a missing dependency: 'cardsPoll'. Either include it or remove the dependency array                              react-hooks/exhaustive-deps

/root/.handoff/worktrees/47973e2e/web/src/app/cards/NewCardDialog.tsx
  26:17  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components

/root/.handoff/worktrees/47973e2e/web/src/app/data/usePreviews.ts
  152:18  warning  The ref value 'openingRef.current' will likely have changed by the time this effect cleanup function runs. If this ref points to a node rendered by React, copy 'openingRef.current' to a variable inside the effect, and use that variable in the cleanup function  react-hooks/exhaustive-deps
  153:15  warning  The ref value 'openRef.current' will likely have changed by the time this effect cleanup function runs. If this ref points to a node rendered by React, copy 'openRef.current' to a variable inside the effect, and use that variable in the cleanup function        react-hooks/exhaustive-deps

/root/.handoff/worktrees/47973e2e/web/src/app/files/FileTree.tsx
  243:6  warning  React Hook useEffect has a missing dependency: 'base'. Either include it or remove the dependency array. If 'setExpanded' needs the current value of 'base', you can also switch to useReducer instead of useState and read 'base' in the reducer  react-hooks/exhaustive-deps

/root/.handoff/worktrees/47973e2e/web/src/app/settings/UpdatePage.tsx
  72:9  warning  The 'machines' logical expression could make the dependencies of useEffect Hook (at line 97) change on every render. To fix this, wrap the initialization of 'machines' in its own useMemo() Hook  react-hooks/exhaustive-deps

/root/.handoff/worktrees/47973e2e/web/src/app/shell/Breadcrumb.tsx
  25:17  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components

/root/.handoff/worktrees/47973e2e/web/src/app/shell/Shell.tsx
   94:17  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components
  114:17  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components
  125:17  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components
  391:6   warning  React Hook useEffect has a missing dependency: 'wb'. Either include it or remove the dependency array                           react-hooks/exhaustive-deps

/root/.handoff/worktrees/47973e2e/web/src/app/task/codeText.tsx
  33:17  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components
  52:17  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components

/root/.handoff/worktrees/47973e2e/web/src/app/tree/MobileWorkspace.tsx
  80:7  warning  React Hook useMemo has a missing dependency: 'activeSessions'. Either include it or remove the dependency array  react-hooks/exhaustive-deps

/root/.handoff/worktrees/47973e2e/web/src/app/tree/ProjectTree.tsx
  214:17  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components
  233:17  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components
  287:17  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components
  302:17  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components
  323:17  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components
  339:17  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components
  367:17  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components

/root/.handoff/worktrees/47973e2e/web/src/app/workbench/BlankTab.tsx
  22:14  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components
  73:17  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components
  84:17  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components

/root/.handoff/worktrees/47973e2e/web/src/app/workbench/MobileKeybar.tsx
  33:14  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components

/root/.handoff/worktrees/47973e2e/web/src/app/workbench/TaskPickerDialog.tsx
  38:17  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components
  50:17  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components

/root/.handoff/worktrees/47973e2e/web/src/components/ui/badge.tsx
  41:17  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components

/root/.handoff/worktrees/47973e2e/web/src/components/ui/button.tsx
  57:18  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components

✖ 33 problems (0 errors, 33 warnings)

[eslint exit=0]
```

- 提交：`git commit -m "feat(B467): 段 B 控制台——store 分区、忽略/恢复、底栏去名与弹层、已忽略区"` 原始输出：`[cards/B467-charter-2 8131894b] feat(B467): 段 B 控制台——store 分区、忽略/恢复、底栏去名与弹层、已忽略区` / `13 files changed, 373 insertions(+), 78 deletions(-)`。本行随同批 amend 收入（amend 会换 hash，台账保留提交当时的 `8131894b`）。

## implement 修复轮 1

### 偏离与自主决定

无。五条只按本轮补充的范围修复（web/src/app/accounts、internal/scheduling/accounts、对应测试），未动 web/src/api/**，未改其他行为。

### 先红后绿

- I1：AccountsPage.test 新增/改写两条用例。改前 `IgnoreDialog` 初值 `[]`，第一条快照已知 carriers 时仍先出确认框：`Unable to find role="dialog" and name "不能忽略 og-team"`；第二条（快照 carriers 空 + 409 兜底）与 B-m5 守卫用例一并通过。改 `useState(a.carriers)` 后两条绿。
- A-m2：`TestIgnoreSurvivesConcurrentRestoreBetweenPutAndRead` 改前直接 panic，原文：`panic: runtime error: invalid memory address or nil pointer dereference ... accounts.(*Service).Ignore ... ignore.go:107`。改用本次写入的 `by/at` 局部值记日志后绿。
- B-m2：`ago('0001-01-01T00:00:00Z', NOW)` 改前 `739910 天前`；账号页已忽略行改前含 `739911 天前忽略`。`ago` 对 `t <= 0` 回「时间未知」后 model/组件两处绿。
- B-m6：LoginWizard 按名字判已忽略改为 `a.id === item.account`。用例把 `account_name` 改成 `hidden-renamed`、`account` 用真实账号 id，改前无「已忽略，可在本页底部恢复」，改后绿。
- B-m5：新增「快照里的已忽略账号只进『已忽略』区，不进六组分组」守卫用例；现有 store 分区下本就通过，去掉分区即会红。

修复后定点命令（本工作树）：`go test ./internal/scheduling/accounts/ -run 'TestIgnore|Test.*Ignore' -count=1` → `ok`；`npx vitest run src/app/accounts/AccountsPage.test.tsx src/app/accounts/model.test.ts` → `41 passed`。

### 收尾命令原始输出

```
$ gofmt -l internal cmd
$ go vet ./...
```

```
$ env -u CLAUDE_CODE_SESSION_ID go test ./internal/scheduling/... ./internal/agentd/... ./cmd/...
ok  	github.com/Xsxdot/handoff/internal/scheduling	24.083s
ok  	github.com/Xsxdot/handoff/internal/scheduling/accounts	0.040s
ok  	github.com/Xsxdot/handoff/internal/scheduling/internal/logging	0.002s
ok  	github.com/Xsxdot/handoff/internal/agentd	281.023s
ok  	github.com/Xsxdot/handoff/internal/agentd/accountwork	0.382s
ok  	github.com/Xsxdot/handoff/cmd	142.252s
```

```
$ npx vitest run
 RUN  v4.1.10 /root/.handoff/worktrees/0d876b87/web

Not implemented: HTMLCanvasElement's getContext() method: without installing the canvas npm package

 Test Files  150 passed (150)
      Tests  2040 passed (2040)
   Start at  04:24:32
   Duration  17.76s (transform 21.16s, setup 17.92s, import 38.13s, tests 80.16s, environment 107.43s)
```

```
$ npx tsc -b
```

```
$ npx eslint .
/root/.handoff/worktrees/0d876b87/web/src/app/board/BoardPage.tsx
  55:9  warning  The 'tasks' logical expression could make the dependencies of useMemo Hook (at line 86) change on every render. To fix this, wrap the initialization of 'tasks' in its own useMemo() Hook  react-hooks/exhaustive-deps

/root/.handoff/worktrees/0d876b87/web/src/app/cards/CardsPage.tsx
  176:129  warning  React Hook useMemo has an unnecessary dependency: 'sharedSource'. Either exclude it or remove the dependency array                        react-hooks/exhaustive-deps
  221:6    warning  React Hook useEffect has a missing dependency: 'sharedData'. Either include it or remove the dependency array                             react-hooks/exhaustive-deps
  221:7    warning  React Hook useEffect has a complex expression in the dependency array. Extract it to a separate variable so it can be statically checked  react-hooks/exhaustive-deps
  245:6    warning  React Hook useEffect has a missing dependency: 'cardsPoll'. Either include it or remove the dependency array                              react-hooks/exhaustive-deps

/root/.handoff/worktrees/0d876b87/web/src/app/cards/NewCardDialog.tsx
  26:17  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components

/root/.handoff/worktrees/0d876b87/web/src/app/data/usePreviews.ts
  152:18  warning  The ref value 'openingRef.current' will likely have changed by the time this effect cleanup function runs. If this ref points to a node rendered by React, copy 'openingRef.current' to a variable inside the effect, and use that variable in the cleanup function  react-hooks/exhaustive-deps
  153:15  warning  The ref value 'openRef.current' will likely have changed by the time this effect cleanup function runs. If this ref points to a node rendered by React, copy 'openRef.current' to a variable inside the effect, and use that variable in the cleanup function        react-hooks/exhaustive-deps

/root/.handoff/worktrees/0d876b87/web/src/app/files/FileTree.tsx
  243:6  warning  React Hook useEffect has a missing dependency: 'base'. Either include it or remove the dependency array. If 'setExpanded' needs the current value of 'base', you can also switch to useReducer instead of useState and read 'base' in the reducer  react-hooks/exhaustive-deps

/root/.handoff/worktrees/0d876b87/web/src/app/settings/UpdatePage.tsx
  72:9  warning  The 'machines' logical expression could make the dependencies of useEffect Hook (at line 97) change on every render. To fix this, wrap the initialization of 'machines' in its own useMemo() Hook  react-hooks/exhaustive-deps

/root/.handoff/worktrees/0d876b87/web/src/app/shell/Breadcrumb.tsx
  25:17  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components

/root/.handoff/worktrees/0d876b87/web/src/app/shell/Shell.tsx
   94:17  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components
  114:17  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components
  125:17  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components
  391:6   warning  React Hook useEffect has a missing dependency: 'wb'. Either include it or remove the dependency array                           react-hooks/exhaustive-deps

/root/.handoff/worktrees/0d876b87/web/src/app/task/codeText.tsx
  33:17  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components
  52:17  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components

/root/.handoff/worktrees/0d876b87/web/src/app/tree/MobileWorkspace.tsx
  80:7  warning  React Hook useMemo has a missing dependency: 'activeSessions'. Either include it or remove the dependency array  react-hooks/exhaustive-deps

/root/.handoff/worktrees/0d876b87/web/src/app/tree/ProjectTree.tsx
  214:17  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components
  233:17  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components
  287:17  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components
  302:17  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components
  323:17  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components
  339:17  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components
  367:17  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components

/root/.handoff/worktrees/0d876b87/web/src/app/workbench/BlankTab.tsx
  22:14  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components
  73:17  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components
  84:17  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components

/root/.handoff/worktrees/0d876b87/web/src/app/workbench/MobileKeybar.tsx
  33:14  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components

/root/.handoff/worktrees/0d876b87/web/src/app/workbench/TaskPickerDialog.tsx
  38:17  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components
  50:17  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components

/root/.handoff/worktrees/0d876b87/web/src/components/ui/badge.tsx
  41:17  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components

/root/.handoff/worktrees/0d876b87/web/src/components/ui/button.tsx
  57:18  warning  Fast refresh only works when a file only exports components. Use a new file to share constants or functions between components  react-refresh/only-export-components

✖ 33 problems (0 errors, 33 warnings)
```

- 提交：`git commit -m "fix(B467): 修复轮 1——忽略确认前置、nil 忽略日志、时间未知、按 id 判已忽略"` 原始输出：`[cards/B467-charter-3 4d0d3027] fix(B467): 修复轮 1——忽略确认前置、nil 忽略日志、时间未知、按 id 判已忽略` / `8 files changed, 213 insertions(+), 11 deletions(-)`。本行随同批 amend 收入（amend 会换 hash，台账保留提交当时的 `4d0d3027`）。

