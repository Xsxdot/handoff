# B294 契约增量：远端预览会话与隔离 Chromium

**上游状态：已批准**（源 spec：`docs/superpowers/specs/b294.md`，头部状态行已核对）
**级别：L3 ｜选档：轻档**（单条主旅程；直通竖切归重档，本节点落空壳与直通镜像）
**冻结物：**本文档、`codegraph/best.json`、`codegraph/target.json`、
`codegraph/diffs/cards-B294-charter-2.json` 与本提交 Ticket 0 骨架，随本提交冻结。
**本节点：**charter / contract；**交棒：**breakdown。
**架构形态：**按子系统分域的平铺领域包，无横向 controller/service/dao 分层。

## 1. 现状查证

本轮先以 `git status --short --branch && git log -1 --oneline` 核对工作树：分支为
`cards/B294-charter-2`，基线 HEAD 为 `5e8826f7`，工作树干净。源 spec 头部已经是
「已批准」，没有回写动作。以下每个新增签名都对应本提交的现状代码；既有签名用
符号锚和现状行号，行号漂移时以锚为准。

| 契约事实 | 现状代码出处 | 本卡关系 |
| --- | --- | --- |
| agentd API/WS 统一从 `Server.Handler` 注册 | `internal/agentd/server.go#Server.Handler`（现状 489-633） | 新增四个 preview API 路由和一个 WS 路由 |
| owner 与 coordinator 使用同一 HTTP client 选路 | `internal/agentd/server.go#Server.Pool`（现状 374-378）；`internal/targetclient/pool.go#Pool.For`（现状 97-137） | mirror/开窗实现复用池，不新造连接方向 |
| 现有任务镜像按 machine 发现并订阅 owner | `internal/agentd/mirror.go#Mirror`、`#Mirror.discoverOnce`、`#Mirror.subscribe` | preview 镜像沿相同发现/重连边界，但使用独立 DTO/事件 |
| 现有任务 WS 单连接、调用方持水位、零读超时 | `internal/client/client.go#Client.StreamEventsOnce`（现状 1558-1578） | preview WS 不持 cursor；重连前重新 list |
| client WS 使用自有 HTTP transport | `internal/client/client.go#Client.NewWithWSTiming`（现状 247-281）、`#Client.wsDialOptions`（现状 308-339） | preview WS 不绕过 relay/直连选路 |
| client HTTP 请求由 `do` 统一加 Bearer、上下文和错误 | `internal/client/client.go#Client.do`（现状 398-439）、`#Client.httpError`（现状 447-460） | preview REST 镜像只调用既有底座 |
| project display name 不是现有跨机树的稳定契约 | `internal/proto/projects.go#ProjectNode`（现状 84-98）；`internal/projectid/projectid.go#FromOrigin`（现状 107） | session 只带 origin，桌面按 origin 派生 project_id |
| 左栏任务行的 kind 当前是闭集 | `web/src/app/tree/ProjectTree.tsx#OpenItem`（现状 54-70）、`#TaskRow`（现状 1113-1161） | implement 扩展第四种 preview；本节点不提前改页面行为 |
| web 通过同源 `/api`、`/ws` 反代访问 agentd | `web/vite.config.ts`（现状 8-46）；`web/src/api/client.ts#request`（现状 127-135） | 浏览器只接本机 agentd，不直拨 owner |

### 1.1 依赖库既成行为

这些默认值是连接契约的一部分，本轮读的是当前 module cache 源码，不凭印象推断：

| 行为 | 出处 | 对 B294 的约束 |
| --- | --- | --- |
| yamux 默认 backlog=256 | `/root/go/pkg/mod/github.com/hashicorp/yamux@v0.1.2/mux.go:61-72` | preview 复用现有 relay session，不另改 backlog |
| yamux keepalive 开启、间隔 30s | 同上 `DefaultConfig` | owner 不可达由既有连接生命周期暴露；不把 keepalive 改成 preview 私有值 |
| yamux connection write timeout=10s | 同上 | SOCKS/WS 写入沿用已有 relay 安全阀 |
| yamux stream close timeout=5m | 同上 | 不在 DTO 中制造第二套 stream close 语义 |
| yamux stream open timeout=75s | 同上 | tunnel 建流失败按连接错误返回，不在浏览器侧伪造成功 |
| coder/websocket 单消息默认读限 32768 | `/root/go/pkg/mod/github.com/coder/websocket@v1.8.15/read.go:88-107`、`ws_js.go:82-87` | preview client 明确设置与 agentd 转发相同的 512KiB 读限；不使用无限读 |
| agentd WS 转发拨号预算=10s、读限=512KiB | `internal/agentd/forward_ws.go#wsDialBudget`（现状 30）、`#wsForwardReadLimit`（现状 136-138） | coordinator→owner 的 preview 订阅沿既有边界 |
| `http.Client.Timeout=0`，调用方用 context 控制 | `internal/client/client.go#Client.HTTPClient`（现状 294-306） | 常驻 preview WS 不设 client 总超时；单次 dial 仍受 `dialTimeout=10s` |

### 1.2 对侧常量执法

| 常量/字面值 | 当前生产者 | 当前消费者 | 结论 |
| --- | --- | --- | --- |
| `preview.created` | 本提交 `internal/proto/preview.go#PreviewEventCreated` | 本提交 DTO/fixture 定义；既有实现零使用 | 新冻结字面值；在 implement 接入 owner emit 与 web consumer |
| `preview.closed` | 本提交 `internal/proto/preview.go#PreviewEventClosed` | 本提交 DTO/fixture 定义；既有实现零使用 | 新冻结字面值；在 implement 接入 owner emit 与 web consumer |
| `X-Handoff-Forwarded` | `internal/agentd/forward.go#forwardedHeader` | `#Server.forwardIfRequested`、`#forwardJSON`、`internal/agentd/forward_ws.go#forwardWS` | 活跃既有一跳防环头；preview mirror 不另造头 |
| `scope=all` | `internal/agentd/projecttree.go#Server.handleProjectTree`、`internal/agentd/tasksfanout.go#Server.tasksAll` | web `fetchProjectTree`/`fetchTasks` | 活跃现状；preview list 沿用同一查询语义 |

零使用的 preview 常量不能反过来作为“已有生产/消费事实”；它们只在本提交的可执行
fixture 中先冻结线格式，真正生产/消费由 implement 节点接线。

## 2. 精确签名与端点

### 2.1 协议 DTO（`internal/proto/preview.go`）

```go
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
```

现状出处（本提交）：`internal/proto/preview.go:5-56`。`Machine` 仅是 coordinator
投影字段；owner 返回空串时因 `omitempty` 缺席。`Via` 缺席表示 loopback 全端口默认
投影；非空值才扩展 IP/CIDR/域名 allowlist。`TTLSeconds` 是线上的秒数，不把 Go
`time.Duration` 直接暴露给 TS。

### 2.2 owner/coordinator HTTP 与 WS

| 方法 | 路径 | 成功响应 | 语义 |
| --- | --- | --- | --- |
| POST | `/api/previews` | 200 + `PreviewSession` | owner 创建并持久化；请求体为 `PreviewOpenReq`，`port/path` 二选一 |
| GET | `/api/previews` | 200 + `PreviewListResp` | owner 侧只列未关闭/未过期 session；`machines` 缺席 |
| GET | `/api/previews?scope=all` | 200 + `PreviewListResp` | coordinator 读本机与所有 owner 镜像；远端 session 盖 `machine`，机器失败逐项进 `machines` |
| DELETE | `/api/previews/{id}` | 200 + `PreviewCloseResp` | 只由 owner 真正关闭；coordinator/CLI 不改远端 truth |
| POST | `/api/previews/{id}/open?machine=` | 200 + `PreviewOpenResp` | 当前点击桌面的本机 agentd 开/聚焦 Chromium；`machine` 只作 owner 选择，不把请求转成浏览器到 owner 的反向拨号 |
| GET | `/ws/previews` | 101，文本 JSON `PreviewEvent` 帧 | 当前 agentd 的实时 preview 事件；浏览器只连本机，owner 订阅由 coordinator agentd 持有 |

HTTP 空壳已在 `internal/agentd/preview.go#Server.handlePreviewCreate`、
`#Server.handlePreviewList`、`#Server.handlePreviewClose`、
`#Server.handlePreviewOpen`、`#Server.handlePreviewWS` 落码，并由
`internal/agentd/server.go#Server.Handler` 注册。Ticket 0 统一返回 503
`{"error":"预览会话尚未接线"}`；实现票替换 handler 内部，不改变路由和 DTO。

### 2.3 transport/client

以下签名均已在 `internal/client/preview.go` 有现状代码出处：

```go
func (c *Client) CreatePreview(ctx context.Context, req proto.PreviewOpenReq) (*proto.PreviewSession, error) // :16
func (c *Client) ListPreviews(ctx context.Context) (*proto.PreviewListResp, error) // :33
func (c *Client) ListPreviewsAll(ctx context.Context) (*proto.PreviewListResp, error) // :39
func (c *Client) listPreviews(ctx context.Context, path, op string) (*proto.PreviewListResp, error) // :43
func (c *Client) ClosePreview(ctx context.Context, id string) (*proto.PreviewCloseResp, error) // :60
func (c *Client) OpenPreview(ctx context.Context, id, machine string) (*proto.PreviewOpenResp, error) // :79
func (c *Client) StreamPreviewEventsOnce(ctx context.Context, onEvent func(proto.PreviewEvent) error) error // :102
```

REST 方法使用现有 `Client.do` / `Client.httpError`；`StreamPreviewEventsOnce` 连接
`/ws/previews`，不读写任务 cursor、不重连，调用方必须先 list，断线后重新 list 再
建立连接。`OpenPreview` 的 `machine` 留在 query，避免把 owner 创建 DTO 污染成路由
请求体；它不调用 `forwardIfRequested`。

### 2.4 CLI 与 web 镜像

CLI 壳现状出处：`cmd/preview.go#previewOpenCmd.RunE`（现状 :26）、
`#previewListCmd.RunE`（现状 :47）、`#previewCloseCmd.RunE`（现状 :71）。固定用法：

```text
handoff preview open --port <n> [--via <逗号分隔名单>]
handoff preview open --path <workspace-relative> [--via <逗号分隔名单>]
handoff preview list
handoff preview close <id>
```

`open` 成功的人话是“预览已发到桌面，在对应项目任务组点开”，不等待 Chromium ack；
Ticket 0 因 agentd 503 返回错误。`--via` 的解析交给 cobra `StringSlice`，实现票
负责把它传入 owner 请求，不在 CLI 解析 DNS。

web 类型镜像已在 `web/src/api/types.ts#PreviewOpenReq`（现状 :202）、
`#PreviewSession`（:209）、`#PreviewListResp`（:221）、`#PreviewEvent`（:228）、
`#PreviewOpenResp`（:234）、`#PreviewCloseResp`（:235）。API 函数已在
`web/src/api/preview.ts#fetchPreviews`（:11）、`#createPreview`（:15）、
`#closePreview`（:19）、`#openPreview`（:23）；本机 WS URL 已在
`web/src/api/ws.ts#previewEventsURL`（:193）。页面仍由 implement 票扩展 TaskRow，
本节点不把 preview 提前接入 workbench。

## 3. 语义冻结

### 3.1 接缝断言（原子清单）

每条都是可独立 pass/fail 且失败可定位的断言：

1. `PreviewOpenReq` 的 `port` JSON 键只承载端口整数。
2. `PreviewOpenReq` 的 `path` JSON 键只承载 workspace-relative 路径。
3. `PreviewOpenReq` 的 `via` JSON 键只承载本会话 allowlist。
4. `port` 与 `path` 的合法请求形态为二选一。
5. owner 创建成功响应的状态码为 200。
6. owner 创建成功响应体为 `PreviewSession`。
7. owner 列表路径为 `GET /api/previews`。
8. owner 列表只包含未关闭且未过期的 session。
9. coordinator 聚合列表路径为 `GET /api/previews?scope=all`。
10. 聚合响应中的远端 session 带 owner machine 名。
11. 聚合响应中的机器失败记录在 `PreviewListResp.machines`。
12. 关闭路径为 `DELETE /api/previews/{id}`。
13. 关闭成功响应体为 `PreviewCloseResp`。
14. 关闭动作由 owner 作为权威写入方。
15. 本机开窗路径为 `POST /api/previews/{id}/open`。
16. 远端开窗的 owner 机器名只放在 `machine` query。
17. 开窗成功响应体为 `PreviewOpenResp`。
18. 实时事件路径为 `GET /ws/previews`。
19. 实时事件帧是 JSON 文本 `PreviewEvent`。
20. 创建事件字面值为 `preview.created`。
21. 关闭事件字面值为 `preview.closed`。
22. 关闭事件携带完整 `PreviewSession`。
23. `PreviewSession.entry_url` 是 owner 产生的入口 URL。
24. `PreviewSession.cwd` 是 owner 创建时的工作目录。
25. `PreviewSession.origin_url` 缺省时从线格式中缺席。
26. `PreviewSession.branch` 缺省时从线格式中缺席。
27. `PreviewSession.created_at` 使用 JSON RFC3339 时间。
28. `PreviewSession.ttl_seconds` 使用整数秒。
29. owner 未提供 `via` 时线格式中缺席 `via` 键。
30. owner 未提供 `via` 时实现默认投影 owner loopback 全端口。
31. `via` 非空时实现只扩展该 session 的 allowlist。
32. owner 响应不写 coordinator machine 名。
33. coordinator 投影可写入 `machine`，不回写 owner truth。
34. web fetch 默认读取 `scope=all`。
35. 浏览器 preview WS URL 不带 `machine` query。
36. CLI `preview open` 调用 `Client.CreatePreview`。
37. CLI `preview list` 调用 `Client.ListPreviews`。
38. CLI `preview close` 调用 `Client.ClosePreview`。
39. CLI 不等待 Chromium ack 即可在 owner 成功后返回人话。
40. `StreamPreviewEventsOnce` 不使用任务 cursor。
41. `StreamPreviewEventsOnce` 不负责重连。
42. transport preview REST 复用 `Client.do`。
43. transport preview WS 复用 `Client.wsDialOptions`。
44. agentd preview WS 不让浏览器直连 owner。
45. owner unreachable 时 coordinator 不把 session 冒充本机 session。
46. Ticket 0 五个 preview handler 返回 503 未接线错误。
47. Ticket 0 不创建 workspace 服务 HTTP listener。
48. Ticket 0 不启动 Chromium。
49. Ticket 0 不生成 PAC 或 SOCKS nonce。
50. Ticket 0 不改变 `TaskRow` 的现有 `tui|terminal|file` 闭集。

### 3.2 三重闸门拍板记录

**拍板 A（owner 权威 + coordinator 只读镜像）：**session 真相放在发布机器，关闭也
必须回 owner；coordinator 只通过 list/WS 投影，不在本地覆盖远端记录。这是跨
`d_gateway`、`d_transport`、`d_protocol` 与 web 的难逆转边界；后人若只看本机库会
惊讶为何不能直接删行；被否掉的方案是 coordinator 中央写一份 session ledger，明确
不做中央 ledger，因为它会在 owner 断线与多桌面并发关闭时制造两个 truth。

**拍板 B（网络命名空间投影，不做端口绑定/像素投影）：**浏览器地址仍是
`localhost:<owner-port>`，但由点击桌面的本机 Chromium 通过本机 SOCKS/PAC 把
loopback 解析投影到 owner；本机 agentd 不监听 owner 端口。这同时锁住 owner
loopback 的安全边界并触及 transport、agentd、web 三个子系统；后人看到 localhost
却发现本机没有该端口可能会想修掉它；被否掉的是同号本地 bind、iframe/path proxy
与像素/RDP，明确不做这些替代路径。

**拍板 C（点击桌面本地开独立 Chromium，不进入 workbench）：**同一 session 可被
多个桌面看到，点击哪台就在哪台以独立 `user-data-dir` 开窗；不复用 `onOpenTask`、
不 drag 到 center、不在控制台加第二页。这会同时约束 agentd、web TaskRow、workbench
回调和安全隔离；后人只看任务行会惊讶它不是 TUI；被否掉的是 iframe、系统默认浏览器
与 workbench tab，明确不做自动弹窗和第二个 Wails 窗口。

## 4. Ticket 0 边界与交棒欠账

本提交只落：

- DTO、事件常量、客户端 REST/单次 WS 镜像签名；
- agentd 五条 route handler 与 `Server.Handler` 的真实接线，空壳统一 503；
- CLI 三个子命令的 cobra 壳与 client 透传；
- web API/types/WS URL 镜像与 Go→web JSON fixture；
- `best.json` 新增 `k_web_api_preview` 容器，`target.json` 增补 allowed entries，
  本分支视图 diff 记录全部新增骨架符号。

本提交明确不落：owner SQLite 表与 TTL/idle 回收、path realpath 校验、端口监听校验、
workspace HTTP 服务、SOCKS server/upstream、DNS owner-side resolution、PAC、
Chromium detection/独立 profile/focus、跨机 preview mirror、任务组第四种 row、
projectid 匹配/“unowned”、搜索/计数、机器点与错误原文。这些是 implement 节点欠账，
不是本节点的“已有活路径”。轻档不执行重档法定直通竖切；由后续 plan 的最薄路径承接。

欠账清单：

1. owner persistence 与 `preview.created`/`preview.closed` producer 尚未接线。
2. coordinator list/WS mirror 与 reconnect catch-up 尚未接线。
3. local SOCKS/PAC/Chromium open/focus 尚未接线。
4. path/port/via 安全校验尚未接线。
5. web TaskRow、计数、搜索、unowned 与不拖拽行为尚未接线。

上述欠账逐条交给 breakdown，不宣称本节点已实现。

## 5. 可执行冻结与命令证据

JSON 编码格式命中可执行冻结：本提交把 `PreviewOpenReq`、`PreviewSession`、
`PreviewListResp`、`PreviewEvent`、`PreviewOpenResp`、`PreviewCloseResp` 的金样本写入
`web/src/api/testdata/Preview*.json`，由 `internal/proto/TestContractFixtures` 逐字节
比较，并由 `web/src/api/contract.test.ts` 做 TS 镜像断言。B294 没有哈希或密钥派生，
故“哈希/密钥金样本”不适用；不是遗漏。

本轮实际命令与结果：

- `go test ./internal/proto ./internal/client ./internal/agentd ./cmd`：首次因默认
  module cache 只读失败；原始错误为 `go: writing go.mod cache: open /root/go/pkg/mod/cache/download/github.com/!xsxdot/charter/graph/@v/v0.10.0.mod430332283.tmp: read-only file system`、
  `open /root/go/pkg/mod/cache/download/github.com/!xsxdot/charter/graph/@v/v0.10.0.lock: read-only file system`。
- `GOMODCACHE=/root/.handoff/tmp/50d413fc/gomodcache go test ./internal/proto ./internal/client ./internal/agentd ./cmd`：`internal/proto`、`internal/client`、`cmd` 输出 `ok`；`internal/agentd` 首次并行编译输出暂缺，随后单独命令得到 `ok  github.com/Xsxdot/handoff/internal/agentd 277.487s`。
- `GOMODCACHE=/root/.handoff/tmp/50d413fc/gomodcache go test ./internal/agentd -run '^TestPreviewTicket0HandlersReturnUnwired$' -count=1`：`ok  github.com/Xsxdot/handoff/internal/agentd 0.004s`。
- `npm test -- --run src/api/contract.test.ts`（在安装 web 锁文件依赖后）：`Test Files 1 passed`、`Tests 39 passed`。
- `npm run typecheck`：退出 0，无错误输出。
- `jq empty codegraph/best.json && jq empty codegraph/target.json && jq empty codegraph/diffs/cards-B294-charter-2.json && git diff --check`：退出 0，无错误输出。
- 按平台规定尝试 `go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . --help`，但依赖下载写入只读 `/root/go/pkg/mod` 失败；`codegraph` 命令也未安装。因此 `codegraph resolve --doc docs/superpowers/specs/b294-contract.md` 本轮未验证，不能把它写成通过。

## 6. 冻结声明

- 允许方向：`d_cli → d_protocol/d_transport`、`d_gateway → d_protocol/d_transport`、
  `d_transport → d_protocol`；web preview API 仍是 web contract 对 client 的同域镜像，
  不直连 owner。
- `codegraph/target.json` 保留基线可解析的 `proto 实体` 入口并增加
  `targetclient.Pool`/`client.Client` 条目及 B294 说明；具体 preview DTO 新符号只在本分支
  视图 diff 中出现，避免基线闸门将其判为 dead-entry；
  `codegraph/best.json` 已增加 `k_web_api_preview → d_web_contract`；视图 diff 与 Ticket 0
  符号同批落盘。
- 无需新 subsystem；无需修改架构形态声明。
- **随本提交冻结**：本文档、目标图、结构图容器、分支视图 diff、骨架、fixture 与测试。
- **交棒：breakdown。**
