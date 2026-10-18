# B439 plan：机器身份收口——自名 + 全队共享设备表

> 卡 B439（L3）· 节点 charter:plan · spec `docs/superpowers/specs/b439.md`（r1 已批准）
> 有效基线分支 `cards/B439-charter`，起手 HEAD `63858ae9`。本节点只出计划与同批台账，不写实现。
> 台账 `docs/superpowers/plans/b439-plan-ledger.md`（全部亲跑命令、原始输出、图查询记录）。
> 凡引用行号者为 `63858ae9` 现状读数，动手前重核，漂了以符号为准。
> **未验证** = 本节点没亲自跑到结果；实现卡必须自己复核。读者假设：对 handoff 仓零上下文。

---

## 0. 待拍板清单

| # | 岔口 | 本计划的建议 | 影响 |
|---|---|---|---|
| **P1** | **有 DSN、有自名、直连形态但缺 `advertise:` 的机器，自登记怎么处理？** | **(甲) 视同缺自名**：不挂载账本子系统 + 可行动错误「在 config.yaml 设置 advertise:」（与 S2 同类 fail-closed）| 选 (乙)「以 advertiseAddr(listen) 兜底 + WARN」会把一条可能不可达的连接方式写进全队设备表，其它机器按它选路即静默断链——正是本卡要杀的缺陷族。**本计划按 (甲) 写，P1 未拍板前该分支按 (甲) 实现** |
| P2 | `proto.Machine` 加性字段 `self`（JSON `"self"`，本机行标 true）| 采纳（加性 JSON，旧客户端忽略未知键；CLI 与控制台用它标「本机」） | 无破坏性；不合意则退回「空串名=本机」的旧约定，但自名行会丢失团队名 |
| P3 | 设备表表名 `devices`（账本侧新领域） | 采纳 | 纯内部命名，拍板人可改名，不改语义 |

其余岔口本计划已按 spec 职权决断（§4 逐条带依据）；拍板时若推翻 §4 任一条，改动只落在对应工作单元，不波及其它单元的文件集。

---

## 1. 触及子系统清单（L3 增段①）

| 子系统 | 类型 | 理由 | 派卡资格四条核对 |
|---|---|---|---|
| `internal/config` | 逻辑型 | 自名/对外地址配置键 + 唯一自名判据（spec §目标架构） | 有界（config.go + 新 selfname 文件）；契约面=两个 yaml 键 + 判据函数；无外部依赖；见 §9 F1/F2 |
| `internal/ledger` | 逻辑型 | 设备表领域（新表 + 写口拒 `''`）；对面是共享 PG/SQLite，同仓自有部署，机内可闭环 | 有界（devices.go 新文件 + store.go DDL 两分支）；契约面=表 DDL + 6 个 Store 方法（§9 F3/F4/F8）；依赖仅 stdlib+驱动；见 §9 |
| `internal/ledgerstep` | 逻辑型 | 派发链身份/路由拆分（ViaTemplate、WorkBranch 存量口径、模板旧别名拒收） | 有界（dispatch.go/node.go）；契约面=NormalizeTarget 缝换形 + 拒 '' 依赖；依赖 config 判据注入；见 §9 F3/F7 |
| `internal/ledgermirror` | 逻辑型 | 命名路由（自名→LocalSource）+ S7 存量兼容 + WARN/健康面 | 有界（mirror.go）；契约面=Options 缝语义（§9 F6/F7）；依赖注入判据 |
| `internal/agentd` | 逻辑型 | 组装点（自登记、池源、挂载门）、CanonicalTarget 收敛、机器管理改写设备表、健康 API | 有界（按文件圈：server/cardstep/handlers/machineadmin/machines/ledgerapi + 新 selfregister/devicesource）；见 §9 |
| `internal/targetclient` | 逻辑型 | 池选路数据源缝（有 DSN→设备表，无 DSN→活配置） | 有界（pool.go 单文件）；契约面=NewPool 签名 + For/Names 语义（§9 F5） |
| `internal/scheduling` | 逻辑型 | carrier.machine 判据收敛（S6）；knownMachines 数据源换设备表 | 有界（scheduling.go 判据段 + PutCarrier 校验段）；契约面=IsLocalMachine 门控语义（§9 F2 引申） |
| `cmd` | 逻辑型 | CLI 归一（card_dispatch）、machines 标本机、init 问 name/advertise、launcher 拒旧别名 | 有界（card_dispatch/machines/init/launcher 四文件 + initflow 字段表）；见 §9 |
| web / desktop / mobile | 不触及 | 控制台机器页走 `POST/DELETE /api/machines` 路由不变（spec 落位三问②），加性 `self` 字段旧前端忽略。**验证面**：S4 真机过一遍机器页增删；若前端对未知字段报错才升级 | — |

全部为逻辑型（对面无外部现实；共享 PG 是自有部署库，多机行为验收走 §11 真机清单）。**不按子系统派子卡，按 §5 的工作单元派**——七个子系统的文件集横切在三个工作单元里，任何单子系统子卡都圈不出可独立验收的行为。

---

## 2. 根因与基线事实（全部亲读/亲跑，原始输出见台账）

### 2.1 缺陷链（spec 问题陈述 1-2 的代码级复核，行号已对 worktree 复核）

```
协调者 linux-01: handoff card dispatch <id>（本机派发）
  → cmd/card_dispatch.go:335,370-377  canonicalCLITarget: 登记名指向本机 → ''
  → internal/ledgerstep/dispatch.go:249-251  NormalizeTarget 缝（agentd 侧=s.CanonicalTarget, cardstep.go:218）
  → dispatch.go:482  RecordDispatchAndLinkTask: card_tasks.target='' + dispatched 快照 Target=''
共享账本（PG）
  → 镜像持有者 macbook-pro: mirror.go:335  want 过滤放行 ''
    → mirror.go:240-241,497  isLocalTarget('')=true → LocalSource = macbook-pro 自己的（空）store
    → 事件永远读不到：无 WARN、无报错、健康行正常 —— 静默失效
```

四个 `''` 写入源（同一缝）：CLI `canonicalCLITarget`、agentd `CanonicalTarget`、（模板缺省 target 空串经同一缝）、卡片 API 派发（同走 ViaTemplate）。

### 2.2 承重基线事实

- **镜像是「活配置驱动」**：`Machines.Names()`/`For` 来自 target 客户端池，池每轮现读 `conf().Targets`（pool.go:90-98,110-117）。设备表切换 = 换池的数据源，池的实例判等（`e.target == t`，B163 ②）与「For 不发网络」语义保（§9 F5）。
- **镜像租约 holder 今天是 hostname**（cmd/agentd.go:542-544），写入共享表 `mirror_lease.holder`——自名落地后必须换成 `name:`，否则租约在两台 hostname 同名的机器上仍会漂。
- **唯一拒 `''` 的口**：`RecordDispatchAndLinkTask` 是 `card_tasks`/dispatched 快照的唯一生产写入方（ledgerstep/dispatch.go:482；`RecordDispatch` 无生产调用方）。在这一个口上拒 `''` 即封死新写入。
- **wire Target 即执行机 task.Target**（handlers.go:583-597）：接收端把 `binding.Target` 直接放进 `orchestration.DispatchReq.Target`。因此本卡**不改 Dispatch wire**：本机短路仍以 `''` 走 wire（那是进程内选路值），全队名字只进共享账本（spec：路由值不进账本）。`proto.Task.Target==''`（本机任务）的读者面（cmd/pull.go:70、roomsapi.go:246）保持。
- **WorkBranch 的存量口径是真实岔口**（spec 首故事选择的承重未知）：dispatch.go:279-303 跨机判据比对「归一后 previousTarget vs 归一后 target」。存量 `''` 快照在今天 `''==''` 判同机；若把 `''` 归一成自名，异机读者会把存量卡误判为跨机拒接。**处置**：存量 `''` WorkBranch 保持「视作本机、跳过跨机判据」（spec 实现决定），新快照恒全队名（§6 T1.4）。
- **序列化陷阱**：`config.Target` 只有 yaml tag 无 json tag（config.go:310-317）；`json.Marshal` 直接序列化会产出大写字段名（`Addr`/`Token`）。设备表行里的连接块必须走**显式投影 struct**（§6 T2.2），并配双后端（PG JSONB / SQLite TEXT）真实往返断言（纪律块序列化边界）。
- **严格解析 + omitempty 纪律**：config 以 `KnownFields(true)` 解析，未知键让旧版 agentd 启动失败（config.go:68-71 注释，B59 同款）。新增顶层键 `name:`/`advertise:` 必须 `omitempty`——未配置时不落盘，旧版可继续读。
- **凭据红线**：`proto.Machine` 无 Token 字段是「令牌只进不出」的类型级保证（machines.go:187-188 注释）；设备表凭据进账本是用户裁决，读面必须收口到池专用读方法（§9 F8），日志沿用 B409.6 脱敏纪律。
- **relay 形态**：一台机器经 relay 可达 = 它以 `RelayConfig{URL,Credential,Node}` 出站注册（config.go:186-192），`config.Target` 的 relay 键五元组（Relay/Credential/Node/Token）在设备表行里原样承载（`Target.IsRelay()` 判形）。控制台新增 relay 机器不在本卡（machines.go:210-212 注释，spec OOS）。

### 2.3 图覆盖债（图未命中 → grep 验证）

- `NormalizeTarget`、`FrozenTarget` 不在图中（近似候选为空），已 grep 落实定义与全部装配点（台账「图覆盖债」条目）。实现卡动这两个缝时以 grep 结果为入口。

### 2.4 基线绿（本节点亲跑）

`go build ./...` 退出 0；`internal/ledgermirror`、`internal/config`、`internal/targetclient` 全量 ok；`internal/agentd`、`cmd` 定向（机器管理/归一/launcher/machines）ok。原始输出见台账 §「基线亲跑」。

---

## 3. 故事映射与行为闭环（L3 增段②）

每条故事一行闭环「触发者 → 权威事实/载体 → 消费者 → 可观察结果 → 实现责任」，归属工作单元见 §5。

| 故事 | 行为闭环 | 集成与回归负责人 |
|---|---|---|
| S1 本机派发落全队名，异机持有者能镜像 | linux-01 协调者 `card dispatch`（target 省略或 =自名）→ 共享账本 `card_tasks.target=linux-01` + `source_target=linux-01`（ledger 写口，拒 `''`）→ macbook-pro 镜像持有者按名经池订阅 linux-01 事件流 → `task_mirrored` 落账、`card wait` 唤醒；反例：持有者取不到该名客户端 → WARN 带 target/task + 健康行断链，不静默 | WU1 实现卡（机内双镜像竖切）+ 协调者（linux-01+macbook-pro 真机，§11-1） |
| S2 机器配置自名 | 用户 `handoff init` 问答 / 手改 config → `config.yaml name:`（本机配置，config 领域）→ agentd 启动日志、`handoff machines` 标本机、全部自名判据消费点 → 日志见自名；反例：有 DSN 缺 name → 共享 DSN 不打开、共享子系统不挂载、可行动错误「在 config.yaml 设置 name:」 | WU1 实现卡 + 协调者真机 |
| S3 自登记、互相可见 | agentd 启动（DSN+自名+advertise/relay 形态齐）→ 设备表一行（名字唯一、连接方式、凭据、实例 id）→ 其它机器的池按名取客户端、控制台机器页、`handoff machines` → 免本地配置即可 `--target <名>` 派发；反例：撞名（异实例 id）→ 拒挂载 + 错误写明冲突名 | WU2 实现卡 + 协调者真机三台（§11-2） |
| S4 控制台与 CLI 同一张表 | 控制台机器页增删 / `handoff machines` → `POST/DELETE /api/machines` 写设备表（路由不变）、列表读设备表 → 全队可见；凭据不出现在任何日志与 API 响应 | WU2 实现卡 + 协调者真机（§11-4） |
| S5 无 DSN 照旧 | 无 DSN 机器日常使用 → 本地 `targets:` 与活配置判据 → 池/调度/CLI → 行为与今天逐点一致（回归锁，零新行为） | WU2 实现卡（既有用例全绿的回归责任） |
| S6 旧别名被拒 | 协调者/模板/launcher 输入 `local`/`本机`/hostname（机器已配自名）→ 自名判据（config.Name）→ 调度登记、launcher 登记、模板派发、升级目标机 → 拒绝 + 可行动错误（点名哪条记录、改成什么名字）；机器未配自名 → 沿今天判法（S5） | WU3 实现卡 |
| S7 存量 `''` 不静默 | 镜像对账遇 `card_tasks.target=''` 非终态挂账 → 镜像 Options 判据 → 尽力按「持有者本机」订阅（持有者恰为派发机时仍可用）→ 每轮每条 WARN「历史无归属挂账，仅在持有者本机时可镜像」+ 健康面「无归属在飞挂账 N 条」 | WU3 实现卡 |
| S8 本地 `targets:` 一次性导入 | 有 DSN 机器 agentd 启动 → 设备表 vs 本地 config → 表中没有的条目导入（同名不覆盖）；此后池只读表 → 本地 `targets:` 被忽略 + 启动 WARN 提示删除；反例：同名异连接 → 不覆盖 + WARN 列差异 | WU2 实现卡 + 协调者真机（§11-5） |

---

## 4. 设计决定与接缝语义（本计划职权内的决断，逐条带依据）

### 4.1 自名判据（config 侧）

- 新键：`Config.Name`（yaml `name,omitempty`）、`Config.Advertise`（yaml `advertise,omitempty`，直连形态对外地址 `host:port`；relay 形态不新增键，沿用 `relay:` 段）。
- 唯一判据（config 导出面，一处定义）：
  - 输入省略（空串）→ 是本机；输入 == `Name` → 是本机；其余 → 否。
  - `Name` 词法：非空、无空白、不含 `/`；`local`/`本机` 是保留字，Load 拒绝（§9 F1）。
- **判据收敛的门控是「有无 `name:`」，不是有无 DSN**。依据：S5（无 DSN 照旧）与 S6（旧别名拒收）同读——无自名的机器没有可改成的名字，拒旧别名等于把人卡死在「设置 name:」之前；有自名的机器，旧别名正是要杀的歧义源。有 DSN 无 name 是 S2 的「不挂载」分支，与判据门控正交。
- 六个旧判据的处置（spec §目标架构点名六个，实现按此表，多余删）：

| 旧判据 | 处置 |
|---|---|
| `config.IsSelfTarget(listen, Target)`（地址判，listenclass.go:80） | **保留**，服务无自名回退路径（S5）与 S8 导入的「同名异连接」比对；有自名时不再参与名字面判等 |
| `scheduling.IsLocalMachine`（scheduling.go:182） | 门控收敛：注入自名后仅「省略或 == 自名」为真，`local`/`本机`/hostname 转拒收（S6）；未注入（无自名）保持今天词表 |
| `agentd.isLocalMachineName`（machineupgrade.go:331） | 删，改走 config 自名判据（注入或经 Server 方法） |
| `cmd.probeLauncherMachine` 的别名分支（launcher.go:245-255） | 有自名 → 旧别名从「照探本机+告警」改为**拒绝登记**（S6 可行动错误）；无自名 → 保持告警（S5） |
| `cmd.canonicalCLITarget` | 换形为身份解析（§4.2），`''` 归一删除 |
| `agentd.Server.CanonicalTarget` / `IsSelfTarget`（server.go:534-555） | 拆成两个语义（§4.2）；addr 判保留给无自名回退 |

### 4.2 身份值与路由值拆分（ledgerstep / agentd / cmd）

- **身份值**（进共享账本）：输入省略 → 自名；输入 == 自名 → 自名；其余 → 原值（全队名字，未知名由下游拒）。**永不为 `''`**。
- **路由值**（进程内选路，不进账本）：身份值 == 自名 → `''`（本机短路）；否则 = 身份值。接收端语义不变（wire `''`=在本机执行，`proto.Task.Target==''`=本机任务，cmd/pull/roomsapi 读者面不动）。
- 落点：`Dispatcher` 的 `NormalizeTarget` 缝换形为身份解析（返回身份值 + 是否本机；函数名实现卡定，缝语义即此）；`stepTransport`/`cliTransport`/`targetClient` 用路由值，快照/挂账用身份值；`FrozenTarget` 原样透传不变（B398 契约）。
- WorkBranch（dispatch.go:279-303）：比对在身份值域进行；`workInfo.Target == ''`（存量）→ 跳过跨机判据（视作本机，今天行为）；非空 → 全队名比对（新写入恒非空，`node.go:451` 的回落分支只服务存量）。

### 4.3 设备表领域（账本侧，新）

- 独立表 `devices`（P3），不挂 `registry` kind：spec 落位三问①判它独立领域；且「凭据只在读给池的那条路径上出现」要求读面收口，通用 registry 读面（编制域在用）做不到列级隔离。registry 文件头「实体形状演进不牵动账本 schema」的通用 KV 设计仍受尊重——**连接块与扩展属性都放 JSONB**，加字段不加列。
- 行形状（DDL 双后端，PG JSONB / SQLite TEXT，沿 store.go 两分支纪律）：
  `name TEXT PK` · `target JSONB`（连接块：直连 `{addr,user,token}` / relay 五元组）· `instance_id TEXT` · `registered_at` · `updated_at` · `attrs JSONB DEFAULT '{}'`（B437 审批链 / B438 端点候选未来落这里，本卡不加其字段）。
- 写入口两个（spec）：自登记 upsert（同 instance_id → 更新连接字段，换 IP/relay 不换身份；异 instance_id → 冲突错误点名名字）；控制台/CLI 增删。读面：池专用读方法（含凭据）+ 列表读方法（**投影时剥凭据**，§9 F8）。
- 已知语义边界（记入 §10）：控制台删一台机器后，那台机器重启自登记会复活该行——凭据吊销/轮换流程在 roadmap OOS；控制台删除提示文案需说明。

### 4.4 池数据源（targetclient / agentd）

- `NewPool` 增一个数据源参数（生产唯一调用方 server.go:344）：有 DSN → 设备表快照源；无 DSN → nil（回落活配置 `targets:`，S5）。
- 设备表快照源：每次调用现读表，读失败 → 回上一份成功快照 + WARN（**不静默、不撕订阅**：瞬态读库失败不等于配置已变更，按 B163 ② 的保守语义持旧凭据；表读成功的下一轮自动更新）。 Names/For 的「不发网络、实例判等」语义不动（§9 F5）。
- 调度 `SetKnownMachines`（cmd/agentd.go:520-527）同步换源：有 DSN 读设备表，无 DSN 读活配置。

### 4.5 挂载门与自登记（cmd/agentd.go setupLedger）

- 顺序：开账本前判定 `cfg.Ledger.DSN != "" && (cfg.Name == "" || 直连形态缺 Advertise)` → **共享 DSN 不打开**，走本机 SQLite 回退（与无 DSN 同路径），镜像/卡派发/自登记/调度/自动化/推送全部不挂载，日志 WARN + 控制台可行动错误（S2「照常起」的机制化：不给一台无名机器留半共享状态——它会消费共享事件流，正是旧缺陷族的变体）。
- 实例 id：`<DataDir>/machine-id`（首次需要时生成随机 hex，0600；文件名归本计划定，spec 留白处）。
- 自登记在账本打开后、镜像挂载前同步执行；冲突（异实例撞名）→ 账本子系统不挂载 + 错误写明冲突名（S3）。S8 导入在自登记后执行一次：本地 `targets:` 中设备表没有的名字导入；同名已存在不覆盖（异连接 WARN 列差异）；保留字条目（`本机` 等）不导入，WARN 点名。

### 4.6 镜像路由与 S7（ledgermirror）

- `Options` 的自判缝换成「自名注入」：判等 = `target == 自名`（`''` 走 S7 分支）；`Holder = 自名`（cmd/agentd.go:544 换 hostname）。无自名（无 DSN 机器跑镜像——今天不存在镜像无 DSN 场景，因账本必需，无 DSN 机器的本机账本也可挂镜像；此时 holder=hostname 回退，判据走 addr 回退，S5）。
- S7：对账遇非终态 `''` 挂账 → 仍按 LocalSource 尽力订阅；每轮对账对每条打一次 WARN（幂等：同一条不重复刷屏，靠对账周期天然限频）；健康 API（ledgerapi.go:1085-1094）加性字段报「无归属在飞挂账 N 条」。

### 4.7 S6 拒收点与文案

- carrier 登记（scheduling PutCarrier）：`machine` 保留字/hostname → `ErrInvalid` 包装，文案点名「carrier.machine=本机 → 改成 config.yaml 的 name: 值」。
- 模板派发（ledgerstep ViaTemplate 前置）：模板 target ∈ 保留字/hostname → 拒发，文案点名「模板 <名> 的 target=<旧别名> → 改成 <全队名或自名>」。
- launcher 登记（cmd）：同上，点名「--machine local → 改成 <自名>」。
- 升级目标机（machineupgrade）：`isLocalMachineName` 删除后，升级无自名机器走 addr 回退判据；有自名时旧别名 → 可行动错误。

---

## 5. 工作单元与依赖（L3 增段③）

**扇出判断（spec 授权 plan 按工作量判）**：**扇出**。三片在既有边界上（判据缝 / 设备表缝 / 存量兼容缝），WU2 与 WU3 无共享生产文件（§1 清单已按文件圈界），一批两张符合「不超过两张」。若拍板人不扇出，三片在本卡按 DAG 顺序执行即可，计划正文不需改。

```
WU1 首故事子卡（S1+S2）：身份/路由拆分 + 自名配置 + 镜像命名路由 + 落账拒 ''
  └─ 单独走完一轮，--no-ff 合入 cards/B439-charter ⇒ 共同基线
WU2 设备表（S3/S4/S5/S8）      ┐ 一批两张并行，基线 = WU1 合入点
WU3 判据收敛与存量兼容（S6/S7） ┘
```

- WU2 ← WU1：**契约硬依赖**（编译与行为都依赖：config 自名判据与 `name:/advertise:` 键、落账拒 '' 口、身份解析缝；S8 导入的「同名」判等以自名判据为准）。
- WU3 ← WU1：**契约硬依赖**（同一判据与身份解析缝；S6 的模板拒收点在 ViaTemplate 身份解析前置段）。
- WU3 ← WU2：**施工排序**（S6 的调度 knownMachines 换设备表源在 WU2；WU3 若先合，该行以 WU1 基线的活配置源运行，WU2 合入后自然切源——两卡在 scheduling 包不碰同一函数，无文件冲突）。

---

## 6. WU1 首故事（S1+S2）：精确文件集与验收

### 6.1 精确文件集

- `internal/config/config.go`（或新 `selfname.go`）：`Name`/`Advertise` 字段 + Load 词法校验（保留字）+ 自名判据导出面。
- `internal/config` 测试：词法表驱动（保留字、空白、`/`、省略、他名、hostname≠自名）。
- `internal/ledger/events.go`：`RecordDispatchAndLinkTask` 拒 `snap.Target == ""`（错误可行动：给「目标机 identity 缺失」指引）；拒在写任何行之前（事务内先断言）。
- `internal/ledgerstep/dispatch.go`：`NormalizeTarget` 缝换形（身份解析）；WorkBranch 存量 `''` 分支；`node.go:451` 注释补「存量口径」标注。
- `internal/ledgermirror/mirror.go`：`Options` 判据缝换自名注入；`isLocalTarget` 三分支（`''`→S7 分支（WU3 落 WARN，WU1 先保行为）、`==自名`→LocalSource、其余→池）。
- `internal/agentd/server.go`、`cardstep.go`、`handlers.go:583-589`：`CanonicalTarget` 拆身份/路由两语义（有自名走 name 判，无自名走 addr 回退）；`stepTransport` 用路由值；B233.27 准入改自名判。
- `cmd/card_dispatch.go`：`canonicalCLITarget` 换身份解析；探活/补偿用路由值。
- `cmd/agentd.go`：`setupLedger` 挂载门（缺 name → 本机回退）；镜像 `Holder`/判据注入换自名。
- `internal/initflow/form.go` + `initflow.go`：`name` 字段（所有角色可见；Notice 说明「有账本 DSN 时必填」；Apply 校验保留字；空值不拦——agentd 端可行动错误兜底）。

### 6.2 竖切测试（spec 测试决定落地）

- 命名版双镜像（改写台账记录的红色回路）：`mirror_test.go` 两实例共享一本账本；持有者 `MacBook-Pro.local`（`Holder`/`自名=macbook-pro`，LocalSource=空 store）先取租约；派发机（自名 `linux-01`，LocalSource=有 message 事件的 store）落挂账 `target=linux-01`；断言 task_mirrored 落账、`SourceTarget=="linux-01"`。持有者判据走注入缝，不碰网络。
- 反例（S1 关键失败）：持有者 `Names()` 不含 `linux-01`（池 For 报配置性错）→ 断言 WARN 带target/task 且健康行不刷新（断链亮灯）。
- 存量 `''` 回路（转 S7 前的行为保持）：`''` 挂账 + 持有者=派发机 → 仍镜像（WU1 保持，WU3 加 WARN 断言）。
- 落账拒 ''：`RecordDispatchAndLinkTask` 传 `''` → 错误 + 卡上无快照/无挂账/无留痕（事务回消断言）。
- 身份/路由拆分：CLI 与 agentd 两条装配路径，自名输入 → 挂账=自名、wire=本机短路；远端名 → 挂账=名、wire=名。FrozenTarget 原样透传（B398 既有用例全绿 + 新增一条「自名载体冻结派发在自名接收机准入」）。
- WorkBranch 存量口径：存量 `''` 快照 + 异机读者 → 不判跨机拒接；新命名快照 + 异机 → 跨机判据照旧（origin 已发布则接续）。
- S2 挂载门：有 DSN 缺 name → 账本为 SQLite 本机文件、镜像/调度不挂、日志含可行动指引；有 DSN 有 name → 照常挂载且日志打印自名。

### 6.3 WU1 不做

设备表（WU2）、S6 拒收文案（WU3）、S7 WARN/健康面（WU3）。WU1 期间 `local`/`本机`/hostname 输入行为不变（S5/S6 边界），只杀 `''` 落账。

---

## 7. WU2 设备表（S3/S4/S5/S8）

### 7.1 精确文件集

- `internal/ledger/devices.go`（新）+ `store.go` 双后端 DDL：表 `devices`（§4.3 行形状）+ 方法：`RegisterDevice`（upsert + 实例冲突）、`PutDevice`/`DeleteDevice`（控制台增删，CAS 沿 registry 风格）、`ListDevices`（剥凭据投影）、`DeviceTarget(name)`（池专用读，含凭据）、`DeviceAttrs`（预留读面，本卡零消费）。
- `internal/targetclient/pool.go`：`NewPool` 数据源参数；For/Names 走源。
- `internal/agentd/`：新 `selfregister.go`（machine-id 文件 + 自登记 + S8 导入）、新 `devicesource.go`（设备表快照源 + last-good）、`machineadmin.go`（add/remove 改写设备表，重名/不存在语义保留）、`machines.go`（列表读设备表 + 本机标记）、`server.go:344`（注入源）、`cmd/agentd.go`（knownMachines 换源、自登记接线）。
- `internal/agentd/ledgerapi.go` 无改动（健康面归 WU3）。
- `cmd/machines.go`：`self` 标记渲染（P2）。
- `internal/initflow/form.go`：`advertise` 字段（角色=执行机/两者；直连形态才必填——P1 拍板前按 (甲) 文案）。
- `internal/proto/projects.go`：`Machine.Self bool`（加性，P2）。

### 7.2 断言清单

- 设备表行往返：`config.Target` → 显式投影 struct（json tag 小写）→ `RegisterDevice` → PG 与 SQLite 两种库真实写读 → 值恒等；**区分字段缺失与零值**（relay 五元组缺 `node` 与空 `node` 用两用例）。
- 冲突判定：同实例重登（换连接方式）→ 更新成功；异实例撞名 → 冲突错误点名名字；控制台增删与自登记并发 → 后到者按判等结果落。
- 凭据不外泄：`ListDevices`/`GET /api/machines`/`handoff machines`/日志四条路径 grep 级断言（响应体无 token/credential 键；日志沿用脱敏测试族）。
- 池源：有 DSN → For(设备表名) 成功、For(未登记名) 拒绝；表读失败 → 回上一份快照 + WARN、订阅不撕；无 DSN → 活配置路径行为逐点不变（S5 回归锁 = 既有 targetclient/config 用例全绿）。
- S8：导入三态（新名导入 / 同名同连接跳过 / 同名异连接 WARN 列差异）；保留字条目拒导入；导入后本地 `targets:` 被忽略（For 不再读它）+ WARN 提示删除。
- S3 互相可见：机内双「agentd 组装点」共享一本账本，B 无本地 targets 即可 For(A 的名)。

---

## 8. WU3 判据收敛与存量兼容（S6+S7）

### 8.1 精确文件集

- `internal/scheduling/scheduling.go`：`IsLocalMachine` 门控收敛（§4.1 表）；`PutCarrier` 拒收文案（S6）。
- `internal/agentd/schedapi.go`（isLocalMachine 转发收敛）、`machineupgrade.go`（isLocalMachineName 删）、`cardstep.go`（resolveStepDiscipline/disciplineTargetCap 的短路改自名判）。
- `cmd/launcher.go`：`probeLauncherMachine` 有自名时旧别名拒绝（S6 文案点名）。
- `internal/ledgerstep/dispatch.go`：模板 target 旧别名前置拒发（点名模板）。
- `internal/ledgermirror/mirror.go`：S7 WARN（每轮每条、幂等）。
- `internal/agentd/ledgerapi.go`：健康 API 加性字段「无归属在飞挂账 N 条」（`ledger` 侧加一个计数读方法或复用 `LiveMirrorTargets` 的 `''` 键）。
- `internal/ledger`：B398 复核断言——冻结绑定在新归一下的既有用例（B398 的 `frozen_target` 透传与 `samePhysicalMachine` 判据）全绿；补一条「carrier.machine=自名 的冻结派发在自名接收机准入」。

### 8.2 断言清单

- S6 表驱动：`--target local`/`本机`/hostname × {有自名, 无自名} × {CLI, agentd step, 模板, carrier 登记, launcher, machineupgrade} —— 有自名全拒 + 文案点名；无自名全部保持今天行为（S5 回归锁）。
- S7：`''` 非终态挂账 → WARN 一次/轮；终态 `''` 挂账 → 无 WARN；健康面计数正确。
- 存量读取兼容（§9 F7）：`''` 挂账/快照/镜像行照旧可读，WorkBranch 缺字段口径保留（WU1 已锁，此处回归）。

---

## 9. 冻结断言（条件节——本卡新增的跨工作单元接缝）

无新增对外兼容承诺（控制台 HTTP 路由不变、Dispatch wire 不变、`proto.Task.Target` 语义不变；`Machine.Self` 为加性 JSON 字段，旧客户端忽略，不构成破坏性承诺——P2 拍板后如被推翻也不入冻结）。以下 8 条冻结 **WU1→WU2/WU3 的跨工作单元依赖**，一条=一支可独立判 pass/fail 的断言；实现选择（私有命名、列名微调）不入。

| # | 断言 | 产 → 消 |
|---|---|---|
| F1 | config 顶层键 `name`/`advertise` 存在且 `omitempty`；`name` 词法=非空、无空白、不含 `/`；`local`/`本机` 作 `name` 被 Load 拒绝 | WU1 → 全体 |
| F2 | 自名判据唯一：输入省略=本机；==`name`=本机；其余（含 hostname、旧别名）=否。config 是唯一定义点 | WU1 → WU2/WU3 |
| F3 | `RecordDispatchAndLinkTask` 在 `snap.Target==""` 时拒绝且事务零残留；存量 `''` 行照旧可读 | WU1 → 全体 |
| F4 | 设备表身份键=机器名，唯一；自登记同 instance_id 更新连接字段、异 instance_id 拒绝并点名；追加字段经 `attrs`，不改键不迁移行 | WU2 → B437/B438 及 WU3 |
| F5 | 池数据源：有 DSN=设备表、无 DSN=活配置；For 不发网络、实例判等语义不变 | WU2 → 全体 |
| F6 | 镜像路由：`==自名`→LocalSource；`''`→存量尽力分支；其余→池；Holder=自名 | WU1(→WU3 S7) |
| F7 | 存量兼容：`''` 挂账/快照可读；WorkBranch 缺字段视作本机口径仅对存量生效 | WU1/WU3 |
| F8 | 设备表凭据只经池专用读方法出现；列表/控制台 API/CLI/日志四条读面零凭据 | WU2 → 全体 |

依赖行为查证（纪律块查证三条）：双后端 DDL 与 `q()` 引号机制、CAS 版本语义（registry.go）、`KnownFields(true)` 严格解析、JSONB↔TEXT 映射均已在基线亲读（§2.2，store.go/registry.go/config.go 带行号）；无第三方库新行为依赖。

---

## 10. 缺陷族对抗审查（L3 增段④）

| 缺陷族 | 设问 | 本计划的结论/锁点 |
|---|---|---|
| 身份/旧数据 | 存量 `''`、旧别名、hostname holder 会以什么形态残留？ | F3/F6/F7 + §4.2 WorkBranch 存量分支；S7 WARN + 健康计数（WU3）；S8 导入同名异连接 WARN。历史 `''` 挂账的归属回填在 roadmap OOS |
| 静默失效复发 | 新链路还有没有「不报错但读不到」的形态？ | S1 反例断言（For 失败→WARN+健康断链）；池源读失败回 last-good+WARN；S2 挂载门 fail-closed（不给无名机器半共享状态） |
| 并发/双写 | 两台同时自登记同名？控制台删除 vs 自登记复活？ | 异实例撞名→后到者拒（F4）；删后重启复活=已记录语义边界（§4.3，控制台文案说明）；镜像租约互斥沿用（mirror_lease 不改） |
| 序列化 | 设备表连接块跨 PG/SQLite、跨 Go 结构的往返会不会丢字段或混缺失与零值？ | 显式投影 struct（`config.Target` 无 json tag 的陷阱已钉，§2.2）+ 双后端真实往返断言 + relay 五元组缺失/零值两用例（§7.2） |
| 配置漂移 | hostname≠登记名（roadmap:709 先例）、S8 后幽灵 targets 行 | 判据唯一化（F2）杀 hostname 判法；池只读表 → 幽灵行只剩 WARN；init 不再用 hostname 当缺省名（spec 弃选已定） |
| 兼容/回退 | 新旧版本混跑团队会怎样？ | 新 config 键 omitempty（旧版可读，§2.2）；新派发机写队名→旧镜像机 targets 里有同名则照常订阅、没有则跳过+WARN（不静默）；旧派发机写 `''`→新镜像机走 S7（尽力+WARN）。混跑期为可见降级，不静默——真机清单 §11-6 项验证 |
| 权限/凭据 | 凭据进账本后从哪里漏？ | F8 四读面收口 + 响应体/日志断言；`mirror_lease.holder` 等既有表无凭据 |
| 资源 | 设备表现读会不会把池/warm 循环打成 DB 热点？ | 量级判断：reconcile 10s 一轮 × 机器数级 For 调用，SQLite/PG 本地查询微秒级（未验证，需真机——§11-7）；快照源把每调用一读降为每轮一读（实现可按此优化，语义等价） |

---

## 11. 真机清单（机内 mock 不能顶替的行为验收，全部「未验证，需真机」）

1. **S1 复现现场**：linux-01 本机派发（自名），租约在 macbook-pro → `card wait` 被唤醒、`task_mirrored` 落账（2026-10-18 现场反向）。
2. **S3 三台互见**：linux-01 + macbook-pro + Mac mini 自登记后互免本地配置派发。
3. **relay 形态选路**：mac-02/macbook-pro（relay node 形态）经设备表行的连接方式建隧道连通。
4. **S4 控制台**：机器页增删写表全队可见，凭据任何界面/响应不回显。
5. **S8 导入**：Mac mini（本地 `targets:` 三条：linux-01 直连 + mac-02/macbook-pro relay）升级后首次启动 → 三行入表 + WARN。
6. **混跑过渡**：一台旧版 agentd（无 name 支持）与新版并存时派发+镜像的可见降级形态。
7. **资源量级**：设备表现读在真实三机负载下的池/warm 循环开销。

---

## 12. 决策权限（交回物「偏离与自主决定」的对照基准）

- **实现卡可自行调整**：内部命名（`devices` 表列名、判据函数导出名、测试文件名）、WU 内步骤顺序、§4 未涉及的局部私有函数与遍历顺序、测试组织。
- **必须升级**：F1–F8 任一条语义变化；S6 门控规则（「有无 name:」）改动；P1/P2/P3 拍板结论与本计划建议不一致；扇出结构变化；任何让「新落账一律全队名字」弱化的选择（例如给 `''` 开新写入口）；设备表读面的凭据暴露范围；跨版本兼容承诺（wire 字段删除/改语义）。
- **本计划未自行拍板的**：P1–P3（§0）。其余 §4 各条均给出 spec 依据或本仓先例行号，拍板人推翻时只动对应工作单元。

---

## 13. 自审（五项法定）

1. **故事/步骤覆盖**：S1–S8 全部映射（§3），无孤儿故事；S5 是纯回归锁，无新行为。
2. **待验证假设有动作与阻塞范围**：§11 七项真机全部标注；机内可验部分已在 §6–§8 断言清单；「混跑降级」的机内近似断言（旧判据路径回归）在 WU3。
3. **决策权限写清**：§12。
4. **L3 四件**：子系统清单（§1）、故事映射+闭环（§3）、工作单元+依赖+首故事子卡（§5）、缺陷族审查（§10）。
5. **冻结节有结论**：§9（8 条 + 「无新增对外承诺」说明）；待拍板清单在稿首（§0）。

红线自查：本节点未写实现代码、未派发、未调用 handoff CLI、未起新 executor；所有结论均亲读/亲跑或标「未验证」；依赖输入（spec）已在场且已读。
