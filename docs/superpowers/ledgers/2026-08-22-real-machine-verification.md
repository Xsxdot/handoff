# 真机验收记录 · 需求 A 第二批 + 需求 B

日期 2026-08-22 · 执行者：协调者（本机会话，不派发）

## 0. 验证台架

在 linux-01 上起一个**隔离 agentd 实例**，不碰生产实例：

| 项 | 生产 | 隔离实例 |
|---|---|---|
| 监听 | `100.79.27.99:7777`（pid 2485606） | `127.0.0.1:7788` |
| DataDir | `/root/.handoff` | `/tmp/hoav/data` |
| 版本 | `v0.3.9-dev.806ca3ff6`（**不含本批代码**） | 构建自 `dde809bc2` |
| HOME / PATH / 凭据 | — | **沿用生产，刻意不隔离** |

**隔离边界为什么这么划**：真机清单要验的是 grok / opencode / 真实 login shell 的
**实际行为**。把 HOME 也隔离掉，`~/.grok`、`~/.opencode` 里的凭据就失效了——那等于
换了一个验证对象，验出来的结论说明不了任何问题。只隔离 DataDir 与端口，两个实例
靠 DataDir 文件锁互不干扰。

**DataDir 放 `/tmp` 下的短路径**是刻意的：PTY 会话的 socket 路径有 ~107 字节上限，
执行机上 `internal/executor/claudecode` 的四个用例在 `/root/.handoff` 长路径下报
「113/116 字节超 107」正是这个原因；短路径下同四个用例全 PASS。

**先钉死被验对象身份**（`handoff version` 在 linked worktree 里构建时报
「非 go build 产物」，版本戳不可信）：

```
src HEAD = dde809bc25cc4244ecf84b5f86c70761ed39915a
grok mapToolUpdate 定义数: 1
opencode mapToolPart 定义数: 1
二进制里相关符号命中: 6
```

冒烟仓库 `/tmp/hoav/smoke`（本地 bare 仓做 origin），计划三步：`echo`、`ls -a`、
写文件并提交。两个任务：grok `ec961612`、opencode `886e2a99`，均跑到 `waiting_review`。

---

## 1. 需求 A 第二批真机清单

### ✅ 第 1 条 · grok 的 `tool_call_update.status` 真实取值

**结论：`completed`。**代码里 `toolResultStatus` 认的就是它，此前唯一依据是
`testdata/updates.jsonl` 这份**手写夹具**——夹具这次碰巧写对了，现在有真机证据。

真机帧（四次工具调用全部）：`tool_result | status='ok'`，即上游给的是 `completed`。

### ✅ 第 2 条 · grok 的 `tool_call_update` 带不带 `content`（工具输出）

**结论：带。**`tool_result` 帧的 `output` 有真实内容：

```
tool_result | status=ok | dur_ms=171   | output=hello-from-executor
tool_result | status=ok | dur_ms=15    | output=. .. .git PLAN.md
tool_result | status=ok | dur_ms=2765  | output=total 936 drwxr-xr-x ...
tool_result | status=ok | dur_ms=6804  | output=/tmp/hoav/data/scratch: ...
```

`updateToolFields` 里那个乐观解析的 `content` 数组是对的，注释里
「grok 实际发不发尚未真机确认」一句可以改成确认结论。

### ✅ 第 3 条 · opencode 重复 `running` 只产一条帧

**结论：去重有效。**10 条 `tool_call` 帧对应 10 次工具调用，没有一次重复。
真机的 `running` 事件是边长边发的多条，去重表挡住了。

### ⚠️ 第 4 条 · 四家 `dur_ms` 口径可比（部分完成）

同量级，无数量级差：

| 执行者 | 命令 | dur_ms |
|---|---|---|
| grok | `echo hello-from-executor` | 171 |
| grok | `ls -a` | 15 |
| opencode | `ls -a`（含 git status） | 188 |
| opencode | `ls -a` | 86 |

**claudecode 与 codex 未真机取样**——本轮只派了 grok 与 opencode 两个冒烟任务。
两者的打点位置有单测罩着（八条变异全红），但「真机上的量级」这一条仍是**未验证**。

### ✅ 第 5 条 · opencode 的审批等待确实算进 tool 段

**结论：算进去了。**一条 `printf ... >> ledger && echo ok` 的真实工作量接近 0，
真机 `dur_ms=38588`——那 38.5 秒几乎全是等我批准的时间。契约 §2.1 的口径
（工具耗时含审批等待，与 claudecode/codex 一致）在真机上成立。

### ❌ 真机发现的缺陷 · opencode 的 `tool_call` 帧 `input` 全空

**10 条 `tool_call` 帧，`input` 全部是 `{}`。**

根因：opencode 的 tool part 第一次出现时 `status="pending"`，那一刻 `state.input`
就是空对象；真命令要到后面的 `running` 事件才带上来。而 `toolStages` 的
`None → Started` 只触发一次，帧就在那一刻按空 input 写死了——帧是追加式的，改不回来。

连带后果更重：`TimingEntry.Detail` 走 `toolDetail(input)`，同样拿到 `{}`。
**opencode 的耗时条目没有任何凭据**，T6 聚合出来会有一整列 `{}`。

**为什么机内没抓到**：计划 §1.4 原样贴了这三行真机抓包，`pending` 那行明明白白
写着 `"input":{}`，出稿时没把结论推出来；单测断言的是「重复的 running 不得重复产
tool_call 帧」——**数量对、内容空，于是全绿**。判据挑错了维度。

处置：已 `continue` 回执行者修（任务 `d278cc18`），要求加一条断言帧 input 内容的
用例 + 变异自检（回退成「首见即写帧」必须变红）。

#### ✅ 修复后的真机复验（commit `10fb151d`，合并为 `3e377d7a`）

用同一台隔离实例、换成修复后的二进制重跑一个 opencode 冒烟任务
（`68a0e27d`）。**帧侧：**

```
tool_call 帧数: 6 | input 为空的: 0        <-- 修复前是 10/10 全空
tool_call | tool=bash  | input={"command":"echo hello-from-executor"}
tool_call | tool=bash  | input={"command":"ls -a"}
tool_call | tool=bash  | input={"command":"git status && git branch --show-current && git log --oneline -3"}
tool_call | tool=bash  | input={"command":"mkdir -p notes"}
tool_call | tool=write | input={"filePath":".../notes/out.txt","content":"smoke sample\n..."}
tool_call | tool=bash  | input={"command":"git add notes/out.txt && git commit -m \"chore: 冒烟取样\" ..."}
```

6 条 `tool_call` 与 6 条 `tool_result` 严格配对，重复的 `running` 没有产生多余帧。
`write` 这类非命令工具也走通了（`toolDetail` 回落到整个 input 的 JSON，符合设计）。

**账本侧**（`task_timing_ledger`，14 条）：

```
kind=tool dur_ms=35 label=bash  detail=echo hello-from-executor
kind=tool dur_ms=16 label=bash  detail=ls -a
kind=tool dur_ms=14 label=bash  detail=git status && git branch --show-current && ...
kind=tool dur_ms=4  label=bash  detail=mkdir -p notes
kind=tool dur_ms=5  label=write detail={"filePath":".../notes/out.txt","content...
kind=tool dur_ms=12 label=bash  detail=git add notes/out.txt && git commit -m ...
```

`detail` 拿到了真实命令（修复前是 `{}`），`label` 是工具名。

**顺带验到的三分法自洽**（这不是本轮的判据，但它给分段正确性提供了一个独立佐证）：

```
api 合计  = 7377+231+0+1324+1467+1205+3079 = 14683
tool 合计 = 35+16+14+4+5+12               = 86
turn 墙钟 =                                 14773
other（差额）= 14773 - 14683 - 86         = 4 ms
```

模型段 + 工具段几乎填满整个回合墙钟，未归类只剩 4ms。契约 §2.1 的三分法口径
在真机上自洽。

---

## 2. 需求 B 真机清单

### ✅ 第 1 条 · 新版控制台 → 旧版远端 agentd

三问全部确认，且是**确认危害的形状**而不是假设它：

**(i) 键是缺席而非 `false`**：

```
生产 agentd 版本: v0.3.9-dev.806ca3ff6
pty_supported     : True
reveal_supported  : False
launchers_supported 这个键在不在: False | 取值: None      ← 旧端
launchers_supported 这个键在不在: True  | 取值: True      ← 隔离实例（新代码）
```

TS 侧因此看到 `undefined`。这正是契约把 `LaunchersSupported` 定成
**nil 按不支持处置**（与 `PtySupported` 相反）的理由，也是能力门必须写
`!== true` 而不是 `!== false` 的理由。

**(ii) 前端不展示该机的启动项**：由 `!== true` 三态门保证，`undefined` 分支有单测；
配合 (i) 实测的「键确实缺席」，这条链路是通的。

**(iii) 手工绕过前端直发带 `env_file`/`init_command` 的建会话请求**：

```
旧端照常建了会话，没有报错 -> 确认是静默忽略，不是拒绝。session= e61b6eb4-...
会话仍在: True
```

**旧端静默忽略、不报错**——`encoding/json` 丢弃未知字段的必然结果。用户会得到
一个环境不对、命令没跑的终端，而界面上什么都不会说。能力门是这条危害的唯一防线。

### ✅ 第 2 条 · 命令在真实 login shell 里执行后会话继续存在，Ctrl-C 只杀命令

前半（会话存续），经隔离实例的真实建会话链路：

```
会话已建: 44f22090-... pid=2841490
标记文件存在: True | 内容: LAUNCHER_RAN
命令跑完后会话仍在: True | exit_code=None | bytes_out=139
```

`exit_code` 为 `None` 即 shell 还活着，人可以接着用这个终端。**这条正是
「不进 argv」那个设计决定要保的东西**——走 `sh -lc cmd` 的话命令退出即会话结束。

后半（Ctrl-C 语义），用 `pty.fork()` 复刻同一机制直接验（写命令进 PTY 输入这一步
之后就全是 shell 与行规程的事，handoff 那段只是 `f.Write`）：

```
Ctrl-C 之后 shell 仍响应新命令: True
shell 进程仍存活: True
```

写 `sleep 300\n` → 写 `\x03` → 再写 `echo STILL_ALIVE_$((6*7))`，回显里拿到
`STILL_ALIVE_42`。Ctrl-C 只打断了 sleep，会话没被带走。

### ✅ 第 3 条 · rc 链读 stdin 的真实发生率

**本机（root / `/usr/bin/bash`）不命中。**判据即第 2 条的标记文件：若 rc 链把
命令原文当输入吃掉了，`/tmp/hoav/marker.txt` 就不会被写出来。实测写出来了，
内容正确。

**范围限定**：这条只证明了这台机器这套 rc 配置。契约 §3.2 的残余风险对
「rc 链会读 stdin」的配置仍然成立，不能推广成「所有机器都没问题」。

### ✅ 第 4 条 · 真实 shell 从 exec 到首字节的延迟

```
$SHELL = /usr/bin/bash
五次首字节延迟(秒): [0.028, 0.022, 0.028, 0.021, 0.021]
最大值: 0.028
```

**21~28ms，远小于人手反应时间（~200ms）。**拆解 §6.1 那个「Open 已返回、命令
还没写进去」的交错窗口实测约 25ms，「接受它、把窗口压到最小」的拍板成立，
不需要重议。

---

## 3. 收尾状态

- 需求 B 真机清单 **4 条全部确认**，无缺陷。
- 需求 A 第二批真机清单 **4 条确认、1 条部分**（第 4 条的 claudecode/codex 未取样），
  并**发现 1 个真实缺陷**（opencode `tool_call` 帧 input 全空）——已回派修复，
  且在同一台隔离实例上**复验通过**（6/6 帧带真实入参，账本 detail 正确）。
- 第一批的真机清单（4 条）**仍未跑**：其中三条依赖 T6（聚合与接线）落地后
  `handoff show` 能带 timing，T6 尚未开工。
- 两张任务卡（`d278cc18`、`51dce9c0`）刻意留在 `waiting_review`，未归档。
- 隔离实例与冒烟仓库留在 `/tmp/hoav/`，供后续复验；生产 agentd（pid 2485606）
  全程未受影响。

### 操作教训：`pkill -f` 会杀掉执行它的那个 shell

重启隔离实例时用 `pkill -f "handoff agentd --config /tmp/hoav/cfg.yaml"`，
结果 `handoff run` 直接返回 `退出码 -1`——**它把执行这条命令的 shell 自己杀了**。

根因：整段脚本是 `sh -c` 的**一个参数**，脚本里后面那行
`setsid nohup ... agentd --config /tmp/hoav/cfg.yaml` 就在这个 shell 自己的
命令行里，而 `pkill -f` 匹配的是完整命令行。

第二次用方括号拆模式（`--con[f]ig`）想绕开，**照样被杀**：绕开的只是 pkill 那一行
的字面串，脚本里 `setsid` 那行的真实文本仍在同一条命令行上，仍被正则匹中。
这是「按模式杀进程」在自动化脚本里的通病——**只要脚本里出现了要杀的目标命令，
模式就一定同时匹配脚本自己**。

正解：从 `ss -ltnp` 的 LISTEN 行取确切 pid 再 kill 那一个。**不能**退回
`lsof -ti tcp:<port>`——那个会把**客户端连接**也算进去一起杀。
