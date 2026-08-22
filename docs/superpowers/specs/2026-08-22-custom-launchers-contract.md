# 契约增量：工作台自定义启动项（需求 B）

日期 2026-08-22 · 前置 [spec §B](2026-08-22-executor-timing-and-custom-launchers-design.md) · 节点 `charter:contract`

**冻结物**：本文档 + `codegraph/target.json` 的域路径与契约条目 + Ticket 0 骨架提交。

每个签名都对着现状代码查证过，出处以 `文件:行` 标注（本轮工作树实读）。

---

## 1. 现状事实（查证结果）

| 事实 | 出处 | 对本次的意义 |
|---|---|---|
| `CreatePtySessionReq{BasePath, BaseKind, Rel, Cols, Rows}` | [pty.go:47](internal/proto/pty.go:47) | 要加两个字段的地方 |
| `ptyhost.OpenOptions` **已有 `Env []string`**，注释写明「完整环境，不会再自动追加 os.Environ」 | [types.go:40](internal/ptyhost/types.go:40) | env 注入的底层能力**已存在**，缺的只是上层契约 |
| `startPty` 用 `exec.Command(shell, "-l")` 起 login shell，`cmd.Env = env` 完整替换 | [platform_unix.go:44](internal/ptyhost/engine/platform_unix.go:44) | 命令注入不能走 argv（会变成非交互 shell），只能写 PTY 输入 |
| `Engine.Open` 起完 shell 后 `go h.pump(s)` 读输出并广播 | [engine.go:78](internal/ptyhost/engine/engine.go:78) | **pump 是「shell 有动静了」的唯一现成观测点** |
| `sessionEnv()` 组装环境：`os.Environ()` + `TERM/COLORTERM` + `env_forward` 解析结果 | [pty_api.go:42](internal/agentd/pty_api.go:42) | 启动项的 env 叠加在它之上 |
| `s.pty.Open(ptyhost.OpenOptions{...})` 单一调用点 | [pty_api.go:125](internal/agentd/pty_api.go:125) | 接线爆炸半径 = 1 |
| `handleCreatePtySession` **第一行就是 `s.forwardIfRequested(w, r)`** | [pty_api.go:100](internal/agentd/pty_api.go:100) | 请求体**原样转发**给目标机 agentd —— 新字段自动穿透，**这既是好消息也是 §3.1 那条风险的根源** |
| `resolvePtyBase` 的失败一律 400，注释写明「这是参数校验，不是安全边界」 | [pty_api.go:65](internal/agentd/pty_api.go:65) | 启动项的校验失败沿用 400 |
| `envfile.Resolver.For(agent)` 按 **agent 名**经 mapping 换算文件名，再 `resolvePath` → `Open` → `Parse(f, os.LookupEnv)`；只打 key 不打值 | [resolver.go:77](internal/envfile/resolver.go:77) | 启动项要按**文件名**直接加载 → 需要一个平级函数（§2.5） |
| `envfile.resolvePath` 拒绝一切含路径分隔符的名字 | [files.go:151](internal/envfile/files.go:151) | 启动项的文件名校验**复用它，不另写** |
| `handleEnvMapping` 的形态：**整段替换** + 保存时一次性校验「file 档的文件必须存在」 + `s.swapConf` 落盘 | [env.go:287](internal/agentd/env.go:287) | 启动项 CRUD 的同构原型 |
| `StatusResp.PtySupported *bool` 三态：**nil = 对端太老，不许当 false** | [status.go:200](internal/proto/status.go:200) | 能力位的既有纪律 —— 本次要**刻意反着来**，见 §4.1 |
| `MachineDetail` 依次渲染 `MachineDiscipline` / `MachineEnv` / `MachineExecutor` | [MachineDetail.tsx:71](web/src/app/machines/MachineDetail.tsx:71) | 第四块的挂载点 |
| `PtySession.Foreground` / `Incompatible` **刻意不带 omitempty**，理由是「false 是有意义的结论，缺键会让前端分不清它和『这版服务端还不认识这个字段』」 | [pty.go:26](internal/proto/pty.go:26) | 启动项的 `EnvMissing` 沿用同一条 |

### 1.1 依赖库既成行为查证

- **`exec.Command(shell, "-l")` 起的是 login shell**（[platform_unix.go:44](internal/ptyhost/engine/platform_unix.go:44) 注释：「rc 链照读——用户要的是『和我在 iTerm 里一样』」）。这直接否掉了「命令即 shell」（`sh -lc cmd`）的实现路径：那会让会话在命令退出时结束，与 spec §B 拍定的「命令跑完人还能接着用」相悖。
- **`pty.StartWithSize` 内部设置 `Setsid+Setctty`**（同上注释），因此伪终端的行规程在子进程 exec 之前就已就位——写进主端的字节会进内核输入缓冲，不会丢。**但这不等于「随便什么时候写都对」**，见 §4.2。
- **`prochost.WaitInputReader(fifoPath, fifoReaderTimeout)`** 是本仓已有的「等真实就绪信号 + 有界超时」先例（`claudecode/proc.go:195`，成功后打 `claude in.fifo 读端就绪`）。本次的就绪判据照这个形状定，**不用固定 sleep**。

### 1.2 对侧常量查执法

- **`EnvModeFile` / `EnvModeOff`**（[env.go:44](internal/proto/env.go:44)）：由 `handleEnvMapping` 消费、由 Web 的 `bindingRow.ts` 产出，两端都在用，是活常量。启动项**不复用这套两档**——启动项的 env 是「可选的一份文件」，没有「档位」这个概念，照抄会造出一个永远只有一个取值的枚举。
- **疑似漂移一处（新发现）**：TS 侧的 `CreatePtySessionReq`（[types.ts:703](web/src/api/types.ts:703)）**只声明了 4 个字段，缺 `rel`**——而 `TerminalTab` 确实在发 `rel`（[TerminalTab.tsx:59](web/src/app/workbench/TerminalTab.tsx:59) 的 `ptyBase` 返回带 `rel` 的对象）。它没被 tsc 拦下，是因为调用点用的是展开写法（`{ ...ptyBase(base, rel), cols, rows }`），而对象展开**不触发超额属性检查**。
  这正是缺陷族里「序列化边界」的标准形状。**本次一并补上 `rel`**：不补的话，我们新加的两个字段会踩着「这个接口本来就不声明全部字段」的先例继续漂。

---

## 2. 契约增量

### 2.1 `proto.Launcher`（新增，`internal/proto/launcher.go`）

```go
type Launcher struct {
    // Name 是启动项的身份，机器内唯一，非空。**没有单独的 id 字段**（§4.3）。
    Name string `json:"name"`
    // EnvFile 是该机 <DataDir>/env 下的纯文件名；空 = 不注入。
    EnvFile string `json:"env_file,omitempty"`
    // Command 是启动后送进终端的命令原文；空 = 只开终端。
    Command string `json:"command,omitempty"`
    // EnvMissing 报告 EnvFile 在本机已不存在。**不带 omitempty**：false 是
    // 一个明确结论，缺键会让前端分不清它和「这版服务端还不认识这个字段」
    //（与 PtySession.Foreground 同一条纪律）。只在 GET 时由服务端算出，
    // PUT 时忽略客户端送来的值。
    EnvMissing bool `json:"env_missing"`
}
```

**不变式（服务端保证，客户端不得自行放宽）**：`EnvFile` 与 `Command` **至少一个非空**。

### 2.2 `proto.LaunchersResp` / `proto.LaunchersReq`（新增）

```go
// LaunchersResp 是 GET /api/launchers 的响应。
type LaunchersResp struct {
    Launchers []Launcher `json:"launchers"`
}

// LaunchersReq 是 PUT /api/launchers 的请求体：**整段替换**。
// 与 EnvMappingReq 同款语义——界面就是整块保存，整段替换让「界面所见 =
// 落盘所得」无需推理。
type LaunchersReq struct {
    Launchers []Launcher `json:"launchers"`
}
```

**端点**（两个，都带 `?machine=`，由 `forwardIfRequested` 透传）：

| 方法 | 路径 | 语义 |
|---|---|---|
| GET | `/api/launchers[?machine=]` | 列该机启动项，逐条算 `EnvMissing` |
| PUT | `/api/launchers[?machine=]` | 整段替换，返回保存后的 `LaunchersResp` |

**PUT 的校验与错误码**（逐条照 `handleEnvMapping` 的形态）：

| 情形 | 码 | 文案要点 |
|---|---|---|
| `Name` 为空 | 400 | 「启动项名字不能为空」 |
| `Name` 重复 | 400 | 带上重复的那个名字 |
| `EnvFile` 与 `Command` 都为空 | 400 | 「Env 文件与执行命令至少填一个」（spec §B.3 故事 3 的原话） |
| `EnvFile` 含路径分隔符 | 400 | 透传 `envfile.ErrBadName` 的中文原文 |
| `EnvFile` 指向的文件不存在 | 400 | 「指定的 env 文件不可用：…」——与 `handleEnvMapping` 同款：把错误挡在保存这一刻，好过三天后炸 |

### 2.3 `proto.CreatePtySessionReq` 增两字段

```go
    // EnvFile 是要额外注入的 env 文件名（该机 <DataDir>/env 下的纯文件名）。
    // 空 = 不注入。**文件不存在时创建会话直接 400 失败，不降级成无变量终端**
    //（spec §B.4 第 5 条：失效引用不静默）。
    EnvFile string `json:"env_file,omitempty"`
    // InitCommand 是 shell 就绪后送进终端输入的命令原文（不含换行，服务端补）。
    // 空 = 不送。它在**交互 shell 内部**执行，命令退出后会话继续存在。
    InitCommand string `json:"init_command,omitempty"`
```

**兼容性硬要求**：两者都缺席时，创建会话的行为与今天**逐字节相同**。这是判据不是愿望，要有断言守住（见 §5）。

### 2.4 能力位：**四处**，不是一处

`LaunchersSupported` 要在**四个地方**同时存在，漏一处就是一条断掉的投影链：

| 位置 | 作用 |
|---|---|
| `proto.StatusResp`（Go） | agentd 自报 |
| `proto.Machine`（Go，[projects.go:130](internal/proto/projects.go:130) 一族） | 汇总方探活时从对端 StatusResp **投影**过来 |
| TS `StatusResp` | 直连 `/api/status` 的消费方 |
| TS `Machine` | 控制台按机器判断的消费方（**工作台实际读的是这个**） |

`PtySupported` / `RevealSupported` / `ScratchRoot` 三个先例都是这条四处链路。**初稿只写了 `StatusResp` 一处**——落骨架时才发现另外三处（见 §7 的第二次订正）。

#### 2.4.1 `proto.StatusResp` 的声明

```go
    // LaunchersSupported 报告本机 agentd 是否认识启动项（env_file / init_command）。
    //
    // **三态的处置与 PtySupported / RevealSupported 刻意相反**，别照抄邻居：
    //   缺席(nil) = 对端 agentd 太老 → **按不支持处置**（不送 env_file /
    //               init_command，界面不展示该机的启动项）
    //   false     = 不支持
    //   true      = 支持
    //
    // 为什么反着来：那两个能力位缺席时「放行」的代价只是一次必然失败的请求，
    // 用户当场看得见；而这里放行的代价是**静默起一个没有环境变量的终端**——
    // 请求返回 200、终端正常出现、变量悄悄不在。用户可能半小时后才发现。
    // 未知时的保守方向由「失败可见性」决定，不由邻居的写法决定（§4.1）。
    LaunchersSupported *bool `json:"launchers_supported,omitempty"`
```

### 2.5 `envfile` 新增按文件名加载

```go
// LoadFile 按文件名加载一份 env 文件，返回可注入的 KEY=VALUE 列表。
func LoadFile(dir, name string, log *slog.Logger) ([]string, error)
```

`Resolver.For(agent)`（[resolver.go:77](internal/envfile/resolver.go:77)）**改为调用它**——`For` 只保留「agent → 文件名」的换算，加载逻辑归一处。这样 `resolvePath` 校验、`Parse(f, os.LookupEnv)` 展开、重复键 WARN、**只打 key 不打值**四条纪律对两个入口自动一致。

> 这是重构不是新增语义：`For` 的对外行为（含「未配置返回 `(nil,nil)`」）一字不变。

### 2.6 `ptyhost.OpenOptions` 增一字段

```go
    // InitCommand 是 shell 就绪后写进 PTY 输入的命令原文（不含换行，实现补）。
    // 空 = 不写。**它不进 argv**：走 argv 会把 login shell 变成非交互 shell，
    // 命令退出即会话结束，与「命令跑完人还能接着用这个终端」相悖。
    InitCommand string
```

### 2.7 新包 `internal/launcher`（d_localint）

```go
// Dir 返回启动项配置文件路径（<dataDir>/launchers.json）。
func Dir(dataDir string) string

// Load 读该机的启动项列表。文件不存在返回 (nil, nil)——那是正常起点。
func Load(dataDir string) ([]proto.Launcher, error)

// Save 整段替换该机的启动项列表（先校验后落盘）。
func Save(dataDir string, list []proto.Launcher) error

// Validate 校验一份启动项列表：名字非空且唯一、至少填一个、文件名形状合法。
// 返回的错误文本可直接作为 400 的响应体（中文原文）。
func Validate(list []proto.Launcher) error
```

**为什么不落 `config.yaml`**：config 以 `KnownFields(true)` 严格解析，新键会让旧版 agentd 读到新版写的配置**直接启动失败**——这是 `PathDirs` / `Proxy` 注释里逐字记着的坑。启动项是一张会长的列表，单独一个文件没有这个换版风险。（spec §B.2 弃选 3，此处只是落地。）

文件权限 `0600`、目录 `0700`，与 `envfile.Write`（[files.go:117](internal/envfile/files.go:117)）一致——启动项本身不含凭据，但它指名了哪份 env 文件，权限基线不该松于同目录其余内容。

### 2.8 TS 侧投影

```ts
export interface Launcher {
  name: string
  env_file?: string
  command?: string
  env_missing: boolean   // 不可选：false 是明确结论
}

export interface CreatePtySessionReq {
  base_path: string
  base_kind: string
  rel?: string            // ← 本次补上：Go 侧一直有，TS 侧一直漏（§1.2）
  cols: number
  rows: number
  env_file?: string
  init_command?: string
}
```

`StatusResp` 的 TS 侧加 `launchers_supported?: boolean | null`，注释必须写明**它的 null 处置与 `pty_supported` 相反**。

---

## 3. 语义细则（本节即冻结的行为契约）

### 3.1 旧版远端 agentd 的静默降级（本需求最大的坑，处置写死在这里）

`handleCreatePtySession` 第一行就 `forwardIfRequested`（[pty_api.go:100](internal/agentd/pty_api.go:100)），请求体**原样转发**。而 `encoding/json` 默认**忽略未知字段**——所以一台旧版远端 agentd 收到带 `env_file` / `init_command` 的请求会：正常起一个终端、返回 200、**变量和命令悄悄消失**。

**处置（两道，缺一不可）**：

1. **前端侧**：只对 `launchers_supported === true` 的机器展示启动项。nil / false 一律不展示（§2.4 的反向三态就是为这一道服务的）。
2. **服务端侧**：本机 agentd 收到自己不认识的组合时不会有这个问题；但**新版汇总方转发给旧版目标机**这条路前端拦不住误操作（比如用户手搓请求）。因此新版 agentd 在 `LaunchersSupported=true` 时**必须真的实现**这两个字段——能力位与实现同生同死，不允许「先上报能力位，实现下一版补」。

### 3.2 「shell 就绪」的判据

**定为：PTY 主端产出第一个字节，或 `initCommandReadyWait` 超时，以先到者为准。**

- 观测点是现成的：`Engine.Open` 之后的 `go h.pump(s)` 读循环（[engine.go:78](internal/ptyhost/engine/engine.go:78)）就是唯一看得见「shell 有动静了」的地方。
- `initCommandReadyWait = 3 * time.Second`。超时**不是失败**：照样把命令写进去（内核输入缓冲一直在，字节不会丢），只打一条 Debug。
- 形状照 `prochost.WaitInputReader`（真实信号 + 有界超时），**不用固定 sleep**。

**被否的两个做法**：

- **立刻写**：伪终端行规程在 exec 前就位，字节确实不会丢；但 login shell 的 rc 链**可以合法地读 stdin**，那种 rc 会把我们的命令吃掉，而症状是「命令没跑，也没有任何报错」。
- **等固定时长**：500ms 在冷启动的机器上不够，2s 在热机上是白等——两头都不对，且它把一个可观测的事实换成了一个猜测。

**已知残余风险（如实记录，不假装解决）**：rc 链在产出任何输出**之前**就读 stdin 时，本判据同样救不了。症状可观测（终端里能看到命令原文被当成 rc 的输入），届时按真实 rc 形态另议。

### 3.3 env 的叠加顺序

`sessionEnv()`（[pty_api.go:42](internal/agentd/pty_api.go:42)）的产物**在前**，启动项 env 文件的解析结果**在后**——后者覆盖前者。

理由：`sessionEnv()` 里是 `os.Environ()` + `TERM` + `env_forward`，都是「这台机器的缺省环境」；用户选一份 env 文件恰恰是为了覆盖缺省（例如换一个 `ANTHROPIC_BASE_URL`）。反过来叠会让选文件这个动作在最需要它的场景下失效。

`Parse` 的展开用 `os.LookupEnv`（沿用 `Resolver.For` 的既有做法），**不查 `sessionEnv()` 的结果**——那会让同一份文件在「作为 executor env」与「作为终端 env」时展开出不同的值。

### 3.4 命令原文不落任何日志

启动项的 `Command` 可能含凭据（`API_KEY=xxx some-cmd`）。日志只记「是否带命令」与长度，**不记原文**——与 env.go 文件头那条「任何路径都不得把 env 的值写进日志或响应」同源。

`GET /api/launchers` 的响应**会**含 `Command` 原文：它是用户自己填的、要在界面上编辑的东西，与 `GET /api/env/file`（含值全文，仅编辑时调用）同一条诚实边界。

---

## 4. 拍板记录

### 4.1 `LaunchersSupported` 的 nil 处置与既有能力位相反

- **难逆转**：改了要同时动 proto 注释、agentd 上报、前端三处判断，且改错的症状是静默的。
- **无上下文会惊讶**：`PtySupported` 与 `RevealSupported` 的注释都逐字写着「缺席**不许**当成 false」。后人看到第三个能力位反着写，第一反应必然是「这里写错了，统一一下」——统一之后，旧机器上的启动项会静默起出没有变量的终端。
- **真取舍**：被否的是「与邻居保持一致」。否因——那两个能力位缺席时放行的代价是**一次必然失败且当场可见**的请求；这里放行的代价是**200 + 终端正常出现 + 变量悄悄不在**。未知时的保守方向由失败的可见性决定，不由邻居的写法决定。

### 4.2 就绪判据用「首字节输出 + 有界兜底」，不用立刻写也不用固定 sleep

- **难逆转**：判据一旦定错，症状是「偶发地命令没跑」，会被当成用户手误而长期不被立案。
- **无上下文会惊讶**：「伪终端的输入缓冲在 exec 前就有了，为什么还要等？」——看起来是多余的一层。
- **真取舍**：被否的两个做法各有像样的理由（见 §3.2），且立刻写在绝大多数 rc 下确实能工作。选它是因为失败模式的形状：立刻写的失败是**静默吞命令**，而多等最多 3 秒的代价是可见且有上限的。

### 4.3 `Name` 即身份，不设 id 字段

- **难逆转**：事后加 id 要迁移已落盘的 `launchers.json` + 改前端的列表键。
- **无上下文会惊讶**：「列表项没有 id」在前端看来是缺陷（React key、稳定引用）。
- **真取舍**：被否的是「服务端生成 uuid 作 id」。否因——启动项没有任何跨对象引用（点一下就开一个终端 tab，tab 不持有启动项引用），id 唯一的用处是 React key，而 `Name` 机器内唯一已经满足；先例是 `EnvBinding` 以 `Executor` 名为键（[env.go:39](internal/proto/env.go:39)）。加一个只服务于框架细节的字段，代价是它会长期被误当成稳定引用而滋生「改名不影响引用」的错觉——而实际上改名就是换一个启动项。

### 4.4 `envfile` 的加载逻辑归一处（`LoadFile`），`For` 改为调用它

- **难逆转**：两份加载逻辑一旦并存，四条纪律（名字校验 / 展开来源 / 重复键 WARN / 只打 key 不打值）就要各维护一遍。
- **无上下文会惊讶**：本可以在 agentd 侧直接 `os.Open` + `envfile.Parse` 三行搞定，不动 `envfile` 包。
- **真取舍**：被否的就是那个三行版本。否因——**「只打 key 不打值」这条凭据纪律会在第二个实现里被漏掉**，而漏掉之后不报错、不变红，只是日志里多了一行带 token 的字符串。

---

## 5. 测试接缝（承 spec §B.6，此处落成可执行判据）

| 缝 | 判据 |
|---|---|
| **主缝**：会话创建请求 → 实际 shell 环境 + PTY 首次输入 | 四种组合（都不带 / 只带 env / 只带命令 / 都带）；**「都不带 == 今天的行为」必须是一条断言**，它是兼容性的唯一守卫 |
| 同上 | `env_file` 指向不存在的文件 → **400 拒绝**，且**没有会话被创建**（反向断言，需配一条正面断言：合法文件时会话确实创建了） |
| 次缝：`launcher.Validate` | 穷举：名字空 / 名字重复 / 两项都空 / 文件名含分隔符 / 合法 |
| 契约夹具 | `Launcher` 进 `internal/proto` 的 fixture 生成器（`contract_fixture_test.go:52`）+ `web/src/api/contract.test.ts` 强类型承接 |

**边界型子系统的真机项**（d_host / d_remote 都是边界型）：

1. 新版控制台 → **旧版远端 agentd** 的实际行为（§3.1 的前提）；
2. rc 链读 stdin 的真实发生率（§3.2 的残余风险）；
3. 命令在真实 login shell 里执行后，会话确实继续存在且 Ctrl-C 只杀命令。

---

## 6. 目标图变更

新包 `internal/launcher/**` 归 **d_localint**（与 `envfile` 同域：都是「这台机器上的本地配置与文件」）。不加路径规则的话它是**图外文件**，`graph check` 会 warn 且它的跨域调用不受任何契约约束。

新增的跨域调用方向逐条核（对照本轮 `graph check` 实测）：

| 方向 | 是否已存在 | 处置 |
|---|---|---|
| `d_controlplane -> d_localint` | 已有（预算 36） | 不动。启动项的 CRUD 走这条边 |
| `d_controlplane -> d_host` | 已有（预算 25） | `OpenOptions` 加字段不新增调用点 |
| `d_localint -> d_contract` | 不存在 | **不需要新增**——见下方订正 |

> **本次不新增任何依赖方向。** 目标图的唯一改动是把 `internal/launcher/**` 登记进 `d_localint` 的路径规则（不登记它就是图外文件，`graph check` warn 且它的跨域调用不受任何契约约束）。

#### 订正：`internal/launcher` 不 import `proto`（Ticket 0 推翻的一条结论）

本文档初稿在 §2.7 写了 `Load/Save` 直接收发 `[]proto.Launcher`，并据此在本节声明「需要新增 `d_localint -> d_contract` 方向」。**落骨架时这条被推翻了**：

`proto.Launcher` 带 `EnvMissing`——一个**每次读盘现算的派生字段**。落盘一份派生值就有了两个真相：文件里写着 `false`，而磁盘上那份 env 文件早已被删。所以落盘形态必须是另一个类型。骨架里它叫 `launcher.Item`（只有 `Name` / `EnvFile` / `Command`），于是 `internal/launcher` **根本不 import proto**，新方向也就不存在了。

`Item` ↔ `proto.Launcher` 的换算（含 `EnvMissing` 的计算）归 agentd 的 handler —— 那正是 `d_controlplane -> d_localint` 这条既有边该承载的事。

**这就是 Ticket 0 的存在理由**：契约错配在编译期（这里是在写骨架的那一刻）暴露，而不是留到集成阶段才发现目标图里多了一条谁也说不清为什么的边。

§2.7 的签名相应订正为：

```go
func Dir(dataDir string) string
func Load(dataDir string) ([]Item, error)
func Save(dataDir string, list []Item) error   // 先校验后落盘
func Validate(list []Item) error               // 纯函数，可穷举
type Item struct { Name, EnvFile, Command string }
```

`Load` **不做校验**（磁盘上可能存着上一版写下的、按今天规则不合法的数据；读出来交给调用方展示，好过让整个配置面因为一条坏数据打不开）；`Validate` **不校验文件是否存在**（那要读盘，属于 handler 的一次性校验，本函数保持纯函数以便穷举）。

---

## 7. 交棒声明

本轮新鲜证据，逐项列出：

| 法定产出 | 证据 |
|---|---|
| 契约增量文档落盘，签名带现状出处 | 本文件 §1、§2，出处全部在本轮工作树复核过 |
| 目标图已更新**且已提交** | `internal/launcher/**` 登记进 `d_localint`；**本次不新增任何依赖方向**（§6 的订正） |
| 契约闸门本轮通过 | `handoff graph check` → `fails=0` |
| Ticket 0 骨架本轮编译通过 | `go build ./...` 退出 0；`go vet ./...` 无输出；`gofmt -l` 无输出；`npx tsc -b` 退出 0 |
| 骨架抓到的**第二处**契约错配 | 初稿把能力位只写在 `proto.StatusResp` 一处。先例（`PtySupported` / `RevealSupported` / `ScratchRoot`）是**四处一条投影链**：Go StatusResp → Go Machine（探活投影）→ TS StatusResp → TS Machine。漏掉 `proto.Machine` 那一处的后果是控制台永远读不到这个能力位——而工作台读的**恰恰是** `Machine`。§2.4 已补 |
| 契约错配在编译期暴露（骨架的存在理由） | **当场兑现了一次**：初稿声称需要新增 `d_localint -> d_contract` 方向，落骨架时发现 `proto.Launcher` 的 `EnvMissing` 是派生字段、不能落盘，于是落盘形态另立 `launcher.Item`，该包**根本不 import proto**，新方向不存在。§6 已订正 |
| 可执行冻结条目 | **有命中**：`LaunchersResp` 与 `CreatePtySessionReq` 已进 `internal/proto` 的 fixture 生成器，`web/src/api/contract.test.ts` 用强类型承接并断言（`npx vitest run src/api/contract.test.ts` → 32 passed）。`launcher.Validate` 的四条规则以穷举测试冻结（`go test ./internal/launcher/` ok） |
| 三重闸门拍板记录 | 4 条，见 §4 |
| 既有测试未被本次改动打红 | `go test ./internal/proto/ ./internal/envfile/ ./internal/launcher/ ./internal/ptyhost/... ./internal/agentd/` 全 ok |

**欠账：无。**

**留给 breakdown 的三个已知事实**（不是欠账，是下一节点的输入）：

1. **`internal/launcher` 只有落盘与校验，没有任何 handler 接线**——`GET/PUT /api/launchers` 两个端点、`Item ↔ proto.Launcher` 的换算（含 `EnvMissing` 计算）、`LaunchersSupported` 的上报，全部还没写。
2. **`ptyhost.OpenOptions.InitCommand` 已声明但无人读**：engine 侧的「首字节就绪 + 3s 兜底」尚未实现。声明与实现同生同死这条（§3.1 第 2 点）在实现落地前**不得上报 `LaunchersSupported=true`**。
3. **顺带补上的 `rel`**：TS 侧 `CreatePtySessionReq` 此前漏声明 `rel`，本次补齐并进了夹具。这处漂移与本需求无关，但它暴露的机制缺口（对象展开绕过超额属性检查）值得在拆解时确认还有没有同类。

交棒：`charter:breakdown`。
