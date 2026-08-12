# 2026-08-12 进程围栏与资源耗尽归因真机烟测记录（B73 / Plan A）

> 记录人：ee6058b1 任务执行者。本机为 macOS（darwin），`kern.maxprocperuid = 2666`、
> `kern.maxproc = 4000`，实测 `ulimit -u` = 2666 / `ulimit -Hu` = 4000，烟测期间本机
> uid 进程占用约 355–365。

## 0. 结论速览

- 准入闸拒发（400，文案带 used/limit 真实数字）、高水位 `resource_pressure` 事件、
  围栏真实生效（executor 树 `ulimit -u`/`-Hu` 均 = 2400）三条真机验证通过。
- **8.1/8.2 两个真实代码缺陷在烟测中被照出，且已修复**（见 §8）：①shim 自身日志在
  真实 spawn 路径下被丢进 /dev/null，撞墙归因行没人看得到——已把 shim 的 stderr 接进
  `<taskDir>/shim.log`（`Start`→`spawnDetached` 接线，含回归用例
  `TestShimLogsLandInTaskDirShimLog`）；②shim 进程从不调 `SetFencePolicy`，归因参考
  上限恒为策略默认 L=2400 而与实际围栏不符——已改为读**本进程实际生效的 RLIMIT_NPROC**
  （`getNprocLimit` 地面真相），判据从「贴 0.9 阈值」收紧为「used ≥ 实际软限即确定归因」
  （含 shim 上下文回归用例 `TestExplainForkFailureUsesActualLimitNotPolicyDefault`，
  变异自检确认该用例在旧行为下 FAIL）。修复后同一构造（§4）从「363/2400 不像配额问题」
  变成「进程配额耗尽（当前 uid 357/100）」，且落进任务目录 shim.log。
- **8.3/8.4 属计划写错，未改码、留作已知偏差**（§8）。
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
# 构造 spec：围栏 100 远低于当前占用 357，shim 装上围栏后 fork /bin/sleep 必 EAGAIN
$ cat shimwall/spec.json
{"argv":["/bin/sleep","60"],"dir":"/tmp","env":[],
 "stdout":"…/serve.log","stderr":"…/serve.log","lock_path":"…/proc.lock",
 "info_path":"…/proc.json","sentinel":false,"nproc_limit":100}

$ handoff _shim --spec …/shimwall/spec.json 2> shim.log ; echo $?
1

$ cat shim.log   # 生产里这是 Start 接的 <taskDir>/shim.log（8.1 修复后）
2026/08/12 20:27:08 INFO  进程围栏已安装 mod=prochost lock=…/proc.lock limit=100
2026/08/12 20:27:08 INFO  shim 拉起执行者进程 mod=prochost bin=/bin/sleep dir=/tmp
2026/08/12 20:27:08 ERROR 进程配额耗尽 mod=prochost used=357 limit=100
2026/08/12 20:27:08 ERROR 拉起执行者进程失败（进程配额） mod=prochost lock=… bin=/bin/sleep
      note="进程配额耗尽（当前 uid 357/100），命令未执行；这不是代码问题，请降低并发后重试"
      fence=100 cause="fork/exec /bin/sleep: resource temporarily unavailable"
Error: 进程配额耗尽（当前 uid 357/100），命令未执行；这不是代码问题，请降低并发后重试: 拉起 /bin/sleep: fork/exec /bin/sleep: resource temporarily unavailable
```

- EAGAIN 被 `ExplainForkFailure` 认领，**确定**判为配额耗尽，Error 分支带 `note` 与
  `fence` 字段，数字是**实际围栏 100**，且落到任务目录的 shim.log（审核者读得到）。✓
- **修复前同一构造**（8.1/8.2 修复前的原始输出，两行都错了）：
  ```
  WARN  进程创建失败（EAGAIN），但占用不高，原因未知 mod=prochost used=363 limit=2400
  ERROR 拉起执行者进程失败（进程配额） … note="进程创建失败（EAGAIN），但当前占用仅 363/2400，不像配额问题，原因未知" fence=100 …
  ```
  装了围栏 100、fork 必然被自己的围栏挡下，却报「363/2400 不像配额问题」——自信的错
  结论，方向恰好相反。修复把参考上限从策略层默认 L 改为**本进程实际生效的软限**
  （`getNprocLimit`），并把判据从「贴 0.9 阈值」收紧为「used ≥ 实际软限即确定归因」。

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

### 8.1 shim 日志落 /dev/null —— **已修**

`spawnDetached` 原先把 shim 的 stdout/stderr 全接进 `/dev/null`，shim 进程的 slog 全量
丢弃，「进程围栏已安装」与「拉起执行者进程失败（进程配额）」这两行永远不会进
agentd.log（修复前真机 grep 命中 0），生产里撞墙时审核者只能看到一个不带归因的
failed。**修复**：`Start` 打开 `<taskDir>/shim.log`（0600，追加），`spawnDetached` 的
stderr 参数接到它（stdin/stdout 仍为 /dev/null）；shim 的 slog 与 cobra 错误输出全部
落进任务目录。打开失败只降级（继续落 /dev/null），不阻断拉起（fail-open）。
回归用例 `TestShimLogsLandInTaskDirShimLog`（真实 shim 入口经 spawnDetached 拉起，
断言 shim 必打的 slog 行出现在日志文件）。

### 8.2 归因参考上限与实际围栏不符 —— **已修**

shim 是独立进程、从不调用 `SetFencePolicy`，其 `ExplainForkFailure` 原先经
`CheckAdmission → fenceReference → fenceLimit` 恒按策略层默认 L 判读，而它实际安装
的是 `spec.NprocLimit`。默认配置下两者相等（都 2400），归因正确；非默认
`reserve_ratio` 下两者背离，文案拿 2400 冒充真实围栏（§4 实证：装了 100 却报
363/2400，且因此**不判**配额耗尽而报「原因未知」——恰是设计要消灭的误导方向）。
**修复**：`ExplainForkFailure` 改为直接读 `enumProcsFn()`（占用）+ `getNprocLimitFn()`
（**本进程当前实际生效的软限**，`getNprocLimit` 读回来即地面真相，无需记账）；判据
从「占用 ≥ 0.9×参考」收紧为「**占用 ≥ 实际软限即确定判配额耗尽**」，占用低于实际
软限或读不出数才如实说未知。在 shim 里该软限就是刚装上的围栏，在 agentd 里是系统
默认上限，两处上下文各自正确。回归用例
`TestExplainForkFailureUsesActualLimitNotPolicyDefault`（shim 上下文：实际软限 100、
占用 363，断言判配额耗尽、数字是 100、绝不出现 2400）；变异自检：把参考源改回
`fenceLimit()` 后该用例 FAIL（note="…363/2400，不像配额问题"），还原后复绿。

### 8.3 计划 Step 5 的验证机制读错上下文 —— **已知偏差，未改码**

`handoff run` 在 agentd 上下文执行（未装围栏，实测 2666/4000），读不到 executor 树
限值。正确的 executor 树证明是让 executor 自己报告（§6：模型经 Bash 工具报
2400/2400）。

### 8.4 真派发路径的 shim 撞墙窗口 —— **已知偏差，未改码**

准入闸放行 ⟺ `used < L`，shim 起 executor 撞墙 ⟺ `used+1 ≥ L`，两条件同时成立只有
`L = used+1` 一个整数点，且 agentd 重启后 L 就钉死、used 随机器活动漂移。本机 ±6
噪声下多轮打不中。计划 4.2 的「刚好」取值在真实机器上不可行，需宿主 shell 构造
（§4）或用别的手段制造确定性窗口。

## 9. 测试与变异检验摘要（详见计划 Task 8 与本分支提交）

- `TestFenceSurvivesSetsid` 与 `TestFenceCannotBeRaisedBack`（新增单向门用例，审核者
  指定的动态取值方案）真机通过：setsid 逃逸后限值仍为 `cur-1`，抬回 `cur` 必被拒。
- 六条变异逐条改码→指定用例 FAIL→还原→`git diff --exit-code` 干净（计划 Task 8 全记录，
  本分支提交 `1686335` 只加单向门用例）。
- **8.1/8.2 修复的回归用例**（本修复提交）：`TestShimLogsLandInTaskDirShimLog`
  （shim 日志落进任务目录文件）、`TestExplainForkFailureUsesActualLimitNotPolicyDefault`
  （归因引用实际软限而非策略默认 L，变异自检确认有效）。原有
  `TestExplainForkFailureQuotaExhausted` / `TestExplainForkFailureLowUsageStaysHonest`
  按新判据（used ≥ 实际软限）同步更新。
- 生产全量六闸门 §2 全绿（本修复后重跑一致）。
