# agentd 单网卡监听的 loopback 辅助监听（B85）

> **定位**：让 `listen` 绑单网卡 IP 成为实际可用的**安全第三档**——只把 agentd 暴露给
> 一块网卡（如虚拟组网网段），同时本机 CLI 不再随那块网卡的状态起伏。
>
> **分支**：待 plan 阶段建（建议 `handoff/loopback-aux-listen`，基于 `main`）。
>
> **来源**：08-13 用户提出（backlog B85）。README「连接远程执行机」现状明确写着
> 「不要绑定单个内网/虚拟网卡 IP」，本文把这条不建议变成可建议。

---

## 1. 病灶

`listen` 名义上任填，实际只有两档可用：

- `127.0.0.1:7777`：仅本机；
- `0.0.0.0:7777`：全网卡，安全面最大（README:104 的公网红线段因此存在）。

绑单个网卡 IP（如 Tailscale 的 `100.x.y.z:7777`）时，坏在两处：

### 1.1 本机 CLI 随网卡状态起伏（本文要修的）

agentd 只有一个监听（[shutdown.go:106](../../../internal/agentd/shutdown.go:106) 单
`net.Listen`，[cmd/agentd.go:193](../../../cmd/agentd.go:193) 只传一个 `cfg.Listen`），
而 CLI 本机模式直接拿 `cfg.Listen` 当拨号地址
（[cmd/root.go:181](../../../cmd/root.go:181)，`Endpoints` 的本机行同理
[cmd/root.go:133](../../../cmd/root.go:133)）。组网工具掉线、IP 从网卡上消失的瞬间，
`handoff status` 等一切本机命令连接拒绝——协调者回路在自己机器上瘫掉。

`init` 的注释早就记录了这条（[cmd/init.go:311](../../../cmd/init.go:311)
askListen：「探到的网卡 IP 绝不写进 listen——绑到某一张网卡会让 127.0.0.1 连不上」）。

### 1.2 网卡 IP 不在时 agentd 起不来（本文不修，见 §6）

开机早于组网工具、或组网工具掉线期间重启，`net.Listen` 对不存在的 IP 直接失败，
托管形态（launchd/systemd `Restart=always`）变成崩溃循环，直到 IP 回来。

### 1.3 为什么「CLI 回退 127.0.0.1」单独做不成立

最初设想的另一个候选是只改 CLI：`cfg.Listen` 连不上就回退拨 `127.0.0.1`。但 agentd
只绑了网卡 IP 时，loopback 上根本没人监听，回退过去还是连接拒绝。**辅助监听是承重
件，CLI 侧只是配套的地址决议**——这是方案取舍的第一条结论。

---

## 2. 方案总览

三件事，合起来构成第三档：

1. **共享判定**：一个函数把 `listen` 的 host 归类，CLI 与 agentd 启动共用同一口径；
2. **agentd 双监听**：host 为单网卡 IP/主机名时，追加绑 `127.0.0.1:同端口`，任一
   监听绑不上即启动失败；
3. **CLI 确定性改写**：本机模式下 host 非 loopback 时一律改拨 `127.0.0.1:同端口`，
   不做先试后退。

`advertiseAddr`（init 配对片段）继续广告网卡 IP，不动——远程协调者要连的本来就是
那块网卡。

---

## 3. 详细设计

### 3.1 共享判定（`internal/config`）

新增一个纯函数（命名由 plan 定，语义如下），解析 `listen` 的 host，三分类：

| host | 归类 | agentd 辅助监听 | CLI 本机改写 |
|------|------|----------------|-------------|
| `127.0.0.1` / `::1` / `localhost` | loopback | 无 | 不改写（现状） |
| `0.0.0.0` / `::` / 空 host | 通配 | 无（通配已含 loopback） | 改写为 `127.0.0.1:同端口` |
| 其余（单 IP 或主机名） | 单点 | `127.0.0.1:同端口` | 改写为 `127.0.0.1:同端口` |

放 `internal/config` 的理由：`cmd`（CLI 决议）与 `cmd/agentd.go`（监听列表）都要用，
而两处口径一旦漂移，就会出现「CLI 改写了、agentd 没绑」的连接拒绝，比现状更迷惑。
判定必须唯一。

注意与 [cmd/init.go:355](../../../cmd/init.go:355) `listenKind` 的关系：那是 init
交互的**预选**口径（连端口都参与归类，`0.0.0.0:7788` 归手填），语义不同，不合并，
各自留在原地。

解析失败（`net.SplitHostPort` 报错）归 loopback 档（即什么都不做）：错的 listen
让 `net.Listen` 自己去报，判定函数不抢这个错误。

### 3.2 agentd 双监听（`internal/agentd/shutdown.go` + `cmd/agentd.go`）

- `cmd/agentd.go` 启动时按 §3.1 算出监听地址列表（1 或 2 个）传给 `Shutdown.Serve`；
- `Serve` 签名改为接受多地址：逐个 `net.Listen`，**任一失败即返回错误**（→ exit 1），
  报文带上具体是哪个地址；
- 每个 listener 各起一个 `srv.Serve(ln)` goroutine，共享同一个 `http.Server`。
  `http.Server` 自己追踪所有经 `Serve` 注入的 listener，现有的 `srv.Shutdown` 会把
  它们全部关掉——**优雅关停逻辑零改动**，任一 goroutine 的非 `ErrServerClosed`
  错误仍走现有 errCh 路径；
- `serveWithListener` 的可测形态保留（多 listener 版本的测试 seam 同理）。

**锁语义（不动）**：data_dir 文件锁照旧管同目录单实例；跨 data_dir 的两个 agentd
靠「端口任一绑不上就 fail-fast」兜住——辅助监听 `127.0.0.1:7777` 被占几乎必然是
另一个 agentd 或配置冲突，静默继续会让本机 CLI 打到别的进程上，恰好制造本功能要
消灭的迷惑状态。

**日志**：`agentd 服务启动` 一行（[cmd/agentd.go:177](../../../cmd/agentd.go:177)）
追加 `listen_aux` 字段（无辅助监听时不打）；每个 listener 建立成功各留一条 Debug。

### 3.3 CLI 确定性改写（`cmd/root.go`）

`TargetEndpoint` 与 `Endpoints` 的本机行：host 非 loopback（通配或单点）时，拨号
地址改写为 `http://127.0.0.1:同端口`。

- 单点档靠 §3.2 的辅助监听兜底；通配档本来拨 `0.0.0.0` 能通只是协议栈宽容，改写
  后语义干净，顺手收掉；
- 显式 `--agentd` **不改写**——用户指明了端点就照拨（现有优先级语义不变，
  [cmd/root.go:178](../../../cmd/root.go:178)）;
- `--target` 路径完全不涉及（远程配对地址本来就该是网卡 IP）。

**已知代价（接受，不做处理）**：新 CLI + 旧 agentd（无辅助监听）且 listen 为单点时，
本机命令连接拒绝，升级 agentd 即愈。窗口窄（`handoff upgrade` 全链升级），不加
专门探测或提示。

### 3.4 status 语义（`internal/agentd/status.go` + proto）

- `StatusResp.Listen` 保持 `cfg.Listen` 不变——它是身份/配对语义，消费方（含
  `advertiseAddr` 的心智模型）不该看到它变成列表；
- 新增 `ListenAux string`（`omitempty`），有辅助监听时填 `127.0.0.1:同端口`；
- `handoff status` 呈现时带出辅址（形如 `listen 100.x.y.z:7777（辅 127.0.0.1:7777）`，
  具体文案 plan 定）。

---

## 4. 决策记录

| 决策点 | 拍板 | 理由 |
|--------|------|------|
| 范围：要不要连 §1.2（IP 不在时起不来）一起修 | **只做辅助监听** | 降级为仅 loopback + 后台重试绑网卡的复杂度（重试循环、降级态 status、恢复语义）远超本次收益；掉线期间重启靠进程管理器反复拉起等 IP 回来，维持现状 |
| 辅助监听绑定失败 | **启动失败（fail-fast）** | 与主监听同等对待，「第三档 = 两个监听都在」恒成立；loopback 被占几乎必然是另一个 agentd/配置冲突，告警后继续会造出「CLI 有时通有时打到别人」的状态 |
| CLI 地址决议 | **确定性改写，不做先试后退** | 辅助监听是硬保证（绑不上 agentd 根本起不来），改写无双拨延迟、无只在故障时才走到的回退分支；代价是版本偏斜窗口内本机命令拒绝（§3.3，接受） |

---

## 5. 文档与测试

### 5.1 文档

- README「连接远程执行机」（README:102 起）：从「两档可用、单网卡不建议」改写为
  三档——`127.0.0.1` 仅本机 / 单网卡 IP 只暴露一块网卡（新档，说明辅助监听保证本机
  命令始终走 loopback）/ `0.0.0.0` 全网卡；保留公网红线段；补一句 §1.2 的已知限制
  （IP 不在时 agentd 起不来，靠托管重启自愈）；
- 配置示例注释（README:186）同步；
- [cmd/init.go:311](../../../cmd/init.go:311) askListen 注释更新：「绑单网卡会让
  127.0.0.1 连不上」已不成立，删前半条；「DHCP/Tailscale 一变 agentd 也起不来」
  仍成立，保留并指向本 spec；手填档旁注可提示新档语义（plan 定）。

### 5.2 测试

- 判定函数表驱动单测：loopback（v4/v6/localhost）、通配（v4/v6/空 host）、单点
  （v4/v6 带方括号/主机名）、坏输入（无端口、纯乱码）；
- `serveWithListener` 多 listener 单测：双随机端口都应答；`Trigger` 后两个端口同时
  停收；单 listener 路径回归不变；
- `TargetEndpoint`/`Endpoints` 改写用例进 [cmd/root_test.go](../../../cmd/root_test.go)：
  单点改写、通配改写、loopback 不动、`--agentd` 显式不改写；
- 真机验收：listen 绑本机真实网卡 IP 启动 agentd → 启动日志见 `listen_aux`；
  `handoff status` 不带 `--agentd` 走 loopback 通，`status` 输出带辅址；从另一台机
  经网卡 IP 照常可达；`127.0.0.1:7777` 先被占位进程占住再启动 → 启动失败且报文指明
  辅助监听地址。

---

## 6. 非目标

- **§1.2 的启动降级/重试**：主监听绑不上仍启动失败（决策见 §4），要做另立条目；
- **TLS / 公网暴露**：README 红线不变，云中转是另案；
- **`advertiseAddr` / 配对片段**：继续广告 `cfg.Listen` 的网卡 IP；
- **`listenKind`（init 预选）**：口径不合并，init 交互不改结构（仅文案微调）。
