# B456 实施计划 r1

状态：已拍板（2026-10-23，主会话）。基线：已批准的 `docs/superpowers/specs/b456-spec.md` r4、卡 B456 今日 note #33097/#33103、`~/b456-proto-r4/README.md`。范围为 S1–S5 与 r4 最近动态；不做 B471 完整逐轮运行内容。单卡不拆子卡；主会话负责协调、契约审查和最终验收，具体实现由本地 gpt-6-luna 两个 agent 按文件所有权协作。HEAD 比 `origin/main` 领先 3 个提交、无落后，不需合并。

## 拍板记录

独立 gpt-6-luna 出稿，主会话逐项审核后批准：采用已批准 spec r4；不设 TTL 自动异常；明确运行/结束仅为最近观测事实；复用任务透明路由与 TicketsPanel 完成就地票据处理；接续走既有 @card 消息，不调用内部 wake 转交接口。两位本地 gpt-6-luna 分别拥有后端与 Web，不拆子卡，不作远端派发。无待用户裁决事项。

补充冻结澄清：后端 `ticket_refs` 是账本未决票据的候选引用；`needs_you` 的可操作展示及 CTA 数量以 Web 成功取到完整、仍 pending 的 task ticket 为准，未取到时保持等待/未知并说明失败。`recent.source` 取现有事件类型（wake_round、room_message、task_mirrored、dispatched、needs_human、needs_cleared、driver_seat_bound、driver_takeover、status_moved），仅供标注来源而非新状态机。API 旧服务缺 role_status 时显示“不支持角色状态”，不得凭旧 member working 推算绿色。归档会话可查历史，所有发送/票据写入入口禁用。

## 主会话确认的施工边界

- 后端 agent 只拥有 `internal/{ledger,collab,proto,agentd}`，以及仅为单条任务快照只读查询涉及的 `internal/{orchestration,store}` 与对应 Go fixture/test；前端 agent 只拥有 `web/`。两者依据下文冻结 wire 协作。任何跨缝语义/字段变更须先回主会话确认并更新冻结契约；不互相覆盖文件。
- 不把 DriverLease、席位绑定、卡列或 `task_mirrored` 历史本身当作进程正在运行；不加 TTL 自动判异常。明确 start 至 end/fail 是“最近观测到的回合运行”，沉默只说明未观察到后继，详情展示最后事实时间，不把沉默或经过时长变成在线心跳。
- S3 必须在抽屉内完成真实批准/拒绝/回答并成功后打开下一张，复用 `TicketsPanel` 与 `replyTicket`。任务详情全文经 `fetchTaskDetail` 获取，禁止拿截断的 `permission_request` 事件摘要当票据正文。Task GET/reply 已由 `internal/agentd/taskroute.go` 按全局唯一 task UUID 经 `byTask` 镜像路由；镜像索引缺失/远端不可用时显示真实读取或答复失败，不把它算入可处理 CTA。仅当前登录用户可处理且已成功读取完整 pending ticket 的项目计入“需要你处理 N”。
- “接续请求”复用 `sendRoomMessage`，以 `mentions:[cardID]` @ 当前卡席位，沿用现有鉴权、寻址和唤醒队列；不调用 coordinator wake 的转交 API。push 模式按钮/结果只说明“请求已发送/受理”，不保证恢复或执行；pull 模式只说明已发送消息、可查看卡，不声称自动唤醒。

## 目标与现有来源

按 r4 原型实现 composer 上方角色条、六类固定状态、需要你处理入口、角色详情、异常真实下一步、溢出与 650px 两行布局，并增加最多 5 条现存事实组成的最近动态。没有来源的内容明确未记录；不新增运行内容持久化，不展示完整逐轮输入输出或工具记录。

会话 GET 当前投影 `summary/nodes/timeline`（`internal/agentd/sessionsapi.go:274-285`、`internal/proto/sessions.go:164-210`、`web/src/api/rooms.ts:161-220,300-307`）。会话投影 `internal/ledger/rooms.go:122-205` 当前不含评论。`wake_round` 的 start/end/fail 已以卡 comment 落账，字段契约在 `internal/agentd/wakeconsumer.go:408-468`、producer 在 `internal/agentd/scheddrain.go:715-755`；房间消息有独立 API `internal/agentd/ledgerapi.go:57-63` / `web/src/api/rooms.ts:248-275`。卡级派发是 `dispatched` (`internal/ledger/types.go:51,167-203`)；任务真实事件类型在 `internal/proto/proto.go:42-119`，包括 `progress`、`permission_request`、`question`、`completed`、`failed`、`turn_failed`、`ticket_answered`、`archived`。continue/resume 是 API 动作，不是镜像事件名；`permission_answered` 不是生产事件名，不得出现在新 fixture。`completed`/`turn_failed` 表示执行回合完成，不等同整卡归档；`archived` 是独立终态。LatestTaskStates 仅表示每个挂账 task 的最高 `source_seq` 最新镜像类型 (`internal/ledger/taskstate.go:13-17,140-170`)，不能证明实时存活。票据核销语义复用 ledger `ticketprojection.go`，collab 不自行重放票据状态机。

## 冻结的兼容 wire 形状与语义

在 `SessionDetail` 上增量增加可选 `role_status`；旧服务缺字段时前端降级到当前页面能力。新服务成功查询时 `role_status.roles` 始终为数组（无角色为 `[]`）；缺字段表示旧服务/不支持，`null` 不作为第三种语义。冻结形状如下：

```json
{
  "role_status": {
    "observed_at": "本次投影完成时间，RFC3339 UTC",
    "roles": [{
      "id": "当前职责关联的稳定标识",
      "kind": "coordinator|resident|executor",
      "identity": "可选展示身份",
      "card_id": "可选卡ID",
      "task_id": "可选任务UUID",
      "target": "可选执行目标",
      "state": "running|idle|needs_you|waiting|exception|unknown",
      "since": "可选状态起点源时间，RFC3339 UTC",
      "conclusion": "面向用户的结论短句",
      "reason": "可选、已有事实的短原因",
      "waiting_for": "可选、已知等待对象",
      "recent": [{"seq": 0, "time": "RFC3339 UTC", "summary": "短摘要", "tone": "neutral|needs_you|failed", "source": "既有来源类型"}],
      "more_activity": false,
      "ticket_refs": [{"card_id": "卡ID", "target": "任务目标", "task_id": "全局唯一任务UUID", "ticket_id": "票据ID", "task_type": "permission_request|question"}],
      "can_request_wake": false
    }]
  }
}
```

`observed_at` 是查询完成时间，只能标注数据更新时间；所有事实时间来自源事件。源时间缺失、零值或未来值不算时长起点，显示“时间未记录”。`roles` 按当前会话成员/卡席位/关联 task 生成，不能将执行者伪装成会话成员；同身份多职责分开。状态优先级、展示名和布局沿 r4。`running` 只表示明确开始或运行记录尚未观察到结束（普通 progress 仅为活动摘要，不单独证明运行）；无 TTL、不宣称实时心跳，详情的 `conclusion` 说明最近事实及“尚未观察到结束”，时长不因源沉默而刷新。`idle` 只由明确结束/归档或来源明确空闲产生。`waiting` 包含明确未决等待以及 `completed` 后无更晚 dispatch/continue 事实而卡未终态的待接续（仅事实，不判故障）；新派发/新轮次消退，卡终态解除。`exception` 只用当前未恢复的明确失败；旧失败不盖较新成功。`needs_you` 只能由现有票据投影确定并在 task detail 成功读到当前用户可处理的 pending 票据后设置；`ticket_refs` 仅标识，不带任何截断正文。无法按身份/卡/task/序号区分、查询失败或证据缺失时 `unknown`/明确未更新，不能静默当无事实。

`recent.seq` 只在同一来源/作用域内比较，不跨 task `source_seq`、card `seq` 或消息游标推断全局先后；前端按源时间倒序，同时间稳定排序。`source` 只能是冻结来源类型；`summary`/`reason` 仅允许 wake 既有用户可见失败摘要、房间消息摘要或用户可见工单/任务短事件，不回传原始账本 payload、密钥、模型推理或完整执行输出。票据正文只由 task detail API 获取并交给既有 `TicketsPanel`。`can_request_wake` 仅表示可提交 `@card` 接续请求的能力/席位类型，不代表唤醒已发生；动作结果仍由发送 API 响应决定。

冻结澄清 r1.1（主会话，2026-10-23）：`progress` 有配额、对账、看门狗等非执行 producer（`internal/orchestration/{manager,reconcile,quota,watchdog}.go`），不得将任意 progress 映射为 running 或覆盖明确结束/未答状态。同任务接续需核真实当前任务状态或可辨认的权威新轮事实；无法辨认时降级未知并保留证据，不以文字猜测。该澄清保持原六态与查询 wire 形状。

冻结澄清 r1.2（主会话，2026-10-23）：同任务 continue 会更新既有任务 Task.State，但事件只有共用 progress，无法仅从它证明新运行。批准 agentd 组装点通过现有 manager facade GetTask（本机）、新增 GetMirrorTask(taskID) 窄读（远端既存 mirror_tasks 表）取得控制面状态，以 collab 公开投影方法输入当前 role task 引用的快照。不新增持久化、不全量 ListTasks/ListMirrorTasks、不现场远端扇出、不从 collab 穿透执行实现。远端说明最后镜像到达时间，控制面 running 也不承诺进程心跳；Task.UpdatedAt 非专用状态起点，不将其直接当 since。取不到或关联/目标冲突则未知；旧回合 completed 被真实继续后的 running 快照替代。文件所有权扩至此窄读需要的 orchestration/store。JSON 形状不变。

冻结澄清 r1.3（主会话，2026-10-23）：远端镜像快照并非实时权威，不能覆盖同任务较新的明确生命周期事件。可将既有 Task.UpdatedAt 传入内部 observation（不新增 HTTP 字段）与同源 task_mirrored.CreatedAt 比较冲突的新旧；FetchedAt 属本机获取时间，不与远端事件墙钟直接等价。UpdatedAt 仅用于冲突次序，不充当状态持续时长或心跳。不能可靠排序时降级未知/保留较新已观测事实，不复活旧 running；较新的真实继续快照可替代旧 completed。该条收紧已有“旧事实不盖新事实”的承诺，不改 r4 结果和冻结 JSON。

冻结补充 r1.4（主会话，2026-10-23，审核 #33154/#33162、用户批准 #33171）：本轮修复 I1–I5，禁止合并。I5 的 push 驻场者与桌面驻场者分开：桌面门铃没有运行观测，继续 unknown；push 首轮与续轮补最小回合事实。原始来源的成功/失败都 CompleteWake，且首轮无 claim，无法仅由原数据区分结果；用户明确批准“补齐最小回合事实（推荐）”，覆盖此前不新增持久化的限制，仅限此项。复用现有会话事件流的 `resident_round`，含唯一 round_id、当前驻场 identity、phase=start/end/fail、源时间与可见短原因，不建表、扫描器、第二套运行状态或完整输入输出记录。实际调用前 start、同轮返回后 end/fail；同 round_id 配对，较新回合取代旧失败，显式 end 才说明空闲，fail 说明异常。显式 start 未观察到结算可说明最近观测运行，结论必须写“尚未观察到结束”，不承诺心跳；claim/租约本身不推出 running，不设 TTL 自动异常。有明确重启中断证据时可记录同轮中断；沉默不猜故障。采集失败不得伪造新状态，沿现有日志记录失败与关联信息。查询按当前 session 与当前驻场关联有界读取，沿 ledger facade/client，禁止穿透 Store。SessionDetail JSON 不变，`recent.source` 仅补 `resident_round`。HTTP 验证真实 producer start→running、end→idle、fail→exception、旧失败→新成功恢复、桌面 unknown；I1 额外锁住真实答复落库后 delivery_failed 未恢复时不能被普通 running 快照覆盖。

## 工作单元

### W1：有界事实投影（后端所有权）

沿 `LedgerClient`/ledger facade 增加按当前 session 的 card IDs/task refs 限定的只读 query：复用 `open_ticket_projection` 窄读和 LatestTaskStates；wake comments 按当前卡、最近有限窗口读取；room activity 取既有有界消息读面。不得从 collab 穿透 Store、全库扫描、前端逐卡 N+1 或复制票据状态机。投影 current card/seat/task，先以卡 `dispatched` 与 task `(target,task,source_seq)` 配对，结合卡终态，正确覆盖 E38 complete→无后续 dispatch→待接续，以及后续 dispatch/同 task 新事件/终态消退。无足够证据就 unknown。评论解析只接受生产结构 `wake_round` phase=start/end/fail；测试不得伪造 `continue`/`resume`/`permission_answered` 类型。

### W2：DTO、HTTP 装配与动作闭环（后端/前端各自文件所有权）

后端 `internal/proto` 固定 wire、`internal/collab` 组装、`internal/agentd` 通过现有 GET sessions 身份/授权返回，扩展有界查询。前端 `web/src/api/rooms.ts` 镜像 DTO。S3 抽屉复用 `TicketsPanel`；按各 role 的 `ticket_refs` 顺序对 task ID 调 `fetchTaskDetail`，以完整 `pending_tickets` 建真实票据视图，并以 `replyTicket` 原样执行已有 allow/deny+reason/answer。每次动作成功后刷新并选下一张；失败保留当前票及错误。仅当前会话用户有权限且详情成功读取的 pending 票计 CTA。镜像缺失/权限失败/远端失败显式显示不可处理原因，不按同 ID 本地票据回退。不得在新 DTO 重复传票据全文。

接续 CTA 调 `sendRoomMessage(sessionID, body, {mentions:[cardID]})`；发送成功后只展示“接续请求已发送”。按当前 `seat_mode` 给 push/pull 解释；失败保留错误，不推断唤醒成功。异常详情的查看卡/现存证据链接复用既有导航。禁止新增审批、恢复、重派或 coordinator wake 代理 API。

### W3：角色栏、详情与 r4 溢出（前端所有权）

入口 `web/src/app/rooms/SessionTab.tsx` 现有 detail/history 轮询；在 composer 上方放 r4 角色栏，扩展 `sessionModel.ts` 与 `SessionDetail.tsx`。最近动态默认展开，最多 5 项倒序；真实可链接事件点到卡/任务/票据。技术标识默认折叠。严格按原型处理首部 CTA、安静 chip、异常永不进 +N、层级收紧和 ≤650px 两行布局；不改 `prototypes/base`。S3 drawer 需键盘操作、失败可恢复、成功跳下一张。

### W4：契约/集成与验证

Go/TS fixture 锁定同一 JSON 形状与旧字段缺失兼容；Go 测试穿过 facade→collab→agentd 序列化，Web 测试验证真实 API client 调用和动作闭环，不仅测 helper。重点覆盖：E38 complete/无后续 dispatch/卡非终态；更晚 dispatch、同 task continue 实际产出的新事件、归档/卡终态；needs 最新未清/已答/自动审批/责任未知；镜像 task detail 正常与缺索引/拒绝/错误；approve/reject/answer 成功跳下一张、失败保留；push/pull `@card` 请求文案准确；start/end/fail 原生产 comment；缺时间、未来时间、轮询错误即时未更新；390px、桌面及拥挤异常布局。主会话运行真实浏览器及可用 agentd/账本验收，对照 r4；mock 不作为接通证据。

日志仅在查询失败、降级与动作错误时记录关联 session/card/task、事件数量与错误类别，不记录消息正文、票据全文、执行 payload 或密钥；不为每次轮询刷成功日志。

## 故事映射

| 故事 | 结果与验收 |
|---|---|
| S1 角色栏 | 会话 GET→SessionTab 显示 r4 六态；空座、多卡/多角色和无观测不假在线 |
| S2 E38 待接续 | latest task + 卡派发/终态→事实性等待；后继派发消退，不判故障 |
| S3 等待/待处理 | 仅可处理完整 pending ticket 计数；抽屉真实批准/拒绝/回答，成功下一张、失败保留 |
| S4 异常/未知 | 明确未恢复失败与读取错误可区分；旧失败不盖新成功，沉默不变红 |
| S5 布局/操作 | 原型桌面/390px/650px 溢出，真实卡/task/票据入口、键盘及归档只读 |
| 最近动态 | wake_round、room message、ticket/task/card 既有事实最多 5 条；无运行正文不编造 |

## 权限、接缝与停机条件

唯一冻结共享接缝为上述 SessionDetail JSON。实现 agent 可调整私有类型、投影算法与 CSS，但不得改字段语义、事件命名、r4 状态词表/顺序/布局/动作承诺。后端公开 query 必须按 card/task 范围受限并沿 ledger facade；前端不得直接探机器或扫描账本。任何需要新增公开端点、扩大为全库读、改变身份/权限/授权、添加远端 ticket 直连或修改冻结 JSON 的情况，停下回主会话裁决。B471 完整运行内容、模型推理和逐轮工具记录不属于本卡。
