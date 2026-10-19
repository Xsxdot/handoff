# 台账：账本 schema 版本闸

- 2026-10-19 建 worktree `.claude/worktrees/ledger-schema-version`，分支 fix/ledger-schema-version 自 main a429b193（fix/ledger-open-retry 已合入）。
- 现状读数：store.go:704 ensureSchema 每次 Open 全跑；ALTER cards ×3（store.go:734/745、migrateDriverMode:665）；b434 迁移取 advisory 915601（b434_retire_migrate.go:183）；ensureOpenTicketProjection 稳态只读、自带 version（ticketprojection.go:165）。
- 判断：投影水位检查留闸外（数据水位非 schema）；SQLite 不加闸（委托方要求路径不变）；整数单调标记优于 DDL 指纹（混跑不互相覆盖）。
- 预见影响：TestPGOpenBoundedWhenCardsLocked（open_bounded_pg_test.go:16）在新语义下二次 Open 会成功，需改为清标记形状；taskstate_projection_test.go:357 DROP 投影表后重开，若走 PG 需清标记。
- spec 起草完成，待批准。
- r2：并入委托会话意见——42P01 视为标记缺失（S2b+测试）、只升 upsert 不取 advisory 且闸内全成功后写、并发首 Open 双跑可接受、S1 加 <1s 时限、核投影稳态仅两条 SELECT（ticketprojection.go:190-209）。
- 2026-10-19 用户批准 r2，回写头部状态；进 implement。

## implement（2026-10-19）

### 偏离与自主决定
1. 偏离：无（闸内/闸外划分、只升 upsert、42P01=缺失、SQLite 不过闸均按 spec r2）。
2. 自主决定：版本号起点 1（spec 授权由实现定）；防漏升指纹 = sha256(PG DDL \x00-join ‖ 0xff ‖ SQLite DDL \x00-join)，钉在 `pinnedDDLFingerprints`；新代码放新文件 schema_version.go，ensureSchema 拆为「闸 + applySchema」——均在 L1 实现三行范围内。
3. 自主决定：b435 退役迁移 PG 夹具 `createLegacyTables` 补删标记（spec 实现决定末条授权：依赖自愈的夹具改为清标记模拟真实旧库）。

### 过程
- 测试库：linux-01 新建 handoff_schema_ver（owner handoff_b435，已登记 credentials.md 已分配表）。
- 先红：S4 编译红 → 空壳后断言红（指纹 d84335…c011 与钉住的 "" 不符）；S2b 断言红「空 schema 首次 Open 后标记=-1，期望 1」。
- 绿：handoff_schema_ver 上 TestPGOpenBoundedWhenCardsLocked PASS 11.29s（清标记后持锁 → 有界瞬时失败、不写标记，放锁后补写标记）；TestPGOpenSkipsSchemaWhenVersionCurrent PASS「版本一致时持锁下 Open 耗时 69.999125ms」，pg_locks 无他人 cards AccessExclusiveLock；TestPGOpenFreshSchemaWritesMarker PASS。
- 首跑全集撞库名守卫：多数 PG 测试只认 handoff_b409_test，改用该库（credentials.md 登记为可 DROP 测试库）。
- 首跑 b435 退役 PG 四支红（夹具 Open 后写了标记再建旧表 → 闸跳过迁移），夹具补删标记后绿。
- 对照：main a429b193 基线在 handoff_b409_test 上 PG 全集 → 仅 TestB439DevicesPG（attrs `{"k": "v"}`）、TestPGSmokeEndToEnd（前缀 P 被 "p" 占用）两红；本分支同库全集 → 同样且仅这两红。均落 roadmap。
- SQLite：`go test ./internal/ledger/... -count=1` → ok ledger 6.556s、ok api 0.907s；`go build ./...`、`go vet ./internal/ledger/` 通过。
- 提交：`git commit` → b54e2827 fix(ledger): PG 账本 schema 版本闸（随后 amend 收台账，hash 会变）。

## review（2026-10-19，独立 subagent，只读）

- 裁决：**通过**（单卡最终，L1）。无 Critical/Important。复跑：SQLite ledger 包绿；schema_ver 库定向 PG 八支 PASS（S1 50.45ms）；handoff_b409_test 全集仅基线两红（TestB439DevicesPG、TestPGSmokeEndToEnd）。
- 变异：schema_version.go `stored >= ledgerSchemaVersion` → `<`（唯一命中、可编译）→ S1 红「迁移 cards.driver_carrier: … lock timeout (SQLSTATE 55P03)（耗时 10.28s）」；还原后绿 48.26ms。
- Minor 1（补披露，归 implement「偏离与自主决定」第 4 条）：写标记 upsert 加了 `CASE WHEN value ~ '^[0-9]{1,9}$'` 守卫（schema_version.go:61-65），spec 示意 SQL 只有 `::int <` 比较；不加守卫时不可解析标记会让 cast 报错、永不被覆盖，违背 spec「不可解析按过期重跑」——属实现 spec 所必需，在授权内。
- Minor 2（记账不改）：S1 的 pg_locks 旁证在 Open 返回、建库池关闭后才查，证明力弱；承重证据是 <1s 计时（变异已证其有牙）。

## finish（2026-10-19）

- 静态门禁：`gofmt -l $(git ls-files '*.go')` → 空；`go vet ./internal/ledger/` 通过。
- 全量 `go test ./... -count=1`：64 包 ok，唯一红 cmd TestSessionSendActorResolution；`env -u CLAUDE_CODE_SESSION_ID go test ./cmd/ -count=1 -run TestSessionSendActorResolution` → ok 0.382s（环境敏感既有红，roadmap「来自 B436 finish」已记）。
- 集成方式归人：委托会话转达用户——不合入 main、不推送，合并由用户决定。分支停在 fix/ledger-schema-version 待裁。
- 残余：已落 roadmap（Go 侧迁移防漏升、PG 两条既有红）。在役 agentd/CLI 升级不在本卡。
