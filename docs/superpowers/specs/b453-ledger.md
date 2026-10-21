# B453 台账

## spec（2026-10-21，主会话）

- 来源：2026-10-21 B452 讨论中用户裁定（卡 note seq 29187）：主动探测而非派发失败被动读回；每个 agent 可多账号、账号可配代理；定时扫；状态 正常/低额度/无额度；claude 不做，先只做 opencode。追加裁定（同日）：一个载体只绑一个模型；opencode 载体可以用 codex 额度，故探测按账号 kind 选，不按载体 CLI。
- 卡：`handoff card move B453 spec`；分支 `git checkout -b cards/B453-charter-1 origin/main`（基线 fad1aadf）。
- 本卡起按全局规则「机械活交熟练工」做现状调查：W1 `code-investigate`（mimo@linux-01）读代码现状八项；W2 `code-investigate --receiver mini --here` 外部 provider 额度接口调研。
- 本机读数：mac-mini `~/.local/share/opencode/auth.json` 不存在（用户称已登 opencode-go，凭据位置待 W2）；linux-01 root 主 HOME auth.json 只有 `deepseek: api`。生产载体（只读）：mimo/dp/cline@linux-01 opencode-go 模型 online，mini@mac-mini opencode-go/deepseek-v4.1-flash standalone online，mbp@macbook-pro opencode/deepseek-v4.1-flash-free。
- W1 交回（任务 db010c7d，mimo@linux-01，基线 fad1aadf，exit 0）：完整报告入仓 `docs/superpowers/specs/b453-facts-code.md`。要点（现状读数，执行节点复核）：载体四态 `status.go:22-30`，ApplyDetect 顺序 不可达>限额>需登录>上线（`:106-119`）；quota 只由人工 detect 写入，roadmap 590-591 未做；Select/admitInto/AdmitFrozen/AdmitSeatCarrier/LaunchAdmit/AdmitCarrier 一律只认 online（quota 与离线同等跳过）；detect 只对 opencode 真跑回合（`driver.go:52`），失败只截 stderr 尾部（`:170-181`）；model 不解析 provider 前缀、原样 `-m` 透传（`driver.go:240-244`）；载体无专属 env，任务 env 按 executor 名映射（`manager.go:767`）；周期任务统一 ticker+ctx，组装在 `cmd/agentd.go:367-401`；Web `settings/SchedulingPage.tsx` + `api/scheduling.ts`，`GET /api/squads` 回 carriers/squads/running；registry 无 kind 白名单，纯新 kind 不升 ledgerSchemaVersion（`schema_version.go:19-22`）；roadmap 相关行 323/362/426/447/590/600/986。
- 熟练工旁见：最终文本含执行者的过程叙述前缀（「只读调查 8 项现状 — 正在…」）与协议尾注，与 B452 roadmap「尾注行」同源，待并入同一条。
- 范围裁定（2026-10-21 用户「可以，重建的新建一张卡，最后做」）：B453 = 派发时跳过低额度/无额度 + 只读任务撞额度自动换小队成员重跑；分支任务中途撞额度只标无额度、停下等人、工单写明原因。分支任务接管另建 B459（低优先级，排在 B453–B455 之后）。
- 形态裁定（2026-10-21 用户）：「单独做一个模块，新增一个 icon，专门管理账号的额度」「载体中就再选一个」「自动推导的对于同厂商多账号不太友好」→ 账号为账本独立实体、手工登记、载体选账号；展示面：控制台侧栏「账号」页 + 控制台底栏（Orca 式额度条）；「做控制台底栏了就不用做菜单栏了。移动端加到设置页面中」→ 不做桌面菜单栏，移动端在设置页。主会话提出并未被否：密钥不离机（账本只存凭据位置与指纹）、探测由账号所在机 agentd 执行、指纹不一致标红、按 L3 拆（后端首故事 + 展示面分批）、前端形态先出原型。
- 用户指路（2026-10-21）：「可以直接让熟练工看 https://github.com/stablyai/orca 是怎么实现的，orca 的很全了」→ W3 首派（主检出 B453 分支 HEAD 92ec39b9 未推送）→ `400 执行机 linux-01：基线提交在任务仓库中不存在`；改从 scratchpad 中停在 a9150118 的干净克隆重派（调研不依赖本仓状态）。落 roadmap 一条。
- 原型交本会话 subagent 做（fork `prototypes/b453-accounts/` 不入库，只读熟练工看不到）：侧栏「账号」页、全局底栏、载体选账号与指纹不一致标红、移动端设置页「账号与额度」；三种额度形态（窗口百分比 / 余额 / 仅可用与否）。
- W3 交回（任务 8a61e164，mimo@linux-01，经 4 张人工工单：克隆进产物目录被审批链判「超出仓库范围」、主会话放行）：报告入仓 `docs/superpowers/specs/b453-facts-orca.md`。要点：orca（MIT）支持 10 家额度，含 opencode-go（`src/main/rate-limits/opencode-go-usage-api.ts:11,71-88`，zen/billing 接口）、Codex（CLI RPC + wham/usage）、Claude OAuth usage 等；凭据一律只读本机文件/keychain；字段统一 session(5h)/weekly/monthly/buckets/extraUsage/planType + resetsAt；阈值硬编码 60% 警告 / 80% 紧急；多账号仅 Claude/Codex；刷新 15min、仅聚焦窗口轮询、失败指数退避、stale 缓存 30min（429 为 24h）；全局代理、无按账号代理；底栏芯片 + 详情下拉 + 溢出收纳；无跨机同步。
- 叫醒事故（2026-10-21）：W2/W3 工单挂起未叫醒主会话，用户指出。根因与修正见 B460 note（seq 29349、29351）：既有做法是 Monitor 挂 `card wait --follow <卡>`，熟练工裸派发不挂卡、`--wait` 遇工单不退出。止血：逐任务 Monitor `wait --follow`，已验证当场叫醒。
- W2 交回（任务 7b28300e，mini@mac-mini，exit 0，自动归档；途中一次偏航扫 opencode 二进制被主会话拒并改道，webfetch 工单直落人工、主会话逐张放行，落 B460 seq 29353）：报告入仓 `docs/superpowers/specs/b453-facts-providers.md`。要点（外部事实，执行节点复核）：opencode-go 官方无公开额度 API，窗口口径 5h=月上限 20%/周 50%/月 100%，用量接口只有 orca 那一路（zen/billing，未验证）；zen 免费模型无额度接口；DeepSeek `GET /user/balance` 官方，余额带币种、无重置，402 无额度/429 限速；阿里 Token Plan 个人版只能 `bl usage token-plan`（Console 登录态，5h/周百分比 + 重置时间）、团队版 OpenAPI 需 AccessKey 签名，**两路都不能用推理 Key 查**；cline-pass `GET api.cline.bot/api/v1/users/me/plan/usage-limits`（未文档化）给 five_hour/weekly/monthly 百分比 + resetsAt；Codex `chatgpt.com/backend-api/wham/usage`（内部接口）给 primary/secondary 窗口与 credits。
- 原型对账（据 W2）：cline-pass 由「仅可用性」改窗口形态（5h/周/月）；阿里 Token Plan 由「仅月窗口」改 5h + 周窗口，且探测凭据单列为 `bl` 登录态（与推理 Key 不同）；opencode zen（mbp 用的是 free 模型）由余额改「仅可用性」；DeepSeek 余额币种取接口返回值。「仅可用性」形态现只剩 zen free。新承重点：账号的**探测凭据可以与推理凭据不同**，登记表单要能分别填，待用户在原型里确认。
- 范围裁定（2026-10-21 用户「阿里那一条用不到，我们现在只做 grok、codex、opencode-go，grok 如果做不了就也不做了」）：主会话查 grok（仓内 grok 执行器对接 xAI Grok Build CLI，`grok login`）——官方无剩余额度接口，只有网页 Settings → Usage（周共享池，不公布数值）；来源 tracecheck.dev/grok-build/usage-limits、engadget 使用说明。按用户规则 grok 不做。B453 账号类型定为 opencode-go 与 codex 两种，均为窗口形态；余额、仅可用性两种形态与「探测凭据单列」随之出范围。DeepSeek/阿里/cline-pass/zen 的调研结论留在 facts-providers 备查。
- 更正（2026-10-21，用户指出「grok 在 orca 中是能查的」）：上条「grok 无额度接口、不做」错，主会话漏看本仓 `b453-facts-orca.md` §1 Grok 节。orca 走 grok CLI 内部接口 `GET cli-chat-proxy.grok.com/v1/billing?format=credits`（回退 `/billing`），凭据只读 `~/.grok/auth.json`（或 `GROK_HOME`）中 issuer `https://auth.x.ai` 条目，过期不自刷、等 grok CLI 刷新；字段 `creditUsagePercent`→weekly、`monthlyLimit/used`→monthly。B453 账号类型定为 opencode-go、codex、grok 三种，均为窗口形态。
- 工单治理裁定（同日用户）：熔断（人工工单超阈值自动停）不做，先观察；网页抓取不需要审批。并入 B460。
- 形态与规则裁定（2026-10-21 用户）：额度未知（未探测/探测失败）的账号照常派发；载体不再显示「限额中」，改看绑定账号的状态；底栏问题账号排前、60%/80% 两线按账号可改未被否。原型 grok 已补（subagent，fork 内 accounts.js 三种 kind，grok 只有 weekly/monthly 两窗口）。`prototypes/base/README.md` 记「确认中」一行。
- 排序裁定（同日用户「先做460」）：B453 spec 暂停，先做 B460；B460 完成后回到本卡写 spec。
- spec r1 草稿（2026-10-22，B460 收尾前写）：`docs/superpowers/specs/b453.md`。定级 L3、形态甲、不扇出；首故事 = opencode-go 真实账号登记→所在机探测→读数落账本→`account list`，先证 opencode-go 用量接口。主会话初稿一度把账号代理收窄成「只用于探测」，与已确认原型「探测与派发」相悖，当场改回（账号代理 > 执行器 env 代理键 > 机器环境）。OOS 后续项落 roadmap「B453 残余」。
- 形态返工（2026-10-22 用户看原型：「太乱了，五颜六色的。你看下 orca 的」，附 orca 底栏弹层与设置页截图）：原型交 subagent 按 orca 视觉语言重做（灰阶、彩色只落越线数字与状态短语、底栏一行弱色字 + 使用量弹层 + 二级面板、账号页按类型分组细边框卡片）；spec S8–S10 同步改写并加色彩纪律。
- 形态第二轮（2026-10-22 用户看弹层：「感觉还是太挤…hover 上去能看到就行…只显示周额度，5h 和月的 hover 上去才能看到…给 opencode、codex、grok 分出类」）：弹层去详细/精简切换，行只显示每周窗口，会话/每月进 hover 二级面板，按类型分组；底栏段也改每周口径（问题账号排前不变）；spec S9/S10 同步。
- 形态第三轮（2026-10-22 用户：「是我误会了，orca 的详细模式是这样的，三个周期的额度在同一行，底栏也会变。但是分类和 hover 出来的地方很好了」）：恢复详细/精简两种模式，详细 = 每账号两行、全部窗口同一行，底栏随模式变；分组与 hover 二级面板保留；spec S9/S10 同步。
- 形态第四轮（2026-10-22 用户：「详细模式下前面不用带 icon，这样分不清层级」）：弹层账号行去图标、相对组标题缩进，图标只留组标题；精简同理；底栏保留图标；spec S9 补层级一条。
- 形态第五轮（2026-10-22 用户：「底栏不用显示低额度和无额度，有数值和进度条就行」）：底栏问题账号也只显示细条与数值、靠颜色区分；探测失败/未探测仍用短语；spec S9 同步。
- 第五轮原型交回：subagent 自主一处——精简底栏的低额度/无额度账号显示触发窗口而非每周（codex-plus 因会话 82% 低额度、每周仅 41%，只显示每周会丢警示）；主会话认可并写入 spec S9。
- **spec r1 批准**（2026-10-22 用户原话「批准，就这么做吧」），回写头部状态行。之后按 2026-10-22 授权自主推进 plan → implement → review → finish（含合并、推送、换版）。

## plan（2026-10-22，plan subagent 出稿）

- 基线：分支 `cards/B453-charter-1` @ `a4e206d8`（已含 main/B460）。读入 plan / architecture-law / defect-families 三份 skill 正文、spec r1、facts-code/orca/providers、本台账 spec 节、`b460-plan.md` 格式。
- spec r1.1（2026-10-22 用户「再加一个 agy，orca 上也有，咱们也有。这个你自己定就行了，我要快点儿出去了」）：加 agy kind，照 orca `agy -p /usage --output-format json` + 版本门 ≥1.1.11（旧版 /usage 白花额度），每模型组 5h/周各成窗口；原型加 Antigravity 组；plan 出稿者已告知。
- 协调者来信（出稿途中）：spec 升 r1.1，新增第四种 kind `agy`（CLI 子进程探测 `agy -p /usage --output-format json --print-timeout 20s`，版本门 ≥1.1.11，不读写 agy 凭据）。本稿按四种 kind 写，agy 适配需超时与进程组清理。
- 读码（账本组装）：`cmd/agentd.go:659-700 setupLedger`——有 `ledger.dsn` 即 `ledger.OpenShared`（共享 PG），并 `SelfRegister` 进设备表；无 DSN 回退 `DataDir/ledger.db` 单机。`cmd/agentd.go:734-760` 每台挂了账本的 agentd 都在组装点 `scheduling.New(srv.SchedulingRegistry())` 构造自己的编制服务，注册表是共享 PG 的 `registry` 表（`internal/agentd/server.go:1447,1507-1550` facadeAsRegistry 适配 schedclient）。记忆 fleet-state：三台机都在 devices 表自登记（linux-01 重启撞 Supabase too many clients 即为挂共享账本的旁证）。判断：所在机 agentd **可以直接写共享账本**，读数写回不需经协调机转发；只有「立即探测」需要机间转发去触发。机队每台是否都配 DSN 属行为事实，真机清单 R0 复核（只看 agentd 启动日志 `dsn_configured=true`/设备表 updated_at，不读配置文件里的 DSN）。
- 读码（registry 原语）：`internal/ledger/registry.go:29-77 RegistryPut` 以 (kind,id) 单行 CAS，`expectVersion` 不等即 `ErrCASConflict`；body 整体覆盖、账本不解释字段。推论：定义与读数若同行，人编辑（持旧 version）与探测写入必然互相 CAS 冲突——人会在编辑对话框里频繁撞「已被修改」；分两行（两个 kind、同 id）则人写定义行、探测写读数行，互不冲突。
- 读码（调度准入）：`internal/scheduling/scheduling.go` 准入点今在 `:428 AdmitCarrier`、`:618 SelectWhere`、`:714 AdmitFrozen`、`:838 AdmitSeatCarrier`、`:1080 admitInto`（Admit/LaunchAdmit 共用），各自写 `Status != StatusOnline`。行号与 facts-code 一致（未漂移，仅 admitInto 判断行 1069→1080 为函数头/判断行之差）。
- 真机存在性检查（只 `test -f`/`ls -l`/`command -v`/`--version`/sqlite `.schema`+`count(*)`，未读任何凭据值）：
  - mac-mini（df-zidong，载体 mini HOME 空=主 HOME）：`~/.local/share/opencode/auth.json` **不存在**；opencode 1.19.3 的 `~/.local/share/opencode/opencode.db` 有 `credential` 表（0 行）与 `account` 表（1 行，列含 email/url/access_token/refresh_token/token_expiry）、`account_state`（1 行，active_account_id）。即 mac-mini 的 opencode-go 是 **opencode 账号 OAuth 登录**，不是 auth.json 里的 API key。spec S1 缺省条目 `auth.json › opencode-go` 在 mini 上不成立 → 稿首岔口 Q2。codex/grok/agy 均未装，`~/.codex`/`~/.grok`/`~/.gemini` 无凭据。
  - linux-01（root）：`~/.local/share/opencode/auth.json` 存在（条目名未读，不知是否含 opencode-go）、`opencode.db` 存在、opencode 1.19.3；`~/.codex/auth.json` 存在、codex-cli 0.163.0（载体 codex HOME `~`）；`~/.grok/auth.json` **缺**（仅 auth.json.lock），grok 1.0.13 已装 → linux-01 grok 未登录；agy 在 `/root/.local/bin/agy`、`agy --version`=1.4.4（≥1.1.11 版本门），`~/.gemini` 目录存在（登录态未判；载体 agy HOME `~`，status online）。
  - macbook-pro（xushixin，`ssh xushixin@100.91.173.63`，hostname MacBook-Pro.local）：`~/.codex/auth.json`、`~/.grok/auth.json`、`~/.local/share/opencode/auth.json`、`opencode.db` 均存在；codex/grok 在 `~/.local/bin`、`~/.grok/bin`；agy 未装；`~/.gemini` 存在。用户 2026-10-19 曾说 mbp 不做协调，但只读探测账号不是协调用途。
  - 结论（真机候选）：opencode-go → mac-mini（OAuth 账号，见 Q2）或 linux-01（auth.json，条目待证）；codex → linux-01 root（已有载体 codex 可做 S4 指纹/S6 回归）或 macbook-pro；grok → macbook-pro（唯一有 auth.json 的机器）；agy → linux-01（唯一装了 agy 的机器）。
- `handoff squad list --json`（生产只读）：载体 codex@linux-01 codex HOME `~`；agy@linux-01 agy HOME `~`；mbp@macbook-pro opencode HOME `/Users/xushixin` 模型 `opencode/deepseek-v4.1-flash-free`；mimo/dp/cline@linux-01 opencode HOME 空；mini@mac-mini opencode HOME 空 模型 `opencode-go/deepseek-v4.1-flash`；全部 online。小队 pro=[cline]、leader=[cline]、runner=[dp]——**每个小队只有一个成员**，S5/S6 换人真机验收需要在测试账本里自建两成员小队。
- 读码（立即探测转发模板）：`internal/agentd/schedapi.go:237-330 handleCarrierDetect`——入口机读载体，本机直调 hostAPI，他机经 `forwardJSON` 转 `POST /api/host/wake`，结果回入口机 `ApplyDetect` 写账本；`forward.go:54 forwardIfRequested`（`?machine=`，带 `forwardedHeader` 不再外扇）、`:116 forwardJSON`。判断：账号「立即探测」不照抄 detect（入口机写），改为把同一端点转到所在机、由所在机的探测器执行并写读数，保证「读数只由所在机写」与周期探测同一条写路径（稿首 Q3）。
- 读码（代理）：`internal/proxycfg/proxycfg.go:43 Validate`、`:89 Transport`（空串=ProxyFromEnvironment）、`:144 Redact`；`internal/config/config.go:83-100` 注释写明 `proxy:` 只作用于升级与 git、不作用于 executor。mac-mini `handoff squad list` 日志「已配置出网代理 proxy=socks5://127.0.0.1:7897」。判断：spec「没填沿用该机 handoff 进程的出网代理环境」有两读（config `proxy:` 或进程环境），mac-mini 出网 opencode.ai/chatgpt.com 是否需要代理、launchd 环境有无 HTTPS_PROXY 未验证 → 稿首 Q5。
- 熟练工/subagent 调查交回（Explore，本会话，两路，只读）：前端壳 `web/src/app/shell/Shell.tsx`（无全局 footer；dock 在 `app/tree/ProjectTree.tsx:1467-1536`；路由 `Shell.tsx:1579-1594` + `fullPageRoute :922-923`；紧凑档 `useShellViewport.ts:14-29`；移动设置 `SettingsHub.tsx:25-38,71-91`；`usePoll` 在 `app/data/usePoll.ts`；无 Popover/Tooltip 组件，图标 lucide-react；契约 fixture 由 `internal/proto/contract_fixture_test.go` 生成、`web/src/api/contract.test.ts` 镜像断言；`quota`/限额中 在 web 只见于 `api/scheduling.ts:19,21,199` 与 `scheduling.fetch.test.ts:146`）。编排侧：回合失败迁 `waiting_review` + `turn_failed`（`manager.go:3463 handleResult`、`:3526-3548`），**不迁 failed**；任务行无失败分类、无任务间链接列；`store.go:297-324` 逐列 ALTER 迁移（任务库是各机本地 SQLite，不涉 ledgerSchemaVersion）；工单 kind 仅 `gate`/`ask`，`RelayAnswer`（`manager.go:1900-1960`）对未知 kind 报错，`cmd/skilled.go:389-432 writePendingTickets` 把非 ask 一律打成 permission；`client/skilled.go:202-258 WaitFinal` 先判 `turnEnded`（含 waiting_review）后判待答工单——与 B460 F4「回合已结束 > 有待答工单」一致，S7 要退出码 5 必须改 F4（稿首 Q8）；env 三处组装 `manager.go:767/1539/3840` → `withCarrierHome`（`:1171`，先删同名键再追加）；Manager 不调编制域（`receiver_bind.go:4` 边界注释）。
- 读码（执行器失败）：`executor.Result{OK, FailReason}`（`executor/executor.go:82-97`）是任务路径唯一失败文本；全仓 grep `insufficient_quota|usage_limit|EntitlementError` 零命中；`hostapi/probe.go:430-444 classifyTurnError` 把 `quota|rate limit|too many requests` 都判 quota——429 限速与额度耗尽不分，spec「分类不确定的不算撞额度」要求拆三类（稿首 Q10）。roadmap 975：opencode 把 403 ENTITLEMENT_ERROR 写在 stdout JSON，失败原因为空——opencode 执行器的撞额度能否被分类到，**未验证，需真机**（稿首 Q17、真机 R6a）。
- 判断汇总（正文为准，此处只留指针）：计划落 `docs/superpowers/plans/b453-plan.md`（待拍板）。稿首 19 条岔口；承重三条 = Q1 定义/读数两 kind 同 id（整行 CAS 决定）、Q2 opencode-go 凭据来源（mini 为 OAuth，需用户）、Q3 所在机直写共享账本 + 立即探测转发到所在机。对已冻结 B460 F2/F4 的两条变更列 §8 F-B460-1/2。spec 更正 SC1–SC7 交协调者。
- 放弃的尝试：曾考虑由所在机探测器顺带算同机载体指纹以免改 `/api/host/wake` wire——spec S4 明写「载体检测时」算，弃；曾考虑分支撞额度停在 `waiting_answer` 以免改 F4——答复后 `ResumeIfIdle` 会迁回 running 而执行器已不在，弃（Q8）。
- 自审（plan skill「自审」节）：S1–S12 均有交付行与验收；待验证假设 U1–U8 有动作与阻塞范围；决策权限 §6；待拍板在稿首；行为闭环 12 行五格齐；段间依赖分硬依赖/施工排序（§4）；真机清单 §5.3 汇总；冻结节有结论（F1–F19 + F-B460-1/2）。
- 提交：`git commit docs/superpowers/plans/b453-plan.md docs/superpowers/specs/b453-ledger.md -m "docs(B453): plan 出稿（待拍板）…"` → `[cards/B453-charter-1 ceb8a28d] … 2 files changed, 413 insertions(+)`；本行随后 amend 一次收进同批提交（hash 会变，不回写）。
- plan 拍板（2026-10-22 主会话，891ede8f 稿）：Q1–Q19 除 Q2 备选（opencode.db OAuth 凭据位置，凭据类留用户）外全部采纳；SC1–SC7 认可、spec 不升版。详见 plan「拍板」节。

## implement 段 1（2026-10-22，本会话 implement subagent，主检出 `cards/B453-charter-1`）

- 基线：`7b1a26a8`（plan 拍板）。读入 implement skill 全文、plan 拍板节 + §0–§10、spec r1.1、facts-code、facts-orca opencode 节、本台账。
- 读码（落点核实）：registry 端口 `internal/schedclient/schedclient.go`（Put/Get/List/Delete，ErrCASConflict/ErrNotFound 由组装点翻译）；agentd 适配 `server.go:1447 SchedulingRegistry`；组装点 `cmd/agentd.go setupLedger`（shared 才 SelfRegister；返回的 cleanup 先停镜像后关库）；转发 `forward.go forwardJSON`（带 `X-Handoff-Forwarded`）、`server.go:598 remoteTarget`、`:610 IsSelfMachine`；检测转发样板 `schedapi.go handleCarrierDetect`；代理 `proxycfg.Transport`；凭据相对路径 `toolchain.CredRelPathFor("opencode")`=`.local/share/opencode/auth.json`；agentd 双机测试夹具 `hostprobe_test.go newRemoteSchedEnv`（远端 stub）——本段的转发测试改用两台真 Server 共用一个 ledger.Store 模拟共享账本。
- T1 quota opencode-go 解析与适配（`internal/quota/{quota,opencodego}.go`）：
  - 红（编译）：`env -u CLAUDE_CODE_SESSION_ID go test ./internal/quota/ -run OpencodeGo` → `undefined: Window / Category / WindowSession …`。
  - 红（断言，空壳落地后）：同命令 → `TestOpencodeGoParseUsageTable` 各子例「窗口数 = 0，want 3」「want failure parse … err=<nil>」「want blocked no_subscription, got <nil>」；`ProbeReadsEntryAndSendsBearer`「请求形状 path=""」；`CredentialFailuresAndNoLeak`「want credential_missing … err=<nil>」；`NetworkFailure…`「断网应判 network，得 <nil>」。`RegistryRejectsUnknownKind` 只见编制红（Registry 与类型同批写成，非空壳，如实记）。
  - 绿：同命令 5 个测试 PASS。
  - 判断：403 与正文含 EntitlementError 判为「成功读数 + Blocked=no_subscription」而非探测失败（plan 拍板 Q4：接口明示不可用归无额度）；429 判 rate_limited（Q10 限速不算额度）；percent 按 0–100、resetsAt 兼容秒/毫秒/数字串/RFC3339——全部是 orca 转述，**未经真机**（U1），R1 真样本回来再校正。凭据只 `os.ReadFile`；响应体先把 key 替换成 *** 再进解析/失败文本；json 解析凭据文件失败不回显 err（可能带出文件片段）。HTTP 超时 20s（§6 授权 10–30s）。失败类别在 §9 草案外增 `credential_invalid`、`rate_limited`、`upstream`、`unsupported_kind`（§9 写「至少」）。
- T2 账号域两行写（`internal/scheduling/accounts/{types,accounts}.go`）：
  - 红（编译）：`go test ./internal/scheduling/accounts/ -run 'Merge|Concurrent'` → `no non-test Go files`。
  - 红（断言）：空壳后同命令 → 「新建账号: v=0 err=<nil>」「开始早于标记的探测抹掉了标记」「无定义行不得写读数（孤儿行），得 <nil>」「定义行应为人最后一次写: version=0」。
  - 首绿后 `-race -count=30` 撞一次 `TestConcurrentHumanProbeMarkerKeepAllFields`：「读数行连续 8 次冲突」——两写者紧循环同相，一方连续落败（饿死）。修复 1 次：保留预算 8（Q1），重试间加 0~attempt ms 随机退让。复跑 `-race -count=300` → ok（5.3s）。人侧冲突 0 次（Q1 承重属性）。
  - 判断：Derive 段 1 只出 none/fail/out/ok（低额度档段 3）；已过重置时刻的窗口按 0% 计（Q4 补条）；读数行坏行按空行重建（可重建缓存，不卡死写回），定义行坏行报错；RecordProbe/MarkQuota 先查定义行，无定义不写孤儿读数行；阈值与间隔校验提前到段 1（间隔驱动探测器节拍，0 会热循环；阈值是一行次序判断），查重与 HOME 形态仍在段 2。
  - 编译：`go build ./... && go vet ./internal/quota/ ./internal/scheduling/...` 通过。
- T3 agentd 账号 API + 所在机探测器 + 组装（`internal/agentd/{account_api,account_prober}.go`、`server.go` 字段与路由注册、`internal/proto/scheduling.go` 账号 wire、`cmd/agentd.go setupLedger`、`internal/config/config.go` proxy 注释）：
  - 红（编译）：`go test ./internal/agentd/ -run AccountProbe` → `srv.SetAccounts undefined … undefined: NewAccountProber … proto.AccountView`。
  - 红（断言，空壳后）：同命令 → 三个测试「入口机登记: 404 404 page not found」。
  - 首绿前一处：转发测试 503「账号探测器未运行」——测试夹具没 Start 探测器（生产在组装点 Start），夹具补 `startProber`。非产品缺陷。
  - 绿：`go test ./internal/agentd/ -run 'AccountProbe|NoDirectHttptest' -race -count=3` ok。
  - 触及包：`go test ./internal/agentd/ ./internal/proto/ ./internal/config/ ./internal/quota/ ./internal/scheduling/...` → agentd 唯一红 `TestNoDirectHttptestServers`（我的测试直接用了 httptest.NewServer，违反 B234 夹具纪律）→ 改 `testhttp.NewServer` 后绿；其余包 ok。
  - 自审后加固（未单开红绿，属既有测试覆盖的路径）：探测写回失败或停机取消经单飞记录回传 ProbeNow → 500/503，不把旧读数当「探测成功」交回（静默失败族）；quota 每次探测后 `CloseIdleConnections`。
  - 判断：两台真 Server 共用一个 ledger.Store 模拟共享 PG——比 stub 远端多锁一条「读数行写者 = 所在机 b、入口机不写」；探测器只在 `shared`（有 DSN）时装配并 Start，cleanup 先停探测器再停镜像、关库；`AccountsClient` 是 agentd 侧新使用方接口（List/Get/Put/RecordProbe 四法，消费点见文件注释），没往 `SchedulingClient` 加方法；PUT 回 AccountView（F2 未定回包形状）；AccountView.proxy 原样回显（可能含 user:pass，日志一律 Redact）——留给协调者裁。
- T4 CLI（`cmd/account.go`、`internal/client/accounts.go`）：
  - 红：`go test ./cmd/ -run TestAccount` → `unknown command "account" for "handoff"`（三测试）。
  - 绿：同命令 ok。`go test ./cmd/ ./internal/client/` → cmd ok（77s）；client 唯一红 `TestProductionHTTPClientCallersAreGatewayOnly`，命中清单全是 `.claude/worktrees/*` 嵌套工作树副本——memory「handoff-env-sensitive-tests」所记环境假红，与本卡无关。
  - 判断：probe 的「失败结论」按「最近尝试晚于最近成功读数」判，不按 state（撞额度标记优先会把失败掩成 out）；表格多一列「说明」放 `△ 原因`（S11 列之外，便于终端判断）。
- 收尾编译：`go build ./... && go vet ./...` 通过。
- 提交：T1+T2 `c94944f6`；T3+T4 `c929fb97`（均只按显式路径提交，未碰 b434-ledger.md / .playwright-mcp / prototypes）。段 1 交回主会话，R1 由协调者在隔离实例上跑。

## 真机 R1（2026-10-22，主会话；隔离实例 t453-linux @ linux-01:7795、t453-mac @ mac-mini 127.0.0.1:7796，共享测试库 handoff_b453；二进制 f65e5628 普通 go build）

- 段 1 交回裁决：偏离 4（403/EntitlementError → 读数 blocked、状态无额度）采纳，以 Q4 为准；偏离 7（AccountView 原样回显 proxy 含 userinfo）**不采纳**——响应中代理 userinfo 的密码段打码，PUT 收到打码值时保留原值，并入段 2；其余偏离认可。
- 凭据条目存在性（只读条目名与 type，不读值）：linux-01 root 主 HOME auth.json 仅 `deepseek: api`；载体 HOME 中只有 `/root/.handoff/home/muse` 含 `opencode-go: api`。
- 登记 `og-muse`（kind opencode-go，machine t453-linux，home `/root/.handoff/home/muse`）→ PUT 200，state none。立即探测 → 真实调用 `https://opencode.ai/zen/go/v1/usage` 得 **HTTP 401 `api_key_expired`**（「This API key has expired. Create a new API key in OpenCode Console…」）；分类 `unauthorized`，state fail，读数写回共享账本，日志 `opencode-go 额度接口结果为失败 status=401`、`账号探测失败 category=unauthorized`，未见请求头。结论：接口存在、Bearer API key 鉴权形态成立（U1 部分证实）；成功响应的字段形状（percent 量纲、resetsAt 格式）**仍未验证**——机队无有效 opencode-go API key。
- 转发：t453-mac `GET /api/accounts` 读到 og-muse（共享账本）；`POST /api/accounts/og-muse/probe` 在 mac 发起 → linux 执行（linux 日志「账号探测失败」计数 1→2，last_attempt_at 前进），mac 不写读数。
- 反例：t453-mac 登记 `og-mini`（home = mac-mini 主 HOME）→ 探测 `credential_missing`「凭据文件 …/opencode/auth.json 不存在（在该机执行 opencode auth login 登记 opencode-go API key）」。`handoff account list` 两行均「探测失败」+ △ 原因。
- 按 plan 拍板 Q2 (b)：首故事的成功读数改由 codex 在 R2 取得；**opencode-go 记欠交**，待用户二选一：在 OpenCode Console 新建 API key 写入某载体 HOME 的 auth.json，或同意新增 `opencode.db › account`（OAuth token）凭据位置。卡 note 已记。
- 事故（主会话）：准备 DSN 时一条打码 sed 未匹配表格，把 credentials.md 中 linux-01/vm-prod PostgreSQL 超管密码与 sq SK 打印进会话；已在卡 note 请用户轮换，记忆 never-print-credentials-registry 已存。DSN 改由脚本直写配置、不输出。

## implement 段 2（2026-10-22，本会话 implement subagent，主检出 `cards/B453-charter-1`，基线 `e0a57d71`）

- 输入：协调者段 1 裁决（偏离 4 采纳；偏离 7 不采纳 → 响应中 proxy 密码段打码 `user:***`、PUT 收到打码值保留原值）；R1 真样本（opencode-go 401 `api_key_expired` 错误体）；凭据结构事实（grok 顶层键 `https://auth.x.ai::<uuid>`，按「等于 issuer 或以 `issuer::` 开头」选、多条取未过期且 expires_at 最晚；codex 顶层 `OPENAI_API_KEY/auth_mode/last_refresh/tokens{access_token,account_id,id_token,refresh_token}`，OPENAI_API_KEY 可为 null；agy 以账号 home_dir 作 HOME 运行、不硬编码子目录）。
- 读码：进程组终止样板 `internal/hostapi/group_unix.go`（Setpgid + 负 pid SIGKILL）；agy 执行器找二进制用 `exec.LookPath("agy")`（`executor/agy/proc.go:68`），agentd 启动时 `pathenv.Apply` 已扩 PATH，探测器同用 `exec.LookPath`。
- T5 quota 其余适配、指纹、V5（`internal/quota/{codex,grok,agy,fingerprint,httpx,procgroup_unix,procgroup_other}.go`，opencodego.go 改用共用出网；testdata 四份）：
  - 红（编译）：`env -u CLAUDE_CODE_SESSION_ID go test ./internal/quota/` → `undefined: ParseAgyUsage / agyIdentity / Agy / KindAgy / Fingerprint / CategoryVersionTooOld …`。
  - 红（断言，空壳后）：同命令 12 个测试 FAIL（Agy×5、Codex×2、Grok×3、`OpencodeGoRealSample401ApiKeyExpired`、`SecretsNeverLeakAcrossAdapters`）。
  - 绿：同命令 ok；`-race -count=3` ok（10.1s）。段 1 的 OpencodeGo 表原样通过（未改既有断言）。
  - 真样本：`testdata/opencodego_401_api_key_expired.json` = R1 真机错误体（协调者转录，message 末尾「…」原样）→ `credential_expired`「在 OpenCode Console 新建 API key…」。其余三份 `*.synthetic.json` 按 facts-providers §E（codex gist 样例）/ facts-orca §1（grok、agy 字段名）合成，测试文件头注明「未经真机」，R2 替换。
  - 判断：codex allowed=false/limit_reached=true → Blocked `limit_reached`（Q4 归无额度）；窗口按 limit_window_seconds 归类（≤1d session、≤10d weekly、其余 monthly，缺长度按位置）；reset_at 缺席用 reset_after_seconds；access_token JWT exp 已过 → credential_expired 不发请求；只有 OPENAI_API_KEY → credential_invalid。grok 条目按协调者事实选（等于 issuer 或 `issuer::` 前缀；未过期且 expires_at 最晚；5 分钟偏斜），credits 非鉴权失败回退 /billing、401/403/429 不回退；字段层级未知，顶层与 config/billing 子对象宽容查找。agy 版本无法识别也按版本门拒（宁可不探也不白花额度）；`exec.CommandContext` + Setpgid + `Cancel` 杀负 pid + `WaitDelay=2s`（孙进程占管道时强关）；非 unix 报 unsupported_platform；未登录按 stderr 关键字判 credential_missing（猜测，R2 校正）；窗口未知的 bucket 忽略并 Debug 日志。指纹 `sha256:` + 前 12 位十六进制，opencode-go 取 key、codex account_id、grok user_id→email、agy 信封 email→账号 id。新增失败类别 credential_expired、limit_reached、version_too_old、cli_missing。
- T6 账号域：S1 全部校验、删除、代理打码（`internal/scheduling/accounts/{accounts,types,delete}.go`）：
  - 红（编译）：`go test ./internal/scheduling/accounts/` → `svc.SetReferrers undefined … view.Carriers undefined … svc.Delete undefined … accounts.ReferencedError / ErrReferenced`。
  - 红（断言，空壳后）：「want ErrInvalid 含 "HOME" / "代理" / "og-linux", got <nil>」「读面应列出引用载体（排序）: []」「MaskProxy(...) = "socks5://user:secret@…", want "socks5://user:***@…"」。
  - 绿：同命令 ok；`-race -count=20` ok（含段 1 并发负载）。
  - 判断：查重键 = 机器 + 归一 HOME（`path.Clean`，`~`/`~/` 单独归一）+ kind + 条目；先扫后写的并发双登记按 plan §7 接受。HOME 只认绝对路径、`~`、`~/…`（`~bob` 拒）。代理在登记时 `proxycfg.Validate`（错误文本已由 proxycfg 脱敏）。打码只打密码段（`user:***@host`，裁决 7），无密码 userinfo 原样；Put 在 expect>0 且来值恰为原值的打码形式时保留原值。载体引用查询是注入位 `SetReferrers`（段 3 由组装点接编制域；未注入 = 无引用，读面 carriers = []），删除先查引用、再 CAS 删定义行、读数行删除失败只 Warn。Reading 增 `fingerprint`、`extra_usage{unit,balance,used,limit,unlimited}`（F8 的 extra_usage 形状由本段定）。
- T7 agentd：DELETE、读面打码与新键、退避、组装四适配（`internal/agentd/{account_api,account_prober}.go`、新测 `account_api_b453s2_test.go`、`internal/proto/scheduling.go`、`cmd/agentd.go newQuotaRegistry`）：
  - 红（编译）：`go test ./internal/agentd/ -run TestAccount` → `r.Fingerprint undefined … r.ExtraUsage undefined`。
  - 红（断言，proto 加键后）：「被引用应 409 并列出载体: 405 Method Not Allowed」「PUT 回执 proxy 应打码: 200 {…"proxy":"socks5://u:s3cr3t-proxy-pass@…"}」「失败 1 次满 30s: probeDue = false, want true」。
  - 修一处自伤：用 python 区间替换 probeDue 时把夹在中间的 `probeFlight` 类型一并删了（编译错 `undefined: probeFlight`），补回；非产品逻辑失败，不计修复轮。
  - 绿：`-run 'TestAccount|NoDirectHttptest' -race -count=2` ok；触及包 `go test ./internal/agentd/ ./internal/proto/ ./internal/quota/ ./internal/scheduling/... ./cmd/` 全 ok（agentd 90.6s、cmd 78.4s）。`go build ./... && go vet ./...` 通过。
  - 判断：DELETE 成功回 `{"name","version":expect}`（沿用载体删除回包形状）；拒删 409 体 `{"error","carriers":[…]}`（proto.AccountDeleteRefusal）；`AccountsClient` 增 Delete 一法（消费点 DELETE handler）。退避 `retryWait`：失败 30s×2^(streak−1) 封顶间隔，成功按间隔，全由读数行推出（重启续上）；失败日志的 next 用同一函数算。日志里代理一律 `MaskProxy`（段 1 用的 proxycfg.Redact 在账号 API 处改为同一打码口径）。组装点注册 opencode-go/codex/grok/agy 四适配，凭据相对路径取 toolchain 表，agy 走 PATH。
- 段 2 提交：T5 `5c4c17ba`；T6 `a677cc6d`；T7 `6914de70`（显式路径提交，未碰 b434-ledger.md / .playwright-mcp / prototypes；未 push）。R2 待协调者：codex/grok/agy 真样本替换三份 `*.synthetic.json`，agy 未登录 stderr 关键字、信封身份键位按真样本校正。

## 真机 R2（2026-10-22，主会话；三隔离实例 t453-linux:7795 / t453-mac:7796 / t453-mbp:7797（macbook-pro，只读 grok），二进制 9adc0cc7）

- 段 2 交回裁决：偏离 1–12 全部认可（5：agy 版本无法识别按版本门拒，更保守，认可）。
- 首次重启隔离实例时又踩 `ssh … 'pkill -f "<含自身命令行的模式>"'` 自杀坑（linux 侧 ssh 会话被杀、后半命令未执行），mac 旧进程因相对路径启动未被 `pkill -f "$S/…"` 匹配；改用 `[h]andoff-mac` 括号模式定位后重启，三实例均报 revision 9adc0cc7。
- 真实探测（登记于 t453-mac，探测在所在机）：
  - `cx-linux`（codex，t453-linux，HOME /root）→ 正常，plan_type `plus`，指纹 `sha256:281e4cdbb6bc`，session 19%（resets 19:17Z）、weekly 18%（resets 10-27），与用户同时刻 orca 截图一致（19%/18%）。
  - `gk-mbp`（grok，t453-mbp，HOME /Users/xushixin）→ 正常，指纹 `sha256:8bb629b008f0`，weekly 64%（与 orca 截图 64% 一致），**缺 resets_at、无 monthly、无 plan**——解析器未覆盖真实字段位置。
  - `agy-linux`（agy 1.4.4，t453-linux，HOME /root）→ 正常，四窗口：Gemini Models 周 13.3% / 5h 5.9%，Claude and GPT models 周 0% / 5h 0%，均带 resets_at；指纹空（真实信封无登录身份字段，按 spec 留空）。
- 真样本（在所在机用同一接口经本机代理抓取，凭据只在远端临时头文件里用、用后即删，不打印；id/邮箱脱敏后拷回 scratchpad `t453/samples/`）：
  - codex `wham/usage` 200：顶层 `user_id, account_id, email, plan_type, rate_limit{allowed, limit_reached, primary_window{used_percent, limit_window_seconds, reset_after_seconds, reset_at(epoch 秒)}, secondary_window{…}}, credits{has_credits, unlimited, overage_limit_reached, balance(字符串), …}, rate_limit_reset_credits{available_count, applicable_available_count}, model_usage, spend_control, …`。
  - grok `billing?format=credits` 200：`config{currentPeriod{type,start,end}, creditUsagePercent(float), onDemandCap{val}, onDemandUsed{val}, productUsage[{product,usagePercent}], isUnifiedBillingUser, prepaidBalance{val}, topUpMethod, billingPeriodStart, billingPeriodEnd}`——**数值包在 `{"val":…}`**，重置时刻 = `currentPeriod.end` / `billingPeriodEnd`；无 `subscriptionTier`。`/billing` 200：`config{monthlyLimit{val}, used{val}, onDemandCap{val}, billingPeriodStart, billingPeriodEnd, history[…]}`，本账号 monthlyLimit=0（= 无月窗口）。
  - agy `/usage` 200（exit 0，stderr 空）：信封 `conversation_id, status:"SUCCESS", response, duration_seconds, num_turns, usage{…tokens 全 0}, command{name:"usage", data{description, groups[{name, description, buckets[{id, name, description, window:"weekly"|"5h", remaining_fraction(float 或 int 1), reset_time(RFC3339)}]}]}}`——usage token 全 0，佐证 1.4.4 的 /usage 不耗额度。
- 代理反例：cx-linux 填 `socks5://u:secretpw@127.0.0.1:1` → PUT 回显 `socks5://u:***@127.0.0.1:1`；探测 fail `proxy`，旧读数 [19,18] 保留；linux 日志 `secretpw` 0 命中。**瑕疵**：失败文案里代理被写成 `socks5://%2A%2A%2A@127.0.0.1:1`（打码后又被 URL 转义、用户名丢失），应与回显同形。清空代理（PUT `proxy:""`）→ 回显无 proxy 键，再探测正常。
- DELETE og-mini（无引用）→ `{"name":"og-mini","version":1}`；`account list` 四行：agy 正常、codex 正常、grok 正常、og-muse 探测失败（新文案「API key 已过期（在 OpenCode Console 新建 API key…）」）。
- 待段 3 并修：grok 真实字段（val 包装、重置时刻、monthlyLimit=0 不出月窗口、无套餐时不报）；代理打码文案统一；CLI/界面百分比显示取整（读数保留原值）；三份合成样本换真样本。
