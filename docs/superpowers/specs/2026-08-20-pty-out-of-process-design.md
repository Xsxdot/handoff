# PTY 托管到 agentd 进程之外（A 部分）设计

日期：2026-08-20
分支：`claude/pty-hosting-state-sync-eb0ff1`

## 0. 背景与范围

今天 agentd 一重启（崩溃、被 OOM 杀、`handoff upgrade` 换版、launchd 拉回），
**所有终端会话连同它们跑着的进程一起没**。跑了一晚上的 build 白跑。

根因在形态：`internal/ptyhost` 是 agentd **进程内**的一个库。`ptyhost.New(log)` 在
`internal/agentd/server.go:181` 建出 `Host`，会话表是内存里的 map（包注释：「不落盘：
会话表只在内存里，随 agentd 生死」），shell 是 agentd 直接 fork 的子进程。agentd 一死，
PTY 主端 fd 关闭，shell 收到 SIGHUP，整棵进程树跟着走。

本 spec 把 PTY 会话搬到 agentd 之外的独立进程里，让它活得比 agentd 长。

**与 B（工作台状态同步）的关系**：两者独立，唯一耦合点是 `session_id`——B 把它存进布局，
A 保证它跨 agentd 重启仍然有效。B 先落地，届时 A 做完当天就能看到「agentd 重启后终端
还在原来那一栏、连滚屏内容都在」。

## 1. 关键决策

### 1.1 一个会话一个进程，agentd 经 unix socket 连它

ptyhost 进程持有主端 fd 与环形缓冲，agentd 只是它的一个订阅者——与浏览器订阅 agentd
的关系完全同构。

**否掉的两个替代：**

- **一个常驻 ptyhostd 守护所有会话**：它把「会话表」这个最容易失配的东西又搬进了一个
  长命进程里，而且那个进程崩了是所有终端一起没——比现状还糟。
- **`SCM_RIGHTS` 把主端 fd 直接递给 agentd**：致命问题是环形缓冲归谁。留在 agentd，
  则 agentd 重启期间的输出没人接，shell 写满管道缓冲区会阻塞——跑了一晚上的 build 会在
  agentd 重启那一刻卡死；在 ptyhost 侧再维护一份，则 fd 要两边同时持有，读者竞争没法收敛。

选一个会话一个进程还有两条附带好处：**故障隔离**（某个 shell 把终端搞崩了只影响它自己）与
**生命周期对齐**（进程的生死就是会话的生死，不需要在守护进程里再维护一张会话表）。它也与
`internal/prochost`「一个 executor 一个 shim」的形态一致，两套东西的心智模型能对齐。

### 1.2 会话活多久

| 场景 | 会话 |
|---|---|
| agentd 崩溃 / 被 OOM 杀 / launchd 拉回 | **活**——这是本 spec 的全部意义 |
| `handoff upgrade` 换版重启 | **活**（可能因协议版本变成只能关闭的死物，见 §1.3） |
| `handoff service stop` | **一起停** |
| 机器重启 / 关机 | 死 |
| 长时间没人订阅 | **活。不加空闲超时** |
| shell 退出 | ptyhost 继续活着守退出码与最后那屏输出，**24 小时**上限 |
| 用户点 × | 杀进程组、退出、清目录 |

**为什么 `service stop` 一起停**：用户敲它的意图是「让这台机器上的 handoff 全停下来」，
背着他留一堆 shell 是违背意图的——而且他下次 start 会看到一批来历不明、自己也不记得
开过的会话。升级走 `handoff upgrade`，那条明确算「重启」，会话保留；两者的区别是**显式
表达出来的**，不是猜的。

**为什么不加空闲超时**：「没人订阅超过 N 小时就自动关」听着卫生，但它会**精准杀掉唯一
值得保护的用例**——跑了一晚上的 build，整晚就是没人订阅。会话堆积不是没有出口：
`/api/pty/sessions` 列得出来，控制台点 × 就关。

**为什么已退出的会话给 24 小时上限**：它与「不加空闲超时」不矛盾，因为对象不同。那条
拒绝的是杀掉一个**还在干活**的会话；这里超时的对象是**已经死掉的** shell，它不在跑任何
东西，留着的唯一价值是让人回来看一眼报错。24 小时之后那屏输出对谁都没有意义了。
不给上限的话，一台开发机跑上几个月会攒出几十上百个谁也不记得的死会话，把
`/api/pty/sessions` 变成噪音。

### 1.3 协议版本错配：如实报，不假装

`handoff upgrade` 之后，新 agentd 要和**旧二进制起的** ptyhost 进程说话。这不是边角情况
——「升级不该杀掉所有终端」正是 §1.2 定下的目标，所以协议错配是本 spec 的**主线场景**。

**处置：握手带版本号，不认识就如实报「这个会话由 vX 托管，本版接不进来，只能关闭」。**
会话进程照旧活着（build 继续跑），但控制台上那个 tab 是个只能关的死物。

否掉的两个替代：

- **承诺协议只增不改、永远向后兼容**：这是一个我们没能力检验的承诺——没有跨版本的
  自动化测试，「向后兼容」就只是一句愿望，守没守住只有在真升级那天才知道。
- **旧进程收到不认识的握手时自己退出，agentd 原地起新的**：把一个罕见事件变成了
  「每次升级都可能丢终端」，正是要根治的东西。

配套是让它尽量不发生：协议保持极小（五个控制帧），加字段走「未知字段忽略」，
只有真正的破坏性变更才升版本号。

## 2. 架构分界

`ptyhost.Host` 今天对外只暴露六个方法——`Open` / `List` / `Get` / `Write` / `Close` /
`Attach`——而 `pty_api.go` 与 `pty_ws.go` 用的就是这六个。`Attach` 返回的 `Attachment`
也只有四个公开字段：`Backlog` / `Since` / `Truncated` / `Out <-chan []byte`。

**A 的做法是保留这套接口，换掉实现：**

- 现在的 `Host` 实现（会话表、环形缓冲、订阅广播、尺寸协商、`pump` / `reap`）**原样搬进
  ptyhost 进程内部**，一行逻辑不改。
- agentd 里的 `Host` 变成一个**客户端**：同样的六个方法，内部改成连 unix socket 发帧。
  `Attachment.Out` 那个 channel 由一条 goroutine 从 socket 喂。

于是 `pty_api.go` 与 `pty_ws.go` **几乎一行不用改**。这不是巧合，是它们的边界注释一直
守着的结果（「不持有任何会话状态，全部转交 s.pty」）。

**附带保住的语义**：`since` 的绝对序号。浏览器断线重连靠 `since=<bytes_out>` 续传，
环形缓冲与计数器都在 `Host` 里；搬进独立进程后它们跟着进程活着，agentd 重启后浏览器
重连拿到的 `since` 还是同一套坐标。这条不需要任何新设计，是选 §1.1 白捡的。

## 3. 会话目录

```
~/.handoff/ptys/<session-id>/
  meta.json   静态事实：base_path / base_kind / cwd / shell / created_at / pid / proto_version
  sock        unix socket，0600（目录 0700）
  lock        存活锁，ptyhost 全生命周期持有
```

**agentd 启动时只扫目录 + 试锁**：锁试得到 = 那个进程已经死了 → 删目录；试不到 = 它活着
→ 用 `meta.json` 把会话登记进内存表。这一步**不连任何 socket**，启动路径不被 N 次连接拖慢。

判活用文件锁不用 pid，理由直接沿用 `prochost.go` 的注释：pid 会被操作系统复用，
「进程存在」不等于「我的那个进程存在」——`workspace.go` 历史上就因此误杀过无关进程组。

`proto_version` 写进 `meta.json` 而不是只在握手里给：这样 agentd 在**列表阶段**就知道
哪个会话接不进来，能直接标出「由 vX 托管」，而不是等用户点进去才发现。

## 4. 两种交互，两种连法

- **控制查询**（`List` / `Get`）：短连接，问一次 `stat` 拿 `bytes_out` / `foreground` /
  `attached` / `cols` / `rows`，断开。
- **数据订阅**（`Attach` / `Write` / `Resize`）：长连接，**只在真的有人订阅时才建**，
  断开即关。

分开的理由：`GET /api/pty/sessions` 要报 `foreground`（有没有命令在前台跑，控制台据此
决定关 tab 前要不要确认）与 `bytes_out`。这两个是**活事实**，`meta.json` 里的必然是陈的
——报陈的比不报更糟。而本机 unix socket 一次往返是微秒级，列表时对每个会话问一遍完全
不心疼。

## 5. 进程与协议

### 5.1 拉起

隐藏子命令 `handoff _ptyhost --spec <路径>`，`Hidden: true`，照 `cmd/shim.go` 的路子。
agentd 用 `os.Executable()` re-exec 自己，detached 拉起。spec 文件 0600，含会话 id、
cwd、shell、env、初始 cols/rows、会话目录路径。

### 5.2 帧格式

```
[类型:1][长度:4 大端][载荷]
  类型 0 = PTY 原始字节（零解析，同 /ws/pty 的 binary 帧）
  类型 1 = 控制帧 JSON
```

控制词汇表五个，与现在 `/ws/pty` 那套几乎一一对应：

| 帧 | 方向 | 载荷 |
|---|---|---|
| `attach` | agentd → ptyhost | `{since}` |
| `attached` | ptyhost → agentd | `{since, truncated, proto_version}` |
| `resize` | agentd → ptyhost | `{cols, rows}` |
| `stat` | 双向（请求/应答） | 应答 `{bytes_out, foreground, attached, cols, rows, exit_code}` |
| `exit` | ptyhost → agentd | `{exit_code}` |

`close` 不是控制帧：关闭走 socket 断开 + 一个显式的 `DELETE` 语义（agentd 发 `stat` 之外
的独立短连接命令 `kill`）。**这一条要在 plan 里定死**：把「断开订阅」和「杀掉会话」压在
同一个信号上，正是「切个 tab 就杀掉跑了一晚上的 build」的经典成因。

agentd 是转译层：浏览器的 `/ws/pty` 帧 ↔ ptyhost 的 socket 帧，两边的词汇表刻意保持
同形，转译不需要状态机。

## 6. 生命周期实现要点

- **`service stop` 一起停**：agentd 收到停止信号时遍历会话表，逐个发 `kill`，等一个短
  超时（2s）后不管结果直接退出——**agentd 的退出不能被一个赖着不死的 shell 卡住**。
- **shell 退出后的 24 小时**：由 ptyhost 进程**自己**计时，不依赖 agentd（agentd 可能整段
  时间都不在）。到点自行退出并清目录。
- **进程组终止**：沿用 `platform_unix.go` 现有的 `terminatePty` / `killPty`，先 SIGTERM
  进程组、宽限后 SIGKILL。

## 7. 错误处理

| 情形 | 处置 |
|---|---|
| fork ptyhost 后 socket 迟迟不出现 | 等 3 秒判失败：杀进程、清目录、`Open` 返回错误 → HTTP 500 带真因 |
| 订阅中 socket 断开（ptyhost 崩了） | 关闭 `Attachment.Out`。这与「会话结束」同一个信号，`pty_ws.go` 的既有路径直接接住——它本来就把 `Out` 关闭理解为「会话结束，别重连」 |
| `stat` 查询超时（1 秒） | 该会话的活事实报为「未知」，**不假装是 false**。`foreground` 报不出来时控制台关 tab 一律先确认 |
| 协议版本不认识 | 会话列出来但标记不可接入，只能关闭（§1.3） |
| 扫目录时 `meta.json` 坏了但锁还占着 | 有个进程活着而我们不知道它是什么。**不删目录、不杀进程**，只记 Error 并在列表里标为「异常，需人工处理」——盲杀一个说不清来历的进程是这套东西最不该做的事 |
| 会话目录能建但 socket 绑不上（路径过长） | unix socket 路径有长度上限（macOS 104 字节）。`~/.handoff/ptys/<uuid>/sock` 约 60 字节，有余量；仍要在 `Open` 里显式检查并给出可读错误，而不是让 `bind` 报一个 `invalid argument` |

## 8. 日志与注释

- ptyhost 进程有自己的日志落点 `~/.handoff/ptys/<id>/ptyhost.log`（与 shim 同款）：
  启动、绑 socket、每次 attach/detach、shell 退出、24 小时到点自退，各一条。
  **agentd 不在的时候只有它能作证**，这是它必须自己落盘的理由。
- agentd 侧：扫目录的结论（活几个、清了几个、几个异常）一条 Info；每次拉起 ptyhost
  一条 Info 带 session id 与 pid；版本错配一条 Warn。
- 每个新建文件写「职责 + 边界」；每个导出函数写参数 / 返回 / 注意事项。

## 9. 测试

- **`ptyhost` 包内既有的那批测试原样保留**（ring、envforward、platform、Host 生命周期）
  ——实现搬了家但没改，它们仍是同一份网。
- 新增单元：会话目录扫描与试锁的三态（活 / 死 / `meta.json` 坏但锁占着）；帧编解码往返；
  `Open` 后 socket 超时的失败路径；版本错配的降级；已退出会话的 24 小时上限。
- **跨进程真机用例（本 spec 的验收判据，必须自动化）**：起一个真 ptyhost，写入若干输出，
  断掉「agentd 那一侧」的连接，重新连上，`since` 续传拿回滚屏——一个字节都不能差。
- 平台：`GOOS=windows go build ./...` 必须过。

## 10. 明确不做

- **Windows 的 ConPTY**。现在 `platform_other.go` 返回 `ErrNotSupported`，一路传到
  HTTP 501 与 `/api/status` 的 `pty_supported=false`。新形态保持这个结论。ConPTY 是
  另一套 API，够写一份自己的 spec。
- **跨机反代层的任何改动**。远程机器上的 PTY 由那台的 agentd 管，本机只是反代
  （`forward_ws.go`）。A 完全在单机内部。
- **旧会话的迁移**。升级到带 A 的那一版时，现有会话还活在旧 agentd 的进程内存里，
  没有目录、没有锁、没有 socket，新版无从认领——它们会死一次。这是一次性代价，
  说清楚就行，不值得为它写迁移。

## 11. 已知取舍

- **进程数**：每个终端会话多一个常驻进程。它极轻（一个 goroutine 泵 + 256 KiB 环形
  缓冲），但会计入 `prochost` 的进程围栏与 `resource_pressure` 告警口径。要在 plan 里
  确认这两处的预算是否需要跟着调。
- **协议错配时那个 tab 是死物**：进程还在跑（build 没白跑），但看不到它的输出。这是
  §1.3 权衡的结果，不是缺陷。
- **`service stop` 的 2 秒超时之后 agentd 照退**：极端情况下可能留下一个没被杀干净的
  ptyhost。下次 agentd 启动时扫目录会发现它还活着并重新认领——不会变成孤儿。
