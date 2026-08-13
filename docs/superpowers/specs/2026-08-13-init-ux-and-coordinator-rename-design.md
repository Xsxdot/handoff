# B83：`handoff init` 交互重做 + 「审核者」更名为「协调者」

> 状态：设计完成，待实现
> 来源：08-13 用户走完 v0.2.0 安装全流程后，对着本机 `handoff init` 截图提出的九条改造；角色更名按产品级统一（方案 B）定案
> 前置：B54.2（`handoff init` / `handoff service`）、B59（`update.auto` 废弃）、B71（探测表 + 托管追问）

## 1. 问题

### 1.1 向导本身

现行 `handoff init`（[cmd/init.go](../../../cmd/init.go)）是 `prompt [default]:` 一行一问。08-13 真机走完一遍后暴露的不是「少问一句」，是形态：

- 角色、执行者、审批链全靠手打数字或名字。截图里监听提示写着 `0.0.0.0:7777`，对端机器实际写成了 `0.0.0:7777`——手填把提示抄残了，agentd 仍绑上了 `*:7777`，但配置已经是非法 IPv4。
- 「缺省执行者」是内部词；「审核者」被理解成「审代码的人」，不是「派发、批权限、盯任务的那一端」。
- 探测到 codex 就贴一段代理专文（B30）。这段只覆盖一家，且把「怎么配 env」埋在一段故障叙事里，装机时没人会照做——08-13 重装后 mac-02 的 `env: {}`、没有 `~/.handoff/env/`，正是这段提示没起作用的实证。
- `update.auto` / `update.interval` 仍在问。B59 已经让这两个键**没有任何效果**，问它们等于教人去开一个已死功能。
- 跑完没有「可以重跑」的提示。幂等其实早就有（默认取当前配置），用户看不见。

### 1.2 监听语义被问成了广告地址

`listen` 是 **bind 地址**（agentd 绑哪张网卡），本机 CLI 又拿 `cfg.Listen` 当连接地址（[cmd/root.go](../../../cmd/root.go) `TargetEndpoint`）。把探到的局域网 / Tailscale IP 写进 `listen` 会同时踩两坑：

| 写入 `listen` | 本机连 `127.0.0.1` | 外机 | IP 一变 |
|---|---|---|---|
| `127.0.0.1:7777` | 通 | 不通 | 无 |
| `0.0.0.0:7777` | 通（连 `0.0.0.0` 落到本机） | 通 | 无 |
| `192.168.x.x` / `100.73.x.x` | **不通** | 只通这张网卡 | bind 失败，agentd 起不来 |

现行提示「要被外机访问需改成 `0.0.0.0:7777`」是对的，但默认仍是 `127.0.0.1:7777`、输入是自由文本，执行机几乎必然手填错。配对片段里的 `addr` 才该放具体 IP，却用 `pairAddr` 把 `0.0.0.0` 换成字面量 `<本机IP>`，抄的人还得自己再填一次。

### 1.3 角色名

产品里「审核者」同时指：派发计划的人、批权限的人、审 diff 的人、以及「人工」这一层（对比廉价审批模型）。用户把它读成 code review。定案改成 **协调者**：驱动任务全生命周期的那一端。廉价模型仍叫 **审批者**，人仍叫 **人工**。

---

## 2. 目标与非目标

### 目标

- TTY 下 `handoff init` 用可选列表完成角色、默认执行者、监听、审批链；自由输入只留给模型名、路径、配对字段、手填监听。
- 监听拆成「绑定」和「广告」：选项只决定 bind；探到的 IP 只进配对片段的 `addr`。
- 删掉 `update.auto` / `update.interval` 两问；codex 专文换成通用 env 提示。
- 结尾明确 `init` 可重跑。
- 产品可见面「审核者」统一改成「协调者」。

### 非目标

- **不改协议与状态机名字。** `waiting_review`、事件类型、Codex 字段 `approvalsReviewer` 一律不动。改它们是破坏性契约，和这次的用词无关。
- **不重写历史文档。** `docs/superpowers/` 里已定稿的 spec / plan / review / notes 保持原文。「审核者」作为当时的用词，改了会让旧证据对不上。
- **不做 env 向导。** 不创建、不编辑、不询问 `~/.handoff/env/*.env`。只提示存在这条路。`init` 重跑不得碰这些文件。
- **不把 huh 接到非 TTY 路径。** `curl | bash` 和测试喂答案继续走「一问不问 / 脚本化 prompter」。
- **不升级成全屏 TUI。** 一个表单不值得自研 bubbletea 应用。
- **不改探测判据。** 仍走 `internal/toolchain`，不发真实模型调用。claude 继续报「登录态未知」，不得猜成就绪或未登录。

---

## 3. 决策记录

| # | 决策 | 选它的理由 | 放弃的替代 |
|---|------|-----------|-----------|
| D1 | **TTY 交互用 `charmbracelet/huh`** | 选择 / 确认 / 输入三件套够用，观感接近 `gh`，SSH 可用。现有 `go.mod` 很瘦，只加这一层（会带上 bubbletea / lipgloss） | 手写 bubbletea（一个表单不值得）；`survey` / `promptui`（观感旧、维护冷） |
| D2 | **`prompter` 接口挡住 huh** | 现有 init 单测按行喂答案、用 `initStdinIsTTY` 缝。huh 要真终端。抽接口后生产走 huh，测试走脚本化实现，不上 PTY | 每条用例起伪终端；把测试改成只断言非 TTY 路径（覆盖塌掉） |
| D3 | **监听只给三档：仅本机 / 所有网卡 / 手填** | bind 和广告是两件事。把探到的 IP 做成 listen 选项，等于教人把 DHCP / Tailscale 地址写进 bind | 把每个网卡 IP 列为 listen 候选项 |
| D4 | **探到的 IP 只写进配对 `addr`** | 协调者机要连的是「现在能通的那张地址」，不是 `0.0.0.0`，也不是 `<本机IP>` 占位符 | 继续打印占位符让人自己填 |
| D5 | **产品级更名，协议与历史文档不动** | 用户看见的词必须一致（README、内嵌 skill、init、帮助、当前代码日志/注释）。协议字段和旧 spec 改了只制造失配 | 只改 init 文案（skill 里仍写审核者，AI 会把两套词当成两个角色）；连 `waiting_review` 一起改 |
| D6 | **彻底删除 `update.auto` / `update.interval`** | 留在 yaml 里等于继续宣传一个已死功能，08-13 重装后两边都还写着 `update.auto: true`。B59 留字段是为了旧文件能启动——改成 Load 先剥掉再严格解码，旧文件照样起，Save 永远不再写出 | 字段留着不写进向导（磁盘上仍误导）；问完再写进去 |
| D7 | **探测结果只标注、不阻断** | 一台刚装好、执行者还没登录的机器必须能配完。拦等于逼人先登录再回来 | 没装的执行者从列表里拿掉 |
| D8 | **托管追问保留，失败不让 init 失败** | B71 已验：只提示没人装。Linux 非 root 仍只打印 `sudo` 命令 | 做成必选项；失败回滚配置 |

---

## 4. 设计

### 4.1 角色更名

用户可见的中文：

| 旧 | 新 | 用法 |
|---|---|---|
| 审核者 | 协调者 | 派发、批权限、答提问、审 diff、下 `continue`/`done` 的那一端 |
| 审核者机 | 协调者机 | 角色选项 |
| 人工审核者 | 人工协调者 | 对比廉价审批模型时的「人」 |

**审批者**（廉价模型）这个词不动。三级分流仍是：静态规则 → 审批者 → 人工协调者。

必须改的活文件：

- [README.md](../../../README.md)
- [skills/handoff/SKILL.md](../../../skills/handoff/SKILL.md)（内嵌进二进制，版本随发版走）
- [cmd/init.go](../../../cmd/init.go) 及 `cmd/init_test.go` 的断言字符串
- 当前源码里面向用户的日志与注释：`人工审核者`、`审核者机`、`升级人工审核者`（[internal/agentd/approver.go](../../../internal/agentd/approver.go)、[internal/agentd/manager.go](../../../internal/agentd/manager.go)、[internal/config/config.go](../../../internal/config/config.go) 的 `WarnDeprecated` 等）
- [cmd/permission_mcp.go](../../../cmd/permission_mcp.go) 的工具描述：`Ask the handoff reviewer` → `Ask the handoff coordinator`

代码标识：`roleReviewer` → `roleCoordinator`。测试函数名跟着改（`TestInitDoesNotAskServiceForReviewer` → `…Coordinator`）。

不改：

- `waiting_review`、一切事件 / 工单 type
- Codex `approvalsReviewer` / `approvals_reviewer`（上游协议）
- `docs/superpowers/specs|plans|reviews|notes|backlog.md` 里已经定稿的段落。新写的 B83 本文用新词；旧条目保持原样

### 4.2 向导骨架

```
探测表（四家 + PATH 补全说明 + claude 登录态未知说明）
通用 env 提示（一段，见 §4.5）
── huh 表单 ──
  角色
  [执行机才问] 默认执行者 / 执行者模型 / 监听 / 项目落点 / 审批链执行者 [/ 审批链模型]
  [协调者才问] 任务结束自动同步 / 远程执行机配对（循环）
── 写盘 ──
已写入 <path>
配对 yaml（addr 用探到的可达 IP）
「init 可随时重跑，默认取当前配置，一路回车即保持不变。」
── 托管追问（执行机，现有 maybeInstallService，逻辑不动）──
```

非 TTY：与现在完全一样——探测 + 写出厂默认 + 「未交互配置，请在终端里运行 `handoff init`」。不进 huh，不追问托管。

幂等：配置已存在时，每一问的默认 / 预选项取**当前配置的实际值**。huh 的光标停在当前值上。

### 4.3 各问形态

**角色**（选择，总是问）

- 执行机 — 跑 agentd 和执行者
- 协调者 — 派发、审批、审阅
- 两者

默认：已有配置按现角色回填；否则探到就绪执行者 → 执行机，否则 → 协调者。

**默认执行者**（选择，角色含执行机）

四家全列，旁注探测态：`就绪` / `已安装，未登录` / `已安装，登录态未知` / `没装`。顺序仍是 toolchain 的固定序（opencode / claude / grok / codex）。没装也能选，选完走现有 `warnIfNotReady`（只警告不拦）。

默认：当前 `executor.default`；空则第一个就绪项；都没有则 `opencode`。

**执行者模型**（输入，可空）。标签用「默认」不用「缺省」。

**监听**（选择，角色含执行机）——见 §4.4。

**项目落点**（输入）。默认当前 `repo_root`。

**审批链执行者**（选择，角色含执行机）

第一项「不启用（权限直接找人）」，其余四家与默认执行者同一套旁注。选「不启用」则 `approver.executor=""`，不问模型。否则再问审批链模型（输入，可空）。

**任务结束自动同步**（确认，角色含协调者）。默认当前 `sync.auto`（出厂 true）。

**远程执行机配对**（循环，角色含协调者）

已有 targets 先列出来。每一轮：名字（空=结束）→ addr → token → ssh user。huh 的 `Input`，不是选择。

**不再出现的问**

- `update.auto` / `update.interval`。结构体删除这两个字段；见 §4.8。

### 4.4 监听：绑定与广告

选项（写入 `listen` 的只有这一列）：

| 选项 | 写入 `listen` | 何时作默认 |
|---|---|---|
| 仅本机 | `127.0.0.1:7777` | 当前 listen 是 loopback（`127.0.0.1` / `::1`），且不是下面「首次执行机」那一档 |
| 所有网卡 | `0.0.0.0:7777` | 当前 listen 是 `0.0.0.0` / `::`；**或** 配置文件在这次 `init` 之前不存在、且角色含执行机 |
| 手填 | 用户输入的 `host:port` | 当前 listen 不属于上面两档（含已经写残的 `0.0.0:7777`） |

「配置文件事先在不在」必须在 `config.Load` **之前** `stat`：Load 在文件不存在时会生成默认并写盘，事后看永远存在。出厂 `127.0.0.1:7777` 和用户选过「仅本机」写入的是同一个字符串，**只能靠文件是否预先存在区分**，不能靠读 listen。

手填必须能 round-trip 现有怪值：打开选择时停在「手填」，输入框预填当前字符串，避免重跑 init 把人配好的非常规地址冲掉。

**广告地址**（配对片段的 `addr`）另算，不写进 `listen`：

1. 枚举本机单播地址：排除 loopback、link-local（`169.254.0.0/16`、`fe80::/10`）、未启用网卡。
2. IPv4 优先。多块网卡全列，Tailscale（`100.64.0.0/10`）排前面——本仓库的远程场景就是这个。
3. `listen` 是 `0.0.0.0:7777` / `[::]:7777` 时，配对 `addr` 用排序后的第一项 + 端口（默认 7777，手填则用输入的端口）。
4. `listen` 是 `127.0.0.1:7777` 时，配对 `addr` 仍是 `127.0.0.1:7777`（这台本来就没打算给外机连）；可在片段下方补一行「本机还听到：…」列出探到的地址，不当默认。
5. `listen` 是具体 IP 时，配对 `addr` 用这个 IP（手填的就是广告）。
6. 一台网卡都探不到时，退回现在的 `<本机IP>:<port>` 占位符，并说明要换成协调者机能连到的地址。

`pairAddr` 不再把 `0.0.0.0` 换成死占位符。探 IP 失败才用占位符。

### 4.5 探测区与 env 提示

探测表保留。PATH 补全说明、claude「登录态未知」两段保留。

删除 [cmd/init.go](../../../cmd/init.go) `printDetection` 里 codex 那三行（「若需代理才能连 OpenAI…failed to refresh available models」）。

换成一段与执行者无关的提示，大致：

> 执行者若需要代理、私有 registry 或额外 PATH，把变量写进 `~/.handoff/env/<名字>.env`，再在 `config.yaml` 的 `env` 段挂上（如 `codex: codex.env`）。`init` 不创建、不修改这些文件。

这段只打印一次，放在探测表和表单之间。不按「探到了哪家」分支。

### 4.6 `prompter` 与包边界

新增小接口，生产实现调 huh，测试实现读预先排好的答案（行为对齐今天的 `ask` / `askString` / `askInt` / `askBool`）：

```go
// prompter 是 init 问答的唯一入口。生产是 huh；测试喂脚本化答案。
type prompter interface {
    Select(title string, options []promptOption, def string) (string, error)
    Input(title, def string) (string, error)
    Confirm(title string, def bool) (bool, error)
}
```

`promptOption` 带 `Value`（写入配置的值）和 `Label`（给人看的一行，含探测态）。`Select` 返回的是 `Value`。

huh 调失败（Ctrl-C、终端不够用）按用户取消处理：`init` 返回错误、**不写盘**。半截答案丢弃，避免写出一份只配了一半的配置。现有「stdin 提前结束当取默认」只留给脚本化 prompter，huh 路径不套这套。

探测、写盘、配对打印、`maybeInstallService` 仍在 `cmd`。huh 的具体调用收在 `cmd` 内一个文件（如 `cmd/init_huh.go`），不要为了三四个控件再开一个 `internal/ui`。

IP 枚举做成可测的纯函数（`listAdvertiseAddrs() []net.IP`，测试替换 `interfaceAddrs` 缝），不要把 `net.Interfaces` 嵌进 huh 回调里。

### 4.7 依赖

`go.mod` 增加 `github.com/charmbracelet/huh`。允许它带上 bubbletea / lipgloss / 相关 indirect。不引入第二套 TUI 库。

Windows：agentd 本就不支持（B37）。huh 在 Windows 终端上的表现不在本期验收范围；`GOOS=windows go build ./...` 仍须通过。

### 4.8 删除 `update.*` 配置项

`Config` 上删除 `Update` 字段和 `UpdateConfig` 类型。`validate` 里对 `update.interval` 的校验一并删。`WarnDeprecated` 整段删除——没有字段可警告。

`handoff upgrade` **不动**。那是操作者触发的换版命令，和这份死配置不是一回事。

**旧文件必须还能启动。** `KnownFields(true)` 若直接解码会把 `update:` 当未知键拒掉，所有 v0.1.x 写过的机器升级即砖。做法：

1. `Load` 在 `decodeStrict` 之前走一遍 yaml 节点，剥掉顶层 `update` 键。
2. 剥到了就打一条 Warn：`配置 update 段已废弃，已忽略并将从文件删除`。
3. 剥掉后再 `decodeStrict`。其它未知键仍硬拒。
4. 若这次 Load 剥过 `update` 且不是 firstRun，**就地 `Save` 一次**，磁盘上立刻干净。Save 只是 yaml 重排 + 丢掉死键，token / listen / targets 不变。回写失败打 Error，**不阻断启动**——内存里已经没有这个字段了。

`decodeStrict` 的「支持字段」清单去掉 `update{auto,interval}`。

README 配置示例和「`update.auto` 已废弃」那一段删掉，改成一句：升级用 `handoff upgrade`，没有定时自动更新，也没有对应配置项。

---

## 5. 测试

现有 `runInit` / `runInitWith` 改走脚本化 prompter，**不断掉**这些契约：

| 用例（已有或等价改名） | 仍须成立 |
|---|---|
| 非 TTY | 不问、写出厂默认、提示去终端跑 init |
| 显式回答被采纳 | 角色 / listen / 执行者写进配置 |
| 空回答取当前值 | 重跑不冲掉已有 listen |
| 协调者机不追问托管 | fake manager 调用次数为 0 |
| 执行机答 n | 不调用 manager |
| 执行机答 y 且 manager 失败 | `init` 仍返回 nil，配置已写盘 |
| 配对片段含 token | 执行机路径末尾打出 yaml |

新增：

| 用例 | 断言 |
|---|---|
| 执行者选项含没装的那家 | 列表里有，选中只警告 |
| 审批链选「不启用」 | `approver.executor` 为空，且没去问模型 |
| 监听三档 | 选「所有网卡」→ `0.0.0.0:7777`；选「仅本机」→ `127.0.0.1:7777` |
| 首次执行机预选 | 配置文件事先不存在 + 角色执行机 + 监听回车 → `0.0.0.0:7777` |
| 重跑尊重仅本机 | 文件已在且 listen=`127.0.0.1:7777` + 角色执行机 + 监听回车 → 仍是 `127.0.0.1:7777` |
| 手填 round-trip | 当前 `0.0.0:7777` 再跑一遍，值不变 |
| 广告地址 | `0.0.0.0:7777` + 注入一条 `100.73.1.2` → 配对片段 `addr` 是 `100.73.1.2:7777`，不是 `<本机IP>` |
| 无网卡 | 退回占位符 |
| huh 取消 | 返回错误，配置文件内容与跑之前一致 |
| 文案 | 用户可见输出不再出现「审核者」「缺省」；出现「协调者」「默认」 |
| 不再问更新 | 脚本化答案序列里没有 `update.auto` 这一档；重跑不会因为少答两行就错位 |
| 旧文件含 `update:` 仍能 Load | 不报未知字段 |
| Load 之后 Save / 就地回写 | 磁盘 yaml 不再含 `update` |
| 新文件 | 首次生成的 config.yaml 没有 `update` 键 |

更名：断言「审核者机不该装服务」的字符串改成「协调者」。MCP 工具描述单测若钉了 `reviewer` 一词，改钉 `coordinator`。

变异：把广告地址函数改成永远返回空，§5「广告地址」那条必须翻红——否则配对片段仍在用占位符，测试没咬住 D4。

---

## 6. 日志与注释

`init` 是给人看的向导，**不要把 slog 打进 stdout**。现在用只放行 WARN 的 logger 做 PATH 补全，这条边界保留。

要打日志的节点（stderr / slog，不进向导画面）：

- 进入交互 / 非交互分支（Info，带 tty=true/false）
- 写盘成功（Info，带路径与角色）
- huh 取消或失败（Warn / Error，带原因）
- 广告地址：探到几条、选用哪一条（Info）；一台都没有（Warn）
- 托管代跑成功或失败（沿用 `installService` 现有日志）
- Load 剥掉 `update` 段（Warn，带路径）；就地回写成功（Info）/ 失败（Error，不阻断启动）

新文件头注释写职责和边界。`prompter`、`listAdvertiseAddrs` 及导出方法补参数 / 返回 / 注意。监听三档和「探到的 IP 不进 listen」两处用中文注释写清为什么——表面上看「列出 IP 让人选」更直观，不写就会有人改回去。

---

## 7. 影响与兼容

- **已有 `config.yaml` 无需手工迁移。** token / listen / targets 结构不变。`update` 段由 Load 自动剥掉并回写。
- **旧 agentd 读新 `init` 写出的配置。** 新文件没有 `update` 键，旧二进制把缺键当默认，仍能启动（B59 的旧二进制只是忽略缺键）。
- **新 agentd 读旧配置。** 靠 §4.8 的剥键，不会因 `KnownFields` 拒启动。
- **内嵌 skill 随下一次发版二进制走。** 开发机用 `go run . skill install` 或 `handoff skill install` 才能看到新词。本期不发 tag。
- **行为变化：** 执行机**首次**跑 init（配置文件事先不存在），监听预选从出厂 loopback 改为 `0.0.0.0:7777`。这是纠错——出厂 loopback 正是 08-13 执行机差点配错的根。重跑时文件已在，listen 是 `127.0.0.1:7777` 就预选「仅本机」，尊重上次的选择（本机「两者」跑探针就是这种形态）。
- **配对片段 `addr` 从占位符变成具体 IP。** 抄过去就能连。IP 变了重跑 init 或手改 targets。

---

## 8. 真机验收

在刚装完 v0.2.0 的本机（协调者 + 本地 agentd）和 mac-02（执行机）上各跑一遍，**不要**用现在那份已经能用的配置当唯一证据——先把配置移走或在隔离 `--config` 下跑。

| # | 动作 | 判据 |
|---|---|---|
| V1 | 本机 `handoff init`，角色选协调者 | 角色是箭头选择；全程无「审核者」「缺省」；无 `update.auto`；结尾有可重跑提示 |
| V2 | mac-02 选执行机，监听回车 | 预选「所有网卡」，`listen: 0.0.0.0:7777`；配对片段 `addr` 是 `100.73.238.21:7777`（或当时的 Tailscale IP），不是 `<本机IP>` |
| V3 | 默认执行者 / 审批链 | 四家都在列表里且带状态；审批链第一项是「不启用」 |
| V4 | 本机重跑，一路确认 | 已有 `targets.mac-02` 还在，token 没被换掉 |
| V5 | `curl -fsSL …/install` 那种非 TTY | 不问，提示去终端跑 |
| V6 | README + `handoff skill` 打开的 SKILL.md | 角色段写「协调者」，回路描述不再用「审核者」 |

V2 依赖 mac-02 仍走 Tailscale。若当时没有 `100.64/10` 地址，广告地址用排序后的第一张非 loopback IPv4，测试记录实际值。

---

## 9. 已知残留

- B30 的「codex 漏配代理会哑失败」仍然成立。本期只把专文收成通用提示，**不加**模型可达性预检。要做代码防线另立项。
- 历史 spec / plan / backlog 行里的「审核者」不改。以后读旧文档会看到两个词，以本篇和 README 为准。新条目（B83 起）用「协调者」。
- `waiting_review` 这个状态名会一直带着 review。它是协议，不是 UI 文案；CLI / skill 可以用「待审阅」解释它，但不改字段。
