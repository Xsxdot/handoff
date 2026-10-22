# B464 台账

## plan 出稿（2026-10-22）

- 出稿者：主会话派出的 plan subagent（charter:plan / architecture-law / defect-families 三份已加载）。工作树 `.claude/worktrees/b464`，分支 `cards/B464-charter-1` @ `5b8eab24`（spec r1 已批准提交 `5715bac7`）。本节只记读数、判断与放弃的方案；裁决归主会话。
- 输入核对：spec `docs/superpowers/specs/b464.md` r1（S1–S9、落位三问、目标架构、首故事、实现/测试决定、OOS）；B453 plan（已拍板，F1–F19 冻结及 r1–r4 修订）与 B453 台账；原型 `prototypes/b464-accounts/shared/accounts.js`（695 行 mock，模型注释 1–20 行）；`prototypes/base/README.md:28` 记 b464-accounts「确认中」。
- 本节点**未做任何真机动作**（未读任何凭据文件、未查钥匙串）；真机事实一律写成 plan §5.3 的待验证项交主会话执行。

### 代码读数（基线 5b8eab24，与熟练工 c6b90865 基线 0e24b012 之后只有文档提交）

- 账本 schema 闸：`internal/ledger/schema_version.go:21` `ledgerSchemaVersion = 2`，注释「改 ddlStatements 或在 ensureSchema 闸内新增迁移时必须加 1」。registry 表 `internal/ledger/store.go:464-468`（PG：kind/id TEXT、body JSONB、PK(kind,id)）——账本不解释 body，**新增 registry kind 与 body 形状演进都不升版本**。
- registry CAS：`internal/ledger/registry.go:29-77 RegistryPut` 整行 CAS，版本从 1 起，`expect=0` = 必须不存在；冲突包 `ErrCASConflict`。
- 账号域现状：`internal/scheduling/accounts/types.go:14-17` kind 常量 `account`/`account_reading`，id = 账号名（`types.go:52` 「名称即 id，登记后不可改」）；`Account` 含 `Machine/HomeDir/Entry/Proxy`（`types.go:53-63`）；`accounts.go:60-98 Put` 含 `sameCredential` 查重（`:119-122`）；kind 词表 `:142`；`mergeReading` 读-合并-CAS（`:282-314`）；`Derive`（`:343-365`）；`Assess`（`:423-440`）；`delete.go:57-87 Delete` 连带删读数行；引用查询经 `SetReferrers` 注入（`delete.go:36`）；打码回填 `isMaskedEcho`（`delete.go:95-97`）。
- 载体：`internal/scheduling/scheduling.go:46-52` `CredentialSource` 两值；`:61-72 Carrier`（`HomeDir`、`Credential`、`Account`、`CredFingerprint`）；`:236-318 PutCarrier`（HOME 变 → pending；HOME 与账号都不变才沿用指纹）；`:322-346 checkCarrierAccount` 同机校验；`receiver.go:147-150 IdentityOf` 物理身份含 `HomeDir`（空 = 主 HOME，是身份值）；`scheduling.go:801-815 AdmitFrozen` 以 `binding.HomeDir != identity.HomeDir` 判物理身份不一致；`:1268-1279 bindingFor` 快照 HomeDir。
- 统一准入：`internal/scheduling/admission.go:86-112 usability`（载体 online + 所选账号不 Blocks）；`CheckUsable`、`requireUsable` 单点（`:115-128`）；`CarriersUsingAccount`（`:141-155`）按账号名匹配；`status.go:183- markDetectQuota`。
- 检测：`internal/scheduling/status.go:93-103 DetectEvidence`（含 `Fingerprint`），`:113-178 ApplyDetect` 写 `CredFingerprint`；`internal/agentd/schedapi.go:239-376 handleCarrierDetect`：本机直调 `WakeHome`，他机 `forwardJSON` 到 `/api/host/wake`（`:300-303`），把 `Credential`、`FingerprintKind/Entry`、`Account` 放进 `HomeWakeReq`；`:378-392 detectEvidence` 白名单；`:466 carrierView`。
- hostapi：`internal/hostapi/probe.go:100-113 ProbeHome`；`:119-158 WakeHome`，`:136-142` 目标空且 `main_home_sync` 时 `copyMainCredential`（`:319-357`）；`:252-262 applyMainHomeSync`、`:296-315 mainHomeHasCredential`；`:164-176 credentialFingerprint`；`hostapi.go:50-67` `SetCredentialFingerprint`/`NewWithCredentialPathFor`。`/api/host/probe` 的唯一生产消费者是 web 载体对话框的 HOME 实时提示（`web/src/app/settings/SchedulingPage.tsx:247`、`web/src/api/scheduling.ts:174-178`）。
- 协调者供给：`internal/orchestration/coordinator_home.go:39-130 Prepare`：`:87-91` 把投影后的活配置 `config.Save` 到 `<targetHome>/.handoff/config.yaml`；`:94` `copyMissingCoordinatorCredential`（`:345-389`）；`:97` provider 配置投影；`:114-118` `Profile.Prepare` 带 `Credential: executor.CredentialMainHomeSync`。`executor.ProfileReq.Credential`（`internal/executor/profile.go:35`）在五家 Profile 实现里**零读取**（grep `req.Credential` 只命中 hostapi 自己），属死字段。
- **新发现（熟练工取证未列）**：agy 任务环境从**进程主 HOME**（`os.UserHomeDir`）拷 OAuth token 进任务 HOME：`internal/executor/agy/taskenv.go:100-128`（`~/.gemini/antigravity-cli/antigravity-oauth-token` 及 `.orig-google-oauth`），与载体 HOME 无关。出处 B305 spec（`docs/superpowers/specs/b305.md:36`，当时裁「拷贝，禁止 symlink 整份 antigravity-cli」）。后果：agy 载体即便 HOME 隔离，任务也用主 HOME 的登录；B464「凭据永不复制、载体运行 HOME = 登录 HOME」在 agy 上不成立。
- grok：`internal/executor/grok/authsync.go:120-170` 任务 home 的 `auth.json` 软链到「权威副本」= 载体 HOME（空 = 主 HOME）的 `.grok/auth.json`，刷新后收编写回同一份——**同一 HOME 内**的链接与收编，不是跨 HOME 复制，不在删除清单。`providercarry.go:184` 读主 HOME 的 `.grok/config.toml`（配置，非凭据，归 B455 一类）。
- codex：`internal/executor/codex/proc.go:211-235` 载体 HOME 非空时保留 env 里的 `CODEX_HOME`；`manager.go:1052-1060` 只在 `task.HomeDir != ""` 时覆盖 HOME——**空 HomeDir = 主 HOME 的语义被多处依赖**（codex CODEX_HOME 例外、grok 权威副本、协调者 attach `coordinator_sessionref.go:64-66`）。
- 任务 HOME：`internal/orchestration/manager.go:1185-1196 withCarrierHome`、`:1052-1060/1574/3914` 三处 env 组装；`withAccountProxy` `:1198-1209` 读任务行 `AccountProxy` 快照。任务库三列 `account/account_kind/account_proxy`（`internal/store/store.go:126-129,325-327`，本机 SQLite）；`proto.Task.Account*` 为 `json:"-"`（`internal/proto/proto.go:328-333`）。
- 账号代理：`internal/agentd/account_carrier.go:31-160`：`accountOfCarrier`、`accountFingerprints`（`:46`）、`accountMismatch`（`:66`）、`hostWakeProxy`（`:76`，目标机按账号名查本机账号代理）、`dispatchAccount`（`:98`）、`coordinatorAccountProxy`（`:119`，按 CLI+HOME 反查本机载体）。
- 探测器：`internal/agentd/account_prober.go`：30s 扫描、并发 2、单飞、`IsSelf(v.Account.Machine)`（`:149,244`）、`ExpandHome(a.HomeDir)`（`:303`）、账号代理（`:296`）、`probeDue` 由读数行推节拍；`probeOnce` 账号代理 > 活配置 `proxy:`。组装 `cmd/agentd.go:697-709 newQuotaRegistry`、`:795-799` hostapi 指纹注入、`:843-869` 账号域/探测器只在有 DSN 时装配。
- 账号 API：`internal/agentd/account_api.go:54-59` 四条路由；`AccountsClient` 方法集（`:35-43`）「多一个即越界」；立即探测转发一跳（`:189-258`）；`accountFromInput`（`:162-185`）；`accountView`（`:261-`）。
- quota 身份：`internal/quota/fingerprint.go:29-47 Identifier/CredentialFingerprint`；opencode-go 两来源与自动选择 `opencodego.go:122-151`，OAuth 身份 = `账号 id/org id`（`:168`），读 `opencode.db` 只读 DSN（`opencode_login.go:126-128`），`token_expiry` 毫秒（`:104-108`）；codex 身份 `tokens.account_id`（`codex.go:114-125`），`OPENAI_API_KEY`-only 报 `credential_invalid`（`:85-89`），access_token JWT exp 过期即报 `credential_expired`（`:101-112`）；grok 身份 `user_id`→`email`（`grok.go:131-160`）；agy 身份只在 `/usage` 输出里（`agy.go:299-312`），探测前要版本门 ≥1.1.11（`:36,73-80`）。claude 无任何适配。
- toolchain 凭据表：`internal/toolchain/detect.go:84-88` 只有 opencode/grok/codex；注释 `:79-83` 记「claude OAuth 在 macOS Keychain，`~/.claude.json` 是配置不是凭证」。B233.2 契约 `docs/superpowers/specs/b233.2-contract.md:36,355` 同此结论。
- 转发：`internal/agentd/forward.go:54-78 forwardIfRequested`（`?machine=`，一跳，`X-Handoff-Forwarded`）、`:116- forwardJSON`；`server.go:603 remoteTarget`、`:610- IsSelfMachine`。ssh 目标：`config.Target.User`（`internal/config/config.go:347-353`，attach/pull 换算 user@host），换算函数只在 `cmd/attach.go:74 sshHostFromTarget`。
- CLI：`cmd/account.go`（list/probe）；`cmd/squad.go:232-241 carrierAccountCell`；CLI 无载体登记命令（载体 HOME 输入只在 web）。
- web：`web/src/app/accounts/AccountsPage.tsx`（登记/编辑/删除对话框 `:66-133`，凭据 HOME 与条目输入 `:127-133`）、`StatusBar.tsx`、`MobileAccounts.tsx`、`store.ts`；`web/src/app/settings/SchedulingPage.tsx`（凭据来源选项 `:37`、HOME 输入 `:489`、HOME 实时探测 `:247-265`、指纹红字 `:61-84`）；`web/src/api/accounts.ts`、`scheduling.ts`。
- roadmap：`docs/roadmap.md:998-1023`「B453 残余」：`:1000` CLI 登记账号（B464 后「手填登记永不做」，应改写）；`:1009` 控制台读不到本机名（B464 新增登录要分本机/他机，相关）；`:1022` opencode-go 自动条目查重（B464 删条目字段后作废）。
- 测试面：`main_home_sync`/`Credential*` 出现在 36 个 `_test.go`（`internal/agentd` 24、`internal/scheduling` 9 等，grep 列表见出稿会话），删凭据模式会改写这批测试——按 plan §6 升级清单处理。

### 判断与放弃的方案

- **登录存储**：选新 registry kind `account_login`（一登录一行）。放弃「登录数组嵌在账号行」：多台机器的发现与人改名/阈值会写同一行，重演 B453 Q1 的 CAS 噪声；也放弃改表（要升 schema 版本、动共享 DDL，收益为零）。
- **账号 id**：选「由 kind + 身份指纹确定性导出」，让去重靠 registry 主键（两机同时发现同一身份 = 同一 id 的 `expect=0` 竞争，一方 409 后重读即可），放弃「随机 id + 先扫后写查重」（并发发现会建出两个同身份账号）。
- **载体 HOME**：选「载体行保留 `home_dir`，但只由服务端按所选登录写入（镜像）」。理由：`IdentityOf`、`bindingFor`、`AdmitFrozen`、scheddrain/coordinator SessionSpec、attach 定位等 20+ 处读 `carrier.HomeDir`，且登录的 HOME 不可变，镜像不会漂移。放弃「读时经登录解析 HOME」：要给 `IdentityOf` 等纯函数接账本，改动面与风险大，换不来行为差异。
- **主 HOME 表示**：登录行 `home = "~"`，载体镜像写 `""`（保持「空 = 主 HOME」这一被 codex/grok/attach 依赖的语义）。
- **迁移执行位置**：选「载体所在机在本机发现轮之后写一次」（只有所在机能展开 `~`、只有它知道本机登录）。放弃「读时按同机同 HOME 推导」：同 HOME 后来多出一条兼容登录时，绑定会无声改变。
- **已过期判据**：倾向「证据型」（探测/检测经该登录得到凭据失效，或离线到期已过且无 refresh token），放弃「离线到期即过期」——codex/claude/opencode OAuth 都是 CLI 运行时用 refresh token 自刷新，闲置 HOME 会长期误报。交主会话裁决（plan Q5）。
- **agy 任务 token**：倾向「源改为登录 HOME + 单文件软链」，真机核 agy 刷新时是原地写还是换目录项；放弃「保持拷贝」（与 spec「凭据永不复制」字面冲突，且源头是主 HOME 时 agy 载体根本用不上隔离登录）。
- **不扇出**：账号/登录模型是其余各段的契约硬依赖，且各段共享 `internal/scheduling/accounts`、`internal/agentd/account_*`、`web/src/app/accounts` 同一批文件；扇出只会制造施工冲突。

## implement 段 1（2026-10-22）

- 执行者：主会话派出的 implement subagent（charter:implement / architecture-law 已加载）。工作树 `.claude/worktrees/b464`，分支 `cards/B464-charter-1` 起点 `92fecd0c`（plan 已拍板）。本节边干边落：提交、命令与原始输出摘要、放弃的尝试、判断。
- 硬约束照办：零真机动作（不起/不重启任何 agentd、不读本机真实凭据、不连生产账本）；claude/agy 取材、登录命令表、Q5 已过期判据不写；Go 测试一律 `env -u CLAUDE_CODE_SESSION_ID`。

### 读数（实现前，基线 92fecd0c）

- 段 1 删 `Carrier.Account` 后，按账号名消费载体账号的生产点：`agentd/account_carrier.go`（accountOfCarrier/hostWakeProxy/dispatchAccount/coordinatorAccountProxy）、`agentd/quota_rerun.go`（任务撞额度标记、重跑排除集 excludes/rerunNoMemberError）、`scheduling/status.go markDetectQuota`、`scheduling/admission.go usability/CarriersUsingAccount`、`scheduling/scheduling.go checkCarrierAccount` 及三处日志字段。它们在段 1 失去编译基础，必须一并改成 载体 → 登录 → 账号 id。
- 账号 id 化后 `MarkQuota`/任务行 `account` 列/`exclude_accounts` 只能存账号 id（名字不再是主键）——plan 把 Q23 排在段 2，段 1 因数据模型被迫提前（见交回物偏离节）。

### 实现记录（按落地顺序）

- **accounts 新模型**（`internal/scheduling/accounts`）：`types.go` 重写（Account = 身份：id `<kind>:<hex12>` / apikey `apikey:<provider>:<hex12>`；Login 行 kind `account_login`，id `login-<sha256(machine\0home\0kind\0provider\0source)[:16]>`；`discovery_run` 行；`ok/expired/gone` 词表，段 1 只产生 ok/gone）；新 `naming.go`（AccountID/LoginID/自动起名/CompatibleKinds）、`login.go`（读登录、合并 CAS 写）、`discover.go`（Reconcile：expect=0 建账号、CAS 冲突回读即归并；登录只在内容变化时写；未扫清的 HOME 不下「已失效」）；`delete.go` 改按「名下登录被载体引用」拒删（`SetReferrers(func() (map[登录 id][]载体, error))`）。B453 旧账号行（无 fingerprint 或未知 kind）解码时跳过并 Warn 一次（plan Q20 的最小形，完整处理在段 2）。
- **quota 离线取材**（`internal/quota/logins.go`）：`Registry.Logins(home)`；`OpencodeGo.Logins`：auth.json › opencode-go key → source `auth.json`；opencode.db 当前登录 → source `opencode.db`（带到期）；只回来源名/短指纹/到期，原始身份不出包；看不清的凭据记 problem（带路径、不带值）。
- **编制域**（`internal/scheduling`）：删 `CredentialSource`/`Carrier.Credential`/`Carrier.Account`/`CredFingerprint`；载体 `Login`/`NoAccount`；`PutCarrier` 校验（二者不得同给、登录存在、同机、kind 与 CLI 兼容）+ `mirrorHome`；统一准入 `usability` 改读 载体 → 登录 → 账号（Assess 按账号 id）；`LoginReferrers` 取代 `CarriersUsingAccount`；新 `migrate.go`（§5 规则 1–4 + Q14 模型前缀判决，CAS 于读到的版本、从不覆盖人的选择，每载体一条 Info）。
- **HOME 供给去复制**：`hostapi.WakeHome` 删 main_home_sync 复制与建目录（HOME 不存在 → need_login + 可行动文案）、删凭据指纹；`orchestration/coordinator_home.go` 删 `copyMissingCoordinatorCredential` 及凭据路径白名单；任务启动未改（经 `home_dir` 镜像），由竖切断言锁住。
- **accountwork 子包**：`git mv internal/agentd/account_prober.go internal/agentd/accountwork/prober.go`；探测改走「可探登录」（Q10 最小形：最小 id 的正常登录，只由其所在机探），代理取登录代理、回落活配置 proxy；新 `discoverer.go`（启动一轮 + 60s 节拍、单飞；只扫主 HOME 与 `~/.handoff/home/*` 一层真实目录，跳过软链/文件；每轮 Info 观测点；AfterRound 接迁移）。子包不 import agentd（Q21）。
- **agentd/proto/cmd**：`account_api.go`（`{ref}` = id 或名；expect=0 → 400；GET 带 logins + discovery；立即探测路由到可探登录所在机）；`account_carrier.go` 改 载体 → 登录（检测本机套登录代理、远程只发 `login` id、派发快照存账号 id、协调者按 CLI+HOME 反查所选登录代理）；`schedapi.go`（PUT 二选一 400、残留 `home_dir`/`credential`/`account` 键按未知键忽略、投影派生 account_id/name/login_state、删指纹）；`hostprobe.go`（wake 只认本机登录）；`quota_rerun.go`（任务行 account = 账号 id，告知用账号名，排除集按账号 id）；`cmd/agentd.go` 组装发现器 + 迁移；`account list` 表格账号行下逐条登录行；`squad list` 账号格删「凭据指纹不符」、增「登录已失效」「无账号」。
- **web 最小对齐**：`api/scheduling.ts`、`api/accounts.ts` 类型按 A2/A4/A5/A6/A13/A14/W1；账号页/状态栏/移动页取 `accountPlace(a)`（可探登录）；载体对话框账号下拉改为「同机登录 / 无账号 · 主 HOME」二选一、HOME 只读回显；测试样本类型随改。

### 命令与读数

- `go build ./...` → 无输出（通过）。
- `go vet ./internal/scheduling/... ./internal/quota/... ./internal/hostapi/... ./internal/orchestration/... ./internal/proto/... ./internal/agentd/... ./cmd/... ./internal/keystone/...` → 无输出（通过）。
- `env -u CLAUDE_CODE_SESSION_ID go test -count=1 <上列触及包>` → 全 ok：scheduling 1.1s、accounts 0.5s、quota 5.1s、hostapi 6.1s、orchestration 45.5s、proto 0.4s、agentd 76.7s、accountwork 0.6s、cmd 79.3s、keystone 0.5s。本段未遇到 CLAUDE_CODE_SESSION_ID / 嵌套 worktree 两个环境敏感测试变红（一律 `env -u` 跑）。
- `gofmt -l <改动的 .go>` → 首次报 3 个测试文件（字段对齐），`gofmt -w` 后为空。
- `go test ./internal/proto/ -run TestContractFixtures` → 先红：AccountsResp/AccountInput/SquadsResp/HomeWakeReq/HomeWakeResp「序列化结果与 fixture 不一致」；`-update` 刷新 `web/src/api/testdata/` 5 个 json 后绿。
- `cd web && npm run typecheck`（worktree 无 node_modules，临时软链主仓 `web/node_modules`，收尾删除）→ 通过。`npx vitest run` → 150 文件 148 过、2004 例 12 红：`AccountsPage.test.tsx` 5 例（B453 手工登记/编辑凭据位置的交互）与 `SchedulingPage.test.tsx` 7 例（凭据来源枚举、HOME 手填探测、按名选账号、指纹不符红字）——它们断言的 UI 行为随 wire 删除，按派发指令只做类型对齐、不重做 UI（交回物偏离节）。
- 竖切 `env -u CLAUDE_CODE_SESSION_ID go test ./internal/agentd/ -run 'Discovery.*Vertical' -v` → `--- PASS: TestDiscoveryCarrierLoginDispatchVertical`。

### 红证据（先红后绿；部分实现先于测试落地，红以「临时注入缺陷 → 断言红 → 还原」补证，还原后 grep 注入标记计数 0）

- 竖切：① `WakeHome` 临时注入写 `auth.json` → `检测：HOME 里已有文件 .local/share/opencode/auth.json 被改动或删除`；② `mirrorHome` 临时让主 HOME 登录返回 `~` → `新载体投影：HOME 镜像应为空…`。还原后 PASS。
- 迁移（`TestMigrateCarrierLogins*`）：`decideMigration` 临时恒回规则 4 → 断言红；HOME 镜像/登录校验（`TestCarrierLogin*`）：`mirrorHome`/`checkCarrierLogin` 临时桩 → 断言红；`TestWakeHomeNeverCopiesCredential`：换回 HEAD 版 `probe.go`/`hostapi.go` → 断言红；发现器扫描范围（V3）：放宽软链过滤 → 断言红。均已还原（备份在 scratchpad）。
- 账号域（本回合补证）：`ensureAccount` 临时让 CAS 冲突不归并 → `TestReconcileDedupeTwoMachinesConcurrent`：`mac-mini 第 0 轮: 新建账号 opencode-go:aaaa11112222 失败: … 注册表版本冲突`；还原后 `ok`。
- quota（本回合补证）：`OpencodeGo.Logins` 临时不认 opencode.db 来源 → `TestLoginsOpencodeTwoSourcesDoNotMerge` 红（「没登录不是问题」与 problems 断言）；还原后 `ok`。
- 上面几条（迁移、镜像/校验、WakeHome、扫描范围）的实现先于测试写成，红是事后注入补证；账号域与 quota 两处「先红」没有留下原始输出，本回合同样以注入补证。违反「先红后绿」顺序的事实如实记录，交回物偏离节同列。

### 放弃的尝试与判断

- **放弃**：rerun/派发快照用例给 fake 执行器直接再挂一个 `"codex"` 键 → `未知 harness "codex"`（能力注册表按 `Provider.Name()` 登记，fake 自报 `fake`）。改为测试内 `namedFake{*fake.Fake, name}`（只改登记名与报告 harness 名）。
- **判断：兼容 kind 对未知 CLI 仍严格**（fake/自定义 CLI 不能选任何登录）——不为测试放宽生产规则；带账号的夹具改用 `namedFake` 扮 codex/opencode。
- **判断：HTTP 层强制二选一、域层放行「两者都缺」**：域 `PutCarrier` 两者都缺 = 存量未迁移形态（迁移与 35 个既有域测试夹具依赖它），HTTP（A14）新建/编辑必须二选一 → 400「二选一」。
- **判断：V10 残留键忽略**（不 400）：`json.Decoder` 未知键忽略，`home_dir` 由服务端镜像覆盖；测试锁「带了也不生效」。
- **判断：Q23 提前**：段 1 删 `Carrier.Account` 后撞额度标记、任务行 `account`、`exclude_accounts` 只能存账号 id，混版本期按 W3 接受最多多换一次。
- **判断：`interval_minutes/warn_percent/low_percent` 在 TS 里改为可缺席**（A2 原文），前端用缺省值兜底。
- **事故披露**：`TestAccountProbeForwardToHostMachineWhichWritesReading` 首次重写后的第一次运行，探测器按进程 HOME 展开登录 HOME `~`，尝试打开本机真实主 HOME 的 `~/.local/share/opencode/auth.json`——该文件不存在（结果 `credential_missing`），**未读到任何凭据内容**。修正：`newAccountMachine` 一律先把进程 HOME 钉到空临时目录，要探测成功的用例再钉到自己的夹具；之后全量运行无此路径访问。
- **既有测试断言改写清单**（plan §9 允许的四类，逐文件）：(1) 凭据模式/复制：`internal/hostapi/probe_test.go`、`b453_wake_test.go`、`internal/orchestration/coordinator_home_test.go`、`b393_provider_config_test.go`、`internal/agentd/hostprobe_test.go`、`schedapi_test.go`（`credential` 键与「空凭据落 standalone」→「无账号读面无 credential 键」）、`internal/scheduling/registry_read_test.go`、`status_test.go` 及约 30 个只删 `Credential: CredentialStandalone` 字段的域/agentd 测试文件；(2) 指纹比对/`account_mismatch`：`internal/agentd/b453_carrier_account_test.go`、`cmd/squad_test.go`；(3) B453 手工登记/按名引用：`internal/agentd/account_api_test.go`、`account_api_b453s2_test.go`（`probeDue` 退避表随探测器迁入 accountwork）、`b453_recover_at_test.go`、`b453_rerun_test.go`、`b453_coord_proxy_test.go`、`internal/scheduling/b453_admission_test.go`、`accounts/*_test.go`、`cmd/account_test.go`；(4) 契约 fixture：`internal/proto/contract_fixture_test.go` + `web/src/api/testdata/*.json` + `web/src/api/contract.test.ts`。另：`b452_pair_test.go`/`receiver_dispatch_test.go` 增 `namedFake` 别名登记（夹具，不改断言）；web 组件测试只改类型字面量。

### 提交（提交当时的命令与输出；末条随后 amend 收进本台账，hash 会变）

- `git commit`（Go 全部改动 + 契约 fixture）→ `cb24bb62 feat(B464): 账号登录发现、载体选登录与 HOME 供给去复制（段 1）`，`83 files changed, 4026 insertions(+), 1724 deletions(-)`。
- `git commit`（竖切测试）→ `de2c0237 test(B464): 首故事竖切——发现 → 存量迁移 → 选登录建载体 → 检测 → 派发，零凭据写入`。
- `git commit`（web 类型对齐 + 本台账）→ `ea4958a1 feat(B464): 控制台类型对齐段 1 wire；台账记 implement 段 1`；提交前删除临时 `web/node_modules` 软链；`git status --porcelain` 空。

## 段 0 真机事实 R0（2026-10-22，主会话；只打印键名/存在性/时间，未读任何凭据值）

- **R0-1 claude**：mac-mini 与 macbook-pro 均 `2.1.220`；linux-01 未装 claude。`~/.claude.json › oauthAccount` 有 `accountUuid`、`emailAddress`、`organizationUuid` 等 18 键（两台 Mac 都有），但 `claude auth status --json` 两台都是 `loggedIn:false, authMethod:none`（mac-mini 本地 GUI 会话执行；mbp 经 SSH）——**oauthAccount 存在 ≠ 已登录**，资料是旧缓存。登录判据须用 `claude auth status --json` 的 `loggedIn`（子进程，按 HOME 跑），身份取 `oauthAccount.accountUuid`（仅在 loggedIn 时可信），label `emailAddress`。两台 Mac 均无 `~/.claude/.credentials.json`；钥匙串：mac-mini 只有 `Claude Safe Storage`（无 `Claude Code-credentials`），mbp 有 `Claude Code-credentials`（无后缀变体）。登录命令 `claude auth login`（`--claudeai` 缺省订阅；`--console` API 计费；`--sso`）。
- **R0-2 claude 钥匙串是否跨 HOME 共用：未定**。空临时 HOME 下 `auth status` 为 false，但两台主 HOME 也都是 false（mac-mini 未登录；mbp 经 SSH 访问不到登录钥匙串），无法对照。需在已登录的 Mac GUI 会话里对照「主 HOME / 空 HOME」各跑一次 `claude auth status --json`——并入 Q22 的用户登录请求一次完成。段 2 的 claude 取材在此之前只按「loggedIn 判据 + accountUuid 身份」写，不写钥匙串分支。
- **R0-3 agy**（linux-01，agy 1.4.4）：`~/.gemini/antigravity-cli/antigravity-oauth-token` 是 JSON，顶层 `auth_method`（值 consumer）、`id_token`（JWT，claims 含 `email`、`sub`、`email_verified`…）、`token{access_token,expires_in,expiry,refresh_token,token_type}`；`.orig-google-oauth` 为 Google 授权库格式（client_id/refresh_token/expiry/scopes…）。→ **Q7 分支 (a)**：离线身份 = id_token `sub`（label `email`），不需要跑 `/usage`。agy 无 login 子命令（`--help` 列出的子命令无 auth/login），首次登录 = 在该 HOME 交互运行 `agy` 按提示登录。
- **R0-4 opencode**（linux-01 1.19.3）：`auth.json` 只有 `deepseek: type=api`（第三方 key）；`opencode.db` `account(id,email,url,access_token,refresh_token,token_expiry,time_created,time_updated)`、`account_state(id,active_account_id,active_org_id)`，1 个账号，active 登录 token_expiry 约 670h 后、有 refresh_token。label 可取 `account.email`。登录命令：OAuth 账号 = `opencode console login`（另有 `console switch` 切 org）；provider API key = `opencode auth login -p <provider>`（`-m` 选方法）。
- **R0-5 codex**（linux-01 codex-cli 0.163.0）：`auth.json` 顶层 `OPENAI_API_KEY`（空）、`auth_mode`、`last_refresh`、`tokens{access_token,account_id,id_token,refresh_token}`；id_token claims 含 `email`、`sub`、`https://api.openai.com/auth`；access_token exp 约 222h 后，last_refresh 2026-10-21。登录命令：`codex login`（浏览器）；无浏览器机器 `codex login --device-auth`；`codex login status` 查状态。label 取 id_token `email`。
- **R0-6 grok**：linux-01 1.0.13 主 HOME 无 `~/.grok/auth.json`；macbook-pro 1.0.62 的 `auth.json` 条目名为 `https://auth.x.ai::<uuid>`（**带 `::<uuid>` 后缀**，B453 适配器按 `https://auth.x.ai` 精确名取条目——段 2 须按前缀匹配，否则新版 grok 探测/发现落空），条目键 `auth_mode,email,expires_at(字符串),key,refresh_token,user_id,team_id,principal_id…`。登录命令 `grok login`（`--oauth`）。
- **R0-7 到期噪声**：codex ~9 天、opencode OAuth ~28 天、agy/grok 有 expiry，**四家都带 refresh token**，闲置 HOME 离线到期必然常年已过 → Q5 按 (C) 证据型执行（不改选 A）。
- **R0-8 登录命令表（段 3 照抄）**：opencode OAuth `opencode console login`；opencode key `opencode auth login -p <provider>`；codex `codex login`（远端/无浏览器 `codex login --device-auth`）；grok `grok login`；agy 交互运行 `agy`；claude `claude auth login`。全部以 `HOME=<登录 HOME>` 前缀运行。
- **R0-9 agy token 写法**：首次记录 linux-01 `antigravity-oauth-token` inode 5374653 mtime 1792637088；下一次在役 agy 任务跑完后复记，比对后定 Q8。

## 主会话复核段 1 交回（2026-10-22）

- 偏离 1（Q23 提前）：追认——Q23 已拍板采纳，段 1 删 `Carrier.Account` 后只能存账号 id，提前合理。
- 偏离 2（既有测试断言改写）：留给独立审查逐条核对「既有测试断言改写清单」。
- 偏离 3（web 12 例红）：段 5 重写账号页与载体对话框时消解；段 5 收口前全量 vitest 必须回绿。
- 偏离 9（部分先实现后补测，红由注入缺陷取得）：记为流程偏离，审查的变异复验对这些路径加做。
- 其余（V10 忽略残留键、A14 只在 HTTP 强制二选一、发现 60s、Q10 最小形、Q20 最小形）在 plan §9 权限内，认可。

## 真机 R1 首故事（2026-10-22，主会话；隔离 t464-mac 127.0.0.1:7796 / t464-linux :7795，测试账本 handoff_b464，在役 7777 未动）

- 二进制：main 755c0aa5c35c 与分支 23b65527d3d1（`build-deploy.sh --pack`，darwin/arm64、linux/amd64）。
- 先以 main 起两实例，API 建存量载体：t464-oc-mac（t464-mac、opencode、HOME 空、standalone）、t464-oc-muse（t464-linux、`~/.handoff/home/muse`、main_home_sync）、t464-oc-cmd（t464-linux、`~/.handoff/home/command`、无凭据）。换分支二进制重启。
- ① 首轮发现：账号 `opencode-go:1e5f72dfee52`（自动名 opencode-go-1e5f）两条登录——t464-mac `~` opencode.db ok、t464-linux `~` opencode.db ok（**跨机同身份归并成立**，指纹 sha256:1e5f72d 与 B453 R1′ 一致）；账号 `opencode-go:54ce90677410` 一条登录 t464-linux `~/.handoff/home/muse` auth.json（探测 fail，key 侧问题，不属本卡）。linux 主 HOME 的 `deepseek` 第三方 key 未发现（apikey 属段 2）。
- ② 迁移：t464-oc-mac → login-f633…（mac 主 HOME），home_dir `''`；t464-oc-muse → login-2dcc…（muse），home_dir 原串保留；t464-oc-cmd → 未选（login/no_account 皆空）。符合 §5。
- ③ `PUT /api/squads/carriers/t464-oc`（login=login-f633…，请求体故意带 `home_dir:"/tmp/should-be-ignored"`）→ home_dir `''`、account_name opencode-go-1e5f、login_state ok；detect → online。
- ④ `dispatch --receiver t464-oc --new-worktree --prompt "echo HOME=$HOME"` → rc 0，最终回答 `HOME=/Users/df-zidong`（主 HOME）。任务 9e400e80（分支 handoff/9e400e80 建在主仓，清理时删）。
- ⑤ 前后对照：在役 agentd pid mac 87935、linux 1205162 不变；linux `opencode/auth.json`、`codex/auth.json`、agy token mtime 不变；两机主 HOME `opencode.db` mtime 变（opencode 运行自身写会话，R1 任务与在役载体都在主 HOME 跑 opencode，非 handoff 写凭据）；`~/.handoff/home/muse/.local/share/opencode/opencode.db-shm` 近 20 分钟有更新——发现器 `mode=ro` 打开 WAL 库时 SQLite 维护 -shm 索引所致，非凭据写入（**Minor，交审查**：可评估 `immutable=1` 与一致性取舍）。
- 结论：**R1 通过**，进段 2。

## implement 段 2（2026-10-22）

- 执行者：主会话派出的 implement subagent（charter:implement / architecture-law 已加载），工作树同段 1，起点 `e5c79704`（已确认 HEAD 即此提交）。纪律同段 1：零真机、不读真实凭据、测试一律 `env -u CLAUDE_CODE_SESSION_ID`、提交不 push。
- 输入：plan §7 段 2、台账「段 0 真机事实 R0」、主会话派发补充（codex/grok/agy/claude/apikey 取材口径、Q5 证据型）。
- **主会话补充裁决（用户 2026-10-22「先按共用处理」）**：R0-2 不再实测，claude 按 Q6 分支 b——darwin 上 claude 登录只认主 HOME（非主 HOME 不发现为 claude 登录），linux 每个 HOME 独立；落为一处可切换的 kind 规则（可注入 GOOS），规则函数导出给段 3 的新增登录校验用。
- 夹具键名只取 R0：codex `tokens{access_token,account_id,id_token,refresh_token}`、`OPENAI_API_KEY`；grok 条目 `https://auth.x.ai::<uuid>`，键 `key,email,expires_at(字符串),refresh_token,user_id`；agy `antigravity-oauth-token` 顶层 `auth_method,id_token(JWT: sub,email),token{access_token,expires_in,expiry,refresh_token,token_type}`；claude `claude auth status --json` 的 `loggedIn/authMethod`、`.claude.json › oauthAccount{accountUuid,emailAddress}`；opencode `auth.json` 条目 `type=api`/`key`。值一律哨兵。

### 读数（实现前）

- **grok 前缀匹配并非 B453 现存缺陷**：`internal/quota/grok.go` `pickEntry`/`Identity` 自 `5c4c17ba`（B453 段 2）起即按「键等于 issuer 或以 `issuer::` 开头」选条目，`expires_at` 字符串由 `flexibleMoment` 解析；`grok_test.go:114-117,161` 已有 `https://auth.x.ai::<uuid>` 夹具。R0-6「B453 按精确名取条目」与代码不符。处理：新写的 grok 离线取材同口径（先红后绿）；对既有探测器以一次变异（去掉前缀分支）验证现有测试会红，作为「有牙」证据，不改产品代码。

### quota 离线取材（codex / grok / agy / claude / apikey）

- 先写 `internal/quota/logins_kinds_test.go`（每 kind 一张夹具表 + V4 哨兵），编译红后只加空桩（新字段、`Claude.Logins` 返回空、`RegisterLoginReader` 空实现）取断言红：`env -u CLAUDE_CODE_SESSION_ID go test ./internal/quota/ -run Logins` → `--- FAIL: TestLoginsCodexTable / TestLoginsGrokTable / TestLoginsAgyTable / TestLoginsClaudeTable / TestLoginsClaudeCachesByFileState / TestLoginsAPIKeyEntriesAreThirdPartyKeys / TestLoginsOpencodeDBRefreshAndChangedAt / TestRegistryLoginsKeepFilterSkipsKinds`（「应恰认出一份登录，得 []」「problems = []，want problem=true」等）。
- 实现后首跑：`TestLoginsClaudeTable` **V4 红**——`claude auth status` 输出不是 JSON 时失败文案回显了 CLI stdout 片段（夹具里塞的哨兵 key 出现在问题文案里）。判断：第三方 CLI 的输出不保证不含凭据，失败文案一律只给 HOME 与字节数，不回显 stdout/stderr；修后绿。另一处红是我自己新测试对返回顺序的假设（opencode-go 条目先于第三方 key），改为排序后比较。
- `go test ./internal/quota/` → `ok`（4.9s）。
- grok 既有探测器「有牙」证据（变异，不改产品代码）：把 `pickEntry` 的条件改成只认精确名（去掉 `issuer::` 前缀分支）→ `TestGrokProbeEntrySelectionFallbackAndExpiry`、`TestGrokProbeUnauthorizedDoesNotFallBack` 红（`credential_missing: … 里没有 issuer "https://auth.x.ai" 的条目`）；还原后 `grep -c MUTATION-B464S2 grok.go` = 0。
- 判断（§9 权限内）：
  - 来源名（登录槽位的一部分）：codex/grok `auth.json`、agy `antigravity-oauth-token`、claude `claude-auth`（跨平台不变：linux 凭据文件与 macOS 钥匙串同一槽位）、apikey `auth.json`（槽位里带 provider）。
  - `LoginFinding` 增 `Refreshable`、`ChangedAt`（凭据变更时刻，Q5 证据比较用；**不进登录行 body**，L6 不变）。ChangedAt：文件类取 mtime；opencode.db 取当前账号行 `time_updated`（库文件 mtime 随会话写入常变，不能当凭据变更）；claude 取 `.claude/.credentials.json` mtime，没有则 `.claude.json` mtime。
  - opencode.db 的 refresh/time_updated 是尽力读：读不出按「可自刷新、变更时刻未知」——宁可少标已过期（Q5 反误报方向）。
  - claude：`.claude.json` 无 `oauthAccount` 不起子进程；CLI 未装按未登录；按两个文件 mtime/大小缓存结果（TTL 10 分钟），文件不变不重跑 node 子进程；子进程环境去掉 `CLAUDE_CONFIG_DIR`、`ANTHROPIC_API_KEY`、`ANTHROPIC_AUTH_TOKEN`（只按该 HOME 的登录判定）。不读钥匙串、不读凭据文件内容（只 stat）。
  - `Registry.Logins(home, keep...)`：发现器按 kind 规则过滤，被过滤的 kind 不读文件、不起子进程（darwin 非主 HOME 不跑 claude）。claude 经 `RegisterLoginReader` 登记（只取材、不探测）。
  - `runGroup` 泛化为 `runGroupLabeled`（文案带程序名；非零退出时 stdout 照样交回，claude 未登录可能非零退出）——agy 两处调用出错即返回、不读 stdout，行为不变。

### 账号域（登录状态 Q5、代理 Q16、可探登录 Q10、na Q11、改名/删除 Q17/Q20）

- 先写 `internal/scheduling/accounts/login_state_test.go`（V5 表、证据保留最晚、代理合并写、V6 三写者并发 N=50、V8 可探登录、kind 规则与 na）与 `manage_test.go`（V7 改名、删除连带全部登录、Q20 旧行可删），桩（`zz_stub.go`：常量/空方法）取断言红：`--- FAIL: TestLoginStateEvidenceTransitions`（「失效证据：状态 "ok" 证据 <nil>，want "expired"」「到期已过 + 不可刷新：状态 "ok"」）、`TestMarkLoginAuthFailedKeepsLatestAndRejectsUnknown`、`TestSetLoginProxyMergesMaskedEchoAndClears`、`TestConcurrentDiscoverProxyEvidenceKeepLastWrites`（`Proxy: AuthFailedAt:<nil>`）、`TestProbeLoginNeedsOKAndFreshMachine`（「首选所在机发现过期应换 m2」）、`TestKindRulesNAAndMainHomeOnly`、`TestRenameRulesAndReferencesSurvive`（「账号改名尚未开放」）、`TestDeleteCascadesAllLoginsAndReading`（登录行残留 3 条）、`TestLegacyB453RowHiddenButDeletable`（「账号 og-old: 账号不存在」）、`TestPutEditValidation`。删桩实现后 `go test ./internal/scheduling/accounts/` → `ok`。
- **既有断言改写**（plan §9 类 (3) 账号登记）：`validate_test.go` `TestPutEditValidation` 的「改名（段 2）→ 拒」一行改为「改名含冒号 → 拒」——段 1 的占位断言随改名开放失效，改名规则的完整表在 `manage_test.go`。
- 判断（§9 权限内）：
  - kind 规则表落在 `accounts/kinds.go`（`SupportsProbe`、`LoginMainHomeOnly(kind, goos)` 导出，段 3 新增登录校验直接用）；平台由调用方注入。
  - Q5 状态推导 `stateOf`/`evidenceAfter`：失效证据作废的三种凭据变化——`CredChangedAt` 晚于证据、到期后移、同槽位换身份；`CredChangedAt` 缺席不算变化。已失效登录不被证据改写，证据随行保留，再现时照同一规则判。
  - 证据写入 `MarkLoginAuthFailed(id, at, writer, category)`：只保留最晚证据，写即读作已过期；探测取开始时刻。
  - Q10 新鲜度窗口 `DiscoveryFreshFor = 3m`（= 3 × 发现节拍 60s，§9 授权 2–5 个周期取 3）。
  - 「没有可用登录」只在读面（`newView`）推导：State=fail、`last_error{category:"no_login"}`、文案分三种原因（无登录 / 都已过期或失效 / 所在机发现过期），不写读数行。**准入（Assess）不读登录与发现**：准入热路径不加两次列表读；载体自己的登录失效由准入的「登录已失效」判据挡（V11），已过期按 spec 放行。`no_login` 是新类别值，web `FAIL_BRIEF` 未列的类别回落「探测失败」，段 5 补短因。
  - 改名并发撞名窗口接受（与 Q2 自动起名同一取舍）；`ErrNameTaken` 与 `ErrInvalid` 区分（gateway 409 vs 400）。
  - 删除：定义行删掉后登录行/读数行删除失败只告警（孤儿登录行下一轮发现以同一 id 重建账号并重新挂上）。

### 发现器 / 探测器（accountwork）

- 先写 `accountwork_s2_test.go`，桩（接口签名、`GOOS` 字段、`ErrProbeUnsupported` 占位）取断言红：`--- FAIL: TestDiscovererClaudeMainHomeOnlyByGOOS`（darwin「claude 登录 HOME = [~/.handoff/home/iso ~]，want [~]」）、`TestDiscovererMapsRefreshAndChangeForExpiredState`（两 HOME 都 expired）、`TestProberWritesEvidenceAndSwitchesLogin`（「5s 内未等到：首选登录记失效证据」）、`TestProberSkipsNAAccounts`（「账号没有可用登录」而非不支持探测）。linux 子例的红是我测试对顺序的假设（归并结果按登录 id 排序），改为排序后比较。实现后 `go test ./internal/agentd/accountwork/` → `ok`。
- V4 发现路径全 kind 泄密表 `TestDiscovererSecretsNeverLeakAllKinds` 实现后才写（首跑即绿）；「有牙」证据：临时让 codex label 拼上 `account_id` → 红「发现路径泄露了 "acct-SENTINEL-raw-identity"」；还原后 `grep -c MUTATION-B464S2 internal/quota/logins.go` = 0。
- 判断（§9 权限内）：
  - 发现器 `GOOS` 注入（空 = `runtime.GOOS`）；kind 规则在取材前过滤（`Registry.Logins(home, keep)`），darwin 非主 HOME 不跑 claude 子进程。
  - 探测器：na 在调 quota 前跳过；`ProbeNow` na → `ErrProbeUnsupported`（gateway 409）；失败类别 `credential_expired`/`unauthorized` → `MarkLoginAuthFailed(登录, 探测开始时刻)`，网络/代理/限速/上游不记；证据写失败只告警。
  - **换登录立即再探**：`probeDue` 在「上次失败用的是另一条登录」时立即到期——失败退避属于失败的那条登录，可探登录已换就不该替它等（否则 V8「换下一条」要等 30s×2ⁿ）。
  - `SourceEntry` 补 codex `auth.json → tokens`、grok `auth.json → https://auth.x.ai`（前缀匹配在适配器里）。
- `TestDiscoverFreshWindowIsThreeRounds` 锁 `accounts.DiscoveryFreshFor == 3 × DefaultDiscoverEvery`。

### 编制域准入 / 检测证据

- 先写 `internal/scheduling/b464_login_admission_test.go`，断言红：`AdmitCarrier 应因登录已失效拒（ErrNoHealthy + 「载体 c1 的登录已失效：m1 ~/.handoff/home/cx」），得 <nil>`；`检测需要登录应给所选登录记失效证据：… State:ok … AuthFailedAt:<nil>`。实现后 `go test ./internal/scheduling/...` → `ok`。
- 放弃：首版夹具把第二个账号放同一台机器 m1——`putAccount` 的那轮发现只含 cy 的 HOME，归并按「本机完整扫描里没有」把 cx 的登录标了已失效（发现语义正确，是夹具写法错）。改为第二个账号放 m2。
- 判断：「登录已失效」文案单载体与小队同一句「载体 X 的登录已失效：<机器> <HOME>」（小队拼进「小队 S 没有可用载体——…」）；已过期放行（七个入口逐一锁）。检测证据只在「可达 且 需要登录 且 非撞额度」时记（撞额度说明 CLI 带着凭据到了上游）。

### gateway / CLI / 契约

- 先写 `internal/agentd/b464_login_api_test.go`，断言红：`设代理应 200 且回打码值：404 404 page not found`；`改名撞名应 409：500 {"error":"账号名字 \"cy\" 已被账号 … 占用…"}`。实现 `PUT /api/logins/{id}/proxy`（体 `{proxy}`，键缺席 400）、`ErrNameTaken → 409`、na 立即探测 409 后 `ok`。
- 放弃：首版测试在 `newSchedEnv` 之前 `t.Setenv("HOME", 临时目录)`——夹具起服务时以该 HOME 下的 Go 缓存拉模块（30s+，且清理时 `go/pkg/mod` 只读文件删不掉报 FAIL）。登录 HOME 用绝对路径写法、不经进程 HOME 展开，去掉 Setenv 后 2.2s 绿。临时目录是空目录，不涉及任何真实凭据。
- V9：端点写入后，检测回合 `hostWakeLogin`、派发快照 `dispatchAccount`、协调者回合 `coordinatorAccountProxy` 三处读到同一代理，清除后三处都回空；探测器一处在 accountwork（`TestProberWritesEvidenceAndSwitchesLogin` 断言请求带登录代理、未设回落机器代理）。
- `cmd/account.go` 状态词表加 `na → 不支持额度探测`；`cmd/account_test.go` 样本**追加**一个 na claude 账号（含已过期登录）与对应表格断言（只加不改）。先红「表格缺 "不支持额度探测"」后绿。
- `cmd/agentd.go` 组装 `RegisterLoginReader(claude, &quota.Claude{})`。
- 契约 fixture（plan §9 类 (4)）：`AccountsResp` **追加** na claude 账号（已过期登录）与「探测失败 · 没有可用登录」agy 账号；新增 `AccountRenameInput.json`、`LoginProxyInput.json`。`go test ./internal/proto/ -run TestContractFixtures` 先红（AccountsResp 漂移、两个新 fixture 不存在）→ `-update` → `ok`。既有样本不改（含 `apikey:opencode-go` 的 out 样本——形状仍合法，语义按段 2 规则已不会产生，段 5 重写账号页时一并换样本）。
- web：临时软链主仓 `web/node_modules`，`npm run typecheck` 通过；`npx vitest run src/api/contract.test.ts` 65/65；全量 `npx vitest run` 仍是段 1 的 12 例红（AccountsPage 5 + SchedulingPage 7），无新增。提交前删软链。TS 侧未加 `LoginProxyInput` 类型与 A9 调用（段 5）。

### 收口读数（段 2）

- `go build ./...` → 无输出。
- `go vet ./internal/quota/... ./internal/scheduling/... ./internal/agentd/... ./cmd/... ./internal/proto/...` → 无输出。
- `gofmt -l <改动的 .go>` → 空。
- `env -u CLAUDE_CODE_SESSION_ID go test -count=1 ./internal/quota/... ./internal/scheduling/... ./internal/agentd/... ./cmd/... ./internal/proto/...` → 全 ok（quota 4.6s、scheduling 0.9s、accounts 0.2s、agentd 75.2s、accountwork 0.7s、cmd 81.2s、proto 0.3s）。未遇到两个环境敏感测试。
- `go test -race -count=1 ./internal/scheduling/accounts/ ./internal/agentd/accountwork/ ./internal/quota/` → 全 ok。

### 交审查的事项（不在本段改）

- R1 ⑤ opencode.db `mode=ro` 触碰 `-shm`：本段 `loginRefreshInfo` 每轮对 opencode.db 再开一次同 DSN 只读连接（读 refresh_token 是否非空与 time_updated），触碰频率翻倍、性质不变；按主会话指示不改 DSN，交审查一并裁 `immutable=1` 的取舍。
- 「没有可用登录」只在读面推导，准入（Assess）不读登录与发现；载体自己的登录失效由「登录已失效」挡。若审查认为准入也要同一推导，代价是准入热路径每次多两次列表读。
- claude 的「凭据变更时刻」在 macOS 只能取 `.claude.json` mtime（钥匙串无 mtime），claude 运行时常写该文件 → 检测证据在 claude 下一次运行后即作废、登录回正常（偏向少报已过期）。

### 提交（提交当时的命令与输出；随后 amend 一次收进本台账，hash 会变）

- 先按三个自然步骤提交（quota / 账号域+编制域 / accountwork+gateway+cmd+契约），自检 `git checkout 0579d9f4 && go build ./...` → `cmd/agentd.go:872:14: cannot use quotaReg … does not implement accountwork.LoginReader (wrong type for method Logins)`——中间提交单独编译不过（取材接口签名与使用方接口同批变）。判断：不留编译不过的中间提交，`git reset --soft e5c79704` 合为一个提交。
- `git commit`（全部段 2 改动 + 本台账）→ `7318d7fb feat(B464): 其余 kind 取材、证据型已过期、登录代理、可探登录完整形、na、账号管理（段 2）`，`35 files changed, 2736 insertions(+), 120 deletions(-)`；`go build ./...` 通过。

## 主会话复核段 2（2026-10-22）

- 更正 R0-6：grok `::<uuid>` 前缀 B453 已支持（`5c4c17ba` 起），不是缺陷；段 2 以变异补证锁住。
- 「没有可用登录」只在读面推导、不进准入：维持（额度未知照常派发，与 B453 语义一致）。
- `opencode.db` `mode=ro` 碰 `-shm` 与 `immutable=1` 取舍：交独立审查裁决。
- 执行形态改丙（用户 2026-10-22）：段 3 起经 `card dispatch --step implement` 派 runner 小队，本会话只审与验。

## implement 段 3（2026-10-22，handoff runner 小队）

- 执行者：`card dispatch --step implement` 派出的 runner；分支 `cards/B464-charter-1`，起点 `8cd3a9d6`。
- 范围：只做 plan §7 段 3（S2、S1 立即发现、S9），不碰段 4/5，不重做段 1/2。
- 纪律：零真机动作（不起/不重启 agentd、不读本机真实凭据、不连生产账本）；测试一律夹具 HOME/临时目录；Go 测试 `env -u CLAUDE_CODE_SESSION_ID`；按纪律块只 commit 不 push。

### 偏离与自主决定

1. **既有测试夹具的时间炸弹修复（自行拍板）**：`internal/scheduling/accounts/accounts_test.go` 的 `seedAccount` 把发现时刻从固定 `base`（2026-10-22 08:00Z）改为 `time.Now()`。全量跑该包时 `TestMergeProbeAndMarkSeparateRowsDoNotBumpDefinition`、`TestMergeProbeClearsMarkOnlyWhenStartedAfterMark` 在真实时钟晚于 base+3min 后红（`ProbeLogin` 的新鲜度窗口使读面推导成 `fail/no_login`，与段 3 改动无关）。只改夹具、不改任何断言；对照 plan §9，属既有测试夹具备份修复（类 (3) 账号域测试面的边角），无产品行为变化。
2. **新增登录/检查的转发实现**：plan 写「`?machine=` 一跳转发（`forwardIfRequested`）」。实际新增 `forwardJSONKeepQuery`（`forward.go`），保留 `?machine=` 让目标机自证「我是被指定的那台」（V13 的转发到达非目标机 409），并让入口机读目标响应后补 ssh 行。行为与 plan 一致；函数拆分/命名在 §9 授权内。
3. **`squad list` 无账号格的文案**：所选登录格改为「账号名 · 机器 HOME」；`no_account` 仍显示「无账号」、未选仍为 `-`，沿用既有契约与测试断言（S9 只规定「账号名 · 机器 HOME」的载体行）。
4. **`LoginView.relogin` 对所有已知 kind 的登录下发**（不只已过期/已失效）：A4 是可选键，S3 的「给复制登录命令」由界面决定展示；放在 wire 上可让段 5 直接用同一份命令表。命令表只在 `internal/agentd/login_api.go` 一处（R0-8 原文）。
5. **C2 的「登录数」**：A10 冻结的响应形状没有登录计数，CLI 每行输出「机器、成功/失败、时刻或原因」，不擅自给 A10 增键。
6. **契约 fixture 刷新**：新增 `LoginHomeResp.json`、`LoginCheckResp.json`、`AccountDiscoverResp.json`；`AccountsResp.json` 的两条登录样本补 `relogin`（plan §9 类 (4)）。web `contract.test.ts` 65/65 通过，未改前端类型（段 5 范围）。
7. **不 push**：卡补充⑧要求 push，纪律块要求「必须 git add 并 commit（不要 push）」；按「纪律块与本 prompt 冲突时以纪律块为准」执行，只提交本分支。

### 实现落地（按文件）

- `internal/proto/scheduling.go`：A4 `LoginView.relogin` + `LoginCommand`；A10 `AccountDiscoverResp/MachineDiscoverResult`；A11 `LoginHomeReq/Resp`；A12 `LoginCheckReq/Resp/Item`。
- `internal/agentd/login_api.go`（新）：
  - `POST /api/logins/home`：agent 五值白名单；目录名 `[a-z0-9-]`；`os.MkdirAll(~/.handoff/home, 0700)` 后 `Lstat` 拒绝软链根，再 `Mkdir` 新目录（已存在 409，umask 后显式 `Chmod 0700`）；`accounts.LoginMainHomeOnly(kind, GOOS)` → darwin 上 claude 400 并说明「钥匙串同机共用、请直接在主 HOME 登录」；命令表 `opencode console login / opencode auth login -p <provider> / codex login（note device-auth）/ grok login / agy（交互）/ claude auth login`，全部 `HOME=<abs>` 前缀。
  - 他机：`forwardJSONKeepQuery` 一跳，目标机 `X-Handoff-Forwarded` + `machine` 自证；入口机用 `config.SSHHost` 补 `ssh user@host`（relay/本机为空）。
  - `POST /api/logins/check`：`~` 或 `~/.handoff/home/<dir>` 白名单（目录名同 Q18 字符集）；`accountwork.Discoverer.CheckHome` 单 HOME 发现（`Discovery.Partial`，不把本机其他 HOME 的登录标失效）；回 `new_account`/`account_logins`/账号名。
  - `POST /api/accounts/discover`：本机自名 + 设备表全队并发扇出（30s/机），单机失败只写该行、整体 200；无 machine 只跑一台；带防环头只跑本机；404 →「该机 agentd 版本过旧」。
- `internal/agentd/forward.go`：`forwardJSON` 与新增 `forwardJSONKeepQuery` 共用 `forwardJSONWithQuery`。
- `internal/agentd/accountwork/discoverer.go`：新增 `CheckHome`（单飞、Partial、不触发 §5 迁移）与 `expandLoginHome`。
- `internal/scheduling/accounts/discover.go`：`Discovery.Partial`（部分扫描不误标已失效）、`ReconcileResult.NewAccountLogins`（新账号归属供检查回答）。
- `internal/agentd/account_api.go`：`accountView/loginView` 改为 `Server` 方法以注入 relogin 的 ssh 行；注册段 3 路由。
- `internal/agentd/server.go` + `cmd/agentd.go`：`accountDiscoverer` 字段、`SetAccountDiscoverer` 组装点注入。
- `internal/config/config.go`：`SSHHost(Target)` 共用换算；`cmd/attach.go` 的 `sshHostFromTarget` 改为委托。
- `internal/client/accounts.go`：`DiscoverAccounts(ctx, machine)`（CLI 消费；agentd 扇出用 `MarkForwarded` 复用同一方法）。
- `cmd/account.go`：`account discover [--machine]`，每机一行、任一失败退出 1。
- `cmd/squad.go`：载体账号格 `账号名 · 机器 HOME`（主 HOME 显示 `~`）。

### 命令与读数（本段真实跑到）

- `go build ./...` → 无输出（通过）。
- `go vet ./internal/agentd/... ./internal/scheduling/... ./internal/proto/... ./internal/client/... ./cmd/... ./internal/config/...` → 无输出。
- `gofmt -l <改动的 .go>` → 空。
- 先红：
  - 新符号首红为编译红（`undefined: loginHomeDir / proto.LoginHomeResp / loginSSHLine …`）；cmd 侧 `account discover` 为断言红（请求为空、`unknown flag: --machine`；账号格仍只打账号名）。
  - 空壳/实现后补一次变异断言红：临时让 `loginSSHLine` 恒返回空串 → `TestLoginHomeForwardKeepsQueryAndAddsSSH`（"入口机应补 ssh 行…: \"\""）、`TestLoginSSHLineOnlyForDirectRemoteTarget`（machine "direct" ssh = ""）、`TestAccountViewReloginCarriesCommandAndSSH`（远端 relogin 无 ssh）三红；还原后 `grep -c MUTATION-B464S3 internal/agentd/login_api.go` = 0。
  - 契约 fixture：新增三个文件 + `AccountsResp` 漂移 → 先红（3 个 fixture 不存在、AccountsResp 不一致）→ `-update` → `go test ./internal/proto/ -run TestContractFixtures` ok。
- 触及包测试（`env -u CLAUDE_CODE_SESSION_ID go test -count=1`）：
  - `./internal/agentd/...` ok（末次全量 agentd 309.1s、accountwork 0.24s；此前同批 281.5s）
  - `./internal/scheduling/...` ok（scheduling 23.4s、accounts 0.04s）
  - `./internal/proto/` ok、`./internal/client/` ok（17.1s）、`./cmd/` ok（142.0s）、`./internal/config/` ok
- `go test -race -count=1 ./internal/scheduling/accounts/ ./internal/agentd/accountwork/` → 均 ok。
- web 契约镜像（临时软链主仓 `web/node_modules`，跑完删除）：`npx vitest run src/api/contract.test.ts` → 65/65 通过。
- 全量跑 accounts 包时暴露的既有时间炸弹（见偏离 1）：`--- FAIL: TestMergeProbeAndMarkSeparateRowsDoNotBumpDefinition（有撞额度标记应为无额度，得 fail）`、`TestMergeProbeClearsMarkOnlyWhenStartedAfterMark（开始早于标记的探测抹掉了标记）`；根因是固定 `base` 超出 `DiscoveryFreshFor`；夹具改 `time.Now()` 后该包 ok（断言未改）。

### 未验证 / 交后续

- R3（需用户本人登录）、R10（README/`--help` 与命令表一致）属主会话真机项，本段未跑。
- 前端 `web/src/api/accounts.ts` 尚未镜像 `relogin` 与新 fixture 类型（段 5 范围）。
- `opencode` 新增登录默认给 OAuth 命令，第三方 key 用法放在 note；若产品要求新增登录直接建 apikey 载体，需在段 5/后续明确入口（A11 的 agent 五值不含 apikey）。

### 提交（提交当时的命令与输出；末条随后 amend 收进本台账，hash 会变）

- `git commit`（全部段 3 改动 + 本台账）→ `cadcab14 feat(B464): 新增登录、单 HOME 检查、立即发现扇出与 CLI discover（段 3）`，`23 files changed, 1586 insertions(+), 50 deletions(-)`。
- 提交前 `go build ./...`、`go vet`（六个触及包树）、`gofmt -l` 均无输出；`git status --porcelain` 仅本台账待 amend。

## 真机 R2/R4/R5/R6/R8（2026-10-22，主会话；段 2 二进制 8cd3a9d670f0，隔离 t464-mac/t464-linux，测试账本 handoff_b464）

- **发现全 kind（S1/S7）**：t464-linux 认出 codex（主 HOME ok + `~/.handoff/home/codex` 证据型 expired，label 邮箱）、agy（主 HOME，label 邮箱，Q7(a)）、opencode-go（主 HOME opencode.db，与 t464-mac 主 HOME 归并为一账号两登录）、muse 的 opencode-go API key、第三方 key ×5（deepseek 两登录：主 HOME 与 muse 归并；alibaba-token-plan-cn、kimi-for-coding、openrouter、self），全部 na。muse 的 opencode-go key 探测得 credential_expired 后下一轮登录转 expired、账号读作「没有可用登录」（no_login）。codex 首轮经 codex HOME 登录探测失败后换主 HOME 登录探测转 ok。
- **R4 已失效**：`~/.handoff/home/t464-key` 放哨兵 key（provider t464prov，非真实凭据）→ 账号 `t464prov-key-739b`（na）；载体 t464-oc-key 选该登录（免费模型以便检测上线）→ online；删文件 → 下轮发现登录 gone、载体 login_state gone；`dispatch --receiver t464-oc-key` → 400「载体 t464-oc-key 的登录已失效：t464-linux ~/.handoff/home/t464-key」。（载体未上线时先报「载体未上线」——准入先查载体状态，符合预期。）
- **R5 代理**：`PUT /api/logins/login-2b5aa…/proxy` 设 `http://127.0.0.1:1080`（socks5 形态下 opencode 检测 UnknownError，换 http 即 online——属 opencode 对代理形态的支持，不是本卡缺陷）；载体 t464-oc-lnx 选该登录 → 任务环境 `HTTPS_PROXY/HTTP_PROXY/ALL_PROXY=http://127.0.0.1:1080`、`NO_PROXY=localhost,127.0.0.1,…`、`HOME=/root`。探测：读面显示探测登录为该 linux 登录且成功，**探测器日志未带代理字段**（观测缺口，交审查）。
- **R6 改名与换探测登录**：`PUT /api/accounts/opencode-go-1e5f?expect=1 {"name":"og-xsx"}` → 成功，载体 t464-oc/t464-oc-mac/t464-oc-lnx 的 account_name 同步；撞名 409 文案「账号名字 "og-xsx" 已被账号 opencode-go:1e5f72dfee52 占用」；`bad:name` 被拒。停 t464-linux（07:43:33Z）→ 07:46:08Z probe_login 由 linux 登录换到 mac 登录，`account probe og-xsx` 成功。重启 t464-linux。
- **R8**：`no_account` 载体 t464-free（opencode 免费模型）home_dir `''` → detect online → 任务回报 `HOME=/Users/df-zidong`；第三方 key 载体 t464-ds（deepseek 主 HOME 登录，模型 `deepseek/deepseek-flash`）→ online。claude 部分未验（两台 Mac claude CLI 均未登录，需用户登录，并入用户批次）。
- **R2 跨机归并**：opencode-go 已由 R1/R2 证实；claude 跨 Mac 归并并入用户批次。
- **观察到的问题（交审查）**：F1 opencode-go 账号无 label、自动名用 `opencode-go-1e5f` 而非邮箱——`opencode.db` 的 `account.email` 列可作 label（R0-4）；F3 探测日志不含所用代理（打码）；F4 `-shm`（R1 ⑤）。

## 主会话复核段 3（2026-10-22，runner 小队任务 41ffd5a0@linux-01，分支 cards/B464-charter 363a5485）

- 文件清单 23 文件 +1591/−50，主体 `internal/agentd/login_api.go`、`b464_seg3_login_test.go`、`cmd/account.go`、`internal/proto/scheduling.go`——非空转。快进合回 cards/B464-charter-1。
- 偏离 1（`seedAccount` 发现时刻改 `time.Now()`）：认可——段 1/2 夹具写死 2026-10-22 08:00Z，真实时钟越过 base+3min 后可探登录新鲜度把两例推成 no_login，是夹具时间炸弹，只改夹具不改断言。偏离 2–6 在 §9 权限内。偏离 7（未 push）：节点 pass 后执行机已推送 cards/B464-charter，无影响。
- 审批：本任务工单 ~6 张全为工作树内 python/cat 改写，均因共享审批链把分支任务误读为只读而升级（根因 `approver_context.go:66` 无条件附 readonly 解释，已提修复会话），主会话用受限自动批准循环放行（仅本任务工作树内 python 改相对路径 + go 工具链 / docs 追加 + 只读 git），一张台账 amend 人工批准。
## implement 段 4（2026-10-22，handoff runner 小队）

- 执行者：`card dispatch --step implement` 派出的 runner。**基线核对**：派发文写「分支 `cards/B464-charter-1` @ `606f236d`」，实际工作树在 `cards/B464-charter-2` @ `363a5485`（含段 1–3；`606f236d` 在本仓不存在，`origin/cards/B464-charter-1` 在 8cd3a9d6，是本分支祖先）。按「只在当前分支工作、不切分支」纪律就地执行，收尾 branch 为 `cards/B464-charter-2`。
- 范围：只做 plan §7 段 4（Q9 协调者主 HOME 整段跳过；删除清单剩余项；Q8 agy token 软链；契约 fixture 刷新），不碰段 5 的账号页/载体对话框重做。
- 纪律：零真机动作（不起/不重启 agentd、不读真实凭据、不连生产账本）；测试一律夹具 HOME/临时目录；Go 测试 `env -u CLAUDE_CODE_SESSION_ID`；只 commit 不 push。

### 偏离与自主决定

1. **`ResumeReq.HomeDir`（自行拍板，超出 plan §6 修改清单的字面文件集）**：agy 冷恢复也调用 `WriteTaskEnv`，若只改 Start 路径，重启后的冷恢复会退回「按进程主 HOME 取 token」，与 Q8 不一致。给 `executor.ResumeReq` 增 `HomeDir`（任务行载体 HOME 快照），manager 两处 Resume 调用透传 `task.HomeDir`；不改变 Env 的 HOME 语义。无冻结契约变化（executor 包内，非 §11 条目）。对照 §9「新文件拆分/私有命名」不覆盖此决定，属为实现 S8/Q8 闭环的最小必要扩展，特此披露。
2. **前端只做删除残留（自行拍板，对照「不碰段 5」）**：§4 删除清单点名 web `probeHome`，若只删服务端端点会留下生产调用方。故最小删除 `web/src/api/scheduling.ts` 的 `ProbeKind/HomeProbeReq/HomeProbeResp/probeHome`、`SchedulingPage.tsx` 的 HOME 实时探测 effect 与提示、对应测试与两个 fixture；未重做账号页/载体对话框（留段 5）。`defaultHomeDir` 仍留（段 5 重写载体对话框时一并处置）。
3. **契约 fixture 无漂移、未跑 `-update`**：段 4 删除的是未再注册的 `HomeProbeReq/Resp` fixture 与已删 wire 类型，剩余类型的序列化结果逐字节未变；`TestContractFixtures` 直接绿，故没有可红的 fixture 漂移，未执行 `-update`（不是跳过）。
4. **Q9 判据的失败回退（§9 授权内）**：`sameHomePath` 先 `filepath.EvalSymlinks` 两侧比较；解析失败（目标不存在等）退回 `filepath.Clean` 字面比较。
5. **agy 软链的重复准备（§9 授权内）**：同一 taskDir 再次 `WriteTaskEnv` 时先删旧 token（链接或历史副本）再建链，避免 `os.Symlink` 因已存在失败。
6. **既有测试改写（§9 允许类）**：删除 `ProbeHome` 四个测试与 `TestCredentialPolicyLiterals`；agentd 远程转发测试去掉 probe 段、只保留 wake 的登录 roundtrip 与未知机器 400；`newProbeHost()`（注入凭据路径表）改为 `hostapi.New()`。

### 实现落地

- **Q9 协调者供给**（`internal/orchestration/coordinator_home.go`）：展开 → 绝对路径后先判定目标是否为主 HOME（EvalSymlinks 后比较，含软链同路径）；是则记 Info「协调者供给目标即主 HOME，跳过全部写入」并返回，不调 `Profile.Prepare`、不投影 config/provider、不装规则/skills、不 MkdirAll。非主 HOME 路径行为不变（仍 reject 白名单软链、写投影配置、写规则/skills）。
- **删除只读探测**：删 `hostapi.ProbeHome/ProbeRequest/ProbeReply/ProbeKind`、凭据路径解析器（`Host.resolveCredential`/`NewWithCredentialPathFor`/`resolveCredentialPath`/`mainHomeHasCredential`/`applyMainHomeSync`）、`WakeHome` 内对凭据表的判态；`inspectHome` 收窄为 `resolveHomePath`（只展开 + stat/目录校验，不存在交给 WakeHome 报需要登录）。删 `POST /api/host/probe` 与 `handleHomeProbe`、`proto.HomeProbeReq/Resp`；`cmd/agentd.go` 组装改 `hostapi.New()`。`toolchain.CredRelPathFor` 保留给 quota 取材。
- **删 `ProfileReq.Credential` / `executor.CredentialPolicy` 两常量**（`internal/executor/profile.go`）；协调者供给不再传凭据策略。
- **Q8 agy token**（`internal/executor/agy/taskenv.go`）：token 源改为载体登录 HOME（空 = 主 HOME；`~`/`~/…` 在目标机展开，相对路径拒绝），任务 HOME 内 `antigravity-oauth-token` 与 `.orig-google-oauth`（存在时）都改为指向登录 HOME 原件的单文件软链，不再 `ReadFile`+`WriteFile` 拷贝。登录 HOME 缺 token → 可行动错误（点名该 HOME、说明在该 HOME 登录、明确不会从其他 HOME 复制）；软链建立失败也带源/目标上下文。建立成功各一条 Info。
- **冷恢复**：`agy/resume.go` 冷恢复把 `req.HomeDir` 传给 `WriteTaskEnv`；manager 两处 Resume 传 `task.HomeDir`。
- **注释与残留**：`schedapi.carrierView` 删掉 AccountMismatch/cred_fingerprint 的过时说明；`status.go`、`profile.go`、`hostprobe.go` 中 ProbeHome/main_home_sync 文案同步；`proto` 契约测试的 `TestHomeWakeReqOmitsEmptyCredential` 改为 `TestHomeWakeReqOmitsEmptyLogin`（锁 `login` 缺席）。
- **契约 fixture**：从用例表删 `HomeProbeReq/Resp`，删 `web/src/api/testdata/HomeProbeReq.json`、`HomeProbeResp.json`；web 契约测试与 fetch 测试去掉 probe 引用。

### 红证据（先红/变异补证，均还原后复跑绿）

- Q9：`TestCoordinatorHomeMainHomeSkipsAllWrites` 先以临时变异让 `sameHomePath` 恒 false → `--- FAIL: TestCoordinatorHomeMainHomeSkipsAllWrites（目标 = 主 HOME 时 Prepare 应成功跳过写入: 协调者隔离 HOME 白名单路径是 symlink …main-home-link）`；还原后 `ok`，`grep -c MUTATION-B464S4` = 0。
- Q8：临时让 `resolveLoginHome` 忽略入参恒回主 HOME → `--- FAIL: TestWriteTaskEnvLinksTokenFromLoginHome（软链目标 = <主 HOME token>, want 登录 HOME token）`、`--- FAIL: TestWriteTaskEnvMissingLoginTokenIsActionable（登录 HOME 缺 token 时 WriteTaskEnv 应失败）`；还原后 `ok`，标记计数 0。
- 删除型改动的「红」是编译/调用面消失：`ProbeHome`/`HomeProbeReq`/`CredentialPolicy` 删后 `go build ./...` 通过、全量触及包测试通过；probe 转发测试改为只保留 wake 后通过。

### 命令与读数（本段真实跑到）

- `go build ./...` → 无输出。
- `go vet ./internal/hostapi/... ./internal/orchestration/... ./internal/executor/... ./internal/proto/... ./internal/agentd/... ./internal/scheduling/... ./cmd/... ./internal/toolchain/...` → 无输出。
- `gofmt -l <改动的 .go>` → 空。
- `env -u CLAUDE_CODE_SESSION_ID go test -count=1`（触及包全树）→ 全 ok：hostapi 1.1s、orchestration 86.0s、executor 0.2s、agy 0.3s、claudecode 4.0s、codex 5.9s、grok 1.3s、opencode 25.2s、proto 0.01s、agentd 317.6s、accountwork 0.2s、scheduling 24.6s、accounts 0.05s、cmd 147.7s。
- `go test ./internal/proto/ -run TestContractFixtures` → `ok`（无漂移，未 `-update`）。
- web（临时软链主仓 `web/node_modules`，跑完删除）：`npm run typecheck` 通过；`npx vitest run src/api/contract.test.ts src/api/scheduling.fetch.test.ts` 通过；全量 `npx vitest run` → 148 文件过、2003 例 11 红（AccountsPage 5 + SchedulingPage 6），与段 1/2 的既有红同源（手工登记/凭据枚举/按名选账号/指纹红字），本段删掉 1 例 probe 测试后由 12 红降为 11，无新增红。

### 未验证 / 交后续

- R9（协调者真机拉起、沙盒 HOME 零写入）、R13（agy 任务 token 软链真机）属主会话真机项，本段未跑。
- 段 5 重写账号页/载体对话框时消解上述 11 例既有红，并处置 `defaultHomeDir` 等 B453 残留。

### 提交（提交当时的命令与输出；末条随后 amend 收进本台账，hash 会变）

- `git commit`（全部段 4 改动 + 本台账）→ `66f9bd4e feat(B464): 协调者主 HOME 零写入、删只读探测与凭据模式残留、agy token 软链（段 4）`，`37 files changed, 441 insertions(+), 587 deletions(-)`；提交后 `git status --porcelain` 空。

## 主会话复核段 4（2026-10-22，runner 小队任务 f3ae6066@linux-01，分支 cards/B464-charter-2 f5ca9b1e）

- 文件清单 37 文件 +441/−587（删除为主：`/api/host/probe`、`ProbeHome` 族、hostapi 凭据路径解析器、`ProfileReq.Credential`、web `probeHome`；新增 Q9 主 HOME 跳过、agy token 软链、`ResumeReq.HomeDir`）——非空转。`--no-ff` 合回 cards/B464-charter-1，台账冲突两侧保留。
- 偏离 1（基线身份）：派发起点由节点取上轮分支 cards/B464-charter@363a5485，未取卡 base_branch charter-1@606f236d（仅台账差异）——记为 handoff 卡派发基线解析行为，非本卡缺陷，落 roadmap 观察。偏离 2（`ResumeReq.HomeDir`）：认可，agy 冷恢复不透传登录 HOME 则 Q8 不闭合。偏离 3–6：§9 权限内。
- 审批：本任务约 4 张工单人工/循环放行，同 B465 根因。

## implement 段 5（2026-10-22，handoff runner 小队）

- 执行者：`card dispatch --step implement` 派出的 runner。起点 `505c8f2d`（卡补充要求先 `git fetch && git merge --ff-only origin/cards/B464-charter-1`，已快进成功；本分支 `cards/B464-charter-3`）。
- 范围：只做 plan §7 段 5（S1–S7 形态）。不改 Go 后端逻辑；契约 fixture 样本按段 2 台账的「段 5 一并换样本」处理（见偏离 1）。
- 纪律：零真机/浏览器验收（R11 归主会话）；不读真实凭据、不起 agentd、不碰生产账本；只提交不 push。

### 偏离与自主决定

1. **改了 Go 侧契约 fixture 样本（`internal/proto/contract_fixture_test.go`）**：段 2 台账写「既有样本不改（含 `apikey:opencode-go` 的 out 样本……段 5 重写账号页时一并换样本）」。本段把它换成 `apikey:deepseek` / `deepseek-key-77d0`，`go test ./internal/proto/ -run TestContractFixtures -update` 只重生成 `web/src/api/testdata/AccountsResp.json`。这不是后端逻辑改动，属 plan §9 类 (4)「契约 fixture 刷新」授权范围。
2. **修了两处与段 5 无关的既有 lint 错误（自行拍板）**：`web/src/app/tree/MobileWorkspace.tsx` 的三元表达式语句改成 if/else、`web/src/app/workbench/TerminalTab.test.tsx` 的未用参数改成 `void cb`。原因：补充⑦要求 `npm run lint` 绿，而基线已有 2 个 error（非本段引入）。只改表达式/参数、无产品行为。**不在 plan §9 明确授权列表内，特此披露。**
3. **删除 `defaultHomeDir` 与其两处断言**：plan 段 5 明确「处置 `defaultHomeDir` 等 B453 残留」；该函数只服务已删除的「载体手填 HOME」。不是删测试凑绿（无功能替代项）。
4. **组件拆分与轮询实现**：`Logins.tsx`（登录子表/代理内联编辑/复制命令）与 `LoginWizard.tsx`（三步、5s 轮询）为新增文件；间隔固定 5s，文案照原型。属 §9「前端组件拆分、轮询实现、空态/提示措辞」授权。
5. **空态链接在机器是本机别名时不带 `machine=`**：载体对话框「新增登录」链接若机器为 `本机/local/空`（控制台不知道自名），只预选 `agent`；真实机器名照带。规格要求预选机器，别名情形无法给出服务端可用的机器名，故披露。
6. **账号页数据面**：`store.ts` 快照新增 `discovery` 与 `applyLogin`（代理回执就地合入）；不新增第二个拉取者。属内部实现调整（§9 授权）。
7. **既有测试重写**：`AccountsPage.test.tsx` 从 B453 的登记/条目/HOME 形态整体重写为六组+登录子表+三步新增登录；`SchedulingPage.test.tsx` 的载体对话框从「HOME/凭据模式/账号名」改为「登录/无账号」；`model.test.ts` 的 kind 顺序与 `credentialMoved`/`entryText` 断言随 B464 模型重写。均为段 5 新形态的对应断言，未删测试凑绿；原 11 例红（段 1/2 删掉的旧行为）全部消解。
8. **未做与后续**：R11 浏览器形态验收、R3/R6 用户登录类真机项未跑；`opencode` 新增登录仍给 OAuth 命令，第三方 key 的具体 provider 入口只在命令 note 里（A11 agent 五值不含 apikey）。

### 实现落地（按文件）

- `web/src/api/accounts.ts`：补 `LoginCommand/relogin`、`LoginProxyInput`、`LoginHomeResp`、`LoginCheckResp/Item`、`MachineDiscoverResult/AccountDiscoverResp`；新增 `putLoginProxy`（A9）、`createLoginHome`（A11）、`checkLoginHome`（A12）、`discoverAccounts`（A10）。
- `web/src/app/accounts/model.ts`：六组 `KIND_ORDER`/`KINDS`（新增 claude/apikey 与 `quota`/`cli`）；`supportsProbe`/`quotaAccounts`；登录状态词与提示、凭据时刻、HOME 文案、重新登录命令文案；`machinesOf/machinesText`；`dispatchHint`（已失效暂停、已过期只提示、低/无额度暂停、na/未知照常、未选由调用方）；`FAIL_BRIEF` 补 `no_login`；`statusPhrase/updatedText` 补 `na`；删 `entryText/credentialMoved`。
- `web/src/app/accounts/Logins.tsx`（新）：机器/HOME/来源/指纹/状态/代理/载体子表；代理行内编辑（PUT A9）；复制服务端 `relogin` 命令；已过期/已失效红字提示。
- `web/src/app/accounts/LoginWizard.tsx`（新）：三步新增登录；目录名 `[a-z0-9-]`、建 HOME 409/400 原文、5s 轮询 + 立即检查、新账号/已有账号两种结果、关闭兜底说明。
- `web/src/app/accounts/AccountsPage.tsx`：六组卡片、登录子表、改名（内联）、阈值/间隔保存、立即探测（仅可探 kind）、在其他机器登录预选、删除对话框（写明下次发现会重新出现）、页头「上次发现 · 立即发现」逐机报错、`?login=new` 预选。
- `web/src/app/accounts/store.ts`：快照带 `discovery`、`applyLogin`。
- `web/src/app/accounts/StatusBar.tsx`：底栏/使用量弹层只列 `quotaAccounts`。
- `web/src/app/accounts/MobileAccounts.tsx`：六组、多机文案、na 不显示立即探测。
- `web/src/app/settings/SchedulingPage.tsx`：载体对话框改为「机器 → 执行器 → 账号登录」；兼容 kind 过滤；同账号同机多登录用「账号 — HOME」；末尾「无账号 · 主 HOME」；空态链到账号页；只读 HOME/来源/代理/共用；删 HOME 输入、凭据模式、指纹红字；载体行显示「账号名 · 机器 HOME」、登录状态与派发提示 + 复制登录命令。
- `web/src/app/shell/Shell.tsx`：`/accounts?login=new&machine=&agent=` 转给页面预选。
- `web/src/api/scheduling.ts`：删 `defaultHomeDir`；`web/src/api/contract.test.ts` 补 A9–A12 与 `relogin` 断言、apikey 样本改 deepseek。
- `web/src/app/accounts/accounts.css`：登录子表、三步对话框、发现行、阈值行的样式（沿用控制台灰阶 token）。

### 红证据（先红后绿）

- 新 `AccountsPage.test.tsx` 首次运行：`Tests 13 failed (13)`，失败原因含 `TypeError: entryText is not a function`（旧页无法渲染）与六组/登录/三步断言缺失。实现后 `npx vitest run src/app/accounts/AccountsPage.test.tsx` → 13/13 通过。
- `SchedulingPage.test.tsx` 基线 6 红（凭据来源/HOME 输入/按名账号/指纹红字）。按新形态重写后 → 22/22 通过。
- `model.test.ts` 新增登录状态/派发提示/na/凭据时刻断言；实现前后各跑一次，红→绿。
- 全量 `npm test` → 基线 11 红（AccountsPage 5 + SchedulingPage 6），收口 **150 文件 / 2016 例全过**。

### 命令与读数（本段真实跑到）

- `cd web && npm test` → `Test Files 150 passed (150)`、`Tests 2016 passed (2016)`，20.79s（另一次收口 23.02s 同结果）。
- `cd web && npm run typecheck` → 无输出，退出 0。
- `cd web && npm run lint` → 退出 0，`✖ 33 problems (0 errors, 33 warnings)`（warning 均为既有 fast-refresh/exhaustive-deps）。
- `env -u CLAUDE_CODE_SESSION_ID go test ./internal/proto/ -run TestContractFixtures` → `ok`；`-update` 只改 `web/src/api/testdata/AccountsResp.json`。
- `go build ./...` → 无输出（通过）。
- 末次改动（空态链接/发现行）后复跑 `AccountsPage.test.tsx` 13/13、`SchedulingPage.test.tsx` 22/22、`typecheck` 通过。

### 提交

- `git commit -m "feat(B464): 账号页六组与登录子表、新增登录三步、载体选登录形态（段 5）"` → `5246020a feat(B464): 账号页六组与登录子表、新增登录三步、载体选登录形态（段 5）`，`24 files changed, 1417 insertions(+), 561 deletions(-)`；提交后 `git status --porcelain` 空。本条随后 amend 收进同批提交（hash 会变，按台账纪律不回写 amend 后 HEAD）。

## 主会话审段 5（2026-10-22）

- 文件清单 24 文件 +1418/−561：web 账号页/新增登录三步/载体对话框为主，Go 侧只换契约 fixture 样本（apikey:opencode-go → apikey:deepseek）——非空转、非越轨。
- 偏离与自主决定已核：1（fixture 换样本）§9(4) 内；2（修两处既有 lint error：MobileWorkspace 三元语句、TerminalTab 未用参数）不在授权列表但已披露、零产品行为，认可；3（删 `defaultHomeDir`）plan 段 5 明文；4/6 内部实现 §9 内；5（本机别名不预选 machine）认可，R11 时核链接行为；7（既有测试重写清单）记入 review 待审项。
- 本地复跑（合并提交 682ebf3c 树）：`npm run typecheck` 退出 0；`npx vitest run` → `Test Files 150 passed (150)`、`Tests 2016 passed (2016)`；`npm run lint` → `✖ 33 problems (0 errors, 33 warnings)`；`go build ./...` 通过。
- 审批：段 5 守护循环自动放行若干 python 编辑；人工放行 2 张（`npm run lint >/tmp/lint.out`、`rm -f /tmp/lint.out`），循环随后补 npm 检查放行规则。
