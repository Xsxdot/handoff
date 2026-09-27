# B397 OpenCode V2 支持：协议隔离、双宿主 monitor 与可回收执行器 — 设计

- 日期：2026-09-22
- 卡：B397（高，charter · 待办）
- 级别 / 档位：**L3 重档**（跨 `d_maintenance`、`d_execution`、`d_orchestration`、`d_cli`）
- 状态：**修订稿，待用户批准（2026-09-22）**
- 证据台账：`docs/superpowers/ledgers/2026-09-22-b397-spec-ledger.md`
- 历史 spike：`docs/superpowers/specs/b397-spike-v2-serve.md` 与路由归档；其结论若与本 spec 冲突，以本次隔离探针和本 spec 为准

> 本文只冻结产品语义、子系统边界与接缝；接口签名、文件新增位置和目标图增量在 contract 节点冻结。用户批准前不进入 contract、plan 或实现。

## 0. 结论

原方案能做出一条 happy path，但不是最优解：它把 OpenCode 的**协议身份**等同于二进制文件名，把同一落点的 monitor 拆成两份互斥源码，并把进程回收、SSE 断流和审批唯一权威推到后续。这会在覆盖安装、同 HOME 双版本、agentd 重启和权限重试时产生静默错路由或孤儿进程。

本设计改为四条不可拆的主线：

1. **一份 monitor、两个宿主入口**：同一文件同时提供 V1 `server()` 与 V2 `id/setup`；不再按探测结果覆盖成另一份源码。
2. **一个语义解析器、两个显式执行器**：`opencode` 永远表示 V1 协议，`opencode2` 永远表示 V2 协议；实际可执行文件名只是候选，不是协议事实。
3. **能力驱动编排**：审批、提问、快照与恢复按能力/权威声明选择，不再扩散 `name == "opencode2"` 分支。
4. **MVP 也必须可回收且 fail-closed**：V2 不做冷恢复，但必须持久化足够的进程足迹、支持 Reap/扫孤儿，并在 SSE 断流时失败收束，不能假成功。

## 1. 问题与目标

本机 OpenCode 已是 `v2.0.12`。当前仓库同时存在三个耦合：

- monitor 在固定落点 `~/.config/opencode/plugins/handoff-monitor.ts`，源码顶层依赖 V1 的 `@opencode-ai/plugin`；干净 V2 环境无法解析该包。
- `internal/executor/opencode` 使用 V1 HTTP/SSE 契约；V2 的 `/api/*`、事件、权限和表单契约不同，不能共享协议解析器。
- toolchain 和 orchestration 多处把字符串 `opencode` 当成能力事实；当 `opencode` 文件本身已被 V2 覆盖时，会先宣称 V1 能力、再在运行期撞协议。

用户价值闭环是：

> 用户在 V1、V2 或两者并存的机器上，只安装一次 handoff monitor；明确选择 `opencode` 或 `opencode2` 后，系统以匹配的协议执行任务，权限/提问/继续/结束可用；版本、鉴权、事件流或 agentd 生命周期异常时，系统给出可行动失败且不遗留 serve 进程。

## 2. 范围与固定语义

### 2.1 本期范围

- 单文件、双宿主的 monitor 插件，覆盖 V1 `>= 1.18.29` 与 V2 `2.0.12` 基线。
- OpenCode 二进制的语义解析与 toolchain 状态表达。
- 新的 `opencode2` V2 adapter：dispatch、权限、表单提问、continue、done/stop 与进程回收。
- 编排层从执行器名称分支迁到能力/权威声明。
- `executor.default`、CLI 枚举、agentd 组装、`env.opencode2` 与文档。
- macOS、Linux、Windows 与既有 OpenCode executor 相同的平台范围；实现不能只靠 Unix shell wrapper 成立。

### 2.2 永久不做

- 不支持 V1 `< 1.18.29` 的双宿主 monitor；用户必须升级 V1。
- 不把显式选择的 `opencode` 静默解释成 V2，也不在 V1 adapter 内转发到 V2。
- 不在同一插件落点按版本自动交换两份源码。
- 不迁移 OpenCode/superpowers 的第三方配置或历史数据库。

### 2.3 本期不做、后续承接

- V2 冷恢复：agentd 重启后重连既有 serve/session、事件补偿与对账。
- `opencode2` 的 coordination、OneShot approver、Profile、Skills provider 能力。
- V2 细粒度 Spend/Timing/Usage 与 deny reason 同帧送达。

以上后续项必须同时进入仓库 `docs/roadmap.md`，不能只留在本文。

## 3. 子系统与所有权

| 子系统 | 本期所有权 | 禁止穿透 |
|---|---|---|
| `d_maintenance` | 安装/检查一份 monitor；实现 OpenCode 语义版本与凭证的具体探针 | skill 安装器不得自己发明另一套版本解析；不得读取 executor 私有状态 |
| `d_execution_contract` | 定义 adapter 所需的窄 runtime-probe 接口，以及能力、权限权威、事件、提问、进程足迹契约 | orchestration 不得靠执行器名猜能力；adapter 不得反向 import maintenance |
| `d_execution_adapters` | V1/V2 各自的 wire adapter；协议帧到统一事件的转换 | V1/V2 不互相调用协议客户端；adapter 不直接改任务状态 |
| `d_orchestration` | 唯一任务状态机、审批策略与提问工单；按声明选择送达路径 | 不解析 OpenCode 原生 JSON；不重复成为 V1 的审批权威 |
| `d_cli` | 枚举、配置组装和依赖注入 | 不复制探测/版本/鉴权规则 |

当前图中存在这些域，但新增“OpenCode 语义解析闸”和“权限权威声明”尚无符号，属于本 spec 的**图覆盖债**；contract 节点必须先命名并写入目标图，再冻结签名。

## 4. 设计决策

### 4.1 monitor：一份源码同时服务 V1/V2

固定落点和一份内容哈希保持不变：

```text
plugins/opencode-monitor/handoff-monitor.ts
  default object
    id/setup    -> V2 宿主读取
    server()    -> V1 宿主调用
```

关键约束：

- V1 `>= 1.18.29` 支持 object-form plugin；V2 读取 `id/setup` 并忽略 V1 `server()`。这是官方迁移路径，不需要两份源码。参考 [V2 plugin 文档](https://opencode.ai/v2/docs/build/plugins) 与 [V1 迁移文档](https://opencode.ai/v2/docs/build/plugins/migrate-v1)。
- 文件顶层**不得运行时 import** `@opencode-ai/plugin` 或 `@opencode/plugin`。本机干净 V2 `2.0.12` 无法解析前者；V1 专属 helper 只能在 `server()` 内惰性 import。V2 用宿主 context 和结构化 JSON schema 注册工具。
- `monitor`、`monitor_kill`、`monitor_list` 的进程/队列核心只有一份；V1/V2 只分开“向宿主注册工具”和“叫醒宿主”的薄入口。
- 安装器仍只管理现有落点和一份 hash。V1、V2 并存时不切换内容，`PluginStatus` 也不因当前 PATH 顺序抖动。
- 如果可确认存在 V1 `< 1.18.29`，安装器必须拒绝覆盖已有插件并给出升级动作；版本未知、探针超时或解析失败时同样 skip，不得以“保守”名义写 V1 内容。
- 空内容、无配置目录等现有 skip 语义不变；新增 skip/incompatible 必须可行动，不能误报 `in_sync`。

### 4.2 二进制解析：文件名不是协议身份

建立一个共享语义、依赖倒置的 OpenCode 解析闸：`d_execution_contract` 定义 adapter
真正需要的窄 runtime-probe 接口与结果语义，`d_maintenance` 实现具体二进制/鉴权探针，
`d_cli` 组装时把同一实现注入 toolchain、skill 安装与 V1/V2 adapter。这样所有入口得到
同一事实，又不会新开 execution → maintenance 的反向依赖。它返回实际路径、语义 major、
候选来源和可行动诊断；contract 节点决定精确类型和签名。

| 用户身份 | 候选与接受条件 | 结果语义 |
|---|---|---|
| `opencode` | 只接受 `opencode` 且解析到 major `1` | V1 Ready；major `2` 是 Incompatible，绝不静默改路由 |
| `opencode2` | 优先 `opencode2` major `2`；没有时接受 `opencode` major `2` | V2 Ready；实际路径可以仍叫 `opencode` |

版本解析器接受 `opencode v2.0.12`、`v2.0.12`、`2.0.12` 等合法前缀，解析语义 major，禁止用字符串 `contains("v2")`。状态必须区分：

- **Missing**：候选文件不存在。
- **Incompatible**：文件存在但 major 不符合所选协议。
- **ProbeFailed/AuthUnknown**：版本或鉴权探针超时、执行失败、输出不可解析。
- **NoCreds**：版本匹配，但原生鉴权查询明确为空。
- **Ready**：版本匹配且原生鉴权查询明确存在连接。

V2 鉴权判据使用有时限、无模型调用的 `auth list --standalone --format json`；不能以 `auth.json` 是否存在作为真相。`auth.json` 在 V2 是兼容迁移来源，不等于当前 DB 已有可用连接。有效 HOME/env 必须与真正启动任务时一致。

V1/V2 `Start` 都必须在拉起任何 HTTP serve 前调用注入的同一解析闸。禁止进程级
`sync.Once`：每次 Start 都重验，或缓存键至少包含解析后的绝对路径与文件身份/mtime，
保证二进制原位替换不会绕过协议门。

toolchain 顺序为 `opencode, opencode2, claude, grok, codex, agy`，但 `FirstReady` 只能选择明确 Ready 的项；Incompatible/ProbeFailed 不能伪装成 Missing，也不能参与自动选择。

### 4.3 能力矩阵：`opencode2` 首版只执行

| 能力 | `opencode` V1 | `opencode2` V2 本期 |
|---|---:|---:|
| Execution | 是 | 是 |
| Coordination | 是 | **否** |
| OneShot approver | 是 | **否** |
| Profile | 是 | **否** |
| Skills provider | 是；作为 V1/V2 共用的唯一 OpenCode 安装位 | **否** |

因此：

- capability report 只描述逻辑 harness 支持什么，保持无 I/O；它**不是**本机就绪报告。
  V1 的 Execution/Coordination/OneShot 等协议入口在产生副作用前分别经过 major 1 runtime
  闸，V2 Execution 经过 major 2 闸。共用的 OpenCode Skills 安装位则用解析结果实施
  §4.1 的兼容保护，不要求机器必须是 V1。
- B397 不增加 `opencode2` coordinator，也不把它加入 approver fallback。现有 approver 候选仍是显式有序列表；配置了不具 OneShot 的 `opencode2` 时必须拒绝或保持未绑定，不能回落到 `opencode`。
- monitor 由现有 OpenCode 安装位管理一次，不能为了 `opencode2` 再注册一份 Skills/Profile provider。
- discipline 已通过 `StartReq.Discipline` 到达 adapter；V2 仍经 `turn.RenderPrompt` 注入，不增加执行器专属的 B129 映射。
- `env.opencode2` 为显式主键，未配置时只在一个组装点回落 `env.opencode`；日志记录最终来源。回落适用于 dispatch 和进程生命周期需要的相同有效环境，但不制造第二份配置事实。

### 4.4 V2 adapter：先订阅，再发 prompt

V2 adapter 使用 V2 HTTP/SSE，不复用 V1 客户端或帧解析器。启动顺序冻结为：

1. 解析闸确认 major 2 与鉴权 Ready。
2. 通过既有 prochost 拉起 `serve`，随机 loopback 端口、随机密码；Basic username 固定为实测的 `opencode`，密码只进入任务环境/受控凭据，不进日志。
3. 调 `/api/info` 等待就绪，并再次确认服务版本 major 2。
4. 创建 session，记录 session/location 与进程足迹。
5. 连接 `/api/event`，收到 `server.connected` 后才继续。
6. 注册 run/process handle。
7. 用 `turn.RenderPrompt` 合并 Discipline，再提交 prompt。

V2 event stream 是 live-only，没有可依赖的 replay 或自动重连；因此“先发 prompt、后订阅”会形成不可接受的丢事件窗口。参考 [V2 client 文档](https://opencode.ai/v2/docs/build/client)。

事件按 session/location 过滤并映射：

| V2 事件 | handoff 事件/动作 |
|---|---|
| `permission.asked` | `AdapterEventPermission`；原生 id 作为稳定 `PermissionID` |
| 任务 session 的 `form.created` | `AdapterEventQuestion`；form id 作为稳定 `QuestionID` |
| text/tool/step | 有界 `progress`；不得把整段日志或秘密透传 |
| `session.execution.succeeded` | `result OK` |
| `session.execution.failed` / `interrupted` | `result Fail`，保留有界、脱敏诊断 |

`PermRequest` 只有在 action/resources 能**无损**映射到 Tool/Command/Paths 时才填；否则置 nil，强制走人工裁决，禁止猜命令或路径后获得更宽权限。

### 4.5 权限唯一权威与提问送达

权限权威必须成为 adapter 的声明能力，而不是 orchestration 的名称判断：

- V1 保持现状：adapter/ApprovalClient 拥有原生权限闭环；manager 只观察，不再重复建票。
- V2：manager 是唯一政策与审批权威；adapter 只把 native request 转成统一事件，并把一次决策回写为 native `once` 或 `reject`。adapter 不再同时调用 ApprovalClient。
- native 明确 deny 可以直接 deny；需要 handoff 管理的动作在 V2 原生层必须配置为 ask，避免原生 allow 绕过当前 manager policy。
- 重复 SSE 帧、工单重放和 reply 重试下，同一 native request 只能形成一张票、一个最终决策、一次有效 native reply。

提问按 `executor.AskResponder` 能力路由，不再检查 `name == "opencode"`：

- 有稳定 `QuestionID` 的事件必须交给 AskResponder；若能力消失则送达失败，不能降级为 `Send` 伪装成功。
- 单字段自由文本表单可以接受普通字符串。
- 多字段或选择表单必须要求确定性的 JSON object，key 使用原生 field/question id；事件文本明确给出 schema。adapter 在 native reply 前校验，非法答案返回可行动 `delivery_failed`，不猜转换。
- 非任务 session 的 global/integration form 忽略，但记录有界 debug 事实。

### 4.6 continue、断流、重启与进程回收

- Continue 只允许在同一 session 空闲时提交下一轮；正在执行时拒绝并给出状态。
- Done/Stop 必须关闭 SSE、停止 serve、清理凭据/足迹；失败也必须进入相同 cleanup。
- SSE 非预期断开时不自动重连后继续宣称完整：发出一次失败、停止/reap serve，由 manager 收束到 `waiting_review` 或既有失败语义。
- 未知终态或未知关键帧 fail-closed，并附有界、脱敏的 serve log tail；空 body/非 JSON 错误也必须可诊断。

冷恢复虽不在本期，但**资源安全在本期**：V2 adapter 必须提供既有编排可消费的 Reap 与 ProcHandle/footprint（或 contract 节点裁出的等价能力），不能只实现五个基本动作。

agentd 重启后的固定行为：

| 重启前状态 | 本期恢复结果 |
|---|---|
| `running` / `waiting_answer` | 因无 V2 Recoverer，既有 startup recovery 记录 `turn_failed` 并转 `waiting_review`；sweep 根据足迹回收 serve |
| `waiting_review` | 状态保留；sweep 仍回收 serve |
| 对上述任务再 Continue | 返回“OpenCode V2 冷续接尚不支持，请重新 dispatch”的可行动错误，不返回泛化 session missing |

### 4.7 弃选方案

| 方案 | 不选原因 |
|---|---|
| V1/V2 两份插件源码，按 PATH 覆盖同一文件 | 双版本同 HOME 时没有稳定选择；状态 hash 会随 PATH 抖动；隔离探针已证明一个 object 可双宿主 |
| 只认名为 `opencode2` 的 V2 文件 | 当前安装里它只是指向 `opencode` 的 wrapper，官方迁移也允许同名命令；会漏掉覆盖安装 |
| 在现有 `name == "opencode"` 分支旁逐处加 V2 | 审批、提问、快照、恢复的选择继续散落，下一版本还会复制；不能证明唯一权威 |
| 首版只做 Adapter 五动作，不做 footprint/Reap | agentd 重启或 SSE 断流就会留下 serve；这不是功能降级，而是资源泄漏 |
| 首版同时做 V2 冷恢复 | live-only event 没有 replay 保证，直接重连无法证明期间未丢权限/终态；应另立对账契约后再做 |

## 5. 契约语义与接缝

本节冻结“谁承诺什么”，不冻结 Go 签名。

1. **OpenCode 语义解析契约**：execution contract 定义使用方接口，maintenance 提供唯一
   具体版本/鉴权实现，CLI 只负责注入；toolchain Detect、skill install 与两个 adapter Start
   共享结果语义。任何 Start 路径都不能绕过，也不能从 adapter 反向 import maintenance。
2. **Executor capability report**：registry/provider 对 execution、coordination、oneshot、profile、
   skills 逐项静态报告，且明确“不等于 runtime ready”；协议能力在实际入口另过注入的
   runtime 闸。V2 按 §4.3 报告，共用 Skills 只由现有 OpenCode provider 暴露。
3. **Permission authority declaration**：adapter 声明 native 权限由 adapter 还是 manager 负责；manager 据此决定建票/忽略，任何 request 只有一个权威。
4. **Question responder contract**：`QuestionID` 存在即承诺原生回答；AskResponder 校验 answer 并返回送达事实。
5. **Process lifecycle contract**：Start 成功前必须有可扫的足迹；Stop、Done、失败、断流、agentd 重启都能幂等 Reap。Recoverer 与 Reaper 是两种能力，不能因为本期不 Recover 就不 Reap。
6. **Plugin install contract**：一个落点、一份 hash、双宿主；版本不兼容/不可知时不覆盖用户当前文件。

### 5.1 测试缝清单（symbol + caller）

| 接缝 | 现有锚点 / 调用者 | 本期要锁的行为 |
|---|---|---|
| 插件安装/状态 | `internal/skill/plugin.go#InstallPlugin`、`#PluginStatus`；`cmd/skill.go` | 同一文件/hash；旧 V1/未知版本不覆盖 |
| OpenCode 解析闸（新） | contract 节点命名使用方接口；maintenance 实现；CLI 注入；调用者为 toolchain Detect、skill install、V1/V2 Start | 无反向域依赖；别名、major、超时、原位替换、鉴权状态一致 |
| 能力报告 | `internal/executor/capability.go#Provider` / Registry；`cmd/agentd.go` 组装 | V1/V2 静态能力矩阵与 runtime readiness 分离；禁止名字猜测 |
| 统一事件与权限权威 | `internal/executor/executor.go#AdapterEvent`；`internal/orchestration/manager.go#handleEvent` | 去重；V1/V2 各只有一个审批权威 |
| 原生提问 | `internal/executor/ask.go#AskResponder`；manager 的 wait-question 送达路径 | 单字段、多字段 JSON、非法答案、能力缺失 |
| 进程生命周期 | `internal/executor/recoverer.go#Recoverer`；manager ResumeTask、`reconcile.go` stop/sweep 的 reaper/footprinter | 无 Recoverer 仍可 Reap；三种重启状态无孤儿 |
| V2 wire parser（新） | V2 adapter 事件入口；外部 `/api/event`、permission/form reply | 表驱动帧、未知帧、断流、空错误体、脱敏日志 |

## 6. 验收

### 6.1 功能与兼容矩阵

1. **插件**：V1 `1.18.29`（或最终声称支持的最老版本）与 V2 `2.0.12` 都从同一 HOME 加载同一文件；三工具工作并能清理。两版本并存仍只有一个内容 hash 且 `PluginStatus == in_sync`。
2. **旧版保护**：V1 `<1.18.29`、版本超时、不可解析时，install/status 不覆盖现有文件并给出升级/重试动作。
3. **解析表**：
   - 只有 V1 `opencode`：`opencode` Ready，`opencode2` Missing/Incompatible。
   - 只有 V2 `opencode`：`opencode` Incompatible，`opencode2` Ready 且实际路径为 `opencode`。
   - V1 `opencode` + V2 `opencode2`：两者各自 Ready。
   - wrong-major、不可解析、超时均不得 Start；第一次任务后原位替换二进制，第二次必须重新判定。
4. **V1 fail-closed**：major 2 下 V1 Start 在任何 HTTP 请求前拒绝。
5. **V2 真机闭环**：dispatch、approve、deny、单字段 question、多字段 JSON question、continue、done 各跑一次；不以 mock 代替。
6. **配置**：只配 `env.opencode` 时 V2 获得回落；同时配置 `env.opencode2` 时后者覆盖。approver 配 `opencode2` 因无 OneShot 被明确拒绝/不绑定，绝不回落成 V1。
7. **鉴权**：V2 原生列表的有连接、空、超时、坏 JSON 分别得到 Ready、NoCreds、AuthUnknown/ProbeFailed；探针不得发模型请求。

### 6.2 对抗与生命周期

1. 重放同一 permission SSE、重试工单 reply、交错 approve/deny：恰好一张票、一个最终决策、一次有效 native reply。
2. prompt 提交前注入事件，证明“订阅成功后发 prompt”没有丢首帧窗口；交换顺序的变异测试必须变红。
3. SSE 中途断开、未知 terminal、404 空 body：任务不得 completed；必须有可行动失败且 serve 被回收。
4. agentd 分别在 `running`、`waiting_answer`、`waiting_review` 重启：状态符合 §4.6，进程表和 OS 均无孤儿；Continue 报本期不支持的具体动作。
5. Stop/Done/失败并发与重复调用：Reap 幂等，端口、进程、临时凭据均释放。
6. Windows 验收至少覆盖路径解析、无 shell wrapper 的进程启动/停止、鉴权 HOME、重启扫孤儿；没有 Windows 真机证据时不能声称全平台完成。
7. 能力闸变异：删掉 major 检查、权限权威声明、Reap、session filter 任一项，相应用例必须变红，防止“真空绿”。

### 6.3 回归与完成证据

- 现有 V1 protocol-bound execution/coordination/OneShot/Profile 在**真实 major 1** 上回归；
  共用 Skills 在 V1/V2 上分别回归。major 2 文件不得进入任何 V1 wire 入口。
- focused tests、相关 package tests、串行 `go test -p 1 ./...` 全绿；仓库已知并发假红不能拿来省略串行全量。
- README、README.zh-CN、handoff skill 的 executor 表与降级清单一致。
- 真机证据必须记录版本、实际解析路径、平台、事件/进程终态；测试输出与行为事实分开落账。

## 7. 风险与缓解

| 风险 | 缓解 |
|---|---|
| V2 beta API 继续变化 | 版本与 `/api/info` 双闸；wire parser fail-closed；未知关键帧不假成功 |
| 一个文件兼容两个宿主变复杂 | 共享业务核心、两个薄宿主入口；锁最老 V1 与 V2 双真机 |
| live-only SSE 丢事件 | 先连接并确认 `server.connected`，再发 prompt；断线不伪重连 |
| 权限在 native/manager 重复裁决 | 显式 authority contract + native id 去重 + 交错重试验收 |
| 不做冷恢复却留下孤儿 | 把 Reap/footprint 留在 MVP，Recoverer 单独列后续 |
| V2 `opencode` 覆盖 V1 文件名 | 语义解析器；显式身份不随文件名漂移；每次 Start 重验 |

## 8. 批准后的节点顺序

1. `charter:contract`：冻结新接缝的精确签名、目标图与可编译骨架。
2. `charter:breakdown`：按 maintenance/plugin、execution adapter、orchestration authority/lifecycle、CLI/docs 四个子系统拆 DAG。
3. 各子系统独立 `plan → implement → review`；权限权威和生命周期先于真机 happy path。
4. `integrate → recon → acceptance → finish`；没有 V1/V2/Windows 与重启证据，不进入 finish。
