# 契约增量：纪律块分层重构 + 入账本 + 派发期下发（B229）

日期 2026-08-25 · 前置 [spec](2026-08-24-b229-discipline-layers-and-store.md)（已批准，L3 重档） · 节点 `charter:contract` · 有效基线 `claude/config-sync-workflow-arch-fd96b7`

**冻结物**：本文档 + `codegraph/target.json` 的契约条目与预算 + Ticket 0 骨架提交（含本分支视图 diff）。

每个签名都对着现状代码查证过，出处以 `文件:行` 标注（本轮工作树实读，台账逐条留痕）。

---

## 1. 现状事实（查证结果）

| 事实 | 出处 | 对本次的意义 |
|---|---|---|
| 平台层正文是二进制内常量，`Compose(base, platformEnabled)` 是唯一组装函数 | [platform.go:10-23](internal/discipline/platform.go:10) | ①层留在二进制的落点；缝 1 复用它，不另写组装 |
| `Resolver.For` 三档（配置读盘 / 显式空串关闭 / 内置默认）、`ByName` 三档（磁盘 → 内置同名 → 报错） | [resolver.go:87,141](internal/discipline/resolver.go:141) | spec「六条回退分支」逐一命中；全数退役 |
| `maxBlockSize = 64KiB` 是纪律块大小上限 | [resolver.go:23](internal/discipline/resolver.go:23) | 既成行为也是契约：入库正文沿用同一上限 |
| 六份内置块经 `go:embed builtin/*.md` 进二进制；`defaultTier` 仅在 `NameImplement` 分支生效 | [discipline.go:14-36,95-103](internal/discipline/discipline.go:95) | ③层「执行器能力轴」不是独立一层——spec 弃选判据属实 |
| `ledgerstep` 包注释明写「不解析纪律块——只把角色名传下去」 | [dispatch.go:8](internal/ledgerstep/dispatch.go:8) | 本轮唯一被显式撤销的既有契约声明 |
| wire 纪律链：`DispatchOpts.Discipline`(名) → `stepTransport` → `client.DispatchOpts.Discipline` → POST `/api/tasks` `"discipline"` → 目标机 `dispatchRequest.Discipline` → 目标机 `resolveDisciplineFor` 读**目标机自己的盘** | [dispatch.go:27](internal/ledgerstep/dispatch.go:27) → [cardstep.go:102](internal/agentd/cardstep.go:102) → [client.go:727,757](internal/client/client.go:757) → [server.go:1096](internal/agentd/server.go:1096) → [manager.go:754](internal/agentd/manager.go:754) | 缺陷二的机制现场；反转点在 `client.DispatchOpts` 与 `/api/tasks` 请求体 |
| `resolveDisciplineFor` 三调用点 :754（首派）/ :1265（continue）/ :3374（resume），注释自认「三个调用点必须用同一套判定」 | [manager.go:351-390](internal/agentd/manager.go:359) | 「唯一裁决点」要搬走的目标；continue/resume 的处置差异见 §2.5 |
| 点名任务续接解析失败=拒绝续接（Cold 路径）；启动恢复热重连点名失败只 Error 不阻断（Cold=false，约束仍在原会话上下文） | [manager.go:1272-1283,3377-3384](internal/agentd/manager.go:1272) | §2.5 执行机侧「缺正文即拒绝」沿用这条不对称 |
| `workflows` 与 `dispatch_templates` 主键 `(name, version)`，PG CREATE 在 :234/:237、SQLite 在 :296/:299 | [store.go:234,237,296,299](internal/ledger/store.go:234) | 缝 2 schema 与 spec 引用逐字核对无误 |
| `PutTemplate(name, def) (int, error)` 只插新版；`GetTemplate(name, version)` 0=最新（`ORDER BY version DESC LIMIT 1`）；`ListTemplateNames()` | [templates.go:66,88,126](internal/ledger/templates.go:66) | 缝 2 的同构原型，Ticket 0 逐行照抄其接线 |
| `DispatchSnapshot` 十字段无版本号；`:113` 注释承认指纹被主动放弃 | [events.go:109-127](internal/ledger/events.go:115) | 加一个字段即恢复可回放性；老事件不回填 |
| `LedgerConfig.Enabled` 默认 false；消费点三处：agentd 启动、CLI openLedger、status 健康 | [config.go:159-168](internal/config/config.go:162)、[agentd.go:248](cmd/agentd.go:248)、[ledgercli.go:30](cmd/ledgercli.go:30)、[status.go:86](cmd/status.go:86) | §2.6 开关退休的完整爆炸半径 |
| `client.Status(ctx) (*proto.StatusResp, error)` 已存在 | [client.go:492](internal/client/client.go:492) | 派发前探能力位无需新增传输方法 |
| 能力位四处投影链先例：Go StatusResp → Go Machine → TS StatusResp → TS Machine | [status.go:218](internal/proto/status.go:218)、[projects.go:160](internal/proto/projects.go:160)、[types.ts:243](web/src/api/types.ts:243)、[types.ts:385](web/src/api/types.ts:385) | 新能力位必须四处同落，漏一处投影链断 |
| `Task.DisciplineName` 落盘理由：「不落盘的话一次 continue 或一次 agentd 重启就会让点名的任务静默退回兜底块」 | [proto.go:243-249](internal/proto/proto.go:243) | 版本号同理由落盘；正文随任务目录落盘同理 |
| 卡节点角色下拉 = 五个 `Name*` 常量 + `discipline.List(dir)` 磁盘文件 | [ledgerapi.go:650-665](internal/agentd/ledgerapi.go:658) | 目录退役后清单改由账本供（§2.7） |
| `charter-must-override` 全仓零代码引用；消费方式是 ByName 未知名报错 | [resolver.go:177](internal/discipline/resolver.go:177) | 对侧常量查执法：活语义非漂移，由缝 1 未知名拒发承接存续 |

### 1.1 依赖库既成行为查证

- **`encoding/json` 宽松解码忽略未知字段**：缺陷三的机制本体（旧 agentd 收到 `discipline_name` 静默忽略）。新字段全部走同一风险面，故拒发闸不是可选优化而是协议正确性的一部分（§3.1）。
- **config 以严格解析加载**（launcher 契约 §2.7 记录过 `KnownFields(true)` 的坑）：从 struct 删 `Ledger.Enabled` 键会让存量 config.yaml 启动失败——§2.6 退休方案据此保留键、忽略值。
- **事件流 append-only 不回填**（[events.go:113-114](internal/ledger/events.go:113) 注释原文）：`DisciplineVersion` 只对新增派发生效，老事件读不到该键。

### 1.2 对侧常量查执法

- **`PurposeReview` / `EvDispatched`**：由 dispatch.go:162,266 产出、events.go:417,428 消费，两端活。本次只在快照上加版本键，不动它们。
- **`charter-must-override`**：数据侧哨兵（工作流模板 v5 正文里的字符串，仓内零代码引用）。它的消费机制是「未知名 → 报错」。重构后该语义由缝 1 承接（§3.2）——**哨兵存活的前提是未知名仍然拒发**，任何「查不到就兜底」的实现都会让它失效。
- **疑似漂移零处**：本轮触及的对侧常量（三态能力位、模板 `(name,version)` 主键、`Ev*` 事件名）均有双向使用。

### 1.3 符号锚（供 `codegraph resolve` 决议；行号会漂，符号不会）

`internal/agentd/manager.go#resolveDisciplineFor` · `internal/discipline/platform.go#Compose` · `internal/ledgerstep/dispatch.go#ViaTemplate` · `internal/ledger/templates.go#PutTemplate` · `internal/ledger/templates.go#GetTemplate` · `internal/ledger/events.go#RecordDispatch` · `internal/ledger/events.go#DispatchSnapshot` · `internal/client/client.go#Dispatch` · `internal/client/client.go#Status` · `internal/agentd/cardstep.go#startCardStep` · `internal/config/config.go#LedgerConfig` · `internal/proto/proto.go#DisciplineName`

---

## 2. 契约增量

### 2.1 缝 2：`disciplines` 聚合（`internal/ledger` 公开面）

Schema 两方言同构，照抄 `dispatch_templates`：

```sql
CREATE TABLE IF NOT EXISTS disciplines (
    name TEXT NOT NULL, version INT NOT NULL, body TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL, PRIMARY KEY (name, version))
```

（SQLite 方言按 store.go:295 注释的同款映射：`INT→INTEGER`、`TIMESTAMPTZ→TEXT`。）

Go API，与 `templates.go#PutTemplate/GetTemplate/ListTemplateNames` 同构（不可变版本化、只插新版）：

```go
// Discipline 是一份版本化的纪律块正文（账本权威副本）。
type Discipline struct {
    Name      string
    Version   int
    Body      string
    CreatedAt time.Time
}

// PutDiscipline 写入下一版本（不改旧行），返回新版本号。
// name 非法（空/含路径分隔符/. ..）拒绝（ledger 包自带校验，不跨包引
// discipline.ErrBadName——d_ledger 不依赖 d_policy）；body 为空或超 64KiB
// （resolver.go:23 maxBlockSize 同源）拒绝。
func (s *Store) PutDiscipline(name, body string) (int, error)

// GetDiscipline 取指定版本；0 = 最新。
func (s *Store) GetDiscipline(name string, version int) (Discipline, error)

// ListDisciplineNames 全部纪律块名（去重升序）。
func (s *Store) ListDisciplineNames() ([]string, error)
```

**将被谁调用**：缝 1、`cmd` 的 discipline 命令族（§2.8）、卡节点角色下拉（ledgerapi.go:658 的来源切换）。

**64KiB 上限的理由**：今天磁盘路径超限拒绝（resolver.go:109-112）防的是「误配二进制塞爆模型上下文」；入库不设限等于把同一把枪换了个地方上膛。上限值与现状逐字相同，不发明新数字。

### 2.2 缝 1：派发期纪律解析入口（`internal/discipline` 公开面）

```go
// DisciplineLookup 是缝 1 对账本的窄依赖：按名字取最新版正文。
// 以函数注入而不是接口/类型引用——d_policy 不 import d_ledger，
// 适配由调用方三行闭包完成。未知名必须原样上抛错误，任何「查不到就兜底」
// 的实现会让 charter-must-override 哨兵连同缺陷三一起复活（§3.2）。
type DisciplineLookup func(name string) (version int, body string, err error)

// DisciplineRef 点名一次纪律来源：Name 走账本解析（Version 记账本版本）；
// RawText 直接作角色层正文下发（不落库，Version=0，服务 spec 用户故事 3 的
// 「临时捏一份下发」）。两者都空 = 未点名，只注入平台层（实现决定 1）。
// 两者都非空为参数错误。
type DisciplineRef struct {
    Name    string
    RawText string
}

// ResolvedDiscipline 是随派发请求下发的产物。
type ResolvedDiscipline struct {
    Text    string // 平台层+角色层组装后的完整正文；空=不注入
    Source  string // 人可读来源标注（沿用 Block.Source 形态）
    Name    string // 点名的角色名；未点名为空
    Version int    // 账本版本号；未点名或临时正文为 0
}

// ResolveDispatch 是派发期纪律正文的唯一裁决点（取正文 → 组装 → 判能力 → 正文或拒发理由）。
//   lookup          账本读取视图；ref 未点名时不调用，可为 nil
//   ref             纪律来源（见上）
//   platformEnabled 协调者侧 PlatformInvariantsEnabled() 原值（manager.go:363 同源）
//   targetCap       目标机 StatusResp.DisciplinesSupported 的原值（nil=对端没上报）
func ResolveDispatch(lookup DisciplineLookup, ref DisciplineRef, platformEnabled bool, targetCap *bool) (ResolvedDiscipline, error)
```

**签名里没有 executor 参数**——这是刻意的。今天 `resolveDisciplineFor(name, execName)` 的 execName 只喂 `builtinByName` 的 implement 档位轴（discipline.go:95-103），内置删除后该轴消失；③层机器级映射本轮语义不动（Out of Scope），不需要 executor。后人若为③层想加回此参数：③层的已定语义是「任何路径都追加」，追加发生在组装之后，同样不需要改本签名。

**归属订正（相对 spec 字面，落骨架时被仓库契约闸推翻，见 §4.5）**：spec 初稿把缝 1 放在「派发编排包」。ledgerstep 在图中属 d_ledger 域，它调 `discipline.Compose` 会造出 d_ledger→d_policy 全新方向；而基线扫描在分支合并重扫前看不到新边，仓库自有闸门 `cmd/graph_gate_test.go#TestRepoContractGate`（跑无视图基线）必红且无法在本分支生命周期内转绿。缝 1 回归平台组装的本体所在包（internal/discipline）后：组装点仍是唯一一处（Compose 不动），三个调用方家族全部走**既有已声明边**，零新增方向。

**将被谁调用**：`cmd` 裸派发路径（d_cli→d_policy，既有边）；agentd 环节执行体装配链（今 cardstep.go，d_gateway→d_policy，既有边）；模板派发路径（ViaTemplate）消费其**产物**——Dispatcher 以数据字段携带已解析三元组，ledgerstep 生产代码不新增 import。

**错误语义（每条独立可断言，全部是拒发不是降级）**：

| 情形 | 行为 |
|---|---|
| `ref.Name` 非法（空格首尾后仍空/含分隔符/`.`/`..`） | 错误，点名校验在缝 1 与缝 2 各有一道（同规则） |
| `ref.Name` 不在账本 | 错误「未知纪律块名字」（lookup 透传账本 ErrNotFound；charter-must-override 哨兵靠它存活，§3.2） |
| `ref.Name` 与 `ref.RawText` 同时非空 | 参数错误 |
| lookup 为 nil 而点名非空 | 参数错误（账本是必需品，见 §2.6） |
| `targetCap` 为 nil 或 false | **拒发**，`ErrUnsupportedTarget` 可辨断言，文案点名目标机需先升级（绝不静默降级） |

组装复用同包 `discipline.Compose`（platform.go:23）：named → `Compose(Block{Text: body}, platformEnabled)`；raw → `Compose(Block{Text: raw}, platformEnabled)`；未点名 → `Compose(Block{}, platformEnabled)`。平台头部/尾部/关闭语义与今天逐字节一致。

### 2.3 wire 增量（全部加法，缺席时行为不变）

| 位置 | 增量 | 出处基线 |
|---|---|---|
| `client.DispatchOpts` | `DisciplineText string`、`DisciplineVersion int` | client.go:715-743 |
| POST `/api/tasks` body | `"discipline_text"`、`"discipline_version"`（与 `"discipline"` 并排） | client.go:757 |
| agentd `dispatchRequest` | `DisciplineText string json:"discipline_text,omitempty"`、`DisciplineVersion int json:"discipline_version,omitempty"` | server.go:1084-1107 |
| agentd `manager.DispatchReq` | `DisciplineText`、`DisciplineVersion`（与既有 `Discipline` 名字段并排） | server.go:1127 装配处 |
| `ledgerstep.DispatchOpts` | `DisciplineText string`、`DisciplineVersion int` | dispatch.go:26-37 |
| `ledgerstep.DispatchResult` | `DisciplineVersion int json:"discipline_version"` | dispatch.go:43-51 |
| `ledger.DispatchSnapshot` | `DisciplineVersion int json:"discipline_version,omitempty"`（老事件无此键，append-only 不回填） | events.go:115-127 |
| `proto.Task` | `DisciplineVersion int json:"discipline_version,omitempty"`（落盘，与 DisciplineName 同一条「不落盘就静默漂移」纪律） | proto.go:243-249 |

`DispatchSnapshot.DisciplineName` 语义收窄为「点名角色名（审计展示用）」，正文的可回放性由 `(DisciplineName, DisciplineVersion)` 二元组承接——B107 那个问题的答案从「名字」升级回「名字 + 版本」。

### 2.4 能力位：四处一条投影链（照 LaunchersSupported 先例，一处不能少）

```go
// proto.StatusResp（status.go，紧邻 LaunchersSupported）：
// DisciplinesSupported 报告本机 agentd 是否认识「接收下发的纪律正文」
// （/api/tasks 的 discipline_text / discipline_version）。
//
// 三态处置与 PtySupported 刻意相反、与 LaunchersSupported 同向：
//   缺席(nil) = 对端 agentd 太老 → 按不支持处置（协调者侧拒发）
//   false     = 不支持
//   true      = 支持
// 为什么：这里放行的代价是静默降级——请求 200、任务正常创建、
// 纪律块悄悄变成执行机本地残留或内置默认（spec 缺陷三的原样复活）。
// 未知时的保守方向由失败的可见性决定（custom-launchers 契约 §4.1 同源）。
DisciplinesSupported *bool `json:"disciplines_supported,omitempty"`
```

| 位置 | 形态 |
|---|---|
| Go `proto.StatusResp` | `*bool`，如上 |
| Go `proto.Machine` | `*bool`，探活时从对端 StatusResp 投影（projects.go:140-163 同款注释结构） |
| TS `StatusResp` | `disciplines_supported?: boolean \| null` |
| TS `Machine` | `disciplines_supported?: boolean`（缺席按不支持处置） |

**上报条件**：实现侧在「收文即用 + 落盘 + continue/resume 消费落盘正文」全部落地后才上报 true——能力位与实现同生同死，不许先报 true 下一版补实现（custom-launchers 契约 §3.1 第 2 点同一条）。

### 2.5 执行机侧行为契约

1. **收文即用**：`Manager.Dispatch` 收到 `DisciplineText` 后**逐字节**作为注入正文，不再调用任何本地解析（`resolveDisciplineFor` 的 ByName/For 分支退役）；`Task.DisciplineName`/`DisciplineVersion` 照收落盘，正文落任务目录（与 plan 内容同落盘形态，具体文件名归实现票）。
2. **continue/resume 不再解析**：两条路径消费首派落盘的同一份正文，**不得另起第二处解析入口**。点名任务缺落盘正文 → Cold 续接拒绝（沿用 manager.go:1272 既有语义）；热重连记 Error 不阻断（manager.go:3377 既有不对称）。
3. **显式空正文是结论不是缺失**：`DisciplineText` 为空且点名也为空 = 本次不注入任何纪律（裸派发在 `PlatformInvariants:false` 机器上的合法形态）；执行机不得把它当成「自己去找一块来注入」的信号。
4. **本地纪律目录退役**：`<DataDir>/discipline/` 不再被读取；`Resolver` 六条回退分支、`builtin/*.md` 六份、`discipline.Dir/List/Read/Preflight` 的执行机调用面删除。linux-01 的两个 AppleDouble 残留随目录退役自然消失（spec Out of Scope 已登记）。

### 2.6 账本开关退休

**决定：`LedgerConfig.Enabled` 退休——struct 字段与 yaml 键保留（严格解析不炸存量 config），值被忽略，加载时 Warn 一条「ledger.enabled 已退休：纪律块入库后账本是必需品」。三处消费分支删除（cmd/agentd.go:248、cmd/ledgercli.go:30、cmd/status.go:86 相应改为恒开路径）。DSN 语义不动。**

理由：翻默认保留 opt-out 会造出第三种失败面——用户显式关账本后每次派发都在取库时炸，而炸的原因是一个他们以为已经关掉的功能；彻底删键会被严格解析拒绝启动（§1.1 第二条）。退休让「配了却无效」可见而不致命。

### 2.7 退役与迁移清单（实现票的拆除范围，本票只冻结边界）

| 对象 | 处置 | 波及 |
|---|---|---|
| `discipline.Resolver`（For/ByName/Preflight）及六条回退分支 | 删除 | manager.go:270 构造点、resolver_test.go |
| `internal/discipline/builtin/*.md` 六份 + embed 声明 + `builtinFor/builtinByName/Builtins/DefaultTierFor/Tier*` | 删除 | discipline.go:14-149；控制台「以此为模板新建」失去数据源（已知后果，界面处理归后续票） |
| `/api/discipline` GET 的 `Builtins`/`Files` 字段 | 类型保留、值恒空数组（防 TS 类型断裂）；端点骨架保留待后续卡重定向到账本 | proto/discipline.go:15-20、fixture `DisciplineResp` |
| `/api/discipline/mapping` PUT | **不动**（机器级 `Discipline` 映射属③层，Out of Scope 明说本轮不动） | discipline.go:199-269 |
| `discipline.Dir/List/Read` | 执行机调用面删除；`ledgerapi.go:658` 角色下拉改 `ListDisciplineNames` | files.go:57,84 |
| 存量 7 份 charter 纪律块导入 v1 | 以本机副本为准（spec 实现决定 3）；`charter-implement` v1 正文需补台账纪律一句 | 数据操作，归实现票；regen 落点改造在 charter 仓不属本卡 |

### 2.8 CLI discipline 命令族（缝 2 的第二个消费方）

`handoff discipline put <name> <file>` / `handoff discipline get <name> [--version N]` / `handoff discipline list`——形态照 template 命令族（cmd/template.go:83 的必填校验风格）。put 走 `openLedger()`（开关退休后恒可用）+ `PutDiscipline`；get 打印正文与版本号。精确 flag 表归 plan 节点。

### 2.9 TS 镜像汇总

```ts
interface StatusResp { disciplines_supported?: boolean | null }  // 缺席=null=不支持，勿照抄 pty_supported
interface Machine { disciplines_supported?: boolean }
interface Task { discipline_version?: number }
```

---

## 3. 语义细则

### 3.1 拒发闸覆盖一切携带正文的派发，含未点名

裸 `handoff dispatch` 也注入平台层正文（实现决定 1），正文走同一 wire 字段，因此**每一次派发前都判 `targetCap`**，nil/false 一律拒发。被否方案「仅点名派发才判闸」：那会让混版舰队里未点名派发的平台层静默丢失——正是缺陷三换个马甲。「全网同批升级是前置」（spec 实现决定 5）与此不矛盾：前置降低触发概率，拒发闸保证触发时可诊断。

### 3.2 `charter-must-override` 哨兵的存续条件

哨兵是工作流模板正文里故意写错的名字。它今天靠 `Resolver.ByName` 未知名报错（resolver.go:177）逼节点 override；重构后靠缝 1「`ref.Name` 不在账本 → 拒发」。**实现票若引入任何「查不到就退回 X」的分支，此哨兵连同缺陷三一起复活**——这是测试必须钉死的反向断言。

### 3.3 导入与版本的语义

存量 7 份以本机副本导入为各自 v1（spec 实现决定 3）。此后每次 regen 产出即 `PutDiscipline` 新版本——不可变版本化意味着「改一句话」永远产生新行，派发取最新版并把版本号记进快照；旧工作流版本钉着的名字照样解析得开（改名 = 发新工作流版本指向新名字，那是工作流的正常操作，spec 弃选「加 ID 列」的论证成立）。

### 3.4 正文不进日志

纪律块正文可能含运营指令与敏感措辞，派发日志沿用今天的计量式记录（`prompt_bytes` 形态，dispatch.go:245），不打正文原文。

---

## 4. 拍板记录

### 4.1 `DisciplinesSupported` 缺席(nil)=不支持=拒发，与 PtySupported 反向

- **难逆转**：四处投影链 + 拒发闸 + proto 注释同动；写反的症状是静默降级，集成期极难定位。
- **无上下文会惊讶**：第四个能力位与前两个方向不同，后人第一反应必然是「统一一下」——统一之后旧机器静默拿到降级正文。
- **真取舍**：被否「与 PtySupported 对齐」。否因——放行的代价形状不同：pty 放行是一次当场可见的失败请求；这里放行是 200 + 任务正常创建 + 纪律悄悄不对。

### 4.2 `LedgerConfig.Enabled` 退休（键保留、值忽略、Warn），不翻默认也不删键

- **难逆转**：三种处置（翻默认/删键/退休）对存量 config 与三处消费分支的影响各不相同，回收任何一种都要再动配置面。
- **无上下文会惊讶**：「这个开关为什么没有任何效果」——Warn 就是给这个疑问预备的答案。
- **真取舍**：被否「翻默认保留 opt-out」（opt-out 下派发必炸于取库，等于保留第三种失败面）与「彻底删键」（严格解析让存量 config 启动失败）。

### 4.3 continue/resume 消费首派落盘正文，不经缝 1 远程解析

- **难逆转**：动执行机存储形态与两条恢复路径的拒绝/降级语义。
- **无上下文会惊讶**：spec 字面写「continue / resume 两条路径同样经它」，落地却是「经它的产物」——因为依赖方向禁止执行机碰账本（spec §依赖方向），远程模式下执行机物理上无法调缝 1。
- **真取舍**：被否「执行机连库按名字重查」（凭据外发，spec 明令禁止）与「执行机按名字查自己磁盘」（缺陷二本体：首回合正文与续接正文分叉）。

### 4.4 拒发闸覆盖一切携带正文的派发（含未点名裸派发）

- **难逆转**：闸的覆盖面一旦收窄，混版舰队丢的是①层恒在内容，症状静默。
- **无上下文会惊讶**：「只是派个任务为什么要探对端能力位」。
- **真取舍**：被否「仅点名派发判闸」——省一次探测，换来平台层静默丢失的通道。

### 4.5 缝 1 落 internal/discipline（d_policy），不落派发编排包——spec 字面被骨架推翻

- **难逆转**：缝 1 是全部派发路径的正文入口，搬家要同时动三个调用方家族与测试布局；落定后再挪等于二次冻结。
- **无上下文会惊讶**：spec 明写「导出到派发编排包的公开面」，实现却在运行策略包——没有这段记录，后人一次「归位重构」就会撞碎契约闸还不知道为什么。
- **真取舍**：被否「ledgerstep 持有缝 1 并申报 d_ledger→d_policy 新方向」——基线扫描在分支合并重扫前看不到新边，仓库自有闸门 `TestRepoContractGate` 必红且整卡生命周期无法转绿（checker 源码核实：dead-contract 只认视图内活跃边，diff 无法为无视图 check 提供豁免）。本方案下组装点仍是唯一一处（Compose 原地不动），三个调用方家族全走既有边，spec 的调用方清单以「消费产物」形态满足。

---

## 5. 测试接缝（承 spec 接缝清单，落成可执行判据）

| 缝 | 判据 |
|---|---|
| **主缝 1**：`ResolveDispatch` | 单元（fake lookup，d_policy 包内）：named 命中（正文=平台头+库正文+平台尾，Source/Version 正确）/ 未知名透传拒发 / RawText 直通（Version=0）/ 都空=纯平台层 / platformEnabled=false 关闭语义 / `targetCap` nil·false·true 三态表（边界型例外，spec 已裁定视为穿缝）/ Name+RawText 同填与带空白名字=参数错误 |
| 直通竖切 | 真实 SQLite 库 + PutDiscipline 种子 + ResolveDispatch 一发穿透两缝（ledgerstep 包测试文件承载——测试不进代码图，生产 import 面零变化） |
| **主缝 2**：`PutDiscipline`/`GetDiscipline` | 与 templates 测试同构：v1→v2 只增不改；Get(0)=最新；Get(不存在)=ErrNotFound；name 非法/正文空/超 64KiB 各一条；schema 含 disciplines 表 |
| wire 冻结 | `Task`/`StatusResp`/`MachinesResp` 进既有 fixture 生成器（contract_fixture_test.go），刷新后逐字节比对；TS 强类型承接（本轮 node_modules 缺失，未验证，见 §7） |
| 反向断言（§3.2） | 未知名拒发的错误**不是**任何形式的回退——哨兵存活判据 |
| 快照回归 | RecordDispatch 后事件 payload 含 `discipline_version`；老事件（无该键）反序列化得 0 不报错 |

直通竖切（重档法定步骤，骨架提交后、扇出前）：夹具直调一发真实调用穿过缝 2 → 缝 1 → wire 解码（真实 SQLite 库 + PutDiscipline 种子 + ResolveDispatch + tri-state 表），钉在 spec 声明的主缝上。归属 contract 节点，不进并行子卡。

---

## 6. 目标图变更

| 变更 | 内容 | 理由 |
|---|---|---|
| `d_cli -> d_policy` entries 追加 `discipline（包级函数）` | 裸派发的 CLI 侧解析入口（缝 1 是 k_discipline_fn 成员；entries 按 checker 语义收容器 Label） | 既有边 budget 32 不动；entry 只把未来调用从 legacy 直调改记为声明入口 |
| `d_gateway -> d_policy` entries 追加 `discipline（包级函数）` | 协调者侧 agentd（cardstep 装配链）同经缝 1 | 既有边 budget 38 不动 |
| **零新增依赖方向** | 缝 1 归位 d_policy（§4.5）；ledgerstep 生产代码不新增 import；执行机→账本仍然为零 | spec 依赖方向红线 + 契约闸可在分支内全程转绿 |

**分支视图执法口径**：本分支与基线的 `graph check` 均 `fails: []`（含 `--view cards-B229-charter` 与无视图两种读数，台账留原始输出）。视图 diff 携带新符号供 breakdown/plan 以 `--view cards-B229-charter` 叠加查询。

本分支视图 diff：`codegraph/diffs/cards-B229-charter.json` 随 Ticket 0 骨架同一提交落盘（新增符号：ledger.Discipline、Store.PutDiscipline/GetDiscipline/ListDisciplineNames、discipline.DisciplineRef/ResolvedDiscipline、discipline.ResolveDispatch；修改符号：proto.Task、proto.StatusResp、proto.Machine、ledger.DispatchSnapshot、TS 三镜像）。

架构形态声明沿既有图 meta（存量项目，best.json 结构树本轮不改）。

---

## 7. 交棒声明

| 法定产出 | 本轮新鲜证据 |
|---|---|
| 契约增量文档，签名带现状出处 | 本文件 §1/§2，出处全部在本轮工作树实读（台账逐笔） |
| 目标图已更新且已提交 | §6 两处 entries 追加，零新增方向；无视图与 `--view` 两种 `graph check` 均 `fails: []`；`TestRepoContractGate` ok |
| Ticket 0 骨架编译通过 | `go build ./... && go vet ./...` 退出 0（输出见台账）；`gofmt -l` 无输出 |
| 可执行冻结命中 | wire 序列化夹具：fixture `-update` 刷新 + 默认模式逐字节通过（台账留原始输出）；三态解释表驱动测试 + 真实 SQLite 直通竖切随骨架落地并通过。哈希/密钥派生类金样本：**未命中**（本契约无此类条目） |
| 符号锚自检 | `codegraph resolve --doc docs/superpowers/specs/b229-contract.md` 12 锚全 ok/moved |
| 三重闸门拍板记录 | 5 条，§4（含 §4.5 归属订正——spec 字面被骨架推翻的一次） |
| web 侧验证 | **未验证**：web/node_modules 缺失，tsc/vitest 无法执行（B185 台账同款环境限制），TS 镜像与 vitest 断言留协调者 |

**欠账（Ticket 0 声明未接线，均已有壳，breakdown 必须逐项派票）**：

1. wire 全链路接线：client body 两键、dispatchRequest/DispatchReq 消费、ViaTemplate 消费缝 1 产物（Dispatcher 以数据字段携带已解析三元组，调用方 cmd/cardstep 绑定 `discipline.ResolveDispatch` + 适配闭包 + 能力位探取）、执行机收文即用。
2. 执行机切换：Manager.Dispatch 收文即用+正文落盘+Task.DisciplineVersion 落盘；continue/resume 改读落盘正文；`resolveDisciplineFor` 及 Resolver/builtin/Dir/List/Read/Preflight 删除。
3. 能力位上报：agentd server.go 上报点置 true（前提：§2.4 同生同死四件事齐）。
4. CLI discipline 命令族 + ledgerapi.go:658 下拉来源切换。
5. LedgerConfig.Enabled 三处消费分支删除 + 加载 Warn。
6. 存量 7 份导入 v1（数据操作）+ charter-implement 补台账句。
7. TS 侧 typecheck/vitest 回归（本轮未验证）。

交棒：`charter:breakdown`。
