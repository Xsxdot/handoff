# B373 实现审查（OpenCode monitor 插件 + Command Code 一次性 wait）

审查对象：分支 `feat/b373-opencode-monitor` vs `origin/main`（`a0563db84`）。
工作树：忽略无关未跟踪 `docs/superpowers/specs/b358.8.md`、`node_modules`。
对照：spec/plan `docs/superpowers/specs/b373.md`（L1 快道，spec 即 plan）、台账 `docs/superpowers/specs/b373-ledger.md`。
审查者：只读；未改源码、未 commit。行号按当前工作树。
日期：2026-09-15

## 总判

**初审不可合；两条 Important 已在本节点修完。** 安装落点、go:embed、不改 `opencode.json`、OpenCode `monitor` 挂 `--follow`、Command Code 任务级一次性 `handoff wait` 均按 spec 落地，Go 声明缝有牙。初审阻塞：SKILL.md 账本段把 CLI 并不存在的「一次性 `card wait` 每事件退出」写成纪律（Important-1，已改成「不要前台挂 card wait，改走任务级 wait」）；插件 `flush` 在非 busy 的 prompt 失败上无退避地重入（Important-2，已加 dead set、失败不立刻重入）。

## 维度化裁决表

| 维度 | verdict | 证据指针 |
|---|---|---|
| plan 覆盖完整性 | **通过** | spec「实现决定」逐条有落地：`plugins/opencode-monitor/handoff-monitor.ts`（新）；`internal/skill/plugin.go` `InstallPlugin`/`PluginStatus`；`cmd/skill.go:51,79` 状态/install 追加插件行；`main.go:24-29` `go:embed` + `SetPluginContent`；`skills/handoff/SKILL.md:113-129,492-497` 分支；`README.md:181-184` / `README.zh-CN.md:122` 能力差异一句。验收三项：install 写出且与内嵌一致（`plugin_test.go:12-35`、`skill_test.go:81-107`）；无 `.config/opencode` 跳过不造目录（`plugin.go:31-38`、`plugin_test.go:37-50`）；skill 含 Command Code 一次性 wait 与 OpenCode `monitor` follow（`SKILL.md:113-129`、`plugin_test.go:117-143`）。 |
| scope drift | **无**（双向） | **缺项**：无。Out of scope 四条均未做（无 Command Code/Codex 插件、无改 `opencode.json`、插件未塞整本 skill、未给 Command Code 加 skill 落点）。**多余**：`cmd/status.go:147-151` 把插件纳入本机 skill 一致性提示，与「同一次 install 写入」同通道，不算漂移。`cmd/permission_hook_test.go:11-14,53-54` 改用已有 `shortSockDir`：台账 L7 记录 `TestServePermissionHookDenyWithReasonAndStep0` 在 macOS sun_path 104 上红，阻塞 spec 验收包 `go test ./cmd/`；helper 原在 `cmd/permission_mcp_test.go:103-113`。顺手改较短的 Allow 用例是同构预防，不是功能漂移。`CHANGELOG.md` / `README.zh-CN.md` / `install.go:9` 注释为用户可见面与边界注，与 README 一句同级。 |
| 架构法合规 | **通过** | 未升格子系统（架构法第一条实操裁决：不值得为「往已有 skill 落点多写一个文件」付契约冻结成本）。`plugin.go` 与 `install.go`/`state.go` 同属 `internal/skill` → `k_skill_*` / `d_maintenance`（`codegraph/best.json:307-308`）。组装点仍在 `main.go` embed + `cmd.SetPluginContent`（第八条）。不改 OpenCode 配置、不穿透他域内部。`internal/skill` 源文件仍 3 个，未触第三条升格阈。 |
| 测试有牙 | **已验（Go 声明缝）** | 声明缝与看守卫：缺 OpenCode → skipped 且不造目录（`plugin_test.go:37-50`；台账变异 1：`StateSkipped`→`StateInstalled` 命中该用例）。哈希对插件内容不对 SKILL.md（`plugin_test.go:100-115`、`skill_test.go:109-130`）。skill 正文分支（`plugin_test.go:117-143`；台账变异 2：`每次收到一个事件`→`两个` 命中）。空内嵌不写文件（`plugin_test.go:52-65`）。不造 `opencode.json`（`plugin_test.go:32-34`、`skill_test.go:104-106`）。审查者未改源码复变（只读约束），采信台账变异 + 读断言；本机复跑 `go test ./internal/skill/ ./cmd/ -count=1` → `ok internal/skill 0.704s`、`ok cmd 10.143s`。插件运行时（行缓冲/stderr/杀树/busy）无单测——spec 验收未列，属 OpenCode 边界型，不单独作「未验：缝级证据缺失」。注意：`每次收到一个事件` 这句锁在账本段假纪律上，见 Important-1。 |
| 日志与注释覆盖 | **通过** | Go：`plugin.go:28,37,42,46,49` slog Warn/Error/Info，跳过带 reason。TS：JSONL `~/.handoff/opencode-monitor.jsonl`（`handoff-monitor.ts:39-47`）+ `client.app.log`（`86-92,139-146`）；日志失败不得拖垮监视。注释是约束/为什么（`plugin.go:23-24` 不造目录、`53` 不能拿 SKILL.md 比；`handoff-monitor.ts:130-131` promptAsync 必须自记 busy、`187` stderr 不叫醒、`216` detached 为了杀进程组）。无复述「是什么」的废话注，无 TODO 占位。 |
| 序列化边界 | **本次无新字段** | 无 proto / agentd HTTP 新键。插件唤醒走 OpenCode SDK `session.promptAsync`/`prompt` 的既有 `parts:[{type:text}]`（`handoff-monitor.ts:126-137`），与仓内 `promptAsyncRequest`（`internal/executor/opencode/api.go:493-502`）同形。wait 事件仍是命令 stdout 单行 JSON，未改线格式。 |
| 冻结物触碰 | **无触碰** | L1 spec 即 plan，实现未推翻/增补冻结口径。无契约面。`codegraph/baseline.json` 未收入 `plugin.go`（图覆盖债，Check 吃的是 JSON 基线不是活源码，不挡本卡）。 |

## 专项核对（任务指定）

| 检查项 | 结论 | 指针 |
|---|---|---|
| `InstallPlugin` 在 `~/.config/opencode` 缺失时跳过、不造该目录 | **是**。只 `Stat` parent；不存在则 `StateSkipped` + Note。`MkdirAll` 只在 parent 已存在时给 `plugins/` 子目录。 | `plugin.go:31-44`；`plugin_test.go:37-50` 断言 `.config/opencode` 仍 `IsNotExist` |
| 是否曾写 `opencode.json` | **否**。安装路径只 `WriteFile` `handoff-monitor.ts`。 | `plugin.go:45-50`；两处测试断言该 json 不存在 |
| 插件哈希是否对插件内容而非 SKILL.md | **是**。`PluginStatus(content, home)` 对入参 `content` 做 sha256。 | `plugin.go:53-79`；`plugin_test.go:100-115`；`skill_test.go:109-130` skill-v1 vs plugin-v2 |
| Skill：OpenCode+monitor 走 follow；Command Code 一次性 wait，每事件/工单退出；不要把 `monitor_command` 当 follow | **任务级 wait 正确；账本 `card wait` 写错。** 见 Important-1。`monitor_command` 禁令在 `SKILL.md:114,497`。 | `SKILL.md:113-129` 正确；`SKILL.md:492` 错误 |
| 插件：行缓冲、stderr 不叫醒、进程组杀树、busy 排队、promptAsync vs prompt、session.deleted/idle 形态 | **实现与仓内 OpenCode 样本一致。** stdout 按 `\r?\n` 切、残行 `flushRest`；stderr 只 `log`；Unix `detached` + `kill(-pid)`，Windows `taskkill /t /f`；`promptAsync` 后 `busy.add`，同步 `prompt` 不记 busy；`session.status` busy/idle + `session.idle` 清 busy 再 flush；`session.deleted` 静默停。事件形态对齐 `spike5-events.jsonl`（`properties.sessionID` + `status.type`）。 | `handoff-monitor.ts:177-193,195-202,216-223,60-74,130-137,336-360`；样本 `internal/executor/opencode/testdata/spike5-events.jsonl:345-347` |
| `permission_hook_test.go` `shortSockDir` | **正当，非漂移。** 见上表 scope drift。 | `cmd/permission_hook_test.go:11-14,53-54`；台账 L7 |
| `main.go` go:embed TS | **是**，与 SKILL.md 同一组装点。 | `main.go:22-29` |

## Findings

### Critical

无。

### Important

1. **SKILL.md 账本段宣称「一次性 `card wait` 每次收到一个事件就退出」，CLI 没有这种模式。** `handoff card wait` 没有 `--follow` 旗标，默认就是跟流直到成员达终态（`cmd/card_wait.go:72-75,134-147`：`st.Follow` 逐事件 `Encode`，只在全员 `StatusDone`/`StatusClosed` 时退出）。B373 在 `SKILL.md:492` 把「用不带 `--follow` 的一次性 `card wait`，**每次收到一个事件（含工单）就退出**，处置后再挂」写成 Command Code / Codex / 未装插件 OpenCode 的纪律，且与同条「一次工作流只挂一次 `card wait`」自相矛盾。任务级 `handoff wait` 默认一次性退出是对的（`SKILL.md:114`，`cmd/wait.go:49-54`）。用户要求的是 Command Code 对 **wait** 不带 `--follow`、每事件退出——账本段把这句话套到 `card wait` 上会让 Command Code 协调者按假契约操作（前台挂一条永不按事件退出的流，或误以为存在 one-shot 旗标）。`TestSkillTextBranchesWaitModes`（`plugin_test.go:140-142`）锁的恰好是这句假纪律（任务级写的是「返回一个事件」而非「每次收到一个事件」），修文案时会误伤。
2. **插件 `flush` 在非 busy 的 prompt 失败上立刻重入，无退避、无上限。** `handoff-monitor.ts:147-156`：`catch` 把 batch 塞回队列；仅当报文匹配 `/busy|SessionBusy/i` 才 `busy.add`；`finally` 只要队列非空且不 busy 就 `void flush(sessionID)`。会话删除与 in-flight flush 竞态时，`session.deleted` 刚 `queues.delete`（`354-360`），失败的 flush 会 `queues.set` 把队列造回来并紧循环打 prompt。payload 被拒（非 busy）同样 livelock。busy/idle 主路径本身是对的。

### Minor

无单独记账项。测试未锁 `monitor_command` 禁令原文、插件运行时无单测，均不构成独立缺陷（前者正文在 `SKILL.md:114,497`；后者不在 spec 验收清单）。

## Issues

### Issue 1 -- Severity: bug
- File: skills/handoff/SKILL.md:492
- Description: 账本 `card wait` 子弹把「不带 `--follow` 的一次性 card wait、每次收到一个事件（含工单）就退出再挂」写成 Command Code / Codex / 未装插件 OpenCode 的纪律。`handoff card wait` 没有 `--follow`，也不是一事件一退出——它跟流直到子树终态（`cmd/card_wait.go:72-75,134-147`）。同条「一次工作流只挂一次 card wait」与「处置后再挂」互相打架。任务级 `handoff wait` 的一次性语义（`SKILL.md:114`）是对的，不应被这段假纪律覆盖。`internal/skill/plugin_test.go:140-142` 用「每次收到一个事件」锁的是这句错误正文。
- Suggestion: 改写 `SKILL.md:492`：有 `monitor` 的 harness 把 `card wait` 挂成后台长订阅（命令本身即跟流，不必也不存在 `--follow`）；Command Code 不要用 `monitor_command` 冒充 follow，卡回路改走任务级不带 `--follow` 的 `handoff wait`（每事件退出再挂），或写明 `card wait` 做不到 per-event exit、前台挂会直到终态/超时。同步修正 `TestSkillTextBranchesWaitModes`，把「每次收到一个事件就退出」锁在任务级 wait 段，不要锁在 card wait 段。
- Status: fixed（任务级 wait 写「每次收到一个事件就退出」；card wait 写明没有 `--follow`、不是一事件一退出，无行叫醒时改走任务级 wait。测试加「不许写成一次性 card wait」负例。）

### Issue 2 -- Severity: bug
- File: plugins/opencode-monitor/handoff-monitor.ts:147
- Description: `flush` 的 `catch` 在非 `SessionBusy` 错误时仍把 batch 塞回队列，`finally`（L155）立刻 `void flush`。无 backoff、无重试上限。`session.deleted` 清掉队列之后，in-flight flush 失败会把队列写回去并紧打 `promptAsync`/`prompt`，可 livelock 事件循环。
- Suggestion: 非 busy 错误不要在 `finally` 里立即重入：记一笔、等下一次 stdout 行或下一次 `session.idle`；busy 继续现有「加 busy、等 idle 再 flush」。对 `session.deleted`/`dispose` 之后的 in-flight flush，catch 里直接丢 batch，不要 `queues.set`。
- Status: fixed（`dead` 集；失败不立刻 flush；deleted/dispose 后丢 batch。）

## 跑测记录（审查者本机）

| 步骤 | 命令 | 结果 |
|---|---|---|
| 声明验收包 | `go test ./internal/skill/ ./cmd/ -count=1` | `ok internal/skill 0.704s`；`ok cmd 10.143s` |
| diff 范围 | `git diff origin/main...HEAD --stat` | 15 files, +738/-17；与 spec 文件集 + 正当测试基建/文档一致 |
| 未独立变异 | （只读，不改源码） | 采信台账变异 1/2 与用例断言对照 |

## 结论

安装通道与任务级 skill 分支可合；OpenCode 插件主路径（行叫醒、stderr 静音、杀树、busy、promptAsync、idle/deleted）与仓内事件样本和官方插件落点（`~/.config/opencode/plugins/` 自动加载、不必改 `opencode.json`）对齐。两条 Important 须本节点修完再进：假的 `card wait` 一次性纪律会误导 Command Code 卡回路；`flush` 错误路径可自激。无 Critical。
