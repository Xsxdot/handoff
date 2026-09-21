# B374 契约增量：房间列表强制分页 + attach 刷新限域 + 日志三件套

**上游状态：已批准**（源 spec `docs/specs/2026-09-15-b374-rooms-pagination.md`，头部状态行「已批准（2026-09-15，用户审批）」——本节点开工核对通过；spec 已纳入本工作分支）
**级别：L3 轻档**　**卡：B374**　**有效基线：未设置**（工作分支 `cards/B374-charter-3`，自既有冻结提交 `5b00e2b5` 续接；不假定 main）
**冻结状态：本提交随 `codegraph/diffs/cards-B374-charter.json` 与 Ticket 0 骨架同批冻结；`codegraph/target.json` 本轮无口径增量（见 §4）**
**架构形态：按子系统分域的平铺领域包，无横向 controller/service/dao 分层**（沿用 `codegraph/best.json`）
**本轮性质：contract 重冻（用户拍板 A）**——上一轮自裁 pass 被驳回，本次从 `5b00e2b5` 续接修订冻结物与骨架接线，不推倒重来、不开始 implement
**台账：** `docs/superpowers/ledgers/2026-09-15-b374-contract-ledger.md`

本契约冻结三件事的接缝语义与精确签名：`GET /api/rooms` 强制分页 wire、attach 刷新限域、日志三件套。Ticket 0 只落类型/常量/签名与直通镜像接线（编译通过）；分页裁剪、legacy 阻断、日志默认级别与轮转的实现归 implement 轮。L3 轻档无直通竖切（纪律块「轻档与 L2 无此步骤」），运行时最薄路径由 plan 节点承接。

## 0. 本轮勘误（相对上一轮冻结）

1. **子系统 ID 以 `codegraph/best.json` 为准**：协作房间是 **`d_collab`**，**不是 `d_sessions`**（`d_sessions` 是终端 PTY 回放域）。spec 与台账里的误写已勘误回写（spec 已加备注；台账续写勘误条目），不重开 spec 语义。
2. **logx 在 `d_policy`（`k_logx_fn`/`k_logx_model`/`k_logx_multiHandler` → `d_policy`）**，不是 `d_runtime_config`（该域在 `best.json` 中不存在，是 baseline 扫描残留的旧域 id），也不是 `d_maintenance`。上轮 §3.4 标题误写 `d_maintenance / d_runtime_config`，本轮改为 `d_policy`。
3. **HTTP 错误映射必须与冻结清单一致**：F4 要求非法游标 → HTTP 400。Ticket 0 的 `handleRoomsList` 上一轮把 `ListRoomsPage` 的**全部** error 写成 500；本轮改为用可识别哨兵 `collab.ErrInvalidCursor` 区分——游标非法 400、列表组装失败 500，并加能变红的测试（见 §3.3、§5-F10）。
4. **禁止用注释把没做的事写成已做**：`handleRoomsList` / `enrichRoomAttachments` 上一轮注释声称「刷新已限域」，与现状（全量 `AllTaskLinks` + `startRoomAttachRefresh(全量 links)`）不符，本轮删改；F17/F18 仍归 implement。
5. **游标兜底必须承认 `listRooms` 的排序事实**（见 §3.2 规则 3）。
6. **`decodeRoomCursor` 必须校验 `a`/`r`**：缺键、类型不符 → error（F4 覆盖），`{}` 不得当合法游标。
7. **落地顺序写入拍板记录**（P5）：必须先落地 Legacy 探测 + HTTP 426，再落地 `trimRoomPage` 真裁剪。
8. **图视图勘误**：`k_collab_model` **相对 `baseline.json` 补齐**（`best.json` 已有该容器、`baseline` 尚无，属 absorb 未回灌的覆盖债），不再写成「scanner 新引入」。

## 1. 现状查证与边界

下表是逐项对现状代码的查证；`file#Symbol` 是代码事实锚（行号只是本轮读数，会漂）。

| 接缝 | 现状代码事实 | 本卡冻结后的精确形状 |
| --- | --- | --- |
| 列表 handler | `internal/agentd/roomsapi.go#Server.handleRoomsList`（`:93`）：`func (s *Server) handleRoomsList(w http.ResponseWriter, r *http.Request)`；现体经 `parseRoomsListParams` → `ListRoomsPage` → `enrichRoomAttachments` → `writeJSON(w, 200, page)`；错误按 `roomsListErrorStatus` 分流 | 签名不变；错误映射冻结为「`ErrInvalidCursor`→400，其余→500」（F10） |
| 参数决议 | `internal/agentd/roomsapi.go#parseRoomsListParams`（`:79`）：现状**空壳**——恒返回 `{Limit: roomsListDefaultLimit}`、`Legacy=false`，不读 `*http.Request` | 签名不变；真解析归 implement（F5–F9） |
| 错误分流 | `internal/agentd/roomsapi.go#roomsListErrorStatus`（`:123`）：`func roomsListErrorStatus(err error) int`——本轮新增，`errors.Is(err, collab.ErrInvalidCursor)`→400 否则 500 | 签名不变；本轮起即为真实映射（有测试锁） |
| attach 投影 | `internal/agentd/roomsapi.go#Server.enrichRoomAttachments`（`:151`）：`func (s *Server) enrichRoomAttachments(_ context.Context, rooms []proto.RoomSummary)`；内部读**全量** `AllTaskLinks()` 并把全量 links 传 `startRoomAttachRefresh` | 签名不变；加「只对本页 rooms 的 links」限域后调 `startRoomAttachRefresh`（F17/F18，归 implement） |
| attach 刷新 | `internal/agentd/roomsapi.go#Server.startRoomAttachRefresh`（`:223`）：`func (s *Server) startRoomAttachRefresh(links []ledger.TaskLink)`；对入参全量远端挂账 fan-out（workers=16） | 签名不变；调用方收窄入参即限域（刷新体不改，F18 归 implement） |
| 列表组装 | `internal/collab/service.go#Service.ListRoomsForMember`（`:316`）：`func (s *Service) ListRoomsForMember(project, member string) ([]proto.RoomSummary, error)`；`internal/collab/service.go#Service.listRooms`（`:404`）全量组装 | `ListRoomsForMember` **签名保持不变**（全量，CLI/既有测试兼容）；新增 `ListRoomsPage`（见 §3.2） |
| 列表排序 | `internal/collab/service.go#Service.listRooms`：`:501-513` active（活动降序，Stable）在前、sunk（终态）沉底，各自内部活动降序，`append(active, sunk...)` | 分页在 **该扁平序** 上做；游标语义见 §3.2 |
| 游标哨兵 | `internal/collab/service.go#ErrInvalidCursor`（`:53`）：`var ErrInvalidCursor = errors.New("collab: 分页游标非法")`——本轮新增，定义在 collab 根包（游标是本包 wire 概念，非 room 执法内核） | 签名不变；F10 的 400 分支据此识别 |
| 游标编解码 | `internal/collab/service.go#encodeRoomCursor`（`:354`）/ `#decodeRoomCursor`（`:366`）/ `#roomCursor`（`:347`） | `decodeRoomCursor` 校验 a/r 缺键与类型（本轮已落，F10） |
| 页裁剪 | `internal/collab/service.go#trimRoomPage`（`:397`）：直通镜像——返回整表、`hasMore=false`、`next=""` | 真裁剪归 implement（F12–F16） |
| 日志 | `internal/logx/logx.go#Setup`（`:31`）：`func Setup(component, logPath string) *slog.Logger`；`:33` stderr TextHandler + `:36` 文件 JSONHandler 双挂 | 签名不变；带 `logPath` 时同一记录只落一处（F20，见 §3.4） |
| 级别默认 | `internal/logx/logx.go#parseLevel`（`:46`）：默认分支 `return slog.LevelInfo` | 默认改 `slog.LevelWarn`（spec 语义3，F19） |
| 双写来源 | `internal/service/launchd.go#plistBody`（`:118-119`）：`StandardOutPath` 与 `StandardErrorPath` 同值；值来自 `cmd/service.go#resolveSpec`（`:83`） | **不改 manager**；单写由 agentd 侧修（拍板 P2） |
| systemd / windows | `internal/service/systemd.go#unitBody`（`:76-101`）不写 StandardOutput/Error；`internal/service/windows.go#taskXML` 不重定向 stdout/stderr | 保持；本轮不动这两个平台 |
| wire 消费方 | `web/src/api/rooms.ts#fetchRooms`（`:85`）：`request<{rooms: RoomSummary[]}>(...)` 只解包数组 | wire 对面；新签名见 §3.5（TS 镜像与孪生金样本**未锁**，见 §7 欠账） |
| web 组件 | `web/src/app/rooms/RoomPanel.tsx#RoomPanel`（`:115`）：`loadRooms`（`:171`）整表轮询，`:355` 全量渲染 | 实施懒加载归 implement（F22） |
| 图覆盖 | `n_logx_Setup` / `n_agentd_Server_handleRoomsList` / `n_collab_Service_ListRoomsForMember` 均在 `codegraph/baseline.json`，`codegraph sym` 用节点 id 命中 `anchor=ok` | spec 备注「logx.Setup 未入图」基于旧扫描，本轮注销该债 |
| 容器归属 | `best.json`：`k_collab_Service`/`k_collab_fn`/`k_collab_model`→`d_collab`；`k_agentd_Server`→`d_gateway`、`k_agentd_fn`/`k_agentd_model`→`d_orchestration`；`k_proto_model`→`d_protocol`；`k_logx_*`→`d_policy`。`k_collab_model` **不在 `baseline.json`**（在 `best.json`） | 视图以 `containersAdded` 相对 baseline 补齐 `k_collab_model`（见 §4） |

**依赖库/平台既成行为也是契约**：

| 行为 | 依赖源码出处 | 冻结影响 |
| --- | --- | --- |
| `codegraph check` 只对跨域 call 边执法，且 `entry` 按被调方容器 Label 匹配 | charter v0.10.0 模块缓存 `codegraph/check.go` 的 `Check`（`:84`/`:103`/`:184`；非本仓文件，无符号锚） | 新边落既有 entry 即不需要 target 增量；新增方法在原容器内不触发 `new-direction` |
| `codegraph validate` 叠的是 **baseline + view**，不读 `best.json`；`containersAdded` 只接受 baseline 里没有的容器 | charter v0.10.1 模块缓存 `codegraph/validate.go` 的 `ValidateDiff`（`:198-226`；非本仓文件，无符号锚） | `k_collab_model` 在 best 有、baseline 无，视图**必须**补该容器；补它不违反「只接受新容器」（baseline 里没有） |
| launchd 的 `StandardOutPath`/`StandardErrorPath` 只重定向、不解析内容 | `internal/service/launchd.go#plistBody`（`:118-119`） | 双写由「同文件两路写入者」造成，去掉任一路即可 |
| `slog` 的 `multiHandler` 把一条 Record 广播给全部子 handler | `internal/logx/logx.go#multiHandler.Handle`（`:75-82`） | 同一文件出现两路输出 = 同一文件两个写入者；单写需拆双 handler |

## 2. 分页语义（spec 语义1/2/4 的落地）

- **页 = 扁平列表序的连续切片**。扁平序沿用 `listRooms` 既有输出：非终态（含群房间）活动降序在前、终态卡房间沉底，各自内部活动降序。
- **游标**：不透明字符串，客户端不回解、不构造。编码格式见 §3.2；`encodeRoomCursor`/`decodeRoomCursor` 私有于 `internal/collab`。
- **翻页不丢不重**：`trimRoomPage` 以游标定位，返回严格位于游标之后的至多 `limit` 条；`has_more` 为真时给 `next_cursor`。注意同刻条目的限制，见 §3.2 规则 3。
- **刷新域 = 本页集合**（语义2）：`enrichRoomAttachments` 只对本页返回的房间对应 links 触发 attach 投影与后台刷新；跨页不预取、不补刷。
- **旧客户端策略 = 阻断**（拍板 P3/P4）：请求 query 里 `limit` 与 `cursor` **双双缺席** 判为旧客户端，返回 HTTP 426 + 可行动升级文案；不静默返回半页。

## 3. 精确契约形状

### 3.1 `internal/proto`（d_protocol）

```go
// internal/proto/rooms.go

// RoomsPage 是 GET /api/rooms 的分页响应信封（B374）。
type RoomsPage struct {
	Rooms      []RoomSummary `json:"rooms"`
	NextCursor string        `json:"next_cursor,omitempty"`
	HasMore    bool          `json:"has_more"`
}
```

- 信封是**加键兼容**：既有消费方只解包 `rooms` 数组仍读到首屏内容；旧客户端识别靠请求形态（拍板 P4），不靠信封形状。
- `has_more` **恒出键**（含 `false`）；`next_cursor` 仅 `has_more=true` 时出键；`rooms` 恒出键（含空数组）。
- `RoomsPage.Rooms` 的元素形状与既有 `RoomSummary` **逐字段一致**（不新增/不改名/不删键）。

### 3.2 `internal/collab`（d_collab）

```go
// internal/collab/service.go

const (
	roomsPageDefaultLimit = 50
	roomsPageMaxLimit     = 200
)

// ErrInvalidCursor 是游标非法哨兵（B374）：坏 base64 / 坏 JSON / 缺 a 或 r 键 /
// 键类型不符。gateway 据此把 ListRoomsPage 的该错误映射 HTTP 400。
var ErrInvalidCursor = errors.New("collab: 分页游标非法")

// ListRoomsPage 是分页会话列表的 wire 组装点（新建）。
func (s *Service) ListRoomsPage(project, member, cursor string, limit int) (proto.RoomsPage, error)

// trimRoomPage 是页裁剪符号（新建，私有），在 ListRoomsPage 内调。
func trimRoomPage(rooms []proto.RoomSummary, cursor string, limit int) (page []proto.RoomSummary, next string, hasMore bool, err error)

// encodeRoomCursor / decodeRoomCursor 是不透明复合游标编解码（新建，私有）。
func encodeRoomCursor(lastActivity time.Time, roomID string) string
func decodeRoomCursor(raw string) (lastActivity time.Time, roomID string, err error)
```

**游标编码格式（冻结，金样本锁定）**：

```
base64url_nopad( json.Marshal(struct{ A int64 `json:"a"`; R string `json:"r"` }{
    A: lastActivity.UnixNano(), R: roomID,
}) )
```

- 键名恰为 `a`（LastActivity 的 Unix 纳秒）与 `r`（房间 ID）；`A` 为 int64、`R` 为 string。
- 金样本向量（测试锁定）：`encodeRoomCursor(time.Unix(0, 1700000000000000000).UTC(), "B42")` → `eyJhIjoxNzAwMDAwMDAwMDAwMDAwMDAwLCJyIjoiQjQyIn0`。
- `decodeRoomCursor("")` → 零值时间 + 空 ID，`err=nil`（首页）。
- 非法 base64 / 非法 JSON / **缺 `a` 或 `r` 键 / 键类型不符**（如 `{}`、`{"a":"x"}`）→ 裹 `ErrInvalidCursor` 的 error（gateway 映射 400）。

**裁剪规则（冻结，本轮修订规则 3）**：

1. `limit <= 0` → `roomsPageDefaultLimit`；`limit > roomsPageMaxLimit` → `roomsPageMaxLimit`。
2. `cursor == ""` → 从扁平序首条开始。
3. 否则解码游标：
   - **主判据**：在当前扁平序里定位 `roomID` 相同的条目，取其后一条起算（房间 ID 唯一）。
   - **兜底**（`roomID` 已不在当前列表，房间被移除）：**跳过所有 `LastActivity` 不早于游标时刻的条目**。
   - **不再用 `ID > r` 比较**：上一轮冻结的 `x.ID > r` 与 F12/F13 的排序事实冲突——`listRooms` 的扁平序是「非终态在前、终态沉底，各自 `LastActivity` 降序，同刻保持 `SliceStable` 插入序」，**不是 ID 序**。用 ID 序做兜底会在同刻条目上丢条目或重条目（有测试就红，但不是本轮的测试面）。
   - **显式限制（写进冻结清单 F14，不假装能保不丢不重）**：同一时刻（`LastActivity.Equal`）的插入序在游标里**不可恢复**——兜底会跳过同刻全部条目。这是非保证面，不得声称 ID 序能修复。
4. 取前 `limit` 条；若还有剩余，则 `hasMore=true` 且 `next = encodeRoomCursor(本页末条的 LastActivity, 本页末条 ID)`，否则 `hasMore=false`、`next=""`。

**`ListRoomsPage` 归属说明**：spec 接缝 #6 字面写「页裁剪符号 ← `ListRoomsForMember` 内调」。本契约冻结为：`ListRoomsForMember` **签名不变**（全量；CLI 与 `internal/collab/readmodel_test.go` 的既有断言依赖它），分页入口新建为 `ListRoomsPage`，页裁剪符号 `trimRoomPage` 在 `ListRoomsPage` 内调。理由是 spec 明确「定语义不定签名」，而改 `ListRoomsForMember` 返回类型会在 Ticket 0 逼出既有测试改写（越过空壳），违骨架纪律。语义归属不变：裁剪规则仍只在 service 层一处。（协调者确认，不重开。）

### 3.3 `internal/agentd`（d_gateway）

```go
// internal/agentd/roomsapi.go

const (
	roomsListDefaultLimit = 50
	roomsListMaxLimit     = 200
)

// roomsListParams 是 GET /api/rooms 的分页参数决议结果（新建）。
type roomsListParams struct {
	Limit  int
	Cursor string
	Legacy bool
}

// parseRoomsListParams 解析分页 query（新建，私有）。
func parseRoomsListParams(r *http.Request) (roomsListParams, error)

// roomsListErrorStatus 把 ListRoomsPage 的错误映射为 HTTP 状态（新建，私有）。
func roomsListErrorStatus(err error) int
```

**解析语义（冻结）**：

1. `rawLimit := query["limit"]`，`rawCursor := query["cursor"]`。
2. `Legacy = rawLimit == "" && rawCursor == ""`（拍板 P4）。
3. `rawLimit == ""` → `Limit = roomsListDefaultLimit`；否则 `strconv.Atoi`：非法 → error（400）；`<=0` → 默认；`> roomsListMaxLimit` → 上限。
4. `Cursor = rawCursor`。

**`handleRoomsList` 组装（冻结形状）**：解析参数 → `s.rooms.ListRoomsPage(project, member, params.Cursor, params.Limit)` → `s.enrichRoomAttachments(r.Context(), page.Rooms)` → `writeJSON(w, 200, page)`。错误按 `roomsListErrorStatus` 分流：`ErrInvalidCursor` → **HTTP 400**；其余列表组装失败 → **HTTP 500**。`params.Legacy == true` 时（implement 落地）不调用 `ListRoomsPage`，改回 426 + 文案：

```
HTTP 426 Upgrade Required
{"error":"客户端版本过旧：会话列表已改为分页加载，请升级 handoff 桌面端与控制台后重试。"}
```

> **Ticket 0 可达性欠账**：`parseRoomsListParams` 仍是空壳（不读 `*http.Request`，`Legacy` 恒 false、`Cursor` 恒空），故经 `GET /api/rooms` 时 F9 的 426、F5–F8 里由 query 输入的 HTTP 路径都**不可达**；F10 的 400 亦不可达（游标不经 query 进入）。本轮唯一落地的可观测映射是 `roomsListErrorStatus` 纯函数（有测试），及 service 层对非法游标的可识别拒绝（有测试）。真解析落地即恢复这些 HTTP 路径——**这正是 P5 要求 426 先于真裁剪的原因**。

### 3.4 日志三件套（`d_policy`）

> 域 id 勘误：logx 的容器 `k_logx_fn`/`k_logx_model`/`k_logx_multiHandler` 在 `best.json` 中均归 **`d_policy`**。上一轮标题误写 `d_maintenance / d_runtime_config`，本轮改正。

- **① 高频 INFO 降 Debug**：`internal/agentd/roomsapi.go` 的逐 task / 逐次 INFO 降 `Debug`（精确行集合以 implement 读数为准）。
- **② 单写（拍板 P2，本轮钉死形态）**：`logx.Setup` 带非空 `logPath` 时，**同一记录只落盘一次**。冻结形态：带 `logPath` 时**只挂文件 JSONHandler，不挂 stderr TextHandler**——即同一 slog 记录只落文件一次，格式为 JSON。不带 `logPath` 时保持仅 stderr 文本。launchd 的 `StandardOutPath`/`StandardErrorPath` 双重定向**不改**（P2 维持，去掉任一路会让 agentd 早期 stderr 丢失且平台行为不一致）；`HANDOFF_LOG_LEVEL` 语义不变。
- **③ 轮转**：`agentd.log` 按大小轮转，规格 **100MB × 5 份**（spec 实现决定，建议值此处冻结）。轮转由 logx 内新增的大小轮转 handler 承担（包注释「不管理日志轮转」需同步修订）。触发粒度到「单条写入后检查」即可；第 6 份挤掉最旧一份。

### 3.5 TS 镜像（d_web_contract / d_web_command，契约形状冻结，Ticket 0 不落码）

```ts
export interface RoomsPage {
  rooms: RoomSummary[]
  next_cursor?: string
  has_more: boolean
}

export const fetchRooms = (opts?: { project?: string; cursor?: string; limit?: number }): Promise<RoomsPage>
```

首屏 `fetchRooms({ limit: 50 })` **显式带 limit**（拍板 P4：不带 limit 即 legacy）。`next_cursor` 原样回传 `fetchRooms({ cursor })`；`has_more=false` 终止续载。

**TS 孪生金样本未锁**：本工作树无 `web/node_modules`，Ticket 0 未跑 vitest/tsc，未新增 `rooms.test.ts` 侧的 `RoomsPage` 孪生样本。该债显式记入 §7（不许静默）。Go 侧信封金样本 `internal/proto/rooms_fixture_test.go#TestRoomsPageGoldenEnvelope` 已锁。

## 4. 图契约面

本卡新增的跨域边全部落在**既有 entry 或既有方向预算内**，`codegraph/target.json` **无口径增量**。下列与 `codegraph/diffs/cards-B374-charter.json` 的 `edgesAdded` 逐条一致（没有的边不写）：

- `e_http_get_api_rooms → handleRoomsList`：`d_gateway→d_gateway` 同域（入口到 handler）。
- `handleRoomsList → parseRoomsListParams` / `roomsListErrorStatus` / `writeErr` / `writeJSON`：`d_gateway→d_orchestration`（被调方容器 `k_agentd_fn`，Label 与 `d_gateway→d_orchestration` 既有预算 321 同侧，legacy 命中在预算内）。
- `handleRoomsList → ListRoomsPage`：`d_gateway→d_collab`，被调方容器 `k_collab_Service`（Label「collab 入站门面」）——已在 `d_gateway→d_collab` 的 `entries`。
- `parseRoomsListParams → roomsListParams`：`d_orchestration` 同域。
- `ListRoomsPage → RoomsPage`：`d_collab→d_protocol`，被调方容器 `k_proto_model`（Label「proto 实体」）——已在 `d_collab→d_protocol` 的 `entries`（budget 1）。
- `ListRoomsPage → listRooms` / `ListRoomsPage → trimRoomPage` / `trimRoomPage → decodeRoomCursor` / `encodeRoomCursor → roomCursor`：`d_collab` 同域。
- `edgesDeleted`：`handleRoomsList → ListRoomsForMember`（handler 不再直调全量入口）。

**容器勘误**：视图 `containersAdded` 落 **`k_collab_model`**（label「collab 实体」，domain `d_collab`）——该容器 **`best.json` 已有、`baseline.json` 尚无**，属 absorb 未回灌的覆盖债；`codegraph validate` 叠的是 baseline + view，`m_collab_roomCursor` 必须靠它挂上。**不再写成「scanner 新引入」**。

新符号与本分支视图 diff（`codegraph/diffs/cards-B374-charter.json`）同批冻结；下游以 `--view cards-B374-charter` 叠加查询命中。

## 5. 冻结清单（逐条可判 pass/fail）

**Wire 信封（GET /api/rooms）**

- F1 `RoomsPage` 序列化时 `has_more` **恒出键**（含 `false`）。
- F2 `next_cursor` **仅非空时出键**；空串时键缺席（omitempty）。
- F3 `rooms` **恒出键**（含空数组）。
- F4 `RoomsPage.Rooms` 元素形状与既有 `RoomSummary` 逐字段一致（不新增/不改名/不删键）。

**分页参数解析（HTTP）**

- F5 `limit` 缺席（且非 legacy）取 `roomsPageDefaultLimit` = 50。
- F6 `limit > roomsPageMaxLimit`（200）取上限 200。
- F7 `limit <= 0` 取默认 50。
- F8 `limit` 非整数 → HTTP 400。
- F9 请求 query 中 `limit` 与 `cursor` 双双缺席 → HTTP 426 + 冻结文案（阻断）。

**游标**

- F10 非法游标（坏 base64 / 坏 JSON / 缺 `a` 键 / 缺 `r` 键 / `a` 或 `r` 类型不符，含 `{}`）→ HTTP 400；gateway 经 `errors.Is(err, collab.ErrInvalidCursor)` 识别。
- F11 游标编码金样本：`encodeRoomCursor(Unix(0,1700000000000000000).UTC(), "B42")` = `eyJhIjoxNzAwMDAwMDAwMDAwMDAwMDAwLCJyIjoiQjQyIn0`；`decodeRoomCursor` 往返一致；`""` 解码为零值首页 + nil error。

**页裁剪**

- F12 沿扁平序翻完全部页，页内不重复、页间不丢条目：并集 = 全量列表，交集 = ∅（常规情形：游标所指房间仍在列表内）。
- F13 主判据：游标所指 `roomID` 在当前扁平序中的位置之后起算。
- F14 兜底（游标所指房间已不在列表）：跳过所有 `LastActivity` 不早于游标时刻的条目；**同刻插入序不可恢复是已知限制**（非「不丢不重」保证）。
- F15 `has_more=false` 当且仅当末页无剩余条目；`next_cursor` 非空当且仅当 `has_more=true`。
- F16 终态卡房间仍可达（分页不剪枝）；`ReadOnly` 标记不变。

**刷新限域**

- F17 `enrichRoomAttachments` 只对入参（本页）rooms 对应的 links 触发 attach 投影。
- F18 `startRoomAttachRefresh` 的远端 fan-out 集合 ⊆ 本页房间对应的远端挂账；未返回房间不触发远端 RPC。

**日志**

- F19 默认级别为 warn（`parseLevel("")` = `slog.LevelWarn`）；INFO 仅在显式 `HANDOFF_LOG_LEVEL=info/debug` 下出。
- F20 带 `logPath` 时同一记录落盘恰一次：只挂文件 JSONHandler，不挂 stderr TextHandler；launchd 双重定向不改。
- F21 日志文件超过 100MB 触发轮转，最多保留 5 份。

**前端（implement 落，形状已冻结）**

- F22 `fetchRooms` 首屏带 `limit`；滚动续载按 `has_more` 终止。

## 6. 移交 plan（实现级决定，不占冻结条目）

> 以下为查证期顺手确立、对契约对侧不可见的实现选择，plan 吸收后销区。

- 轮转 handler 的缓冲与重开文件时机、`maxSize`/`maxBackups` 常量命名。
- `roomsapi.go` 降级的精确行集合（实现时以读数为准）。
- `ListRoomsPage` 内部是否复用 `listRooms` 还是新拆分私有函数。
- `web` 懒加载的滚动阈值与组件测试落点。
- `roomsListErrorStatus` 未来是否并入更通用的 gateway 错误映射 helper。

## 7. 交棒欠账（implement 必须消化；带序号的依赖不得并行）

1. **【第一步，必须先于 2】** `parseRoomsListParams` 真解析 + legacy 探测（F5–F9），落地 HTTP 426 + 冻结文案；对应 handler 测试。**理由（P5）**：Ticket 0 已把 handler 切到 `ListRoomsPage`；若先让 `trimRoomPage` 真裁剪（honours `limit=50`）、后落 426，则无参 `GET /api/rooms` 会静默返回半页，正打 US5。
2. **【第二步，在 1 之后】** `trimRoomPage` 真裁剪（F12–F16）+ 游标金样本 F11 测试；主判据按 roomID 位置、兜底按 LastActivity 时刻、**不比 ID 序**（§3.2 规则 3）。
3. `enrichRoomAttachments` / `startRoomAttachRefresh` 限域实现 + F17/F18 测试。
4. 日志三件套：降级 INFO、单写（F20 形态：带 logPath 只挂文件 JSONHandler）、轮转 + F19/F20/F21 测试。
5. TS 镜像 `RoomsPage` 与 `fetchRooms` 新签名 + 懒加载 + F22 组件测试。**TS 孪生金样本未锁**（本工作树无 `web/node_modules`，Ticket 0 未跑 vitest/tsc）。
6. `internal/collab/readmodel_test.go` 的 `TestListRoomsForMemberScansEventsOnceForUnreadAndActivity` 仍锁全量入口，**不得**改签名迁就分页；分页另有测试。
7. 同批发版约束（spec 硬约束）：agentd + web + 外置桌面 app；旧客户端阻断提示。发版编排归 implement/plan，不在本节点。
8. **镜像发现超时定性**（spec 承诺）：`linux-01 context deadline exceeded` 本期排查定性（是刷新风暴的次生症状还是独立故障），结论落卡。
9. **b358 §4.4 文档修订**（spec 承诺）：列表改为分页后，修订 b358 §4.4「列表全量」表述——旧房间形态、只读、历史可查语义不变，仅列表访问分页化。
10. **勘误 carry**：spec 与台账中 `d_sessions`（协作房间）误写已回写为 `d_collab`；§3.4 域 id 已回写 `d_policy`。下游引用勿再沿用旧 id。

## 8. Ticket 0 骨架范围（本轮已落）

- `internal/proto/rooms.go`：`RoomsPage` 类型。
- `internal/collab/service.go`：`roomsPageDefaultLimit/roomsPageMaxLimit`、`ErrInvalidCursor`、`ListRoomsPage`、`trimRoomPage`、`encodeRoomCursor`、`decodeRoomCursor`（a/r 校验）；`ListRoomsPage` 为**直通镜像**（调 `listRooms` 后交 `trimRoomPage`，后者本轮返回整表、`hasMore=false`），不实现真裁剪。
- `internal/agentd/roomsapi.go`：`roomsListDefaultLimit/roomsListMaxLimit`、`roomsListParams`、`parseRoomsListParams`（空壳）、`roomsListErrorStatus`（真实映射）、`handleRoomsList` 接线到 `ListRoomsPage` 并回 `proto.RoomsPage` 信封。
- 骨架不落 legacy 426、真裁剪、限域、日志改动；编译必须通过。
- 越过空壳的可观测行为（`decodeRoomCursor` 的 a/r 校验、`roomsListErrorStatus` 映射）各有能变红的测试锁定：
  - `internal/collab/roomcursor_test.go`：金样本、往返、空游标、非法游标、a/r 缺键与类型、`ListRoomsPage` 可识别拒绝。
  - `internal/agentd/roomslist_status_test.go`：`roomsListErrorStatus` 的 400/500 分布与反向锚。

## 拍板记录

> 见台账 `docs/superpowers/ledgers/2026-09-15-b374-contract-ledger.md` §3。每条同时满足：难逆转 / 无上下文会惊讶 / 真取舍。

- **P1** 刷新限域按「本页房间 ID 集合」，而非「本页 links」。
- **P2** 单写修复取「agentd 侧只写一处」而非「manager 侧不再重定向 stderr」；**本轮钉死形态**：带 `logPath` 时只挂文件 JSONHandler，不挂 stderr TextHandler（F20）。被否：改 `launchd.plist` 或去掉 JSON handler 的反面。
- **P3** 旧客户端策略取「阻断升级提示」而非「降级首屏」。
- **P4** legacy 探测靠「既无 limit 也无 cursor」的请求形态，不新增客户端版本头。
- **P5（本轮新增）落地顺序：Legacy 探测 + HTTP 426 先于 `trimRoomPage` 真裁剪。** 被否：并行 / 先真裁剪后 426。为什么必须记：这是「反过来写不会有任何测试变红」的流程裁决——先真裁剪时，无参 GET 先命中 `limit=50` 默认、F5 绿而 T2 绿，但 US5（旧客户端看到明确升级提示）当场失守，且没有现有测试会红。
- **P6（本轮新增）游标兜底不比 ID 序。** 决定：主判据 = 当前扁平序里 roomID 的位置；房间已不在列表时按 LastActivity 时刻跳过，同刻插入序不可恢复。被否：上一轮的 `x.ID > r` 比较。为什么必须记：`listRooms` 的扁平序是「非终态在前 / 终态沉底 / 各自 LastActivity 降序 / 同刻 SliceStable 插入序」，ID 序与它不同构；ID 序在多数用例里与正确结果巧合一致，只有同刻条目才暴露，反向写不会有常规测试变红。

**交棒：breakdown。**

## 9. breakdown 核对修订记录

以下澄清由 breakdown 节点（`docs/superpowers/specs/b374-breakdown.md`，2026-09-15）在契约增量核对中做出；结论均为「不退回 contract」，只留痕不改冻结语义。冻结正文一字未动。

- **C-1（2026-09-15，breakdown 核对）：状态位核对通过，头部「工作分支」标签已漂移。** 上游 spec 头部「已批准」与本文头部「冻结状态」均实读在位（台账条目 4、5）。本文头部 §3 写「工作分支 `cards/B374-charter-3`」，而 breakdown 在本分支 `cards/B374-charter-5` 上开工——冻结提交 `32756584` 在两个分支上均可达，属分支标签漂移，非冻结物失真；下游以提交 hash 为准，不据分支名判定。
- **C-2（2026-09-15，breakdown 核对）：存量代码注释里的 `F` 编号是上一轮遗留，非权威。** 重冻后 §5 已重编号（F1–F4 信封、F5–F9 参数/426、F10–F11 游标、F12–F16 裁剪、F17–F18 刷新、F19–F21 日志、F22 前端），而 `internal/agentd/roomsapi.go` 与 `internal/collab/service.go` 多处注释仍写上轮编号（如 `F4`=游标 400、`F9/F10`=刷新限域）。实现以 §5 重编号为准；本卡不要求为注释编号单独开卡，实现轮顺手对齐。
- **C-3（2026-09-15，breakdown 核对）：`trimRoomPage` 的兜底注释与 P6 冲突，属代码注释待修，非契约面。** `internal/collab/service.go#trimRoomPage` 注释写「(LastActivity,roomID) 为兜底比较」，与 §3.2 规则 3 + P6「主判据 roomID 位置、兜底按 LastActivity 时刻、不比 ID 序」矛盾。归 implement 轮修注释与实现，契约语义以 §3.2/P6 为准。
- **C-4（2026-09-15，breakdown 核对）：`k_agentd_fn`/`k_logx_fn` 的域归属以 `best.json` 为准，`codegraph sym` 单点输出可能仍报 baseline 残留域 id。** 实读 `codegraph sym n_logx_Setup` 报 `domain=d_runtime_config`，而 `best.json` 的 `k_logx_fn→d_policy`。图覆盖债（baseline 残留 id 未 absorb 回灌），不属本卡修；子卡归属按 `best.json`。
