# B312 实现计划：开卡绑当前会话（linux-01/codex 对照）

## 0. 执行边界与已拍板裁决

本计划以 `docs/superpowers/specs/b312-contract.md`（已批准、有效基线
`acc/b156.2-156.3 @ 5f8b71b34`）为冻结接口，以
`docs/superpowers/specs/2026-09-03-b307-bind-current-session-design.md` 为行为来源。
实现只在当前分支 `cards/B312-charter-3` 完成，不切分支、不改 git 配置、不 push。

本稿开头固定本回合已落卡的三个决策，后续任务不得把它们当成待拍板项：

- D1 方案甲：`CardView` 增加可选 `driver_session`、`driver_source`；所有 Go→TS
  列表投影和金样本同步更新；禁止为了列表字段逐卡拉详情，不能制造 N+1。
- D2 方案乙：CLI `card bind` 与 `card rebind --self` 走本机账本和当前会话出示
  （`HANDOFF_SESSION_CLI`/`HANDOFF_SESSION_ID`，由现有 env/seat 注入链提供），不
  采信 HTTP body 的 `Identity`；HTTP `POST /api/cards/{id}/coordinator/rebind` 只允许
  `mode=launch`；Web 永不提供 `mode=self`。
- D3 方案乙：Launch 成功后若席位 CAS 冲突，不自动 kill 新会话；响应 409 并带
  新 `session_id`，把人工回收所需信息交给操作者。

本节点只提交本计划及同批台账，不实现运行时代码。后续实现者按任务顺序执行；每个
任务的步骤是一个 2–5 分钟的动作，步骤中的命令、原始结果和判断追加到
`docs/superpowers/ledgers/2026-09-03-b312-plan-ledger.md`。

## 1. 基线、图证据与可执行判据

### 1.1 已在基线真实运行的命令

后端基线已实际运行并退出码 0：

```text
GOMODCACHE=/root/.handoff/tmp/31aa1c4e/gomodcache GOCACHE=/root/.handoff/tmp/31aa1c4e/gocache go test ./internal/proto/... ./internal/ledger/... ./internal/keystone/... ./internal/collab/... ./internal/client/... ./internal/agentd/... ./cmd/... -count=1
```

原始结果包含：`ok github.com/Xsxdot/handoff/internal/proto 0.010s`、
`ok github.com/Xsxdot/handoff/internal/ledger 21.785s`、`ok github.com/Xsxdot/handoff/internal/ledger/api 0.869s`、
`ok github.com/Xsxdot/handoff/internal/keystone 0.215s`、`ok github.com/Xsxdot/handoff/internal/collab 5.436s`、
`ok github.com/Xsxdot/handoff/internal/client 9.569s`、`ok github.com/Xsxdot/handoff/internal/agentd 174.328s`、
`ok github.com/Xsxdot/handoff/cmd 17.385s`；`internal/collab/client`、
`internal/collab/cursor`、`internal/collab/room` 返回 `[no test files]`。

Web 基线已经执行，但当前工作树没有安装依赖：

```text
npm test -- --runInBand
> web@0.0.0 test
> vitest run --runInBand
sh: 1: vitest: not found

npm run typecheck
> web@0.0.0 typecheck
> tsc -b
sh: 1: tsc: not found
```

`web/package-lock.json` 已存在；Web 任务的第一步必须先在 `web/` 执行
`npm ci`，随后重新执行 `npm test` 与 `npm run typecheck`，不能把
上述 127 结果记成通过。

### 1.2 代码图结论及覆盖债

已用仓库自带 codegraph 实跑：

```text
GOMODCACHE=/root/.handoff/tmp/31aa1c4e/gomodcache GOCACHE=/root/.handoff/tmp/31aa1c4e/gocache go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . summary
本仓库有代码图：4007 节点 / 4965 边 / 23 领域（codegraph/）
```

按最佳领域查过 `d_protocol`、`d_ledger`、`d_gateway`、`d_keystone`、`d_collab`、
`d_cli`、`d_web`。以下符号的 `codegraph sym` 均真实返回
`Error: 符号 "internal/ledger/binding.go#Store.ClaimCardAs" 不在图中（图未覆盖或名字有误）；近似候选: []。确认图未覆盖时回落 grep，并把该符号记入本节点产出物的「图覆盖债」小节`（其余实参返回同格式）并退出码 1，
因此实现者必须以源码和测试为准，这些项目是本卡图覆盖债：

```text
internal/ledger/binding.go#Store.ClaimCardAs
internal/ledger/binding.go#Store.RebindDriver
internal/keystone/keystone.go#Service.Wake
internal/collab/room/room.go#VerifyWriter
internal/collab/service.go#Service.Pending
internal/agentd/server.go#coordinatorRunner.Resume
internal/client/squads.go#Client.CoordinatorLaunch
web/src/api/scheduling.ts#launchCoordinator
```

`codegraph context` 显示协议域是 wire 真源，账本域负责 DDL/CAS，gateway 是
HTTP/投影边界，keystone 是 Launch/Wake，collab 是房间/Pending，CLI 是命令边界。
因此本计划按这些有限文件集分卡，不凭 `chain` 伪造控制流；流程问题只以源码和
任务测试断言为准。

### 1.3 依赖行为已核对的基线事实

实现不得增加未经验证的超时或重定向语义。当前
`internal/client/client.go:398-424` 的 `Client.do` 使用调用方 context 创建请求，
非空 body 经 `json.Marshal` 并设置 `Content-Type: application/json`；
`internal/client/client.go:447-454` 的错误正文由 `io.LimitReader(resp.Body, 4096)` 读取。
当前 `internal/agentd/coordapi.go:95-101` 用 `json.NewDecoder(r.Body).Decode`。
这些是本计划唯一引用的 client/decoder 行为出处。

## 2. 跨任务接口合同与文件边界

### 2.1 Consumes / Produces

| 任务 | Consumes（精确签名/字段） | Produces（精确签名/字段） |
| --- | --- | --- |
| T1 协议与账本 | `proto.EncodeSeatIdentity(cli, sessionID string) (string, error)`；`proto.ParseSeatIdentity(raw string) (cli, sessionID string, err error)`；现有 `ledger.Store.mutate` 事务 | `func (s *Store) BindSeat(id, identity string, source proto.SeatSource) error`；`func (s *Store) RebindSeat(id, identity string, source proto.SeatSource, expect string) error`；`proto.Card.DriverSource string`；`proto.CardView.DriverSession string`、`DriverSource string`；`proto.CoordinatorRebindReq` |
| T2 CLI | T1 的 `BindSeat`/`RebindSeat`；`func currentSeatIdentity() (string, error)`；现有 `openLedger() (*ledger.Store, error)`；`func (c *Client) CoordinatorLaunch(ctx context.Context, cardID string) (*proto.CoordinatorLaunchResp, error)` | `handoff card bind <id>`；`handoff card rebind <id> --self|--launch`；`card coordinate` 只写 `coordinate` 席位；`ledgerActor()` 只保留普通审计用途 |
| T3 gateway/keystone | `func (s *Server) launchCoordinatorRound(ctx context.Context, card, source string) (keystone.RoundResult, error)`；`func (s *Service) Wake(ctx context.Context, card string, evs []WakeEvent, spec keysclient.SessionSpec) (RoundResult, error)`；T1 的 Store 写面 | `POST /api/cards/{id}/coordinator/launch`；`GET /api/cards/{id}/coordinator`；`POST /api/cards/{id}/coordinator/rebind`；D3 的 409 `{error,session_id}`；来源分支与重启后恢复行为 |
| T4 transport/collab/dispatch | T1 的 wire；`func (s *Service) Pending(consumer string) ([]proto.LedgerEvent, error)`；`func VerifyWriter(r *Room, kind, actor string) error`；`func (c *Client) CoordinatorRebind(ctx context.Context, cardID string, req proto.CoordinatorRebindReq) (*proto.CoordinatorLaunchResp, error)` | client rebind 拨号；房间/`Pending` 规范身份比较；`--step` 和 coordinator room 的共享出示；Resume 环境键 |
| T5 Web | `proto.CardView`/`proto.Card` 的 Go→HTTP→TS 投影；`launchCoordinator(cardId: string)`；`rebindCoordinatorLaunch(cardId: string)` | TS `SeatSource`、CardView 字段、source-free launch、launch-only rebind；抽屉/列表/设置页用户词；建卡不拉起 |

### 2.2 不变与禁止变更

- `driver_session` 仍是唯一席位身份字段；不得新增 `driver_cli`、
  `driver_session_id`、`seat` 或第二套席位对象。`driver_carrier` 仅兼容存量列，
  不读、不写、不映射到席位。
- `internal/collab/client/client.go#LedgerClient` 不扩方法；gateway 组装点可直接
  持有 `*ledger.Store` 完成席位 CAS，不把席位写入塞进协作门面。
- `driver_leases`、`DriverLeaseTTL` 不接入新席位；`card wait` 仍走 `Follow`，不按
  席位过滤。
- 不保留 `manual`、`card_create` source 分支；不保留 `card add --coordinate` 的
  隐式 Launch；Web 不发送 `web:<addr>` 作为协调者席位；任何实现不得自动 kill
  D3 冲突后新会话。

## 3. 任务 DAG

```text
T1 协议 + ledger 原子写面
  └─> T2 CLI bind/rebind-self 与非按钮入口
        ├─> T3 HTTP launch/rebind/status + keystone + D3
        └─> T4 client / Resume env / collab / step
              └─> T5 Web 投影与操作面
```

T1→T2 是本卡最薄可跑路径：当前声明缝只有身份编码金样本，尚不能从 CLI 声明缝
得到 `card bind` 的预期结果；T1 点亮原子写面，T2 立即用真实 root command 和本机
SQLite 账本锁住第一条行为。T3/T4 可并行实现，T5 等两侧 wire 固定后开始。

## 4. T1：协议投影、DDL 迁移与唯一席位写面

### 精确文件集

生产文件：

- `internal/proto/seat.go`
- `internal/proto/ledger.go`
- `internal/proto/scheduling.go`
- `internal/ledger/types.go`
- `internal/ledger/store.go`
- `internal/ledger/cards.go`
- `internal/ledger/binding.go`
- `internal/ledger/move.go`
- `internal/ledger/tasks.go`

测试/金样本：

- `internal/proto/ledger_wire_test.go`
- `internal/proto/contract_fixture_test.go`
- `web/src/api/testdata/CardView.json`
- 新增 `web/src/api/testdata/CoordinatorRebindReq.json`
- `internal/ledger/binding_test.go`
- `internal/ledger/wire_test.go`
- `internal/ledger/ddl_parity_test.go`

### 精确实现形状

协议层新增来源验证辅助函数；它不是 wire 字段，也不改变两个已冻结的编码函数：

```go
// ValidateSeat 检查席位身份与来源是否构成可占用的规范席位。
// 参数 identity 为空时 source 必须为空；非空 identity 必须能由
// ParseSeatIdentity 解码，source 只能是 SeatSourceBind 或 SeatSourceCoordinate。
// 返回非 nil 时调用方必须保留账本原值并把它当作占用态。
func ValidateSeat(identity string, source SeatSource) error
```

两个 Go Card 镜像都加入同一字段，类型和 JSON tag 固定如下：

```go
// internal/ledger/types.go#Card
DriverSession     string    `json:"driver_session,omitempty"`
DriverSource      string    `json:"driver_source,omitempty"`
DriverHeartbeatAt time.Time `json:"driver_heartbeat_at,omitempty"`

// internal/proto/ledger.go#Card 加入
DriverSession     string    `json:"driver_session,omitempty"`
DriverSource      string    `json:"driver_source,omitempty"`
DriverHeartbeatAt time.Time `json:"driver_heartbeat_at,omitempty"`

// internal/proto/ledger.go#CardView 仅加入列表需要的两个可选席位字段
DriverSession string `json:"driver_session,omitempty"`
DriverSource  string `json:"driver_source,omitempty"`
```

`CardView` 是 D1 列表投影，必须直接带 `driver_session`/`driver_source`，不允许列表
序列化时再发详情请求。`CoordinatorRebindReq` 放在
`internal/proto/scheduling.go`，字段固定为：

```go
// CoordinatorRebindReq 是协调者换绑端点请求；HTTP 端只接受 mode=launch，
// mode=self 由 CLI 本机账本路径执行，Identity 不得成为 HTTP 可信输入。
type CoordinatorRebindReq struct {
	Mode     string `json:"mode"`
	Identity string `json:"identity,omitempty"`
}
```

PostgreSQL 与 SQLite 建表 SQL 都加入这一列：

```sql
driver_source TEXT
```

`ensureSchema` 对旧库执行两方言对应的
`ALTER TABLE cards ADD COLUMN driver_source TEXT`，沿用当前已有的 duplicate-column
容忍分支；第二次打开同一库必须成功，其他迁移错误必须带列名和库类型返回并写
结构化 warn。`cardColumns`、`scanCard`、所有 insert/select 投影同步加入该列；
空座是两列同时为空，旧非空非法 identity 或合法 identity+空/未知 source 必须
原样读出且是占用态。

唯一新写面必须保持下列签名和事务顺序：

```go
// BindSeat 只把空座原子地占为 identity/source；不写 driver_carrier，不落事件。
func (s *Store) BindSeat(id, identity string, source proto.SeatSource) error

// RebindSeat 以 expect 比较当前 driver_session 原始字节，成功覆写两列并落一条
// EvDriverTakeover；expect 不从用户 flag 取得。
func (s *Store) RebindSeat(id, identity string, source proto.SeatSource, expect string) error
```

`BindSeat` 和 `RebindSeat` 都先 `ValidateSeat`，再进入现有 `s.mutate`；事务内用
`getCardTx` 读卡。Bind 只有 `DriverSession == "" && DriverSource == ""` 才更新
两列和 `driver_heartbeat_at`，否则统一 `ErrCASConflict`。Rebind 要求当前身份非空、
`card.DriverSession == expect`，CAS 不符不得改任何列；成功写新 identity、source、
认领时刻，并只调用一次
`appendEvent(tx, sink, id, EvDriverTakeover, identity, map[string]string{"from": old, "to": identity})`。所有入口、外部调用前后、错误分支
和成功分支使用既有 `log()` 结构化日志；日志只能记录 card、source、has_expect 等
上下文，不记录当前会话环境键的值。导出方法注释写清参数、返回错误和事务边界。

兼容方法的精确行为也在本任务落定：`ClaimCard`、`ClaimCardAs`、`TakeoverCard`
必须不调用新写面、不更新 `driver_session`/`driver_source`，返回带 `bind`、
`coordinate` 或 `rebind` 指引的 `ErrBadState`；`ReleaseCard` 空座仍幂等成功，
非空座不修改两列并返回当前席位和 rebind 指引的 `ErrBadState`。已有运行锁行为和
`driver_carrier` 兼容测试不迁入本卡。

### T1 步骤与红绿

1. 在开始改动前重新执行基线中的 `go test` 命令；预期仍为上述各包 `ok`。把结果
   追加台账，再在 `internal/proto/ledger_wire_test.go` 增加失败测试：`CardView`
   occupied 样本必须出 `driver_session`/`driver_source`，empty 样本不出这两个
   `omitempty` 键；`CoordinatorRebindReq{Mode:"launch"}` 的 fixture 必须为
   `{"mode":"launch"}`。运行 `go test ./internal/proto/... -run 'TestCardView|TestContractFixtures' -count=1`，在实现前记录红色差异。
2. 实现 `ValidateSeat`、协议字段和 rebind DTO，更新
   `contract_fixture_test.go` 的固定样本：occupied 使用
   `cli:codex#thread-01` + `bind`，empty 样本使用空字符串；手写 map 断言字段
   缺失和非零值分开，禁止只断言 struct 默认值。运行上述 proto 命令，预期绿。
3. 在 `store.go` 两套 DDL 和 `ensureSchema` 加 `driver_source`，再扩充
   `cardColumns`/`scanCard`；在 `ddl_parity_test.go` 中用旧 schema 打开两次并断言
   新列存在，SQLite/PG SQL 文本都包含该列。运行
   `go test ./internal/ledger/... -run 'Test.*DDL|Test.*Wire|Test.*Card' -count=1`，预期绿。
4. 写 `BindSeat`/`RebindSeat` 的红测试后最小实现事务和日志，再运行
   `go test ./internal/ledger/... -run 'Test.*Bind|Test.*Rebind|Test.*Binding' -count=1`。
   逐条断言：空座 bind=bind 成功；空座 bind=coordinate 成功；合法/非法非空席位
   bind 都是 `ErrCASConflict`；expect 按原始 `driver_session` 比较；CAS 失败身份和
   来源均不变；成功恰有一条 `EvDriverTakeover`，payload 的 from/to 和 actor
   精确匹配；失败不写列、不写事件。
5. 更新 `binding_test.go`/`move`/`tasks` 兼容测试，断言 Claim/Takeover/Release
   不再碰席位，并核对导出方法头部注释和每条结构化日志；运行
   `go test ./internal/proto/... ./internal/ledger/... -count=1`，预期退出码 0。

### T1 接缝、范围和验收

本任务的缝级入口是 `Store.BindSeat` 与 `Store.RebindSeat`；CLI 端到端 bind 在 T2
补强。T1 测试范围仅为 `internal/proto/...` 与 `internal/ledger/...`，不跑全仓。
协议→ledger 的真实序列化边界由 `CardView`/`Card` JSON fixture 和
`ledger.cardWire` 的后续测试共同锁住；T1 必须先锁 DTO，再允许 T3/T5 消费。

T1 对应冻结断言 1–30；每条通过上述具体测试名/断言矩阵归属。缺陷族对抗：
非法旧身份不会空座化（状态/迁移族）、双列更新同事务（并发/CAS 族）、旧 API
不再改席位（兼容回退族）、JSON 缺键与非零值同时断言（序列化族）、两方言迁移
重复执行（部署族）。

## 5. T2：CLI 当前会话 bind/rebind-self、coordinate 与非按钮入口

### 精确文件集

生产文件：`cmd/card_driver.go`、`cmd/card_coordinate.go`、`cmd/card.go`、
`cmd/ledgercli.go`、`cmd/card_dispatch.go`、`cmd/card_node.go`、`cmd/room.go`、
新增 `cmd/card_seat.go`（只放当前身份 helper，不放账本写逻辑）。

测试文件：`cmd/card_driver_test.go`、`cmd/card_coordinate_test.go`、
`cmd/card_test.go`、`cmd/card_dispatch_test.go`、`cmd/card_node_test.go`、
`cmd/room_test.go`。

### 精确接口与行为

`cmd/card_seat.go` 只提供共享出示函数：

```go
// currentSeatIdentity 从当前执行环境的 seat/env 注入读取规范席位身份。
// 缺任一 HANDOFF_SESSION_CLI 或 HANDOFF_SESSION_ID 都报错；不回退 USER、主机名、
// PID、ledgerActor 或 web actor。
func currentSeatIdentity() (string, error)
```

它读取且仅读取 `HANDOFF_SESSION_CLI`、`HANDOFF_SESSION_ID`，调用
`proto.EncodeSeatIdentity`；输入键值不写日志。`ledgerActor()` 保留给普通人尺度
事件审计。

命令行为固定：

```text
card bind <id>                 -> currentSeatIdentity -> openLedger -> BindSeat(id, identity, SeatSourceBind)
card rebind <id> --self        -> currentSeatIdentity -> openLedger -> 读卡得到 expect -> RebindSeat(id, identity, SeatSourceBind, expect)
card coordinate <id>           -> Client.CoordinatorLaunch(ctx, id)，服务端负责以新 session 写 coordinate 席位
card rebind <id> --launch      -> Client.CoordinatorRebind(ctx, id, proto.CoordinatorRebindReq{Mode:"launch"})
```

`bind` 与 `rebind --self` 不查小队、不经 HTTP、不接受用户 session 参数；成功仍输出
单行 `{"ok":true}`。`rebind` 的 `--self`/`--launch` 必须互斥且至少一个，删除
`--to`/`--carrier`/`--expect` flags；空座 self/launch 都报指向 bind/coordinate
的可行动错误。`rebind --self` 的本机 ledger 写成功即完成 CLI 写入；不能凭空让
独立 agentd 进程共享 Go 内存，因此 `Service.Wake` 的账本预检必须在读到 bind 或
非法来源时删除 `sessions[card]` 后再返回，禁止继续 Resume。若 self 是 agentd
同进程测试/内部接线触发，则调用同一删除操作；不得新增无来源的远程 HTTP identity
通道，也不得把“下次 Wake 清 stale”写成账本未成功时的成功保证。

`card add` 删除 `--coordinate` flag、`coordinateAfterCreate` 和所有建卡后 launch
分支；旧 flag 必须由 Cobra 明确失败并指向 `card coordinate <id>`。`card coordinate`
只负责调用 source-free client，成功回显服务端 JSON；服务端 409 原文透传到 stderr。
非 `--step` 派发也不能调用 `ClaimCard` 占席位；`--step` 读取账本后，非空合法或
非法席位都要求 `currentSeatIdentity()` 与 `DriverSession` 精确相等，空座允许执行
但 actor 只落普通审计且不写席位。`room send` 的 coordinator kind 使用同一 helper，
user kind 继续用 `ledgerActor`。

### T2 步骤与红绿

1. 在 `cmd` 实现前运行现有最小基线：
   `go test ./cmd/... -run 'TestCard|TestRoom|TestDispatch' -count=1`，预期基线绿；
   在 `card_driver_test.go` 先把现有旧 rebind/旧 takeover 断言改成冻结的新失败
   断言，运行该命令得到红色，台账记录原始输出。
2. 新增 `card_seat.go` 和 `card bind`，复用 `cmd` 既有 `runLedgerCLI` fixture
   harness；红测试逐条断言：两 env 键完整时写规范 identity/source=bind；缺任一键
   非零退出且 ledger 两列不变；设置 USER/hostname 也不能补齐缺失 env；bind 不
   触碰小队/HTTP。最小实现后运行
   `go test ./cmd/... -run 'TestCardBind|TestCurrentSeat' -count=1`，预期绿。
3. 把 `rebind` flags 改为 `--self`/`--launch`，实现本机 self 直接读卡+CAS，
   删除 `card_coordinate.go` 的旧 helper；红绿测试断言 self 不产生 HTTP/Launch，
   成功 source=bind、旧席位被替换、CAS 冲突不改动；flags 缺失/并用及旧 `--to`
   均失败。运行 `go test ./cmd/... -run 'TestCardRebind|TestCurrentSeat' -count=1`。
4. 删除 add coordinate 状态/flag/调用，更新 `card_coordinate_test.go`：旧 flag
   指向 coordinate；coordinate client body 为空对象、路径精确、成功 session 原样
   输出、HTTP 错误原样可见。更新 dispatch/room tests，逐条锁有席位出示匹配/不匹配、
   空座不写席位、coordinator kind 不使用 `ledgerActor`、wait 仍走 Follow。运行
   `go test ./cmd/... -run 'TestCard|TestRoom|TestDispatch|TestWait' -count=1`。
5. 核对新文件头职责边界、导出 `currentSeatIdentity` 参数/错误说明、每个命令入口
   带输入和成功/失败结构化日志，日志不得打印两个 session env 值；最后运行
   `go test ./cmd/... -count=1`，预期退出码 0。

### T2 验收与接缝

`TestCardBindUsesCurrentSeat` 的入口是 root command→真实 SQLite，锁声明缝；
`TestCardRebindSelfUsesLocalLedger` 同样从 root command 进入本机 CAS；
`TestCardCoordinatePostsEmptyObjectToLaunchPath` 从 root command→真实 client HTTP
进入 launch 缝；`TestCardDispatchStepRequiresCurrentSeat` 与
`TestRoomSendCoordinatorRequiresCurrentSeat` 分别穿过 step/room 声明缝。T2 只跑 `cmd/...`，不跑
agentd 或 Web。对应冻结断言 31–39、56–58；对抗审查覆盖错误身份、空座/非空座、
旧 flag、重复请求及普通 actor 误当席位。

## 6. T3：HTTP coordinator 三端点、D3 CAS 冲突与 Keystone Wake 来源执法

### 精确文件集

生产文件：`internal/proto/scheduling.go`（若 T1 已完成仅消费 DTO）、
`internal/agentd/coordapi.go`、`internal/agentd/ledgerapi.go`、
`internal/agentd/scheddrain.go`、`internal/agentd/server.go`、
`internal/keystone/keystone.go`、`internal/keysclient/keysclient.go`。

测试文件：`internal/agentd/coordapi_test.go`、`internal/agentd/scheddrain_test.go`、
`internal/agentd/coordrunner_test.go`、`internal/agentd/ledgerapi_test.go`、
`internal/keystone/keystone_spec_test.go`、`internal/keystone/slice_test.go`。

### 精确实现形状

路由只保留以下协调者控制面：

```text
POST /api/cards/{id}/coordinator/launch
GET  /api/cards/{id}/coordinator
POST /api/cards/{id}/coordinator/rebind
```

`POST /api/cards/{id}/coordinator/launch` 的 body 只接受 `{}` 或 `{"source":"coordinate"}`，缺 source 也按
`coordinate`；`manual`、`card_create`、未知值返回 400。旧 `/api/cards/{id}/rebind`
路由、`handleCardRebind` 和 `rebindPort` 从 rooms API 移除。

launch/rebind-launch 必须在同一张卡的控制段持有串行锁，顺序为：读账本并验证空座
或期望旧身份 → 解析唯一 coordinator squad → LaunchAdmit/载体/SessionSpec →
`LaunchForCard(ctx, card, "coordinate", spec)` → 用返回 `result.SessionID` 和 `spec.CLI`
编码 `cli:<cli>#<session>` → 对 launch 用 `BindSeat`，对 rebind-launch 用
`RebindSeat(id, identity, proto.SeatSourceCoordinate, expect)`。Launch 前席位非空直接 409，不能启动第二个机器人。
Launch 后 CAS 冲突必须原样保留新会话，不 kill，写结构化 error 并返回：

```json
{"error":"协调者已启动但席位 CAS 冲突；新会话未自动终止，请人工回收","session_id":"sess-new"}
```

其中 `session_id` 必须等于 `RoundResult.SessionID`；Launch 前冲突没有新 session，
409 body 不强行添加空键。错误路径继续按现有 400/409/502/503 分类，成功继续
`proto.CoordinatorLaunchResp` 200。D3 测试通过一个可控 runner/ledger barrier
使外部写在 Launch 返回后发生，断言不会调用 kill、席位保持外部写入、响应 409 带
新 session。

rebind handler 解码 `proto.CoordinatorRebindReq`；`mode=self` 一律 400 并指向
CLI `card rebind <id> --self`，不读取/验证 body Identity；`mode=launch` 要求
Identity 为空（非空也 400，防止 body 冒充当前身份），执行上述 launch-only CAS。
Web 与 client 只生成 mode launch。

status 的 `Bound` 先读 ledger card 并调用 `proto.ValidateSeat`：合法两列才 true，
仅内存 session 且账本无合法席位为 false，账本 bind 席位而 keystone memory 为空
为 true。`Locate` 失败只让 Attach 为空并写 warn，不能把合法 ledger bound 降为
false；attach_active 语义不改。

`keystone.Service.Wake` 保持精确签名：

```go
func (s *Service) Wake(ctx context.Context, card string, evs []WakeEvent, spec keysclient.SessionSpec) (RoundResult, error)
func (s *Service) LaunchForCard(ctx context.Context, card, source string, spec keysclient.SessionSpec) (RoundResult, error)
```

Wake 每次先 `LedgerView.GetCard`。空座/非法旧席位不得因内存缺失 Launch；bind
返回 `Woke=false`，不 Resume、不 Launch，并清掉该卡 stale memory；coordinate
解析账本 identity，内存有 ref Resume，内存无 ref 用账本 CLI/session 加当前 spec
补齐 HomeDir/Workdir/Model 后 Resume，只有 Resume 失败才按现有路径重建。LaunchForCard
生产 source 不是 `coordinate` 时返回带 source 的错误，不调用 Runner。重建得到新
session 时只返回结果，由 agentd 组装点按当前期望做 CAS，避免 keystone 自己写账本。

`coordinatorRunner.Resume` 经过 `resumeTurnRequest` 给 `hostapi.TurnRequest` 追加：

```go
Env: []string{
	"HANDOFF_SESSION_CLI=" + ref.CLI,
	"HANDOFF_SESSION_ID=" + ref.SessionID,
}
```

并保留 CLI/SessionID/HomeDir/Workdir/Model；不得把 env 值写日志。新导出/改动方法
头部写职责、参数、返回和为什么不能把 bind 当 Launch。

### T3 步骤与红绿

1. 在改 agentd 前执行 `go test ./internal/agentd/... -run 'TestCoord|TestLedgerApi|TestResume' -count=1` 与 `go test ./internal/keystone/... -count=1`；基线预期绿，记录原始结果。为 launch 缝新增缺 source/旧 source/非空 seat 的失败 HTTP 测试并先跑红。
2. 修改路由和 body 校验，添加 launch-only rebind DTO 消费；完成后只跑
   `go test ./internal/agentd/... -run 'TestCoord.*(Launch|Rebind|Status)|TestLedgerApi' -count=1`，逐条断言 route、body、status、`CoordinatorLaunchResp`。
3. 增加 per-card 控制锁和“Launch 后 CAS”组装函数；使用可控 fake runner + barrier
   写 D3 红测试，最小实现后复跑同一组测试。断言空座一次 Launch、非空座 Launch 前
   409、CAS race 409 带 session_id 且 no kill、成功 source=coordinate。
4. 改 `keystone.Wake/LaunchForCard/Locate` 来源分支，先写 fake Runner 计数红测再
   最小实现；运行 `go test ./internal/keystone/... -run 'TestWake|TestLaunch|TestLocate' -count=1`，逐条断言 bind no-op、coordinate resume、Resume 失败才 Launch、重启内存缺失不把合法席位当空座。
5. 更新 `resumeTurnRequest` 测试，断言两 env key 及值精确存在、日志不包含 env
   内容；检查成功/失败分支均有结构化日志和注释。最后运行
   `go test ./internal/agentd/... ./internal/keystone/... -count=1`，预期退出码 0。

### T3 验收与类型清单

真机/集成清单：linux-01 agentd 使用真实本机 ledger；`POST launch {}` 返回
coordinate 席位；已有席位不启动第二会话；人为制造 Launch 后外部 CAS 时收到
409+session_id 且新进程仍可人工定位回收；agentd 重启后 GET bound 仍由 ledger
决定；bind 席位 Wake 不执行 Runner；coordinate 席位 Resume 注入真实 env；Web
端点不能 self。

入口覆盖：`TestCoordLaunchHTTPContract` 穿过真实 `httptest` Server/mux/auth/ledger；
`TestCoordRebindLaunchHTTPContract` 穿过新端点；`TestCoordStatusReadsLedgerSeat`
穿过 GET；`TestWakeViaAutomationHonorsSeatSource` 从 `wakeCoordinatorRound` 进入
keystone。每条
路由均有至少一个缝级断言。对应冻结断言 40–51；对抗并发、重启、非法旧席位、
runner 失败、无小队和无名额族。T3 只跑 `internal/agentd/...`、`internal/keystone/...`。

## 7. T4：Client、Resume 环境、协作房间与 dispatch 接线

### 精确文件集

生产文件：`internal/client/coordinator.go`、`internal/client/squads.go`、
`internal/collab/room/room.go`、`internal/collab/service.go`、
`cmd/card_dispatch.go`、`cmd/card_node.go`、`cmd/room.go`。

测试文件：`internal/client/client_test.go`、`internal/collab/service_test.go`、
新增 `internal/collab/room/room_test.go`、`cmd/card_dispatch_test.go`、
`cmd/card_node_test.go`、`cmd/room_test.go`。

### 精确接口与实现

退役 `CoordinatorLaunchAs` 的 source 专用拨号；保留既有：

```go
func (c *Client) CoordinatorLaunch(ctx context.Context, cardID string) (*proto.CoordinatorLaunchResp, error)
```

新增：

```go
func (c *Client) CoordinatorRebind(ctx context.Context, cardID string, req proto.CoordinatorRebindReq) (*proto.CoordinatorLaunchResp, error)
```

二者均使用 `url.PathEscape(cardID)`、`Client.do`、`decodeWire` 和原始 http error；
launch body 是 `{}`，rebind launch body 是 `{"mode":"launch"}`，不自行添加
Identity，不更改已有 409 正文透明行为。函数注释明确请求/返回/错误边界。

`room.VerifyWriter`、`Service.Pending`、`Consume` 签名不改。coordinator kind 仅接受
规范合法 `card.DriverSession`；relay 仍接受本卡或直接父卡席位，平级拒绝；user 和
群房间规则不变。`Pending(consumer)` 与 `ListAllCards` 用同一字节字符串匹配，
`@B号` 仍把事件送到卡的当前席位。校验失败每条日志带 card/kind/actor（actor 可
截断），成功写入/消费也有 info，不把身份值当 secret 记录。

### T4 步骤与验收

1. 先执行 `go test ./internal/client/... ./internal/collab/... -count=1` 与
   `go test ./cmd/... -run 'TestCardDispatch|TestRoom|TestCardNode' -count=1`，记录
   基线绿。为 client fetch 写失败测试：launch body `{}`；rebind URL、POST、body
   精确；非 2xx 错误正文保留；先跑红再实现，之后只跑上述包绿测。
2. 将 room tests 的 actor 从旧 `cli:user@host` 改为
   `cli:codex#thread-01`，补非法旧身份、另一卡、父卡 relay、平级 relay、user
   kind 五组断言；运行 `go test ./internal/collab/... -run 'Test.*(Writer|Pending|Consume|Room)' -count=1`。
3. 在 `card_node.go` step 入口接入 T2 helper 和 ledger read：非空席位出示不匹配
   立即失败且不发 HTTP/不写席位；空座仍发 step 且不写席位；在 `card_dispatch.go`
   删除所有 ClaimCard 占座路径但保留运行锁。运行 `go test ./cmd/... -run 'TestCardDispatch|TestCardNode' -count=1`。
4. `room.go` coordinator kind 与 T2 共享规范 helper；普通 user 继续 ledgerActor。
   运行 `go test ./cmd/... -run 'TestRoom' -count=1`，每个成功和拒绝分支均核对
   结构化日志/导出注释，不另造 controller 层。
5. 本任务结束前执行 `go test ./internal/client/... ./internal/collab/... ./cmd/... -count=1`，预期退出码 0。

T4 的声明缝入口分别是 `Client.CoordinatorRebind`、collab `Service.Pending`/
`Send`、root `card dispatch --step`、root `room send`；没有内部纯 helper 测试替代
它们。对应冻结断言 52–58，并对抗跨卡身份、父子继承、未知 kind、HTTP 非 2xx、
step 空座和普通 actor 污染。

## 8. T5：Go→HTTP→TS 序列化、Web 列表/抽屉/设置页

### 精确文件集

生产文件：`internal/agentd/ledgerapi.go`、`internal/ledger/api/api.go`、
`cmd/card.go` 的 `cardViewWire`、`web/src/api/ledger.ts`、
`web/src/api/scheduling.ts`、`web/src/app/cards/NewCardDialog.tsx`、
`web/src/app/cards/CoordinatorPanel.tsx`、`web/src/app/cards/CardDrawer.tsx`、
`web/src/app/cards/ListView.tsx`、`web/src/app/settings/SchedulingPage.tsx`。

测试/fixture：`internal/agentd/ledgerapi_test.go`、`internal/ledger/api/api_test.go`、
`cmd/card_test.go`、`web/src/api/scheduling.fetch.test.ts`、
`web/src/api/contract.test.ts`、`web/src/api/testdata/CardView.json`、
`web/src/api/testdata/CoordinatorLaunchResp.json`、
`web/src/app/cards/CoordinatorPanel.test.tsx`、`web/src/app/cards/NewCardDialog.test.tsx`、
`web/src/app/cards/CardDrawer.test.tsx`、`web/src/app/settings/SchedulingPage.test.tsx`。

### 精确投影与 UI 形状

所有手写投影都必须显式列出字段：

```go
// internal/agentd/ledgerapi.go
return proto.Card{
	DriverSession: card.DriverSession,
	DriverSource: card.DriverSource,
	DriverHeartbeatAt: card.DriverHeartbeatAt,
}

// ledgerCardViewWire 与 cmd.cardViewWire 同样直接投影
DriverSession: view.DriverSession,
DriverSource: view.DriverSource,
```

生产实现保留现有其他字段，不新增详情请求。`internal/ledger/api/api.go#cardWire`
也要把 `DriverSource` 传入 `proto.Card`；`ListActiveCards/ListAllCards/GetCard` 的
真实 JSON 由测试锁住。占用样本必须输出非空 identity/source，空座样本必须没有
optional keys；断言原始 `map[string]json.RawMessage` 的 key presence，再断言值，
以区分字段缺失和零值。

TS 只在 `web/src/api/ledger.ts` 声明一次：

```ts
export type SeatSource = 'bind' | 'coordinate'

export interface CardView {
  driver_session?: string
  driver_source?: SeatSource
}

export interface Card {
  driver_session?: string
  driver_source?: SeatSource
  driver_heartbeat_at?: string
}
```

`web/src/api/scheduling.ts` 的可用 API 是：

```ts
export type CoordinatorRebindMode = 'self' | 'launch'

export interface CoordinatorRebindReq {
  mode: CoordinatorRebindMode
  identity?: string
}

export const launchCoordinator: (cardId: string) => Promise<CoordinatorLaunchResp>
export const rebindCoordinatorLaunch: (cardId: string) => Promise<CoordinatorLaunchResp>
```

实现中 `launchCoordinator(cardId)` 只调用 `postJSON<CoordinatorLaunchResp>(path, {})`；
`rebindCoordinatorLaunch(cardId)` 只发送 `{ mode: 'launch' }`。不再导出或使用
`CoordinatorLaunchSource`，不生成 self helper。

组件精确变化：

- `NewCardDialog.tsx` 删除 coordinate state、checkbox、每张成功卡的 launch loop、
  launch failure UI 和 scheduling import；批量建卡只串行 `createCard`，成功回调
  最后一张卡，失败显示 create error。
- `CoordinatorPanel.tsx` 未绑定按钮文字改为“▶ 叫机器人”，调用 source-free
  `launchCoordinator(cardId)`；已绑定状态增加“换绑：叫机器人”，调用
  `rebindCoordinatorLaunch(cardId)`；409 错误显示换绑提示，不提供 self。
- `CardDrawer.tsx` 与 `ListView.tsx` 直接显示 `driver_session` 和来源用户词：
  `bind`→“坐下”，`coordinate`→“叫机器人”，非法/未知→“席位异常”；列表只
  使用 CardView 字段，绝不 fetch 详情。
- `SchedulingPage.tsx`/测试把“开卡即绑 / 一键拉起”改为“坐下 / 叫机器人”；设置页
  只说明 Web 能叫机器人、当前会话坐下需 CLI，不显示 `web:<addr>`。

### T5 步骤、真实序列化边界与验收

1. 在 `web/` 执行已核对存在的 `npm ci`；随后执行 `npm test`、
   `npm run typecheck`，记录安装和基线原始结果。后端先执行
   `go test ./internal/agentd/... ./internal/ledger/api/... ./cmd/... -run 'Test.*(Card|Ledger)' -count=1`。
2. 先改 Go 投影测试和 fixture：occupied/empty 两个样本、`driver_source` 字面值、
   `CardView` 列表字段、rebind request fixture。先运行
   `go test ./internal/proto/... ./internal/ledger/api/... ./internal/agentd/... ./cmd/... -run 'Test.*(Fixture|CardView|Ledger)' -count=1` 得到红，再实现每一个 map/DTO 投影并跑绿。
3. 更新 TS 类型和 `scheduling.fetch.test.ts`：断言 source-free launch body
   `{}`，launch-only rebind body `{mode:'launch'}`，不出现 `identity`；更新
   `contract.test.ts` 以真实 fixture 解码 occupied/empty，并运行
   `npm test -- src/api/scheduling.fetch.test.ts src/api/contract.test.ts`。
4. 修改 NewCardDialog/CoordinatorPanel/Drawer/ListView/Settings 及测试；逐条断言
   建卡不调用 launch、按钮文字/调用参数、换绑调用、来源词、空字段不渲染成伪造
   席位；运行
   `npm test -- src/app/cards/CoordinatorPanel.test.tsx src/app/cards/NewCardDialog.test.tsx src/app/cards/CardDrawer.test.tsx src/app/settings/SchedulingPage.test.tsx`。
5. 检查所有生产导出函数头注释、关键节点结构化日志；执行
   `npm test`、`npm run typecheck`，再执行受影响后端包测试。Web
   若依赖安装仍失败，保留命令原始错误，不能给 pass；必须在依赖可用且命令退出码
   0 后才将 Web 判定绿。

### T5 序列化清单与接缝覆盖

新增字段的每个手写边界逐一列入断言：

1. `internal/ledger/types.go#Card` → `internal/ledger/api/api.go#cardWire` →
   `proto.Card` → `/api/cards/{id}`；
2. `internal/ledger/types.go#CardView` → `internal/agentd#ledgerCardViewWire` →
   `proto.CardView` → `/api/cards`；
3. `internal/ledger/types.go#CardView` → `cmd#cardViewWire` → CLI JSON；
4. `proto.CardView`/`proto.Card` → `web/src/api/ledger.ts` → ListView/CardDrawer；
5. `proto.CoordinatorRebindReq` → agentd decoder → `web/src/api/scheduling.ts`；
6. `CoordinatorLaunchResp` 409 error body → `Client.httpError` → CLI/Web error text。

占用与空座 raw JSON 断言穿过第 1–4 条真实边界，不能用两端孤立 struct 测试替代。
缝级入口是 `/api/cards`、`/api/cards/{id}`、CLI `card show`/`card coordinate`、
Web fetch API 和组件 click；每条新增 UI 行为都必须由这些入口进入。对应冻结断言
59–62；对抗字段缺失/零值、未知 source、HTTP error body、N+1 详情、Web actor
伪造席位。

## 9. 五项法定自审

### 9.1 缺陷族对抗审查

| 缺陷族 | 设问 | 本计划的具体结论/锁点 |
| --- | --- | --- |
| 身份/旧数据 | `cli:user@host` 或空来源会不会变空座？ | `ValidateSeat` + `scanCard` 保留原值，Bind 统一 CAS conflict，T1 非法旧值测试。 |
| 并发/CAS | Launch 后外部改座会不会杀错会话或覆盖别人？ | per-card 串行锁 + Bind/Rebind 事务 CAS；D3 409 带新 session、无 kill barrier 测试。 |
| 来源分支 | bind 会不会 Wake/Launch，manual 会不会复活？ | Wake source 分支、launch body 旧 source 400、runner 计数测试。 |
| 权限/出示 | 人 actor、Web actor、另一卡会不会冒充协调者？ | currentSeatIdentity 唯一 env；room/step HTTP/root seam 断言。 |
| 兼容/回退 | 老 Claim/Release/Takeover 会不会继续改席位？ | T1 旧 API 负测，明确不写两列；运行锁独立。 |
| 序列化 | 任一手写 map 会不会漏 `driver_source` 或把空值伪造成占用？ | 六段 boundary 清单、occupied/empty raw-key fixture、TS fetch tests。 |
| 进程重启 | keystone memory 丢失是否误判空座？ | status 读 ledger、Wake coordinate 从 ledger 恢复、bind 清 stale memory 测试。 |
| 资源/N+1 | 列表是否逐卡详情或错误回收新会话？ | CardView 直投影断言；D3 明确人工回收、无 kill；列表测试记录 HTTP 请求次数。 |

### 9.2 上下文预算

每个任务均是有界文件集：T1 9 个生产文件+7 个测试/fixture，T2 8 个生产文件+
6 个测试，T3 7 个生产文件+6 个测试，T4 6 个生产文件+6 个测试，T5 9 个生产文件+
10 个测试/fixture。T5 对 `ledgerapi.go`/`card.go` 是投影交接点，不向调度、executor
或其它页面扩张；没有未归属的横向 controller。

### 9.3 类型标注与真机清单

边界类型均显式列出：Go `proto.SeatSource`、Go/TS `driver_source`、Go
`CoordinatorRebindReq`、TS `CoordinatorRebindMode`、D3 `{error,session_id}`、
`SessionSpec.Env`。最终实现验收必须实际检查：

- linux-01/codex env 注入能产生 `cli:codex#<session>`；
- 本机 SQLite bind/rebind-self 不经 HTTP；
- HTTP launch/rebind-launch 使用真实 coordinator squad/载体，成功写 coordinate；
- CAS race 返回 409+session_id 且没有 kill；
- agentd 重启后 status/Wake 以 ledger 为准；
- bind Wake no-op，coordinate Resume/重建携带 HOME、Workdir、Model 与两环境键；
- 房间 `@B`、relay、wait 和 step 使用同一席位字符串；
- Web Chromium/WK/Wails 面能叫机器人、换绑机器人，不能 self；
- 列表/抽屉展示 source/session 无详情 N+1；
- `go test` 受影响包、Web Vitest、TypeScript typecheck 均真实退出码 0。

## 10. 自审三查、占位符声明与收口顺序

### 10.1 spec 覆盖

冻结断言 1–30 归 T1，31–39/56–58 归 T2，40–51 归 T3，52–58 归 T4，59–62 归
T5；跨卡交接由 T1→T2、T1→T3、T1→T5 的 Consumes/Produces 表逐字对齐。用户故事
“坐下当前会话”“叫机器人占 coordinate”“rebind self/launch”“旧 API 不改座”“
Wake 来源执法”“房间/step/Pending”“Web 列表与按钮”均有具体任务和测试入口。

### 10.2 占位符扫描声明

本稿不使用未决标记、任务间空泛引用或未定义的错误处理指令。测试步骤复用仓库
既有 harness：`cmd/runLedgerCLI`、`cmd/runSubcommandForTest`、
`internal/agentd/newHostTestEnv`/`testhttp.NewServer`、现有 agentd fake runner、
Web Vitest Testing Library。因这些 harness 的构造字段随包定义，本稿采用允许的
“既有 harness + 逐条列全断言”形式；每条断言均在 T1–T5 正文和接缝矩阵中列出，
没有用内部锁替代声明缝。D3 barrier 使用既有 fake runner 的可控回调，不新起进程。

### 10.3 收口命令

实现者完成 T5 后依次执行：

```text
go test ./internal/proto/... ./internal/ledger/... ./internal/keystone/... ./internal/collab/... ./internal/client/... ./internal/agentd/... ./cmd/... -count=1
cd web && npm test && npm run typecheck
git diff --check
git status --short --branch
```

全量命令只作为卡级收口，不归任何单个任务的最小测试范围；它必须在所有任务局部
绿测完成后运行。提交前将实际 commit 命令及原始输出追加台账，再只 amend 一次
收进同批提交；最终判据是工作树干净，台账不追写 amend 后 hash。
