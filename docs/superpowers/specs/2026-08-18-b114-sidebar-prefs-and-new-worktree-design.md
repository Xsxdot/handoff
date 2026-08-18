# 控制台左栏：显示偏好菜单 + 机器行新建工作树

日期：2026-08-18
分支：`claude/project-settings-executor-create-7babb1`（基线 `handoff/web-console` @ `12c48fb0b`）
backlog：B114
状态：已评审通过，待实现

## 0. 这份 spec 解决什么

左栏树目前是「看」的界面，不是「管」的界面：

| # | 问题 | 现状 |
|---|------|------|
| 1 | 项目一多，左栏就是一条读不完的长列表，既不能藏掉不关心的项目，也不能按「谁在动」排序 | [`ProjectTree.tsx`](../../../web/src/app/tree/ProjectTree.tsx) 按后端返回顺序 `filtered.projects.map` 直出 |
| 2 | 跑过几轮任务的机器下堆着一排早就没人管的工作树，把正在干的活挤出视口 | 目录行只按 [`sortWorkspaces`](../../../web/src/app/tree/sortWorkspaces.ts) 排序，从不隐藏 |
| 3 | 想在某台执行机上开一个新工作树，只能去终端手敲 `git worktree add`，或者靠 `handoff dispatch --new-worktree` 顺带建 | agentd 没有「建工作树」这个接口，工作树只在 `PrepareWorkspace` 里作为派发的副产物出现 |

1 与 2 是同一件事的两个方向（左栏显示偏好），合成一个菜单；3 是一个独立入口加两个新接口。合成一份 spec 是因为两者都落在 `ProjectTree.tsx` 这一个文件上，分开做必然互相踩。

### 非目标

- **不做删除工作树**。那是右键菜单的另一条需求，掺进来会让验收面翻倍，且删除涉及脏树判定与二次确认，值得单独一期。
- **不做在新工作树里直接派发任务**。web 端至今没有 dispatch 表单（指令输入、执行器选择、权限模式……），那是另一份 spec 的量。建完树后照旧走 CLI 派发。
- **不动目录行既有的排序规则**（`sortWorkspaces` 的「主树恒第一 → 工单↓ → 任务↓ → 时间↓ → path↑」）。本期的排序菜单只管**项目层**。两层排序各管一段，混进一个开关里会互相解释不清。
- **不动 `GET /api/tasks/{id}/branches`**（审阅栏基准分支下拉的既有契约）。
- 不动看板弹层、工单弹层、设置页。
- 不做偏好的跨端同步（见 §1.6 的取舍）。

---

## 1. 左栏显示偏好

### 1.1 入口

「项目 N」那行的右端放一个 `SlidersHorizontal`（推子）图标按钮，**常驻显示**，不需要 hover。

**刻意不用齿轮**：左栏底部图标区已经有一个 `Settings` 齿轮通向设置页。两个齿轮在同一栏里会让人以为点哪个都一样，而它们一个是全局配置（开发机、Env 文件），一个是「这块屏幕想看到什么」的视图偏好——不是一类东西。

### 1.2 菜单内容

```
显示
  ☑ 隐藏无活跃任务的工作树
排序方式
  ● 活跃优先        ○ 名称        ○ 最近活动
项目 · 8
  全选   全不选
  ☑ tk
  ☑ super-debug
  ☑ ai-hub
  …（超过视口高度时菜单内部滚动）
```

- 三组之间用分组标题（不可点）分隔。
- 勾选项、单选项**点了不关菜单**——连着调三个开关是常态，每点一次就关掉等于逼人开三次。
- 「全选 / 全不选」是两个普通动作项，也不关菜单。
- 菜单最大高度 `min(60vh, 420px)`，超出内部滚动。项目多到几十个时不做二级面板与搜索（左栏本身就有搜索框，且项目数在这个工具的现实规模里是十几量级）。

### 1.3 隐藏无活跃任务的工作树

**判据**：该目录下没有任何 `running` / `waiting_answer` / `waiting_review` 任务。口径与 `RowCounts` 在目录行上显示的两个数完全一致（`tasksOfWorkspace` 归集，`work_dir` 为空归主目录），两处不会分叉。

刻意不把已结束任务算作「有任务」：这个开关要回答的是「哪些目录现在还在动」，历史堆积会让一个跑完 30 个任务的老分支永远留在屏幕上——那正是它要解决的问题。

**两条豁免，恒不隐藏**：

1. **主工作树**（`is_main`）。它是这个项目在这台机器上的家，不是一个任务分支；藏掉它，用户对「主目录在第一行」的肌肉记忆当场失效。
2. **当前选中的目录**（`selectedKey === base.key`）。选中态的行凭空消失是 bug 观感，而不是「界面变干净了」。

**隐掉的留口子**：该机器节点下、目录行之后、「已结束」分组之前，插一行

```
▸ 已隐藏 3 个目录
```

点开就地展开这 3 行（正常目录行的样式与行为），再点收起。展开状态是**组件内 state，不写进偏好**——它是一次性的「我现在想看看」，不是长期设定。

这一行的存在与项目一贯的「诚实展示、绝不静默少一条」（spec §8）同源：偏好可以让东西不占地方，但不能让人以为它不存在。

### 1.4 项目排序

三档单选：

| 档 | 排序键 | 说明 |
|----|--------|------|
| `active`（默认） | 活跃任务数 ↓ → 名称 ↑ | 「现在该看哪个」。活跃 = `running` + `waiting_answer` + `waiting_review`，直接复用 [`countsForProject`](../../../web/src/app/tree/counts.ts) 的 `running + pending`，不新算一套 |
| `name` | 名称 ↑ | `localeCompare`。想按字母找项目时用 |
| `recent` | 该项目下任务 `updated_at` 最大值 ↓ → 名称 ↑ | 「最近动过哪个」。一条任务都没有的项目视为最旧（`''`），排在末尾 |

三档末位都以名称升序兜底，理由与 `sortWorkspaces` 末位的 `path ↑` 一样：**稳定性**，不是排序意图。前键全等时若不给确定次序，行会随每次 2.5s 任务流心跳无缘无故重排。

默认取 `active` 而不是 `name`：左栏的本职是回答「我该看哪」，字母序是查找工具，而查找已经有搜索框了。

### 1.5 隐藏项目

菜单里未勾选的项目不在树上渲染。

**存的是「隐藏名单」（`hiddenProjects: string[]`，project_id），不是「显示名单」**：新登记的项目必须默认可见。存显示名单的话，刚登记完的项目在左栏找不到，看起来像登记失败——这是个真实且高频的踩坑面。

「项目 N」的 N 恒等于**这一刻真的渲染出来的项目行数**（先按搜索过滤、再按隐藏名单剔除），否则数字与能展开的行对不上。有项目被隐藏时，在它后面追加一段浅色说明：

```
项目  6 · 已隐藏 2
```

同样是不静默。隐藏只影响左栏树，**不影响看板与工单**——那两处有自己的 FilterBar，把左栏的视图偏好偷偷带过去会让筛选结果无法解释。

### 1.6 持久化

localStorage 单键 `handoff.tree.prefs`：

```ts
export interface TreePrefs {
  v: 1                                       // 版本号，将来改形状时用来判断要不要丢弃
  hideIdleWorktrees: boolean                 // 默认 false
  projectSort: 'active' | 'name' | 'recent'  // 默认 'active'
  hiddenProjects: string[]                   // project_id；默认空
}
```

- 读取时任何异常（键不存在、坏 JSON、字段类型不对、`v` 不是 1）一律**静默回退默认值**并覆写一份干净的。视图偏好读不出来的正确反应是「按默认显示」，不是报错打断。
- 不落 agentd 配置：这是「我这块屏幕想看到什么」，不是服务端事实。换浏览器要重设是可接受的代价，换来的是零后端改动、零多端并发覆盖问题。

### 1.7 搜索期间旁路

`query` 非空时，**两类隐藏偏好全部旁路**：隐藏的项目照常参与匹配与渲染，`hideIdleWorktrees` 不生效。搜到了却被偏好过滤掉，等于搜索坏了。

**排序偏好不旁路**——排序不会让东西消失，搜索期间跟着当前排序档反而更连贯。

这与现有 `expanded()` 在搜索期间旁路 `collapsed` 是同一条规矩：旁路不是清空，`query` 清空后偏好原样回来。

### 1.8 组件落点

| 文件 | 内容 |
|------|------|
| `web/src/app/tree/treePrefs.ts`（新） | `TreePrefs` 类型、默认值、`loadPrefs` / `savePrefs`、以及三个**纯函数**：`sortProjects`、`splitHiddenProjects`、`splitIdleWorkspaces` |
| `web/src/app/tree/TreePrefsMenu.tsx`（新） | 把 prefs + 树装配成 `IconMenu` 的 items，不含业务判断 |
| `web/src/app/lib/IconMenu.tsx`（改） | 扩展项类型，见下 |
| `web/src/app/tree/ProjectTree.tsx`（改） | 持有 prefs state、渲染菜单与「已隐藏 N」行 |

排序与拆分做成**纯函数 + metrics 回调**（与 `sortWorkspaces` 同一形状），测试可以用手写数字驱动，不必造整棵树加一批任务。

`IconMenu` 的扩展（**向后兼容，现有调用方一字不改**）：

```ts
export interface IconMenuItem {
  key: string
  label: string
  kind?: 'action' | 'check' | 'radio' | 'header'  // 缺省 = 'action'，与现有行为一致
  checked?: boolean        // check/radio 的选中态
  keepOpen?: boolean       // true = 选中后不关菜单
  icon?: ReactNode
  hotkey?: string
  onSelect?: () => void    // header 不需要
}
```

- `check` 选中显示 `Check` 图标，`radio` 选中显示实心小圆点，未选中都留等宽空位（行文字不能因为选中态左右跳）。
- `header` 不可点、不参与方向键遍历、不响应 hover 高亮。
- 菜单容器加 `max-h` 与 `overflow-y-auto`。

**为什么扩展 IconMenu 而不是新写一个弹层**：portal 到 body、点外部关闭、Esc 关闭、方向键遍历，以及那个「mousedown 时不能关，否则 click 永远不发生」的坑，它全趟过并且有测试兜着。重写一份等于把这些坑重新踩一遍。

---

## 2. 机器行的「新建工作树」入口

### 2.1 位置与既有约束

入口在**机器行**（`data-testid="machine-row"`）右端，**hover / focus-within 时才出现**的 `Plus` 图标按钮。

这里撞上一条既有结论。`ProjectTree.tsx` 机器行下方写着：

> 注销入口在右键菜单里，不在行内。行内 absolute 按钮与同一行右端的 RowCounts 抢位置——08-14 修过一次垂直居中（定位上下文从 578px 子树收进本行），但水平方向两者都要右端，改不出不重叠的排法

**本期的解法是让位，不是叠加**：hover 时给该行的 `RowCounts` 加 `invisible`（`group-hover:invisible group-focus-within:invisible`），`+` 按钮 absolute 浮在同一位置。两者视觉互斥，物理上永不重叠。

- 用 `invisible` 而不是 `hidden`：它保留占位，行内其它元素不会因为 hover 而左右位移。
- 按钮必须是机器行 `<button>` 的**兄弟节点**（挂在 `group relative` 容器里），不能是子节点——button 套 button 是非法 HTML，这也正是 `Arrow` 用 `span` 的原因。
- `problem !== ''`（机器不可达）时不渲染这个按钮：机器都断开了，建树必然失败，给一个必然报错的入口是骗人。

hover 按钮对键盘与触屏不友好，所以**右键菜单里同时加一项「新建工作树」**，走完全相同的弹层。右键菜单已经是 `Shift+F10` / ContextMenu 键可达的既有通道，不需要再发明快捷键。

### 2.2 弹层

新组件 `web/src/app/tree/NewWorktreeDialog.tsx`：

```
新建工作树
项目 tk · 机器 mac-02

  (•) 新建分支    分支名 [feat/sidebar-prefs        ]
                  基线   [ main                   ▾ ]
  ( ) 检出已有分支        [ feat/b105-task-attribution ▾ ]
                          （已被别的工作树占用的选项置灰并标出占用者）

将建在 /Users/x/.handoff/worktrees/manual 下，目录名按分支名生成
                                          [取消]  [创建]
```

- 打开时拉一次 `GET /api/projects/{name}/branches?machine=`。加载中禁用「创建」；拉取失败在弹层里显示原文并给「重试」。
- 「新建分支」模式：分支名必填；基线下拉默认选中响应里的 `default`。
- 「检出已有分支」模式：下拉列出全部本地分支，`worktree !== ''` 的项 `disabled` 并在标签后缀写「已被 `<路径>` 占用」——**列出但不可选**，直接不列会让人以为分支丢了。
- 落点只回显**根目录**（响应里的 `worktree_root`），不在前端拼出完整路径：目录名由后端按分支名生成，前端复刻一份生成规则就是等着两边分叉。
- 提交中禁用两个按钮；失败把 agentd 报错**原文**贴在弹层里，不缩略成「操作失败」（spec §10）。
- 成功：关闭弹层 → 刷新项目树 → **把新目录选为当前基准目录**。

弹层由 `ProjectTree` 自己持有并渲染（与注销确认弹层同层），成功后回调 `onWorktreeCreated(project, machine, workspace)`，由 `Shell` 做 `treeState.refresh()` + `wb.select(workspaceBase(...))`。树只管发生了什么，选中与刷新是 Shell 的职责——与 `onUnregister` / `onEdit` 的既有分工一致。

---

## 3. 后端

### 3.1 `GET /api/projects/{name}/branches?machine=`

列出该项目位置的本地分支，供弹层的两个下拉用。

```go
// ProjectBranch 是一个本地分支，带「是否已被工作树占用」。
type ProjectBranch struct {
    Name string `json:"name"`
    // Worktree 是已检出该分支的工作树路径；空串 = 没有任何工作树占用它。
    // git 不允许同一分支被两个工作树同时检出，占用者就是「这个分支现在不能再开树」的原因。
    Worktree string `json:"worktree"`
}

// ProjectBranchesResp 是 GET /api/projects/{name}/branches 的响应。
type ProjectBranchesResp struct {
    Branches []ProjectBranch `json:"branches"`   // 永不为 nil
    Default  string          `json:"default"`    // 推导出的基准分支；推导不出为空串
    // WorktreeRoot 是手工新建工作树的落点根目录，供界面如实回显「会建在哪」。
    WorktreeRoot string `json:"worktree_root"`
}
```

- 分支名与 `default` 复用既有的 `Branches(repo)` 与 `resolveBaseBranch(repo)`，不另写一套 git 调用。
- 占用信息复用 `probeWorkspaces(ctx, repoPath, managedRoot)`：它已经给出每个工作树的 `Path` 与 `Branch`，反向建成 `branch → path` 映射即可，不多跑一次 `git worktree list`。
- 顶层形状（`branches` + `default`）与 `/api/tasks/{id}/branches` 一致，但 `branches` 是**对象数组**而非字符串数组——多了占用信息，因此**不与前端既有的 `BranchesResult` 共用类型**。
- 项目不存在（`store.ErrNotFound`）→ 404 带原文；`machine` 指名别的机器 → `forwardIfRequested` 原样转发。

### 3.2 `POST /api/projects/{name}/worktrees?machine=`

```go
// CreateWorktreeReq 是 POST /api/projects/{name}/worktrees 的请求体。
type CreateWorktreeReq struct {
    // Mode 二选一："new_branch"（建新分支并开树）/ "existing_branch"（把已有分支开成一棵树）。
    Mode   string `json:"mode"`
    Branch string `json:"branch"`
    // Base 是新分支的起点，仅 new_branch 模式有意义；空串时由 resolveBaseBranch 推导。
    Base   string `json:"base"`
}
```

响应 200：`proto.Workspace`（与项目树上那一条**完全同一口径**，含 `head` / `created_at` / `managed`）。前端拿到就能直接组装 `BaseDir` 选中，不必等下一轮树刷新。

### 3.3 落盘位置

```
<DataDir>/worktrees/manual/<分支名安全化>
```

与任务自建树（`<DataDir>/worktrees/<id8>`）同区不同层，路径形状本身就能区分谁是谁。

**分支名安全化**：`/` 换成 `-`，其余原样保留（分支名已过 `git check-ref-format`，不含空格与控制符）。因此 `feat/x` 与 `feat-x` 会撞同一个目录名——**撞了就拒**，报「落点 `<path>` 已存在」，不自动加数字后缀。自动改名会让用户以为建在了他以为的位置。

**`managed` 标记的语义副作用**：`probeWorkspaces` 按「路径落在 `<DataDir>/worktrees` 下」判 `managed`，手工树因此也会被标 `managed: true`。

**判据不改**，只订正 `proto.Workspace.Managed` 的注释为「agentd 数据区下的工作树（任务自建或手工新建）」。理由：左栏当前不读这个字段，回收（`reclaim`）只认终态任务的记录、从不扫目录，所以**没有任何行为受影响**；为一个没有消费者的标记加一层特例，是给将来的人留一个要读三处代码才懂的例外。

### 3.4 实现与校验

新文件 `internal/agentd/manualworktree.go`（**不塞进已经 2269 行的 `workspace.go`**）：

```go
// CreateManualWorktree 在 worktreesDir/manual 下开一棵不属于任何任务的工作树。
func CreateManualWorktree(ctx context.Context, repo, worktreesDir string, req proto.CreateWorktreeReq) (proto.Workspace, error)
```

校验顺序（每条失败都返回可判别的错误，报文含具体值）：

1. `mode` 必须是两个值之一；`branch` 非空。
2. `branch` / `base` 不以 `-` 开头（git 参数注入面，与 `PrepareWorkspace` 同一条防线）；`git check-ref-format --branch <值>` 通过。
3. `new_branch`：分支**不得已存在**（`rev-parse --verify --quiet refs/heads/<b>` 有输出即存在）；`base` 为空则 `resolveBaseBranch(repo)`，仍为空则拒（说清「推导不出基准分支，请显式指定」）；`base` 必须能 `rev-parse --verify` 通过。
4. `existing_branch`：分支**必须已存在**；且未被其它工作树占用（先查 `probeWorkspaces` 给人话，git 自己那层拒绝是兜底）。
5. 落点目录不得已存在。
6. `MkdirAll(<worktreesDir>/manual, 0o700)`。

执行：

- `new_branch`：`git worktree add -b <branch> <dir> <base>`
- `existing_branch`：`git worktree add <dir> <branch>`
- 超时用既有的 `WorkspaceGitTimeout`。
- 失败时 best-effort 清理：`os.Remove(dir)`（**只删空目录，绝不用 `RemoveAll`**）；删不掉只 Warn，不掩盖原始错误。

回读：成功后调一次 `probeWorkspaces(ctx, repo, managedRoot)`，按 `canonPath` 归一后挑出 `Path == dir` 的那条返回——与树上一模一样的口径（macOS `/var → /private/var` 由 `canonPath` 吸收）。挑不到（并发被人删掉之类）时退回手工组装 `proto.Workspace{Path, Branch, Managed: true}` 并 Warn，不因为回读失败就把一次成功的建树报成失败。

HTTP 层（`server.go` 注册两条路由，handler 放 `projectadmin.go`）：

- 一律 `forwardIfRequested` 打头，与 `handleProjectPatch` 同形。
- 请求体解析走 `io.LimitReader(r.Body, 1<<20)`。
- 项目不存在 404；参数类拒绝 400；git 执行失败 500，**stderr 原文经 `truncateRunes(err.Error(), 200)` 透出**。

---

## 4. 契约同步

新增 proto 类型必须三处同步，缺一处对应侧的契约测试就红：

1. `internal/proto/projects.go`：`ProjectBranch` / `ProjectBranchesResp` / `CreateWorktreeReq`
2. `web/src/api/types.ts`：同名 TS 接口
3. `web/src/api/testdata/*.json`：把 `ProjectBranchesResp` 与 `CreateWorktreeReq` 加进 `internal/proto/contract_fixture_test.go` 的 `cases`，用 `go test ./internal/proto/ -run TestContractFixtures -update` 生成，**不手写**

`web/src/api/client.ts` 新增两个函数，`machine` 参数的处理与 `patchProject` / `deleteProject` 完全一致（空串= 本机、不带该查询参数）。

---

## 5. 日志

Go 侧（`logger` 风格与 `handleWorkspaceEntryCreate` 对齐，绝不 `fmt.Printf`）：

| 时机 | 级别 | 字段 |
|------|------|------|
| 建树请求进入 | Info | `name` `machine` `mode` `branch` `base` |
| 每一条校验拒绝 | Warn | 上述 + `status` + `cause`（拒绝的具体理由） |
| `git worktree add` 失败 | Error | 上述 + `dir` + `cause`（含 stderr） |
| 建树完成 | Info | `name` `dir` `branch` `managed` `elapsed_ms` |
| 回读挑不到新树 | Warn | `dir` + 原因 |
| 清理空目录失败 | Warn | `dir` + `cause` |
| 列分支请求/完成 | Info | `name` `machine` / `count` `default` |

前端：偏好读取失败走 `console.warn` 一次（带被丢弃的原文前 200 字），不静默吞——坏偏好是真实排查线索。

---

## 6. 测试

**Go（`internal/agentd/manualworktree_test.go`，真起临时仓库）**

- `new_branch` 成功：目录存在、分支存在、`git worktree list` 里有它
- `existing_branch` 成功
- 拒绝六条：分支已存在（new）/ 分支不存在（existing）/ 分支已被别的树占用 / 落点已存在 / `-` 开头 / 非法 ref 名
- `base` 为空时走 `resolveBaseBranch` 推导
- handler 层：项目不存在 → 404；请求体坏 → 400

**前端（vitest）**

- `treePrefs.test.ts`：默认值、坏 JSON 回退、三档排序（含末位名称兜底）、隐藏项目拆分、空闲目录拆分（含主树/选中目录两条豁免）
- `IconMenu.test.tsx` 增补：`check`/`radio` 选中态渲染、`keepOpen` 不关菜单、`header` 不可点、现有 action 行为不变
- `ProjectTree.test.tsx` 增补：菜单开关联动树、「已隐藏 N」行展开收起、hover 时 `RowCounts` 让位且 `+` 出现、不可达机器不渲染 `+`、搜索期间隐藏偏好旁路
- `NewWorktreeDialog.test.tsx`：两模式切换、占用分支置灰、错误原文透出、成功回调带回 workspace

---

## 7. 验收

**可自动验（执行者必须亲自跑到结果，不许写没跑过的结论）**

1. `gofmt -l .` 无输出
2. `go build ./... && go vet ./... && go test ./...` 全绿
3. `cd web && npx tsc -b && npx eslint . && npx vitest run && npm run build` 全绿
4. 新增用例数相对基线有增长（在 ledger 里写明基线数与新数）

**需肉眼看页面（执行者无浏览器，如实标「未验」，不许猜通过）**

5. 推子菜单三组开关的实际观感与联动
6. hover 机器行时 `RowCounts` 让位、`+` 不与任何东西重叠
7. 建树弹层的排版与错误态

---

## 8. 已知边界

- 手工建的工作树**没有任何自动回收**：`reclaim` 只处理终态任务的 managed 树，本期又不做删除入口。用户要清理只能去终端 `git worktree remove`。这是本期的自觉取舍，删除入口留待下一期。
- 分支名安全化会让 `feat/x` 与 `feat-x` 抢同一个目录名，撞了直接拒（§3.3）。
- 偏好只在当前浏览器生效（§1.6）。
