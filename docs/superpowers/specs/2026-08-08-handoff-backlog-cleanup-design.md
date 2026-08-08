# Handoff 三期设计：backlog 小问题批量收口（B4–B12）

日期：2026-08-08
状态：已与用户确认
前置：一期 `2026-08-07-handoff-design.md`（MVP）、二期
`2026-08-08-handoff-approver-dispatch-observability-design.md`（审批链/executor 选择/
dispatch 扩展/可观测性，已合入 main c89932a）

## 1. 问题与目标

二期真实 e2e 与外部代码审阅沉淀出 10 条待办（backlog B4–B11）。除 B2/B3 两个
adapter 单独立项外，其余 8 条都是「已定位到根因、改动局部」的收口项，本期一次做完；
brainstorm 期间用户追加第 9 条（B12），与 B4 构成同步闭环。

贯穿本期的一条主线：**审核者与执行者之间的每一处「看不全 / 对不齐」都要堵死**——
B4 堵开工前远程代码落后、B12 堵收工后本地代码落后、B6 堵权限描述截断导致的盲批、
B9 堵审批裁决被伪造、B8 堵 worktree 归属误判。

## 2. 核心决策（已确认）

| 决策点 | 结论 |
|--------|------|
| B4 同步策略 | 自动 fetch：基线 commit 缺失才 fetch 再复查，仍缺失即拒发；凭证/网络问题在此暴露成拒发理由 |
| B5 中止终态 | 复用 `failed` + 事件写明原因，不新增 `aborted` 状态（状态机零改动，`failed→running` 已允许重试） |
| B6 权限全文 | 工单存全文、事件 payload 仍截断；黑名单与审批者改为对全文匹配 |
| B7 PATH | agentd 启动时用登录 shell 解析 PATH 并合并，不新增配置项 |
| B6 误升级取舍 | 黑名单扫全文后误升级变多（长命令含 `prod` 即命中）——接受：错误方向是「多叫醒审核者一次」，反方向是「漏放一条 rm -rf」 |
| B12 传输通道 | ssh 直连远程仓库 fetch，不经 GitHub：复用 handoff 已依赖的免密 ssh 信任，远程机无需 push 凭证，共享远端不被任务分支污染 |
| B12 同步范围 | 只同步任务分支，不动本地 `main`——合并是人的决定 |

## 3. B4：派发前的远程基线校验

### 3.1 流程

```
handoff dispatch（--target 远程）
  → CLI 在 cwd 解析 git rev-parse HEAD 作为基线 sha
    （cwd 不是 git 仓库 / --no-sync-check → 不带基线，服务端跳过校验）
  → agentd 在 PrepareWorkspace 之前：
      git cat-file -e <sha>^{commit}
        ├─ 命中 → 直接放行（零网络开销，常态路径）
        └─ 缺失 → git fetch --all --prune
                    ├─ 复查命中 → 放行
                    └─ 仍缺失 / fetch 失败 → 拒发（400）
```

### 3.2 契约

- 请求新增字段 `base_commit`（空=不校验）；`DispatchReq.BaseCommit`；
  CLI 新增 `--no-sync-check` flag；
- 基线 sha 必须是 40 位十六进制（CLI 与服务端各校验一次）——非法值一律拒绝，
  杜绝把任意串拼进 git 参数；
- 拒发错误文本必须含三样：基线 sha、fetch 的 stderr 原文、`请先 git push` 的动作提示。
  缺任何一样审核者都得自己再 ssh 上去查一遍；
- fetch 单独 2 分钟上限（见 §9 B10），超时按拒发处理。

### 3.3 为什么「缺失才 fetch」而不是每次都 fetch

常态下远程仓库并不落后（审核者刚推过），每次 dispatch 都付一次网络 fetch 的代价
不划算；而 `cat-file -e` 是纯本地对象库查询，微秒级。只有真落后时才付网络代价，
判据与结论完全一致。

## 4. B5：`handoff stop`

### 4.1 契约

- CLI：`handoff stop <task> [--target]`；路由 `POST /api/tasks/{id}/stop`；
- `Manager.Stop(ctx, taskID)` 顺序：
  1. 读任务；状态已是 `completed`/`failed` → 返回 409（幂等友好，不报错崩掉）；
  2. `adapter.Stop(taskID)` 停 executor（失败只 Warn 不中断——目的是让任务离开
     活跃态，executor 残留由 tmux 会话过期兜底）；
  3. `VoidPendingTickets(taskID)` 作废挂起工单；
  4. 追加 `failed` 事件，`fail_reason` = `审核者主动中止`；
  5. 状态置 `failed`，`hub.Publish` 唤醒。
- 不删分支、不删 worktree——那是 `handoff done` 归档时的职责，stop 只负责「让它停下」。

### 4.2 为什么必须作废挂起工单

与 `handleResult` 的失败分支同一条理由（二期 U-3）：executor 已停，若挂起工单还留在
`pending_tickets` 里，`handoff show` 仍会把它展示成可操作项；审核者一 reply，工单被
消耗、中继打进已死会话返回 502，任务落进不可恢复状态。

## 5. B6：权限描述全文

### 5.1 改动

- opencode adapter 产出 permission 事件时上传**全文**，`permTextLimit` 语义从
  「交给审核者的上限」改为「防失控硬上限 64KB」（超限仍加 `TruncationMarker`）；
- `escalatePermission` / `approvePermission` 落工单时存全文（`tickets.request` 是
  TEXT 列，无需 schema 迁移）；
- `permission_request` 事件 payload 的 `permission` 字段截断 200 字——事件是唤醒
  消息，短即可，全文经 `handoff show` 的 `pending_tickets` 取；
- `Approver.Blacklisted` 与审批 prompt 的权限原文改为**全文**输入。

### 5.2 为什么这是安全修复而不只是体验修复

现状是先截断到 200 字、再拿截断版去扫黑名单。一条 heredoc 脚本或复合命令，前 200 字
完全人畜无害、尾部藏着 `rm -rf`——黑名单扫不到，审批者看不到，审核者也看不到。
三道门同时失效。全文匹配后误升级会变多（长命令含 `prod` 即命中），这是刻意选择的
错误方向（见 §2 决策表）。

## 6. B7：agentd 的 PATH 继承

- agentd bootstrap（config.Load 之后、store.Open 之前）执行
  `$SHELL -l -i -c 'printf %s "$PATH"'`，3 秒超时，**stderr 丢弃**；
- 把结果中**自身 PATH 尚未包含**的目录追加到 `PATH` 尾部（追加而非覆盖：不动
  systemd/launchd 显式注入的路径优先级）；
- 日志记录追加了哪些目录；`$SHELL` 未设置 / 命令失败 / 超时 → 只 Warn，不拦启动。

为什么修在这里：真实踩坑是「用户终端里能跑 `go`，agentd 拉起的 executor 找不到 `go`」，
根因是 agentd 常由非登录 shell 拉起、拿不到用户 rc 文件里的 PATH。在 agentd 侧一次
解析，executor 与审批者 CLI 全部受益，用户零配置。

**为什么必须带 `-i`（2026-08-08 devbox 实测修正）**：本设计初稿写的是 `-l -c`，
在真实开发机上实测**一无所获**——三条 PATH 实测对比：

| 调用方式 | 实测 PATH 是否含 `/usr/local/go/bin` |
|---------|--------------------------------|
| 非登录非交互（agentd 现状） | 否 |
| `$SHELL -l -c`（初稿方案） | **否** |
| `$SHELL -l -i -c`（修正方案） | 是 |

根因是 zsh 的 rc 加载规则：`-l` 只 source `.zshenv`/`.zprofile`/`.zlogin`，而用户的
PATH 追加写在 `.zshrc`（实测该机 `.zshrc` 第 2 行 `export PATH=$PATH:/usr/local/go/bin`），
那是**交互式**才加载的文件。少了 `-i`，这条修复在它要解决的那台机器上恰好无效。

**`-i` 的代价与兜底**：交互式 shell 在非 TTY 下会向 stderr 输出作业控制告警，且
退出码可能非零。因此实现必须 ①只取 stdout（stderr 丢弃，否则告警文本会污染 PATH）；
②**不以退出码判定成败**——只要 stdout 拿到了非空内容就采信；③3 秒超时兜住
「rc 脚本在交互模式下等输入」的挂死风险。

## 7. B8：worktree 归属校验收紧

`worktreeBelongsToRepo` 在现有 `--git-common-dir` 比对之外，追加一道
`git -C <path> rev-parse --show-toplevel` 与入参路径（均经 `EvalSymlinks` 归一）
必须相等的校验。

根因：`git-common-dir` 会向上查找，`/repo/internal/sub` 这种仓库子目录同样返回主仓
git 目录、被误判为合法 worktree；实际改的是主仓 HEAD，且把后续审阅面（diff/run 的
工作目录）收窄到了那个子目录。

## 8. B9：审批裁决的 nonce 防伪

- 每次裁决生成一次性随机 nonce（16 字节 hex）写入 prompt，要求模型在 JSON 中原样
  回显 `"nonce":"<值>"`；
- `parseDecision` 校验 nonce 相等，不等即判无效 → fail-closed 升级人工，并记录
  「nonce 不匹配」的裁决失败原因。

防的是什么：权限请求原文由**被监管的 executor** 产生（不可信）且被插进审批 prompt。
executor 可以构造一段包含 `{"decision":"approve"}` 的文本，若模型复述它、或该文本以
任何方式混入裁决输出，就会被当成裁决结果采信。nonce 是 executor 无法预知的值，
只有真正读到本次 prompt 的模型才回显得出。

## 9. B10：workspace git 调用的超时

- `PrepareWorkspace(ctx, req)`、`RemoveManagedWorktree(ctx, repo, workdir)` 收 `ctx`；
- dispatch/done 路由透传 `r.Context()`；
- 工作区准备整体上限 2 分钟，B4 的 fetch 单独 2 分钟。

根因：现有实现全部 `context.Background()`，`worktree add` 遇网络文件系统、git hook
或 credential 交互式提示会挂死，并且拖住 dispatch 的 HTTP handler 不放。

## 10. B11：attach 建议命令与日志分级

- 非 TTY 降级打印的建议命令带上 `--target`（取任务自身的 `task.Target`）——
  现在打印的 `handoff attach <id>` 对远程任务照抄会打到本机 agentd：先 404、
  再 attach 一个本机不存在的 tmux 会话；
- `client.httpError` 按状态码分级：4xx 打 Warn（预期内的客户端错误，如任务不存在），
  5xx 保持 Error。

## 11. B12：任务完成后本地自动同步

### 11.1 流程

```
handoff wait 收到事件
  → 事件类型 ∈ {completed, failed}？ ── 否 → 原样返回（回合中途，不同步）
  → 是 → 任务有 target（远程任务）且 cwd 是 git 仓库？ ── 否 → 打印跳过原因
  → 是 → git fetch <host>:<task.RepoPath> <task.Branch>:<task.Branch>
        ├─ 成功 → 打印「已同步 <分支>（N 个提交）」
        └─ 失败 → 打印失败原因，wait 仍以原退出码返回
```

- 分支名取 `task.Branch`（**不是**从任务 ID 派生的 `handoff/<id8>`）：二期起
  `--branch`/`--new-branch` 可指定任意分支名，派生式命名只在缺省路径成立；
- `host` 取 `cfg.Targets[task.Target].Addr` 的冒号前段（与 attach 的换算同源）；
- 远程仓库路径取 `task.RepoPath`（不是 `Workdir()`：worktree 是主仓的从属工作树，
  分支对象在主仓库里）；
- 新增 `handoff pull <task>` 手动补拉（同一实现，供 wait 之外的时机使用）；
- `--no-sync` flag 与配置 `sync.auto: false` 可关（默认开）。

### 11.2 为什么只 fetch 不 checkout

写进本地同名分支（与远程任务分支同名），不 checkout、不碰 HEAD——审核者本地可能正在改
别的东西。用 `git fetch <url> <src>:<dst>` 的默认语义：**非快进时 git 自己拒绝**，
这正是要的行为——宁可报错也不能悄悄覆盖本地提交。

### 11.3 为什么失败不影响 wait 的退出码

`wait` 的唯一职责是唤醒审核者并交出事件。同步是增强，把它做成阻塞条件会让
「ssh 临时不通」变成「审核者收不到完成通知」——本末倒置。

## 12. 错误处理汇总

| 场景 | 行为 |
|------|------|
| B4 基线在远程缺失且 fetch 后仍缺失 | 拒发 400，错误含 sha + fetch stderr + `请先 git push` |
| B4 基线 sha 格式非法 | 拒发 400（CLI 侧先拦一次） |
| B5 stop 已终结任务 | 409，不改状态 |
| B5 `adapter.Stop` 失败 | Warn 后继续走完作废工单 + 置 failed |
| B6 权限描述超 64KB | 截断 + `TruncationMarker`，审批链据标记 fail-closed 升级（现行为不变） |
| B7 登录 shell 解析失败/超时 | Warn，agentd 正常启动 |
| B9 nonce 不匹配 | 判裁决无效 → 升级人工，记裁决失败 |
| B10 工作区准备超时 | 拒发，错误含超时上限 |
| B12 ssh 不通 / 非快进 / cwd 非仓库 | 打印原因跳过，wait 退出码不变 |

## 13. 测试策略

- **B4 / B8 / B10**：临时真实 git 仓库集成测试。B4 覆盖两条真路径——「基线未推 →
  拒发且错误含 sha」与「基线在远程 → 零 fetch 放行」；B8 覆盖「仓库子目录被拒绝」
  与「真 worktree 被接受」；B10 用已取消的 ctx 断言快速失败；
- **B5**：fake executor 走全链路（stop → 工单作废 → failed 事件 → 状态 failed），
  外加「重复 stop 返回 409」；
- **B6**：断言工单 request 存全文、事件 payload 截断至 200 字、黑名单命中**长命令
  尾部**的危险命令（这条正是现状漏掉的）；
- **B7**：注入假 `$SHELL` 脚本回固定 PATH，断言目录被追加且已有目录不重复；
- **B9**：断言 nonce 回显正确即通过、缺 nonce 与 nonce 错误均判无效；
- **B11**：非 TTY 输出断言含 `--target`；`httpError` 表驱动断言 404→Warn、500→Error；
- **B12**：本地建「远程」裸仓 + 工作仓模拟 ssh 路径（fetch 的 url 换成本地路径，
  同一代码路径），断言分支被拉到本地、非快进被拒、cwd 非仓库时跳过且退出码不变。

## 14. 范围外（YAGNI）

- B2 Claude Code adapter、B3 grok adapter（各自单独立项，见二期 spec §4.4）；
- B12 自动合并/rebase 到本地 `main`（合并是人的决定）；
- B4 的双向自动同步（远程领先本地时自动 pull 到本地）——本期 B12 只在任务结束时
  拉任务分支，不做常态双向镜像；
- `handoff stop` 的批量形态（`stop --all`）与停止后自动重派。
