# 2026-08-12 进程围栏与资源耗尽归因真机烟测记录（B73 / Plan A）

> 记录人：ee6058b1 任务执行者。本机为 macOS（darwin），`kern.maxprocperuid = 2666`、
> `kern.maxproc = 4000`，实测 `ulimit -u` = 2666 / `ulimit -Hu` = 4000，烟测期间本机
> uid 进程占用约 355–365。

## 0. 结论速览

- 准入闸拒发（400，文案带 used/limit 真实数字）、高水位 `resource_pressure` 事件、
  围栏真实生效（executor 树 `ulimit -u`/`-Hu` 均 = 2400）三条真机验证通过。
- **两条计划偏差被真机照出，详见 §7**：①shim 自身的日志在真实 spawn 路径下被丢弃
  （`spawnDetached` 把 shim 的 stdio 接进 `/dev/null`），计划的 4.2「agentd.log 里有
  「拉起执行者进程失败（进程配额）」」预期无法成立——该行只可能出现在 shim 自己的
  stderr（被丢弃）；②shim 进程从未收到 `SetFencePolicy`，其 `ExplainForkFailure` 的
  参考上限恒为策略层默认 L=2400，与**实际安装的围栏值**（`spec.NprocLimit`）可能不符，
  非默认 `reserve_ratio` 部署下归因文案会拿错数字。两条均已用直接 shim 启动实证。
- 真机路径的「shim 撞墙」窗口是 `L = used+1` 的单整数窗口，在本机 ±6 的占用噪声下
  用真派发打不中（多轮实测），改为**宿主 shell 直接构造 shim**（B69 同款手法）做
  确定性复现，归因代码路径（`fork/exec …: resource temporarily unavailable` → EAGAIN
  → `ExplainForkFailure` → 「拉起执行者进程失败（进程配额）」note+fence）逐字段验证。

## 1. 隔离实例参数（不占 devbox）

| 项 | 值 |
|---|---|
| 独立二进制 | `go build -o /tmp/opencode/handoff-b73 ./`（本分支提交后的工作树） |
| 独立端口 | `127.0.0.1:7892`（生产 7777 的 agentd 未动，pid 22072 前后一致） |
| 独立 DataDir | `/tmp/opencode/handoff-fence-data`（tasks/ 独立于 `~/.handoff`） |
| 独立仓库 | `/tmp/opencode/handoff-fence-repo`（git init + 1 commit + 假 origin） |
| 配置 | `/tmp/opencode/handoff-fence-config.yaml`（listen 7892 / token / datadir / executor.default=opencode / proc_fence） |
| 实例生命周期 | 每次改 `proc_fence.reserve_ratio` 均以 `pkill -f "handoff-b73 agentd --config …"` 重启（按二进制+配置路径唯一匹配，生产实例两样都不匹配，不可能误伤） |

## 2. 六闸门实际输出（Task 9 Step 1）

```
$ go build ./...                                   # 无输出，退出 0
$ go vet ./...                                     # 无输出，退出 0
$ gofmt -l .                                       # 无输出
$ go test ./... -count=1                           # 27 个包全部 ok，无 FAIL
  ok  github.com/xushixin/handoff              6.282s
  ok  github.com/xushixin/handoff/cmd          1.775s
  ok  github.com/xushixin/handoff/internal/agentd  26.460s
  ok  github.com/xushixin/handoff/internal/prochost 3.254s
  …（其余包全 ok）
$ go test -race ./internal/prochost/ ./internal/agentd/
  ok  github.com/xushixin/handoff/internal/prochost  7.023s
  ok  github.com/xushixin/handoff/internal/agentd    35.241s
$ GOOS=windows go build ./...                     # 无输出，退出 0
```

## 3. 验证一：准入闸拒发（Task 9 Step 4.1）

隔离实例配置 `proc_fence.reserve_ratio: 0.99` → 系统上限 2666、保留 2639、围栏 27。
本机占用 356+ ≥ 27，任何派发必被拒。

```
$ handoff dispatch --prompt "smoke test task"
WARN agentd 请求被拒 op=dispatch status=400 body="{\"error\":\"进程余量不足：当前 360/27，请等待在跑的任务结束或先回收残留\"}\n"
Error: dispatch: 状态码 400: {"error":"进程余量不足：当前 360/27，请等待在跑的任务结束或先回收残留"}
```

- 400，文案含 used=360 与 limit=27 真实数字；拒发发生在任何建任务/建 worktree 副作用之前
  （任务表里零新增）。✓

## 4. 验证二：shim 撞墙归因（Task 9 Step 4.2）

**真派发路径（`L = used+1` 单整数窗口）**：多轮实测 `used` 在 357–363 间漂移（±6，
机器上有生产任务活动），窗口窄到打不中——首轮 `L=360` 被准入拒（`当前 363/360`）、
`L=364`/`L=359`/`L=358` 时 executor 均成功拉起（used_at_fork 分别为 359/358/… < L）。
此路径的命中概率在本机噪声下接近碰运气，且即便命中，shim 的日志也进不了 agentd.log
（见 §7），故改用**宿主 shell 直接构造 shim** 做确定性复现（B69 同款手法，构造进程
用完即回收，不派发真任务）：

```bash
# 构造 spec：围栏 100 远低于当前占用 363，shim 装上围栏后 fork /bin/sleep 必 EAGAIN
$ cat shimwall/spec.json
{"argv":["/bin/sleep","60"],"dir":"/tmp/opencode/handoff-fence-repo","env":[],
 "stdout":"…/serve.log","stderr":"…/serve.log","lock_path":"…/proc.lock",
 "info_path":"…/proc.json","sentinel":false,"nproc_limit":100}

$ handoff _shim --spec …/shimwall/spec.json 2> shim.stderr ; echo $?
1

$ cat shim.stderr
2026/08/12 20:09:44 INFO  进程围栏已安装 mod=prochost lock=…/proc.lock limit=100
2026/08/12 20:09:44 INFO  shim 拉起执行者进程 mod=prochost bin=/bin/sleep dir=…
2026/08/12 20:09:44 WARN  进程创建失败（EAGAIN），但占用不高，原因未知 mod=prochost used=363 limit=2400
2026/08/12 20:09:44 ERROR 拉起执行者进程失败（进程配额） mod=prochost lock=… bin=/bin/sleep
      note="进程创建失败（EAGAIN），但当前占用仅 363/2400，不像配额问题，原因未知"
      fence=100 cause="fork/exec /bin/sleep: resource temporarily unavailable"
Error: 进程创建失败（EAGAIN），但当前占用仅 363/2400，不像配额问题，原因未知: 拉起 /bin/sleep: fork/exec /bin/sleep: resource temporarily unavailable
```

- EAGAIN 被 `ExplainForkFailure` 认领，Error 分支带 `note` 与 `fence` 字段。✓
- **但归因数字是错的**（`363/2400`，实际围栏 100）——见 §7.2。这是本烟测照出的真实缺陷。

## 5. 验证三：高水位事件（Task 9 Step 4.3）

配置 `reserve_ratio: 0.864216`（围栏 L≈363）→ 占用 356–357 ≥ 0.9×363，准入闸放行
（打 Warn「已达高水位，仍放行」），opencode 任务 `d91f421d` 正常跑起进入 waiting_answer。
看门狗下一 tick 即发事件：

```
$ handoff show d91f421d … | 事件流
seq 2  type=resource_pressure  payload={"used":356,"limit":363}  created_at=…T12:03:21.092957Z

$ grep 高水位 agentd.log
…WARN 进程余量已达高水位，仍放行 op=dispatch used=357 limit=363
…WARN 执行机进程余量达高水位，已告警活跃任务 used=356 limit=363 fired=1
```

- 越线沿触发一次（fired=1），payload 带真实 used/limit，事件出现在任务事件流
  （审核者经 `handoff show`/`wait` 可见）。✓

## 6. 验证四：恢复正常 + 围栏真实生效（Task 9 Step 4.4 / Step 5）

配置改回 `reserve_ratio: 0.1`（围栏 2400），pkill 重启隔离实例：

```
$ grep 围栏 agentd.log
…INFO 进程围栏策略已设定 mod=prochost disabled=false reserve_ratio=0.1
```

派发真实 opencode 任务 `29af931f`，提示词要求「用 Bash 工具运行 ulimit -u 和 ulimit -Hu，
把输出的两个数字原样报告」。任务正常跑起（恢复正常 ✓）。opencode 的 Bash 工具是
executor 树内 setsid 的后代（B72 已实证的形态），它读到的限值即 executor 树实际值：

```
$ handoff show 29af931f …
state: waiting_answer
seq 13  progress {text: "240"}
seq 14  question {ticket_id:…, question: "2400\n2400", kind: "ask"}
```

- **`ulimit -u` = 2400，`ulimit -Hu` = 2400**：executor 树软硬限都等于围栏值，且不是
  系统默认的 2666/4000。✓（这是 Plan A 最直接的一次验收：executor 及后代确实活在围栏里。）

**计划 Step 5 原文的 `handoff run <task> 'ulimit -u; ulimit -Hu'` 实测**：输出 `2666\n4000`
——`handoff run` 由 agentd 直接 fork sh 执行（agentd 不受围栏约束），读的是 agentd 侧
限值而非 executor 树。计划的 Step 5 机制读错上下文，见 §7.3。

## 7. 无误伤与清理（Task 9 Step 6）

- 生产 7777 agentd：pid **22072**，烟测前后一致；其任务与进程全程未被触碰。
- 本机 uid 进程数：烟测前约 356，收尾时 **357**（同一量级）。
- 隔离实例：agentd 已 pkill，7892 已释放，DataDir/仓库/配置/二进制已删除，
  `ps` 无 `handoff-b73` 残留。构造用的 shimwall 已随 DataDir 删除。

## 8. 计划偏差 / 缺陷（照实直说）

- **8.1 计划的 4.2 与 4.4 的日志落点预期不成立**：`spawnDetached` 把 shim 的
  stdout/stderr 接进 `/dev/null`（`platform_unix.go`），shim 进程的 slog 全量丢弃——
  「进程围栏已安装」与「拉起执行者进程失败（进程配额）」这两行**永远不会进 agentd.log**
  （真机 grep 命中 0）。计划 4.2/4.4 预期「agentd.log 里有……」无法兑现；归因行只能在
  宿主 shell 直接构造 shim 时从它的 stderr 看到（§4）。生产里撞墙时唯一可见的痕迹是
  任务 failed + 一个不含归因文案的失败原因，**归因文案被丢进了 /dev/null**——这削弱了
  3.1「撞墙必须认得出是配额」的目标，建议后续把 shim 的 stderr 接进任务目录的日志文件。
- **8.2 归因参考上限与实际围栏值可能不符**：shim 是独立进程，从不调用 `SetFencePolicy`，
  其 `ExplainForkFailure → CheckAdmission → fenceReference → fenceLimit` 恒按**策略层
  默认**（ratio 0.1 → L=2400）判读，而它实际安装的是 `spec.NprocLimit`。默认配置下两者
  相等（都 2400），归因正确；非默认 `reserve_ratio` 下（如烟测的 0.99 配置）两者背离，
  文案会拿 2400 冒充真实围栏（§4 实证：装了 100 却报 363/2400，且因此**不判**配额耗尽
  而报「原因未知」——恰是设计要消灭的误导方向）。建议 `ExplainForkFailure` 接受实际
  围栏值作参考（或 shim 侧把 `spec.NprocLimit` 传进去）。
- **8.3 计划 Step 5 的验证机制读错上下文**：`handoff run` 在 agentd 上下文执行（未装
  围栏，实测 2666/4000），读不到 executor 树限值。正确的 executor 树证明是让 executor
  自己报告（§6：模型经 Bash 工具报 2400/2400）。
- **8.4 真派发路径的 shim 撞墙窗口**：准入闸放行 ⟺ `used < L`，shim 起 executor 撞墙
  ⟺ `used+1 ≥ L`，两条件同时成立只有 `L = used+1` 一个整数点，且 agentd 重启后 L 就
  钉死、used 随机器活动漂移。本机 ±6 噪声下多轮打不中。计划 4.2 的「刚好」取值在真实
  机器上不可行，需宿主 shell 构造（§4）或用别的手段制造确定性窗口。

## 9. 测试与变异检验摘要（详见计划 Task 8 与本分支提交）

- `TestFenceSurvivesSetsid` 与 `TestFenceCannotBeRaisedBack`（新增单向门用例，审核者
  指定的动态取值方案）真机通过：setsid 逃逸后限值仍为 `cur-1`，抬回 `cur` 必被拒。
- 六条变异逐条改码→指定用例 FAIL→还原→`git diff --exit-code` 干净（计划 Task 8 全记录，
  本分支提交 `1686335` 只加单向门用例）。
- 生产全量六闸门 §2 全绿。
