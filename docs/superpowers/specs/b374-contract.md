# B374 契约增量：房间列表强制分页 + attach 刷新限域 + 日志三件套

**上游状态：已批准**（源 spec `docs/specs/2026-09-15-b374-rooms-pagination.md`，头部状态行「已批准（2026-09-15，用户审批）」——本节点开工核对通过）
**级别：L3 轻档**　**卡：B374**　**有效基线：未设置**（本分支 `cards/B374-charter` @ `41a28474`；spec 提交在 `origin/main` @ `85840cf2`）
**冻结状态：本提交随 `codegraph/diffs/cards-B374-charter.json` 与 Ticket 0 骨架同批冻结；`codegraph/target.json` 本轮无口径增量（见 §4）**
**架构形态：按子系统分域的平铺领域包，无横向 controller/service/dao 分层**（沿用 `codegraph/best.json`）
**台账：** `docs/superpowers/ledgers/2026-09-15-b374-contract-ledger.md`

本契约冻结三件事的接缝语义与精确签名：`GET /api/rooms` 强制分页 wire、attach 刷新限域、日志三件套。Ticket 0 只落类型/常量/签名与直通镜像接线（编译通过）；分页裁剪、legacy 阻断、日志默认级别与轮转的实现归 implement 轮。L3 轻档无直通竖切（纪律块「轻档与 L2 无此步骤」），运行时最薄路径由 plan 节点承接。

## 1. 现状查证与边界

下表是逐项对现状代码的查证；`file#Symbol` 是代码事实锚（行号只是本轮读数，会漂）。

| 接缝 | 现状代码事实 | 本卡冻结后的精确形状 |
| --- | --- | --- |
| 列表 handler | `internal/agentd/roomsapi.go#Server.handleRoomsList`（`:53`）：`func (s *Server) handleRoomsList(w http.ResponseWriter, r *http.Request)`；现体无 limit/cursor 解析，`writeJSON(w, 200, map[string]any{"rooms": rooms})` | 签名不变；改经 `parseRoomsListParams` 与 `rooms.ListRoomsPage` 组装 `proto.RoomsPage` |
| attach 投影 | `internal/agentd/roomsapi.go#Server.enrichRoomAttachments`（`:84`）：`func (s *Server) enrichRoomAttachments(_ context.Context, rooms []proto.RoomSummary)`；内部 `s.startRoomAttachRefresh(links)` 传 **全量** `AllTaskLinks()`（`:88`） | 签名不变；加「只对本页 rooms 的 links」限域后调 `startRoomAttachRefresh`（用同形方法逐行接线） |
| attach 刷新 | `internal/agentd/roomsapi.go#Server.startRoomAttachRefresh`（`:156`）：`func (s *Server) startRoomAttachRefresh(links []ledger.TaskLink)`；对入参全量远端挂账 fan-out 16 并发 | 签名不变；调用方收窄入参即限域（刷新体不改） |
| 列表组装 | `internal/collab/service.go#Service.ListRoomsForMember`（`:303`）：`func (s *Service) ListRoomsForMember(project, member string) ([]proto.RoomSummary, error)`；`:310` `func (s *Service) listRooms(project, member string) ([]proto.RoomSummary, error)` 全量组装 | `ListRoomsForMember` **签名保持不变**（全量，CLI/既有测试兼容）；新增 `ListRoomsPage`（见 §3.1） |
| 列表排序 | `internal/collab/service.go#Service.listRooms`：`:413-416` active（活动降序，Stable）在前、sunk（终态）沉底，各自内部活动降序 | 分页在 **该扁平序** 上做；游标语义见 §3.2 |
| 日志 | `internal/logx/logx.go#Setup`（`:31`）：`func Setup(component, logPath string) *slog.Logger`；`:33` stderr TextHandler + `:36` 文件 JSONHandler 双挂 | 签名不变；带 `logPath` 时同一记录只落一处（见 §3.4） |
| 级别默认 | `internal/logx/logx.go#parseLevel`（`:46`）：默认分支 `return slog.LevelInfo` | 默认改 `slog.LevelWarn`（spec 语义3） |
| 双写来源 | `internal/service/launchd.go#plistBody`（`:118-119`）：`StandardOutPath` 与 `StandardErrorPath` 同值；值来自 `cmd/service.go#resolveSpec`（`:83`） | **不改 manager**；单写由 agentd 侧修（拍板 P2） |
| systemd / windows | `internal/service/systemd.go#unitBody`（`:76-101`）不写 StandardOutput/Error；`internal/service/windows.go#taskXML` 不重定向 stdout/stderr | 保持；本轮不动这两个平台 |
| wire 消费方 | `web/src/api/rooms.ts#fetchRooms`（`:85`）：`request<{rooms: RoomSummary[]}>(...)` 只解包数组 | wire 对面；新签名见 §3.3（TS 镜像待 implement 落，见 §7 欠账） |
| web 组件 | `web/src/app/rooms/RoomPanel.tsx#RoomPanel`（`:115`）：`loadRooms`（`:171`）整表轮询，`:355` 全量渲染 | 实施懒加载归 implement |
| 图覆盖 | `n_logx_Setup` / `n_agentd_Server_handleRoomsList` / `n_collab_Service_ListRoomsForMember` 均在 `codegraph/baseline.json`，`codegraph sym` 用节点 id 命中 `anchor=ok` | spec 备注「logx.Setup 未入图」基于旧扫描，本轮注销该债 |

**依赖库/平台既成行为也是契约**：

| 行为 | 依赖源码出处 | 冻结影响 |
| --- | --- | --- |
| `codegraph check` 只对跨域 call 边执法，且 `entry` 按被调方容器 Label 匹配 | charter v0.10.0 模块缓存 `codegraph/check.go` 的 `Check`（`:84`/`:103`/`:184`；非本仓文件，无符号锚） | 新边落既有 entry 即不需要 target 增量；新增方法在原容器内不触发 `new-direction` |
| launchd 的 `StandardOutPath`/`StandardErrorPath` 只重定向、不解析内容 | `internal/service/launchd.go#plistBody`（`:118-119`） | 双写由「同文件两路写入者」造成，去掉任一路即可 |
| `slog` 的 `multiHandler` 把一条 Record 广播给全部子 handler | `internal/logx/logx.go#multiHandler.Handle`（`:75-82`） | 同一文件出现两路输出 = 同一文件两个写入者；单写需拆双 handler |

## 2. 分页语义（spec 语义1/2/4 的落地）

- **页 = 扁平列表序的连续切片**。扁平序沿用 `listRooms` 既有输出：非终态（含群房间）活动降序在前、终态卡房间沉底，各自内部活动降序。分页在该序上做，不改变既有排序。
- **游标**：不透明字符串，客户端不回解、不构造。编码格式见 §3.2；`encodeRoomCursor`/`decodeRoomCursor` 私有于 `internal/collab`。
- **翻页不丢不重**：`trimRoomPage` 以游标定位，返回严格位于游标之后的至多 `limit` 条；`has_more` 为真时给 `next_cursor`。
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
- `has_more` **恒出键**（含 `false`）；`next_cursor` 仅 `has_more=true` 时出键。

### 3.2 `internal/collab`（d_collab）

```go
// internal/collab/service.go

const (
	roomsPageDefaultLimit = 50
	roomsPageMaxLimit     = 200
)

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
- 非法 base64 / 非法 JSON → 非 nil error（gateway 映射 400）。

**裁剪规则（冻结）**：

1. `limit <= 0` → `roomsPageDefaultLimit`；`limit > roomsPageMaxLimit` → `roomsPageMaxLimit`。
2. `cursor == ""` → 从扁平序首条开始。
3. 否则解码游标：**主判据**为定位 `roomID` 相同的条目，取其后一条起算（房间 ID 唯一）；**兜底**（`roomID` 不在当前列表，房间已被移除）按 `(lastActivity, roomID)` 比较——保留满足 `x.LastActivity.Before(a) || (x.LastActivity.Equal(a) && x.ID > r)` 的条目。
4. 取前 `limit` 条；若还有剩余，则 `hasMore=true` 且 `next = encodeRoomCursor(本页末条的 LastActivity, 本页末条 ID)`，否则 `hasMore=false`、`next=""`。

**`ListRoomsPage` 归属说明**：spec 接缝 #6 字面写「页裁剪符号 ← `ListRoomsForMember` 内调」。本契约冻结为：`ListRoomsForMember` **签名不变**（全量；CLI 与 `internal/collab/readmodel_test.go` 的既有断言依赖它），分页入口新建为 `ListRoomsPage`，页裁剪符号 `trimRoomPage` 在 `ListRoomsPage` 内调。理由是 spec 明确「定语义不定签名」，而改 `ListRoomsForMember` 返回类型会在 Ticket 0 逼出既有测试改写（越过空壳），违骨架纪律。语义归属不变：裁剪规则仍只在 service 层一处。

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
```

**解析语义（冻结）**：

1. `rawLimit := query["limit"]`，`rawCursor := query["cursor"]`。
2. `Legacy = rawLimit == "" && rawCursor == ""`（拍板 P4）。
3. `rawLimit == ""` → `Limit = roomsListDefaultLimit`；否则 `strconv.Atoi`：非法 → error（400）；`<=0` → 默认；`> roomsListMaxLimit` → 上限。
4. `Cursor = rawCursor`。

**`handleRoomsList` 组装（冻结形状）**：解析参数 → `s.rooms.ListRoomsPage(project, member, params.Cursor, params.Limit)` → `s.enrichRoomAttachments(r.Context(), page.Rooms)` → `writeJSON(w, 200, page)`。`params.Legacy == true` 时（implement 落地）不调用 `ListRoomsPage`，改回 426 + 文案：

```
HTTP 426 Upgrade Required
{"error":"客户端版本过旧：会话列表已改为分页加载，请升级 handoff 桌面端与控制台后重试。"}
```

> 426 是新增 wire 状态；Ticket 0 不落该分支（否则既有 `roomsapi_test.go` 全部无参请求当场 426）。分支与文案由 implement 落，测试决定见 spec「handler 分页单测（…旧客户端提示分支）」。

### 3.4 日志三件套（d_maintenance / d_runtime_config）

- **① 高频 INFO 降 Debug**：`internal/agentd/roomsapi.go` 的 `:63/:138/:144/:151/:211/:216` 逐 task / 逐次 INFO 降 `Debug`（精确行号以 implement 读数为准）。
- **② 单写（拍板 P2）**：`logx.Setup` 带非空 `logPath` 时，**同一记录只落盘一次**。冻结实现方向：文件 JSONHandler 与 stderr TextHandler 不再同时挂给同一 Record（修后 `multiHandler` 至多一路落文件）；`HANDOFF_LOG_LEVEL` 语义不变。
- **③ 轮转**：`agentd.log` 按大小轮转，规格 **100MB × 5 份**（spec 实现决定，建议值此处冻结）。轮转由 logx 内新增的大小轮转 handler 承担（包注释「不管理日志轮转」需同步修订）。触发粒度到「单条写入后检查」即可；第 6 份挤掉最旧一份。

### 3.5 TS 镜像（d_web，契约形状冻结，Ticket 0 不落码）

```ts
export interface RoomsPage {
  rooms: RoomSummary[]
  next_cursor?: string
  has_more: boolean
}

export const fetchRooms = (opts?: { project?: string; cursor?: string; limit?: number }): Promise<RoomsPage>
```

首屏 `fetchRooms({ limit: 50 })` **显式带 limit**（拍板 P4：不带 limit 即 legacy）。`next_cursor` 原样回传 `fetchRooms({ cursor })`；`has_more=false` 终止续载。

## 4. 图契约面

本卡新增的跨域边全部落在**既有 entry** 内，`codegraph/target.json` **无口径增量**：

- `handleRoomsList → ListRoomsPage`：`d_gateway→d_collab`，被调方容器 `k_collab_Service`（Label「collab 入站门面」）——已在 `d_gateway→d_collab` 的 `entries`。
- `handleRoomsList → proto.RoomsPage`、`ListRoomsPage → proto.RoomsPage`：`d_gateway→d_protocol` / `d_collab→d_protocol`，被调方容器 `k_proto_model`（Label「proto 实体」）——已在两侧 `entries`。
- `parseRoomsListParams`、`trimRoomPage`、`encodeRoomCursor`、`decodeRoomCursor`：同域新符号，不产生跨域边。

新符号与本分支视图 diff（`codegraph/diffs/cards-B374-charter.json`）同批冻结；下游以 `--view cards-B374-charter` 叠加查询命中。

## 5. 冻结清单（逐条可判 pass/fail）

**Wire（GET /api/rooms）**

- F1 `RoomsPage` 序列化：`has_more` 恒出键；`next_cursor` 仅非空时出键；`rooms` 恒出键且元素形状与既有 `RoomSummary` 逐字段一致。
- F2 `limit` 缺席（且非 legacy）取 `roomsPageDefaultLimit`；`limit > roomsPageMaxLimit` 取上限；`limit <= 0` 取默认；非整数 → HTTP 400。
- F3 请求 query 中 `limit` 与 `cursor` 双双缺席 → HTTP 426 + 冻结文案（阻断）。
- F4 `cursor` 非法（坏 base64 / 坏 JSON）→ HTTP 400。

**页裁剪**

- F5 沿扁平序翻完全部页，页内不重复、页间不丢条目：并集 = 全量列表，交集 = ∅。
- F6 `has_more=false` 当且仅当末页无剩余条目；`next_cursor` 非空当且仅当 `has_more=true`。
- F7 终态卡房间仍可达（分页不剪枝）；`ReadOnly` 标记不变。
- F8 游标编码金样本：`encodeRoomCursor(Unix(0,1700000000000000000).UTC(), "B42")` = `eyJhIjoxNzAwMDAwMDAwMDAwMDAwMDAwLCJyIjoiQjQyIn0`；`decodeRoomCursor` 往返一致。

**刷新限域**

- F9 `enrichRoomAttachments` 只对入参（本页）rooms 对应的 links 触发 attach 投影。
- F10 `startRoomAttachRefresh` 的远端 fan-out 集合 ⊆ 本页房间对应的远端挂账；未返回房间不触发远端 RPC。

**日志**

- F11 默认级别为 warn（`parseLevel("")` = `slog.LevelWarn`）；INFO 仅在显式 `HANDOFF_LOG_LEVEL=info/debug` 下出。
- F12 同一记录落盘恰一次（带 `logPath` 时单文件单条）。
- F13 日志文件超过 100MB 触发轮转，最多保留 5 份。

**前端（implement 落，形状已冻结）**

- F14 `fetchRooms` 首屏带 `limit`；滚动续载按 `has_more` 终止。

## 6. 移交 plan（实现级决定，不占冻结条目）

> 以下为查证期顺手确立、对契约对侧不可见的实现选择，plan 吸收后销区。

- `trimRoomPage` 的主判据用 ID 定位、兜底用 `(a,r)` 比较——这是实现细节，wire 侧只看到不透明游标。
- 轮转 handler 的缓冲与重开文件时机、`maxSize`/`maxBackups` 常量命名。
- `roomsapi.go` 降级的精确行集合（实现时以读数为准）。
- `ListRoomsPage` 内部是否复用 `listRooms` 还是新拆分私有函数。
- `web` 懒加载的滚动阈值与组件测试落点。

## 7. 交棒欠账（implement 必须消化）

1. `handleRoomsList` 的 legacy 426 分支 + 冻结文案；对应 handler 测试。
2. `trimRoomPage` 真裁剪实现 + F5/F6/F7 测试；游标金样本 F8 测试。
3. `enrichRoomAttachments` / `startRoomAttachRefresh` 限域实现 + F9/F10 测试。
4. 日志三件套：降级 INFO、单写、轮转 + F11/F12/F13 测试。
5. TS 镜像 `RoomsPage` 与 `fetchRooms` 新签名 + 懒加载 + F14 组件测试（本工作树无 `web/node_modules`，Ticket 0 未验证 TS 编译）。
6. `internal/collab/readmodel_test.go` 的 `TestListRoomsForMemberScansEventsOnceForUnreadAndActivity` 仍锁全量入口，**不得**改签名迁就分页；分页另有测试。
7. 同批发版约束（spec 硬约束）：agentd + web + 外置桌面 app；旧客户端阻断提示。发版编排归 implement/plan，不在本节点。

## 8. Ticket 0 骨架范围（本轮已落）

- `internal/proto/rooms.go`：`RoomsPage` 类型。
- `internal/collab/service.go`：`roomsPageDefaultLimit/roomsPageMaxLimit` 常量、`ListRoomsPage`、`trimRoomPage`、`encodeRoomCursor`、`decodeRoomCursor`；`ListRoomsPage` 为**直通镜像**（调 `listRooms` 后交 `trimRoomPage`，后者本轮返回整表、`hasMore=false`），不实现真裁剪。
- `internal/agentd/roomsapi.go`：`roomsListDefaultLimit/roomsListMaxLimit` 常量、`roomsListParams`、`parseRoomsListParams`（本轮返回默认值、`Legacy=false`）；`handleRoomsList` 接线到 `ListRoomsPage` 并回 `proto.RoomsPage` 信封。
- 骨架不落 legacy 426、真裁剪、限域、日志改动；编译必须通过，直通镜像行为由既有测试兜底。

## 拍板记录

见台账 `docs/superpowers/ledgers/2026-09-15-b374-contract-ledger.md` §3：P1（刷新限域按本页房间集合）、P2（单写修 agentd 侧）、P3（旧客户端阻断）、P4（legacy 靠请求形态不靠版本头）。
