# B229 拆解提案：纪律块分层重构 + 入账本 + 派发期下发

日期：2026-08-25 · 节点 `charter:breakdown` · L3 重档 · 有效基线 `claude/config-sync-workflow-arch-fd96b7`

上游：[spec](2026-08-24-b229-discipline-layers-and-store.md)（状态：已批准（用户，2026-08-24））
冻结物：[contract](b229-contract.md)（状态：已冻结，提交 97dcaf96；本轮只在头部补状态元数据、末尾追加拆解期修订记录，未触碰任何冻结正文）

本稿由 handoff executor 出稿，协调者拍板；不写实现代码、不建卡、不派发。

## 0. 待拍板岔口清单（裁决前不得视为决定）

| 编号 | 岔口 | 方案 | 出稿者建议（仍待拍板） |
|---|---|---|---|
| P1 | 用户故事 3「临时捏一份直接下发」的用户入口本期做不做 | (a) CLI 裸派发加 `--discipline-file <path>`（读文件作 RawText 走缝 1，不落库；wire 与库能力已被 Ticket 0 备好，CLI flag 不属契约冻结物）；(b) 本期不做入口，RawText 仅作为库能力留存，story 3 延后另卡 | **(a)**。spec 已批准的 story 3 若无载体即静默落空；(a) 只加一个读文件的 flag，无新 wire、无新接缝。若选 (b)，须在 roadmap 记一笔，不许无声丢弃 |
| P2 | 存量 7 份导入 v1 的执行载体 | (a) 协调者在真机用 `handoff discipline put` 七连（charter-implement 先拼好台账句暂存为临时文件再 put），台账逐笔留痕；(b) 仓内随 T6 提交一次性导入命令/脚本 | **(a)**。七份一次性数据操作不值得留长期命令面；账本版本行本身就是审计记录。选 (a) 则 T6 依赖 T4 |
| P3 | charter-implement v1 补的「台账句」措辞 | (a) 逐字复用平台不变量第 3 条原文（platform.go:14 第 3 条「每确立一个事实就往台账文件追加一行……」）——机制上这句正是从①层搬下来的；(b) 另写一句更贴合 implement 角色的措辞 | **(a)**。内容侧修复的本义就是「这句话本来就该在角色层」，搬家而非重写；(b) 的措辞权在拍板者，若改须同步考虑 review 之外三个产出型角色是否也要（spec 实现决定 3 只点名 charter-implement，本稿不扩围） |
| P4 | **契约退役清单缺口**：GET/PUT `/api/discipline/file` 与 `discipline.Write` 契约 §2.7 未列 | (a) 随目录退役一并拒服务：两端点返回可行动错误（「纪律块已入账本，请用 handoff discipline put」），路由与 TS 类型保留不动 UI，界面改造归后续卡（沿 §2.7「以此为模板新建失去数据源」同款先例）；(b) 端点原样保留继续读写死目录 | **(a)**。(b) 会造出「控制台编辑成功但永不生效」的静默失败 + 把刚埋掉的漂移载体目录重新变成写入面——缺陷二换马甲复活。此缺口是本轮核对的新发现，已按纪律回写契约修订记录（§8） |
| P5 | T1（执行机切换）与 T3（派发侧接线）并行还是合并 | (a) 两卡并行：编译互不依赖、文件集不相交，分支内先后无运行时风险（混版窗口只在发布时存在，全网同批升级前置收敛它）；(b) 合一卡单上下文顺序做 | **(a)**。两侧各自都超过流程固定成本（重档定级依据），合并会拖长单卡周期。T7 收口回归兜底整绿 |

## 1. 触及子系统清单与派卡资格核

子系统 id 与类型以 `codegraph/best.json` `domains` 中 `parent` 为空的顶层领域为准（本轮实读）。包→域归属：internal/discipline + internal/config→d_policy、internal/agentd→d_gateway、internal/ledger + internal/ledgerstep→d_ledger、cmd→d_cli、internal/proto→d_protocol、internal/client→d_transport、web→d_web。

| 子系统 | 类型 | 触及内容 | 有界文件集 |
|---|---|---|---|
| d_policy 运行策略与配置 | logic | 缝 1 骨架已冻结；实现票删 Resolver/builtin 六份/Tier 轴/执行机调用面；config 开关退休 Warn | `internal/discipline/{resolver.go,discipline.go,files.go,builtin/*.md}`、`internal/config/config.go` |
| d_gateway 控制门面 | boundary | 执行机收文即用+落盘+续接改造、能力位上报、下拉来源切换、cardstep 装配绑缝 1、file 端点处置（P4）；真实跨机行为机内验不了 | `internal/agentd/{manager.go,server.go,cardstep.go,ledgerapi.go,discipline.go,resume_test.go 等}` |
| d_ledger 卡片账本 | logic | 缝 2 骨架已冻结；ViaTemplate 消费数据字段并记快照版本（生产 import 面零新增） | `internal/ledgerstep/dispatch.go`、`internal/ledger/events.go`（骨架已含字段） |
| d_cli 协调者命令面 | logic | 裸派发绑缝 1+探活、discipline 命令族、Ledger.Enabled 三消费点删除 | `cmd/{dispatch.go,discipline.go 新,agentd.go,ledgercli.go,status.go}` 及测试 |
| d_protocol 协议契约 | logic | wire 类型四处投影链骨架已冻结；本轮无进一步改动预期 | `internal/proto/*`（仅当 fixture 需刷新） |
| d_transport 跨机连接 | boundary | client 两键骨架已冻结；Status() 复用；真实对端行为归真机 | `internal/client/client.go`（预期零改动） |
| d_web Web 控制台 | logic（验收边界型） | TS 镜像骨架已冻结；typecheck/vitest 本环境跑不了 → 验收归协调者 | `web/src/api/types.ts`（预期零改动）、vitest 断言 |

**派卡资格四条逐项核**：

| 子系统 | 1 有界文件集 | 2 契约面可枚举 | 3 DAG 无环 | 4 类型标注 | 结论 |
|---|---|---|---|---|---|
| d_policy | 上表列出，删除范围即契约 §2.7 清单 | 缝 1 签名 + 退役清单 + 开关退休语义，全冻结 | T1 删除不反向依赖任何消费方改造 | logic | 通过 |
| d_gateway | manager/server/cardstep/ledgerapi/discipline 五族 | §2.5 四条行为契约 + §2.4 上报条件 + §2.7 处置表 | T1→T2 单向；对 T3 无反向依赖 | boundary（跨机现实机内只验形状） | 通过 |
| d_ledger | dispatch.go 一个生产文件 | 数据字段消费 + 快照版本键，§2.3 冻结 | 只依赖 Ticket 0 根 | logic | 通过 |
| d_cli | 五个 cmd 文件 | §2.8 命令族形态 + §2.2 绑定 + §2.6 三消费点 | T4→T6 单向；T3/T5 与 T1 并行无环 | logic | 通过 |
| d_protocol/d_transport/d_web | 骨架已闭合 | §2.3/§2.4/§2.9 全冻结 | 仅作 T7 回归对象，不派实现卡 | logic / boundary / logic | 通过（无新卡资格问题） |

## 2. 契约增量核对

上游状态位核对结果：spec 头部有「已批准」✓；契约头部**缺字面「已冻结」状态行**（冻结事实只在 git 提交说明里）——已补状态元数据行并记入修订记录，属流程状态回写，不动冻结物。

| contract 冻结物 | 子卡如何使用 | 越界结论 |
|---|---|---|
| §2.1 disciplines 聚合三 API + schema + 校验 | Ticket 0 已落地并通过本轮复测；T4 CLI 族与 T6 导入只做消费方 | 不越界，不改签名 |
| §2.2 ResolveDispatch 签名/错误语义表/DisciplineRef | T3 两处调用方绑定；无 executor 参数的刻意缺席被保持 | 不越界 |
| §2.3 wire 八处加法字段 | Ticket 0 已落；T1/T3 只消费不新增 | 不越界 |
| §2.4 能力位四处投影链 + 同生同死上报条件 | Ticket 0 已投影；T2 按 §2.4 四件事前提置 true | 不越界 |
| §2.5 执行机四条行为契约 | T1 的全部验收判据来源 | 不越界 |
| §2.6 Ledger.Enabled 退休（键留值忽略 Warn） | T5 逐字执行 | 不越界 |
| §2.7 退役与迁移清单 | T1 拆除范围；**发现缺口：GET/PUT file 端点与 discipline.Write 未列** → P4 待拍板 + 修订记录 | 缺口处置待拍板，不自行扩权 |
| §2.8 CLI discipline 命令族 | T4；flag 表归 plan | 不越界 |
| §3.1–§3.4 语义细则 | §3.1 拒发覆盖一切带正文派发 → T3 各调用方判据；§3.2 哨兵存活 → 反向断言已在 Ticket 0 锁住（dispatch_test.go:111）且 T1 加防降级复活断言；§3.3 版本语义 → T3 快照判据；§3.4 正文不进日志 → T3 日志断言 | 不越界 |
| §4.1–§4.5 拍板记录 | T1/T2/T3 设计约束来源（尤其 §4.3 continue/resume 消费落盘正文不经缝 1 远程解析） | 不越界 |
| §5 测试接缝表 | 直通竖切已随骨架落地（discipline_passthrough_test.go 本轮实测 PASS）；快照回归两条判据未见测试 → 列入 T3 | 不越界 |
| §6 目标图（零新增方向、两处 entries 追加） | 所有子卡生产 import 面零新增方向；T7 复跑 graph check 双读数 | 不越界 |

**本轮边界澄清（已回写契约 §8 修订记录，均不需退回 contract 改签名）**：

1. 下拉来源切换（契约欠账 4 的后半）从「与 CLI 命令族同票」调整为随 T1 原子落地——`discipline.Dir/List` 一删 ledgerapi.go:658 即编译失败，两者必须同票；CLI 命令族独立成 T4。
2. RawText 的用户入口不在契约冻结物内（契约只冻了库能力与 wire 字段）；story 3 载体是范围决策 → P1 待拍板。
3. GET/PUT `/api/discipline/file` 与 `discipline.Write` 是 §2.7 退役清单的遗漏项 → P4 待拍板。
4. T2 上报 true 的四件事前提已在 §2.4 冻结，本轮确认无需修订；DAG 排序（T1→T2）是其流程保障，非运行时断言。

## 3. 子卡清单与依赖 DAG

根：Ticket 0 骨架（已随 97dcaf96 冻结，本轮五包测试实测 ok，直通竖切 PASS）。

```text
Ticket 0（缝1+缝2+wire 骨架，已冻结）
   │
   ├──────────────┬──────────────┬──────────────┐
   ▼              ▼              ▼              ▼
  T1             T3             T4             T5
 执行机切换     派发侧接线      CLI命令族      Ledger开关退休
 (d_gateway     (d_cli+        (d_cli)        (d_policy+d_cli)
  +d_policy删)   d_gateway)       │
   │                             ▼
   ▼                            T6 存量导入v1（数据操作；P2/P3 裁决后）
  T2 能力位上报                    │
   ├──────────────┴───────────────┘
   ▼
  T7 收口回归 + CHANGELOG（真机清单交协调者）
```

并行组：{T1, T3, T4, T5} 文件集互不相交可并行（P5 裁决）；T2 严格在 T1 后；T6 在 T4 后；T7 收全部。

### T1 · 执行机切换与本地解析退役（d_gateway 主 + d_policy 删除）

**①契约引用**：contract §2.5 全部四条、§2.7 退役清单、§4.3（continue/resume 消费首派落盘正文）、§1.2（哨兵存续条件）。

**②意图与为什么**：执行机从此不再拥有「猜一份正文」的任何能力——收文即用消灭缺陷二的分叉点，删六条回退分支消灭缺陷三的降级通道。continue/resume 读首派落盘正文而不是重新解析，是因为首回合之后协调者侧的「最新版」可能已经变了，续接必须看到与会话开始时同一份世界。

**③验收（行为化）**：
- 新增 agentd 测试：向 `Manager.Dispatch` 注入 `DisciplineText="X"` + 点名 → executor adapter fake 捕获到的注入正文逐字节等于 X，且本机 `<DataDir>/discipline/` 为空/不存在时照样成功；`go test ./internal/agentd -run 'TestDispatchConsumesDeliveredText' -count=1` 命中且 ok。
- 反向断言（防降级复活）：`DisciplineText` 为空而 `req.Discipline` 点名非空 → Dispatch 返回错误拒派（不是悄悄退回本地盘或内置）；磁盘上有同名文件也必须拒——这条在「有人加回兜底分支」时变红。
- 显式空正文 + 未点名 → 不注入任何块（§2.5 第 3 条，PlatformInvariants:false 机器合法形态），有断言。
- continue：任务目录有首派落盘正文 → 续接注入同一份（fake 断言逐字节）；缺落盘正文 → Cold 续接拒绝、热重连 Error 不阻断（沿用 manager.go:1272/:3377 既有不对称，各一条断言）。
- `Task.DisciplineVersion` 随任务元数据落盘并有读回断言；正文落盘文件名归 plan，但「先落盘后启动 executor」的顺序要有测试锁（落盘失败 → 不启动，可断言）。
- 拆除完成判据：`grep -rn "NewResolver\|builtinFor\|builtinByName\|DefaultTierFor\|TierImplement\|discipline.Dir\|discipline.List\|discipline.Read\|discipline.Write" --include="*.go" internal/ cmd/`（生产代码）零命中；`internal/discipline/builtin/` 目录不存在；`resolver.go` 删除。
- `/api/discipline` GET 返回 `Builtins==[] && Files==[]` 且 HTTP 200（类型不破）；ledgerapi.go:658 下拉改 `ListDisciplineNames`，有种子数据的 agentd 测试断言下拉含账本名、不含磁盘名。
- P4 裁决为 (a) 时：GET/PUT `/api/discipline/file` 各一条断言——返回可行动错误原文含「handoff discipline put」，且 PUT 后磁盘目录无新文件出现。
- `go build ./... && go test ./internal/agentd ./internal/discipline ./internal/config -count=1` 全 ok。

**④入口指针（有界文件集）**：`internal/agentd/manager.go#resolveDisciplineFor`（删）、manager.go:270 构造点、Dispatch/continue/resume 三调用点(:760,:1271,:3380)、`internal/agentd/discipline.go`（file 端点处置）、`internal/agentd/ledgerapi.go:658`、`internal/agentd/server.go`（dispatchRequest 已备）、`internal/discipline/resolver.go`（删）、`internal/discipline/builtin/`（删）、`internal/discipline/files.go`（Dir/List/Read/Write 执行机调用面删）、`internal/discipline/discipline.go`（embed/Tier 轴删）、`proto/discipline.go`（字段恒空数组）、测试：manager_test/resolver_test(删)/files_test(缩)/resume_test:376(清理)/新增收文即用测试。

### T2 · 能力位上报置 true——同生同死（d_gateway）

**①契约引用**：contract §2.4 上报条件（四件事齐才报 true）、§2.2 ErrUnsupportedTarget 语义、§4.1 三态方向。

**②意图与为什么**：能力位是拒发闸的对端半边。先报 true 后补实现 = 老 agentd 缺陷三的镜像事故（协调者信了能力位，正文下发到一台不会用的机器）。所以上报点必须与 T1 四件事同批落地后才翻开。

**③验收（行为化）**：
- agentd status handler 组装处（今 server.go:683 LaunchersSupported 同位）置 `resp.DisciplinesSupported=&true`；扩展既有 status handler 测试断言响应 JSON 含 `"disciplines_supported":true`；`go test ./internal/agentd -run 'TestStatus' -count=1` ok。
- 代码注释逐条列出 §2.4 四件事核对单（收文即用/落盘/continue/resume 消费），review 对照 T1 提交核销。
- 流程属性声明：T1→T2 的先后靠 DAG 与 review 保证，没有运行时断言能锁「上报不早于实现」——这是本卡的已知残余风险，记入真机清单第 8 条（升级批次核对）。

**④入口指针**：`internal/agentd/server.go`（status handler 组装处 :680 附近）、status handler 测试文件。

### T3 · 派发侧缝 1 接线与快照版本（d_cli + d_gateway + d_ledger 消费）

**①契约引用**：contract §2.2（绑定方式与闭包适配）、§2.3（数据字段消费 + 快照键）、§3.1（拒发闸含未点名）、§3.3（版本语义）、§3.4（正文不进日志）、§6（entries 已声明，零新增 import）。

**②意图与为什么**：「正文解析收口在协调者侧且只有一处」。两个调用方家族（裸派发的 CLI 进程、环节派发的 cardstep 装配链）各自三行闭包绑 lookup + 探一次 Status，ViaTemplate 只消费 Dispatcher 上的数据字段——ledgerstep 生产代码零新增 import，图闸不破。每次派发（含未点名裸派发）都过拒发闸，混版舰队丢的是①层恒在内容，不能省。

**③验收（行为化）**：
- ViaTemplate 测试（ledgerstep 包内）：Dispatcher 数据字段携带已解析三元组时，Transport 收到的 `DispatchOpts.DisciplineText/DisciplineVersion` 到位，且 RecordDispatch 后事件 payload 含 `discipline_version`（events 回读断言）；未点名模板 → DispatchOpts 仍携带纯平台层文本（§3.1），version=0。
- cardstep 装配链测试：startCardStep → stepTransport 发出的 client.DispatchOpts 含 text/version；目标机能力位 nil/false → 拒发错误上抛且 `errors.Is(err, discipline.ErrUnsupportedTarget)` 可辨，HTTP 层映射为可行动文案；不发任务。
- cmd 裸派发测试（httptest 假目标机）：body 含 `discipline_text`（组装产物含「平台不变量」标记）与 `discipline_version`；探活返回不支持 → 拒发、目标机请求计数为 0；P1 裁决 (a) 时加 `--discipline-file` 用例（RawText 直通、version=0、文件不存在给可行动错误）。
- §3.4 断言：派发日志含计量字段不含正文原文（捕获日志断言无正文片段）。
- 快照回归（契约 §5 两判据，现无测试）：RecordDispatch payload 含键；构造无该键的老事件 JSON 反序列化得 0 不报错。
- `go test ./internal/ledgerstep ./internal/agentd ./cmd -count=1` ok。

**④入口指针**：`internal/ledgerstep/dispatch.go#Dispatcher.ViaTemplate`（:120 起、快照组装 :277/:289）、`internal/agentd/cardstep.go#Server.stepTransport`(:102) 与 startCardStep 装配、`cmd/dispatch.go`（runDispatch 装配 + P1 flag）、`cmd/root.go#newTargetClientNamed`（复用）、`cmd/ledgercli.go#openLedger`（lookup 闭包源）、各自 _test。

### T4 · CLI discipline 命令族（d_cli）

**①契约引用**：contract §2.8（put/get/list、template 命令族形态、flag 表归 plan）、§2.1（缝 2 API）。

**②意图与为什么**：给权威副本一个人手可及的读写口——regen 改造（charter 仓，另行）、存量导入（T6）、排障 diff 都从这族命令走，不逼人连库敲 SQL。

**③验收（行为化）**：
- `go test ./cmd -run 'TestDiscipline(Put|Get|List)' -count=1` 命中且 ok（穿真实 SQLite 临时库）。
- put 后 get 打印正文与版本号；get --version N 取历史版；list 升序去重。
- put 空 body / 超 64KiB / 含路径分隔符名字 → 各自可行动错误退出非零，穿真实校验层不是 CLI 复制一份规则。
- 未配 ledger.dsn 时走 openLedger 本机回退照常可用。
- P2 选 (a) 时：七连 put 的操作序列在 T6 台账留原始输出。

**④入口指针**：`cmd/discipline.go`（新建）、`cmd/template.go:83`（必填校验风格参照）、`cmd/ledgercli.go#openLedger`、`cmd/discipline_test.go`（新建）。

### T5 · LedgerConfig.Enabled 退休（d_policy + d_cli）

**①契约引用**：contract §2.6 全文、§1.1 第二条（严格解析约束）。

**②意图与为什么**：关账本等于没纪律块，这个开关的存在本身就是第三种失败面的种子。退休让「配了却无效」可见（Warn）而不致命（严格解析不炸存量 config）。

**③验收（行为化）**：
- 含 `ledger: {enabled: false}` 的存量 yaml 加载成功，且日志恰一条「ledger.enabled 已退休」Warn（config 测试断言条数）。
- cmd/agentd.go:248、cmd/ledgercli.go:30、cmd/status.go:86 三处分支删除后：enabled=false 的 config 下 agentd 启动账本路径可用、ledger cli 正常开库、status 报账本健康——各一条测试或既有测试翻绿证明。
- `grep -rn "Ledger.Enabled" --include="*.go" cmd/ internal/ | grep -v _test` 生产代码零命中；struct 字段与 yaml 键保留（config.go:166 注释更新为退休说明）。
- `go build ./... && go test ./internal/config ./cmd -count=1` ok。

**④入口指针**：`internal/config/config.go#Load`(:349，Warn 落点)、`internal/config/config.go#LedgerConfig`(:161 注释)、cmd 三处消费点及其测试。

### T6 · 存量 7 份导入 v1 + 台账句（数据操作，边界型）

**①契约引用**：contract §2.7 行 6、§3.3；spec 实现决定 3；P2/P3 裁决。

**②意图与为什么**：权威副本从磁盘搬到账本的第一批住户。以本机副本为准因为它是三台里最新的；charter-implement 补台账句是缺陷一的内容侧修复——机制降层不会自动补出源头没有的话。

**③验收（行为化）**：
- 七个名字（charter-plan/implement/contract/review/breakdown/recon/integrate）`handoff discipline get <name>` 各得 v1，正文与本机 `~/.handoff/discipline/<name>.md` 逐字节一致，charter-implement 多出台账句一行（P3 裁决措辞）。
- `handoff discipline list` 含七名。
- 操作全程命令与原始输出记入执行台账；重复执行产生的多余版本如实记录不掩盖（不可变版本化的既成性质，接受多版本无害）。
- 本卡不验证 regen 新链路（charter 仓改造另行）——端到端列真机清单第 3 条。

**④入口指针**：输入 `~/.handoff/discipline/*.md`（真机现实，机内不可预设其内容）；载体待 P2；产出证据落 `docs/superpowers/ledgers/`。

### T7 · 收口回归与变更说明（全子系统横切）

**①契约引用**：contract §5 全表、§6 图执法口径、§2.9；本稿真机清单。

**②意图与为什么**：把并行卡的局部绿收敛成整绿；CHANGELOG 记录对外行为变化（内置角色块消失、裸派发注入平台层、老目标机拒发、ledger.enabled 退休、控制台纪律文件编辑面处置）。

**③验收（行为化）**：依序实际执行并把原始输出记台账：
`go build ./... && go vet ./...` → 退出 0；`gofmt -l .` → 无输出；`go test ./... -count=1` → 全 ok；`git diff --check` → 无输出；`handoff graph check`（无视图与 `--view cards-B229-charter` 两种读数）→ `fails: []`；CHANGELOG `[Unreleased]` 含上述五条行为变化；web 侧 tsc/vitest 在 node_modules 就绪的环境跑，否则记「未验证，需真机」（B185/B229 契约同款环境限制，不假装跑过）。

**④入口指针**：`CHANGELOG.md`；各卡测试入口；graph 读数命令如上。

## 4. 缺陷族对抗审查总表

| 族 | 正面结论（对应子卡验收栏） |
|---|---|
| 生命周期/状态机中断 | T1 落盘先于 executor 启动有测试锁，窗口内崩溃=任务缺正文→Cold 拒绝续接可诊断（沿用既有不对称）；无新资源类型故无新孤儿。T3 拒发发生在任何任务创建之前，无半状态。T6 重复导入堆版本行无害但须台账留痕。 |
| 静默失败/误导报错 | 拒发闸三条错误路径（能力位 nil/false、未知名、参数错）全部可辨错误非降级（Ticket 0 已锁 + T3 穿线）；T1 反向断言钉死「点名但没收到正文=拒绝」防降级复活；T2 上报与实现同生同死防「报了能力却不接」；P4(b) 方案被否正是因为它是编辑「成功」却永不生效的静默失败通道。 |
| 跨平台假设 | 名字校验双查 filepath.Separator 与 '/'（Windows 反斜杠被挡，骨架已有）；正文落盘沿用任务目录既有 UTF-8 文件惯例无新假设；linux-01 AppleDouble 残留随目录退役失读，清理确任列真机第 5 条；三台机器三种版本的舰队现实只能真机验（真机第 1/2/8 条）。 |
| 假红/假绿测试 | 反面断言齐备：未知名拒发（哨兵存活）、能力位 nil/false 必拒、「点名无正文必拒」、「--plan 类本地文件不上 wire」（P1 若做 flag 则对应负例）；直通竖切穿真实 SQLite 不是 mock store（Ticket 0 实测 PASS）；夹具行为假设均有真机项对应（老事件反序列化、混版拒发）。测试锁的是调用方依赖的行为（收文即用、取最新版、拒发），不是内部帮手——换落盘文件名或闭包实现不该红。 |
| 门禁绕过 | /api/tasks 与 /api/discipline 既有 Bearer/forwardIfRequested 门不变；正文经同一门不走旁路；mapping PUT 不动（§2.7）；TOCTOU：Status 探活与派发之间目标机可被降级/替换——窗口固有，由「全网同批升级前置」收敛概率、拒发闸保证触发时可诊断，残余窗口在此显式接受并记录（真机第 8 条核对批次）。 |
| 序列化边界（追加设问） | 手写投影清单：client.go body map 两键、server.go decode→DispatchReq、manager 落盘读写、ViaTemplate→DispatchSnapshot、TS 三镜像。fixture 逐字节（Ticket 0 已刷新）+ T3 快照回归两条判据（含老事件缺键得 0）穿过真实 JSON 边界；字段均为 primitive，roundtrip 属性测试的增益不抵成本，以 fixture+缺失/零值分辨断言替代——此取舍显式声明。 |
| 枚举新值过白名单（追加设问） | 无新枚举值：无新状态名/kind/事件类型（Ev* 未动）；ResolvedDiscipline.Source 是展示字符串非枚举。唯一新 wire 键 discipline_version 流经的每一处（Task/StatusResp/Machine/TS/fixture）已在 Ticket 0 四处投影链同落，漏一处则 T7 的 graph entity 查询与 vitest 红。 |
| 承重安全属性有测试锁住（追加设问） | 「未知名必拒」（哨兵存续，dispatch_test.go:111 可变红）、「nil/false 必拒」（TriState 表）、「点名无正文必拒」（T1 新增）、「正文不进日志」（T3 新增）各有能变红的测试；「执行机不连库」是无运行时断言的架构性质，由 graph check 依赖方向闸长期看护。 |

## 5. 真机清单（未验证，需真机；归协调者执行）

1. 全网同批升级前置的实际执行与批次核对：三台机器（本机/mac-02/linux-01）换到含 B229 的版本后再开新派发；升级编排本身不属本卡。
2. 混版拒发实测：向一台未升级目标机派发 → 得 ErrUnsupportedTarget 同因的可行动拒发文案，绝不静默降级（缺陷三的验收性反演）。
3. 端到端回放：charter-implement 派发一轮 → dispatched 事件 payload 含 discipline_name=charter-implement 且 discipline_version=1；（另行）charter 仓 regen 改造后新版本入库、下一次派发取最新版。
4. continue/resume 真机各一次：续接回合用的是首派落盘正文（timeline/注入观察），Cold 缺失场景拒绝文案可诊断。
5. linux-01：`<DataDir>/discipline/` 不再被读取；两个 AppleDouble 残留不再出现在任何清单/日志中，物理删除确认无害。
6. web 侧 tsc/vitest（node_modules 就绪环境）；控制台纪律页面对空 Builtins/Files 与 file 端点新错误文案的表现（P4 处置后的 UI 退化是否符合预期）。
7. PlatformInvariants:false 机器裸派发一次：无任何正文注入、任务正常创建（§2.5 第 3 条的真机形态）。
8. 升级批次核对：确认没有任何一台机器在 T1 合入前收到「上报 true」的中间构建（T2 的流程属性只能人工核对）。

## 6. 交稿自检

- [x] 四样齐全：§1 子系统带类型+派卡资格四条、§2 契约逐条核对含状态位结论、§3 七张子卡全部四段式且判据行为化（「跑 X 命令命中 Y」形态）、§4 逐族有答案（含「无，因为……」形态的枚举族）。
- [x] 待拍板岔口集中 §0（P1–P5），正文建议未伪装成裁决。
- [x] 真机清单 §5 汇总全部「未验证，需真机」项。
- [x] 每张子卡有界文件集核过（T1 最大但每一项都来自契约 §2.7 清单或本轮 grep 实测的现存调用点，无需插竖切还债卡；d_gateway 单包大是存量形态，本次触及五个具名文件族）。
- [x] 契约修订记录已回写（头部状态行 + §8 四条澄清）；未触碰任何冻结正文。
- [x] 符号锚自检：`handoff graph resolve --doc docs/superpowers/specs/b229-breakdown.md` 结果见台账（codegraph 二进制本机不可用，用只读 handoff graph resolve 替代）。
- [x] 红线自查：未写实现代码、未建卡派发、未调 handoff 写操作、未起 executor；台账边干边追加。

协调者裁决 §0 后即可扇出；P4/P5 的裁决影响 T1 卡面与并行方式，宜先裁。
