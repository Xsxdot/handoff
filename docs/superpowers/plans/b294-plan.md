# B294 实现计划：远端预览会话与隔离 Chromium

状态：plan 节点，L3 轻档。U0–U5 在同一轮 implement 中按顺序执行，不拆并行子卡。

基线：cards/B294-plan，HEAD 1fa4668bcb490b74eb38047e5e08a4e67b0efd21；该提交含 P0–P5 拍板。本节点产出为本文件及
docs/superpowers/ledgers/2026-08-29-b294-plan-ledger.md；本节点只写计划和台账，不写实现代码、不改 git 分支/配置。

输入冻结物：docs/superpowers/specs/b294.md、docs/superpowers/specs/b294-contract.md、docs/superpowers/specs/b294-breakdown.md、
codegraph/best.json、codegraph/target.json、codegraph/diffs/cards-B294-charter-2.json。

## 0. 冻结边界

### 0.1 P0–P5

1. is-open 是当前页面收到 OpenPreview 成功后的短暂本地投影；刷新、关 Chromium、owner session 仍存在时，都不能宣称权威附着态。
2. TTLSeconds 固定 7200；owner 按 idle 过期；被本机 Chromium 附着的 session 续命；关一个 Chromium 不删 owner session。
3. 上游是 targetclient.Pool 生命周期管理的 raw DialContext，复用 relay/direct；不是新 owner HTTP/CONNECT 端点，浏览器不得直连 owner。
4. owner truth 复用 internal/store.Store 打开的 handoff.db；coordinator 只持有独立、可重建 preview mirror，不写 owner truth。
5. preview mirror 独立于任务 Mirror；launcher 用集中接口，executable discovery、参数、进程组、聚焦按 OS 文件实现。
6. via 只接受 spec 的 IP、CIDR、域名；拒绝 wildcard、正则、host:port。未传 via 时允许 owner localhost、127.0.0.1、::1 全端口。

### 0.2 唯一 wire 形状

实现不得增加字段、route 或 event kind。所有单元使用下列冻结形状：

~~~go
type PreviewOpenReq struct {
    Port int      `json:"port,omitempty"`
    Path string   `json:"path,omitempty"`
    Via  []string `json:"via,omitempty"`
}

type PreviewSession struct {
    ID         string    `json:"id"`
    EntryURL   string    `json:"entry_url"`
    Via        []string  `json:"via,omitempty"`
    CWD        string    `json:"cwd"`
    OriginURL  string    `json:"origin_url,omitempty"`
    Branch     string    `json:"branch,omitempty"`
    CreatedAt  time.Time `json:"created_at"`
    TTLSeconds int64     `json:"ttl_seconds"`
    Machine    string    `json:"machine,omitempty"`
}

type PreviewListResp struct {
    Sessions []PreviewSession `json:"sessions"`
    Machines []MachineStatus  `json:"machines,omitempty"`
}

const (
    PreviewEventCreated = "preview.created"
    PreviewEventClosed  = "preview.closed"
)

type PreviewEvent struct {
    Type    string         `json:"type"`
    Session PreviewSession `json:"session"`
    Machine string         `json:"machine,omitempty"`
}

type PreviewOpenResp struct { Opened bool `json:"opened"` }
type PreviewCloseResp struct { OK bool `json:"ok"` }
~~~

固定端点与责任：

| 入口 | 责任 | 成功形状 |
| --- | --- | --- |
| POST /api/previews | owner 创建 | 200 + PreviewSession |
| GET /api/previews | owner 列表 | active session；不带 machines |
| GET /api/previews?scope=all | coordinator 聚合 | 本机 + 远端；远端盖 machine；机器失败逐项写 machines |
| DELETE /api/previews/{id} | owner 关闭 | 200 + PreviewCloseResp；coordinator 不删远端 truth |
| POST /api/previews/{id}/open?machine= | 当前点击桌面的 agentd | 200 + PreviewOpenResp；machine 只选 owner |
| GET /ws/previews | 当前 agentd → 浏览器 | 文本 JSON PreviewEvent；浏览器只连本机 |

client 的精确签名不变：

~~~go
func (c *Client) CreatePreview(ctx context.Context, req proto.PreviewOpenReq) (*proto.PreviewSession, error)
func (c *Client) ListPreviews(ctx context.Context) (*proto.PreviewListResp, error)
func (c *Client) ListPreviewsAll(ctx context.Context) (*proto.PreviewListResp, error)
func (c *Client) listPreviews(ctx context.Context, path, op string) (*proto.PreviewListResp, error)
func (c *Client) ClosePreview(ctx context.Context, id string) (*proto.PreviewCloseResp, error)
func (c *Client) OpenPreview(ctx context.Context, id, machine string) (*proto.PreviewOpenResp, error)
func (c *Client) StreamPreviewEventsOnce(ctx context.Context, onEvent func(proto.PreviewEvent) error) error
~~~

### 0.3 基线判据（动手前实测）

以下命令在本节点改动前实际通过，后续单元只跑触及包：

~~~text
GOMODCACHE=/root/.handoff/tmp/3da7945a/gomodcache go test ./internal/proto ./internal/client ./internal/agentd ./cmd -count=1
  ok github.com/Xsxdot/handoff/internal/proto 0.013s
  ok github.com/Xsxdot/handoff/internal/client 9.326s
  ok github.com/Xsxdot/handoff/internal/agentd 218.461s
  ok github.com/Xsxdot/handoff/cmd 12.999s

GOMODCACHE=/root/.handoff/tmp/3da7945a/gomodcache go test ./internal/store ./internal/targetclient -count=1
  ok github.com/Xsxdot/handoff/internal/store 3.420s
  ok github.com/Xsxdot/handoff/internal/targetclient 0.157s

(cd web && npm ci --ignore-scripts)
  added 290 packages, and audited 291 packages in 2s
  found 0 vulnerabilities

(cd web && npm test -- --run src/api/contract.test.ts)
  Test Files 1 passed (1)
  Tests 39 passed (39)

(cd web && npm test -- --run src/app/tree/counts.test.ts src/app/tree/search.test.ts src/app/tree/ProjectTree.test.tsx)
  Test Files 3 passed (3)
  Tests 79 passed (79)

(cd web && npm test -- --run src/app/shell/Shell.test.tsx)
  Test Files 1 passed (1)
  Tests 41 passed (41)

(cd web && npm run typecheck)
  exit 0, no output
~~~

依赖默认行为以 contract §1.1 的已查证出处为准：yamux v0.1.2 为
/root/go/pkg/mod/github.com/hashicorp/yamux@v0.1.2/mux.go:61-72；coder/websocket v1.8.15 读限为
/root/go/pkg/mod/github.com/coder/websocket@v1.8.15/read.go:88-107；agentd WS 10 秒拨号预算/512 KiB 读限为
internal/agentd/forward_ws.go#wsDialBudget/#wsForwardReadLimit；Client.HTTPClient 总超时为 0 的事实来自
internal/client/client.go#Client.HTTPClient。实现不凭记忆改这些默认值。

## 1. DAG 与文件边界

~~~text
U0 Store/线格式 → U1 owner API/事件/CLI/生命周期 → U2 client/Pool/mirror
→ U3 SOCKS/PAC/Chromium → U4 web 树/preview 行 → U5 跨域回归/协调者真机
~~~

| 单元 | 文件集 | 边界 |
| --- | --- | --- |
| U0 | internal/store/store.go；新增 internal/store/previews.go、previews_test.go、internal/proto/preview_test.go、web/src/api/testdata/PreviewZeroValues.json | 只加 preview 表/测试/线格式样本，不改任务、ticket、task mirror 或 proto 生产文件 |
| U1 | internal/agentd/preview.go、server.go、ptyreclaim.go、cmd/agentd.go、cmd/preview.go；新增 agentd preview_owner.go/hub.go/static.go 及对应测试 | 不加 route/DTO，不启动 SOCKS/Chromium |
| U2 | internal/client/preview.go、internal/targetclient/pool.go 及测试；新增 agentd/preview_mirror.go 及测试；仅 raw relay/direct 需要时触及 relay/dialer.go、listener.go | 不复用任务 cursor，不写 coordinator Store，不加 owner HTTP 端点 |
| U3 | 新增 agentd/preview_proxy.go、preview_launcher.go、preview_launcher_unix.go、preview_launcher_windows.go 及对应测试；必要的 preview.go/server.go/cmd/agentd.go 接线 | 不改 Wails，不用系统默认浏览器，不 bind owner 端口，不做 iframe/pixel/path proxy |
| U4 | web/src/api/preview.ts、ws.ts；新增 app/data/usePreviews.ts 及测试；shell/Shell.tsx、tree/ProjectTree.tsx、counts.ts、search.ts 及对应测试 | 不改 OpenItem/workbench reducer，不进任务 MIME、中央 tab 或第二页面 |
| U5 | 新增 internal/agentd/preview_regression_test.go、web/src/app/preview_regression.test.tsx | 不改生产代码；不把 fake 浏览器/DNS/多机当真机通过 |

## 2. U0：owner persistence 与序列化

### 2.1 Interfaces

~~~go
type PreviewSource struct {
    Kind          string // "port" 或 "path"
    Port          int
    WorkspaceRoot string
    RelativePath  string
}

type PreviewRecord struct {
    Session      proto.PreviewSession
    Source       PreviewSource
    LastActiveAt time.Time
    ClosedAt     *time.Time
}

func (s *Store) InsertPreview(row PreviewRecord) error
func (s *Store) GetPreview(id string) (PreviewRecord, error)
func (s *Store) ListActivePreviews(now time.Time) ([]PreviewRecord, error)
func (s *Store) ClosePreview(id string, at time.Time) (PreviewRecord, bool, error)
func (s *Store) TouchPreview(id string, at time.Time) (bool, error)
func (s *Store) ExpirePreviews(now time.Time) ([]PreviewRecord, error)
func (s *Store) UpdatePreviewEntry(id, entryURL string) error
~~~

Consumes：proto.PreviewSession、PreviewSource、time.Time。Produces：SQL roundtrip、active 列表、幂等 close/expire/touch。PreviewRecord 的 Source、LastActiveAt、ClosedAt 均不出 wire。

在 internal/store/store.go#Open 的现有 DDL 列表中增加 preview_sessions：id、entry_url、via_json、cwd、origin_url、branch、created_at、ttl_seconds、source_kind、source_port、workspace_root、relative_path、last_active_at、closed_at；id 主键，除 closed_at 外非 NULL，DDL 使用 IF NOT EXISTS。via 用 JSON 保存，closed_at 用 SQL NULL 表达 active。

web/src/api/testdata/PreviewZeroValues.json 的完整线格式固定为：

~~~json
{
  "id": "preview-zero",
  "entry_url": "http://localhost:0",
  "cwd": "/tmp/preview",
  "created_at": "2026-08-29T00:00:00Z",
  "ttl_seconds": 0
}
~~~

该样本刻意缺席 via、origin_url、branch、machine；port=0 只在 PreviewOpenReq 的非法输入测试中出现，不能把它当成可打开的 session。

### 2.2 步骤、判据、范围

1. 基线复核：运行 GOMODCACHE=/root/.handoff/tmp/3da7945a/gomodcache go test ./internal/store ./internal/proto -count=1，预期两个 package 均输出 ok；失败原文落台账。
2. 在 internal/proto/preview_test.go 写并运行 go test ./internal/proto -run 'Preview|Contract' -count=1：直接 Marshal/Unmarshal 冻结 DTO，逐字段断言 port/path/via 二选一，缺席 optional 与零值区分，CreatedAt 为 RFC3339，TTLSeconds=0 仍有 ttl_seconds，nonzero port/via/machine roundtrip；将 ttl_seconds:0 与缺席 via/origin_url/branch 的原始样本写入 web/src/api/testdata/PreviewZeroValues.json，供 U5 的 TS JSON projection 使用。
3. 在 internal/store/previews_test.go 写并运行 go test ./internal/store -run 'Preview' -count=1：用现有临时 SQLite harness 断言 Insert/Get 保留全部字段；ClosedAt=nil 与 TTLSeconds=0 可区分；ListActivePreviews 过滤关闭和 last_active_at+ttl_seconds<=now；第二次 close ok=false；closed 行不能 Touch；ExpirePreviews 第二次为空。
4. 在 store.go#Open 加表，在 previews.go 实现方法。close/expire 必须事务内读取完整 record、条件更新 closed_at、再返回，阻止并发双发 closed；时间只用传入 UTC now。
5. 运行 GOMODCACHE=/root/.handoff/tmp/3da7945a/gomodcache go test ./internal/store ./internal/proto -run 'Preview|Contract' -count=1，预期新增/既有测试均 ok；失败输出原样落台账。
6. previews.go 文件头写“仅 preview_sessions 持久化，不拥有业务规则/事件发布”的职责边界；导出方法写参数、返回、事务和幂等注意事项。Store 叶子沿现有约定不重复打日志，U1 负责带 session/operation 上下文记录。

测试范围：仅 internal/proto、internal/store 的 Preview/Contract 测试；不跑全仓。

### 2.3 接缝覆盖

- 测试入口 TestPreviewJSONRoundTrip 穿过 DTO→JSON→DTO；TestPreviewStoreRoundTrip 穿过 Store.InsertPreview/GetPreview。
- 缝→测：port/path/via 与缺席/零值由 JSON 测试锁；active/close/expire/touch 由 Store 测试锁。
- Store 纯 SQL 断言仅是附加内部锁，理由是声明缝无法验证 NULL ClosedAt、条件更新和 idle 边界；不能替代 U1 HTTP。

## 3. U1：owner API、事件、CLI、生命周期

### 3.1 Interfaces

~~~go
type PreviewClock func() time.Time
type PreviewID func() string
type PreviewPortProbe func(context.Context, int) error
type PreviewViaValidator func([]string) error
type PreviewStaticServer interface {
    Start(ctx context.Context, workspaceRoot, relativePath string) (entryURL string, stop func() error, err error)
}
type PreviewWorkspaceResolver func(context.Context, string) (workspaceRoot, originURL, branch string, err error)
type PreviewOwnerDeps struct {
    Now              PreviewClock
    NewID            PreviewID
    Getwd            func() (string, error)
    ProbePort        PreviewPortProbe
    ResolveWorkspace PreviewWorkspaceResolver
    ValidateVia     PreviewViaValidator
    Static           PreviewStaticServer
}
func NewPreviewOwner(st *store.Store, hub *PreviewHub, deps PreviewOwnerDeps, log *slog.Logger) *PreviewOwner
func (o *PreviewOwner) Create(ctx context.Context, req proto.PreviewOpenReq) (*proto.PreviewSession, error)
func (o *PreviewOwner) List(ctx context.Context) (*proto.PreviewListResp, error)
func (o *PreviewOwner) Close(ctx context.Context, id string) (*proto.PreviewCloseResp, error)
func (o *PreviewOwner) Touch(ctx context.Context, id string, at time.Time) error
func (o *PreviewOwner) Expire(ctx context.Context) error
func (o *PreviewOwner) Restore(ctx context.Context) error
func (o *PreviewOwner) Stop(ctx context.Context) error

type PreviewHub struct {
    mu          sync.Mutex
    nextID      uint64
    subscribers map[uint64]chan proto.PreviewEvent
    closed      bool
    log         *slog.Logger
}
func NewPreviewHub(log *slog.Logger) *PreviewHub
func (h *PreviewHub) Subscribe() (<-chan proto.PreviewEvent, func())
func (h *PreviewHub) Publish(ev proto.PreviewEvent)
func (h *PreviewHub) Close()
~~~

Consumes：U0 Store、PreviewOpenReq、workspace resolver、port probe、static server。Produces：owner PreviewSession/ListResp/Event/CloseResp 与 GET /ws/previews 文本帧。

Create 规则：Port 与 Path 恰一非空；port 为 1..65535 且 ProbePort 证明 owner loopback 已监听；path 是 workspace-relative，realpath 仍在 workspace root，拒绝绝对路径、.. 穿越、symlink 越界；ValidateVia 拒绝 wildcard/regex/host:port。port entry 固定 http://localhost:<port>；path 用 Static 在 owner loopback ephemeral port 服务，不能 file://。写入 CWD、origin、branch、UTC CreatedAt、TTLSeconds=7200；owner response 的 Machine 为空串。

### 3.2 步骤、判据、范围

1. 基线复核：运行 GOMODCACHE=/root/.handoff/tmp/3da7945a/gomodcache go test ./internal/agentd ./cmd -count=1，预期 ok。已有 Ticket 0 503 测试先改为实现行为测试。
2. 在 internal/agentd/preview_owner_test.go 写真实 httptest.NewServer(srv.Handler()) 失败测试并运行 go test ./internal/agentd -run 'Preview' -count=1：有效 port 的 POST 返回 200/完整 PreviewSession；path 越界、port 0/65536、port/path 同时给、皆空、未监听、非法 via 返回 4xx 且错误带 operation/field/value 上下文；GET 只列 active；DELETE 首次 200+ok=true，第二次 404/现有 API error。
3. 同一 Handler harness 连接 GET /ws/previews，创建后读文本 PreviewEvent preview.created，关闭后读 preview.closed 且完整 Session；入口必须穿过 Server.Handler，不直接调用 Hub。
4. preview_owner.go 接入 Store：落库成功后才 Publish created；close/expire 只有条件更新成功才 Publish closed；Restore 恢复未过期 path static server，更新 entry URL；Stop 先停 static、取消 expiry goroutine。expiry 用 UTC PreviewClock。Touch 只续 LastActiveAt，不写 is-open wire。
5. preview_hub.go 用独立有界订阅；慢订阅取消并记录，不阻塞 create；WS 只编码文本 JSON，沿 contract 的 512 KiB 边界，不持任务 cursor。
6. preview.go 保留五条注册路径，替换 503：严格解码单 JSON 并拒绝尾随 token；列表只接受无 scope/scope=all；close 只 owner；open 调注入 PreviewOpener；WS 只本机 Hub。错误用 writeJSON，日志含方法/path/id/machine/operation，禁 print。
7. server.go 保持 NewServer 参数不变，增加：

~~~go
type PreviewOpener interface {
    OpenPreview(ctx context.Context, id, machine string) (*proto.PreviewOpenResp, error)
    Stop(ctx context.Context) error
}
func (s *Server) SetPreviewOwner(owner *PreviewOwner)
func (s *Server) SetPreviewMirror(mirror *PreviewMirror)
func (s *Server) SetPreviewOpener(opener PreviewOpener)
func (s *Server) StartPreviewServices(ctx context.Context) error
func (s *Server) StopPreviewServices(ctx context.Context) error
~~~

Start 调 owner Restore 并启动 expiry/mirror；Stop 以 opener→mirror→owner 顺序等 goroutine/child/proxy，再由 GracefulShutdownCleanup 取消 wdCtx。cmd/agentd.go 在 sd.Serve 前 Start，cleanup 中带 timeout 调 Stop 后 wdCancel，Serve 返回后保留现有 Pool close。
8. preview_static.go 只绑定 127.0.0.1:0 或 ::1:0，服务 realpath workspace subtree，拒绝 root 外文件，stop 可重复；不占 owner port。
9. cmd/preview.go 保持 open --port/--path、list、close：open 互斥校验并调用 CreatePreview，owner 成功即输出固定人话且不等待 Chromium；list/close 分别调用 ListPreviews/ClosePreview；cmd 测试用现有 client HTTP harness 逐条断言方法、body、文案。
10. 新文件头写职责/边界；导出函数写参数、返回、幂等、为何落库后发 event；每个外部调用前后/错误/成功用结构化 logger。运行 GOMODCACHE=/root/.handoff/tmp/3da7945a/gomodcache go test ./internal/agentd ./internal/store ./internal/proto -run 'Preview' -count=1 与 GOMODCACHE=/root/.handoff/tmp/3da7945a/gomodcache go test ./cmd -run 'Preview' -count=1，预期触及包 ok。

测试范围：internal/agentd、internal/store、internal/proto Preview 测试与 cmd Preview 测试；不跑全仓。

### 3.3 接缝覆盖

- 测试入口是 Handler 的 create/list/close、websocket.Dial(GET /ws/previews)、CLI RunE；不是直接调 owner/hub。
- 覆盖 #5–8、#12–14、#18–24、#27–32、#36–39、#46–49；事件只能在 Store 成功后出现。
- Hub 纯订阅测试是附加锁，理由是只有它能单独证明慢消费者不阻塞 HTTP；不能替代 Handler 测试。

## 4. U2：client、Pool raw dial、独立 mirror

### 4.1 Interfaces

~~~go
func (p *Pool) DialContext(ctx context.Context, targetName, network, addr string) (net.Conn, error)

type PreviewMirror struct {
    pool         *targetclient.Pool
    owner        *PreviewOwner
    hub          *PreviewHub
    isSelfTarget func(string) bool
    log          *slog.Logger
    mu           sync.RWMutex
    sessions     map[string]proto.PreviewSession
    machines     map[string]proto.MachineStatus
    cancels      map[string]context.CancelFunc
    wg           sync.WaitGroup
}
func NewPreviewMirror(pool *targetclient.Pool, owner *PreviewOwner, hub *PreviewHub, isSelfTarget func(string) bool, log *slog.Logger) *PreviewMirror
func (m *PreviewMirror) Run(ctx context.Context)
func (m *PreviewMirror) Stop()
func (m *PreviewMirror) ListAll(ctx context.Context) (*proto.PreviewListResp, error)
func (m *PreviewMirror) Resolve(id, machine string) (proto.PreviewSession, bool)
~~~

Consumes：既有 client preview 方法、U1 owner List/Hub、targetclient Pool、MachineStatus。Produces：只读聚合 PreviewListResp、加盖 machine 的远端 PreviewEvent、(id,machine)→Session。

Pool.DialContext 只在 agentd preview opener 内部使用；targetName 必须登记；entry 删除/Pool close 后新 dial 失败；调用方关闭返回 conn，Pool 仍拥有 client/dialer。relay entry 用池内 Dialer 的 target-scoped raw path，direct entry 用既有 direct target transport，不把 coordinator localhost 当 owner；不增加 owner HTTP/CONNECT。raw relay framing 如确需新增，只能改 relay/dialer.go/listener.go，同时保留已有 HTTP app stream。

快照 key 为 machine + "\x1f" + session.ID；本机 machine 空，远端 event 盖 target name。ListAll 对失败 target 仍返回 MachineStatus{Name,Ok:false,FetchedAt,Error}；失败不清成本机正常，旧快照不冒充实时。

### 4.2 步骤、判据、范围

1. 基线复核：运行 GOMODCACHE=/root/.handoff/tmp/3da7945a/gomodcache go test ./internal/client ./internal/targetclient ./internal/agentd -count=1，预期 ok；已实际查到 Pool.For 与 relay Dialer.DialContext，流程不用 chain 冒充。
2. 在 internal/client/preview_test.go 用 fake owner httptest 记录 path/query/Auth/cancel，运行 go test ./internal/client -run 'Preview' -count=1：断言 ListPreviews→/api/previews、ListPreviewsAll→?scope=all、OpenPreview 的 machine 只在 query、REST 走 do/httpError、WS 走 wsDialOptions、无 task cursor。
3. 在 targetclient Pool 测试中运行 go test ./internal/targetclient -run 'Preview|Dial' -count=1：同 target 复用 entry；配置变更/Close 后 cleanup 和新 dial failure；未知 target failure；fake relay/direct 记录 network/addr。必要 relay 改动使用既有 dialer_test/listener_test yamux harness，断言旧 HTTP 流仍工作、raw destination 只经认证隧道。
4. 在 preview_mirror_test.go 运行 go test ./internal/agentd -run 'PreviewMirror' -count=1：两个 fake owner 第一次必须 list→WS；created/closed、重复 event、同 id 不同 machine、unknown type、部分 machines failure、本机 machine 空、coordinator close 不写 owner Store。
5. preview_mirror.go 实现每 target 一个 bounded supervisor；断线先 ListPreviews 替换快照，再 StreamPreviewEventsOnce；退避 300ms 起、10s 上限；不保存 task cursor、不调用任务 Mirror、不起无界 goroutine。通过 Hub 投影 event，owner 原 session 不回写。
6. server.go 的 scope=all 调 ListAll，无 scope 调 owner；open 只调 U3 PreviewOpener，不把 machine 当 forwarded header。Handler seam 断言 remote response 有 machine、owner 原始 response 无 machine。
7. raw 适配文件头/导出注释说明职责和 HTTP 兼容边界；每个外部调用前后及错误日志含 target/session/machine/addr，不记 token/nonce。运行 GOMODCACHE=/root/.handoff/tmp/3da7945a/gomodcache go test ./internal/client ./internal/targetclient ./internal/agentd -run 'Preview|Mirror|Dial' -count=1，预期 ok。

测试范围：internal/client、internal/targetclient、必要时 internal/relay 的 Preview/Dial 测试及 internal/agentd mirror/Handler 测试；不跑全仓。

### 4.3 接缝覆盖

- client 入口是五个 public preview method；Pool 入口是 Pool.DialContext；mirror 入口是真实 fake owner HTTP/WS；Handler 入口是 scope=all。
- 覆盖 #9–11、#18–22、#32–33、#40–45、#42–44；每次断线都断言 list-before-WS。
- key 纯函数测试是附加锁，理由是声明 HTTP/WS 缝不能单独构造跨 machine 同 id 回放碰撞；不能替代 fake owner WS。

## 5. U3：SOCKS/PAC、allowlist、隔离 Chromium

### 5.1 Interfaces

~~~go
type PreviewRawDial func(ctx context.Context, network, addr string) (net.Conn, error)
type PreviewAllowlist struct {
    loopback bool
    networks []*net.IPNet
    domains  map[string]struct{}
}
func ParsePreviewAllowlist(via []string) (PreviewAllowlist, error)
func (a PreviewAllowlist) Allows(host string) bool
func RenderPreviewPAC(socksURL string, allowlist PreviewAllowlist) ([]byte, error)
type PreviewProxy struct {
    sessionID string
    listener  net.Listener
    allowlist PreviewAllowlist
    dial      PreviewRawDial
    nonce     []byte
    log       *slog.Logger
    closeOnce sync.Once
}
func NewPreviewProxy(ctx context.Context, sessionID string, via []string, dial PreviewRawDial, log *slog.Logger) (*PreviewProxy, error)
func (p *PreviewProxy) Addr() net.Addr
func (p *PreviewProxy) Serve(ctx context.Context) error
func (p *PreviewProxy) Close() error

type PreviewLaunchSpec struct {
    SessionID string
    EntryURL string
    PACPath string
    ProxyServer string
    ProxyNonce string
    ProxyBypassList string
    UserDataDir string
}
type PreviewBrowserHandle struct { PID int; Done <-chan error }
type PreviewLauncher interface {
    FindExecutable(ctx context.Context) (string, error)
    Start(ctx context.Context, executable string, spec PreviewLaunchSpec) (PreviewBrowserHandle, error)
    Focus(ctx context.Context, pid int) error
    Stop(ctx context.Context) error
}
type previewProcess struct {
    proxy       *PreviewProxy
    pacPath     string
    userDataDir string
    browser     PreviewBrowserHandle
}
type PreviewOpenService struct {
    owner     *PreviewOwner
    mirror    *PreviewMirror
    pool      *targetclient.Pool
    launcher  PreviewLauncher
    log       *slog.Logger
    mu        sync.Mutex
    processes map[string]*previewProcess
}
func NewPreviewOpenService(owner *PreviewOwner, mirror *PreviewMirror, pool *targetclient.Pool, launcher PreviewLauncher, log *slog.Logger) *PreviewOpenService
func (o *PreviewOpenService) OpenPreview(ctx context.Context, id, machine string) (*proto.PreviewOpenResp, error)
func (o *PreviewOpenService) Stop(ctx context.Context) error
~~~

Consumes：U2 Resolve/Pool.DialContext、U1 owner local Touch、PreviewSession。Produces：loopback SOCKS、PAC、独立 browser process、PreviewOpenResp。

loopback 三 host 始终允许；via 每项只能单 IP/CIDR/规范化域名；非 loopback 非名单 CONNECT 明确拒绝而非 DIRECT；SOCKS 二次校验，DNS 在 owner side；不支持 wildcard/regex/host:port。listener 绑定 127.0.0.1:0；session nonce、临时 PAC/profile 标识不写日志；同 session 存活 PID 再点只 Focus。

Chromium 参数必须有 --user-data-dir、--proxy-pac-url（或等价 PAC）、--proxy-bypass-list=<-loopback> 和 EntryURL；不调用 open/xdg-open/rundll32/default browser/Wails。关 browser 只回收本地 child/proxy/profile，不 owner close。

### 5.2 步骤、判据、范围

1. 基线复核：运行 GOMODCACHE=/root/.handoff/tmp/3da7945a/gomodcache go test ./internal/agentd ./internal/targetclient ./internal/relay -count=1，预期各 package ok；不把真 Chromium 算作通过。
2. 在 preview_proxy_test.go 写实际 SOCKS 请求失败测试并运行 go test ./internal/agentd -run 'PreviewProxy|PreviewAllowlist' -count=1：接受 loopback/IP/CIDR/domain，拒绝 wildcard/regex/port/空；Proxy.Addr 是 loopback 且 port 非零；非名单 CONNECT reject，名单调用 PreviewRawDial(ctx,"tcp",addr)。
3. preview_proxy.go 实现 bounded SOCKS5 协商/认证/CONNECT，先 allowlist 再同一路径 raw dial，context 超时，随机端口+一次性 nonce，成功/拒绝/raw dial 前后结构化日志而不记 nonce。
4. preview_launcher_test.go 用 fake launcher 捕获 executable/spec，运行 go test ./internal/agentd -run 'PreviewOpen|PreviewLauncher' -count=1：断言四项 flags、profile 每 session 独立、不含系统 opener；活 PID 第二次只 Focus；Start/Focus failure 是 Opened:false 且 cleanup。
5. preview_launcher.go 按 (id,machine) Resolve；machine 为空用本机 raw dial，非空 Pool.DialContext；proxy/PAC/profile→detect executable→Start，失败逆序 cleanup；Done 只清本地资源；fork 后立即失败不伪造成功；本机 attached lease 调 owner Touch。
6. unix/windows 文件只实现 discovery、参数、进程组停止、Focus；OS 单测用 fake exec/handle，不启动浏览器。文件头写 OS 边界；导出注释写 cleanup 和 --proxy-bypass-list=<-loopback> 的必要原因。
7. 接入 preview.go/server.go/cmd/agentd.go 的 opener 生命周期；Stop 等 child/proxy 结束；TTL/close 同一路径 cleanup；每外部调用前后/错误/成功带结构化 logger。运行 GOMODCACHE=/root/.handoff/tmp/3da7945a/gomodcache go test ./internal/agentd ./internal/targetclient ./internal/relay -run 'Preview|Dial' -count=1，预期 ok；浏览器加载 owner localhost 仍未验证。

测试范围：agentd proxy/launcher/open，targetclient/relay raw dial；不跑全仓和真实 Chromium。

### 5.3 接缝覆盖

- 入口分别为实际 SOCKS CONNECT、Pool.DialContext、PreviewOpenService.OpenPreview、POST /open Handler；不能只测 PAC 字符串。
- 覆盖 #15–17、#30–31、#44、#47–49；二次 allowlist、no-bind、flags、cleanup 都有可变红断言。
- allowlist 纯解析测试是附加锁，理由是 HTTP open 缝无法穷举语法；不能替代实际 SOCKS 请求。

## 6. U4：web 聚合树与第四种 preview 行

### 6.1 Interfaces

~~~ts
export interface PreviewRowModel {
  session: PreviewSession
  projectId: string | null
  machine: string
  label: string
}
export interface PreviewState {
  data: PreviewListResp | null
  error: string
  opening: ReadonlySet<string>
  open: ReadonlySet<string>
}
export function previewKey(session: PreviewSession, machine: string): string
export function normalizePreviewOrigin(raw: string): string
export function previewLabel(session: PreviewSession): string
export function usePreviews(): {
  data: PreviewListResp | null
  error: string
  refresh: () => void
  open: (id: string, machine: string) => Promise<void>
  isOpen: (id: string, machine: string) => boolean
}
~~~

ProjectTreeProps 新增：

~~~ts
previews: PreviewSession[]
previewOpenKeys: ReadonlySet<string>
previewOpeningKeys: ReadonlySet<string>
onOpenPreview: (id: string, machine: string) => void
~~~

~~~ts
export interface NodeCounts {
  dirs: number
  running: number
  pending: number
  previews: number
}
export function countsForProject(tasks: Task[], project: ProjectNode, previews: ReadonlyArray<PreviewSession> = []): NodeCounts
export function countsForMachine(tasks: Task[], project: ProjectNode, machine: string, previews: ReadonlyArray<PreviewSession> = []): NodeCounts
export function filterTree(tree: ProjectTreeResp, tasks: Task[], rawQuery: string, openedItems: OpenItem[] = [], previews: ReadonlyArray<PreviewSession> = []): TreeFilter
~~~

Consumes：冻结 API/WS、ProjectTreeResp、TasksResp、MachineStatus、既有 OpenItem/workbench callbacks。Produces：preview row、local open/opening、openPreview(id,machine)。preview 不进 OpenItem、onOpenTask、onFocusOpenItem、drag MIME 或 workbench。

project join 用与 internal/projectid.FromOrigin 同口径的 normalized origin 内部 key，不增加 project_id wire。无匹配 session 进未归属；MachineStatus error 保持可见；key 必含 machine。label 从 entry_url 得 localhost:<port>，branch 非空为 branch · localhost:<port>。

### 6.2 步骤、判据、范围

1. 基线复核：运行 cd web && npm test -- --run src/app/tree/counts.test.ts src/app/tree/search.test.ts src/app/tree/ProjectTree.test.tsx src/app/shell/Shell.test.tsx && npm run typecheck，预期 Test Files 4 passed、typecheck 无输出。
2. usePreviews.test.ts 以 fetchPreviews/openPreview/WebSocket mock 写失败 seam 并运行 npm test -- --run src/app/data/usePreviews.test.ts：初次调用 fetchPreviews('all')；WS URL 无 machine query；created/closed 以 machine+id 去重/删；并发 open 去重；reject 不留 isOpen 且暴露错误。
3. usePreviews.ts 实现 fetch 后订阅，unmount/close/断线清 socket/timer；只接受两个冻结 event；成功 open 才加入短暂 open set；沿现有 preview.ts/ws.ts，不改 URL 语义。
4. ProjectTree.test.tsx 用既有 props harness 运行 npm test -- --run src/app/tree/ProjectTree.test.tsx：origin match 入项目任务组，unowned 入未归属；显示 localhost:5173/branch · localhost:5173、machine dot/name；普通行无 data-open/aria-current，open 行仅 data-open=true；点击调用 onOpenPreview，onOpenTask=0，drag 无既有 MIME，无 workbench callback。
5. ProjectTree 的 TaskIconSlot/TaskRow 增加 preview 专用分支，OpenItem.kind 仍 tui|terminal|file；preview draggable=false/selected=false，保留任务组顺序；项目/机器 count 用 running+pending+previews。
6. counts/search 测试补充：相同 normalized origin+machine 才计 active；query 命中 project/machine/port/branch；closed/stale 不计，unowned/failure 可见。Shell.test.tsx 运行真实 Shell→hook→Tree→openPreview，断言没有 wb.openOrFocus/openTaskTui，reject 回普通态。
7. 新文件头写 fetch/WS/local projection 边界；导出函数写参数/返回/缺失字段注意事项；ProjectTree 注释解释 preview 不进 workbench 闭集。运行 cd web && npm test -- --run src/api/contract.test.ts src/app/data/usePreviews.test.ts src/app/tree/counts.test.ts src/app/tree/search.test.ts src/app/tree/ProjectTree.test.tsx src/app/shell/Shell.test.tsx && npm run typecheck，预期列出的 Test Files 通过且 typecheck 无输出。

测试范围：web contract、usePreviews、tree counts/search/ProjectTree、Shell harness、typecheck；不跑全仓/真浏览器。

### 6.3 接缝覆盖

- 入口是 usePreviews mount、ProjectTree render/click、Shell→hook→Tree→openPreview；不是直接调用 helper。
- 覆盖 #9–11、#22、#33–35、#50 及三重闸门 C：project/unowned、machine、label、count/search、is-open、no task/workbench/drag。
- key/origin/label pure tests 是附加锁，理由是声明缝无法分别枚举 URL 规范化、zero port/empty branch、machine+id collision；不能替代 Tree/Shell。

## 7. U5：真实序列化边界回归

### 7.1 Interfaces

U5 不加生产 API；Go 使用真实 Handler、Client、WS 文本和 U3 fake launcher，Web 使用现有 golden JSON 原始文本：

~~~go
func TestPreviewRegressionOwnerMirrorProjection(t *testing.T)
~~~

~~~ts
export function renderPreviewRegressionFixture(listJSON: string, eventJSON: string): PreviewSession[]
~~~

Consumes：U0–U4 DTO/HTTP/WS/TS projection。Produces：owner truth→transport text→coordinator machine projection→web row/open callback 的单链结果；optional via/origin_url/branch 缺席和 ttl_seconds:0/port 0 零值必须分开。

### 7.2 步骤、判据、范围

1. 基线复核：Go 运行 GOMODCACHE=/root/.handoff/tmp/3da7945a/gomodcache go test ./internal/proto ./internal/client ./internal/agentd ./cmd -count=1；Web 运行 cd web && npm test -- --run src/api/contract.test.ts src/app/tree/ProjectTree.test.tsx src/app/shell/Shell.test.tsx && npm run typecheck；这些基线均已实际通过。
2. preview_regression_test.go 先写真实 owner POST、client list、owner WS created/closed、mirror list-before-WS、machine 盖章/owner 不盖、owner-only close、open query→fake launcher、allowlist/Pool raw dial、agentd stop cleanup 的失败回归；每条注明 contract §3.1 编号。
3. preview_regression.test.tsx 读 web/src/api/testdata/Preview*.json（含 PreviewZeroValues.json）的原始文本，经 JSON.parse/PreviewSession projection 后挂 ProjectTree；断言 machine/machines error、branch/localhost、unowned、duplicate event、open callback、无 workbench/drag、ttl_seconds:0 与 optional 缺席不同；请求 port=0 断言为非法输入而不是把它与缺席请求混为成功；不得只比较 fixture 文本。
4. 运行 GOMODCACHE=/root/.handoff/tmp/3da7945a/gomodcache go test ./internal/agentd -run '^TestPreviewRegressionOwnerMirrorProjection$' -count=1 与 cd web && npm test -- --run src/app/preview_regression.test.tsx，预期实际输出 ok/Test Files 1 passed；失败原文落台账。
5. 重跑 U0–U4 的最小 Go Preview 与 Web contract/usePreviews/tree/Shell/typecheck 命令并记录 output；不把全仓测试归给 U5。
6. 回归文件头说明“不证明真实 Chromium/OS/DNS/桌面”；helper 注释写 JSON 缺失/零值边界；每外部调用/错误有结构化日志或已有 logger harness，禁 print。

测试范围：新增 Go/Web 回归、U0–U4 触及包、Web typecheck；不把真机伪装成自动测试。

### 7.3 序列化与接缝清单

同一 golden JSON/文本链路必须逐条断言：port/path/via 二选一和缺席/零值；owner 200 Session 的 entry/cwd/RFC3339/TTL；完整 created/closed frame；scope=all machine/machines；OpenPreview machine query/Opened/is-open；unknown/重复 event、unreachable、重复 close、open failure；project/unowned/machine/label/count/search、无 workbench/drag。

这条测试贯穿 Go JSON encode/decode、HTTP response decode、WS text decode、mirror machine projection、TS JSON.parse/tree projection，不能用“Go 和 TS 各自通过”代替。

## 8. 缺陷族对抗审查

| 缺陷族 | 计划结论 |
| --- | --- |
| 生命周期中断 | Store 条件 close/expire/touch；owner Restore/Stop；mirror bounded list-before-WS；proxy/child 逆序 cleanup；U5 stop/reconnect。跨 OS 收尸仍未验证。 |
| 序列化丢字段 | U0 roundtrip、owner 完整 event、mirror 只盖 machine、U4/U5 raw JSON→TS 且区分缺席/零值。 |
| 静默/误导错误 | machine failure 带原文，Opened:false，CLI 不等 Chromium，proxy/launcher/route 错误带上下文。 |
| 跨平台 | 集中 launcher + unix/windows 文件；禁止系统 opener；三平台真实结果未验证。 |
| 假红/假绿 | 每单元至少一个 Handler/client/Pool/proxy/open/tree/Shell seam；pure helper 只能附加。 |
| 门禁/SSRF | PAC + SOCKS 二次 allowlist、<-loopback>、Pool target 绑定、无 HTTP/CONNECT；非名单 CONNECT 可变红。 |
| 枚举白名单 | 仅两个冻结 event；preview 只在 tree 专用 kind；OpenItem/workbench 闭集不变；unknown event 明确处理。 |
| 承重安全 | machine+id key、SQL 条件 close、open 去重、每 session profile/proxy/nonce；U5 并发/重放。 |

## 9. 未验证，需协调者真机执行

1. owner 真实运行 handoff preview open --port <n> 和 --path <workspace-relative>，确认 port 由 owner 服务监听、path 相对资源可用、entry 是 owner localhost；本机同号占用时没有 bind/抢占。
2. 另一台桌面点击第四种行，确认 Chromium 在点击桌面启动/聚焦、独立 user-data-dir、地址仍是 owner localhost，不是 iframe/default browser/workbench；再次点击只聚焦。
3. 访问 owner loopback 与 via IP/CIDR/domain 及未列目标，确认 owner-side DNS、拒绝策略、PAC DIRECT 不绕 SOCKS、本机已有 Vite/8080 不收 owner 请求。
4. 关闭 Chrome、preview close、TTL idle、owner/coordinator 重启、网络断开恢复，观察 Store truth、mirror、child、进程组、proxy、PAC/profile 回收；确认关 Chrome 不删 session、attached Chromium 可续命。
5. Linux/macOS/Windows 分别验证 executable、权限、桌面 session、进程组、临时目录、proxy flags；不得跨平台外推。
6. owner unreachable 时确认 Web 显示 machine failure/可行动状态而非本机 truth；恢复后 list-before-WS 补齐且不重复。
7. 两桌面并发点/关同一或不同 session，确认 nonce/profile/proxy/upstream 隔离，无 TOCTOU、双重回收、跨 machine id 碰撞。

协调者执行以上清单；不派发、不调用 handoff CLI；每项记录 OS、机器、命令/观察原文和结论，未观察写“未验证”。

## 10. 自审、图债、交棒

### 10.1 spec 与占位符

用户故事 1/2/3/4/5/6 具体归属：U1 owner create/WS/CLI/lifecycle；U2 mirror/transport；U3 proxy/Chromium/no-bind；U4 row/click/count/search；U5 跨域回归。故事 2 的真实 no-bind、故事 4 的真实 DNS、故事 5 的真实 cleanup 另归 §9。

本稿的每个实现分支都给出了文件、精确签名、可判定断言和既有 harness；relay raw framing 的条件分支也限定到两个文件、既有 yamux harness、HTTP 兼容判据和“不新增 owner HTTP/CONNECT”边界，没有把未知实现留给执行者猜。

### 10.2 双向接缝

owner JSON/Store→JSON 测试；owner HTTP→Handler httptest；owner WS→websocket.Dial；client→五个 public method；mirror→fake HTTP/WS supervisor；Pool→DialContext；SOCKS→实际 CONNECT；open→POST Handler→OpenService→fake launcher；web→hook mount/WS/open；tree→ProjectTree click/drag；Shell→Shell fixture；U5→Go raw text→TS projection。每条均有 seam-level 测试，内部纯测试仅作已声明附加锁。

### 10.3 图覆盖债与协调者审计

本节点实际按 codegraph context d_orchestration/d_gateway/d_transport/d_web、sym Server.Handler、Pool.For、Client.StreamEventsOnce 圈文件；未用 chain 冒充 flow。可解析的现状入口锚点为 `internal/agentd/server.go#Server.Handler`、`internal/targetclient/pool.go#Pool.For`、`internal/client/client.go#Client.StreamEventsOnce`。历史 cards-B294-charter-2 view 的 check 原文为：

~~~text
Error: 视图 cards-B294-charter-2 引用不完整: [新增节点 n_web_api_preview_closePreview 引用不存在的容器 k_web_api_preview 新增节点 n_web_api_preview_createPreview 引用不存在的容器 k_web_api_preview 新增节点 n_web_api_preview_fetchPreviews 引用不存在的容器 k_web_api_preview 新增节点 n_web_api_preview_openPreview 引用不存在的容器 k_web_api_preview]
~~~

本计划不宣称历史图视图通过。落盘后由协调者实际运行 resolve --doc docs/superpowers/plans/b294-plan.md；失败须保留原文并修锚。冻结物逐条核对、A Produces/B Consumes 逐字符比对、spec 故事具体 task 归属的独立跨卡审计由协调者执行，不派发本 task。

### 10.4 交棒条件

implement 只按 U0→U5 序贯执行；交回变更文件、每个最小命令原始输出、U5 输出、真机清单、commit hash。没跑的命令写未验证；接缝只靠内部 helper 则补 seam；想扩 wire/route/event kind 则退回 contract。
