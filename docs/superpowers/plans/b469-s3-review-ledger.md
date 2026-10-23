# B469 S3 独立审查裁决（技术阶段，2026-10-23）

## 偏离与自主决定

- 对照 r1.3 spec/plan、`b469-s3-contract.md` v1 与实施交回物首节，未见自行增加所有者 CRUD、第二答复面或绕开原票 CAS。审查者未改实现、未提交；按协调者要求仅写本审查裁决，并在 `/tmp/b469-s3-review/` 放只读 Go overlay 测试。
- 本轮只裁 S3 当前实现的技术阶段。单机真实 permission 闭环已有协调者的隔离运行证据；两台物理机器、实际桌面和真实模型判断仍是独立验收门，不能由本轮单测或 loopback provider 代替。

## 双轴裁决

| 轴 | 技术阶段 verdict | 证据与边界 |
| --- | --- | --- |
| 故事目标 | **局部通过；S3 最终未通过** | `origin_session` 经 CLI/client/agentd/orchestration/执行机持久化；原工单通知经来源订阅、单表版本路由、session/card wait 与 push/desktop 消费；无收件者保留待办，原票仍在执行机回答。`b469-s3-runtime-ledger.md` 已记录真实 opencode 进程加 loopback provider 的 permission→驻场短回合→原票 waiting_answer→重启后 reply，准入归零。真实双机与物理桌面未验。 |
| 架构与证据 | **已审路径通过；最终证据未齐** | 原任务/工单/答复仍归执行机 store，通知归 ledger；通知版本与寻址事件同一账本事务，镜像游标在通知成功后才推进。未修改 reply 答权。共享 PG 双连接及一次结构比较变异见 `b469-s3-pg-ledger.md`；它不等于双物理机交付。 |

交回物的「偏离与自主决定」已核：来源清单复用、同地址结构比较、单通知表、无 origin 的历史待答票仍保留待办，都在 contract v1 的边界内。序列化边界有 `origin_session` Go/JSON/TS 测试，真实单机 permission 来源也已穿过 CLI 环境与任务持久化；web `tsc -b` 与五文件 92/92 见运行台账。本次独立新鲜定向复跑 `go test -mod=readonly ./internal/ledger ./internal/ledgermirror ./internal/agentd ./cmd -run 'Test(TaskTicketNotice|MirrorDiscoversBareLocalOriginTaskWithoutRemoteTargets|MirrorInventoryBlackholeDoesNotBlockHealthyMachine|MirrorNoticeFailureDoesNotAdvanceSourceCursor|MirrorInventorySlowHealthy|MirrorInventoryTransient|CardWaitSuppressesLegacyMirroredTicketWhenNoticeExists|SessionWaitRejectsUnknownTicketNoticeKind|CardWaitReceivesAddressedTaskTicketNoticeAndAcknowledgesIt)' -count=1` 四包全绿。

## Finding 核销：对抗输入与红→绿

以下红测均为审查者在独立 `/tmp` overlay 注入的实际断言；实施者在共享工作树修复并加入正式回归后，审查者撤掉旧假设、按最新源码重跑。六个核心 overlay 于本轮再次全绿，未将施工中的暂时编译错误计为 finding。

1. **F1 卡席三态路由（已核销）**：合法 pull 席位卡未加入 session 时，旧路由产出 `needs_human/origin_unknown,target=nil`；`route_no_session_test.go` 红，修后保留 `seat(card,identity,session="")` 绿。模拟历史坏状态「子卡空席却标 pull、父卡合法 push」时，先错误投向空身份子卡，第一轮修后又错误落 needs_human；`invalid_pull_test.go` 红，补非空席位判据并上走祖先后绿。两项最新 overlay 命令 exit 0。
2. **F2 来源恢复与游标（已核销）**：审初 `Mirror.subscribe` 先提交 mirrored source_seq 再写 notice；通知写失败会让重连跳过原事件。当前实现改为先 notice 后推进游标，失败注入的 `mirror_notice_window_test.go` 及正式 `TestMirrorNoticeFailureDoesNotAdvanceSourceCursor` 均证明重订从旧 seq 回放。仅遍历远机池曾漏掉本机未挂卡任务；`local_inventory_test.go` 红，现独立 `Options.LocalTasks` 生产注入并有正式回归。顺序清单曾让坏机器卡住健康机器；`inventory_block_test.go` 红。其后 300ms 聚合截止又会永远饿死 450ms 健康清单、暂错撕掉已订阅流；`inventory_recovery_test.go` 两输入红，现并发有界查询及 last-good 保留后绿。当前正式测试也绿。
3. **F3 通知 wire fail-closed（已核销）**：未知 `ticket_kind` 曾被 ledger Put 接受，`session wait` 仍 hit 并可能推进游标；`unknown_notice_test.go` 红。现 Put 限 `ask/gate` 且拒残缺地址，session/card wait 与自动化/桌面入口校验来源 seq、版本、kind、目标；正式 `TestTaskTicketNoticeRejectsUnknownKindAndEmptySeat`、`TestSessionWaitRejectsUnknownTicketNoticeKind`、`TestInvalidTicketNoticeWireDoesNotRingOrAcknowledge` 通过。原始 overlay 依赖「Put 接受未知值」，修后已失效，不再作为绿测使用。
4. **F4 投递前换责窗口（已核销）**：通知 v1 后但 mirror 下一轮刷新前，卡席 old→new 换绑、驻场 old→new 换人，旧 wait 曾仍把 old target 当当前归属；`card_wait_rebind_test.go` 与 `session_reseat_test.go` 各红。现 card wait 复读卡席身份/模式，session wait 复读当前驻场者，两个 overlay 本轮绿；正式换责回归也已加入。
5. **F5 同票两轮重复唤醒（已核销）**：通知事件先消费并确认 delivered，随后同源 `task_mirrored(question)` 再消费，旧逻辑起第二个真实 runner Resume；`duplicate_wake_test.go` 红，日志有两次独立 claim/准入、`resumes=2`。现自动化与 card wait 以来源机器/任务/票 ID 查已登记 notice，抑制旧镜像唤醒；无 notice 的历史事件保留旧消费路径。两轮 overlay 本轮绿，正式 `TestTaskTicketNoticeSuppressesLaterLegacyMirrorWake` 与 `TestCardWaitSuppressesLegacyMirroredTicketWhenNoticeExists` 通过。

本轮六个最新 overlay 复跑命令分别使用 `/tmp/b469-s3-review/{route,invalid-pull,duplicate-wake,inventory-recovery,card-wait-rebind,session-reseat}-overlay.json`，对应 `agentd`、`ledgermirror`、`cmd` 测试全部 exit 0。原始红的具体输入与测试源保留在同目录；共享实现未由审查者修改。PG JSONB 同址重放的独立编译通过变异红→恢复绿由协调者记于 `b469-s3-pg-ledger.md`，本审查不冒认其执行。

## 最终验收仍缺

- `linux-01` 已被选为第二台物理测试机；尚需在两机同测试 PG 下实际断连、来源补发、驻场/卡席换责、只在目标机交付、重启恢复及未知/缺字段反例。不能把两条 PG 连接或本机 fake inventory 当成双机。
- 实际桌面信箱与 deep-link opener 的 OS 行为，以及真实模型对原 permission 的判断，仍无相应实测。loopback provider 只证明 CLI/adapter/通知传输链。原 question 的实际工具运行结果由协调者另核，不在本审查台账提前宣称。
- 完整触及 Go 包、web 与物理环境的最终记录由协调者汇总；本裁决不代替父卡全量、真实多环和归档裁决。当前无未核销的 S3 源码 finding，最终故事轴仍保持未通过。
