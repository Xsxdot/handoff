# B298 spec 审查

审查对象：`docs/superpowers/specs/b298.md`（状态：待独立审查；头部定级 L2，自 L3 改判）
对照台账：`docs/superpowers/ledgers/2026-08-29-b298-spec-ledger.md`
对照 roadmap：`docs/roadmap.md`「来自 B298 spec」四条（工作区已改、未提交）
冻结设计：`docs/superpowers/specs/2026-08-12-failed-task-worktree-reclaim-design.md`（B77 reclaim）
定级先例：`docs/superpowers/specs/b286.md` 定级理由；同日 `docs/superpowers/specs/b294.md` L3 轻档
对照代码：工作树 `/Users/sycm/.grok/worktrees/repos-handoff/2026-08-29-22337fb3`，分支 `cards/B294-breakdown`，HEAD `1fa4668b`。B298 spec / 台账为未跟踪文件，roadmap 为已暂存/工作区改动。B294 生产 diff 不在审查范围。
审查者：独立 spec 审查人（charter 流，只读；与作者无会话史，一切以亲手读码为准）
日期：2026-08-29

行号按当前工作树，会漂。`codegraph` CLI 本会话不在 PATH；`k_agentd_Manager → d_orchestration`、`k_agentd_Server → d_gateway`、`c_cli / k_cmd_fn → d_cli` 从 `codegraph/best.json` 容器表核过。未跑 `flow`，图覆盖债与 spec 备注一致。linux-01 / 本机 `du` 未独立取证（本审查无该执行机），采信台账为运行时读数、不当作代码事实。

## 1. 总判

**修订后再批。**

方向对，且与活代码、B77 冻结非目标、B160 归属判据对齐：终态才丢可再生缓存；`render.log` / 任务目录 / SQLite / 分支 / 用户自建树 / `agentd.log` 不动；`handoff gc` 默认预览、`--yes` 才动盘、`--force` 只对脏 managed 树沿用 reclaim；不做设置页。缝 1 收口 `Done`/`Stop`（加派发失败补偿）是堵住新泄漏的正确最高缝；缝 2 拖历史。不是 L1。弃选站得住。roadmap 四条后续项已落账。

不能批的原因不是产品走偏，是两件承重的事正文没钉死：

1. **定级独立判为 L3 轻档，不能留 L2。** 作者用「架构法第一条实操裁决 + B286」把「会新增 HTTP 端点」压成 L2。B286 留 L2 的前提是**不新增 HTTP**；本卡自己写「虽会新增端点」。实操裁决回答的是「值不值得为并行开发付冻结成本」→ 选档轻档，不是豁免 定级两问。轻档不许跳契约冻结。
2. **`gc` 作为跨机删盘命令，失败/跳过/老 agentd/扫描范围仍能读成互不兼容的实现。** 零上下文落地会在 linux-01 这条主故事上要么对老 agentd 报错跑偏（B77 专门消过的 404 歧义），要么第一棵脏树让整批停掉、缓存只清一半还报成功。

批准前最小补丁（只改 spec 正文，不是代码）：改判 L3 轻档并补契约语义段（不定签名）；把 I1–I3 写进该段。M1–M5 建议顺手带上，不单独挡批准。

## 2. Findings

### Critical

#### C1. 定级 L2 不成立；应 L3 轻档。作者引用的 B286 先例与本卡「会新增端点」直接相反

- **位置**：spec 头部与定级理由 `b298.md:4-16`；台账「定级」段；活代码 `codegraph/best.json:7-51,125-158`（顶层域）、`internal/agentd/server.go:435-517,619`（HTTP 面在 `k_agentd_Server` / `d_gateway`）、`cmd/reclaim.go:35-44`（`d_cli` 经 `newTargetClient` 打跨进程 reclaim）；对照 `docs/superpowers/specs/b286.md:16`、`docs/superpowers/specs/b294.md:10-22`；法条：`spec/SKILL.md`「定级两问」、`architecture-law/SKILL.md` 第一条实操裁决
- **事实**：
  1. 定级两问：跨几个子系统契约面？动不动契约层？「实现或对接一个已存在的跨仓/跨进程 wire 契约，即便本侧零修改，也按动契约层计」。答案跨子系统或动契约 → L3。
  2. 本卡缝 2 是新建、由 `handoff gc`（可 `--target`）调用的 agentd 能力。协调者 CLI 与执行机 agentd 已经是两个顶层域（`d_cli`、`d_gateway`/`d_orchestration`），中间的 HTTP 是既有跨进程契约面。新增端点 = 在该契约面上加动词，不是领域内部导出。`d_protocol` 还要为预览字节量 / 终态任务数 / 工作树四态加 wire 形状（reclaim 已有 `internal/proto/reclaim.go`，gc 不能靠 `ReclaimListResp` 表达缓存字节）。
  3. 作者写「机械『新 HTTP = 跨进程 wire』过税」并引 B286。B286 定级理由原文是「**不新增 HTTP 路径**」。B286 审查把「无新 HTTP」当作 L2 成立要件。本卡同一段承认「虽会新增端点」。先例切的是反面。
  4. 同日 B294：新 CLI 命令族 + 新 HTTP/WS 面 + `d_protocol` wire → **L3 轻档**，理由「跨子系统、动契约层……选轻档：拆并行子卡会在接缝上等对方。契约冻结照做，实现一轮收口」。这才是形状匹配的先例。
  5. 架构法第一条实操裁决问的是：拿不准要不要把一块东西**升格为子系统**时，值不值得为这条缝付冻结成本来换**并行开发**。`d_cli` / `d_gateway` 早已是子系统，不是本卡要升格的对象。作者用它论证的「CLI 与编排不会拆成可并行子卡」= L3 **轻档**判据（`spec/SKILL.md`：轻档不许跳契约冻结），不能把级降到 L2。
- **为什么承重**：批成 L2 则下一节点是 plan，跳过 contract。轻档的法定半段就是把跨进程语义冻住（预览只读、`--yes` 才写、404 消歧、批处理失败、扫描范围），签名仍归 contract 节点。跳过之后 I1–I3 会在 plan 里被当成「内部落点」现编，两个实现者会做出可观察行为不同的删盘命令。
- **建议**：头部改 **L3 / 轻档 / 路由 contract → breakdown →（单轮）implement → …**。定级理由改成：缝 1 单落 `d_orchestration`；缝 2 跨 `d_cli` + `d_gateway` + `d_protocol` + `d_orchestration`，新增跨进程动词，故 L3；CLI 与编排同一条用户故事、不扇出子卡，故轻档。补「契约语义」段（不定路径/类型名），最低限度收入 I1–I3。不要再拿 B286「没新增 HTTP」为新增 HTTP 辩护。

### Important

#### I1. `handoff gc --target linux-01` 打到未升级 agentd 时的 404 消歧未写

- **位置**：用户故事 2 `b298.md:112`、方案 2 `b298.md:64-67`、实现决定 `b298.md:135`；活代码 `internal/client/client.go:573-645`、`internal/agentd/server.go:440,496,517`；B77 设计 §5.1（404 消歧「必须专门处理」）
- **事实**：今天没有 `gc` 命令、也没有对应路由。linux-01 故事的第一下就是协调者本机 `handoff gc --target linux-01`。对端在升级前对未知路径走 `s.auth` 内层 mux，精确匹配失败 → **404**，与「资源不存在」撞码。B77 已经在 `Client.Reclaim` 为同一撞码写了「补打 GET /api/reclaim；两条都 404 → `ErrReclaimUnsupported`，CLI 打印『版本过旧』**退 0**；列表 200 才是真不存在」。`runReclaimList` 对 unsupported 也退 0（`cmd/reclaim.go:71-76`）。本卡未写 gc 的对应纪律。
- **为什么承重**：三种零上下文读法，用户可见行为全不同。(a) 把 404 译成「任务/资源不存在」退非零——B64/B77 点名的错方向；(b) 当作命令未实现，提示升级，退 0（与 footprint/reclaim/status 同款）；(c) 预览空表退 0，看起来像「没有可清的」——150G 还在，假绿。故事 2 是本卡存在的理由，第一条通道就是这台未升级机。
- **建议**：契约语义写死：对端无此能力（预览与执行端点皆 404，或等价探测）→ 明确「该 agentd 过旧，升级后再跑 gc」，**退 0**（诊断结论，不是失败）；禁止译成任务不存在，禁止空预览冒充「无可清」。接缝 2 加这一支负例。升级顺序可以一句 OOS/验收：「目标机 agentd 需含本卡之后，gc 才清得到盘」。

#### I2. `gc --yes` 批处理与单棵 `Reclaim`「失败即失败」未划界；退出码与失败可见性可做成三种活

- **位置**：方案 2 执行内容 `b298.md:84-87`、测试接缝「纯资源动作」`b298.md:120-124`、测试决定缝 2 `b298.md:143`；活代码 `internal/agentd/reclaim.go:246-248,286-288`（脏树无 force 返回 `*DirtyWorktreeError`）、`cmd/reclaim.go:101-106`（单任务脏树退非零）；B77 §7「回收不降级……单任务回收失败就是失败」vs §4.1「列表恒退 0」
- **事实**：spec 要 gc 内部走现有 `Reclaim`：净/prunable 直接收，脏树无 `--force` **跳过并报告**，判不出如实留下。但 `Manager.Reclaim` 对脏树/非终态/非 managed/仓库不可达是 **error 返回**，不是 skip 结果。CLI 单任务路径把脏树变成退 1。另：「调用方改为清理命令的执行段」既可读成 agentd 内循环 `m.Reclaim`，也可读成 CLI 对残留行逐个 `POST /api/tasks/{id}/reclaim`。Done/Stop 上缓存删除是「失败只记日志，不阻断」（`b298.md:58`）；gc 是人主动发起的删盘，B77 对这类动作的纪律是「吞错误没有好处」。
- **为什么承重**：读法 A：第一棵脏树让 `Reclaim` 报错，整次 `--yes` 中止——此时若缓存已按 spec 顺序先删，工作树只清了一部分，退出非零；若先收树，缓存可能一字节没动。读法 B：脏树/判不出当 skip，其余继续，退出 0。读法 C：缓存 `RemoveAll` 失败也只打日志仍退 0（抄 Done），人读报告写「已清」而目录还在——静默失败族。预览「将跳过的脏树」与执行「真的跳过而不是 abort」必须是同一句话。循环在 CLI 还是 agentd 还决定 I1 的探测次数、以及 `--target` 中途断线是否留下半批。
- **建议**：写死四句：① 收树循环在 **agentd 该条 gc 能力内部**调 `Manager.Reclaim` / `ReclaimList`（CLI 不逐个打 reclaim；跨机仍是一次 `--target`）。② 脏树无 `--force`、判不出、非 managed = **本行 skip**，不得 `return err` 中止本轮其余缓存删除与其余树。③ `RemoveAll` 缓存失败必须出现在人读（及 `--json`）报告里，不得只进 `agentd.log`。④ 退出码：预览恒 0（拿不到列表才非零，同 reclaim 列表）；`--yes` 仅当存在「本应删除却失败」的项时非零；「脏树被跳过」不是失败。接缝 2 加：一净一脏无 `--force` → 净树与终态缓存都清掉、脏树仍在、退出 0。

#### I3. 「历史缓存」扫描范围与删除目标的安全边界未钉；`TaskTmpDir("",)` 等值 `DataDir/tmp`

- **位置**：问题陈述「已经堆着的历史缓存」`b298.md:27`、方案 2「所有终态任务的两处缓存目录」`b298.md:86`、短号碰撞 `b298.md:60,123`；活代码 `internal/executor/tempdir.go:18-24`、`internal/executor/tempdir_test.go:16-33`（空 id → `/var/lib/handoff/tmp`）、`internal/store/store.go:414-432`（`ListTasks` 全表、无删除任务 API）
- **事实**：方案正文的枚举主语是「终态任务」的两处路径（存在才删）。问题陈述的主语是「已经堆着的历史缓存」一键清掉。第二种读法是 `ReadDir(DataDir/tmp)` 扫孤儿短号、或直接 `RemoveAll(DataDir/tmp)`。黄金用例把空任务 id 的 `TaskTmpDir` 钉成 **tmp 根目录本身**。生产任务 id 是 `uuid.NewString()`（`manager.go:805`），正常路径碰不到空 id；实现若把「清 tmp」写成整棵根、或对扫出来的异常短号调用 `TaskTmpDir`，会把**进行中任务的缓存一并删掉**（含 claude `perm.sock` 所在的 TMPDIR）。任务行目前只增不删，sqlite 枚举能覆盖台账里那 623 个任务目录对应的缓存；真正的孤儿目录（无任务行）正文没表态。
- **为什么承重**：清「任务表里的终态两处叶子」与「把 DataDir/tmp 清空」对 150G 和正在跑的任务是两种产品。后者不可逆，且破坏非终态「continue 还能跑测试」（故事 3）。短号碰撞已规定「有非终态占用者不删该短号目录」，但没规定删除目标不得等于 `DataDir/tmp`。
- **建议**：写死：只按任务表终态行计算两处叶子（`TaskTmpDir(DataDir, id)` 与 `tasks/<完整id>/tmp`），存在才 `RemoveAll` 该叶子；**禁止**把等值于 `filepath.Join(DataDir, "tmp")` 的路径当作删除目标；磁盘上无任务行的孤儿目录本期不扫，写进 OOS（可挂 roadmap）。短号：同一 id8 下有任一非终态占用者 → 现役叶子本轮不删（Done/Stop 与 gc 同一条）。预览字节按将删除的**去重后路径**求和，禁止两终态任务共用 id8 时把同一目录加两次。

### Minor

#### M1. 问题陈述写「Go/npm 构建缓存」，现役 adapter 只钉了 TMPDIR/GOTMPDIR/GOCACHE

四份生产 adapter（codex `adapter.go:138-143`、opencode `adapter.go:155-159`、grok `adapter.go:74-78`、claudecode `adapter.go:86-90`）同构三键，**没有** `NPM_CONFIG_CACHE`。默认 npm 缓存在 `~/.npm`，不在 DataDir。验收句是「gocache/tmp」（`b298.md:149`），与代码一致。问题陈述的 npm 会让人以为 `gc` 清得到机器级 npm 缓存。改成「任务私有 tmp/gocache（Go 测试缓存；若命令自己把 npm 缓存指进 TMPDIR，则一并清）」即可。

#### M2. 现状表只点 codex/opencode，漏 grok/claudecode；结论仍成立

无第三生产布局。fake adapter 明确不碰文件系统（`internal/executor/fake/fake.go:9-12`）。现状表补两行 adapter 出处，避免 plan 以为还要探第三路径。

#### M3. `gc --force` 不带 `--yes` 仍是预览，建议写成命令形态

`--yes 才动盘`（`b298.md:82`）足够挡「`--force` 暗示执行」。建议补一句：`handoff gc --force` 与 `handoff gc` 同为预览，只是把脏树从「将跳过」改列为「将强删」；真正强删仍要 `--yes --force`。与故事 2 的三步操作一致。

#### M4. 台账本机 tasks「7.7G（缓存 ~7.9G / 其余 0.43G）」自相矛盾

spec 正文写「任务目录里缓存 ~7.9G、其余 0.43G」（`b298.md:43`），台账写 tasks 7.7G 内含 7.9G 缓存。不影响方案，口径统一即可。linux-01 150G 本审查未独立 `du`。

#### M5. 「已经会走同一条工作树清理的派发期失败补偿」不是 Done/Stop 函数

`compensateWorkspace`（`manager.go:1068-1106`）是 Dispatch defer（`897-908`）在 `executorStarted==false` 时另调 `RemoveManagedWorktree`，与 Done/Stop 并列第三处。adapter.Start 在 CreateTask 之后（`968,994-1001`），Start 里已经 `MkdirAll` tmp（codex `adapter.go:310-314`）；Start 失败会 `transitBestEffort(failed)`，任务在表里、tmp 在盘上、工作树走补偿。把缓存删除接到这一处是对的，但「同一条路径」不成立——实现漏接补偿处不会被编译器提醒。建议现状/缝 1 点名 `compensateWorkspace` 符号。

## 3. 现状读数逐条对码

| spec 引用 | 实际 | 结论 |
|---|---|---|
| `done` 删 managed 工作树，失败只降级为 progress；不删任务目录 | `Manager.Done` `manager.go:1387-1474`：`1459-1473` 仅 `RemoveManagedWorktree`；失败 `AppendEvent(progress)` + `worktreeCleanupHint`；无 `RemoveAll` 任务目录或 tmp | 成立。**也不删两处缓存**（本卡要补的洞） |
| `stop` 同样删 managed、不删分支 | `Manager.Stop` `1502-1582`：注释 `1496-1498`「不删任务分支」；`1565-1579` 同款 managed 清理 | 成立。同样不删 tmp |
| 现役 tmp = `<DataDir>/tmp/<id8>`，adapter 指 TMPDIR/GOTMPDIR/GOCACHE | `executor.TaskTmpDir` `tempdir.go:18-24`；codex `taskTmpDir`/`tmpEnvKVs` `adapter.go:129-143,310-316`；opencode `managedTaskTmpEnv` `adapter.go:147-159` | 成立。grok/claudecode 同构（M2）；fake 不写盘 |
| 旧布局 `tasks/<id>/tmp` 现役不再写入 | 全仓生产写入点只走 `TaskTmpDir`；`tasks/.../tmp` 仅出现在 codex 注释（`adapter.go:124`）说明为何弃用 | 代码侧成立。盘上 7.9G/81G 未独立 `du` |
| `reclaim` 只收终态 managed；不删任务目录/分支、不改状态；脏树无 `--force` 拒 | `reclaim.go` 包头 `8-12`；`Reclaim` `263-268,286-288`；`ReclaimList` `357-358` 跳过非终态/非 managed；`cmd/reclaim.go:7-11,57-58` | 成立。与 B77 §2 非目标一致 |
| `agentd.log` 无轮转 | `internal/logx/logx.go:8`「不管理日志轮转」；`Setup` 只 `O_APPEND` | 成立 |
| 设置五分区：开发机 / 执行纪律 / 常规 / Env 文件 / 更新；无 reclaim、无磁盘 | `web/src/app/settings/SettingsPage.tsx:28-35` `SECTIONS` 五键，无磁盘/清理 | 成立 |
| 设置页归属是持久配置，不是一次性动作 | B160 spec §1.1：落在哪就属于谁；一次性删盘不是 A/B/C 任一类配置 | 成立。不做前端的裁决与判据一致 |
| linux-01 150G / 本机 11G 等 | 台账「现状探针」；本审查未连 linux-01、未跑本机 `du` | **未独立核**。不挡方案，不当作代码事实 |
| `Manager.ReclaimList` 最优图归 `d_orchestration`（容器 `k_agentd_Manager`） | `codegraph/best.json:133` `k_agentd_Manager: d_orchestration` | 成立。`k_agentd_Server` 在 `d_gateway`（`125`），新 HTTP 落网关不是编排内部 |
| 终态 = `done`/`stop`（及派发失败落 `failed`）；`waiting_review` 非终态 | `proto.IsTerminal` `proto.go:33-36` 只有 `completed`/`failed`；`waiting_review` 不在其中。`failed→running` 合法：`transitTable` `proto.go:400` | 成立 |
| 派发失败补偿已清 managed 工作树 | `Dispatch` defer `manager.go:897-908` → `compensateWorkspace` `1099-1106` 调 `RemoveManagedWorktree`；CreateTask 已发生、Start 失败则任务 `failed` 且 tmp 已建 | 成立（清树）；**不清 tmp**（要补）。措辞「同一条」过满，见 M5 |
| `--target` 与 reclaim 同形 | `cmd/root.go:58` 根命令持久 flag；`cmd/reclaim.go:40` `newTargetClient()` | 成立 |

Done/Stop/compensateWorkspace **当前零处删除 tmp**。这是本卡缝 1 的真实缺口，不是现状写错。

## 4. 定级独立验证

独立结论：**L3 轻档**。不接受本次 L3→L2 改判。不是 L1。

定级两问套到定稿范围：

| 问 | 本卡 | 答 |
|---|---|---|
| 跨几个子系统契约面？ | 缝 1：`Manager.Done`/`Stop`/`compensateWorkspace`（`d_orchestration`，调用方 `handleDone`/`handleStop`/`Dispatch` defer，均在 agentd 进程内）。缝 2：新 CLI 命令（`d_cli`）+ 新 agentd HTTP（`d_gateway`）+ 新预览/执行 wire（`d_protocol`）+ Manager 判定（`d_orchestration`），工作树复用 `d_workspace` 已有 `Reclaim`。`d_web` 不消费，作者写对了。 | 整卡跨顶层域，不是单子系统 |
| 动不动契约层？ | 作者：「虽会新增端点」。`--target linux-01` 是跨进程。即使 CLI 零逻辑、只转发，也按对接跨进程 wire 计。 | 动 |

L2 的法定前提是「单子系统、**不动契约**」。缺一条就不能 L2。

实操裁决不能改写两问：它是升格阀，不是「HTTP 通道免税」。作者真正成立的观察是「不值得为这条缝扇出并行子卡」——这是轻档，且 **轻档不许跳契约冻结**。

B286：不新增 HTTP、不改 202、CLI 消费已有账本事件 → L2。本卡新增端点，不能抄这个结论。B294 同日：新命令 + 新 HTTP + proto → L3 轻档。抄 B294。

不是 L1：收口点、两处布局、短号碰撞、预览/执行/`--force` 分工、与 reclaim 边界、404、批处理失败——plan/contract 写出来不会只复述三行；验收也不是一眼（linux-01 盘、脏树、非终态负例）。

若强行维持 L2：至少要把 I1–I3 全部写成实现决定级产品句，否则 plan 会发明删盘行为。即便如此，下一节点仍走错（该 contract 却去 plan），审查仍判修订后再批。

## 5. 缺陷族

通用五族逐族结论（族名 | 设问 | 结论）：

- **生命周期 / 状态机中断** | `--yes` 中途 agentd 崩溃？孤儿 tmp 谁收？ | 删除按叶子、存在才删，崩溃留下半批；重跑 `gc --yes` 应幂等收完。**无新的状态机半态**（不改任务状态，与 B77 纯资源动作一致）。Done 在 `handleDone` `server.go:1513-1515` 对已 `completed` 短路 200、不再进 `Manager.Done`——结束时删除失败**不会**因重发 done 而重试，gc 是唯一重试入口，与弃选「只做结束时删除」互证。补偿路径漏接则 Start 失败的 failed 任务要等 gc（M5）。进程重启本身不需要新收尾者。
- **静默失败 / 误导报错** | 报成功但没做？老 agentd？ | **有病，即 I1/I2。** Done/Stop 缓存失败只记日志是作者有意对齐工作树清理，可接受（任务已终态，残缓存变运维）。gc 是人盯着报告删 150G：空预览、404 译错、脏树 abort、`RemoveAll` 失败只进日志，都会造成「报清了还是 150G」。必须在契约语义里把人读报告与退出码钉成可行动。
- **跨平台假设** | 路径、占用、权限 | **无新的分隔符假设**，因为两处布局都经 `filepath.Join`（`tempdir.go:23`）。Windows 上 `RemoveAll` 碰到锁文件会失败——并入 I2「失败必须上报」，不要假设 Unix 一定能删。claude 的 AF_UNIX `perm.sock` 落在 TMPDIR（codex 注释 `adapter.go:121-128` 的预算就是这条）：非终态不删现役 tmp 已经挡住；若 I3 的「整棵 tmp 根」读法落地，跨平台的 socket 会一起被拆。
- **假红 / 假绿测试** | 锁的是调用方依赖还是内部帮手？ | 缝 1（Done/Stop/补偿后两处叶子消失、`render.log` 仍在、删除失败不阻断终态）锁的是归档/中止调用方依赖的磁盘事实，是真缝。缝 2 锁预览不动盘、`--yes` 后终态叶子消失、非终态仍在、脏树无 force 仍在，是真缝。假缝禁令（不把「算字节」抽成对外符号）合格。**缺负例**：老 agentd 404、批量一净一脏不中止、删除目标等于 `DataDir/tmp` 必须拒绝、短号非终态占用者（测试决定已点短号，保留）。没有这些，接缝可以在假注入下全绿、活路径仍错。
- **门禁绕过** | 新写路径过没过权限门？TOCTOU？ | 现网 `/api/*` 挂在 `s.auth(mux)`（`server.go:493,619,636-654`），Token 空 fail-closed。新 gc 路由必须落在**同一内层 `api` mux**，禁止另挂 `root` 绕过 Bearer/cookie。spec 没写——L3 契约语义补一句即可，不另开 finding。TOCTOU：`failed→running` 合法（`proto.go:400`）；spec「动手前重读快照」与 `Reclaim` `reclaim.go:249-250,263-264` 同一条，成立。预览与 `--yes` 是两次 CLI 进程，不能共享内存白名单；正确读法是**同一判定函数、执行当下重算**，并与「不得清当时判定为进行中的」合取（见 §6）。

追加设问：

- **序列化边界** | gc `--json` 与 reclaim「同形」 | reclaim 的 JSON 是 `ReclaimListResp`（无缓存字节）。gc 预览「必须打出字节量」，`--json` 必有新键。L3 契约冻语义（有哪些列：将释放字节、终态任务数、四态树、跳过原因），签名归 contract。接缝 2 现有「CLI 人读契约表驱动」不够穿过 JSON 边界——补一支 `--json` 含字节字段、缺席与零可分。
- **枚举新值过既有白名单** | 无新任务状态、无新事件类型。工作树四态沿用 reclaim。无。
- **承重安全属性** | 短号隔离：非终态占用者不得删现役叶子。测试决定已点，必须能变红（缝 1 + 缝 2 各一支）。删错 `DataDir/tmp` 根是安全属性，见 I3。

## 6. 二解测试（承重句）

| 句子 | 读法 A | 读法 B | 必须以正文消掉 |
|---|---|---|---|
| 「本卡虽会新增端点，但 CLI 与编排不会拆成可并行子卡」→ L2 | 新 HTTP 仍 L2（作者） | 新 HTTP = 动契约，不并行只说明轻档 | **C1**：L3 轻档 |
| 「协调者经 CLI（可 `--target`）打过去」打到老 agentd | 404 = 不存在 / 空预览 | 版本过旧，退 0，提示升级 | **I1** |
| 「内部走现有 `Reclaim`；脏树无 `--force` 跳过」 | `Reclaim` 一报错整批停 | skip 该行，其余继续 | **I2** |
| 「调用方改为清理命令的执行段」 | CLI 循环 `Client.Reclaim` | agentd 内循环 `m.Reclaim` | **I2**① |
| 「失败只记日志，不阻断」 | Done/Stop 与 gc `--yes` 同样吞 | 只限终态收口；gc 必须报告并按 I2④ 退码 | **I2**③④ |
| 「已经堆着的历史缓存一键清掉」vs「所有终态任务的两处缓存目录」 | `RemoveAll(DataDir/tmp)` 或扫孤儿 | 只 `RemoveAll` 任务表算出的叶子 | **I3** |
| 「预览与执行是同一条判定；执行不得清预览里没有列成将删除的东西」 | 两次 CLI 要共享上一份预览快照（计划文件） | 同一函数，`--yes` 当下重算；测试在状态不变时集合相等 | 写死：**当下重算**。两次调用之间新变成终态的，本轮可清；变成 running 的，本轮不清（已有「重读快照」句，保留） |
| 「`--yes` 才动盘」+「`--force` 只作用于脏树」 | `gc --force` 即执行 | `gc --force` 仍预览，只改脏树列为将强删 | **M3**，建议写死 |
| 「`--json` 与 reclaim 同形」 | 复用 `ReclaimListResp`（无字节） | 新 JSON，必含将释放字节 | 追加设问；人读已要求字节，JSON 不得少 |
| 「短号删除权 = 没有任何非终态占用者」用于 Done | 只看「另有一个」占用者 | 占用者集合含自己之外的全部非终态 | 写清：id8 的非终态集合非空则不删现役叶子（自己正在进入终态不算占用者） |

## 7. 看过但未成为 finding 的地方

- **四份生产 adapter 同一布局，无第三路径。** grok / claudecode 与 spec 点名的两份同键；fake 不写盘。M2 只补现状表。
- **`attach` 仍可读。** `render.log` 在 `tasks/<完整id>/`（仓内 skill / README），不在 tmp。Done 后删 tmp 不碰 attach 数据源。`frames.jsonl` / `proc.json` 同任务目录，spec 不动它们，与代码一致。
- **`waiting_review` 非终态、`continue` 仍可能跑测试。** `IsTerminal` 不含它；故事 3 成立。
- **`WorktreeManaged=false` 用户自建树。** `ReclaimList` `357-358` 已跳过；gc「当它们不存在」与现网一致，不会误伤 `b156-23-acc` 这类树。
- **B77 非目标未被撕开。** 不删任务目录、不删分支、不改状态、不回写 `worktree_managed`。新命令名 `gc` 而不是把缓存塞进 `reclaim`，与 B77 §2「不删任务侧文件」一致。
- **B160 不做设置页。** 一次性删盘不是持久配置。SECTIONS 五分区验收可机械看。roadmap 已收「开发机详情占用 + 同语义清理」。
- **OOS 四条已进 `docs/roadmap.md`。** spec 收尾自检第 6 条本卡完成（B283 审查 I4 那种漏账这里没有）。
- **Done 幂等短路。** 已 completed 再 done 不进清理（`server.go:1513-1515`）。历史 150G 不能靠重发 done 清，必须有 gc。不是缺陷，是缝 2 存在的理由。
- **`--target` / `--json` 旗的挂法。** 根命令已有 `--target`；reclaim 把 `--json` 标成仅列表。gc 预览用 `--json` 合理；与 `--yes` 组合的 JSON 属合同 I2/序列化，不另开 finding。
- **短号碰撞在 UUID 前 8 位上概率低**（任务 id `uuid.NewString()`），但代码与测试已经按「会撞」写（`tempdir.go:20-22`）。spec 处理碰撞是对的，不是过度设计。
- **`handoff gc` 名字未占用。** 全仓无 `gc` 子命令。
- **鉴权现状。** 新路由只要挂现有 `api` mux 即自动过 Token/cookie；契约语义一句即可，见 §5 门禁。
- **图覆盖债。** 与 spec 自述一致，不挡。本审查用 `best.json` 容器表核了 Manager/Server/CLI 归属，未跑 `flow`。
