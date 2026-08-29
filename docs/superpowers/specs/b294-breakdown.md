# B294 拆解提案：远端预览会话与隔离 Chromium

**状态：已拍板（2026-08-29）**（协调者按已批准 spec 采纳全部推荐项；不退回 contract）
**卡号：B294 ｜级别：L3 轻档 ｜分支：`cards/B294-breakdown`**
**基线：** `1e45ff75c9b969db6ee3467779ad6261f8b3a029`；上游 spec 已批准，contract 已冻结。
**配套台账：** `docs/superpowers/ledgers/2026-08-29-b294-breakdown-ledger.md`。

本稿不写实现代码、不创建可独立派发的并行子卡。U0–U5 是同一轮 implement 的序贯有序单元，供协调者审阅和扇出；每个单元均有界到文件集合。契约边界澄清已回写 `b294-contract.md §1.3`，本稿不把新字段或新路由偷偷加入冻结面。

## 0. 拍板清单

以下岔口已由协调者于 2026-08-29 拍板。全部采纳推荐项；与已批准 spec 一致，不退回 contract。

| 编号 | 岔口与提案 | 裁决 |
| --- | --- | --- |
| P0 | `is-open` 是本页收到 `OpenPreview` 成功后的短暂本地投影，还是刷新/Chromium 自行退出后仍可证明的权威附着态？ | **本页投影。**关窗不删 owner 会话，行回到未打开；刷新后也不宣称 Chromium 仍在。权威附着态会扩 wire，不做。 |
| P1 | TTL 精确值和 idle/attached 刷新规则。 | **`TTLSeconds=7200`，idle 过期、本机已附着 Chromium 则续命。**与 spec「默认 2 小时空闲」一致。 |
| P2 | agentd 如何通过现有 target pool 为本机 SOCKS 提供任意上游 TCP：新增 pool-scoped raw `DialContext`，还是另造 owner 端点。 | **`targetclient.Pool` 上受池生命周期管理的 raw `DialContext`，复用 relay/direct。**这是既有隧道的内部拨号，不是给浏览器的新 HTTP 面，不退回 contract。禁止 HTTP/CONNECT 绕过、禁止浏览器直连 owner。 |
| P3 | owner session 表/Store 方法的命名与迁移位置。 | **复用 `internal/store.Store` 的 `handoff.db`。**触及 `d_orchestration` 只表示 SQLite 文件归属，不新增跨域 wire。 |
| P4 | preview mirror 是否独立于任务 `Mirror`，浏览器启动器是否按 OS 拆文件。 | **独立 preview mirror；launcher 集中接口 + OS 文件。**不复用任务 cursor。 |
| P5 | `via` 的匹配语法是否沿 spec 的 IP/CIDR/域名 allowlist 原样冻结。 | **原样沿用 spec。**不加通配符/正则/端口表达式。 |

P2 落成 `Pool.DialContext` 时必须绑 session/machine 生命周期，不能变成任意 TCP 代理。轻档维持：U0–U5 同一轮 implement 序贯，不扇出并行子卡。

## 1. 触及子系统与派卡资格

以下清单以 `codegraph/best.json` 的 `.domains` 中 `parent == null` 为准；类型为图中标记的 `logic`/`boundary`。`d_workspace`、`d_execution`、`d_sessions`、`d_ledger`、`d_policy`、`d_maintenance` 不在本稿实现文件集内：它们的既有 helper、进程/权限/项目事实只被消费，未发现需改其边界的文件。

| 子系统 id | 图类型 | B294 触及面 | 有界文件集 |
| --- | --- | --- | --- |
| `d_orchestration` | logic | owner session 的本机持久化、过期查询与删除 | `internal/store/store.go`、新增 `internal/store/previews.go` 及其测试；仅含 preview 表/方法，不改其它表语义 |
| `d_gateway` | boundary | owner API、事件、mirror 编排、SOCKS/PAC、Chromium 启停、Server 生命周期 | `internal/agentd/preview.go`、`server.go`、`cmd/agentd.go`，preview owner/mirror/proxy/browser 新文件及对应测试；不把全部 61 个 `internal/agentd` 文件作为范围 |
| `d_transport` | boundary | preview REST/WS 调用、owner machine 选路、P2 raw dial seam | `internal/client/preview.go` 及测试、`internal/targetclient/pool.go` 及 raw dial 测试；relay 只复用既有实现，若需改其契约须退回 contract |
| `d_protocol` | logic | DTO、JSON fixture、事件字面值的生产/消费断言 | `internal/proto/preview.go`、`internal/proto/preview_test.go`、`web/src/api/types.ts`、相关 fixture；字段不扩张 |
| `d_cli` | logic | `preview open/list/close` 输入校验与人话输出 | `cmd/preview.go` 及测试；`cmd/agentd.go` 只作为生命周期接线入口 |
| `d_web` | logic | 聚合列表、项目/机器归并、第四种 preview 行、搜索/计数/未归属、开窗本地态 | `web/src/api/preview.ts`、`ws.ts` 及测试，新增 `usePreviews`，`Shell.tsx`、`ProjectTree.tsx`、`counts.ts`、`search.ts` 及对应测试 |

### 1.1 四条派卡资格核对

1. **有界文件集：通过。**每个域已给出文件级范围；`internal/agentd` 虽是 61 文件平铺包，但功能面可圈为 preview 文件、`Server.Handler`/生命周期接线、mirror/proxy/browser 文件，故不插竖切还债卡。若实现中必须改出该范围，先停止并补竖切卡。
2. **契约可枚举：通过。**contract §3.1 的 50 条断言可逐条归属 U0–U5；P0/P1/P2 已按推荐项拍板，P2 落成 Pool 内部 DialContext，不新增浏览器可见 wire。
3. **依赖可排序：通过。**持久化/协议先于 owner，owner/transport 先于镜像和本机开窗，数据消费先于 web；见 §4 DAG。
4. **独立可验：有条件通过。**逻辑域有测试闭环；`d_gateway`、`d_transport` 的真实 Chromium、桌面、跨机器行为属于边界事实，见各卡验收与 §6「未验证，需真机」。

## 2. 契约增量核对

### 2.1 上游状态与边界记录

- `docs/superpowers/specs/b294.md` 头部状态为「已批准（2026-08-29，用户原话「批准」）」；本稿引用前已核对。
- `docs/superpowers/specs/b294-contract.md` 头部声明上游已批准、冻结物已冻结、交棒 breakdown；本稿引用前已核对。
- contract §1.3 已回写两项澄清：复用 `internal/store.Store` 会实际触及 `d_orchestration` 但不新增 wire；页面本地 `is-open` 与权威附着态必须区分。
- 没有把 `origin_url`、`branch`、机器失败、浏览器 ack 等现状字段重新解释成新契约；只有 owner 产生的冻结 DTO 和 event 是跨域载体。
- P2 的 raw upstream dial 能力尚未被现有 contract 精确冻结，故列为 contract gate；本稿不新增签名。P0 若选权威附着态、P1 若不选 7200，同样先退回 contract。

### 2.2 50 条冻结断言逐条归属

“保持”表示 implement 只能让现有 Ticket 0 壳替换为满足该断言的行为，不得改断言本身。§0 各项已拍板，implement 按裁决落地。

| # | 核对结论 | 归属 |
| ---: | --- | --- |
| 1 | 保持 `port` 为 JSON 整数 | U0 |
| 2 | 保持 `path` 为 workspace-relative 字符串 | U0 |
| 3 | 保持 `via` 为本会话 allowlist | U0 |
| 4 | 保持 `port/path` 二选一 | U0 |
| 5 | 保持 owner create 200 | U1 |
| 6 | 保持 create body 为 `PreviewSession` | U1 |
| 7 | 保持 owner `GET /api/previews` | U1 |
| 8 | 保持 owner 只列未关闭/未过期 session | U0/U1 |
| 9 | 保持 coordinator `?scope=all` | U2 |
| 10 | 保持远端 session 带 owner machine | U2 |
| 11 | 保持机器失败逐项进入 `machines` | U2 |
| 12 | 保持 `DELETE /api/previews/{id}` | U1 |
| 13 | 保持关闭体为 `PreviewCloseResp` | U1 |
| 14 | 保持 owner 为关闭权威写入方 | U0/U1 |
| 15 | 保持本机开窗 POST 路径 | U3 |
| 16 | 保持远端 machine 只在 query | U3 |
| 17 | 保持开窗体为 `PreviewOpenResp` | U3 |
| 18 | 保持 `GET /ws/previews` | U1/U2 |
| 19 | 保持文本 JSON `PreviewEvent` 帧 | U1/U2 |
| 20 | 保持 `preview.created` 字面值 | U0/U1 |
| 21 | 保持 `preview.closed` 字面值 | U0/U1 |
| 22 | 保持 close event 携带完整 session | U1/U2 |
| 23 | 保持 `entry_url` 由 owner 产生 | U0/U1 |
| 24 | 保持 `cwd` 为 owner 创建时工作目录 | U0/U1 |
| 25 | 保持缺省 `origin_url` 在线格式缺席 | U0 |
| 26 | 保持缺省 `branch` 在线格式缺席 | U0 |
| 27 | 保持 `created_at` 为 RFC3339 | U0 |
| 28 | 保持 `ttl_seconds` 为整数秒 | U0；P1 门 |
| 29 | 保持缺省 `via` 键缺席 | U0 |
| 30 | 保持缺省 `via` 投影为 owner loopback 全端口 | U1/U3 |
| 31 | 保持非空 `via` 只扩展本 session allowlist | U0/U3 |
| 32 | 保持 owner 响应不写 coordinator machine | U1 |
| 33 | 保持 coordinator 只投影 machine、不回写 owner truth | U2 |
| 34 | 保持 web 默认 `scope=all` | U4 |
| 35 | 保持浏览器 WS URL 不带 machine query | U4 |
| 36 | 保持 CLI open 调用 `CreatePreview` | U5 |
| 37 | 保持 CLI list 调用 `ListPreviews` | U5 |
| 38 | 保持 CLI close 调用 `ClosePreview` | U5 |
| 39 | 保持 owner 成功后 CLI 不等待 Chromium ack | U5 |
| 40 | 保持 preview stream 不使用任务 cursor | U2 |
| 41 | 保持 preview stream 不负责重连 | U2 |
| 42 | 保持 REST 复用 `Client.do` | U2 |
| 43 | 保持 WS 复用 `Client.wsDialOptions` | U2 |
| 44 | 保持浏览器不直连 owner | U3/U4 |
| 45 | 保持 owner unreachable 不冒充本机 session | U2 |
| 46 | Ticket 0 五 handler 的 503 只作为基线，不是实现验收 | U1 |
| 47 | Ticket 0 不创建 workspace HTTP listener；实现可使用 owner 内部服务但不得改变冻结路由形状 | U1/U3 |
| 48 | Ticket 0 不启动 Chromium；实现只在明确 open 动作启动 | U3 |
| 49 | Ticket 0 不生成 PAC/SOCKS nonce；实现 open 需生成受生命周期管理的临时能力 | U3 |
| 50 | 保持既有 `tui|terminal|file` TaskRow/workbench closure 不回归 | U4 |

结论：P0/P1/P2 已拍板。未发现需要新增跨子系统 DTO、路由或事件。P2 落成既有隧道上的 Pool.DialContext，不另造 owner HTTP 端点。

## 3. 产品行为闭环

只核 spec 承诺的跨子系统可观察行为，不为内部 helper 单独造闭环。

| 触发者 | 权威事实/载体 | 消费者 | 可观察结果 | 归属子卡 |
| --- | --- | --- | --- | --- |
| CLI 用户执行 `preview open --port/path [--via]` | owner `PreviewSession` 持久化记录与 `preview.created` | CLI、coordinator mirror、web tree | CLI 得到冻结人话；对应项目任务组出现第四种 preview 行 | U0/U1/U2/U4 |
| 用户点击远端 preview 行 | owner session + `OpenPreview` machine query；本机 Chromium/PAC/SOCKS | 本机 agentd launcher 与浏览器 | 当前桌面打开隔离 Chromium，地址仍为 owner 的 `localhost:<port>` | U3/U4 |
| 浏览器访问 preview localhost 资源 | 本机临时 SOCKS/PAC allowlist 与 owner upstream dial | Chromium 网络栈 | allowlist 内请求到 owner loopback；不在 allowlist 的请求按冻结规则拒绝/直连；本机同端口不被占用 | U3 |
| owner 关闭、TTL 到期或 owner 不可达 | owner store 状态、`preview.closed`、machine failure | mirror、web tree | session 从 owner/聚合列表消失或进入可行动失败信息；不被伪造为本机 session | U0/U1/U2/U4 |
| 用户搜索项目名、机器名或端口 | session 的 `origin/project_id`、machine、entry URL port | web tree search/filter | 命中对应 preview 行；未匹配 origin 的 session 进入 unowned | U4 |
| 用户关闭浏览器/agentd 重启 | launcher 的 child/process state 与 owner session TTL | agentd cleanup、owner store | 浏览器关闭不直接删除 owner session；agentd 重启不遗留临时 proxy/process，TTL 最终负责回收 | U1/U3 |
| web WS 断线后重连 | owner list + preview event stream（list-before-WS） | web preview hook/tree | 先补齐当前列表再订阅，不依赖任务 cursor，避免重复/漏掉可见 preview | U2/U4 |

上述每行均已指向存在的子卡；没有只活在接口、测试或无人认领格子里的产品承诺。若 P0 选择权威附着态，第二、六行的权威载体缺失，必须退回 spec/contract 后才可继续。

## 4. 依赖 DAG 与单轮顺序

```text
U0 protocol + owner store
        |
        v
U1 owner API/events/lifecycle -----> U2 mirror + transport
        |                                  |
        +---------------> U3 proxy/browser-+
                         |
                         v
                    U4 web tree
                         |
                         v
                    U5 seam/regression review
```

- U0 先冻结本机数据边界和 JSON roundtrip；U1 才能实现 owner 权威与 shutdown 顺序。
- U2 复用 U1 的 list/event 事实并验证现有 HTTP/WS 选路；U3 依赖 P2 的 raw dial 决策。
- U4 只消费冻结 web API/WS 与 U2 投影，不把 preview 塞进 `OpenItem`/workbench。
- U5 是同一轮实现后的串行跨域回归，不是并行派发卡。
- L3 轻档固定不扇出并行子卡；若 P2 或 Chromium 平台差异使工作量超过一轮固定成本，协调者应另行拍板升重档，而不是本稿暗中拆分。

## 5. 子卡清单

### U0 — 协议与 owner session 持久化（`d_protocol` logic + `d_orchestration` logic）

#### ①契约引用

引用 `b294-contract.md §2.1` 的 `PreviewOpenReq`、`PreviewSession`、`PreviewListResp`、`PreviewEvent`、`PreviewCloseResp`，§3.1 #1–4、#8、#14、#20–31、#46–49，以及 §3.2 A。P1 的 TTL 数值必须先拍板；P3 采用现有 Store 只是提案。

#### ②意图与为什么

让 owner session 成为单一权威、可在重启后恢复查询的本机事实，并让 JSON 线格式能区分字段缺失与零值。路径/端口/via 的校验和 session 生命周期必须在 owner 端形成可测试的规则，不能把安全边界留给 web 或 CLI。复用 `handoff.db` 可沿现有 Store 生命周期收口，不生成 coordinator 中央 ledger。

有界文件集：`internal/store/store.go`、新增 `internal/store/previews.go` 及测试，`internal/proto/preview.go`、其测试和 Go/TS fixture；如需改现有非 preview 表或协议字段，越界并退回 contract。

#### ③验收

- 逻辑验收：Go 单测以 table-driven 输入覆盖 port/path XOR、workspace-relative realpath、via allowlist、RFC3339/整数 TTL、缺省可选字段；非法输入返回可行动错误，且没有持久化半成品。
- 序列化边界：运行 `go test ./internal/proto ./internal/store` 与 fixture roundtrip/property test；至少断言 `via`/`origin_url`/`branch` 的“缺失”不等于零值/空字符串，`encode(decode(x))` 保留可观察字段。
- 生命周期/状态机中断：**有防护，因为** Store 方法先写 session 状态再发事件，启动恢复只加载未关闭未过期记录；宿主在写入/清理中断时由 SQLite 事务和下次启动扫描收口，孤儿 session 由 owner TTL 扫描负责。
- 静默失败/误导报错：**有防护，因为** port/path/via、store 写失败和已关闭/过期分别返回可行动错误；事件只在权威写入成功后发送，不允许返回成功而无记录。
- 跨平台假设：**有防护，因为**路径判定使用既有 workspace root/realpath 语义，不拼接平台分隔符；SQLite 时间用 UTC；外部浏览器和 OS 行为留给 U3 真机，不在 U0 假绿。
- 假红/假绿测试：**有防护，因为**测试穿过真实 JSON 编解码和 Store 读写，并对非法 XOR、字段缺失、零值作反向断言；不锁内部函数名，只锁调用方依赖的 session 事实。并发 TTL 测试需验证同一记录不会被重复关闭。
- 门禁绕过：**有防护，因为** owner API 后续只能调用 U0 的校验/Store 入口，coordinator 没有本机删除远端记录的 Store 权限；状态检查与写入在同一事务内，避免关闭/过期 TOCTOU。
- 枚举新值白名单：**无，因为**本卡不新增枚举；`preview.created/closed` 是已有冻结字面值，U1/U2 仍需在消费处逐一登记。
- 承重安全属性：**有防护，因为** session ID 唯一性、owner-only close、过期不可重新列出各有能变红的 Store 测试，不只依赖实现偶然成立。

#### ④入口指针

`internal/store/store.go`、`internal/proto/preview.go`、`docs/superpowers/specs/b294-contract.md §2.1/§3.1`、`internal/agentd/workspace.go` 的既有 workspace/path helper（只消费，不扩大其文件集）。

### U1 — owner API、事件与生命周期接线（`d_gateway` boundary）

#### ①契约引用

引用 §2.2 五条 route、Ticket 0 `Server.Handler` 注册、§3.1 #5–8、#12–14、#18–24、#30–33、#46–49，以及三重闸门 A。`internal/agentd/server.go#Server.Handler` 是现有入口锚。

#### ②意图与为什么

把 HTTP/WS 空壳替换为 owner 权威 API：create/list/close/open/stream 的成功和失败传播要一致；事件顺序必须先有持久化事实再通知消费者；agentd 关停、重启、owner unreachable 不能遗留 goroutine、临时目录或假 session。open 只接受明确动作，不在 create/CLI 成功时自动开窗。

有界文件集：`internal/agentd/preview.go`、`internal/agentd/server.go` 的 preview route/lifecycle 接线、新增 preview owner/lifecycle 文件及其测试、`cmd/agentd.go` 的启动/停止接线。不要修改其它 task route 语义。

#### ③验收

- 逻辑/边界验收：Go handler 测试逐条跑 POST/GET/DELETE/WS，断言状态码、JSON body、完整 close event、owner machine 规则和 503/4xx/5xx 错误；测试不得只检查 handler 返回 nil。
- 生命周期/状态机中断：**有防护，因为** preview supervisor 的 stop 顺序先停事件/镜像/代理，再关闭 Store；agentd 重启会恢复未过期 owner session，子进程/临时目录由 launcher cleanup 和 TTL 扫描回收。宿主中断后的跨机器可达性仍“未验证，需真机”。
- 静默失败/误导报错：**有防护，因为** store、path/port/via、owner close、machine forward 和 WS 建立失败都沿既有 `Client.httpError`/JSON error 传播，create 不在事件未落库时报成功；owner unreachable 不转成本机成功。
- 跨平台假设：**有防护，因为** handler 不假设桌面 OS、默认浏览器或固定路径；工作目录、UTC 时间和 machine 选择使用既有 helper，Chromium/PAC 平台差异由 U3/真机清单承接。
- 假红/假绿测试：**有防护，因为** handler 测试通过真实 HTTP route 和 JSON 编解码，反向断言错误 body、重复 close、过期 session 和 WS 非 JSON 帧；重启/网络断线的实际通路“未验证，需真机”，不能用内存 fake 代替结论。
- 门禁绕过：**有防护，因为**所有写动作经 owner auth/forward policy 和 owner session ID 校验，coordinator `scope=all` 只读；关闭的检查和事务写入在 owner 内完成，避免并发双关/越权删除。
- 序列化边界：**有防护，因为** create/list/close/WS 均使用冻结 proto 类型，U0 fixture 测试覆盖缺失与零值；不得手写 map 漏掉 `ttl_seconds`、`via` 或 machine 投影。
- 枚举新值白名单：**有防护，因为** producer 只发冻结的 `preview.created`/`preview.closed`，测试会对未知 event type 拒绝或显式忽略；新增 type 必须同时改 producer/consumer 白名单并回 contract。
- 承重安全属性：**有防护，因为** owner-only close、session ID 唯一、事件仅一次提交后发布均有并发/重放测试；若不能写出能变红的测试，不得宣称安全属性成立。

#### ④入口指针

`internal/agentd/preview.go`、`internal/agentd/server.go#Server.Handler`、`cmd/agentd.go`、`internal/agentd/mirror.go`（只参考现有生命周期顺序）、`internal/store/store.go`。

### U2 — transport client 与 coordinator preview mirror（`d_transport` boundary + `d_gateway` boundary）

#### ①契约引用

引用 §2.2 聚合 list/WS、§2.3 `Client` preview 方法、§3.1 #9–11、#18–22、#32–33、#40–45，以及 §3.2 A。P2 raw dial 不在本卡擅自新增；若 U3 需要它，先由协调者裁决 P2。

#### ②意图与为什么

让 coordinator 按 owner machine 发现、list、订阅和重连 preview，而不是复制任务 cursor 或创建另一条直连通路。断线先 list 后 WS，机器失败逐项记录，远端 session 只作本机只读镜像；REST/WS 必须复用现有 client 认证、timeout、relay 选路。

有界文件集：`internal/client/preview.go` 及测试、`internal/targetclient/pool.go`（仅当 P2 选择 raw dial 时）及测试、新增 preview mirror 文件及测试；不修改任务 `Mirror` 的 cursor/事件语义。

#### ③验收

- 逻辑/边界验收：Go 集成式 fake owner 测试跑 list-before-WS、created/closed、重复事件、重连、owner failure；断言 `machines` 错误可定位、远端 machine 不被冒充成本机、REST 使用既有 `do`、WS 使用既有 `wsDialOptions`。
- 生命周期/状态机中断：**有防护，因为** mirror supervisor 在 agentd shutdown 时取消 owner goroutine/WS，再清本机投影；重连使用 bounded backoff，不产生无界 goroutine。跨机器网络恢复的真实时间顺序“未验证，需真机”。
- 静默失败/误导报错：**有防护，因为** owner list/WS 错误保留 machine 和错误原因，失败不会返回空的“全部正常”列表；部分 owner 成功与失败都可观察，不能将旧镜像伪装成实时。
- 跨平台假设：**有防护，因为** transport 沿现有 relay/direct 选路，不在 mirror 拼 OS 路径或桌面调用；machine 名只作 owner 选择，OS 浏览器边界由 U3。
- 假红/假绿测试：**有防护，因为**测试经过实际 `Client` REST/WS 边界并有反面断言“断线先 list、不能用 task cursor、不能冒充本机”；fake owner 只验证协议形状，网络通路/机器发现仍“未验证，需真机”。
- 门禁绕过：**有防护，因为** mirror 只调用只读 list/stream，close 不走 coordinator 本地 Store；`machine` 选择与 owner auth 使用同一既有 forward policy。P2 raw dial 若开放，必须绑定 Pool 生命周期和目标 machine，不能成为任意未授权拨号入口。
- 序列化边界：**有防护，因为** Go client、WS 文本帧、list 聚合和 `machines` 投影各有真实 decode/encode 断言，缺失字段与零值分开测试；禁止两端各自绿却漏掉 mirror projection。
- 枚举新值白名单：**有防护，因为** mirror 登记两个冻结事件字面值并为未知值留下明确忽略/错误测试；不新增 event kind。
- 承重安全属性：**有防护，因为**同一 owner/session 的重复 created/closed 是幂等测试，owner failure 不会生成本机 truth；P2 raw dial 的隔离/唯一连接若无可变红测试则不得落实现。

#### ④入口指针

`internal/client/preview.go`、`internal/client/client.go` 的 `Client.do`/WS 选路、`internal/targetclient/pool.go`、`internal/agentd/mirror.go`、`internal/proto/preview.go`。

### U3 — 本机 SOCKS/PAC 与隔离 Chromium launcher（`d_gateway` boundary + `d_transport` boundary）

#### ①契约引用

引用 spec 的 owner loopback 投影、安全 allowlist、独立 user-data-dir、`--proxy-bypass-list=<-loopback>`、不本地同号 bind、不 iframe/pixel projection；contract §2.2 open、§3.1 #15–17、#30–31、#44、#47–49，以及 §3.2 B/C。P2 是本卡实现前置 gate。

#### ②意图与为什么

点击桌面才在当前执行机启动/聚焦独立 Chromium；浏览器地址保持 owner 的 `localhost:<port>`，网络由本机受限 SOCKS/PAC 投影到 owner loopback。这样既不占本机同号端口，也不把网页内容经过 agentd HTTP/iframe；关闭浏览器不删除 owner session。代理、PAC、nonce、child process、临时目录必须绑定 session 和 shutdown 生命周期。

有界文件集：新增 preview proxy/browser/launcher 文件及测试、`internal/agentd/preview.go` 的 open 接线、必要的 `internal/targetclient/pool.go` P2 seam 及测试、`cmd/agentd.go` stop hook；OS-specific launcher 文件仅包含 executable discovery/args/cleanup，不扩张到通用 console 打开器。

#### ③验收

- 边界验收：机内可验 PAC/SOCKS 字节规则、allowlist、nonce/独立 profile 参数、无本地 owner-port listener；真实“点击哪个桌面/Chromium 实际加载 owner loopback 页面”必须标为“未验证，需真机”。
- 生命周期/状态机中断：**有防护，因为** launcher 持有 child、proxy listener、PAC/临时 profile 的 cleanup；agentd 重启先终止 child/proxy，再关闭 Store；异常退出由 supervisor/TTL 扫描收尾。进程组信号和各 OS 实际收尸“未验证，需真机”。
- 静默失败/误导报错：**有防护，因为** Chromium executable 不存在、profile 创建失败、raw dial/allowlist 拒绝都会返回可行动错误且 `Opened:false`，绝不把“命令已 fork”当页面已打开；CLI 仍只承诺 owner create 成功，不伪造 Chromium ack。
- 跨平台假设：**有风险，需真机清单，因为** executable 名称、进程组、临时目录权限、桌面会话和代理参数在 Linux/macOS/Windows 不同；OS adapter 必须显式列出，不能用系统默认 `open/xdg-open/rundll32` 替代 Chromium 隔离启动。
- 假红/假绿测试：**有防护，因为**单测反向断言不绑定本机 owner port、不启动默认浏览器、不允许 loopback bypass 绕过 allowlist，并检查 child/proxy cleanup；夹具不能证明真实网络路径，浏览器加载、DNS、同号端口和进程组列入真机。
- 门禁绕过：**有防护，因为**所有 CONNECT/域名/IP 请求先经 session allowlist、loopback-only owner policy 和一次性 nonce；P2 raw dial 必须只由已授权 owner session 获得，不能被任意本地端口复用。检查与拨号需在同一代理请求路径内，避免 TOCTOU。
- 序列化边界：**无新增跨 wire 字段，因为**PAC 是本机生成的派生文本，session 仅传冻结 `entry_url/via/ttl`；PAC 生成测试仍需区分缺省 via 与空 allowlist，不能把默认全端口误编码成拒绝全部。
- 枚举新值过白名单：**无，因为**本卡不增加协议 event/kind；浏览器状态只作内部有限状态，若要写入 `PreviewEvent` 必须退回 contract。
- 承重安全属性：**有防护，因为**nonce 一次性、profile 隔离、不同 session 不共享浏览器数据目录各有能变红测试；raw dial 跨 session 隔离也必须有并发测试。真实安全边界“未验证，需真机”。

#### ④入口指针

`internal/agentd/preview.go`、`internal/targetclient/pool.go`（P2 gate）、`internal/relay/dialer.go`（复用现有 relay 事实）、新增 preview proxy/browser/launcher 文件、`cmd/agentd.go`。

### U4 — web 聚合树与第四种任务行（`d_web` logic）

#### ①契约引用

引用 §2.4 web types/API/WS，§3.1 #9–11、#22、#33–35、#50，spec 左栏行为与三重闸门 C。preview 是 task-group 的第四种产品行，但不是 `OpenItem`、workbench tab 或现有任务 MIME。

#### ②意图与为什么

将聚合 preview session 按 `origin/project_id` 归入项目/机器树；机器失败和 unmatched origin 保持可见；搜索覆盖项目名、机器名、端口，活动 preview 计入项目 in-progress；点击只调用 `openPreview` 并在当前页面更新短暂 `is-open`，不调用 `onOpenTask`、不拖放、不选中 workbench。

有界文件集：`web/src/api/preview.ts`、`web/src/api/ws.ts`、新增 `web/src/app/data/usePreviews.ts` 及测试、`web/src/app/shell/Shell.tsx`、`web/src/app/tree/ProjectTree.tsx`、`counts.ts`、`search.ts` 及对应测试。若必须改 `OpenItem` 的 kind 闭集或 workbench reducer，先回到 contract/spec；默认不改。

#### ③验收

- 逻辑验收：运行 web 单测/类型检查，断言 list 默认 `scope=all`、WS URL 无 machine、created/closed 后树投影更新；渲染测试断言 preview 行显示 `localhost:<port>`/`branch · localhost:<port>`、machine green dot/name、普通/is-open 两态。
- 生命周期/状态机中断：**有防护，因为** hook 在 unmount/WS 断线时取消订阅并 list-before-WS，closed/expired 从投影移除；浏览器进程真实回收不由 web 假定，列入 U3 真机。
- 静默失败/误导报错：**有防护，因为** open API 失败显示可行动错误并不切换 `is-open`，machine failure 不变成空树；普通/is-open 不是 `is-selected`，不能以视觉状态伪造 Chromium 已加载。
- 跨平台假设：**无新增 web 平台假设，因为**浏览器 URL/OS 启动均由本机 agentd；web 只使用同源 `/api`/`/ws` 与冻结字段。不同桌面实际开窗“未验证，需真机”。
- 假红/假绿测试：**有防护，因为**测试锁调用方行为：preview click 不触发 `onOpenTask`、不产生 drag MIME、不进入 workbench；反向测试 stale/closed/unowned/search/port/count。fake API 不能证明 Chromium，真机项单独列出。
- 门禁绕过：**有防护，因为**web 不提供 close 作为本机删除远端的替代入口，open 只带 machine query 并经同源 agentd；点击并发去重/失败回滚，不能把 UI state 当权限。
- 序列化边界：**有防护，因为**真实 API fixture→TS decode→tree projection 测试覆盖 optional `via/origin_url/branch` 缺失与零值端口；不手搭遗漏 `machine/machines` 的 map。
- 枚举新值过既有白名单：**有防护，因为**`TaskRow` 的 preview kind 只用于左栏专用分支，明确不改 `OpenItem` 的 tui/terminal/file 白名单；`preview` 若流过 tree kind/switch，测试覆盖其登记。
- 承重安全属性：**有防护，因为**同一 session 的重复 event 去重、不同 machine 同 id 隔离、open loading 防重入各有能变红测试；本地 `is-open` 不宣称权威附着，P0 选择后再补对应 wire/test。

#### ④入口指针

`web/src/api/preview.ts`、`web/src/api/ws.ts`、`web/src/app/shell/Shell.tsx`、`web/src/app/tree/ProjectTree.tsx`、`web/src/app/tree/counts.ts`、`web/src/app/tree/search.ts`、各现有 `*.test.tsx`/`*.test.ts`。

### U5 — 单轮跨域回归与交棒核对（serial verification unit）

#### ①契约引用

覆盖 contract §3.1 全部 #1–50、spec 的七行行为闭环、三重闸门 A/B/C，以及本稿 §0 的所有已裁决项。U5 不添加实现 API；它是实现完成后的跨边界验收单元。

#### ②意图与为什么

把真实序列化、owner authority、mirror 重连、点击开窗、网络 allowlist、web 行为和 shutdown 组合起来验证，专门抓“每个子系统各自绿、接缝仍断”的缺陷。它不把夹具世界写成真机结论；跨机器、桌面、Chromium、DNS/进程组事实明确交给协调者执行真机清单。

有界文件集：现有 Go/web 集成测试入口与新增 B294 regression fixture/测试文件；不为 U5 改生产代码，不创建新的服务或 ledger。

#### ③验收

- 逻辑验收：执行项目规定的 Go preview/store/client/agentd 测试、web preview/tree 测试、typecheck 与 fixture roundtrip；每条输出需能定位到 #1–50 或 §3 行。
- 生命周期/状态机中断：**有防护，因为**回归必须包含 owner/agentd 重启、WS 重连、proxy/Chromium cleanup 的机内可模拟路径；子进程真实行为“未验证，需真机”。
- 静默失败/误导报错：**有防护，因为**回归包含 owner 写失败、owner unreachable、open 失败、close 重复、机器部分失败的反向断言；没有“HTTP 200 即页面可用”的断言捷径。
- 跨平台假设：**未在机内证明，因为**真实浏览器、桌面会话、OS 进程组、网络 namespace 和 DNS 不是 fixture 能代表；全部转入 §6 真机清单，三平台结果不能用单平台推断。
- 假红/假绿测试：**有防护，因为**回归穿过真实 JSON/HTTP/WS/TS projection 边界，并验证调用方行为而非内部 helper；同时保留反面断言和并发/重放用例，未执行的命令不写成通过。
- 门禁绕过：**有防护，因为**回归检查 owner-only close、machine 选择、allowlist、一次性 nonce、profile 隔离和并发窗口；任何安全属性没有能变红测试则回退，不以审阅代码替代。
- 序列化边界：**有防护，因为**至少一条测试贯穿 Go DTO→真实 WS/HTTP 文本→TS fixture/projection，区分 optional 缺失与零值；不能用“两端各自单测”代替。
- 枚举新值过白名单：**有防护，因为**回归枚举事件与 preview row kind 的每个生产者/消费者/switch；未知值的行为有明确断言。
- 承重安全属性：**有防护，因为**回归要求唯一 session ID、一次性 nonce、跨 session 隔离、owner authority 各有可变红测试；任何只在实现中“恰好为真”的属性判为未完成。

#### ④入口指针

`docs/superpowers/specs/b294.md`、`docs/superpowers/specs/b294-contract.md`、U0–U4 的测试入口、项目已有 Go/web test scripts。该单元只串行核验，不派发并行 executor。

## 6. 未验证，需真机：协调者执行清单

以下均依赖真实行为事实，不能以 fake、fixture 或 grep 结论替代：

1. 在真实 owner machine 创建 port/path preview，确认 `entry_url` 的 localhost 端口实际由 owner workspace 服务提供；本机同号端口已被占用时仍不被 agentd bind/抢占。
2. 从另一台真实桌面点击对应第四种 preview 行，确认 Chromium 在点击桌面启动/聚焦、使用独立 user-data-dir，地址栏仍为 owner `localhost:<port>`，不是 iframe、默认系统浏览器或 workbench tab。
3. 访问 `via` 内 loopback/IP/CIDR/domain 与未列入目标，确认 PAC/SOCKS allowlist、`<-loopback>` bypass 和 non-loopback CONNECT 规则按 spec 生效；确认 DNS 解析路径没有把目标泄漏到本机直连。
4. 关闭浏览器、agentd 重启、owner 重启、网络断开/恢复，逐项观察 child、进程组、SOCKS/PAC 临时目录、profile、session、mirror 是否按责任方和 TTL 回收；确认“浏览器关闭不删除 owner session”。
5. 跨 Linux/macOS/Windows（若项目支持）验证 Chromium executable discovery、权限、桌面 session、进程组终止、临时目录和代理参数；任何单平台结果不得外推。
6. 真实 owner unreachable 时，确认 web 显示机器失败/旧 session 的可行动状态，不显示为本机 truth；恢复后 list-before-WS 能补齐，不重复创建。
7. 并发点击同一/不同 session，确认每 session nonce、profile、proxy、upstream 隔离，close/TTL 与 open 不发生 TOCTOU 或双重回收。

## 7. 图与覆盖债

- `codegraph/best.json` 已提供本稿 §1 的顶层域依据；旧 `codegraph domains` 输出的是 `view: baseline` 的 20 域树，和 best 的平铺顶层结构不一致，本稿按纪律以 best 为准。
- 已实际运行 `codegraph check --view cards-B294-charter-2`，失败原文为：`Error: 视图 cards-B294-charter-2 引用不完整: [新增节点 n_web_api_preview_closePreview 引用不存在的容器 k_web_api_preview 新增节点 n_web_api_preview_createPreview 引用不存在的容器 k_web_api_preview 新增节点 n_web_api_preview_fetchPreviews 引用不存在的容器 k_web_api_preview 新增节点 n_web_api_preview_openPreview 引用不存在的容器 k_web_api_preview]`。因此不能把图视图宣称为通过；这是进入图对账节点前的覆盖债。
- 已实际验证 `internal/agentd/server.go#Server.Handler` 可由图解析；部分 `Pool.For`、`agentd.Mirror`、`Client.StreamEventsOnce` 查询未在当前图中命中，入口指针保留文件/contract 事实，不把未解析符号写成已验证图事实。
- 本稿完成后必须运行 `codegraph resolve --doc docs/superpowers/specs/b294-breakdown.md`。若失败，必须以原始输出修正坏锚；不能用“图未覆盖”替代修复。

## 8. 交棒与收口自检

- 四样产出齐全：域清单带 logic/boundary 类型；50 条契约逐条核对；U0–U5 四段式子卡与 DAG；每张相关子卡逐族回答缺陷族。
- 所有岔口集中在 §0 并已拍板（2026-08-29）。
- §6 汇总所有“未验证，需真机”行为事实。
- 每张子卡文件集有界；`internal/agentd` 大包已完成范围核对，无需竖切还债卡。
- §3 每行具备触发者、权威事实/载体、消费者、可观察结果、归属子卡五格。
- 本稿与 contract §1.3、台账应同批提交；提交后 final verdict 只能依据实际 commit/命令结果，不把未来实现测试写成通过。
