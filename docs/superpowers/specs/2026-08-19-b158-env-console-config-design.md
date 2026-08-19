# 控制台配置 Env 文件：分区编正文 + 开发机配映射 —— 设计

> 日期：2026-08-19
> Backlog：**B158**
> 基线：`handoff/web-console`（含 B157 的纪律配置面全部提交）
> 前身：**B157**（纪律块配置面）。本设计是它的孪生兄弟，凡结构相同处一律照搬、不发明新概念；
> 差异只有三处，全部在 §1.2、§2.3、§7。

## 1. 问题与目标

设置页的「Env 文件」分区今天是一句占位（`SettingsPage.tsx`：「本期不做。原型里有完整设计，
等它有真实落点时再实现」）。而后端的 env 注入（B19）与纪律块是**结构上的双胞胎**：

| 维度 | env（B19，现存） | discipline（B157，已落地） |
|---|---|---|
| 文件位置 | `<DataDir>/env/` | `<DataDir>/discipline/` |
| 配置键 | `Env map[string]string` | `Discipline map[string]string` |
| 键 → 值 | agent 名 → 文件名 | executor 名 → 文件名 |
| 解析器 | `internal/envfile.Resolver` | `internal/discipline.Resolver` |
| 文件名校验 | 纯文件名 | 同 |
| 大小上限 | 64 KiB | 64 KiB |
| **控制台配置面** | **无** | **有（B157）** |

于是「改一个 executor 的环境变量」今天仍然只有一条路：ssh 到那台执行机、手改
`~/.handoff/env/*.env`。本设计把这件事搬进控制台，形状照抄 B157。

### 1.1 非目标

- **不加密、不接 secret 后端**。这是 `internal/envfile` 包头写明的边界，本设计不推翻它。
- **不做 KEY/VALUE 表格编辑**。理由见 §2.1：会碾平注释、引号风格与 `export` 前缀。
- **不碰「常规」分区**。那一块的内容尚未定义，且横跨「客户端偏好（localStorage）」与
  「agentd 服务端配置（config.yaml）」两种截然不同的持久化，另立条目。
- **不做删除与改名**（沿 B157 §1.1：映射存的是文件名，改名会让它静默指空）。

### 1.2 与 B157 的三处真实差异

写这份 spec 的全部价值在这三处；其余部分是照搬。

1. **没有内置默认，只有两档**（§2.3）。env 未配置 = 不注入；这是 B129 当初就与
   discipline 刻意分开的一处，界面必须长得不一样。
2. **文件内容有语法**，保存前要解析校验（§3.3）。纪律块是自由文本，env 不是。
3. **值常含凭据**，默认不送进浏览器（§2.1、§7）。

## 2. 形态

沿 B157 的三栏结构：机器切换条 + 左文件列表 + 右内容区。

### 2.1 Env 分区（设置页第四项）

打开一个文件时，默认显示的**不是正文，而是解析出的变量清单**：每行一个 `KEY`，附值的
字节长度与 `重复定义` 标记。值本身不显示。

这一屏回答的是日常最高频的问题——「这台机给 opencode 注了哪些变量」——而回答它
**不需要看见任何一个值**。

想看值或改内容，点「编辑正文」，切成纯文本编辑器（复用 B157 那一个）。

**为什么正文是纯文本而不是 KEY/VALUE 表格**：env 文件是手写的，语法里有表格表达不出
的东西——行首 `#` 注释（**行内 `#` 不是注释**，因为代理 URL 里 `#` 合法）、可选 `export `
前缀、`'字面量'` 与 `"可展开"` 两种引号、`$VAR`/`${VAR}` 单层展开、重复键「后者覆盖但
位置留在首次出现处」。表格化再回写会把这些全部碾平，而 README 给的示例本身就带一行
解释性注释。这与 `config.Save` 丢注释是同一类损失，区别是那处是既有行为、这处是新造。

左列表只有一组「<机器> 上的文件」——**没有内置版**，右上角因此也没有「以此为模板新建」。
新建文件的弹层只有文件名 + 起始内容（空白）两项。

断开的机器：与 B157 同款降级（不发请求、不画编辑器、透出 error 原文）。

### 2.2 开发机详情内的「Env 文件」块

在现有的「执行纪律」块之下再加一块，每个该机上报的 executor 一行：

```
opencode  [ 不注入            ▾ ]  未配置——启动时不注入任何环境变量
codex     [ proxy.env         ▾ ]  正文在「Env 文件」分区里编辑
```

改动即标脏、整块一个「保存」、保存后提示「下一个任务即生效」——与「执行纪律」块逐字同构。

### 2.3 两档 vs 三档（唯一容易照抄错的地方）

| 界面下拉 | 请求体 `mode` | `config.yaml` 的 `env` 段 | Resolver 行为 |
|---|---|---|---|
| 不注入 | `off` | **键不存在** | 不注入 |
| `<文件名>` | `file` + `file: "x.env"` | `opencode: x.env` | 读 `<DataDir>/env/x.env` |

**注意与 discipline 的对应关系是错位的**：discipline 的「键不存在」= 用内置默认，
「空串」= 关闭注入；env 只有一种「不注入」，落盘用**键不存在**表示。

因此 env 的保存**不写空串**。空串在 `envfile.Resolver` 里会走到「读 `<dir>/`」这种
无意义路径，是纯粹的脏数据。服务端收到 `mode=off` 时删键，收到空文件名一律 400。

## 3. 后端接口

四读一写，全部走 `?machine=` 转发（复用 `forwardIfRequested`）。

| 方法 | 路径 | 用途 |
|---|---|---|
| GET | `/api/env` | 目录 + 文件列表（名字/大小/sha256）+ 每个 executor 的档位 |
| GET | `/api/env/file/keys?name=` | 解析出的变量清单（**只有 key 名、值长度、标记；不含值**） |
| GET | `/api/env/file?name=` | 正文全文（含值），仅「编辑正文」与「显示某个值」时调用 |
| PUT | `/api/env/file?name=` | 写正文（前置哈希；新建时哈希为空串），**写前解析校验** |
| PUT | `/api/env/mapping` | 整段替换该机的 `env` 配置段 |

### 3.1 数据结构（`internal/proto/env.go`）

```go
// EnvResp 是 GET /api/env 的响应。与 DisciplineResp 同构，少了 Builtins 一节。
type EnvResp struct {
    Dir      string        `json:"dir"`
    Files    []EnvFile     `json:"files"`
    Bindings []EnvBinding  `json:"bindings"`
}

// EnvFile 是 env 目录下的一个文件（不含正文）。
type EnvFile struct {
    Name   string `json:"name"`
    Size   int64  `json:"size"`
    SHA256 string `json:"sha256"`
}

// EnvBinding 是一个 executor 的当前档位。两档：off（键不存在）/ file。
type EnvBinding struct {
    Executor string `json:"executor"`
    Mode     string `json:"mode"`
    File     string `json:"file,omitempty"`
}

// EnvKey 是解析出的一个变量。**永不含值**——这是本设计的凭据边界所在。
//
// ValueBytes 是值的字节长度，口径是**展开后**（Parse 的产物）：它让
//「这个变量是不是空的」可判断，而不泄露内容。注意展开用 lookup=nil，
// 所以引用了外部变量的值在这里会显示为更短甚至 0——这不是 bug，见 §3.2。
//
// Duplicate 为真表示该键在文件里出现过多次（Resolver 的既有行为是 WARN 不拒，
// 界面照此标注、不拦保存）。
//
// **刻意没有「是否单引号字面量」这一项**：Parse 只回 Key/Value，不暴露引号风格，
// 要标它就得在 handler 里重扫一遍原始行、再造一套与 Parse 可能漂移的解析。
// 一个装饰性标记不值这个代价。
type EnvKey struct {
    Key        string `json:"key"`
    ValueBytes int    `json:"value_bytes"`
    Duplicate  bool   `json:"duplicate,omitempty"`
}

// EnvKeysResp 是 GET /api/env/file/keys 的响应。
type EnvKeysResp struct {
    Keys []EnvKey `json:"keys"`
}

// EnvMappingReq 是 PUT /api/env/mapping 的请求体：整段替换。
type EnvMappingReq struct {
    Bindings []EnvBinding `json:"bindings"`
}
```

`Bindings` 列哪些 executor、整段替换如何对待请求体之外的键——**与 B157 §3.1 完全相同**：
并集（注册的 adapter ∪ 配置里已出现的键），回写全集，不保留请求体之外的键。

正文的读写复用 `proto.FileRead` / `FileWriteReq` / `FileWriteResp` / `FileConflictResp`。

### 3.2 `keys` 端点的实现

服务端读文件 → `envfile.Parse(bytes.NewReader(data), nil)` → 把 `[]KV` 折成 `[]EnvKey`
（丢掉 `Value`，只留 `len(Value)`），`dups` 折进 `Duplicate`。

**`lookup` 传 nil**：展开时不查外部环境。理由——这个清单是给人看「文件里写了什么」的，
把 agentd 进程自己的环境变量掺进去会让同一个文件在不同机器上显示出不同的值长度，
既误导又多泄露一层信息。

### 3.3 校验与错误语义

| 情况 | 状态码 | 说明 |
|---|---|---|
| 文件名含路径分隔符 / 为 `.` `..` / 空 | 400 | 判据复用 `envfile` 侧的纯文件名校验 |
| 正文超 64 KiB | 400 | 用 `envfile` 的 `maxEnvFileSize`，不另立常量 |
| **正文解析失败** | **400** | **原样透传 `Parse` 的错误（它自带行号与原行）** |
| 新建时同名文件已存在 | 409 | 防止「新建」静默覆盖 |
| `base_sha256` 与现盘不符 | 409 + `FileConflictResp` | 界面给「重新加载」，绝不静默覆盖 |
| 映射 `mode=file` 但文件不存在 | 400 | 同 B157：把错误挡在保存这一刻 |
| `mode` 不是 `off`/`file`、executor 名为空、`mode=file` 但文件名为空 | 400 | |

**为什么 env 比纪律块多一道解析门**：纪律块写错了，模型顶多读到一段怪话；env 写错了，
症状是「代理配了但连不上」「go test 突然全红」，离根因十万八千里。`Parse` 已经能产出
带行号的错误，白不用。

重复键**不拦**：`Resolver` 的既有行为是 WARN + 后者覆盖，界面照此标注即可。拦它等于
在控制台里发明一条 agentd 不认的规则。

### 3.4 目录不存在

与 B157 同：GET 时返回空文件列表（不是 404），首次 PUT 时以 0700 建目录，文件 0600。

## 4. 既有点改造

三处，都是 B157 已经趟过的同一条路：

0. **`internal/envfile` 要补一层文件操作面**。它今天**只有 Resolver**——`List`/`Read`/`Write`
   与包级的纯文件名校验都不存在（`resolvePath` 是 `Resolver` 的私有方法）。B157 给
   `internal/discipline` 补的 `files.go` 就是模板：同名函数、同样的哨兵错误
   （`ErrBadName`/`ErrTooLarge`/`ErrExists`/`ErrBaseMismatch`）、同样「本层不打日志、
   错误一律 %w 带上下文」的边界。**不要把这层做进 handler**——那会让两个包对
   「什么是合法文件名」各有一套，正是 `Dir()` 当初收口目录知识要防的漂移。


1. **`swapConf` 深拷 `Env`**。B157 已经给 `Discipline` 补过一层，`Env` 还没有——现在它
   要变成运行期可写，必须一起深拷。那条注释里「新增运行期可变字段时必须在此补一层深拷」
   正是为这一刻写的。
2. **`envfile.Resolver` 改吃活映射**。`NewResolver` 的第二参从 `map[string]string` 改为
   `func() map[string]string`，`For` 每次取当前值；`Server` 加 `EnvMapping()` 访问器，
   `NewManager` 多收一个取值函数。与 B157 对 `discipline.Resolver` 做的改造逐字同构，
   包括 `Static()` 助手与 nil 时的 Warn。

   **不改的后果是具体的**：控制台改完 env 映射要重启 agentd 才生效，而「重启 agent」
   在控制台里至今没实现。B157 已经拒绝过这个尾巴一次，这里不能再留。

## 5. 前端落点

| 文件 | 动作 |
|---|---|
| `web/src/app/settings/EnvPage.tsx` | 新增：机器切换 + 文件列表 + 变量清单/正文双视图 |
| `web/src/app/machines/MachineEnv.tsx` | 新增：开发机详情内的 env 映射块（两档） |
| `web/src/app/machines/MachineDetail.tsx` | 在「执行纪律」块之后挂上 |
| `web/src/app/settings/SettingsPage.tsx` | 「Env 文件」分区从占位换成 `<EnvPage />` |
| `web/src/api/types.ts` / `client.ts` | 五个接口的类型与函数 |

沿用 B157 已确立的两条纪律：**不进轮询**（进分区/切机器/保存后各拉一次，避免 15s 探活
覆盖正在编辑的正文）、**错误原文透传**。

**共用组件的边界**：B157 的编辑器（正文 textarea + 保存 + 409「重新加载」）会被两处共用，
本期把它抽成 `settings/BlockEditor.tsx`。这是抽取，不是重写——抽的时候顺手把 B157 记账的
那处 Minor 修掉（409 判定当前用 `error === '已被改动'` 字符串相等来决定是否显示「重新加载」，
改成一个显式的 `conflict: boolean` 状态）。

## 6. 契约与测试

契约走既有流程：Go 结构 → `-update` 刷新 fixture → 同步 TS 类型与 `contract.test.ts`。

Go 侧：五个 handler 的正常路径 + §3.3 每一行错误语义；`?machine=` 转发；两条回归——
`swapConf` 改 `Env` 不污染旧快照、改完 env 映射**不重建 Manager** 时 `Resolver.For` 立即
反映新值。**外加一条**：`keys` 端点的响应体里不得出现任何值——用一个值可辨识的样本
（如 `TOKEN=zzz-secret-zzz`）断言序列化后的 JSON **不含**该子串。这条是 §7 的机器判据。

前端 vitest：两档下拉的保存 payload；变量清单默认不显示值；点「编辑正文」才拉全文
（断言此前没有调用过全文接口）；语法错误时的 400 原文展示；断开机器降级；编辑中不被刷新覆盖。

## 7. 凭据：诚实的边界

**做得到的**：
- 默认视图不把值发给浏览器（`keys` 端点结构上不含值）
- 值不进任何日志——`envfile` 包只打 key 名，新端点沿用；写文件的日志只记字节数与短哈希
- 文件磁盘权限 0600；接口全在已鉴权的 agentd API 之下；纯文件名校验杜绝穿越

**做不到、必须写明的**：点「编辑正文」时全文（含值）仍会到浏览器——不然没法编辑。
所以默认掩码防的是**肩窥、截图、录屏、把整页贴给别人**，不是防浏览器本身，更不是加密。
任何声称「凭据不出执行机」的说法都是错的，本设计不做这种承诺。

## 8. 验收判据

1. Env 分区可切机器、列文件、看变量清单（值不显示）、点「编辑正文」改并保存；语法写错时
   界面出现带行号的原文错误且**保存被拒**。
2. 开发机详情可给每个 executor 配两档并保存；落盘后 `config.yaml` 的 `env` 段与界面一致，
   `不注入` 档表现为**键不存在**（不是空串）。
3. **真机判据**：控制台改完某 executor 的 env 映射，**不重启 agentd**，随即派一个任务，
   该任务的 executor 进程确实拿到了新文件里的变量（用一个可观测的变量验证，如让任务
   打印某个自定义变量）。
4. `keys` 端点的响应体不含任何值（§6 的机器判据 + 一次人工抓包确认）。
5. 页面形态对照原型走查通过（形态基准见下）。

**形态基准**：Env 分区的三栏骨架与开发机详情的映射块是 B157 已确认形态的克隆，
沿用 `prototypes/discipline-config/` 那次的确认结果；**新的只有「变量清单（值掩码）」
这一屏**，它需要单独走查一次。

## 9. 取号

按三路并集实测（`main` ∪ `handoff/web-console` ∪ 分支名认领，本地与 origin 取较大值）：
`main` 到 119、`handoff/web-console` 到 **157**、分支名无 >157 的认领，故取 **B158**。

取号有时效——B157 就是因为二十分钟内汇流点被抢而改过一次号。**开工前请再算一遍**。
