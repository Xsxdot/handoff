# 控制台配置执行纪律：分区编辑正文 + 开发机详情配映射 —— 设计

> 日期：2026-08-19
> Backlog：**B156**（取号依据见 §9）
> 基线：`claude/dispatch-discipline-frontend-config-f7d563`（main 线，含 B129 全部提交）
> 形态基准：`prototypes/discipline-config/pages/settings.html`（2026-08-19 走查确认）

## 1. 问题与目标

B129 把执行纪律块做成了「按执行器分档 + 可配置文件」的机制，但配置面**只有一条路**：
去那台执行机上手改 `~/.handoff/config.yaml` 的 `discipline:` 段，再手动往
`<DataDir>/discipline/` 丢文件。控制台里一处都没有——`grep -rn discipline web/src` 零命中。

代价不是「不方便」，是**这个机制的可达性取决于用户记不记得住它存在**。纪律块选错一档的
实测代价已经量化过（B93：subagent 版给 codex，同一份 6-task plan 从「0 推动 26 分钟跑完」
退化成「9 次人工推动只到 3/6 且最后卡死」），而今天想微调它，第一步是 ssh 到那台机器。

**目标**：把「写纪律块正文」和「给某台机器的某个 executor 指定用哪块」这两件事搬进控制台，
且两件事都按机器分——因为纪律块的真相就存在每台执行机各自的磁盘与配置里。

### 1.1 非目标

以下都是真缺口，但不在本期，避免范围滑坡：

- **不做删除与改名**。映射里存的是文件名，改名会让映射静默指空；要换名就另存一份再改映射。
  废弃文件留在盘上不产生任何行为（没有 executor 引用它就是死文件）。
- **不做任务详情里的「本次用了哪块纪律」**。`proto.Task.Discipline` 有 json tag 但 TS 类型
  没有该字段，且 `appendProgress` 落的那条 `纪律块: <Source>` 事件被前端
  `QUIET_EVENTS`（`eventPhrase.ts:30`）整类滤掉。这是回显链在控制台上的断点，另立条目。
- **不做纪律内容的语义校验**（沿 B129 §1.2）。handoff 不理解纪律块写了什么。
- **不做 `config.yaml` 的注释保全**（见 §4.3）。
- **不动 `env` 分区**，它仍是占位。本设计只在形状上参照 env，不顺手实现它。

## 2. 形态

形态已在 fork 副本 `prototypes/discipline-config/` 上走查确认，**该副本即真实页面的验收基准**
（`prototypes/base/README.md` 已记「确认中」）。以下是文字化的判据。

### 2.1 设置页新分区「执行纪律」

`SettingsPage` 的 `SECTIONS` 从三项变四项，新项排在「开发机」之后：
开发机 / **执行纪律** / 常规 / Env 文件。

分区内自上而下：

1. **机器切换条**（本机 / 各远程机）。纪律块文件是每台机器各自的，切机器换整个列表。
   断开的机器仍然列出并标断开——不静默少一台（沿 `MachinesPage` 的诚实展示纪律）。
2. **左列表**，两组：
   - 「内置（只读，随二进制分发）」：`subagent`、`single-context`
   - 「<机器名> 上的文件」：该机 `<DataDir>/discipline/` 下的文件，附目录路径
   - 每项下一行小字标**谁在用**（`codex 在用` / `未被引用`）。不做删除也要有这行：
     它把「映射」这件在另一个分区里的事摆在编辑现场，避免改错文件。
   - 底部「＋ 新建文件」
3. **右编辑区**：纯 textarea 原文编辑，不做 markdown 预览与语法高亮。
   - 选中内置版时：textarea `readonly` + 灰底 + 「只读」徽章，主按钮变成**「以此为模板新建」**
   - 选中用户文件时：主按钮是「保存」，脚注写明「保存后**下一个任务**即生效（正在跑的任务
     不受影响）；上限 64 KiB」
4. **断开的机器**：整个左右两栏换成一句说明 + 断开原因原文，不画一个按不动的编辑器。

「新建文件」弹层两个字段：文件名（纯文件名）+ 起始内容（空白 / 复制内置 subagent /
复制内置 single-context）。新建成功后提示「还没有任何 executor 用它——去开发机分区指过去」。

### 2.2 开发机详情内的「执行纪律」块

`MachineDetail` 在现有只读信息之下新增一块，每个该机上报的 executor 一行：

```
opencode  [ 内置默认（subagent）        ▾ ]  未配置——按该执行器有没有 subagent 机制自动选
codex     [ codex-strict.md             ▾ ]  正文在「执行纪律」分区里编辑
grok      [ 关闭注入（不发纪律块）      ▾ ]  这台机器上派 grok 时不注入任何纪律块
```

- 下拉选项 = `内置默认（<tier>）` + 该机全部文件 + `关闭注入（不发纪律块）`
- 改动即标脏（琥珀边框 + 「有未保存的改动」），整块一个「保存」
- 保存后脚注恢复为「保存后下一个任务即生效，不必重启 agentd」
- 机器断开时不渲染控件，写明读不到也写不了

**为什么映射挂在每台机器的详情里，而不是纪律分区里做一张跨机大表**：映射是机器级配置，
与「Agent 版本 / 可用执行者 / 运行任务数」同属一台机器的属性；跨机大表会让「这台机器有哪些
executor」这件事失去落点，而每台机器的 executor 列表本来就不同。

### 2.3 三档的翻译（唯一容易搞反的地方）

| 界面下拉 | 请求体 `mode` | `config.yaml` 里的 `discipline` 段 | Resolver 行为 |
|---|---|---|---|
| 内置默认（tier） | `default` | **键不存在** | 按 `defaultTier` 取内置版 |
| `<文件名>` | `file` + `file: "x.md"` | `codex: x.md` | 读 `<DataDir>/discipline/x.md` |
| 关闭注入 | `off` | `grok: ""` | 不注入 |

「未配置 ≠ 不注入」是 B129 与 env 刻意的偏离，界面上靠下拉默认项的文案
（`内置默认（single-context）`）把它变可见，不靠一行说明文字。

## 3. 后端接口

四个端点，全部挂在 `?machine=` 转发之下（复用 `forwardIfRequested`，与 workspaces 那套同路）。

| 方法 | 路径 | 用途 |
|---|---|---|
| GET | `/api/discipline` | 目录路径 + 内置两版全文 + 该机文件列表 + 每个 executor 当前档位 |
| GET | `/api/discipline/file?name=` | 单个用户文件正文 + sha256 |
| PUT | `/api/discipline/file?name=` | 写正文（带前置哈希；新建时哈希为空串） |
| PUT | `/api/discipline/mapping` | 整段替换该机 `discipline` 配置段 |

**为什么 GET 一次给全**：纪律分区要文件列表 + 内置全文，开发机详情要 executor 列表 + 当前
映射 + 可选文件名。同一份数据喂两处界面，不做两套接口。内置全文只有两份、几 KB，随列表
带走；用户文件正文按需单读，避免一次把几百 KB 塞进列表响应。

### 3.1 数据结构（`internal/proto`）

```go
// DisciplineResp 是 GET /api/discipline 的响应。
type DisciplineResp struct {
    Dir      string              `json:"dir"`      // <DataDir>/discipline 绝对路径，界面照原样显示
    Builtins []DisciplineBuiltin `json:"builtins"` // 内置两版全文，随二进制走
    Files    []DisciplineFile    `json:"files"`    // 该目录下的文件（不含正文）
    Bindings []DisciplineBinding `json:"bindings"` // 该机每个 executor 的当前档位
}

// DisciplineBuiltin 是一份内置纪律块。Tier 取 discipline.TierSubagent / TierSingleContext。
type DisciplineBuiltin struct {
    Tier    string `json:"tier"`
    Content string `json:"content"`
}

// DisciplineFile 是纪律块目录下的一个文件。Size 是磁盘真实大小。
type DisciplineFile struct {
    Name   string `json:"name"`
    Size   int64  `json:"size"`
    SHA256 string `json:"sha256"`
}

// DisciplineBinding 是一个 executor 的当前档位。
//
// Mode 三值：default（未配置，用内置默认）/ file / off。DefaultTier 恒有值——
// Mode 为 default 时界面要显示「内置默认（single-context）」，其余两档它是
// 「改回默认会变成什么」的预告，同样要显示。
type DisciplineBinding struct {
    Executor    string `json:"executor"`
    Mode        string `json:"mode"`
    File        string `json:"file,omitempty"`
    DefaultTier string `json:"default_tier"`
}

// DisciplineMappingReq 是 PUT /api/discipline/mapping 的请求体：整段替换。
//
// 为什么整段替换而不是逐项 patch：界面就是整块保存，整段替换让「界面所见 = 落盘所得」
// 无需推理；逐项 patch 则要额外定义「没出现的键是保持还是删除」。
type DisciplineMappingReq struct {
    Bindings []DisciplineBinding `json:"bindings"`
}
```

**`Bindings` 列哪些 executor**：该机注册的 adapter 名 ∪ 该机 `discipline` 配置段里已出现的键，
按名字排序。并集的第二半不能省——一个配了纪律块但当前没注册的 executor 名（改名、临时摘掉）
若不列出，界面就看不见它，而它还躺在配置里。

**整段替换如何对待请求体之外的键**：一律不保留。这是安全的，且仅因为
`Bindings` 是上面那个并集——界面拿到的就是全集，回写全集不会丢任何东西。
若日后有别的写入方只送部分键，这条语义必须重新审视。

文件读写**复用既有类型**，不新造：`GET .../file` 回 `proto.FileRead`（`Content`/`Size`/`SHA256`），
`PUT .../file` 收 `proto.FileWriteReq`（`content` + `base_sha256`）、回 `proto.FileWriteResp`，
冲突时回 `proto.FileConflictResp`（带 `current`）。这与工作树文件编辑是同一件事的同一形状，
前端的冲突处理也能照抄。

### 3.2 校验与错误语义

| 情况 | 状态码 | 说明 |
|---|---|---|
| 文件名含路径分隔符 / 为 `.` `..` | 400 | 复用 `discipline.Resolver` 的同款判据，错误文本带目录路径 |
| 正文超 64 KiB | 400 | 与 `maxBlockSize` 同一常量，不另立 |
| 新建时同名文件已存在（`base_sha256` 为空但文件在） | 409 | 防止「新建」静默覆盖别人的文件 |
| 写入时 `base_sha256` 与现盘不符 | 409 + `FileConflictResp` | 界面提示「已被改动」并给「重新加载」，**绝不静默覆盖** |
| 映射 `mode=file` 但该文件不存在 | 400 | 见下 |
| `mode` 不是三值之一 / executor 名为空 | 400 | |
| 目标机器不可达 | 由 `forwardIfRequested` 原样透出 | 界面按 §2.1 降级 |

**为什么保存映射时要前置校验文件存在**：`Resolver.For` 的既定语义是「配了但读不到 = 派发
失败」（刻意不退回内置，否则用户会以为跑的是自己那套）。把这个错误挡在保存那一刻，比让它
在三天后某次派发时炸出来强得多。注意这是**保存时的一次性校验**，不是恒真保证——文件仍可能
在保存之后被删除，那时的失败仍由派发路径承担。

### 3.3 目录不存在

`<DataDir>/discipline/` 今天没有任何东西自动创建。GET 遇到目录不存在时返回空文件列表
（不是 404——「目录还没建」和「读不了」是两件事），首次 PUT 文件时以 0700 建目录。

## 4. 三处既有点的改造

### 4.1 `swapConf` 必须深拷 `Discipline`

`server.go:240` 的写时复制**只深拷了 `Targets`**，注释写着「其余字段在 agentd 运行期不可变，
共享是安全的」。本设计让 `Discipline` 变成运行期可变，于是：

- `next.Discipline` 必须与 `Targets` 一样逐键复制
- 那句注释要改对，并写明「新增可变字段时必须在此深拷」——否则下一个人照抄现状就会踩

不深拷的后果是就地改到旧快照：`s.cfg.Load()` 返回的指针本应在调用方持有期间恒定，
共享 map 会让这个性质破掉（这正是 `conf()` 注释承诺的「不会看到改到一半的状态」）。

### 4.2 Resolver 改吃活配置

现状：`NewManager` 里 `discipline.NewResolver(dir, cfg.Discipline, log)`（`manager.go:258`），
map 在构造时被吞下，此后配置怎么改都与它无关——**映射改了不重启 agentd 不生效**。

改法：`NewResolver` 的第二参从 `map[string]string` 改为 `mapping func() map[string]string`，
`For` 每次取当前值。这与它「不缓存文件内容、改完下个任务即生效」的既有纪律同构——
两个来源（文件、映射）从此有同一种时效。

接线点是干净的：`cmd/agentd.go` 里 `srv := NewServer(...)`（:156）先于
`mgr := NewManager(...)`（:183），因此给 `Server` 加一个窄的导出方法
（返回当前 `Discipline` 映射）并传进 `NewManager` 即可，不需要新造配置持有者类型。

`envfile.Resolver` 有完全相同的问题，**本期不动它**：env 没有写接口，映射不会在运行期改变。

### 4.3 `config.Save` 会丢注释

`config.save` 是整份 `yaml.Marshal` 后覆写，用户手写的注释与键顺序都会丢。这是**既有行为**
（控制台「新增/删除开发机」早就在这么写配置），本期沿用、不解决。如实记在这里，是因为
本设计让写配置从「偶尔一次」变成「日常操作」，踩到的概率随之上升。

## 5. 前端落点

| 文件 | 动作 |
|---|---|
| `web/src/app/settings/DisciplinePage.tsx` | 新增：机器切换 + 文件列表 + 编辑器 + 新建弹层 |
| `web/src/app/machines/MachineDiscipline.tsx` | 新增：开发机详情内的映射块 |
| `web/src/app/machines/MachineDetail.tsx` | 挂上映射块 |
| `web/src/app/settings/SettingsPage.tsx` | `SECTIONS` 加一项 |
| `web/src/api/types.ts` / `client.ts` | 四个接口的类型与函数 |

**数据获取刻意不进轮询**：配置不是实时事实，进分区 / 切机器时拉一次、保存后刷新即可。
这条是防一个具体的坑——开发机分区那边是 `useMachines(true)` 的 15s 探活，纪律分区若照抄，
用户正在编辑的 textarea 会被后台刷新覆盖。

**错误一律原文透传**，不吞成「操作失败」（沿 `FileTab.tsx` 的既有纪律）。

## 6. 契约与测试

契约走既有纪律（`web/README.md:75`）：Go 结构 → `-update` 刷新 `internal/proto/testdata` fixture
→ 同步 TS 类型与 `web/src/api/contract.test.ts`。

Go 侧测试：

- 四个 handler 各自的正常路径 + §3.2 每一行错误语义
- `?machine=` 转发到位（沿 workspaces 转发测试的既有写法）
- **回归 1**：`swapConf` 改 `Discipline` 后，改动前取到的旧快照不受影响
- **回归 2**：`swapConf` 改完映射，**不重建 Manager** 的前提下 `Resolver.For` 立即反映新值
  （这是 §4.2 的唯一判据，缺了它热更新就是一句口号）

前端 vitest：

- 三档下拉的渲染与保存 payload（`default`/`file`/`off` 三种都要断言）
- 脏态标记与保存后清除
- 内置版只读 + 「以此为模板新建」
- 断开机器的降级展示
- **编辑中不被刷新覆盖**（§5 那条纪律的回归）

## 7. 安全

写接口落在已鉴权的 agentd API 之下（与 workspaces 写接口同一层）。纯文件名校验杜绝路径
穿越，64 KiB 上限杜绝把二进制垃圾塞进模型上下文。内置两版在二进制里，不落盘，因此
「用户文件覆盖内置」这种事在结构上不可能发生。

`config.yaml` 含 token，`config.Save` 的 0600 权限是硬要求，本设计不触碰该路径。

## 8. 验收判据

1. 控制台设置页「执行纪律」分区可切机器、可编内容、可新建、可从内置另存；断开机器按 §2.1 降级。
2. 开发机详情可给该机每个 executor 配三档并保存。
3. **真机判据**：在控制台把某台机器的 codex 从「内置默认」改成一份自定义文件并保存，
   **不重启 agentd**，随即向该机派一个 codex 任务，`dispatch` 的 stderr 回显
   `纪律块: 配置:<文件名>`，且执行者抄回的第一条纪律与该文件正文一致。
   （这条与 B129 的取证手法相同：服务端解析 + 模型实收两层互证。）
4. 页面形态对照 `prototypes/discipline-config/` 走查通过。

## 9. 取号与落点

取号按三者并集（`main` ∪ `handoff/web-console` ∪ 分支名认领）：`main` 到 119、
`handoff/web-console` 到 155、分支名无 >155 的认领，故取 **B156**。

**backlog 行要加在汇流点 `handoff/web-console` 上**，不要加在本分支的 backlog 副本里
（本分支是 main 线，其 backlog max 停在 119，在这里算 max 会撞号）。
