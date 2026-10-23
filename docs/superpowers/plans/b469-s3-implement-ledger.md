# B469 S3 实施台账

## 偏离与自主决定

- 在核定 v1 的允许边界内完成了 origin 传播、未挂卡任务发现、通知账本/路由版本、事件寻址、session/card wait 收件、任务详情/web 通知状态投影及通知重试入口。resident print/desktop 外部渠道确认由并行实现者接线中；双机验收仍未完成，不能称 S3 全部完成。
- 新增 `origin_session` 采用 `ses`/`local_` 以外也兼容的可移植 `[A-Za-z0-9_-]` 形状校验，空值表示未知；该格式选择属于必要的内部校验实现，未证明会话存在性。
- 新增通知记录只拥有通知去向与投递状态，不介入答案 CAS。通知目标重放按 `DeliveryAddress` 字段结构比较，不能以 JSON 文本字节比较（PG JSONB 会规范空格/键序）；这项调整由真实 PG 并发/重放验证发现并修正。
- 对无卡且无 origin 的历史 `waiting_answer` 任务仍扫描来源事件，投影为 `needs_human/origin_unknown`；缺少来源会话是待办，不是跳过来源的理由。源清单其余非等待态旧任务不全量扫，避免无票历史任务持续回放。
- PG DDL 在本批共享分支尚未发布的 schema 3 上合并；版本闸指纹更新为最终当前 DDL。发布后再增 DDL 时须新升版本。

## 已完成

- 裸派发新增 `--origin-session`，缺省读取 `HANDOFF_ORIGIN_SESSION`；显式 flag 优先。空值不发送、不推断。
- 字段经 client Dispatch JSON、agentd 请求 DTO、编排 DispatchReq、proto.Task 进入执行机 SQLite tasks 表；老库迁移默认空串，不回填旧任务。
- TS Task 镜像增加可选 `origin_session`。
- 会话创建驻场首轮与 print 驻场续轮注入 `HANDOFF_ORIGIN_SESSION=session:<id>`；resume 时只保留此来源键，仍剥离卡席位身份键。
- 测试先红后绿：任务持久化回读测试缺列时断言红，增加 DDL/迁移/读写后绿。
- `task_ticket_notices` 以 (source_machine, task_id, ticket_id) 唯一定位，存来源 seq、ticket kind、地址、ownership_version 与闭集状态。PG 通过账本 `mutate` 事务序列化更新；版本变更只在地址结构有变化时递增，同址重放不另发事件，过期版本状态写入失败，answered/superseded 不被源重放复活。
- 镜像通过既有任务列表加每 task 持久事件流纳入未挂卡任务；涵盖远端和本机 `origin_session` 非空任务、无 origin 的 `waiting_answer` 任务。自机清单单独读本机 taskStore，不塞进远端 pool `Names`。远端 inventory 并发请求、每机最多等待 2s；失败保留该机上次成功快照，成功空清单才移除任务。卡任务仍复用 `AllTaskLinks`，不会伪造 `card_tasks`。
- 通知路由优先卡 push/祖先，保留 origin 自身 pull 的 card-wait 地址；只有无卡路由才读取 origin 会话当前登记驻场者。无 origin/无驻场者形成可见待办。账本重解析活动通知以驻场变化刷新 owner version。
- 新事件只在一个 seat/resident 目标明确时产生。session wait/card wait 先重读通知当前目标、版本、状态，stdout 成功后才标记 delivered；by_system/pointer 等普通房间事件候选语义未改。
- 有效卡席不要求已加入 session；独立 pull 卡以 `card+identity` 地址供 card wait 收取，空 session 不降级为 origin fallback。镜像先持久化通知路由再提交 source cursor，通知写入失败会让来源事件重放，避免先推进 watermark 后丢通知。
- GET `/api/tasks/{id}` 的既有待答工单附加 `notification` 投影；CLI `show` 返回的 AttachInfo JSON 同样保留该字段，web 工单面板显示状态/原因/最近错误，答复路径不变。
- `POST /api/tasks/{id}/tickets/{ticket}/notification/retry` 只接受 `{}`，从本机权威源重读任务/工单/pending truth 和原始问题事件，再按当前公开路由事实重解析；404 表示任务或票未知，409 表示票已答/void/非待答，源不可用返回 503。重试不改变 answer CAS/答权；同一目标的 needs_human 重试保留 ownership_version 并重新发寻址事件，状态转移使用当前版本 CAS。

## 验证与未验证

- 已通过：`go test ./internal/store -run TestTaskOriginSessionPersistsAndLegacyRemainsUnknown -count=1`。
- 已通过：`go test ./internal/store ./internal/orchestration ./internal/client ./internal/agentd -run 'TestTaskLifecycle|TestB460|TestDispatch|TestCoordinatorRunner|TestTaskOrigin' -count=1`。
- 已通过：`go test ./internal/proto ./internal/store ./internal/client ./internal/agentd -run 'TestValidateOriginSessionID|TestTaskOriginSessionPersistsAndLegacyRemainsUnknown|TestDispatchOriginSessionWireAndOmission|TestB444PushResidentResumesOnHumanMessage' -count=1`。
- 已通过：`go test ./internal/agentd -run 'TestB444ResidentResumeHasNoSeatEnv|TestB444PushResidentResumesOnHumanMessage' -count=1`。
- 已通过：`go test ./internal/ledger -run 'TestSchemaVersionPinsDDL|TestTaskTicketNoticeRoutingIsIdempotentAndAddressed' -count=1`；重放测试包括 PostgreSQL JSONB 样式的空格/键序规范化。
- 已通过：`go test ./internal/agentd -run TestTaskTicketNoticeReparsesCurrentResident -count=1`；先无驻场产生 needs_human，驻场注册/换人后刷新到新地址并递增责任版本。
- 已通过：`go test ./internal/agentd -run 'TestTaskTicketNoticeKeepsStandalonePullCard|TestRetryTaskTicketNotificationHTTPRoundTrip' -count=1`；包括空 session 独立卡席保留和 client→agentd HTTP 重试/404/409。
- 已通过：`go test ./internal/ledger -run 'TestRetryTaskTicketNotice|TestTaskTicketNoticeRouting' -count=1`；同址失败重试发出一次事件、版本不变，重放不二次唤醒。
- 已通过：`go test ./internal/ledgermirror -run TestMirrorNoticeFailureDoesNotAdvanceSourceCursor -count=1`；注入通知写入失败后重订阅仍从原 watermark 重放。
- 已通过：`go test ./internal/ledgermirror -run 'TestMirror(DiscoversBareLocalOriginTaskWithoutRemoteTargets|InventoryBlackholeDoesNotBlockHealthyMachine|IncludesInventoryThatReturnsAfter450ms|InventoryErrorRetainsLastGoodUntilSuccessfulEmpty|DiscoversUnlinkedOriginSessionTasks|NoticeFailureDoesNotAdvanceSourceCursor)' -count=1`；包括自机裸任务、慢/黑洞来源、失败保持 last-good、明确空清单移除。
- 已通过：session/card wait 在消费通知前重读当前resident/card座；通知source seq/kind必须同时匹配wire和最新行。未知kind/不完整地址由共享 proto validator 拒收，不推进交付游标。
- 已通过：`go test ./cmd -run 'TestCardWait(SuppressesLegacyMirroredTicketWhenNoticeExists|ReceivesAddressedTaskTicketNoticeAndAcknowledgesIt|RejectsOldSeatAfterRebindBeforeNoticeRefresh)' -count=1`；当来源三元组已有通知记录时，旧 `task_mirrored` 问题/权限事件只保留账本审计，不再次作为可动作通知；无记录的旧来源仍兼容。
- 已通过：自动化 consumer 对同一来源三元组采用相同去重；已有 notice 后旧 `task_mirrored` ticket 不再生成第二轮 wake，仍无 notice 的历史镜像沿旧契约消费。并行实现者运行了 10 项定向测试和 `go build -mod=readonly ./...`。
- 已通过：`go test ./internal/ledgermirror -run 'TestMirror(DiscoversBareLocalOriginTaskWithoutRemoteTargets|InventoryBlackholeDoesNotBlockHealthyMachine|IncludesInventoryThatReturnsAfter450ms|InventoryErrorRetainsLastGoodUntilSuccessfulEmpty)' -count=1`；裸本机来源也覆盖了仅由 holder 推导自机名的部署形态。另全仓编译复核 `go test ./... -run '^$'`。
- 已通过：`go test ./internal/ledger ./internal/agentd ./internal/ledgermirror ./cmd ./internal/client ./internal/store ./internal/orchestration ./internal/proto -run 'Test(TaskTicketNotice|MirrorDiscoversUnlinkedOriginSessionTasks|SessionWaitReceivesOnlyCurrentAddressedTaskTicketNotice|CardWaitReceivesAddressedTaskTicketNoticeAndAcknowledgesIt|TaskOrigin|ValidateOriginSession|DispatchOriginSession|B444ResidentResumeHasNoSeatEnv)' -count=1`。不代表各包全量行为验收。
- 已通过：`go test ./... -run '^$'`（全仓编译）。
- 验证边界：根协调者已运行触及包测试与隔离 PG 多连接并发测试，并报告 web typecheck 与 TicketPanel 测试通过；本执行者未重复跑这些高成本检查。触及包运行中 `TestHostWakeResolvesLoginProxyOnHostMachine` 曾受宿主 `~/.handoff/env/opencode.env` 的 `NO_PROXY` 影响，需以根协调者隔离 HOME 的最终测试快照为准。
- 当前状态：通知重试入口已实现并有真实 HTTP client→agentd 测试；web 单按钮已接入，根协调者报告 typecheck 和面板测试通过。
- 未验：resident print/desktop 目标机消费确认需等待并行接线和相应定向测试。不能将 ledger mirror 事件本地存在当成目标机已送达。
- 未验：跨机断连/补发/换驻场 race、真实模型/permission 分支、CLI/UI 联合验收和人可见 OS 通知；P0 临时探针只证明 loopback protocol fixture 下真实 opencode 工具任务短回合/待答/重启答复，不证明这些项。
