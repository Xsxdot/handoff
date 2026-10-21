# 「载体账号与额度」现状调查报告（只读，基线 fad1aadf）

> 调查仓：handoff（`/root/.handoff/worktrees/db010c7d`，detached HEAD `fad1aadf`）。
> 本报告只读，未改工作树任何文件。所有结论带 `文件:行号`；推测已标「推测」。

## 1. 载体模型：结构、四态、ApplyDetect、调度是否跳过 quota

### 1.1 Carrier 结构
- `internal/scheduling/scheduling.go:52-65`：`Carrier{Name, Machine, CLI, HomeDir, Model, Credential, MaxConcurrency, Status, LastError}`；注释明示 Status 是唯一运行真相，`LastError` 不参与准入。
- `internal/scheduling/scheduling.go:44-50`：`CredentialSource` 两值 `standalone` / `main_home_sync`。
- `internal/scheduling/scheduling.go:75-86`：`SquadMember{Carrier, MaxConcurrency}` 政策位；`Binding{Squad, Carrier, Target, Executor, Model, HomeDir}`，`HomeDir` 是绑定当时快照，空=沿用主 HOME（`scheduling.go:137-138`）。

### 1.2 四态定义
- `internal/scheduling/status.go:22-30`：`StatusPending="pending"`（未上线）、`StatusOnline="online"`（已上线）、`StatusQuota="quota"`（限额中）、`StatusUnreachable="unreachable"`（不可达）。
- `internal/scheduling/status.go:38-52`：`Label()` 中文名与 `web/src/api/scheduling.ts:196-201` 的 `CARRIER_STATUS_LABEL` 同一份词表。

### 1.3 ApplyDetect 判定顺序
- `internal/scheduling/status.go:85-89,106-119`：优先级「不可达优先、限额次之、需登录再次之、否则上线」。细节：
  - `!Reachable`：若旧态是 online/quota/unreachable → `unreachable`，否则（pending/新建）保持 `pending`（`status.go:107-112`；测试 `status_test.go:83-86`）。
  - `ev.Quota` → `quota`；`ev.NeedLogin` → `pending`；否则 → `online`，并清空 `LastError`（`status.go:113-124`）。
- `DetectEvidence{Reachable, NeedLogin, Quota}` 定义 `status.go:73-79`；写回经 registry CAS（`status.go:89-138`），`detectEvidence` 白名单映射在 `internal/agentd/schedapi.go:364-369`（`ready→Reachable`、`need_login→Reachable+NeedLogin`、`quota→Reachable+Quota`、`unreachable→{}`）。

### 1.4 quota 今天由谁写入
- 唯一写状态入口是检测链：`POST /api/squads/carriers/{name}/detect`（`agentd/schedapi.go:237-360`）→ `WakeHome` 得 outcome → `ApplyDetect`。`quota` outcome 来自 `hostapi.classifyTurnError` 对 RunTurn 错误文本的关键字匹配（`quota|rate limit|too many requests`，`probe.go:430-443`）。
- 新建/改 HOME 时 `PutCarrier` 强制回 `pending`（`scheduling.go:225-297`），不写 quota。
- 「派发失败或后台探活把已上线改成不可达」**今天不存在**：roadmap 明确记为未做（`docs/roadmap.md:591`）；「周期探测自动迁移 online↔quota」也未做（`docs/roadmap.md:590`）。因此今天 quota 只能由人工点 detect 写入。

### 1.5 调度是否跳过 quota
- 是，全部跳过。`admitInto`（`scheduling.go:1069-1101`）、`SelectWhere`（`scheduling.go:618`）、`AdmitFrozen`（`scheduling.go:714`）、`AdmitSeatCarrier`（`scheduling.go:838`）、`AdmitCarrier`（`scheduling.go:428`）、`LaunchAdmit`（经 `admitInto`）一律只认 `Status == StatusOnline`，quota/pending/unreachable 均无差别地拒绝（`ErrNoHealthy`）。Select 里 quota 与 offline 同走 `carrier_offline` 日志分支，无单独计数。

## 2. 载体 HOME 与凭据

### 2.1 credential 两值含义
- `standalone`（独立账号）：隔离 HOME 自带凭据，不碰主 HOME。
- `main_home_sync`（主 HOME 认证态同步）：空目标 + 主 HOME 有凭据时，对外三态按 `logged_in` 报（`probe.go:223-236` `applyMainHomeSync` 只读对照，不改目标目录）；`WakeHome` 在目标目录本身为空时先拷表内凭据再检测（`probe.go:126-132`）。`standalone` 不拷。
- 新建载体空 credential 默认为 `standalone`（`scheduling.go:233-235`；测试 `registry_read_test.go:294-309`）。登记弹窗 B334 起不再预填默认 HOME（`status.go:55` 注释）。

### 2.2 copyMainCredential 复制哪些文件
- `internal/hostapi/probe.go:289-327`：只复制**单个表内凭据文件**——经组装点注入的 `resolveCredential(cli)` 解析出相对路径（如某 CLI 的 token/key 文件），从主 HOME 读该文件、写入目标 HOME 同一相对路径，只建该文件需要的父目录，「不搬运技能/规则树」（`probe.go:290` 注释）。权限沿用源文件 mode。`resolveCredential` 未覆盖的 CLI（非表内）返回 false、不复制（`probe.go:295-298`）。**具体每个 CLI 对应哪个相对路径，本轮未在 hostapi 内找到映射表，须看组装点注入处（未验证）。**

### 2.3 home_dir 为空时 opencode 用哪个 HOME
- 探测/检测：空串回落到目标机主 HOME（`probe.go:163-180`，`userHomeDir()` + `filepath.Clean`）。
- 回合进程：`buildEnv` 中 `expandedHome==""` 时不覆写 `HOME`，子进程继承 agentd 的 HOME（`driver.go:268-303`）；`RunCommand` 空 HOME 只输出 CLI（`status.go:64-71`）。
- 协调者续接同理：`keystone.go:313-323` 注释「协调者小队 → LaunchAdmit → Binding → Carrier 读 HomeDir」，`coordinator_home.go:117` 协调者载体固定 `main_home_sync`（推测：协调者默认与主 HOME 同态）。

### 2.4 model 字符串 provider 前缀是否被解析
- **不解析，原样透传。** `hostapi/driver.go:240-249` `buildArgv` 只是 `if req.Model != "" { args += ["-m", req.Model] }`；`TurnRequest.Model` / `WakeRequest.Model` / `Binding.Model` / `EffectiveModel`（`receiver.go:167-174`）均无 split/解析。handoff 内无 `providerID` 切分逻辑（grep `provider.*split|Split.*model|strings.Split.*model` 在 internal 无命中；`providerID` 只出现在 opencode 事件 JSON 解析与注释里）。
- 背景证据：`docs/superpowers/backlog.md:54.3` 记载「模型 id 写成 `deepseek-v4-flash` 缺 `opencode-go/` 前缀连续零产出，补上前缀恢复正常」——说明前缀语义由 opencode CLI 侧解释，handoff 只负责把登记的完整串（如 `opencode-go/deepseek-v4-flash`）经 `-m` 递给 CLI。`b452-ledger` 台账亦记 `t452-oc` 登记 `opencode-go/deepseek-v4.1-flash`（`docs/superpowers/specs/b452-ledger.md:104`）。

## 3. 探测链路

### 3.1 完整链路
1. 入口 `POST /api/squads/carriers/{name}/detect`：`internal/agentd/schedapi.go:237-360`（路由 `schedapi.go:44`）。
2. 读载体登记 → 本机（`IsSelfMachine`）直接调 `hostAPI.WakeHome(ctx, {CLI, HomeDir, Credential, Model})`（`schedapi.go:256-266`）；远程机经 `forwardJSON` 转发 `POST /api/host/wake` 到目标机（`schedapi.go:267-314`），body 为 `HomeWakeReq`。
3. 目标机 `handleHomeWake`（`agentd/hostprobe.go:3-83`，空 home_dir 合法、原样交给 WakeHome）→ `ProbeHome` 只读探测 → 条件 `copyMainCredential` → `MkdirAll` → `runWake`（`probe.go:109-147, 329-371`）。
4. `runWake`：名单内 CLI（今天仅 `opencode`，`driver.go:52`）经 `detectTurn`（=`RunTurn`）发 `DetectPrompt="ping"`（`probe.go:26, 353-357`），`Timeout=0` 用 `DefaultDetectTimeout=3m`（`probe.go:21-23`）；名单外只 `lookPathWithFallback` 查命令（`probe.go:339-351`，B342：找不到→unreachable，找到→ready，不跑回合）。
5. `RunTurn` 本体 `driver.go:71-88, 102-198`：`opencode run --format json [--] ping`，`-m <model>` 透传（`driver.go:242-244`），`HOME=<隔离HOME>` 确定性注入且赢过 env 同名行（`driver.go:264-303`），超时杀整棵进程组，stderr 只取尾部 4096 字节进错误消息（`driver.go:170-181`，B399 r2 只有含 "Session not found" 才包哨兵降级）。
6. 回包 `WakeReply{Outcome, Detail}` → 协调机 `detectEvidence` 白名单转 `DetectEvidence`（`schedapi.go:364-376`，未知 outcome 直接 502、不落库）→ `ApplyDetect` 写 registry → 读回 `CarrierRows` 取新 version → `CarrierDetectResp{Name, Status, LastError, Version}`（`schedapi.go:323-360`；wire 定义 `proto/scheduling.go:172-178`）。

### 3.2 哪些 CLI 真跑回合
- 仅 `opencode`（`driver.go:52 supportedCLIs={"opencode":true}`）。codex/grok/agy 等一律走「查命令是否存在」分支，不跑回合。

### 3.3 探测结果写回哪里
- 协调机持有的 registry（账本设施）：`ApplyDetect` 以 CAS `Put(kindCarrier, name, rec.Version, …)` 写回（`status.go:125-138`），actor `"scheduling.detect"`。远程机只负责 Wake，不写状态（`schedapi.go:234-236` 注释）。

### 3.4 roadmap 相关条目
- `docs/roadmap.md:975`：「载体 CLI 回合失败时只截 stderr 尾部；opencode 把错误（如 403 ENTITLEMENT_ERROR）写在 stdout JSON，导致失败原因显示为空。来源：B448 R11。」对应代码 `driver.go:170-181`（tail 4096）+ `consumeEvents` 跳过非 JSON 行（`driver.go:309-336`）：opencode 失败时 stdout JSON 里错误信息被当普通事件行消费/丢弃，stderr 又是空，Detail 为空——与 b452 台账实测「`stderr 尾部:` 空」一致（`b452-ledger.md:104`）。

## 4. 代理 / env 文件注入

- 机器级配置两处（`internal/config/config.go`）：
  - `proxy: string`（`:83-100`）：handoff **自身**出网代理；空=沿用进程环境 `HTTPS_PROXY/HTTP_PROXY/NO_PROXY`；故障域声明「只影响升级，不影响任务执行」（`:91`）。校验 `proxycfg.Validate`（`:640`）。
  - `env: map[agent]文件名`（`:133-136`）：executor 名→`<DataDir>/env/` 纯文件名映射；`env_forward: []string`（`:116`）为终端透传键（查 `agentd/pty_api.go:59` 经 `envfile.LoadFile` 加载注入终端）。
- 解析器 `internal/envfile/resolver.go:26-84`：`Dir(dataDir)=<dataDir>/env`；`For(agent)` 每次现读、无缓存，改文件/改映射下个任务生效、无需重启 agentd；未配置返回 nil；日志只打 key 名不打值。
- 注入执行器任务进程：`orchestration/manager.go:767-770`（dispatch 最前段，早于建任务/建树，失败干净拒发）、`:1539`、`:3832`；审批者链每个候选各取 `env.For(name)`（`approver.go:571-591`、`approver_chain.go:356`）。
- 注入载体检测回合：`probe.go:373-426 wakeProxyEnv(cli)`——优先取进程环境既有代理；缺失时读 `~/.handoff/env/{opencode.env, proxy.env}`（注意：是**固定家目录** `os.UserHomeDir()` 下的 `.handoff/env`，不是 `<DataDir>/env`）补 `https_proxy/http_proxy/ALL_PROXY/NO_PROXY` 七键，支持 `$NO_PROXY` 展开。只注入检测回合 `TurnRequest.Env`（`probe.go:356`）。
- **载体没有自己的 env 文件或代理字段**：`Carrier` 结构（`scheduling.go:55-65`）与 `CarrierInput`（`proto/scheduling.go:64-72`）均无 env/proxy 键；grep `carrier.*[Ee]nvFile|envfile.*carrier` 在 internal 无命中。结论：载体级 env/代理今天不存在，检测回合走机器级代理（进程环境或 `~/.handoff/env/*.env`），任务回合走 executor 名映射的 `<DataDir>/env/<file>`。

## 5. agentd 现有周期任务写法

统一形态：`time.NewTicker` + `select { ctx.Done / ticker.C }`，组装在 `cmd/agentd.go`，停止靠 `wdCtx` 取消（`GracefulShutdownCleanup(wdCancel)`，`agentd.go:410`）与各 `Stop()`：

| 任务 | 间隔 | 实现 | 组装 | 停止 |
|---|---|---|---|---|
| 产物保留期清扫 | 启动一次 + 每 1h | `orchestration/artifacts_retention.go:58-78 RunArtifactsRetention`（`retention()` 每轮现读活配置；`interval<=0` 取 1h；只删终态任务 `<DataDir>/tasks/<id>/artifacts`） | `cmd/agentd.go:367` `go mgr.RunArtifactsRetention(wdCtx, …, time.Hour)` | ctx 取消 |
| 任务看门狗（stalled/失配/进程围栏） | 每 1m（`watchdogTick`） | `orchestration/watchdog.go:120-147 RunWatchdog/runWatchdog` | `cmd/agentd.go:394-396` | ctx 取消 |
| 事件镜像（多机发现） | 启动一次 + `mirrorDiscoveryTick`=30s | `workspace/mirror.go:158-179 Run`（另为每台远端机起快照门铃循环 `ensureSnapshotLoops`） | `cmd/agentd.go:400-401` | ctx 取消 + `Mirror.Stop()`（`mirror.go:203-211`，cancel subs + wg.Wait） |
| 预览镜像 | 每 30s | `agentd/preview_mirror.go:79 ticker 30s`（`PreviewMirror.Run`） | `agentd/server.go:771-785 StartPreviewServices` 内 `go Run(ctx)` | `StopPreviewServices` → `previewMirror.Stop()`（`server.go:787-805`） |
| 预览 owner TTL | 每 1m | `workspace/preview_owner.go:474-498 startExpiry`（`Restore` 时启动，`Expire` 关 idle 会话） | `Restore`（经 `StartPreviewServices`） | `PreviewOwner.Stop`（`preview_owner.go:501-519`，cancel + `expireWG.Wait()`） |
| 唤醒认领续租 | `wakeClaimRenewInterval` | `agentd/wakeconsumer.go:597-633 startWakeClaimRenewal`（包级 var 捕获为本地副本，stop 等 goroutine 真正退出） | 按卡按需启动，返回 stop func | `stop()` 关 done + `<-stopped` |
| 自动化 drain | `automationPollInterval` | `agentd/scheddrain.go:90 ticker` | 同文件消费循环 | ctx 取消（推测， draining 循环常规形态；**未逐行验证**） |

## 6. 控制台与 CLI

- Web 文件：
  - API 面：`web/src/api/scheduling.ts`（`CarrierView/SquadView/SquadsResp/QueueResp` 类型 + `getSquads/getQueue/putCarrier/putSquad/probeHome/wakeHome/detectCarrier/getCarrierRunCommand`；`CARRIER_STATUS_LABEL` 四态中文、`defaultHomeDir` 与后端同格式）。
  - 页面：`web/src/app/settings/SchedulingPage.tsx`（编制展示 + CAS 编辑分区；`SchedulingPage.test.tsx` 锁接缝）→ 挂载于 `SettingsPage.tsx:26,128`（`section==='automation'`）与 `SettingsHub.tsx:20,87`（compact）；`FlowsPage.tsx:4-5,98-231` 读 `getSquads` 做执行者/协调者小队下拉；`rooms/NewSessionDialog.tsx:12-54` 只列 coordinator 小队。
- `GET /api/squads` 响应：`proto/scheduling.go:45-53 SquadsResp{carriers[], squads[], running[]}`；载体行 `CarrierView:18-29`（name/machine/cli/home_dir/model?/credential/max_concurrency?/status?/last_error?/version），小队行 `SquadView:38-43`（name/role/members[]/version，政策位在成员上），占用行 `RegistryRunningView:57-60`（key=`squad/<队>/<载体>` 或 `carrier/<载体>`，count）。空库返回 `[]` 而非 null（`:45-46` 注释）。线格式由 `proto/contract_fixture_test.go:188-204` + `web/src/api/contract.test.ts:622-637` 钉住，fixture `web/src/api/testdata/SquadsResp.json`。
- CLI：`handoff squad list`（`cmd/squad.go:145-212`）：缺省表格列「类别/名称/角色·机器/成员·CLI/并发上限/版本」（`:194`），载体行打 `machine/cli`，小队行打成员串（`formatSquadMembers`），运行位行打 key/count；`--json` 一行一对象依次输出 carriers→squads→running（`:170-191`）。

## 7. 账本 registry kind 机制

- 存储层无 kind 白名单：`ledger/registry.go:35-124` 的 Put/Get/List/Delete 只以 `(kind,id)` 为键，无 CHECK 约束、无合法 kind 表（grep 确认无 `kind.*CHECK|allowedKind|validKind`）。kind 是调用方约定的名字空间。
- 今天使用的 kind（编制域 `scheduling.go:27-33` + `receiver.go:26-31`）：
  - `carrier`（载体定义）、`squad`（小队定义）、`sched_running`（两级占用计数，键 `squad/<队>/<载体>` / `carrier/<载体>`，见 `occupancy.go`）、`launch_queue`（`KindLaunchQueue` 协调者拉起队列）、`ignition_queue`（`KindIgnitionQueue` 执行者点火队列）、`default_carrier`（默认载体 singleton，id 恒 `current`，`receiver.go:29-30`，body 经 `Encode/DecodeDefaultCarrier`，`receiver.go:176-196`）。
- 新增一种 kind 要改的地方：
  1. 常量：编制域 `scheduling.go:27-33`（或 `receiver.go` 如 default_carrier）。
  2. 跨 kind 同名冲突：`CrossKindConflict`（`receiver.go:139-145`）+ `PutCarrier:243-251` / `PutSquad:400-408` 的对侧存在检查——新 kind 若参与名称空间需在此加检查（今天只防 carrier↔squad 同名）。
  3. 投影/观察面：`CarrierRows`/`GET /api/squads`（`schedapi.go`）、`SquadsResp`（`proto/scheduling.go`）如需暴露；`squad list` JSON/表格（`cmd/squad.go:170-205`）如需展示。
  4. 测试：`scheduling/*_test.go`、`proto/contract_fixture_test.go`（线格式）、`ledger/registry_pg_test.go:29` 冒烟 kind 注释提及的「冻结五 kind」。
- `ledgerSchemaVersion` 何时必须升：`ledger/schema_version.go:19-22` + `ledger/store.go:851-854`——改 `ddlStatements` 或在 `ensureSchema` 闸内加任何迁移就必须 `+1`（`TestSchemaVersionPinsDDL` 按 DDL 指纹强制，`schema_version_test.go:34-37`）。**纯新增一种 registry kind（只换 kind 字符串、不改表结构）不需要升版本**（registry 表是通用键值结构）；只有动 DDL/迁移才升。PG 过版本闸（`store.go:819-848`），SQLite 不过闸。

## 8. roadmap 相关条目原文行号

- `docs/roadmap.md:323-324`：「**CLI 限额探测（小队成员健康位）**：探测到额度耗尽的成员不领活；三期只预留成员健康位的形状。触发条件：小队真跑起来后第一次撞限额。」
- `docs/roadmap.md:361-365`（约 362）：「三期承接…；**限额检测与自动接手（本期只有手动换绑+重建四步）**；…」。
- `docs/roadmap.md:426-428`（约 426）：「**Web 控制台加驱动释放/接管入口与运行锁展示**：今天 Web 只读 `driver_session` 做展示（`web/src/app/cards/CardDrawer.tsx:369`），没有释放/接管入口…」。
- `docs/roadmap.md:442-447`（约 447，接管机制说明）：执行者由 agentd 经 shim 以独立会话拉起、重启不连坐（`KillMode=process` 硬要求），重启后按 `proc.json` + `proc.lock` 存活锁重新接管。「**这是设计使然，不是侥幸。**」
- `docs/roadmap.md:590`：「**自动限额查询**：周期探测账户额度，把载体状态在「已上线」↔「限额中」之间自动迁移，不必人点检测。…本卡只把「限额中」状态位落地并允许检测写入。」
- `docs/roadmap.md:600`：「**CLI 限额探测与账户池**：同 HOME / 同凭据的多个载体共享真实账户额度，载体级物理上限管不到跨载体的账户池。…B292 调度仍按各载体物理上限保守计数。」
- `docs/roadmap.md:975`：「载体 CLI 回合失败时只截 stderr 尾部；opencode 把错误（如 403 ENTITLEMENT_ERROR）写在 stdout JSON，导致失败原因显示为空。来源：B448 R11。」
- `docs/roadmap.md:986`：「载体不可达/满负荷/无额度时顺小队换人；B452 只报错点名机器。归 B453。」
- 另见：`roadmap.md:974`（detect 后须 online 否则叫机器人报无可用载体、文案未提示去探测）、`roadmap.md:591`（不经检测写入不可达未做）。

## 查过但没找到的位置（诚实记录）

- 各 CLI 的 `resolveCredential` 凭据相对路径映射表：hostapi 内只有注入点（`probe.go:256-265`），具体表在组装点，未定位。
- `wakeProxyEnv` 读的 `~/.handoff/env/*.env` 与 `<DataDir>/env/` 的关系：代码如此（`probe.go:386-390`），历史原因未考证。
- `scheddrain.go` 自动化 drain 循环的停止路径：只确认 ticker 存在，未逐行验证取消链。
- 载体 model provider 前缀：全仓 grep 无解析逻辑；若 opencode 2.x 等新 CLI 改 `-m` 语义，需另行实测。
