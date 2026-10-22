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
