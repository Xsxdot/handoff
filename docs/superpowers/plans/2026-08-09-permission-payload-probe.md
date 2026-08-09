# 权限载荷真机探针结论（Task 1）

> 计划：`docs/superpowers/plans/2026-08-09-handoff-permission-gate-precision.md` Task 1
> 环境：devbox `100.73.238.21`，agentd + 三个 executor 真机跑通
> 日期：2026-08-09

## 0. 方法

在本地临时把三个 adapter 的写文件工具挪进 `ask`（**未提交**，取样后已 `git checkout --` 还原），
并在三个 adapter 的权限入口各加一行原始载荷 dump（`PROBE_CLAUDE_PERM` /
`PROBE_GROK_PERM` / `PROBE_OPENCODE_PERM`），部署到 devbox 后各派发一个
「用 Write 工具写 probe.md，再用 Edit 改一次」的探针任务，从 `~/.handoff/agentd.log`
取原始 JSON。

取到的样本已落盘为 testdata，Task 7/8/9 的字段名一律以它们为准：

| 文件 | 内容 |
|---|---|
| `internal/executor/claudecode/testdata/perm_write.json` | claude Write |
| `internal/executor/claudecode/testdata/perm_edit.json` | claude Edit |
| `internal/executor/grok/testdata/perm_write.json` | grok Write（绝对路径） |
| `internal/executor/grok/testdata/perm_edit_absolute.json` | grok Edit（绝对路径） |
| `internal/executor/grok/testdata/perm_edit_relative.json` | grok Edit（**相对路径**） |
| `internal/executor/opencode/testdata/perm_edit.json` | opencode edit |
| `internal/executor/opencode/testdata/perm_bash.json` | opencode bash |
| `internal/executor/opencode/testdata/perm_external_directory_file.json` | opencode 越界写文件 |
| `internal/executor/opencode/testdata/perm_external_directory_bash.json` | opencode 越界 cd |

---

## 1. claude（Claude Code，经 perm.sock 的裁决 MCP）

载荷即裁决工具收到的 `ask` 帧本体：

```json
{"tool_use_id":"tool_ZXpq73R4ylBsSXeyTF3AZ1qU","tool_name":"Write",
 "input":{"file_path":"/Users/sycm/.handoff/worktrees/9fce125c/probe.md","content":"probe\n"}}
```

| 问题 | 答案 |
|---|---|
| 工具名在哪个字段、取值原文 | `tool_name`；实测取值 `"Write"`、`"Edit"`（首字母大写，与 settings.json 规则表同源） |
| 路径在哪个字段、绝对还是相对 | `input.file_path`；**绝对路径**（两次取样均为工作树全路径） |
| 一次请求是否可能带多个路径 | 否。Write / Edit 的 `input` 各只有一个 `file_path`（Edit 另带 `old_string`/`new_string`/`replace_all`） |
| 能否可靠提取路径 | **能**。Task 7 按 `tool_name` + `input.file_path` 实现，不需要回退 |

## 2. grok（ACP `session/request_permission`）

```json
{"toolCall":{"toolCallId":"call-…","kind":"edit",
  "title":"Write `/Users/sycm/.handoff/worktrees/a2e10493/probe.md`",
  "rawInput":{"variant":"Write","file_path":"/…/probe.md","content":"probe\n"},
  "_meta":{"x.ai/tool":{"name":"write","kind":"write","namespace":"opencode",
     "label":"Write","read_only":false,"input":{"path":"/…/probe.md"}}}}}
```

| 问题 | 答案 |
|---|---|
| 工具名在哪个字段、取值原文 | **不是 `toolCall.kind`**——它对 Write 和 Edit 一律是 `"edit"`，分不开。真正可区分的有两处：`toolCall.rawInput.variant`（`"Write"` / `"SearchReplace"`）与 `toolCall._meta["x.ai/tool"].kind`（`"write"` / `"edit"`，同处另有 `name` = `write` / `search_replace`、`label` = `Write` / `Edit`） |
| 路径在哪个字段、绝对还是相对 | `toolCall.rawInput.file_path`，`_meta["x.ai/tool"].input.path` 是它的副本。**两种都出现过**：Write 与首次 Edit 给绝对路径，同一任务后一次 Edit 给了相对路径 `"probe.md"` |
| 一次请求是否可能带多个路径 | 未观察到。每个 toolCall 只有一个 `file_path` |
| 能否可靠提取路径 | **能**，但**必须做相对路径解析**——相对于任务工作目录展开后再判归属。这不是理论风险，样本里已经出现（`perm_edit_relative.json`） |

**对 Task 8 的修正**：

1. 归一化工具名取 `rawInput.variant`，缺失时回落 `_meta["x.ai/tool"].kind`。**不要**用 `toolCall.kind`。
2. `rawPaths` 的字段名确定为 `file_path`（`path` 可保留作 `_meta` 分支的备用键）。
3. Step 4 的规则表改动保留：grok 的 `allowRules` 里留着 `Edit`/`Write` 就等于这些事件**根本不产生**，permgate 无从判定。探针期间 `allowRules` 置空、Edit/Write 进 `ask`，grok 任务正常跑完并逐次产出权限事件，未出现「默认全 ask」的连环唤醒（grok 内建对只读命令自动放行，见 `grok/taskenv.go` 文件头）。

## 3. opencode（SSE `permission.asked`）

三种 `permission` 取值各取到样本：

```json
{"permission":"edit","patterns":["probe.md"],
 "metadata":{"filepath":"/Users/sycm/.handoff/worktrees/4030564e/probe.md","diff":"…"}}

{"permission":"bash","patterns":["sleep 2","curl … 2>&1"],
 "metadata":{"command":"sleep 2; …; curl … 2>&1"}}

{"permission":"external_directory","patterns":["/tmp/*"],
 "metadata":{"filepath":"/tmp/e2e-outside.md","parentDir":"/tmp"}}
```

| 问题 | 答案 |
|---|---|
| 工具名在哪个字段、取值原文 | `permission`；实测取值 `"edit"`、`"bash"`、`"external_directory"` |
| 路径在哪个字段、绝对还是相对 | `metadata.filepath`（**小写 p，不是 `filePath`**），绝对路径。`patterns` 里是相对路径/通配摘要，不可作判据 |
| 一次请求是否可能带多个路径 | `edit` 只有一个 `filepath`。`external_directory` 有两形态：文件写入形态是 `{filepath, parentDir}`；bash 触发形态是 `{command, directories[], patterns[]}`，`directories` 可为多项 |
| 能否可靠提取路径 | **能**。`edit` 与 `external_directory` 文件形态取 `metadata.filepath`；`external_directory` bash 形态取 `metadata.directories` |

### 3.1 `external_directory: "ask"` 对绝对路径写入是否生效——**生效**

这是本次探针最重要的一条，它**推翻了 B27 对 opencode 的前提**。

在**生产配置**（`edit: "allow"`，`external_directory: "ask"`）下实测：

- 工作树内 `Write probe.md`：**不产生任何权限事件**，文件直接写成——这恰恰就是 B27 想要的 AutoAllow 结果。
- 工作树外 `Write /tmp/e2e-outside.md`：触发 `permission: "external_directory"`，**文件未被创建**，任务停在待答复。

也就是说 opencode 的越界写入**已经**被 `external_directory` 拦住了，`edit: "allow"` 放行的只是范围内的写入。

**对 Task 9 的修正**：

1. **删除 Step 4 与 Step 5**（`edit: "allow"` → `"ask"` 及其断言改动）。翻成 `ask` 只会给每一次范围内的正常编辑加一道空转的门——permgate 判完还是 AutoAllow，安全收益为零、噪音为正。`edit` 留在 `allow`。
2. Step 3 的结构提取保留，但主用例改用 `testdata/perm_external_directory_file.json`——那才是生产配置下真正会到达 handoff 的文件类事件。`perm_edit.json` 作为次要用例保留（若将来有人把 `edit` 翻成 `ask`，提取路径的代码已经就位）。
3. `Metadata` 结构体的路径键名确定为 `filepath`，并补 `parentDir` 与 `directories []string`。

## 4. 汇总：三个 adapter 都能可靠提取路径

spec §6.1 的条件性回退（「若某 adapter 提取不出路径则该 adapter 的 Write/Edit 保持 allow」）**不需要触发**，Task 10 也不需要为载荷缺口追加 backlog 条目。
