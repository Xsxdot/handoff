# Breakdown：B293 隔离 HOME 生命周期 + 载体四态（L3 轻档）

> 状态：**已拍板（2026-08-29）**。出稿：本地 grok `7fc06468`（ACP 中断未提交）；协调者回收未跟踪稿并拍板。
> 拍板结论：第 52 / 56 条缺冻结签名，**退回 contract 补冻**；补完后 DAG 仍是 U1–U5，轻档不 card split。
> 上游：spec `docs/superpowers/specs/2026-08-29-b293-isolated-home-carrier-status-design.md`
> （头部「已批准（用户 2026-08-29：非空无凭据选方案 1；检测按钮；四态含不可达与限额中；原型形态「可以」）」，开工核对一致）；
> 契约 `docs/superpowers/specs/b293-contract.md`（头部「随本提交冻结」，冻结提交 `3921d60e`，开工核对一致；
> 拆解澄清已回写契约 §12；第 52/56 条签名尚未补冻，不得当已冻条目用）。
> 档位：spec/契约冻死**轻档**——实现归一轮，下列单元由单个 implement 执行者按 DAG 序贯消化，
> **不** `card split`、不做跨执行器扇出。
> 形态权威：`prototypes/b293-carrier-home/pages/settings.html`。
> 台账：`docs/superpowers/specs/b293-ledger.md`。
> Ticket 0 空壳已落；`PutCarrier` 翻真与 `admitInto` 按 `Healthy` 留给实现票。

**契约缺口（不是偏好）**：冻结清单第 56 条（main_home_sync 供给拷贝）与第 52 条的小队派发半边（HomeDir 送到执行机）
在冻结签名里没有落点。按纪律「发现需要新接缝 → 退回 contract，不许边拆边加」。
子卡仍按「补齐后的形状」写出，避免拍板后再拆一轮；**在合同补冻之前不得开工 U2 的供给半边与 U5**。

---

## 待拍板岔口（集中清单）——已拍板（2026-08-29）

**岔口一：main_home_sync 供给动作补哪条冻结面**（两案都退回 contract，禁止拆解私加）

- 方案 A：给已冻的 `WakeRequest` / `HomeWakeReq` 增加 `Credential` 字段；`WakeHome` 在隔离路径为 empty 且 `credential=main_home_sync` 时，先拷 §4 表内凭据文件再唤起。不新开 HTTP 路径，跨机仍走已冻的 `POST /api/host/wake?machine=`。
- 方案 B：新导出 `Host.SyncMainHomeCreds` + `POST /api/host/sync-creds?machine=`，与 probe（只读）/ wake（唤起）三分。detect 编排：拷贝（若需要）→ wake → `ApplyDetect`。
- 取舍实质：少一条 HTTP 面、把供给收进唤起 vs 读写分离更干净。方案 C（gateway 本机 `os` 拷贝、跨机无面）跨机不可行，不列为候选。
- 硬约束两案共用：`kind=occupied` 不得覆盖（条 57）；claude 无文件可拷 = 空操作；probe 路径仍只读。
- **裁决：方案 A。** 理由：检测已是「拷贝（若需要）+ 唤起」一次用户动作；probe 只读不能接供给；新开 sync-creds 多一条跨机面而无新用户旅程。退回 contract 把 `Credential` 补进 `WakeRequest` / `HomeWakeReq`（空 = standalone）。

**岔口二：凭据表如何进 `ProbeHome` 而不另造表**

- 方案 A：导出 `CredRelPathFor`；组装点 `SetupAutomation` 把函数注入 `hostapi.Host`（构造参数或测试缝包级 var）。不新开 `target.json` 的 `d_execution → d_maintenance` 边。
- 方案 B：退回 contract，加 `d_execution → d_maintenance` 边，`hostapi` 直接 import `toolchain`。
- 取舍实质：守现有 target vs 包内直调少一层。禁止方案 C（hostapi 自写一份路径表）——契约明文禁止。
- **裁决：方案 A。** 理由：凭据表权威已冻在 toolchain；加 `d_execution → d_maintenance` 边是为了一个函数扩大契约图。组装点注入不新开方向。不退回 contract。

**岔口三：`internal/agentd` 已越过实例化清单尺寸红线，要不要先插竖切**

实测（本工作树亲自数）：顶层非测试源文件 **70**、行 **23147**（递归 74 / 23167）。命中「单包 ≥40 文件无子包」与项目红线 20,000 行。架构法第三条第三款字面是「可派发上下文单元超限 → **拒发功能卡**」。

- 方案 A：先插竖切还债卡（至少把编制 HTTP 面 `schedapi.go` / `hostprobe.go` 收进可圈边界），B293 功能单元等还债后派。
- 方案 B：本卡上下文只圈下列有界文件（U3/U5 入口指针），整包欠账仍记实例化清单 §6，不插竖切。先例：B239 在 61 文件命中时只要求显式回答。**注意：B239 当时未过 20,000 行。**
- 取舍实质：法条字面停发 vs 本卡文件集仍圈得出。
- **裁决：方案 B。** 理由：第三条第三款的「可派发上下文单元」是本卡圈出的文件集，不是 `internal/agentd` 整包。U3/U5 入口指针已圈 `hostprobe.go` / `schedapi.go` / `cardstep.go` / `dispatchRequest` 四处，不是 2.3 万行。整包欠账记实例化清单，不挡 B293。禁止把整包派给执行者。

**岔口四：执行者任务如何叠隔离 HOME 与任务级沙箱**

契约条 51 已冻 `hostapi.buildEnv` 的 HOME 覆写（协调者拉起）。条 54 允许 grok 任务级 `grokhome`。条 55 禁止凭据指回机器主 HOME（除非本轮 `main_home_sync` 供给）。

- 方案 A：小队派发把进程 `HOME` 设成载体 `HomeDir`；grok 任务级 `GROK_HOME` 仍指向 `<taskDir>/grokhome`，`EnsureAuthLink` 的权威副本改为隔离 HOME 下 `.grok/auth.json`。
- 方案 B：不改进程 `HOME`（避免 `os.UserHomeDir()` 漂到隔离目录、grok provider 配置读空）；只设 CLI 专用键（`CODEX_HOME` / 权威 auth 路径参数），任务级 grokhome 照旧。
- 取舍实质：一个 HOME 语义 vs 少动 `UserHomeDir` 依赖面。方案 C（取消 grokhome）违反条 54，不列为候选。
- 依赖：U5 在合同补冻 `POST /api/tasks` 的 `home_dir` 之前不能开工（见契约核对条 52）。
- **裁决：方案 A。** 理由：条 51 已冻协调者拉起覆写进程 `HOME`；小队派发必须同一语义，否则「检测看隔离 HOME、任务用主 HOME」。grokhome 按条 54 保留；`EnsureAuthLink` 权威改为隔离 HOME 下 `.grok/auth.json`。具体 env 键仍按契约 §9 由实现票选，但不得让进程 HOME 漂回机器主目录。

**岔口五：存量载体 `status` 空（omitempty 缺席）怎么进准入**

Ticket 0 现网行：`Healthy=true`，`status` 空。实现票改 `admitInto` 只认 `online` 后，这些行会被跳过，协调者小队可能当场 `ErrNoHealthy`。

- 方案 A：实现票一次迁移——读到 `status==""` 且旧 `Healthy=true` 的行写成 `online`（或写成 `pending` 并提示去点检测）。
- 方案 B：`admitInto` 把空 status 暂当 `online`（兼容窗），新行必须有 status。
- 方案 C：空 status 当 `pending`，要求用户对每条载体点一次检测。可能饿死现网拉起。
- 取舍实质：静默放行旧载体 vs 强迫重新检测。
- **裁决：空 status 当 pending，不扫库、不写成 online。** 理由：只放行已上线是本卡要杀掉的根；把旧 `Healthy=true` 迁成 online 等于保留翻真。`admitInto` 把 `status==""` 与 pending 同等跳过。GET 空 status 仍 omitempty 缺席；控制台把缺席画成未上线。不在启动时改写 registry（避免新写路径）；不违反条 12（home_dir 未变不改 status）。升级后现网载体需点一次检测才能再进派发——真机清单第 12 条。不退回 contract。

**岔口六：小队派发 `home_dir` 补冻的字段形态**（退回时的精确文本，不是「要不要传」——不传则跨机不可行）

- 方案 A：`POST /api/tasks` 请求体增 `home_dir`（空/缺席 = 非小队派发，执行机不覆写任务 HOME）。omitempty：空串缺席。穿过 `DispatchOpts` → `client.Dispatch` 手搭 map → `dispatchRequest` → `DispatchReq`，用可空区分「字段缺失」与「值为空」。
- 方案 B：同字段但放在已有 `env` 通道（今日请求体无通用 env 列表）——等于先造 env 通道再塞 HOME，面比 A 大。
- 取舍实质：最小字段 vs 通用 env。反查协调机编制面（执行机无账本）不列为候选。
- **裁决：方案 A，退回 contract。** 理由：执行机 `Manager.Dispatch` 无编制账本，跨机不能反查载体。通用 env 通道面比一个可空 `home_dir` 大。空/缺席 = 非小队派发，不得覆写执行机 HOME。必须穿过手搭 map，用可空区分缺席 vs `""` vs 非空。

---

## 一、触及子系统清单

子系统 id 与类型以 `codegraph/best.json` 为准（`parent` 为空的顶层领域）。本刀触及 5 个顶层域，另有 2 个附属触点（不单独派卡）：

| # | 子系统 | best.json id | 图类型 | 本卡类型标注 | 说明 |
|---|--------|---|---|---|---|
| 1 | 编制调度 | `d_scheduling` | logic | **逻辑型** | 状态机与准入规则所有者。接缝对面是自有 registry + 测试闭环。 |
| 2 | 任务执行 | `d_execution` | boundary | **边界型** | 子域 `d_execution_host`（`ProbeHome`/`WakeHome`，对面是本机 FS 与 CLI 进程）与 `d_execution_adapters`（codex/grok 任务环境，对面是真实 executor）。机内验契约形状，行为走真机清单。 |
| 3 | 控制门面 | `d_gateway` | boundary | **边界型**（HTTP 形状机内可闭环） | `k_agentd_Server`。detect 编排、carrierView 投影、`?machine=` 转发。跨机转发与剪贴板之外的 handler 测试可闭环。 |
| 4 | 协议契约 | `d_protocol` | logic | **逻辑型** | `CarrierView` 删 `healthy`、fixture / TS 孪生。 |
| 5 | Web 控制台 | `d_web` | logic | **逻辑型** | `d_web_admin` 设置页 + `d_web_contract` scheduling.ts。剪贴板/WKWebView 走真机。 |

**附属触点（不派卡）**：

- `d_maintenance`（`k_toolchain_*`）：导出 `credRelPathFor` 供 U2 复用，一个函数。
- `d_ledger`（`ledgerstep.DispatchOpts`）与 `d_transport`（`client.Dispatch` 手搭 map）：仅当岔口六补冻后由 U5 改透传字段；今日零功能改动。
- `d_keystone` / `d_cli` / `d_orchestration`（`Admit` 调用方）：预计零改。`LaunchAdmit`/`Admit` 已走 `admitInto`；`cmd/squad.go` 不展示 healthy，仅测试夹具可能随 wire 刷新。

**派卡资格四条逐核**（轻档不并行扇出，但每张单元仍按四条核）：

| 单元 | ①有界文件集 | ②契约面可枚举 | ③DAG | ④类型 | 结论 |
|---|---|---|---|---|---|
| U1 | `internal/scheduling/*.go` 一包 | §5 条 1–21、38–42 | 无上游 | 逻辑型 | 通过 |
| U2 | `internal/hostapi/probe.go` + 实现/测试；toolchain 导出一函数 | §5 条 22–37、56–57 | 可与 U1 并行；供给半边等合同 | 边界型 | 通过（供给半边 blocked） |
| U3 | `schedapi.go` / `hostprobe.go` / `proto/scheduling.go` + fixture | §3.3 HTTP 表、条 33–48 | U1+U2 | 边界型 | 通过；岔口三若选 A 则等竖切 |
| U4 | `SchedulingPage.tsx` + `.test.tsx` + `scheduling.ts` | 条 44–47、49–50、假缝默认路径 | U3 | 逻辑型 | 通过 |
| U5 | 见该卡入口指针（跨 dispatch 链 + 两 adapter） | 条 51–55 | 合同补冻 home_dir + U1 | 边界型 | 圈得出；**合同未补前拒开** |

**架构法第三条**（必须显式回答）：

- `internal/agentd`：70 文件 / 23147 行，两条尺寸判据都命中。本卡能圈出 `hostprobe.go`、`schedapi.go`、`cardstep.go`、`server.go` 的 `dispatchRequest`/`handleDispatch` 四处，**不是**把 2.3 万行整包派给执行者。是否因此拒发功能卡见岔口三。
- `internal/scheduling`、`internal/hostapi`：远低于红线。
- `internal/executor/codex` 14 源、`grok` 13 源：不命中前缀家族 ≥5 的「同一层目录同一前缀」形态（文件是按职责命名的适配器内部，不是 `carrier_*.go` 家族）。U5 只圈 taskenv/authsync/proc 与测试。
- 不插「硬塞进功能卡」的无界改动。若岔口三选 A，先派竖切卡。

---

## 二、契约增量核对

对照冻结物逐条。**零私加接缝**；两条缺口标「退回」，其余不越界。

| 冻结条目 | 本拆解对应 | 越界？ |
|---|---|---|
| §2 不新开子系统、不新开 target 方向 | 无新顶层域；hostapi 不 import toolchain（岔口二） | 否 |
| §3.1 编制域签名 | U1 填 `ApplyDetect` 肉、改 `PutCarrier`/`admitInto`；不改 Label/DefaultHomeDir/RunCommand 金样本 | 否 |
| §3.2 ProbeHome/WakeHome | U2 填肉；Timeout=0 → 30s；禁 RunTurn、禁交互登录 | 否；Credential 缺口见下 |
| §3.3 HTTP 表 | U3 填 detect 编排；probe/wake 已直通；run-command 已穿过 Handler | 否 |
| §3.4 TS/Go 默认串与中文名 | Ticket 0 已锁；U4 只消费 | 否 |
| §4 凭据表 | U2 必须调用同一 `credRelPathFor` | 否 |
| §5 条 1–8 词表与投影 | U1+U3：删 `Healthy` 后 GET 仍带 status/last_error，空 omitempty | 否 |
| §5 条 9–14 PutCarrier | U1 | 否 |
| §5 条 15–21 准入 | U1；哨兵名 `ErrNoHealthy` 沿用 | 否 |
| §5 条 22–32 探测 | U2+U3 | 否 |
| §5 条 33–44 检测 | U2 唤起 + U1 写状态 + U3 编排 + U4 自动第二次 POST | 否 |
| §5 条 45–50 运行命令与默认路径 | Ticket 0 已锁；U4 复制不拼接 | 否 |
| §5 条 51 协调者 HOME | 已有测试；本拆解不重做 | 否 |
| **§5 条 52 小队派发消费 HomeDir** | 传输链无字段 | **退回**（见契约 §12.4） |
| §5 条 53–55 adapter 执法 | U5，依赖 52 的传输 | 否（执法本身不新开面） |
| **§5 条 56 供给拷贝** | 无签名 | **退回**（见契约 §12.3） |
| §5 条 57 occupied 永不覆盖 | U2 反面断言 | 否 |
| §6 Ticket 0 空壳 | 实现票填肉，不把翻真提前 | 否 |
| §7 四项拍板 | 沿用，不推翻 | 否 |
| §9 移交区 | `.DS_Store` 默认不忽略；Wake argv 归 U2；USERPROFILE 叠层归 U5 实现 | 否 |

上游状态位：spec「已批准」✓；契约「已冻结 / 3921d60e」✓。引用状态位失真的上游视同未核对——本轮文件头部已读。

**必须退回 contract 的提案文本**（已拍板；由下一轮 contract 落进 §5，不在拆解里当已冻）：

- 条 56 补签名：岔口一方案 A（`WakeRequest` / `HomeWakeReq` 增 `Credential`）。
- 条 52 补签名：岔口六方案 A（`POST /api/tasks` 可空 `home_dir`）。

**不退回的澄清**已写进契约 §12.1 / §12.2 / §12.6。

---

## 三、子卡清单 + 依赖 DAG

轻档序贯（一个 implement 执行者）。竖切卡若拍板插入，挡在 U3/U5 之前。

```
U1 编制状态机 ──────────────────┐
U2 本机探测/唤起 ──（供给半边等合同）─┤
                                 ▼
                            U3 检测编排 + wire
                                 ▼
                            U4 设置页
U5 执行者消费 HOME ── 等合同补 home_dir + U1 准入改完
```

U1 ∥ U2 无相互 import。U3 消费 U1 的 `ApplyDetect` 与 U2 的 Wake 结局。U4 只打已冻 HTTP。U5 不依赖设置页。

### 行为闭环（spec 用户故事 → 五格）

| 触发者 | 权威事实/载体 | 消费者 | 可观察结果 | 归属 |
|---|---|---|---|---|
| 登记弹窗输入载体名 | `DefaultHomeDir` / TS `defaultHomeDir` | 设置页 HOME 框 | 串精确为 `~/.handoff/home/<trim(名)>`；用户改过路径后不再覆盖 | U4 |
| 改 HOME 路径 | `POST /api/host/probe` | 设置页提示 | empty / logged_in / occupied 三类可区分，且目录未被修改 | U2+U3+U4 |
| 新建或 `home_dir` 变更的保存 | PUT（不调 detect）+ 控制台紧接着 POST detect | 载体卡药丸 | 先 `pending`；检测成功 → `online`，否则停 `pending` | U1+U3+U4 |
| 保存 occupied 路径 | PUT 不因 occupied 拒绝；探测/唤起不删文件 | 药丸 + 磁盘 | 能登记为未上线；目录条目仍在 | U1+U2+U4 |
| 点「运行」 | `GET .../run-command` | 剪贴板 | 服务端那一条 `HOME=<已存串> <cli>`，客户端不拼接 | U3+U4 |
| 点「检测」（含自动那一次） | wake 结局 → `ApplyDetect` | GET 投影 | 按四态表迁移；`last_error` 给人看、不参与准入 | U1+U2+U3+U4 |
| 小队点火 / 协调者拉起 | `admitInto` | 派发/拉起 | 只尝试 `online` 且有空位；全非 online → `ErrNoHealthy`；有 online 但满 → `ErrNoSlot` | U1 |
| 该载体此后的执行者任务 | 载体 `HomeDir` 经派发请求到达执行机 | adapter 启动态 | 凭据不以机器主 HOME 为权威（除非本轮供给） | U5 |

触发或结果说不清的没有。缺传输字段的闭环（最后一行）标在 U5，合同补齐前不得扇出实现。

---

### U1：编制状态机（PutCarrier / admitInto / ApplyDetect）

- **①契约引用**：§5 条 1–21、38–42；§3.1 `ApplyDetect`；废止 `if !c.Healthy { c.Healthy = true }`。岔口五适用。
- **②意图与为什么**：状态的唯一写点在编制域。Ticket 0 仍翻 Healthy、仍按位准入——这是「未探测写成已上线」的根，不先改这里，后面所有 UI/检测都是假绿。
- **③验收**（逻辑型，机内闭环）：
  - `go test ./internal/scheduling/ -count=1` 绿，且含：`expect=0` 后 `status==pending`；改 `home_dir` → pending 并清空 `last_error`；`home_dir` 未变则 status/last_error 原样；`PutCarrier` 不得把零值写成 `online`。
  - `admitInto`：`online` 且有空位才选中；`pending`/`quota`/`unreachable` 被跳过；全非 online → `ErrNoHealthy`；有 online 但满 → `ErrNoSlot`。反面：把 `online` 改成 `pending` 的夹具必须让「能准入」那支翻红。
  - `ApplyDetect` 表驱动四态（含「上过线」= 检测前为 online/quota/unreachable）。标志互斥按契约 §12.1。不再返回 `ErrDetectUnwired`。
  - 删除 `Carrier.Healthy` 后，`TestRowsCarryVersionsForCASLock` 不得再断言 `"healthy":true`——改锁 `status` 的缺失/零值分辨（空 status omitempty 缺席 vs `pending` 在场）。
  - 生命周期：CAS 冲突走既有 409；进程重启后 registry 仍是权威，无孤儿写。无，因为本域不拉进程。
  - 静默失败：ApplyDetect 失败必须把 `detail` 写入 `last_error` 且不谎报 `online`。
  - 门禁：登记请求仍不含 status（`CarrierInput`）；本卡不新开写入口。
  - 承重安全属性：准入「非 online 不得选中」必须有能变红的测试（上条反面即此）。
- **④入口指针**：`internal/scheduling/scheduling.go#Service.PutCarrier`、`#admitInto`、`internal/scheduling/status.go#Service.ApplyDetect`、`#Carrier`。
- **有界文件集**：`internal/scheduling/scheduling.go`、`status.go`、`registry_read.go` 与对应 `*_test.go`。

### U2：本机路径探测与一次性唤起（边界型）

- **①契约引用**：§5 条 22–37、56–57；§3.2；§4 凭据表。岔口一、二适用。禁 `RunTurn`、禁交互登录。
- **②意图与为什么**：探测是只读事实；唤起是有时限的本机副作用（空白 HOME 落 CLI 自己的文件）。编制域零 import 执行域，这两件事必须停在 `hostapi`。
- **③验收**：
  - 机内（形状 + 本地 FS 夹具）：`go test ./internal/hostapi/ -count=1`。不存在与空目录 → `empty`；夹具里放 §4 相对路径文件 → `logged_in`（claude / Windows opencode 永不 `logged_in`）；非空无凭据 → `occupied`。`main_home_sync` 且隔离 empty 且主 HOME 已登录 → `logged_in`，**隔离目录仍无新文件**。
  - 反面：探测后对临时目录 `ReadDir`，条目集合与探测前相同（条 22–23）。occupied 目录在 wake 之后仍不得被清空（条 57）。
  - `WakeRequest.Timeout==0` 使用 `DefaultDetectTimeout`（30s）；测试注入短超时，到期返回且不挂死。
  - 类型系统/调用图：`WakeHome` 函数体不得调用 `RunTurn`（可用测试或 grep 闸：本包 `WakeHome` 路径不出现 `runTurn(`）。
  - 供给（合同补齐后）：按岔口一拷贝 §4 表内文件，不搬技能/规则树；claude 空操作。
  - 跨平台：`~` 在本机展开（条 31）；Windows 路径分隔与 opencode 无文件判据走既有 `credRelPathFor`。真机补 Windows。
  - 生命周期：超时杀唤起进程（沿用 hostapi 进程组惯例）；不留孤儿 TUI。行为「CLI 是否真落文件」标**未验证，需真机**。
  - 静默失败：本机错误上浮，gateway 映射 5xx；不得返回假 `logged_in`。
- **④入口指针**：`internal/hostapi/probe.go#Host.ProbeHome`、`#Host.WakeHome`；`internal/toolchain/detect.go#credRelPathFor`；组装点 `internal/agentd/server.go#Server.SetupAutomation`。
- **有界文件集**：`internal/hostapi/probe.go`（+ 本包新增实现/测试文件）、`internal/toolchain/detect.go` 与 `detect_test.go`（仅导出与既有锁）、`SetupAutomation` 注入处一行。

### U3：检测编排与 wire 投影

- **①契约引用**：§3.3 HTTP 表；条 6–8、33–44、45–48；拍板记录④ detect 不整段转发。
- **②意图与为什么**：写状态的入口只有 detect；跨机只转发 wake/probe。Ticket 0 的 detect 还把空 evidence 丢给未接线的 `ApplyDetect`。
- **③验收**：
  - `go test ./internal/agentd/ ./internal/proto/ -count=1` 含：缺失载体 detect/run-command → 404；detect 成功响应带 `version`（从 registry 行读，Ticket 0 未接通）。
  - 编排：本机直接 `WakeHome`；跨机 POST 对端 `/api/host/wake?machine=`（经 `forwardIfRequested`），再 `ApplyDetect`。反面：detect handler **不得**调用 `forwardIfRequested` 整段转发自己。
  - `carrierView` 删除 `healthy`；GET `/api/squads` fixture 与 `TestContractFixtures` 刷新。空 status / 空 last_error omitempty 缺席 vs 非空在场，各有断言（穿过真实 JSON，不是只比 Go 结构体）。
  - TS 孪生 `web/src/api/scheduling.ts#CarrierView` 同步删 `healthy`；`contract.test.ts` 不再 `expect(c.healthy).toBe(true)`。
  - 枚举白名单：`status` 四值、`ProbeKind` 三值、`WakeOutcome` 四值的 JSON 解码遇未知值不得静默当 `online`（失败或保持 pending，plan 钉死一种并测）。
  - 门禁：这些路由已在 `Handler()` 的 auth mux 上；本卡不新开绕过 auth 的入口。
  - 序列化边界文件清单：`internal/proto/scheduling.go#CarrierView`、`internal/agentd/schedapi.go#carrierView`、`web/src/api/scheduling.ts`、`web/src/api/testdata/SquadsResp.json`、`cmd/squad_test.go` 夹具。每一处改 `status`/`last_error`/`healthy` 都要有断言。
- **④入口指针**：`internal/agentd/schedapi.go#Server.handleCarrierDetect`、`#carrierView`、`#Server.handleCarrierPut`（确认仍不调 detect）、`internal/agentd/hostprobe.go#Server.handleHomeProbe`、`#Server.handleHomeWake`、`internal/agentd/forward.go#Server.forwardIfRequested`。
- **有界文件集**：上列文件 + `internal/proto/scheduling.go` + `web/src/api/testdata/*` + `web/src/api/contract.test.ts` + `cmd/squad_test.go`（夹具）。岔口三若选 A，这些 agentd 文件先随竖切搬家再填肉。

### U4：设置页四态、默认 HOME、探测提示、检测/运行

- **①契约引用**：条 44、47、49–50；形态权威原型；假缝「是否等于上一份默认」不占冻结。
- **②意图与为什么**：用户只看见设置·自动化。Ticket 0 页仍画 health 点、手填 HOME、无检测/运行。
- **③验收**：
  - `SchedulingPage.test.tsx`：登记填名后 HOME 框等于 `~/.handoff/home/<名>`；用户改过 HOME 后再改名不再覆盖；改路径会调用 `probeHome`（machine 走 query）；保存新建或 HOME 变更后必须再调 `detectCarrier` 一次（PUT 成功路径不得由页面去调 wake）；点运行只把 `getCarrierRunCommand` 的返回值交给剪贴板 API，测试断言调用参数等于服务端 command。
  - 反面：保存路径里 `putCarrier` 的 body 不得出现 `status`/`last_error`/`healthy`；页面不得 `HOME=` 拼接。
  - 四态药丸对照原型 class（pending/online/quota/unreachable）与 `CARRIER_STATUS_LABEL`。小队成员 chip 非 online 点灰（原型 `mdot`），不进派发的文案可有可无但不得把非 online 画成健康点。
  - 假红：不得只断言「有检测按钮」；要点击后看到 mock 的 `detectCarrier` 被调用。
  - webview 族：剪贴板写入标**未验证，需真机**（Wails 真实手势 vs 合成点击）。
  - 静默失败：探测/检测/保存错误展示可行动原文，不得把 503 显示成已上线。
- **④入口指针**：`web/src/app/settings/SchedulingPage.tsx#SchedulingPage`、`SchedulingPage.test.tsx`、`web/src/api/scheduling.ts#defaultHomeDir`、`#probeHome`、`#detectCarrier`、`#getCarrierRunCommand`。
- **有界文件集**：上列 + 若拆小组件则限 `web/src/app/settings/` 下本卡新建文件。

### U5：执行者任务消费载体 HomeDir（合同补冻后）

- **①契约引用**：条 51–55。岔口四、六适用。协调者拉起已满足 51/52 的拉起半边（`scheddrain.go`），本卡只补小队派发半边。
- **②意图与为什么**：现在 `startCardStep` 把 Binding 的 CLI/机/模型交给 runner，HomeDir 丢在编制域。codex 丢弃 `CODEX_HOME` 复用 `~/.codex`；grok `EnsureAuthLink` 软链 `os.UserHomeDir()/.grok/auth.json`。健康检测看隔离 HOME、任务却用主 HOME，会对不上。
- **③验收**：
  - 序列化边界（合同补 `home_dir` 后必过）：从 `DispatchOpts` 到 `client.Dispatch` 手搭 map 到 `dispatchRequest` 到 `DispatchReq` 的 roundtrip——缺席 vs `""` vs 非空三态。文件：`internal/ledgerstep/dispatch.go#DispatchOpts`、`internal/client/client.go#Client.Dispatch`、`internal/agentd/server.go#dispatchRequest`、`internal/agentd/manager.go#DispatchReq`。
  - `startCardStep` 在 Binding 非空时读 `Carrier(binding.Carrier).HomeDir` 写入上述字段。反面：非小队节点该字段缺席，不得误覆写执行机 HOME。
  - 载体 HomeDir 非空时，codex 不得再把任务打进用户级 `~/.codex`（条 53）。改写 `droppedEnvKeys` 的测试：隔离 HOME 场景下原「必须丢弃 CODEX_HOME」那支要按岔口四改断言，避免换实现就假红。
  - grok 任务级 grokhome 仍创建（条 54）；`EnsureAuthLink` 的目标不得是机器主 HOME，除非本轮凭据来源是供给动作（条 55）。反面：给一个主 HOME auth 路径，断言 link 不指向它。
  - 门禁：不新开绕过审批门的启动路径；只改凭据落点。
  - 行为「真 grok/codex 进程读到的 auth 文件是隔离副本」标**未验证，需真机**。
- **④入口指针**：`internal/agentd/cardstep.go#Server.startCardStep`、`internal/agentd/scheddrain.go#launchCoordinatorRound`（对照已做对的拉起）、`internal/executor/codex/taskenv.go#droppedEnvKeys`、`internal/executor/grok/taskenv.go#EnsureAuthLink`、`internal/executor/grok/authsync.go#authorityAuthPath`。
- **有界文件集**：上列 + `internal/ledgerstep/dispatch.go`、`internal/client/client.go`（Dispatch 手搭 map 与测试）、`internal/agentd/server.go` 仅 `dispatchRequest`/`handleDispatch`、`internal/agentd/manager.go` 的 `DispatchReq` 与 `ad.Start` 前 env 装配、对应 `*_test.go`。圈不出再拆，不把整个 `server.go`/`manager.go` 当工作集。

---

## 四、缺陷族对抗审查

项目清单：`docs/superpowers/specs/2026-08-21-handoff-instantiation-checklist.md` §3（五族 + 序列化边界 + 枚举白名单 + webview 候选）。顶部缺 `基线版本：charter@<commit>`，本拆解按项目清单加严，并并入基线追加「承重安全属性」。

对每个触及顶层域逐族正面回答（无风险写「无，因为……」）：

### d_scheduling（U1）

| 族 | 回答 |
|---|---|
| 生命周期 | ApplyDetect/PutCarrier 是 registry CAS 写，无子进程。中途崩溃 = CAS 未提交则旧行仍在。无孤儿资源。无，因为本域不持进程。 |
| 静默失败 | 旧翻真是「报已上线但没探测」的窗口，本卡废止。ApplyDetect 失败不得写 online。`ErrNoHealthy` vs `ErrNoSlot` 必须继续可 `errors.Is` 分流（scheddispatch 依赖）。 |
| 跨平台 | 状态字符串与路径串都是字面量，不在本域展开 `~`。无，因为展开归目标机 hostapi。 |
| 假红/假绿 | 锁调用方依赖的行为（status 值、哨兵），不锁 Healthy 字段名。 concurrent admit 沿用既有 CAS 测试；本卡不改计数。 |
| 门禁 | 请求仍不能写 status。无新写入口。TOCTOU：准入与计数仍在同一 CAS 循环（既有岔口三方案 A），本卡只换健康谓词。 |
| 序列化 | Carrier 的 JSON 是 encoding/json 标签，不是手搭 map；registry 测必须覆盖 status 空缺席。 |
| 枚举白名单 | 四态新值流经 Label switch、admitInto 比较、ApplyDetect 表。未知 status 不得当 online（U1 验收）。 |
| 承重安全 | 「非 online 不派发」有反面测试。 |
| webview | 无，因为无 UI。 |

### d_execution（U2+U5）

| 族 | 回答 |
|---|---|
| 生命周期 | WakeHome 有 30s 上界，超时杀进程树（unix 组 / 其余子进程）。中途 agentd 重启：唤起进程可能残留——必须在超时路径与包注释里承诺回收策略；真机验杀干净。U5 不新起长驻进程。 |
| 静默失败 | Probe 不得在 Stat 失败时报 logged_in。Wake 失败经 Outcome/error 上浮，不在本层写编制状态。 |
| 跨平台 | `~` 目标机展开；Windows opencode / claude 无文件判据；USERPROFILE 叠层归实现票且不得悄悄改 RunCommand 金样本。路径空格不改冻结无引号格式。 |
| 假红/假绿 | FS 夹具锁「目录条目不变」「凭据文件 Stat 成功」；不锁内部 helper 名字。CLI 落文件是行为事实，机内假绿风险 → 真机清单。U5 改 droppedEnvKeys 时旧测试「必须丢弃 CODEX_HOME」会假红，必须按隔离/非隔离分场景，避免锁内部帮手。 |
| 门禁 | WakeHome 不是派发、不进审批门；不得借机绕过 permgate。U5 只改凭据落点，不放宽工具门。 |
| 序列化 | Probe/Wake DTO 已有 fixture；U5 的 home_dir 传输是手搭 map，必须 roundtrip。 |
| 枚举 | ProbeKind / WakeOutcome 未知值不得映射成 ready/logged_in。 |
| 承重安全 | 隔离凭据「不指回主 HOME」有能变红测试（U5 反面）。occupied 不覆盖有能变红测试（U2）。 |
| webview | 无，因为无 UI。 |

### d_gateway（U3，部分 U5）

| 族 | 回答 |
|---|---|
| 生命周期 | detect 不整段转发，避免写到执行机空账本。转发失败 502 不重试（既有 `forwardIfRequested`）。中途重启：detect 未 ApplyDetect 则状态停在 pending，用户可再点检测。 |
| 静默失败 | Ticket 0 的 503 空壳必须在接线后消失；接线后 wake 失败要 ApplyDetect 成 pending/unreachable 而不是 HTTP 200 + online。Put 成功文案不得隐含「已上线」。 |
| 跨平台 | machine query 选路与 OS 无关。 |
| 假红/假绿 | wire 测试走 `Handler()`，不单测未导出拼接函数。 |
| 门禁 | 路由挂在已有 auth mux。不把 detect 暴露成未鉴权。检查（读载体）与写（ApplyDetect）之间允许别人 CAS 改行——ApplyDetect 必须自己 CAS，冲突可观测。 |
| 序列化 | `carrierView` 是手写投影，已列入 U3 文件清单。 |
| 枚举 | HTTP JSON 四态/三 kind 白名单见 U3。 |
| 承重安全 | 无新 token。无，因为不引入一次性令牌。 |
| webview | 无，因为本域无浏览器 API。 |

### d_protocol（U3）

| 族 | 回答 |
|---|---|
| 生命周期 / 门禁 / webview / 承重 | 无，因为本域只持 DTO。 |
| 静默失败 | omitempty 把空 status 藏起来是契约，不是静默失败；实现票不得把空当成 online。 |
| 跨平台 | 无，因为是 JSON 键。 |
| 假红/假绿 | `TestContractFixtures` 逐字节；删 healthy 必须 `-update` 显式刷新，禁止手改一半 fixture。 |
| 序列化 | 本域就是序列化边界的 Go 侧。孪生 TS + testdata 必须同批。 |
| 枚举 | fixture 样本至少覆盖四态各一（可分文件），避免只锁 pending。 |

### d_web（U4）

| 族 | 回答 |
|---|---|
| 生命周期 | 探测是即时请求，刷新页即停。自动 detect 是 PUT 成功后一次，不是轮询。无，因为无本地持久。 |
| 静默失败 | 检测中按钮 disable；失败展示 last_error / HTTP error，不得把药丸画成已上线。 |
| 跨平台 | HOME 默认串带 `~`，不在浏览器展开。Windows 展示已存串（原型有 `C:\Users\...` 样本）。 |
| 假红/假绿 | 从按钮进 API mock，不测草稿 helper。反面：body 无 status。 |
| 门禁 | 沿用控制台已登录 session；本卡不新开匿名写。 |
| 序列化 | TS CarrierView 与 Go 孪生；删 healthy。 |
| 枚举 | `CARRIER_STATUS_LABEL` 四键必须穷尽 `CarrierStatus` 联合类型（TS 会在缺键时红）。 |
| 承重安全 | 无，因为不持凭据明文。 |
| webview | 「运行」剪贴板：WKWebView / Wails / Chromium 行为不同，真机三档。合成 click 的 clipboard 绿不当验收。 |

---

## 五、真机清单（归协调者执行；机内结论不得冒充）

凡标「未验证，需真机」的汇总：

1. 空白隔离 HOME 经检测后，对应 CLI 落下自己的文件（条 37）。四家 CLI 各一次。
2. WakeHome 在时限内返回，不进入交互登录 TUI，不喂模型 prompt。
3. 跨机：`?machine=` 探测与唤起打到对端真实 FS；detect 仍写协调机 registry。
4. `kind=occupied` 保存再检测，目录原有文件仍在。
5. main_home_sync：隔离 empty + 主 HOME 已登录 → 探测 logged_in 且未改隔离目录；保存/供给后表内凭据文件出现在隔离 HOME，技能/规则树不在。
6. claude 探测永不 logged_in；登录后检测能否到 online（无文件判据，只能看 wake 结局）。
7. Windows：`~` 展开、opencode 永不 logged_in、RunCommand 仍是 `HOME=...`（USERPROFILE 若叠层，另记）。
8. 已上线载体拔网/关机后再检测 → unreachable；恢复后再检测 → online。
9. 额度用尽（若协调者有可复现账户）→ quota；没有则记未验，不把夹具当事实。
10. 执行者小队任务：codex/grok 实际读取的 auth 落在载体 HomeDir，不是机器主 HOME。
11. 设置页「运行」在桌面控制台真实手势下剪贴板写入成功。
12. 存量载体（空 status）按岔口五拍板后的迁移，现网协调者小队仍能拉起或给出可行动错误。

---

## 六、图覆盖债

- `Service.PutCarrier` / `admitInto` 等在 baseline 缺席，只在 `codegraph/diffs/cards-B293-charter-3.json`。本节点不回灌。
- `codegraph entity CarrierView`：`projScanned: false`，序列化边界按本拆解手列，不把 entity 输出当清单。
- 无 view 时 `sym scheduling.Service.PutCarrier` 不命中——文档锚用 `file#Symbol`，resolve 必须带 `--view cards-B293-charter-3`。
- 项目缺陷族清单缺 `基线版本：charter@<commit>`，记清单债，不在本卡改实例化协议。

---

## 七、交稿自检

1. 四样齐全：子系统带类型、契约逐条、子卡四段式且判据行为化、缺陷族逐族有「无，因为……」。
2. 待拍板六条集中在稿首，均已写裁决 + 理由。
3. 真机清单一节。
4. 有界文件集已核；圈不出的没有硬塞。
5. 行为闭环五格完整；U5 缺传输字段已指向退回 contract，没有无人认领的承诺。

本轮未碰 handoff CLI、未起新 executor、未写功能实现。
