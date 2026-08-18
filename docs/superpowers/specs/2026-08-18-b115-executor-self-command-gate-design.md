# B115：executor 自指令收口——handoff 变更命令一律升级人工

> 状态：设计定案，待实现
> 关联：B73（进程围栏，同一类失控面的另一侧）、B23/B27（黑名单与截断判定迁入 permgate）、
> B59（skill 随二进制分发）、B19（executor env 注入通道）

## 1. 背景与根因

### 1.1 实测现象

2026-08-17 在 mac-02 + codex 的 B93 基准评测中，executor **在 shell 里执行了
`handoff dispatch` 派出子任务**（还自作主张传了 `--model`），审批链**自动放行、
未升级人工**。后果：凭空多占进程预算、留下没人收的孤儿任务、任务树失控且协调者
不知情。

### 1.2 根因：三层同时敞着

| 层 | 现状 | 位置 |
|---|---|---|
| **能力** | 执行机上 `~/.handoff/config.yaml` 持有 `token`，executor 与 agentd 共用 HOME → `handoff` CLI 直接可用 | `cmd/root.go:198` |
| **知识** | `handoff skill install` 把审核者 skill 装进四家 agent 的 skills 目录（`.claude/skills`、`.codex/skills`、`.config/opencode/skills`、`.grok/skills`），执行机上正是这四家在跑 → executor 自动加载，被完整教会审核者回路 | `internal/skill/install.go:46-49` |
| **门** | 判定链两道闸都不认识「handoff 自身」 | 见 §1.3 |

「知识」这一层有仓库内实证：`internal/executor/opencode/testdata/session_tooltail.json`
里有一整段 handoff skill 正文被加载进 opencode 会话上下文。**executor 不是碰巧敲对了
命令，是我们亲手教的。**

### 1.3 门为什么没拦住

**第一道闸——静态黑名单**（`internal/permgate/blacklist.go:37-46`）共 8 条规则，
全部属于「破坏宿主 / 破坏数据」这一个威胁轴：

```
rm -rf · rm -r ... -f · rm --recursive --force（两种顺序）
git push --force · sudo · git reset --hard · drop table|database
```

`handoff dispatch ...` 一条都不命中。`judgeCommand`（`blacklist.go:130`）走到最后一行，
返回 `Consult{Reason: "黑名单未命中"}`。

**第二道闸——廉价模型裁决 prompt**（`internal/agentd/approver.go:268`）的判据是：

- 放行：任务仓库内读写、跑测试/构建、装项目依赖、常规 git 提交
- 升级：可能破坏数据、影响范围超出任务仓库、涉及生产环境/部署/运维目标机

`handoff dispatch` 从模型视角看：不删数据、不碰生产、长得就像个开发工具命令 →
**判成明显安全，approve**。

**这不是模型判错了，是判据里压根没有「改变任务图」这个维度。** 两道闸的威胁模型
共用同一个轴（对宿主与数据的破坏面），而 executor 派生 executor 的危害走的是另一个
轴——占用进程预算、留孤儿任务、协调者失去对任务树的掌握。沿着现有轴怎么调参都
拦不住它。

### 1.4 一条更重的越权链

executor 用**只读**的 `handoff tasks` 能拿到别的任务 id，再 `handoff run <别人的id>
"..."` 即可在**别人的工作树**里获得一个不受约束的 shell（`run` 是 `sh -c`，10min
超时）。这条链绕开了 permgate 的路径范围判定（`internal/permgate/path.go` 管的是
写文件路径落在**本任务**范围内）。

另有一条同性质的：executor 可以 `handoff reply --approve`，**自己批自己的工单**。

## 2. 目标与非目标

### 2.1 目标

executor 调用 handoff CLI 的**变更类**子命令时，一律 `Escalate`（升级人工协调者），
不进廉价模型裁决。

### 2.2 非目标

- **不做任务级降权 token**。服务端鉴权是正确的终局（判据确定，连 `curl` 直打 API 都
  拦得住），但改动面是 token 模型 + 端点鉴权分级 + CLI 读取优先级 + 跨机 target 场景，
  且**不消除根**——`~/.handoff/config.yaml` 里那份主 token 仍躺在 executor 读得到的
  HOME 里。本次不做，记为后续（见 §7）。
- **不拦 `curl` 直打 agentd HTTP API**。匹配 agentd 地址的正则太脆，且它属于降权 token
  要解决的那一类。
- **不收窄 skill 落点**。「按角色只往协调者机装」需要先把角色落盘——`RoleExecutor` /
  `RoleCoordinator` / `RoleBoth` 目前只活在 `internal/initflow/form.go:74` 的表单字段
  可见性里，`config.Config` 里没有；且 `RoleBoth`（用户本机）是常态，收窄不掉。

### 2.3 威胁模型的明确边界

本设计防的是**「模型无意识地把自己当协调者」**，不是恶意规避。B93 那次 codex 是照着
skill 教的做，不是在找绕过。文本判据对无意识越权是充分的；对恶意规避不充分——但那种
情况下 executor 早就有 Bash 工具了，`handoff dispatch` 不是它最短的攻击路径。

## 3. 判据设计

### 3.1 白名单形态（而非黑名单）

枚举「哪些放行」，其余一律拦：

| | 子命令 | 处置 |
|---|---|---|
| **白名单（只读、无外部副作用）** | `tasks` `show` `diff` `fetch` `status` `frames` `sessions` `footprint` `ls` | 落回原有判定链 |
| **其余一切 `handoff` 子命令** | `dispatch` `continue` `done` `stop` `reply` `resume` `run` `reclaim` `attach` `pull` `agentd` `init` `service` `skill` `upgrade` `update-check` `project` `machines` `revoke` `console` | `Escalate` |

选白名单而非黑名单，三个理由：

1. **安全默认**。以后新增子命令默认落在「拦」这一侧。黑名单形态下新命令默认是敞的——
   而这正是 B115 本身的成因：现有黑名单写于只有「破坏宿主」这一个威胁轴的时候。
2. **清单更短更稳**。只读面 9 个且几乎不变；变更面 20 个且还在长（`machines` 是 B112
   刚加的，`console` 是 W5 加的）。
3. **正则同样简单**。先试白名单，命中即跳过；否则 `handoff\b` 命中即 `Escalate`。

`attach` 与 `pull` **不在白名单里**：两者都要 ssh 到别的机器、用的是协调者的 ssh 身份，
不满足「无外部副作用」。`attach` 还开交互会话——executor 无 tty，本来就会失败，拦它
零损失。

### 3.2 判定顺序（与黑名单同构地处理引号）

在 `judgeCommand` 最前面前置一段：

| 判定 | 出口 |
|---|---|
| `StripQuoted(s)` 命中自指令 | `Escalate`——真调用，剥完引号还在 |
| 原文命中 + `HasExecWrapper(s)` | `Escalate`——`sh -c "handoff dispatch"`，引号内内容将被执行 |
| 原文命中、剥引号后不命中、无包装器 | **落回原有黑名单链**（多半 `Consult`）——commit message 的降级出口 |
| 未命中 | 落回原有黑名单链，行为完全不变 |

这套引号取舍与现有黑名单逐条同构，复用现成的 `StripQuoted` / `HasExecWrapper`，
不新造机制。核心取舍照旧：**误判的修法不是「直接放行」，而是「从硬拦降级为让模型
看一眼」。**

### 3.3 判定形态：三步，而非单条正则

纯正则做不到「安全默认」：要覆盖新增子命令就得用裸 `\bhandoff\b`，那会误伤
`cd ~/handoff`、`go test ./handoff/...`；要避免误伤就得枚举变更名单，那又退回黑名单
形态、新子命令默认敞着。改为**先切段、再取词元、三步判定**。

**第 0 步（切段）**：按 `|` `;` `&` `\n` 切开，逐段独立判定。这样 `handoff tasks | grep done`
的 `done` 被隔在下一段，不参与本段判定。

**第 1 步（定位）**：段内找首个 basename 为 `handoff` / `handoff.exe` 的词元。
这天然覆盖 `./handoff`、`/usr/local/bin/handoff`、`handoff.exe`。找不到 → 不命中。

**第 2 步（取候选）**：该词元之后、本段内、不以 `-` 开头的全部词元。为空 → 不命中
（`cd handoff && make` 的 `make` 已被 `&` 切走；`rm -rf handoff` 后面没词元）。

**第 3 步（三级判定，顺序不可换）**：

| 序 | 条件 | 出口 | 例 |
|---|---|---|---|
| 1 | 候选词元含**变更名单**中的词 | `Escalate` | `handoff run T1 handoff show` → `run` 命中，不被后面的 `show` 救回 |
| 2 | 否则含**白名单**中的词 | 放行，落回原链 | `handoff --agentd http://x:1 tasks` → flag 值不干扰 |
| 3 | 否则候选非空 | `Escalate` | `handoff foo`（未来新增的任何子命令）——**安全默认在这一条上** |

变更名单优先于白名单，是为了堵「把白名单词塞进变更命令的参数里」这类形态。
第 3 条是安全默认的落点：两个名单都不认识的子命令一律拦，代价是 `handoff --help`
这类无害调用也会升级人工——噪音可忽略（executor 本来就不该敲它）。

引号处理（§3.2）作用在**切段之前的原文**与 `StripQuoted` 结果上，两者各跑一遍这三步。

## 4. 实现点

**新文件 `internal/permgate/selfcmd.go`**，职责单一：识别 handoff 自指令。

不塞进 `blacklist.go`——那个文件的职责是「危险性判据」，本条是「身份越权判据」，
两者威胁轴不同，混在一起下次改会互相牵扯。文件头注释写明这个边界。

导出面：

```go
// IsSelfCommand 判断命令文本是否为 handoff 的变更类自指令。
// 返回 (命中, 子命令名)；子命令名用于填 Verdict.Reason，识别不出时返回空串。
func IsSelfCommand(s string) (bool, string)
```

**改动点 `internal/permgate/blacklist.go`**：`judgeCommand` 开头插入 §3.2 的四路判定。

**Verdict 形态**：`Rule: "self-command"`，`Reason: "executor 试图调用 handoff 变更命令
<子命令>"`。协调者在工单里一眼看懂拦的是什么，不用去翻判据。

**改动点 `internal/agentd/approver.go:268`**：`approverPromptTemplate` 补一句

> 涉及调用 handoff 自身 CLI 变更任务状态的操作，一律升级给上级协调者。

这**不是主判据**（`Escalate` 根本不会走到审批者），是兜底：万一判据漏了某种形态落到
`Consult`，模型还有一次机会。

## 5. 可观测性

命中自指令时在 agentd 侧打一条 **Warn** 日志，带子命令名与 task id：

```
logger.GetLogger().WithEntryName("permgate").Warnf(
    "拦截 executor 自指令：task=%s 子命令=%s 原文=%s", taskID, sub, truncated)
```

理由：B115 的现场证据当初是靠翻 codex 的 session json 才挖出来的。有这条日志，
下次同型问题直接 `search_logs` 就能定位，不必再考古。

原文按 `permEventTextLimit` 同款上限截断，避免长命令灌爆日志。

## 6. 测试

`internal/permgate/selfcmd_test.go` 表驱动，钉死以下形态：

| 输入 | 期望 |
|---|---|
| `handoff dispatch plan.md` | `Escalate` |
| `./handoff run T1 ls` | `Escalate` |
| `/usr/local/bin/handoff done T1` | `Escalate` |
| `handoff --agentd http://x:1 dispatch plan.md` | `Escalate`（flag 插中间） |
| `handoff reply T1 --ticket X --approve` | `Escalate`（自己批自己） |
| `handoff tasks` | 不命中，落回原链 |
| `handoff show T1` / `handoff diff T1 --base main` | 不命中 |
| `handoff tasks \| grep done` | **不**命中（管道隔段） |
| `handoff showoff` | **命中** `Escalate`（`\b` 边界，不得被当成 `show` 放行） |
| `git commit -m "修 handoff dispatch 的正则"` | **不得** `Escalate`（引号降级） |
| `sh -c "handoff dispatch plan.md"` | `Escalate`（包装器硬拦） |
| `cd ~/handoff` / `go test ./handoff/...` / `rm -rf handoff` | **不**命中（第 2 步候选为空 / basename 不是 handoff） |
| `cd handoff && make` | **不**命中（`&` 切段，`make` 不在本段） |
| `handoff foo`（未来新子命令） | `Escalate`（§3.3 第 3 条，安全默认） |
| `handoff --help` / 裸 `handoff` | **不**命中——第 2 步「候选词元」跳过 `-` 开头的 flag，全是 flag 时候选为空。比原设想更好：无害调用不产噪音 |
| `echo handoff dispatch` | `Escalate`（已知误伤：`echo` 后的词元同样进候选。代价只是一次人工点击，不值得为它引入命令语义解析） |
| `handoff run T1 handoff show` | `Escalate`（变更名单优先，不被 `show` 救回） |

回归证据要求：`go test ./internal/permgate/ ./internal/agentd/` 全绿，
`go test -race ./internal/agentd/` 全绿，`gofmt -l .` 无输出。

## 7. 破坏性变更与影响面

**行为破坏性变更**：executor 沙箱内的 `handoff upgrade`、`handoff service install`
这类运维命令会开始被升级人工。

**实际影响≈0**：那些命令是人在终端里敲的，不经审批链——审批链只作用于 executor 发出的
权限请求。协调者本人在执行机上 ssh 敲命令完全不受影响。

**不影响现有任务**：判据只在 `judgeCommand` 前置，未命中时行为逐字不变。

## 8. 后续（不在本次范围）

| 项 | 说明 |
|---|---|
| 任务级降权 token | §2.2 的方案 B。真出现绕过（executor 用变量构造或 `curl` 直打 API）再做 |
| skill 落点按角色收窄 | 需先把 `RoleExecutor` / `RoleCoordinator` 落盘进 `config.Config` |
| B114 / B126 | 纪律块分档与 plan 写作纪律，用户在另一会话处理 |
