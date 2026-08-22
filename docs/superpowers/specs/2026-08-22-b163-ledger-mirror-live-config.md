# B163：跨机镜像跟随活配置（账本镜像补齐 + 机器解析统一到池）

- **级别与档位**：L2（单子系统，不动跨进程 wire 契约层）→ plan → implement → review → acceptance → finish
- **日期**：2026-08-22
- **来源**：backlog B163（原记「Manager 的 Targets 运行期配置未热读」）

## 1. 问题陈述

B163 立条时记的是任务镜像（`internal/agentd/mirror.go`）持 `NewMirror` 那一刻的静态
`cfg.Targets`：控制台新增开发机后要重启 agentd 才被镜像。**这一半已经做掉了**——
`2fb62e6ad`（08-20，target 客户端池那轮顺手做的）把机器判据整个换成活配置：

- `internal/agentd/mirror.go:98 machineNames` 判据只剩池（`pool.Names()`，池每次现读配置快照）；
  该文件现在零处 `m.cfg`；
- `internal/agentd/mirror.go:122 ensureSnapshotLoops` 让运行期新增的机器也有快照消费者；
- 回归判据在 `internal/agentd/mirror_pool_test.go:14 TestMirrorSeesTargetsAddedAtRuntime`；
- agentd 其余全部 `Targets` 读点均走活快照 `s.conf()`（`forward.go:55`、`forward_ws.go:38`、
  `machines.go:43/111/189`、`machineadmin.go:69`、`machineupgrade.go:125`、`ledgerapi.go:196`）。

但本条的标题需求——**「跨机镜像仍等 agentd 重启才认识它」——对另一个镜像仍然字面成立**：
账本镜像子系统（`internal/ledgermirror`）自己另起了一套机器解析，没走池。由此派生四条缺陷，
根因同一个：

| # | 现状读数 | 后果 |
|---|---|---|
| ① 启动闸 | `cmd/agentd.go:277` `if len(cfg.Targets) > 0` 才挂载账本镜像 | 零机器起的 agentd + 控制台加机器 → 账本镜像到重启才活。**同一个闸在任务镜像上已被显式拆掉**（`cmd/agentd.go:238-240` 注释写明「留着会让控制台新增的第一台机器永远等不到镜像」），账本这个被漏下 |
| ② 在飞订阅持旧凭据 | `internal/ledgermirror/mirror.go:203` 起订时按值捕获 `config.Target`，`mirror.go:259` 的重连循环一直用那份 `target.Addr/Token` | 控制台改了机器地址或令牌，在飞订阅拿旧凭据无限重连到任务归档或进程退出。任务镜像靠每轮 `pool.For` 规避了这个形状 |
| ③ 配置源不统一 | `cmd/agentd.go:279-287` 注入的 targets 函数每轮 `config.Load(p)` **重读磁盘**，读失败时静默回落启动快照（只 Warn） | 与 `Server.conf()`（`internal/agentd/server.go:291`，写时复制的原子快照）并存两套真相；磁盘读失败窗口内看到的是启动时的旧配置 |
| ④ relay 形态不可用 | `internal/ledgermirror/mirror.go:37 DefaultSource` 恒走 `client.New(addr, token)`，只认直连；而 relay 形态的 target 按契约 `Addr` 恒为空（`internal/config/config.go:236` relay 与 addr 互斥），`client.New("")` 得到的是被毒化的 client（`internal/client/client.go:198 initErr`） | **relay 形态的执行机，账本镜像永远连不上**，表象只是日志里无穷的「订阅断开，退避重连」。本机配置里 `linux-01` 正是 relay 形态且 `ledger.enabled=true`，缺陷是活的（当前 `card_tasks` 为空，尚未显形） |

另有一条同族、且**用户可见**的：`internal/agentd/tasksfanout.go:40` 把 `ListMirrorTasks()`
全量摊进任务汇总，不按当前登记机器过滤——控制台删掉一台机器后，它的任务仍留在列表里。

以上均为 2026-08-22 在本工作树的现状读数，由 contract/plan 节点对当轮工作树复核。

## 2. 方案

### 选定：账本镜像的机器解析统一到既有的 target 客户端池

账本镜像不再自己持有「机器名 → 地址/令牌」这张表，改为向注入的**机器源**要两样东西：
当前机器名清单、按名取一个可用客户端。生产实现就是 agentd 已有的 target 客户端池
（`internal/targetclient/pool.go`，与任务镜像**共用同一个实例**——两个池等于两套 relay 隧道）；
测试实现是内存 fake。四条缺陷由此一并消失：

- ① 启动闸拆掉，账本镜像与任务镜像一样恒挂载，无机器时对账空转；
- ② 池的既有语义是「target 值未变返回同一实例，变了关旧隧道重建」
  （`internal/targetclient/pool.go:114-127`），于是「客户端实例变了」就是「机器配置变了」的判据；
- ③ 池的配置来源就是 `Server.conf()` 的原子快照，磁盘重读消失；
- ④ 池按 target 形态选路（`internal/targetclient/targetclient.go:40-54`），relay 自动可用。

代价：账本镜像的事件源注入点从「地址 + 令牌」上移到「客户端」，现有三处测试夹具
（`internal/ledgermirror/mirror_test.go:57/89/113`）要跟着改；账本镜像新增一个对
`targetclient` 的依赖方向（消费既有 API，不改它）。

### 弃选 A：四个独立补丁，保持事件源现签名

拆启动闸 + 对账存 target 原值比对重订 + targets 改走 `Server.conf()`，relay 另记一条。
弃因：两套机器解析继续并存，而 relay 那条**最终仍然只能走池**——单次构造的
`targetclient.New` 在包注释里被明令禁止用于常驻场景（`internal/targetclient/targetclient.go:71`
「常驻场景不要用它——每次调用都会新建一条 relay 隧道」）。等于把同一件事拆成两轮做。

### 弃选 B：只在重连前现取机器配置（不主动断连）

改动最小，但长订阅**连接不断就永不生效**，而连接不断正是常态——等于没修。

### 删除侧的取舍（用户裁决）

只做**读时过滤**：任务汇总按当前登记机器过滤镜像任务，判据取活配置，与本条同一条哲学，
且修掉用户可见的错误数据。goroutine 与遗留行属不可见的资源残留，另记（见 Out of Scope）。

## 3. 用户故事

1. 我在全新装的协调者机上起了 agentd（还没登记任何开发机），随后在控制台加了第一台机器；
   **不重启**，它的挂账任务事件就开始进账本。
2. 我在控制台把某台开发机的地址（或令牌）改了；**不重启**，在飞的账本订阅在下一轮对账内
   切到新凭据，从水位续拉，事件不重不漏。
3. 我的开发机是 relay 形态（无 addr）；它的挂账任务事件照样进账本，和直连机器无差别。
4. 我在控制台删掉一台开发机；它的任务立刻从任务列表里消失，不必重启 agentd。
5. agentd 读不到磁盘配置时，账本镜像看到的机器清单与控制台/CLI 看到的**永远是同一份**，
   不会出现「控制台显示三台、镜像只认两台」。

## 4. 实现决定

1. **机器源是账本镜像的唯一机器判据**：清单与客户端都从它取，包内不再出现 `config.Target`
   的地址/令牌字段读取。
2. **凭据热换语义 = 变更即退订重订**（用户裁决）：对账每轮按名取客户端；与该订阅当轮持有的
   不是同一个实例即判定配置已变，取消旧订阅并重起，新订阅从本机水位续拉。改地址后**最迟一个
   对账周期**生效（现状 tick 10s，`internal/ledgermirror/mirror.go:74`）。
3. **恒挂载**：账本镜像的启动不再以「启动时有无已登记机器」为条件；账本域总开关
   （`ledger.enabled`）仍是唯一的挂不挂载判据。
4. **relay 由选路自动获得**：包内不做任何形态判断，也不再有 addr 非空的隐含假设。
5. **共用同一个池实例**：与任务镜像同一个，不新建。
6. **读时过滤**：任务汇总里镜像任务按当前登记机器过滤；库里的遗留行不删（删库是破坏性操作，
   且读时判据已经正确）。
7. **停机次序不变**：订阅回调在写账本库，`Stop` 必须先于账本库 `Close`（现状硬约束，
   `cmd/agentd.go:255-259` 注释）。恒挂载后这条更要守——空 targets 也有循环在跑。

## 5. 测试决定（接缝清单）

**主缝一个：账本镜像的机器源注入点**（fake 可控 Names/For 与订阅行为，全程不碰网络）。
四条判据都落在这一个缝上：

1. 起于零机器，运行期出现机器 → 起订（覆盖①的语义）；
2. 机器配置变更（fake 返回新客户端实例）→ 退订重订，且新订阅从水位续拉（覆盖②）；
3. 机器消失 → 退订（既有语义，防回归）；
4. 机器为 relay 形态（fake 返回 relay 客户端）→ 订阅照常发起，无 addr 假设（覆盖④）。

**次缝：任务汇总 handler 的既有测试缝**——删掉机器后镜像任务不再出现在列表里（覆盖读时过滤）。

**③ 与①的布线那一行（`cmd/agentd.go`）没有单测缝**：cmd 层是组装点，抽一层只为可测不划算。
它由代码审查 + 真机验收把关，plan 里必须显式写出这一点，不得假装被单测覆盖。

**真机验收必须留在本地做，不得随 plan 派发**：它要驱动 handoff 自身（起独立 agentd 实例、
造挂账任务、控制台改机器），与执行纪律块「不要调用 handoff CLI、不要起新的 executor 进程」
直接冲突。取证要点：独立 DataDir 放 `/tmp` 短路径（PTY socket 路径有 104 字节上限）、
独立端口、配置里只放 relay 形态的机器，红绿两侧同一份探针。

## 6. Out of Scope

显式不做，各自的理由：

- **任务镜像的静态 cfg**：已由 `2fb62e6ad` 完成，本轮不重做（backlog 变更痕迹里记明）。
- **已删机器的 Mirror `loops`/`ring` 反向清理**：每台被删机器留一个阻塞在门铃上的空转
  goroutine 到进程退出，不可见、不耗 CPU；取消与 `wg` 的时序另有并发语义要验，另记 idea。
- **已删机器的 `mirror_tasks` 遗留行清理**：读时过滤后不可见；删行是破坏性操作，
  要另立判据（谁删、何时删、误删怎么恢复）。
- **账本镜像的 lease/holder 与健康表语义**：本轮只改机器解析，不动多协调者抢占与健康行语义。
- **把池引入 CLI 单次命令路径**：CLI 是一次性进程，池的价值（隧道复用）在那里不成立。
- **`internal/executor/{codex,grok}` 的 WS 拨号**：与本条无关（B161 已记明留待）。
- **relay 真机验收之外的多机组合**（同时直连 + relay + 删改并发）：单测覆盖组合，
  真机只验 relay 与热改两条主路。

## 7. 备注

- **图覆盖债**：`Manager.conf`（实为 `Server.conf`，backlog 原文记错了名字）、
  `Mirror.reconcile`、`DefaultSource` 三个符号在 `codegraph/` 未命中——账本镜像
  子系统（`internal/ledgermirror`）整体不在图内，本次全部回落读码。留待后续重扫消化。
- **backlog 记账**：B163 保号不新开，变更痕迹里写明「mirror.go 那半由 `2fb62e6ad` 顺手落地，
  本轮做账本镜像四条 + 读时过滤」。
- **本机现状对本条的意义**：`~/.handoff/config.yaml` 里 `linux-01` 是 relay 形态、
  `mac-02` 是直连、`ledger.enabled: true`——真机验收的两种形态都现成，不必造环境。
