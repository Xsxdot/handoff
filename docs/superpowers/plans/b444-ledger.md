# B444 台账

## P1（S5 push 驻场者续轮）——实现者 subagent，2026-10-19

### 偏离与自主决定

1. **转交收件端过滤口径**（plan §3「coordapi.go 只保留 `Card==id` 的卡唤醒」）：实际实现为「丢弃驻场唤醒（`WakeEvent.IsResident()`），其余卡唤醒照旧」。理由：转交批里 task_mirrored 等卡事件的 `WakeEvent.Card` 是**事件卡**（子卡），发送端经 `resolveWakeCard` 冒泡到父卡席位后才转交；按 `Card==id` 过滤会把冒泡的子卡事件全部丢掉（回归）。A1.4 的承重点（驻场唤醒不进卡回合）不变。授权：§6「驻场唤醒在 agentd 内的载体形态……只要满足 A1.1–A1.5」——属执行者可定；与 plan 字面不一致，故列此。
2. **A1.6 的 HTTP 投影落点**：`GET /api/sessions/{id}` 现有响应里没有 `proto.SessionResident`，驻场者只以 `SessionMember{kind:resident}` 出现。为让序列化链走真实入口，给 `proto.SessionMember` 加四键 `driver_mode/carrier/carrier_machine/model`（omitempty，只驻场者行填），`collab.membersOf` 从驻场行填写。HOME/workdir/push 会话 id 不投影到 Web。改动面超出 P1 文件集（`internal/collab/sessions.go` 成员投影、`proto.SessionMember`）；属 plan §8「成员与驻场投影新增 driver_mode 及 bearing 键（omitempty）」的版本化增量，不改既有键。
3. **`pointer_gate_test.go` 白名单加一条**（`internal/agentd/residentpush.go` `Server.pointResidentFailure`）：该守卫要求「新的正当引用在同一提交显式加进白名单并写明理由，不许放宽断言」；plan B3/A1.9 明定失败可见面走 `collab.Service.Pointer`。只新增白名单数据行，未改断言。对照 §6「删除或改写任何既有测试的断言」——未删改断言，但改了既有测试文件，提请审查确认。
4. **迁移只改基表**：`cmd` 包两条既有测试（`TestB435CardWaitOneShotExitsOnRoomHit`、`TestB435CardShowHintReadFailureSilent`）把 `session_residents` 换成同名视图，约定「Open 成功、读面在查询期报错」。新加列迁移对视图 ALTER 必败、会让 Open 整体失败。处置：`migrateResidentPushColumns` 先查对象类型（SQLite `sqlite_master.type`、PG `information_schema.tables.table_type`），非基表 Warn 跳过。未改那两条测试。授权：迁移内部实现，§6 未列；不涉及回填/改写存量行。
5. **驻场回合的小队取自驻场身份**：`agent:<名>` 的名字段即 `AdmitSeatCarrier` 的小队名（spec：push 驻场者身份记 `agent:<小队名>`）；未新增 squad 列。若身份名不是协调小队，准入报错 → 非准入失败路径（指针行 + 退避）。属 §6「驻场唤醒在 agentd 内的载体形态」。
6. **驻场回合注入席位 env：沿用不特判**（§6 明授执行者定）：`coordinatorRunner.Resume` 照旧注入 `HANDOFF_SESSION_CLI/ID`=驻场载体自己的 cli/会话对；驻场者按简报用 `--agent` 发言（`--agent` 不读环境键），该对不对应任何卡席位。
7. **驻场者续轮失败不重建**（含 `ErrSessionNotFound`）：重建要换 push 会话 id、重写驻场行，属编排层（P2 `resident/launch`）。keystone `WakeResident` 只透传错误，agentd 落指针行 + 退避。plan 未规定此分支；属 keystone 私有组织。
8. **push 行同时带桌面端两列也拒**（A1.7 之外加严一条）：push 行带 `cli_session`/`desktop_session` 会让门铃与 keystone 双路唤醒（R2）。同一哨兵 `ErrResidentIdentity`。
9. **新文件拆分**：agentd 驻场批与回合放 `internal/agentd/residentpush.go`（plan 写「scheddrain.go 新增函数」）；keystone 驻场方法放 `internal/keystone/resident.go`。§6「私有函数与文件拆分」。
10. **`proto.SessionUntitledTitle = "新会话"`** 提前在 P1 落 proto（简报 CAS 改名指令要用，A1.8）；P2 自动命名复用同一常量。

### 事实与命令

- 基线：`cards/B444-charter-1` @ `7500df76`。
- 载体选择（§6 第 1 条）：扩 `keystone.WakeEvent` 加 `Session`/`Resident` 两字段 + `IsResident()`；消费循环把驻场唤醒分进 `pendingByResident`，在门铃之后、卡批之前处理（卡批 attach 暂缓会提前 return）。
- 认领键 `resident:<会话>`；退避复用 `recordWakeBackoff/shouldSkipByBackoff`（以认领键为 key）；准入满复用 `admissionStalled` 判据，但**不**走 `recordAdmissionStall`（那条会落卡级 needs_human）。
- 指针行去重：`residentPushState.pointed[会话]=批次最大 seq`，成功即清。
- 先红后绿记录：
  - ledger：`TestB444*` 编译红 → 加结构字段后断言红（读回不符）→ 改 SELECT/INSERT 后红（`no such column: driver_mode`，迁移缺失）→ 加 DDL + 迁移绿。
  - collab：`TestB444SitResidentPushShape` 10 个拒绝子例断言红 → `validateResidentDriver` 绿。
  - keystone：编译红 → 空壳断言红（4 测）→ 实现绿。
  - agentd：编译红（类型错）→ 断言红 7 测（A1.1/A1.2 对照/A1.4 push_resident/A1.5 对照/A1.6 model 键/A1.9×2）→ 实现绿。
- 跑过的命令与结果：
  - `go test ./internal/ledger/... ./internal/collab/... ./internal/keystone/... ./internal/agentd/... ./internal/proto/...` → 7 包全 ok（agentd 88s）。
  - `go test -count=1 -v -run TestB444 ./internal/{ledger,collab,keystone,agentd}/` → 17 PASS、1 SKIP（PG）。
  - `go build ./...` ok；`go vet ./internal/...` ok；`gofmt -l` 触及目录为空。
  - `go test ./cmd/...`：首跑两条视图用例红（见偏离 4）→ 修后仅 `TestSessionSendActorResolution` 红；去掉 `CLAUDE_CODE_SESSION_ID/HANDOFF_SESSION_*` 单跑 ok（已知环境敏感，memory 有记）。
- **未验证**：PG 腿 `TestB444ResidentPushMigrationPG`——本环境无 `LEDGER_TEST_PG_DSN`，按指令不找凭据，SKIP。真机 R1–R3 归协调者。
