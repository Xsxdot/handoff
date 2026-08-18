# grok 自定义 provider 透传设计（B131）

> 状态：设计已确认（08-18），待实现。
> 背景缺陷：backlog B131。阻塞 B128 真机验收第 7 条。

## 1. 问题与根因

在配了自定义 provider 的机器上，派 grok 一律起不来：

```
handoff dispatch --target win-b37 --executor grok plan.md
→ 500: 启动 executor 失败: grok 未登录或凭据已失效，请在本机执行 `grok login`
       后重试: ACP 错误 -32000: Authentication required
```

报错文案指向「没登录」，但根因不是登录。`internal/executor/grok/taskenv.go` 的
`WriteTaskEnv` **重新生成**一份任务级 `config.toml`，只写四段：

```toml
[ui]        permission_mode
[models]    default = "<--model 传入值>"   # 仅当非空
[permission] ask / allow
[cli]       auto_update = false
```

用户权威 `~/.grok/config.toml` 里的 `[model.<名字>]` provider 定义块**不在其中**。
任务 home 里的 grok 因此不认识那个模型名，回落内建 x.ai provider，于是要认证。

### 1.1 物证（08-18 win-b37 实测）

权威配置的段：

```
[models] [model.deepseek-v4-pro] [model.deepseek-v4-flash] [marketplace] [cli] [ui]
```

失败任务 `ae2837a0` 的 `grokhome/config.toml` 实际段：

```
[ui] [models] [permission] [cli] [marketplace]
```

（`[marketplace]` 是 grok 自己补的。）`[model.*]` 两段确实不在。

### 1.2 已排除的更省事解释

「只是没传 `--model`，所以没写 `[models] default`」——**已排除**：显式传
`--model deepseek-v4-pro` 重派，同样 500、同样报文。缺的是 provider 定义本身，
不是默认模型名。

## 2. 目标与非目标

**目标**：让配了自定义 provider 的机器能正常派 grok，且不传 `--model` 时也能用
用户平时的默认模型。

**非目标**：

- 不改 grok 的凭据所有权模型（`auth.json` 软链 + 收编写回，见
  `2026-08-09-handoff-grok-credential-ownership-design.md`），本条与它正交
- 不改任务级权限隔离：`[ui]` / `[permission]` / `[cli]` 永远以 handoff 为准，
  权威配置里的同名段一律不搬
- 不为 grok 引入密钥改写机制（见 §5）
- 不动 opencode / claude / codex 的任务环境生成

## 3. 架构与放置

新增 `internal/executor/grok/providercarry.go`，单一职责：**从权威 config.toml
的文本里抽出 `[model.*]` 段与 `[models] default`**。

```go
// carryResult 是从权威配置里抽出的可搬运部分。
type carryResult struct {
    ModelSections string   // [model.*] 段原文（原样字节，含注释与段间空行），可能为空
    SectionNames  []string // 段名，如 ["model.deepseek-v4-pro"]，仅供日志
    DefaultModel  string   // [models] 段里 default 的值，可能为空
}

// extractProviderConfig 从 config.toml 的内容里抽出可搬运部分。纯函数，不碰
// 文件系统。
func extractProviderConfig(content string) carryResult

// loadAuthorityProviderConfig 读取 ~/.grok/config.toml 并抽取。
// 文件不存在或读取失败一律返回零值（本函数不返回 error——调用方无从处置，
// 也不该因此拒发）；失败原因经 log 留痕。
func loadAuthorityProviderConfig(log *slog.Logger) carryResult
```

核心函数吃**内容字符串**而非路径，是为了让表驱动单测不需要碰文件系统。

`WriteTaskEnv` 是唯一调用点。`proc.go` / `adapter.go` 一行不动。

## 4. 生成的 config 形态

### 4.1 `[models] default` 的取值优先级

TOML 不允许同名表定义两次，所以**不能**「handoff 写一个 `[models]`、再原样搬一个」。
生成的文件里 `[models]` 只出现一次，其 `default` 按下列优先级取值：

1. `--model` 传入值（`WriteTaskEnv` 的 `model` 参数，非空）
2. 权威 config 的 `[models] default`
3. 都没有 → **不写 `default` 这一行**（保持今天的行为，grok 用内建默认）

### 4.2 `[model.*]` 段的位置

原样追加在文件**末尾**，前面加一行生成标记注释。把 handoff 自己写的四段保持
连续，便于逐字节比对与人工核对。

生成结果示例（权威配置含两个 provider、`--model` 未传时）：

```toml
# 由 handoff agentd 生成的任务级 grok 配置，勿手工编辑。

[ui]
permission_mode = "default"

[models]
default = "deepseek-v4-pro"

[permission]
ask = [
  "Write(*)",
  ...
]
allow = [
]

[cli]
auto_update = false

# 以下 provider 定义由 handoff 从 ~/.grok/config.toml 原样透传，勿手工编辑。

[model.deepseek-v4-pro]
model = "..."
base_url = "..."
api_key = "..."
api_backend = "chat_completions"
context_window = 131072

[model.deepseek-v4-flash]
...
```

## 5. 密钥处理：原样搬，不改写

handoff **不解释、不改写**段内任何字段。任务目录里会不会出现明文 api key，
取决于用户自己的权威 config 怎么写。

**为什么不主动把 `api_key` 改写成 `env_key`**（grok 支持 `env_key`，凭据解析
顺序为 `api_key` > `env_key` > 登录态 > `XAI_API_KEY`）：

1. **没有跨越信任边界**。`~/.grok/config.toml` 本来就在同一用户的 home 里明文
   存着这个 key；任务目录是 0700、文件 0600、同机同用户。复制一份不改变任何
   人能读到它。
2. **想避免明文的人已经有办法**：在自己的权威 config 里用 `env_key`，搬过去的
   自然也是 `env_key`。grok 进程本来就继承 agentd 的环境
   （`proc.go:258` 是 `append(os.Environ(), env...)`），env 通路是现成的。
3. **半吊子改写反而更危险**。密钥不只藏在 `api_key`：`extra_headers`、
   `query_params` 都可能带。只改 `api_key` 会造出「任务目录无密钥」的错觉。

## 6. 抽取机制与已知边界

### 6.1 段边界判据

逐行扫描。**行首（允许前导空白）以 `[` 开头即为新段起点**，段名取 `[` 之后到
第一个 `]` 之前的原文。

- 段名以 `model.` 开头 → 该段起，本行及其后续行原样收进 `ModelSections`，
  直到下一个段起点（不含）
- 段名为 `models` → 该段内匹配 `^\s*default\s*=` 的行，取其值为 `DefaultModel`
- 其余段 → 丢弃（含 `[ui]` / `[permission]` / `[cli]` / `[marketplace]`：
  handoff 自己写，权威版本一律不要）

数组表 `[[x]]` 的行 trim 后以 `[[` 开头，段名解析得到 `[x`，不以 `model.` 开头，
因此被当成普通段——它正确地终结上一个 model 段，不会被误收。

### 6.2 `default` 值的解析

取 `=` 之后的部分，去首尾空白：

- 以 `"` 开头 → 取到下一个 `"` 为止（这样 `default = "x"  # 注释` 能正确解析）
- 否则 → 截到第一个 `#` 为止再去空白

### 6.3 已知边界（不假装不存在）

**若某字段的值是跨行数组、且续行顶格以 `[` 开头，会被误判成段边界**，导致该
provider 段被截断。真实 provider 段里字段都是单行标量或内联表
（`extra_headers = { … }`），不触发这条。用测试固化这个形态，不做更复杂的解析。

**为什么不引入 TOML 库**：本仓零 TOML 依赖，且 `WriteTaskEnv` 本身就是手写
字符串拼接。引入「解析 + 再序列化」会让生成的 config 不再一眼可读——序列化器
会重排键、丢掉用户注释；而原样搬字节连注释都保得住。代价就是要自己认段边界，
上面这条边界是这个取舍的已知成本。

## 7. 错误处理与降级

共同原则：**这是增强、不是必需，任何失败都不许拖垮派发**。

| 情形 | 处置 | 日志 |
|---|---|---|
| 权威 config 不存在 | 静默无操作，生成结果与今天逐字节一致 | Debug |
| 读取失败（权限 / IO） | 不阻断 dispatch，按「无操作」继续 | Warn，带路径与 cause |
| 存在但无 `[model.*]` 段 | 无操作（但 `[models] default` 仍可搬） | Debug |
| 正常搬运 | 写进任务 config | Info：`home`、`sections=<段数>`、`names=<段名列表>`、`default_from=flag\|authority\|none` |

## 8. 日志纪律（承重）

**任何情况下不打印段内容、不打印字段值。** 只打段名与条数。与 `authsync.go`
文件头「只打账号键、expires_at、任务 id，任何情况下不打 token 值」同源。

段名（如 `model.deepseek-v4-pro`）不是密钥，可以打。

## 9. 测试策略

`providercarry_test.go` 表驱动单测，八条：

1. 两个 `[model.x]` 段 + `[models] default` → 两段全搬、`DefaultModel` 正确
2. `WriteTaskEnv` 传了 `model` → 生成的 `default` 是传入值，不是权威值
3. 权威无 `default` 且 `model` 为空 → 生成的 config 里没有 `default` 行
4. **权威 config 不存在（或既无 `[model.*]` 又无 `[models] default`）→
   `WriteTaskEnv` 的输出与改动前逐字节相同**（回归保护：用当前实现的输出做
   golden）。注意前置条件必须同时排除 `[models] default`——只排除 `[model.*]`
   时，若权威有 default 而调用方没传 model，输出会多一行 `default`，与
   §7 的降级表一致但与「逐字节相同」矛盾
5. 段内注释与缩进原样保留
6. `[model.x]` 后跟 `[marketplace]` → 切在边界，不吞下一段
7. 生成的 config 里 `[models]` 只出现一次（防 TOML 重复表定义）
8. **日志不含密钥**：用捕获式 `slog` handler 跑一遍 `WriteTaskEnv`，断言输出里
   不含权威配置中的哨兵密钥字符串

第 4 条与第 8 条是承重的：前者保证既有用户零影响，后者保证 §8 的纪律不是口号。

## 10. 真机验收

B128 spec §10 第 7 条原样重跑（win-b37）：

1. `handoff dispatch --target win-b37 --new-worktree --executor grok <任务书>`
   —— **不传 `--model`**，验证权威 default 兜底
2. 权限门拦截产工单 → `reply --approve` 放行 → `completed`
3. `Get-Item <taskDir>\grokhome\auth.json` 看到 `ReparsePoint`（auth 软链真的建成）
4. `done` 后 worktree 与进程清干净

## 11. 影响面

- 平台无关：三个 OS 同一套代码路径，不是 Windows 特有修复
- 对不用自定义 provider 的用户零影响（§9 第 4 条测试保证）
- 不改任何已有函数签名；`WriteTaskEnv` 的参数与返回不变
