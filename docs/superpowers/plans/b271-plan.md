# B271 本机卡派发是本机：禁止 loopback 当远端再镜像

状态：实现计划；规格 `docs/superpowers/specs/b271.md` r1 已批准，审查意见已吸收。
法定产出物：`docs/superpowers/plans/b271-plan.md`。
事实台账：`docs/superpowers/specs/b271-ledger.md`；实现者每确立一个事实、跑完一条命令或放弃一次尝试，都追加一行。

本计划只覆盖本机身份收口、两条镜像的自机分流、本机事件源、节点本机客户端和 WorkBranch 等价判断。实现者不得修改任务/工单状态机、`from_seq` 的开区间语义、`mirrorSkip`、`resume --force` 的事件类型、WS 乱序迟到断开规则，亦不得把 `--target 本机` 或 `--target localhost` 变成魔法别名。

## 1. 基线证据、图读数与执行边界

### 1.1 本节点已实际执行的基线

下表是本计划节点亲自运行的结果。时间只用于记录，不作为验收计数；实现者开始每个 task 前必须在未改该 task 实现的状态下重跑对应命令。输出发生变化时，先把原始输出追加台账，再重新确认判据。

| 范围 | 命令 | 本节点实跑结果 |
|---|---|---|
| config/store/ledgerstep/ledgermirror | `go test ./internal/config ./internal/store ./internal/ledgerstep ./internal/ledgermirror -count=1` | 退出码 0；逐包为 `ok github.com/Xsxdot/handoff/internal/config 0.024s`、`ok github.com/Xsxdot/handoff/internal/store 5.209s`、`ok github.com/Xsxdot/handoff/internal/ledgerstep 7.803s`、`ok github.com/Xsxdot/handoff/internal/ledgermirror 3.327s` |
| agentd | `go test ./internal/agentd -count=1` | 退出码 0；原始结果 `ok github.com/Xsxdot/handoff/internal/agentd 150.339s` |
| CLI B271 相关 | `go test ./cmd -run 'Test(TargetEndpoint|BareDispatch|CardDispatch)' -count=1` | 退出码 0；原始结果 `ok github.com/Xsxdot/handoff/cmd 2.194s` |
| CLI 全包诊断 | `go test ./cmd -count=1` | 退出码 1；输出末尾原文为 `FAIL`、`FAIL\tgithub.com/Xsxdot/handoff/cmd\t10.687s`、`FAIL`。失败原因未验证，不能把它归因于 B271；实现 task 只使用命名子集，最终全量由协调者执行。 |

基线命令都在 `ef61ffeac`（`origin/fix/b271-local-dispatch`）上、计划实现尚未改动前执行。`git fetch origin fix/b271-local-dispatch` 与 `git merge --ff-only origin/fix/b271-local-dispatch` 已实际成功：当前分支 `cards/B271-charter` 快进到 `ef61ffea`，父提交 `d319f92d`，无冲突。

### 1.2 图与源码核对

- 仓内存在 `codegraph/best.json`、`codegraph/baseline.json`。已运行：
  - `go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . context d_transport_channel`
  - `go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . context d_gateway`
  - `go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . context d_orchestration`
  - `go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . context d_ledger`
  - `go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . sym ViaTemplate`
  - `go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . who-calls waitForTurnEnd`
  - `go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . chain n_ledgerstep_Dispatcher_ViaTemplate --with-source`
  - `go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . chain n_agentd_Mirror_discoverOnce --with-source`
  - `go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . chain n_ledgermirror_Mirror_reconcile --with-source`
  - `go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . chain n_ledgerstep_StepRunner_awaitNode --with-source`
- `sym ViaTemplate` 实际命中 `func (d *Dispatcher) ViaTemplate(ctx context.Context, c ledger.Card, req TemplateDispatch) (DispatchResult, error)`，文件为 `internal/ledgerstep/dispatch.go:120`。
- `who-calls waitForTurnEnd` 实际回到 `StepRunner.awaitNode`，再由 `StepRunner.Run` 调用；因此节点等待不是卡流等待，不能用账本镜像来替换 `WaitEvent`。
- `chain n_ledgerstep_Dispatcher_ViaTemplate --with-source` 未截断，源码窗口确认当前空 target 拒绝、WorkBranch 判断、`ModelByTarget[target]`、`Transport`、`LinkTask` 与 dispatched 快照在同一主链。
- `chain n_agentd_Mirror_discoverOnce --with-source` 的组合输出发生预算截断；`internal/agentd/mirror.go` 已用源码窗口核实。 `context d_ledger` 退出码为 0，但输出有 `fociTruncated total=57 shown=5 reason=focus-quota`、`truncated=false`，且图接口列表仍给出旧版 `ledgermirror.New` 形状。故实现签名以当前源码为准，不以过期图接口列表为准；这是本卡的图新鲜度/覆盖债。
- 当前实现事实：`internal/agentd/mirror.go` 的 `onEvent` 会 `AppendMirrorEvent` 后 `hub.Publish`；`internal/ledgermirror/mirror.go` 的 `reconcile` 先 `registered`、再 `Machines.For`、再 `Source`；`internal/store/store.go` 已有 `SetEventHook` 单一同步钩子和 `EventsFromAsc(taskID string, fromSeq int64, limit int)`；`internal/agentd/hub.go` 的缓冲为 16 且 `Watchers` 只代表真实 WS 订阅者。

### 1.3 任务 DAG 与共享接口

执行顺序为 `Task 1 → Task 2 → Task 3`。Task 1 先落共享的自机地址判定、可复用本机拨号地址和 store 门铃；Task 2 才把本机源接到 ledger mirror、把自机从 task mirror 发现分支拿掉；Task 3 使用同一判定接入 ViaTemplate、节点运行器和 CLI，并完成仓内技能说明。各 task 的文件集合有界，不能顺手改全包。

以下接口是 task 之间逐字对齐的约定；函数名、参数类型/顺序、返回值类型/顺序不得改写成别名。

```go
// internal/config/listenclass.go
func IsSelfTarget(listen string, target Target) bool
func LocalDialAddr(listen string) string

// internal/store/store.go（或同包新增 eventdoorbell.go）
func (s *Store) SubscribeEventDoorbell(taskID string) (events <-chan struct{}, cancel func())

// internal/ledgermirror/mirror.go
type LocalEventStore interface {
    EventsFromAsc(taskID string, fromSeq int64, limit int) ([]proto.Event, error)
    SubscribeEventDoorbell(taskID string) (<-chan struct{}, func())
}
func NewLocalSource(events LocalEventStore, log *slog.Logger) Source

type Options struct {
    Holder       string
    Tick         time.Duration
    LeaseTTL     time.Duration
    Source       Source
    LocalSource  Source
    IsSelfTarget func(target string) bool
}
type Source func(ctx context.Context, c *client.Client, taskID string, fromSeq int64,
    onEvent func(proto.Event) error) error

// internal/agentd/mirror.go
func NewMirror(pool *targetclient.Pool, st *store.Store, hub *Hub,
    isSelfTarget func(name string) bool, log *slog.Logger) *Mirror

// internal/agentd/server.go / cardstep.go
func (s *Server) IsSelfTarget(name string) bool
func (s *Server) CanonicalTarget(name string) string
func (s *Server) clientForTarget(target string) (*client.Client, error)

// internal/ledgerstep/dispatch.go
type Dispatcher struct {
    St *ledger.Store
    Transport Transport
    Actor string
    DisciplineText string
    DisciplineVersion int
    NormalizeTarget func(target string) string
}

// internal/agentd/cardstep.go
func (s *Server) resolveStepDiscipline(node ledger.NodeDef, reqTarget string) (discipline.ResolvedDiscipline, string, error)

// cmd/agentd.go
func setupLedger(cfg *config.Config, srv *agentd.Server, taskStore *store.Store,
    ctx context.Context, logger *slog.Logger) (func(), error)
```

`Dispatcher.NormalizeTarget == nil` 必须等价于恒等函数，保证已有远端测试和既有调用方不被无关改变。`Server.CanonicalTarget` 对空串返回空串；只对配置中存在且经 `config.IsSelfTarget(s.conf().Listen, target)` 判定为自机的登记名返回空串；未知名原样返回，交给既有“未登记”错误。

## 2. Task 1：统一自机判定、本机拨号地址与任务 store 门铃

### 2.1 文件范围与 Interfaces

只允许改动下列文件：

- 生产：`internal/config/listenclass.go`、`cmd/root.go`、`internal/store/store.go`；若为职责隔离新增文件，只能是 `internal/store/eventdoorbell.go`。
- 测试：`internal/config/listenclass_test.go`、`internal/store/eventhook_test.go`；若新增门铃测试文件，只能是 `internal/store/eventdoorbell_test.go`。
- 本 task 不改 `internal/ledgermirror/mirror.go` 的 reconcile，不改 `internal/agentd/mirror.go`，不改 `internal/agentd/hub.go`。

消费与产出：

```go
// Consumes: existing Config.Listen, Target and ClassifyListen.
// Produces: the single self-address classifier and local dial endpoint.
func IsSelfTarget(listen string, target Target) bool
func LocalDialAddr(listen string) string

// Consumes: the existing synchronous AppendEvent/SetEventHook contract.
// Produces: task-scoped persisted-event wakeups for NewLocalSource.
func (s *Store) SubscribeEventDoorbell(taskID string) (<-chan struct{}, func())
```

### 2.2 基线判据先跑与最小测试范围

1. 先运行 `go test ./internal/config ./internal/store -count=1`，预期两个包均输出 `ok`、退出码 0；只复核本 task 触及的包。
2. 再运行 `go test ./cmd -run 'TestTargetEndpointLocalAuth|TestTargetEndpointLocalRewrite' -count=1`，预期输出 `ok github.com/Xsxdot/handoff/cmd ...`、退出码 0；只复核 `cmd/root.go` 委托不破坏现有本机端点。
3. 若输出变化，先追加台账原文；不以本计划已有时间值替代新结果。

### 2.3 红绿步骤：地址身份接缝的失败测试

这是纯函数附加内部锁；真正的生产接缝在 Task 2 的 task mirror、ledger mirror 和 Task 3 的 WorkBranch/客户端测试中。单独矩阵的合法理由是：URL 去 scheme、`net.SplitHostPort` 失败、IPv6/通配/loopback 归一无法由某一条单独镜像接缝逐项显式枚举，若没有矩阵，带 `http://` 的生产地址会被一个高层正向用例掩盖。

1. 在 `internal/config/listenclass_test.go` 追加完整测试，先运行 `go test ./internal/config -run TestIsSelfTarget -count=1`，实现前应因缺少函数而编译失败；所有子用例必须保留：

```go
func TestIsSelfTarget(t *testing.T) {
    cases := []struct {
        name, listen string
        target Target
        want bool
    }{
        {"scheme loopback against single listen", "100.64.0.5:7777", Target{Addr: "http://127.0.0.1:7777"}, true},
        {"exact address after scheme removal", "127.0.0.1:7777", Target{Addr: "https://127.0.0.1:7777"}, true},
        {"localhost same port", "127.0.0.1:7777", Target{Addr: "localhost:7777"}, true},
        {"wildcard listen loopback variant", "0.0.0.0:7777", Target{Addr: "http://127.0.0.1:7777"}, true},
        {"ipv6 loopback same port", "[::1]:7777", Target{Addr: "http://[::1]:7777"}, true},
        {"same listen host and port", "myhost.local:7777", Target{Addr: "http://myhost.local:7777"}, true},
        {"other direct host", "127.0.0.1:7777", Target{Addr: "10.0.0.9:7777"}, false},
        {"other port", "127.0.0.1:7777", Target{Addr: "127.0.0.1:8888"}, false},
        {"relay never self", "127.0.0.1:7777", Target{Relay: "wss://relay", Credential: "c", Node: "node", Token: "token"}, false},
        {"malformed direct addr", "127.0.0.1:7777", Target{Addr: "http://"}, false},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            if got := IsSelfTarget(tc.listen, tc.target); got != tc.want {
                t.Fatalf("IsSelfTarget(%q, %+v) = %v, want %v", tc.listen, tc.target, got, tc.want)
            }
        })
    }
}

func TestLocalDialAddrReusesListenClassification(t *testing.T) {
    cases := map[string]string{
        "127.0.0.1:7777": "http://127.0.0.1:7777",
        "0.0.0.0:7777": "http://127.0.0.1:7777",
        "100.64.0.5:7777": "http://127.0.0.1:7777",
        "http://127.0.0.1:7777": "http://127.0.0.1:7777",
    }
    for listen, want := range cases {
        if got := LocalDialAddr(listen); got != want {
            t.Fatalf("LocalDialAddr(%q) = %q, want %q", listen, got, want)
        }
    }
}
```

2. 在 `internal/config/listenclass.go` 实现以下核心逻辑。可把重复的 `net.ParseIP` 存为局部变量，但不能改变判定集合：

```go
func IsSelfTarget(listen string, target Target) bool {
    if target.IsRelay() || target.Addr == "" {
        return false
    }
    listenHP, ok := normalizeHostPort(listen)
    if !ok {
        return false
    }
    targetHP, ok := normalizeHostPort(target.Addr)
    if !ok {
        return false
    }
    if targetHP == listenHP {
        return true
    }
    _, loopback := ClassifyListen(listenHP)
    loopbackHP, loopbackOK := normalizeHostPort(loopback)
    if loopbackOK && targetHP == loopbackHP {
        return true
    }
    listenHost, listenPort, listenOK := splitNormalizedHostPort(listenHP)
    targetHost, targetPort, targetOK := splitNormalizedHostPort(targetHP)
    targetIP := net.ParseIP(targetHost)
    return listenOK && targetOK && listenPort == targetPort &&
        (targetHost == "localhost" || targetIP != nil && targetIP.IsLoopback())
}

func LocalDialAddr(listen string) string {
    if cls, loopback := ClassifyListen(listen); cls != ListenLoopback {
        listen = loopback
    }
    if !strings.Contains(listen, "://") {
        listen = "http://" + listen
    }
    return listen
}

func normalizeHostPort(raw string) (string, bool) {
    raw = strings.TrimSpace(raw)
    if strings.Contains(raw, "://") {
        u, err := url.Parse(raw)
        if err != nil || u.Host == "" {
            return "", false
        }
        raw = u.Host
    }
    host, port, err := net.SplitHostPort(raw)
    if err != nil || port == "" {
        return "", false
    }
    return net.JoinHostPort(strings.ToLower(host), port), true
}

func splitNormalizedHostPort(raw string) (host, port string, ok bool) {
    host, port, err := net.SplitHostPort(raw)
    return strings.ToLower(host), port, err == nil
}
```

必须先判断 `://`，因为 `url.Parse("127.0.0.1:7777")` 会把前段误当 scheme；`ClassifyListen` 只能接收无 scheme 的 host:port，所以传入 `listenHP`，不能把原始 `target.Addr` 直接传给它。relay 先判 false，token、登记名和 relay node 都不参与身份判断。

3. 把 `cmd/root.go` 的现有 `localDialAddr` 改成调用 `config.LocalDialAddr`，保留其现有 debug 日志（只在 `ClassifyListen` 不是 loopback 时记录 `listen` 和最终 `dial`）；不要在 cmd 再复制地址归一算法。运行 `TargetEndpoint` 子集至绿。

### 2.4 红绿步骤：store 落库门铃

1. 新增 `internal/store/eventdoorbell_test.go`，测试必须使用真实 `Open`、`AppendEvent`、`EventsFromAsc`，先运行 `go test ./internal/store -run TestEventDoorbell -count=1`，实现前应失败。断言逐条列全：

   - `SubscribeEventDoorbell("task-a")` 不接收 `task-b` 的 append；
   - `task-b` 自己的 channel 在一秒内收到通知；
   - task-a 连续 append progress 与 permission_request 时至少收到一次通知；
   - 未消费第一条通知前第二次 append 不生成第二个 channel 通知（容量 1 合并）；
   - `EventsFromAsc("task-a", 0, 10)` 仍返回两条真实事件、按 seq 升序，证明通知计数不被当作事件数。

   该测试是 store 门铃没有更外层声明缝时的附加内部锁；它不能替代 Task 2 从 `Mirror.reconcile`/`Mirror.subscribe` 进入的本机源测试。

2. 在 `Store` 增加 task → `map[chan struct{}]struct{}` 的互斥容器，懒初始化以保持既有构造路径。`SubscribeEventDoorbell` 返回容量 1 channel 和幂等 cancel；cancel 只从 map 删除，不关闭 channel。signal 持锁遍历目标 task，用非阻塞 send 合并通知，不能阻塞 `AppendEvent`。
3. 在 `AppendEvent` 的 INSERT 成功、`proto.Event` 组装完成之后保留现有 `fireEventHook(evt)`，并紧接着调用 doorbell signal；不得把门铃塞进 `SetEventHook` 或替换 frames 钩子。钩子 panic recover 和事件返回语义保持。
4. 运行 `go test ./internal/store -run 'Test(EventHook|EventDoorbell|EventsFromAsc)' -count=1` 至绿，再运行 Task 1 的最小范围命令。

实现代码按以下完整形状落在 store.Store；与 eventHook 使用不同的锁和字段，不替换已有 frames 钩子：

~~~
type Store struct {
    db *sql.DB

    eventHookMu sync.RWMutex
    eventHook   func(proto.Event)

    eventDoorbellMu sync.Mutex
    eventDoorbells  map[string]map[chan struct{}]struct{}
}

func (s *Store) SubscribeEventDoorbell(taskID string) (<-chan struct{}, func()) {
    events := make(chan struct{}, 1)
    s.eventDoorbellMu.Lock()
    if s.eventDoorbells == nil {
        s.eventDoorbells = make(map[string]map[chan struct{}]struct{})
    }
    if s.eventDoorbells[taskID] == nil {
        s.eventDoorbells[taskID] = make(map[chan struct{}]struct{})
    }
    s.eventDoorbells[taskID][events] = struct{}{}
    s.eventDoorbellMu.Unlock()

    var once sync.Once
    cancel := func() {
        once.Do(func() {
            s.eventDoorbellMu.Lock()
            listeners := s.eventDoorbells[taskID]
            delete(listeners, events)
            if len(listeners) == 0 {
                delete(s.eventDoorbells, taskID)
            }
            s.eventDoorbellMu.Unlock()
        })
    }
    return events, cancel
}

func (s *Store) signalEventDoorbell(taskID string) {
    s.eventDoorbellMu.Lock()
    defer s.eventDoorbellMu.Unlock()
    for events := range s.eventDoorbells[taskID] {
        select {
        case events <- struct{}{}:
        default:
        }
    }
}
~~~

Store 的现有构造路径不需要初始化 eventDoorbells；nil map 在第一次订阅时懒建。signalEventDoorbell 的读写必须与 cancel 共用 eventDoorbellMu，channel 不关闭，避免 AppendEvent 与取消并发时向已关闭 channel 发送。将 AppendEvent 中已有的三行顺序改为：

~~~
s.fireEventHook(evt)
s.signalEventDoorbell(evt.TaskID)
return evt, nil
~~~

fireEventHook 仍先执行，保证既有帧观察顺序；门铃只表示“有新落库事件”，不携带事件内容、不增加 seq、失败也不改变 AppendEvent 的成功返回。

### 2.5 Task 1 的日志、注释与验收

- `IsSelfTarget`、`LocalDialAddr` 的导出注释写参数、返回值与注意事项：前者只比地址、relay 永不自机；后者复用 `ClassifyListen`、输出带 scheme 的本机拨号地址。新 helper 注释解释先去 scheme 与 `ClassifyListen` 禁接 scheme 的原因。
- 门铃字段和方法注释写清 task-scoped、容量 1 只作“有新事件”提示、真实事件必须再次从 store 以 `seq > fromSeq` 读取；cancel 不关闭共享 channel，避免并发 signal/cancel panic。
- Task 1 不新增错误返回路径；关键节点不使用 print，不以 CLI stderr 作为诊断通道。

## 3. Task 2：两条镜像排除自机，并以本机事件库作为账本源

### 3.1 文件范围与 Interfaces

只允许改动下列文件：

- 生产：internal/ledgermirror/mirror.go、internal/agentd/mirror.go、cmd/agentd.go。
- 测试：internal/ledgermirror/mirror_test.go、internal/ledgermirror/machines_test.go、internal/agentd/mirror_test.go、internal/agentd/mirror_pool_test.go；若需要门铃到镜像的跨包回归，只能新增 internal/agentd/mirror_local_test.go。

Consumes：

~~~
// internal/store
func (s *store.Store) EventsFromAsc(taskID string, fromSeq int64, limit int) ([]proto.Event, error)
func (s *store.Store) SubscribeEventDoorbell(taskID string) (<-chan struct{}, func())

// internal/ledgermirror
type Source func(ctx context.Context, c *client.Client, taskID string, fromSeq int64,
    onEvent func(proto.Event) error) error
type Machines interface {
    Names() []string
    For(name string) (*client.Client, error)
}
~~~

Produces：

~~~
// internal/ledgermirror
func NewLocalSource(events LocalEventStore, log *slog.Logger) Source
func New(st *ledger.Store, machines Machines, opt Options) *Mirror

// internal/agentd
func NewMirror(pool *targetclient.Pool, st *store.Store, hub *Hub,
    isSelfTarget func(name string) bool, log *slog.Logger) *Mirror
~~~

NewLocalSource 的 c 参数必须接受 nil 且不读取它；远端 Source 保持 DefaultSource 的 MarkForwarded().StreamEventsOnce 行为。Options.Source 仍是远端源，Options.LocalSource 只服务空 target 或自机登记名；Options.IsSelfTarget 为 nil 时只把空 target 当本机。两条镜像都保留 pool.Names() 的完整返回值供 UI、升级和探活使用，但发现、订阅、快照门铃不能触碰自机。

### 3.2 基线判据与最小测试范围

1. 未改 Task 2 生产代码时运行 go test ./internal/ledgermirror -count=1；预期退出码 0。
2. 未改 Task 2 生产代码时运行 go test ./internal/agentd -run 'TestMirror' -count=1；预期退出码 0，覆盖既有远端发现、终态、池变更行为。
3. 未改 Task 2 生产代码时运行 go test ./cmd -run TestSetupLedgerMountsWithRetiredEnabledFlag -count=1；预期退出码 0，确认 agentd 接线测试入口可用。
4. 实现者只重跑 internal/store、internal/ledgermirror、internal/agentd 与上述命名 cmd 测试；全包测试不属于本 task。

### 3.3 红绿步骤：本机源穿过账本镜像接缝

1. 在 internal/ledgermirror/mirror_test.go 增加缝级测试 TestMirrorLocalTargetUsesLocalSource，先运行 go test ./internal/ledgermirror -run TestMirrorLocalTargetUsesLocalSource -count=1，实现前必须红。测试复用该文件已有 testLedger、fakeMachines 和真实账本镜像对象；若需真实本机事件库，照抄 internal/store/eventhook_test.go 的临时 SQLite Store 建立方式。该测试逐条断言：

   - 空 target 的 TaskLink 被 reconcile 接受；fake Machines 的 For("") 调用次数为 0；
   - Options.LocalSource 使用 NewLocalSource(localStore, logger)，而不是 DefaultSource；
   - 本机 store 先追加 32 条 progress，再追加 seq=N 的 permission_request；不需要任何 Hub Subscribe/Publish，账本镜像仍在一秒内收到 permission_request；
   - 账本 card_events 中对应记录的 source_target == ""、source_task 等于 task ID、source_seq == N，permission_request 原 payload 保持不变；
   - approver_decision 不出现在镜像结果中，progress 不出现在镜像结果中；
   - 对同一个 seq 再触发一次门铃后，唯一键保持一条，不能得到重复 task_mirrored；
   - fakeMachines.For 的所有调用记录都为空，证明本机路径没有拨号、没有 StreamEventsOnce。

   这是声明缝 Mirror.reconcile → Mirror.subscribe → Options.LocalSource 的最小端到端锁；store 门铃纯测试只能附加，不能替代它。实现前先保留红色输出；实现后用同一命令跑绿。

2. 在 internal/ledgermirror/mirror.go 新增本机源。完整函数逻辑如下；批量读取用现有 EventsFromAsc 的开区间，从回调成功后才前进游标，等待只靠 store 门铃，所以不会使用 Hub 的 16 槽缓冲：

~~~
const localSourceBatchSize = 100

func NewLocalSource(events LocalEventStore, log *slog.Logger) Source {
    return func(ctx context.Context, c *client.Client, taskID string, fromSeq int64,
        onEvent func(proto.Event) error) error {
        if events == nil {
            return errors.New("本机事件源缺少 store")
        }
        if onEvent == nil {
            return errors.New("本机事件源缺少事件回调")
        }
        wake, cancel := events.SubscribeEventDoorbell(taskID)
        defer cancel()
        cursor := fromSeq
        log.Info("本机账本源启动", "task", taskID, "from_seq", fromSeq)
        for {
            if err := ctx.Err(); err != nil {
                log.Info("本机账本源退出", "task", taskID, "cause", err)
                return err
            }
            batch, err := events.EventsFromAsc(taskID, cursor, localSourceBatchSize)
            if err != nil {
                log.Warn("本机账本源回放失败", "task", taskID, "from_seq", cursor, "err", err)
                return err
            }
            if len(batch) > 0 {
                for _, event := range batch {
                    if err := ctx.Err(); err != nil {
                        log.Info("本机账本源退出", "task", taskID, "cause", err)
                        return err
                    }
                    if err := onEvent(event); err != nil {
                        log.Warn("本机账本源交付失败", "task", taskID,
                            "seq", event.Seq, "type", event.Type, "err", err)
                        return err
                    }
                    cursor = event.Seq
                }
                continue
            }
            select {
            case <-ctx.Done():
                log.Info("本机账本源退出", "task", taskID, "cause", ctx.Err())
                return ctx.Err()
            case <-wake:
                log.Debug("本机账本源收到门铃", "task", taskID, "from_seq", cursor)
            }
        }
    }
}
~~~

函数头注释要写清：参数 c 在本机路径必须为 nil、fromSeq 排他、事件按 seq 升序回放、函数阻塞到 ctx 取消或回调失败。成功回放、门铃唤醒、回放失败、回调失败和取消都必须有结构化日志；不使用 print。mirrorSkip 仍只在 subscribe 的回调处判断，不能在 Source 里过滤，否则 Source 无法复用既有事件语义。

### 3.4 实现镜像自机分流与接线

1. 扩展 Options，保留原字段并新增下列字段；New 不擅自把本机 Store 当成远端 Source，生产接线明确传入 LocalSource：

~~~
type LocalEventStore interface {
    EventsFromAsc(taskID string, fromSeq int64, limit int) ([]proto.Event, error)
    SubscribeEventDoorbell(taskID string) (<-chan struct{}, func())
}

type Options struct {
    Holder       string
    Tick         time.Duration
    LeaseTTL     time.Duration
    Source       Source
    LocalSource  Source
    IsSelfTarget func(target string) bool
}
~~~

在 New 中保留 Tick、LeaseTTL、Holder、Source 的原默认值。Source == nil 仍为 DefaultSource；LocalSource == nil 不可静默回退到 DefaultSource，应在第一次需要本机 link 时写带 target/task 的错误并让该订阅按已有重试/健康规则处理。IsSelfTarget 的判定封装成：

~~~
func (m *Mirror) isLocalTarget(target string) bool {
    return target == "" || m.opt.IsSelfTarget != nil && m.opt.IsSelfTarget(target)
}
~~~

2. 改 reconcile 的 want 构造和取客户端顺序。先建立 registered，空 target 与 isLocalTarget 为 true 的登记名都允许进入 want；只有非本机 target 才调用 Machines.For。保持 For 在订阅锁之前完成，以免配置错误时持锁；本机 client 必须是 nil，不能伪造一个 client 给 LocalSource：

~~~
want := map[string]ledger.TaskLink{}
for _, link := range links {
    if link.Target != "" && !registered[link.Target] && !m.isLocalTarget(link.Target) {
        continue
    }
    want[link.Target+"/"+link.TaskID] = link
}

clients := map[string]*client.Client{}
for _, link := range want {
    if m.isLocalTarget(link.Target) {
        continue
    }
    if _, ok := clients[link.Target]; ok {
        continue
    }
    c, err := m.machines.For(link.Target)
    if err != nil {
        m.log.Warn("取远端机器客户端失败，本轮跳过", "target", link.Target, "err", err)
        continue
    }
    clients[link.Target] = c
}
~~~

订阅增删判断也要分本机/远端：本机 link 不因 clients[""] == nil 被丢弃，不比较 nil client；远端仍比较 client 指针，保留配置变更退订重订。起订时调用 go m.subscribe(subCtx, link, clients[link.Target])，本机传 nil。subscribe 中完整替换事件源选择：

~~~
var source Source
var c *client.Client
if m.isLocalTarget(link.Target) {
    source = m.opt.LocalSource
    if source == nil {
        err := errors.New("本机账本源未配置")
        m.log.Warn("本机订阅无法建立", "target", link.Target, "task", link.TaskID, "err", err)
        return
    }
    m.log.Info("建立本机账本订阅", "target", link.Target, "task", link.TaskID, "from_seq", wm)
} else {
    if c == nil {
        m.log.Warn("远端客户端缺失，跳过订阅", "target", link.Target, "task", link.TaskID)
        return
    }
    source = m.opt.Source
    m.log.Info("建立远端账本订阅", "target", link.Target, "task", link.TaskID, "from_seq", wm)
}
err = source(ctx, c, link.TaskID, wm, func(e proto.Event) error {
    if mirrorSkip[e.Type] {
        m.log.Debug("账本镜像过滤事件", "target", link.Target, "task", link.TaskID,
            "seq", e.Seq, "type", e.Type)
        return nil
    }
    wrote, err := m.st.AppendMirroredEvent(link.CardID, ledger.MirroredEvent{
        Target: link.Target, Task: link.TaskID, SourceSeq: e.Seq,
        Type: string(e.Type), Payload: e.Payload, CreatedAt: e.CreatedAt,
    })
    if err != nil {
        m.log.Warn("账本镜像落库失败", "target", link.Target, "task", link.TaskID,
            "seq", e.Seq, "type", e.Type, "err", err)
        return err
    }
    if wrote && !m.isLocalTarget(link.Target) {
        if err := m.st.TouchMirrorHealth(link.Target, e.Seq); err != nil {
            m.log.Warn("远端镜像健康更新失败", "target", link.Target, "seq", e.Seq, "err", err)
        }
    }
    if e.Type == proto.EventTypeArchived {
        return errMirrorArchived
    }
    return nil
})
~~~

上面代码放在当前 subscribe 的水位获取之后，保留断线退避、errMirrorArchived、m.ended 和 mirrorSkip 的现有行为；变量作用域必须让每轮重连重新选择远端 client。空 target 不写 mirror_cursors，避免本机被伪装成 per-target 远端健康行；健康 touch 的现有远端分支、配置缺失日志和归档收尾都保留。MirrorHealth 行回扫时跳过 isLocalTarget(row.Target)。

3. 修改 cmd/agentd.go 的接线：setupLedger 增加 taskStore *store.Store 参数，并使用与 task mirror 同一个本机 Store：

~~~
func setupLedger(cfg *config.Config, srv *agentd.Server, taskStore *store.Store,
    ctx context.Context, logger *slog.Logger) (func(), error) {
    ldsn := cfg.Ledger.DSN
    if ldsn == "" {
        ldsn = filepath.Join(cfg.DataDir, "ledger.db")
    }
    lst, err := ledger.Open(ldsn)
    if err != nil {
        logger.Error("打开账本库失败", "dsn", ldsn, "cause", err)
        return nil, fmt.Errorf("打开账本库: %w", err)
    }
    srv.SetLedger(lst)
    host, _ := os.Hostname()
    localSource := ledgermirror.NewLocalSource(taskStore, logger.With("source", "local"))
    lm := ledgermirror.New(lst, srv.Pool(), ledgermirror.Options{
        Holder:       host,
        LocalSource:  localSource,
        IsSelfTarget: srv.IsSelfTarget,
    })
    go lm.Run(ctx)
    logger.Info("账本镜像子系统已挂载", "holder", host,
        "machines", len(srv.Pool().Names()), "dsn", ldsn)
    return func() {
        lm.Stop()
        lst.Close()
    }, nil
}
~~~

实现者按当前 cmd/agentd.go 的真实变量直接调用 setupLedger(cfg, srv, st, wdCtx, logger)，并把 cmd/agentd_ledger_test.go 的调用改为 setupLedger(cfg, srv, taskStore, ctx, discardLogger())；Run/Stop 与 Close 仍只保留这一套生命周期。

4. 修改 internal/agentd/mirror.go 的构造参数和两个发现入口。Mirror 增加 isSelfTarget func(name string) bool，nil 时恒为 false；完整筛选规则：

~~~
func (m *Mirror) remoteMachineNames() []string {
    names := m.machineNames()
    if m.isSelfTarget == nil {
        return names
    }
    out := make([]string, 0, len(names))
    for _, name := range names {
        if m.isSelfTarget(name) {
            m.log.Debug("跳过本机任务镜像", "machine", name)
            continue
        }
        out = append(out, name)
    }
    return out
}
~~~

ensureSnapshotLoops 和 discoverOnce 都只使用 remoteMachineNames；machineNames 本身仍直接返回 pool.Names()，不能把 local 从池中删除。discoverOnce 的结果长度、fan-out、失败日志和成功日志照旧，只是不再为自机调用 ListTasks；本地 task 不调用 UpsertMirrorTask，也不开 subscribe。远端 task 的 AppendMirrorEvent 后 hub.Publish 保持一次，不改变 Hub 语义。

### 3.5 红绿步骤：任务镜像发现自机排除

1. 在 internal/agentd/mirror_test.go 增加 TestMirrorDiscoverOnceSkipsSelfTarget，复用已有 newTestAgentdEnv、Pool 和 fake target HTTP harness；先运行 go test ./internal/agentd -run TestMirrorDiscoverOnceSkipsSelfTarget -count=1，实现前必须红。断言逐条列全：

   - Pool Names 同时包含 local 和 devbox，注入的自机谓词只对 local 返回 true；
   - local target 的 ListTasks HTTP 计数为 0，local target 的 StreamEventsOnce/WS 连接计数为 0；
   - devbox 仍恰好执行一次 ListTasks，其活跃任务仍启动既有订阅，证明过滤没有把远端分支删掉；
   - mirror_tasks 不存在 target == "local" 的行；
   - Mirror.machineNames 或同等池查询仍返回两个配置名，不能因跳过发现而缩减 UI/探活数据；
   - 既有远端 mirror 测试全绿。

2. 将构造点统一为 NewMirror(srv.Pool(), st, srv.Hub(), srv.IsSelfTarget, logger)，不在测试里绕过构造器设置私有字段。使用结构化日志记录目标名与跳过原因，生产文件头注释补充“本机任务已在本机 Store/Hub，不进入 mirror_tasks”边界；导出的 NewMirror 注释说明自机谓词和 nil 语义。

### 3.6 高负载与观测验收

1. TestMirrorLocalTargetUsesLocalSource 必须以真实 Store 一次性追加至少 32 条 progress 后再追加 permission_request；不能用逐条等待替代批量压力。断言仍须在真实门铃与 EventsFromAsc 边界上观察到 permission_request，证明不受 Hub 16 槽丢弃影响。
2. 同一测试或 internal/agentd/mirror_local_test.go 中断言本机路径不增加 Hub.Watchers；不能通过真实 /ws/events 订阅来证明无 Hub，因为那会人为增加 watcher。回归既有 TestWSOutOfOrderPublishNotDropped，确认晚到更低 seq 仍按原语义断开，不能因本卡改动让它变绿或变红。
3. 逐条错误分支记录 target、task、seq/from_seq、source 和 err；成功的本机源启动、批回放、门铃唤醒、事件落账也必须有 Info/Debug 结构化日志。新增类型和导出函数写职责、参数、返回与 nil/开区间注意事项；非显然的“先读再等门铃”和“不触碰 Hub”原因写注释。

### 3.7 Task 2 验收

在 Task 2 完成后，触及包只跑：

~~~
go test ./internal/store ./internal/ledgermirror ./internal/agentd -count=1
go test ./internal/agentd -run 'TestMirror|TestEventDoorbell' -count=1
go test ./cmd -run TestSetupLedgerMountsWithRetiredEnabledFlag -count=1
~~~

每条命令都必须亲自取得退出码和原始输出；本 task 通过标准是：空/self link 进入本机源、permission_request 穿过真实 Store→LocalSource→AppendMirroredEvent、approver_decision/progress 仍过滤、没有 For/Hub/WS 自订、远端与既有 WS 乱序回归保持原行为。

## 4. Task 3：节点、CLI 与 WorkBranch 统一本机客户端和身份

### 4.1 文件范围与 Interfaces

只允许改动下列文件：

- 生产：internal/ledgerstep/dispatch.go、internal/ledgerstep/runner.go、internal/agentd/server.go、internal/agentd/cardstep.go、cmd/card_dispatch.go、cmd/root.go、skills/handoff/SKILL.md。
- 测试：internal/ledgerstep/dispatch_test.go、internal/ledgerstep/runner_test.go、internal/agentd/cardstep_discipline_test.go、cmd/card_dispatch_test.go、cmd/dispatch_discipline_test.go、cmd/target_client_test.go、cmd/root_test.go；若必须使用真实本机 HTTP/WS，允许新增 internal/agentd/cardstep_local_test.go 和 cmd/card_dispatch_local_test.go。

Consumes：

~~~
// internal/config
func IsSelfTarget(listen string, target Target) bool
func LocalDialAddr(listen string) string

// internal/ledgerstep
type Transport func(ctx context.Context, opts DispatchOpts) (taskID string, baseCommit string, err error)
func PreflightDiscipline(st *ledger.Store, templateName, overrideName, reqTarget string) (name, target string, err error)
func (r *StepRunner) awaitNode() func(context.Context, string, string) (string, error)
func (r *StepRunner) diffNode() func(context.Context, string, string) ([]string, error)

// internal/agentd
func (s *Server) IsSelfTarget(name string) bool
func (s *Server) CanonicalTarget(name string) string
func (s *Server) clientForTarget(target string) (*client.Client, error)
~~~

Produces：

~~~
// internal/ledgerstep/dispatch.go
type Dispatcher struct {
    St *ledger.Store
    Transport Transport
    Actor string
    DisciplineText string
    DisciplineVersion int
    NormalizeTarget func(target string) string
}

// internal/agentd/cardstep.go
func (s *Server) resolveStepDiscipline(node ledger.NodeDef, reqTarget string) (discipline.ResolvedDiscipline, string, error)
~~~

NormalizeTarget 为 nil 时必须等价于恒等函数；它只负责把已知自机登记名归一为空串，不把未知名称改写。Dispatcher 仍不做网络请求；clientForTarget 负责本机直连或交给 pool.For 取得远端 client。StepRunner 的 awaitNode、diffNode 继续消费同一 Clients 函数，不新增进程内等待通道。

### 4.2 基线判据与最小测试范围

1. 在未改 Task 3 代码的基线运行 go test ./internal/ledgerstep -run 'Test(ViaTemplate|Continuation|StepRunner)' -count=1；预期退出码 0。
2. 运行 go test ./internal/agentd -run 'Test(CardStep|Step)' -count=1；预期退出码 0。
3. 运行 go test ./cmd -run 'Test(TargetEndpoint|BareDispatch|CardDispatch|NamedTarget)' -count=1；本节点已经实际跑过相关子集并得到退出码 0，执行者仍需在 Task 3 改动前复核。
4. 全量 go test ./... 由协调者在所有卡合并后执行，不属于本 task；Task 3 只跑 internal/ledgerstep、internal/agentd 和命名 cmd 子集。

### 4.3 红绿步骤：ViaTemplate 空目标、别名归一和 WorkBranch

1. 在 internal/ledgerstep/dispatch_test.go 增加缝级测试 TestViaTemplateEmptyTargetIsLocal，复用现有模板、卡、ledger Store 和 Transport capture harness；先运行 go test ./internal/ledgerstep -run TestViaTemplateEmptyTargetIsLocal -count=1，实现前必须红。测试逐条断言：

   - 模板 target 为空、请求 target 为空时 ViaTemplate 返回 nil error，Transport 被调用一次；
   - Transport 收到的 DispatchOpts.Target 是空串，DispatchResult.Target 也是空串；
   - dispatched 快照 JSON 中存在 target 键且值为 JSON 字符串空值，不是缺键；再次读取 WorkBranch 返回非空 branch 且 Target 为空；
   - 通过真实 ledger Store 的 TasksOf 读取挂账，target 为空；不是只检查 Transport capture；
   - ModelByTarget 含空键时使用空键模型，避免归一后仍查原登记名；
   - 既有远端模板 target 仍交给原远端 Transport。

2. 增加 TestViaTemplateSelfAliasContinuesLocalWorkBranch：给 Dispatcher 注入 NormalizeTarget，把 local 映射为空串；第一轮请求 target local，第二轮请求 target 为空。两轮都必须调用 Transport，第二轮不得返回“工作分支只存在于创建它的那台机器”，第二轮收到的 Base 为第一轮 branch，挂账与快照两次都保存空 target。再用真实远端名与空 target 交叉调用，断言仍在 Transport 之前拒绝，错误保留 push origin 和显式 base 的行动指引。

3. 最小实现顺序：有效 target 先按请求覆盖再按模板缺省计算，随后调用 d.NormalizeTarget；删除空 target 的“目标机未定”拒绝；WorkBranch 比较改为归一后的 target 与归一后的 workInfo.Target，不能保留 workInfo.Target 为空即拒的析取。历史 JSON 缺少 target 反序列化为空串，也因此按本机处理。LocalBaseBranch 的目标侧本地 ref 检查保持不动，找不到远端遗留分支时仍返回既有工作区错误，不能退回卡基线。ModelByTarget 在归一后的 target 上查询。

4. Dispatcher 的有效 target、WorkBranch 判定、Transport 参数、LinkTask 参数、DispatchResult.Target 与 dispatched 快照必须来自同一归一值；每个错误分支带 card、target、previous_target、branch 和 cause 的结构化日志，成功路径记录 target、task、branch。导出的 NormalizeTarget 字段在 Dispatcher 注释中写 nil 语义与“只归一自机”的边界。

### 4.4 本机 client 接线与纪律探活

1. 在 internal/agentd/server.go 增加：

~~~
func (s *Server) IsSelfTarget(name string) bool {
    if name == "" {
        return true
    }
    cfg := s.conf()
    if cfg == nil {
        return false
    }
    target, ok := cfg.Targets[name]
    return ok && config.IsSelfTarget(cfg.Listen, target)
}

func (s *Server) CanonicalTarget(name string) string {
    if s.IsSelfTarget(name) {
        return ""
    }
    return name
}

func (s *Server) clientForTarget(target string) (*client.Client, error) {
    canonical := s.CanonicalTarget(target)
    if canonical == "" {
        cfg := s.conf()
        if cfg == nil || cfg.Token == "" {
            return nil, fmt.Errorf("本机客户端缺少配置或 token")
        }
        s.log.Info("采用本机节点客户端", "target", target, "canonical_target", canonical)
        return client.New(config.LocalDialAddr(cfg.Listen), cfg.Token), nil
    }
    s.log.Info("采用远端节点客户端", "target", canonical)
    return s.pool.For(canonical)
}
~~~

函数注释必须说明空串与配置中自机地址均返回本机 client，未知名保留给 pool.For 报既有错误；本机 client 不由调用方关闭。错误日志由调用方带 node/task/target，不能只返回裸 error。

2. 修改 internal/agentd/cardstep.go 的 resolveStepDiscipline 签名和调用方。完整控制流必须是：

~~~
func (s *Server) resolveStepDiscipline(node ledger.NodeDef, reqTarget string) (discipline.ResolvedDiscipline, string, error) {
    name, target, err := ledgerstep.PreflightDiscipline(
        s.ledger, node.Template, node.Override.Discipline, reqTarget)
    if err != nil {
        return discipline.ResolvedDiscipline{}, "", err
    }
    target = s.CanonicalTarget(target)
    cl, err := s.clientForTarget(target)
    if err != nil {
        s.log.Error("节点纪律探活取得客户端失败", "node", node.Name, "target", target, "cause", err)
        return discipline.ResolvedDiscipline{}, target, fmt.Errorf("目标机探活失败：%w", err)
    }
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    status, err := cl.Status(ctx)
    if err != nil {
        s.log.Error("节点纪律探活失败", "node", node.Name, "target", target, "cause", err)
        return discipline.ResolvedDiscipline{}, target, fmt.Errorf("目标机探活失败：%w", err)
    }
    cap := status.DisciplinesSupported
    lookup := func(n string) (int, string, error) {
        d, err := s.ledger.GetDiscipline(n, 0)
        if err != nil {
            return 0, "", err
        }
        return d.Version, d.Body, nil
    }
    resolved, err := discipline.ResolveDispatch(lookup,
        discipline.DisciplineRef{Name: name},
        s.conf().PlatformInvariantsEnabled(), cap)
    if err != nil {
        s.log.Warn("节点纪律拒发闸拦下", "node", node.Name, "target", target,
            "discipline", name, "cap_absent", cap == nil, "cause", err)
        return discipline.ResolvedDiscipline{}, target,
            fmt.Errorf("环节 %s 派发前纪律解析失败: %w", node.Name, err)
    }
    s.log.Info("节点纪律正文已就绪", "node", node.Name, "target", target,
        "discipline", name, "version", resolved.Version)
    return resolved, target, nil
}
~~~

删除空 target 的提前返回；本机 target 也必须 Status 探活并走同一 ResolveDispatch 拒发闸。startCardStep 接收 (resolved, target, err)，Dispatcher 设置 NormalizeTarget 为 s.CanonicalTarget，runner 的 Target 使用返回的 canonical target，Clients 设置为 s.clientForTarget，不能再设置为 s.pool.For。stepTransport 把 opts.Target 先 canonicalize，再用 clientForTarget；发送给 agentd 的 DispatchOpts.Target 必须是 canonical 空串。本机/远端的请求前后、Status/Dispatch 每条错误和成功都带 target、canonical_target、task/node 的结构化日志。

3. awaitNode 与 diffNode 保持其入口符号和 WaitEvent/Diff 协议，只保证被注入的 Clients 已能接受空 target。补充导出/非显然逻辑注释：WaitEvent 仍走 /ws/events，本机任务不再被任务镜像二次 Publish；不要把 await 改成直接读 ledger 或 Store。

### 4.5 CLI targetClient 与 skill 说明

1. 在 cmd/card_dispatch.go 实现配置驱动的 canonicalCLITarget，并让 targetClient 先调用它：

~~~
func canonicalCLITarget(target string) (string, error) {
    if target == "" {
        return "", nil
    }
    cfg, err := config.Load(effectiveConfigPath())
    if err != nil {
        return "", fmt.Errorf("加载配置: %w", err)
    }
    t, ok := cfg.Targets[target]
    if !ok {
        return target, fmt.Errorf("目标机 %s 未登记（handoff init/机器登记先行）", target)
    }
    if config.IsSelfTarget(cfg.Listen, t) {
        return "", nil
    }
    return target, nil
}

func targetClient(target string) (*client.Client, func(), error) {
    canonical, err := canonicalCLITarget(target)
    if err != nil {
        return nil, func() {}, err
    }
    if canonical == "" {
        addr, token, err := LocalEndpoint()
        if err != nil {
            return nil, func() {}, err
        }
        slog.Info("CLI 采用本机客户端", "target", target, "canonical_target", canonical)
        return client.New(addr, token), func() {}, nil
    }
    slog.Info("CLI 采用远端客户端", "target", canonical)
    return newTargetClientNamed(canonical)
}
~~~

实际代码直接复用当前 effectiveConfigPath 配置入口，不能让 targetClient 与 TargetEndpoint 读两份配置。未知 target 继续返回含原名的未登记错误；字符串 本机和 localhost 不增加别名。空 target 通过 LocalEndpoint 获得本机 token/LocalDialAddr，不能再输出未指定目标机。

2. card dispatch 的 target 在 discipline 预探活、Dispatcher 构造和 Transport 三处使用同一 canonical 值；不要只让 client 拨本机而把挂账仍写成 local。裸 dispatch 的空 target 已由 newTargetClient 本机路径覆盖，--step 主路径已有 LocalEndpoint，不改变 HTTP 受理与异步返回语义。cmd/root.go 只保留 Task 1 的 LocalDialAddr 委托和日志，TargetEndpoint 的本机端点行为不另造归一算法。

3. 在 skills/handoff/SKILL.md 当前 --step 的目标机说明附近增加一句：本机卡派发省略 --target，并且不要在 targets 登记 loopback 自机；--target 本机不是合法键；版本不一致时的“目标机未定”仍是版本 skew 文案，不表示本机 target 缺失。只改仓内 handoff skill，不改仓外 product-backlog skill。新增句子不能把空 target 改写成魔法别名。

### 4.6 红绿步骤：节点真机清单与序列化边界

1. 在 internal/agentd/cardstep_discipline_test.go 增加 TestLocalStepDisciplineProbesStatus：复用现有真实 ledger/server harness，使用一个与 cfg.Listen 相同的 httptest listener；先运行 go test ./internal/agentd -run TestLocalStepDisciplineProbesStatus -count=1，实现前必须红。断言本机空 target 经过 resolveStepDiscipline 后返回 canonical target 空串，真实本机 /api/status 被命中一次，capability 缺失/存在分别按既有拒发闸处理；不能断言“空 target 返回空 ResolvedDiscipline”。

2. 增加 TestLocalStepTransportUsesLocalClient：从 startCardStep 的生产装配或同一真实 server harness 进入 stepTransport，断言请求的 Target 为空、远端 Pool.For("") 计数为 0、服务端真实创建 task 且返回 BaseCommit；自机配置别名 local 也必须得到空 target。这里必须测试实际 HTTP，而不是只替换 Transport 返回 task。

3. 在 internal/ledgerstep/runner_test.go 或允许的新本机测试中，使用真实 httptest agentd 的 /api/tasks/{id}/events 与 diff 返回，调用 StepRunner 的生产 Run 路径；断言 awaitNode 能用空 target 取到 completed final_text 并调用 Done，diffNode 能用空 target 得到 ChangedPaths；WS 仍走 client.WaitEvent，ctx 取消不留下 goroutine。复用现有 runner fixture 只做决策锁，以上三条真实 client 断言不可省略。

4. 在 cmd/target_client_test.go 增加 TestTargetClientEmptyAndConfiguredSelf：配置含 listen 127.0.0.1:port 和 local addr http://127.0.0.1:port，真实 httptest 服务检查 targetClient("") 与 targetClient("local") 都能 Status；断言没有未指定目标机错误，没有 pool/relay 连接。再调用 targetClient("本机")，断言返回未登记且包含原名。对于所有 JSON 断言，检查 wire 中 target 键存在且空值与缺键可区分。

5. 序列化边界清单与断言必须落在测试文件：

   - YAML config.Target.Addr → config.IsSelfTarget：使用带 http scheme 的 Addr 和 relay 字段矩阵；
   - CardStepReq.Target wire → server resolveStepDiscipline → StepRunner.Target：真实 HTTP 请求断言空串；
   - TemplateDispatch/Dispatcher → DispatchOpts.Target → agentd task JSON：真实 target 字符串空值存在；
   - Dispatcher → LinkTask 与 dispatched snapshot：真实 ledger 查询断言 target 为空；
   - WorkBranch JSON 缺 target → Go 空串 → LocalBaseBranch：缺键样本必须按本机尝试本地 ref，不得被“信息缺失”分支拒掉；
   - mirror task target projection：Task mirror 不写本机 mirror_tasks，远端 target 仍保留原名。

   每条边界至少有一条穿过真实 marshal/HTTP/ledger 写入的断言，禁止仅比较内存 struct。无需新增枚举值；现有 mirrorSkip 与 WS disconnect 的 switch/类型保持原白名单。

### 4.7 回归、日志、注释与 Task 3 验收

1. 运行既有 internal/agentd/ws_regression_round2_test.go 中的 TestWSOutOfOrderPublishNotDropped，以及 remote mirror/ledger mirror 全部命名回归。它必须继续证明同 seq 晚到事件断开 WS；实现者不能通过放宽 server 判定来“修复”本卡。
2. 生产新增/修改函数头写参数、返回、client 所有权、空值语义和本机/远端边界；Dispatcher、Server client 路由、CLI canonical 归一、WorkBranch 历史快照原因写注释。关键点日志至少覆盖 target 输入、canonical target、Status/Dispatch/WaitEvent/Diff 前后、LinkTask/快照写入以及每条错误。
3. Task 3 触及包最小验收命令：

~~~
go test ./internal/ledgerstep -run 'TestViaTemplate|TestStepRunner|TestLocal' -count=1
go test ./internal/agentd -run 'Test(CardStep|Mirror|LocalStep|WSOutOfOrderPublishNotDropped)' -count=1
go test ./cmd -run 'Test(TargetEndpoint|BareDispatch|CardDispatch|TargetClient|NamedTarget)' -count=1
~~~

行为通过标准是：空 target 成功派发且快照/挂账显式保存空串；配置自机名归一空串；同机 WorkBranch 连续续接放行；真远端仍原样拒发；本机 Status/Dispatch/WaitEvent/Diff 均走本机 client；未知名称与 本机仍拒；task mirror 不订自机；ledger mirror 的本机源与远端 WS 结果均保留。

## 5. 五项计划自审与实现者收口

### 5.1 缺陷族对抗审查

| 缺陷族 | 本计划结论与锁点 |
|---|---|
| 生命周期/状态机中断 | 本机源以 ctx + task-scoped 门铃退出，cancel 不关闭共享 channel；ledger mirror Stop 先于账本 Close；任务镜像 Stop 仍等待 goroutine；WS/WaitEvent 与状态机不改。锁点是 LocalSource 取消测试、既有 mirror Stop 与 WS 回归。 |
| 静默失败/误导报错 | 空 target 不能被“目标机未定”误报；自机别名不能落成远端；未知名称仍用未登记原文；本机 Status 拒发闸失败保留 cause；每条错误分支有 target/task/node 上下文。 |
| 跨平台假设 | 地址判定使用 net/url、net.SplitHostPort、net.ParseIP 和 ClassifyListen，测试 IPv4、IPv6、localhost、通配、主机名、scheme、relay；不假设当前 OS 的 hostname 或解析结果。 |
| 假红测试 | 红绿命令在每个 task 实现前先跑；关键锁从真实 Store、HTTP、WS、ledger JSON 进入；不以 sleep 代替门铃/ctx，使用已有 harness 与可判定计数/事件内容。 |
| 门禁绕过 | 本机也走 discipline ResolveDispatch 与 Status；Dispatcher 仍在 Transport 前做 WorkBranch gate；LocalBaseBranch 保持真实本地 ref 安全闸；不以内部 fake source 顶替 Mirror.reconcile 接缝。 |
| 序列化边界 | Task 3 的六处 config/request/dispatch/link/snapshot/projection 边界各列出真实断言；target 空值和 target 缺键分别测试；Task 2 的 source_seq/source_target 原样断言。 |
| 新增枚举值白名单 | 本卡不引入 EventType、TaskState 或新 switch 值；mirrorSkip、Archived、WS 乱序判定只复用既有值并由回归测试锁住。 |
| webview / 平台表现差异 | 本卡无前端页面和浏览器 API，不触及 webview；因此没有浏览器平台行为需要外推。 |

### 5.2 上下文预算、接缝双向覆盖与故事归属

文件集合已经按 Task 1（config/store）、Task 2（ledger mirror/task mirror/agentd 接线）、Task 3（ledgerstep/agentd 节点/cmd/skill）圈定；每个 task 的生产文件、测试文件和允许新增文件均显式列出。没有需要新增竖切卡的无界域；internal/agentd 的大包只读其 mirror、server、cardstep 相关窗口。

接缝 → 测试：

| 声明接缝 | 锁它的测试入口 |
|---|---|
| Dispatcher.ViaTemplate ← StepRunner.dispatchNode / 裸模板派发 | TestViaTemplateEmptyTargetIsLocal、TestViaTemplateSelfAliasContinuesLocalWorkBranch、既有远端 ViaTemplate/裸派发命名测试 |
| IsSelfTarget ← task mirror / ledger mirror reconcile / WorkBranch gate | TestIsSelfTarget 矩阵、TestMirrorDiscoverOnceSkipsSelfTarget、TestMirrorLocalTargetUsesLocalSource、TestViaTemplateSelfAliasContinuesLocalWorkBranch |
| LocalSource ← Mirror.subscribe | TestMirrorLocalTargetUsesLocalSource；真实 Store 32+progress 后 permission_request、source_seq、过滤、幂等、For=0、Hub Watchers=0 |
| task Mirror.discoverOnce | TestMirrorDiscoverOnceSkipsSelfTarget；local 无 ListTasks/WS/mirror_tasks，remote 仍发现/订阅 |
| local client ← stepTransport/awaitNode/diffNode/resolveStepDiscipline/CLI targetClient | TestLocalStepDisciplineProbesStatus、TestLocalStepTransportUsesLocalClient、StepRunner 本机真实 WaitEvent/Diff、TestTargetClientEmptyAndConfiguredSelf |
| WS duplicate/late seq regression | TestWSOutOfOrderPublishNotDropped；二次同 seq Publish 仍断开 |

测试 → 接缝：

- TestIsSelfTarget 是附加内部锁，理由是单一高层接缝无法逐项构造 scheme/IPv6/通配/解析失败矩阵；它不能替代上表三条生产调用方。
- store TestEventDoorbell 是附加内部锁，理由是只从 Store 声明缝无法证明 Mirror 选择了 LocalSource；端到端本机源测试仍从 Mirror.reconcile 进入。
- 其余列出的测试入口均直接调用表中声明符号或进入其生产调用链；不把纯 helper 测试标成接缝。

用户故事逐条归属：故事 1 由 Task 2 的本机源、Task 3 的 ViaTemplate/stepTransport/awaitNode 与 CLI wait 端点验收覆盖；故事 2 由 Task 1 自机矩阵、Task 2 两镜像筛选、Task 3 CLI canonical 及 unknown name 验收覆盖；故事 3 由 Task 3 WorkBranch alias/empty 续接覆盖；故事 4 由 Task 2 远端 mirror 既有回归及 Task 3 remote negative 覆盖；故事 5 由 TestTargetClientEmptyAndConfiguredSelf 的本机名负例覆盖；故事 6 由 Task 2 mirrorSkip 真实事件类型断言和 Task 3 skill 文案覆盖。

### 5.3 占位、变更边界与最终提交

本计划所有实现步骤都给出具体文件、接口签名、代码块或既有 harness 的逐条断言；测试复用既有 fixture 的地方明确写明文件名和每条 pass/fail 断言。实现者不得把未列出的 fallback 改成另一个入口，不得新增 HTTP 路径、状态机事件、from_seq 语义、Hub Watchers 语义或外部 skill 修改。

计划节点收口顺序：

1. 写完本文件后运行 git diff --check、文件存在性检查、结构段落检查和无占位词扫描；命令的退出码与原始输出逐条追加 b271-ledger.md。
2. 复读本文件的 Interfaces、每个 task 的基线命令、红绿命令、日志/注释要求、缺陷族表和双向接缝表；确认只描述实现步骤，没有写入实现代码。
3. 将本计划与台账一起 git add，并在当前分支创建一个不 push 的提交；最后报告实际分支和提交 hash。全量测试由协调者在集成阶段执行。
