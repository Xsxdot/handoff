# B469 S3 独立审查裁决（技术阶段，2026-10-23）

## 偏离与自主决定

- 对照 r1.3 spec/plan、`b469-s3-contract.md` v1 与实施交回物首节，未见自行增加所有者 CRUD、第二答复面或绕开原票 CAS。审查者未改实现、未提交；按协调者要求仅写本审查裁决，并在 `/tmp/b469-s3-review/` 放只读 Go overlay 测试。
- 本轮只裁 S3 当前实现的技术阶段。协调者已在 Mac 与用户指定的 `linux-01` 两台物理机器上跑通隔离的原生 permission/question 基础链；离线补投、换驻场、卡路由联合矩阵、实际桌面与真实模型判断仍是独立验收门，不能由 loopback provider 代替。

## 双轴裁决

| 轴 | 技术阶段 verdict | 证据与边界 |
| --- | --- | --- |
| 故事目标 | **局部通过；S3 最终未通过** | `origin_session` 经 CLI/client/agentd/orchestration/执行机持久化；原工单通知经来源订阅、单表版本路由、session/card wait 与 push/desktop 消费；无收件者保留待办，原票仍在执行机回答。`b469-s3-runtime-ledger.md` 已记录单机原 permission/question 闭环及 Mac→`linux-01` 两物理机的原生 gate/ask 基础链：真实 opencode CLI、隔离测试 PG、原票答复回传和准入释放。离线恢复、换驻场与物理桌面未验。 |
| 架构与证据 | **已审路径通过；最终证据未齐** | 原任务/工单/答复仍归执行机 store，通知归 ledger；通知版本与寻址事件同一账本事务，镜像游标在通知成功后才推进。未修改 reply 答权。共享 PG 双连接及一次结构比较变异见 `b469-s3-pg-ledger.md`；它不等于双物理机交付。 |

交回物的「偏离与自主决定」已核：来源清单复用、同地址结构比较、单通知表、无 origin 的历史待答票仍保留待办，都在 contract v1 的边界内。序列化边界有 `origin_session` Go/JSON/TS 测试，真实双机基础链也已穿过 CLI 环境与任务持久化。最终冻结快照 10 个触及 Go 包整包、PG 双连接与显式重试、触及包 vet、Mac/Linux 构建、web `tsc -b` 与五文件 92/92 全绿，详见运行台账；全仓 Go 行为测试未运行。本次独立新鲜定向复跑 `go test -mod=readonly ./internal/ledger ./internal/ledgermirror ./internal/agentd ./cmd -run 'Test(TaskTicketNotice|MirrorDiscoversBareLocalOriginTaskWithoutRemoteTargets|MirrorInventoryBlackholeDoesNotBlockHealthyMachine|MirrorNoticeFailureDoesNotAdvanceSourceCursor|MirrorInventorySlowHealthy|MirrorInventoryTransient|CardWaitSuppressesLegacyMirroredTicketWhenNoticeExists|SessionWaitRejectsUnknownTicketNoticeKind|CardWaitReceivesAddressedTaskTicketNoticeAndAcknowledgesIt)' -count=1` 四包全绿。

## Finding 核销：对抗输入与红→绿

以下红测均为审查者在独立 `/tmp` overlay 注入的实际断言；实施者在共享工作树修复并加入正式回归后，审查者撤掉旧假设、按最新源码重跑。六个核心 overlay 于本轮再次全绿，未将施工中的暂时编译错误计为 finding。

1. **F1 卡席三态路由（已核销）**：合法 pull 席位卡未加入 session 时，旧路由产出 `needs_human/origin_unknown,target=nil`；`route_no_session_test.go` 红，修后保留 `seat(card,identity,session="")` 绿。模拟历史坏状态「子卡空席却标 pull、父卡合法 push」时，先错误投向空身份子卡，第一轮修后又错误落 needs_human；`invalid_pull_test.go` 红，补非空席位判据并上走祖先后绿。两项最新 overlay 命令 exit 0。
2. **F2 来源恢复与游标（已核销）**：审初 `Mirror.subscribe` 先提交 mirrored source_seq 再写 notice；通知写失败会让重连跳过原事件。当前实现改为先 notice 后推进游标，失败注入的 `mirror_notice_window_test.go` 及正式 `TestMirrorNoticeFailureDoesNotAdvanceSourceCursor` 均证明重订从旧 seq 回放。仅遍历远机池曾漏掉本机未挂卡任务；`local_inventory_test.go` 红，现独立 `Options.LocalTasks` 生产注入并有正式回归。顺序清单曾让坏机器卡住健康机器；`inventory_block_test.go` 红。其后 300ms 聚合截止又会永远饿死 450ms 健康清单、暂错撕掉已订阅流；`inventory_recovery_test.go` 两输入红，现并发有界查询及 last-good 保留后绿。当前正式测试也绿。
3. **F3 通知 wire fail-closed（已核销）**：未知 `ticket_kind` 曾被 ledger Put 接受，`session wait` 仍 hit 并可能推进游标；`unknown_notice_test.go` 红。现 Put 限 `ask/gate` 且拒残缺地址，session/card wait 与自动化/桌面入口校验来源 seq、版本、kind、目标；正式 `TestTaskTicketNoticeRejectsUnknownKindAndEmptySeat`、`TestSessionWaitRejectsUnknownTicketNoticeKind`、`TestInvalidTicketNoticeWireDoesNotRingOrAcknowledge` 通过。原始 overlay 依赖「Put 接受未知值」，修后已失效，不再作为绿测使用。
4. **F4 投递前换责窗口（已核销）**：通知 v1 后但 mirror 下一轮刷新前，卡席 old→new 换绑、驻场 old→new 换人，旧 wait 曾仍把 old target 当当前归属；`card_wait_rebind_test.go` 与 `session_reseat_test.go` 各红。现 card wait 复读卡席身份/模式，session wait 复读当前驻场者，两个 overlay 本轮绿；正式换责回归也已加入。
5. **F5 同票两轮重复唤醒（已核销）**：通知事件先消费并确认 delivered，随后同源 `task_mirrored(question)` 再消费，旧逻辑起第二个真实 runner Resume；`duplicate_wake_test.go` 红，日志有两次独立 claim/准入、`resumes=2`。现自动化与 card wait 以来源机器/任务/票 ID 查已登记 notice，抑制旧镜像唤醒；无 notice 的历史事件保留旧消费路径。两轮 overlay 本轮绿，正式 `TestTaskTicketNoticeSuppressesLaterLegacyMirrorWake` 与 `TestCardWaitSuppressesLegacyMirroredTicketWhenNoticeExists` 通过。

本轮六个最新 overlay 复跑命令分别使用 `/tmp/b469-s3-review/{route,invalid-pull,duplicate-wake,inventory-recovery,card-wait-rebind,session-reseat}-overlay.json`，对应 `agentd`、`ledgermirror`、`cmd` 测试全部 exit 0。原始红的具体输入与测试源保留在同目录；共享实现未由审查者修改。PG JSONB 同址重放的独立编译通过变异红→恢复绿由协调者记于 `b469-s3-pg-ledger.md`，本审查不冒认其执行。

## 最终验收仍缺

- Mac 与 `linux-01` 的原生 gate/ask 两轮基础链已完成并落脱敏报告：`docs/superpowers/evidence/b469/dualhost-permission.json` 与 `dualhost-question.json`。两轮均由真实 CLI 创建原票、Mac 驻场收通知、Linux 原票答复成功，且自有任务/进程/隧道清理成功。它们不覆盖 A 离线时待办、重启唯一短回合、`stand`→B `launch` 后新目标投递、卡席换责，也不覆盖桌面 OS 门铃。
- 双机恢复脚本 `/tmp/b469-p0-runtime/dual_host_recovery_probe.py` 已做只读静态审查，尚未由本审查者执行。它使用已有会话 `summary.members[kind=resident]` 与 launch 返回值核新驻场、从原票 notification 投影核状态/版本；投影 wire 只有 `status/reason/ownership_version/last_error`，**不含 target**。脚本日志的 old/new target 是由当前会话驻场推导，不可冒称直接读到通知实体 target。恢复唯一一轮目前在 delivered 当下比较 coordinator fixture 请求数，尚无后续稳定窗口排除迟到重复回合。离线票出现后立即读取 notification 投影，异步镜像若尚未落账会误判，应等到 pending/retryable 状态再裁。`finally` 正常路径按自有 task→A agentd→PID 核验 B agentd→隧道顺序清理；B API 或 SSH 断连时只能记录失败，不能保证进程已停。这些边界须在物理运行报告里如实限界或补直接证据。
- 实际桌面信箱与 deep-link opener 的 OS 行为，以及真实模型对原 permission 的判断，仍无相应实测。loopback provider 只证明真实 CLI/adapter/通知传输链，不证明模型自主审批质量。当前无未核销的 S3 源码 finding；最终故事轴仍保持未通过。

## 协调者补证（独立审查者已只读复核，18:00）

上述恢复脚本的三项证据缺口已修正后，由根协调者亲自运行；sol 审查者随后只读检查原始 `dual-6690594252/report.json` 与脚本，回传确认：实际 PG SELECT 读到通知 target；A 离线 pending v1，原配置恢复 delivered v1 且3秒内主请求增1/槽位0；再次停A后，现有 stand/launch 将当前驻场换到B，实际通知 target 转新B身份 delivered v2；原两张 ask 都经原答复入口回传，三任务/远端PID/隧道清理无失败。该证据现落 [`dualhost-recovery.json`](../evidence/b469/dualhost-recovery.json)。

因此“双物理机离线补投与当前驻场跨机换责”本轮通过。上节静态审查指出的投影无 target、无稳定窗及竞态是修改前记录，已由直接实体查询、3秒观察和等待持久化消除。此补证不代替卡席位联合矩阵、桌面OS门铃或真实模型判断；S3最终故事轴暂保持未通过。

## 协调者卡路由补证与 sol 回传

根协调者 `route-78355cfa` 双机矩阵 exit0，sol 只读核原始 report 和最终脚本后回传：**目标轴，本轮卡路由/通知阶段目标有物理双机通过证据；证据轴，阶段可采信；技术阶段建议通过。** 五路结果、原票 CAS 与自有进程清理见 [`dualhost-card-routes.json`](../evidence/b469/dualhost-card-routes.json) 和运行台账。

本轮用新 schema 隔离此前测试配置，四条连接路径相同且启动前核 current_schema；未改旧 roster。边界同时回传：LinkTask 临时夹具不是完整 card dispatch；loopback fixture 不是实际模型判断；显式 --since 不是默认持久游标；桌面按用户原话暂留未验。父故事最终验收继续受这些明示边界约束，不将阶段通过写成整批最终通过。

## 协调者默认游标补证与阶段裁决

根协调者 `cursor-ce88fa5d` 窄实验 exit0，sol 随后只读核脚本与报告，确认两次独立 CLI 均不带 --since：第一次唯一通知 delivered 且原票待答；新进程第二次等3秒退出124、无重复；精确 resident PG 持久行 last_seq 2→2。原票 Continue 回传、源任务槽0、自有远端进程/隧道清理均通过，A已提前停止exit0。见 [`dualhost-session-cursor.json`](../evidence/b469/dualhost-session-cursor.json)。

当前阶段裁决：已实现的 S3 原票收件、路由与通知技术阶段目标轴通过、证据轴可采信。默认游标实测范围为同 resident 身份、单条 notice、顺序跨 CLI 进程；换责/并发/崩溃游标窗口不从本报告推断。完整 card dispatch 未在卡路由实验执行（使用公开 LinkTask 夹具），实际模型自主判断未验；桌面 OS 按用户原话暂留未验。S1可信管理写入尚未实现，不能将本轮阶段结果冒称四卡最终验收。
