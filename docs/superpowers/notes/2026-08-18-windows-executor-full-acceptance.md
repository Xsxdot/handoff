# Windows 执行机全方位真机验证记录（2026-08-18）

执行机：win-b37（47.80.243.155，Windows Server 2025 Datacenter，build 10.0.26100）。
全部由协调者在本地驱动 `handoff` CLI 完成，**未派发**——本轮判据几乎全部要驱动
handoff 自身（起停 agentd、派发、审批、回收），按 B126 的教训必须留本地。

## 0. 与既有验收的关系

B37 / B123 / B128 / B124 已覆盖：注册面、四个执行器各自的全链路、多轮输入通道不
EOF、agentd 重启存活、`done` 零残留、Job Object 围栏内核硬拒 fork。**本轮不重复
跑这些**（唯一例外是重启存活——本轮换了二进制，招牌属性必须在新版上重验）。

本轮靶子对准从未验过的面：协调者只读取证三件套、去程校验的两条拒绝路径、回程
`pull`、中止与回收、并发、以及部署换版路径。

## 1. 前置：被验对象的身份必须先钉死

验证开始时 win-b37 上的 agentd 自报 `c32a1f8b1998 带未提交改动`——不对应任何提交。
先把合并后的 HEAD（`85c1e2322`）部署上去，否则所有结论都归因不到具体代码。

换版过程本身产出了三条发现（§3 的 D1/D6/D8），换版后 `handoff status` 报
`85c1e2322a08`，「带未提交改动」的假标记消失。

## 2. 通过项

| # | 项 | 证据 |
|---|---|---|
| B1 | `handoff diff` | 对 `086e7f1e` 返回空，并用 `git status --porcelain` + `git log base..HEAD` 双双为空**反证**该任务确实一行没改（空输出是对的，不是坏了）；对 wintest-a/b 返回完整 diff |
| B2 | `handoff fetch` | 正常取回 `CODEXPLAN.md`；取不存在的文件返回 404「文件不存在」 |
| B3 | `handoff run` | `git log --oneline -3 && echo RUN_OK` 正常执行。**Windows 上它走 Git for Windows 的 `sh.exe`，候选路径兜底真的生效**（PATH 里没有 `C:\Program Files\Git\bin`） |
| B4 | `handoff attach` | 流回 411 行 render 日志，中英文均无编码损坏 |
| C1 | 基线校验拒绝 | 本地 `85c1e2322` 未 push，dispatch 被 400 拒；报文含 Windows 侧 `git fetch` 的原始 stderr 作根因物证；退出码 1 |
| C2 | 本地脏工作区拒绝 | 客户端侧就拦下（没白跑一趟网络），点名 `README.md`，给出 commit/stash/`--allow-dirty` 三条处置；退出码 1 |
| C3 | 新分支起点 | `base_commit=f7f8bbf2e39…` 与本地 HEAD 逐字节一致，stderr 亦报「起点 f7f8bbf」 |
| D1 | `stop` 连坐回收 | 中止前树为 `agentd→shim→claude→bash→bash→sleep.exe` 外加裁决 MCP，共 6 个 pid；`stop` 后**逐个点名核对，6 个全部消失**，只剩 agentd。孙子辈的 `sleep.exe` 也被收掉——那正是裸 pid kill 会漏的部分 |
| D2 | `reclaim` | 列表形态从 18 个任务里精确挑出唯一残留并标「脏（1 项改动）」；指定回收被守卫拦下并点名 `WINTEST_C.md`；`--force` 后 worktree 目录与 git 登记双清，分支按文档保留 |
| D3 | `done` 零残留 | 两个任务归档后 worktree 目录消失、`git worktree list` 登记同步清掉、进程只剩 agentd；任务目录按设计保留 |
| E1 | 并发两任务 | opencode 与 claude 同时 `running`，独立 worktree（`66f4b410` / `f25391fe`）、独立进程树，产物各自正确 |
| E2 | opencode 回归 | `WINTEST_A.md` 内容与要求逐行一致，提交 `1c63509b` |
| E3 | DataDir 锁 | 手工起第二个 agentd 被 `agentd.lock` 挡下，报文完整、退出码非零、未碰任何数据。**Windows 上 `LockFileEx` 这条路是通的** |
| F | 活过 agentd 重启（新二进制复验） | 先证明 agentd pid `GONE`，再断言 shim/claude/裁决 MCP/两层 bash **5 个 pid 启动时间一秒未变**；新 agentd `recovered=1 failed=0 waiting_review_kept=2`，且 `执行器存活，重建订阅继续消费 alive=true state=running`（`resume_seq=316`）；任务随后穿过新 agentd 跑到 `completed` |

## 3. 缺陷

### D1（B142）Windows 无托管路径，`update` 闸二永久关闭

`internal/service/service.go` 的 `New` 对 `windows` 直接返回错误，理由写的是
「进程承载层 Windows 实现尚未完成（backlog B37）」。**B37 早已 done**，那台机器
此刻正拿这套 prochost 跑任务——理由是过期的。

后果不止文案错：没有托管管理器 → `handoff status` 恒报「agentd 非托管启动，换版
会被拒绝（`--force` 也不越过）」→ **Windows 执行机的换版只能手工停进程覆盖文件**，
而这正是本轮 §1 干的事。

实测（win-b37）：`handoff service status` 与 `service install` 均退出 1，报文为上述过期理由。

### D2（B143）回程 `pull` 对 Windows 执行机不可用——两个独立原因叠加

任务结束时的自动同步与显式 `handoff pull` 都失败。挖到两层，**都不是配错了**：

**a. ssh 主机由 agentd 的 HTTP 地址推导。** `sshHostFromTarget`（`cmd/attach.go:74`）
把 `Target.Addr` 去掉端口就当 ssh 目标，而 `Target` 只有 `Addr/Token/User` 三个字段
（`internal/config/config.go:177`），没有任何位置能单独写 ssh 主机。win-b37 的 agentd
经 ssh 隧道暴露在 `127.0.0.1:17777`，于是 pull 去 ssh `Administrator@127.0.0.1`
——那是协调者本机，根本不是执行机。实测报 `Host key verification failed`。

这不是 Windows 独有，任何经隧道/端口转发接入的执行机都中招；但 win-b37 恰恰是**必须**
走隧道的那台（公网 IP，B127 记录过被爆破，不能直接暴露 agentd 端口）。

**b. 即便主机对，`git fetch` 仍然失败。** 手工换成真实主机 `47.80.243.155` 后，
三种路径形式（反斜杠 / 正斜杠 / `ssh://`）**全部**失败于同一个签名：

```
fatal: ''C:/Users/administrator/.handoff/repos/handoff'' does not appear to be a git repository
```

双层引号是判据：git 给远端路径加单引号，而该机 sshd 的 `DefaultShell` 未设置
（用 cmd.exe），cmd 不剥单引号。已核实远端仓库与分支确实存在
（`git log --oneline -1 tmp/wintest-a` → `1c63509b chore: wintest A`），
所以失败的唯一原因就是这层引号。

**影响与现状**：审阅本身没被卡住——`diff` / `fetch` / `run` 走 agentd 的 HTTP 通道，
全部可用。卡住的是「把任务分支取回本地 git」。b 的补法是改该机 sshd 的
`DefaultShell` 注册表项（系统设置，需人工决定）；a 的补法要改代码（`Target` 加
ssh 主机字段）。

### D3（B144）`procenum` 未实现被放在例行只读路径上按 ERROR 刷

每次 `handoff status` 都在 agentd 日志里打两条 ERROR：

```
level=ERROR msg=足迹枚举失败      cause=本平台不支持进程枚举
level=ERROR msg="统计 uid 进程占用失败" cause=本平台不支持进程枚举
```

这是 B122（Windows 无 `procenum`）的已知缺失，但它是**预期形态**，不该按 ERROR 打在
一个例行只读路径上——同一进程里紧跟着的 WARN「状态聚合：读不到进程占用，该字段留空」
才是正确的措辞档位。shim 侧的同款刷屏之前已经修过（任务 `5fc8fe7d` 的验收记录：
「整轮 shim.log 仅 4 行，含一条 Info 说明本平台不做名册采样」），agentd 侧漏了。

### D4（B148）任务收尾误报「残留进程」，并指向一个本平台同样不可用的诊断命令

任务 D 完成时事件流里出现（seq 133）：

```
残留进程清扫失败（pid=2476，原因：本平台不支持进程枚举），请先 handoff footprint 确认再人工处理
```

`pid 2476` 是该任务的 shim。**这是误报**：`done` 之后逐个点名核对，2476 与整棵树
（claude、裁决 MCP、两层 bash）全部消失，worktree 也清干净——回收由 Job Object
连坐完成，从来不需要名册点名。

比 B144 的日志刷屏更糟的是它**面向用户且看起来可操作**：它要协调者「先 handoff
footprint 确认再人工处理」，而 `footprint` 在 Windows 上依赖的正是同一个未实现的
`procenum`，回答不了它自己提出的问题。

正确形态应与 shim 侧一致（任务 `5fc8fe7d` 的修复）：本平台不做名册采样时说明一句，
不产出「清扫失败」这种要人介入的措辞。

### D5（B145）`reclaim` 的处置建议给短 id，照着敲必然 404

工作树脏时 `reclaim` 拒绝并提示：

```
处置     确认可丢弃后重跑：handoff reclaim 9413e365 --force
```

而 handoff 的 `<task>` 是精确匹配、不认短 id。照敲实测：

```
Error: 回收 worktree: 状态码 404: {"error":"记录不存在","reason":""}
```

错误提示给了一条跑不通的命令。用完整 UUID 则正常。

### D6（B146）linked worktree 里构建的二进制，版本戳取自主工作树

Go 的 VCS 戳在 linked git worktree 里读的是**主工作树**的 HEAD 与脏状态。
同一份源码的对照实证：

| 构建位置 | `vcs.revision` | `vcs.modified` |
|---|---|---|
| linked worktree（`wc-merge`，实为 `85c1e2322`、零脏文件） | `c32a1f8b1998` | `true` |
| 独立克隆（同一提交） | `85c1e2322a08` | `false` |

`c32a1f8b1998` + 11 个脏文件正是主工作树当时的状态。

这个仓库几乎所有开发都在 `.claude/worktrees/*` 里做，**意味着从 worktree 构建的
每一个二进制自报版本都是错的**，`version` / `status` / `upgrade` 三处全受影响。
本轮开局把「agentd 是脏树构建的」当成事实，就是被这个戳骗了一次。

绕法：从独立克隆构建，或用 `-ldflags -X …buildinfo.releaseVersion=` 显式注入。

### D7（B147，小）`upgrade --check` 对没有 releaseVersion 的机器渲染空版本列

`renderCheckRow` 取 `machineState.Agentd`，其值来自 `status.Version.Version`
（release 版本号），只在 ldflags 注入时才有值。手工 `go build` 的二进制该字段为空，
于是 win-b37 那一行的版本列是空的，而 `status --target win-b37` 明明报得出
revision `85c1e2322a08`。有 revision 却渲染成空格，可读性差。

**这不是 Windows 的问题**，任何未注入 releaseVersion 的机器都一样。

### D8（B142 附带）手搓的 schtasks 托管收不掉 agentd

`schtasks /end /tn handoff-agentd` 报 SUCCESS、任务状态回到 `Ready`，但 agentd 进程
**原样活着**（StartTime 未变）——它只杀掉外层 `cmd.exe`，孙进程没被连坐。管理器视图
与现实分叉；随后 `schtasks /run` 起的第二个实例被 DataDir 锁挡下（见通过项 E3，
锁救了这一手）。

这是 D1 的直接后果：没有 `handoff service install`，托管只能手搓，而手搓的这份不具备
进程组回收能力。修 D1 时这条一并解决。

## 4. 未覆盖

- **`delivery_failed` → `resume` 恢复阶梯**：需要在裁决在途时精确杀掉 executor 才能
  构造，本轮没造；且该路径与平台无关。
- **`stalled` 看门狗**：需要 2 小时无产出，成本不合算。
- **`deny` 路径**：B128 §12 第 4 条已验并已定性——拦截成立，两个判据受既有缺陷
  B137（理由迟到一整回合）与 B134（`Bash` 全量 allow）影响。本轮不重复。
- **`init` 的 executor 角色选择**：`huh` TUI 经 ssh 驱不动，要 RDP（B124 第 ① 条）。

## 5. 结论

**Windows 作为执行机，主干链路是通的**：派发去程的两条拒绝路径、四个执行器、并发、
协调者只读取证三件套、中止与回收、招牌属性「活过 agentd 重启」——全部通过，且回收
能力（Job Object 连坐）在实测中收掉了裸 pid kill 会漏的孙进程。

**两处让它不好用**：一是没有托管路径（B142），换版要手工停进程；二是回程 `pull`
不可用（B143），改动取不回本地 git。两者都不影响「任务能不能跑完」，但都影响
「这台机器好不好养」。
