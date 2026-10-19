# 账本 schema 版本闸：PG Open 版本一致时跳过建库

> 状态：待批准（2026-10-19 起草；r2 并入委托会话五条意见）
> 级别：**L1**（单子系统 internal/ledger，不动契约；实现决定见末尾三行，验收一眼可核：pg_locks / 持锁下 Open 成功）
> 来源：2026-10-19 agentd 打开账本挂死事故的续篇（fix/ledger-open-retry 已合入 main a429b193）
> 台账：[ledger-schema-version-ledger.md](ledger-schema-version-ledger.md)

## 问题陈述

现状读数（待执行节点复核）：`internal/ledger/store.go:704` `ensureSchema` 在**每次** Open 都把全部 DDL 与迁移跑一遍，
CLI 每次调用都 Open 一次。其中 `ALTER TABLE cards ADD COLUMN …`（store.go:734、745，以及
`migrateDriverMode` store.go:665）列已存在时照样先申请 cards 上的 AccessExclusiveLock 再报重复列；
`migrateRetireB434Tables` 取 `pg_advisory_xact_lock(915601)`（b434_retire_migrate.go:183）。
共享 Supabase PG 上：任何会话持有 cards 的任意锁（哪怕一条长读）时，ALTER 排队，排队期间它挡住其后
**全部** cards 读（锁护航），全队一起卡。a429b193 给建库阶段加了 lock_timeout=10s，把「无限挂」降成
「10s 后失败重试」，但每次 CLI 调用仍要排一次排他锁。

## 方案

PG 账本在 `ledger_meta` 存一个整数 schema 版本标记；代码持有一个整数常量「当前 schema 版本」。
Open（PG）在建库专用短命池上（沿用现有 lock_timeout/statement_timeout）先读标记：

- **标记 ≥ 代码版本** → 跳过全部 DDL 与一次性迁移，只做下文「闸外」一项。
- **标记缺失 / ledger_meta 表不存在（42P01，新库或极老库）/ 标记 < 代码版本 / 标记不可解析（Warn）** → 照旧跑完整
  ensureSchema；42P01 视为「标记缺失」，不是 Open 失败。闸内**全部**成功后才写标记：单条只升 upsert
  （`INSERT … ON CONFLICT (key) DO UPDATE … WHERE ledger_meta.value::int < EXCLUDED.value::int`），不取 advisory 915601。
  失败不写标记，下次 Open 重跑。
- 两个首次 Open 并发时都会跑完整 DDL——与现状相同、幂等，可接受。

**闸内**（版本一致即跳过）：wake_claims 复合键迁移、DDL 列表、B434 退役表迁移、cards 三条加列、driver_mode 回填。
**闸外**（每次 Open 照跑）：未决工单投影水位检查 `ensureOpenTicketProjection`——它是数据水位不是 schema，
自带 version 字段，只在投影陈旧时走 mutate 重建。稳态路径已核（ticketprojection.go:190-209 现状读数）：仅两条 SELECT
（open_ticket_projection_state、card_events），只取 AccessShare，无更强锁。

**SQLite 不加闸**，行为与现在完全一致（单机文件、无护航问题）。

弃选：
- *DDL 指纹（哈希）当标记*：无序，新旧二进制混跑时互相判「不一致」各自重跑并互相覆盖标记，混跑期间护航照旧。整数单调更稳。
- *改成 `ADD COLUMN IF NOT EXISTS`*：PG 上它仍先拿 AccessExclusiveLock 再判存在，解决不了护航。
- *两方言都加闸*：委托方明确要求 SQLite 路径不变，且 SQLite 无此问题。

**防漏升版本**：加一条单测，把两方言 DDL 列表的指纹钉在当前版本号上；改了 DDL 不升版本号即红，失败文案提示「升版本号并更新钉住的指纹」。
新增 Go 侧迁移（不在 DDL 列表里的）由 ensureSchema 头注释写明「必须同时升版本号」，测试管不到，靠注释与审查。

## 落位三问

① 读写谁的权威数据：账本自身的 `ledger_meta`（internal/ledger 领域内），不跨领域（架构法第一条）。
② 谁消费：只被同包 Open 消费，无导出符号、无契约面（第四条不触发）。
③ 同步还是异步：同步——Open 返回前必须确定 schema 可用（第十二条：调用方需要结果才能继续）。

## 必交用户故事

- **S1（护航消除）**：PG 账本已是当前版本时，任一调用方（CLI / agentd）`Open/OpenShared` 不申请 cards 上的
  AccessExclusiveLock，也不取 advisory 915601。验证：隔离 PG 测试库上，另一会话持 `LOCK TABLE cards IN ACCESS SHARE MODE`
  时第二次 Open **成功且耗时 < 1s**（证明没有 ALTER 在排队）；并在 Open 期间观测 pg_locks 无本连接的 cards AccessExclusiveLock。
- **S2（升级照跑）**：标记缺失或低于代码版本的 PG 库，Open 跑完整建库迁移并写入标记；cards 被他人持锁时仍在
  lock_timeout 内失败且判瞬时（a429b193 的有界性保留）。验证：清标记后复现现有 `TestPGOpenBoundedWhenCardsLocked` 形状。
- **S2b（无 ledger_meta 的库）**：ledger_meta 表不存在的 PG 库（新 schema）Open 成功，跑完整建库并写入标记。
- **S3（SQLite 不变）**：SQLite 路径 Open 行为不变，ledger 包全部 SQLite 测试绿。
- **S4（防漏升）**：改 DDL 列表不升版本号 → 单测红。

验证环境：linux-01 上按 credentials.md 约定新建的隔离测试库 + 专属账号；绝不碰在役 agentd 与共享账本。

## 实现决定

- 标记落 `ledger_meta`，键名 `schema_version`，值为十进制整数文本；本次版本号起点由实现定（建议 1）。
- 读标记与写标记都在建库专用池上，受同一 lock_timeout 约束；读标记只对 ledger_meta 取 AccessShare。
- 闸内/闸外划分如上，不得把投影水位检查放进闸内，不得把任何 ALTER/advisory 留在闸外。
- 日志：跳过时 Info 一行（含库内/代码版本）；执行时 Info「from→to」；标记不可解析 Warn。
- 现有依赖「同一 PG 库上手工改坏 schema 再 Open 自愈」的 PG 测试夹具，改为同时清标记以模拟真实旧库（真实的老库本来就没有标记）。

## 测试决定

- 真实入口：`ledger.Open` / `OpenShared`（生产消费者：CLI 各命令与 agentd 启动）。
- PG 新测（LEDGER_TEST_PG_DSN 门控）：S1 持 ACCESS SHARE 下二次 Open 成功且 < 1s + pg_locks 观测；S2b 空 schema 首次 Open；S2 清标记后持锁 → 有界瞬时失败、放锁后 Open 写标记。
- 单测：S4 指纹钉版本。
- 回归：`go test ./internal/ledger/...`（SQLite）+ 设 DSN 跑 PG 测试全集。

## Out of Scope

- **永不做**：schema 降级/回滚迁移（迁移一律加法式，高版本库对低版本二进制是超集）。
- **永不做**：PG 上手工改坏 schema 但标记仍为当前版本时的自愈（标记即事实来源；需要时手工删标记）。
- **本期不做、后续要做**：Go 侧迁移也纳入自动防漏升检测（目前只钉 DDL 列表）→ 落 roadmap。
- **本期不做**：在役 agentd / CLI 升级与部署（另走统一升级清单）。

## 备注

- 混跑期：本改动前的旧二进制仍每次跑 DDL（行为不变），护航要等全队升级后才彻底消失。
- 升版本当次 Open 仍会排一次排他锁，受 10s lock_timeout 约束、失败重试，属可接受的一次性成本。

---
实现三行（L1，spec 即 plan）：
1. `internal/ledger/store.go`：ensureSchema 前加 PG 版本闸（读/比/写 `ledger_meta.schema_version`），投影检查移到闸外；SQLite 不进闸。
2. `internal/ledger/*_pg_test.go`：新增 S1/S2 PG 测试，改写 `TestPGOpenBoundedWhenCardsLocked` 为清标记形状；依赖自愈的 PG 夹具补清标记。
3. 验收：linux-01 隔离库跑 PG 全集 + 本地 `go test ./internal/ledger/...` 绿 + S4 指纹单测。
