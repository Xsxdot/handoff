# B452 实现计划：熟练工派发（按名调用、只读形态、机器亲和、等结果回文、产物通道）

- 状态：**待拍板**（出稿者：plan subagent；拍板者：主会话协调者）
- 输入：`docs/superpowers/specs/b452.md` **r1.2**（2026-10-21 用户批准，L3，执行形态甲：本地盯，预计不扇出）；台账 `docs/superpowers/specs/b452-ledger.md`
- 基线：分支 `cards/B452-charter-1` @ `e75fc97c`（spec r1.2 提交；代码与 `a9150118` 相同）
- 读者：零上下文执行者。§10「核实读数」列出本稿引用的代码事实和出处，动手前可以复核。
- 出稿中途变更：r1.2（S8 产物通道）在出稿途中到达，本稿一并覆盖 S1–S8。

---

## 待拍板

每条给推荐项（**荐**）和理由。标「**需用户**」的是我认为必须回人的；其余由协调者定。

1. **Q1 模板在哪一侧解析（跨进程接缝）。**
   - **荐 甲：CLI 侧解析。** `handoff dispatch --template` 在 CLI 进程里开账本（`openLedger`，与 `template show`、`card dispatch` 同一入口）取最新版定义，就地组装提示词（模板正文 + `## 本次任务`），把接收者、形态、亲和放进派发请求，再发给本机 agentd。理由有三：B229 的既定方向是「协调者侧组装、执行机收文即用」（裸派发的纪律正文今天就在 CLI 侧组装，`cmd/dispatch.go:197-226`）；卡派发的模板解析今天也在 CLI 进程里（`cmd/card_dispatch.go:406`）；agentd 侧解析还得在转发时再决定「转发展开后的正文还是模板名」，多出一个版本漂移面。
   - 乙：agentd 侧解析，wire 带模板名。好处是将来 Web 控制台能直接用；坏处是正文组装分到两处、转发要多一层决定。Web 增删改本期 Out of Scope，不为它预铺。
   - 推论：wire **不加** `template` 字段（spec 落位三问 ② 写了「加 `template`、`mode`」）。CLI 侧解析后 agentd 用不到模板名；审计靠 CLI 在 stderr 回显「熟练工 <名> v<版本>」。这是对 spec 设计说明措辞的偏离，不改任何用户可见结果，请协调者确认。

2. **Q2 S3 跨机转发走哪条 wire。** spec 说「B448 的转发面」。读码发现一条更贴近的现成路径：卡节点派发早已是「受理机只读选人（`scheduling.Select`）→ 用冻结身份字段 `carrier/squad/home_dir/frozen_target` 经既有 `POST /api/tasks` 发到载体机 → 载体机 `AdmitFrozen` 准入」（`internal/agentd/cardstep.go:180-231`、`:402-439`；`handlers.go:526-556`）。
   - **荐 甲：裸派发复用这条冻结身份 wire，不新开端点。** 受理机在 `handleDispatch` 的普通分支里先只读定出落点：载体直派读载体登记；小队调 `Select`（加亲和过滤，见 Q5）。落点在本机，就本机 `AdmitFrozen` 后照旧 `mgr.Dispatch`。落点在他机，就带冻结身份字段、加 `X-Handoff-Forwarded`，经 `clientForTarget(机器)` 原样转发，收件端走既有冻结分支。理由：wire 只多 `mode` 一个业务字段；防环天然成立，因为收件端对带 `carrier` 的请求从不选人、从不转发；名额只在载体机占（与卡节点同一套 B398 语义）。B448 的首轮转交端点是为协调者首轮的「席位 + 驻场」语义造的，裸派发用不上。
   - 乙：照 B448 新开 `POST /api/tasks/forward`。多一套 DTO、错误体与老版本 404 哨兵，没有对应收益。
   - 连带改变：小队裸派发从 `Admit`（原子地按登记顺序取第一个有空位成员、不看机器）改为 `Select`→`AdmitFrozen`。两步之间若被别人抢满，得到 409 满员，而不是退到下一个成员。卡节点今天就是这个语义，可以接受；spec 现状 5 的「跨机小队吃 400」随之消失（见 §2 S3）。载体直派且载体在本机时，保留 `AdmitCarrier` 原路径不动。

3. **Q3 `--wait` / `wait --final` 怎么跟到另一台机上的任务。** 今天的 `handoff wait` 只能经镜像跟他机任务：本机 `/ws/events` 对本机没有、镜像也没有的任务以 1008 关闭，客户端判永久失败（`handlers.go:1520-1540`）；镜像每 30 秒发现一轮，而且只订阅非终态任务（`workspace/mirror.go:283-296`）。转发后头 30 秒内 `wait` 必然失败；任务若在一个周期内跑完，镜像永远拿不到 completed。spec 风险 ① 说的「复用 B448 人刷新可见的同一通道」对 `--wait` 不成立。
   - **荐 甲：显式按机转发 + 受理机即时登记镜像行。**
     - (a) `/ws/events` 认 `?machine=`，照 `/ws/pty` 的先例（`pty_ws.go:27-34`）：非本机就 `forwardWS`。
     - (b) `GET /api/tasks/{id}` 与新的 `GET /api/tasks/{id}/artifacts` 先过 `forwardIfRequested` 再过 `byTask`。
     - (c) 受理机转发成功后立刻 `UpsertMirrorTask(载体机, 任务快照, now)`，让 `show/attach/reply/done <id>` 这类不带机器名的命令马上能经 `byTask` 路由。
     - CLI 从派发应答的 `task.target` 拿到载体机名，`--wait` 一律带 `?machine=` 跟随。
   - 乙：只做 (c)，靠镜像订阅。不行：任务在一个发现周期内跑完时镜像不订阅，事件流里永远没有 completed。

4. **Q4 只读任务的「完成即归档」由谁触发，名额由谁还。** 名额今天只在 HTTP 层 `handleDone`/`handleStop` 里归还（`handlers.go:875-988` → `releaseTaskCarrierOccupancy`），`Manager.Done` 不还。
   - **荐 甲：编排内部触发，组装点注入归还回调。**
     - `handleResult` 在 completed 事件落库并广播之后，对 `mode=readonly` 的任务**另起 goroutine** 调既有 `m.Done(ctx, id, "只读任务完成，自动归档")`。另起 goroutine 是为避免在 mediate 循环里同步停自己的 executor。
     - `Done` 返回 `Claimed=true` 时，调用经组装点注入的终态回调（接口定义在 orchestration，使用方；实现由 agentd Server 提供，内部就是 `releaseTaskCarrierOccupancy`；架构法第九条），快照规则与 `handleDone` 相同。
     - 回合失败（turn_failed）**不自动归档**，任务照旧停在 waiting_review，协调者可以 `continue` 或 `done`。
     - agentd 重启时，处于 waiting_review、mode=readonly、且最后一条回合结果是 completed 的任务，启动恢复时补做自动归档（同一函数）。
   - 乙：Server 层订阅 hub 自己触发 Done。会让网关认识编排事件语义，否。
   - spec 文字核对：S4 写「不进 waiting_review」，实现决定 4 写「走既有 Done 路径」，而 Done 只认 waiting_review。本稿读作「不停留」：任务会瞬时经过 waiting_review，`--wait` 的调用方不会看到需要人动作的停留。请协调者确认这一读法；它不改用户可见结果。

5. **Q5 亲和过滤落在哪、wire 怎么表达。**
   - **荐**：
     - `scheduling` 新增 `SelectWhere(req, accept func(Carrier) bool)`，`Select` 委托它（传 nil）。不复用 `IgnitionRequest.Target`：那个字段会随排队行持久化，给它加过滤语义会让存量排队行改变行为。
     - 谓词在 agentd 侧组装：`here` 用 `s.IsSelfMachine(carrier.Machine)`；`--machine M` 在 M 是本机时同上，否则 `carrier.Machine == M`。
     - wire 加两个互斥字段 `machine`（机器名）和 `here`（布尔）；`here` 在受理机解析成受理机自身。不用「`machine:"here"`」这种哨兵值，避免和一台恰好叫 here 的机器撞名。
     - CLI 侧 `--here` 与 `--target` 互斥：带 `--target` 时受理机不是调用方所在机，「here」语义不清。
   - spec 落位三问没有列编制域（scheduling）。这一处是实现落位的补充，不改结果，请协调者确认。

6. **Q6 用户可见的码值（实现决定 5、7 要 plan 定）。**
   - **荐**：
     - 亲和拒绝：HTTP **422**，体 `{"error":…,"kind":"affinity_unmet"}`；CLI 退出码 **4**（`ExitAffinityRefused`）。
     - 任务以非完成告终（turn_failed / failed / 未完成就归档）：退出码 **3**（`ExitTaskFailed`）。
     - 等待超时：沿用 **124**；其余错误沿用 1。
     - `--wait` 缺省超时 **30m**。主会话的 Bash 工具前台上限是 10 分钟，skill 写明用后台运行，或显式 `--timeout 9m` 后接 `wait --final`。
     - 文案前缀固定为「机器亲和不满足：」。
   - **需用户**：退出码与文案是 spec 指定的「用户可见名」。spec 授权由 plan 定值，但它们要写进 skill，并被 B453–B455 的主会话依赖。建议协调者把这组值连同 Q9 一次性转给用户过目；用户不看则按本稿执行。

7. **Q7 产物目录位置与注入范围。**
   - **荐**：
     - 目录：`<DataDir>/tasks/<任务id>/artifacts/`。理由：它已经在权限门的 `TaskDir` 可写范围里（`authority.go:180-184`），权限门**零改动**；它不在工作树里；`purgeTaskCache` 与 `gc` 都不碰它。
     - 环境变量：`HANDOFF_ARTIFACTS_DIR`。
     - 保留期：缺省 **168h**，配置键 `artifacts.retention`，0 表示不清。
     - 清理：agentd 启动时跑一次，之后每小时一次；只删终态任务、且 `updated_at` 早于保留期的 `artifacts` 子目录，绝不删任务目录本身。
   - 提示词注入范围，二选一：
     - **荐 甲**：所有任务都建目录、注入环境变量和一节「## 产物目录」。spec S8 写「每个任务」，B454 的截图也可能出现在分支形态里。
     - 乙：只给 mode=readonly 注入提示词，目录和环境变量仍对所有任务建。卡派发的提示词零变化，回归面更小。
     - 甲会让所有派发的 `PlanContent` 多出一节，断言 `PlanContent` 全文的既有测试要改期望（执行者先 grep 列出来）。

8. **Q8 产物下载接口与打包格式。**
   - **荐**：`GET /api/tasks/{id}/artifacts[?machine=M]`，挂 `forwardIfRequested` 与 `byTask`。
     - 非空：200，`Content-Type: application/gzip`，tar.gz，带 Content-Length（照 bundle 先落临时文件再整体发）。
     - 目录不存在或为空：204。
     - 打包只收普通文件和目录，**跳过符号链接**（防止把 `~/.ssh` 链出来）；总大小上限 512 MiB，超出回 413 并点名大小。
     - CLI 解到 `os.MkdirTemp("", "handoff-artifacts-<id8>-")`。条目带 `..`、绝对路径或非普通类型一律拒收，整次失败，不写半截。
     - 同机判据：本机能 `os.Stat` 到 `task.artifacts_dir` 且是目录，就直接用原路径，不复制。路径里含完整任务 UUID，他机不可能恰好有同名目录；共享文件系统时原路径本来就可读。

9. **Q9 `--wait` 产物行格式。** spec 写「第一段最终文本、产物路径单独一行」。
   - **荐 甲**：最终文本原样输出，不以换行结尾时补一个换行；产物非空时再输出一行 `artifacts: <绝对路径>`。带固定前缀，脚本才能区分「最终文本最后一行恰好是个路径」与「产物行」。
   - 乙：裸路径独占一行。更贴 spec 字面，但不可机器区分。
   - 与 Q6 一起转用户（**需用户**，同 Q6 理由）。

10. **Q10 熟练工定义的字段规则。**
    - **荐**：
      - `template put`：`mode ∈ {"", "branch", "readonly"}`，其他值拒绝；`mode=readonly` 时 discipline 可空。`receiver` 非空时 executor 可空：执行器由载体决定，填了就当一致性断言，派发时与载体 CLI 不符即被拒，这是现状语义。
      - `target` 允许机器名或 `here`。
      - 卡派发（`ViaTemplate`）遇到 `receiver` 或 `mode` 非空、或 `target=here` 的定义，**拒发**并点名「熟练工定义只能经 `handoff dispatch --template` 调用」，防止 `here` 流进 `ResolveTarget` 被当成一台叫 here 的远端机。
      - 新字段带 omitempty，旧模板 `template show` 的输出逐字不变。

11. **Q11 版本兼容：能力位。** 老 agentd 会静默忽略未知 JSON 字段：`mode:readonly` 被忽略 → 按分支形态在主仓原地切分支；`machine/here` 被忽略 → 亲和失效。
    - **荐**：`StatusResp` 加 `skilled_dispatch_supported *bool`，同 `DisciplinesSupported` 的三态，nil 按不支持。
      - CLI 在请求带 mode/亲和/`--wait` 时，先核本机 agentd 的能力位。现有 `resolveBareDiscipline` 已经调过一次 `Status`，复用那次结果。
      - 受理机转发 `mode=readonly` 前，探活载体机，能力位缺席就 502「执行机 <M> 的 agentd 版本过旧，不支持只读形态，请升级」。
      - 转发的只读请求同时带 `new_worktree:true`，作为第二道保险：即使对端误收，也不会落到主仓原地。
    - 能力位与实现同生同死：mode、亲和、`/ws/events?machine=`、产物接口四件都齐了才上报 true。

12. **Q12 真机账本（需用户：凭据）。** 跨机转发要两台 agentd 共用一套编制登记（载体、小队、名额）。
    - **荐 甲**：照 B448，在 linux-01 的 PG 18 上用测试库。优先复用已登记的 `handoff_b448` 测试库与专属账号，不可用则新建 `handoff_b452` 并登记 credentials.md。两台隔离实例共库：linux-01 `t452-linux`，mac-mini `t452-mac`。
    - 乙：两台各用 SQLite 账本，手工把同一载体登记两遍。不碰凭据，但名额计数分裂，S3 的「只在载体机占名额」验不真。
    - 碰凭据按全局规则回人。

13. **Q13 载体机上项目未登记。** 今天 CLI 收到「项目未登记」会自动补登「本机 + --target」两跳（`cmd/project.go:139-200`）。转发场景下缺登记的是载体机，CLI 不知道它，补登记补错地方。
    - **荐**：本卡不做跨转发自动补登记。受理机把载体机的错误加前缀「执行机 <M>：」原文回传；CLI 识别到这个前缀时跳过自动补登记，直接报错并提示在该机登记。补登记落 roadmap 残余。

14. **Q14 S6 的生产载体。** linux-01 上的 opencode 载体有 cline（leader 与 pro 两队在用，协调者常驻）、dp（runner 小队）、mimo（模型 `opencode-go/muse-spark-1.3-contributor`，不在任何小队）。
    - **荐 mimo**：不与协调者和 runner 争名额。备选 dp。

15. **Q15 扇出。** **荐 不扇出（确认 spec 预测）**，单卡单工作单元，内部分四段，每段过关再进下一段（§4）。理由：
    - 八个故事共用同一组文件：`cmd/dispatch.go`、`handlers.go` 派发段、`proto.Task`、`StatusResp`。拆成两张卡，依赖全是**施工排序**（共享文件），不是契约硬依赖。
    - 唯一可以独立出来的「只读 + 产物」段（§4 段 1），其消费者只有 `--wait` 与 S6，单独成卡没有真实消费者。
    - 每个领域的增量都在百行量级。

---

## 0. 交付目标

本轮交付 spec r1.2 的 S1–S8 全部故事，单卡一轮：

| 故事 | 本轮做到 |
|---|---|
| S1 定义熟练工 | `TemplateDef` 加 `receiver`、`mode`（omitempty）；put 校验按 Q10；show 原样回显；旧行解析不变 |
| S2 按名调用 | `dispatch --template <名> --prompt …`（或 plan 文件）：CLI 取定义 → 组装「模板正文 + `## 本次任务`」，三个占位符替换为空串 → 接收者、形态、亲和进请求；`--receiver` 可覆盖；模板名不存在时报错点名 |
| S3 跨机路由 | 受理机只读定落点；他机落点带冻结身份转发；小队先选再转；对端够不着时 502 点名机器，不换人，受理机零写入（Q2） |
| S4 只读形态 | `mode=readonly` → 在接收机以基线提交 `git worktree add --detach` 建托管树，不建分支；调用方不要求干净树；completed 后自动 Done 并回收工作树、归还名额；不出 bundle（Q4） |
| S5 等结果回文 | `dispatch --wait [--timeout]` 与 `wait <task> --final [--machine M]`：stdout 只有最终文本（S8 时再加产物行），退出码按 Q6；超时不杀任务，stderr 打续等命令 |
| S7 机器亲和 | 定义 `target: here` 或机器名，`--here` / `--machine` 覆盖；受理机先按机器过滤，再选人；不满足 → 422 + 退出码 4，零任务、不换机；`--machine` 指他机时照 S3 转发 |
| S8 产物通道 | 每任务一个 `<DataDir>/tasks/<id>/artifacts`，提示词与环境变量注入；写它不建工单；`--wait` 输出产物行（同机给原路径，跨机下载到本机临时目录）；`handoff artifacts <task>` 事后拉取；按保留期清理；不进 git（Q7–Q9） |
| S6 首个熟练工 | `skills/handoff/skilled/code-investigate.json` 入仓（只读、receiver=mimo）；隔离环境真机跑通「mac-mini 一条命令 → linux-01 跑 → stdout 拿回结论与产物」；生产账本 put 留到 finish 换版之后 |
| skill | `skills/handoff/SKILL.md` 新增「熟练工」一节（§4 段 4） |

**留待后续，本轮不做**（落 roadmap 残余）：
- 转发场景下自动补登记载体机项目（Q13）；
- 不带 `--template` 的裸派发转发到他机时，基线仍取载体机仓库 HEAD（CLI 只对 `--template` 与 `--target` 采本地 HEAD，见 §3）；
- agentd、cmd 两包的竖切债（§1 末段）；
- spec Out of Scope 已列的各项。

## 1. 触及子系统与类型

| 子系统 / 领域 | 类型 | 本轮角色 |
|---|---|---|
| 协调者命令面（`cmd/`） | 逻辑型 | `dispatch` 新 flag 与模板组装、`--wait`；`wait --final`；新子命令 `artifacts`；`template put` 校验；退出码 |
| agentd 客户端（`internal/client`） | 逻辑型 | `Dispatch` 手搭 body 加字段；按机跟随事件流、取任务、取产物的方法 |
| wire DTO（`internal/proto`） | 逻辑型（契约面） | `Task` 加 `mode`、`artifacts_dir`；`StatusResp` 加能力位 |
| 控制门面（`internal/agentd`） | 逻辑型 | 派发落点判定与转发、亲和谓词与 422、镜像行即时登记、`/ws/events?machine=`、产物接口、能力位上报、终态回调实现 |
| 编制域（`internal/scheduling`） | 逻辑型 | `SelectWhere`（Q5） |
| 卡片账本（`internal/ledger`、`internal/ledgerstep`） | 逻辑型 | `TemplateDef` 字段；无卡渲染函数与占位符替换共用；`ViaTemplate` 拒用熟练工定义 |
| 任务编排（`internal/orchestration`） | 逻辑型 | `DispatchReq.Mode`；只读建树；产物目录、环境变量与提示词；自动归档与终态回调；启动补归档；保留期清扫 |
| 项目与工作区（`internal/workspace`） | 逻辑型 | detached 托管树模式 |
| 本机存储（`internal/store`） | 逻辑型 | tasks 表加 `mode` 列（逐列 ALTER 迁移，同既有写法） |
| 执行器（opencode）与跨机传输 | **边界型** | 不改代码。「opencode 越界写是否带出可判路径」「跨机 WS 与下载经 relay/直连可达」只能真机验 |
| 权限门（`internal/permgate`） | 逻辑型 | **不改产品代码**，只加锁行为的测试 |

单卡单工作单元，不派子卡，所以不核派卡资格四条。

**架构法第三条答题**：
- `internal/agentd`：72 个非测试源文件、21989 行、无子包；`cmd`：64 个、13256 行、无子包。两者都命中「单包 ≥40 且无子包」，agentd 逼近「2 万行」上限。
- 本卡仍圈得出有界文件集：agentd 只改 `handlers.go` 派发段与 `handleEvents` 头部、`server.go` 路由表与能力位，新增两个文件（例如 `dispatchroute.go`、`artifacts_api.go`）；cmd 新增 `artifacts.go`，改 `dispatch.go`、`wait.go`、`template.go`、`exit.go`。执行者无需加载整包。竖切还债不在本卡，落 roadmap。
- `Manager` 方法数早已 ≥20 且名字说不出拥有哪些数据（第六条嫌疑），本卡只在它上面加两个小方法（自动归档、保留期清扫），不新增编排职责类型。保留期清扫若超过 60 行，执行者另起一个文件。

## 2. 故事映射与行为闭环

### 2.1 故事 → 实现责任 → 真实消费者 → 集成/回归负责人

| 故事 | 实现责任 | 真实消费者 | 集成/回归 |
|---|---|---|---|
| S1 | ledger 字段与校验、`template put/show` | 主会话、协调者 | 执行者：V1；协调者：R1 |
| S2 | CLI 取定义与组装；无卡渲染函数 | `handoff dispatch --template` | 执行者：V2；协调者：R2 |
| S3 | 受理机落点判定与转发、镜像行即时登记 | 本机 agentd 的 `POST /api/tasks` | 执行者：V3（两 Server 竖切）；协调者：R3、R4 |
| S4 | workspace detached 模式；编排自动归档与回调；启动补归档 | 执行机 Manager | 执行者：V4；协调者：R2 |
| S5 | CLI `--wait` 与 `wait --final`；client 按机跟随 | 主会话（经 skill） | 执行者：V5；协调者：R2、R3 |
| S7 | `SelectWhere`、agentd 谓词、422；CLI flag | 主会话 | 执行者：V6；协调者：R5 |
| S8 | 编排目录、注入与清扫；门面产物接口；CLI 下载与 `artifacts` | 主会话；B454 | 执行者：V7；协调者：R6 |
| S6 | 示例 JSON 与 skill | 主会话 | 协调者：R7 |

### 2.2 行为闭环（触发者 → 权威事实/载体 → 消费者 → 可观察结果 → 实现责任）

| 触发者 | 权威事实 / 载体 | 消费者 | 可观察结果 | 实现责任 |
|---|---|---|---|---|
| 主会话在 mac-mini 执行 `dispatch --template code-investigate --prompt … --wait` | 共享账本 `dispatch_templates` 最新版；CLI 组装的提示词进 `prompt`；`mode=readonly` 进请求 | mac-mini agentd `handleDispatch` | 受理机日志「落点他机，转发」；stdout 最终只有结论文本 | cmd + agentd |
| mac-mini agentd 选定 mimo（机器 linux-01） | 冻结身份 `carrier/squad/home_dir/frozen_target` + `X-Handoff-Forwarded`；名额只在 linux-01 的 `AdmitFrozen` 里占 | linux-01 agentd 既有冻结分支 | 任务在 linux-01 创建，应答 `task.target=linux-01`；mac-mini `mirror_tasks` 立刻有该行；两机名额计数：受理机零写入，linux-01 +1 | agentd |
| linux-01 Manager 准备工作区 | `git worktree add --detach <p> <基线 sha>`；`tasks.mode=readonly`；`tasks/<id>/artifacts` 已建 | executor（opencode） | `git worktree list` 多一棵 detached；`git branch` 无新分支 | workspace + orchestration |
| executor 写 `$HANDOFF_ARTIFACTS_DIR/report.md` | opencode `external_directory` 请求 → 权限门 `InScope`（命中 TaskDir） | 编排的权限处置 | 无 `permission_request`、无待答工单（**未验证，需真机**：事件是否带出路径） | 无产品代码；测试 + 真机 |
| executor 回合以 `{"summary":…}` 收尾 | `completed` 事件带 `final_text`；随后自动 Done → `archived` | 编排自动归档；`/ws/events` 订阅者 | 任务进 completed；托管树被删；名额归还到 0；archived 事件 note「只读任务完成，自动归档」 | orchestration + agentd 回调 |
| CLI `--wait` 经 mac-mini `/ws/events?task=&machine=linux-01` | linux-01 的事件流（转发 WS） | CLI | 收到 completed 即输出 final_text；本机读不到 `task.artifacts_dir` → `GET …/artifacts?machine=linux-01` → 解到临时目录 → 输出 `artifacts: <路径>` | client + cmd + agentd |
| `--here` 但小队在 mac-mini 无在线成员 | 账本登记（成员机器、状态） | 受理机 `SelectWhere` | 422「机器亲和不满足：…」；CLI 退出码 4；两机任务表无新行 | scheduling + agentd + cmd |
| agentd 重启发生在 completed 之后、自动归档之前 | 本机 tasks 表：readonly + waiting_review；事件表最后一条回合结果是 completed | 启动恢复 | 重启后任务被补归档、名额归还 | orchestration |
| 保留期到 | tasks 表：终态 + `updated_at` | 清扫循环 | 过期任务的 `artifacts/` 被删，任务目录其余文件不动 | orchestration |

## 3. 改动边界

**有界文件集**（新文件拆分属执行者权限，见 §6）：

- `internal/ledger/templates.go`：`TemplateDef` 加 `Receiver string json:"receiver,omitempty"`、`Mode string json:"mode,omitempty"`，以及模式常量与一个 `Validate()`（或等价函数）供 put 用。不动表结构。
- `internal/ledgerstep/dispatch.go`：
  - 抽出占位符替换（`:349-353`）成一个导出函数，`ViaTemplate` 与 CLI 的无卡渲染共用。新增无卡渲染函数：正文占位符替换为空串，再接 `## 本次任务` 一节。
  - `ViaTemplate` 开头拒用熟练工定义（Q10）。
- `cmd/template.go`：put 校验改用 `Validate()`。
- `cmd/dispatch.go`：
  - 新 flag：`--template`、`--wait`、`--timeout`（只与 `--wait` 同用）、`--here`、`--machine`。互斥：`--here`×`--machine`；`--here`×全局 `--target`；readonly 定义×`--branch/--new-branch/--worktree/--new-worktree`。
  - 带 `--template` 时总采本地 HEAD 作 `base_commit`（`--no-sync-check` 与 `--project` 仍可关闭）。分支形态照远端语义做脏树拒发（`--allow-dirty` 覆盖）。只读形态不拒发，有已跟踪改动时 stderr 提示「只读调查基于已提交的 <sha7>，本地未提交改动不可见」。
  - 能力位核对（Q11）。
  - `--wait` 时任务 JSON 改打 stderr，stdout 只留回文。
- `cmd/wait.go`：`--final`（与 `--follow`、`--until-done` 互斥）与 `--machine`。
- `cmd/artifacts.go`（新）：`handoff artifacts <task> [--machine M]`。
- `cmd/exit.go`：加 `ExitTaskFailed=3`、`ExitAffinityRefused=4`。
- `internal/client/client.go`：
  - `DispatchOpts` 与 `Dispatch` 手搭 body 加 `mode`、`machine`、`here`，三者都只在非零值时写入，与 `receiver` 同写法。
  - 事件流 URL 可带 `machine`。
  - 新增「按机取任务」「按机取产物」（照 `bundle.go`：404 → `ErrArtifactsUnsupported`；204 → 空）。
  - 新增「无 cursor、可重连、从 0 回放」的终态等待：不写审核者 cursor 文件，避免与人手敲的 `wait` 互相推水位，理由同 `StreamEventsOnce` 的注释。
- `internal/proto/`：`Task` 加 `Mode string json:"mode,omitempty"`、`ArtifactsDir string json:"artifacts_dir,omitempty"`。后者由执行机在任务读出时填，**不落库**。`StatusResp` 加 `SkilledDispatchSupported *bool json:"skilled_dispatch_supported,omitempty"`。Web 契约夹具 `web/src/api/testdata/Task.json` 如需重生成，随同更新。
- `internal/agentd/`：
  - `handlers.go`：`dispatchRequest` 加 `Mode`、`Machine`、`Here`。普通分支改为调用新的落点判定函数（建议放新文件 `dispatchroute.go`）：本机 → 准入后 `mgr.Dispatch`；他机 → 能力探活（只读时）→ 冻结身份转发 → 成功后登记镜像行。冻结分支加 `Mode` 透传，保持不转发。错误映射加 422（亲和）与转发失败。`handleEvents` 头部加 `?machine=` 的 `forwardWS` 分支。
  - `server.go`：路由加 `GET /api/tasks/{id}/artifacts`；`GET /api/tasks/{id}` 与产物路由前置 `forwardIfRequested`；`handleStatus` 上报能力位；组装点把终态回调注入 Manager。
  - `artifacts_api.go`（新）：打包与发送。
- `internal/scheduling/scheduling.go`：`SelectWhere`（Q5）。
- `internal/orchestration/`：
  - `contracts.go`：`DispatchReq.Mode`。
  - `manager.go`：`Dispatch` 处理 readonly（强制托管树、拒绝分支类参数、传 Detached 给工作区）；建产物目录（0700）；`startEnv` 加环境变量；提示词加一节（Q7）；任务行写 `mode`。`handleResult` 在 completed 之后对 readonly 起自动归档。新增终态回调字段与 setter。
  - 启动补归档。注意 `RecoverOnStartup` 是 `watchdog.go:548` 的包级函数，拿不到 Manager。补归档放在 Manager 装配完成后的启动入口，具体位置由执行者选，在交回物里写明。
  - 保留期清扫（可新文件）。
- `internal/workspace/gitworkspace.go`：`WorkspaceReq.Detached` 与对应 `PrepareReq` 字段。分支模式 × detached 互斥。detached 只与 NewWorktree 同用，用 `git worktree add --detach <p> <start>`；Workspace.Branch 为空。分支身份核对改为核 HEAD == start（`verifyBranchIdentity` 对 detached 改判 `rev-parse HEAD`）。
- `internal/store/store.go`：tasks 加 `mode TEXT NOT NULL DEFAULT ''`，进迁移列表与读写列清单。
- `skills/handoff/SKILL.md`：新节（§4 段 4）。`skills/handoff/skilled/code-investigate.json`（新）。
- 测试：各包新增测试文件，命名带 `b452`。

**不许动**：
- `internal/permgate` 产品代码。
- `internal/executor/turn` 协议与解析（spec 实现决定 6）。
- 各执行器 adapter。
- B448 首轮转交端点与 `scheddrain.go`。
- `Manager.Stop` 的留树语义。
- 镜像发现节奏。
- `IgnitionRequest` 结构。
- 卡派发除 `ViaTemplate` 拒用守卫以外的行为。
- `cmd/agentd.go` 的 server 级超时。

**架构方向**：
- CLI → client → agentd（既有）；agentd → client 出站（既有，卡节点先例）。
- 编排 → workspace（既有）。
- 终态回调是编排定义、agentd 实现的「实现→接口」边，在组装点接线（第八、九条）。
- 不新增 scheduling、ledger 对 agentd 内部的依赖。

## 4. 工作顺序

**段 0：先验三条承重假设（阻塞后续一切）。**
1. 两 Server 共账本竖切夹具能搭起来：A 的 `cfg.Targets={"t452-linux": B.ts.URL}`、B 的 `cfg.Name="t452-linux"`。照 B448 的 `newNoPTYLedgerEnvWithTargets`（`ledgerapi_test.go:194-217`），在 B 上登记一个载体。打 A 的 `POST /api/tasks`，带上 B 载体的冻结身份字段，**直接经 `clientForTarget` 发出**（不改产品代码，测试里手工发），B 能建任务。搭不起来就停下回报，不得改用假对端。
2. `git worktree add --detach <p> <sha>` 之后 `git branch --show-current` 输出空串，`RemoveManagedWorktree` 能删掉它，且主仓 `git branch` 无新增（真 git 仓的探针测试）。
3. 在 mediate 的 result 处理路径里另起 goroutine 调 `Done`，不会死锁：编排测试里用假 adapter 跑一次 completed → Done，断言在时限内到达 completed 且 adapter `Stop` 被调用。若必须同步调用才行，回报协调者。

**段 1：只读与产物（单机闭环，S4 + S8 的执行机侧）。**
- workspace detached 模式 → store `mode` 列 → `DispatchReq.Mode` 与 Manager 建树、目录、环境变量、提示词 → 自动归档 + 终态回调 + 启动补归档 → 保留期清扫 → 权限门锁行为测试。
- 过关标准：V4、V7 中执行机侧那几条全绿；`go test ./internal/workspace/ ./internal/orchestration/ ./internal/store/ ./internal/permgate/` 全绿。

**段 2：wire、路由与亲和（S3、S7 + S8 的门面侧）。**
- proto 字段与能力位 → client body 字段（先写 V8 序列化链路测试）→ `SelectWhere` → agentd 落点判定、转发、镜像行、422 → `/ws/events?machine=` → 产物接口。
- 过关标准：V3、V6、V8 全绿；B233/B398 冻结派发、卡节点派发、`handleDispatch` 的既有测试零改全绿。只有 §6 列名允许改写的测试例外。

**段 3：CLI（S1、S2、S5 + S8 的命令侧）。**
- ledger 字段与 put 校验 → 无卡渲染 → `dispatch --template` → `--wait` 与 `wait --final` → `handoff artifacts` → 退出码。
- 过关标准：V1、V2、V5 全绿。

**段 4：skill 与示例（S6 的仓内部分）。**
- `skills/handoff/skilled/code-investigate.json`（草案见 §9）。
- `SKILL.md` 在「## 执行器选型与模型」（`:102`）之后、「## 在 agent 会话里挂 wait」（`:115`）之前插入「## 熟练工：按名派发、等结果回文」，内容：
  - 什么活交熟练工；
  - 命令形态；
  - `--wait` 在主会话里怎么跑（后台运行，或 `--timeout 9m` 加 `wait --final`）；
  - stdout 格式与退出码表；
  - **退出码 4 = 亲和拒绝 → 退回本会话子 agent 做，不要换机重试**；
  - 只读形态的边界（只看已提交、push 后才能跨机、不提交）；
  - 产物目录与 `handoff artifacts`。
- 然后按 implement 三段律跑全量，提交交回物（首节「偏离与自主决定」）。

**段 5：真机 R1–R8 由协调者执行，不派发**（§5.3）。

## 5. 验收方式

### 5.1 机内断言（执行者）

- **V1（S1）**
  - put/show 往返：带 `receiver`、`mode` 的定义原样回显。
  - 旧定义 JSON（无两字段）解出后 `Mode==""`、`Receiver==""`，`template show` 字节输出与基线一致。
  - `mode:"ro"` 被拒，错误点名合法值。
  - `mode:readonly` 且 discipline 空可写入；`mode:branch` 且 discipline 空仍被拒。
  - `ViaTemplate` 遇熟练工定义拒发，且错误点名「handoff dispatch --template」。
- **V2（S2）**
  - 无卡渲染：正文含 `{{CARD}}{{TITLE}}{{ACCEPT}}` 时三者替换为空串；输出 = 正文 + 空行 + `## 本次任务` + 空行 + 任务文本。
  - CLI 竖切：起一个 httptest agentd 捕获请求体，断言 `prompt` 等于上式，`receiver` 取自定义，`--receiver` 覆盖生效，`mode` 键在且等于 readonly。
  - 模板不存在：报错含模板名，且**零 HTTP 请求**。
  - 执行机侧：经真实 `mgr.Dispatch` 后 `StartReq.PlanContent` 含模板正文与 `## 本次任务`（以及 Q7 甲时的「## 产物目录」）。
- **V3（S3，两 Server 竖切，真实 HTTP，假 Runner 只装在 B）**
  - (a) 载体直派到 B 的载体：A 返回的 `task.target` 为 B 名，`task.id` 在 B 的任务表、不在 A 的任务表；A 的 `mirror_tasks` 有该行；A 对该任务 `GET /api/tasks/{id}` 不带 machine 也拿到 200（经 byTask）；两级名额只在 B +1，A 零写入。
  - (b) 小队 `[X@A, Y@B]` 且 X 满员：选中 Y 转发成功。这条是旧 400 缺陷的回归锁：基线上这个用例应当红。
  - (c) B 够不着（target 指向已关闭端口）：A 回 502，文案点名 B 并含「若请求已送达…」；两机任务表零新行；名额全 0。
  - (d) 防环：B 收到带 `X-Handoff-Forwarded` 却无 `carrier` 的请求，若判出他机落点 → 拒绝，不出站（B 的 target 池指向计数 handler，计数 0）。
  - (e) 转发只读请求、B 不报能力位（桩回 Status 不带该键）→ 502「版本过旧」，B 零任务。
  - (f) B 的错误（例如项目未登记 400）原样回传，带「执行机 <B>：」前缀。
- **V4（S4，真 git 仓）**
  - readonly 派发后：`git worktree list --porcelain` 有该树且为 `detached`；`git for-each-ref refs/heads` 前后一致；任务 `branch==""`、`mode==readonly`、`worktree_managed==true`；HEAD == 基线 sha。
  - 调用方仓有未提交改动时照常派发，脏改动不在树里。
  - 假 adapter 回 completed（带 final_text）→ 在时限内任务 completed；事件序列 `completed` → `archived`（note「只读任务完成，自动归档」）；工作树目录不存在；终态回调恰调一次，带该任务快照。
  - 门面竖切：名额计数回到 0，并发两次（模拟 `handleDone` 与自动归档同时到）时回调仍只一次，靠 `Claimed`。
  - turn_failed → 停在 waiting_review、工作树在、回调零次。
  - 重启补归档：手工造 readonly + waiting_review + 末条 completed 的任务行，跑启动恢复 → completed、回调一次；末条是 turn_failed 的不动。
  - 分支类参数与 readonly 同给 → 400，零任务、零工作树。
  - 基线 sha 不在执行机仓 → `ErrBaseCommitMissing` 400，零工作树（spec 要求基线校验照旧）。
  - Stop 一个 readonly 任务：留树（现状语义）。
- **V5（S5，事件流桩）**
  - 完成：stdout **恰好**等于 final_text（含原样的末行 trailer，不加装饰），产物为空时没有任何额外行，退出码 0。
  - turn_failed / failed / 未完成就 archived：stdout 空，退出码 3，stderr 含 fail_reason 与任务 id。
  - 超时：退出码 124，stderr 含 `handoff wait <id> --final --machine <M>`；任务未被 stop（桩上零 stop 调用）。
  - 中途断线重连后仍拿到 completed，且不写 `~/.handoff/cursors`（断言目录无该任务文件）。
  - `wait --final` 对一个已在 waiting_review、经过一次 `continue` 的分支任务，返回**最后一回合**的结果，不是首回合。
  - `--final` × `--follow` 被拒，且零网络。
- **V6（S7）**
  - `SelectWhere`：谓词全拒 → `ErrNoHealthy`；谓词放行的成员全满 → `ErrNoSlot`；`Select(req)` 与 `SelectWhere(req,nil)` 行为一致（复用既有 Select 测试）。
  - 门面：
    - (a) `here` + 载体在他机 → 422，体 `kind=affinity_unmet`，`error` 以「机器亲和不满足：」开头，点名本机名与载体所在机；零任务。
    - (b) `here` + 小队本机无在线成员 → 422，`error` 列出成员@机器。
    - (c) `machine=B` + 小队在 B 有成员 → 转发到 B。
    - (d) `machine` 与 `here` 同给 → 400。
    - (e) 亲和满足但满员 → 409（不是 422）。
  - CLI：422 → 退出码 4，stderr 原文；`--here` 与 `--target` 同给被拒且零网络。
  - 命令 flag 覆盖定义 target：定义 `target: linux-01` + `--here` → 请求 `here=true` 且无 `machine`。
- **V7（S8）**
  - 执行机：任务目录下 `artifacts/` 存在且为 0700；`StartReq.Env` 含 `HANDOFF_ARTIFACTS_DIR=<该路径>`；续接与重启恢复路径同样带上该变量（执行者先查恢复路径怎么组 env，没有统一入口的列为偏离上报）。
  - 权限门：`Gate.Judge` 对写 `<DataDir>/tasks/<id>/artifacts/r.md` → AutoAllow（编排经 `approval.Authority.Judge` 走一遍：无工单）；对写 `$HOME/x.md`（不在三处范围）→ Escalate。这两条是 spec 风险 ④ 的锁。
  - 只读任务自动归档后工作树没了，`artifacts/` 仍在，内容不变。
  - 保留期清扫：终态且超期 → `artifacts/` 删掉、`plan` 等其他文件还在；非终态或未超期 → 不动；保留期 0 → 不动。
  - 产物接口：
    - 空目录 → 204；
    - 有文件 → 200 tar.gz，解开后内容逐字节一致；
    - 目录里放一个指向 `$HOME` 的符号链接 → 包里没有它；
    - 超上限 → 413；
    - `?machine=B` 经 A 转发得到同一包。
  - CLI：
    - 同机（`artifacts_dir` 本机可读）→ 输出原路径，零下载请求；
    - 跨机 → 解到临时目录，内容一致，输出 `artifacts: <路径>`（Q9 甲）；
    - 包内含 `../x` 条目 → 整次失败、零文件落地；
    - `handoff artifacts <id>` 空产物 → stdout 空、stderr「任务无产物」、退出码 0。
  - 产物不进 git：只读任务结束后，主仓 `git status --porcelain` 与 `git for-each-ref` 前后一致。
- **V8（序列化边界，穿过真实链路）**
  - `client.Dispatch`（手搭 map）→ httptest 上的真实 `handleDispatch` 解码：`mode`、`machine`、`here` 在请求体里**有键**；`here:false` 与 `machine:""` 时**无键**（区分缺席与零值）；收件端解出的 `DispatchReq.Mode` 与发送值一致。
  - 转发链路：A 发往 B 的原始字节里 `mode` 与冻结身份四键都在（在 B 前挂一层透传 handler 捕获）。
  - `Task` JSON：`mode`、`artifacts_dir` 经执行机 → 受理机原样应答 → CLI 解码三段后值不变；旧任务（空 mode）序列化时无 `mode` 键。
  - `StatusResp` 能力位：老结构（无键）解出 nil，CLI 按不支持拒发。
- **变异锚点**（供 review 做一次变异）：
  - 去掉自动归档的回调 → V4 名额断言红；
  - `SelectWhere` 谓词改成「排序」→ V6(b) 红；
  - 产物接口不跳符号链接 → V7 符号链接条红；
  - `--wait` 把任务 JSON 打到 stdout → V5 第一条红；
  - 受理机转发前先本地 `AdmitCarrier` → V3(a) 的「A 零写入」红。

### 5.2 运行入口

```
go test ./internal/workspace/ ./internal/store/ ./internal/orchestration/ ./internal/permgate/ -run 'B452|Readonly|Artifact|Detached' -count=1
go test ./internal/scheduling/ ./internal/ledger/ ./internal/ledgerstep/ ./internal/proto/ ./internal/client/ -count=1
go test ./internal/agentd/ -run 'B452|Dispatch|Frozen|CardStep|Events' -count=1
go test ./cmd/ -run 'B452|Dispatch|Wait|Template|Artifacts' -count=1
```

之后按 implement 三段律跑全量：`go test ./...`、`go vet ./...`、`gofmt -l .`（必须为空）。已知环境敏感用例见 memory「handoff env-sensitive tests」：`TestSessionSendActorResolution` 用 `env -u CLAUDE_CODE_SESSION_ID` 复跑；嵌套 worktree 下的 `TestProductionHTTPClientCallersAreGatewayOnly`。先按这两条复核，再判回归。

### 5.3 真机清单（**本节由协调者执行，不派发**）

前置：
- 账本按 Q12 裁决：荐 linux-01 PG 测试库。凭据只走 credentials.md，不进计划、不进对话。
- **绝不碰在役 7777 与生产账本**；在役 agentd 的进程与二进制全程不动，需要动在役机器的先报告回滚方式。
- 所有 CLI 命令一律带 `--config <草稿配置>`，草稿配置的 `ledger.dsn` 指向测试库、`agentd` 指向隔离实例。CLI 直接开账本，漏带 `--config` 会把 `template put` 写进生产账本。

步骤：

- **R1 靶子与 S1**：
  - linux-01 起隔离 agentd `t452-linux`：端口 7796，独立 HOME、datadir、config。
  - mac-mini 起隔离 agentd `t452-mac`：端口 7797。两者共测试库，设备表互登。二进制为分支构建，放草稿路径。
  - 载体 `t452-oc`：机器 t452-linux，opencode，模型取 mimo 同款或 `opencode/deepseek-v4.1-flash-free`，HOME 用隔离目录；先在载体机 `POST /api/squads/carriers/t452-oc/detect` 让它上线（B448 R1 的经验）。
  - 小队 `t452-run = [t452-oc]`。
  - 测试项目 `t452demo`：linux 侧建假 origin 仓并在两实例登记；mac 侧 clone，已 push。
  - `template put code-investigate --file skills/handoff/skilled/code-investigate.json`，receiver 改成 `t452-oc` 后写入测试库；两端 `template show` 同版本。
  - 证据：两端 `/api/status` 的能力位为 true，`squad list`。
- **R2 单机只读链路（S4、S5、S8 同机）**：在 linux-01 上用 CLI 指隔离实例：`dispatch --template code-investigate --prompt "列出 README 的二级标题，写一份 report.md 到产物目录" --wait`。断言：
  - stdout 是结论文本加 `artifacts: /…/tasks/<id>/artifacts`，且该路径是原路径；
  - `git worktree list` 派发中多一棵 detached、结束后消失；`git branch` 前后一致；
  - 任务 completed、archived；名额 0；
  - 无 `permission_request`（S8 风险 ④ 的真机腿；**若出现工单，记录实际权限事件载荷**，回主会话判是不是 opencode 没带路径）。
- **R3 跨机（S3、S5、S8 跨机）**：在 mac-mini 用 CLI 指 `t452-mac` 跑同一命令。断言：
  - 两端日志：受理机「转发」行与执行机任务行按任务 id 对上；
  - stdout 结论加 `artifacts: /var/folders/…/handoff-artifacts-<id8>-…`，`shasum` 与 linux 侧原文件一致；
  - mac 侧 `handoff show <id>`（不带 machine）当即可见（镜像行即时登记）；
  - 名额只在执行机侧起落。
- **R4 跨机失败**：停 linux 实例后在 mac 派发。断言：502 点名 `t452-linux`，测试库与 mac 任务表无新行。恢复后重派成功。
- **R5 亲和（S7）**：
  - (a) mac `--here` 对 `t452-oc` → 退出码 4，文案点名 t452-mac 与 t452-linux；
  - (b) 在测试库登记小队 `t452-mix = [t452-oc]`，mac `--here --receiver t452-mix` → 退出码 4，列出成员@机器；
  - (c) mac `--machine t452-linux` → 转发成功；
  - (d) 两条拒绝后两机任务表均无新行。
- **R6 产物事后拉取与保留期（S8）**：
  - mac `handoff artifacts <R3 的 id>` 再拉一次，内容一致；
  - 隔离实例配 `artifacts.retention: 2m`、重启，等 3 分钟后 linux 侧 `artifacts/` 消失，任务目录其余文件仍在；
  - `handoff artifacts <id>` 得 stderr「任务无产物」。
- **R7 S6 组合**：用入仓 JSON 原文（只换 receiver），对 handoff 自身仓库问一个真实调查题（例如「列出 `internal/agentd` 里所有调用 `releaseTaskCarrierOccupancy` 的位置并说明触发条件」）。断言 stdout 结论可读、产物里有完整报告。证据存台账。
- **R8 版本偏斜**：mac 实例换成基线 `a9150118` 构建（只拷贝，不动在役），在 mac 用新 CLI 发 `--template` → CLI 因能力位缺席拒发，零请求到达。再把 linux 实例换旧版、mac 用新版 → 受理机 502「版本过旧，不支持只读形态」。完事后换回。
- 收尾：停两隔离实例；按 Q12 记账保留或删测试库；删草稿 datadir 与 demo 仓。生产账本的 `template put code-investigate`（receiver=mimo）留到 finish 换版之后，单独向人请示。

**未验证，需真机**（汇总）：
- (a) opencode 写工作树外文件时，`external_directory` 事件是否带出可判的路径，使权限门命中 TaskDir（R2）。若不带，权限门 fail-closed 升级，S8 的「不建工单」不成立。这是**阻塞级发现**：不改权限门，回主会话。
- (b) 跨机 WS（`/ws/events?machine=`）经 relay 或直连能持续跟随一个 10 分钟以上的任务（R3 可让任务刻意多做几步）。
- (c) detached 树里 opencode 的 git 取证不出错（`rev-parse --abbrev-ref HEAD` 得 "HEAD"），trailer `{"summary":…}` 被判完成（R2）。
- (d) 自动归档的另起 goroutine 在真实 opencode serve 下不与 mediate 互锁（R2 任务确实到达 completed 且 serve 进程退出）。

## 6. 决策权限

**执行者可自行决定**（交回物「偏离与自主决定」逐条对照本表）：
- 新文件的拆分与命名；私有函数、帮手与测试命名。
- 无卡渲染函数与占位符替换函数的名字和所在文件（限 ledgerstep 或 ledger 包内）。
- 终态回调的接口名与 setter 形态（限「接口在 orchestration、实现在 agentd、组装点接线」）。
- 保留期清扫的时钟注入方式与 ticker 间隔（≤1h）。
- tar.gz 打包的实现细节（先落临时文件或内存，总上限 512 MiB 不变）。
- 日志措辞与级别。转发、自动归档、亲和拒绝、产物下载四处必须各有一行 Info 及以上日志，带任务 id 与机器名。
- `wait --final` 的「最后一回合」判定实现（流式加状态复核，或先读状态再定起点）。

**必须升级回主会话**：
- §8 任一冻结条目的改动（键名、状态码、退出码、输出格式、环境变量名、路径布局、能力位名）。
- 改动 §3「不许动」清单里的任何东西，尤其是为了让 S8 的「不建工单」成立去改权限门。
- 改写既有测试的断言。允许改写的只有：(1) 断言 `PlanContent` 全文、因 Q7 甲多出「## 产物目录」一节的测试；(2) 锁定「小队裸派发走 `Admit`」的测试（若有），改写为 `Select`→`AdmitFrozen` 语义。改前先列清单报协调者。
- 段 0 任一承重假设不成立。
- 发现自动归档必须同步调用，或必须改 `Done` 签名。
- 发现续接或重启恢复路径无法注入环境变量而不改 adapter。
- 发现某条故事在现有门面下做不到。

## 7. 缺陷族

| 族 | 在本卡的具体形态 | 验收责任 |
|---|---|---|
| 生命周期 / 状态机中断 | completed 之后、自动归档之前 agentd 重启，任务停在 waiting_review 占着名额与工作树；自动归档与人工 done 并发 | 启动补归档（V4）；`Claimed` 去重保证回调一次（V4 并发条）；R2 观察 (d) |
| 名额泄漏 | 编排内部 Done 不经 `handleDone`，名额不归还；受理机误先本地准入再转发 | 终态回调（V4 名额断言、变异锚点）；V3(a)「A 零写入」 |
| 静默失败 / 误导报错 | 老 agentd 忽略 `mode` → 主仓原地切分支；忽略亲和 → 静默换机；转发后回执丢失被当成「没建」 | 能力位（Q11、V3(e)、R8）+ 只读请求带 `new_worktree`；502 文案含「若请求已送达…」（V3(c)） |
| 门禁绕过 | 新写路径（产物目录）与权限门：同一规则的所有入口（Manager 路径与 OpenCode `approval.Client` 路径）都靠同一个 `Authority.Judge` 的 Scope；产物打包把符号链接带出机外 | V7 两条权限断言走 `Authority.Judge`；打包跳符号链接；客户端解包拒 `..` |
| 跨平台假设 | 临时目录在 macOS 是 `/var/folders/…`；tar 路径分隔；detached 树在 Windows 的路径 | 解包统一用 `filepath.FromSlash` 加 `..` 校验；R3 在 mac 验解包。Windows 不在本卡真机范围，记为假设 |
| 假红 / 假绿 | 跨机竖切若用 mock 对端会假绿（B448 教训）；`--wait` 的 stdout 断言若用 Contains 会漏掉多余行 | 段 0 第 1 条强制两 Server 真 HTTP；V5 用全等断言 |
| 序列化边界 | client 手搭 map、agentd 解码结构、转发重编码、`Task` 跨两跳应答、`StatusResp` 能力位，五处手写投影 | V8 全链路；缺席与零值分辨 |
| 枚举新值过白名单 | `mode=readonly` 流经工作区准备、`MayRecycle`、自动同步（`autoSyncAfterWait` 对空分支）、卡派发 `ViaTemplate`；`target=here` 流经 `ResolveTarget`、`RejectLegacyAlias` | V1 `ViaTemplate` 拒发；V4 `MayRecycle` 对 detached 托管树判回收；执行者 grep `Branch == ""` 与 `TaskStateWaitingReview` 的消费点，列进交回物 |
| 承重安全属性 | 「只读任务不进任何 git 分支」「写产物目录不建工单、写其他工作树外路径仍升级」「亲和拒绝零任务」 | V4 前后对照、V7 两条、V6 零任务断言，各有变异锚点 |

**观测项（不在本卡范围）**：小队裸派发改走 `Select` 后，满员竞态的结果从「换下一个成员」变成 409（Q2）；`IgnitionRequest.Target` 至今只进日志、Select 不读，记 roadmap 观察。

## 8. 需冻结的外部承诺

**入口判据成立**：本卡新增跨进程 wire（CLI↔agentd、agentd↔agentd，混版本互调），并新增主会话与 B453–B455 依赖的 CLI 输出契约。下列每条是一个独立可判 pass/fail 的断言，标出 producer / consumer。复用的既有契约不重述：冻结身份派发（B233.10/B398，`handlers.go:452-469`）、`X-Handoff-Forwarded` 一跳封顶（`forward.go:36-43`）、`completed.final_text`（`contracts.go:126-134`）、退出码 124。

**派发请求 `POST /api/tasks`**
- **F1**（producer CLI/受理机，consumer agentd）：新键 `mode`，取值 `"branch"|"readonly"`；缺席或空串等于 branch。
- **F2**（consumer agentd）：`mode` 为其他值 → 400，零任务。
- **F3**（consumer agentd）：`mode=readonly` 且 `branch`、`new_branch`、`worktree` 任一非空 → 400，零任务、零工作树。
- **F4**（producer CLI）：`machine`（string）与 `here`（bool）只在非零值时出现。
- **F5**（consumer 受理机）：`machine` 与 `here` 同时出现 → 400。
- **F6**（consumer 受理机）：亲和不满足 → **422**，体 `{"error":"机器亲和不满足：…","kind":"affinity_unmet"}`，零任务、零出站。422 在本端点只表示这一种拒绝。
- **F7**（producer 受理机）：转发请求带冻结身份四键与 `mode`，带 `X-Handoff-Forwarded: 1`；只读转发同时带 `new_worktree:true`。
- **F8**（consumer 受理机）：转发目标够不着 → 502，`error` 点名机器；目标能力位缺席且 `mode=readonly` → 502「…版本过旧，不支持只读形态…」。
- **F9**（producer 受理机）：转发成功时应答体是载体机应答的 Task JSON 原样，`target` 为载体机名。

**任务与状态 JSON**
- **F10**（producer 执行机）：`Task.mode` 键，空值省略。
- **F11**（producer 执行机）：`Task.artifacts_dir` 是执行机上的绝对路径 `<DataDir>/tasks/<id>/artifacts`，空值省略，不落库。
- **F12**（producer agentd）：`StatusResp.skilled_dispatch_supported`。true 当且仅当 F1–F9、F13–F16 全部实现。
- **F13**（consumer CLI）：能力位缺席或 false，且请求需要 mode、亲和或 `--wait` → 拒发，零派发请求。

**按机读取**
- **F14**：`/ws/events?task=<id>&machine=<M>`：M 非本机 → 转发到 M；带转发头时忽略 `machine`。
- **F15**：`GET /api/tasks/{id}?machine=<M>` 同 F14 语义。
- **F16**：`GET /api/tasks/{id}/artifacts[?machine=<M>]`：
  - 200 = `application/gzip` 的 tar.gz，只含普通文件与目录，条目为相对路径；
  - 204 = 目录不存在或为空；
  - 404 = 任务不存在或对端过旧（客户端先证明任务存在，再把 404 判为过旧，与 bundle 同法）；
  - 413 = 超 512 MiB。

**执行机环境**
- **F17**：执行器进程环境含 `HANDOFF_ARTIFACTS_DIR=<F11 同值>`。
- **F18**：产物目录在终态之后至少保留 `artifacts.retention`（缺省 168h），且不随工作树回收而删除。

**CLI 输出契约（skill 与后续卡依赖）**
- **F19**：`dispatch --wait` / `wait --final` 在完成时，stdout 的第一段是 `completed.final_text` 原文。
- **F20**：产物非空时，stdout 在最终文本之后另起**恰好一行** `artifacts: <本机绝对路径>`（Q9 甲；取乙则改为裸路径行）；产物为空时无此行。
- **F21**：退出码：0 完成；3 任务非完成告终；4 亲和拒绝；124 等待超时（任务照跑）；1 其他。
- **F22**：不带 `--wait` 的 `dispatch` 的 stdout 仍是单行任务 JSON（既有契约不变）。
- **F23**：`handoff artifacts <task>` 成功时 stdout 恰一行本机路径；无产物时 stdout 空、退出码 0。

**拍板记录**（同时满足难逆转、无上下文会惊讶、真取舍）：
- **CLI 侧解析模板，wire 不带模板名**（Q1）。被否方案是 agentd 侧解析。反过来写不会有任何测试变红；它决定了将来 Web 端要用熟练工时，要么在 Web 侧重做组装，要么把组装挪进 agentd。
- **422 专用于亲和拒绝**（F6）。被否方案是复用 400 加文本标记（`projectNotRegisteredMarker` 先例）。文本标记把错误措辞变成契约；选 422 之后，本端点将来的任何其他拒绝都不得用 422。

## 9. S6 示例定义草案（`skills/handoff/skilled/code-investigate.json`）

路径理由：放在教主会话用它的 skill 旁边，`template put … --file` 直接指这里；仓内现无模板 JSON 先例。名字 `code-investigate`。

```json
{
  "receiver": "mimo",
  "mode": "readonly",
  "target": "",
  "purpose": "investigate",
  "executor": "",
  "discipline": "",
  "prompt": "你是只读的代码调查员。你在一棵按基线提交检出的独立工作树里，这棵树会在任务结束后删除。\n\n规矩：\n1. 只读。不要修改、新建或删除工作树里的任何文件，不要 git add / commit / push，不要改分支。\n2. 需要落地的报告、清单、长摘录一律写到产物目录（路径见下文「产物目录」一节，也在环境变量 HANDOFF_ARTIFACTS_DIR 里），默认写 report.md。不要写到工作树或其他位置。\n3. 结论要有出处：每一条结论带 文件:行号；推测必须标「推测」。\n4. 查不到就说查不到，列出你查过的位置，不要编。\n\n最终回复格式（会被原样交给调用方）：\n## 结论\n（不超过 15 行，直接回答问题）\n## 依据\n- 文件:行号 —— 一句话说明\n## 产物\n- 写了哪些文件（只写文件名）；没有写就写「无」\n\n最后一行单独输出一行 JSON，作为本回合的收尾：{\"summary\":\"<50 字内结论摘要>\"}"
}
```

占位符：本定义不使用 `{{CARD}}`、`{{TITLE}}`、`{{ACCEPT}}`。`executor` 为空依赖 Q10；若 Q10 不取，填 `"opencode"`。

## 10. 核实读数（出稿时在基线 `e75fc97c` 上核对，详见台账 plan 节）

- `cmd/dispatch.go`：
  - `resolveBareDiscipline` 在 `:197-226`（spec 写 :199-226）；
  - 基线采集与脏检查在 `:262-276`，**只在 `targetName != ""` 时**（spec 写 :266-276）；
  - flag 在 `:348-370`。
- `internal/agentd/handlers.go`：
  - `dispatchRequest` 在 `:447-497`，冻结身份字段 `:456-469`；
  - `handleDispatch` 在 `:501-637`，冻结分支 `:526-556`，普通分支 `:559-566`；
  - 非本机判据 `IsSelfMachine` 在 `:589-597`（spec 写 :585-592）；错误映射 `:639-713`；
  - `handleDone` 与名额归还在 `:875-940`，`releaseTaskCarrierOccupancy` 在 `:993-1025`；
  - `handleEvents` 的未知任务 1008 在 `:1520-1540`；bundle 在 `:1229-1286`。
- `internal/agentd/cardstep.go`：卡节点冻结身份链路在 `:180-231`、`:402-439`；`scheddispatch.go:48-133`（`admitSquadStep` 用 `Select`）。
- `internal/agentd/receiver_bind.go:62-108`：裸派发载体走 `AdmitCarrier`、小队走 `Admit`。
- `internal/agentd/server.go`：
  - `IsSelfTarget`/`IsSelfMachine`/`ResolveTarget`/`clientForTarget` 在 `:554-664`；
  - 路由在 `:928-957`，`/api/tasks/{id}/…` 全包 `byTask`。
- `internal/agentd/taskroute.go`（byTask 查镜像转发）、`forward.go:36-77`（`?machine=` 与防环）、`pty_ws.go:27-34`（WS `?machine=` 先例）。
- `internal/workspace/mirror.go`：发现周期 30s 在 `:37`，只订阅非终态任务在 `:283-296`。
- `internal/scheduling/scheduling.go`：
  - `IgnitionRequest` 在 `:124-134`，其 `Target` 只进日志；
  - `AdmitCarrier` 在 `:420-461`，`Admit` 在 `:532-566`，`Select` 在 `:569-656`（spec 写 :569-656 吻合），`AdmitFrozen` 在 `:684-773`。
- `internal/orchestration/manager.go`：
  - `Dispatch` 在 `:680-1052`：占用守卫 `:833-845`，`ResolveBaseline` `:849`，`Prepare` `:899-905`，任务目录 `:921`，`startEnv` `:1012-1020`；
  - `Done` 在 `:1611-1735`，`MayRecycle` 在 `:1694-1700`；`Stop` 在 `:1753`；
  - `handleResult` 在 `:3425-3541`，completed 追加在 `:3477-3486`；
  - spec 的 `:1695-1720`、`:1814`、`:3479-3487`、`:837-846` 都有小漂移，结论不变。
- `internal/orchestration/watchdog.go:449-500`：启动对账只为非终态任务保留名额。
- `internal/workspace/gitworkspace.go`：
  - `WorkspaceReq` 在 `:354-365`，`PrepareWorkspace` 在 `:414-544`（spec 写 :347-544 吻合）；
  - `verifyBranchIdentity` 在 `:584-597`；`ResolveBaseline` 在 `:1218-1268`，`ErrBaseCommitMissing` 在 `:87`。
- `internal/permgate/permgate.go:68-77`：Scope 三处；`path.go:39-67` 判 `InScope`；`internal/approval/authority.go:180-184` 填 `TaskDir=<DataDir>/tasks/<id>`。
- `internal/executor/opencode/taskenv.go:50-80`：`external_directory: ask`；`adapter.go:1655-1700` 映射路径字段。
- `internal/proto/status.go:220-230`：能力位先例 `DisciplinesSupported`。
- `internal/ledger/templates.go:15-33`、`internal/ledgerstep/dispatch.go:240-480`（占位符 `:349-353`，吻合）、`:612-670`（`buildPrompt`）；`cmd/template.go:82`；`cmd/ledgercli.go:29-44`。
- `internal/executor/turn/text.go:18-29`：`FinalTextLimit` 取尾部 16K rune；`turn/protocol.go:40-50`：协议规则。
- `cmd/exit.go:20-23`：只有 1 和 124。
- 只读命令 `handoff squad list` 的读数见台账。
