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

## implement 段 3（2026-10-22，本会话 implement subagent，主检出 `cards/B453-charter-1`，基线 `a63c218a`）

- 输入：协调者段 2 裁决（偏离 1–12 全认可）、台账「真机 R2」节、R2 修补五条、段 3 范围（plan §4 段 3）。
- T8 R2 修补（同轮先红后绿）：
  - 样本：协调者脱敏真样本 4 份（`codex_wham_usage_200.real.json`、`grok_billing_credits_200.real.json`、`grok_billing_200.real.json`、`agy_usage_200.real.json`）拷入 `internal/quota/testdata/`（拷前全文读过，无凭据值、id/邮箱已脱敏），`git rm` 三份 `*.synthetic.json`；opencode-go 成功体仍是测试内联合成（文件头已注明未经真机）。
  - 红：`go test ./internal/quota/` → agy「窗口[0] UsedPercent 13.310402631759644 want 13.31040263175964」（测试侧常量折叠精度，改为运行期 `usedOf` 计算，非产品问题）；grok「窗口[0] 重置时刻 = <nil>」「parse: 响应里没有 creditUsagePercent 或 used/monthlyLimit：{"config":{"monthlyLimit":{"val":200}…」；codex 真样本用例首跑即绿（reset_at 秒、balance 字符串段 2 已兼容）。
  - 红：新增 `TestProxyFailureMessageMaskedLikeAPI` →「经代理 socks5://%2A%2A%2A@127.0.0.1:1 出网失败」。
  - 红：`go test ./cmd/ -run TestAccount` →「表格缺 "月 13%"」。
  - 绿：quota、accounts、proxycfg、cmd `-run TestAccount` 全 ok。
  - 判断：grok 数值经 `num` 统一解 `{"val":N}`；周窗口重置 = `config.currentPeriod.end`，回退 `billingPeriodEnd`；月窗口重置 = `billingPeriodEnd`；`monthlyLimit=0` 不出月窗口；无 `subscriptionTier` 不报套餐。credits 成功后**不**追调 `/billing`（照 orca：credits 优先、失败回退）——真机这个账号 monthlyLimit=0，追调也拿不到月窗口，还多一次外呼。codex `rate_limit_reset_credits` 忽略（不是可用额度，是「重置次数券」，进 extra_usage 会误导）。代理打码口径收到 `proxycfg.Mask`（新函数；proxycfg 不在 plan §3 可改清单内，属偏离，见交回）：账号 API 回显、探测失败文案、quota/探测器日志共用；`accounts.MaskProxy` 改为委托它。CLI 百分比 `math.Round` 取整，读数原值不动；组名原样。
- T8 提交：`git commit … -- <路径>` → `d2215240 fix(B453): R2 修补——真样本替换合成样本、grok val 包装与重置时刻、代理打码同形、CLI 百分比取整`；工作树只剩 b434-ledger.md 与 .playwright-mcp/（红线外，不碰）。
- T9 五档推导（Q4/V6）：
  - 红（编译）：新增 `accounts/derive_test.go` → `too many arguments in call to accounts.Derive`、`undefined: accounts.RecoverAt`、`st.Blocks undefined`、`svc.Assess undefined`。
  - 红（断言，桩：Derive 换签名不改逻辑、RecoverAt 恒 nil、Blocks 恒 false、Assess 恒空）：`Derive = "ok"，want "low"`、`RecoverAt = <nil>，want 2026-10-22 13:16:40`、`低额度线调到 40、已用 50 → "ok"，want low`、`"fail": Label="fail"，want "探测失败"`、`不存在的账号应 ErrNotFound，得 <nil>`。
  - 绿：`go test ./internal/scheduling/accounts/` ok。
  - 判断：`Derive(a Account, rec, now)` 取账号低额度线（缺阈值按 80 兜底）；`RecoverAt` 取**阻挡窗口里最晚**的重置时刻——spec S5 原文「最早相关窗口的重置时刻」，两个窗口同时越线时最早那个重置后仍被另一个挡，报最早会让人按预计时刻派发照样被拒；标记/明示不可用/阻挡窗口缺重置时刻 → 恢复未知。此条与 spec 字面不同，列入交回「偏离」待裁。`Assess` 不查载体引用（准入热路径，避免 referrers 回扫全部载体）。
- T10 编制域：限额中退役 + 载体账号引用 + 统一准入（Q12/Q19、V7、V10）：
  - 红（编译）：新增 `internal/scheduling/b453_admission_test.go` → `svc.SetAccounts undefined`、`unknown field Account in struct literal of type scheduling.Carrier`、`unknown field Fingerprint … DetectEvidence`。
  - 红（断言，桩：加字段、SetAccounts 只存、CheckUsable 恒 nil、admission.go 空壳）：`存量 status=quota 读出 "quota"，want online`、`绑定账号：载体 = "quota"/…/""，want online/空/指纹写回`、`选他机账号应 ErrInvalid 且点名账号，得 <nil>`、`低额度拒/无额度拒：应 ErrNoHealthy，得 <nil>`、`第 1 次选择 = {…Carrier:A…}，want 落 B`、`冻结准入应因账号无额度拒，得 <nil>`、`AdmitSeatCarrier 应因账号低额度拒，得 <nil>`、`CheckUsable 应点名账号无额度且恢复未知，得 <nil>`、`scheduling.go 里有直接比较 Status != StatusOnline 的准入代码`。
  - 改写既有断言（plan §6 预许第 1 类，StatusQuota 随退役删改）：`status_test.go` 删「quota unreachable」用例与 quota 前置分支；「quota beats login」改为「quota is online and beats login」（want online + last_error=quota）；准入四载体用例去掉 quota 载体；Label 表删 限额中 行。编译红：`undefined: scheduling.StatusQuota` ×4。
  - 绿：`go test ./internal/scheduling/...` 三包 ok；`go build ./... && go vet ./internal/scheduling/...` 过。
  - 判断：读时归一落在 `CarrierStatus.UnmarshalJSON`（一切解码路径同一处，不做写迁移）；`ApplyDetect` 三态表 = 不可达 > 需登录（且非撞额度）> 上线，撞额度先标账号（标记失败 = 检测失败、载体不写）再写载体，账号已删/未选/未装配 → 原因进 last_error；`cred_fingerprint` 每次检测覆盖写，登记请求设的值丢弃，HOME 或账号变化时清空。统一判据 `admission.go usability`：账号引用不存在按未选放行 + Warn（§7 门禁族）；账号读取故障按准入失败上浮。全员被跳过文案 `小队 X 没有可用载体——A：账号 acc-a 低额度，预计 3h0m 后恢复；B：账号 acc-b 无额度，恢复时刻未知`；载体状态挡的写「c1：载体不可达」。新增导出 `SetAccounts`、`CheckUsable`（后者给 gateway 他机点名派发预检）。
- T9/T10 提交：`37a777d9 feat(B453): 五档推导与预计恢复、载体账号引用、限额中退役读时归一、统一准入判据与点名文案`。
- T11 检测链：三类分类、离线指纹、账号代理进检测回合（Q10/Q19/Q13/Q6、F15 的 hostapi 半边）：
  - quota 红（编译）：`classify_test.go` → `undefined: TurnFailureClass`、`undefined: TurnQuota`、`undefined: TurnRateLimited`；红（断言，桩恒 other/空指纹）：9 条「= "other"，want "quota"/"rate_limited"」、`opencode-go 离线指纹 = ""/<nil>，want "sha256:c099fb950502"`。绿：`go test ./internal/quota/` ok。
  - proxycfg 红（编译）：`undefined: WithProxyEnv`、`undefined: LocalNoProxy`；红（断言，桩原样返回）：`HTTPS_PROXY = [http://file:1]，want 唯一一行账号代理`、`NO_PROXY = []，want …`。绿：ok。
  - hostapi 红（编译）：`too many arguments in call to classifyTurnError`、`host.SetCredentialFingerprint undefined`、`unknown field AccountKind/AccountEntry/Proxy`、`reply.Fingerprint undefined`；红（断言，桩只加字段沿用旧分类）：`"usage_limit_reached: You've hit your usage limit" = "unreachable"，want "quota"`、`"HTTP 429 Too Many Requests" = "quota"，want "ready"`、`"rate limit reached…" = "quota"，want "ready"`、`"quota check failed…" = "quota"，want "unreachable"`、`指纹 = ""（kind="" …）`。绿：`go test ./internal/hostapi/` 全包 ok。
  - 判断：`quota.ClassifyTurnFailure(cli, accountKind, text)` 是唯一判据（段 4 任务链同用）；额度签名先于限速判（OpenAI insufficient_quota 本身走 429）；单独「quota」字样不再算额度（不确定判其他，spec S6）；签名全为合成（orca/公开错误码），段 4 由 R6a 真样本校正。kind 缩小签名；无账号时按执行器外壳可承载 kind（opencode → opencode-go/codex/grok）。检测链限速 → ready + 「检测回合被限速（不算额度耗尽，载体可达）」说明。离线指纹 `Registry.CredentialFingerprint` + `Identifier` 接口（opencode-go/codex/grok 实现，agy 无离线来源 → 空串不比对）；codex/grok 取身份**不判过期**（指纹问「哪个账号」）；codex 抽出 `loadTokens` 供探测与指纹共用。代理覆盖 `proxycfg.WithProxyEnv`（新文件 proxyenv.go；proxycfg 不在 §3 清单，同 Mask 一并列偏离）：删六键再追加，NO_PROXY 不覆盖、env 与进程环境都没有时补 `localhost,127.0.0.1,::1`（大小写各一），空代理原样返回。hostapi 新增 `Host.SetCredentialFingerprint`、`WakeRequest.AccountKind/AccountEntry/Proxy`、`WakeReply.Fingerprint`；指纹算不出只 Warn、不影响结局。
- T11 提交：`4eef7f2b feat(B453): 撞额度三类分类唯一判据、检测回合离线凭据指纹与账号代理覆盖`。
- T12 wire 与 gateway（F13/F15/F16、S4 投影、S5 他机点名预检、SetReferrers 接线）：
  - proto 红（编译）：fixture 用例加 `Account/AccountMismatch`、`FingerprintKind/FingerprintEntry/Account`、`HomeWakeResp.Fingerprint` → `unknown field …` ×6；加字段后红（断言）：`SquadsResp / HomeWakeReq / HomeWakeResp: 序列化结果与 fixture 不一致（契约已漂移…）`；`-update` 后绿，fixture diff 只有三份文件新增键（未改既有键）。web：`scheduling.ts` 四个接口镜像新键；`npx vitest run src/api/contract.test.ts` 58 passed、`npm run typecheck` 过。CarrierStatus 的 TS 词表删 quota 留段 5（词表随前端一起删）。
  - agentd 红（编译）：`env.svc.CarriersUsingAccount undefined`；红（断言，桩返回 nil）：`未检测时：account="" …`（投影缺键）、`选他机账号应 400 …：200`、`唤起请求应带所选账号 kind/条目/名：{"cli":"codex",…}`、`检测回合应走本机账号代理且补 NO_PROXY，得 "|\n"`、`点名他机载体而账号无额度应拒并点名，得 <nil>`。绿：五支 ok；`go test ./internal/agentd/` 全包 ok（87s）。
  - cmd 红：`TestSquadListShowsCarrierAccount` → `表头缺「账号」列`；绿：`go test ./cmd/ -run TestSquad` ok。
  - 判断：HomeWakeReq 多加一个可选键 `account`（账号名）——plan F15 只列 `fingerprint_kind/fingerprint_entry`，但 spec S4 要求检测回合走账号代理，而 Q6 定代理串不过机间 wire；目标机凭账号名在本机账号域查代理（只认所在机 = 本机的账号）。这是对 §8 F15 的增量，列入交回「需裁决」。`cred_fingerprint` 不出 wire，投影只给派生的 `account_mismatch`。SchedulingClient 新增 1 个方法 `CheckUsable`，消费点 `dispatchroute.go` 点名他机载体预检（原 `c.Status != StatusOnline`）。`coordinator_sessionref.go:61` 的在线判断不是派发准入（恢复协调者 attach HOME），未换判据。组装点：`quotaReg` 一份供探测器与 `hostAPI.SetCredentialFingerprint`；`sched.SetAccounts(acc)`、`acc.SetReferrers(sched.CarriersUsingAccount)`。
- T12 提交：`d5a10771 feat(B453): 载体账号投影与指纹比对、检测链发指纹键并标账号、目标机查账号代理、他机点名预检、引用查询接线`。
- T13 派发落点 → 任务行三列 → env 覆盖（Q6/V9）：
  - orchestration 红（编译）：`b453_proxy_env_test.go` → `unknown field Account/AccountKind/AccountProxy in struct literal of type DispatchReq`、`stored.Account undefined`；红（断言，桩只加字段不接线）：`首回合：env 缺 HTTPS_PROXY=账号代理：[HOME=/carrier/home HANDOFF_ARTIFACTS_DIR=…]`、`启动恢复：env 缺 …：[HOME=/carrier/home]`、`续接冷恢复：env 缺 …：[HOME=/carrier/home]`。绿：`go test ./internal/orchestration/ -run ProxyEnv` ok；全包 ok（38s）；store、proto ok。
  - agentd 红（断言）：`TestDispatchCarrierAccountSnapshotReachesTaskRow` → `任务行账号快照 = ""/""/""`；绿：ok。
  - 判断：任务库三列 `account/account_kind/account_proxy`（建表 DDL、逐列 ALTER、INSERT、taskColumns、scanTaskRow 五处同改）；`proto.Task` 三字段 `json:"-"`——落本机库、不进任务快照 wire（代理可能带密码，F17 只增 rerun 链接）。`withAccountProxy` 在三处都排在载体 HOME 之后、产物目录之前；日志只记 Mask 后的代理。落点机 `dispatchAccount` 读不到载体/账号 → 按未选账号派发 + Warn。
- T13 提交：`3a3eb18a feat(B453): 派发落点查载体账号快照、任务行三列、三处执行器环境以账号代理覆盖代理键`。
- T14 收尾：`web/src/api/contract.test.ts` 新增两条 TS 侧穿链断言（载体行 account/account_mismatch 键缺席 ≠ false、HomeWake 指纹键）——fixture 已由 Go 侧先红后 `-update`，TS 断言首跑即绿（如实记：无独立的 TS 红）；`npx vitest run src/api/contract.test.ts` 60 passed、`npm run typecheck` 过。
  - 收尾检查：`go build ./... && go vet ./...` 过；本段改过的 Go 文件 gofmt 无输出。触及包：`go test ./cmd/` ok（78s）、`./internal/agentd/` ok（87s）、`./internal/orchestration/` ok（38s）、`./internal/scheduling/...`、`./internal/accounts`、`./internal/quota/`、`./internal/proxycfg/`、`./internal/hostapi/`、`./internal/proto/`、`./internal/store/` 全 ok；`./internal/client/` 唯一红 `TestProductionHTTPClientCallersAreGatewayOnly`，列出的全是 `.claude/worktrees/*` 下的文件（已知环境假红，memory handoff-env-sensitive-tests）。
- 段 3 交回裁决（0a759338）：D1 采纳（预计恢复 = 越线窗口最晚重置，spec S5 措辞更正 r1.2）；D2 采纳（HomeWakeReq 带 account 名，目标机本机查代理）；D3 采纳（proxycfg 收口 Mask/WithProxyEnv）；协调者拉起回合（keystone）也套账号代理，并入段 4。

## implement 段 4（2026-10-22，本会话 implement subagent，主检出 `cards/B453-charter-1`）

- 输入：协调者段 3 裁决（D1 采纳、spec r1.2 `4729e6e7`；D2、D3 采纳；其余认可）；段 4 范围 = plan §4 段 4 去掉分类签名表（等 R6a 真样本），另并入「keystone 协调者回合套账号代理」（spec S4 任务回合含协调者回合）。
- 基线：段 3 末提交 `0a759338`（之后协调者提交了 spec r1.2）。
- Q14（hostapi，`run --format json` 路径：检测回合与协调者回合）：新测 `internal/hostapi/b453_turn_error_test.go`（假 opencode 回放 `{"type":"error",...,"error":{"name":"APIError","data":{"message":"Forbidden: ENTITLEMENT_ERROR …","statusCode":403,"responseBody":"…"}}}`；形状按上游 MessageV2 错误联合推出，**非真样本**，R6a 到后换）。首红为编译红（`undefined: OpencodeErrorText`）；补桩后断言红：`TestRunTurnNonzeroExitCarriesErrorEventText` 「回合错误缺 "ENTITLEMENT_ERROR model not in plan"：hostapi: 回合失败（exit status 1）stderr 尾部: Error: Forbidden」、`TestRunTurnZeroExitErrorEventOnlyIsFailure` 「只有 error 事件的回合不得判成功：{SessionID:ses_err Output:}」、`TestOpencodeErrorText` 四例缺字。
- Q14 hostapi 实现：新文件 `internal/hostapi/ocerror.go`（`OpencodeErrorText`：`<name>（HTTP n）: <message>；响应体: <≤400 字>`，整条 ≤800 字）；`driver.go` `ocEvent.Error`、`consumeEvents` 另返 errEvents、非零退出失败文本追加「；事件流错误: …」、**零退出 + 只有 error 事件 + 无文本 → 改判失败**（行为变化：今天当成功空输出；有文本时照旧成功只打 Warn）。`go test ./internal/hostapi/` ok。
- Q14（opencode 执行器 serve 路径）：新测 `internal/executor/opencode/b453_session_error_test.go`（session.error / assistant info.error + 零文本 idle；形状按上游推出，非真样本）。断言红（既有接缝，无编译红）：「FailReason 缺 "ENTITLEMENT_ERROR model not in plan"："回合结束但零文本产出（可能是供应商流中断）；executor 仍在线，可 continue 续接重试"」、「消息级错误应恰出现一次，实际 0」、「首回合应带错误」。
- Q14 适配实现：`runState.turnErrors`（turnMu 保护、同文去重、`clearTurn` 清空）；`session.error` 与 assistant `message.updated` 的 `info.error` 经 `hostapi.OpencodeErrorText` 摘要登记；**只改普通零文本分支**的 FailReason（追加「；会话错误：…」），被拒/在飞兜底两条分支不动。`go test ./internal/executor/opencode/` ok（28.7s）、`./internal/hostapi/` ok。
- F17 落库与 wire：新测 `internal/store/b453_rerun_test.go`、`internal/proto/b453_rerun_wire_test.go`。首红编译红（`orig.RerunBy undefined`）；加字段与桩 `SetTaskRerunBy` 后断言红「rerun_of 往返失败: of=<nil> by=<nil>」（proto wire 测在字段落地即绿——纯结构标签，无独立断言红，如实记）。实现：`proto.TaskRef{task,machine}`（machine 不 omitempty，形状照 F17 `{"task","machine"}`）、`Task.RerunOf/RerunBy`（omitempty 指针）；store 四列 `rerun_of_task/_machine`、`rerun_by_task/_machine`（DDL + ALTER + INSERT 只写 rerun_of + taskColumns + scanTaskRow，空任务 ID 读回 nil）；`Store.SetTaskRerunBy`（不存在 → ErrNotFound）。`go test ./internal/proto/ ./internal/store/` ok。

## 真机 R3–R5、R6a（验收执行者）

环境：三隔离实例换 `0a759338`（`version.revision` 三机均 `0a759338…`，modified=true 仅因工作树台账改动）后以 `HANDOFF_LOG_LEVEL=info` 重启（缺省 warn 级会吞掉全部验收用 Info 日志）。测试账本 handoff_b453；在役 7777 未碰。操作入口：t453-mac 7796 的 API/CLI；任务派发在 linux-01 上用隔离二进制作 CLI（`--config /root/b453-iso/config.yaml --agentd http://127.0.0.1:7795`），在 `/root/b453-iso/proj`（新建草稿仓，origin `https://example.invalid/b453/proj.git`，`project add` 仅本机）里 `--no-sync-check --new-worktree` 派发。
- 踩坑记录：linux 侧 `ssh … 'P=$(pgrep -f "[b]453-iso/config.yaml"); kill $P; …; nohup ./handoff agentd --config /root/b453-iso/config.yaml …'` 仍自杀——括号模式只防 pgrep 那一段，**同一条 ssh 命令里后面的启动段字面含 `b453-iso/config.yaml`**，照样被匹配。停与起必须分成两次 ssh。

**R3 低额度——通过（颜色部分无法验证）**
- `PUT /api/accounts/cx-linux?expect=3 {warn_percent:5, low_percent:10}`（已用 5h 19% / 周 18%）→ 回 `version 4, state "low"`；`account list`：`cx-linux   codex        t453-linux  低额度   5h 19%  周 18%  1h55m后`。
- 只调预警线：`{warn_percent:15, low_percent:80}` → `state "ok"`，`account list` 仍「正常」。warn 不影响状态：通过。warn 影响颜色：CLI 在 tty 下也无着色（`script -q /dev/null … | cat -v` 无 ANSI 序列），代码里 warn_percent 只有校验与存储、无消费方（颜色属段 5 前端）——本轮无法验证。
- 复原 `60/80`（v6，state ok）。

**R4a 载体绑账号 + 检测——通过**
- `PUT /api/squads/carriers/t453-codex?expect=0 {machine:t453-linux, cli:codex, home_dir:/root, credential:standalone, account:cx-linux}` → v1；`POST …/detect` → `{"status":"online","version":2}`。
- `GET /api/squads` 载体行：`…"status":"online","version":3,"account":"cx-linux"}`，**无 `account_mismatch` 键**；日志 mac：`检测载体开始 … account=cx-linux account_kind=codex`、`载体检测完成 … outcome=ready … fingerprint_set=true`；registry body `cred_fingerprint: "sha256:281e4cdbb6bc"`（= cx-linux 读数指纹）。
- 事实：codex 不在 `supportedCLIs`（只有 opencode），检测只走「命令可执行」+ 离线指纹，linux 日志 `未实装 CLI 检测：命令可执行，跳过检测回合`——codex 载体的检测**不跑回合**，故「检测回合使用账号代理」只能在 opencode 载体上验（见 R4c）。

**R4b 指纹不符——通过（用合成身份 HOME 造出不一致）**
- 哨兵账号 `cx-sentinel`（codex，HOME `/root/.handoff/home/codex`）探测 → `fail credential_expired（2026-09-08T09:12:25Z 到期）`，无读数指纹。变体 1：t453-codex(HOME /root) 改绑 cx-sentinel → detect online，无 `account_mismatch`（账号侧无指纹，按 spec 不比对，符合预期）。变体 2：载体 HOME 改 `/root/.handoff/home/codex`、绑 cx-linux → body `cred_fingerprint` 仍 `sha256:281e4cdbb6bc`（两处 auth.json 是同一 account_id），无红字。
- 变体 3（造不一致）：linux-01 新建 `/root/b453-iso/fakehome/.codex/auth.json`（合成 `{"tokens":{"access_token":"fake-not-a-token","account_id":"acct-b453-fake-sentinel"}}`，无真凭据），载体 HOME 指它、绑 cx-linux → `PUT` 200（可保存）、detect `online`（检测不拦）；`GET /api/squads`：`"account":"cx-linux","account_mismatch":true`；`squad list` 账号列：`cx-linux（凭据指纹不符）`。「派发不拦」未用真派发验（fake token 派 codex 必败），代码上准入 `usability` 不读 mismatch。
- 复原：t453-codex HOME `/root`、绑 cx-linux、重检 online（v11）；`DELETE /api/accounts/cx-sentinel?expect=1` → 200。fakehome 留在 linux-01（无凭据，可复用）。

**R4c 账号代理进任务与检测回合——通过（含 Q6/U6 行为事实）**
- linux-01 `127.0.0.1:1080` = trojan-go，http 与 socks5h 都通（`curl -x` 两种均 200）。
- 第一次填 `http://127.0.0.1:1080` 派 codex 任务（`--receiver t453-codex`，prompt `env | grep -i proxy | sort`）完成、六键 = 该值——但隔离 agentd 进程环境本身就是这六键 = `http://127.0.0.1:1080`，**分不出覆盖与继承**，作废。改填可区分的 `http://b453u:b453p@127.0.0.1:1080`（假口令，trojan 不校验，`curl -x` 实测可用）：PUT 回显 `http://b453u:***@127.0.0.1:1080`。
- 检测回合：新登记 opencode 载体 `t453-oc`（HOME /root，model `deepseek/deepseek-flash`，绑 cx-linux——opencode 载体接受 codex kind 账号）→ detect online（4.3s，真跑回合）；linux 日志 `检测回合使用账号代理 account=cx-linux proxy=http://b453u:***@127.0.0.1:1080`（agentd 层）与 `mod=hostapi cli=opencode 检测回合使用账号代理`，随后 `检测回合成功 output_bytes=4`。
- opencode 任务（`--receiver t453-oc`，exit 5 是 bash 权限工单，`reply --approve` 后 `wait --final` exit 0）：输出 `ALL_PROXY/HTTPS_PROXY/HTTP_PROXY/all_proxy/http_proxy/https_proxy = http://b453u:b453p@127.0.0.1:1080`，`NO_PROXY/no_proxy` = 进程原有列表（`localhost,127.0.0.1,::1,10.0.0.0/8,…`，未覆盖）；日志 `账号代理已覆盖 executor 环境 task=c486fe91… where=首回合 account=cx-linux account_kind=codex proxy=http://b453u:***@127.0.0.1:1080`。opencode serve 经 localhost 通信正常、任务完成。
- 补 NO_PROXY 分支：以 `env -u NO_PROXY -u no_proxy` 重启 linux 隔离实例（`/proc/<pid>/environ` 中 no_proxy 0 条）再派同任务 → 六键同上，`no_proxy=localhost,127.0.0.1,::1`、`NO_PROXY=localhost,127.0.0.1,::1`，任务 `wait --final` exit 0——**U6 行为事实成立**：账号代理生效 + 补 localhost 后 opencode serve 可达。
- 口令泄漏：linux 与 mac 两份 agentd.log `grep -c b453p` = 0。
- 复原：`PUT … proxy:""` → 回显无 proxy 键（v9）；linux 实例按原环境（带 NO_PROXY）+ info 级重启。

**R4d——通过**（并入 R4a/R4b：`squad list` 账号列与 `GET /api/squads` 载体行 `account` 键均在）。

**R4e 存量 quota 读出上线——通过**
- 载体 PUT 无 status 入参，改 psql：`update registry set body = body || '{"status":"quota","last_error":"r4e-marker 存量限额中"}' where kind='carrier' and id='t453-codex'` → DB `quota|r4e-marker 存量限额中`；`GET /api/squads`：`t453-codex online r4e-marker 存量限额中`（marker 读出证明读的是库里这条，不是缓存）；linux 端 7795 同为 online；`squad list` 无「限额中」。
- 复原：再 detect → v12，body `status online`、last_error 清空。

**R5 派发跳过与点名——通过（文案瑕疵 1 条）**
- 小队 `t453-pair`（executor，成员 `t453-agy`〔agy，HOME /root，绑 agy-linux，detect online〕在前、`t453-oc`〔绑 cx-linux〕在后）。agy-linux 调 `5/10`（Gemini 周 14% > 10、5h 9% < 10）→ `state low`。
- 连续 3 次 `dispatch --receiver t453-pair --prompt "Reply with the single word OK…"`：任务 b9e7ea22 / b995719d / 9e8bbdb4 的 `carrier` 全为 `t453-oc`；linux 日志 3 条 `准入因账号跳过载体 carrier=t453-agy account=agy-linux state=low recover_at=2026-10-24T13:01:04Z`（= 越线的 Gemini 周窗口重置，未越线的 5h 窗口不参与，符合 D1）。
- cx-linux 也调 `5/10` → 小队派发 400：`scheduling: 小队内没有已上线且有空的载体: 小队 t453-pair 没有可用载体——t453-agy：账号 agy-linux 低额度，预计 2d19h 后恢复；t453-oc：账号 cx-linux 低额度，预计 5d13h 后恢复`（cx-linux 两窗口都越线，取最晚的周窗口 10-27，符合 D1）。
- 点名 `--receiver t453-agy`（linux 本机落点）→ 400：`scheduling: 小队内没有已上线且有空的载体: t453-agy：账号 agy-linux 低额度，预计 2d19h 后恢复`；经 mac 网关点名（`--project proj`，他机落点）→ 同文，mac 日志 `准入因账号跳过载体 … dispatch 身份准入失败 … error_kind=admit`。
- **瑕疵（文案）**：外层哨兵前缀「小队内没有已上线且有空的载体:」在点名载体时不对（没有小队），在小队时与内层「小队 t453-pair 没有可用载体——」重复。复现：上面两条命令。不影响判据。
- 旁见（非本卡）：prompt「Reply with the single word OK」的回合结果被归成 `kind: question` 工单（exit 5），三个任务卡在 waiting_answer，已 `stop`。
- 复原：cx-linux、agy-linux 均回 `60/80`（ok）。

**R6a 真样本——部分取得；ENTITLEMENT 403 未复现**
- `HOME=/root timeout 120 opencode run --format json -m opencode-go/deepseek-v4.1-flash -- ping` → **exit 0 成功**（回 "I'm here…"，cost 0.000999）；`/root` 的 auth.json 只有 deepseek 条目、`opencode auth list` 只 1 条，但 opencode-go 仍可用（来源未查明）。加试 `opencode-go/claude-haiku-5-5` 同样成功（"pong"，cost 0.0015）。不再烧额度。`/root/.config/opencode/opencode.jsonc` 默认 model 仍写 `cline-pass/cline-pass/deepseek-v4.1-flash`，但该 provider 已无定义，历史 403 来源不可再现。→ **ENTITLEMENT 无真样本**，段 4 该签名只能合成并注明。
- `HOME=/root/.handoff/home/muse … ping` → exit 1，stderr 空，stdout 一行 `{"type":"error",…,"error":{"name":"APIError","data":{"message":"This API key has expired. …","statusCode":401,"isRetryable":false,"responseHeaders":{…},"responseBody":"{\"error\":{\"type\":\"authentication_error\",\"code\":\"api_key_expired\",…}}","metadata":{"url":"https://opencode.ai/inference/go/openai/v1/chat/completions"}}}}`。
- 同 HOME 在检测链上：临时载体 `t453-muse`（opencode，HOME muse，绑 og-muse）detect → `pending`，`last_error: "hostapi: 回合失败（exit status 1）stderr 尾部: "`——**错误全在 stdout JSON，stderr 尾部为空**，`0a759338` 的检测回合看不到错因（即 Q14 hostapi 半边，段 4 已在修）。载体已删。
- codex/grok/agy 额度错误样本：无低成本触发途径，未取。
- 文件（`$S/samples/`，均已 grep `sk-…`/Bearer/长 token 0 命中，无需脱敏）：`opencode_key_expired.stdout.jsonl`（960B）/`.stderr`（0B）；`opencode_go_root_ok_not403.stdout.jsonl`（970B，step_start/text/step_finish 三行成功形状）/`.stderr`（0B）；`opencode_go_haiku_root_ok_not403.stdout.jsonl`（933B）/`.stderr`（0B）。命名没用 `opencode_entitlement_403.*`，因为内容不是 403。

**收尾状态**：三实例运行于 `0a759338`、info 级；账号 4 个（agy-linux/cx-linux 60/80 无代理 ok，gk-mbp ok，og-muse 探测失败）；新增并保留载体 `t453-codex`（cx-linux，HOME /root）、`t453-oc`（opencode deepseek/deepseek-flash，cx-linux）、`t453-agy`（agy-linux）与小队 `t453-pair`（t453-agy,t453-oc），供 R6b 复用；linux 测试任务全部终态（3 completed + 3 failed/stopped），运行位清零。

## implement 段 4（续，接在验收执行者 R3–R6a 节之后）

- R6a 真样本入库：`$S/samples/opencode_{key_expired,go_root_ok_not403,go_haiku_root_ok_not403}.stdout.jsonl` → `internal/hostapi/testdata/b453/*.real.stdout.jsonl`；`grep -iE "sk-|key\"|token|bearer|authorization"` 只命中 step_finish 的 `"tokens"` 计数，无密钥。
- 真样本回放测试 `TestRunTurnRealKeyExpiredSampleCarriesTextAndIsNotQuota`、`TestRunTurnRealOkSamplesSucceed`：在已提交的 Q14 实现上直接绿；为证其能红，把 `driver.go` 临时换回 `3ce59d4b^` 版本跑：「回合错误缺 "This API key has expired"：hostapi: 回合失败（exit status 1）stderr 尾部: 」——与 R4 真机读数（last_error 只有「stderr 尾部: 」）同一症状；换回后绿，`git diff driver.go` 为空。检测分类：key 过期 → `needlogin`（含 authentication_error），不是 quota。
- 适配真对象回放 `TestZeroTextTurnFailReasonCarriesRealErrorObject`（真样本 error 对象装进 session.error）：在实现上直接绿（红已由合成用例在实现前证过，如实记）。
- quota：签名表与测试头注明「全部合成、未经真机」（ENTITLEMENT 两条行尾注）；新反例 `TestClassifyRealKeyExpiredIsNotQuota`（kind opencode-go 与空各一）绿——表里本无命中它的签名，此测是防将来放宽签名时误判。
- 文案缺陷（协调者 R5）：新测 `TestB453UnusableMessagesHaveNoSentinelPrefix` 红「点名载体文案应以「载体 A 不可用：账号 acc-a 低额度，预计 」开头，得 scheduling: 小队内没有已上线且有空的载体: A：账号 acc-a 低额度，预计 3h0m 后恢复」。修法：`admission.go` 新 `unusableError`（`Is(ErrNoHealthy)` 成立、`Error()` 不带哨兵前缀），`requireUsable` →「载体 X 不可用：<原因>」，`noUsableMember` →「小队 X 没有可用载体——A：…；B：…」/「小队 X 没有在线载体」；`Unusable.reason()` 拆出不带载体名的原因。随改段 3 自己的两条断言（`TestCheckUsableNamesStatusAndAccount`、agentd `TestRouteNamedRemoteCarrierUsesUnifiedCriterion` 的「c1：账号 cx 无额度」→「载体 c1 不可用：账号 cx 无额度」）。`humanDuration` 导出为 `HumanDuration`（notice 文案复用）。`go test ./internal/scheduling/...` ok。
- 编排 S6/S7：新测 `internal/orchestration/b453_quota_test.go`（5 支）。写测后先补桩（类型 + 空 `SetQuotaHandler`）才首跑，未单独观察编译红；断言红：「等待超时: 任务状态 failed」×2（只读重跑/无人可换）、「应恰有一张 notice 待答工单，得 []」；限速与未注入回调两支在桩上即绿（行为与今天一致，是锁现状的反例）。
- 编排实现：新文件 `quota.go`（`QuotaFailure/QuotaHit/RerunOutcome/QuotaHandler`、`SetQuotaHandler`、`classifyQuota`、`beginReadonlyQuota`、`rerunAfterQuota`、`openQuotaNotice`、`quotaNoticeText`）；`manager.go`：`quotaHandler atomic.Value`、`quotaInFlight sync.Map`（处置期间到达的结果丢弃）、`handleResult` 失败先分类（只读+额度+有回调 → 另起 goroutine 接管、不迁 waiting_review、不出 turn_failed；分支+额度 → 作废旧工单后、turn_failed 前开 notice）、`Stop` 抽出 `stopWithReason(ctx,id,reason,trigger)`（撞额度用 trigger=quota）、`RelayAnswer` 加 `notice` 分支只 `markDelivered`。`proto.go`：`TicketKindNotice`、`NoticeRequest`。`go test ./internal/orchestration/` ok（38.8s）；`./internal/agentd/` 首跑 1 红即上面那条随改断言，改后 ok。
- 门面 S6（V12）：新测 `internal/agentd/b453_rerun_test.go`（b452 双 agentd 夹具 + 账号域；5 支：留存输入、MarkTaskQuota、链排除、无账号按载体名排除、编排→门面→派发端到端）。首红编译红（`*Server does not implement orchestration.QuotaHandler (missing method MarkTaskQuota)`、`undefined: rerunInputFile`）；桩后断言红：「读重跑输入: … rerun-input.json: no such file or directory」「告知素材不全: {Account: Windows:[] RecoverAt:<nil>}」「重跑应成功: 未实现」「应换到 rq-n2，得 … 未实现」「等待超时（10s）：原任务 failed 且带 rerun_by」。
- 门面实现：新文件 `internal/agentd/quota_rerun.go`（`MarkTaskQuota`、`saveRerunInput`、`RerunReadonly`、进程内 `dispatchInProcess`〔最小 ResponseWriter，不用 httptest〕、`rerunExclusion`/`andAccept`/`rerunNoMemberError`〔`rerunExhaustedError` Is ErrNoHealthy〕）；`handlers.go` `dispatchRequest.RerunOf/ExcludeAccounts`（F14）、只读派发成功后留存输入、`RerunOf` 进 `DispatchReq`；`dispatchroute.go` 小队选人谓词 = 亲和 ∧ 链排除，链排除生效时 ErrNoHealthy 改写为点名排除集（亲和改写只在无链排除时）；`account_api.go` `AccountsClient` 加 `MarkQuota`；`accounts.QuotaHitWindows`/`WindowLabel`（+`TestQuotaHitWindows`，实现先于测试写、直接绿，如实记）；`orchestration` `DispatchReq.RerunOf` → `Task.RerunOf`；`cmd/agentd.go` `mgr.SetQuotaHandler(srv)`。`go test ./internal/agentd/` ok（91s）、`./internal/orchestration/` ok（42s）。
- 客户端 S6/Q8（V13、V14 客户端条）：新测 `internal/client/b453_waitfinal_test.go`（多任务桩 `b453Multi`；5 支）。首红编译红（`out.FollowedFrom undefined`）；加字段后断言红：跟链两支「应跟到 t2…」「应跟两跳到 t3…」（结论停在 t1）、notice 两支「waiting_review + notice 应先交回工单」「流里 turn_failed 后应重取快照交回 notice」（得回合结论）；无链接反例在桩上即绿（锁现状）。
- **Q8 窄改范围的自主决定**：只对 notice 生效——waiting_review + 快照有 **notice** 待答工单 → 先交回（5）；gate/ask 在 waiting_review 下维持「回合已结束优先」。依据 plan V14「gate/ask 工单在 waiting_review 下的旧行为不变（加一条构造用例锁 Q8 的窄改范围）」——既有 `TestB460WaitFinalTurnEndedBeatsPending`（gate + waiting_review → 回合结论）正是那条构造用例，**原样保留、未改写**；F-B460-2 字面「pending_tickets 非空」比实现宽，差异只落在今天不可达的组合上（Q8 事实：回合失败路径先作废挂起工单），报协调者裁。
- 客户端实现：`WaitFinal` 外包跟链循环（`waitFinalOne` 为原单任务体）；`rerunNext` 只在 failed 且带 rerun_by 时跟，事件流路径重取一次快照补读链接；成环保护；`FinalOutcome.FollowedFrom/Machine`；单任务体内快照路径与流内 turn_failed 后各一次 notice 判定。`go test ./internal/client/` 只剩已知假红 `TestProductionHTTPClientCallersAreGatewayOnly`（嵌套 `.claude/worktrees`）。
- CLI（F-B460-1）：新测 `cmd/b453_notice_test.go`（3 支，入口 `-run 'PendingTicket.*Notice'` + 跟链输出）。断言红：notice 块被打成 `kind: permission` + `--approve/--deny`（即 plan §7 跨版本风险的现形）、「产物应按链尾 t2@linux-01 取，得 t1@mac-mini」。实现：`writePendingTickets` notice 分支（全文取 `notice`、不打 reason 行、答复行恰一条 `--answer "已知悉"`）；`runFinalWait` 跟链后 stderr 提示「任务 t1@… 撞额度已换人重跑，结论取自新任务 t2@…」，之后的产物/续等/错误以链尾为准；`reply` 无 kind 校验（`--answer` 对 notice 直接可用），只改帮助文案。`go test ./cmd/` ok（79s）。
- 白名单 roomsapi：新测 `internal/agentd/b453_inbox_notice_test.go` 红「waiting_review 的 notice 应上浮且标题为告知，得 <nil>（全部 []）」；实现：收件箱 ticket 源对 waiting_review 任务只收 notice，`ticketTitle` notice →「告知工单待确认」。推送（pushfanout）按事件分类、notice 不发事件 → 不推送（开放项）。
- 续接作废告知：新测 `TestB453ContinueVoidsPendingNotice` 红（续接后 notice 仍挂着——续接回合中 `--wait` 会被陈旧告知以 5 打回）；实现：`Continue` 迁 running 后 `VoidTicketsWithAudit(…, "continue：已续接，未确认的告知工单作废")`。
- 其余白名单逐处核对（grep 结果）：`watchdog.RecoverOnStartup` 不动 waiting_review 的工单（notice 跨重启保留）；`ticketvoid`/`client/backlog`/`cmd/show`/`handlers.handleAttach` 无 kind 分支；`client.WaitDeliveryPolicy` 未改——本卡没有新事件类型（重跑/告知都用 progress 文本 + failed/turn_failed 原因，自主决定，免去事件白名单与前端时间线渲染改动）；账本镜像开放工单投影在仓内无 kind 分支（grep `PendingTickets` 无 ledger 命中）。
- web：`review.ts`（`NOTICE_ACK='已知悉'`、`buildTicketAnswer` notice → 已知悉、`parseTicketRequest` 读 `notice`、`ticketKindLabel` → 告知、`TicketAction` 加 acknowledge）、`TicketsPanel.tsx`（notice 只一个「已知悉」按钮）。vitest 首跑有一次我自己写错 import 的假红（`ticketKindLabel is not a function`），改正后真断言红 4 条（textarea 未隐藏、notice 全文退回 JSON、编码得 ''、展示名得 'notice'）；实现后 `src/app/task/` 174 passed，typecheck OK。
- 契约：Go fixture 新 `TaskRerunLinks`、`TicketNotice`（红「读取 fixture … 失败（契约尚未生成？）」→ `-update`）；TS `Task.rerun_of/rerun_by`、`TaskRef`、`NoticeRequest` + `contract.test.ts` 两支（fixture 更新后首跑即绿，如实记）；62 passed。`go build ./... && go vet ./...` OK。
- keystone 账号代理（段 3 裁决并入）：新测 `internal/agentd/b453_coord_proxy_test.go`（runner 记录型 Coordinator：Launch/Resume 六键 = 账号代理、Resume 席位对保留、无代理不加键；门面按（CLI, 展开 HOME）解析）。首红编译红（`unknown field accountProxy`、`coordinatorAccountProxy undefined`）；桩后断言红「Launch 回合 HTTPS_PROXY 应为账号代理，得 ""（env=0 项）」「应解析到载体账号代理，得 ""」。
- 实现：runner 是 keystone 全部协调者回合（卡拉起、卡续轮、驻场续轮）的唯一咽喉，在此套代理：`coordinatorRunner.accountProxy` + `withAccountProxy`（`proxycfg.WithProxyEnv` 作用于追加 env；hostapi.buildEnv 让追加键覆盖进程环境，NO_PROXY 不覆盖/缺席补回环——与任务回合同一函数）；Launch 在 prepareHome 后、Resume 在席位对之后套。门面 `coordinatorAccountProxy`：runner 只有 SessionSpec，按物理身份（CLI 基名 + `ExpandHomePath` 后的 HOME + 本机）找载体；同身份多载体账号不一致 → 不套并 Warn；账号不在本机 → 不套（复用 `hostWakeProxy`）。`NewCoordinatorRunner` 加第 5 参（3 处调用同改，测试夹具传 `srv.CoordinatorAccountProxy` / nil）。日志「协调者回合使用账号代理 … proxy=<掩码>」。`go test ./internal/agentd/` ok（90s）、`./cmd/` ok（78s）；`go build ./... && go vet ./...` OK。

## implement 段 5（2026-10-22，本会话 implement subagent，主检出 `cards/B453-charter-1`，基线 `d94b42d9`）

- 段 4 裁决：偏离 1–8 认可；开放项 1 签名表保持合成、真机按 Q16 注入；2–5 收尾落 roadmap；6 留段 6。
- 读入：原型 `prototypes/b453-accounts/`（README、shared/accounts.js、shared/styles.css B453 段、pages/accounts.html、pages/settings.html B453 段、pages/mobile-settings.html B453 段）；spec S1/S4/S8–S10；plan 段 5、§8 F1–F13、§6 决策权限。
- 现状读数：账号 API wire 在 `internal/proto/scheduling.go:205-292`（AccountsResp/View/ReadingView/WindowView/ProblemView/ExtraUsageView/DeleteRefusal/Input），契约 fixture 尚无账号用例；`web/src/api/scheduling.ts` CarrierStatus 仍含 quota（段 3 留段 5）；SchedulingPage 载体 PUT 不带 `account`——PUT 为整体替换（`scheduling.go:236 PutCarrier` 不沿用旧 account），今天从控制台编辑一个已绑账号的载体会把账号清掉，段 5 载体对话框必须回传 `account`。
- 自名缺口：控制台无「本机全队名」读面（`Machine.name=""` 表本机）；账号所在机是全队名。载体对话框「同机账号」判据取：名字相等，或载体在本机别名（空/本机/local）且账号所在机不是任何已登记远端机名。

## 真机 R6b、R7（验收执行者）

环境：当前 HEAD `d94b42d9` 重建 `$S/handoff-linux`/`handoff-mac`，三实例停、起分两次 ssh，`HANDOFF_LOG_LEVEL=info`；`/api/status` 三机 `revision d94b42d9`（modified=true，工作树仅 b434 台账脏）。派发仍在 linux-01 用隔离 CLI、草稿仓 `/root/b453-iso/proj`。

**注入法（未用假 CLI）**：读 `executor/opencode/proc.go`——任务回合走 `opencode serve` + HTTP API，二进制由 agentd 进程的 `exec.LookPath("opencode")` 解析（不随载体变），假 CLI 只能换整个实例的 PATH，且要伪造 serve 协议。改用**真 opencode + 假供应商端点**：linux-01 起 `fakeprov.py`（127.0.0.1:18453，任何请求回 403 `{"error":{"type":"EntitlementError","code":"ENTITLEMENT_ERROR","message":"B453 injected: monthly usage limit reached for this plan (ENTITLEMENT_ERROR)"}}`），注入载体 HOME `/root/b453-iso/inj/home*` 下 `.config/opencode/opencode.json` 覆盖 `provider.opencode-go.options.baseURL/apiKey`（假 key）。手跑 `HOME=… opencode run --format json -m opencode-go/deepseek-v4.1-flash -- ping` → exit 1，stdout `{"type":"error",…,"error":{"name":"APIError","data":{"message":"B453 injected: …","statusCode":403,"isRetryable":false,…,"metadata":{"url":"http://127.0.0.1:18453/v1/chat/completions"}}}}`——与 R6a 真 401 样本同形。执行器路径、检测路径都是真代码。
- 注入载体先**不绑账号** detect（否则检测回合撞额度会先把账号标无额度、准入就跳过它）：`t453-inj` → `online`，`last_error: "hostapi: 回合失败（exit status 1）stderr 尾部: ；事件流错误: APIError（HTTP 403）: B453 injected: …；响应体: {…}"`——Q14 检测半边已修好（对比 R6a 时只有「stderr 尾部: 」）。再绑 opencode-go 账号 `og-inject`（HOME 无 auth.json，探测失败/未探测，准入照常放行）。
- 只读形态只能经模板：`template put r6b-ro {"receiver":"t453-pair","mode":"readonly",…}`（测试账本，模板不可删，留存）。

**R6b 只读撞额度换人重跑——通过**
- 小队 `t453-pair` 临时改为 `[t453-inj, t453-oc]`。`dispatch --template r6b-ro --no-sync-check --prompt "README.md 第一行…" --wait` → **exit 0**；stdout = 新任务结论（`` `# b453 scratch` ``）；stderr `提示: 任务 ecd4b4f9…@t453-linux 撞额度已换人重跑，结论取自新任务 17972650…@t453-linux`。
- 快照：原任务 `state failed, carrier t453-inj, rerun_by {task:17972650…, machine:t453-linux}`；新任务 `completed, carrier t453-oc, rerun_of {task:ecd4b4f9…, machine:t453-linux}`。原任务时间线 progress `因额度耗尽改由 t453-oc 重跑（新任务 17972650…@t453-linux）`，failed `fail_reason: 额度耗尽（账号 og-inject 已标无额度）：…会话错误：APIError（HTTP 403）: B453 injected…；已改由 t453-oc 重跑，新任务 …`。
- 日志链：`只读任务已留存重跑输入` → `回合失败判为额度耗尽 … signature=entitlementerror` → `写入撞额度标记 … category=quota_exhausted` → `只读任务撞额度重跑已派出 … chain_accounts=1` → `回写重跑链接` → `stop 进入 trigger=quota` → `running→failed reason=quota`。`account list`：`og-inject … 无额度`。
- `wait ecd4b4f9 --final` → exit 0，同样打「撞额度已换人重跑」提示并输出新任务结论（跟链）。
- 产物取自新任务：第二个账号 `og-inject2`/载体 `t453-inj2`，小队 `[t453-inj2, t453-oc]`，prompt 让执行者写 `$HANDOFF_ARTIFACTS_DIR/r6b.txt` → exit 0，`artifacts: /root/b453-iso/data/tasks/676dbd8b…/artifacts`（新任务目录，文件内容 `r6b-artifact`），原任务 43411a9b 产物目录为空。
- 两成员都撞：`og-inject3`/`og-inject4` 分别绑 `t453-inj`/`t453-inj2`，小队 `[t453-inj, t453-inj2]` → **exit 3**；stderr 先「撞额度已换人重跑…新任务 96063dac」，后 `Error: 任务 96063dac… 未完成（failed）：额度耗尽（账号 og-inject4 已标无额度）：…；没有可换的成员重跑：小队 t453-pair 没有可换的成员——重跑链已排除：t453-inj（账号 og-inject3）、t453-inj2（账号 og-inject4）（已用：og-inject3、og-inject4；每个账号在一次重跑链里只用一次）`。注：plan R6(c) 写「退出 3 + S5 文案」，实际是重跑链排除文案（不是 S5 的「账号 X 无额度，恢复时刻未知」），请协调者裁是否可接受。

**R7 分支任务撞额度——通过**
- 新账号 `og-inject5` 绑 `t453-inj`；`dispatch --receiver t453-inj --new-worktree --prompt "在 README.md 末尾追加一行 r7，并提交。" --wait` → **exit 5**，stdout notice 块：`kind: notice`，全文「分支任务撞额度，已停下：不自动换人，工作树保留。/ 载体：t453-inj@t453-linux / 账号：og-inject5（已标无额度，持续到下一次成功探测）/ 撞到的窗口：未知（账号最近读数未越线或无读数）/ 预计恢复：未知 / 分支：handoff/5c1d4641 / 工作树：/root/b453-iso/data/worktrees/5c1d4641 / 失败原文：… / 接管见 B459；本工单答「已知悉」即关单，不会在原会话续跑。」，答复行恰一条 `--answer "已知悉"`，无 permission/approve 行。
- 任务 `waiting_review`；`reply … --answer "已知悉"` → `{"ok":true}`；`wait --final` → **exit 3**，`未完成（turn_failed）：额度耗尽（账号 og-inject5 已标无额度，见告知工单 e304a714…）：…`；工作树目录与分支 `handoff/5c1d4641` 仍在；`og-inject5 无额度`。

**瑕疵（文案，不影响判据）**
1. 撞额度的失败原因都带着通用零文本分支的句子「回合结束但零文本产出（可能是供应商流中断）；executor 仍在线，可 continue 续接重试」——只读原任务已被 stop（failed），「可 continue 续接」不成立；分支任务按 notice 说「不会在原会话续跑」，同一条原因里又叫人 continue，前后矛盾。复现：上面任一注入派发，看 `fail_reason`/`turn_failed reason`。
2. 旁见：删除载体后 `squad list` 仍残留 `运行位 carrier/t453-inj`、`squad/t453-pair/t453-inj` 等计数 0 的行（未查是否本卡引入）。
3. 旁见：R7 任务 `done` 归档后 state 显示 `completed`（撞额度停下的分支任务归档为「完成」，看列表会误读）。

**复原**：小队 `t453-pair` 回 `[t453-agy, t453-oc]`（v5）；删载体 `t453-inj`、`t453-inj2`，删账号 `og-inject`…`og-inject5`；杀 fakeprov、删 `/root/b453-iso/inj`；R7 任务已 `done`；linux 测试任务全部终态。账号回到 4 个（agy-linux、cx-linux、gk-mbp 正常，og-muse 探测失败）；模板 `r6b-ro` 不可删，留在测试账本。三实例保持运行于 `d94b42d9`、info 级。

## implement 段 5（续，接在验收执行者 R6b、R7 节之后）

- 契约（F1–F9 前端镜像）：先写 TS `contract.test.ts`「账号 wire」三支 → 红「Failed to resolve import "./testdata/AccountsResp.json"」；Go fixture 加 `AccountsResp`（四 kind 五档、可缺席键：agy 无 entry、none 无 reading/last_attempt_at/last_error/proxy、fail 无 reading、窗口无 resets_at）、`AccountInput`、`AccountDeleteRefusal` → `go test ./internal/proto/ -run TestContractFixtures` 红「读取 fixture …AccountsResp.json 失败（契约尚未生成？）」×3 → `-update`，`git status` 只多三份新 json（既有 fixture 零改动）→ proto ok。vitest 此时即绿（`import type` 运行期擦除），`tsc -b` 红「Cannot find module './accounts'」→ 新 `web/src/api/accounts.ts`（类型 + getAccounts/putAccount/deleteAccount/probeAccount）→ 65 passed、typecheck 过。
- 词表删 quota（plan §6 预许改写第 1 类）：`scheduling.fetch.test.ts` 原断言 `CARRIER_STATUS_LABEL.quota === '限额中'` 改为「键只剩 online/pending/unreachable、值不含限额中」→ 红「expected [ 'online', 'pending', 'quota', …(1) ] to deeply equal [ 'online', 'pending', 'unreachable' ]」→ `CarrierStatus` 删 quota、标签删一行（`WakeOutcome` 保留 quota，F16）→ 7 passed；typecheck 过；web 源码再无载体 quota 引用。
- 协调者来信（段 5 进行中）：R6b/R7 通过；额度失败原因仍拼通用零文本句（「回合结束但零文本产出…可 continue 续接重试」）是缺陷，段 5 交回前同轮先红后绿修；「两成员都撞」退出 3 文案用重跑链排除版，采纳。
- 展示规则 `web/src/app/accounts/model.ts`（核心方法：问题优先排序、窗口分档与色彩类、底栏两模式取窗、状态短语、窗口名、倒计时、同机判据）：`model.test.ts` 8 支，桩（恒空/恒 ok）上断言红 8 条（例「expected [ 'ok1', 'none', 'low', 'ok2', …(2) ] to deeply equal [ 'out', 'low', 'fail', 'none', …(2) ]」「expected 'ok' to be 'warn'」「expected [] to deeply equal [ 'session', 'weekly' ]」）→ 实现后 8 passed。
- Shell 级数据源 `store.ts`（Q17：模块级快照 + 订阅；`useAccountsSource` 唯一拉取者 = usePoll 30s；`useAccounts` 读、`probe/apply/remove` 就地合入）：`store.test.tsx` 3 支，桩上红 2 条（toHaveTextContent 等不到「a:none,b:ok」），「无数据源停 loading」桩上即绿（锁不自起轮询的反例）→ 实现后 3 passed，GET 只调一次、探测后快照就地变 `a:low`。
- 偏好 `webPrefs.usageMode`（可选键，不升 v）：新测先红「TypeError: usageModeOf is not a function」→ 桩恒 detail 再见断言红「expected 'detail' to be 'compact'」→ `isPrefs` 接受缺席/detail/compact、`usageModeOf` 缺席取详细 → 12 passed（既有 11 支不改）。判断：可选键而非升 v——升 v 会把老用户的会话打开方式/角标偏好整份丢回默认。
- 底栏与弹层 `StatusBar.tsx` + 共用片段 `QuotaBits.tsx` + `accounts.css`（原型 B453 段移植；`--q-warn`=`--state-intervention-text`，`--q-out`=`--state-failed`——移动壳把 `--destructive` 压成灰，无额度在移动端仍须红字）：`StatusBar.test.tsx` 5 支，null 桩上红 4 条（「还没有快照不渲染」桩上即绿，锁反例）；实现后首跑红 3 条均为测试侧问题——倒计时边界（weekly 98h 过几毫秒成「4d 1h」，样本改 98.5h）与选择器（`.q-out` 先命中细条，改 `.q-pct.q-out`），改后 5 passed。溢出「+N」依赖真实宽度，jsdom 量不出（实现里宽度为 0 时不收纳），交 R8 浏览器形态验收。
- 账号页 `AccountsPage.tsx`（S8 四组卡片 + S1 登记/编辑/删除对话框 + 立即探测）：`AccountsPage.test.tsx` 7 支（W1 缺省值随 kind 预填、agy 条目置灰「由 agy CLI 管理」、expect=0 请求体逐键；编辑名称只读带版本、打码代理原样提交、400 原文显示；删除被引用拦住不发请求、无引用确认删、服务端 409 体 carriers 也列出；?account= 预展开高亮；探测就地刷新），null 桩上 7 红 → 实现后 7 passed（首跑即绿）。所在机器用 input+datalist（候选 = 已登记远端机名 ∪ 已有账号所在机）：控制台无本机全队名读面，下拉会漏掉本机，自由输入兜底。
- 移动设置节 `MobileAccounts.tsx` + `SettingsHub` 首页挂一节（S10，W6）：`MobileAccounts.test.tsx` 3 支经 SettingsHub 真实入口，未接入时红 2 条（找不到 `settings-section-accounts`），「无快照不出节」即绿（反例）→ 实现后 3 passed。只读 + 立即探测，无编辑/删除入口。
- 载体对话框与载体行（S4，W2）：`SchedulingPage.test.tsx` 新增 3 支（不改既有 18 支）：红「Unable to find a label with the text of: 账号」×2、「Unable to find an element with the text: 凭据指纹与所选账号不一致」→ 实现：草稿带 `account`、PUT 非空才带键（**修了一个潜在丢写**：今天控制台编辑已绑账号的载体会因 PUT 整体替换把 account 清掉）；下拉只列同机账号（model.sameMachine），换机器时当前账号不在新机则清空；快照未到时保留当前值作选项（不静默清绑定）；指纹不一致红字（行 + 对话框，`q-out`）；载体行「账号」栏带图标/类型/状态短语，低额度/无额度加「暂停派发，预计 x 后恢复」→ 21 passed。顺带把对话框说明里「跨载体账户池的归属归 roadmap 的『限额探测』」改为指向账号（本卡即该 roadmap）。
- Shell 接线（dock「账号与额度」ChartPie 图标、`/accounts` 整页路由 + `fullPageRoute`、`?account=` 预展开、外壳底部整宽底栏 `!compact && ledgerEnabled`、`useAccountsSource(ledgerEnabled)`）：`Shell.test.tsx` 新增 describe「B453 账号与额度入口」3 支 + 模块层 `api/accounts` 打桩（缺省空库，底栏在全部既有用例里照常挂）→ 红「Unable to find an element by: [data-testid="usage-statusbar"]」「…sb-seg-codex-plus」「expected "vi.fn()" to be called at least once」→ 接线后 3 passed。
- 全量 web：`npx vitest run` → Test Files 150 passed、Tests 1995 passed；`npm run typecheck` 过；eslint 触及文件 0 error（16 条 warning 均为既有文件既有行）。浏览器形态未自验：本机只有原型静态服务，无挂账号的控制台实例（在役 agentd 不动），交 R8。
- 段 5 提交：`0b9283f4 feat(B453): 前端账号与额度……`（台账随附，含验收执行者「真机 R6b、R7」节原样收入）。
- 真机瑕疵 1 修复（协调者来信：判额度耗尽后的失败原因仍拼通用零文本句「回合结束但零文本产出…可 continue 续接重试」）。根因：opencode 适配把会话错误原文**拼在**通用处置句后面作为唯一的 FailReason，编排层撞额度路径只能拿这整串去写原因——「原因」与「处置建议」混在一个字段，建议的对错取决于编排层的裁决（只读已终止、分支不续接原会话），适配层不可能知道。修法：`executor.Result` 新增 `ErrorDetail`（载体报出的错误原文，无处置建议）；opencode 零文本失败填 `turnErrorsText()`；编排 `quotaSourceText` 撞额度分类与重写原因用 ErrorDetail（缺席回退 FailReason）；普通失败路径不变，通用句照旧进 turn_failed。
  - 红（编译）：`unknown field ErrorDetail in struct literal of type executor.Result`（orchestration）、`got.Result.ErrorDetail undefined`（opencode）→ 加字段空接缝后红（断言）：只读「failed 原因 不应拼通用零文本句 "零文本产出"：{"fail_reason":"额度耗尽（账号 acct-a 已标无额度）：回合结束但零文本产出（可能是供应商流中断）；executor 仍在线，可 continue 续接重试；会话错误：stream error: insufficient_quota…」、分支 turn_failed 同形、opencode「ErrorDetail 应是会话错误原文：""」；反例「非额度零文本保留通用句」在空接缝上即绿 → 实现后 `go test ./internal/orchestration/ -run B453` ok、opencode `-run 'ErrorDetail|ZeroText|SessionError'` ok。
  - 触及包：orchestration ok（39s）、executor/... 全 ok（opencode 28s）、quota、proto ok、agentd ok（90.8s）；`go build ./... && go vet ./...` 过；gofmt 无输出。
  - 偏离：`internal/executor/executor.go` 不在 plan §3 可改清单（只列 `executor/opencode/*` 仅 Q14）；加一个只读可选字段，其余四个执行器不填即今天行为。

## implement 段 6（2026-10-22，本会话 implement subagent，主检出 `cards/B453-charter-1`，基线 `2df747f8`）

- 段 5 裁决：偏离 1–9 认可（含 `Result.ErrorDetail`）；R8 浏览器对照原型在跑。
- 范围：S11 打磨、S12 文档（`skills/handoff/SKILL.md`、`README.md`、`README.zh-CN.md`、`CHANGELOG.md` [Unreleased]、`docs/roadmap.md`）；全量编译 + 触及包测试，全量测试留 review。
- `--help` 核对（本分支二进制 `go build -o $SCRATCH/handoff .`）：`account list [--json]`（「一行一个账号对象（与 GET /api/accounts 元素同形）」）、`account probe <名称>`（「探测失败退出码 1」）、`reply --answer`（「告知工单答「已知悉」即关单」）；表格列 `名称/类型/机器/状态/窗口已用/最近重置/上次探测/说明`（`cmd/account.go:116`）。
- 文案取证（写文档用原文）：准入拒绝「载体 X 不可用：账号 a 低额度，预计 … 后恢复」/「小队 X 没有可用载体——A：…；B：…」（`scheduling/admission.go`）；链尽「小队 X 没有可换的成员——重跑链已排除：…」；跟链提示「提示: 任务 <t1@m> 撞额度已换人重跑，结论取自新任务 <t2@m>」（`cmd/skilled.go:250`）；notice 全文模板 `orchestration/quota.go quotaNoticeText`；探测端点 opencode-go `opencode.ai/zen/go/v1/usage`、codex `chatgpt.com/backend-api/wham/usage`、grok `cli-chat-proxy.grok.com/v1/billing?format=credits`（失败回退 `/v1/billing`）、agy CLI `/usage`（版本门 ≥ 1.1.11）。
- roadmap 旁见核对：`squad list` 删载体后残留 count 0 运行位——运行位行来自 `RunningCounts()` 全量键（B390 `a65baefc` 观察面），删载体不清计数是 B448 R11 已记的既有缝；本卡对 `schedapi.go` 的改动（`d5a10771`）只加账号投影，未碰该段 → 非本卡引入，只记。
- S11 打磨核对：`cmd/account.go` 的窗口标签与 `humanDuration` 和编制域 `accounts.WindowLabel` / `scheduling.HumanDuration` 重复；不合并——CLI 命令文件只消费 wire（`cmd/` 里只有组装点 `agentd.go` import 编制域），为一张标签表穿透分层不值；且两个时长函数取整不同（CLI 四舍五入，准入文案向上取整），合并会改 CLI 输出。记 roadmap。S11 无其它待打磨项（段 2/3 已做取整与打码）。
- 文档：
  - `skills/handoff/SKILL.md`：新节「账号与额度：派发看账号，不看载体自报」（`account list|probe`、五档、派发跳过与被拒文案、限额中退役、账号代理优先级、指纹红字）；熟练工节补 notice 块形状与处置（答「已知悉」后续等得 3、别 continue 原会话、旧 CLI 误打成 permission）与只读撞额度换人重跑跟链（stderr 提示原文、无人可换退出 3 文案、只在经小队且有留存输入时发生）；退出码表 3/5 行补注；排障表加两行（准入被拒、重跑链无人可换）。
  - `README.md` / `README.zh-CN.md`：命令表加 `account list`、`account probe`，`reply` 行注 notice 答法；新节 Accounts and Quota / 账号与额度（登记项与缺省值、探测方式与 agy 版本门、五档、载体选账号与指纹、派发跳过、撞额度两种处置、账号代理、限额中退役）；升级节加「全队升级后再绑账号」。agy 探测命令按 `internal/quota/agy.go:84` 写 `agy -p /usage`。
  - `CHANGELOG.md` [Unreleased]：新增「升级须知」小节（同批升级后再绑账号；F-B460-1 notice 块；F-B460-2 按实现写窄范围——只对 notice）；新增 7 条、变更 4 条（准入跳过与文案、限额中退役与 F16、429 不算额度、opencode 失败原因带原文与零退出只有错误判失败的行为变化）。
  - `docs/roadmap.md`：关闭 323（CLI 限额探测健康位）、590（自动限额查询）、600（限额探测与账户池）、975（opencode 失败原因为空）；改写 362 中「限额检测」为已完成、「自动接手」仍开；改写 986 为「无额度部分已由 B453 完成，不可达/满负荷仍开」；「B453 残余」补 10 条（Q9 崩溃窗口、转发留存丢亲和、点名/升级前任务不重跑、notice 无推送、本机全队名、签名表合成、opencode-go 欠交、窗口标签重复、三条旁见）。
- 收尾检查：`go build ./... && go vet ./...` 过；`go test ./internal/skill/` ok（内嵌 SKILL.md）、`go test ./cmd/ -run 'Skill|Account'` ok；文档无其它测试消费（release workflow 按版本标题抽 CHANGELOG，[Unreleased] 新增「升级须知」小节不影响）。本段无代码改动，无红绿周期。

## implement 修复轮 1（R8）

- 来信：R8 浏览器对照原型三处修正——① 底栏段/弹层行的探测失败短因按类别映射，整句 message 只在二级面板/账号卡片；② 载体行显示所选账号状态（S4）；③ 精简模式多组 kind 取各组每周里已用最高的。
- 判断：① 不加 wire 字段，前端按 `last_error.category` 映射（类别全集读自 `internal/quota/quota.go:68-84`）。协调者列的映射照收；它未列而后端会发的五类补了短语：timeout「探测超时」、no_subscription「无订阅」、parse「读数无法解析」、unsupported_platform「本平台不支持」；unsupported_kind、limit_reached、缺类别与未知类别回「探测失败」（limit_reached 只随 out/读数出现，不走 fail 短因）。短因落点 = `failBrief`，底栏段、弹层详细/精简行、账号卡片状态位、载体行同源；整句仍在 title、二级面板、卡片第三行与详情、移动展开。
- 判断：② 载体行口径取原型 `summary(acct,'lead')`——正常每周「已用% 倒计时」，低额度/无额度「状态短语 触发窗口倒计时」，探测失败/未探测只有短语。抽成 `QuotaBits.Summary({order})`，弹层精简行原 `CompactValue` 改用它（tail 序），两处一份规则。
- 判断：③ `weeklyWindow` 改为每周窗口里按「已用最高、并列先重置」取（与 `tightest` 共用 `byTightness`）；详细底栏「最短两个窗口」不动。
- 红（测试先行，样本 grok-mbp/og-mini 的 message 改成真实长句，免得短因与整句相同让断言空过）：`npx vitest run src/app/accounts src/app/settings/SchedulingPage.test.tsx` → `Tests 7 failed | 43 passed (50)`。失败原因逐条为：账号卡片状态位收到「△ 代理 socks5://127.0.0.1:7897 拒绝连接」、底栏/弹层收到整句「△ grok 凭据已过期，等待 grok CLI 刷新（在该机执行 grok login）」、`failBrief` 回整句、`weeklyWindow` 回「Gemini 周」、载体行不匹配 `/OpenCode Go\s*62%\s*4d 2h/`。
- 绿：同命令 → `Tests 50 passed (50)`。全量 `npx vitest run` → `Test Files 150 passed (150) / Tests 1998 passed (1998)`；`npm run typecheck` 过；eslint 触及文件 0 error、5 warning（均为 `SchedulingPage.tsx:265` 既有 exhaustive-deps，改前同数）；`go build ./... && go vet ./...` 过。
- 意外：为比对 lint 基线跑了一次 `git stash` / `git stash pop`，连带把用户未提交的 `b434-ledger.md` 收进又放回，pop 后状态仍为 M、内容未改；此后不再用 stash。

## 真机 R8（2026-10-22，主会话 + 浏览器熟练工；隔离实例 t453-mac 内嵌前端构建 2df747f8，经本机回环鉴权反代 127.0.0.1:7896 访问）

- 入口：先试把一次性 console ticket 放进原型站临时跳转页兑换，被自动权限分类器以「凭据泄露」拦下（临时页与 ticket 文件已删，未绕过）；再试内置浏览器直开 ticket 链接，ticket 已失效未登录。最终改用本机回环反代（`scratchpad/t453/authproxy.py`，token 只在反代进程内、服务端注入 Bearer，仅监听 127.0.0.1），浏览器不经手凭据。
- 结论（浏览器熟练工 1440×900 与 375 视口，对照 `prototypes/b453-accounts/`）：
  1. 底栏：「已用」+ 四账号段，问题账号 og-muse 排前，无「低额度/无额度」字样，右侧刷新——**不符一处**：og-muse 段塞整句 last_error（约 660px），原型为短因「△ 凭据已过期」。详细模式 agy 取两个 5h 窗口（最短两窗口规则），保留。
  2. 使用量弹层：详细/精简切换（缺省详细）、四组、组标题带图标、账号行无图标缩进、详细两行（agy 按组分行）、精简一行每周、底栏同步——符合；**待修**：精简模式 agy 只显示 Gemini 周，Claude and GPT 周被隐藏。
  3. hover 二级面板：窗口细条与「已用 x% / x 后重置」、所在机器、载体、立即探测、管理账号…；不遮挡列表；「管理账号…」→ `/accounts?account=og-muse` 展开高亮——符合。
  4. /accounts：dock 饼图入口、页标题、四组细边框卡片、展开详情（代理/阈值/间隔/上次/「不晚于 14m 后」/上次错误/载体）、登记对话框字段齐（agy 条目置灰「由 agy CLI 管理」）——符合；低额度/无额度卡片状态字因真实账号未越线无法核。
  5. 色彩纪律：彩色仅 gk-mbp「64%」琥珀（≥60 预警）、og-muse 红色状态点与 64% 细条；无药丸、无色块——符合。
  6. 设置→自动化：载体行显示「账号 cx-linux · Codex」，编辑对话框账号下拉（不选/同机账号带状态）——**不符一处**：载体行缺账号状态（原型显示每周用量或状态短语），spec S4 要求显示。
  7. 375：设置首页「账号与额度 4 个 · 2 台机器」，分组、点开展开窗口、无横向滚动——符合。
  8. 控制台：`GET /api/sessions` 403（隔离实例未配 console_user，非本卡）；`/ws/previews` 经反代失败（反代不支持 WS，非本卡）。
- 裁决：短因、载体行状态、agy 精简取最高周三处交实现修复轮 1；其余通过。
- 修复轮 1（R8）交回 a2eca051：偏离 1–4 认可。实现者为比对 lint 基线跑过一次 git stash/pop，用户未提交的 b434-ledger.md 被收进又放回；主会话核对与早先备份逐字节一致、无残留 stash。
