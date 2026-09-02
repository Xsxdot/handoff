# B298 拆解稿：终态缓存收口删除 + `handoff gc` 批量预览/执行

状态：已拍板（2026-08-30，协调者无人值守拍板；岔口 1–3 全数裁决，另附 C-4 更正见契约 §8）

## 待拍板岔口清单

1. **`GCResp.scanned` 的字段语义**。契约 wire 断言 1–13 钉了其余全部字段，唯独 `scanned` 只在金样本里以零值键出现、语义未钉（已记入契约 §8 修订记录「待拍板」条）。候选：(a) 对齐 `internal/proto/reclaim.go#ReclaimListResp` 的既有 `scanned` 语义——本轮判定读过的任务表终态任务行数（含无缓存叶子、无残树的行），CLI 可打「共扫 N 个终态任务」；(b) 报告行总数 = `cache_rows + worktree_rows` 行数；(c) 读到的任务表总行数（不分终态）。**倾向 (a)**：与 reclaim 同名同义，跨命令心智一致，且「体检了多少」与「报了几行」可分，丢了哪个任务一眼能看出；实现成本三案相同。
   **拍板（2026-08-30）：取 (a)。**同名同义跨命令一致；implement 落实现时以测试钉死该语义，并销契约 §8 的「待拍板」条。
2. **契约交棒欠账「全包 `go test ./internal/proto ./internal/client ./internal/agentd ./cmd` 退出 1」的处置**。失败原文（契约台账 2026-08-30 行）是 `integration_test.go:972` 的 `git: couldn't create cache file …/xcrun_db-… (errno=Operation not permitted)` 与 `status_test.go` 三行——与机内执行沙箱拦写缓存目录的形状一致，疑似环境失败而非代码失败，但 contract 节点未归因。候选：(a) implement 卡第一步在无沙箱限制环境复跑定性：属既有环境债则记账绕行（定向测试仍须全绿），属本分支引入则先修再动工；(b) 不专门定性，验收时一并看。**倾向 (a)**：带着一颗未爆的「全包红」进实现，红了分不清是谁的；定性成本一次复跑。
   **拍板（2026-08-30）：已由协调者复跑定性，implement 无需再做。**事实：① xcrun/git 缓存写失败是执行器沙箱产物，协调者无沙箱复跑无此错；② 协调者首轮全包跑出 `pty_ws_test.go:123 新会话 backlog_bytes = 160，期望 0`，随后三连对照——基线 `b0a1ac44` 单跑 ok、本分支单跑 ok、本分支全包复跑 ok（96.5s）——判定为间歇 flake，非本分支引入、不属本卡。T4 验收第 4 条按无沙箱环境执行；全包再红时先重跑一次确认是否 flake 再分流。
3. **契约 §8 修订记录 C-2 的追认**（工作树清理失败不豁免缓存叶子删除尝试，`compensateWorkspace` 提前 return 不得截走缓存删除）。本节点已按「边界澄清回写」纪律写入契约 §8；因它直接约束 implement 行为且冻结正文没写，**请拍板追认或否决**。倾向追认：两叶子与工作树互不依赖，抑制删除没有任何安全收益，只放大泄漏。
   **拍板（2026-08-30）：追认。**工作树处置与两片缓存叶子互不依赖，失败路径抑制删除无安全收益、只放大泄漏；T1 验收第 3 条按此口径出题。

---

卡：B298 · L3 轻档（spec 明确不扇出并行子卡） · 上游：spec 已批准 / 契约已冻结（提交 `26e2ab7fb5`，头部状态位已由本节点补钉）
台账：`docs/superpowers/ledgers/2026-08-30-b298-breakdown-ledger.md`
上游：`docs/superpowers/specs/b298.md`、`docs/superpowers/specs/b298-contract.md`（含 §8 拆解期修订记录）

## 一、触及子系统清单

子系统 id 与类型取自 `codegraph/best.json`（顶层领域即子系统，`type` 字段为准）。触及 = 有文件改动；只消费 = 只调用既有导出符号。

| 子系统 | 图类型 | 本卡角色 | 派卡资格四条核对（架构法第一条） |
|---|---|---|---|
| `d_protocol` 协议契约 | logic | `GCRequest`/`GCItemStatus`/`GCCacheRow`/`GCWorktreeRow`/`GCResp` DTO——Ticket 0 已冻结落盘，本卡原则上零改动 | ① 有界：`internal/proto/gc*.go`；② 面可枚举：5 个类型 + wire 断言 1–13；③ 依赖：无出边（纯被依赖）；④ 类型 logic（对面是同仓 Go 结构，机内闭环） |
| `d_orchestration` 任务编排 | logic | 缝 1 三收口删缓存 + 缝 2 `Manager.GC` 批处理判定 | ① 有界：`internal/agentd/manager.go`、`internal/agentd/gc*.go`、新增 helper 文件；② 面可枚举：`Manager.GC` 签名 + 断言 42–72；③ 依赖 DAG：→ `d_protocol`（contracts[26]）、→ `d_execution`（contracts[22]，复用 `TaskTmpDir`）、→ 本域 `store`（`k_store_Store` 归 d_orchestration，非跨子系统）；④ 类型 logic（对面是本机文件系统与 SQLite，`t.TempDir` 真实目录机内闭环） |
| `d_gateway` 控制门面 | boundary | `Server.handleGC` 成功路径写响应 + 路由（Ticket 0 已注册，预计零改动） | ① 有界：`internal/agentd/gc.go`（Server 侧）+ `server.go` 注册行；② 面可枚举：`GET/POST /api/gc` + 断言 19–21；③ 依赖 DAG：→ `d_orchestration`（contracts[14]）、→ `d_protocol`（contracts[16]）；④ 类型 boundary（图定：对面是 CLI/浏览器的 HTTP 现实）——本卡接缝对面实为自有 `Manager.GC`，httptest 可机内闭环报告形状与鉴权，端到端行为走真机清单 |
| `d_transport` 跨机连接（`d_transport_channel` 子域） | boundary | `Client.GCPreview`/`Client.GC`——Ticket 0 已实现并锁测试，本卡原则上零改动 | ① 有界：`internal/client/gc*.go`；② 面可枚举：两方法 + `ErrGCUnsupported` + 断言 24–31；③ 依赖 DAG：→ `d_protocol`；④ 类型 boundary（对面是目标 agentd 的网络现实）——404 探测/解码已机内 httptest 锁定，对真机的行为走真机清单 |
| `d_cli` 协调者命令面 | logic | `handoff gc` 命令：渲染与退出码补全 | ① 有界：`cmd/gc*.go`；② 面可枚举：命令 flag 形状 + 断言 32–41；③ 依赖 DAG：→ `d_transport`（contracts[8]）、→ `d_protocol`（contracts[6]）；④ 类型 logic（对面是 client 库，httptest 闭环） |
| （只消费）`d_execution` 执行契约 | boundary | `internal/executor/tempdir.go#TaskTmpDir` 只读复用，零改动 | 不派卡。走 target.json 在册 `d_orchestration → d_execution` 面（contracts[22]），边界澄清 C-1 已回写契约 §8 |

`d_web` 不触及：spec §3 明确不做前端，验收第 5 条「设置页五个分区无新增」以 diff 范围核对兜底（T4）。

**类型判定的诚实注记**：`d_gateway`/`d_transport` 的 boundary 标注按图引用；本卡内它们各自的接缝对面实际是自有代码（`Manager.GC`、httptest 夹具），故机内能闭环的比一般 boundary 多，真机清单只留真正出机的条目。

## 二、契约增量核对

对照 `docs/superpowers/specs/b298-contract.md`（72 条原子断言 = wire 1–13 + agentd 14–23 + client 24–31 + CLI 32–41 + 收口 42–58 + 批处理 59–72）逐条核对：

1. **上游状态位**：spec 头部「已批准（2026-08-29，用户原话「批准」）」✓；契约头部原写「已批准」（指上游 spec），**缺自身冻结标记**——已按 b229/b239/b249 惯例修正为「已冻结（2026-08-29，提交 26e2ab7fb5）」并追加 §8 修订记录（C-0），冻结正文一字未动。
2. **无新增接缝**：本稿引用的缝全部在契约 §2 冻结清单内——DTO、`Manager.GC`、`handleGC`+路由、`Client.GCPreview`/`GC`、`gcCmd`/`runGC`/`renderGC`；行为语义全部落在 §3 断言 42–72。未发现需要退回 contract 的新缝。✓
3. **五条新增契约面实核在册**：`d_cli→d_transport`[8]、`d_cli→d_protocol`[6]、`d_gateway→d_orchestration`[14]、`d_gateway→d_protocol`[16]、`d_orchestration→d_protocol`[26]——`codegraph/target.json` contracts 数组逐一命中。✓
4. **收口调 `TaskTmpDir` 不越面**：`d_orchestration→d_execution` 已在册（contracts[22]），且 `internal/agentd/manager.go` 现状即 import `internal/executor`——澄清 C-1，非增量。✓
5. **边界澄清回写**（即便不退回 contract 也留痕）：C-1（面归属）、C-2（收口「之后」= 工作树处置尝试之后，失败不抑制）、C-3（附区吸收标注落点改 implement 文档头）——均入契约 §8。C-2 同时列为岔口 3 请拍板追认。
6. **发现一处契约未钉语义**：`GCResp.scanned`——不是新缝，不做契约退回，提岔口 1 待拍板后由 implement 以测试钉死。
7. **断言计数复核**：13+10+8+10+17+14 = 72，与文档声称一致。✓
8. **Ticket 0 交付面与剩余工作量实核**（对照当前工作树）：`internal/proto/gc.go` DTO 完整；`internal/client/gc.go` 含双 404 探测与 200 解码（断言 24–31 代码已落，测试锁了 404 路径，200 解码待 T4 链路测试补锁）；`cmd/gc.go` 已接 client 与过旧降级，**execute 失败非零退出（断言 40）未实现**、`renderGC` 细节文案待补；`internal/agentd/gc.go` 仍是 503 空壳（`ErrGCUnwired`），`handleGC` 成功路径无写响应。结论：剩余实现集中在 d_orchestration（收口 + 批处理）、d_gateway（写响应）、d_cli（渲染/退出码）。✓

## 三、子卡清单 + 依赖 DAG

### 3.0 单实现卡直进的论证（L3 轻档）

- spec 已定：CLI 与编排是同一条用户故事的前后段，不扇出并行子卡。
- 有界文件集可以一条路径规则写出：`internal/proto/gc*.go`、`internal/agentd/{gc,manager,reclaim,server}*.go` 及 `internal/agentd/` 内至多一个新增 helper 文件、`internal/client/gc*.go`、`cmd/gc*.go`（测试含内）。其余文件零改动。→ 不需要为满足有界文件集而扇出，**单实现卡直进成立**。
- **架构法第三条显式回答**（判据 2 命中：`internal/agentd` 非测试源文件 64 个、`cmd` 51 个，均 ≥40 且无子包）：本卡能圈出有界文件集——触点集中在 `gc` 前缀家族（两包各 2 个文件，<3，无层内自我分解信号）与 `manager.go` 既有收口函数；不插竖切还债卡。`internal/agentd`/`cmd` 的整包规模是既有债，不随本卡扩权，留待专门的还债卡。

### 3.1 内部 ticket DAG（单卡内执行顺序）

```
T1 收口删缓存（d_orchestration）──┐
                                  ├─→ T3 CLI 渲染/退出码（d_cli）──→ T4 缝合与负例
T2 agentd 批处理 + 写响应 ────────┘
（d_orchestration + d_gateway）
```

T1、T2 无相互依赖，但共用同一套「短号占用判定 + tmp 根保护」helper（spec：Done/Stop/补偿与 gc 共用这一条）——单卡单上下文内先落 helper 再分别接线，无并行接缝风险。T3 依赖 T2 产出真实 `GCResp`（夹具可先行）。T4 收口全卡。

### 3.2 Ticket T1 —— 缝 1：三收口删缓存叶子

**① 契约引用**：断言 42–58；三重闸门记录 B、E；契约 §6 附区第 1–3 条（helper 点名接三处、复用 `TaskTmpDir`、遗留路径按完整 ID 组装、短号占用者从 `store.Store.ListTasks` 同一快照算、不复用 `ActiveTasksByWorkDir`）。

**② 意图与为什么**：收口是缓存泄漏的最高缝——任务一旦终态，私有 tmp/gocache 永久残留是 linux-01 堆到 150G 的主因。在既有「尽量删 managed 工作树」之后补删两处叶子（现役 `<DataDir>/tmp/<id8>`、遗留 `<DataDir>/tasks/<id>/tmp`），best-effort、失败不阻断收口；历史残留的重试入口是 gc 而非重发 done（done 对已 completed 短路，重发无效）。

**③ 验收（行为化，判据均可机内红绿）**：
1. waiting_review 任务走 Done：两叶子从 `t.TempDir` DataDir 消失；`render.log`/`frames.jsonl`/`proc.json`/任务目录/分支/SQLite 行全部还在；Done 返回 nil（断言 42、55、56）。
2. Stop 同款（43）。
3. 补偿路径：派发 Start 失败落 failed 后两叶子消失；且**managed 工作树删除失败的分支下缓存删除仍被尝试**（C-2；测试点名词抓 `compensateWorkspace` 漏接——spec 测试决定 1）（44）。
4. 空任务 ID 使活动 leaf 等值 `<DataDir>/tmp` 根 → 拒绝删除、根目录幸存（48；黄金向量形状见 `internal/executor/tempdir_test.go#TestTaskTmpDirGoldenVectors`）。
5. 同 id8 存在其他非终态任务 → 现役叶子保留；占用者集合不含正在进入终态的自己（49、50）。
6. 遗留叶子按完整 ID 判定，不受短号规则影响（51）。
7. 注入删除失败 → 收口成功返回，日志含任务/路径/原因（52–54）。
8. 非终态任务（含 waiting_review）不进删除集合（57）。
9. 缺陷族结论入栏：族 1（中断残留由 gc 兜底，58；TOCTOU 窗口已识别并接受，见 §四）、族 2（失败不静默成成功：nil 不虚报「曾删除」）、族 3（Windows 删除失败走日志路径不崩溃）、族 5（收口入口与 gc 共用同一占用/根保护规则）。

**④ 入口指针**：`internal/agentd/manager.go#Manager.Done`（1387 行）、`internal/agentd/manager.go#Manager.Stop`（1502 行）、`internal/agentd/manager.go#Manager.compensateWorkspace`（1090 行）；复用 `internal/executor/tempdir.go#TaskTmpDir`（18 行）、`internal/proto/proto.go#TaskState.IsTerminal`；短号占用快照 `internal/store/store.go` `Store.ListTasks`（414 行）；有界文件集 = `internal/agentd/manager.go` + `internal/agentd/` 内新增 helper 文件（落点命名归 plan）+ 对应 `_test.go`。

### 3.3 Ticket T2 —— 缝 2 agentd 侧：`Manager.GC` 批处理 + `handleGC` 出报告

**① 契约引用**：断言 14–23、59–72；三重闸门记录 A、C、D；契约 §6 附区第 4–5 条（残树批处理复用 reclaim 分类与 `WorkspaceGitTimeout`，不从 CLI 逐个 POST 单任务 reclaim）。

**② 意图与为什么**：拖历史的最高缝——任务表里已堆着的终态缓存与残留 managed 树需要一次机器级「先看再清」。预览/执行同一判定函数，`--yes` 当下重算不共享快照（记录 C）；`releasable_bytes` 用 `*int64` 保住缺席/零可分（记录 D）。Ticket 0 的 503 空壳必须被真实资源动作替换，503 测试退役，不留「已接线但 503」的假绿。

**③ 验收（行为化）**：
1. `GC(ctx,false,false)`：夹具 DataDir 含终态+非终态任务 → 报告只含终态叶子行、路径去重、`releasable_bytes` = 去重路径字节和、盘上零删除（15、59–62）。
2. `GC(ctx,true,false)`：只改脏 managed 树的报告处置，仍零删除（16、62）。
3. `GC(ctx,_,true)`：执行前重读快照（夹具在判定后改状态可钉 64/65）；执行后终态叶子消失、净/prunable 树按 reclaim 语义收、脏树无 force → `skipped` 行且其余行照常（17、18、63、66–69、71）。
4. 缓存 `RemoveAll` 失败注入 → `failed` 行进报告、`Failures` 计数、不阻断其余行；skip 不计入 `Failures`（13、70、72）。
5. 本就不存在的叶子按幂等成功处理，不产生 `failed` 行、不虚增字节（47 + 依赖库行为 3）。
6. `GET /api/gc` 只走 execute=false、`POST /api/gc` 只走 execute=true；成功路径写出 `GCResp` JSON（Ticket 0 缺口补全）（19、20）。
7. 未带凭据打 `/api/gc` → 401，与 `/api/reclaim` 同门（21；auth 包裹现状 `internal/agentd/server.go#Server.Handler` → `s.auth(mux)`）。
8. `TestHandleGCTicket0` 退役，代之以真实资源断言；503 空壳不可再达（契约 §2.2 Ticket 0 注记）。
9. 孤儿目录（盘上有、任务表无行）不扫（59）。
10. 缺陷族结论入栏：族 1（执行中崩 → 幂等重跑；TOCTOU 接受项）、族 2（失败必进报告，70）、族 4（503 假绿退役）、族 5（双路由同一 auth 门）。

**④ 入口指针**：`internal/agentd/gc.go#Manager.GC`（36 行）、`internal/agentd/gc.go#Server.handleGC`（52 行）、`internal/agentd/gc.go#ErrGCUnwired`（22 行）；复用 `internal/agentd/reclaim.go#Manager.Reclaim`（251 行）、`internal/agentd/reclaim.go#Manager.ReclaimList`（342 行）；路由 `internal/agentd/server.go` 498–499 行；有界文件集 = `internal/agentd/gc.go`、`internal/agentd/gc_test.go`、`internal/agentd/server.go`（预计零改动）、`internal/agentd/reclaim.go`（只读复用）。

### 3.4 Ticket T3 —— CLI：渲染与退出码

**① 契约引用**：断言 32–41；三重闸门记录 A、D。

**② 意图与为什么**：CLI 是 gc 的唯一操作面（无前端），文本契约必须可脚本化——字节量必打、脏树 skip 可见、「本应删而失败」才非零。Ticket 0 已交付 flag 形状、target client 接线与过旧降级；剩余是渲染细节与断言 40 的退出码。

**③ 验收（行为化）**：
1. 无 `--yes` 只发 GET，盘不动；输出含将释放字节量（33、39）。
2. 仅 `--force` 仍 GET 且带 `?force=true`，不动盘（34）。
3. `--yes` 发 POST、force 透传请求体（35、36）。
4. `--json` 输出完整 `GCResp`，`releasable_bytes` 缺席与 0 在输出中可分（37）。
5. execute 报告存在 failed 行 → 退出非零；仅 skip/待清理 → 退出 0（40；现状 `cmd/gc.go#runGC` 未实现，须补）。
6. `ErrGCUnsupported` → 「过旧，升级后再跑 gc」退出 0（38；`cmd/gc_test.go#TestRunGCDegradesOnOldAgentd` 已锁，保持绿）。
7. 位置参数拒绝（32）；`--target` 与 reclaim 同一 `cmd/root.go#newTargetClient`/`TargetEndpoint` 语义（41）。
8. 缺陷族结论入栏：族 2（preview 恒 0、失败可行动）、族 4（无 `--yes` 不动盘以请求路径断言负例可红）。

**④ 入口指针**：`cmd/gc.go#gcCmd`（30 行）、`cmd/gc.go#runGC`（57 行）、`cmd/gc.go#renderGC`（95 行）；渲染惯例参照 `cmd/reclaim.go`；有界文件集 = `cmd/gc.go`、`cmd/gc_test.go`。

### 3.5 Ticket T4 —— 缝合与负例（跨五子系统收口）

**① 契约引用**：断言 24–31 复验、37–38 复验；追加族「序列化边界」设问；spec 验收判据 5（设置页无新增）。

**② 意图与为什么**：接缝缺陷不属于任何单个包——「两端各自绿、中间断」只有一条穿过真实 JSON 边界的链路测试能抓住。client 200 解码（断言 26/27）现状无测试锁定，Ticket 0 只锁了 404 探测；本 ticket 补链。

**③ 验收（行为化）**：
1. 链路回归一条：httptest agentd 产出真实 JSON 两种形状（`releasable_bytes` 缺席 / 显式 0）→ `Client.GCPreview` 解码 → `runGC --json` 与 `renderGC` 输出中缺席与 0 可分——穿过真实序列化边界，非两端各自测（10、11、26、27、37）。取舍：wire 面仅 7 键，金样本（`internal/proto/gc_test.go#TestGCGoldenJSON`）+ 此链路回归已覆盖缺席/零与形状；不强制 roundtrip 属性测试，如拍板认为要再加不迟。
2. `GCItemStatus` 四值（planned/deleted/skipped/failed）各至少一次出现在渲染输出（追加族「枚举新值」的链路证据）。
3. 老 agentd 双 404 保持绿：`internal/client/gc_test.go#TestGCPostDouble404IsUnsupported`、`cmd/gc_test.go#TestRunGCDegradesOnOldAgentd`（28–31、38）。
4. 全包 `go test ./internal/proto ./internal/client ./internal/agentd ./cmd` 在无沙箱限制环境退出 0（既有沙箱失败按岔口 2 定性后处置）。
5. `git diff` 对基线不含 `web/` 路径——验收 5「设置页五分区无新增」的结构证据。
6. 缺陷族结论入栏：族 4（负例清单可红：未鉴权 401、根保护拒绝、无 --yes 不动盘）、追加族（序列化链路、枚举链路）。

**④ 入口指针**：`internal/proto/gc_test.go#TestGCGoldenJSON`、`internal/client/gc_test.go`、`internal/agentd/gc_test.go`、`cmd/gc_test.go`；有界文件集 = 上述测试文件（实现文件目标零改动，如需小改限于 T1–T3 已列文件）。

### 3.6 行为闭环核对（spec 跨子系统可观察行为 → 归属）

| 触发者 | 权威事实/载体 | 消费者 | 可观察结果 | 归属 |
|---|---|---|---|---|
| 协调者 `handoff done`（waiting_review 任务） | POST `/api/tasks/{id}/done` → `Manager.Done` 收口 → 目标机 DataDir 两叶子 | 该机磁盘；协调者 `handoff attach` | `<DataDir>/tmp/<id8>` 与 `<DataDir>/tasks/<id>/tmp` 消失；任务目录与 render.log 仍在；done 成功返回 | T1 |
| 协调者 `handoff stop` | `Manager.Stop` 收口（任务落 failed） | 同上 | 同上 | T1 |
| 派发 Start 失败（Dispatch defer） | `Manager.compensateWorkspace` 补偿收口（任务已 failed） | 同上 | 两叶子消失；工作树处置失败不抑制缓存删除（C-2） | T1 |
| 协调者 `handoff gc [--target X] [--force]` | GET `/api/gc`（auth 门）→ `Manager.GC(execute=false)` → `GCResp` 去重字节与行清单 | CLI 人读/`--json` 输出 | 打印将释放字节与将跳过脏树；盘不动；退出 0 | T2+T3 |
| 协调者 `handoff gc --yes [--force]` | POST `/api/gc` → `Manager.GC(execute=true)` 当下重算 → 删缓存 + 内部调 `Reclaim` 收残树 | 该机磁盘；CLI 报告 | 终态叶子消失、非终态仍在；净树收、脏树无 force 则 skip；failed>0 才非零 | T2+T3 |
| 对端 agentd 未含本卡 | POST 404 → GET 探测 404 → `ErrGCUnsupported` | CLI | 「过旧，升级后再跑 gc」退出 0；非空预览、非「任务不存在」 | Ticket 0 已交付；T4 复验 |
| 存在 waiting_review 任务 | `TaskState.IsTerminal` = false（收口与 gc 两侧同判） | 该任务的 executor | 其缓存叶子保留，`continue` 可再跑测试（spec 用户故事 4） | T1+T2 |
| 同 id8 存在其他非终态任务 | `store.Store.ListTasks` 快照占用判定（不含自己） | 该并行任务的 executor | 现役叶子保留 | T1+T2 |
| 空 task ID / 路径等值 `<DataDir>/tmp` 根 | 根保护显式拒绝（依赖库行为 1、3 证明库不兜底） | 进行中任务的 executor | 根目录幸存，删除动作不发出 | T1+T2 |

每行五格完整，归属 ticket 均存在；无只活在接口或测试里的承诺。

## 四、缺陷族对抗审查（defect-families 通用五族 + 追加设问）

**族 1 生命周期/状态机中断**
- done/stop/补偿中途 agentd 重启：transit 已落库、删除未跑 → 缓存残留；gc 是法定重试入口（58），gc 自身幂等可重入（47、63、`Manager.Reclaim` already_absent 语义）。无新增需收尾的运行时资源（删除是一次 RemoveAll 序列，无锁无后台态），**除此之外无**——因为不新增任何状态机状态、工单或进程。
- 已识别并接受的 TOCTOU：占用者判定（`ListTasks` 快照）与 `RemoveAll` 之间有窗口，窗口内同 id8 新任务 Start 会丢刚建的 TMPDIR；后果=该新任务 executor 写临时文件失败（任务失败，可观察、不毁用户数据/工作树）；接受理由=彻底消除需目录级锁或 rename 方案，成本与风险高于后果。写进 T1/T2 验收栏作已知接受项，测试不锁此窗口。

**族 2 静默失败 / 误导报错**
- 收口删除失败只进日志（52–54）：这是 spec 明选（与工作树清理失败同款），可观察出口=gc；误导窗口「归档成功但缓存仍在」被 spec 接受（重试入口 gc）。T1 日志必须含任务/路径/原因。
- gc 路径「报成功但没做」窗口：被 70（失败必须进人读+JSON）与 13（failures 统计语义）堵死；T2 注入测试钉。`os.RemoveAll` 对不存在目标返回 nil（依赖库行为 3）→ 报告不得把「本不存在」渲染成 failed、字节求和不含不存在路径——T2 验收第 5 条。

**族 3 跨平台假设**
- Windows：被占用文件 `RemoveAll` 失败 → 必须走报告/日志失败路径而非命令崩溃；路径拼装全走 `filepath`（`TaskTmpDir`/`Join`）；id8 取前 8 字节与平台无关；字节统计若遍历，`WalkDir` 不跟随 symlink（依赖库行为 2、5、8）保证不越出叶子。机内可用真实临时目录闭环删除/统计；Windows 真机差异进真机清单第 5 条。**无其他**——本卡不引入新平台假设，超出既有 reclaim/`TaskTmpDir` 形状的部分没有。

**族 4 假红 / 假绿测试**
- `TestHandleGCTicket0` 是刻意的临时绿：契约 §2.2 明文要求退役——T2 验收第 8 条点名，防「已接线但 503」假绿存活。
- 占用/根保护测试锁的是产品承诺（非终态任务不被误删），换实现不改需求不会无意义地红——合规。
- 夹具目录形状可能与真实 gocache 布局不符：夹具全绿 ≠ 真机释放——真机清单第 1、2 条对应，未验证条目不得写成结论。
- 负例必须能红：未鉴权 401（T2）、根保护拒绝（T1/T2）、无 `--yes` 不动盘以请求路径断言（T3）——各自点名，acceptance 变异复验有测试可红。

**族 5 门禁绕过**
- `/api/gc` 双路由在 `s.auth` 包裹内（`internal/agentd/server.go` `Server.Handler`：`root.Handle("/", s.auth(mux))`，`/api/` 前缀经 `mux.Handle("/api/", api)`）——断言 21；T2 加未鉴权 401 反例使门可红。
- 缓存删除的全部入口（三收口 + gc 批处理）共享同一占用/根保护规则（spec「共用这一条」）——门覆盖全部表面；T1/T2 用同一 helper，测试各钉一侧防只改一处。
- 检查与动作的 TOCTOU 窗口 → 族 1 已答（接受项）。
- CLI/client 侧无本地删除或字节计算路径（断言 25；Ticket 0 边界注释）→ 无绕过面；T4 diff 范围核对佐证。

**追加设问一：序列化边界**——投影点清单：agentd `GCResp`→JSON（json tag，`TestGCGoldenJSON` 锁）、client 200 解码（现状无锁 → T4 链路回归补）、CLI `--json` 透传 Encode（无手写投影）、`renderGC` 人读投影（`TestRenderGCDistinguishesUnknownBytes` 已锁缺席/零）。链路级一条穿真实 JSON 边界的回归归 T4 验收第 1 条。

**追加设问二：枚举新值过既有白名单**——`GCItemStatus` 四值为新类型新枚举，流经点=agentd 产生、client 不解释、CLI 渲染；中间**无**既有校验器/白名单/switch，无通道分裂面。**无风险，因为**枚举只存在于产生端与终结渲染端之间，无第三方消费方持白名单；T4 验收第 2 条以四值渲染各现一次作链路证据。

**追加设问三：承重安全属性有测试锁住**——五条逐一点名：tmp 根不可删（T1.4/T2）、非终态不删（T1.8/T2.1）、短号占用保留（T1.5/T2）、未鉴权不可达（T2.7）、force 无 `--yes` 不动盘（T2.2/T3.2）。每条都有能变红的测试，acceptance 变异复验有对应物。

## 真机清单（归协调者执行；机内夹具验不了的行为事实）

1. linux-01 升级 agentd 后：`done` 一个跑过 `go test` 的任务 → 该任务 gocache 叶子消失、`handoff attach` 仍可读 render.log（spec 用户故事 1 / 验收 1）。
2. linux-01：`handoff gc --target linux-01` 预览给出**真实缓存规模**的去重字节量与脏树 skip 行；`--yes` 后终态叶子消失、非终态任务 tmp 仍在、退出 0（验收 2）。
3. 未升级机器：`handoff gc --target …` 报过旧、退出 0、盘上不变（验收 3）。
4. linux-01：一净一脏无 `--force` → 脏树仍在退 0；`--yes --force` 后脏树消失（用户故事 2）。
5. Windows 真机（如仍在支持矩阵）：gc 冒烟——文件被占用导致的删除失败呈现为报告 failed 行/日志，命令不崩溃（族 3 行为面，机内夹具造不出真实文件锁竞争）。
6. 全包 `go test` 在无沙箱限制环境复跑定性（岔口 2 的执行落点）：全绿或明确既有环境债范围。

## 图覆盖债

- `codegraph sym` 不接受 `file#Symbol` 查询形（连契约已冻结的 `Manager.GC` 锚也报「不在图中」）；锚点合法性以 `codegraph resolve --doc` 为准（contract 节点同形锚全部 ok）。本稿收尾已跑 `resolve --doc`，结果见台账。
- 沿承 contract 台账的既有项：`validate --stale` 退出 1（decl 域缺失 + stale 节点）、`check --stale` 退出 0 但带既有 warnings（anchor-off-domain、container-misplaced、legacy、oversized-package、prefix-family）——均为 baseline 债，不归本卡修。

## 交棒

- 交棒：implement（单轮；契约 §6 附区吸收标注落 implement 执行文档头，见 C-3）。
- 欠账：岔口 1–3 待协调者拍板；真机清单 6 条归协调者；契约 §8 修订记录为拆解期新增，review 时冻结物触碰行以 §1–§7 + §8 对照。
