# 机器身份收口：自名 + 全队共享设备表

- 状态：草稿 r1，待用户批准（2026-10-18；r1 吸收协调会话约束：身份键稳定、可扩展）
- 级别：**L3**（跨 config / ledger / ledgerstep / ledgermirror / agentd / scheduling / cmd 七个子系统的契约面，并动账本 schema）
- 来源：账本镜像静默失效排查（2026-10-18，linux-01 本机派发、租约在 MacBook-Pro.local 时零 task_mirrored）
- 台账：`docs/superpowers/specs/machine-identity-ledger.md`
- 卡：未建（handoff 仓 L3 一律走卡——批准后先建卡再进 plan）

## 问题陈述

共享账本里存的机器名必须是**全队都能解析成同一台机器**的名字。现状不是：

1. 本机派发把 target 归一成空串 `''` 落账（现状读数：`cmd/card_dispatch.go:175-191`、`internal/agentd/server.go:550-555`、`internal/ledgerstep/dispatch.go:244-257`）。`''` 的含义是「写入者那台机器」，写进共享 PG 以后，读的人不知道是哪台。
2. 账本镜像把 `''` 解释成「租约持有者的本机」（`internal/ledgermirror/mirror.go:240-241`），订阅持有者自己的 store。持有者不是派发机时，事件永远读不到：不报错、无 WARN、健康行正常——**静默失效**。复现与原始读数见台账。
3. 机器没有「自己叫什么」的配置。「本机」现有四套判法，互不一致：`''`/`local`/`本机`/hostname/自指 addr（`internal/scheduling/scheduling.go:182-189`、`internal/agentd/machineupgrade.go:331-337`、`cmd/launcher.go:241-268`、`cmd/card_dispatch.go:175-191`）。hostname 也不等于登记名（`MacBook-Pro.local` 对 `macbook-pro`；roadmap 第 709 行 `mac-02` 对 `sycmdeMacBook-Air.local`）。同类问题此前修过一次：mac-pro 给自己加了 `macbook-pro` 别名。
4. 设备表（`targets:`）只活在每台机器自己的 `config.yaml` 里，名字靠人工在各台之间对齐。这正是 3 的根源。

## 方案

**甲（采纳）：每台机器配置自名，全队共享一张设备表，账本只存全队名字。**

- 有账本 DSN 的 agentd 启动时，把自己（自名 + 对外地址或 relay 节点 + 连接凭据）登记进共享账本的设备表。对端按名从设备表取连接方式，不再读本地 `targets:`。
- 新落账的 target/machine 一律是全队名字，**禁止写 `''`**。路由层仍可以把自名短路成本机 client，那是进程内的事，不进账本。
- 「本机」只有一个判据：名字 == 自名（输入省略 = 自名）。

弃选：

- **乙：镜像按「谁的 store 里有这个 task」分区，`''` 由派发机自己镜像。** 能修镜像，但 `''` 对其它读者（WorkBranch 跨机判断、看板、健康行）照样有歧义——是补丁。用户裁决：彻底清理。
- **自名缺省取 hostname**：hostname ≠ 登记名正是现存漂移的来源。
- **设备表不存凭据 / 全队共享一个 token**：前者每台机器仍要手配对端 token，「不用各处维护」只做到一半；后者改鉴权模型，波及面更大。持有 DSN 的机器本来就能写卡、派发，凭据进账本不新开信任边界（用户裁决）。

## 落位三问

① **读写谁的权威数据**：设备表是独立权威状态（全队机器的名字与连接方式），写者是各机 agentd 的自登记和控制台的增删机器，读者是 target 客户端池、调度准入、镜像、CLI。它落共享账本，归**设备表领域**；按第一条、第五条判，它有独立生命周期与多个消费者，在账本侧单独成领域，不挂在 scheduling 的 registry kind 里冒充载体。自名是本机配置，归 config。
② **谁消费**：同进程内由 target 客户端池消费，池仍是唯一的选路判据（`ledgermirror` 与 `ledgerstep` 经池或注入的自名判据取用，不直接读设备表）。CLI 走账本读接口。不新增跨进程 HTTP 契约；控制台 `POST/DELETE /api/machines` 的语义从「写本地 config」改成「写设备表」，路由不变（第四条）。
③ **同步还是异步**：自登记是启动时同步写；对端读设备表按现有活配置轮询的节奏同步读（池本来就每轮现取）。不引入消息（第十二条：调用方要立即拿到连接方式，不能接受最终一致）。

## 全部必交用户故事

| ID | 故事 | 真实入口 | 可观察结果 | 关键失败呈现 | 验证环境 |
|---|---|---|---|---|---|
| S1 | 在 linux-01 本机 `handoff card dispatch`，镜像租约在 macbook-pro | `handoff card dispatch <id> --step …`（linux-01）；`handoff card wait`（任意机器） | card_tasks.target = `linux-01`；持有者按名经池订阅 linux-01，产生 task_mirrored，card wait 被唤醒 | 持有者取不到 linux-01 的连接：WARN 带 target/task，健康行亮断链，不静默 | 单测：两个镜像实例共享一本账本、持有者≠派发机（竖切）；真机：linux-01 + macbook-pro |
| S2 | 机器配置自名 | `config.yaml` 顶层 `name:`；`handoff init` 生成时询问 | agentd 启动日志打印自名；`handoff machines` 标出本机 | 有 DSN 但缺 `name:`：agentd 照常起，账本相关子系统（镜像、卡派发、自登记、调度）不挂载，日志与控制台给可行动错误「在 config.yaml 设置 name:」 | 单测 + 真机 |
| S3 | 机器自登记、互相可见 | agentd 启动（有 DSN + 自名） | 设备表出现一行：名字、`advertise:` 地址或 relay 节点、凭据；其它机器无需改本地配置即可 `--target <名>` 派发 | 名字已被另一台机器占用：新来者拒绝挂载账本子系统，错误写明冲突的名字（凭本机数据目录里的机器实例 id 区分「同一台重启」与「另一台撞名」） | 单测 + 真机三台 |
| S4 | 控制台与 CLI 看到同一张设备表 | 控制台机器页增删；`handoff machines` | 有 DSN 的机器上增删即写设备表，全队可见；凭据不出现在任何日志与 API 响应里 | 写设备表失败：可行动错误，不回落写本地 config | 单测 + 真机 |
| S5 | 无 DSN 的机器照旧 | 单机 / 纯执行机的 `targets:` | 行为与今天一致 | — | 单测 |
| S6 | 旧别名被拒，不被猜 | `--target local`、carrier.machine=`本机`/hostname、模板 target 为旧别名 | 拒绝并给可行动错误：点名哪条记录、改成什么名字 | — | 单测 |
| S7 | 存量 `''` 不迁移，但不再静默 | 镜像对账遇到 target=`''` 的历史挂账 | 照旧按「持有者本机」尽力订阅（持有者恰是派发机时仍能用）；每条非终态的 `''` 挂账每轮对账打一次 WARN「历史无归属挂账，仅在持有者本机时可镜像」；健康面显示「无归属在飞挂账 N 条」 | — | 单测 |
| S8 | 有 DSN 机器上残留的本地 `targets:` | agentd 启动 | 本地 `targets:` 里**设备表中没有的条目**一次性导入设备表（同名已存在不覆盖），之后本地 `targets:` 被忽略，启动打一次 WARN 提示删除 | 导入时同名但连接方式不同：不覆盖，WARN 列出差异 | 单测 |

## 目标架构与接缝语义

- **设备表领域（账本侧，新）**：权威状态 = 全队机器行（名字唯一、连接方式、凭据、登记者实例 id、更新时间）。
  - **身份键 = 机器名（自名）**，全局唯一、稳定，可被别的子系统按名引用。它**不与传输形态或地址绑定**：一台机器换 IP、换 relay 节点名、在直连与 relay 之间切换，身份不变，只改它那一行的连接方式。连接方式是挂在身份下的属性，不参与判等。
  - **按机器可追加字段**：存储结构要能在不改身份键、不迁移既有行的前提下给每台机器追加属性（不做只放得下名字+一组连接字段的扁平表）。已知后续消费者：B437 共享审批链（按名唯一找到一台机器）、B438 传输层（给每台机器挂端点候选）。**它们的字段本卡不加**，留给各自 spec。写入口只有两个：自登记（upsert 自己那一行，实例 id 不匹配即冲突）和控制台/CLI 的增删。凭据只在读给池的那条路径上出现。
- **target 客户端池（agentd 侧，既有）**：唯一选路判据。有 DSN 时数据源从「活配置 `targets:`」换成「设备表」，无 DSN 时不变。池的「实例判等 = 配置未变」语义保持（`internal/ledgermirror/mirror.go` 的 subscription 注释依赖它）。
- **自名判据（config 侧，新）**：「这个名字是不是我」只有一处定义 = 与 `name:` 相等（输入省略视作自名）。`IsSelfTarget`/`CanonicalTarget`/`IsLocalMachine`/`isLocalMachineName`/`probeLauncherMachine`/`canonicalCLITarget` 收敛到它，多余的删掉。
- **落账层（ledger/ledgerstep，既有）**：新写入的 card_tasks.target、dispatched 快照 Target、镜像 source_target、seat/desktop machine 一律是全队名字；账本写接口拒收 `''`（存量读取照旧兼容）。路由值与身份值分开：传给传输层的仍可是本机短路，落账的是名字。
- **账本镜像（既有）**：命名挂账按「名字 == 自名 → 本机源，否则 → 池」。`''` 只剩 S7 的兼容分支。
- 依赖方向：ledgermirror / ledgerstep / scheduling → 自名判据与池（注入），不反向；设备表领域不依赖 agentd。

## 首故事选择

**S1 + S2 合一做首故事**：配好自名 → 本机派发落 `linux-01` → 持有者按名经池订阅。验证的承重未知是「把落账身份与路由值拆开」会不会撞 B271 的本机路径（WorkBranch、stepTransport、FrozenTarget）。它不依赖设备表：今天各机的 `targets:` 已有 `linux-01`/`macbook-pro`，名字是对齐的，所以首故事能先止住线上静默失效。S3/S4/S5/S8（设备表）与 S6/S7 在首故事合入后跟进。是否扇出子卡由 plan 按工作量判。

## 实现决定

- 用户可见名字：配置键 `name:`（自名）、`advertise:`（对外直连地址，形如 `100.81.114.119:7777`；relay 形态沿用既有 `relay.node`）；控制台与 `handoff machines` 标「本机」。
- 自名格式：与现有登记名同一词法（非空、无空白、不含 `/`）；`local`、`本机` 作保留字，不可用作自名。
- 有 DSN 无自名时只是**账本子系统不挂载**，不让 agentd 进程退出（用户裁决）。
- 历史 `''` 不迁移（用户裁决）；B271 对历史 WorkBranch 的「缺字段视作本机」口径保留，只对存量生效。
- 凭据进账本（用户裁决）：日志、控制台 API 响应、`handoff machines` 输出一律不带凭据值，只标「已配置」。
- 机器实例 id 存本机数据目录，首次登记时生成；精确文件名与格式归 plan。

## 测试决定

- 竖切 S1：在 ledgermirror 包内起两个镜像实例共享一本账本，持有者≠派发机，挂账 target 为派发机名字，断言 task_mirrored 落账。排查期的复现测试（target `''` 版，红，原始读数见台账）按本 spec 改写为命名版；`''` 版的语义转归 S7 的断言。
- 核心方法：自名判据（表驱动，含保留字、省略、他名）；设备表自登记的冲突判定（同实例重登 vs 撞名）；账本写接口拒收 `''`。
- S2/S3/S8 经 agentd 组装点测试（`cmd/agentd.go` 的账本挂载路径）。
- 真机：linux-01 + macbook-pro + 本机（Mac mini），复现 2026-10-18 现场：租约在 macbook-pro 时 linux-01 本机派发、card wait 被唤醒。

## Out of Scope

- **永不做**：自名缺省取 hostname；`local`/`本机`/hostname 的输入别名；把 `''` 当全队身份。
- **本期不做、后续要做**（落 `docs/roadmap.md`）：
  - 存量 `''` 挂账的归属回填（用户裁决本期不迁移；若将来要做，走「属主认领」：各机认领本机 store 里有的 task）。
  - relay 形态机器经控制台添加（`internal/agentd/machines.go:211-212` 已记为另卡）。
  - 设备表凭据轮换 / 吊销流程。
  - B398 OOS 遗留：被 target 归一改写过的冻结绑定盘点（roadmap 756-758）——本期改了归一，plan 需复核是否顺带覆盖。

## 备注

- 本 spec 由镜像缺陷排查升级而来：排查已定根因（见问题陈述 1–2），修法经用户裁决从「镜像侧修」升为「身份收口」。
