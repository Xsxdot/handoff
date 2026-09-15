# B374 实现计划：房间列表强制分页 + attach 刷新限域 + 日志三件套

读者：零上下文实现执行者。工作目录：本工作树。
上游 spec：`docs/specs/2026-09-15-b374-rooms-pagination.md`（头部「已批准（2026-09-15，用户审批）」）。
冻结契约：`docs/superpowers/specs/b374-contract.md`（**§5 重编号 F1–F22 是本计划唯一权威编号**；骨架注释里的旧 F 号不是）。
拆解稿：`docs/superpowers/specs/b374-breakdown.md`（状态「已拍板（2026-09-16）」，P-1..P-6 全选甲）。
本计划台账：`docs/superpowers/ledgers/2026-09-16-b374-plan-ledger.md`（本节点事实、命令原文）。
本工作树分支：`cards/B374-charter-6`，基点 `d73f993b`（= origin/cards/B374-charter-5）。

**边界**：实现只能在当前分支；不切分支、不改 git 配置、不 push。只改下文「有界文件集」内的文件；集合外的生产改动退回协调者。

---

## 0. 拍板约束（不变量，违反即失败）

以下六条来自协调者 2026-09-16 裁决（`b374-breakdown.md` §9），实现必须逐条照做，不得改乙：

- **P-1**：两处「列表全量」**原文保留**，追加「B374 废止：列表已服务端分页」注。不改写 b156.2 原决策句、不搬运 b358 文档。
- **P-2**：镜像超时只做**机内通路独立性结论**，不改 `internal/agentd/mirror.go` 的 `discoverOnce` / 预算。
- **P-3**：`roomsapi_test.go` **13 处**无参列表请求改 `limit=50`；`TestRoomsEndpoints503WithoutLedger`（`:623`）**保持无参、断言 503**。另增无参 426 + 冻结文案测试，且「返回 200 半页」必须能红。
- **P-4**：仍用 `AllTaskLinks()` 建全量索引，只按**本页 rooms** 取子集投影并传 `startRoomAttachRefresh`。不改 `startRoomAttachRefresh` 函数体。
- **P-5**：`TestSetupWritesJSONToFile` 显式 `t.Setenv("HANDOFF_LOG_LEVEL","info")`；默认 warn 另加独立测试。**禁止**把 `log.Info` 改成 `log.Warn` 迁就默认。
- **P-6**：日志单写测试必须锁「文件恰一次**且**不落 stderr」两半边。`Setup` 签名不变；文件打开失败仍降级 stderr。

**DAG 硬约束**：
1. **T1 必须先于 T2**（契约 P5：426 先于真裁剪；否则无参 GET 静默返回半页，US5 失守且无既有测试变红）。
2. T3（web）与 T4（日志）可与 T1/T2 并行写码，但 **T3 不得先于 T1 单独合主线**（无 limit 的 web 请求会被 426 拦）。
3. T5 收口，最后。

**错误分流三路（禁止并进 `roomsListErrorStatus`）**：
- `parseRoomsListParams` 返回的 parse 错 → 直接 `writeErr(w, 400, err)`；
- 仅 `collab.ErrInvalidCursor` 经 `roomsListErrorStatus` → 400；
- 其余 `ListRoomsPage` 错 → 500。
- 反例：`limit=abc` 若进 `roomsListErrorStatus` 会变 500（F8 假绿）。
- 426 固定走 `writeErr(w, http.StatusUpgradeRequired, errors.New(冻结文案))`，**文案一字不改**。

**骨架注释勘误（C-2/C-3）**：`roomsapi.go` / `service.go` 里旧 F 号注释与「(LastActivity,roomID) 为兜底比较」注释均已否决；实现顺手改注释，**不改冻结语义**。`codegraph` 单点输出仍报 `d_runtime_config` 残留域属覆盖债（C-4），不修。

---

## 1. 基线事实（动手前已亲自跑到，原始输出见台账）

实现者必须先在基线复核下列事实，**不得凭记忆**。台账条目号对应 `2026-09-16-b374-plan-ledger.md`。

| 事实 | 命令 | 基线结果 |
| --- | --- | --- |
| 全仓编译 | `go build ./...` | 退出 0，无输出 |
| agentd 房间测试绿 | `go test ./internal/agentd/ -run 'TestRooms' -count=1` | `ok github.com/Xsxdot/handoff/internal/agentd 3.376s` |
| collab + logx 绿 | `go test ./internal/collab/ ./internal/logx/ -count=1` | 两包 `ok` |
| P-5 事实：默认改 warn 会打红既有测试 | `HANDOFF_LOG_LEVEL=warn go test ./internal/logx/ -run TestSetupWritesJSONToFile -count=1` | `FAIL ... logx_test.go:24: 文件日志缺少消息:` |
| web 依赖可装 | `npm ci`（workdir `web/`） | `added 290 packages in 2s` |
| web 全量测试绿 | `npx vitest run`（workdir `web/`） | `Test Files 124 passed (124) / Tests 1328 passed (1328)` |
| web 类型检查绿 | `npx tsc -b`（workdir `web/`） | 退出 0，无输出 |
| 图校验 | `codegraph validate --repo . --view cards-B374-charter` | `issues: null`、`containers: 300` |
| 图检查 | `codegraph check --repo .` | `fails: []`（有若干既有 warn，非本卡引入） |

**web 依赖说明**：本工作树 `web/node_modules` 已由本节点 `npm ci` 装好且被 `.gitignore` 忽略；若实现节点拿到的工作树没有它，先 `npm ci`（npm 网络已验证可达：`npm ping` → `PONG`）。**未实际跑过的 web 命令不许写成绿**；若环境不允许安装，T3 的 web 断言标「未验证」，不得假称 vitest/tsc 已绿。

**代码图查证**（均用 `codegraph`，带 view 才识别本卡新符号）：
- 已有符号：`codegraph sym n_agentd_Server_handleRoomsList` → `anchor=moved`；`codegraph sym n_collab_Service_ListRoomsPage --view cards-B374-charter` → `anchor=ok`（`internal/collab/service.go:333`）；`n_collab_trimRoomPage` 同视图 → `anchor=ok`（`:397`）。
- 图覆盖债：不带 `--view cards-B374-charter` 时 `n_collab_Service_ListRoomsPage` / `n_collab_trimRoomPage` 不在图中（Ticket 0 新符号）；`n_logx_parseLevel` 报 `domain=d_runtime_config`（baseline 残留，实际 `best.json` 归 `d_policy`）。两者均不归本卡修。
- `codegraph flow n_agentd_Server_handleRoomsList` 无控制流数据时回落读源码，**禁止拿 `chain` 冒充流程图**。

**依赖库/平台既成行为出处**（契约 §1 表，逐条已核）：
- `internal/agentd/mirror.go:34` `mirrorDiscoveryTick = 30s`、`:48` `mirrorDiscoverBudget = 3s`、`:183` `discoverOnce` 对全部 target 共享一个 `fanCtx` 预算（只读，P-2 不改）。
- `internal/logx/logx.go:31` `Setup(component, logPath string) *slog.Logger`；`:33` stderr TextHandler、`:36` 文件 JSONHandler 双挂；`:46` `parseLevel` 默认 `slog.LevelInfo`。
- `internal/agentd/roomsapi.go:217` 现状 `s.startRoomAttachRefresh(links)` 传**全量** links；`:223` 刷新体 workers=16、`roomAttachRefreshInterval=5s`、缓存 TTL=1m（均不改）。
- `internal/collab/service.go:404` `listRooms` 扁平序：active（活动降序，`SliceStable`）在前、sunk（终态卡）沉底，各自内部活动降序；`:410` `ReadAllEvents` 只扫一次。
- `internal/logx/logx.go:75` `multiHandler.Handle` 把一条记录广播给全部子 handler（F20 双写机理）。

---

## 2. 缺陷族对抗审查（出稿结论，逐族入栏）

**族 1 生命周期/状态机中断**
- T1/T2 是**纯请求内计算**（解析、游标解码、切片），无 goroutine、无临时资源、无状态机迁移；中途重启不残留。
- attach 后台刷新沿用既有 goroutine + `roomAttachRefreshing` 门 + `context.WithTimeout(10s)`；本卡只收窄**入参**，不改生命周期。
- T4 轮转在**单条写入后同步检查**并改名重开：无独立后台 goroutine，进程崩溃最多留下一个未改名文件，不产生孤儿进程/句柄（Windows 句柄竞争属族 3，见真机清单）。

**族 2 静默失败/误导报错**
- 426 是刻意阻断（US5），文案冻结、可行动；参数非法 → 400 带原因（`limit 非法: "abc"`）；游标非法 → 400（`errors.Is(ErrInvalidCursor)`）；列表组装失败 → 500。三态互不吞没。
- 分页 `has_more`/`next_cursor` 若算错会静默丢页——T2 的「并集=全量、交集=∅」断言是堵口。
- 限域若漏收窄，attach 照常工作、不报错——T1 用 httptest 远端 RPC 计数反例堵住（F17/F18）。
- 日志文件不可写仍降级 stderr + `slog.Warn`（既有行为保留）。

**族 3 跨平台假设**
- launchd 双重定向（macOS）是 P-2 既定不改面；单写修复在 agentd 侧，`logx` 纯 Go，systemd/windows 不重定向 stdout/stderr，行为一致。
- 轮转的改名/重开在 **Windows** 有句柄占用风险；本卡不碰 manager，`agentd.log` 由 logx 自己打开、自己关闭后改名，但真实 Windows 句柄竞争机内造不出 → **未验证，需真机**（真机清单 §7.5）。
- 路径走 `filepath`；游标/信封是纯字符串，无平台假设。

**族 4 假红/假绿测试**
- 反例清单（各自能变红）：legacy 写反 → 426 测试返回 200 红；主判据改成 `x.ID > r` → 同刻用例红；`has_more` 算错 → 并集断言红；限域漏收窄 → 远端 RPC 计数红；默认级别回 info → `TestSetupDefaultLevelWarnBehavior` 红；单写挂回 stderr → 单写断言红。
- 已识别假绿温床：13 处既有无参请求在 426 落地后**必须一起改**，否则会红（这是好事）；但「把 legacy 探测写成缺 limit 也按默认」会让全绿而 US5 失守 → 426 必须有**独立**断言，不靠既有测试数量。
- httptest 的远端 RPC 计数**不能**证明真实 relay 行为 → 真机清单。

**族 5 门禁绕过**
- `/api/rooms` 在 `s.auth(mux)` 内、经 `withRooms`（`internal/agentd/server.go:2474`）守 503；426/400/500 全在同一 `handleRoomsList` 内，无旁路。
- 检查与动作间无窗口：解析、解码、裁剪、限域入参构造全在单次请求内。
- 刷新限域的门是**入参收窄**，全仓只有 `enrichRoomAttachments` 一处调 `startRoomAttachRefresh`（grep 已证）。

**追加设问一：序列化边界**
- 新增字段：`proto.RoomsPage{rooms, next_cursor?, has_more}`（Ticket 0 已冻结）。手写投影链：Go struct → `writeJSON`（json tag；金样本 `internal/proto/rooms_fixture_test.go#TestRoomsPageGoldenEnvelope` 已锁）→ HTTP body → TS `request<RoomsPage>` 解码 → RoomPanel 渲染/续载。
- 游标链：`roomCursor{A,R}` → `encodeRoomCursor`（base64url_nopad）→ wire → `decodeRoomCursor`（`map[string]RawMessage` 逐键校验）。
- 必须有一条**穿过真实序列化边界**的回归：T5 收口在 `internal/agentd` 加 `TestRoomsListPageEnvelopeWireShapes`（真实 httptest JSON，断言 `has_more` 恒出、`next_cursor` 缺席 vs 非零可分辨）；T3 在 `rooms.fetch.test.ts` 加对应解码断言（`has_more` 缺失/零值 vs `next_cursor` undefined）。游标 roundtrip 属性测试加在 `roomcursor_test.go`。
- TS 孪生金样本：T3 落 `RoomsPage` 派生的 fetch 解码断言；Go 侧金样本已锁。

**追加设问二：枚举新值过既有白名单**：本卡不新增枚举（`roomsPageDefaultLimit` 等是数值常量，游标键 `a`/`r` 是冻结 wire，日志 handler 形态非枚举）。`room.Kind` / `ReadOnly` 不变。无风险。

**追加设问三：承重安全属性有测试锁住**：旧客户端被阻断（T1 426）、翻页不丢不重（T2 并集/交集）、同刻限制被显式承认（T2 fallback）、本页之外零远端 RPC（T1 计数）、单记录落盘恰一次且不落 stderr（T4）。无一次性 token/租约/唯一性，不命中。

**追加设问四：webview/平台表现**：T3 的 426 在浏览器经 `parseResponse` 转 `ApiError(426, detail)`，RoomPanel 渲染可行动提示；跨平台滚动/缓存旧 JS **未验证，需真机**。

---

## 3. 子任务 DAG 与接缝清单

```
T1 gateway 参数真解析 + legacy 426 + 限域调用点 + roomsapi INFO→Debug（d_gateway）
   │  [必须先，且先于 T2]
   ▼
T2 collab 真裁剪 + 游标定位（d_collab）          T3 web 镜像 + 懒加载（d_web）  ──┐
   │                                              T4 日志三件套（d_policy）      ──┤
   └──────────────────────────────► T5 欠账收口（8 定性 / 9 文档）◄──────────────┘
```

**测试 → 缝**（看入口符号）：
- T1：`handleRoomsList`（HTTP `GET /api/rooms`，spec 接缝 4）；`enrichRoomAttachments→startRoomAttachRefresh`（接缝 2）；`parseRoomsListParams`（接缝 5，经 handler）。
- T2：`Service.ListRoomsPage`（接缝 6 的契约修订落点：页裁剪符号在 `ListRoomsPage` 内调）。
- T3：`fetchRooms`（接缝 4 wire 对面）。
- T4：`logx.Setup`（接缝 3）。
- T5：跨缝收口（真实 httptest JSON ↔ fetch 解码）。

**缝 → 测试**：每条缝至少一支缝级断言（见各 task 验收）。**内部锁**：`roomcursor_test.go` 的编解码测试是 Ticket 0 冻结的 wire 格式锁（非 T2 实现锁）；T2 不再新增直接调 `trimRoomPage` 的测试（全部经 `ListRoomsPage` 缝构造），故无未声明的内部锁。T4 的 `rotate_test.go` 直接调 `newRotatingHandler` 属内部锁，理由：轮转触发阈值是 **100MB**，经 `Setup` 缝构造需实际写 100MB；「构造不出」成立，且 T4 另有经 `Setup` 缝的 F19/F20 断言，不靠它顶替。**无退路步骤**。

---

## 4. T1 —— gateway 参数真解析 + legacy 426 + 限域调用点 + INFO 降级

**契约引用**：§2（旧客户端策略=阻断）、§3.3（解析语义 1–4、`handleRoomsList` 组装形状、426 文案）、§3.4①；F5–F10、F17/F18；P-1/P-3/P-4/P-5。

**有界文件集**：`internal/agentd/roomsapi.go`、`internal/agentd/roomsapi_test.go`、`internal/agentd/roomslist_status_test.go`。
**Consumes**（精确签名，已存在，不改）：
- `func (s *Service) ListRoomsPage(project, member, pageCursor string, limit int) (proto.RoomsPage, error)`
- `var ErrInvalidCursor error`（`internal/collab`）
- `func writeErr(w http.ResponseWriter, code int, err error)`
- `func writeJSON(w http.ResponseWriter, status int, v any)`
- `func (s *Server) startRoomAttachRefresh(links []ledger.TaskLink)`（函数体不改）
- `func (s *Store) AllTaskLinks() ([]ledger.TaskLink, error)`
**Produces**（签名已冻结，本 task 填正文）：
- `func parseRoomsListParams(r *http.Request) (roomsListParams, error)`
- `func roomsListErrorStatus(err error) int`（已实现，不动）
- `const roomsListLegacyMessage string`

### 4.1 先写失败测试

在 `internal/agentd/roomsapi_test.go` 增（复用既有夹具 `newRoomsEnv`、`seedCard`、`ledgerGet`、`ledgerPost`、`testhttp.NewServer`、`eventually`）：

**（a）新增日志捕获 handler（放测试文件顶部）**：
```go
// roomsLogCapture 记录 Server.log 的每条记录（含级别），供「噪声降 Debug」断言。
// 它不按级别过滤：断言读 Record.Level，直接证明降级，不靠 logx 的过滤行为。
type roomsLogCapture struct {
	mu      sync.Mutex
	records []slog.Record
}

func (c *roomsLogCapture) Enabled(context.Context, slog.Level) bool { return true }
func (c *roomsLogCapture) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	c.records = append(c.records, r.Clone())
	c.mu.Unlock()
	return nil
}
func (c *roomsLogCapture) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *roomsLogCapture) WithGroup(string) slog.Handler      { return c }
func (c *roomsLogCapture) find(msg string) (slog.Record, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.records {
		if r.Message == msg {
			return r, true
		}
	}
	return slog.Record{}, false
}
```

**（b）legacy 426（缝级，F9）**：
```go
func TestRoomsListLegacyRequestRejectedWith426(t *testing.T) {
	env := newRoomsEnv(t)
	seedCard(t, env, "旧客户端卡")
	for _, path := range []string{"/api/rooms", "/api/rooms?project=p"} {
		code, body := ledgerGet(t, env.testAgentdEnv, path)
		if code != http.StatusUpgradeRequired {
			t.Fatalf("%s 旧客户端必须 426，实得 %d %s", path, code, body)
		}
		if !strings.Contains(body, roomsListLegacyMessage) {
			t.Fatalf("%s 426 文案漂移: %s", path, body)
		}
		if strings.Contains(body, `"rooms"`) {
			t.Fatalf("%s 426 不得夹带半页 rooms: %s", path, body)
		}
	}
}
```
> 变异：把 legacy 探测写反（缺 limit 也按默认）→ 返回 200 半页 → 本测试红。

**（c）参数/游标 HTTP（缝级，F5–F8、F10）**：种子 205 张卡（循环 `seedCard(t, env, fmt.Sprintf("分页卡-%03d", i))`），一次建账覆盖多条判据：
```go
func TestRoomsListPaginationHTTP(t *testing.T) {
	env := newRoomsEnv(t)
	for i := 0; i < 205; i++ {
		seedCard(t, env, fmt.Sprintf("分页卡-%03d", i))
	}
	// F7：limit<=0 取默认 50
	code, body := ledgerGet(t, env.testAgentdEnv, "/api/rooms?limit=0")
	if code != http.StatusOK {
		t.Fatalf("limit=0: %d %s", code, body)
	}
	page := decodeRoomsPage(t, body)
	// 205 卡 + project:p + global = 207 条扁平序；首页 50 条。
	if len(page.Rooms) != 50 || !page.HasMore {
		t.Fatalf("limit=0 应取默认 50 且 has_more=true: len=%d has_more=%v", len(page.Rooms), page.HasMore)
	}
	// F5：limit 缺席但带合法游标（非 legacy）→ 默认 50。
	// 游标取首页末条（第 50 条），其后仍有 157 条，足够再取一页 50。
	last := page.Rooms[len(page.Rooms)-1]
	cursor := testRoomCursor(t, last.LastActivity, last.ID)
	code, body = ledgerGet(t, env.testAgentdEnv, "/api/rooms?cursor="+url.QueryEscape(cursor))
	if code != http.StatusOK {
		t.Fatalf("cursor-only: %d %s", code, body)
	}
	if got := decodeRoomsPage(t, body); len(got.Rooms) != 50 {
		t.Fatalf("limit 缺席应取默认 50，实得 %d", len(got.Rooms))
	}
	// F6：limit>200 取上限 200
	code, body = ledgerGet(t, env.testAgentdEnv, "/api/rooms?limit=500")
	if code != http.StatusOK {
		t.Fatalf("limit=500: %d %s", code, body)
	}
	page = decodeRoomsPage(t, body)
	if len(page.Rooms) != 200 || !page.HasMore {
		t.Fatalf("limit=500 应取上限 200 且 has_more=true: len=%d", len(page.Rooms))
	}
	// F8：limit 非整数 → 400（不得 500）
	if code, body := ledgerGet(t, env.testAgentdEnv, "/api/rooms?limit=abc"); code != http.StatusBadRequest {
		t.Fatalf("limit=abc 必须 400（若经 roomsListErrorStatus 会假绿成 500）: %d %s", code, body)
	}
	// F10：非法游标 → 400
	if code, body := ledgerGet(t, env.testAgentdEnv, "/api/rooms?limit=50&cursor=!!!not-base64!!!"); code != http.StatusBadRequest {
		t.Fatalf("非法游标必须 400: %d %s", code, body)
	}
}
```
辅助（同文件）：
```go
func decodeRoomsPage(t *testing.T, body string) proto.RoomsPage {
	t.Helper()
	var page proto.RoomsPage
	if err := json.Unmarshal([]byte(body), &page); err != nil {
		t.Fatalf("分页信封解码失败: %v（原文 %s）", err, body)
	}
	return page
}

// testRoomCursor 复刻契约 §3.2 的游标编码（base64url_nopad(json{a,r})），
// 供测试构造真实客户端拿到的续页游标；encodeRoomCursor 在 internal/collab 私有。
func testRoomCursor(t *testing.T, at time.Time, roomID string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"a": at.UnixNano(), "r": roomID})
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}
```
（`roomsapi_test.go` 新增 import：`encoding/base64`、`log/slog`、`net/url`、`context`。）

**（g）两套 limit 常量各自锁定（白盒，无跨包导出）**：
```go
func TestRoomsListLimitConstants(t *testing.T) {
	if roomsListDefaultLimit != 50 || roomsListMaxLimit != 200 {
		t.Fatalf("gateway limit 常量漂移: %d/%d", roomsListDefaultLimit, roomsListMaxLimit)
	}
}
```
契约 §3.2/§3.3 要求两套 limit 同值（50/200）。`collab` 侧常量是小写私有，且 `internal/collab/service.go` **不在 T1 有界文件集内**（是 T2 的文件），故 T1 不做跨包导出、不 import `collab` 只读它。两侧各自的 50/200 由各自 task 内断言锁定：gateway 侧本支 + `TestRoomsListPaginationHTTP`；service 侧 `TestListRoomsPageLimitClamp`（§5.1(f)）末尾的 `roomsPageDefaultLimit != 50 || roomsPageMaxLimit != 200` 断言。**无退路、无跨包导出**；两处常量若漂移，两侧测试各自变红，不需要单一对比断言。

**（d）限域（缝级，F17/F18）**：远端挂账卡放在**第 2 页**，断言第 1 页零远端 RPC。
```go
func TestRoomsListAttachRefreshLimitedToPageRooms(t *testing.T) {
	env := newRoomsEnv(t)
	// 先建远端挂账卡（时间最早 → 沉到扁平序末段），再建 55 张有发言的卡把首页占满。
	remoteCard := seedCard(t, env, "远端卡")
	if err := env.ledger.LinkTask(remoteCard.ID, "relay", "T-relay", ledger.PurposeImplement, "test"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 55; i++ {
		c := seedCard(t, env, fmt.Sprintf("占位卡-%02d", i))
		if _, err := env.srv.rooms.Send(c.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "x"}, "user:sy"); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	calls := 0
	remote := testhttp.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"task": map[string]any{"id": "T-relay", "repo_path": "/repo", "work_dir": "/relay/B1"},
			"pending_tickets": []any{}, "recent_events": []any{},
		})
	}))
	env.srv.conf().Targets = map[string]config.Target{
		"relay": {Addr: remote.URL, Token: "remote-token"},
	}
	code, body := ledgerGet(t, env.testAgentdEnv, "/api/rooms?limit=50")
	if code != http.StatusOK {
		t.Fatalf("首页: %d %s", code, body)
	}
	page := decodeRoomsPage(t, body)
	for _, r := range page.Rooms {
		if r.ID == remoteCard.ID {
			t.Fatalf("夹具失效：远端卡必须不在首页: %s", body)
		}
	}
	time.Sleep(200 * time.Millisecond) // 给后台刷新一个起跑窗口
	mu.Lock()
	got := calls
	mu.Unlock()
	if got != 0 {
		t.Fatalf("首页之外的房间不得触发远端 RPC，实得 %d", got)
	}
	// 翻到含远端卡的那一页后，远端 RPC 才允许发生。
	last := page.Rooms[len(page.Rooms)-1]
	cursor := testRoomCursor(t, last.LastActivity, last.ID)
	if code, body := ledgerGet(t, env.testAgentdEnv, "/api/rooms?limit=50&cursor="+url.QueryEscape(cursor)); code != http.StatusOK {
		t.Fatalf("续页: %d %s", code, body)
	}
	eventually(t, 2*time.Second, "续页触发远端 attach", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return calls > 0
	})
}
```

**（e）INFO 降级（缝级，§3.4①）**：
```go
func TestRoomsListAttachLogsAtDebug(t *testing.T) {
	// 显式 info：若实现漏降级，级别过滤（未来经 logx）不会把 Info 藏掉。
	// 本测试用不过滤的捕获 handler，断言 Record.Level 精确等于 Debug。
	t.Setenv("HANDOFF_LOG_LEVEL", "info")
	env := newRoomsEnv(t)
	cap := &roomsLogCapture{}
	env.srv.log = slog.New(cap)
	seedCard(t, env, "降级卡")
	if code, body := ledgerGet(t, env.testAgentdEnv, "/api/rooms?limit=50"); code != http.StatusOK {
		t.Fatalf("列表: %d %s", code, body)
	}
	if _, ok := cap.find("会话列表响应成功"); !ok {
		t.Fatal("成功路径不得静默：会话列表响应成功缺失")
	}
	rec, ok := cap.find("房间 attach 投影完成")
	if !ok {
		t.Fatal("逐次 attach 汇总日志缺失")
	}
	if rec.Level != slog.LevelDebug {
		t.Fatalf("高频 attach INFO 必须降 Debug，实得 %s", rec.Level)
	}
}
```

**（f）P-3 既有 13 处改参**：把下列 13 行的路径加 `limit=50`（`?project=p` 的用 `&limit=50`）：
`roomsapi_test.go` 的 **87、123、168、185、211、254、309、325、375、396、411、457、859**。保留原断言语义；**623 行不动**（未装配 503，`withRooms` 先行返回）。

**跑红**（只跑触及包）：
```bash
go test ./internal/agentd/ -count=1 -run 'TestRoomsListLegacyRequestRejectedWith426|TestRoomsListPaginationHTTP|TestRoomsListAttachRefreshLimitedToPageRooms|TestRoomsListAttachLogsAtDebug' -v
```
基线必然失败（422/426/限域/降级均未实现），把真实失败原文追加台账。

### 4.2 最小实现

在 `internal/agentd/roomsapi.go`：

```go
// roomsListLegacyMessage 是旧客户端阻断文案（契约 §3.3，冻结，一字不改）。
const roomsListLegacyMessage = "客户端版本过旧：会话列表已改为分页加载，请升级 handoff 桌面端与控制台后重试。"

// parseRoomsListParams 解析分页 query（B374，冻结清单 F5–F9）。
// limit 与 cursor 双双缺席 → Legacy=true（旧客户端，handler 回 426）。
// limit 缺席但带 cursor → 非 legacy，limit 取默认；limit 非整数 → error（handler 直回 400）。
func parseRoomsListParams(r *http.Request) (roomsListParams, error) {
	q := r.URL.Query()
	rawLimit := q.Get("limit")
	rawCursor := q.Get("cursor")
	if rawLimit == "" && rawCursor == "" {
		return roomsListParams{Limit: roomsListDefaultLimit, Legacy: true}, nil
	}
	params := roomsListParams{Cursor: rawCursor}
	if rawLimit == "" {
		params.Limit = roomsListDefaultLimit
		return params, nil
	}
	n, err := strconv.Atoi(rawLimit)
	if err != nil {
		return roomsListParams{}, fmt.Errorf("limit 非法: %q", rawLimit)
	}
	if n <= 0 {
		n = roomsListDefaultLimit
	}
	if n > roomsListMaxLimit {
		n = roomsListMaxLimit
	}
	params.Limit = n
	return params, nil
}
```

`handleRoomsList`（替换现体，接线形状冻结；解析错直回 400，legacy 回 426）：
```go
func (s *Server) handleRoomsList(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	member := s.roomUserActor(r)
	params, err := parseRoomsListParams(r)
	if err != nil {
		s.log.Warn("会话列表分页参数无效", "project", project, "cause", err)
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if params.Legacy {
		s.log.Warn("会话列表命中旧客户端请求，回 426", "project", project)
		writeErr(w, http.StatusUpgradeRequired, errors.New(roomsListLegacyMessage))
		return
	}
	page, err := s.rooms.ListRoomsPage(project, member, params.Cursor, params.Limit)
	if err != nil {
		if code := roomsListErrorStatus(err); code == http.StatusBadRequest {
			s.log.Warn("会话列表游标非法", "project", project, "cause", err)
			writeErr(w, code, err)
			return
		}
		s.log.Warn("会话列表读取失败", "project", project, "member", member, "cause", err)
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.enrichRoomAttachments(r.Context(), page.Rooms)
	s.log.Debug("会话列表响应成功", "project", project, "member", member, "rooms", len(page.Rooms))
	writeJSON(w, http.StatusOK, page)
}
```
> 注：`会话列表响应成功` 与 `房间 attach 投影完成` 等高频项降 `Debug`（§3.4①）；`收件箱已聚合`(`:507`)、`房间消息已发送`(`:411`) 等用户动作项保持 `Info`。

`enrichRoomAttachments`（限域，P-4：仍全量建索引，只按本页取子集；不改刷新体）：
```go
func (s *Server) enrichRoomAttachments(_ context.Context, rooms []proto.RoomSummary) {
	if s.ledger == nil {
		return
	}
	links, err := s.ledger.AllTaskLinks()
	if err != nil {
		s.log.Warn("读取房间挂账失败", "cause", err)
		return
	}
	linksByRoom := make(map[string][]ledger.TaskLink)
	for _, link := range links {
		linksByRoom[link.CardID] = append(linksByRoom[link.CardID], link)
	}
	for roomID := range linksByRoom {
		sort.SliceStable(linksByRoom[roomID], func(i, j int) bool {
			return linksByRoom[roomID][i].CreatedAt.Before(linksByRoom[roomID][j].CreatedAt)
		})
	}
	// 限域（B374 P1/P-4）：只收集本页 rooms 的 links，fan-out 用该子集。
	pageLinks := make([]ledger.TaskLink, 0)
	for i := range rooms {
		if rooms[i].Kind != room.KindCard {
			continue
		}
		pageLinks = append(pageLinks, linksByRoom[rooms[i].ID]...)
	}

	localTasks := make(map[string]proto.Task)
	for _, link := range pageLinks {
		if link.Target != "" || s.st == nil {
			continue
		}
		tasks, listErr := s.st.ListTasks()
		if listErr != nil {
			s.log.Warn("读取本机任务列表失败，attach 降级禁用", "cause", listErr)
			break
		}
		for _, task := range tasks {
			localTasks[task.ID] = task
		}
		s.log.Debug("本机 attach 任务索引已就绪", "tasks", len(localTasks))
		break
	}

	for i := range rooms {
		if rooms[i].Kind != room.KindCard {
			continue
		}
		roomLinks := linksByRoom[rooms[i].ID]
		if len(roomLinks) == 0 {
			continue
		}
		for linkIndex := len(roomLinks) - 1; linkIndex >= 0; linkIndex-- {
			link := roomLinks[linkIndex]
			if link.Target == "" {
				attach, lookupErr := s.lookupRoomAttach(context.Background(), link, localTasks)
				if lookupErr != nil {
					s.log.Warn("房间 attach 本机任务不可解析", "room", rooms[i].ID,
						"task", link.TaskID, "cause", lookupErr)
					continue
				}
				rooms[i].Attach = attach
				s.log.Debug("房间 attach 投影成功", "room", rooms[i].ID,
					"target", link.Target, "task", link.TaskID, "workdir", attach.WorkDir)
				break
			}
			if attach := s.cachedRoomAttach(link); attach != nil {
				rooms[i].Attach = attach
				s.log.Debug("房间 attach 从缓存投影成功", "room", rooms[i].ID,
					"target", link.Target, "task", link.TaskID, "workdir", attach.WorkDir)
				break
			}
		}
	}
	s.startRoomAttachRefresh(pageLinks)
	s.log.Debug("房间 attach 投影完成", "rooms", len(rooms), "links", len(pageLinks))
}
```
`startRoomAttachRefresh` 内两处逐 task/逐轮 `Info`（`房间 attach 后台刷新成功`、`房间 attach 后台刷新完成`）降 `Debug`；函数体逻辑不动。

### 4.3 注释与常量

- 删 `parseRoomsListParams` 头「Ticket 0 为直通镜像…」整段（已不成立），改为契约语义说明。
- 改 `handleRoomsList` 头「限制域尚未实现，归 implement 轮」整段；「错误映射（契约冻结清单 F4）」改 §5 重编号（游标非法=F10）。
- 改 `enrichRoomAttachments` 头「B374 现状（Ticket 0）…尚未实现」整段为已限域说明。
- 常量 `roomsListDefaultLimit=50`、`roomsListMaxLimit=200` 不变。

### 4.4 验收（行为化，均可机内红绿）

| 判据 | 断言 |
| --- | --- |
| F5 limit 缺席（非 legacy）取 50 | `TestRoomsListPaginationHTTP` cursor-only 分支 |
| F6 limit>200 取 200 | 同上 limit=500 分支（205 卡） |
| F7 limit<=0 取 50 | 同上 limit=0 分支 |
| F8 limit 非整数 → 400 | 同上（且不得 500） |
| F9 双缺席 → 426 + 冻结文案 | `TestRoomsListLegacyRequestRejectedWith426` |
| F10 非法游标 → 400 | `TestRoomsListPaginationHTTP` cursor 分支 |
| F17/F18 本页之外零远端 RPC | `TestRoomsListAttachRefreshLimitedToPageRooms` |
| §3.4① 高频 INFO 降 Debug | `TestRoomsListAttachLogsAtDebug` |
| 两套 limit 常量同值 | 新增 `TestRoomsListLimitConstants`：`roomsListDefaultLimit==50 && roomsListMaxLimit==200` |
| 既有 13 处改参后仍绿、623 仍 503 | `go test ./internal/agentd/ -run TestRooms -count=1` |
| 未鉴权仍 401 / 未装配仍 503（族 5） | 既有 `TestRoomsEndpoints503WithoutLedger` + 既有 auth 测试 |

**测试范围声明**：本 task 只跑 `go test ./internal/agentd/ -count=1` 与 `go build ./...`；不跑全量。
**日志步骤**：新增 426 分支带 `project` 上下文 Warn；成功路径不静默（Debug）。
**注释步骤**：见 4.3。

---

## 5. T2 —— collab 真裁剪 + 游标定位（**必须 T1 之后**）

**契约引用**：§2、§3.2（裁剪规则 1–4）；F11–F16；P6；P-6 的「同刻 ID 序兜底反例」。
**有界文件集**：`internal/collab/service.go`、`internal/collab/rooms_page_test.go`（新建）、`internal/collab/roomcursor_test.go`（只在末尾追加属性测试）。
**Consumes**：`func (s *Service) listRooms(project, member string) ([]proto.RoomSummary, error)`、`func decodeRoomCursor(raw string) (time.Time, string, error)`、`func encodeRoomCursor(lastActivity time.Time, roomID string) string`、`type roomCursor`、`roomsPageDefaultLimit/roomsPageMaxLimit`。
**Produces**：`func trimRoomPage(rooms []proto.RoomSummary, cursor string, limit int) (page []proto.RoomSummary, next string, hasMore bool, err error)`（签名冻结，填正文）；`func (s *Service) ListRoomsPage(...)` 不变。

### 5.1 先写失败测试

新建 `internal/collab/rooms_page_test.go`（`package collab`），全部经 `Service.ListRoomsPage` 缝进入，复用既有 `fakeLC`（`readmodel_test.go`）。

**夹具事实（实现者必须先核，否则断言会误红）**：`listRooms` 恒在列表尾部追加群房间——有卡带 `Project!=""` 时加 `project:<p>`，再加 `global`；群房间 `LastActivity` 为零值，扁平序里沉到 active 段末尾。因此本文件的卡片一律 `Project: ""` 且查询用 `project=""`（projects 表为空，只剩 `global` 一个群房间），断言统一用 `cardIDs(page)` 过滤出 card 房间，避免把 `global` 算进条数。
测试辅助：
```go
// pageCards 造 n 张活动降序卡（下标 0 最新），项目留空（不额外产生 project 群房间）。
func pageCards(base time.Time, n int) []proto.Card {
	cards := make([]proto.Card, 0, n)
	for i := 0; i < n; i++ {
		cards = append(cards, proto.Card{
			ID: fmt.Sprintf("B%03d", i), Title: fmt.Sprintf("B%03d", i),
			Status: "进行中", UpdatedAt: base.Add(-time.Duration(i) * time.Second),
		})
	}
	return cards
}

// idsOf 取 page 里全部房间 ID（含群房间）。
func idsOf(rooms []proto.RoomSummary) []string {
	out := make([]string, 0, len(rooms))
	for _, r := range rooms {
		out = append(out, r.ID)
	}
	return out
}

// cardIDs 只取 card 房间 ID：listRooms 恒追加 global（有 project 时还有 project:<p>），
// 它们 LastActivity 为零值，不应参与「按卡裁剪」的条数断言。
func cardIDs(rooms []proto.RoomSummary) []string {
	out := make([]string, 0, len(rooms))
	for _, r := range rooms {
		if r.Kind == room.KindCard {
			out = append(out, r.ID)
		}
	}
	return out
}
```
（`rooms_page_test.go` 需 import：`fmt`、`testing`、`time`、`"github.com/Xsxdot/handoff/internal/collab/room"`、`"github.com/Xsxdot/handoff/internal/proto"`。`roomcursor_test.go` 的属性测试需新增 import `math/rand`、`fmt`。）

**（a）F12/F13/F15 翻页不丢不重（缝级，对全量含群房间比对）**：
```go
func TestListRoomsPagePaginatesFlatOrder(t *testing.T) {
	base := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	fake := &fakeLC{cards: pageCards(base, 7), leases: map[string]time.Time{}}
	svc := New(fake)
	full, err := svc.ListRoomsForMember("", "")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	cursor := ""
	for pages := 0; ; pages++ {
		page, err := svc.ListRoomsPage("", "", cursor, 3)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, idsOf(page.Rooms)...)
		if !page.HasMore {
			if page.NextCursor != "" {
				t.Fatalf("has_more=false 时 next_cursor 必须为空: %q", page.NextCursor)
			}
			break
		}
		if page.NextCursor == "" {
			t.Fatal("has_more=true 必须给 next_cursor")
		}
		cursor = page.NextCursor
		if pages > 20 {
			t.Fatal("翻页不收敛")
		}
	}
	seen := map[string]bool{}
	for _, id := range got {
		if seen[id] {
			t.Fatalf("页间重复: %s", id)
		}
		seen[id] = true
	}
	want := map[string]bool{}
	for _, r := range full {
		want[r.ID] = true
	}
	if len(got) != len(want) {
		t.Fatalf("并集=%d 全量=%d", len(got), len(want))
	}
	for id := range want {
		if !seen[id] {
			t.Fatalf("丢条目: %s", id)
		}
	}
}
```

**（b）F13 主判据 + ID 序反例（缝级）**：同刻三房间，游标指向中间 ID，断言只取其后（用 `cardIDs` 屏蔽 `global`）。
```go
func TestListRoomsPageLocatesByRoomIDNotIDOrder(t *testing.T) {
	base := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	cards := []proto.Card{
		{ID: "B2", Title: "B2", Status: "进行中", UpdatedAt: base},
		{ID: "B1", Title: "B1", Status: "进行中", UpdatedAt: base},
		{ID: "B3", Title: "B3", Status: "进行中", UpdatedAt: base},
	}
	fake := &fakeLC{cards: cards, leases: map[string]time.Time{}}
	page, err := New(fake).ListRoomsPage("", "", encodeRoomCursor(base, "B1"), 10)
	if err != nil {
		t.Fatal(err)
	}
	// 扁平序（同刻 SliceStable 保插入序）= [B2,B1,B3]；主判据=B1 之后=[B3]。
	// ID 序兜底 x.ID>"B1" 会给 [B2,B3] → 红。
	got := cardIDs(page.Rooms)
	if len(got) != 1 || got[0] != "B3" {
		t.Fatalf("主判据必须按 roomID 位置取，实得 %v", got)
	}
}
```

**（c）F14 兜底 + 同刻限制（缝级）**：
```go
func TestListRoomsPageFallbackSkipsSameInstant(t *testing.T) {
	base := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	// 房间 A0 不在列表（已消失），游标时刻 = base；两张同刻卡都被跳过。
	fake := &fakeLC{cards: []proto.Card{
		{ID: "B1", Title: "B1", Status: "进行中", UpdatedAt: base},
		{ID: "B2", Title: "B2", Status: "进行中", UpdatedAt: base},
	}, leases: map[string]time.Time{}}
	page, err := New(fake).ListRoomsPage("", "", encodeRoomCursor(base, "A0"), 10)
	if err != nil {
		t.Fatal(err)
	}
	// 同刻插入序不可恢复是已知限制（F14）：同刻全部 card 被跳过。
	if got := cardIDs(page.Rooms); len(got) != 0 {
		t.Fatalf("兜底必须跳过同刻全部 card: %v", got)
	}
	// 混合：一条更早的卡要留下（证明不是「全空」假绿）。
	fake2 := &fakeLC{cards: []proto.Card{
		{ID: "B1", Title: "B1", Status: "进行中", UpdatedAt: base},
		{ID: "B2", Title: "B2", Status: "进行中", UpdatedAt: base.Add(-time.Second)},
	}, leases: map[string]time.Time{}}
	page2, err := New(fake2).ListRoomsPage("", "", encodeRoomCursor(base, "A0"), 10)
	if err != nil {
		t.Fatal(err)
	}
	// 扁平序 [B1(base), B2(base-1s)]；跳过不早于 base 的 B1，从 B2 起算。
	if got := cardIDs(page2.Rooms); len(got) != 1 || got[0] != "B2" {
		t.Fatalf("兜底应从首个更早条目起算，实得 %v", got)
	}
}
```

**（d）主判据 vs 兜底（房间仍在 / 已移走）**：
```go
func TestListRoomsPageCursorRoomPresentVsRemoved(t *testing.T) {
	base := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	fake := &fakeLC{cards: pageCards(base, 4), leases: map[string]time.Time{}} // B000..B003
	svc := New(fake)
	first, err := svc.ListRoomsPage("", "", "", 2) // [B000,B001]
	if err != nil {
		t.Fatal(err)
	}
	if got := cardIDs(first.Rooms); len(got) != 2 || got[0] != "B000" || got[1] != "B001" {
		t.Fatalf("首页: %v", got)
	}
	cursor := encodeRoomCursor(first.Rooms[1].LastActivity, first.Rooms[1].ID)
	nxt, err := svc.ListRoomsPage("", "", cursor, 2) // 主判据 → [B002,B003]
	if err != nil {
		t.Fatal(err)
	}
	if got := cardIDs(nxt.Rooms); len(got) != 2 || got[0] != "B002" {
		t.Fatalf("房间仍在时应从其后起算，实得 %v", got)
	}
	// 移走 B001 后重放同一游标 → 兜底：跳过不早于 B001 时刻的 card。
	fake.cards = []proto.Card{fake.cards[0], fake.cards[2], fake.cards[3]}
	after, err := svc.ListRoomsPage("", "", cursor, 2)
	if err != nil {
		t.Fatal(err)
	}
	// 扁平序 [B000(base), B002(base-2s), B003(base-3s), global]；
	// B000 不早于 B001(base-1s) → 跳过；从 B002 起算 → [B002,B003]。
	if got := cardIDs(after.Rooms); len(got) != 2 || got[0] != "B002" {
		t.Fatalf("房间消失后兜底应从首个更早条目起算，实得 %v", got)
	}
}
```

**（e）F16 终态卡可达 + ReadOnly 不变（缝级）**：
```go
func TestListRoomsPageKeepsTerminalRoomsReachable(t *testing.T) {
	base := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	fake := &fakeLC{cards: []proto.Card{
		{ID: "B1", Title: "B1", Status: "进行中", UpdatedAt: base},
		{ID: "B9", Title: "B9", Status: "已完成", UpdatedAt: base.Add(time.Hour)},
	}, leases: map[string]time.Time{}}
	svc := New(fake)
	var seenTerminal bool
	cursor := ""
	for i := 0; i < 5; i++ {
		page, err := svc.ListRoomsPage("", "", cursor, 1)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range page.Rooms {
			if r.ID == "B9" {
				seenTerminal = true
				if !r.ReadOnly {
					t.Fatalf("终态卡 ReadOnly 必须为 true: %+v", r)
				}
			}
		}
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}
	if !seenTerminal {
		t.Fatal("分页不得剪枝终态卡房间")
	}
}
```

**（f）limit 收敛（缝级）**：`limit=0`/`-1` → 默认；`limit=1000` → 上限。
```go
func TestListRoomsPageLimitClamp(t *testing.T) {
	base := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	fake := &fakeLC{cards: pageCards(base, 250), leases: map[string]time.Time{}}
	svc := New(fake)
	for _, c := range []struct {
		in   int
		want int
	}{{0, 50}, {-1, 50}, {1000, 200}} {
		page, err := svc.ListRoomsPage("", "", "", c.in)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Rooms) != c.want {
			t.Fatalf("limit=%d 应收敛到 %d，实得 %d", c.in, c.want, len(page.Rooms))
		}
	}
	if roomsPageDefaultLimit != 50 || roomsPageMaxLimit != 200 {
		t.Fatalf("limit 常量漂移: %d/%d", roomsPageDefaultLimit, roomsPageMaxLimit)
	}
}
```
> 注意 `TestListRoomsPageLimitClamp` 的 `want` 断言含 `global`：250 张卡 + global = 251 条扁平序，limit=50/200 时页内 50/200 条（都是 card），仍在页内条数上成立。实现者若改动群房间追加逻辑需重核。

**（g）F11 游标属性测试**（追加到 `roomcursor_test.go`，复用既有金样本）：
```go
func TestRoomCursorRoundTripProperty(t *testing.T) {
	rng := rand.New(rand.NewSource(374))
	for i := 0; i < 200; i++ {
		at := time.Unix(0, rng.Int63()).UTC()
		id := fmt.Sprintf("B%03d", rng.Intn(1000))
		gotAt, gotID, err := decodeRoomCursor(encodeRoomCursor(at, id))
		if err != nil {
			t.Fatalf("第 %d 轮解码: %v", i, err)
		}
		if !gotAt.Equal(at) || gotID != id {
			t.Fatalf("第 %d 轮往返不一致: (%s,%q) != (%s,%q)", i, gotAt, gotID, at, id)
		}
	}
}
```

**（h）`ListRoomsForMember` 全量语义不变**：既有 `TestListRoomsForMemberScansEventsOnceForUnreadAndActivity`（205 卡、`eventReads==1`、207 行）保绿，不改。

**跑红**：
```bash
go test ./internal/collab/ -count=1 -run 'TestListRoomsPage|TestRoomCursorRoundTripProperty' -v
```
基线 `trimRoomPage` 是直通镜像（返回整表、hasMore=false），这些测试必红。

### 5.2 最小实现（替换 `trimRoomPage` 正文）

```go
// trimRoomPage 在扁平序上按游标切片（B374，契约 §3.2 规则 1–4 + 拍板 P6）。
//
// 语义：
//  1. limit<=0 取 roomsPageDefaultLimit；limit>roomsPageMaxLimit 取上限。
//  2. cursor=="" 从首条起算。
//  3. 否则解码游标，主判据=当前扁平序里 roomID 相同条目的位置之后；
//     兜底（房间已不在列表）=跳过所有 LastActivity 不早于游标时刻的条目。
//     **不比 ID 序**：listRooms 的扁平序是「非终态在前/终态沉底/各自活动降序/
//     同刻 SliceStable 插入序」，与 ID 序不同构；同刻插入序在游标里不可恢复
//     （F14 显式限制，不假装不丢不重）。
//  4. 取前 limit 条；有剩余则 hasMore=true 且 next=末条 (LastActivity,ID) 编码。
func trimRoomPage(rooms []proto.RoomSummary, pageCursor string, limit int) ([]proto.RoomSummary, string, bool, error) {
	if limit <= 0 {
		limit = roomsPageDefaultLimit
	}
	if limit > roomsPageMaxLimit {
		limit = roomsPageMaxLimit
	}
	start := 0
	if pageCursor != "" {
		at, roomID, err := decodeRoomCursor(pageCursor)
		if err != nil {
			return nil, "", false, err
		}
		start = -1
		for i := range rooms {
			if rooms[i].ID == roomID {
				start = i + 1
				break
			}
		}
		if start < 0 {
			// 兜底：房间已不在当前列表。跳过所有 LastActivity 不早于游标时刻的条目。
			start = len(rooms)
			for i := range rooms {
				if rooms[i].LastActivity.Before(at) {
					start = i
					break
				}
			}
		}
		if start > len(rooms) {
			start = len(rooms)
		}
	}
	page := rooms[start:]
	if len(page) > limit {
		page = page[:limit]
	}
	hasMore := start+len(page) < len(rooms)
	next := ""
	if hasMore && len(page) > 0 {
		last := page[len(page)-1]
		next = encodeRoomCursor(last.LastActivity, last.ID)
	}
	out := make([]proto.RoomSummary, len(page))
	copy(out, page)
	return out, next, hasMore, nil
}
```

`ListRoomsPage` 头注释改：删「Ticket 0 直通镜像」段，改「按 §3.2 真裁剪」；并加一条成功路径 `Debug` 日志（不静默）：
```go
func (s *Service) ListRoomsPage(project, member, pageCursor string, limit int) (proto.RoomsPage, error) {
	rooms, err := s.listRooms(project, member)
	if err != nil {
		return proto.RoomsPage{}, err
	}
	page, next, hasMore, err := trimRoomPage(rooms, pageCursor, limit)
	if err != nil {
		return proto.RoomsPage{}, err
	}
	log().Debug("会话分页完成", "project", project, "page", len(page), "has_more", hasMore)
	return proto.RoomsPage{Rooms: page, NextCursor: next, HasMore: hasMore}, nil
}
```
`trimRoomPage` 头旧注释「(LastActivity,roomID) 为兜底比较」按 P6 删除（本 task 修注释，C-3）。

### 5.3 验收

| 判据 | 断言 |
| --- | --- |
| F12 不丢不重 | `TestListRoomsPagePaginatesFlatOrder` |
| F13 主判据 roomID 位置 + ID 序反例 | `TestListRoomsPageLocatesByRoomIDNotIDOrder` |
| F14 兜底 + 同刻限制显式承认 | `TestListRoomsPageFallbackSkipsSameInstant`、`TestListRoomsPageCursorRoomPresentVsRemoved` |
| F15 has_more ⟺ next_cursor 非空 | `TestListRoomsPagePaginatesFlatOrder` 尾部断言 |
| F16 终态可达、ReadOnly 不变 | `TestListRoomsPageKeepsTerminalRoomsReachable` |
| limit 收敛 | `TestListRoomsPageLimitClamp` |
| F11 游标金样本/往返/空/非法 | 既有 `roomcursor_test.go` 五支 + 新增属性测试 |
| F1–F4 信封 | `internal/proto` 金样本（T5 复验） |
| `ListRoomsForMember` 全量不变 | 既有 `TestListRoomsForMemberScansEventsOnceForUnreadAndActivity` |

**测试范围声明**：只跑 `go test ./internal/collab/ -count=1`（不跑全量）。
**日志步骤**：`ListRoomsPage` 成功路径 Debug；错误分支继续向上传播、不吞。
**注释步骤**：见 5.2（含 C-3 的兜底注释修订）。

---

## 6. T3 —— web TS 镜像 + 会话列表懒加载

**契约引用**：§3.5；F22；族 2（426 可行动提示）。
**有界文件集**：`web/src/api/rooms.ts`、`web/src/api/rooms.fetch.test.ts`、`web/src/app/rooms/RoomPanel.tsx`、`web/src/app/rooms/RoomPanel.test.tsx`、`web/src/app/shell/Shell.test.tsx`（只改 mock 返回值）。
**Consumes**：`func request<T>(path: string, init?: RequestInit): Promise<T>`、`class ApiError { readonly status: number }`、`usePoll`、既有 `RoomPanel` 状态。
**Produces**：
```ts
export interface RoomsPage {
  rooms: RoomSummary[]
  next_cursor?: string
  has_more: boolean
}
export const fetchRooms: (opts?: { project?: string; cursor?: string; limit?: number }) => Promise<RoomsPage>
```

### 6.1 `web/src/api/rooms.ts`

替换现有 `fetchRooms`（第 84–88 行）：
```ts
// RoomsPage 是 GET /api/rooms 的分页信封（B374，契约 §3.5）：rooms 恒出、
// has_more 恒出、next_cursor 仅非空时出键。与 internal/proto/rooms.go 逐字段对应。
export interface RoomsPage {
  rooms: RoomSummary[]
  next_cursor?: string
  has_more: boolean
}

// fetchRooms 会话列表（GET /api/rooms?project=&cursor=&limit=）。
//
// 首屏必须显式传 limit（拍板 P4：不带 limit 也不带 cursor 即旧客户端，服务端 426）。
// next_cursor 原样回传取下一页；has_more=false 终止续载。
export const fetchRooms = (
  opts: { project?: string; cursor?: string; limit?: number } = {},
): Promise<RoomsPage> => {
  const q = new URLSearchParams()
  if (opts.project) q.set('project', opts.project)
  if (opts.cursor) q.set('cursor', opts.cursor)
  if (opts.limit !== undefined) q.set('limit', String(opts.limit))
  const qs = q.toString()
  return request<RoomsPage>(`/api/rooms${qs ? `?${qs}` : ''}`)
}
```

### 6.2 `web/src/app/rooms/RoomPanel.tsx`

1. import：现有 `import { useEffect, useMemo, useRef, useState, type PointerEvent as ReactPointerEvent } from 'react'` 改为追加 `type UIEvent`（即 `... , type PointerEvent as ReactPointerEvent, type UIEvent } from 'react'`）；新增 `import { ApiError } from '../../api/client'`；`import type { RoomsPage, RoomHistoryItem, RoomSummary } from '../../api/rooms'`（在既有 `import type { RoomHistoryItem, RoomSummary }` 行加 `RoomsPage`）。
2. 常量：`const ROOMS_PAGE_LIMIT = 50`、`const ROOMS_SCROLL_THRESHOLD = 48`（挨着 `HISTORY_LIMIT`）。
3. 新增状态（挨着 `const [draft, setDraft]`）：`upgradeRequired`、`moreRooms`、`moreCursor`、`moreHasMore`、`loadingMore`。
```ts
  const [upgradeRequired, setUpgradeRequired] = useState('')
  const [moreRooms, setMoreRooms] = useState<RoomSummary[]>([])
  const [moreCursor, setMoreCursor] = useState('')
  const [moreHasMore, setMoreHasMore] = useState(false)
  const [loadingMore, setLoadingMore] = useState(false)
```
4. `loadRooms` 重写（首屏带 limit；426 单独提示）：
```ts
  const loadRooms = async () => {
    logRoom('debug', 'rooms_request_started', { request: 'rooms', limit: ROOMS_PAGE_LIMIT })
    try {
      const page = await fetchRooms({ limit: ROOMS_PAGE_LIMIT })
      logRoom('debug', 'rooms_request_succeeded', {
        request: 'rooms', count: page.rooms.length, has_more: page.has_more,
      })
      return page
    } catch (error: unknown) {
      if (error instanceof ApiError && error.status === 426) {
        setUpgradeRequired(errorMessage(error))
        logRoom('warn', 'rooms_upgrade_required', { request: 'rooms', status: error.status })
      }
      logRoom('error', 'rooms_request_failed', { request: 'rooms', error: errorMessage(error) })
      throw error
    }
  }
```
5. 派生数据 + 首屏签名重置 + 续载：
```ts
  const roomsPoll = usePoll(loadRooms, COLLAB_POLL_MS)
  const firstPage: RoomsPage | null = roomsPoll.data
  const firstPageRef = useRef(firstPage)
  firstPageRef.current = firstPage
  // 首屏签名：内容变了才重置已续载的页，避免每轮 5s 轮询把翻页进度冲掉。
  const firstPageSignature = firstPage === null
    ? ''
    : `${firstPage.rooms[0]?.id ?? ''}|${firstPage.rooms.length}|${firstPage.has_more}`
  useEffect(() => {
    const page = firstPageRef.current
    if (page === null) return
    setMoreRooms([])
    setMoreCursor(page.next_cursor ?? '')
    setMoreHasMore(page.has_more)
  }, [firstPageSignature])
  const rooms = useMemo(
    () => (firstPage === null ? [] : [...firstPage.rooms, ...moreRooms]),
    [firstPage, moreRooms],
  )
  const loadMore = async () => {
    if (loadingMore || !moreHasMore || moreCursor === '') return
    setLoadingMore(true)
    logRoom('debug', 'rooms_more_started', { request: 'rooms', cursor: moreCursor })
    try {
      const page = await fetchRooms({ cursor: moreCursor })
      setMoreRooms((previous) => [...previous, ...page.rooms])
      setMoreCursor(page.next_cursor ?? '')
      setMoreHasMore(page.has_more)
      logRoom('debug', 'rooms_more_succeeded', {
        request: 'rooms', count: page.rooms.length, has_more: page.has_more,
      })
    } catch (error: unknown) {
      logRoom('error', 'rooms_more_failed', { request: 'rooms', error: errorMessage(error) })
    } finally {
      setLoadingMore(false)
    }
  }
  const onRoomsScroll = (event: UIEvent<HTMLDivElement>) => {
    const el = event.currentTarget
    if (el.scrollHeight - el.scrollTop - el.clientHeight > ROOMS_SCROLL_THRESHOLD) return
    void loadMore()
  }
```
（删掉原 `const rooms = useMemo(() => roomsPoll.data ?? [], ...)` 一行；`inboxPoll` 不变。）
6. 列表容器加滚动与 testid（原第 354 行 `<div className="min-h-0 flex-1 overflow-y-auto p-2">` 改为）：
```tsx
      <div
        data-testid="room-list-scroll"
        onScroll={onRoomsScroll}
        className="min-h-0 flex-1 overflow-y-auto p-2"
      >
```
7. 426 提示块（放在 `{(roomsPoll.disconnected || ...)}` 块之前），并把该既有块条件前置 `upgradeRequired === '' &&`：
```tsx
      {upgradeRequired !== '' && (
        <div className="shrink-0 border-b bg-amber-50 px-3 py-2 text-xs text-amber-800">
          <p role="alert">{upgradeRequired}</p>
        </div>
      )}
```
8. `loadingMore` 提示（紧挨列表容器内 `visible.map` 之后）：`{loadingMore && <p className="p-2 text-xs text-muted-foreground">正在载入更多会话…</p>}`。

### 6.3 `web/src/api/rooms.fetch.test.ts` 重写 `fetchRooms` 段

```ts
describe('fetchRooms', () => {
  it('首屏显式带 limit=50，project 走查询串，解包信封', async () => {
    const fetchMock = vi.fn().mockImplementation(() =>
      Promise.resolve(jsonResp({ rooms: roomsFixture, has_more: false })))
    vi.stubGlobal('fetch', fetchMock)
    const page = await fetchRooms({ project: 'handoff', limit: 50 })
    expect(fetchMock.mock.calls[0][0]).toBe('/api/rooms?project=handoff&limit=50')
    expect(page.rooms).toHaveLength(2)
    expect(page.rooms[0].last_activity).toBe('2026-08-26T08:00:00.123456789+08:00')
    expect(page.rooms[0].attach).toEqual({
      target: 'devbox', task_id: 'T1', work_dir: '/w/B1', command: 'handoff attach T1',
    })
  })

  it('续载原样回传 cursor，不带 limit', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResp({ rooms: [], has_more: false }))
    vi.stubGlobal('fetch', fetchMock)
    await fetchRooms({ cursor: 'eyJhIjoxLCJyIjoiQjQyIn0' })
    expect(fetchMock.mock.calls[0][0]).toBe('/api/rooms?cursor=eyJhIjoxLCJyIjoiQjQyIn0')
  })

  it('缺失/零值可分辨：has_more=false 且 next_cursor 缺席 vs has_more=true 且非空', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(jsonResp({ rooms: [], has_more: false }))
      .mockResolvedValueOnce(jsonResp({ rooms: [], has_more: true, next_cursor: 'C1' }))
    vi.stubGlobal('fetch', fetchMock)
    const last = await fetchRooms({ limit: 50 })
    expect(last.has_more).toBe(false)
    expect(last.next_cursor).toBeUndefined()
    const more = await fetchRooms({ limit: 50 })
    expect(more.has_more).toBe(true)
    expect(more.next_cursor).toBe('C1')
  })
})
```
其余 `fetchRoomMessages` / `sendRoomMessage` / `markRoomRead` / `fetchInbox` 段落不动。

### 6.4 `web/src/app/rooms/RoomPanel.test.tsx`

- 把**所有** `vi.mocked(fetchRooms).mockResolvedValue([...])` 改为信封形：`mockResolvedValue({ rooms: [...], has_more: false })`；`mockResolvedValue([])` → `{ rooms: [], has_more: false }`（基线实读 15 处 `mockResolvedValue` + `beforeEach` 内 1 处；实现时以 `grep -n 'fetchRooms).mockResolvedValue' web/src/app/rooms/RoomPanel.test.tsx` 的实际读数为准，逐个改全）。
- `mockRejectedValue(new ApiError(401, ...))` 不动。
- 新增三支：
```ts
  it('首屏以 limit=50 拉一页', async () => {
    vi.mocked(fetchRooms).mockResolvedValue({ rooms: [room()], has_more: false })
    render(<RoomPanel workbench={workbench()} persistent={false} />)
    await screen.findByRole('button', { name: /卡房间/ })
    expect(fetchRooms).toHaveBeenCalledWith({ limit: 50 })
  })

  it('has_more=true 滚动到底按 cursor 续载，has_more=false 终止', async () => {
    vi.mocked(fetchRooms)
      .mockResolvedValueOnce({ rooms: [room({ id: 'B1' })], has_more: true, next_cursor: 'C1' })
      .mockResolvedValueOnce({ rooms: [room({ id: 'B2', title: 'B2 卡房间' })], has_more: false })
    render(<RoomPanel workbench={workbench()} persistent={false} />)
    await screen.findByRole('button', { name: /卡房间/ })
    const list = screen.getByTestId('room-list-scroll')
    Object.defineProperty(list, 'scrollHeight', { value: 1000, configurable: true })
    Object.defineProperty(list, 'clientHeight', { value: 0, configurable: true })
    Object.defineProperty(list, 'scrollTop', { value: 1000, configurable: true, writable: true })
    fireEvent.scroll(list)
    await waitFor(() => expect(fetchRooms).toHaveBeenCalledWith({ cursor: 'C1' }))
    expect(await screen.findByText('B2 卡房间')).toBeInTheDocument()
    fireEvent.scroll(list)
    expect(fetchRooms).toHaveBeenCalledTimes(2) // has_more=false 不再续载
  })

  it('426 渲染可行动升级提示，不显示半页', async () => {
    vi.mocked(fetchRooms).mockRejectedValue(
      new ApiError(426, '客户端版本过旧：会话列表已改为分页加载，请升级 handoff 桌面端与控制台后重试。'))
    render(<RoomPanel workbench={workbench()} persistent />)
    expect(await screen.findByText(/客户端版本过旧/)).toBeInTheDocument()
  })
```
（`RoomPanel.test.tsx` 已 import `ApiError`、`fireEvent`、`waitFor`。）

### 6.5 `web/src/app/shell/Shell.test.tsx`

把 `fetchRooms: vi.fn().mockResolvedValue([])` 改为 `fetchRooms: vi.fn().mockResolvedValue({ rooms: [], has_more: false })`。

### 6.6 验收

| 判据 | 断言 |
| --- | --- |
| F22 首屏显式带 limit | `RoomPanel.test.tsx` 首屏用例 + `rooms.fetch.test.ts` URL 断言 |
| F22 next_cursor 续载、has_more 终止 | `RoomPanel.test.tsx` 续载用例、`rooms.fetch.test.ts` 缺失/零值用例 |
| 族 2 426 可行动提示 | `RoomPanel.test.tsx` 426 用例 |
| 序列化边界（缺键/零值） | `rooms.fetch.test.ts` 第三支 |
| 回归 | `npx vitest run`、`npx tsc -b`（workdir `web/`） |

**测试范围声明**：只跑 `npx vitest run src/api/rooms.fetch.test.ts src/app/rooms/RoomPanel.test.tsx src/app/shell/Shell.test.tsx`，再跑一次全量 `npx vitest run`；`npx tsc -b`。**未跑前不宣称绿。**
**日志步骤**：见 6.2（`rooms_more_started/succeeded/failed`、`rooms_upgrade_required`）。
**注释步骤**：`fetchRooms` 头注释、`RoomsPage` 接口注释、`onRoomsScroll`/`firstPageSignature` 为什么注释。

---

## 7. T4 —— 日志三件套（默认级别 / 单写 / 轮转）

**契约引用**：§3.4① ② ③；F19–F21；P2/P-5/P-6。
**有界文件集**：`internal/logx/logx.go`、`internal/logx/logx_test.go`、`internal/logx/rotate.go`（新建）、`internal/logx/rotate_test.go`（新建）、`internal/logx/level_internal_test.go`（新建）。
**Consumes**：`func Setup(component, logPath string) *slog.Logger`（签名不变，调用点 `cmd/agentd.go:69`、`cmd/wait.go:111`、`cmd/card_wait.go:101` 语义不变）。
**Produces**：`func newRotatingHandler(path string, maxBytes int64, backups int, opts *slog.HandlerOptions) (*rotatingHandler, error)`（私有）。

### 7.1 `internal/logx/rotate.go`（新建）

```go
// rotate.go —— agentd.log 的按大小轮转 handler。
//
// 职责：单条记录写入后检查文件大小，超过 maxBytes 就把当前文件改名为 path.1、
// 旧备份顺延，最多保留 backups 个备份（加当前文件共 backups+1 份）。
// 边界：只服务于 logx.Setup 的文件分支；不管理除 path 及其 .N 备份外的文件。
package logx

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
)

const (
	// logRotationMaxBytes 触发轮转的单文件上限（契约 F21：100MB）。
	logRotationMaxBytes = 100 * 1024 * 1024
	// logRotationMaxBackups 轮转备份数；加当前文件共 5 份（F21「最多保留 5 份」，
	// 第 6 份挤掉最旧一份）。命名与份数解释属契约 §6 移交 plan 区。
	logRotationMaxBackups = 4
)

// rotationState 是共享到全部派生 handler 的写端：文件句柄、当前大小与轮转规则。
// 实现 io.Writer 供内部 slog.JSONHandler 直接写；写后由 maybeRotate 按大小轮转。
type rotationState struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	backups  int
	file     *os.File
	size     int64
}

func (s *rotationState) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.file.Write(p)
	s.size += int64(n)
	return n, err
}

// maybeRotate 在单条记录写完后调用（契约 §3.4③ 允许「写入后检查」粒度）。
func (s *rotationState) maybeRotate() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.size <= s.maxBytes {
		return nil
	}
	if s.file != nil {
		_ = s.file.Close()
	}
	_ = os.Remove(fmt.Sprintf("%s.%d", s.path, s.backups))
	for i := s.backups - 1; i >= 1; i-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", s.path, i), fmt.Sprintf("%s.%d", s.path, i+1))
	}
	if err := os.Rename(s.path, s.path+".1"); err != nil && !os.IsNotExist(err) {
		return err
	}
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	s.file = f
	s.size = 0
	return nil
}

// rotatingHandler 把 JSON 记录写进 path，并在每条写入后按大小轮转。
// 派生 handler（WithAttrs/WithGroup）与本体共享同一个 rotationState，
// 因此 component 等属性照常落盘，且写端始终唯一（F20）。
type rotatingHandler struct {
	state *rotationState
	inner slog.Handler
}

func newRotatingHandler(path string, maxBytes int64, backups int, opts *slog.HandlerOptions) (*rotatingHandler, error) {
	if opts == nil {
		opts = &slog.HandlerOptions{}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	var size int64
	if info, statErr := f.Stat(); statErr == nil {
		size = info.Size()
	}
	st := &rotationState{path: path, maxBytes: maxBytes, backups: backups, file: f, size: size}
	return &rotatingHandler{state: st, inner: slog.NewJSONHandler(st, opts)}, nil
}

// Enabled 与内部 JSON handler 同口径（级别由 opts.Level 决定）。
func (h *rotatingHandler) Enabled(ctx context.Context, lvl slog.Level) bool {
	return h.inner.Enabled(ctx, lvl)
}

// Handle 交给内部 JSON handler 写完后按大小检查轮转。
func (h *rotatingHandler) Handle(ctx context.Context, r slog.Record) error {
	if err := h.inner.Handle(ctx, r); err != nil {
		return err
	}
	return h.state.maybeRotate()
}

// WithAttrs / WithGroup 保留属性并复用同一写端与轮转状态（component 等必须落盘）。
func (h *rotatingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &rotatingHandler{state: h.state, inner: h.inner.WithAttrs(attrs)}
}

func (h *rotatingHandler) WithGroup(name string) slog.Handler {
	return &rotatingHandler{state: h.state, inner: h.inner.WithGroup(name)}
}
```
> 关键点：`Setup` 末尾 `.With("component", component)` 会走 `WithAttrs`——若这里返回本体而不转发属性，`TestSetupWritesJSONToFile` 会因缺 `"component":"test"` 变红。上面已转发。

### 7.2 `internal/logx/logx.go` 改造

```go
// Package logx 提供统一的 slog 日志初始化入口。
//
// 职责：
//   - 根据 HANDOFF_LOG_LEVEL 解析日志级别（默认 warn）
//   - 带 logPath 时只写文件 JSON 并按其大小轮转；不带时只写 stderr 文本
//
// 边界：
//   - 不引入第三方日志库，仅使用标准库 log/slog
package logx

// Setup 创建并返回带 component 标签的 logger。
//
// 参数：
//   - component: 日志中固定的组件标识（如 "agentd"）
//   - logPath: 日志文件路径；为空时只写 stderr 文本
//
// 返回：
//   - 带 logPath：只写该文件的 JSON logger（带 100MB×5 轮转）；
//     不带：只写 stderr 文本
//
// 注意：
//   - 级别由 HANDOFF_LOG_LEVEL 控制（debug/info/warn/error，默认 warn，F19）
//   - 带 logPath 时绝不再挂 stderr handler：同一记录只落盘一次（F20，拍板 P2）
//   - 文件打开失败降级为仅 stderr 并输出 Warn，不影响程序运行
func Setup(component, logPath string) *slog.Logger {
	lvl := parseLevel(os.Getenv("HANDOFF_LOG_LEVEL"))
	opts := &slog.HandlerOptions{Level: lvl}
	var h slog.Handler
	if logPath == "" {
		h = slog.NewTextHandler(os.Stderr, opts)
	} else if rh, err := newRotatingHandler(logPath, logRotationMaxBytes, logRotationMaxBackups, opts); err == nil {
		h = rh
	} else {
		slog.Warn("日志文件打开失败，降级为仅 stderr", "path", logPath, "err", err)
		h = slog.NewTextHandler(os.Stderr, opts)
	}
	return slog.New(h).With("component", component)
}

// parseLevel 将环境变量字符串解析为 slog.Level，非法或空值回退到 warn（F19）。
func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelWarn
	}
}
```
**删除** `multiHandler` 类型及其四个方法（`internal/logx/logx.go:59-100`）——单写形态下不再有第二路 handler，保留即死代码。删除前 `grep -rn multiHandler internal/` 确认为 0（基线已证；若实现时发现新引用，退回协调者）。

### 7.3 测试

`internal/logx/level_internal_test.go`（`package logx`，因 `parseLevel` 未导出）：
```go
package logx

import (
	"log/slog"
	"testing"
)

func TestParseLevelDefaultsToWarn(t *testing.T) {
	if got := parseLevel(""); got != slog.LevelWarn {
		t.Fatalf("空值默认级别必须 warn: %v", got)
	}
	for in, want := range map[string]slog.Level{
		"debug": slog.LevelDebug, "info": slog.LevelInfo,
		"warn": slog.LevelWarn, "warning": slog.LevelWarn,
		"error": slog.LevelError, "bogus": slog.LevelWarn,
	} {
		if got := parseLevel(in); got != want {
			t.Fatalf("parseLevel(%q)=%v want %v", in, got, want)
		}
	}
}
```

`internal/logx/logx_test.go`：
- `TestSetupWritesJSONToFile` 首行加 `t.Setenv("HANDOFF_LOG_LEVEL", "info")`（P-5；其意图是验 JSON 落盘，不是验默认级别）。
- 新增：
```go
func TestSetupDefaultLevelWarnBehavior(t *testing.T) {
	t.Setenv("HANDOFF_LOG_LEVEL", "")
	p := filepath.Join(t.TempDir(), "handoff.log")
	log := logx.Setup("test", p)
	log.Info("info-msg")
	log.Warn("warn-msg")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Contains(s, "info-msg") {
		t.Fatalf("默认级别下 INFO 不得落盘: %s", s)
	}
	if !strings.Contains(s, "warn-msg") {
		t.Fatalf("默认级别下 WARN 必须落盘: %s", s)
	}
}

func TestSetupSingleWriteWithLogPath(t *testing.T) {
	t.Setenv("HANDOFF_LOG_LEVEL", "info")
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = writer
	p := filepath.Join(t.TempDir(), "handoff.log")
	log := logx.Setup("test", p)
	log.Info("once-only", "k", "v")
	os.Stderr = old
	_ = writer.Close()
	captured, _ := io.ReadAll(reader)
	_ = reader.Close()

	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(b), `"msg":"once-only"`); got != 1 {
		t.Fatalf("带 logPath 时同一记录必须落文件恰一次，实得 %d: %s", got, b)
	}
	if strings.Contains(string(captured), "once-only") {
		t.Fatalf("带 logPath 时不得再落 stderr: %s", captured)
	}
	// 另一半：不带 logPath 时仅 stderr 文本，不创建文件。
	dir := t.TempDir()
	path := filepath.Join(dir, "should-not-exist.log")
	reader2, writer2, _ := os.Pipe()
	os.Stderr = writer2
	log2 := logx.Setup("test", "")
	log2.Warn("stderr-only-line")
	os.Stderr = old
	_ = writer2.Close()
	captured2, _ := io.ReadAll(reader2)
	_ = reader2.Close()
	if !strings.Contains(string(captured2), "stderr-only-line") {
		t.Fatalf("不带 logPath 必须写 stderr: %s", captured2)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("不带 logPath 不应创建文件: %s", path)
	}
}
```
（`logx_test.go` 新增 import `io`。）

`internal/logx/rotate_test.go`（`package logx`，内部锁，理由见 §3）：
```go
package logx

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestRotatingHandlerKeepsConfiguredBackups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agentd.log")
	h, err := newRotatingHandler(path, 200, 4, &slog.HandlerOptions{Level: slog.LevelInfo})
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(h)
	for i := 0; i < 60; i++ { // 每条约 100B，足够触发多次轮转
		log.Info("rotation-line", "i", i, "pad", "0123456789012345678901234567890123456789")
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, e := range entries {
		if e.Name() == "agentd.log" ||
			len(e.Name()) > len("agentd.log.") && e.Name()[:len("agentd.log.")] == "agentd.log." {
			count++
		}
	}
	if count > 5 {
		t.Fatalf("轮转最多保留 5 份（当前+4 备份），实得 %d", count)
	}
	if _, err := os.Stat(path + ".4"); err != nil {
		t.Fatalf("应保留到 .4: %v", err)
	}
	if _, err := os.Stat(path + ".5"); err == nil {
		t.Fatal("不得保留 .5（第 6 份挤掉最旧）")
	}
	if info, err := os.Stat(path); err != nil {
		t.Fatalf("当前文件必须存在: %v", err)
	} else if info.Size() > 200 {
		t.Fatalf("当前文件超过单文件上限: %d", info.Size())
	}
}

func TestRotationConstants(t *testing.T) {
	if logRotationMaxBytes != 100*1024*1024 {
		t.Fatalf("单文件上限必须 100MB: %d", logRotationMaxBytes)
	}
	if logRotationMaxBackups+1 != 5 {
		t.Fatalf("总份数必须 5: %d", logRotationMaxBackups+1)
	}
}
```

### 7.4 验收

| 判据 | 断言 |
| --- | --- |
| F19 默认 warn；INFO 仅显式 info/debug | `TestParseLevelDefaultsToWarn` + `TestSetupDefaultLevelWarnBehavior` |
| F20 带 logPath 文件恰一次且不落 stderr；不带时仅 stderr | `TestSetupSingleWriteWithLogPath` |
| F21 100MB×5 轮转、第 6 份挤掉最旧 | `TestRotatingHandlerKeepsConfiguredBackups` + `TestRotationConstants` |
| 既有落盘意图保留 | `TestSetupWritesJSONToFile`（P-5 显式 info） |
| 调用点语义不变 | `cmd/agentd.go:69` 仍传 logPath；`cmd/wait.go:111`/`cmd/card_wait.go:101` 仍传 `""` |

**测试范围声明**：只跑 `go test ./internal/logx/ -count=1` 与 `go build ./...`。
**日志步骤**：文件打开失败仍 `slog.Warn` 降级（既有）；轮转 handler 内不递归打日志（注释说明）。
**注释步骤**：包注释更新（不再说「不管理轮转」）；`Setup` 头注释加 F19/F20/P2；`rotate.go` 文件头职责/边界。

---

## 8. T5 —— 欠账收口（8 镜像超时定性 / 9 文档修订）+ 跨边界回归

**契约引用**：§7 欠账 8、9；§3.4/§4；P-1（修订载体）、P-2（不改 mirror.go）。
**有界文件集**：`docs/superpowers/plans/b156.2.8-plan.md`、`docs/superpowers/specs/b156.2-breakdown.md`、`internal/agentd/roomsapi_test.go`（只加一支信封 wire 测试）。**不改 `internal/agentd/mirror.go`。**

### 8.1 欠账 8：镜像发现超时定性（机内层）

**机内可判事实（只读证据，写进台账）**：
- 镜像发现是独立循环：`internal/agentd/mirror.go:130` `Run` 以 `mirrorDiscoveryTick`（`:34`=30s）周期调 `:183 discoverOnce`；预算 `:48 mirrorDiscoverBudget = 3s` 是整个发现循环的**总**预算（`:186` `fanCtx`）。
- 房间列表刷新通路：`handleRoomsList` → `enrichRoomAttachments` → `startRoomAttachRefresh`，用 `s.roomAttachMu` 门 + `roomAttachRefreshInterval`(5s) 节流 + `context.WithTimeout(10s)`。
- 两者**无共享锁**（镜像不碰 `roomAttachMu`）、**无共享 goroutine**（镜像 goroutine 归 `Mirror.wg`，attach 刷新归自身 `sync.WaitGroup`）、**无共享远程调用**（镜像 `pool.For(name).ListTasks`，attach `pool.For(target).Attach`）。

**结论**：code-path 上镜像发现与房间列表刷新相互独立，`linux-01 context deadline exceeded` **不是**刷新风暴的次生症状。真实归因（3s 预算耗尽 / 网络抖动 / 真机环境）归协调者真机清单。**该结论原文写入 `docs/superpowers/ledgers/2026-09-16-b374-implement-ledger.md`**，卡级回写归协调者。

### 8.2 欠账 9：文档修订（P-1 甲，原文保留 + 追加注）

**编辑 1** `docs/superpowers/specs/b156.2-breakdown.md:39`，在该行末尾（`spec §7 注意力面本就未要求预览。` 之后）追加一段同段落文字：
```text

（B374 废止：会话列表已改为服务端分页，本行「列表全量」仅记录 B156.2 当时的决策；旧房间形态、只读、历史可查语义不变。）
```
**编辑 2** `docs/superpowers/plans/b156.2.8-plan.md:1449`，把该行改为：
```markdown
- **不把 A.2 列表分页做成服务端分页**（列表全量）。（B374 废止：会话列表已改为服务端分页。）
```
两处均**保留原文**，只追加废止注；不搬运 b358 文档。b358 §4.4 的不可达事实（`b358.md` 不在本树/origin/main/merge-base）已在 `b374-breakdown.md` §0 P-1 记录，T5 只引用、不再重复搬运。

### 8.3 跨序列化边界回归（追加设问一）

在 `internal/agentd/roomsapi_test.go` 增真实 httptest JSON 形状断言（穿过 `writeJSON` 真实序列化）：
```go
func TestRoomsListPageEnvelopeWireShapes(t *testing.T) {
	env := newRoomsEnv(t)
	seedCard(t, env, "信封卡")
	code, body := ledgerGet(t, env.testAgentdEnv, "/api/rooms?limit=50")
	if code != http.StatusOK {
		t.Fatalf("列表: %d %s", code, body)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["rooms"]; !ok {
		t.Fatalf("rooms 恒出键: %s", body)
	}
	hasMore, ok := raw["has_more"]
	if !ok {
		t.Fatalf("has_more 恒出键（含 false）: %s", body)
	}
	if string(hasMore) != "false" {
		t.Fatalf("单页 has_more 应为 false: %s", body)
	}
	if _, ok := raw["next_cursor"]; ok {
		t.Fatalf("has_more=false 时 next_cursor 必须缺席: %s", body)
	}
}
```

### 8.4 收口验证（跑真实结果并记台账）

```bash
go build ./...
go test ./internal/agentd/ ./internal/collab/ ./internal/logx/ ./internal/proto/ -count=1
git diff --check
git status --short --branch
```
（web 侧：`npm ci`（如缺）→ `npx vitest run` → `npx tsc -b`，workdir `web/`。）把每条命令与**原始输出**追加到 `docs/superpowers/ledgers/2026-09-16-b374-implement-ledger.md`。

### 8.5 验收

| 判据 | 断言 |
| --- | --- |
| 欠账 8 机内通路独立性结论 | 台账 `b374-implement-ledger.md` 段落 + §8.1 只读证据（不改 mirror.go：`git diff -- internal/agentd/mirror.go` 必须为空） |
| 欠账 9 两处废止注 | `git diff` 显示两文件只在原文后追加注、未改原决策句 |
| F1–F4 信封（重编号） | `TestRoomsListPageEnvelopeWireShapes` + `internal/proto` 金样本绿 |
| 全量 Go 触及包绿 | §8.4 命令退出 0 |

**测试范围声明**：`./internal/agentd/ ./internal/collab/ ./internal/logx/ ./internal/proto/` + 编译；不跑 `./...` 全量。
**日志步骤**：无新运行时日志。
**注释步骤**：两处文档注即交付注释；不再改运行时注释。

---

## 9. 用户故事 → 任务归属（spec §用户故事）

| 用户故事 | 归属 task / 断言 |
| --- | --- |
| US1 首屏 ≤2s、滚动续载无闪断 | T1（limit 收敛）+ T2（真裁剪）+ T3（首屏带 limit、滚动续载）；真机 §7.2 |
| US2 `card wait` 不再被刷新风暴饿死 | T1（F17/F18 限域）；真机 §7.3 |
| US3 房间发言低延迟、已读不受列表刷新牵连 | T1（限域）+ T4（日志增速）；真机 |
| US4 磁盘不被日志吃光 | T4（F19/F20/F21） |
| US5 旧客户端看到明确升级提示而非半页 | T1（F9 426 + 冻结文案，P-3 能红）；T3（426 前端提示）；真机 §7.1 |
| spec 承诺：镜像发现超时定性 | T5 §8.1（机内结论）+ 真机 §7.6 |
| spec 承诺：b358 §4.4 修订 | T5 §8.2（可达载体两处，P-1 甲） |

---

## 10. 未验证，需真机（本 task 由协调者执行，不派发）

1. **旧桌面端 426 呈现**：agentd 升级后，真实未升级桌面 app 打开列表 → 看到冻结升级文案而非半页/白屏（US5）。
2. **首屏性能**：本机 366 房间（358 card + 7 project + 1 global）复测首屏 ≤2s（基线 6.8s / 止血后 1.06s）；`card wait` 端到端延迟对照。
3. **attach fan-out 真量**：翻页后真实远端 Attach RPC 数从 800+/轮降到「本页房间数」量级；未返回房间零 RPC。
4. **日志三件套实况**：真机 `agentd.log` 增速常态 <1KB/s、单记录落盘恰一次（launchd 重定向下）、100MB 触发轮转且保留 5 份、第 6 份挤掉最旧。
5. **Windows 轮转句柄竞争**：`agentd.log` 改名/重开在 Windows 上不被其他句柄占用（族 3，机内造不出）。
6. **镜像发现超时真机归因**：linux-01 真实 `context deadline exceeded` 的三选一归因。
7. **web 懒加载真机**：Chromium/WKWebView/Wails 的滚动续载、`has_more=false` 终止、缓存旧 JS 命中新 agentd 的 426 呈现。
8. **发版编排**（spec 硬约束）：agentd + web + 外置桌面 app **同批升级**。

---

## 11. 占位符扫描节

- 全文无 `TBD` / `TODO` / 「同 Task N」/ 「加适当的错误处理」；每个 task 有精确文件路径、完整代码块、Consumes/Produces 精确签名。
- **声明的形态例外**（唯一）：T1/T2/T3/T4 的测试代码复用既有夹具 `newRoomsEnv`/`seedCard`/`ledgerGet`/`fakeLC`/`jsonResp`/`vi.stubGlobal`/`newTestAgentdEnv*`，其构造不复制进计划；但每支测试的**入口符号、参数、成功/失败、序列化与副作用断言**已逐条列全（§4.1、§5.1、§6.3–6.5、§7.3）。计划已给出新引入测试夹具（`roomsLogCapture`、`decodeRoomsPage`、`testRoomCursor`、`pageCards`）的完整代码。
- **内部锁声明**：T4 `rotate_test.go` 直接调 `newRotatingHandler`，理由：轮转阈值 100MB，从 `Setup` 缝构造需真实写 100MB，构造不出；T4 另有经 `Setup` 缝的 F19/F20 断言，不靠它顶替。T2 无直接调 `trimRoomPage` 的测试（全经 `ListRoomsPage` 缝）。`roomcursor_test.go` 属 Ticket 0 已冻结 wire 锁，非 T2 新增内部锁。
- **无退路步骤**：正文无「若意外先绿就改成直喂 X」类条件退路。

## 12. 自审三查

1. **spec 覆盖**：US1–US5 与 spec 承诺逐条指到 task（§9）；F1–F22 逐条映射（§4.4/§5.3/§6.6/§7.4/§8.5）。
2. **占位符扫描**：见 §11。
3. **跨 task 类型/签名一致性**：`ListRoomsPage(project, member, pageCursor string, limit int) (proto.RoomsPage, error)` T1 Consumes ↔ T2 Produces 逐字一致；`proto.RoomsPage` 字段 Go/TS 双侧（`rooms`/`next_cursor`/`has_more`）逐字一致；`Setup(component, logPath string)` 调用点不变；无参数类型别名差异。两块 limit 常量都是 50/200（§4.4、§5.3 各带断言）。
