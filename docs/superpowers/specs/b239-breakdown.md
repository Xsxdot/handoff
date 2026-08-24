# Breakdown：B239 把「认领」一分为二——归属锁（人尺度）+ 运行锁（运行尺度）（L3 轻档）

> 状态：**出稿待拍板**（2026-08-25，handoff executor 单上下文出稿；本稿全部产出是提案，扇出归协调者）。
> 上游：spec `docs/superpowers/specs/2026-08-24-b239-claim-lock-split.md`（头部 :4「状态：**已批准**——2026-08-24 用户批准」，开工核对一致，无需回写）；
> 契约 `docs/superpowers/specs/b239-contract.md`（头部「contract 轮冻结稿（2026-08-25）……随本提交冻结……交棒：breakdown」，冻结提交 c7565808，开工核对一致）。
> 档位：spec 选档复核冻死**轻档**——契约冻结照做，**实现归一轮**：下列单元由单个 implement 执行者按 DAG 序贯消化，不做跨执行器扇出。
> 台账：docs/ledger-b239-breakdown.md（边干边追加）。

---

## 待拍板岔口（集中清单，拍板者按此裁决）

> **裁决（协调者）**：＿＿＿＿＿＿（拍板后随本稿一并提交入库）

**岔口一：看板 conflict 徽标的运行锁取数落点**
徽标判据（断言 38）要「存在未过期运行锁且最新 task 态 failed」。列表页一次画 N 张卡。
- 方案 A：`handleCardsList` 里 `AllRunLocks()` 一次拉全量，内存建 `card_id→RunLock` join。查询次数 O(1)；代价是多一个中间 map，且拉回的行含大量与当前列表无关的卡。
- 方案 B：每卡 `RunLockOf(view.ID)` 逐卡查。代码直白、局部性好；代价 N 次往返，看板大时放大数据库压力。
- 取舍实质：列表页查询次数 vs 代码直白度。出稿倾向 A（账本是本机 SQLite/PG，N 卡同页常见几十到几百）。

**岔口二：`StepRunner.RunHolder` 的精确构造形态**（agentd 每次 startCardStep 时生成；契约钉了边界——全局唯一、含机器线索、空值拒绝放行——未钉格式）
- 方案 A：`fmt.Sprintf("run:%s#%d#%d", host, pid, time.Now().UnixNano())`。人读取证直接看出机器与进程；唯一性靠纳秒+pid；同机重启后 pid 复用概率被纳秒压掉。
- 方案 B：crypto/rand UUID。全局唯一最省心；代价是人读不出任何线索，取证要另查日志。
- 方案 C：host + agentd 启动时刻 + 进程内原子序号。严格单调、可排序；代价要多一块进程内状态，重启丢序号但启动时刻兜底。
- 取舍实质：人读取证 vs 纯唯一性 vs 实现成本。出稿倾向 A（运行锁 holder 的主要读者是报文里的人，不是程序）。无论哪种，「同一轮编排内 holder 恒定」是实现硬约束（续租/释放都拿它当键）。

**岔口三：续租循环的驱动源与可测性路径**（断言 30 要求注入时钟推进断言、不真等 5 分钟）
- 方案 A：续租间隔做成可注入字段（生产默认常量 2 分钟），循环用真实 `time.Ticker`；测试注入毫秒级间隔真实短暂 sleep（≤100ms），过期判定仍走 Store 假时钟（断言 15）。循环体最简单；代价是测试引入真实 sleep（量级无害，违背「不真等」的字面精神但不违背其本意——禁的是等 5 分钟）。
- 方案 B：循环高频 tick（如每秒）比对 `s.timeNow()` 是否到达下次续租时刻，完全由注入时钟驱动。测试推进假时钟即触发续租，零真实等待；代价是循环体复杂、有空转 tick，且「每秒醒来」本身是一种新计时行为要自证不泄漏。
- 取舍实质：机制简单 vs 测试零真实等待。两案都必须满足：随回合 ctx 取消而停、不留泄漏 goroutine（spec 实现决定 3）；断言 30 的判据落在**库行 expires_at 推进**上，不许落在 runner 内存字段上（防假绿，见缺陷族·假红假绿）。

---

## 一、触及子系统清单

子系统 id 与类型以 `codegraph/best.json` 为准（纪律：有图以图为准）。**边界澄清一**（详见 §二）：spec/contract 行文的「d_coordination（internal/agentd 与 cmd）」在图上不存在该域——图把它拆成 `d_gateway`（控制门面=internal/agentd，type=boundary）与 `d_cli`（协调者命令面=cmd，type=logic）；本拆解按图立 id。

| 子系统 | 图类型 | 本次触及 | 有界文件集（生产文件 / 测试文件） |
|---|---|---|---|
| `d_ledger` 账本域 | logic（逻辑型） | 缝 1 全部（归属四操作语义改造 + 运行锁五方法实现）、缝 2（编排入口改造） | internal/ledger/move.go、tasks.go、runlock.go（store.go/events.go 本轮预计不动，DDL 已进）／ move_test.go、tasks_test.go、runlock_test.go（新）、ledgerstep/runner.go、node.go（仅当共享 haltForHuman 需小改）、runner_test.go |
| `d_gateway` 控制门面 | boundary（边界型） | agentd 装配 RunHolder、徽标判据替换、fallback actor 收敛 host 档 | internal/agentd/cardstep.go、ledgerapi.go ＋ 二者的既有测试文件 |
| `d_cli` 协调者命令面 | logic（逻辑型） | 四条 CLI 变更 + ledgerSession() 删除 | cmd/card_driver.go、card_dispatch.go、card_node.go、ledgercli.go ／ card_dispatch_test.go |

`web/src` **零改动**（契约 §1.2：payload 加 reason 后 web 泛化渲染直读，不新增前端代码）。

### 1.1 派卡资格四条逐核（架构法第一条）

| 子系统 | ①有界文件集 | ②契约面可枚举 | ③依赖 DAG 无环 | ④类型标注 | 结论 |
|---|---|---|---|---|---|
| `d_ledger` | 上表 5 生产 + 4 测试文件，圈得出 | 冻结物即枚举：契约 §2.1–§2.3 签名与规则、§3 断言 1–32 | U0/U1 → U2，无回边 | 逻辑型 | 通过 |
| `d_gateway` | 2 生产文件 + 既有测试，圈得出（61 文件平铺包的竖切欠账仍在实例化清单 §6，本次只碰两个既有文件，不预支还债） | 徽标判据一条（§2.5）、RunHolder 装配一处、fallback host 档一行 | U1 → U3 且 U2 → U3，无反向 | 边界型（图类型；本卡接缝对面仍是自有 d_ledger，行为断言机内可闭环，真正外部现实见真机清单） | 通过 |
| `d_cli` | 4 生产 + 1 测试文件，圈得出 | 契约 §2.4 表四行 + 断言 33–37、39 | U0 → U4，无反向 | 逻辑型 | 通过 |

### 1.2 架构法第三条判据（竖切还债核查）

- `internal/agentd`：单包 ≥40 文件且无子包判据命中（61 文件）——按实例化清单 §5.1 只要求显式回答：本次能圈出有界文件集（cardstep.go + ledgerapi.go 两文件），**不插竖切卡**；家族欠账记在清单 §6 原处。
- `internal/ledger`、`internal/ledgerstep`、`cmd`：均远低于尺寸红线；cmd 是正当扁平（一文件一子命令）。不命中。

---

## 二、契约增量核对

对照冻结物逐条核对（b239-contract.md §2–§6 + target.json/diffs 视图 + Ticket 0 骨架）：

| 冻结条目 | 本拆解的对应 | 越界？ |
|---|---|---|
| §2.1 归属锁面（ClaimCard 收窄/ClaimDriver 删除/ReleaseCard 反转/TakeoverCard 不动/pid 消失/web fallback host 档） | U0 承接前三项；TakeoverCard 不动照守；pid 消失跨 U0（签名）+U2（Session 语义）+U4（调用点迁移+删 ledgerSession）；host 档归 U3 | 否 |
| §2.2 运行锁面五方法 + card_run_locks 存储 + TTL 常量值（5min/2min） | U1 承接全部；TTL **位置**契约留给 plan，路由上无 plan 节点，本稿钉定：internal/ledger/runlock.go 包级常量（紧邻使用处，无第二种合理位置）——拍板时可否决 | 否 |
| §2.3 编排缝法定顺序六步 + 落卡同形要求 | U2 承接；落卡三件套复用 `internal/ledger/events.go#Store.AddComment` 与 `internal/ledger/events.go#Store.MarkNeedsHuman` 既有形状（:163/:264 已亲读），署名统一 `node:<请求的节点名>`（Run 参数恒可得，含 nodeFor 失败场景） | 否 |
| §2.4 CLI 四条变更 | U4 承接 | 否 |
| §2.5 徽标判据 + 在飞 map 降格保留 + Actor 语义收窄 + RunHolder 现算注入 | U3 承接；取数落点为岔口一 | 否 |
| §2.6 依赖方向（d_gateway→d_ledger entries=["ledger.Store","ledgerstep.StepRunner"]） | 三张子卡触 d_ledger 只经 Store 门面与 StepRunner 入口，target.json 零改动；实现轮若发现需要新 entry 即退回 contract | 否 |
| §3 断言 1–39 | 全数分配进各单元验收（见各卡③栏），原判据无一放松、无一删除 | 否 |
| §4 Ticket 0 欠账七条 | 第 1 条→U0；第 2 条→U1；第 3 条→U2；第 4 条→U4；第 5、6 条→U3；第 7 条（抢占 payload 金样本）→U1 测试（事件在缝 1 落账，fixture 锁 from/to/reason 三键） | 否 |
| §6 可执行冻结（金样本无命中、目标图、分支视图） | 本拆解零图改动、零 wire 新面；U1 的 payload fixture 属实现 §4.7 欠账销账，非新增契约面 | 否 |

**新接缝需求**：无。两条缝都是契约冻结的既有缝，本拆解没有发明第三条。

**上游状态位**：spec「已批准」（:4）、contract「随本提交冻结」（:3-7）均已亲读核对，失真项为零。

**边界澄清（核对中做出，结论均为「不退回 contract」，需回写契约修订记录）**：

- **澄清一（子系统命名）**：spec §契约语义与 contract §6 行文的 `d_coordination` 不是 best.json/target.json 里的域 id——图上该职责分属 `d_gateway`（internal/agentd，boundary）与 `d_cli`（cmd，logic）。有图以图为准，依赖方向不变（两者各自单向指向 d_ledger，entries 与 §6 冻结一致）。此澄清不改任何冻结语义，只正名；修订行随本拆解稿落入契约文档。
- **澄清二（web fallback 收窄范围）**：`internal/agentd/ledgerapi.go` 中 `web:<r.RemoteAddr>` 共七处（:89/:367/:386/:446/:522/:542/:693，本轮 grep 实测），其中只有 **:446**（CardStepReq 的 fallback actor，会变成锁身份）随本卡收敛为 host 档；其余六处是事件审计署名，带端口只影响取证文本不影响身份判定，**不在本卡范围**。把这条写死，防实现轮顺手「全仓收敛」越出冻结面。

---

## 三、子卡清单 + 依赖 DAG

```
U0 归属锁面 ──┬──► U2 编排缝 ──► U3 agentd 装配与徽标
U1 运行锁面 ──┤                        │
              └──► U4 CLI 面 ─────────┴──► 收尾核验（全量 build/vet/变异复验）
```

单轮实现的**序贯执行序**：U0 → U1 → U2 → U3 → U4 → 收尾核验（U0 与 U1 无相互依赖，先后只为评审方便）。轻档不扇出；若协调者想改档并行，那是拍板权，不在本稿。

### U0 缝 1a：归属锁面改造（断言 1–12）

- **①契约引用**：contract §2.1 全部；§3 断言 1–12；§5.1/§5.2 拍板记录；Ticket 0 欠账第 1 条。
- **②意图与为什么**：归属锁今天和状态 CAS 捆在一个事务里、非持有者释放静默假成功、双符号等价入口必然后漂移——这三点是 B213/B237/B239 的共同根因面。先改归属面，是因为 U2/U4 都消费它的新签名与新语义；它不改任何编排时序，是纯账本面改造，机内可闭环。
- **③验收（全部行为化，引用冻结断言号）**：
  - 断言 1–12 逐条落地为 `internal/ledger` 测试（SQLite 方言即可，PG 由 store 抽象共用逻辑）；
  - 变异复验三点照契约执行：「非持有者释放返回成功」改回去 → 断言 11 红；归属加过期判据 → 断言 4 红；认领转状态改回去 → 断言 6 红；
  - `go test ./internal/ledger/...` 全绿；`go build ./...` 不因签名收窄残留旧调用方（此时 runner.go 还没改——U0 内先用最小适配让编译过还是先改 runner？→ 不允许：**U0 与 U2 必须同一轮连续完成**，U0 落地后 runner.go:97 的 ClaimDriver 调用点同步改为 `ClaimCard(cardID, r.Session)` 以保编译，行为改造留 U2；这是编译完整性例外，不是提前做 U2）。
- **④入口指针**：`internal/ledger/move.go#Store.ClaimCard`（:145 现签名四参）、`internal/ledger/move.go#Store.ReleaseCard`（:175，no-op 分支 :188-191）、`internal/ledger/tasks.go#Store.ClaimDriver`（:94-117 删除目标）、`internal/ledger/tasks.go#Store.TakeoverCard`（:122-145 不动）、moveCardTx（终态拒绝现状在 :32-35，解耦后须显式补回）；既有测试 move_test.go、tasks_test.go。
- **有界文件集**：move.go、tasks.go + move_test.go、tasks_test.go（+runner.go 一行编译适配）。圈得出。

### U1 缝 1b：运行锁面实现（断言 13–23）

- **①契约引用**：contract §2.2 全部；§3 断言 13–23；§5.3 拍板记录（首取不落事件、抢占复用 driver_takeover+reason）；§4 欠账第 2、7 条。
- **②意图与为什么**：运行尺度占用需要自己的寿命（租期+续租+随回合消亡），权威必须在账本而不是 agentd 内存——这是 story 1/2/10 与 B225 恢复落点的共同前提。空壳已在，本轮填肉并配齐缝 1 测试；它是 U2 续租循环与 U3 徽标的直接前置。
- **③验收（行为化）**：
  - 断言 13–23 逐条落地为 runlock_test.go；过期判定一律走 `internal/ledger/store.go#Store.timeNow` 注入时钟（推时间不真等）；
  - 断言 16 的抢占事件加 **payload 金样本断言**（from/to/reason 三键，缺一键或多余键即红）——销契约 §4.7 欠账；
  - TTL 常量落 runlock.go 包级（值 5min/2min 已冻，位置见 §二核对行）；
  - `go test ./internal/ledger/ -run TestRunLock` 全绿。
- **④入口指针**：internal/ledger/runlock.go（Ticket 0 空壳 :15-51）、internal/ledger/store.go ensureSchema 两方言 DDL（PG :252-256、SQLite :318-322，已进，勿重写）、`internal/ledger/mirror.go#Store.AcquireMirrorLease`（:92-127 单行 CAS 先例，节奏参照）。
- **有界文件集**：runlock.go + runlock_test.go（新）。圈得出。

### U2 缝 2：编排入口改造（断言 24–32）

- **①契约引用**：contract §2.3 全部（法定顺序六步、落卡同形要求、写权边界允许/禁止清单）；§3 断言 24–32；§5.2 拍板记录；spec 失败可见性清单。
- **②意图与为什么**：编排入口是唯一在 RunOnce 保护之外的失败面（B239 伤害面一半的来源），也是两种锁的交汇点：入口取运行锁、认领持久归属、回合结束只退运行锁、defer ReleaseCard 删除。归属被拒也并入落卡（契约明文扩展，漏掉等于留半个静默面）。
- **③验收（行为化）**：
  - 断言 24–28、29、32 逐条落地 runner_test.go；断言 30 的判据落在**库行 expires_at 推进**上（注入时钟推进前后各读一次 RunLockOf 比较），不许断言 runner 内存字段；
  - 断言 31 写权 gate：移列、RecordReviewVerdict、AttachFile、Mark/ClearNeedsHuman 四个写点各有**独立反面断言**（Renew 返回 false 后逐一验证不发生），说明 comment 恰一条（EvComment 计数）；
  - 反面口子上账：AcquireRunLock acquired=false 不算错误（返回现存行），编排必须显式分支转落卡——若实现漏分支，断言 26 红；这是「第二返回值必须被消费」的唯一机内执法，写明依赖它；
  - 落卡动作自身失败的传播契约：AddComment/MarkNeedsHuman 任一失败 → 错误原样上抛（Run 返回错误，agentd 日志第二现场），不吞、不再补写；
  - `go test ./internal/ledgerstep/...` 全绿。
- **④入口指针**：`internal/ledgerstep/runner.go#StepRunner.Run`（:60 起；现状 nodeFor 失败 ：63-67 直接 return、Session 空 ：92-95 直接 return、ClaimDriver :97、defer ReleaseCard :102-108）、node.go 的 haltForHuman（:66-80 落卡三件套形状）、`internal/ledgerstep/node.go#NodeStep.RunOnce`（:157-178 内部异常保护范围）、cmd/card_wait.go:98（「card wait 看得见」判据通道）。
- **有界文件集**：runner.go、runner_test.go（node.go 仅当 haltForHuman 需要跨结构体复用时小改，预期不动）。圈得出。

### U3 agentd 装配与徽标（断言 38 + RunHolder 装配 + host 档）

- **①契约引用**：contract §2.5 全部；§1.3 末行（Actor 语义收窄）；§2.1 web fallback host 档；§3 断言 38；Ticket 0 欠账第 5、6 条。
- **②意图与为什么**：agentd 从「谁在跑」的真相源降格为消费者：在飞 map 保留为快速去重，权威判定让位给账本运行锁；每次装配注入 RunHolder（岔口二定形态），徽标改判运行锁——状态列从此与互斥/冲突指示无关（story 9）。
- **③验收（行为化；图类型 boundary 但本卡接缝对面是自有 d_ledger，以下均可机内闭环）**：
  - 断言 38：构造「运行锁未过期 + 最新 task 态 failed」与「无运行锁 + 状态进行中」两组夹具，前者 conflict=true 后者 false，且状态列不再参与判定（变异：把 StatusDoing 判据加回去 → 红）；
  - 装配级反面断言：startCardStep 构造的 StepRunner.RunHolder 非空（防「机内测试手工塞 holder 而生产漏传」的假绿温床）；
  - AllRunLocks 读取失败 → 徽标退化 false + 日志告警，列表不 500（对齐 :180-182 既有工单推导失败的退化形态）；
  - fallback actor：无 actor 的 CardStepReq 归一后形如 `web:<host>` 且不含端口；
  - `go test ./internal/agentd/...` 全绿。
- **④入口指针**：`internal/agentd/cardstep.go#Server.startCardStep`（:41-64，生产唯一 StepRunner 构造点 :45）、internal/agentd/ledgerapi.go 徽标块（:186-196）、fallback（:446）、错误映射 ledgerErr（:65-76，409 通路已有）。
- **有界文件集**：cardstep.go、ledgerapi.go + 各自既有测试文件。圈得出。

### U4 CLI 面（断言 33–37、39）

- **①契约引用**：contract §2.4 表全部；§3 断言 33–37、39；§2.1 身份契约（ledgerActor 档）。
- **②意图与为什么**：对人契约面的两条可观察变更都在这里（release 退出语义反转【变更点一】、裸 dispatch 不挪列+守卫改判【变更点二】），它们是 spec 用户故事 5/6/7/8 的直接载体；ledgerSession() 删除钉死 pid 从归属链路消失。
- **③验收（行为化）**：
  - 断言 33–37 逐条落地（CLI 级测试或经 ledger 层等效断言 + CLI 参数传递单测）；
  - 断言 39 双口径：生产代码 grep `ledgerSession` 零命中；行为断言「同 owner 不同 pid 重入幂等」（U0 断言 5 的 CLI 面）；
  - review 对账清单提醒：变更点一、二是契约 §2.4 点名的可观察行为变更，review 节点须点名核（本稿只在交棒里提醒，不占验收）；
  - `go build ./... && go vet ./cmd/...` 全绿。
- **④入口指针**：cmd/card_driver.go:17/:41（ledgerSession 调用点）、cmd/card_dispatch.go:208-210（守卫）/213（认领）/216-220（补读报文，随守卫改判一并调整）/236-242（回滚简化：删 MoveCard 回退，只退归属）、cmd/card_node.go:40（--step Actor）、`cmd/ledgercli.go#ledgerSession`（:60-62 删除目标）、cmd/card_dispatch_test.go:460-461（wire actor==ledgerSession 的旧断言随迁）。
- **有界文件集**：card_driver.go、card_dispatch.go、card_node.go、ledgercli.go + card_dispatch_test.go。圈得出。

### 收尾核验（不出子卡，单轮实现的最后一站）

`go build ./...`、`go vet ./internal/ledger ./internal/ledgerstep ./cmd ./internal/agentd`、`gofmt -l` 触碰文件、`go test ./internal/ledger/... ./internal/ledgerstep/... ./internal/agentd/... ./cmd/...` 全绿；契约 §3 变异复验三点逐条变红记录在案。

---

## 四、缺陷族对抗审查（项目清单=通用五族 + 序列化边界 + 枚举白名单 + webview 候选族 + 承重安全属性）

**1. 生命周期/状态机中断**
- agentd 编排中崩溃：运行锁留过期行，TTL 到期自愈（story 1）；归属留在人名下，takeover/release 显式处置且不再假成功——收尾责任方是**人与 TTL**，无自动回收，这是 8-23 裁决的设计而非遗漏。孤儿资源只有一种：一条过期运行锁行，自愈机制=TTL，可被新 Acquire 覆盖（断言 15）。
- 续租 goroutine：随 ctx 取消停（frozen）；断言 28/29 验证回合结束后锁行消失，间接证明循环已停。
- mutate 中途崩溃：取得/续租/释放/抢占各为单事务（store.mutate 串行），覆盖写与抢占事件同事务，无半截态。
- U3 新增读面失败路径：AllRunLocks 出错必须退化徽标 false 而不是 500 整个卡片列表——已写进 U3 验收（对齐既有 :180-182 退化形态）。
- 结论：有已识别风险面，全部落在可见机制（TTL 自愈 / 显式转移 / 单事务原子性 / 徽标退化），无静默孤儿。

**2. 静默失败/误导报错**
- 本卡的**目的本身就是修这一族**：release 假成功→可见失败（断言 11/33）；编排入口静默 return→落卡三件套（断言 24–27）；被挡报文从「可能被并发抢先」变为「谁在跑/哪个节点/租期到几点」（断言 14/26 的报文素材）。
- 残余口子一：AcquireRunLock 的 acquired=false 不算错误，若编排漏分支即回到静默——防线=断言 26 + U2 验收的反面口子上账。
- 残余口子二：落卡动作自身失败（AddComment 成功、MarkNeedsHuman 失败）会留半截痕迹——传播契约已定：原样上抛，agentd.log 第二现场，不吞不补写（U2 验收）。
- 结论：有风险，三条残余口子各有明确防线与归属单元。

**3. 跨平台假设**
- 新增面全是包内 Go 逻辑 + SQL；时间编解码两方言走 store 既有 tval/toTime；TTL 判定走注入时钟，无 sleep 依赖 CI 速度；身份串（cli:user@host、run:host#pid#nano）只用相等比较，hostname 差异只影响人读取证不影响判定。
- 结论：无，因为无新增路径/进程组/信号/webview 假设；唯一平台敏感物（时间）已被注入时钟与方言抽象双层隔离。

**4. 假红/假绿测试**
- 最大温床：测试手工给 RunHolder 塞值而生产装配漏传 → 机内全绿真机拒绝放行。防线=U3 装配级反面断言（startCardStep 产出的 runner.RunHolder 非空）+ 真机清单第 3 条端到端。
- 断言 30 若判据落在 runner 内存「上次续租时刻」上，换实现即假绿——已钉死判据=库行 expires_at 推进（岔口三两案通用约束）。
- 断言 32「编排全程不再调 ReleaseCard」以**终态断言**表达（回合后 driver_session 保持 owner + runlock 行消失），不做 mock 调用计数——换实现不改需求不会无意义地红。
- 变异复验：契约三点照打；追加建议两点——删 AcquireRunLock 过期分支 → 断言 15 红；RenewRunLock 去掉 WHERE holder → 断言 19 红（承重安全属性的对应红测试）。
- 并发翻红风险：断言 14（租期内他人拒绝）在同进程两 goroutine 下跑，CI 慢机不会偶发红（无真实时间依赖）。
- 结论：有风险，四个口子已各配独立验收动作或判据钉定。

**5. 门禁绕过**
- 权限模型不变：账本凭据即权限边界，无新增门。所有锁写路径（取得/续租/释放/抢占/归属四操作）全走 store.mutate 单事务串行——同一道门覆盖全部表面；TOCTOU 由事务内判定+写入消灭（检查与动作间无窗口）。
- driver_session 的全部写点 grep 实测四处：ClaimCard（收窄后仍守他主拒）、ReleaseCard（反转后他主可见失败）、ClaimDriver（本轮删除）、TakeoverCard（**有意保留的显式旁路**：无条件覆盖但同事务落 EvDriverTakeover 可审计，永不过期——8-23 裁决钦点的唯一合法转移门）。改造后不存在未过门的静默写路径。
- CLI 守卫（dispatch 改判归属）只是提前报错美化，真正的门在 ClaimCard 库层——绕过 CLI 直连账本同样被拒，门与提示分层不混淆。
- 结论：有一个有意保留的旁路（TakeoverCard），其合法性由事件流审计背书；其余表面共享 mutate 一道门，无 TOCTOU 窗口。

**序列化边界（命中）**
RunLock 字段从产生到消费的手写投影全清单：
1. runlock.go rows.Scan→RunLock 结构（手写列序）——U1 测试覆盖字段序与 NULL/零值分辨（expires_at/acquired_at NOT NULL，无 NULL 分辨问题，存在性用第二个返回值表达）;
2. U2 评论文案 fmt.Sprintf 手拼（holder/node/expires 人读文本）——断言 26 要求评论含三要素子串；
3. EvDriverTakeover payload `map[string]string{from,to,reason}` → JSON → web CardDrawer 泛化取值（body/reason/text 优先级）——**穿过真实序列化边界的回归**=payload 金样本 fixture（U1，契约 §4.7 欠账销账处）；
4. RunLock 结构本身不出进程（Go 直调，契约 §6 已声明），无 TS 孪生投影。
链路上无遗漏投影点；roundtrip 属性测试对本卡收益低（唯一 wire 面 payload 三键已被金样本逐字节钉住）。

**枚举新值过既有白名单（同构命中）**
- 无新词表位（拍板 §5.3：复用 EvDriverTakeover + reason）。反向命中：StatusDoing 失去唯一生产写入者——grep 非测试命中四处（types 定义、dispatch 守卫/认领/回滚、badge）全部在本卡改造范围内（U0/U3/U4），无第三方白名单/switch 依赖它（charter-v4 种子 states 不含进行中，契约 §1.4 已核方向）。通道分裂风险被「写入者全集在本卡」封死；断言 36/38 锁两侧。
- kind="普通" comment 为既有值，haltForHuman 同款，不经任何白名单。

**承重安全属性有测试锁住（命中）**
- 互斥（story 10）：断言 14/26 锁；归属不可静默转移（8-23 裁决）：断言 4 + 变异红；非持有者释放可见失败：断言 11/33 + 变异红；被抢者失去写权：断言 31 四写点独立反面断言；释放即时生效：断言 20。
- 显式遗留风险：写权 gate 的达成机制归实现（契约允许钩子或其他），若实现成「每个写点手动 if」而非结构性手段，未来新增写点会无声绕开——断言 31 只能锁现有四点。缓解已写进 U2 验收（优先结构性收口），并在真机清单第 7 条留观察项。不假装有完美锁。

**webview / 平台表现差异（候选第六族触发判定）**
- 无，因为本卡 web/src 零改动、不触及浏览器 API；web 侧唯一相关行为（payload.reason 泛化直读）的输入形状由 Go 侧金样本 fixture 锁定，渲染本身沿用既有路径。真机确认看板显示列入真机清单第 4 条。

---

## 真机清单（全部「未验证，需真机」，归协调者执行）

| # | 项 | 为什么机内验不了 |
|---|---|---|
| 1 | agentd 运行中 kill -9 → 等 TTL → 重派不被挡 | 机内只能造「行过期」，造不出进程死亡+在飞 map 清空+重启的组合现实 |
| 2 | 两个真实会话并发对同卡同节点 --step，恰一成功一落卡 | httptest 是同进程两 goroutine，跨进程/跨机竞争形态不同 |
| 3 | card wait 在编排入口失败场景真的看得见 needs_human+评论 | 分段有机内测试；HTTP 受理→后台编排→落卡→Follow 的整条通路未串验 |
| 4 | 看板 conflict 徽标在新判据下亮/灭 + payload.reason 直读显示 | 浏览器渲染行为不在机内判据内 |
| 5 | charter 流真卡裸 dispatch 跑通且不挪列（story 7） | 真 v9 workflow 读数引上游（契约 §1.4），种子 v4 只方向一致 |
| 6 | takeover→release 人尺度双终端闭环（story 6） | 机内模拟的是相同字符串，真人双机形态未验证 |
| 7 | >5 分钟长回合真实续租发生、远端任务不被强杀、被抢者 comment 恰一条 | 真时钟节奏（B196 读数引上游）；写权 gate 对未来新增写点的实际覆盖观察项 |

## 图覆盖债

本稿引用的符号锚以 `codegraph resolve --doc docs/superpowers/specs/b239-breakdown.md` 自检为准（结果记台账）。best.json 未收录的新符号为零（RunLock 族符号已经 diffs 视图 cards-B239-charter 收录）。

---

## 交棒声明

- 实现节点先读本稿与契约 §4 欠账清单；U0→U4 序贯消化；不得把 Ticket 0 空壳当行为已完成。
- review 节点注意：契约 §2.4 两条可观察行为变更（release 退出语义、dispatch 不挪列）须进对账清单点名。
- finish 节点欠账（spec Out of Scope 末行）：skills/handoff/SKILL.md 与 product-backlog 中「裸 dispatch 必然失败」「驱动权泄漏 CLI 侧无解」两处文案回流，本卡实现合入后必须做。
- 台账：docs/ledger-b239-breakdown.md。

## 交稿自检

1. 产出四样齐全：子系统 3 面带图类型 + 派卡资格四条逐核 ✓；契约核对十行逐条有结论 + 两条边界澄清（待回写契约）✓；子卡 U0–U4 全四段式、判据全部行为化（断言号+命令+期望）✓；缺陷族五族+三追加+webview 候选逐族有答案，无风险处均写「无，因为……」✓。
2. 待拍板岔口三条集中稿首 ✓。
3. 「未验证，需真机」七条汇总成真机清单 ✓。
4. 五张子卡有界文件集逐卡核过（4/2/3/4/5 个文件级条目），圈得出，无需插竖切还债卡；agentd 平铺包判据命中已显式回答（§1.2）✓。

红线自守：本稿零实现代码、零建卡、零派发、零 handoff CLI 写命令（仅计划只读 graph resolve 自检）；扇出归协调者。
