# B283 spec 审查（悬浮窗终端 tab 每次打开累积）

审查对象：`docs/superpowers/specs/b283.md`（状态：待用户批准）
对照台账：`docs/superpowers/specs/b283-ledger.md`
对照测试：`web/src/app/workbench/b283-redloop.test.ts`（工作树内未提交）
对照代码：工作树 `/Users/xushixin/.handoff/worktrees/manual/B283`，分支 `fix/B283-float-terminal-dup`，HEAD `994311da0`（origin/main），除 spec/台账/红色回路三文件外无未提交改动
审查者：独立 spec 审查人（charter 流，只读；与作者无关联，一切以亲手读码、亲手跑测、亲手只读查询为准）
日期：2026-08-28

行号按当前工作树，会漂。本会话未跑 codegraph（核对对象全是精确到行的引用，直接读码更可靠），符号靠读码——图覆盖债照 b286 先例记一笔，不挡批准。

## 1. 总判

**修订后再批。**

方向对，根因链四环节经独立读码全部成立，且本审查用只读 sqlite 查询本机 agentd 库、grep 本机 agentd.log 独立复证了真机取证（dock 快照 4 个 UUID tab、id==sessionId、machine=mac-02、seq 1..4、windowOpen=false；扇出失败日志 114 行）。红色回路亲手跑过，失败形态与 spec 断言一致（两次打开 tab 1→2）。L2 定级独立验证成立：改动全落 web 控制台，`machines` 字段已在 Go wire（`internal/proto/pty.go:42`）、前端类型（`web/src/api/types.ts:730`）与前端契约夹具（`web/src/api/testdata/PtySessionsResp.json` 含 `machines`）三方就位，无 Go 改动、无新 HTTP 路径（`fetchPtySessions('all')` 已在 `web/src/api/client.ts:632-633`）。

不能批的原因是四条方案里有两条在边界形态上留了承重歧义：方案 2 清除外来 dock tab 后 `activeId` 悬空与「tabs 清空但 windowOpen=true」两个形态没写处理——前者正是 restore.ts:223 注释点名要防的「浮窗一片空白且没人能解释为什么」，现状代码的兜底（restore.ts:225）只修 `activeId===null`，不修悬空；方案 3 的「它名下的会话引用」没写明两处 prune 各自从哪里取机器归属（dock 侧是 `tab.machine`，workbench 侧 TabContent **没有** machine 字段、只能取 `base.machine`），零上下文实现者在 workbench 侧会找不到可用的归属来源。另有一条现状引用指错了接口（`types.ts:194-198` 是 `ProjectTreeResp`，不是 `PtySessionsResp`）。四条都是正文补几句就能修的，不推翻方案。

## 2. 根因链独立验证（任务 B）

逐环节亲手读码，结论：**四环节因果成立，无遗漏环节，未发现更简单的替代解释**。

| 环节 | 独立证据 | 结论 |
|---|---|---|
| ① 扇出缺席误判死亡 | `internal/agentd/pty_api.go:186-241`：`ptySessionsAll` 单台失败只 `st.Error` + warn 日志（210-219），该机器会话整体缺席而 HTTP 仍 200；机器行照常追加（228-229，含失败行）。恢复入口 `useWorkbenchSync.ts:100-111` 只取 `sessResp.sessions` 灌 `buildRestore`，缺席即「不在 live 集」 | 成立 |
| ② 剥引用 | `dockPersist.ts:133-141`、`persist.ts:203-219`：不在 liveIds 的 sessionId 被抹、tab 留位。两处均为恢复主路径调用（`restore.ts:173`、`restore.ts:187`） | 成立 |
| ③ 挂载静默自建 | `TerminalTab.tsx:379-398`：`if (!id)` 即 `createPtySession`，`start()` 在挂载 effect 里调（`TerminalTab.tsx:503`）；会话建成即经 `onSession` → `dock.setSession`（`useHomeDock.ts:187-189`）→ 写回快照（`useWorkbenchSync.ts:152-165` 监听 `dockSnapshot`，去抖 500ms）。孤儿后果有代码自证：「ptyhost 只在 shell 退出时回收、没有空闲清扫」（`TerminalTab.tsx:386-387`）；「不参与 agentd 崩溃或升级重启」（`ptyreclaim.go:10`）；`survive_test.go:20-89` 实证 agentd 客户端整体消失再回来会话与滚屏都在 | 成立 |
| ④ 跨机收编累积 | `restore.ts:196-221`：③ 补孤儿循环对 `input.sessions` 里「live 且不在 used」的会话收编，**无任何 machine 过滤**；home 会话收进悬浮窗时 `machine: s.machine`（209）。`adopt` 的全部生产调用点只有 `useWorkbenchSync.ts:123` 一处；dock tab 的 machine 字段除 ③ 收编外没有任何写入点会产生非空值（`newTerminal`/`newFile` 恒空串，`useHomeDock.ts:142-156`；生产调用点 `HomeDock.tsx:40,58` 均不传 machine）——**真机快照里 4 个 machine=mac-02 的 tab 只能来自 ③ 收编，这是对根因的独立反证式确认** | 成立 |

替代解释排查：React StrictMode 双挂载只在开发端（且 `useWorkbenchSync.ts:73-74` 已有 ranRef/cancelledRef 防护）；decodeDock 版本迁移丢数据（`DOCK_PERSIST_VERSION=1` 从未变过）；第二控制台并发写（dock 快照 per-agentd 单例，本卡不改）。均不能解释真机形状。收编循环是唯一能产生 machine=mac-02 tab 的路径。

真机取证独立复证（只读，未写任何外部状态）：

- `sqlite3 "file:~/.handoff/handoff.db?mode=ro"`：`workbench_singletons` 的 dock 行 = 4 个 tab，全部 `id==sessionId`（UUID）、`machine:"mac-02"`、`seq` 1..4、`windowOpen:false`——与台账/spec 逐字吻合。
- `grep -c 终端会话扇出失败 ~/.handoff/agentd.log` = 114 行；按机器拆分为 win-b37 34 行（connection refused）、mac-02 21 行、linux-01 2 行，text/JSON 双格式各记一次（57 个事件）。台账「win-b37 连接拒绝、linux-01 NODE_OFFLINE / no Host 等」核实；`config.yaml` targets 恰为 linux-01 + mac-02。
- **未核实**：台账里 mac-02 侧 created_at 41ms 四连发、pid 6968/6969/6973/6975 连号——那是远端机器的运行时状态，本机无法独立取证；机制上由「4 tab 同帧挂载各建一会话」自洽支撑，采信台账。台账「本地 base_kind=home 建立记录 6 条均不在当前活会话列表」亦未核实（需活 agentd 查询），不影响链路。

## 3. 现状读数逐条对码（任务 A）

| spec 引用 | 实际 | 结论 |
|---|---|---|
| `internal/agentd/pty_api.go:186` 起，失败只进 machines 行 error 与日志 | `ptySessionsAll` 起于 186，失败路径 210-219 | 成立 |
| `internal/agentd/pty_api.go:189` 本机行恒 ok=true | 189 行 `{Name:"", Ok:true, ...}`，无其他赋值点 | 成立 |
| `internal/agentd/ptyreclaim.go:10` 不参与崩溃/升级重启 | 逐字命中 | 成立 |
| `internal/ptyhost/survive_test.go` 会话跨 agentd 客户端存活 | `TestSurviveAgentdClientRestart`（20-89） | 成立 |
| `web/src/app/homedock/dockPersist.ts:133` pruneDeadDockSessions | 函数起于 133 | 成立 |
| `web/src/app/workbench/persist.ts:203` pruneDeadSessions | 函数起于 203 | 成立 |
| `web/src/app/workbench/TerminalTab.tsx:379` 无 sessionId 即自建 | 379 `if (!id)` → 380 createPtySession | 成立 |
| `web/src/app/workbench/restore.ts:196` 起 ③ 收编 | 196 注释 `// ③ 补孤儿会话` | 成立 |
| **`web/src/api/types.ts:198` / `194-198` 是 PtySessionsResp.machines** | **194-198 是 `ProjectTreeResp`**；`PtySessionsResp` 在 728-731，`machines?` 在 **730** | **不成立（I3）**——事实为真，锚点指错接口 |
| `Shell.tsx:610` base={HOME_BASE} | 逐字命中 | 成立 |
| `Shell.tsx:655` 「agentd 重启会清掉所有会话」 | 逐字命中；相关陈旧注释另有 Shell.tsx:217/270/301、TerminalTab.tsx:8/616 | 成立 |
| 快照本身已是机器本地，控制台连谁用谁的那份 | `workbench_api.go:46-58` 纯本库读（无扇出/转发），路由 `server.go:531-534` 无 scope 变体；`client.ts:661-662` 无参 GET | 成立 |
| 新建走 `newTerminal()` 不带机器 | `HomeDock.tsx:40,58` 均无参；`useHomeDock.ts:144` machine 兜空串 | 成立 |

## 4. Findings

### Critical

无。根因、方向、定级均独立成立；下列各条不推翻方案。

### Important

#### I1. 方案 2 清除后的 `activeId` 悬空与「tabs 清空 + windowOpen=true」形态未写处理

- **位置**：spec 方案 2（`b283.md:50`）、实现决定（`b283.md:70-73`）；活代码 `dockPersist.ts:112`、`restore.ts:223-227`、`HomeDock.tsx:51`、`HomeWindow.tsx:165-233`
- **事实**：`decodeDock:112` 的 activeId 校验发生在**解码时**，方案 2 的丢弃发生在**合成层**（spec 自己写明「decode 照旧接受旧数据」），所以校验帮不上忙：当被清的外来 tab 恰是 `activeId` 指向的 tab（真机快照正是这个形状——activeId=第 1 个 mac-02 tab），purge 后 activeId 悬空。现状兜底 `restore.ts:225` 只修 `activeId === null && tabs.length > 0`，不修「非 null 但指向已不存在的 tab」；`HomeWindow.tsx:165-233` 对悬空 activeId 的渲染是无高亮 tab、内容区空白——restore.ts:223 注释自己把这种状态定义为「没人能解释为什么」的坏形态。另一个形态：所有 tab 都是外来 tab 时清完 `tabs=[]` 而 `windowOpen=true`，`HomeDock.tsx:51` 照样渲染一个只有 tab 条的空窗。
- **为什么承重**：两种读法——(a) 不动 activeId → 升级后首次打开看到空白浮窗（把 B283 的「怪」换成一个新「怪」）；(b) 置 null/改指 → 置 null 后 restore.ts:225 免费接住（重指 tabs[0]）、tabs 为空时还须决定 windowOpen 是否一并收起。差异改变可观察行为，属 spec 阶段必须消除的承重歧义。
- **建议**：实现决定补两句：① purge 命中 activeId 时置 null（让 restore 既有 225 行兜底重指）；② purge 后 tabs 为空时把 windowOpen 一并收为 false（首次升级当拍浮窗不该凭空弹一个空壳）。接缝 1 的③断言加一条：全外来快照 decode+purge 后 dock.tabs 为空且 activeId 为 null。

#### I2. 方案 3 的「会话所属机器」归属来源未写明；workbench 侧 TabContent 无 machine 字段这一现状未记录

- **位置**：spec 方案 3（`b283.md:51`）、实现决定（`b283.md:70-72`）；活代码 `persist.ts:64`（terminal content 形状无 machine）、`dockPersist.ts:100`（HomeTab 有 machine）、`restore.ts:166-178`（prune 按 base 行循环）
- **事实**：门控判据是「该会话所属机器本次扇出是否 ok」，但两处 prune 的归属来源不同：dock 侧可直接读 `t.machine`（decodeDock 强制该字段为 string，`dockPersist.ts:97`）；workbench 侧终端 tab 的 content **没有** machine 字段，唯一可靠归属是该行 base 的 `machine`（TerminalTab 建会话即用 `base.machine`，`TerminalTab.tsx:380-383`）。spec 未写明这一点。零上下文实现者在 workbench 侧可能试图从会话行取 machine——但被判死的会话恰恰不在会话行里，那条路不存在。
- **为什么承重**：归属取错来源，门控在 workbench 侧直接写不出来或写错（整表按错误机器判 ok/不 ok）。扇出侧的对应关系本身可靠（会话行 `Machine` 与 machines 行同循环盖章，`pty_api.go:233-237`；本机行恒在，189），spec 这半句成立——缺的只是消费侧归属来源。
- **建议**：实现决定补一句：dock 侧按 `tab.machine`、workbench 侧按所在 base 行的 `machine` 取归属；并把这个不对称记进现状读数。

#### I3. `types.ts:194-198` 指错接口：那是 `ProjectTreeResp`，不是 `PtySessionsResp`

- **位置**：spec 定级理由（`b283.md:14`）与现状读数（`b283.md:41`）；活代码 `web/src/api/types.ts:194-198`（ProjectTreeResp）、`728-731`（PtySessionsResp，machines 在 730）
- **事实**：引用行号落在 `ProjectTreeResp` 上。`PtySessionsResp.machines` 实际在 `types.ts:730`。同款事实的旁证：`internal/proto/pty.go:39-42` wire 侧、`web/src/api/testdata/PtySessionsResp.json` 契约夹具已含 `machines`。
- **为什么承重**：现状读数标注「消歧用，contract 节点复核」，锚点指错接口会把复核引到不相干的类型上（b286 审查 I8 同类定级）。
- **建议**：两处引用改为 `web/src/api/types.ts:728-731`（可补 wire 侧 `internal/proto/pty.go:42` 与契约夹具作旁证，L2 论证更硬）。

#### I4. Out of Scope「后续要做」三条未落 roadmap——spec 收尾自检第 6 条未完成

- **位置**：spec OOS（`b283.md:87`）；`docs/roadmap.md`（全文件无 B283 条目，末段只有 B286 的五条）
- **事实**：OOS 三条「本期不做、后续要做（进 roadmap）」在 roadmap 里一条都不存在。spec SKILL 收尾自检第 6 条把「逐条落进 docs/roadmap.md」定为 spec 节点义务，且明说「推迟项只活在 OOS 行里，会话一断就成孤儿（实测）」。
- **建议**：批准前把三条（远程 home 终端显式入口、ptyhost 空闲回收/孤儿清扫、扇出部分失败的用户可见呈现）逐条追加进 `docs/roadmap.md`，注明来源 B283 spec。

### Minor

#### M1. 红色回路「转正转绿」需要反转 open1 的断言，spec 未点明

`b283-redloop.test.ts:44` 现状断言「机器缺席时 sessionId 被剥」（注释自认「先钉住现状行为」）；方案 3 落地后该机器缺席不剥、sessionId 保留，这条断言若原样并入必红。spec 实现决定「红色回路用例转正并入 restore.test.ts 并转绿」（`b283.md:73`）应补一句：open1 断言按缝 1 ①反转为「保引用」，不是原样搬。

#### M2. 定级理由的落点清单漏了两个将要改动的文件

`b283.md:14` 列「restore.ts、persist.ts 与一处文案」，但方案 3 需要 `useWorkbenchSync.ts` 传出 machines 映射（现 101-107 行只取 sessions），dock 侧 prune 门控的落点也绕不开 `dockPersist.ts`（pruneDeadDockSessions 在此）或 restore.ts 内改写调用方式。不改 L2 结论（全在 web），但清单应如实列全，免得 plan 对不上。

#### M3. 方案 4「相关注释」未列点

陈旧话术散布在 `Shell.tsx:217/270/301/655` 与 `TerminalTab.tsx:8/616` 六处。文案本体（655）已点名，注释靠 grep「agentd 重启」可发现，不算假缝，但列点可省 acceptance 一眼时的歧义。

#### M4. 方案 1 使 `baseOfSession` 的 home@machine 分支成为生产死代码，去留未表态

`restore.ts:41-43` 的 `~@machine` 分支唯一调用点是 ③ 循环（`restore.ts:205`）；③ 跳过外来 home 会话后该分支生产不可达。保留或删除是 plan 决定，但 spec 应留一句（例如「分支保留，符号锚随收编判据走」），免得 review 节点当 scope drift 误报，也免得测试继续锁一条死路径。

#### M5. 「真连不上走既有 1008 显式死」措辞过满

1008 是「会话不存在/被吊销」（`pty.ts:15,181`）——覆盖机器重启过的情况；机器网络不可达时 WS 根本建不起来，走的是连接错误路径（TerminalTab 另有 error/dead 呈现），不是 1008。两条出口都存在且都有「重开」终态，结论不变，措辞宜分开写。

#### M6. 台账「114 条扇出失败」是行数不是事件数

`agentd.log` 对同一事件按 text/JSON 双格式各记一行：114 行 = 57 个事件（win-b37 17、mac-02 10-11、linux-01 1 个事件量级）。结论不受影响，计数口径宜注一句，免得后续引用者当 114 次独立故障。另：mac-02 自身也扇出失败过——这反而强化链路（收编来源机器自身就抖），台账可补。

#### M7. 方案 2 的「一次性清除」实为「恢复后随首次写回落盘」，用户可见外来 tab 消失

这是用户裁决（本机面）的直接后果，方向没错；建议备注一句「升级后首启外来 tab 消失属预期」，免得 acceptance 把它当回归。

## 5. 方案逐条对抗（任务 D，缺陷族设问）

**通用五族逐族结论**（族名 | 设问 | 结论）：

- **生命周期/状态机中断** | purge 与写回之间宿主重启？| 幂等：purge 发生在每次恢复，未写回则下次重做，无半态。机器注销后 machines 无行 → 门控恒「不 ok」→ 该机器 tab 永不剥，用户可手动关（有重开出口）——spec 已表态保守取向（`b283.md:72`），成立。
- **静默失败/误导报错** | 机器离线时保留的引用点开会看到什么？| 不新建会话（sessionId 在，不走 `TerminalTab.tsx:379` 自建支），attach 失败走既有 error/dead + 重开出口——正是用户故事 3 的可见行为。M5 的措辞修正不改变结论。
- **跨平台假设** | 本改动纯 web TS，无平台假设 | 无，因为不涉及任何平台差异面（桌面/webview/路径）。
- **假红/假绿** | 红色回路锁的是不是调用方依赖的行为？| 是：断言 `buildRestore` 的输出形状（tab 数、sessionId 有无），这正是缝 1 声明的调用方依赖面；换实现不改需求不会无意义翻红。open1 断言钉现状属「先钉后反」，见 M1。两面断言齐备（strip 有钉、growth 有钉）。
- **门禁绕过** | 新增写路径？| 无新写路径；machines 映射是既有扇出响应的只读投影，两处 prune 用同一份响应判 ok——单一真相，无 TOCTOU 窗口。

**追加设问**：

- **序列化边界** | 本卡不新增序列化字段；`machines` 的 wire↔TS 两侧已有契约夹具锁着（`PtySessionsResp.json`），消费侧不新增投影。无穿透风险。
- **枚举新值** | 无新枚举值。
- **承重安全属性** | 不涉及。

**任务 D 指定边界逐项**：

1. **方案 2 × decodeDock:112 / hydrate**：activeId 悬空与空 tabs 见 I1（有病）。seq/计数器播种**不受影响**（已核）：被清 tab 的 id 是 UUID，本就不参与 `tabIdCounter` 播种（`useHomeDock.ts:247` 的 `/^h(\d+)$/` 过滤）；其 seq 移除只会调低种子且 tab 已不存在，不会撞号（`useHomeDock.ts:244-252`）。恢复后清过的 dock 与写回种子 `dockSentRef`（`useWorkbenchSync.ts:117`）不同，首拍自动 PUT 一次清后形态——正是「一次性」的落盘机制，顺带成立。
2. **方案 3 × 本机恒 ok / 机器注销 / workbench 同规则 / machine 戳可靠性**：本机行恒 `ok=true`（`pty_api.go:189`，独立核实无第二赋值点）成立；注销机器不在 `pool.Names()` → machines 无行 → 按 spec 判「不 ok」保守保留，成立；扇出循环里会话行 machine 戳与 machines 行同源盖章（233-237），对应关系可靠；workbench 同规则的归属来源缺口见 I2。
3. **方案 1 × baseOfSession 死代码 / 同机多控制台**：死代码见 M4。同一台机器浏览器+桌面端共享同一份 per-agentd dock 快照：两控制台恢复同一快照、引用同一批本机会话，`used` 集合挡住重复收编；本机 home 会话归属本机 dock 与「本机面」裁决一致，无行为回归。
4. **定级 L2**：成立（三要件亲手验证：wire `internal/proto/pty.go:42`、TS `types.ts:730`、无 Go 改动与无新 HTTP 路径——恢复流程复用既有 `fetchPtySessions('all')`）。非 L1 的论证也成立：ok 门控判据矩阵、清理次序、两 prune 规则一致性，plan 写出来不止复述 spec。
5. **接缝清单**：缝1 `restore.ts#buildRestore`（导出，唯一生产调用方 `useWorkbenchSync.ts:105`）；缝2 `persist.ts#pruneDeadSessions`（导出，生产调用方 `restore.ts:173`，spec 写「useWorkbenchSync→buildRestore」链条属实）。**均真缝，无假缝**；文案不设缝级断言、归 acceptance 一眼，处置正确。缺口：I2（缝2 断言缺归属来源表述）与 I1（缝1 缺 purge 后形态断言）。

## 6. 骨架门检（任务 E）

| 自检项 | 结论 |
|---|---|
| 骨架段落齐全 | 齐全。问题陈述（含谁/通道/要变的事实）、级别与档位（头部+定级理由）、方案含弃选四条各附理由、用户故事编号列表、实现决定、测试决定接缝清单、Out of Scope、备注；L2 无契约段，正确 |
| Out of Scope 非空且分类 | 已分「后续要做（进 roadmap）」与「永不做」两类——但 roadmap 落账缺失，见 I4 |
| 定级复核 | 头部 L2，理由与独立验证一致（第 5 节第 4 条） |
| 读数分界 | 合格：台账收原始读数（真机 SQL 取证、日志计数、勘误过程），正文只留结论与一行台账指针；根因链段的「真机证据」是结论形态 |
| 零上下文复读 | 无 TBD；含糊点即 I1/I2/M3/M4 四处，均为局部补句可修 |
| 弃选质量 | 四条弃选全部站得住：重试/等就绪把冷启动挂最慢机器且盖不住注销（对）；localStorage 降级更差且边界是「机器」不是「设备」（对）；打标记保留连不上的死 tab 只占位（对）；修 Shell.tsx:610 属死代码加固（对——修复后 dock 终端 tab machine 恒空串，该不对称不可达，已独立核过 newTerminal/收编两条写入路径） |

## 7. 用户故事与验收（任务 F）

四条故事全部真机可验收、判据落在用户可见面、无「本轮该干什么」的实现语泄漏（全文未出现 buildRestore/machines 映射入故事判据），节点中立性成立。故事 1 的「会话真死的除外：tab 保留、终端重开」把设计内行为与缺陷行为划清，验收时不会把正确行为误报为 bug。故事 3「机器回来后接上的还是原来那个会话」可由 sessionId 比对机械验收。故事 2 与「永不做」边界互相印证。无修改意见。

## 8. 批准前最小补丁（只改 spec 正文/roadmap，不是代码）

1. **I1**：实现决定补 purge 后 activeId 置 null（交给 restore.ts:225 既有兜底重指）与「tabs 清空则 windowOpen 收为 false」两句；缝 1 ③加全外来快照清空形态断言。
2. **I2**：实现决定写明两处 prune 的机器归属来源（dock=tab.machine；workbench=base 行 machine），现状读数补「TabContent 无 machine 字段」。
3. **I3**：`types.ts:194-198` 两处引用改为 `types.ts:728-731`，可加 wire `pty.go:42` 与契约夹具旁证。
4. **I4**：三条后续做项落 `docs/roadmap.md`，注明来源 B283 spec。

M1–M7 建议一并带入（M1/M2 与上述同段修改顺路），均不单独挡批准。

方向保持：判据收口 buildRestore 合成层、home 收编仅本机、存量外来 tab 合成层清除不 bump 版本、扇出缺席不判死、话术订正——五者与活代码和用户裁决逐条对得上，无需返工。
