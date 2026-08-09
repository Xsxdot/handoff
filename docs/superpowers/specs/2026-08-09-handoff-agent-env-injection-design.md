# Agent 启动前注入环境变量 —— 设计

> 日期：2026-08-09
> 来源：用户需求「启动前载入环境变量。在安装目录中，用户可以自己编辑一些环境变量的文件，
> 然后在配置文件中指定哪个 agent 启动的时候，要在终端中注入哪个文件中的环境变量」
> Backlog：B19

## 1. 问题与目标

agent（executor）由 agentd 在执行机上拉起，它继承的是 agentd 进程的环境。三期 B7 已经让
agentd 启动时合并登录 shell 的 PATH，但那是**自动猜**用户的环境；它猜不到、也不该猜的东西还有一类：
代理（`HTTPS_PROXY`）、模块代理（`GOPROXY`）、私有 registry 地址、`LANG`、额外的 PATH 段。
这些直接决定 agent 能不能连出去、能不能跑通构建，缺了就是一整轮任务白跑。

目标：让用户在执行机上**显式声明**「某个 agent 启动时该带什么环境」，并保证这份声明对
所有 agent 形态（当前的 opencode，backlog 上排队的 B2 Claude Code、B3 grok）都有效。

**非目标**：不做密钥管理。本设计面向环境类变量；虽然实现上不区分（值一律不进日志），
但不提供加密、不提供 secret 后端接入。

## 2. 文件位置与形态

env 文件放**执行机**的 `<DataDir>/env/` 下（DataDir 默认 `~/.handoff/`）。用户手工创建与编辑，
handoff 不生成模板、不写入、不接管内容。

之所以必须是执行机而不是审核者本机：进程是 agentd 拉起的，本机 CLI 那份配置管不到远程进程的环境。

示例 `~/.handoff/env/dev.env`：

```sh
# 整行注释；空行忽略
export HTTPS_PROXY=http://127.0.0.1:7890
GOPROXY=https://goproxy.cn,direct
PATH=${PATH}:/usr/local/go/bin
LANG="en_US.UTF-8"
LITERAL='原样 $不展开'
```

## 3. 解析语义

逐行解析，规则完整列举如下（实现与测试都以本节为准）：

| 规则 | 行为 |
|---|---|
| 行尾 `\r` | 先剥离，兼容 CRLF 文件 |
| 首尾空白 | trim 后再判断 |
| 空行 | 跳过 |
| trim 后以 `#` 开头 | 跳过（整行注释） |
| `export ` 前缀 | 可选，后跟至少一个空白即识别并剥离（方便从 `.zshrc` 直接抄行） |
| 分割 | 第一个 `=`；整行无 `=` → 报错，错误带行号与该行原文 |
| KEY | trim 后必须匹配 `^[A-Za-z_][A-Za-z0-9_]*$`，否则报错带行号 |
| VALUE | trim 首尾空白后按引号形态处理（见下） |
| `'...'` 完整包裹 | 去引号，**不展开**，内容字面量 |
| `"..."` 完整包裹 | 去引号，**展开** |
| 无引号 | **展开** |
| 重复 KEY | 后者覆盖前者，覆盖时打 WARN 带 key 名 |
| 文件大小 | 上限 64KiB（`maxEnvFileSize = 64 << 10`），超限报错 —— 防止误配一个二进制文件后解析出一堆垃圾变量 |

展开用 `os.Expand`，变量查找顺序：

1. 本文件中**前面已解析**的键
2. agentd 进程环境（`os.Getenv`，已含 B7 合并后的 PATH）
3. 都没有 → 空串

文件内优先是为了让文件自洽：读文件的人不必脑补外部环境是什么。`PATH=${PATH}:/usr/local/go/bin`
走的是第 2 条，拿到 agentd 的 PATH。

### 3.1 刻意不支持的（连同理由）

- **行内注释**。`#` 只在行首生效。因为 `HTTPS_PROXY=http://host/a#b` 里 `#` 是合法字符，
  支持行内注释会把这类值静默吃掉半截 —— 静默截断一个代理地址，症状是「代理配了但连不上」，
  排查成本极高。
- **多行值、命令替换、条件逻辑**。要这些请写 shell，这里不是 shell。
- **值尾部空白**。一律被 trim 掉，不要靠它传值。
- **字面量 `$`**。不用单引号时 `$` 一律按变量语法处理，包括 `$$`、`$1` 这类 shell 特殊名
  —— 它们会被 `os.Expand` 展开为空串。需要字面 `$` 请用单引号包裹整个值。

## 4. 配置

`~/.handoff/config.yaml` 新增顶层 `env` 段：

```yaml
env:
  opencode: dev.env
  claude: dev.env
```

- 类型 `Env map[string]string`，key 是 agent（executor）名，value 是 `<DataDir>/env/` 下的**纯文件名**。
- value 含路径分隔符 `/` → 报错；value 为 `.` 或 `..` → 报错。一杜绝路径穿越，二保证
  「env 文件只有一个家」不会散落各处。
- 未配置的 agent 不注入；配了空值等同未配置。
- `config.decodeStrict` 的「支持的键」提示文本需同步补 `env{<agent>: <文件名>}` —— 该文本是
  严格解析报错时用户唯一的键名清单，漏补等于新键对排错不可见。

审批者（`approver.executor`）是同一个 agent 的另一次启动，注入**同一份** env。否则代理只配了半边，
审批者连不出去会静默 fail-closed 升级，是最难查的那种故障。

## 5. 注入点

agent 有两种启动形态，各走一条注入路径。

### 5.1 任务执行

`executor.StartReq` 增加字段：

```go
// Env 是启动 executor 进程时额外注入的环境变量（形如 KEY=VALUE，已解析、已展开）。
// 由 manager 按 task.Executor 从 env 文件解析后填入；空表示不注入。
// 实现方必须把它注入到自己拉起的进程环境中。
Env []string
```

manager 在 `Dispatch` 的**最前段**解析（早于建任务、早于 `PrepareWorkspace`），解析失败直接返回
——§6 要求「任务不创建」，解析若发生在 `ad.Start` 前的最后一刻，任务已落库、worktree 已建，
就变成「创建了一个注定 failed 的任务」，与该要求矛盾。解析结果一路带到 `ad.Start` 的
`StartReq.Env`。之所以加在**契约**上而不是加在 `opencode.New()` 的构造参数上：
B2/B3 两个 adapter 的进程模型与 opencode 完全不同（stream-json 双向流 / ACP over WebSocket），
构造参数那条路要各写各的；契约字段则是它们白捡 —— 拿到 `req.Env` 塞进各自的 `cmd.Env` 即可。

opencode adapter 的落法：`writeServeScript` 在生成的 `run_serve.sh` 中追加 export 行，
**排在 `OPENCODE_SERVER_PASSWORD` / `OPENCODE_CONFIG` 之前**：

```sh
#!/bin/sh
exec 2>> '<taskDir>/serve.log'
export HTTPS_PROXY='http://127.0.0.1:7890'
export PATH='/usr/bin:/bin:/usr/local/go/bin'
export OPENCODE_SERVER_PASSWORD='<随机>'
export OPENCODE_CONFIG='<任务配置路径>'
exec opencode serve --port <p> --hostname 127.0.0.1 2>&1 | tee -a '<taskDir>/serve.log'
```

两条硬要求：

- **值必须用 `shellq.Quote` 单引号包裹**。Go 侧已经展开过一次，单引号保证 shell 不再展开第二次
  —— 否则 `PATH=${PATH}:/x` 展开后的值里若含 `$`，会被 shell 二次展开成别的东西。
- **顺序保证 handoff 自身变量胜出**。env 文件若定义了 `OPENCODE_SERVER_PASSWORD` 或
  `OPENCODE_CONFIG`，注入仍在前、被后面的 export 覆盖，同时打 WARN 说明「该键被 handoff 自身注入覆盖」。
  不静默忽略用户写的行。

### 5.2 审批者

`internal/agentd/approver.go` 的 `defaultRunCmd` 当前 `cmd.Env` 为 nil（继承 agentd 环境）。
改为 `cmd.Env = append(os.Environ(), injected...)`。`runCmd` 测试注入点的签名随之变为
`func(ctx context.Context, argv []string, env []string) (string, error)`。

`NewApprover` 增加一个 resolver 入参；nil 表示不注入（测试与未启用场景）。

### 5.3 加载时机

**每次启动 agent 时读盘**，不在 agentd 启动时缓存。

理由：改了代理下一个任务就生效，不必重启 agentd —— 重启会打断正在跑的任务的事件订阅，
代价不小。读一个几百字节的文件的开销相对于拉起一个 agent 可以忽略。

## 6. 失败语义

一律 fail-closed，档位分三处：

| 时机 | 失败情形 | 处置 |
|---|---|---|
| 派发（manager 解析 env） | 文件不存在 / 语法错 / 超 64KiB / 文件名含 `/` | **拒发**，任务不创建，HTTP 500 携带可行动真因（文件路径 + 行号 + 原因） |
| 审批者裁决 | 同上 | 走现有 escalate 路径升级人工审核者 + ERROR 日志 |
| agentd 启动预检 | 同上 | 逐个文件 WARN，**不阻断启动** |

- **派发为什么必须拒发而不是裸奔启动**：配了代理却没注入的 agent 会跑一整轮任务，然后卡在
  拉不到依赖上 —— 症状离根因隔了十万八千里。直接拒发把根因摆在派发者眼前。
- **500 而非 400**：这是 agentd 侧的环境/配置问题，与 B16 立的先例一致（状态码保留 500，
  关键是回显可行动真因）。
- **审批者不能崩**：env 文件坏了不该让审批链整体失效，但也不能让审批者在没有代理的情况下
  连不出去还假装裁决过了 —— escalate 是唯一正确的中间态。
- **启动预检为什么只 WARN**：env 文件是数据文件不是配置键，可能在 agentd 启动后才创建，
  为它拒绝启动太硬；但完全不检查会把问题拖到第一次派发才暴露，WARN 让它在启动日志里就可见。

## 7. 可观测性

按 instrumenting-code 的关键点清单：

| 位置 | 级别 | 内容 |
|---|---|---|
| 解析成功 | Info | `agent` / `path` / `keys=[key 名列表]` / `count` |
| 该 agent 未配置 env | Debug | `agent`，说明「跳过是因为没配，不是出错」 |
| 解析失败 | Error | `agent` / `path` / `line` / `cause` |
| 重复 key 覆盖 | Warn | `path` / `key` |
| 撞上 handoff 保留变量 | Warn | `key`，说明被 handoff 自身注入覆盖 |
| 启动预检失败 | Warn | `agent` / `path` / `cause` |

**只打 key 名，绝不打值。** 环境类变量看着不敏感，但 `HTTPS_PROXY=http://user:pass@host`
是完全正常的写法，值里带凭据的概率不低。

## 8. 模块划分

新增 `internal/envfile` 包，两个文件：

- **`envfile.go`** —— 纯解析器：
  ```go
  // KV 是一条解析结果。
  type KV struct{ Key, Value string }

  // Parse 解析 env 文件内容。lookup 提供展开时的外部变量查找（通常包 os.LookupEnv）。
  func Parse(r io.Reader, lookup func(string) (string, bool)) ([]KV, error)
  ```
  无文件 IO、无配置依赖。这是本功能唯一有真实逻辑密度的部分，也是最该被表驱动单测钉死的部分。

- **`resolver.go`** —— 定位与读盘：
  ```go
  type Resolver struct { /* Dir string; Map map[string]string */ }

  func NewResolver(dir string, m map[string]string) *Resolver

  // For 返回该 agent 应注入的环境变量（KEY=VALUE 形式）；该 agent 未配置时返回 (nil, nil)。
  func (r *Resolver) For(agent string) ([]string, error)

  // Preflight 读一遍所有被引用的文件，把问题以 WARN 暴露在启动日志里；不返回错误。
  func (r *Resolver) Preflight(log *slog.Logger)
  ```

放独立包而不是塞进 `internal/config`：config 包的职责是「解析 config.yaml」，env 文件是另一类文件、
另一套语法，混进去这个包就有两个理由改动了。

接线：`cmd/agentd.go` 构造 `res := envfile.NewResolver(filepath.Join(cfg.DataDir, "env"), cfg.Env)`，
调用 `res.Preflight(logger)`，再把 `res` 分别传给 `NewApprover` 与 `NewManager`。

## 9. 测试

**解析器（表驱动）**：注释行 / 空行 / `export` 前缀 / 三种引号形态 / `${VAR}` 与 `$VAR` /
`PATH=${PATH}:/x` 自引用 / 文件内前置键优先于进程环境 / 未定义变量展开为空 / 重复键覆盖 /
缺 `=` 报错带行号 / 非法 key 名报错带行号 / CRLF 行尾 / 超 64KiB 报错。

**Resolver**：文件名含 `/` 被拒；文件缺失的错误文本带完整路径；未配置的 agent 返回 `(nil, nil)`；
**改文件后第二次 `For` 拿到新值**（热更新是 §5.3 的设计承诺，必须有测试钉住）。

**接线**：`writeServeScript` 生成的脚本中 env 行位于 `OPENCODE_*` 之前且值被单引号包裹；
approver 的 `runCmd` 收到的 env 含注入变量；dispatch 遇 env 解析失败时任务不被创建且响应体带真因。

## 10. 明确的范围外

- **`handoff run` 不注入**。那是审核者取证时在自己终端里跑命令，不是 agent 启动。
- **attach 进 tmux 后新开的 pane 不带这些变量**。只有 serve 进程及其派生的 bash 工具调用带
  —— 变量是 `run_serve.sh` 里 export 的，作用域是那条进程链。
- **不做密钥加密与 secret 后端**（见 §1 非目标）。
- **不提供 CLI 管理 env 文件**（无 `handoff env edit/list`）。用户直接编辑文件即可，
  加一层 CLI 只是把 `vim` 包一遍。
