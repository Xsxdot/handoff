# B234 spec 审查（macOS 测试端口假红 / PTY Close 未收摊，并入 B193）

审查对象：`docs/superpowers/specs/b234.md`（状态：待独立审查）
对照台账：`docs/superpowers/specs/b234-ledger.md`
对照代码：工作树 `/Users/sycm/.handoff/worktrees/b234-macos-ports` 分支 `fix/b234-macos-test-ports`（与 `origin/main` @ `5d733488e` 同内容，外加未提交 spec/台账）
源卡：B234、B193（审查未用 `handoff card show` 复核，以 spec/台账引述为准）
审查者：独立 subagent（charter spec 审查，只读，不改 spec/代码）
日期：2026-08-28

行号按当前工作树，会漂。

## 1. 总判

**修订后再批。**

方向对：族一不是忘了用 httptest，是 Darwin 临时端口被测试 TCP + TIME_WAIT/其它状态占住；族二是 `Host.Close` 把控制连接 EOF/超时当收摊、`hostproc.stopNow` 只关连接、`Engine.Close` SIGTERM 后立刻返回。两族禁止收成一个框架、禁止 Skip/sleep 混过去，这些与活代码对齐。不能批的原因是三处按正文落地会做错题：① `Engine.Close`「等到 reap（`cmd.Wait`）」会被读成再 `Wait` 一次，而 `reap` 已经占用了唯一的 `cmd.Wait`；② 族二红回路把「今天应红」绑在 Close 后立刻 `RemoveAll` 失败上，现网 Close 返回时 EXIT trap 往往还没写，implement 会「未红不许改」卡死，或只等 `_ptyhost` 进程就假绿；③ `Host.Close` 的进程 Wait 被钉在 `Open` 的 `waitDone` 上，Adopt / `CloseAll` / 重启后点 × 没有子进程可 `Wait`。最小补丁见文末。

## 2. Findings

### Critical

#### C1. `Engine.Close`「等到 reap（`cmd.Wait`）」有两种不兼容读法；字面再 `Wait` 一次会与现成 `reap` 对撞

- **位置**：spec 方案族二 §1 `b234.md:91`；活代码 `internal/ptyhost/engine/engine.go:reap`（195–207）、`Engine.Close`（294–327）、`internal/ptyhost/engine/platform_unix.go:waitExitCode`（115–128）
- **事实**：`Engine.Open` 在 121–122 行已经 `go h.pump(s)` + `go h.reap(s)`。`reap` 的第一句是 `waitExitCode(s.cmd)` → `cmd.Wait()`。`exec.Cmd.Wait` 只能成功调用一次，第二次返回 `exec: Wait was already called`。`Engine.Close` 今天只 `terminatePty`（SIGTERM 整组）然后立刻从 `h.sess` 摘掉，SIGKILL 放在 `termGrace=2s` 的 goroutine 里，**不** `Wait`。
- **为什么承重**：方案原文是「SIGTERM → 等到 reap（`cmd.Wait`）或 `termGrace` 到点 SIGKILL 再 wait」。读法 A：等已有 `reap` 把 `s.exited` 置位（正确）。读法 B：在 `Close` 里再调 `cmd.Wait()`（按括号字面）。读法 B 下 Close 与 reap 竞态，一条拿到退出码、一条报 Wait already called；SIGKILL 兜底与 `s.f.Close()` 的时序被打乱，PTY 主端可能在 shell 死之前或之后乱关。族二「进程退出前必须 wait 到 shell」整段会写成一个新的红。
- **建议**：写死：`Close` **禁止**再调 `cmd.Wait`；SIGTERM 之后等现有 `reap`（观测 `s.exited` / 已有 done 通道）；`termGrace` 到点仍未退出则 `killPty`，继续等同一个 reap 返回。返回时 `reap` 已执行 `_ = s.f.Close()`，PTY 主端已关、shell 已死。把「再 wait」三个字从括号里拿掉。

#### C2. 族二红回路把「今天应红」绑在立刻 `RemoveAll` 失败上；这个观测在现网经常是绿的，修一半也会绿

- **位置**：spec 红回路 `b234.md:105-108`、implement 门「未红不许改 Close」；活代码 `Host.Close` `internal/ptyhost/client.go:284-307`；`hostproc` `CtrlKill` → `stopNow`（`hostproc.go:396-399, 237-248`）；`Run` defer 里才 `eng.Close`（125–133）；`pty_ws_test.go` LIFO：测试自己的 `pty.Close` cleanup 先于 `t.Setenv("HOME", t.TempDir())` 的 RemoveAll
- **事实**：`Host.Close` 在控制连接上 `WriteControl(CtrlKill)` 后 `ReadFrame`。`CtrlKill` 处理函数只调 `stopNow()`（关 listener + 所有连接，**不杀 PTY**），然后 `return`。控制连接被对端关掉，`ReadFrame` 立刻 EOF。此时 `Engine.Close` **还没开始**——它在 `Run` 返回后的 defer 里。EXIT trap / HISTFILE 写盘发生在 SIGTERM/SIGHUP 之后，也就是 Close 已经返回之后。因此「Close 返回后立刻 `os.RemoveAll(home)`」在今天**经常成功**：目录里还没有 `late`，shell 还没写。Go 的 `os.RemoveAll` 能删非空目录；现场原文 `TempDir RemoveAll cleanup: directory not empty` 是 **RemoveAll 进行中又有人新建文件** 导致最后 `rmdir` 得 ENOTEMPTY，不是「目录非空所以 RemoveAll 失败」。这是竞态，立刻 RemoveAll 把它变成更窄的窗口。
- **为什么承重**：正文写「implement 第一件事，未红不许改 Close」+「今天应红（directory not empty 或 late 文件被占用）」。linux-01 上这条若绿，执行者按纪律不准改 Close，族二整卡卡死。若执行者把判据弱成「修完 RemoveAll 不报错」，则只做「Host.Close 等 `_ptyhost` 进程退出」也会绿：进程死后内核才关 PTY 主端，SIGHUP 写 HOME 发生在 Close 返回**之后**、RemoveAll **之后**——正是 spec 自己在 `b234.md:89` 警告的「只等进程仍不够」。红回路测不出第二道屏障。
- **建议**：今天的红判据改成 Close 返回后立刻断言：**进程仍在，或 `late` 尚未落盘**（二者至少一条，允许竞态里偶发 trap 已写，但不得把 RemoveAll 当红信号）。修完的绿判据维持正文已有的三件套，且顺序写死：`late` **已经**落盘 → `_ptyhost` 与 shell 都不在 → **然后**立刻 `RemoveAll` 成功。禁止用 sleep 拉开窗口。trap 必须经现网 login shell 路径注入（`Write` 或 `InitCommand`），不得另起 `sh -c` 绕过 Engine。

#### C3. `Host.Close` 的 Wait 被写成「接上 Open 那根 `waitDone`」；Adopt / `CloseAll` / 重启后点 × 没有这根通道

- **位置**：spec 方案族二 §2 `b234.md:92`、实现决定 `b234.md:122`、接缝 2 `b234.md:129`；活代码 `Host.Open` `client.go:165-166,201-203`（成功后 `waitDone` 丢掉）；`startClientHost` `client_test.go:234-235`（**Adopt**，不是 Open）；`CloseAll` `closeall.go:57-72`（`New` + `Adopt` + `Close`）；`reclaimPtySessions` 后的 DELETE 走同一条 Adopt 登记
- **事实**：`Open` 里 `go func() { waitDone <- cmd.Wait() }()` 只用来侦测「socket 出现前进程就死了」。成功路径 `cleanupDir = false` 后通道不被保存，goroutine 仍在 Wait（所以不是僵尸泄漏），但 Close 接不上。`configureDetached` 只是 `Setsid: true`（`client_process_unix.go:15-17`），`_ptyhost` 仍是 agentd 的子进程，**Open 路径**上 `cmd.Wait` 合法。Adopt 路径上 ptyhost **不是**当前进程的子进程：Unix 不能对非亲子 `waitpid`。`TestClientCloseRemovesSession`、`TestSurviveAgentdClientRestart` 的 Close 都走 Adopt。`handoff service stop` 的 `closePtySessionsForStop` → `CloseAll` 也是 Adopt。agentd 重启后用户点 × 是 PTY 拆进程的主路径，同样是 Adopt。
- **为什么承重**：按字面只把 Open 的 `waitDone` 接到 `clientSession` 上，接缝 2 点名要改的那条测试（Adopt）的 Close 仍然是 EOF=成功；生产 `CloseAll` 与重启后 DELETE 仍然在 shell 写盘之前返回。故事 3「点 × 之后 shell 已经不在」对活会话在 agentd 重启后不成立——而这正是把 ptyhost 搬出 agentd 的全部意义。
- **建议**：写死两条 Wait：① Open 启动的子进程必须把 `waitDone` 留在会话上，Close 与失败清理共用，禁止丢掉；② Adopt 登记的会话按 `meta.PID`（hostproc 写入的是 `os.Getpid()`，即 `_ptyhost` 自己，`hostproc.go:178-180`）轮询存活，或等 socket/会话目录消失，超时与 ① 同一预算。接缝 2 至少一支走 Open、一支走 Adopt（现成 `TestClientCloseRemovesSession` 就是 Adopt）。`CloseAll` 与 `shutdownPtySessions` 不得再依赖「当前进程是 parent」。

### Important

#### I1. 族一 helper 的 linger/RST 没写死加在哪一侧套接字；只包 Server 接受端挡不住 DefaultClient / websocket 主动关闭

- **位置**：spec 方案族一 §2 `b234.md:71`、接缝 1 `b234.md:128`；活代码全仓零 `SetLinger`；agentd 测试拨号大量走 `http.DefaultClient.Do`（`w3a_testhelpers_test.go:151`、`server_test.go:139`、`forward_test.go`、`pty_api_test.go:105,125` 等）和 `websocket.Dial`（`pty_ws_test.go:23`、`ws_regression_round2_test.go:153`）
- **事实**：TIME_WAIT 落在**主动关闭**的那一侧。`httptest.Server.Close` 关掉已接受连接时，服务端是主动关闭方——在 **Accept 出来的 `*net.TCPConn` 上 `SetLinger(0)`** 会 RST，客户端临时端口不进 TIME_WAIT。测试里 `websocket.Conn.Close` / `DefaultClient` 因 `MaxIdleConns` 淘汰空闲连接时，**客户端**是主动关闭方——只给 Server 设 linger，客户端仍进 30s TIME_WAIT。`httptest.Server.Client()` 的 `CloseIdleConnections` 不影响 `http.DefaultClient`。`client.New`（`internal/client/client.go:256-268`）自建 `Transport`+`DialContext`，也不走 helper 自备 Client。
- **为什么承重**：B234 现场失败集合含 HostGuard / machines 这类 HTTP，也含 PtyWS 的 WS 拨号。只做 Server 侧 linger + helper 自己的 Client，故事 1 的 `go test ./internal/agentd` 仍会被 DefaultClient/WS 的 TIME_WAIT 打断。接缝 1 允许「`TCPConn.SetLinger` 被调用」当探测——linux-01 上这条会绿，Darwin 端口照样耗。
- **建议**：写死 RST 两侧：① helper 的 Listener Accept 后对 `*net.TCPConn` `SetLinger(0)`；② 夹具拨号（见 I2）的客户端连接关闭前同样 linger 0。`ts.Close` 之后 `CloseIdleConnections` 的对象写死为 `http.DefaultTransport` 以及本测试创建过的所有 `*http.Transport`（含 `client.New` 那条），不是 helper 私有 Client 自己一份。

#### I2. 「夹具 Dial 重试」与「禁止重试产品断言」互相打架；不写作用域就会要么假绿、要么重试根本没挂上

- **位置**：spec 方案族一 §3 `b234.md:72`、实现决定 `b234.md:119-120`、故事 4 `b234.md:115`；B193 现场「可伪装成重试次数不对、transport 没注入」（台账 + `docs/superpowers/notes/2026-08-23-a-group-acceptance-result.md:42`）
- **事实**：agentd 测试连 loopback 的路径至少四条：`http.DefaultClient`、`ts.Client()`、`websocket.Dial`（默认 `net.Dialer`）、`client.New` 的产品 `DialContext`（integration 走这条）。产品 `client.New` 明确自建 Transport、`Proxy: nil`，不读 `DefaultTransport`。若重试只包 helper 返回的 Client，integration / PtyWS / 白盒 `getJSON` 全吃不到。若在 `TestMain` 里改 `http.DefaultTransport.DialContext`，`client.New` 仍吃不到，但任何误用 DefaultClient 的产品路径会被重试捂住。
- **为什么承重**：EADDRNOTAVAIL 在 B193 已经伪装成业务断言。重试若包到 HTTP `Do` / 产品重试计数，那些用例假绿。重试若没包到真正在拨号的那几条 Dial，族一在 mac 上照红，linux-01 的「Dial 重试上限」缝只测了没人走的 helper Client。
- **建议**：作用域写死为 **agentd 测试二进制里、目标为 loopback httptest 的 `Dial/DialContext`**，匹配条件仅 `errors.Is(err, syscall.EADDRNOTAVAIL)` 或报文 `can't assign requested address`（Darwin 的 `connectx` 文案）。次数/间隔有上限，超限错误原文保留这族形状。禁止包 `Client.Do`、禁止改生产 `client.New`。`client.New` 在 **agentd 测试**里连 httptest 时，用测试缝换 Dial（或测试里构造 Client 时注入同一 Dialer），不要改 `internal/client` 生产默认。接缝 1 必须有：DefaultClient、websocket.Dial、client.New 三条拨号各至少一次打到重试上限文案。

#### I3. 「所有 agentd 测试服经 helper」没有闭包清单；`NewUnstartedServer` 与黑盒包会漏网

- **位置**：spec `b234.md:70,119`；活代码 `internal/agentd` 内 TCP 测试服 21 处：
  - 白盒夹具：`w3a_testhelpers_test.go:91`
  - 黑盒夹具：`server_test.go:69`（`package agentd_test`）
  - 其它：`ledgerapi_test.go:185`、`integration_test.go:86,700`、`hostguard_test.go:37`、`diffbase_test.go:62`、`render_stream_test.go:41`、`mirror_test.go:32,39`、`machineupgrade_test.go:24`、`cardstep_local_test.go:44`、`cardstep_discipline_test.go:38,227`、`forward_test.go` 五处 remote、`ws_regression_round2_test.go:96` 与 **`:102 httptest.NewUnstartedServer`**
- **事实**：`*_test.go` 且 `package agentd` 的符号，`package agentd_test` 看不见。helper 若只写进 `w3a_testhelpers_test.go`，黑盒 `newTestEnvWithCfg` 无法调用。`NewUnstartedServer` 不是 `NewServer`，grep 迁调用点会漏。`httptest.NewRequest` / `NewRecorder` 不占端口，不是漏网，不必迁。
- **为什么承重**：正文用「所有」但落点归 plan。执行者迁了点名的 `newTestAgentdEnv*` / `newTestEnv*` / forward/cardstep/machineupgrade，仍可能留下 `NewUnstartedServer`、`integration_test.go:700`、`hostguard`、`mirror`、`render_stream`。故事 1 单包复跑仍偶发。
- **建议**：spec 写死 helper 落在可被 `agentd` 与 `agentd_test` 同时导入的非 `_test.go` 包（正文已允许「测试辅助包」，把「必须」写上）。接缝 1 的调用方改成闭包清单（上列 21 处，含 Unstarted），并加负例：`go test` 后 `internal/agentd` 内不得再出现未包一层的 `httptest.NewServer` / `NewUnstartedServer`。

#### I4. Host.Close 等待预算与 `termGrace` 都写 2s，从 CtrlKill 起算会对不齐，DELETE 会偶发 500

- **位置**：spec `b234.md:91-94`；`termGrace` `engine.go:40`；`statWait` `client.go:42`；`handleDeletePtySession` `pty_api.go:249-257`（Close 非 `ErrNoSession` → 500）；`ptyShutdownWait` / `ptyCloseBudget` 都是 2s
- **事实**：CtrlKill → `stopNow` → Accept 循环退出 → defer 才进 `Engine.Close`（内部最多再等 2s）→ 删 socket / 锁 / 会话目录 → 进程退出。Host 若从发 kill 起设 2s 截止，Engine 侧 2s 宽限尚未结束进程就还在，Host 报错、`forget`、HTTP 500，进程随后才退。前端 `deletePtySession`（`web/src/api/client.ts:648-652`）无超时，500 会变成 × 失败再点一次 404。
- **为什么承重**：故事 3 要求 DELETE 200 且 shell 已不在。预算对不齐就把「等收摊」变成「偶发关终端失败」。`shutdownPtySessions` 总预算 2s 正文已允许打到超时日志；单条 DELETE 没有这条豁免。
- **建议**：Host.Close 等待预算写死为 **覆盖 `termGrace` + hostproc defer 收摊**（删 socket/锁/目录），从 **CtrlKill 被对端处理后**起算，或明确 `termGrace + 一个短余量`。生产 `http.Server` 的 ReadTimeout 30s / WriteTimeout 11min（`cmd/agentd.go:412-414`）吃得下，不必改生产 HTTP 超时（正文已禁，保持）。

#### I5. 派 linux-01 做实现门，族一在 Linux 上不会红；接缝若只锁「调用了 SetLinger」就会假绿合 main

- **位置**：spec 备注 `b234.md:153`、故事 4 `b234.md:115`、用户授权「独立审查后吸收、无人值守推到合 main」
- **事实**：Linux `ip_local_port_range` 通常宽于 Darwin 16384，且本卡只收口 agentd 测试服，linux-01 上 `go test ./internal/agentd` 本来就绿（B234 现场）。接缝 1 允许 `netstat` **或** `SetLinger 被调用`。后者在 linux-01 上与「Darwin 临时端口被 RST 释放」不是同一命题。Darwin 上 linger 0 的 abortive close 是 POSIX 常规手段，能让**设了 linger 的那一侧**跳过 TIME_WAIT；偶发 loopback RST 丢失（cpython #153117，约百万分之一）不构成换方案的理由，但机制测试必须测到「连接关闭后客户端源端口不留 TIME_WAIT」，而不是「函数被调用过」。
- **为什么承重**：无人值守合 main 的实现/审查在 linux-01。族一的产品门是 mac 本机复跑。正文把 mac 复跑写在备注，没写进合入门。按「linux-01 绿 = 实现门过」会把未在 Darwin 验证的 linger 夹具推进 main，B234 的验收手段继续废。
- **建议**：写死两道门：linux-01 锁机制测试（linger 两侧都设上、Dial 重试上限文案、族二红回路修完三件套）；**合 main 前**必须有 mac 本机 `go test ./internal/agentd -count=1` 非 EADDRNOTAVAIL 的复跑记录。接缝 1 在无法 `netstat` 的 Linux 上允许用「测试内建的连接表/hook 证明 SetLinger(0) 发生在 Accept 与客户端 Dial」，但不得作为 Darwin 行为的替代验收。

#### I6. 接缝清单漏了 `CloseAll` / `closePtySessionsForStop`；`Engine.Close` 也没有自己的缝

- **位置**：spec 接缝 2–3 `b234.md:129-132`；生产调用 `internal/ptyhost/closeall.go:72`、`cmd/service.go:204,254-261`、`internal/agentd/ptyreclaim.go:105`、`internal/agentd/pty_api.go:249`；`Engine.Close` 的生产调用方是 `hostproc.Run` defer（`hostproc.go:129-132`），图里几乎看不到这条边
- **事实**：图上 `n_ptyhost_Host_Close` 的真生产调用是 `handleDeletePtySession`、`shutdownPtySessions`→`Host.Close`、`CloseAll`→`Host.Close`。`Host.Attach`/`Host.Write` 图里也有到 `Host.Close` 的边，对着的是 `conn.Close()`（`client.go:269,331`），是假边。测试 Cleanup 不在图里，spec 已声明，对。`Engine.Close` 的异步 SIGKILL 图覆盖不到，必须以源码为准，spec 已写，对。
- **为什么承重**：只锁 `handleDeletePtySession` 会让 `handoff service stop` 继续走「EOF=成功」的 Close（在 C3 的 Adopt 路径上）。只锁 `Host.Close` 不锁 `Engine.Close` 等待 reap，执行者可能只改客户端 Wait（C2 的假绿）。
- **建议**：接缝 2 扩成：`Host.Close` 的三条生产调用方（DELETE / `shutdownPtySessions` / `CloseAll`）各至少一支；另加 `Engine.Close` 一条：返回后 shell 已不在（可用现成 `engine.TestCloseRemovesSession` 升级为等 `reap`，或红回路下沉到 Engine）。`shutdownPtySessions` 2s 总预算不动的豁免保持，但 `CloseAll` 的 `ptyCloseBudget=2s` 必须同样写一句「超时只打日志，不把 Close 改回异步」。

### Minor

#### M1. TIME_WAIT=138 与「池子 16384 耗尽」不能靠那个计数本身收成同一 bug；正文已不把它当回路，表述仍略混

- **位置**：spec 问题陈述 `b234.md:37`、现状 `b234.md:46-47`；对照：Darwin 默认 `49152–65535`（16384）成立；2×MSL≈30s、546 端口/秒是算术正确。整机 TIME_WAIT 真要耗尽需要上万条（公开复现里出现过 17546 条 TIME_WAIT 才 EADDRNOTAVAIL），138 只是当时一个快照。
- **事实**：EADDRNOTAVAIL 在 Darwin `connect()` 上就是「分不出本地源端口/地址」。ESTABLISHED / FIN_WAIT / 生产 agentd / 其它进程都算占用，只数 TIME_WAIT 会低估——这句对。138 本身证明不了耗尽。
- **建议**：问题陈述改成「当时 TIME_WAIT=138 只说明机制未查清，本卡不采信该计数；根因是连接创建速率 × Darwin 回收 + 整机占用」。不挡批准。

#### M2. `Host.Open` 注释仍写「agentd 不等待它退出」

- **位置**：`client.go:124`
- **事实**：这是 Open 的语义（启动后不等），与 Close 要等不矛盾，但落地时容易被读成「整条 Host 都不 Wait」。
- **建议**：plan 里改成「Open 成功后不阻塞在启动路径；显式 Close 必须 Wait」。

#### M3. 包内 `t.Parallel` 现状读数正确

- **位置**：spec `b234.md:44`；活代码 agentd 无 `t.Parallel()` 调用，仅 `ws_regression_round2_test.go:427` 注释提到它。
- **建议**：无。

#### M4. 前端 DELETE 无短超时，2s 挂起不会被浏览器掐掉

- **位置**：`web/src/api/client.ts:127-134,648-652`；`Shell.tsx:298`
- **事实**：`fetch` 无 AbortTimeout；agentd WriteTimeout 11min。废止「DELETE 不该挂 2s」不会撞上 HTTP 层超时。列表「立刻没有」是 `forget` 时机，与 DELETE 200 可以不同步——正文「列表可以立刻摘、收摊等待在 Close 返回前」与 handler 同步等 Close 兼容（200 时列表已空）。
- **建议**：无。不要为了动画去改前端。

#### M5. W4 plan 里仍有被废止的那段注释原文

- **位置**：`docs/superpowers/plans/2026-08-12-w4-pty-terminal.md:1127-1128`（历史稿，当时 Engine 还叫 Host、还在进程内）
- **事实**：不是冻结 wire。本卡废止的是 `engine.go:297-298` 的产品注释。
- **建议**：吸收时在 spec 备注一句「W4 plan 那段是历史引用，不回写 plan 正文」。

## 3. 定级：L2 成立，不抬 L3

独立判断与 spec 同结论。

- **族一只动测试 HTTP 夹具，不改生产 `http.Server` / 生产客户端 linger**：与代码对照成立。全仓零 `SetLinger`。
- **族二改的是进程内导出符号 `Host.Close` / `Engine.Close` 的等待**（架构法第四条 1 档）。`k_ptyhost_Host` / `k_engine_Engine` / `k_hostproc_*` 在 `d_sessions`，`k_agentd_Server` 在 `d_gateway`（`codegraph/best.json`）。这是领域之间的 import + 导出方法，不是跨子系统 wire。不值得为「等到子进程死」付契约冻结成本（架构法第一条实操裁决）。
- **HTTP 面不变**：`DELETE /api/pty/sessions/{id}` 仍是 200 `{ok:true}` / 404 / 500。变的是 200 的时机（杀已发出 → 进程已收摊）。前端无 2s 超时。不是新路径、不改任务/工单状态机、不改 WS 帧。
- **`Engine.Close`「DELETE 不该挂 2 秒」不是已冻结跨子系统契约**：它是 `engine.go` 的产品注释，从 W4 进程内 Host 抄来。ptyhost 拆进程之后，`Engine.Close` 已经不在 HTTP handler 栈上；本卡把等待重新接到 `Host.Close`，是显式废止旧产品意图，不是改 target.json 里的契约面。必须在实现里改掉该注释（正文已要求），不必抬 L3。
- **不是 L1**：linger 接到全部 agentd 测试服、Close 接到已有 `cmd.Wait`、hostproc defer 等 reap、Adopt 非亲子等待，plan 不会只复述三行。

不因「agentd 与 ptyhost 分属 `d_gateway` / `d_sessions`」抬 L3。也不因 DELETE 200 时机变化抬 L3。

## 4. 接缝清单：假缝少，漏缝与错观测是问题

| 缝 | 符号 + 调用方 | 判定 |
|---|---|---|
| 1 测试 HTTP helper linger/RST + Dial 重试 | 新建符号 ← agentd 全部 loopback 测试服 | 真缝。假缝禁令「纯函数算不算 EADDRNOTAVAIL 不占缝」写对了。**漏闭包清单与 Unstarted（I3），漏 DefaultClient/WS/client.New（I1/I2），Linux 上 SetLinger 被调用 ≠ Darwin 端口释放（I5）。** |
| 2 `Host.Close` + `handleDeletePtySession` | `internal/ptyhost/client.go#Host.Close` ← DELETE | 真缝。控制连接超时不再当成功，必须锁。**漏 `shutdownPtySessions`、`CloseAll`（I6），漏 Adopt 路径（C3）。** `TestClientCloseRemovesSession` 要收成 shell 已死——这条测试今天走 Adopt，与「接 Open 的 waitDone」对不上。 |
| 3 PTY HOME cleanup | `pty_ws_test.go` 是消费方 | 真缝，不是假缝。红回路观测写错（C2）。「agentd PtyWS 不需要每条都复制 trap」对。 |

假缝：Unix 套接字拨号本期不做、纯函数识别 EADDRNOTAVAIL，合格。

`Engine.Close` 等待 reap 没有单独占缝——按 C1/C2 它必须能被红回路或 engine 测试打红，否则只改 Host 会假绿。

## 5. 弃选：站得住

| 弃选 | 审查意见 |
|---|---|
| 这族错误 `t.Skip` / 全量判据「按形状认」 | 站得住。B234 的存在理由。 |
| 只给 Darwin `go test -p 1` | 站得住。`./internal/agentd` 已是单进程，B234 仍红。 |
| 全仓 Unix 域套接字替换 httptest | 站得住。根因方向对，`ts.URL`→`ws://` 面太大。 |
| 共享进程级 httptest.Server | 站得住。Manager/配置/Store 隔离会被拆掉。 |
| `DisableKeepAlives: true` 当修法 | 站得住。每请求新连接，端口压力更大。 |
| 调大 `portrange` 写进手册 | 站得住。环境补丁。 |
| 本期改完全仓 115 处 `httptest.NewServer` | 站得住。OOS 已记。审查在工作树看到的 `NewServer` 调用远多于 115 行匹配（grep 截断前已上百），「115」是当时计数，以「其余包以后自己迁」为准，不必在 spec 核这个数字。 |
| 用例 sleep / 重试 RemoveAll | 站得住。代理条件。 |
| 只给测试加 `WaitClosed`、生产 Close 保持 EOF=成功 | 站得住。DELETE 与 Cleanup 必须同一条 Close。 |
| Close 加 `wait=false` | 站得住。双语义假绿。 |
| HOME 改走固定目录躲 cleanup | 站得住。 |

## 6. 与源卡现场对齐；两族没有混成一个修法

未用 `handoff card show`。按 spec/台账/B193 验收笔记：

| 现场 | 代码 | 本卡修法 |
|---|---|---|
| `go test ./internal/agentd` 偶发 `can't assign requested address`，失败用例每次不同，基线同样红，linux-01 绿 | 夹具已是 `httptest.NewServer`+`t.Cleanup(ts.Close)`；包内无 `t.Parallel` | 族一 linger/RST + 夹具 Dial 重试。对齐。 |
| TIME_WAIT 当时 138，机制当时未查清 | 16384 池、msl 15s 与 Darwin 默认相符；138 单独不够耗尽 | 不把该计数当回路。对齐（表述见 M1）。 |
| 全量并发同文案可伪装成别包业务断言 | 其它包大量 `httptest.NewServer`（client/cmd/executor/release） | 本期只收口 agentd；OOS 记识别纪律。对齐。 |
| `go test ./internal/agentd/ -count=12 -run TestPtyWS` 是池子已空条件下的确定性 | 不是干净会话 12 轮必红 | 正文已禁写错判据。对齐。 |
| PtyWS 用例体过、cleanup `directory not empty`；落点 ResumeSince/Resize/Echo/`TestForwardWSPtyEndToEnd` | HOME=`t.TempDir()` + `base_kind: home`；LIFO 先 Close 再 RemoveAll | 族二收摊。对齐。Close 语义才是根因，顺序已经对。 |
| B193 当年不派 linux-01 因为沙箱 `/tmp` 只读 | B202 `ptytestroot` 已按可写性择位 | 该理由作废。对齐。Darwin 端口耗尽 linux-01 仍不会出现——正文已写，对齐。 |

**没有混成一个修法。** 族一不动生产 HTTP，族二不动测试端口。禁止 Skip/sleep 两条都写了。

## 7. 指定核对题（独立确认 / 证伪）

1. **现状读数是否与活代码一致？**
   **基本确认，有几处精度问题。** `newTestAgentdEnvWithCfg` / `newTestEnvWithCfg` / ledger 夹具确是 httptest+Cleanup Close。`Host.Close` EOF/超时当成功（`client.go:302`，`isTimeout` 637–640）属实。`statWait=1s` 属实。`stopNow` 不杀 PTY 属实。`Engine.Close` 立刻返回 + 异步 SIGKILL + 注释原文属实。`Host.Open` 的 `waitDone` 成功后丢掉属实。`ptyShutdownWait=2s` 属实。`pty_ws_test.go` HOME=TempDir + base_kind home 属实。`t.Parallel` 仅一处注释属实。图 who-calls 含 `handleDeletePtySession` / `CloseAll`；台账写的 `pumpPtyUplink` 是假边（`conn.Close`）。`Engine.Close` 异步路径图覆盖不到，spec 已标，对。

2. **族一：TIME_WAIT=138 与池子耗尽能否收成同一 bug？linger 0 在 Darwin 上能否释放临时端口？夹具 Dial 重试会不会把产品拨号假绿？agentd 是否还有不经 helper 的 httptest 漏网？**
   **138 不能当耗尽证据（M1）；机制类「源端口分配失败」成立。** linger 0 在 Darwin 上对**设了 SO_LINGER 的那一侧**是 abortive RST、跳过 TIME_WAIT 的常规手段；只设 Server 侧不够（I1）。Dial 重试若不限 EADDRNOTAVAIL、或包到 `Do`/产品重试计数，会假绿（I2）。漏网清单见 I3（含 `NewUnstartedServer`）。forward/cardstep/machineupgrade 正文已点名，不是漏，但不是闭包。

3. **族二：只等 `_ptyhost` 是否仍不够？`Engine.Close` 改等待会不会打穿 2s 关停预算或 DELETE 产品语义？`waitDone` 丢掉是否属实？`isTimeout` 当成功是否属实？**
   **只等进程不够，spec 自己写对了；红回路却测不出这一点（C2）。** `Engine.Close` 等待最多 2s，`shutdownPtySessions` 总预算 2s 并发 Close，最坏打到超时日志——正文已允许，不构成否决。单条 DELETE 会从「毫秒级 200」变成「最多约 termGrace 的 200」，这是故事 3 的显式产品变更，前端无短超时，不打穿 Read/WriteTimeout。`waitDone` 丢掉属实。`isTimeout` 当成功属实。`cmd.Wait` 双重调用是正文没写清的坑（C1）。Adopt 无 Wait（C3）。预算对不齐会导致 500（I4）。

4. **接缝清单：假缝？缺生产调用方？红回路能否在 linux-01 变红？**
   假缝禁令合格。缺 `CloseAll` / `shutdownPtySessions` / `Engine.Close`（I6）。红回路在 linux-01 **能跑**（B202 择位，不依赖 Darwin 端口），但按现在的「立刻 RemoveAll」**不保证今天红**（C2）。这是 linux-01 上族二的真风险，不是族一那种「Linux 无此 bug」。

5. **L2 vs L3：`d_sessions` + `d_gateway` 是否必须抬级？Engine.Close 注释算不算已冻结契约？**
   **不抬 L3。** 见 §3。注释是产品意图，本卡废止它；不是 target.json 契约面。

6. **派 linux-01 是否让族一假绿？实现门只靠机制测试是否够？**
   **够当实现门的一半，不够当合 main 门。** 见 I5。族二红回路修完三件套在 linux-01 上是真门（前提是 C2 改观测）。族一必须另有 mac 复跑。

7. **OOS 是否漏了会让落地做错的东西？**
   全仓其余 httptest、Skip 禁令、生产 linger、PTY 改 unix socket、from_seq/镜像/状态机、重装生产 agentd、charter 卡 C7/C8/C11——这些漏了不会让**本期**做错题。漏的是合 main 的 mac 门（I5）和 helper 对黑盒包可见（I3），应写进正文而不是 OOS。roadmap 此刻还没有「来自 B234 spec」段（spec 未吸收），吸收时两条后续（迁其余 httptest、Darwin 全量仍可能伪装）要落 `docs/roadmap.md`。

8. **二解测试：关键陈述是否仍有两种不兼容的承重解释？**
   **有，且都承重：**
   - 「等到 reap（`cmd.Wait`）」= 等现有 reap **vs** Close 里再 Wait（C1）
   - 「夹具 Dial」= helper 私有 Client **vs** 所有连 httptest 的 Dial（I2）
   - 「接上 Open 的 Wait」= 只修 Open **vs** Adopt 也要等（C3）
   - 「RST 离开」= Server Accept **vs** 客户端也要 linger（I1）
   - 红回路「今天应红」= RemoveAll 失败 **vs** 进程仍在/late 未落盘（C2）

## 8. Out of Scope / roadmap

spec OOS 两条后续（迁其余 httptest；Darwin 全量未迁包仍可能伪装）与「永不做」六条未混进本期方案，合格。`docs/roadmap.md` 尚无 B234 段——审查对照的是未吸收 spec，不记为正文缺陷；吸收时必须落账。

「重装生产 agentd」放进永不做/划界是对的：测试包复跑不依赖现役二进制；真机点 × 要用含 Close 修法的二进制。不要把部署偷渡进本卡。

## 9. 图覆盖债

与 spec 备注一致，独立核：

- 测试夹具与 `httptest.NewServer` 不在图里。
- `n_ptyhost_Host_Close` 在 `d_sessions`（`codegraph/best.json` `k_ptyhost_Host`）。flow 能看到 `handleDeletePtySession` → `Host.Close`、`CloseAll` → `Host.Close`。
- 图把 `Host.Write`/`Host.Attach` 里的 `conn.Close()` 收成对 `n_ptyhost_Host_Close` 的 call（`client.go:269,331`），who-calls 会掺假边。`pumpPtyUplink` 不调 `Host.Close`。
- `n_hostproc_Run` 的 steps 没有画出 defer 里的 `eng.Close`；`n_engine_Engine_Close` 的异步 SIGKILL goroutine 无图。以源码为准。
- 审查未跑 `go run github.com/Xsxdot/charter/graph/cmd/codegraph` CLI，who-calls/sym 是读 `codegraph/baseline.json` + `best.json` 对的；与「测试夹具无图」一起记债。

## 10. 批准前最小补丁（只改 spec 正文，不是代码）

1. **C1**：`Engine.Close` 写死等现有 `reap`，禁止第二次 `cmd.Wait`；SIGKILL 仍由 Close 在 `termGrace` 后发给同一进程组，然后继续等那个 reap。
2. **C2**：红回路今天的红 = Close 返回后进程仍在或 `late` 未落盘；修完 = `late` 已落盘且两级进程都不在且立刻 RemoveAll 成功。未红不许改 Close 继续成立，但红信号必须改。
3. **C3**：Host.Close 对 Open 子进程 Wait + 对 Adopt 按 PID/会话目录 Wait；接缝 2 两条路径都要锁；`CloseAll` 算生产调用方。

建议一并写入，否则 plan 仍会分叉：

4. **I1+I2**：RST 两侧；Dial 重试作用域（DefaultClient / websocket.Dial / 测试中的 client.New，仅 EADDRNOTAVAIL）；`CloseIdleConnections` 打在 DefaultTransport 与自建 Transport。
5. **I3**：helper 必须对 `agentd` 与 `agentd_test` 可见；21 处 TCP 测试服闭包（含 `NewUnstartedServer`）。
6. **I4**：Host.Close 等待预算覆盖 `termGrace` + hostproc 收摊，避免与 2s 对不齐变成 DELETE 500。
7. **I5**：合 main 门写上 mac `go test ./internal/agentd`；linux-01 只当机制门。
8. **I6**：接缝覆盖 `shutdownPtySessions` / `CloseAll` / `Engine.Close` 等待。

M1–M5 不挡批准。

方向保持：族一只收口 agentd 测试服 + RST/短重试；族二 Close 返回 = shell 已死且不再写盘；不 Skip、不 sleep、不双语义 Close、不改生产 linger、不抬 L3。
