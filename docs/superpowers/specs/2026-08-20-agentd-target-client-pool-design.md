# agentd 侧 target client 池：让扇出认识 relay 机器

日期：2026-08-20
分支：claude/linux-01-relay-display-issue-b42c03

## 1. 问题

桌面控制台把 relay 形态的开发机一律显示成「已断开」，而同一台机器在
handoff-server 端显示中继中，CLI 的 `dispatch` / `wait` 也一切正常。

界面上的错误原文是：

```
状态查询请求: 对端 agentd 够不着: GET /api/status: Get "http:/api/status": http: no Host in request URL
```

### 1.1 根因

relay 形态的 target 没有 `addr`（`config.Target.Validate()` 明写 relay 与 addr
互斥）：

```yaml
linux-01:
    relay: "wss://relay.chanliu.net/relay"
    node: "linux-01"
```

而 agentd 侧的探活写死了直连构造（[internal/agentd/machines.go:117](../../../internal/agentd/machines.go)）：

```go
st, err := client.New(t.Addr, t.Token).Status(ctx)
```

`t.Addr` 是空串 → `client.New("")` 里 `strings.Contains("", "://")` 为 false →
补成 `"http://"` → `strings.TrimRight(addr, "/")` 把两个斜杠一起削掉 → baseURL
退化成 `"http:"` → 请求 URL 成了 `http:/api/status`，没有 Host。

已在 `internal/client` 里跑复现用例确认，错误字符串与界面逐字相同：

```
状态查询请求: 对端 agentd 够不着: GET /api/status: Get "http:/api/status": http: no Host in request URL
```

**所以「已断开」不是探测结论，是请求压根没发出去。**

### 1.2 影响面

CLI 正常，是因为它走 [cmd/root.go:246](../../../cmd/root.go) 的
`newTargetClientNamed`，那里有 `t.IsRelay()` 分支。agentd 侧一个都没有——
六处对 target 的扇出全是直连构造，对 relay 机器全部失效：

| 位置 | 功能 | 现表现 |
|---|---|---|
| `machines.go:117` | 探活 | 已断开 |
| `machines.go:195` | 新增机器的可达性探测 | 控制台加不了 relay 机器 |
| `mirror.go:155/210/269` | 任务镜像与事件流 | relay 机器的任务不进控制台列表 |
| `projectfanout.go:63` | 项目树 | 「项目目录数 0」 |
| `pty_api.go:196` | PTY 会话 | 拉不到 |
| `machineupgrade.go:47/142` | 远程升级 | 对 relay 机不可用 |

控制台右侧「执行纪律 / Env 文件 / 缺省执行者」三块的黄条是同一个
`machine.error` 透出来的，不是三个独立故障。

### 1.3 根因的形状

不是「agentd 忘了写一个 if」，是**同一个判据存在两份，其中一份从来没被写出来**。
只在六个调用点各补一个 `IsRelay()` 分支，等于把这个结构原样留给下一个人。

## 2. 范围

**做**：新建 `internal/targetclient` 包（一次性工厂 + 常驻池），六个调用点全部
改走池，`cmd/root.go` 的选路重构到同一个工厂，Mirror 换活快照，`proto.Machine`
加 `Relay` 字段，防回归的源码扫描测试。

**不做**：让控制台支持**新增** relay 机器（`proto.AddMachineReq` 没有 relay
字段，那是独立的一张单）；改 `client.New` 的签名；relay 协议本身的任何改动。

## 3. 决策记录

以下六条是承重决定，附被否掉的选项与理由。

### 3.1 选路逻辑收进独立包，而不是 agentd 私有

被否的选项是「agentd 内部加一个私有 clientPool，`cmd/root.go` 那份不动」。
改动面确实更小，但那样选路判据仍是两份：今天它们一致，明天 relay 加个字段
（SNI、代理、重连参数）就是两处改、漏一处——**与本次 bug 同款成因，只是换了
个位置复发**。

也否掉了「把选路做进 `client.NewForTarget(t config.Target)`」：那要让
`internal/client` 反向依赖 `internal/config`，把干净的传输层绑死在 handoff 的
配置形态上。

依赖图已确认无环：`config` 只依赖 `proxycfg`，`client` 只依赖 `proto` 与
`relay`，新包同时依赖两者是安全的。

### 3.2 池持活配置快照，Mirror 一并修

`cmd/agentd.go:232` 的 `NewMirror(cfg, ...)` 拿的是**启动时的静态 cfg**，控制台
运行期新增的机器要重启才会被镜像。

池持一个 `func() *config.Config`（即 `s.conf`），并把「有哪些机器」也收成
`Pool.Names()`。Mirror 改用池之后活快照是**自然结果**，不是额外补丁——现在
六个调用点各写一遍 `for name := range s.conf().Targets`，收拢之后这个判据也
只剩一份。

### 3.3 隧道预热，不占探活预算

relay 隧道首次建立要 WSS 拨号 + CONNECT 控制帧 + E2E 握手 + yamux，而
`machineProbeBudget` 只有 3s。纯惰性建立会让控制台冷启动先闪一次「已断开」，
且机器真掉线时每轮探活都重跑一次完整拨号、耗光预算。

选：agentd 启动后跑 `Pool.Warm` 循环，用独立超时主动建隧道，失败按 1s→60s
指数退避。被否的选项是「惰性 + 给 UI 加『隧道建立中』中间态」——那要动
`proto.Machine` 与前端渲染，为一个几秒的瞬态付前端的账不值。

**预热只保证隧道，不代表可达。** 机器是否在线仍由 `/api/status` 说了算：
隧道通了但对端 agentd 没起，照样是「已断开」。两个判据不合并。

### 3.4 防回归用源码扫描，不用 relay 假件集成测试

这个 bug 不会让任何测试变红——直连机器一切正常。下一个人新增第七处扇出时
照样可能直接写 `client.New`。

选：一条测试扫 `internal/agentd/*.go`，除池自身外出现 `client.New(` 即变红，
错误文案直接指向池。便宜、确定、不依赖 relay 真环境。被否的选项是「起假
relay 服务端 + 假对端 agentd 跑真实扇出」：罩得真，但新增第七处时**仍然不会
自动变红**——而那正是要防的失败。

代价是字符串扫描略粗糙，需要给池文件开白名单；接受。

### 3.5 `proto.Machine` 加 `Relay` 字段

relay 机器的 `Addr` 恒为空，控制台卡片因此没有任何身份标识（截图里 linux-01
只有名字，本机与 mac-02 都有 `ip:port`）。加 `Relay string`（relay 节点名），
前端在 `Addr` 为空时显示「中继 · <node>」。

### 3.6 堵掉 `client.New("")` 的静默退化，但不改签名

`client.New("")` 退化成 `baseURL = "http:"` 是把「配置缺失」翻译成「网络错误」
的元凶——它让一个配置问题伪装成三个子系统的连接故障，排查要从错误文案一路
倒推到字符串裁剪。

改法：`Client` 加 `initErr` 字段，构造时地址无 host 就毒化，每个请求入口先查
并返回明确错误。**不改 `New` 的签名**——加返回值要波及二十多个调用点，收益
只是把同一个错误提早半步。

## 4. 设计

### 4.1 包接口

`internal/targetclient`：

```go
// New 按 Target 形态选路，返回一次性客户端与清理函数。CLI 用。
func New(name string, t config.Target, log *slog.Logger) (*client.Client, func(), error)

// Pool 是 agentd 常驻侧的复用池：一台机器一条隧道，全子系统共用。
type Pool struct{ ... }
func NewPool(conf func() *config.Config, log *slog.Logger) *Pool
func (p *Pool) For(name string) (*client.Client, error) // 调用方**不**负责 Close
func (p *Pool) Names() []string                          // 当前配置里的 target 名，已排序
func (p *Pool) Warm(ctx context.Context)                 // 预热循环，阻塞至 ctx 取消
func (p *Pool) Close() error
```

选路只有一处：`t.IsRelay()` → `relay.CheckTokenEntropy` 前置 +
`relay.NewDialer` + `client.NewRelay`；否则 `client.New("http://"+t.Addr, ...)`。

`cmd/root.go:newTargetClientNamed` 重构成 `targetclient.New` 的薄壳，只保留
自己的配置加载与 `--target` 语义。

**直连 target 也进池**：不只为统一写法——现在每轮扇出都 `client.New` 造一个
全新 `http.Transport`，连接池建了就扔。池化顺带拿到连接复用。

### 4.2 生命周期

**缓存与失效**：key 是 target 名，entry 存构造时的 `config.Target` 值。
`config.Target` 全是 string 字段（可比较），`For(name)` 每次与当前快照 `!=`
比一下：不等就 Close 旧 Dialer 重建。改 token、换 relay 节点、直连转 relay
全靠这一条覆盖，不为每种变更写分支。target 被删则 Close 并移出池。

**预热**：给 `relay.Dialer` 加导出方法 `Ensure(ctx) error`（内部就是现成的
`ensureTunnel`，薄壳一层）。`Warm` 每 30s 扫一轮（与 `mirrorDiscoveryTick` 同
量级）对所有 relay target 调 `Ensure`，用独立超时。单台失败按 1s→60s 指数
退避**各算各的**：一台长期离线的机器不能把其余机器的重试节奏一起拖慢。新加的
机器由下一轮扫到。

`For(name)` 对配置里不存在的名字返回错误——池不为未登记的机器造 client。

预热**不借业务请求代劳**：借一次 `Status` 会把「隧道通没通」与「对端活没活」
两个判据搅在一起，正是 3.3 要分开的东西。

**关停**：`cmd/agentd.go` 退出路径调 `p.Close()`。`Dialer.Close` 是终态
（`closed` 标志阻止重连），符合进程退出语义。

### 4.3 六个调用点

统一形态：`t := s.conf().Targets[name]` + `client.New(t.Addr, t.Token)` →
`c, err := s.pool.For(name)`；枚举改用 `s.pool.Names()`。`MarkForwarded()` /
`NoRedirect()` 照旧链式调用——它们共享 `hc`（`wsDialOptions` 也交出了 `c.hc`），
relay 透传成立，**client 层不动**。

- `machines.go:117` 探活：顺带填 `proto.Machine.Relay`
- `machines.go:195` 新增机器探测：改走工厂只为一件事——空 addr 被明确拒绝，
  不再产出 `http:/api/status`。控制台新增 relay 机器仍不支持（见 §2）
- `mirror.go:155/210/269`：见 §4.4
- `projectfanout.go:63`、`pty_api.go:196`：直接替换
- `machineupgrade.go:47/142`：`upgrade.RemoteOne` 收的是 `Peer` 接口，换 client
  即可。**推 tar.gz 大包走 yamux 流没验过**，列为必须真机验收项（§6）

### 4.4 Mirror 顺带瘦身

`t config.Target` 现在被穿过 `subscribe` → `onEvent` → `refreshSnapshot` 三层，
而 `onEvent` 根本没用它——它存在的唯一理由就是末端要造 client。改走池后这个
参数从三个签名里删掉。

`cmd/agentd.go:231` 的 `if len(cfg.Targets) > 0` 启动闸要去掉：留着的话启动时
没有机器就永远不镜像，与活快照直接矛盾。

### 4.5 错误语义

- `targetclient.New` 对「既无 addr 又无 relay」的 target 返回带 target 名的
  `ErrNoEndpoint`。这条不变式 `config.Target.Validate()` 早就写着，只是扇出侧
  从没问过它
- relay 机器探活失败时展示 relay 拨号错误原文（节点离线 / CONNECT 被拒 /
  凭证不对）。沿用既有纪律：**够不着只报原文，不编处置建议**

## 5. 测试策略

- **守卫**：扫 `internal/agentd/*.go`，除池自身外出现 `client.New(` 即变红
- **选路单测**：relay 型 Target → 拿到 relay-backed client（`baseURL` 为
  `http://localhost`）；直连型 → `http://<addr>`；两者皆无 → `ErrNoEndpoint`
- **池语义单测**：同名两次 `For` 复用同一 Dialer；改 token 后 `For` 重建并
  Close 旧的；删 target 后移出；`Names()` 跟随活快照变化
- **毒化单测**：`client.New("")` 的任一请求返回明确的构造错误，且**不含**
  `no Host in request URL`
- **Mirror 回归**：新增 target 后无需重启即被 `Names()` 枚举到

写测试前先在基线上跑一遍判据（既有纪律：判据错的代价比实现错大）。

## 6. 真机验收

以下几项自动化测试罩不住，必须对 linux-01 实测：

1. 控制台机器列表：linux-01 显示已连接、版本、执行者、项目目录数非 0
2. 卡片显示「中继 · linux-01」
3. relay 机器上派一个任务，事件在控制台实时可见（镜像走通）
4. 项目树、PTY 会话列表能拉到 linux-01 的内容
5. **relay 机器的远程升级**（推包走 yamux）——本次唯一没有先例的路径
6. agentd 冷启动后首轮列表不出现「已断开」闪烁

第 6 项与 agentd 自身的启停有关，**由审核者本地执行，不派发**（派发的纪律块
禁止执行者驱动 handoff CLI 与起 agentd 进程）。
