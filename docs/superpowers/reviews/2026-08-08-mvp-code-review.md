# handoff MVP 代码审阅报告

日期：2026-08-08
范围：commit `605bdfb`..`bd0a1c6`（Task 1–13 全部实现）
方式：4 名独立审阅者分区并行审阅（协议/存储、opencode 挂载、中介/工作区/CLI、韧性与纪律），多数结论带失败探针复现
结论：**不通过——需修复后复审**

---

## 一、总体评价

工程质量在平均线之上，问题不在「写得糙」，而在**几处并发时序与失败路径**，以及**核心诉求之一未真正交付**。

做得好的地方（不是客套，是判断依据）：

- `store.UpdateTaskState` 用 read → `CanTransit` → **CAS 写**（`WHERE id=? AND state=?`），`RowsAffected()==0` 映射 `ErrBadTransit`；配套 20 轮双 goroutine 竞争测试断言「恰好 1 胜 1 败」。这是全仓最扎实的一段。
- `hub` 的 remove+close 与 send 都在同一把锁内，从结构上消灭了 send-on-closed-channel，且理由写在注释里。
- `AnswerTicket` 以 `answer IS NULL` 作存储层守卫，双重应答在 SQL 层就不可能。
- adapter 的事件通道单一所有权（`subscribeLoop` 是唯一 closer，看门狗只 cancel 不 close），这是最容易写错的地方，写对了。
- `RunCmd` 的 `runOutputBuffer` 有界保留且持续排空、`Write` 返回 `len(p)`，不会把子进程堵死。
- 测试基本都是真行为测试：真 httptest + 真 SSE 分帧、真 `git init` 仓库、真 HTTP/WS 跑通全环。不是同义反复。
- **纪律审计满分**：31/31 非测试文件有中文「职责/边界」头注释；107/107 导出符号有 doc 注释；无 `fmt.Printf` 当日志；日志无任何密钥泄漏；注释写的是「为什么」不是「做了什么」；热循环无高频日志。

---

## 二、P0 阻塞项（必须修，全部已复现）

### P0-1 WS 补发→订阅 之间的窗口期事件永久丢失，唤醒链断裂

`internal/agentd/server.go:602-628`

`handleEvents` 先从 DB 读历史（602）→ **把历史全部写进 socket**（617-623）→ 才 `hub.Subscribe`（628）。这个窗口不是几条指令，而是整个重放写循环，会因 TCP 背压阻塞任意久。窗口期内 `Publish` 的事件订阅者为零，被 `hub.Publish` 直接丢弃。

代码注释声称「客户端凭 seq 重连补拉」——**不成立**。`client.WaitEvent`（`internal/client/client.go:419-434`）在健康连接上阻塞于 `conn.Read`，只有连接**出错**才重连。而丢掉的偏偏是 `question` / `permission_request`：任务随即进入 `waiting_answer` 不再产出任何事件，连接永远健康，审核者永远不被唤醒，executor 阻塞到 2h 看门狗。

复现（5000 条积压 + 不读取的客户端，重放期间 Publish 一条 question）：
```
共收到 5000 条事件, 收到 question=false
BUG: 窗口期 Publish 的 question 事件（seq=5001，已落库）永久未送达
```

修法：**先 Subscribe 再读历史**，重放完成后排空实时通道并跳过 `ev.Seq <= lastReplayedSeq`。现有 `server_test.go:299 TestWSReplayThenLive` 是在重放排空**之后**才 Publish，结构上不可能覆盖这个交接点——必须补一个「重放期间 Publish」的测试。

### P0-2 `handoff wait` 对永久性失败无限重试，静默挂死

`internal/client/client.go:350-379`

`WaitEvent` 把**每一个** `waitOnce` 错误都当成瞬时错误，包括握手 401。生产路径 `cmd/root.go:39` 用 `rootCmd.Execute()`（非 `ExecuteContext`），ctx 是 `context.Background()`，没有任何 deadline。而 token 不同步正是**文档里写明的手工配对步骤**——最可能的配置错误，表现为 `wait` 永久挂起，和「还没有事件」完全无法区分。同理 `wait <打错的 task-id>`：`server.go:567` 从不校验任务是否存在，也是永久阻塞。

修法：401/403（建议含 400）立即返回错误而非退避；`handleEvents` 校验任务存在，不存在则以 policy-violation 码关闭；补一个可选 `--timeout`。

### P0-3 `RunCmd` 向已回收的进程组发 SIGKILL，约 38% 的成功命令中招

`internal/agentd/workspace.go:237,258-270`

`defer cancel()`（237）在 `close(cmdDone)`（270）**之后**触发，此时回收 goroutine 通常还停在 `select`，两个 case 同时就绪，随机走到 `case <-ctx.Done(): killProcGroup(cmd.Process.Pid)` ——**无条件**杀一个 `cmd.Wait()` 已经回收的 pid。通常是 ESRCH，但一旦 OS 把该 pid 复用为某个进程组组长，handoff 就会 SIGKILL 掉 executor 机器上一个毫不相干的进程组。不确定、不可归因，且几乎每次 `handoff run` 都在赌。

复现：`killProcGroup called 114 times for 300 successful commands`。

修法：回收 goroutine 在进程已回收后必须不杀——`case <-ctx.Done(): select { case <-cmdDone: return; default: killProcGroup(...) }`，或用在 `close(cmdDone)` 前置位的 atomic 标志守卫。

### P0-4 opencode server 密码进 argv，本机任意用户 `ps` 可见

`internal/executor/opencode/proc.go:88-92`

```go
exec.Command("tmux", "new-session", ..., "-e", "OPENCODE_SERVER_PASSWORD="+password, ...)
```

密码是 `tmux` 客户端进程的字面命令行参数。Linux 上 `/proc/<pid>/cmdline` 默认全局可读，任何本地用户轮询 `ps` 即可拿到，随后就能以 handoff 的身份操作 opencode server：批准权限、向 executor 注入 prompt、在用户仓库里跑任意 `bash`。spec 把「127.0.0.1 + 随机密码」当作安全边界，argv 泄漏正好把这条边界作废。密码同时进了 tmux 会话环境（`tmux show-environment -t handoff-<id8>` 可读）。

修法：把启动写成 `taskDir` 下的 0600 启动脚本，argv 里只剩脚本路径：

```sh
#!/bin/sh
export OPENCODE_SERVER_PASSWORD='...'
export OPENCODE_CONFIG='...'
exec opencode serve --port N --hostname 127.0.0.1 2>&1 | tee -a "$TASKDIR/serve.log"
```

顺带解决 P1-8（那条 `tee`）。

### P0-5 `reply` 中继失败仍返回 `ok:true`，且工单从此再也答不了

`internal/agentd/server.go:264-281`

顺序是 `AnswerTicket`（落库，`answer IS NULL` 守卫被消耗）→ `NotifyAnswer` 返回 false → `RelayAnswer` **失败仅记日志** → `writeJSON(200, ok:true)`。

失败场景：agentd 重启时有挂起权限工单且探活失败 → `RelayAnswer` 找不到运行中的任务 → CLI 打印 `{"ok":true}`。工单已应答，从 `attach` 的 `pending_tickets` 里消失；再 `reply` 得 404；`continue`/`done` 因状态不是 `waiting_review` 得 409。**CLI 层已无任何补救路径**，审核者唯一的信号是远端 `agentd.log` 里一行他没理由去看的日志。这正是 `RelayAnswer` 本来要防的「回答已落库但 executor 永远阻塞」，只是下沉了一层。

修法：中继结果进响应体（`{"ok":true,"relayed":false,"reason":...}`），中继失败记 Error 并返回非 2xx；或中继失败时把工单回滚为未应答。

---

## 三、P1 应修项

| # | 问题 | 位置 | 影响 |
|---|------|------|------|
| 1 | `EventsFrom` 是 `ORDER BY seq DESC LIMIT` 取**最新 N 条**，用于 WS 重放时静默截掉**最旧**的事件，而 cursor 只会前进，缺口永不补齐 | `store.go:298-302` + `server.go:602` | 违反「事件不丢」这条 cursor 设计赖以成立的不变量。另：`server.go:610-616` 的截断告警恒不触发（`replays[len-1]` 就是 latest），是死代码。修法：给 WS 重放另开 ASC 变体，DESC 语义只留给 `attach` |
| 2 | manager 先 `Publish` 后注册 waiter，中间隔两次 SQLite 往返 | `manager.go:407-417`、`463-472` | 探针 1/60 轮复现：任务停在 `waiting_answer` 但 `pending_tickets=0`，且 waiter 用 `context.Background()` 永久泄漏。修法：先注册 waiter、先置 `waiting_answer`，再 `Publish`；wait goroutine 用可取消 ctx |
| 3 | opencode 一元 HTTP 调用无超时（与 SSE 共用无 Timeout 的 client），且 manager 传 `context.Background()` | `api.go:69-76`；`manager.go:339,345,444,493` | 半死的 opencode（TCP 通但不响应）会让 `handoff reply` 在审核者终端永久挂起——agentd 侧 `http.ListenAndServe` 也没有任何 server timeout。修法：一元调用单独一个 `Timeout: 30s` 的 client |
| 4 | **tmux 第二窗口 `tail -f render.log` 从未实现** | 全仓无 `new-window`/`split-window` | `render.log` 写了，但没人 tail。`tmux attach` 看到的是 serve 自己的输出，不是模型文本。**spec 三大核心诉求之一「用户能打开终端看到 executor 实况」实际未交付**，而 README:90 已宣称可用，e2e 清单该项必挂 |
| 5 | `ReadFile` 不防符号链接逃逸，且无大小上限 | `workspace.go:161-188` | `repo/notes.md -> /outside/id_rsa` 与目录软链均可读出仓外内容（已复现，`err=nil`）；64MiB 文件整读进内存再 JSON 编码，一次 `fetch` 就能 OOM agentd。修法：`filepath.EvalSymlinks` 前缀校验或 `os.OpenRoot`；`io.LimitReader` 复用 `maxRunOutput` |
| 6 | ticket id 直接用裸 `PermissionID`，未按 task 命名空间隔离 | `manager.go:398-404` | 跨任务 permissionID 相同时 `INSERT OR IGNORE` 静默无操作，第二个任务**永久无法应答**且 `attach` 显示 0 挂起项（fake executor 下已复现）。修法：`taskID+":"+permID` 作 ticket id，`RespondPermission` 仍传裸 permID |
| 7 | `CreateTicket` 返回的 `created` 被丢弃 | `manager.go:401-417` | SSE 重连重放同一权限时仍会追加第二条事件、二次迁移、二次起 waiter，审核者被重复唤醒、`RespondPermission` 被调两次。幂等只做了一半 |
| 8 | `capturePaneTail()` 在 serve 死亡时**必然为空** | `proc.go:113-118,171-177`；`adapter.go:425-430` | tmux 会话随其唯一命令退出而销毁，`capture-pane` 直接报错。于是「启动超时」「serve 已退出」两类错误都不带 stderr，违反 spec §8。修法：serve 输出重定向到 `<taskDir>/serve.log` 读尾部（P0-4 的启动脚本一并解决） |
| 9 | `Stop()` 时 `Kill()` 失败仍无条件 `drop` 运行态 | `adapter.go:388-400` vs `407-413` | 注释声称「kill 失败保留运行态待重试」，但 `subscribeLoop` 的 defer 已无条件 drop。已复现：serve 变永久孤儿，占着端口和模型会话，无任何途径回收 |
| 10 | SSE backoff 永不复位，且断连期间新产生的 permission 无补拉 | `api.go:272-296` | backoff 爬到 30s 上限后终身不降；`/event` 无 `Last-Event-ID` 无重放，间隙内的 `permission.updated` 永久看不见 → opencode 永久等决策 → 挂到 2h 看门狗 |
| 11 | `Events(taskID)` 对已终结任务返回 `nil` | `adapter.go:320-325` + `manager.go:358` | `Dispatch` → `go mediate` 的调度窗口内 serve 若死亡，`for ev := range nil` 永久阻塞，缓冲区里的 `result{OK:false}` 永不投递，任务停在 `running` 且无 failed 事件。修法：返回已关闭的 channel（契约本就是「通道关闭 = 执行终结」） |
| 12 | `PlanSummary` 从未被写入 | `manager.go:186-195` | 字段一路打通到 store 和 API，但 `Dispatch` 只写 `PlanPath`（agentd 侧路径，审核者读不到）。**违反 spec §7 验收标准**：全新会话能恢复状态和挂起项，却无法知道这个任务本来要干什么 |
| 13 | `handoff run T1 go test -v ./...` 直接报错 | `cmd/run.go:22-41` | cobra 解析交叉 flag，`-v`/`-race`/`-run X` 全部失败。审核者最主要的验证动作不可用。修法：`SetInterspersed(false)` |
| 14 | Dispatch 失败一律扁平化为 500 | `server.go:342-349` | `ErrDirtyWorktree`——最常见且审核者一条命令就能修的拒绝原因——只呈现为 `{"error":"派发任务失败"}`，真实原因只在（可能是远端的）`agentd.log` 里 |
| 15 | 看门狗两个盲区 | `watchdog.go:101-121` | (a) `handleReply` 不追加事件，因此「已 stalled → 审核者回答 → executor 仍然死着」这个**最需要二次告警**的场景永远不会再告警；(b) 零事件任务（`ErrNotFound`）被永久跳过，即「静默挂起」这一类根本不受监控。修法：以 `task.UpdatedAt` 作兜底基线 |
| 16 | 恢复为 dead 的任务仍保留挂起工单 | `watchdog.go:177-188` | `attach` 向审核者展示可操作的挂起项，而 executor 已不存在——一操作就撞 P0-5 |
| 17 | 200ms 探活循环终身运行 | `adapter.go:48,443-467` | 每次 `Alive()` fork 一个 `tmux has-session` 进程 + 一次 HTTP 请求。任务在 `waiting_review` 过夜（正常情况，审核者在睡觉）＝每天每任务约 43 万次 fork。修法：产出 result 后退避到秒级，或做成配置项 |

---

## 四、P2 可后置

空 token 的 fail-open 隐患（`ConstantTimeCompare("","")==1`，今天只因 `net/textproto` 截空格才 401）；`pending_tickets`/`recent_events` 空值序列化为 `null` 而列表接口归一化为 `[]`；cursor 用固定 `.tmp` 文件名，并发 `wait` 可读到空文件（探针 478/58935）；`Diff` 的 `base` 未校验，`--output=/tmp/x` 可让 git 写文件；所有 git 调用用 `context.Background()` 无超时；`rootCmd` 未设 `SilenceUsage`，任何运行时错误都打印整页 flag 帮助，且退出码只有 0/1；`ParseTrailer` 不支持多行/围栏 JSON；SSE 单行 >1MB 触发重连且该事件永久丢失；`"opencode"` 用户名、`id8` 实现、1MB/4MB 限额等魔法值散落多处；`internal/proto/proto_test.go` 缺文件头注释（另外 16 个测试文件都有）。

---

## 五、比任何单个 bug 更要紧的一条

`docs/superpowers/e2e-checklist.md` **每一项都未勾选**。spec §10 的两个 spike 至今没对真实 opencode 验证过：

- **SPIKE-1**：SSE 事件类型名与回合结束判定。`adapter.go:20-24` 自己承认：整条链路依赖 `message.updated` 携带 `role` + `parts`。如果真实 opencode 走的是 `message.part.updated`，或把字段嵌在 `properties.info` 下，那么**回合文本累积、提问识别、完成识别、两条兜底分支全部失效**，且失败形态是最安静的一种——只有一行 Warn，任务挂到 2h 看门狗。
- **SPIKE-2**：`OPENCODE_CONFIG` 环境变量注入是否真的生效（权限门是否真的开着）。

也就是说：**opencode 侧的全部事件映射假设目前都是未经证实的**。这比上面任何一个 P0 更能决定 MVP 到底能不能用。建议在修 P0 的同时，先花半小时手工跑一次真实 opencode，抓一份事件流样本回来对齐——不然修完这些 bug，仍然可能得到一个「跑起来什么都不产出」的系统。

---

## 六、建议的修复顺序

1. **先做 SPIKE-1/SPIKE-2**（抓真实事件样本），确定 adapter 的事件映射是否需要返工——避免在错误假设上修 bug。
2. **P0-4 + P1-8** 一起做（0600 启动脚本同时解决密码 argv 泄漏与 serve 日志缺失）。
3. **P0-1 + P1-1**（WS 交接顺序 + ASC 重放变体），配一个「重放期间 Publish」的回归测试。
4. **P0-2、P0-3、P0-5**，各自独立，都是小改动。
5. **P1-2、P1-6、P1-7、P1-11**（manager 时序与幂等），建议一并重写 `handlePermission`/`handleQuestion` 的顺序。
6. **P1-4、P1-12、P1-13**（交付缺口：终端可见性、plan 摘要、`run` 传参），直接影响验收标准。
7. 其余 P1 与 P2。
