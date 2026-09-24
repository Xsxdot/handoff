# B392 拆解稿：mobile bind 生产接线 SessionCookie / SwitchMachine（去掉 notWiredSessions）

状态：**已拍板**（协调者 2026-09-24；P1–P5 裁决见 §8）
卡：B392
标题：mobile bind：生产接线 SessionCookie/SwitchMachine（去掉 notWiredSessions）
定级：**L3 轻档**（spec §2.3；contract 头部「级别」）。非「只动内部实现」——对接既有 gomobile 跨语言契约与平台 cookie jar，本侧零形状修改仍算动契约层
路由：spec → contract → **breakdown（轻档复核，不拆子卡；本稿）** → plan（单实现轮）→ implement → review / acceptance / finish
有效基线：`cards/B392-spec` @ `400ee3c4`（任务卡注明的本卡合并目标；`origin/cards/B392-spec` 实读同号）；当前工作分支 `cards/B392-charter-3` @ `6efc3954`（不切换、不越过）
上游 spec：`docs/superpowers/specs/b392.md` —— **在本工作树/本分支内**（`400ee3c4` 为 HEAD 祖先，实读「状态：已批准（用户 2026-09-24 批准…）」）
冻结 contract：`docs/superpowers/specs/b392-contract.md` —— 头部有「冻结状态：本提交随 Ticket 0 骨架…冻结」；本轮实读基线链（spec 父 `a8e60208` / 第 1 轮基线 `cards/B392-spec @400ee3c4` / 第 2 轮续接点（breakdown 执行时）`origin/cards/B392-charter @d2cd3246`，与 `6efc3954` 同父 `400ee3c4`；其后 canonical 修订为 `bccfabcf`，内容等价且已进入远端）——**逐条核对通过**
图依据：`codegraph/best.json`（`parent` 缺省 = 顶层子系统）；本分支**无** `codegraph/diffs/<分支>.json`（合法：`mobile/` 图外嵌套 module，本卡未引入根模块新符号）
本稿台账：`docs/superpowers/ledgers/2026-09-24-b392-breakdown-ledger.md`
角色边界：本文是**提案**；不写实现代码、不建卡、不派发、不调用 handoff CLI、不起新 executor。扇出与拍板归协调者。

---

## 0. 拍板岔口清单（已裁决）

| 编号 | 岔口 | 方案与取舍 | 本稿倾向 |
| --- | --- | --- | --- |
| **P1** | 本卡子卡形态 | spec §2.3 已批准「**不拆子卡**」，contract 把生产代码与适配器契约测试在 Ticket 0 一次落齐。**甲（推荐）：0 子卡**——剩余工作量收敛为单实现轮（真实 Core 竖切测试 + 生产守卫/变异 + README），plan 直接排期，不再拆卡。**乙**：另立 1 张 implement 子卡承载同范围（多一次派发往返；产出与 spec §8 欠账逐字重复）。 | **甲**。上游已冻结「不拆」，且唯一实现链无并行面；再拆卡只增加流程成本。 |
| **P2** | `context.Background()` 与取消策略 | contract §4.2 已把 `ctx` 来源冻为 `context.Background()`（§6 记「不立」），但 §9.3（移交 plan 附区）又把「注入 `func() context.Context` vs 保留 Background + 壳侧整体放弃」交给 plan 二选一——两处并存。**甲（推荐）：保留 `context.Background()`**，不下沉取消策略；若壳需要超时/取消，另开卡（contract §8.6）。**乙**：本轮给适配器注入 `func() context.Context`（改生产签名与 Ticket 0 测试，反转 §4.2 冻结）。 | **甲**。与已冻 §4.2/§6 一致；Core 内 `exchangeTimeout=5s` 已兜底单次兑换。建议 plan 明确「§9.3 由 §4.2 冻结吸收、不另开」以消歧义。 |
| **P3** | 真实 Core 竖切与生产守卫的测试隔离 | 生产守卫（spec §8.3）**禁止** `swapCore`/`swapSessions`，必须走包级默认 `sessions = newCoreSessions(liveCore)`；而现存 `mobile/bind/bind_test.go:90 TestBindSessionFailIsClosed` 也直接用默认 `sessions`，靠「共享 `liveCore` 无任何机器」才通过，注释仍写「S2 未接线时」。守卫测试一旦在 `liveCore` 上配对成功，二者会互相污染、结果依赖测试次序。**甲（推荐）**：守卫测试自建**独立**真实 Core（`mobilecore.New` + 本地夹具）走完整链，**不碰** `liveCore`；`TestBindSessionFailIsClosed` 改为显式注入空替身（或改名/改注释为「未配对即失败」），消除对全局空态的隐式依赖。**乙**：守卫测试直接复用 `liveCore` 并在末尾 `Close`+重配收尾（次序脆弱、易假红）。**丙**：把默认运行时守卫挪进独立测试包/二进制（隔离最彻底，但 `sessions`/`liveCore` 未导出，跨包不可见，需额外导出缝——越界）。 | **甲**。丙不可行（需新导出缝，违 spec §6.1「不另造只为测试存在的生产 seam」）；乙脆弱。注意：spec §8.3 的「不调用 swap」只约束**守卫测试本身**，不禁止修既有测试。 |
| **P4** | 契约回写 | 原 contract §5.1-2 标注 `[T0]`「生产包中不存在 `notWiredSessions` 与 `errSessionsNotWired`（源码零命中）」并称由 `adapter_test.go` 锁住，但：①`adapter_test.go` **无此断言**；②`session.go:5` 注释仍含 `notWiredSessions` 字样（源码文本非零命中）。**甲（推荐）**：本卡把该条改述为「生产**不装配**占位」——由 `TestDefaultRuntimeSharesOneCore`（类型断言 + 指针同一性）锁住；「源码零命中」若保留则须删注释并加 grep 断言（自相矛盾，不取）。同时在 contract 末尾补一行修订记录，并补「测试文件可 import `internal/client`/`internal/proto`，生产文件不可」这条边界澄清（`export_surface_test.go` 的 import 禁令只扫非 `_test.go`，实现竖切夹具依赖此事实）。**乙**：不再回写，澄清只活在本稿。 | **甲**。纪律：澄清只活在拆解稿里，review 的冻结物触碰行会对不上账。 |
| **P5** | 真实 Core 竖切是否必须落 `-race` 且不串机 | spec §8.2-8 已要求并发 `SwitchMachine`/`SessionCookie` 在 `-race` 下通过且不把 B 的 cookie 返给 A。**甲（推荐）**：实现轮按冻结判据落 `-race` 并保留适配器锁的确定性握手（`TryLock`）测法，双保险。**乙**：只跑 `-race` 不保留握手断言（`-race` 只是附加闸、不保证复现无锁交错）。 | **甲**。contract §7.3 的 `TryLock` 握手是确定性打红手段，real-Core 版应沿用。 |

> 协调者裁决口径（2026-09-24）：P1–P5 全按推荐方向。P3 的“独立真实 Core”只用于行为竖切；默认生产身份守卫仍直接检查 `liveCore` 与 `sessions` 的同一实例，且不替换为 fake。P4 的 contract 修订已回写 `b392-contract.md §11`，不是只留在本稿。

> 若协调者裁决改变契约冻结面（如 P2 选乙改签名、P4 选乙不回写），须先回写 `b392-contract.md` 再进入 plan；本轮已按 P1–P5 甲案吸收。

---

## 1. 触及子系统清单与派卡资格核

子系统 id 与类型取自 `codegraph/best.json`（`domains` 的 `parent` 缺省 = 顶层；子域在其父域下）。本卡**主实现面 `mobile/bind` 图外**（独立嵌套 module，扫描配方显式排除 `mobile` 前缀），故并列图外条目。

| 子系统 / 载体 | 图类型 | 本卡角色 | 有界文件集 / 暴露面 | 派卡资格核（本卡无扇出，仅记事实） |
| --- | --- | --- | --- | --- |
| **`mobile/bind`（图外嵌套 module）** | **逻辑型**（Go 适配器逻辑，机内可闭环）+ **边界型**（平台 cookie jar，对面是 Android/iOS 壳现实） | **唯一生产实现面**：Ticket 0 已落生产接线；剩余实现轮只加真实 Core 竖切/守卫测试与 README | 已落：`mobile/bind/adapter.go`（新，`sessionCoreAPI`+`coreSessions`）、`mobile/bind/bind.go`（`liveCore`）、`mobile/bind/session.go`（接 `newCoreSessions(liveCore)`，删占位）、`mobile/bind/adapter_test.go`（新，8 支契约测试）。剩余：`mobile/bind/*_test.go`（真实 Core 竖切/守卫）、`mobile/README.md`。**导出面零变化**（七函数 + `Machine`）。 | ①文件集可圈（见 §3）；②七函数签名已冻（`mobile/bind/export_surface_test.go` 的 `wantBindSurface`）；③无根模块新增依赖方向（图外）；④逻辑型=真 Core 机内闭环（欠），边界型=真机（欠）。 |
| `d_transport` → 子域 `d_transport_channel` | **边界型**（对面是跨机 relay/直连 agentd 现实） | **核侧能力供应方（零改动）**：`internal/mobilecore` 提供 `Core.Activate/Session/ActiveMachine/Origin`；本卡只消费 | 只读：`internal/mobilecore/core.go`（`Activate` :219、`Session` :266、`ActiveMachine` :279、`Origin` :333、`New` :107）、`internal/mobilecore/session.go`（`SessionCookie` DTO :42、`exchangeTicket` :69、`toSessionCookie` :125）、`internal/mobilecore/proxy.go`（回环反代，零改）。 | ①不派卡；②签名冻结（contract §1.2）；③无新边；④核侧行为由既有 `internal/mobilecore` 测试背书，本卡不重写协议。**注意 `codegraph check` 对 `k_mobilecore_*` 报 best-dangling warn（基线既存，非本卡引入）。** |
| `d_gateway` | **边界型**（对面是浏览器/壳的 HTTP 现实） | **cookie 属性对侧（零改动）**：`handoff_session` 名与 `Path/HttpOnly/SameSite/Secure` 的唯一生产者 | 只读：`internal/agentd/auth.go`（`sessionCookieName` :28）、`internal/agentd/authroutes.go#sessionCookie`（:343）。 | ①不派卡；②wire 不变；③无新边；④手机核不 import agentd，cookie 名由 `TestSessionCookieNameMatchesAgentdSource` 逐字锁定（`internal/mobilecore/session_test.go:646`）。 |
| `internal/client`（`d_transport_channel`） | **边界型** | **测试夹具依赖（仅 `_test.go`）**：真实 Core 竖切需 `client.New` 造可控对端客户端 | 只读：`internal/client/client.go#Client.IssueAuthTicket`（:1267）、`Client.New`（:195）。生产 `mobile/bind` 文件**不得** import（`export_surface_test.go:77` 锁）。 | ①不派卡；②契约由 B369 冻；③无新边；④夹具 HTTP 对端可 fake，机内可闭环。 |

**不列为本卡实现域（零改动）**：`d_orchestration`、`d_workspace`、`d_execution`、`d_sessions`、`d_protocol`、`d_ledger`、`d_collab`、`d_cli`、`d_web`、`d_policy`、`d_maintenance`、`d_scheduling`、`d_keystone`。

### 1.1 竖切债核对（架构法第三条）

- `mobile/bind` 是既有图外包，本卡只在其内新增/修改 `_test.go` 与 `README.md`；不新增源文件族、不扩前缀族。**能圈出有界文件集，不插竖切还债卡。**
- 实现若需改 `mobile/bind` 生产文件（`.go` 非测试）以外的面，或改 `internal/mobilecore`、`internal/agentd`，必须退回协调者重核边界（contract §1.3 / spec §7 明示「限定在 `mobile/bind`、其测试与移动交接文档」）。当前 Ticket 0 已把生产改动落完，剩余实现**不应**再动生产 `.go`。

### 1.2 图覆盖债

- 图外（`codegraph sym` 亲跑「不在图中」，与本卡一致，§台账）：`mobile/bind` 全部符号（`coreSessions` / `newCoreSessions`，预期图外）；`Core.Activate` / `Core.Session` / `Core.ActiveMachine` / `Core.Pair`（S2 新符号未进旧 baseline，spec §11 / contract §3 已记）。
- 上述符号本稿**只用普通路径、不带 `#Symbol` 锚**；已入图符号用 `file#Symbol`（§3④）。
- 本稿不新增 `codegraph/diffs/<branch>.json`：breakdown 不改生产符号、不引入根模块新符号。

---

## 2. 契约增量核对

### 2.1 上游状态位核对（读文件头，不靠会话记忆）

- **spec**：`docs/superpowers/specs/b392.md:3` 「状态：已批准（用户 2026-09-24 批准；D1 锚点与 S1–S3 建议已吸收）」——**逐字核对通过**；文件在本工作树内、`400ee3c4` 为 HEAD 祖先（`git merge-base --is-ancestor` → YES）。
- **contract**：`b392-contract.md:5` 「冻结状态：本提交随 Ticket 0 骨架、能变红的适配器契约测试与本台账冻结」——**在位**；冻结事实由提交 `6efc3954` 承载，其父 `400ee3c4` 与头部基线链一致（台账 1–3）。
- **图门禁**（本轮新鲜，§台账 11–12）：`codegraph --repo . check` → exit 0，`fails=[]`（41 条 warns 为基线既存：`best-dangling`（含 `k_mobilecore_*`）/`budget-raised`/`legacy`/`oversized-package`/`prefix-family`）；`codegraph --repo . validate` → exit 1，`issues` 恰为 contract 所记两条他分支视图问题（`cards-B272-charter`、`cards-B374-charter`），**非本卡引入**。

### 2.2 契约 §4 冻结条目逐条对照（现状代码位置）

| 契约条目 | 现状核对 | 越界结论 |
| --- | --- | --- |
| §4.1 唯一组装点 | `mobile/bind/bind.go:40` `liveCore = mobilecore.New(nil, log)`、`:42` `core coreAPI = liveCore`；`mobile/bind/session.go:20` `sessions sessionAPI = newCoreSessions(liveCore)`。配对面/会话面/`Close` 同一实例。`grep notWiredSessions/errSessionsNotWired` 仅命中 `session.go:5` 注释（无装配）。 | **不越界**，逐字在位 |
| §4.2 适配器签名与顺序 | `mobile/bind/adapter.go`：`sessionCoreAPI`（`Activate/Session/ActiveMachine/Origin`）、`coreSessions{mu, core}`、`newCoreSessions`、`SessionCookie`（先 `ActiveMachine()==machine` 再 `Session()` 再判空值）、`SwitchMachine`（`Activate(context.Background())`→判空→`Origin`，任一步失败 `("",err)`）。`ctx` 来源 = `context.Background()`。 | **不越界**，逐字在位 |
| §4.3 壳侧顺序与 cookie 属性 | 属**交接文档面**（`mobile/README.md`），尚未写；spec §9.8 列为欠账。 | **不越界**（未落项已入欠账） |
| §4.4 图面 | `mobile/` 图外；`target.json`/`best.json` 未改；本分支无视图 diff。`codegraph check` excludes mobile。 | **不越界** |
| §5 原子冻结清单 | 原 §5.1-2 的“源码零命中”缺口已按 P4 回写为“生产默认装配不包含占位”；其余 17 条均有在位测试或编译期锁定（台账 4–9）。 | **已修订**，不构成新接缝 |
| §7.2 可执行冻结 | 无哈希/密钥/编码新命中（本卡只透传 Core 兑换值）。 | **不越界** |
| §7.3 变异证据 | contract 自述三条 double 层变异红（`return "",nil` / 去活动机检查 / 去互斥）；**本节点未复跑**（避免扰动工作树），如实标注。 | 记录事实，非本稿结论 |

### 2.3 退回 contract（不许边拆边加）

**无。** 逐条核对后，未发现「spec 承诺了行为、冻结物里没有载体」的新接缝：真实 Core 竖切所用的 `mobilecore.New(dial DialFunc, log)`（`:107`）、导出的 `DialFunc`（`core.go:35`）、`client.New`、`proto.EncodePairBundle`（`:111`）均已存在且非本卡新造；适配器所需 Core 四方法签名冻结；平台 jar 属外部真机接缝（spec §6.3 已认）。**不需新增接缝。**

### 2.4 边界澄清（不退回；建议按 P4 回写 contract 修订记录）

1. **`mobile/bind` 测试文件可 import `internal/client`/`internal/proto`**：`export_surface_test.go:77 TestBindProductionHasNoProtocolLogic` 的 import 禁令只扫非 `_test.go`（AST 过滤 `!strings.HasSuffix(fi.Name(), "_test.go")`），故真实 Core 竖切夹具在 `_test.go` 内 import `internal/client`（造对端客户端）与 `internal/proto`（造配对 bundle）合法。生产文件仍禁。
2. **核侧行为归 `internal/mobilecore`，本卡不重写**：`Activate` 的「切机清旧槽/同机缓存/重兑换」（`core.go:236-263`）与「无 cookie 即报错」（`session.go:99-118`）是本卡消费的既有权威事实，B392 不复制协议。
3. **`d_gateway` 的 cookie 属性是唯一生产者**：`Secure=r.TLS!=nil` 在明文 loopback 下为 false；string-only 导出面**不返回**这些属性，壳按 §4.3 硬编码——该跨端属性无机内序列化测试，归真机（§6）。
4. **`codegraph check` 的 `k_mobilecore_*` best-dangling warn 属基线既存**：本卡不动 `best.json`，`mobile/bind` 图外，不引入新违规。

---

## 3. 子卡清单与依赖 DAG

### 3.0 DAG

```text
[Ticket 0 已落，不重开]  contract @6efc3954：
    adapter.go（coreSessions 生产适配）+ bind.go（liveCore 单实例）+ session.go（接会话视图，删占位）
    + adapter_test.go（8 支能变红契约测试）
         │  （生产代码在本卡已全部落地；剩余为测试加固与交接文档）
         │
         └──> plan：单实现轮 I1（真实 Core 竖切 + 生产守卫/变异 + `mobile/README.md`）  ← 唯一剩余面
                  │
                  └──> review / acceptance / finish
真机验收（gobind 产物 / AAR·XCFramework / 平台 cookie jar）──────────> 协调者执行（§6）
```

**子卡数 = 0**（L3 轻档；spec §2.3 已批准「不拆子卡」；P1=甲）。Ticket 0 **不是**可独立派发的子卡——它是已完成的 contract 冻结物，本稿只复核、不重开。

### 3.1 唯一实现工作流 I1（**非扇出子卡**；plan 直接吸收，四段式如下）

**①契约引用**：contract §4.2（适配器语义）、§5（§5.1–5.5 全部 `[T0]` 条目）、§8.1–8.4（欠账 1–4）、§9.1–9.3（plan 附区）；spec §8.2（真实 Core 竖切 8 项）、§8.3（非 fake 生产守卫 + 四变异）、§9.6–9.8。

**②意图与为什么**：Ticket 0 只证明了「适配器自身契约」在 **test double** 层可红（contract §7.3），尚未证明「真实 `*mobilecore.Core` 穿过导出绑定面」这条生产链（spec §8.1 表：禁以 fake Core 作验收集合），也尚未把「恢复占位/去活动机检查/去互斥/`return "",nil`」四变异在生产装配层打红（spec §8.3），交接文档也缺 §4.4 调用顺序。I1 补齐这三件，**不改生产 `.go`**（生产接线 Ticket 0 已落）。为什么必须真实 Core：fake 全绿与生产 unwired 可同时成立，正是本卡要消灭的假绿（spec §1 卡面红线）。

**③验收（行为化，按子系统类型分流）**：

*逻辑型（`mobile/bind` 机内闭环）——必须实跑到结果：*
- **竖切**（spec §8.2）：新增测试在 `mobile/bind` 内用 `mobilecore.New(dialFunc, log)` + 本地 `httptest` agentd 夹具（夹具按机器/ticket 签发**可区分**的 `handoff_session`）搭真实链，覆盖 8 项——配对 A/B 在线；`SwitchMachine(A)` 返回 A 的 loopback origin；`SessionCookie(A)` **逐字等于**夹具 `Set-Cookie.Value`；切 B 后 origin 改 B、`SessionCookie(A)` 错机 error、`SessionCookie(B)` 逐字等于 B 值且 ≠ A；切回 A 重新兑换；同机重复命中缓存；未配对/离线/兑换无 cookie/`Close` 后均 error 且**绝不空串 + nil**；`-race` 并发不把 B 的 cookie 返给 A。
- **生产守卫**（spec §8.3）：一条**不调用 `swapCore`/`swapSessions`**、从默认生产组装直走 `Pair → SwitchMachine → SessionCookie` 的真实 Core 测试；并实测四变异，保存**原始红输出**后还原——① 生产恢复 `notWiredSessions` → 守卫红；② 去掉 `ActiveMachine==requested` 检查 → A/B 串机测试红；③ 任一失败路径改 `return "", nil` → 失败语义测试红；④ 去掉适配器互斥 → 阻塞式并发测试确定性打红（`-race` 作附加闸，`TryLock` 握手作确定性打红）。
- **模块回归**（spec §9.6）：`cd mobile && go build ./... && go test ./... -count=1 && go test ./bind/ -race -count=1 && go vet ./...`，`gofmt -l` 无输出。
- **根模块**（spec §9.7）：根 `go build ./...`、根全量测试不退化；`go list ./...`/`go list -deps ./...` 均不含 `github.com/Xsxdot/handoff/mobile`。
- **测试隔离**（P3=甲）：守卫测试自建独立 Core、不污染包级 `liveCore`；`TestBindSessionFailIsClosed` 语义/隔离收口。

*文档面（`mobile/README.md`）：*
- 明写「`SwitchMachine(target)` 成功 → 清该 loopback host 全部端口的旧 `handoff_session` → `SessionCookie(target)` → 写 host-only cookie（`Path=/`、`HttpOnly=true`、`SameSite=Lax`、`Secure=false`、无持久 `Max-Age/Expires`）→ 导航到返回 origin」，并写「任一步失败不得导航；清 jar/注入失败由壳呈现可行动错误，Go 绑定层不操作平台 cookie store」（spec §9.8）。可 grep 断言该顺序串与失败不导航句。

*边界型（真机，归协调者，§6）：* gobind 真产物、AAR/XCFramework、平台 cookie jar 行为。

**④入口指针与有界文件集**：
- `mobile/bind/adapter_test.go`（既有 8 测试；可增补真实 Core 变体）
- `mobile/bind/<新增>_test.go`（真实 Core 竖切 + 生产守卫，文件名由 plan 定；**仅 `_test.go`**）
- `mobile/bind/bind_test.go`（`TestBindSessionFailIsClosed` 隔离/语义收口，P3=甲）
- `mobile/README.md`（§4.4 调用顺序与职责边界；建议置于「绑定面」之后，contract §9.4）
- 可选：`docs/superpowers/specs/b392-contract.md`（修订记录一行，P4=甲——由协调者在拍板批回写）
- **禁止**：改 `mobile/bind` 生产 `.go`（`bind.go`/`session.go`/`adapter.go`）、`internal/mobilecore/**`、`internal/agentd/**`、`cmd/**`、`web/**`、`codegraph/**`。
- 符号锚（已入图，可 `resolve`）：`internal/client/client.go#Client.IssueAuthTicket`、`internal/agentd/authroutes.go#sessionCookie`；`mobile/bind`/`internal/mobilecore` 全部符号**图外，不带锚**。

---

## 4. 跨子系统行为闭环核对

只核 spec 承诺的跨子系统可观察行为（spec §3.1 目标 + §5 用户故事 1–5）。五格齐全，归属存在；「Ticket 0」= contract 已冻实现 + 本稿复核，非新子卡。

| 触发者 | 权威事实/载体 | 消费者 | 可观察结果 | 归属子卡 |
| --- | --- | --- | --- | --- |
| 移动壳配对后显式选机，调 `SwitchMachine(target)` | `coreSessions.SwitchMachine`（`adapter.go:58`）→ `Core.Activate` 清槽+重兑换（`core.go:219`）→ 校验非空 → `Core.Origin`（`:333`） | 壳导航 / webview | 返回目标 loopback origin；两步失败均 error、壳不得导航（不报「会话已就绪」的假地址） | Ticket 0（adapter）+ I1 竖切 + 真机 §6 |
| 壳在切到 B 后误取 `SessionCookie(A)` | `coreSessions.SessionCookie`（`adapter.go:39`）锁内校验 `ActiveMachine()==machine`（`core.go:279`） | 壳 | 返回 `("",err)`，绝不把 B 的 cookie 给 A | Ticket 0 + I1 串机测试 |
| 用户 A→B→A 切机 | `Core.Activate` 切机清单槽+重兑换（`core.go:242-262`，一罐只装一机）；壳按 §4.3 清旧 jar | webview cookie jar | 每次进入所选机器，旧 cookie 不被发送 | Ticket 0（Core 既有）+ README（I1）+ 真机 §6 |
| 维护者跑测试 | 默认生产装配同一 `liveCore`（`bind.go:40-42` / `session.go:20`） | 测试套件 | 若恢复 `notWiredSessions`/第二 Core，生产守卫红；fake 换实现不再能证明生产接线 | Ticket 0（`TestDefaultRuntimeSharesOneCore`）+ I1 守卫与变异 |
| Android/iOS 壳开发者 | contract §4.3 + `mobile/README.md` | 壳 | 拿到稳定「切机→清 jar→取 cookie→注入→导航」顺序，不接触 token | I1 文档（README） |

**闭环结论**：每条 spec 承诺行为五格齐、有归属；无「只活在接口、测试或无人认领格子里的承诺」。`SessionCookie.Value` 的跨端注入、平台 cookie 属性属外部现实，落 §6 真机，不假装机内可达。

---

## 5. 缺陷族对抗审查（逐族正面回答）

覆盖面 = Ticket 0 的 B392 生产改动（`liveCore` 组装 + `coreSessions` 适配器 + `session.go` 接线）与 I1 的测试/文档面。

**1. 生命周期 / 状态机中断**
- 适配器 `mu` **跨越** `Core.Activate` 的网络兑换（最长 `exchangeTimeout=5s`，`session.go:35`）：切机/取 cookie 期间所有会话入口串行阻塞，最长约 5s；这是 §4.2 冻结语义，非缺陷。
- 宿主中途重启：`Core` 是**进程内、非持久**单槽（`core.go:89-91`），无磁盘会话、无临时目录、无后台 goroutine/子进程；`exchangeTicket` 用一次性 `http.Client`（`session.go:87`）且请求结束即回收。**孤儿资源 = 无。** 重启后配对与活动槽全丢，壳需重新配对/切机——由壳侧流程承担，非本卡引入。
- `Core.Close` 与 `Activate` 并发：`Activate` 在兑换后重取 `c.mu` 并检查 `c.closed` 返回 `errCoreClosed`（`core.go:252-256`），`Session`/`Origin` 同样先检查（`:269/:336`）。适配器不持有 `Core.mu`，`Close` 不被适配器锁阻塞。失败即返回 error、不吐 origin。**收尾责任清晰。**
- 残余（真机观察）：壳「清 jar / 注入」失败时 Go 已返回成功 origin（见族 2）——归 §6。

**2. 静默失败 / 误导报错**
- 传播契约：适配器每条失败路径 `return "", err`，无 `("",nil)`（`adapter.go:42-51`、`:62-71`）；`SwitchMachine` 在 `Activate` 失败/**空值**后**不调用** `Origin`（`:62-67`），避免把 origin 当 cookie/成功信号。
- 可行动性：绑定层对错误加机器名日志上抛（`session.go:27/:41`），壳呈现错误并可重试（spec §11）。
- **存在「报成功但没做」的窗口吗**：`SwitchMachine` 成功只保证「Core 已兑换且 origin 可用」；壳后续清 jar/注入是**外部步骤**，Go 无法感知。**有窗口，但已由契约显式划界**：spec §4.4/§11 规定任一步失败不得导航、壳呈现可行动错误，Go 层不伪装能操作平台 cookie store。另有「`Activate` 成功而 `Origin` 失败 → 返回 error 但 Core 已持目标会话且不伪造回滚」（spec §4.2 明示）——壳不得导航，属**受控不一致**非静默成功。
- 凭据卫生：只记 `machine`、`origin`、`value_len`（`session.go:31`），不记 cookie value / token。**无静默失败。**

**3. 跨平台假设**
- Go 适配器只用 `sync.Mutex`/`context`/`net/http`/`fmt`/`errors`（`adapter.go:3-10`），平台中立。**无，因为不引入平台相关系统调用。**
- **边界风险（真机）**：平台 cookie jar（`WKHTTPCookieStore`/`CookieManager`）的 host-only + `Path=/` + 清所有端口旧 cookie 行为、`SameSite=Lax`/`Secure=false` 在明文 loopback 的实际接受度，均**未验证，需真机**（§6）。gomobile 产物形状（gobind）也要真机/CI 实跑，本工作树仅 `go build`（shape 测试因缺 gobind **skip**，§台账 10）——**skip ≠ 绿**。

**4. 假红 / 假绿测试**
- 既有适配器测试锁**调用方可观察行为**（顺序、失败闭合、错机拒绝、锁串行化），非内部帮手；换实现只要行为不变仍绿。
- **反面断言在位**：`FailsClosed` 断言失败时 `origin==""` 且 `Activate` 失败后**不调** `Origin`；`RejectsWrongMachine` 断言错机/空值/核错均 `("",err)`；`LockSerializesSwitchAndRead` 用同包 `TryLock` 确定性握手（去锁即红，不靠定时器猜）。
- **假绿温床（本稿发现，须由 I1 处置）**：① 生产装配目前只有**身份/指针同一性**断言（`TestDefaultRuntimeSharesOneCore`，`adapter_test.go:226`），**无**「真实 Core 穿过导出面走完整链」的守卫——这正是 spec §8.3 欠账，也是本卡假绿根源，必须在 I1 补齐。② `bind_test.go:90 TestBindSessionFailIsClosed` 注释与语义**已过期**（写「S2 未接线时」，实则靠共享 `liveCore` 无机器才通过），且与「守卫测试跑真实 Core」存在**共享全局态次序耦合**——P3 必须处置，否则新守卫带来假红/假绿。③ 原 contract §5.1-2 的「源码零命中」缺口已按 P4 回写为生产装配语义。
- 负载/并发：真实 Core 并发竖切须在 `-race` 下跑，且用阻塞式交错（沿用 double 的 `sessionEntered`/`sessionGate` 思路）确定性验证「读 A 期间切 B 不串机」。
- **独立验收集合纪律**：spec §8.1 表禁以 fake Core 作验收集合；I1 的真实 Core 夹具必须按机器/ticket 签发**可区分**的 `Set-Cookie`，断言与响应对得上，**不得**预置与响应无关的假值制造通过（spec §8.2 末）。

**5. 门禁绕过**
- 本卡**不新增写路径/执行路径**：`SwitchMachine` 触发的 token→ticket→cookie 兑换走 Core 既有已配对客户端，门禁（回环不注入凭据、token 不导出）未变。
- 门覆盖全部表面：绑定面导出面由 `export_surface_test.go` 逐字钉住七函数、禁 `Token`/`Dial`（`:66-71`）——**回环门禁的承重属性有测试锁**。`mobile/bind` 生产文件禁 import `relay/client/proto`（`:77`），协议零重实现。
- 检查与动作之间的窗口（TOCTOU）：`SessionCookie` 的「校验活动机 + 读 cookie」在**同一把适配器锁**内不可交叉（`adapter.go:40-52`），`SwitchMachine` 同锁（`:59-72`）；contract §7.3 已证去锁可确定性打红。**无门禁绕过，因为本卡未放松任何准入；适配器锁正是为封住错机 TOCTOU 而冻结。**

**6. 序列化边界**
- 本卡**无新字段**；既有手工投影点：`mobilecore.toSessionCookie`（`session.go:125`，`http.Cookie`→`SessionCookie` DTO）→ 适配器只取 `.Value`（`adapter.go:52`）→ gomobile 映射为 Java/Kotlin `String`/ObjC `NSString`。**「字段缺失 vs 零值」分辨**：Core 在 `exchangeTicket` 对「无 cookie」与「cookie 值为空」分别报错（`session.go:107-118`），适配器再对 `.Value==""` 兜底报错（`adapter.go:49/:65`）——可空语义不塌成零值。
- 「两端各自有测试 ≠ 链路有测试」：`internal/mobilecore` 有 `toSessionCookie`/兑换测试，`mobile/bind` 有适配器 double 测试，但**无一条穿过「真实 Core → 适配器 → 导出面」**的链——正是 I1 竖切要补的回归（spec §8.2）。**这不是新序列化字段，故不需 roundtrip 属性测试；判据是 `SessionCookie(A)` 逐字等于夹具 `Set-Cookie.Value`。**
- 平台 cookie **属性**（`Path/HttpOnly/SameSite/Secure`）不由 Go 返回（string-only），壳硬编码——跨端序列化边界无机内测试，归真机 §6。

**7. 枚举新值过既有白名单**
- 本卡**不引入新枚举值**（无新状态名/事件类型/kind）。既有的 `handoff_session` cookie 名、`SameSite` 字面值（`sameSiteName`）、`SessionCookie` DTO 字段均不变。**无，因为没有新值流经任何既有校验器/白名单/switch。**

**8. 承重安全属性有测试锁住**
- **「一罐只装一机 / 不串机」**：由适配器锁 + `ActiveMachine()==machine` 检查承重；double 层 `LockSerializesSwitchAndRead` 与 `RejectsWrongMachine` 能变红（contract §7.3），**真实 Core 层**的并发断言归 I1（spec §8.2-8）。→ 尚未完全锁满，I1 补齐后才算。
- **「回环不注入凭据 / 绑定面不导出 Token·Dial」**：由 `export_surface_test.go` 与 mobilecore 既有测试锁住，本卡不改。
- **「Close 后拒绝」**：Core 侧有测试；绑定导出面层的 `Close` 后拒绝归 I1 守卫（spec §8.2-7）。→ 未锁满，I1 补。
- **结论**：两条安全属性（不串机、Close 后拒绝）在**适配器/导出面层**尚无测试锁，I1 必须补，否则 acceptance 的变异复验无对应测试可红。

**9. webview / 平台表现差异候选族**
- **无，因为**不触 `d_web`/Wails/Chromium；但**消费端是移动 webview 的 cookie jar**，其行为（清全端口 host cookie、host-only 注入、`SameSite=Lax`、明文 `Secure=false`）**未验证，需真机**（§6）——属边界型，非机内可判。

---

## 6. 真机清单（全部「未验证，需真机」或「机内欠跑」，归协调者/实现按标注执行）

1. **gobind 真产物（CI/装了钉版 gobind 的机器）**：`cd mobile && go test ./bind/ -run TestGomobileSurfaceHasNoSkips -count=1` 实跑，确认产物无 `skipped function/field` 且七函数都在；本工作树无 gobind，本轮 **SKIP（非绿）**（spec §9.5）。工具缺失/版本不符/skip 均阻塞验收，不得以退出 0 冒充。
2. **移动 AAR / XCFramework 重建 + 真机注入**：重建 Android AAR 或 iOS XCFramework，验证切机后 API 与 WebSocket 带新 cookie、旧机 cookie 不再发送、无 cookie 仍 401（spec §9.9）。
3. **平台 cookie jar 边界**：真机验证壳按 §4.3 清该 loopback host 全部端口旧 cookie、写 host-only `handoff_session`（`Path=/`、`HttpOnly=true`、`SameSite=Lax`、`Secure=false`、无持久 `Max-Age/Expires`）后导航不被旧 cookie 拉回。
4. **壳清 jar/注入失败 UX**：模拟失败，确认不导航且呈可行动错误、可重试（spec §4.4/§11；Go 层不可代替）。
5. **根模块全量测试**：本工作树已跑根 `go build ./...`（绿）与 `go list` 不含 mobile（0）；根**全量测试**属 acceptance 判据（spec §9.7），本节点未跑，标「机内欠跑」。
6. **真实 Core 竖切 + 生产守卫 + 四变异红输出**：属 I1（机内、可跑），本节点仅复核 Ticket 0 的 double 层现状（`go test ./...` 绿，§台账 6–9），未复跑变异。

---

## 7. 出稿自检

- [x] **产出四样齐全**：§1 子系统清单每个带 best.json 类型（并标 `mobile/bind` 图外逻辑+边界）；§2 契约增量逐条有结论（§2.3 无退回、§2.4 四条边界澄清）；§3 子卡=0（L3 不拆）且唯一实现工作流 I1 四段式、判据行为化；§5 缺陷族逐族含「无，因为…」。
- [x] **「拍板」岔口集中**：P1–P5 全在 §0，正文岔口回指。
- [x] **「未验证，需真机」汇总**：§6 六条（含 2 条「机内欠跑」显式标注）。
- [x] **每张子卡有界文件集核过**：I1 已圈（`mobile/bind/*_test.go` + `mobile/README.md` [+可选 contract 修订记录]）；子卡数 0，Ticket 0 非扇出卡。
- [x] **行为闭环每行五格完整**：§4，归属存在；无无人认领格子。
- [x] **契约状态位**：spec「已批准」、contract「冻结状态」均实读在位；§2.1 逐条核对。
- [x] **未亲自跑到结果的命令未写成结论**：本轮亲跑 `go build`/`go test ./...`/`-race`/`vet`/`gofmt`、`codegraph check/validate/sym/resolve`、`go list`（§台账）；变异红、gobind、真机一律标未验/待跑。
- [x] **收尾**：`codegraph resolve --doc docs/superpowers/specs/b392-breakdown.md` 亲跑（结果落台账）；坏锚即修。

---

## 8. 拍板记录区（协调者回填）

| 编号 | 裁决 | 理由 |
| --- | --- | --- |
| **P1** | **甲：0 子卡，单实现轮** | spec 已冻结不拆；剩余测试与 README 是一条有界实现链，拆卡只增加往返。 |
| **P2** | **甲：保留 `context.Background()`** | 与 contract §4.2/§6 一致；Core 已有 5 秒兑换超时，未来取消需求另开契约卡。 |
| **P3** | **甲：独立真实 Core 竖切 + 默认身份守卫** | 独立 Core 避免污染包级 `liveCore`；默认 `TestDefaultRuntimeSharesOneCore` 仍直接证明生产不是占位/第二 Core；旧空态测试改为显式替身。 |
| **P4** | **甲：回写 contract** | “源码零命中”与注释事实不符；已改为生产不装配占位，并由身份守卫锁定；测试 import 边界澄清写入 contract §11。 |
| **P5** | **甲：`-race` + `TryLock` 双闸** | race 检查并发安全，TryLock 确定性证明去锁变异可红；二者不可互相替代。 |

（以上裁决与 contract 修订同批提交；状态行与裁决记录一致。）
