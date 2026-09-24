# 会话定向消息续收：前置契约冻结 v1

> 基线 a8e60208；用户 2026-09-24 已批准 [r2](2026-09-24-session-reliable-mentions.md)。本契约先于 Wave 0 冻结 CLI 输出和共享账本接缝。生产者：`session send`、`session wait`、账本 Store；消费者：外部 agent 监听器、桌面发送器、其他机器上的同身份监听器。

## 冻结清单

1. 无 `--since` 的 `session wait <member>` 从共享账本中该 `member` 的 `session_delivery_cursors.last_seq` **排他**续读；无行等价于 0。它替代现有启动时 `MaxSeq` 起点（`cmd/session.go#runSessionWait`，基线 a8e60208）。
2. `--since N` 保持排他续读 `seq>N`，不读取持久游标。
3. `--since N` 模式不写持久游标。
4. 默认一次性模式：存在启动时积压则输出一行 backlog JSON 并退出 0；没有积压则等首条定向消息，输出既有 `SessionWake` JSON 后退出 0。
5. `--follow` 模式：先输出至启动水位的积压摘要（若有），随后每条新定向消息各输出一行既有 `SessionWake` JSON，直至主动退出或空闲超时。
6. 积压行的 `type` 固定为 `session_backlog`，`member` 为订阅参数原文，`from_seq` 为本次扫描的排他起点，`to_seq` 为摘要中最大的命中 seq，`hits` 为升序数组。
7. 每个 `hits[]` 元素有 `hit`，其字段沿用 `SessionCite` 的 `seq`、`room`、`actor`、`body`；`reply_to` 可解析时另有 `referenced`，字段同 `SessionCite`。无引用时省键。
8. 单条实时输出仍沿用既有 `SessionWake` 的 `session`、`hit`、可选 `referenced`、`unread`，不增加包装类型（`internal/proto/sessions.go#SessionWake`，基线 a8e60208）。
9. 共享游标键仅为外部 `member` 原文；与 UI 已读水位、`message_consumed` 和本地文件游标各自独立。
10. 游标写入必须单调：旧水位大于本次值时保持旧值；读写失败不得伪装为空游标或成功。
11. 定向唤醒或积压摘要写入 stdout 成功后，才推进该输出覆盖的最大命中 seq；stdout 失败不推进。游标写失败返回错误并记 Warn；已写出的消息允许下次重复。
12. 只通过 `collab.Service.MessageWakeTargets` 判定接收目标；`member` 与目标逐字相等，不根据会话成员列表广播（既有 `cmd/session.go#runSessionWait` / `internal/collab/room/delivery.go#ResolveDelivery`）。
13. `session send` 正文中以 `@` 开头、由空白隔开的完整 token，去掉一个 `@` 后若满足 `proto.ValidateMemberIdentity` 或卡号 `^[A-Z]{1,4}[0-9]+(\.[0-9]+)*$`，写入 `mentions`；无效 token 只保留正文。CLI 和桌面采用同一组金样本。
14. 自动提取的 mentions 与显式 `--mention` 合并并按首次出现顺序去重；显式参数的现有归一化语义继续有效（`internal/collab/service.go#normalizeMentions`，基线 a8e60208）。
15. 未填 mentions 且无有效 `reply_to` 的消息不唤醒；结构消息不唤醒；`@卡号` 仍按当前席位解算（既有 `internal/collab/room/delivery.go#ResolveDelivery`）。

## 协议金样本

`session_backlog` 唯一新增 stdout 线形状；`hits` 不截断，分页扫描直至启动时 `MaxSeq` 水位。此样本作为消费方断言，Wave 0 的 Go 测试按键和值回放：

```json
{"type":"session_backlog","member":"agent:main","from_seq":10,"to_seq":14,"hits":[{"hit":{"seq":12,"room":"session:1","actor":"user:sy","body":"@agent:main 看这里"}},{"hit":{"seq":14,"room":"session:1","actor":"user:sy","body":"回复"},"referenced":{"seq":11,"room":"session:1","actor":"agent:main","body":"原消息"}}]}
```

输入 token 金样本：`@agent:main`→`agent:main`；`@user:sy`→`user:sy`；`@B233.16`→`B233.16`；`@agent:`、`@B23x`、`@unknown`、`email@agent:main`→无自动寻址。大小写原样保留；token 不含尾随标点修剪。

## 存储和运行边界

- 新表 `session_delivery_cursors(member TEXT PRIMARY KEY, last_seq BIGINT/INTEGER NOT NULL, updated_at TIMESTAMPTZ/TEXT NOT NULL)` 随 `Store.Open` 的 PG/SQLite 幂等 schema 建表；Store 提供读取与单调推进两个公开操作。共享 PG 同一表，SQLite 单文件同一表。订阅进度是交付状态，不写入追加事件流。
- 在启动时读 `MaxSeq` 作为积压边界，对 `(cursor, MaxSeq]` 全流按升序分页扫描并用唯一寻址入口过滤；若有命中，输出一条摘要并写最高命中 seq。扫描期新增事件留给实时循环。无命中不输出；下次可重扫无命中尾段。
- 既有 stdout 语义为交付，不是模型处理完成回执；同一 member 同时只有一个主动监听器。并发多监听器的唯一认领、模型处理回执另立契约。

## 拍板记录

- **持久水位代替流尾起点**：改变外部 CLI 默认行为并影响多机监听器，难逆转；旧合同明确要求流尾，后人可能据此回退；用户选择停机补拉的交付级语义，否掉旧的“只等启动后事件”。
- **stdout 成功即交付、无逐条 ack**：外部模型若额度到期而未处理已写到 stdout 的行，系统无法识别；引入 ack 可提高处理级保证但增加每条操作。用户明确选择普通 `wait` 的方式，接受该边界。

## 图与移交

`d_cli→d_ledger` 既有 `ledger.Store` entry，`d_cli→d_collab` 既有 `collab.Service` entry；本次不增加方向、组装点或预算。契约仅定义对外语义和 Store 接口行为，不预铺实现骨架；Wave 0 负责实际竖切。`runSessionWait` 与 `sessionSendCmd` 当前图未覆盖，记作图覆盖债；本次无新代码符号，合法无视图 diff。
