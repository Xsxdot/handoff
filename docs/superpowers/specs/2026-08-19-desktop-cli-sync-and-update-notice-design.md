# 桌面端与 CLI 的版本同步 + 新版通知

日期：2026-08-19
状态：设计定案，待 plan

## 1. 问题

装了桌面端的用户，**换一份新的 `.app` 不会让 agentd/CLI 跟着升级，而且他收不到任何信号**。

三道口子都不通：

| 通路 | 现状 | 证据 |
|---|---|---|
| 桌面壳释出内嵌二进制 | `releaseEmbedded` 只在 `state == StateUnconfigured` 分支被调 | `desktop/main.go:125-128` |
| ——它的第三态提示 | 发 `wizard-notice` 事件，只有内嵌向导前端在监听；配置完成后窗口已切到控制台外链页，无接收者 | `desktop/main.go:296` / `desktop/frontend/src/wizard.ts:267` |
| CLI 自更新 | **CLI 永远不自动替换自己**（D13）；`maybeNotifyUpdate` 只在每条 CLI 命令后往 stderr 打一行，纯桌面用户永远看不到 | `internal/selfupdate/clicheck.go:5` / `cmd/root.go:52,284` |
| agentd 自查 | agentd 不查版本。`release.Client.Latest` 全仓只有两个调用方，都在 CLI 侧 | `cmd/root.go:265` / `cmd/upgrade.go:250` |

后果：纯桌面用户的 agentd 永久停在他第一次装桌面端时的版本。更难受的是控制台本身由 agentd 托管——他换了 `.app`、看到的却是一个永不变化的控制台，很容易以为自己已经升级了。

### 1.1 设计时为什么漏掉

不是没想过，是押错了地方。W5 spec §5.2 写明释出落点与 `install.sh` 同路径的理由是「**B59 更新一次两边同时受益**」。这话对——但 B59 是**操作者触发**的（`handoff upgrade --now`），它假设了一个会敲命令的人。桌面端引入的恰恰是「不敲命令的用户」这一类人，而 spec 里没有一条为这类人重新审过 B59 的前提。

## 2. 目标与非目标

**目标**

1. 用户换了新 `.app` 之后，本机 agentd/CLI 自动与之同版
2. 有新版安装包时，桌面用户能知道
3. 协调者能从桌面端升级他的执行机
4. `.app` 自报的版本号真实（§6.4）

**非目标（明确排除）**

- 自动下载并替换 `.app` 自身。要处理 `.app` 自我替换、Gatekeeper、DMG 挂载，是另一个量级
- agentd 周期性自查版本（那会动所有机器上的 agentd，含执行机，风险面大得多）
- 在 GUI 里重建 `handoff upgrade --now` 的编排。壳只 exec 它并显示输出
- 给 `handoff upgrade` 加 `--json`。是更正的解法，但要设计 schema，本次不做

## 3. 决策

| # | 决策 | 理由 |
|---|---|---|
| D1 | 本机同步用**内嵌的那份**，不出网 | 离线可用（W5 spec §5.3 有实证：`github.com:443` 连续两次 75s 超时而 `api.github.com` 正常，首次启动才去下载等于把「能不能用」押在一条被记录过会断的链路上）；版本与 `.app` 严格一致，不冒出第三个版本号 |
| D2 | 同步与通知是**两条独立的路** | 同步不依赖网络所以永不因网络失败；通知失败只是少提示一次，不影响同步。D1 的代价是 agentd 被锁在用户手上那份 `.app` 的版本上，通知路正是用来补这个缺口的 |
| D3 | 无活跃任务就**直接同步**，不问 | 用户刚主动装了新 `.app`，这个动作本身就是「我要升级」的表达；且这正是 `handoff upgrade --now` 的现有语义（无任务直接换、有任务才拦），桌面端应当是它的 GUI 表达而不是另一套 |
| D4 | 有活跃任务时**给强制入口，但藏在一层后** | 挂长期 `waiting_answer` 任务的用户很常见，不给入口等于永远升不了且看不出为什么；藏一层保证它不会被误点 |
| D5 | 执行机升级 = **exec `handoff upgrade --now`** + 输出面板 | 七种结论、两道闸、部分失败不中断、逐行报告全在 CLI 里。壳只负责显示 |
| D6 | **不**调起真实终端 | GUI 进程的 PATH 与登录 shell 不同（B71 同源教训：launchd 拉起的进程 PATH 只有 `/usr/bin:/bin:/usr/sbin:/sbin`）；Windows 还要回答「哪个终端」；命令失败时用户被丢在 shell 里没有指引 |
| D7 | 同步走 `release.Activate`，**不走 `ReleaseBinary`** | 后者的承重是「目标已存在一律报错，绝不覆盖」，那是首次释出的正确语义，本次不动它。`Activate` 是原子 rename + 留 `.prev`，与 CLI 本机升级同一个函数 |
| D8 | 同步失败**绝不阻断**打开控制台 | 沿用 `releaseEmbedded` 已有的哲学。用户双击是想用控制台，不是想升级；把「升级失败」变成「应用打不开」是把小问题放大成大问题 |
| D9 | 版本比较函数**收敛成一份** | 详见 §6.2，这是对既有设计的一处推翻 |

## 4. 架构

三个单元，前两个放 `desktop/internal/shell/`（与 `release.go` / `ready.go` / `lifecycle.go` 同层）：

| 单元 | 职责 | 依赖 |
|---|---|---|
| `sync.go` | 本机同步：判要不要换 + 执行换 | `embedbin`、`release.Activate`、`client` |
| `latest.go` | 查有没有新版安装包，24h 限流 | `release.Client`、`selfupdate` |
| 升级面板 | 显示同步进度与 `handoff upgrade --now` 的流式输出 | 内嵌前端第二个路由 + 独立窗口 |

前两个是纯逻辑 + 一个动作，可单独测；第三个是 UI。

桌面端 import 主模块 internal 包是既有做法，无需新机制（`desktop/main.go:30-33` 已 import `config`/`pathenv`/`service`/`toolchain`）。Go 的 internal 规则按路径前缀判定，与模块边界无关。

### 4.1 判据复用，不新造

`DecideRelease` **已经算得出**「已装的比内嵌旧」——就是它的第三态。本次只做两件事：

- **改名** `DecisionNotifyOutdated` → `DecisionEmbeddedNewer`。原名把处置烧进了枚举名，而现在同一个事实有两种处置（首次引导时提示、已配置时同步）。名字该陈述事实，处置交调用方
- 调用点从「只在 `StateUnconfigured` 分支」扩到两个分支

新增 `PlanSync(decision, busy, embedAvailable) SyncPlan` 在它之上只叠闸一，**不重复实现版本比较**。四态：

| 态 | 含义 | 动作 |
|---|---|---|
| `SyncSkip` | 不需要换，或版本判不出 | 不动 |
| `SyncDo` | 该换且无活跃任务 | 走 §5 的同步序列 |
| `SyncBlocked` | 该换但有活跃任务 | 托盘挂条目，见 §7.2 |
| `SyncNoEmbed` | 开发构建未带 `-tags embedbin` | 不动，只记 Debug 日志 |

## 5. 同步路：顺序是承重的

`openConsole` 的已配置分支改成：

```
1. specFor → EnsureRunning          起旧的，先让 agentd 可达
2. 读已装版本 → DecideRelease        对账
3. client.Status → len(Active)      闸一判据
4. PlanSync 四态分支
   ├ SyncDo      等 webview → Show → 进度 → Activate 换文件
   │              → exec 新二进制 skill install → RestartAgentd → 等 agentd 回来
   ├ SyncBlocked 不动，托盘挂「有更新待应用（N 个任务进行中）」
   └ 其余         不动
5. 等 webview → 握手 → SetURL(控制台)
```

三条顺序是承重的：

**① `EnsureRunning` 必须在对账之前。** 闸一判据要从 agentd 的 `/api/status` 探（`client.Status` → `StatusResp.Active`），agentd 不在跑就探不出。这里起的是**旧**二进制——无妨，同步紧接着会重启它。

**② 闸一必须在换文件之前**，与 `cmd/upgrade.go:500` 同序。反过来会留下「磁盘上是新的、跑着的是旧的」这种持续不一致：`handoff version` 报新版而 agentd 是旧版，用户看不出为什么，且该状态会一直持续到他下次重启。

**③ `RestartAgentd` 之后必须等 agentd 重新可达才握手。** 它优雅退出后由 launchd/schtasks 拉起，中间有个连不上的窗口。不等就是握手 401 或连接被拒——而这个报错看起来跟「刚升过级」毫无关系，排查时会往完全错的方向走。

「重新可达」的判据写死为：**轮询 `client.Status` 直到返回的版本号等于 `embedbin.Version`**，超时上限 90 秒（Windows 的 60 秒重复触发窗口 + 余量）。

判据必须是**版本号相等**而不是「Status 调得通」——旧进程优雅关停期间仍会应答在途请求，只判连通会立刻通过，然后握手到一个正在退出的 agentd 上。这是「就绪判据取早了」那一类假绿，本仓库已经吃过（见 flaky-tests 的记录）。

### 5.1 必须带上 `skill install`

CLI 本机升级是**五步**不是三步（`cmd/upgrade.go:555` `localUpgrade`）：定位二进制 → 取新字节 → `Activate` → **`syncSkill`** → `RestartAgentd`。

第四步不能省。skill 随二进制分发（B59），换了二进制不同步 skill，协调者手上就是一份**按已经变了的状态机主动误导他**的旧 skill。且必须 exec **新**二进制来跑——当前进程内嵌的是旧的（`cmd/upgrade.go:591` 的注释已记此事）。

### 5.2 Windows 的重启窗口

Windows 的 KeepAlive 是**每分钟一次**的模拟（`internal/service/windows.go:150` 的 `<Interval>PT1M</Interval>`），不是 launchd 那种秒回。agentd 自更新退出后最坏要等 60 秒。

处置：换版后**主动催一次 `schtasks /Run`**（`internal/service/windows.go:271` 已有这条 + 500ms 轮询复核），不要傻等重复触发。

催的时机必须在旧进程真的退出之后——`MultipleInstancesPolicy=IgnoreNew` 会把早催的那次拒掉，而且拒绝时写进「上次结果」的正是 `0x800710E0`（十进制 `-2147020576`），也就是 rc5 那个 bug 的同一个值。

### 5.3 为什么 `Activate` 而不是覆写

除 D7 的语义理由外，还绕开一个已踩过的坑：`Activate` 是 rename 落位，产出**新 inode**。直接覆写会撞上 macOS 的 provenance 把 inode 钉住，进程当场 SIGKILL 且无任何输出（`rc=137`，排查时完全看不出根因）。

## 6. 通知路：有新版安装包

`CheckLatest(ctx, dataDir) (tag string, newer bool)`：查 GitHub 最新 release，与本机 `.app` 的版本比。

点击行为：**打开浏览器到 release 页面**，用户自己下载安装。

失败：**静默**，当作「没有新版」。沿用 clicheck 既有约定（「读缓存的失败一律静默」）——通知路是锦上添花，它自己绝不能成为故障源。

### 6.1 共用 CLI 那份限流缓存

用 `<dataDir>/update/cli-check.json`，即 `selfupdate.CLICheckPath` 那份，24h 判据用 `selfupdate.CLICheckStale`。

看着像耦合，其实正是要的：同一台机器 24h 内查一次就够，而 `api.github.com` 有 60 次/小时/IP 的匿名限流。多个消费者各查各的正是触发限流的方式，而限流一旦触发，agentd 的换版也会跟着失败。

### 6.2 版本比较函数收敛（推翻一处既有决定）

现状是**两份**实现：`selfupdate` 里未导出的 `cmpVersion`/`parseVersion`，和 `desktop/internal/shell/release.go` 里为绕开它另写的 `compareVersion`。后者的注释明确拒绝过改 selfupdate 的导出面：「不值得为此改动 selfupdate 的导出面」。

**本次推翻它**：把 `selfupdate` 的版本比较导出，`shell` 那份删掉改为调用。

理由：那个判断在两个消费者时成立，加上通知路就是第三个了。三份实现里一定会有一份哪天被改歪，而这个函数的历史已经证明它会被写错——B59 验收当场抓出的反向提示（装了 v0.1.1 的机器被劝「有新版本 v0.1.0」），根因正是没按三段整数比。

### 6.3 拿什么版本去比

用 `embedbin.Version`。**它是这份 `.app` 里唯一一个来自 TAG 的版本**（`release.yml:380,543,733` 的 `EXTRA_LDFLAGS` 注入），`release_workflow_test.go` 已有断言钉住这条注入存在。

**不能用 `Info.plist` 的版本**：那里的 `CFBundleVersion` / `CFBundleShortVersionString` 是**写死的 `0.1.0`**，从不随 TAG 变。设计期核实发现，见 §6.4。

判据：`CheckLatest` 在 `embedbin.Version` 为空时（开发构建未注入）**一律判「没有新版」**，不提示。与 `DecideRelease` 的保守约定同源——基准判不出时不猜，猜错的症状（一直提示或永不提示）都不会报错，排查成本极高。

### 6.4 顺带发现：`.app` 的版本号是写死的

`desktop/build/darwin/Info.plist` 的 `CFBundleVersion` 与 `CFBundleShortVersionString` 都硬编码为 `0.1.0`，不随 TAG 变。于是每一份发出去的 `.app` 在访达「显示简介」里都自称 0.1.0，而它内嵌的 CLI 是真实 tag。

这不影响本设计（§6.3 已改为只认 `embedbin.Version`），但它本身是个缺陷：用户想回答「我装的是哪个版本」时，系统给他的是一个假答案。

**本次一并修**：让 `release.yml` 在打包前把 TAG 写进 `Info.plist`，并加一条契约测试断言这两处版本不再是常量。范围很小（一个 `plutil`/`sed` 步骤 + 一条测试），而放着不修的话，P1/P2 真机走查里「确认装的是新版」这个判据本身就没有可信的读数。

## 7. 升级面板

内嵌前端加第二个页面（`/upgrade.html`），装在**独立窗口**里——主窗口此时正显示控制台外链，不能抢。

### 7.1 rc7 的坑会在这里重演

新窗口同样要等它自己的 webview 建好才能发事件或导航。`windowsWebviewWindow.setURL` 至今没有 nil 守卫（相邻的 `execJS` 有 `if w.chromium == nil { return }`），导航进一个没建好的 chromium 会让进程**直接消失、没有任何输出**。

`AwaitWebviewReady` 直接复用，但**必须为新窗口单独挂 `WindowRuntimeReady`**。漏挂就是同一个 bug 的第二次。

### 7.2 三态与强制重试

三态：运行中（流式追加输出）/ 成功 / 失败（退出码非零）。

**「强制重试」按钮：只要退出码非零就亮出来**，不去解析输出文本判断是不是闸一导致的。

理由：`upgrade --now` 的输出是给人看的中文表格，没有 JSON 模式；解析「N 个活跃任务」这种串是脆的，格式一改就悄悄失效——而失效方式是「按钮再也不出现」，没有任何报错。

代价说清：网络失败之类的非闸一原因，用户点了也没用，白跑一次。缓解是按钮旁一行小字说明它只对活跃任务那种失败有用，完整输出就在上面供他自己判断。

`SyncBlocked`（本机同步被闸一拦）走同一个面板：托盘条目 → 面板显示活跃任务数与实际代价 → 「仍然同步」按钮 = `RestartAgentd(force=true)`。

实际代价要写准：执行者是 setsid 出去的独立进程，B59 V3 实测跨过 agentd 重启存活 16m29s，工单也在库里不丢；重启真正打断的是**事件推送与在途请求**，不是任务本身。

## 8. 错误处理

总原则见 D8。分级：

| 失败点 | 处置 | 用户的实际后果 |
|---|---|---|
| 读已装版本失败 | 判不出 → `SyncSkip` | 用旧的，与 `DecideRelease` 的保守约定一致 |
| `Activate` 失败 | 记日志 + 面板如实说，继续 | 磁盘没变，旧版完好 |
| `skill install` 失败 | **必须说出来**，但不算同步失败 | 二进制已换、skill 是旧的。悄悄留一份旧 skill 会按已变的规则主动误导协调者（沿用 `syncSkill` 既有措辞） |
| `RestartAgentd` 失败 | 提示「已换文件，agentd 仍是旧版，重启后生效」 | 即 `swapAndTell` 的既有语义 |
| 等 agentd 回来超时 | 提示 + 仍尝试握手 | 可能成功也可能 401，如实报，不猜 |
| `CheckLatest` 任一步失败 | 静默，当作没有新版 | 少提示一次 |

## 9. 测试策略

四层，对应四种失败模式：

1. **纯函数穷举** — `PlanSync` 四态 × 输入组合。`DecideRelease` 既有测试保持
2. **顺序钉死** — 用记录调用序列的假实现，断言 `EnsureRunning → Status → Activate → skillInstall → RestartAgentd` 的相对次序。**必须变异复验**：顺序断言最容易写成看着在测、其实恒真
3. **失败注入** — §8 每个失败点各一条，断言「控制台仍被加载」。这是 D8 的唯一守卫
4. **契约测试** — §6.3 的版本变量同源断言

日志按 `instrumenting-code` 铺：进入同步、四态决策结果、每一步成败、重启后的等待结果。这不只是可观测性——真机走查时它是唯一的取证手段（rc7 那个 bug 就是靠 agentd 日志里「签发 ticket 但从未被消费」定位的）。

## 10. 真机验收

单测验不了的，必须真机走查。清单进 `docs/windows-desktop-acceptance.md` 与一份 macOS 对应文档。

| # | 验什么 | 平台 | 判否则怎样 | 状态 |
|---|---|---|---|---|
| P1 | 换版后 launchd 真把新版拉起来，`handoff version` 与 `.app` 同版 | macOS | 同步等于没做 | 未验 |
| P2 | 换版后 schtasks 真把新版拉起来，且主动催 `/Run` 确实缩短了等待 | Windows | 用户要盯 60 秒空白 | 未验 |
| P3 | Gatekeeper 不拦 `~/.local/bin` 里那份（它脱离了 `.app` 的签名覆盖） | macOS | §5 整条路走不通，须回到 bundle 内运行方案 | 未验（W5 spec §5.4 的 P2 同一件事，至今未验） |
| P4 | 有活跃任务时同步确实不发生，托盘条目出现；强制后任务未被杀 | 两平台 | D4 的前提不成立 | 未验 |
| P5 | 新窗口在两平台都能出来（rc7 同款风险） | 两平台 | 进程静默消失，无输出 | 未验 |
| P6 | 同步失败时控制台**仍能打开** | 两平台 | D8 被破，双击打不开应用 | 未验 |

P3 是最承重的一条：它若不成立，§5 整条路都要重做。它与 W5 spec §5.4 的 P2 是同一件事，那条至今未验——**本 plan 必须把它排在最前面**，不能留到最后才发现。

## 11. 已知风险

**① 这条路径在用户最不能容忍出错的时刻运行。** 双击打开应用，是整个产品里唯一一个「必须成功」的动作。我们在它前面插了一段会换二进制、重启服务的逻辑。第 3 层测试（失败注入）与 D8 是唯一的保险，plan 里不能砍。

**② 面板是第三个 UI 面，而前两个各自都出过一次真机才照得出的 bug**（向导那次是 `showError` 在 `app.Run()` 之前 nil deref；控制台那次是 rc7 的 webview 未就绪）。新窗口大概率也有一个，走查清单要为它单列一节。

**③ D1 的固有代价：agentd 被锁在用户手上那份 `.app` 的版本。** 通知路（§6）是补丁而不是根治——它依赖用户看到提示后主动去下新 `.app`。真正的根治是 agentd 自查 + 控制台通知，那是非目标，另记。

## 12. 对既有设计的推翻记录

| 被推翻 | 出处 | 为什么 |
|---|---|---|
| 「不值得为此改动 selfupdate 的导出面」 | `desktop/internal/shell/release.go` `compareVersion` 注释 | 两个消费者时成立，第三个出现后不成立。见 §6.2 |
| 「B59 更新一次两边同时受益」隐含的前提 | W5 spec §5.2 | B59 是操作者触发的，假设了会敲命令的人；桌面端引入的正是不敲命令的用户。见 §1.1 |
| `DecisionNotifyOutdated` 这个名字 | `desktop/internal/shell/release.go` | 把处置烧进了枚举名，而同一事实现在有两种处置。见 §4.1 |
