# B469 S3 协调者 PG 取证

## 偏离与自主决定

- 新增独立 `internal/ledger/b469_ticket_notice_pg_test.go`，只测通知组件的共享存储性质。每次创建独立 schema，两条真实 PG 连接，清理仅 DROP 本测试 schema；DSN 来自本批专用测试库，不写仓库或日志。
- 首次测试夹具误用 `pgx.ParseConfig` 修改后的 `ConnString()`（该方法返回原始连接串），导致写在本批专用库 public，而非新 schema；没有触及生产库。已改用 URL query 明确 search_path，并在写入前断言 `current_schema()`。不能将初跑的总事件数当成隔离计数证据。
- 旧 source_seq 的 API 可以显式拒绝，测试随后读取持久记录确认没有退回旧地址；没有强迫实现采用“静默 no-op”这一未冻结行为。

## 实际发现与修复

原始 `/tmp/b469-p0-runtime/s3-notice-pg.log` 暴露：相同地址连续 12 次 Put 仍把 ownership_version 从 1 增到 12，每次又生成通知。PG JSONB 回读会格式化空白/键序，旧实现按 JSON 字符串相等比较。执行者改为解析后比较 DeliveryAddress 结构。

修复后在**真正独立 schema** 复跑 `/tmp/b469-p0-runtime/s3-notice-pg-final.log`，两条测试通过：

- 两 Store 的 12 个并发同票重放只生成一个通知；已送达重放保持 delivered。
- 换收件人递增版本；旧版本确认失败；旧源序号无法夺回目标。
- answered 后即使新源重放也不复活，重新打开 ledger 保持 gate/版本/终态。
- 无 origin 的无卡原票保留 needs_human，不发布无目标事件、不伪造 card_tasks；获得明确收件人后从同票进入 pending。

后补显式重试：两条独立连接并发重试同一失败通知，只一次 CAS 成功并只新增一个寻址事件；answered 后禁止再次重试复活。`s3-notice-pg-retry.log` 两条 PG 测试通过。

## 独立变异

协调者用 Go overlay 将结构比较改回 `oldTargetRaw.String == newTarget`，不修改共享工作树。真实 PG 测试编译成功并在“同票只生成一个通知”断言转红，原始 `s3-notice-pg-mutated.log`。撤掉 overlay 后同测试通过，`s3-notice-pg-restored.log`。这证明测试能抓住 PG 与 SQLite 表面相同、行为不同的重复通知问题。

## 边界

这是共享 PG 存储、并发和序列化证据；两连接不等于两台执行机器，不证明自动唤醒、桌面深链或原执行者收到回复。S3 的消费者、原 CLI 和跨机实测另行登记，不能凭本台账关闭故事。
