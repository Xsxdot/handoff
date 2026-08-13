# 纯协调者机的可用性（B84）

> **定位**：让一台**只当协调者**的机器（不跑 agentd、不跑 executor）能完整走通派发与
> 审阅回路。本文修两条互相独立的缺陷，它们有同一个根因（§1.3）。
>
> **分支**：`handoff/coordinator-only-machine`，基于 `main`。
>
> **来源**：08-13 用户问「Windows 当协调机能不能跑」。静态审查（`GOOS=windows` 交叉编译
> 全绿 + 逐条走协调者代码路径）扫出三处会卡住的地方，本文修其中两处；第三处（无 Windows
> release 资产）是分发问题，列为非目标（§4）。
>
> **两条都不是 Windows 专属**：macOS 上只装协调者角色的机器同样撞得上第一条，第二条
> 是删掉一个本就不该有的特权调用。Windows 只是把它们从「偶发」变成「必然」。

---

## 1. 病灶

### 1.1 首次派发被一次记账动作否决

在一台没有本机 agentd 的机器上，对着一个目标机尚未登记的项目派发：

```
$ handoff dispatch --target devbox --plan x.md
目标机上还没有这个项目，正在自动登记…
Error: 登记到本机: project add 请求: dial tcp 127.0.0.1:7777: connect: connection refused
```

`registerProjectBothHops`（[cmd/project.go:112](../../../cmd/project.go:112)）无条件先打本机那一跳，
失败即返回，目标机那一跳根本没发出去。`handoff project add` 与 dispatch 的自动登记
（[cmd/dispatch.go:243](../../../cmd/dispatch.go:243)）走的是同一个函数，因此两条入口一起挂。

在 Windows 上这是死结：agentd 的进程承载层在非 unix 平台全部返回 not implemented
（[internal/prochost/platform_other.go](../../../internal/prochost/platform_other.go)），本机 agentd 起不来，
这一跳永远连不上。

### 1.2 skill 落点全靠目录软链

`skill.Install` 给四家 agent 的 `skills/handoff` 各建一个软链指向基准副本
（[internal/skill/install.go:105](../../../internal/skill/install.go:105)）。Windows 上创建目录软链需要
`SeCreateSymbolicLinkPrivilege`（管理员，或开启开发者模式），未满足时四个落点全部落到
`StateSkipped`、Note 写「建软链失败」，只有基准副本 `~/.handoff/skill` 落地——协调者的
Claude Code 会话因此拿不到 handoff skill，而 skill 恰恰只服务于协调者。

`Install` 不因此返回错误（单个落点失败不打断全局，这个决定是对的），所以症状是
**静默半残**：命令退 0，四行「跳过」混在输出里。

### 1.3 共同根因

两条都是同一件事的两种表现：**协调者机被要求具备只有执行机才需要的能力**——
一是本机跑着 agentd，二是有建软链的特权。

第一条的前提写在 spec §6.2
（[repo-registration-normalization-design.md:225](2026-08-11-repo-registration-normalization-design.md)）：

> 之所以顺带补本机（哪怕派发目标是远程）：本机位置是免费且已知的，不补它 W3a 的项目树就会缺行

**「免费」是个未被检验的前提。** 它只在本机跑着 agentd 时成立；作者的 mac 是 role=both，
于是它一直成立，代码就把一个纯记账用途的附带动作写成了能否决整次派发的硬失败。

第二条的软链同理：它买的是「改一次基准四家同时生效」，而内容来自 `go:embed`、
`Install` 每次运行本来就全量重写所有落点——这个收益在真实工作流里为零，
代价却是一个在某个平台上需要特权的系统调用。

---

## 2. 设计一：本机登记跳连不上就降级

### 2.1 判据落在 client 层

新增哨兵 `client.ErrUnreachable`，在 `do()`（[internal/client/client.go:219](../../../internal/client/client.go:219)）里
当 `hc.Do` 返回错误时包上。

**判据是「一个 HTTP 响应都没拿到」**，不是「错误文本里有 connection refused」。这是传输层
事实，只有 client 知道；让 `cmd` 去 grep 错误字符串是把平台差异与 Go 错误措辞变成契约。
写法与既有的 `ErrStatusUnsupported`（[client.go:266](../../../internal/client/client.go:266)）同源，
那条注释已经立好规矩：唯一「HTTP 失败但结论不是失败」的分支必须是可判别的哨兵。

**一个例外必须排除**：`context.Canceled` 与 `DeadlineExceeded` 不包哨兵。它们也从
`hc.Do` 的错误返回出来，但含义是「人按了 Ctrl-C」或「主动超时」，不是「本机没有 agentd」；
包了就会出现「用户中断后派发反而继续往下走」。

范围只覆盖 `do()`，不动 `doStream()`：流式调用（attach / render）没有降级需求，不铺。

### 2.2 只有本机那一跳降级

`registerProjectBothHops` 的本机跳：`errors.Is(err, client.ErrUnreachable)` → 打 Warn +
stderr 一行，继续走目标跳。

**拿到了响应的失败一律仍然致命**——409 位置冲突、400 origin 不符、500 服务端故障。
那些是真冲突，咽下去就是脏登记，正是 `ErrProjectOriginMismatch` 单列哨兵要防的东西
（[internal/agentd/projectadmin.go:43](../../../internal/agentd/projectadmin.go:43)）。

**目标机那一跳永不降级**：它连不上就是派发失败，没有第二种解释。

这不是新概念。`handoff upgrade` 早已把「本机 agentd 没跑」列为一等状态
（`verdictAgentdDown`，[cmd/upgrade_verdict.go:68](../../../cmd/upgrade_verdict.go:68)），并专门注释了
它与「远端够不着」语义不同。本条只是把同一条判据用到登记上。

### 2.3 两跳全空时仍然报错

不带 `--target` 且本机连不上 → 报错，不降级。两跳都没发生时报成功是撒谎。
报文给出路：本机装 agentd，或带 `--target` 指名执行机。

### 2.4 文案

降级时写 stderr（**硬约束**：dispatch 的 stdout 是「第一行是任务 JSON」的既有契约，
上层按行解析）：

```
本机 没有 agentd（127.0.0.1:7777 连不上），跳过本机登记
     本机项目树会缺这一行；本机起了 agentd 之后重跑 handoff project add 补上
```

只说「跳过了」不够——必须同时说后果（项目树缺行）与补救（起了 agentd 重跑）。
同时打一条 `slog.Warn`，带 addr 与 cause。

---

## 3. 设计二：skill 落点由软链改副本（全平台）

### 3.1 为什么不是「Windows 才退化成副本」

因为副本这个形态**本来就已经是一等公民**：

- `Status` 按**文件内容** sha256 逐个落点比对（[internal/skill/state.go:29](../../../internal/skill/state.go:29)），
  注释明写「经软链读到的就是基准副本；落点是实体目录时读到的是它自己那份」——
  它压根不关心落点是链还是目录。
- `Install` 已经写了「目标可能是上一次装的软链，也可能是手工放的实体目录」并以
  `RemoveAll` 兜底，`TestInstallReplacesRealDirectory` 钉着这条。

也就是说改成副本，**现有一致性逻辑一行都不用动**。而按平台分叉要付的是永久成本：
两个平台两种落点形态，此后每一处碰 skill 落点的代码都要同时想两遍。

### 3.2 变更

`os.Symlink(base, link)` → `os.MkdirAll(link, 0o755)` + `os.WriteFile(link/SKILL.md, content, 0o644)`。

全平台一条路径，不加 `runtime.GOOS` 分支，不加 Windows 专有文件。净删代码。
基准副本 `~/.handoff/skill` 保留：它是 `Status` 的第一个落点，也是一台 agent 都没装的
机器上唯一的落地位置。文件头与 `Install` 的方法注释里「建软链」的措辞一并订正。

### 3.3 老装机的迁移

无需额外代码：`Install` 先 `os.RemoveAll(link)`，对软链只摘链、不动链指向的基准副本，
下一步直接写成实体目录。**这是 `RemoveAll` 语义唯一会咬人的地方，用例钉死**（§5）。

---

## 4. 非目标

- **`handoff project ls` / `rm` 不带 `--target` 仍会连本机 agentd 失败**。这是诚实的：
  纯协调者机上本来就没有本机位置表可列。不为它编造一份空表。
- **不发 Windows release 资产、不修 `handoff upgrade` 的自升级**。release workflow 只出
  darwin/linux 四个资产（[.github/workflows/release.yml:25](../../../.github/workflows/release.yml)），
  `install.sh` 是 bash；Windows 协调者需自行 `go build`，`handoff upgrade` 升本机会明确
  报「没有 windows/amd64 的资产」。这是分发问题，与本文两条无关，要做另立一条。
  注意**升远端不受影响**：`upgrade` 按对端上报的平台取资产（[cmd/upgrade.go:500](../../../cmd/upgrade.go:500)）。
- **不给非 darwin 平台补桌面通知**。`wait --notify` 与 dispatch 自动开终端都是 darwin 硬判，
  其他平台静默跳过。Windows 协调者的唯一唤醒通道是 `wait` 的 stdout。
- **不动 agentd 的 Windows 支持**（B37，已评估·暂不做）。本文让协调者机不再需要它，
  不是让它能跑。

---

## 5. 测试

**client 层**（`internal/client`）：

1. 端口 1（必定 refused）→ `errors.Is(err, ErrUnreachable)` 为真；
2. httptest 返回 409 → **不是** `ErrUnreachable`（拿到了响应）；
3. ctx 取消 → **不是** `ErrUnreachable`。

**cmd 层**（`cmd`，沿用 [root_test.go:182](../../../cmd/root_test.go:182) 既有的
`listen: "127.0.0.1:1"` 造 refused）：

4. 本机 refused + 目标机正常 → 目标机**确实收到**登记请求、stderr 有警告、退 0；
5. 本机返回 409 → 整体失败（降级不吞语义错误）；
6. 无 `--target` + 本机 refused → 失败；
7. dispatch 自动登记路径同款走一遍（首次派发被拒 → 登记 → 重发成功）。

**skill 层**（`internal/skill`）：

8. `TestInstallLinksPointAtBase` 作废（它锁的是软链拓扑），换成「四个落点内容都等于 content」；
9. 迁移用例：落点是**指向基准副本的旧软链**时能被就地换成实体副本，
   且**基准副本仍在**（`RemoveAll` 摘链不删目标，这条必须钉死）；
10. `TestInstallReplacesRealDirectory` / `TestInstallIsIdempotent` / `TestInstallSkipsMissingAgentDirs`
    保持绿。

**平台门禁**：`GOOS=windows GOARCH=amd64 go build ./...` 保持绿（既有
`TestWindowsCrossCompiles` 已覆盖）。

**真机验收**：Windows 协调者上走通 `init`（选协调者）→ `skill install`（四落点实际写入）
→ `dispatch --target <执行机>`（首次派发触发自动登记、本机跳降级、任务真的跑起来）
→ `wait` → `diff` → `done`。**这一条不能用交叉编译代替**：本文两处改动的收益全部
兑现在一台真实 Windows 机器上。无 Windows 机器时如实记为「未验」，不得记为已验。

---

## 6. 风险

**风险一：本机 agentd 只是碰巧挂了，登记会悄悄少一行。**
这是选择自动降级（而非要求配置显式声明）时接受的代价。缓解是 §2.4 的 stderr 警告
必须同时说后果与补救；不缓解的部分是它只说一次。若日后「每次派发都刷一行警告」变得
烦人，再用配置项把警告关掉——但**不要**反过来用配置项来决定降不降级，那会让一台
临时挂了 agentd 的机器在配置说「我有 agentd」时继续硬失败。

**风险二：改副本后 macOS/Linux 上的既有行为变了。**
`ls -la ~/.claude/skills` 从此看到目录而不是链。功能等价（§3.1），且迁移自动（§3.3）。
接受这个变化，理由是替代方案（平台分叉）的长期成本更高。
