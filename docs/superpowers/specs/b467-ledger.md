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
