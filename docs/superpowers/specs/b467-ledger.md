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
