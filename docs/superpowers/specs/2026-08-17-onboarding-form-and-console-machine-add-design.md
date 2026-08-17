# 首次配置改单页表单 + 控制台新增开发机

**状态**：已确认，随 W5b-3 一起实现
**日期**：2026-08-17
**关联**：[W5 主 spec](2026-08-16-w5-embed-and-desktop-shell-design.md) §4.4 / §4.5、backlog B111.2

---

## 1. 背景

W5b-2 交付的图形化首次引导，把 CLI 的逐题问答原样搬进了窗口：Go 侧 `initflow.AskAll` 阻塞在每一问上，桌面壳用事件往返把「当前该问什么」发给前端，一问一屏。

2026-08-17 真机走查（协调者本机，临时 HOME）暴露两类问题。

**一、慢。** 一次首次配置最多 9 问，每问一屏。终端一次只能显示一问是硬约束，窗口没有这个约束——把约束一并搬过来，就是把终端搬进了窗口。走查中最直观的证据是窗口里那句「以下每一问直接回车即保留预选项」：它是终端语言，在图形界面里没有意义。

**二、远程配对在首次配置里本来就填不完。** 现在是一个 4 字段循环（名字 / 地址 / 令牌 / ssh 用户），而令牌要从**对方机器** `handoff init` 的输出里取。用户在自己机器上首次配置时手里没有它，只能回车跳过——但仍要先读懂再决定跳过，这是整个流程里最钝的一段。

同一次走查还顺带暴露一个独立缺陷，见 §6。

## 2. 目标与非目标

**目标**

- 桌面首次配置变成**一页**：默认值已填，最快路径是「确认角色 → 点完成」。
- 主次分离：角色与监听地址常显，其余收进默认折叠的「高级设置」。
- 远程配对移出首次配置，改由控制台承担。
- CLI 的 `handoff init` **行为逐字不变**。
- 字段、默认值、显隐规则只有**一份**真相。

**非目标**

- 不做完整的控制台设置面（执行者/审批链/同步等字段在控制台里可编辑）——那是另一件事，本 spec 只做「新增/删除开发机」。
- 不改 `config.Load` 的首次运行语义。
- 不做 Windows 桌面壳（见主 spec §4.6，已定为选项 B「纯协调者」，但排在 B37 之后）。

**拆分预期**：本 spec 覆盖两个子系统——「桌面壳 + `internal/initflow`」与「`internal/agentd` + 控制台前端」。它们由一条承诺耦合（首次配置移除远程配对 ⇒ 控制台必须能加），因此写在同一份 spec 里说清；但实现应拆成**两份 plan**，且顺序固定：**控制台新增开发机先落地**，桌面首次配置再移除配对循环。反过来做会出现一个「配对既不在向导里、也不在控制台里」的窗口期。

## 3. 为什么是「字段描述表」而不是别的

桌面表单要知道：有哪些字段、各自什么控件、选项是什么、默认值是什么、什么时候该出现。这些规则今天全部藏在 `AskAll` 的控制流里（`if isExec` / `if isCoord` / 嵌套的 listen 预设）。

三条路评估过：

| | 做法 | 否决/采纳理由 |
|---|---|---|
| A（**采纳**） | `initflow` 导出字段描述表，`AskAll` 改成按表逐项问，桌面壳按表渲染整页 | 一份枚举、两种渲染，零漂移 |
| B | 桌面壳自己调 `RoleOptions`/`DefaultRole` 等纯函数，显隐规则在前端再写一遍 | **否**。分支规则两份，加字段时改一边忘一边不会报错，只会静静地不一致——正是主 spec §4.4 要避免的漂移 |
| C | Go 侧给桌面壳一个一次性 `FormModel`，`AskAll` 不动 | **否**。CLI 零风险，但字段枚举仍有两处（AskAll 的顺序 + FormModel 的列表），留下同一道缝 |

A 的代价是重写 `AskAll`（约 80 行、有现成测试）。§8 规定了「行为不变」的证法。

## 4. 第一部分：`internal/initflow` 字段描述表

### 4.1 类型

```go
// Kind 是字段的控件形态。
type Kind string

const (
	KindSelect  Kind = "select"
	KindInput   Kind = "input"
	KindConfirm Kind = "confirm"
)

// Field 描述首次配置里的一个字段。
//
// 它同时被两种渲染器消费：CLI 的 AskAll 逐项问，桌面壳一次铺成整页。
// 因此本结构只描述「问什么」，不描述「怎么问」——任何终端或窗口特有的
// 表达方式都不得出现在这里。
type Field struct {
	// Key 是稳定标识：答案 map 的键，也是 Apply 分派写回的依据。
	// 一旦发布不得更名（桌面前端按它取值）。
	Key string
	Kind Kind
	Title string
	// Notice 是必须当场解释的产品输出（如 Windows 上角色只有一个选项的原因）。
	// 空=无。CLI 在问该字段前打印，桌面壳渲染为该字段的说明文案。
	Notice string
	// Default 是预选值；KindConfirm 用 "true"/"false"。
	Default string
	// Options 仅 KindSelect 有意义。
	Options []Option
	// Roles 是适用角色（RoleCoordinator/RoleExecutor 的展开集合）；
	// 空 = 与角色无关，恒显示。
	Roles []string
	// Advanced=true 时桌面壳折叠进「高级设置」；CLI 忽略本字段。
	Advanced bool
	// ShowWhen 是对另一字段取值的依赖；nil=无依赖。
	ShowWhen *Cond
}

// Cond 表达字段间的显隐依赖。Equal 与 NonEmpty 互斥，同时置为零值即恒真。
type Cond struct {
	Key      string
	Equal    string
	NonEmpty bool
}
```

### 4.2 函数

```go
// Form 返回本次首次配置要填的字段表。
//
// 切片顺序即 CLI 的提问顺序，也是桌面壳的默认排版顺序。
// 参数与 AskAll 同源：cfg 提供现值作默认，rs 提供执行者探测结果，
// goos 决定 Windows 的角色限制，cfgExisted 影响监听预设的默认档。
func Form(cfg *config.Config, rs []toolchain.Result, goos string, cfgExisted bool) []Field

// Visible 判断在已有答案下该字段是否应当出现。
//
// CLI 边问边判（answers 只含已答部分），桌面壳用同一套规则做实时显隐。
// 判定顺序：先 Roles（取 answers["role"] 展开），再 ShowWhen。
func Visible(f Field, answers map[string]string) bool

// Apply 校验答案并写回 cfg。
//
// 校验包含：Select 的答案必须在 Options 内；Confirm 只接受 "true"/"false"；
// 不可见字段的答案被忽略而非报错（前端可能残留切角色前填过的值）。
// 返回第一个校验错误，cfg 在出错时不保证未被部分修改——调用方应在
// 副本上 Apply，成功后再落盘。
func Apply(cfg *config.Config, fields []Field, answers map[string]string) error
```

### 4.3 字段表内容

与今天 `AskAll` 的行为逐项对齐，不增不减：

| Key | Kind | Roles | Advanced | ShowWhen | 写回 |
|---|---|---|---|---|---|
| `role` | select | — | 否 | — | 不落配置（决定 isExec/isCoord，由调用方使用） |
| `listen_preset` | select | executor, both | 否 | — | 不落配置（选 custom 才有下一项） |
| `listen` | input | executor, both | 否 | `listen_preset == "custom"` | `cfg.Listen` |
| `executor_default` | select | executor, both | 是 | — | `cfg.Executor.Default` |
| `executor_model` | input | executor, both | 是 | — | `cfg.Executor.Model` |
| `repo_root` | input | executor, both | 是 | — | `cfg.RepoRoot` |
| `approver_executor` | select | executor, both | 是 | — | `cfg.Approver.Executor` |
| `approver_model` | input | executor, both | 是 | `approver_executor` NonEmpty | `cfg.Approver.Model` |
| `sync_auto` | confirm | coordinator, both | 是 | — | `cfg.Sync.Auto` |

注意两处保持现状、**本次不改**：

- 监听地址只对执行机/两者提问（今天 `askListen` 在 `if isExec` 分支内）。纯协调者也跑 agentd、监听地址对它同样有意义，但改这个属于行为变更，不在本 spec 范围。
- `listen_preset` 的默认档由 `ListenPreset(cfg.Listen, cfgExisted, isExec)` 决定，`isExec` 取自 `role` 的当前答案。

### 4.4 `AskAll` 改造后的形态

```go
func AskAll(w io.Writer, p Prompter, cfg *config.Config, rs []toolchain.Result, cfgExisted bool) (bool, string, error) {
	fmt.Fprintln(w, "\n以下每一问直接回车即保留预选项。") // CLI 专有前言，不进字段表
	fields := Form(cfg, rs, runtime.GOOS, cfgExisted)
	answers := map[string]string{}
	for _, f := range fields {
		if !Visible(f, answers) {
			continue
		}
		if f.Notice != "" {
			fmt.Fprintln(w, "\n"+f.Notice)
		}
		ans, err := ask(p, f) // 按 Kind 分派到 Select/Input/Confirm
		if err != nil {
			return false, answers["role"], err
		}
		answers[f.Key] = ans
	}
	if err := Apply(cfg, fields, answers); err != nil {
		return false, answers["role"], err
	}
	role := answers["role"]
	return role == RoleExecutor || role == RoleBoth, role, nil
}
```

Windows 特判从函数开头的 `if` 变成 `role` 字段的 `Notice`（文案逐字不变）。远程配对循环**删除**（见 §5）。

## 5. 第二部分：桌面单页表单

### 5.1 数据流

```
桌面壳启动，判为未配置
  → pathenv.Apply(...)            §6：先把 PATH 补全，否则探测全灭
  → toolchain.Detect()
  → cfg := config.Defaults()      不落盘（W5b-2 已确立）
  → initflow.Form(cfg, rs, goos, false)
  → 经 binding 一次性交给前端：字段表 + 默认值
  ── 前端渲染整页，本地按 Roles/ShowWhen 实时显隐 ──
  → 用户点「完成」，回传 answers map[string]string
  → initflow.Apply(cfg副本, fields, answers)
  → 校验通过 → config.Save(path, cfg) → EnsureRunning → 握手 → 控制台
  → 校验失败 → 把错误回给前端定位到具体字段，不落盘
```

**判据不变**（W5b-2 已钉住）：向导未成功完成时，磁盘上不得留下会让 `shell.Resolve` 判为「已配置」的文件。落盘只发生在 `Apply` 成功之后。

### 5.2 版面

- 顶部常显：**角色**、**监听地址**（选自定义时其下出现输入框）。
- 中部：默认折叠的「**高级设置**」，含 `executor_default` / `executor_model` / `repo_root` / `approver_executor` / `approver_model` / `sync_auto`。折叠状态下这些字段仍以默认值参与提交。
- 底部：单个「**完成**」按钮。
- **不出现**任何终端语言（「直接回车即保留预选项」属 CLI 前言）。
- `Notice` 渲染为对应字段下方的说明行（Windows 的角色说明即走这条）。

### 5.3 退役的部分

`desktop/internal/shell/wizard.go` 的 `eventPrompter`（`Select`/`Input`/`Confirm` 三个阻塞方法与 `wizard-ask`/`wizard-answer` 事件往返）不再被桌面壳使用，随本次改造**删除**。`initflow.Prompter` 接口保留——CLI 仍在用它。

`NewNoticeWriter` 一并删除。它的作用是把 `AskAll` 写进 `io.Writer` 的流式提示转成事件，而新设计里桌面壳**根本不调用 `AskAll`**（它直接消费 `Form`/`Apply`），那个 writer 没有生产者。`Notice` 已成为字段上的数据，由前端静态渲染。

**`MaybeInstallService` 不变**：它今天由 `cmd/init.go` 在 `AskAll` 之后单独调用，不在字段表内；桌面壳一直不走它（服务托管由 `shell.EnsureRunning` 承担）。本 spec 不触碰这条分工。

## 6. 顺带修复：双击启动时执行者探测全灭

走查实测（协调者本机，macOS）：`launchctl getenv PATH` 为空，即 **Finder/Dock 双击启动的 GUI 应用拿到的是 launchd 默认 `PATH=/usr/bin:/bin:/usr/sbin:/sbin`**。而四家 executor 全部装在该 PATH 之外：

| | 实测位置 | 默认 PATH 下 |
|---|---|---|
| opencode | `~/.opencode/bin/opencode` | 找不到 |
| claude | `~/.local/bin/claude` | 找不到 |
| grok | `~/.grok/bin/grok` | 找不到 |
| codex | `/opt/homebrew/bin/codex` | 找不到 |

`toolchain.Detect` 用 `exec.LookPath` 判装没装，因此**双击打开桌面端走首次配置时四家会全部显示「未安装」**——而「双击就能用」正是 W5b 的立项理由。走查当天只暴露了 opencode 一个，是因为薄壳从终端启动、继承了终端 PATH。

修法仓库里现成：`internal/pathenv` 的存在理由就是这件事（包注释：「把『本进程能看到的 PATH』补成『这台机器上用户实际可用的 PATH』」），其已知目录表里 `.opencode/bin` 一行的注释直接写着「B71 故障现场」——同一个坑 agentd 已经踩过并修好。桌面壳目前**完全没有引用它**。

**要求**：桌面壳在调用 `toolchain.Detect()` 之前先 `pathenv.Apply`。实测按该目录表补 PATH 后四家全部可解析。

## 7. 第三部分：控制台新增开发机

远程配对移出首次配置后，控制台必须能加，否则用户只能改配置文件。

### 7.1 现状与约束

- `targets` 由 agentd 在**请求时**读（`s.cfg.Targets[name]`，见 `internal/agentd/forward.go`、`forward_ws.go`、`machines.go`），共 30 处 `s.cfg.` 读取分布在 14 个文件。
- agentd **从不写自己的配置**（全包无 `config.Save`），`Server` 也**不持有配置文件路径**。
- `proto.Machine` **不含 Token 字段**——控制台从来看不到令牌。这个性质必须保持。
- `NewServer` 只有一个调用方（`cmd/agentd.go`）。

### 7.2 并发：改为原子快照

`s.cfg` 被并发的 HTTP handler 读取，新增 target 要改 `Targets`，直接改 map 是数据竞争。

**采用写时复制 + 原子指针**：`Server.cfg` 字段改为 `atomic.Pointer[config.Config]`，读取一律经访问器 `s.conf()`；写入时构造一份新的 `config.Config`（含新的 Targets map）整体换上。30 处读取全部改为 `s.conf().X`。

选它而不是「只给 Targets 加 RWMutex」的理由：后者把 `s.cfg` 留在原地，任何人日后新写一处 `s.cfg.Targets` 就是一个静默竞态；前者删掉了那个字段，同样的错误变成**编译错误**。30 处是机械改动且由编译器保证覆盖完全。

### 7.3 接口

```
POST   /api/machines          新增
DELETE /api/machines/{name}   删除
```

`POST` 请求体：

```json
{"name":"devbox","addr":"100.73.238.21:7777","token":"...","user":"sycm","force":false}
```

**校验**（顺序即返回第一个错误的顺序）：

1. `name` 非空、不含空白、不与既有 target 重名（重名返回 **409**）、不为保留名（空串代表本机）
2. `addr` 形如 `host:port`
3. `token` 非空

**可达性探测**：校验通过后用 `client.New(addr, token).Status(ctx)` 探一次（复用 `machines.go:107` 同一条调用）。

- 探通 → 落库
- 探不通 → **400**，响应体带探测失败原文
- 前端展示原文并提供「仍然保存」，勾选后以 `force:true` 重发，跳过探测直接落库

这样设计的理由：绝大多数失败是地址或令牌粘错，当场发现比事后排查便宜得多；但机器临时离线是合理场景，不该因此完全加不进来。

**落库**：写时复制加入 targets → 原子换上 → `config.Save(cfgPath, newCfg)`。落盘失败则回滚内存快照并返回 500——内存与磁盘必须一致，否则重启后配置凭空消失。

**响应**：返回更新后的机器列表（与 `GET /api/machines` 同结构）。**任何响应都不得包含 token。**

`DELETE` 同样走写时复制 + 落盘；删除不存在的名字返回 404。

之所以一并做删除：只能加不能删的话，地址粘错又选了「仍然保存」的用户就只能去改配置文件——那正是本 spec 想消灭的场景。

### 7.4 前端

机器页（`web/src/app/machines/MachinesPage.tsx`，目前只读）新增：

- 「新增开发机」按钮 → 表单（名字 / 地址 / 令牌 / ssh 用户）
- 提交失败时展示后端原文；探测失败额外提供「仍然保存」
- 机器卡片上提供删除入口，删除前二次确认（`ConfirmDialog` 已有）
- **令牌输入框用密码型，且提交后不回显**——后端本就不返回

### 7.5 安全边界

写配置是特权变更。控制台已由 ticket 鉴权（B57 那套），与 CLI 同信任级，因此不额外加门。但两条硬约束：

- token 只进不出：请求体接受，任何响应体、任何日志都不得出现。日志只打 target 名与地址。
- 探测请求只发往请求体里给定的 addr，不做任何跳转跟随。

## 8. 测试与验收

**`initflow`**

- 字段表金样：用 `ScriptedPrompter` 跑完一整轮，把**提问文本与顺序**逐字录下来，改造前后比对必须完全一致（每个角色各一份：coordinator / executor / both；外加 `goos=windows` 一份）。
- `Visible` 的矩阵用例：角色 × ShowWhen 的组合，含「切角色后残留答案不影响判定」。
- `Apply` 的校验用例：Select 答案越界被拒、Confirm 非法值被拒、不可见字段的残留答案被忽略。

**CLI 不回归**

- 改造前后 `go test ./cmd/... ./internal/initflow/... -count=1 -v` 的**用例名集合完全一致**，after 侧无 FAIL。（证法与 W5b-2 同款。）

**桌面壳**

- `desktop/internal/shell` 不得 import Wails，表单相关逻辑用普通 `go test` 覆盖。
- 判据复验：向导未完成时磁盘不留任何会让 `Resolve` 判「已配置」的文件（`kill -9` 实测）。

**agentd**

- `-race` 下并发「读 machines 列表 + 新增 target」不得报竞态。
- 重名 409、地址非法 400、探测失败 400 且带原文、`force:true` 跳过探测落库。
- 落盘后重启 agentd，新 target 仍在。
- 响应体与日志断言**不含 token**。

**真机（必须真机，因为它本身就是环境问题）**

- 双击启动（**不经终端**）打开桌面端，首次配置页上四家 executor 的探测结果与登录态正确——这条是 §6 的验收，从终端启动验不出来。
- 单页表单：默认值已填、切角色时区块显隐正确、折叠「高级设置」不影响提交、点「完成」后进入控制台。
- 控制台新增开发机：填对能加上、地址粘错能看到探测失败原文、「仍然保存」可强加、删除可用。

## 9. 与既有 spec 的关系

- 主 spec **§4.4**：选项 B（下沉到 `internal/initflow`）已由用户确认；本 spec 在其之上把「逐题问答」换成「字段表 + 两种渲染」，不推翻 B——可复用的「内容」（选项、默认值）与新增的「显隐规则」都仍只有一份。
- 主 spec **§4.5**（目录选择器 / 收口 B110）：核实发现**至今未接线**——`desktop/internal/shell/picker.go` 只有 `NormalizeProjectDir` 一个路径规整函数，没有 Wails 原生对话框、没有 binding、前端无入口，全仓无调用方。它不在本 spec 范围内，但需要在 W5b-3 的计划里被安置，否则会随 W5b 一起被当作已完成。
- 主 spec **§4.6**：Windows 已由用户定为**选项 B（纯协调者形态）**，且 B37（Windows prochost，让 Windows 能当执行机）提升为下一步重点规划。本 spec 的字段表已通过 `Roles` 与 `goos` 支持「Windows 只有协调者」这一档，不构成 Windows 薄壳的阻塞。

## 10. 未决与风险

- **`AskAll` 重写的回归风险**是本 spec 最大的一处。它是刚验收过的 CLI 路径，§8 的金样比对是唯一防线；金样必须在**改造前**先录下来，否则录的是改造后的行为，等于没测。
- **本 spec 不做**控制台的完整设置面。首次配置页里的「高级设置」字段配完之后，用户目前仍无法在控制台里改（只能改配置文件或重跑 `handoff init`）。这是已知缺口，留待后续。
- 原型：本次**按用户决定跳过**可点击原型，版面以 §5.2 的文字描述为准。仓库尚无 `prototypes/base/`，建站的开销与两个标准表单的收益不成比例。
