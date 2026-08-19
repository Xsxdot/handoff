# 控制台「常规」分区与机器级默认执行者 —— 设计

> 日期：2026-08-19
> Backlog：**B160**
> 基线：`handoff/web-console`（含 B157 纪律配置面；B158 env 配置面在途，本设计不依赖它）
> 姊妹条目：**B158**（Env 文件配置面）。B158 从 B157 拆出去时留下了这一条，理由是
> 「常规里放什么尚未定义，且横跨客户端偏好与 agentd 服务端配置两种持久化」。
> **本设计的首要产出就是把「放什么」定义出来**（§1.1），其次才是实现。

## 1. 问题

设置页的「常规」分区今天是一句占位（`SettingsPage.tsx`：「常规设置本期没有可配置项。
桌面行为、主题、快捷键等留待后续」）。

同一时刻，开发机详情里挂着一条 `NOT_WIRED`：

```
可用执行者 —— 执行者开关尚未实现：需要 agentd 提供机器级配置的写接口。
```

这两件事看起来无关，其实是**同一个缺口的两面**：控制台今天只能改「以文件为单位」的
配置（纪律块、env），改不了任何**标量**配置项。B157/B158 各自打通了一条「文件 →
映射」的路，标量那条路一次都没走过。

### 1.1 归属判据（本设计的核心产出）

「常规」这个名字最大的风险是变成杂物间。判据只有一条——**这个设置的持久化落在哪，
它就属于谁**：

| 类别 | 落在哪 | 影响范围 | 落点 |
|---|---|---|---|
| A. 客户端偏好 | 浏览器 `localStorage` | **只影响这一个浏览器** | 「常规」分区 |
| B. 某台机器的运行参数 | 该机 `config.yaml` | 该机上所有任务 | **开发机详情** |
| C. 协调者 CLI 本机的行为 | 敲 CLI 那台机的 `config.yaml` | 你的终端 | **两处都不放**（见下） |

**C 类为什么两处都不放**——这是最容易做错的一格。`Sync.Auto`（wait 结束后自动
fetch）与 `Terminal.Auto`（dispatch 后弹终端）读取方是**协调者本机的 CLI**，不是
agentd。控制台连的是某台 agentd，改它的 `config.yaml` 只影响**那台机器上**敲的
CLI。而你多半是在另一台机器上敲命令。把它们画进控制台，用户会改完发现没生效，
且找不到原因——**一个改了不生效的开关，比没有这个开关更糟**。

按这条判据分完，今天的实际清单是：

- **A 类只有一样**：左栏显示偏好（`treePrefs`：项目排序、隐藏项目、折叠空闲目录）。
  主题与快捷键都还不存在，占位文案里的承诺现在**兑现不了，也不该假装能**。
- **B 类里值得现在做的只有一对**：`Executor.Default` 与 `Executor.Model`。
- 其余 B 类一律**只读或不做**，逐条理由见 §1.2。

### 1.2 不做清单（每条都有具体理由）

| 配置 | 为什么不给写 |
|---|---|
| `Listen` / `Token` / `DataDir` | 改错当场把自己锁在门外，且改完必须重启 agentd——而「重启 agent」在控制台里至今没实现（同一块 NOT_WIRED 的第二条） |
| `Web.AllowedHosts` | 同上，且它就是 Host 白名单本身：写错 = 下一次刷新 403 |
| `RepoRoot` | 改它不会搬走已经 clone 的仓库，只会让**新**项目落到别处，制造一台机上两个仓库根 |
| `PathDirs` / `EnvForward` | 只在 agentd **启动时**读（注入 PATH / 转发进 PTY），改完不重启等于没改 |
| `Proxy` | 值里常含 `user:pass@`。写它就要在控制台里输入凭据，而 `internal/proxycfg` 的既定纪律是这个值**不得出现在任何日志文本里**。只读且**脱敏展示**（`proxycfg.Redact`） |
| `Approver.*` | 它是安全裁决链。配错的后果是「本该升级给人的请求被廉价模型自动批了」，这不该是一个下拉框顺手能干的事 |
| `ProcFence` / `StallTimeout` | 有默认值且极少改；`StallTimeout` 已在 `GET /api/status` 里只读外露，够用 |
| `Sync.Auto` / `Terminal.Auto` | C 类，见 §1.1 |

**明确的非目标**：不做主题、不做快捷键、不做「重启 agent」、不做执行者的**启停**
开关（那需要 agentd 支持动态注册/注销 adapter，是另一件事）。

## 2. 形态

### 2.1 「常规」分区 = 这个浏览器的偏好

一屏，顶部一句话点明范围：

```
这些设置只保存在当前浏览器里，不同步到其他设备，也不影响任何一台开发机。
```

内容就是左栏那个 `TreePrefsMenu` 的全部三项（项目排序 / 隐藏项目 / 折叠空闲目录），
**在设置页里平铺展开**，而不是复用那个下拉菜单的紧凑形态——设置页有空间，菜单没有。

**两处是同一份状态，不是两份**。这是本块唯一的实现难点：偏好今天由
`ProjectTree` 用 `useState(() => loadPrefs())` **私有持有**，设置页里改一份，左栏那
份不会知道。做法见 §4.3（抽共享 hook），**不接受「设置页只读」这种绕法**——一个
看得见改不动的偏好页毫无价值。

「常规」里不再放别的。分区如果只有三项就只有三项——**不为了填满一屏去发明设置**。

### 2.2 开发机详情新增「默认执行者」块

挂在「执行纪律」块（B157）与「Env 文件」块（B158）之后，形态与它们同构：

```
默认执行者   [ opencode          ▾ ]   不带 --executor 派发时用它
默认模型     [ gpt-5.6-luna        ]   留空 = 各执行器用自己的默认模型
                                        ⚠ 这一项是机器级的，不分执行器
                        [ 保存 ]  保存后下一个任务即生效
```

改动即标脏、整块一个「保存」——与另外两块逐字同构。

同时**退役** `MachineDetail.tsx` 里 `NOT_WIRED` 的「可用执行者」那一条：它承诺的
「机器级配置的写接口」正是本块。执行者的**列表**继续只读展示（带「默认」标记），
本块把那个标记变成可改的。「重启 agent」「打开终端」两条 NOT_WIRED **保持原样**。

### 2.3 「默认模型」到底作用于谁——先实测，再写文案

这一格是本设计里唯一一处**我原本记错、靠读代码纠正过来**的地方，记在这里防止
下一个人照着旧印象写：

`resolveModel`（`internal/agentd/manager.go:329`）今天的实现是：

```go
	if execName == m.cfg.Executor.Default {
		return m.cfg.Executor.Model
	}
	return ""
```

即 **`Executor.Model` 只对缺省执行者生效**，派别的执行器时不套用。函数上方的注释
写明了这是修过的行为：「以前不分执行者一律套上，于是配了 opencode 模型名的机器派
codex 时第一回合就被 provider 顶回 400」。

**结论有两个，都影响形态**：

1. `Default` 与 `Model` 是**语义耦合**的一对——`Model` 是「`Default` 的模型」，
   不是全局默认。所以它们必须在**同一块**里、共用一个保存按钮，中间不能插别的东西。
   界面上 `Model` 的标签就该写成随 `Default` 变的活文案：`opencode 的默认模型`。
2. 该写的提示**不是**「⚠ 这一项不分执行器」（那是旧行为），而是：

   > 只对上面选的缺省执行者生效。派其他执行器时用 `--model` 逐次指定。

   placeholder：`留空——用 opencode 自己的默认模型`。

**服务端仍然校验不了 `Model`**：agentd 不认识任何执行器的模型名单，没有可判据
（模型名按执行器、也按机器不同——实测 codex 在 mac-02 上叫 `gpt-5.6-luna`，在
win-b37 上叫 `deepseek-v4-pro`）。这是「用文案承担校验」的少数正当场合之一。

## 3. 后端接口

两个端点，走 `?machine=` 转发（复用 `forwardIfRequested`）。

| 方法 | 路径 | 用途 |
|---|---|---|
| GET | `/api/executor/default` | 该机的默认执行者、默认模型、可选名单 |
| PUT | `/api/executor/default` | 保存这两项 |

### 3.1 为什么不复用 `Machine.default_executor`

`Machine` 是**探活投影**，由 15s 轮询刷新，且它没有 `model` 字段。拿一个被轮询
覆盖的对象当编辑基线，用户输到一半就会被刷掉——B157 已经为此立过「配置面不进
轮询」的规矩，这里照办。

### 3.2 数据结构（`internal/proto/executor_default.go`）

```go
// ExecutorDefaultResp 是 GET /api/executor/default 的响应。
type ExecutorDefaultResp struct {
    Default   string   `json:"default"`   // 当前缺省执行者名
    Model     string   `json:"model"`     // 机器级默认模型；空串 = 各执行器用自己的默认
    Available []string `json:"available"` // 该机已注册的 adapter 名，按名字升序
}

// ExecutorDefaultReq 是 PUT /api/executor/default 的请求体。
//
// 两个字段都是**必填但可为空串**：整体替换语义，缺席与空串一视同仁。
// Model 为空串是有意义的取值（= 不设机器级默认），不是「不改」。
type ExecutorDefaultReq struct {
    Default string `json:"default"`
    Model   string `json:"model"`
}
```

### 3.3 校验与错误语义

| 情况 | 状态码 | 说明 |
|---|---|---|
| `default` 不在该机已注册 adapter 名单里 | 400 | 错误里**列出可选名单**——「未知 executor "opencde"（可选：claude/codex/fake/grok/opencode）」 |
| `default` 为空串 | 400 | 缺省执行者不能没有：为空时 `adapterFor` 会拿空名查注册表，每一次不带 `--executor` 的派发都失败 |
| `model` 任意值 | — | **不校验**，理由见 §2.3 |
| manager 未就绪 | 503 | 名单来自 manager，与 discipline/env 同款 |

`model` 前后空白 `TrimSpace` 后落盘；`default` 同理。

**成功响应回 `ExecutorDefaultResp`**（保存后的最新状态），界面直接拿它刷新——与
`PUT /api/discipline/mapping` 同款。

## 4. 既有点改造

### 4.1 `swapConf` **不需要**再补深拷

`Executor` 是 `config.ExecutorConfig` 值类型（两个 `string` 字段），结构体浅拷即
完整拷贝。**这一条要显式写进实现说明**，否则实现者会照着 `Targets` / `Discipline`
/ `Env` 的样子再加一层「深拷」，而对一个无引用字段的 struct 做深拷是无意义的
噪声，还会让下一个人以为它有引用字段。

`swapConf` 落盘成功的那行日志加一个 `default_executor` 字段即可。

### 4.2 `Manager` 读的是**构造时的配置快照** —— 本设计的承重改动

已实测确认：`cmd/agentd.go:183` 是 `agentd.NewManager(st, srv.Hub(), ads, cfg, …)`，
`Manager.cfg` 存的就是这个 `*config.Config` **指针**。而 `swapConf` 做的是
`next := *old` + `s.cfg.Store(&next)`——**换的是一个新指针**，`m.cfg` 仍指向原来那份。

所以：**照抄 B157/B158 的「保存后下一个任务即生效」在这里不成立**。B157 的纪律块
之所以能热更新，是因为它走的是 `s.DisciplineMapping`（读 `s.conf()`），根本没经过
`m.cfg`；`Executor.Default` 没有这条旁路。

**已实测列全的读点（7 处，一处都不能漏）**：

| 位置 | 读的是 | 漏改的后果 |
|---|---|---|
| `manager.go:288` `adapterFor` | `Executor.Default` | 恢复/续接老任务时回退到旧缺省 |
| `manager.go:305` `resolveExecutor` | `Executor.Default` | **dispatch 主路径**，漏了等于没做 |
| `manager.go:333-334` `resolveModel` | `Default` + `Model` | 模型不跟着变 |
| `manager.go:1155` | `Executor.Default` | |
| `manager.go:3196` | `Executor.Default` | |
| `status.go:72` `DefaultExecutor` | `Executor.Default` | **`GET /api/status` 会一直回旧值**——而开发机列表的「默认」标记就来自它，保存完界面上那个标记不动，看起来像没保存成功 |
| `status.go:138` | `Executor.Default` | 活动任务行的执行者名回填 |

做法：`Manager` 加一个 `conf func() *config.Config` 字段（生产传 `(*Server).Conf`，
测试传 `func() *config.Config { return cfg }`），上述 7 处改走它。与 B157 给
`discipline.Resolver`、B158 给 `envfile.Resolver` 做的是同一件事，**第三次了**——
这次做完，把「`Manager` 不得缓存配置快照」这条写进 `NewManager` 的注释。

> 注意 `Server` 今天的 `conf()` 是**私有**的（`internal/agentd` 包内可见），
> `Manager` 与 `Server` 同包，所以直接传 `s.conf` 即可，不必新增导出方法。
> 这与 B157/B158 需要导出 `DisciplineMapping`/`EnvMapping` 不同——那两个是给
> 别的包（`internal/discipline`、`internal/envfile`）用的。

> 这是本设计里唯一的**结构性**改动，也是它比「加两个 handler」值钱的地方。
> 计划阶段必须为它单开一个 task，不许折进 handler 那个 task 里。

### 4.3 前端：偏好状态要提到共享层

新建 `web/src/app/tree/useTreePrefs.ts`：模块级单例 + 订阅者集合，`loadPrefs()` 惰性
初始化一次，`update()` 写 `localStorage` 并通知全部订阅者。`ProjectTree` 与新的
「常规」分区都改用它。

**不用 Context**：`ProjectTree` 与 `SettingsPage` 不在同一棵子树下（设置页整页替换
中央内容区），套一层 Provider 要动到 `Shell`，收益不抵改动面。模块级订阅是这个
形状最小的解。

`TreePrefsMenu` 本身**不动**：它已经是无状态受控组件（`prefs` / `projects` /
`onChange`），换个数据源即可。「常规」分区平铺的那份是**另写一个展示层**，共用
`treePrefs.ts` 的纯函数与 `useTreePrefs` 的状态——不复用菜单的紧凑形态。

## 5. 前端落点

| 文件 | 动作 |
|---|---|
| `web/src/app/tree/useTreePrefs.ts` | 新增：模块级订阅 + localStorage |
| `web/src/app/tree/ProjectTree.tsx` | 改用 `useTreePrefs`，删掉私有 `useState` + `savePrefs` |
| `web/src/app/settings/GeneralPage.tsx` | 新增：平铺的三项偏好 + 范围说明 |
| `web/src/app/settings/SettingsPage.tsx` | 「常规」占位换成 `<GeneralPage />` |
| `web/src/app/machines/MachineExecutor.tsx` | 新增：默认执行者 + 默认模型块 |
| `web/src/app/machines/MachineDetail.tsx` | 挂上新块；`NOT_WIRED` 删掉「可用执行者」那条 |
| `web/src/api/types.ts` / `client.ts` | 两个接口的类型与函数 |

沿用 B157/B158 已确立的两条纪律：**配置面不进轮询**、**错误原文透传**。

## 6. 契约与测试

契约走既有流程：Go 结构 → `-update` 刷新 fixture → 同步 TS 类型与 `contract.test.ts`。

Go 侧：两个 handler 的正常路径 + §3.3 每一行错误语义；`?machine=` 转发；**一条
承重回归**——改完默认执行者**不重建 Manager**，随即 `adapterFor` 解析出的就是新
adapter（这条直接钉住 §4.2，缺了它整个改动等于没做）。

前端 vitest：
- 「常规」改一项后 `localStorage` 立即落盘，且**同一测试里挂载的 `ProjectTree` 跟着变**
  （这条钉住 §4.3 的共享状态，两处各测各的等于没测）；
- 默认执行者块的保存 payload；`default` 选了不存在的名字时展示后端 400 原文；
- 断开机器降级。

## 7. 风险

**把 `Executor.Default` 改成该机没有的执行者名，会让此后所有不带 `--executor` 的
派发全部失败。** 这是本设计引入的唯一一个「一个下拉框能搞挂一台机」的操作，所以
下拉框的选项**只从 `Available` 生成**（不是自由输入），服务端再校验一次。两道门都
要有：界面那道防误操作，服务端那道防绕过界面的调用方。

`Executor.Model` 没有这道保护（§2.3：校验不了）。但它的失败面比旧印象里小得多——
`resolveModel` 只在 `execName == Default` 时套用，所以填错**只影响缺省执行者、且
只影响不带 `--model` 的派发**，失败还是**当场**的（第一个事件就是 400 或秒退），
不会静默跑偏。

**改 `Default` 会连带改变 `Model` 的作用对象**：把缺省从 opencode 换成 codex，
那个原本给 opencode 填的模型名会立刻套到 codex 头上——这正是注释里记的那个 400。
所以两项必须同块保存，且切换 `Default` 时界面要**当场刷新 `Model` 的标签**
（「opencode 的默认模型」→「codex 的默认模型」），让这个连带效应在保存前就可见。

## 8. 验收判据

1. 「常规」分区可改三项偏好；**改完不刷新页面**，退出设置回到工作台，左栏当场是新
   排序 / 新折叠状态。反向亦然（左栏菜单改完，进设置页看到的是新值）。
2. 开发机详情可改默认执行者与默认模型并保存；落盘后 `config.yaml` 的 `executor`
   段与界面一致。
3. **承重判据**：改完默认执行者，**不重启 agentd**，随即向该机派一个**不带
   `--executor`** 的任务，实际起来的是新选的执行者。
4. 选一个不存在的执行者名（绕过界面，直接 PUT）被 400 拒绝，错误里列出可选名单。
5. `MachineDetail` 里不再有「可用执行者」那条 NOT_WIRED，「重启 agent」「打开终端」
   两条仍在。

## 9. 取号

按三路并集实测（`main` ∪ `handoff/web-console` ∪ 分支名认领，本地与 origin 取较大值）：
最大为 **B159**，故取 **B160**。

**这个号是改过一次的**：写 spec 时算出 max=158、取了 B159，落地前复核发现
`origin/handoff/web-console` 上已经有人推了 B159（「TestApproverApprovesPermission-
WithoutWaking 偶发红」）。这是同一周内第二次撞——**汇流点在被并行推进，算出来的
max 只在那一刻成立**。开工前请再算一遍。
