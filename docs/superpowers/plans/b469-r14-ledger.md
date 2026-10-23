# B469 r1.4 审查修复台账（2026-10-23，已拍板）

## 偏离与自主决定

本轮只改 spec/plan/契约/roadmap 文档，不实施 I1–I7、不声明 S2/S3 通过。r1.3 是已批准基线；用户已授权的三项裁决来自 B469 #33153/#33182、B463 #33181。协调者已于 2026-10-23 分段核定 S2 I1/I2+Minor、用户 C 确认级别和未知 kind 跳过/Warn/推进游标；I5 准确 wire 随后由根协调者核定，见本台账拍板节。更强确认、S3 独立合并资格、未知 kind 游标不是本稿自行裁决。桌面 OS 行为按用户决定保留未验。

**r1.4 政策澄清附注（用户已批准）**：用户随后明确允许按真正只取名称修正，历史 `env | cut` 保持人工。此前把该行式管线列为名字正例是事实错误，不能以旧卡措辞覆盖真实输出。Bash `compgen -e` 改为名字候选，仍由模型裁决；不新增 Perl/NUL 静态闭集、不扩静态 Allow。

合成隔离实验只设置 `PATH` 与值为 `dummy\nLEAK_SENTINEL` 的 `SECRET`，没有读取实际环境值。`env | cut -d= -f1 | sort` 的 stdout 含合成值第二行哨兵（`contains_value_sentinel=true`）；`compgen -e` 不含该哨兵（`false`）。行式管线再接 `sort` 或 `wc`/计数无法恢复“只列名”属性。本机 `env -0` 可运行，BSD `cut -z` 报不支持；NUL 感知实现只作后续实验读数，本期不承诺支持或为脚本解释器开例外。真实模型与审批链证据仍须在隔离环境补足。

## 审查输入与本轮判据

独立审查 B463 #33179 判 S2 fail，B469 #33180 判 S3 fail；审查快照 `c93fbb627`，已推送。审查者报告 `go test ./...` 68 包、vitest 2045、tsc、eslint 无错误；这些只是旧快照绿灯，不能覆盖变异未红的 I1–I7。S3 既有双机 CLI/print 基础链、离线补投、换责与持久游标证据，见 `b469-s3-runtime-ledger.md`，不等于原生桌面或 PG 验收。S1/S4 尚在 plan 列，本轮无实现。

| 审查点 | 现行源码边界与根因读数 | r1.4 修复/验收责任 |
|---|---|---|
| I1 | `internal/orchestration/approver_context.go` 的模型上下文把 env/printenv 一概升人工；`internal/permgate/credential.go` 的 env 检测丢完整管线结构。root/Luna 核到 `credSegments/Words`、`credEnvDump` 与 `SecretEnvCommandsText` 需同源修。合成值含换行证实历史 env-cut 行式“只列名”说法不成立。 | S2 执行者按真实输出区分变量名和值，不扩静态 allowlist；Bash `compgen -e` 作真正只取名的模型候选，历史 env-cut（含 sort/计数）保持人工；真实模型隔离复核 help 原票、compgen 与危险反例。裸 `printenv`/`env` 实际输出值，也不能算只列名。 |
| I2 | `internal/orchestration/approver.go` 本地候选链失败/结果日志可带 permission、cause、output 原文；真实入口 `Decide→decideLocalChain→decideWithCandidate`。 | S2 执行者逐支脱敏 `attempt.Err/final.Err`、env resolver cause、Invoke permission/cause/reply.Text 与 nonce permission，默认 Warn + error_kind + 哨兵与 m3c 变异；独立日志复审。 |
| I3 | `cmd/session.go` 候选 SQL、notice.Target 与当前 Target 三层身份过滤缺反例锁。 | S3 执行者分别在生产候选查询、wire target 校验、current target 校验三层本地入口锁 other 反例；整条 wait 链不收/不标 delivered，三层同时拆掉须红，不要求任意删一层都破整链。 |
| I4 | `internal/agentd` 通知投递测试没有原执行机 store 待答票，误调 `AnswerTicket(allow)` 不红。 | S3 执行者以真实待答 gate/ask 票证明投递后仍待答，只有原 reply CAS 能改。 |
| I5 | `internal/ledgermirror/mirror.go:reconcile` 每 10s 对每机枚举全部 `/api/tasks`；`internal/store/store.go:ListTasks` 无界读；`m.addInventoryTask` 旧过滤 `(origin非空 || state=waiting_answer)` 会使无 origin 的新 running task 在 creation 游标前进后、后来才 question 时漏掉。活动通知含 delivered 每轮刷新；`internal/agentd/taskticketnotice.go:RefreshTaskTicketNotice` 复用 Put，而 `internal/ledger/task_ticket_notice.go:PutTaskTicketNotice` 对未变地址仍进事务/更新时间。现有源 store 没有全局 task-created cursor/API 或 `created_at` 索引；`CreateTask` 单独 INSERT，`tasks.id` 为文本 PK，现源码未见删任务路径。ledger 已有 RegistryGet/Put CAS JSON，mirror_cursors.last_seq 是别的事件水位。 | S3 执行者在 CreateTask 同事务落窄 task ID→自增 seq，旧任务一次回填；agentd/client/proto 按已核定 v1.1 wire 提供有界页，ledgermirror 用 registry CAS 保存每机水位，**所有非终态 task 都建事件责任，终态有待答票也补拉**；重启从 0 分页重建未出票订阅、稳态按水位增量，逐 task event seq 补齐。新路径 404 告警不回落每10s全扫；活动 notice 可读时重解析，但同址/同状态不写。测无 origin 后来 question、时钟回退/同 timestamp、分页断连、终态待答、重启和零写；无新 PG 表，若确需 DDL 先评估 3→4。 |
| I6 | `task_ticket_notice` 解码错误现可返回给自动化循环而卡住 cursor；`internal/proto/delivery.go#ValidateTaskTicketNotice` 注释明确写 cursor 不推进，`internal/agentd/taskticketnotice_consumer_test.go` 旧测试锁的是非法即报错；`b435-contract.md` 旧第 4 点只说“不唤醒”，S3 v1 曾写“不上推游标”。 | B435 r1.4/S3 v1.1 同步已授权口径：bad kind/payload 跳过+Warn(kind/seq/source，无正文)+推进游标+继续好事件；改旧注释/测试，各消费者用真实候选链回归。 |
| I7 | `origin_session` 有 store 单侧测试，但无 POST→store→GET 串行化穿透断言。 | S3 执行者经真实 agentd HTTP 与 store 测精确往返、缺失/非法边界；删字段投影要红。 |

## Minor 逐项归属

| 事项 | 本轮处理 |
|---|---|
| S2 命令替换、进程替换、if、函数体内 handoff 正例变异不红；`strings.Contains(word,"c")` 偏保守；引号/变量动态例仍 consult | `Gate.Judge` 穿透正反例补锁；准确识别 shell option；动态不可证的三个例逐项记为保守 consult，不能声称误判已全修。 |
| desktop 门铃失败重扫绕过退避 | 恢复扫描按有效 retry_after/退避，到期才重试；离线/失败不忙轮询。 |
| retry 读失败 500 与前 10000 事件截断 | 读失败 503 且状态不变；按来源 seq/分页越界定位原工单，过 10000 反例可红。 |
| B435 头部“源码尚未实现”和 kind 字面值缺失 | 本稿改为“已有 S3 源码但未验收”，冻结 `task_ticket_notice`、`gate`/`ask`。 |
| ListActiveTaskTicketNotices 未按任务过滤、retry_after 从未写 | 查询最窄源 task 范围；退避列若保留即生产写/按到期消费，否则删假承诺并重审 wire。 |
| 产物无 commit/manifest、错误日志都说目标版本变化 | 证据 JSON 记 commit，入库 manifest 记录命令/隔离环境/脱敏 hash；版本冲突与 DB/网络错误分开日志。 |
| UI 新状态/重试无原型确认记录 | S3 UI 执行者补已批准原型版本、状态与实现差异记录；用户已明确桌面原生行为未验，不得以原型记录代替真机。 |
| agentd 规模与 Server 方法数 | 记入 roadmap 的用例竖切债，本轮不重构。 |

## 独立工作与验收边界

Luna 分别负责 S2 的 I1–I2/语法 Minor 与 S3 的 I3–I7/S3 Minor，不能相互当审查者；协调者核契约、运行真实模型隔离实验、PG 与双机系统实跑并拍板。I5 若需要新跨进程查询 wire，先把确切字段、索引、分页终点与旧客户端行为送协调者核定，不能把备选全局总线直接做出。S3 满足 I3–I7、独立复审及隔离 PG 测试后取得独立合并资格；schema 2→3 的 fleet 必须同批换版，合并和升级另经用户动作门。S1 按用户 C 解阻后依原顺序开始，S4 随 U1/U4。

测试 PG 凭据位于 `/tmp/b469-p0-runtime/test-db.json`（0600），仅协调者读取并注入 `LEDGER_TEST_PG_DSN`；任何文档、日志、工具输出不回显 DSN。真实模型只复用现有已授权通道作合成只读判词，Handoff 测试 PG/agentd/HOME/工作区隔离，单一认证值仅进入隔离进程环境，不落盘/不入 argv/不复制 authstore；不读实际 env 值。一次性 test artifact 保存脱敏报告与 commit/manifest。

## 图覆盖债

本 checkout 无 `codegraph/`，按上述生产源码与审查记录定位。后续有图时优先补 `Mirror.reconcile`、`RefreshTaskTicketNotice`、`taskTicketNoticeWake`、`runSessionWait` 的符号与调用关系；本轮不虚构图命中。

## 根协调者拍板（2026-10-23）

独立 Sol 出稿后，根协调者核定 r1.4 全部文档与 S3 contract v1.1。用户已授权的 C 确认级别、S2 原范围和非法通知跳过语义直接落盘，不再重复请批。I5 选择源 SQLite 的自增创建序与 CreateTask 同事务；现有 ledger registry CAS 存每源机发现序，无新增共享 PG 表，保留2→3机队约束。全部非终态任务（含无 origin、尚未出票）都纳入 task 事件责任，重启从0有界重建；精确 endpoint/字段与 source404可观测行为按契约，不回退周期全量扫描。

I1/I2/S2 Minor 已先分段交 Luna 施工；I3/I4/I6/I7 先交另一 Luna，现 I5 wire 一并放行。实际源迁移与分页实验若推翻约定，执行者须上报，不自建新事件总线。root负责真实模型、PG、双机回归；独立审查者负责变异与两轴裁决。S1已解阻，待当前复审修复交回后按 U1 继续。

根协调者核定并提交用户取名修订：`git commit -m "docs(approval): correct names-only environment policy after synthetic counterexample"` 原始输出：`[codex/approval-batch-b469 445d50e6b] ...`、`3 files changed, 9 insertions(+), 5 deletions(-)`。随后只 amend 一次收本记录，不追写 amend 后 hash。
