# 代理配置项 + 更新分发反转为执行机自拉（B87）

> **定位**：给 handoff 自己的出网加一个配置项（`proxy`，支持 http/https/socks5/socks5h），
> 并把换版的分发方向从「协调者下载后推 20MB 给执行机」反转为「协调者只下发 tag+sha256，
> 执行机自己去下」。
>
> **分支**：待 plan 阶段建（建议 `handoff/proxy-and-pull-update`，基于 `main`）。
>
> **来源**：08-13 用户反馈「配置项里加代理地址，主要在更新场景用」。追问范围时用户追加了
> 第二件事：「我承诺了马上要支持云服务器中转连接，服务那些没有 tailscale 的用户，
> 还是推送版本过去的话，流量消耗太大了」——于是本文合成一份 spec 覆盖两件事，
> 因为**执行机自拉的前提就是执行机上有代理配置可用**。

---

## 1. 病灶

### 1.1 代理只能靠环境变量，而 agentd 是托管进程

`internal/release` 的两个 http.Client（`client.go:119` 查 GitHub API、`install.go:75` 下载资产）
都用默认 Transport，也就是**已经认 `HTTPS_PROXY`/`HTTP_PROXY`/`NO_PROXY` 了**。
所以本需求的真实缺口不是「不支持代理」，而是「只能靠环境变量配代理」：

- agentd 由 launchd / systemd 拉起，**读不到操作者终端的 shell env**。要给它配代理，
  得去改 service 单元文件（`~/Library/LaunchAgents/*.plist` 或 systemd unit），
  改完还要 reload——而这台机器上明明已经有一份 `~/.handoff/config.yaml` 了。
- 国内网络下拉 GitHub release 是这条链路最常见的失败点，而失败时的表现是**静默**：
  `maybeNotifyUpdate` 的纪律是「任何一步失败都静默跳过」（`cmd/root.go:281`），
  于是后台更新检查永远失败、永远没人知道。

### 1.2 推送模式在中转场景下流量不可接受

B59 的设计（`2026-08-11-update-and-skill-delivery-design.md` §4.2 / D1）是：
协调者下载各机平台的资产 → `POST /api/update` 把 tar.gz 原文推给远端 → 执行机**无需出网**。

这在 Tailscale 直连下是好设计。但用户即将支持**云服务器中转**，服务没有 Tailscale 的用户：
每台执行机每次换版都要有 ~20MB 二进制**穿过中转服务器**（上行一遍、下行一遍），
成本按机器数与换版频次线性放大。而执行机自己去 GitHub 下这 20MB，中转链路上只走
几十字节的 tag + sha256。

### 1.3 两件事是依赖关系，不是两件独立的事

执行机自拉的前提是执行机能出网到 GitHub。而「执行机能出网」在国内恰恰经常等价于
「执行机上得配代理」——`proxy` 配置项正是为它提供的。反过来，`proxy` 单独做也成立
（协调者本机拉 GitHub 就要用），所以两者的关系是「§2 是 §3 的前置」，而非互为条件。

---

## 2. 方案总览（proxy 配置项）

新增顶层配置项，空值 = 保持现行为：

```yaml
proxy: "socks5://127.0.0.1:1080"   # 空 = 沿用 HTTPS_PROXY 等环境变量
```

新增 `internal/proxycfg` 包，只干一件事：把配置字符串翻译成各消费方要的形态
（http Transport / git 参数 / 脱敏后的日志文本）。消费方三处：

| 消费方 | 位置 | 形态 |
|--------|------|------|
| 查最新版本 | `release.Client`——只有 CLI 用（`upgrade` 与 `update-check`）。agentd 不查 latest，它只按协调者下发的 tag 下载，见 §8 | `*http.Transport` |
| 下载资产 / checksums | `release.Installer`（CLI 侧 + agentd 自拉侧） | `*http.Transport` |
| git clone / fetch | `internal/agentd` 的两处出网 git | `-c http.proxy=<url>` |

**Go 1.26 的 `http.Transport` 原生支持 `socks5://` 与 `socks5h://`，git 的 `http.proxy`
同样认这两个 scheme——本设计不引入任何新依赖**（不需要 `golang.org/x/net/proxy`）。

### 2.1 边界：谁不走代理

| 链路 | 走不走代理 | 理由 |
|------|-----------|------|
| 协调者 ↔ agentd（`internal/client`） | **永不** | 目标是 LAN / Tailscale / loopback 地址。把它代理化，轻则每次请求多绕一跳，重则 socks5 代理解析不了 `100.x.y.z` 直接断链。这条链路的可达性是 handoff 的命根子，不给它加任何新的失败模式 |
| executor 的出网 | **不走** `proxy`，仍归 `env` 段 | 语义严格分开（用户 08-13 拍板）。`env` 段（B19）已经在做这件事且更强（可配任意变量、按 executor 分别配）。两者故障域不交叉：代理挂了只影响升级，不影响任务执行 |
| agentd 的本地 git 操作（rev-parse / status / worktree / diff…） | **不走** | 它们根本不出网。只有 `clone` 与 `fetch` 两处套代理，见 §3.3 |

---

## 3. 详细设计（proxy 配置项）

### 3.1 配置字段（`internal/config`）

```go
// Proxy 是 handoff **自身**出网时使用的代理地址，形如 http://host:port、
// https://host:port、socks5://host:port、socks5h://host:port。
// 空 = 不配，沿用 HTTPS_PROXY/HTTP_PROXY/NO_PROXY 环境变量（现行为不变）。
//
// 作用范围只有两处：更新链路的 HTTP 出网（查 release、下资产）与 agentd 的
// git clone/fetch。**不作用于协调者↔agentd 链路**（那是 LAN/loopback），
// 也**不作用于 executor**（executor 的出网归 env 段，见 README）。
//
// omitempty 是硬要求，不是风格：配置以 KnownFields(true) 严格解析，未知键让
// agentd 启动失败。没有 omitempty 时，新版 Save 会把 proxy: "" 写进每一台
// 机器的 config.yaml，而一台还没换版的旧 agentd 读到它就再也起不来了
//（与 PathDirs 同款教训）。
Proxy string `yaml:"proxy,omitempty"`
```

`validate()` 增加一段（**只在非空时生效**）：

- `url.Parse` 必须成功；
- scheme 必须 ∈ `{http, https, socks5, socks5h}`——其余（`socks4`、`ssh`、空 scheme、
  裸 `host:port`）一律拒绝并在错误文本里列出支持的四种；
- `Host` 非空。

**为什么放在启动期硬拒而不是运行时容错**：一个拼错的代理只会让后台更新检查静默失败
（`cmd/root.go:281` 的纪律是「任何一步失败都静默跳过」，那条路径挂在每条命令上，
不能成为故障源），于是错误配置可以存在数月而无人察觉。启动期报错只需改配置重启，
与 `approver.blacklist` 的正则在启动期编译校验是同一条纪律。

`decodeStrict` 的「支持的键」提示串补上 `proxy`。

### 3.2 `internal/proxycfg`（新包）

```go
// Package proxycfg 把 handoff 配置里的 proxy 字符串翻译成各消费方要的形态。
//
// 职责：
//   - Transport：给 net/http 用的 *http.Transport
//   - GitArgs：给 git 子进程用的 -c http.proxy=<url> 前缀参数
//   - Redact：日志用的脱敏文本
//
// 边界：
//   - 不读配置文件、不碰网络：调用方把字符串给它，它只做翻译
//   - 不判断代理是否可达（那是消费方在真发请求时才知道的事）
package proxycfg
```

| 函数 | 空串时 | 非空时 |
|------|--------|--------|
| `Transport(proxy string) (*http.Transport, error)` | 返回 `http.DefaultTransport.(*http.Transport).Clone()`——即 `ProxyFromEnvironment`，**现行为一字不变** | 克隆后把 `Proxy` 换成固定返回该 URL 的函数 |
| `GitArgs(proxy string) []string` | `nil` | `[]string{"-c", "http.proxy=" + proxy}` |
| `Redact(proxy string) string` | `""` | 有 userinfo 时替换成 `***`，如 `socks5://***@host:1080`；无 userinfo 原样返回 |

**为什么固定返回而不是保留 `ProxyFromEnvironment` 的 NO_PROXY 逻辑**：显式配置就是显式
意图，"我配了代理但某些域不走"这个需求在 handoff 里不存在——走代理的出网只有 GitHub
一个域（§2.1 已把 LAN 链路排除在外）。加一个 `no_proxy` 配置项是纯粹的 YAGNI。

**`Redact` 不是可选项**：代理 URL 常含 `user:pass@`。本仓已有明确纪律——
`internal/envfile/resolver.go:64` 的注释写着「日志只打 key 名，绝不打值：环境类变量里
`HTTPS_PROXY=http://user:pass@host`」。任何打印 proxy 的日志一律经 `Redact`。

### 3.3 接线

**`internal/release`——保持"不读配置"的包边界。** 该包的 package 注释明确写着
「不知道 agentd、不知道任务、**不读 handoff 的配置**」。所以不给它传配置字符串，
只传一个已经造好的 `http.RoundTripper`：

- `NewClient(tr http.RoundTripper) *Client`（nil = 现有默认）
- `NewInstaller(log *slog.Logger, tr http.RoundTripper) *Installer`（nil = 现有默认）

两个构造函数原有的 Timeout 语义不变（30s / 10min）。

调用点：

| 位置 | 改动 |
|------|------|
| `cmd/upgrade.go:68-69`（两个测试缝） | 从已加载的 cfg 造 transport 传入 |
| `cmd/root.go:265`（update-check 子命令） | 同上（该处已 `config.Load`） |
| agentd 自拉（§4 新增） | 从 `s.cfg.Proxy` 造 |

**git 侧**——只有两处出网：

| 位置 | 命令 |
|------|------|
| `internal/agentd/projectadmin.go:359` | `clone -- <originURL> <dest>` |
| `internal/agentd/workspace.go:789` | `fetch --all --prune` |

新增 `gitRunNet(ctx, repo, args...)`：等价于 `gitRun`，但在 `-C repo` **之前**插
`proxycfg.GitArgs(...)`（`git -c` 必须在子命令之前）。这两处改调 `gitRunNet`，
其余 git 调用点一律不动。

proxy 值经**包级变量**注入（agentd bootstrap 时设一次），与本包 `log()` 的运行时取值
同款。**为什么不显式传参**：`ResolveBaseline` 等函数是包级函数、且调用链上大部分环节
与网络无关，把 proxy 串进每个签名会污染一大片无关代码——这正是本仓在 `log()` 上
已经做过的同一个权衡。

### 3.4 SSH remote 吃不到代理（已知限制，写进文档）

`http.proxy` 只对 `http(s)://` 的 remote 生效，对 `ssh://` / `git@host:path` **无效**。
而本项目的自动登记（B62）默认取 origin 的地址，SSH remote 是真实存在的常见形态。

不修：让 SSH 走代理要配 ssh 的 `ProxyCommand`（改用户 `~/.ssh/config` 或给 git 注入
`GIT_SSH_COMMAND`），那是另一件事，且会动到用户的 ssh 配置面。README 明说这条限制，
并给出既有解法：把 origin 换成 HTTPS 地址（`insteadOf` 重写），HTTPS 的 clone/fetch
就吃得到 `proxy` 了。

---

## 4. 方案总览（更新分发反转）

现状（B59）：

```
协调者：查 latest → 下 tar.gz + checksums → POST /api/update（body=20MB）
agentd：读 body → 校验 → 解包 → 自检 → 换版 → 重启          [同步 handler]
```

改为：

```
协调者：查 latest → 下 checksums（几百字节，一个 release 只下一次）
        → 探对端能力 → POST /api/update?mode=pull&tag=&sha256=（body 空）
agentd：受理 → 202                                          [立即返回]
        后台：下 tar.gz（带 proxy） → 比对下发的 sha256 → 解包 → 自检 → 换版 → 重启
协调者：WaitVersion 轮询 status，顺带读 pull 进度/错误
```

`--push` 强制走老路。老 agentd 自动降级走老路（§5.1）。

---

## 5. 详细设计（更新分发反转）

### 5.1 能力探测：`UpdateStatus.Pull *bool`

**这一条是安全性关键，不是锦上添花。** 老 agentd 收到 `?mode=pull&tag=v0.2.3` + 空 body
会掉进现有的 `len(body) == 0` 分支（`internal/agentd/update.go:95`），当成**纯重启**处理，
返回 `{ok:true, restarted:true}` 而 `Version` 为空。CLI 侧看到 200 便以为受理了，
接着 `WaitVersion` 干等到超时，报一句「已换版但新进程未上线」——一次**纯粹的误导**，
而实际发生的事是"对端白重启了一次"。

因此 `proto.UpdateStatus` 增加：

```go
// Pull 表示对端支持「自拉换版」（POST /api/update?mode=pull）。
//
// 为什么是指针：nil 表示**对端没给这个字段**（老 agentd 不上报），与「对端说
// false」是两回事。用 bool 零值把前者塌成后者不会出错（都降级推送），但用
// 指针能让日志如实说出"对端过旧"而不是"对端不支持"——与同结构里 Managed *bool
// 是同一条纪律。
Pull *bool `json:"pull,omitempty"`
```

`machineState` 相应增加 `Pull *bool`，在 `probeMachine` 里从 status 填。
选路：`Pull != nil && *Pull && !upgradePush` → 自拉；否则推送。

### 5.2 协议：三种模式显式判别

`POST /api/update?tag=&sha256=&force=&mode=`

| mode | body | 语义 |
|------|------|------|
| `pull` | 空 | **新**：自拉换版。`tag` 与 `sha256` 必须成对给出，缺任一 → 400 |
| 省略 / `push` | 非空 | 推送换版（现有，行为一字不变） |
| 省略 | 空 | 纯重启（现有 D8，行为一字不变） |
| `push` | 空 | **400**。不塌成纯重启——调用方显式说了"我要推送"却没带字节，这是个 bug，静默当成重启会让它以为换版成功了 |
| `pull` | 非空 | **400**。同上，两种模式的意图互斥，不做"猜一个"的兼容 |

**为什么加显式 `mode` 而不是靠「tag 有没有」隐式判别**：现在的判别已经压在
「body 空不空」这一个维度上，再叠一层「tag 有没有」，三种模式的判据就散在两个维度上，
加第四种时必然出错。显式 `mode` 还让新旧分歧点变成一个可测的单点。

`mode` 取值非法（既不是 `pull` 也不是 `push`/空）→ 400，不静默当成某个默认模式。

### 5.3 agentd 侧：异步受理

`handleUpdate` 的 `mode=pull` 分支：

1. **两道闸复检**（不变，且顺序不变：闸二非托管在前、闸一活跃任务在后）；
2. 参数校验：`tag` 与 `sha256` 必须都非空；
3. **抢并发锁**：同一时刻只允许一个自拉在跑。已在跑 → `409` + 新 reason
   `proto.UpdateReasonPullInProgress`。**没有这道锁**，连点两次 `upgrade` 会有两个
   goroutine 往同一个临时文件路径（`release.TempName(tag)` 是确定性的）写，
   互相截断出一个坏二进制，而 `Activate` 会把它装上去；
4. 立即 `202` + `proto.UpdateResp{OK:true, Accepted:true, Version:tag}`；
5. 后台 goroutine：`Installer.FetchArchive`（本机平台，带 proxy）→ 用**下发的**
   `sha256` 比对 → `Install`（解包 + 自检）→ `Activate` → `triggerRestart`。

后台 goroutine 的 ctx **不能用 `r.Context()`**：handler 一返回它就被取消，下载会当场断。
用 agentd 的生命周期 ctx，叠一个下载总超时（取 `Installer` 现有的 10min）。

**为什么异步而不是同步等完**：同步方案里，下载那几分钟连接是完全 idle 的
（没有任何字节流动），而云中转 / 反向代理的空闲超时普遍是 60s——**中转恰恰是这个需求的
初衷**，把方案建在一条会被中转掐断的长连接上是自相矛盾。

### 5.4 状态回报：`UpdateStatus.PullState`

```go
// PullState 是最近一次自拉换版的状态。仅存内存，不落盘。
type PullState struct {
    Tag       string `json:"tag"`
    Stage     string `json:"stage"`            // downloading / verifying / installing / failed
    Error     string `json:"error,omitempty"`  // Stage=failed 时的原文
    StartedAt time.Time `json:"started_at"`
    UpdatedAt time.Time `json:"updated_at"`
}
```

**为什么只在内存、为什么没有 `done` 这个 Stage**：成功路径的终点是**进程重启**，
状态自然消失——而那时 `/api/status` 报的版本号已经变了，`WaitVersion` 靠版本号就能确认，
一个落盘的 `done` 状态既没人读也会在下次启动时变成误导性的陈旧数据。
**失败**时进程不重启，状态留在内存里可查，这正是需要它的场合。

已知代价：agentd 在下载途中被外力重启，状态丢失，CLI 会看到「pull 段没了且版本没变」
→ 报失败。这是正确结论（那次换版确实没成），接受。

### 5.5 信任链：sha256 由协调者下发

协调者下 `checksums.txt`，按各机平台取自己那行，随 tag 一起下发；agentd 下完资产
比对**这个** sum，而不是自己再去取一份 checksums。

**理由**：让校验和与资产走两条不同的信任路径。agentd 侧的代理或镜像被投毒时，
它下到的资产哈希对不上协调者从 GitHub 直接拿到的声明，当场被抓。反过来（agentd
自己取 checksums 自己比）等于让同一条被控链路给自己出具证明，只防传输出错、不防投毒。
成本几乎为零：`checksums.txt` 几百字节，且**一个 release 只下一次**——
巡检阶段下一份缓存在内存，多台机器按各自平台取自己那行。

新增 `Installer.FetchChecksum(ctx, rel, goos, goarch) (string, error)`：只下 checksums、
只解出那一行，不下资产。

### 5.6 CLI 侧

`remoteUpgrade` 按 §5.1 的选路分两条；自拉路径不再调 `FetchArchive`（不下 20MB）。

`WaitVersion` 两处改动：

- 超时从 60s 放宽到 **10min**（自拉要下 20MB，慢网 + 代理下几分钟很正常）；
- **轮询时顺带读 `status.Update.PullState`**：`stage` 用于打进度，`stage == "failed"`
  时**立刻中止并打印错误原文**，不等满超时。没有这一条，一次代理配错的失败要让人
  干等 10 分钟才看到一句「对端版本仍是 X」——而真正的原因
  （`proxyconnect tcp: dial tcp 127.0.0.1:1080: connect: connection refused`）
  就在对端的 status 里躺着。

新增 `--push` 标志：强制走推送模式（内网执行机确实出不了网时的逃生路径）。

`upgrade --check` 的巡检表不变（不新增列）——「这台机器走自拉还是推送」是 `--now` 的
执行细节，在只读巡检表上多一列只会挤掉真正的结论。

---

## 6. 决策记录

| 决策点 | 拍板 | 理由 |
|--------|------|------|
| 两件事合一份 spec 还是拆两份 | **合一份** | 用户 08-13 拍板。两者是依赖关系（自拉的前提是执行机能配代理），接口一次想清楚 |
| 自拉 vs 推送 | **默认自拉，`--push` 回退** | 用户 08-13 拍板。不出网的内网执行机是真实场景，推送是它唯一的路；且老 agentd 必须能降级，否则升级链路会在过渡期断掉 |
| sha256 来源 | **协调者下发** | 用户 08-13 拍板。校验和与资产走两条信任路径才防得住投毒（§5.5）；成本几乎为零 |
| 接口同步还是异步 | **异步受理 + 状态回报** | 用户 08-13 拍板。同步方案的连接在下载期间完全 idle，会被中转/反代的 60s 空闲超时掐断——而中转正是本需求的初衷（§5.3） |
| `proxy` 与 `env` 段的关系 | **严格分开** | 用户 08-13 拍板。`proxy` 只管 handoff 自己出网，executor 出网仍归 `env` 段。故障域不交叉：代理挂了只影响升级，不影响任务执行 |
| 代理配置形态 | **单个顶层字符串，scheme 决定协议** | 分 http/https/socks 三个字段是在解决一个不存在的问题——handoff 自己只打 GitHub 一个域，一个代理够了。`no_proxy` 同理，YAGNI |
| 坏 proxy 值的处置 | **启动期 fail-fast** | 更新检查路径的纪律是"失败静默跳过"，坏代理在那里表现为**什么都不发生**，可以数月无人察觉。与 `approver.blacklist` 正则启动期编译是同一条纪律 |
| `release` 包怎么拿到代理 | **传 `http.RoundTripper`，不传配置字符串** | 该包 package 注释明写"不读 handoff 的配置"。传 transport 守住这条边界，也让测试能塞假 transport |
| git 代理怎么注入 | **`-c http.proxy=`，只套 clone/fetch 两处** | 比注入环境变量精准：不污染子进程环境，也不会让本地 git 操作平白多一个配置。代价是 SSH remote 吃不到（§3.4，明确非目标） |
| 能力探测要不要做 | **要，`Pull *bool`** | 不做的话老 agentd 会把 `mode=pull` 当成纯重启并回 200，CLI 报"已换版但未上线"——一次纯误导。`Managed *bool` 已经有这套 nil 语义的先例 |
| 自拉状态存哪 | **只在内存，无 `done` 态** | 成功路径的终点是重启，状态自然消失且版本号已可自证；失败时进程不重启，内存态正好可查（§5.4） |

---

## 7. 文档与测试

### 7.1 文档

- **README 配置块**：加 `proxy: ""` 一行并注明「空 = 沿用 `HTTPS_PROXY` 等环境变量」，
  以及作用范围（更新链路 + git clone/fetch，**不含** executor——那是 `env` 段）。
- **README `env` 段说明**：现有那段讲「给 executor 带上代理」的文字要点明与顶层
  `proxy` 的分工，否则两个都叫代理的东西会让人配错地方。
- **README 升级章节**：说明默认执行机自拉、执行机需能访问 GitHub、`--push` 回退，
  以及"老 agentd 自动降级推送"。
- **README 排障表**：加一行——升级卡住/失败时去看 `handoff status --target X` 的
  `pull` 段拿错误原文。
- **SSH remote 限制**：写在自动登记/代理相关处，给出 `insteadOf` 改 HTTPS 的解法。
- **推翻记录（硬要求）**：`2026-08-11-update-and-skill-delivery-design.md` 的 D1
  「执行机无需出网」被本文正面推翻。必须在那份 spec 里留一条明确的推翻记录并指向本文，
  否则两份 spec 各说各话，下一个读到 B59 的人会照着一个已经不成立的前提做设计。

### 7.2 测试

**`internal/config`**
- `proxy` 四种合法 scheme 均能加载；
- 非法 scheme（`socks4://`、裸 `host:port`、空 scheme）在**启动期**被 validate 拒绝，
  错误文本含支持的四种 scheme；
- **回归**：`Save` 一份 `Proxy == ""` 的配置，产物里**不含** `proxy:` 键
  （omitempty 的旧版兼容契约，与 `path_dirs` 同款）。

**`internal/proxycfg`**
- 空串 → Transport 的 Proxy 行为等价于 `ProxyFromEnvironment`（现行为不变）；
- 非空 → 任意请求都解析到配置的代理；
- `GitArgs` 空串返回 nil、非空返回两元素切片；
- `Redact` 对 `socks5://u:p@h:1080` 不泄漏 `p`——**这条是凭据纪律的回归测试**。

**`internal/agentd`（update）**
- `mode=pull` 缺 tag 或缺 sha256 → 400；
- `mode` 非法值 → 400（不静默降级）；
- `mode=push` 带空 body → 400（**不塌成纯重启**）；`mode=pull` 带非空 body → 400；
- 两道闸对 `mode=pull` 同样生效，且顺序不变（非托管在前）；
- 自拉在跑时再来一个 → 409 + `reason=pull_in_progress`；
- 下载到的字节 sha256 与下发值不符 → 不 Activate、`PullState.Stage == "failed"` 且
  `Error` 非空、**进程不重启**；
- 成功路径：Activate 被调用且 `triggerRestart` 被触发（用现有 `UpdateDeps` 替身）；
- **回归**：空 body 且无 `mode` 仍是纯重启（D8 行为一字不变）；
  body 非空仍走推送（现有路径不受影响）。

**`internal/client` + `cmd`**
- `Pull` 为 nil（老 agentd）→ 选路走推送；`*Pull == true` → 走自拉；
  `--push` 时无论 `Pull` 为何都走推送；
- `WaitVersion` 读到 `PullState.Stage == "failed"` 时**立刻返回**并把 `Error` 原文
  带进错误里（不等满超时）——这条是 §5.6 的核心行为，必须有测试钉住；
- `FetchChecksum` 在一次 `upgrade --now` 多机器时**只被调用一次**（缓存契约）。

**`internal/release`**
- `NewClient(nil)` / `NewInstaller(log, nil)` 与改造前行为一致（默认 transport）；
- 传入的 transport 真的被用上（假 RoundTripper 计数）。

---

## 8. 非目标

- **SSH 协议 remote 的代理**：`http.proxy` 对 `ssh://` 无效，要走 ssh `ProxyCommand`
  或 `GIT_SSH_COMMAND`，会动到用户的 ssh 配置面。README 给 `insteadOf` 改 HTTPS 的解法。
- **`install.sh` / `install.ps1` 的代理**：首装时 `config.yaml` 还不存在，只能走命令行
  参数或环境变量，不是同一个配置项。两个脚本用的 `curl` / `Invoke-WebRequest`
  本来就认环境变量。
- **`no_proxy` / 分 scheme 代理**：handoff 自己走代理的出网只有 GitHub 一个域（§2.1）。
- **executor 的出网代理**：归 `env` 段（B19），本文不动。
- **代理凭据的加密存储**：`config.yaml` 已是 0600，与其中的 token 同级——代理密码
  并不比 agentd token 更敏感，为它单独做密钥管理不成比例。
- **agentd 自己查 latest / 自动升级**：本文只让 agentd 按**协调者下发的 tag** 去下载。
  「agentd 自己决定何时升级」是另一件事（且会绕开协调者的两道闸编排）。
- **中转服务器本身**：本文只是为它省流量，不设计它。
