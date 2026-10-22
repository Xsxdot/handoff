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
