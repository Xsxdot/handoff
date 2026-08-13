# W4 PTY 终端设计

> 前置：[W4 外壳校准期](2026-08-12-w4-shell-calibration-design.md) 已交付中央 tab 系统，终端 tab 是其中唯一还是占位说明的一种（`web/src/app/workbench/TerminalTab.tsx`）。
> 本轮把它接成真终端。

**目标一句话**：控制台里能开出真正可用的 shell——本机与远程开发机都行，跑着长任务时可以关掉页面走人，回来接着看。

---

## 0. 范围

**本轮做**

1. agentd 侧 PTY 会话服务：开 shell、持有会话、回放缓冲、resize、显式关闭（§2、§3）
2. 会话级环境变量转发，解决托管形态下终端里 `ssh` / `git push` 失效（§4）
3. REST 会话管理 + `/ws/pty` 数据通道 + 能力上报（§5）
4. 远程开发机终端：本机 agentd 到远端 agentd 的 WS 反代（§5.4）
5. 前端 xterm 接入、断线续传、刷新后按服务端会话列表恢复 tab（§6）
6. 修正 W4 spec §2.6 关于「控制台会话弱于主令牌」的错误陈述（§1）

**本轮不做**，理由逐条见 §10：agentd 重启后的会话存活、空闲回收与数量上限、Windows ConPTY、终端输出落盘归档、tmux 式会话分屏、ligatures/serialize 两个 addon。

---

## 1. 前提修正：控制台会话在能力上等价于主令牌

W4 spec §2.6 写道：控制台会话「是**刻意做得比主令牌弱**的凭据」，因此不能让它读 `$HOME`，否则「弱凭据当场提权成强凭据，整套会话管理的意义归零」。它还给自己留了一句免责：「本期悬浮按钮只开终端——而终端本期还是占位，所以这条路径这一期根本不通到磁盘上，风险为零」。

**这段陈述在写下时就已经不成立。**

- [`server.go:256`](../../../internal/agentd/server.go) 的 `auth` 中间件里，Bearer 主令牌与 cookie 会话通过鉴权后进入**同一个 mux**，路由层面没有任何区分。
- 其中包含 `POST /api/tasks/{id}/run`，其实现是 [`workspace.go:1014`](../../../internal/agentd/workspace.go) 的 `exec.CommandContext(ctx, "sh", "-c", cmdline)`，命令内容完全由请求方给定。
- 任何一个控制台会话发一条 `{"cmd":"cat ~/.handoff/config.yaml"}` 即可读出主令牌。

**本轮采用的口径**：控制台会话在**能力上等价于主令牌**。会话管理的价值是**可吊销、按设备记录、可审计**，不是「权限更小」。

由此有三条推论，本设计据此展开：

1. PTY 终端**不引入新的能力边界**，它只是把一个已经存在的能力（任意命令执行）做成了好用的界面。
2. 悬浮按钮的 home 基准终端**照做**，不再以「安全边界放宽」为由推迟。
3. `base_path` 的白名单校验保留，但它是**参数校验不是安全边界**（§5.2）——必须在代码注释与文档里说清，不再借安全的名义。

**同步动作**：在 W4 spec §2.6 末尾追加一行指向本节。留着不管，下一个读到它的人会照着一个错误的安全模型做决策。

---

## 2. 架构与组件边界

```
浏览器 xterm ──WS（binary=字节 / text=控制）──▶ 本机 agentd
                                                   │
                                       machine=="" │ machine!=""
                                                   ▼            ▼
                                          internal/ptyhost   WS 反代
                                          （开 PTY、持会话）      └──▶ 远端 agentd 的 /ws/pty
                                                                        └──▶ 远端 ptyhost
```

| 单元 | 职责 | 明确不做 |
|---|---|---|
| `internal/ptyhost`（新包） | 开 PTY、起 shell、持有会话表、维护回放环形缓冲、resize、按进程组终止 | 不认识 HTTP/WS；不认识 agentd 的任务模型；不落盘 |
| `internal/agentd` PTY 接口层（新文件 `pty_api.go`） | REST 管会话 + `/ws/pty` 接流 + 能力上报 | 不持有会话状态，只调 ptyhost |
| WS 反代（新文件 `forward_ws.go`） | `machine != ""` 时本机 agentd 拨到远端 `/ws/pty`，两个 conn 对拷 | 不解析帧内容。是 `forwardTo` 的 WS 孪生 |
| 前端 `TerminalTab` 改写 | xterm 渲染、按键与尺寸上送、断线重连续传、加载时按服务端列表恢复 tab | 不持有会话真相，服务端说了算 |

**为什么 ptyhost 独立成包**：它是唯一需要按平台分文件的部分（unix 走 `creack/pty`，windows 走「不支持」桩），与 `internal/prochost` 同形。混进 agentd 会让 `GOOS=windows go build ./...` 这道现成闸门退化成一堆散落的构建标签。

**依赖新增**：Go 侧 `github.com/creack/pty`（MIT，无传递依赖）。项目目前只有 7 个直接依赖，多这一个是破例，理由是：`openpty` 在 darwin 与 linux 上的仪式完全不同（`TIOCPTYGRANT`/`TIOCPTYGNAME` vs `TIOCSPTLCK`/`TIOCGPTN`），而写错的后果不是「算错一个数」而是「挂死或留下孤儿会话」。这与 `prochost` 自写 `procenum` 的取舍不同——那里是只读枚举，错了只是数字不准。

前端新增 `@xterm/xterm`、`@xterm/addon-fit`、`@xterm/addon-webgl`（稳定版，不带补丁，理由见 §8.4）。

---

## 3. 会话模型与生命周期

一个会话 = 一个 PTY + 一个 shell 进程 + 一个环形缓冲。

### 3.1 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | string | uuid |
| `machine` | string | **线注解，不入库**：`""`=本机，否则为本机 `cfg.Targets` 的键，由汇总方盖章。与 `proto.Task.Machine` 同款 |
| `base_path` | string | 会话 cwd |
| `base_kind` | string | `workspace` 或 `home` |
| `shell` | string | 实际启动的 shell 绝对路径 |
| `created_at` | time.Time | |
| `cols` / `rows` | int | 当前生效尺寸（多方接入时是最小值，见 §3.3） |
| `attached` | int | 当前连接数 |
| `pid` | int | shell 进程 pid |
| `exit_code` | `*int` | **nil = 还活着**。遵循项目「指针三态、绝不猜」的纪律（同 `Watchers` / `Live` / `Procs`） |
| `bytes_out` | uint64 | 该会话自创建以来写入缓冲的总字节数，`since` 续传的水位（§5.3） |

会话表只在内存里，随 agentd 生死。**不落 SQLite**——理由见 §10。

### 3.2 生死规则

| 事件 | 行为 |
|---|---|
| WS 断开（断网、关页面、切设备） | **只是 detach**。PTY 照跑，输出继续进缓冲 |
| 前端切基准目录 / 组件卸载 | detach，同上 |
| 用户点 tab 上的 `×` | `DELETE /api/pty/sessions/{id}` → 按进程组终止（SIGTERM，宽限 2s 后 SIGKILL） |
| shell 自己退出（用户敲 `exit`） | 会话进终态：保留缓冲与 `exit_code` 供最后一眼，列表里仍可见并标出已退出 |
| agentd 重启 | 全部消失。重启后列表为空，前端如实显示，不假装 |

**不做空闲回收，不设数量上限**——用户明确要「跑一晚上的 build 不能被杀」，而按时间猜「空闲」必然会杀掉正是这个场景。代价是忘了关的会话会占进程预算，由 §8.2 的可见性兜底。

**「关」必须是显式的**：切换左栏基准目录只是换渲染哪一组 tab，不销毁会话；只有点 `×` 才杀。这条在前端与后端各有一半——前端组件卸载时**只能**断 WS，不许发 DELETE。

### 3.3 多方同时接入

会话列表在服务端，两台设备打开控制台会同时恢复出同一个会话，所以必须支持共享接入（tmux 语义）：

- 输出**广播**给所有连接
- 输入**任意连接都可以打**
- 尺寸取**所有活跃连接中最小的那个**。不取最小的话，大屏一 resize 就把小屏那侧刷成乱码——终端尺寸是单一物理属性，没有「各看各的」这个选项

订阅者数量上限 8，超出时拒绝新连接并给出明确原因（不是静默丢弃）。

### 3.4 回放缓冲

每会话一个 256 KiB 环形缓冲，attach 时按 `since` 灌历史再转实时。

**已知代价，如实记录**：环从头部截断时可能切在一个 ANSI 转义序列中间，xterm 会吞掉开头那点垃圾字符。接受它。替代方案（服务端做终端状态重建，如 `addon-serialize` 的服务端等价物）复杂度与收益不成比例。

---

## 4. 会话环境构造

### 4.1 shell 与基础环境

- shell：`$SHELL`，缺失时回退 `/bin/sh`
- 以 **login shell** 形式启动（`-l`），rc 文件照读——用户要的是「和我在 iTerm 里一样」
- 追加 `TERM=xterm-256color`、`COLORTERM=truecolor`
- cwd = `base_path`

PATH 不需要本设计做额外处理：B71 的 `pathenv.Apply` 在 agentd **fork 任何子进程之前**就把补全结果写回了自身环境，login shell 以它为父 PATH 再做推导。真机走查要验证这一条实际生效（§9）。

### 4.2 会话级环境变量转发（`SSH_AUTH_SOCK` 类）

**这是本轮必须解决的一个真实缺陷，不是预防性设计。**

问题：`SSH_AUTH_SOCK` 由 launchd（macOS）/ ssh-agent 会话（Linux）**按会话注入**，它不来自任何 dotfile，因此 `-l` 登录 shell **无法**像恢复 PATH 那样把它恢复出来。agentd 若以服务形态托管（launchd / systemd），它自身环境里就没有这个变量，终端里的 `ssh`、`git push`、私有仓库操作全部失败。

2026-08-12 在开发机上实测三条：

| 观察 | 结果 |
|---|---|
| 交互 shell | `SSH_AUTH_SOCK=/var/run/com.apple.launchd.tvZOp5bXsS/Listeners` |
| `launchctl getenv SSH_AUTH_SOCK` | 返回同一个值——**macOS 上的解析路径可用** |
| `env -i PATH=/usr/bin:/bin:/usr/sbin:/sbin` + `zsh -l` | PATH 由 4 条恢复到 15 条（rc 链正常工作），但 `SSH_AUTH_SOCK` **仍未设置** |

**这个缺陷在开发者自己的机器上测不出来**：当前运行的 agentd 是从交互终端手起的，`ps eww` 确认它的环境里有 `SSH_AUTH_SOCK`。只有装成服务之后才会消失——而 B71 正在推动用户去装服务（否则重启后 agentd 就没了）。两件事撞在一起：我们刚把用户赶向托管，托管恰好让终端里的 `git push` 失效。

**设计**：

1. 新增配置 `env_forward []string`，**必须 `omitempty`**——B59 D7 的教训：新版 `Save` 写出旧版不认识的键，旧 agentd 会因 `yaml.KnownFields` 直接起不来。同时要把该键加进 `config.go` 的未知字段错误提示串。

   **默认值只能在用的时候取，不能在 Load 时填进结构体。** 内置默认清单是 `["SSH_AUTH_SOCK"]`，但字段本身保持 nil：一旦 Load 把默认值写进结构体，下一次 `Save` 就会把 `env_forward: [SSH_AUTH_SOCK]` 落进 `config.yaml`，`omitempty` 形同虚设，旧 agentd 照样被顶死。判定是「字段为 nil → 用内置默认清单；字段非 nil → 完全以配置为准（含显式空列表 `[]` = 一个都不转发）」。这是 `path_dirs` 没有默认值因而不需要考虑、而这里必须考虑的一处差别。
2. 会话创建时对清单里每个名字按三级解析：

   | 顺序 | 来源 | 结论 |
   |---|---|---|
   | ① | agentd 自身环境（`os.Getenv`）非空 | `inherited` |
   | ② | 平台是 darwin，`launchctl getenv <NAME>` 有输出 | `resolved` |
   | ③ | 都没有 | `unavailable` —— **不编造，不设默认值** |

3. Linux 不猜：systemd 用户会话下没有等价的稳定查询口径。只做「继承 + 用户在配置里显式写死」，探不到就如实记 `unavailable`。
4. **逐个变量打日志**，三态各一条。这样「终端里 git push 失败」是一行可搜的日志，而不是一次盲猜。成功路径同样要有声（`inherited` / `resolved` 都记），否则无法区分「解析成功」与「这段代码根本没跑」。
5. 解析结果只进**这个会话的** `cmd.Env`，**不写回 agentd 自身环境**——与 `pathenv` 相反。PATH 是进程级恒定事实，socket 路径是会话级易变事实，写回会让后续所有 fork 拿到一个可能已经失效的路径。

**关于 `launchctl getenv` 的一次 fork**：B73 要求「防线全链路零 fork」，那条约束的对象是**进程耗尽时仍需工作的诊断路径**。会话创建本身就要 fork 一个 shell，此处多一次 fork 不改变可用性边界。解析失败（含 fork 失败）一律降级为 `unavailable`，不阻断会话创建。

---

## 5. 接口契约

四个新端点，全部挂在现有 `auth` 之后。REST 三个都支持 `?machine=`，直接复用 [`forward.go:45`](../../../internal/agentd/forward.go) 的 `forwardIfRequested`，零新代码。

### 5.1 REST

| 端点 | 语义 |
|---|---|
| `GET /api/pty/sessions[?scope=all]` | 列会话。`scope=all` 扇出 `cfg.Targets` 并给每行盖 `machine` 章，与 `/api/tasks` 同款 |
| `POST /api/pty/sessions` | 请求体 `{base_path, base_kind, cols, rows}` → 返回新会话 |
| `DELETE /api/pty/sessions/{id}` | 显式关闭，按进程组终止 |

### 5.2 `base_path` 校验

- `base_kind=workspace`：`base_path` 必须命中现有「已探测到的工作树」白名单（复用 `workspaceRootOrErr` 同一份判定）
- `base_kind=home`：直接用 `$HOME`，忽略传入的 `base_path`

**这是参数校验，不是安全边界。** 会话既然等价于主令牌，白名单挡不住任何有心人——终端里一条 `cd ~` 就出去了。它存在的唯一理由是防止前端传一个打错的路径、让 shell 起在文件系统某个莫名其妙的角落。代码注释必须写明这一点，不得复述 §2.6 那套已被证伪的安全说辞。

### 5.3 `GET /ws/pty?session=<id>&since=<n>[&machine=]`

帧形状：

| 方向 | 帧类型 | 内容 |
|---|---|---|
| 服务端 → 客户端 | **binary** | PTY 原始输出字节 |
| 服务端 → 客户端 | **text** | JSON 控制：`{"type":"attached","since":N,"truncated":bool}`（建连首帧）、`{"type":"exit","exit_code":0}`、`{"type":"error","message":"..."}` |
| 客户端 → 服务端 | **binary** | 用户按键原始字节 |
| 客户端 → 服务端 | **text** | JSON 控制：`{"type":"resize","cols":120,"rows":40}` |

**为什么二进制跑数据**：`coder/websocket` 天然区分 binary 与 text 帧，输出路径因此零解析、零 base64 膨胀。把输出也包成 JSON 会在最高频的路径上多一层编解码和约 33% 体积。

**`since` 是断线续传**，抄的是 `handoff attach` 已经验证过的做法（[`cmd/attach.go`](../../../cmd/attach.go) 的「断线可凭已收字节数续传」）：客户端记住自己收到的字节数，重连时带上；服务端能覆盖就从该点续，已被环覆盖掉就从环头开始并在 `attached` 帧里标 `truncated: true`。

**`truncated` 不是可选装饰**：前端据此决定要不要先清屏。不带这个标记，一次重连就会把同一段输出重复画一遍。

### 5.4 远程：WS 反代

`machine != ""` 时，本机 agentd 用 `coder/websocket`（已在 `go.mod`）拨到远端 `/ws/pty`（带 Bearer，取 `cfg.Targets[name].Token`），然后两个 conn 双向对拷，**不解析帧内容**。这是 `forwardTo` 的 WS 孪生。

为什么不让浏览器直连远端 agentd：cookie 是 host-only 的，远端那台没有本机这份会话，等于要另做一套跨机 ticket 分发与跨域处理。而「本机 agentd 是唯一入口」本来就是既有模型（`/api/workspaces/*` 的 `?machine=` 转发即此）。

这是本轮**唯一一块全新的基础设施**，也是往后任何 WS 能力跨机的地基。

### 5.5 能力上报

`GET /api/status` 的响应增加 `pty_supported *bool`：

- `nil` = 对端 agentd 太老，没上报（**不猜**）
- `false` = 平台不支持（Windows）
- `true` = 支持

与 `Watchers` / `Live` / `Procs` 同一条纪律。前端据此决定这台机器的终端 tab 画真终端还是画一句实话——**不能让用户点了才知道**。

---

## 6. 前端

### 6.1 会话恢复

页面加载时拉 `GET /api/pty/sessions?scope=all`，把活着的会话按 `base_path` 归到各自的 tab 组里，恢复成终端 tab。

**这修改了 W4 spec §10 的一条既定决策。** 原决策是「tab 组存内存，刷新即丢」，理由是持久化要处理「目录被删了但 tab 还在」这类失效态。本轮只对**终端 tab** 破例，且**不引入前端持久化**——服务端会话列表是唯一真相，失效态天然不存在（会话不在列表里就是没有）。文件 tab 与 TUI tab 维持原状。

换一台设备打开控制台，看到的是同一批会话——这是服务端为真相的直接后果，也正是用户要的。

### 6.2 tab 生命周期

| 前端事件 | 动作 |
|---|---|
| 组件卸载（切基准目录、切 tab） | 关 WS。**不发 DELETE** |
| 点 `×` | 发 `DELETE`，成功后移除 tab |
| 点 `×` 且会话内还有前台进程 | 先弹确认 |
| WS 断开 | 指数退避重连，带 `since` |
| 收到 `attached` 且 `truncated=true` | 先 `terminal.clear()` 再灌 |
| 收到 `exit` | 停止重连，在终端底部显示退出码，tab 保留直到用户手动关 |

### 6.3 xterm 接入

`@xterm/xterm` 稳定版 + `@xterm/addon-fit`（尺寸自适应）+ `@xterm/addon-webgl`（大量输出时的帧率）。WebGL 在不可用环境下要有降级分支，不能白屏。

封存分支上的 `XtermSurface.tsx`（194 行，handoff 自建）作为**参考实现**重写，不作依赖。

---

## 7. 错误处理与降级

| 情形 | 行为 |
|---|---|
| Windows agentd | `ptyhost` 的 windows 桩返回不支持：`POST` → **501**；`GET` → **空列表而非报错**（「没有会话」是真的）；WS → close 1011 带原因。前端靠 `pty_supported=false` 提前显示实话 |
| `base_path` 不在白名单 / 不存在 | **400**，文案说清是参数问题（不是 403——见 §5.2，这里不再借安全的名义） |
| 会话不存在或已被关 | 404 |
| 远端机器够不着 | **502 带原文**，与 `forwardTo` 一致：这是本机与目标机之间的问题，不能伪装成目标机的业务错误 |
| 建连时会话已退出 | **不当错误**：先灌缓冲让用户看到最后的输出，再发 `exit` 控制帧，再正常 close(1000) |
| shell 起不来（`$SHELL` 不存在等） | 会话直接进终态，`exit_code` 与错误原文一并保留，列表里看得到——不是静默失败 |
| 订阅者已达 8 个 | 拒绝新连接，close 时带明确原因 |
| `env_forward` 某个变量解析失败 | 降级为 `unavailable` 并记日志，**不阻断会话创建** |

---

## 8. 与既有子系统的关系

### 8.1 与 `handoff attach` 的分工

`attach` 看的是**任务实况**（`render.log`，模型回合正文），是只读的、任务维度的。PTY 终端是**人自己的 shell**，可写，目录维度。两者不重叠，本轮不合并，`attach` 一行不动。

### 8.2 与 B73 进程围栏 / B69-B70 足迹的关系

终端里起的进程**不套 B73 的 `RLIMIT_NPROC` 围栏**。理由：用户手敲的命令被静默限流，比 fork 失败更难归因；而 B73 的归因通道本来就会告诉你「进程配额耗尽 (N/M)，非代码问题」。

但终端会话**必须进可见性账本**：`handoff status` 与 `handoff footprint` 要能看到「有几个终端会话、各占多少进程」。不然它就是账本上的黑洞——而 B70 的整个立论就是「先让占用可见」。

### 8.3 与 B71 `pathenv` 的关系

B71 解决的是「agentd 自身 PATH 不全」，已在 fork 之前写回自身环境，终端天然继承。§4.2 解决的是**另一类**问题：会话级注入、不来自 dotfile、login shell 恢复不了的变量。两者互补，`env_forward` 与 `path_dirs` 在配置里比邻而居，`omitempty` 纪律相同。

### 8.4 与封存分支 `internal/ptyservice` 的关系

封存分支 `codex/plan02-workspace-resources-rest` 上有一套完整的 `internal/ptyservice`（1090 行实现 + 746 行测试，含 ring buffer、`command_id` 幂等、原子 replay+订阅）。

**不搬过来**，理由：它依赖 `internal/controlplane`、`internal/workspaceapi` 与一整套 machine-outbox 事件架构——这些在 main 上都不存在；并且它把 PTY 会话元数据落进 SQLite，与本轮「会话不持久化」的决策相反。

**可参考**：`ring.go`（96 行环形缓冲）与 `service_unix.go` 的进程处理（进程组 SIGTERM→SIGKILL、`128+signal` 的退出码换算）。当参考实现读，不当依赖引。

`service_unix.go:43-45` 的 `cmd.Env = append(os.Environ(), ...)` 正是 §4.2 要避免的那个写法——同一个坑，本轮从设计上绕开。

### 8.5 xterm 补丁栈不迁移

封存分支的 `desktop/config/patches/` 下五个补丁，实测结论与 ADR-0009 的记载**不一致**，如实更正：

- `node-pty@1.1.0.patch` 在 Go PTY 形态下**定义上就没有意义**（node-pty 整个不存在）。ADR-0009 「五个补丁没有一个是 Electron 相关的」这句不准确。
- 其余四个是打在 Orca pin 死的 **beta 版压缩产物**上的（`@xterm/xterm@6.1.0-beta.287` 那个 diff 有 3.7 MB，改的是 `lib/xterm.js` 这个 minified bundle 本身），换版本即失效。
- 前端用 npm，没有 pnpm 的 `patchedDependencies` 机制。

结论：用稳定版，不带补丁。中文输入法先实测，真有问题再单独立项处理——不预先背一套搬不动的补丁栈。

---

## 9. 测试策略

**`ptyhost` 单测**（unix build tag）：开会话 → 写命令 → 读回显 → resize 生效（用 `stty size` 验证）→ 终止 → `exit_code` 正确。环形缓冲的 `since` 续传与 `truncated` 判定用表驱动钉住。

**`env_forward` 单测**：必须覆盖「**目标变量不在 `os.Environ()` 里**」那条路径（用 `t.Setenv` 清掉）。否则在开发者手起 agentd 的机器上，测试与现实会一起绿——这正是这个缺陷躲过一整轮实现的原因。三态各一条用例，`launchctl` 那层用可注入的解析函数打桩。

**配置往返单测**（`internal/config`）：Load 一份不含 `env_forward` 的 yaml → Save → 断言输出里**仍然没有**这个键，同时断言解析逻辑此时用的是内置默认清单。这条钉的是 §4.2 那个「默认值不许在 Load 时填进结构体」的约束——没有它，实现者按最顺手的写法就会把旧 agentd 顶死，而所有功能测试仍然全绿。

**agentd 接口层**：`httptest` + 真 WS。覆盖会话 CRUD、多方广播、**尺寸取最小**、`since` 续传、订阅者上限、平台不支持时的降级形状。

**WS 反代**：起两个 `httptest` agentd 串起来，验证字节端到端透传与 close 传播。这是全新基础设施，必须有端到端钉子。

**前端**：假 WS。验证「卸载只 detach 不杀」「显式关才杀」「加载时按服务端列表恢复 tab」「`truncated` 时先清屏」「收到 `exit` 后停止重连」。

**真机走查**（照 W4 Task 17 的规格执行）：

1. 本机开一个终端，`stty size` 与实际尺寸一致，中文输入正常
2. devbox 开一个终端，确认走的是 WS 反代
3. 终端里 `ssh -T git@github.com` 或 `git push --dry-run` 成功——**这条是 §4.2 的验收**
4. 跑一个长任务，关掉浏览器页面，换一个窗口尺寸重新打开：会话自动恢复、输出连续、`truncated` 时清屏正确
5. 两个浏览器窗口同时接同一个会话，验证广播与最小尺寸
6. 点 `×` 关闭，`handoff footprint` 里该会话的进程数归零
7. `pathenv` 的补全在终端 PATH 里实际生效（§4.1）

**§4.2 的托管形态验收**：理想情况下应在 `service install` 托管的 agentd 上验证。B71 的 V2 因为 launchd label 固定、无法与生产实例并存而未验，本轮同此约束。若拿不到停机窗口，就用 `env -i` 起一个隔离实例复现最小环境，并**如实记「托管形态未验 + 原因」**，不打勾。

---

## 10. 本轮不做

| 不做 | 理由 |
|---|---|
| agentd 重启后会话存活（shim 化） | 要解决 PTY 主端 fd 移交、缓冲落盘、认领时尺寸重协商三个问题，体量与本轮相当。「活过断线」已覆盖 99% 的实际场景 |
| 空闲回收与会话数上限 | 按时间猜「空闲」必然杀掉「跑一晚上的 build」，而那正是要的能力。可见性由 §8.2 兜底 |
| Windows ConPTY | 另一套 API。本轮如实降级并上报能力（§5.5），不假装支持 |
| 终端输出落盘归档 | 那是 `render.log` 的职责，任务维度。人的 shell 历史不该被 agentd 存起来 |
| tmux 式会话分屏 / 窗口管理 | 工作台的 tab 与左右分屏已经是那个东西，再做一层是同一件事的两个入口 |
| `addon-ligatures` / `addon-serialize` | 前者是观感，后者服务于「服务端状态重建」——而我们选了环形缓冲（§3.4） |

---

## 11. 验收标准

1. 本机工作树里开出的终端可交互，`stty size` 与窗口一致，中文输入正常
2. home 基准（悬浮按钮）开出的终端同样可用
3. 远程开发机（devbox）的终端可用，且确认走 WS 反代而非浏览器直连
4. 终端里 `git push` / `ssh` 可用；agentd 日志里能看到每个 `env_forward` 变量的三态结论
5. 跑长任务时关闭页面，重开后会话自动恢复、输出连续；`truncated` 场景下先清屏不重复
6. 两个客户端同时接入：输出双方都看到、任一方可输入、尺寸取最小
7. 切换左栏基准目录**不杀**会话；点 `×` **才杀**，且杀后 `handoff footprint` 中该会话进程归零
8. shell 自己 `exit` 后，终端显示退出码，会话在列表里标为已退出
9. Windows agentd（或 `pty_supported=false` 的对端）上，前端显示实话而非可点的死按钮
10. 老版本 agentd（`pty_supported=nil`）不被误判为「不支持」，文案区分「对端没上报」与「明确不支持」
11. 用户没有显式配置时，新版 `Save` 写出的 `config.yaml` **不含** `env_forward` 键（默认值不落库，`omitempty` 生效，旧版 agentd 不被顶死），而 `SSH_AUTH_SOCK` 转发照样工作
12. W4 spec §2.6 已追加指向 §1 的修正说明
