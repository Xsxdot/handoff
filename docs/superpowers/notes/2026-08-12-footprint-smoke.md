# 2026-08-12 任务进程足迹真机烟测记录（B69 + B70）

> 记录人：3fb84c66 任务执行者。本机为 macOS（darwin），`kern.maxprocperuid = 2666`。

## 0. 结论速览

- agentd 的「executor 已不在 → 自动清扫残留进程组」全链路在真机验证通过：
  自然死亡路径与 RecoverOnStartup 的 waiting_review 保持分支（修正二的第二个
  调用点）均触发清扫；日志出现 `残留进程已清扫 task=… killed=1`，复核确认退出。
- `handoff footprint` 的可见性验证通过：清扫前该任务显示 `1 进程`，清扫后 `0 进程`。
- 全程无误杀：7777 默认实例的 agentd（pid 1859）与其 shim/serve 自始至终未被碰过；
  清扫只对目标进程组（SIGKILL -pgid）动手。
- **附带发现（重要，已上报审核者并裁决）**：opencode 的 Bash 工具用 setsid 把每条
  命令放进全新的会话+进程组，因此**经 Bash 工具拉起的进程不在 shim 的进程组里**
  （详 §4）。**裁决结果：判据维持 pgid（方案 A），一行未改**——「按祖先匹配」在
  事后走不通（reparent 断链），补全口径的「树活着时记账」已另立 B72 走 brainstorm。
  本盲区已写成代码内一等公民注释（`classify` 的「判据覆盖边界」与 `Sweep` 的 doc），
  spec §3.2/§2.2 已按实证修正，本特性的宣称同步降级为「shim + executor 本体这一层」。

## 1. 隔离实例参数（不占 devbox）

| 项 | 值 |
|---|---|
| 独立二进制 | `go build -o /tmp/handoff-fp ./`（本次提交后的工作树） |
| 独立端口 | `127.0.0.1:7891`（7777 的 agentd 未动） |
| 独立 DataDir | `/tmp/handoff-fp-data`（tasks/ 独立于 `~/.handoff`） |
| 独立仓库 | `/tmp/handoff-fp-repo`（`git init` + 1 个 commit + 假 origin） |
| 配置 | `/tmp/handoff-fp-config.yaml`（listen 7891 / token / datadir / approver 同生产缺省） |
| agentd 启动 | `/tmp/handoff-fp agentd --config /tmp/handoff-fp-config.yaml` |

真实任务：`handoff dispatch --executor opencode`（任务 `ef241c45`，opencode 1.18.16）。

## 2. 清扫前（残留可见）

用审查者认可的构造法（宿主 shell 直接构造，不经模型）：
向任务的 spec.json 写入

```
argv = /bin/sh -c 'nohup sleep 1800 >/dev/null 2>&1 & echo residual_up; sleep 20'
```

以 `python3 -c 'os.setsid(); os.execv(...)'` 拉起 `handoff _shim`（复刻生产里
`spawnDetached` 的 Setsid 语义），shim 即会话+进程组组长。shim 拉起 shell，shell
后台起 `sleep 1800`（残留在组内）后退出 → shim 退出 → 存活锁释放。此时：

```
pid    ppid  pgid  stat  command
35767  35763 35767 SNs  /tmp/handoff-fp _shim --spec …/ef241c45…/spec.json   （已退出）
35770     1 35767 SN   sleep 1800                                            （残留，孤儿，仍在组 35767）
```

`handoff footprint`（清扫前）：

```
进程     349/2666（本机 uid 已用/上限）
  ef241c45  fp-smoke  waiting_review  1 进程
```

**残留被正确数出来（B70 可见性 ✓）。**

## 3. 清扫（修复后）与日志证据

重启隔离 agentd → `RecoverOnStartup` 探活到 `ef241c45`（waiting_review）executor
已不在 → 走保持分支（不迁状态、不追加事件）→ 清扫：

```
15:17:11.769 waiting_review 任务 executor 已不在，保持现状等审核者裁决  task=ef241c45 alive=false
15:17:11.770 回收残留进程组  mod=prochost pid=35767 members=1 pids=[35770]
15:17:11.782 清扫完成，已确认残留退出  mod=prochost pid=35767 killed=1 probe=1
15:17:11.782 残留进程已清扫  task=ef241c45 pid=35767 killed=1
15:17:11.782 启动恢复完成  recovered=0 failed=0 waiting_review_kept=1
```

清扫后：`ps` 无 pid 35770；`handoff footprint`：

```
进程     351/2666（本机 uid 已用/上限）
  ef241c45  fp-smoke  waiting_review  0 进程
```

**残留被杀、计数归零、复核确认（killed=1 probe=1）✓。**

### 3.1 自然死亡路径（同一任务，修正二的第一个调用点）

在此之前，先以 `kill -9` 直接杀掉 executor（opencode serve，pid 30080，模拟自然死亡）
验证事件流终结路径：

```
15:15:23 executor 已不在，开始对账  task=ef241c45 state=waiting_review
15:15:23 任务无需状态对账，仅清扫残留  task=ef241c45 state=waiting_review
15:15:23 无残留可清扫  mod=prochost pid=30079
15:15:23 无残留进程  task=ef241c45 pid=30079
```

该次组内无残留（30079 组只有 shim+serve，均已死），故 `无残留可清扫`。**提前返回
分支也清扫**这条修正二的语义，两处调用点都在真机日志里得到了验证。

## 4. 附带发现：opencode Bash 工具用 setsid 逃逸进程组（已上报，未改代码）

烟测过程中实证：opencode（1.18.16）的 Bash 工具把每条命令放进**全新的会话与
进程组**。证据（前台 `sleep 60` 运行期间的原始快照）：

```
pid    ppid  pgid  stat  command
30079  29895 30079 SNs  /tmp/handoff-fp _shim --spec …/ef241c45…/spec.json
30080  30079 30079 SN   /Users/sycm/.opencode/bin/opencode serve --port 55804 …
35068  30080 35068 SNs  sleep 60
```

- shim pid = 30079；`sleep 60` 的 pgid = 35068。**不相等**。
- `sleep 60` 的 STAT 带 `s`（session leader）⇒ 是 **setsid**（新会话+新进程组），
  不是 setpgid；ppid 30080 = serve ⇒ 由 opencode 直接 spawn，带 detach 语义。
- 复现：模型后台起 `nohup sh -c 'sleep 1800' &`（pid 30809，pgid 30808，ppid 1），
  同样不在 shim 组 30079 内。本任务的**所有** Bash 工具命令（含我自己的工具调用，
  如 pid 35177）都是这个形态。

含义：经 opencode Bash 工具拉起的进程（含 subagent 扇出、并行 `go test`——如果它们
走 Bash 工具）不在 shim 的进程组里，`Sweep` 按 pgid 匹配数不到也杀不到它们。
对照组：spec §2.2 里 claude executor 的孙进程（handoff CLI）曾实测留在组内——不同
executor 行为不同。

**裁决（审核者 08-12）：判据维持 pgid（方案 A），本任务不改判据。** 理由：setsid 改
pgid/sid 但**不改 ppid**，树活着时沿 ppid 能从 shim 走到逃逸者；但 Sweep 工作的时刻
恰恰是执行者已死、子进程被 reparent 给 launchd 之后——ppid 正好断在最需要它的地方，
「按祖先匹配」会得到一个测试里好使、事故现场失效的结果。补全口径的方向（树活着时
shim 记账）已另立 backlog B72 走 brainstorm，不由本任务承担。本判据的盲区已写入
`classify`（「判据覆盖边界」段）与 `Sweep` 的 doc comment，spec §3.2 的「会话封闭性」
改为「只封闭外侧」并按实证写明内侧不封闭。

## 5. 不误杀验证

- 7777 默认实例 agentd：`pid 1859`，`lstart Wed Aug 12 12:56:00 2026`，烟测前后一致；
  其任务 `8d0bc0e3`（shim 91621/serve 91622）与 `3fb84c66`（shim 92656/serve 92657）
  全程未被动过。
- 清扫只发 `SIGKILL -35767`（目标 pgid）；组外进程（包括 30809 逃逸 sleep、隔离
  实例之外的一切）未被触碰。
- 隔离实例任务 ef241c45 收尾时无残留（footprint 全量仅它一行、procs=0）。
- fp-smoke2 从未创建：第二次派发被 agentd 以「目标工作目录已被活跃任务占用」拒绝，
  未留下任何任务或进程。

## 6. 环境余量

烟测全程进程数峰值约 360、`ulimit -u` 余量充足；结束后已回收全部构造进程
（残留 sleep、逃逸 sleep 均已 kill）。
