# 项目位置模型（B62）

> **文件名说明**：本文原题「仓库登记归一化」，范围在 08-11 的设计讨论中被彻底重划。
> 文件名保持不变，只为让 backlog B62 那行的 spec 链接不断。
>
> **定位**：W3a（项目与机器控制面）的**前置**。W3a 要在 Web 控制台里画一棵
> 「这台机器上有哪些项目」的树；本文把这棵树的数据源从「半真的目录清单」
> 变成「可信的项目位置表」，并顺手把派发的入参收敛干净。
>
> **分支**：`handoff/b62-repo-registration`，基于 `main`。做完合进 `handoff/web-console`，
> 且必须在 W3 开工前合并——W3a 的实现直接依赖本文确立的不变式。
>
> **上游决策**：ADR-0008「项目至少有一个位置且最多绑定一台远程开发机」（已接受）。
> 本文实现它，并修订它的一处措辞（见 §9）。

---

## 1. 病灶

### 1.1 症状

`handoff repo ls` 在本机与 devbox 上**都返回空**，而两台机器加起来跑过几十个任务。
登记表不是因为没干活才空，是因为**干活根本不经过它**。

直接原因在 `resolveRepoInput`（[internal/agentd/reporegistry.go:150](../../../internal/agentd/reporegistry.go:150)）：

```go
if looksLikePath(input) {
	log().Info("仓库解析：按路径直通", "input", input)
	return input, nil          // 完全不看 entries
}
```

只要 `--repo` 里含 `/`、`\`、`:` 之一，就绕开登记表原样返回。注释写着「保持今天的行为不变」——
这是 B46 引入登记表时留的向后兼容口子，而它吞掉了登记表的全部实际流量。

### 1.2 根因不是这个分支，是 `--repo` 本身

把路径直通改成路径查表，能让表不再是空的，但会立刻长出一串新问题：路径要不要
`filepath.Clean`、要不要绝对化、`/tmp` 与 `/private/tmp` 这类 symlink 别名认不认、
路径敲错但恰好指到另一个真实仓库怎么办、拒绝报文要列名字还是列路径。

这些问题有一个共同的源头：**`--repo <路径>` 让调用方去描述「代码在那台机器上的哪个目录」，
而那是那台机器自己的私事。**

派发在本质上只需要两个输入：

- **哪个项目**——由 git origin 唯一确定，与机器无关；
- **哪台机器**——`--target`，省略即本机。

「项目在这台机器上放在哪」是执行机的 agentd 才知道、也只有它需要知道的事。
调用方一旦被要求提供它，就必然要么猜、要么记、要么在多台机器间维护一份影子清单——
上面那一串问题全是从这个漏洞派生出来的。

**因此本文的做法不是把路径查表做对，是让路径不再出现在派发命令上。**

### 1.3 代价

在修好之前，这个漏洞有三个具体代价：

1. **W3a 的项目树没有数据源。** 树要显示「这台机器上有哪些项目」，唯一的持久化来源是
   登记表；表是空的，树就是空的。
2. **跨机对话没有共同名词。** 多台执行机之间引用同一个项目需要一个各机都算得出的稳定标识；
   标识只能从 origin 派生，而直通派发的仓库连 origin 都没被读过。
3. **登记表是半真的，比全假更坏。** `repo ls` 显示的是「登记过的」，不是「用过的」，
   两者今天可以毫无交集。任何基于它做的判断都是错的。

---

## 2. 模型

三个名词，边界清楚：

| 名词 | 是什么 | 标识 |
|---|---|---|
| **项目（project）** | 一份代码的逻辑身份，与机器无关 | `project_id = sha256(normalizeGitURL(origin_url))[:16]` |
| **位置（location）** | 项目在**某一台**机器上的工作副本 | 该机器上的绝对路径 |
| **机器（machine）** | 本机，或 `--target` 指定的一台执行机 | target 名（本机无名） |

`project_id` 是**纯函数**：每台机器各算各的，同一个 origin 必然得到同一个值。
跨机引用因此不需要任何协调，也不需要派发前多跑一轮读 ID。

ADR-0008 定：一个项目在一台机器上**最多一个位置**（可以只有本机、只有远程、或两者各一）。
于是每台机器的位置表是一张 `project_id → path` 的映射，一行一个项目。

---

## 3. 命令形态

### 3.1 登记

```
handoff project add [名字]                                              # 把 cwd 登记为本机位置
handoff project add [名字] --target devbox                              # 本机(cwd) 与 devbox 一起登记，devbox 自动 clone
handoff project add [名字] --target devbox --path /root/work/handoff    # 同上，但 devbox 上已有一份，用它
handoff project ls  [--target devbox]
handoff project rm  <名字> [--target devbox]
```

规则：

- **项目身份永远不用填**，从 cwd 的 origin 算（cwd 不是 git 仓库或没有 origin 则拒）。
- **本机位置的路径永远不用填**，就是 cwd（归并到主工作树，见 §5）。
- `[名字]` 可省，省略时由 origin 末段派生；它只是人可读引用，不参与身份判定。
- **`--target` 表示「本机与那台机器一起登记」，不是「只登记那台机器」。** 一条命令发两跳请求
  （本机 agentd + 目标机 agentd），任一跳失败都报出来、不回滚另一跳（登记是幂等的，重跑即可）。
  之所以不提供「只登远程」的形态：项目身份是从 cwd 算的，cwd 既然是这个项目，本机位置就是已知且免费的，
  刻意不登记它只会让本机的项目树缺一行。
- **远程位置的路径可选**：
  - 省略 → 让那台机器 clone 到它自己的 `repo_root/<名字>`；
  - 给了 → 用那个已有目录，且 agentd **现读该目录的 origin 校验与请求一致**，不一致则拒。
    这挡住「路径敲错但恰好指到另一个真实仓库」——自动化最容易造出的脏登记。
- `--target` 只接受**一个**执行机名（ADR-0008：最多绑定一台远程）。

`--path` 只在登记时出现，且只在「这台机器上已经有一份克隆」时才需要。

### 3.2 派发

```
handoff dispatch --plan x.md                                  # 派到本机，项目 = cwd
handoff dispatch --plan x.md --target devbox                  # 派到 devbox，项目 = cwd
handoff dispatch --plan x.md --project nova --target devbox   # 跨项目
```

`--repo` **删除**。`--project <名字>` 是可选逃生口，只服务两种场景：cwd 不是目标项目，
以及 Web 控制台（它没有 cwd，从项目树里选）。

**cwd 不是 git 仓库、或没有 origin，且未给 `--project`** → CLI 在本地就拒，不发请求，
报文说明「派发的项目由当前目录识别；请在项目目录内执行，或用 `--project <名字>` 指定」。
这一条在 CLI 侧判是因为它只依赖本机信息，多跑一次网络毫无意义。

其余 flag（`--base`、`--worktree`、`--new-branch`、`--allow-dirty`、`--no-sync-check` 等）行为不变。

---

## 4. 数据模型

### 4.1 新表

```sql
CREATE TABLE project_locations (
  project_id TEXT PRIMARY KEY,        -- sha256(normalizeGitURL(origin_url))[:16]
  name       TEXT NOT NULL UNIQUE,    -- 人可读引用，由 origin 末段派生，冲突时 name-2
  path       TEXT NOT NULL UNIQUE,    -- 本机绝对路径（登记时 Abs+Clean）
  origin_url TEXT NOT NULL,           -- agentd 在本机现读的权威值
  created_at TIMESTAMP NOT NULL)
```

**`project_id` 做主键，ADR-0008 的「一台机器上一个项目最多一个位置」由主键直接强制**——
不需要额外的唯一索引，也不需要在应用层再校验一遍。

`path` 保留 UNIQUE：两个不同项目不能声称在同一个目录。

`name` 仍然唯一，因为 `--project <名字>` 与 `project rm <名字>` 要靠它引用。

### 4.2 迁移

旧 `repos` 表（`name` 主键 / `path` UNIQUE / `origin_url` 无约束）逐行迁入新表：

```
for r in SELECT * FROM repos ORDER BY created_at ASC:
    pid := projectID(r.origin_url)
    if 新表已有 pid:  跳过，Warn 记 name/path/origin 三项完整信息
    else:             INSERT（path 做 Abs+Clean）
DROP TABLE repos
```

**同 origin 多行时保留 `created_at` 最早的一条**，其余丢弃并在启动日志里打出完整信息，
人照着 `handoff project add --path <那个路径>` 自己补。

> **为什么敢直接 DROP**：本机与 devbox 的 `repos` 表**实测均为空**（§1.1）。
> 这段迁移在现实中是空跑，写它只为让任意第三台机器上的旧库也能平滑升级。
> 也正因为表是空的，改表名、换主键在此刻是零成本的——过了这次就不再便宜。

### 4.3 不做历史回填

原方案有一节「把 `tasks.repo_path` 回填成登记」。**删掉。**

自动登记（§6）让它变成「下次派发时自愈」：目标机器上没有这个项目的位置，下一次派发就会补上。
回填换来的只是「W3a 项目树在你下次派发之前就是全的」，而它的成本是一段一次性代码、一张 `meta` 标记表，
外加「路径已移走」「同 origin 多条选哪条」「`rm` 掉的不能复活」一堆边角与对应测试。账算不过来。

---

## 5. 主工作树归并

cwd 落在 git worktree 里（本仓库当前有 13 个）时，用
`git rev-parse --git-common-dir` 找到主工作树，**项目位置永远登记主仓**。

这是**行为变更**：今天在 worktree 里派发会拿那个 worktree 当仓库，此后不会了。
想接着当前分支干，用 `--base <分支>` 显式表达——那本来就是它的用途，而
「cwd 恰好在某个 worktree 里」是一种隐式表达，隐式表达在自动化面前不可靠。

不归并的代价是直接的：worktree 与主仓 origin 相同，各登一条就撞 `project_id` 主键；
若改为允许，同一个项目在本机会有十几个位置，项目树彻底没法看，ADR-0008 也就没了。

---

## 6. 自动登记

### 6.1 两跳

派发是两跳，两跳都可能缺位置：

```
agent ──①──> 本机 handoff CLI ──②──> 目标机 agentd
```

① 由 CLI 在本地解决（算 origin → 归并主工作树 → 算 project_id），不需要网络。
② 是真正可能缺位置的一跳。

### 6.2 编排在 CLI 侧

```
CLI: POST /api/tasks { project_id, ... }
     ← 400 ErrProjectNotRegistered
CLI: POST /api/projects { origin_url, path?: <仅本机，为 cwd 主工作树> }
     ← 201 { project_id, name, path }
CLI: POST /api/tasks { project_id, ... }        # 重发，成功
```

- **自动登记走的就是 `project add` 那条路，语义完全一致**（§3.1）：既补目标机，也补本机。
  本机送 `path = cwd 的主工作树`、不 clone；远程**不送 path**，由那台机器 clone 到自己的
  `repo_root/<名字>`——本机因此一个远程细节都不需要知道，这正是新执行机 bootstrap 的路径。
- 之所以顺带补本机（哪怕派发目标是远程）：本机位置是免费且已知的，不补它 W3a 的项目树就会缺行；
  而让自动登记与手动登记行为一致，能少掉一条需要记住的规则。
- 自动登记本身失败（如 clone 失败、目录被占）→ **透出 agentd 原文，不重试、不降级**。

> **为什么编排放 CLI 侧而不是 agentd 的 dispatch 里**：agentd 侧收编等于把
> 「必须先登记才能派发」从服务端拿掉，W3a 之后就没有这条不变式可依赖了。
> 放 CLI 侧则契约干净——agentd 永远只接受已登记的项目，CLI 只是把两条命令的编排自动化了。
>
> 代价是 Web 控制台（W2/W3）走 API 直连，享受不到这层编排。那一侧将来是一个
> 「登记并重试」按钮，而 Web 上派发本来就是人在点，多一次确认不是负担。

### 6.3 派发被拒时零副作用

项目解析是 dispatch 的第一道闸（[manager.go:423](../../../internal/agentd/manager.go:423)），
早于建任务目录、早于 `ResolveBaseline`、早于 `PrepareWorkspace`、早于 executor 启动。
「先发再被拒再重发」的全部代价就是一次 HTTP 400，没有任何要清理的残留。

这是「不预检、靠拒绝」成立的前提，也是不在 CLI 侧复制一份服务端规则的理由：
预检还会有 TOCTOU（查完到派发之间可以 `project rm`），服务端照样得判。

---

## 7. `repo_root` 默认值

`repo_root` 今天默认为空，空则 `--clone` 必须显式给路径（[config.go:44](../../../internal/config/config.go:44)、
[init.go:143](../../../cmd/init.go:143)）。自动登记把 clone 变成了主路径，所以它必须有默认值：

- 默认 `<DataDir>/repos`（即 `~/.handoff/repos`）；
- 在配置**解码之后、`validate` 之前**补默认，并随首次写盘落到 `config.yaml` 里，
  让人看得见，而不是藏在使用点。落盘之后就固定，此后改 `datadir` 不会静默改落点。

---

## 8. API 与类型变更

| 今天 | 之后 |
|---|---|
| `POST /api/repos`、`GET /api/repos`、`DELETE /api/repos/{name}` | `/api/projects` 同三条 |
| `proto.Repo{Name, Path, OriginURL, ...}` | `proto.ProjectLocation{ProjectID, Name, Path, OriginURL, ...}` |
| dispatch 请求字段 `repo`（路径/名字/空三态）、`origin_url` | `project_id` 或 `project_name`（二选一，都空则 400）；`origin_url` 删除 |
| `ErrRepoNotRegistered`、`ErrRepoAmbiguous` | `ErrProjectNotRegistered`（`Ambiguous` 随三态解析一并删除） |

dispatch 不再接收 `origin_url`：agentd 拿 `project_id` 查表即可，不需要信任调用方送来的 URL 字符串。
origin 只在**登记**请求里出现，且 agentd 会现读目标路径的 origin 覆盖/校验它。

`tasks.repo_path` 保留不动（历史数据 + executor 的 cwd 来源）。**不给 tasks 加 `project_id`**——
W3a 若要按项目筛任务再议。

> **并行冲突提示**：本文改 `internal/proto/`，而它是 `handoff/web-console` 上
> W1 契约 fixture 的来源（Web 控制台总方案 §6 定它由审核者独占）。
> B62 合进 web-console 时必须重跑 fixture 更新开关并提交，否则两侧测试同时变红。

---

## 9. ADR-0008 补一笔

ADR-0008 原文写「远程目录只接受明确路径」。本文修订为：

> 远程位置的目录**可选**；省略时由该执行机 clone 到它自己的 `repo_root/<名字>`。

修订理由：要求调用方提供远程路径，等于要求它知道另一台机器的私事，而新执行机的
第一次登记根本没有路径可给。已接受的决策不能默默改，故在 ADR-0008 上追加一节修订记录。

---

## 10. 影响面

**这是一次彻底的破坏性变更，不留兼容层。** 明确列出：

| 影响 | 说明 | 处置 |
|---|---|---|
| `--repo` 删除 | 所有带 `--repo` 的派发命令报未知 flag | 报错文案直接指向新形态：「项目由当前目录自动识别；跨项目用 `--project <名字>`」 |
| `repo` 子命令改名 `project` | `repo add/ls/rm` 不再存在 | 不留别名。留别名等于让旧心智继续繁殖，而这次的目的就是收敛 |
| 在 worktree 里派发的语义变了 | 项目位置归并主仓，不再拿 worktree 当仓库 | §5；想接着分支干用 `--base` |
| 首次派发到某机器会多一轮往返 | 自动登记 + 重发 | 对调用方透明；远程首次含一次全量 clone，日志里打出耗时 |
| 既有测试大面积要改 | 4 个测试文件带路径派发（`manager_test`/`workspace_test`/`approver_test`/`integration_test`），共 56 处 `Repo:` 赋值 | 加 `registerTestProject(t, env, path)` 助手，在建任务前先登记。**不给测试开旁路** |
| 文档与 skill 全部过时 | `README.md`、`skills/handoff` 里的 `--repo <路径>` 示例 | 同一次改掉。不改的话 agent 会照旧示例继续写路径 |

**不提供 `--allow-unregistered` 之类的逃生口。** 那等于把刚堵上的洞重新开一个带名字的版本，
半年后所有脚本都会带上它。逃生口就是 `project add`（一条命令，多数情况下零参数）。

---

## 11. 交付给 W3a 的不变式

做完之后，W3a 可以直接依赖这四条：

1. **凡是 agentd 派发过的项目，都在 `project_locations` 表里**——由「未登记就 400」在服务端单方面保证，
   没有任何旁路。
2. **一台机器上一个项目最多一行**——由 `project_id` 主键强制，等价于 ADR-0008。
   项目树可以直接 `SELECT`，不需要去重。
3. **每条位置都有非空 origin**（沿用 `repoOriginURL` 的既有规矩：没有 origin 的仓库拒绝登记）。
4. **`project_id` 跨机一致且可离线计算**，因此「同一个项目在本机和 devbox 上」这件事
   不需要任何中心服务或协调协议就能判定——W3 的跨机汇总据此展开。

---

## 12. 测试

**`projectID` 折叠**（纯函数，表驱动穷举）：`git@github.com:x/y.git`、`https://github.com/x/y`、
`https://github.com/x/y.git`、带尾斜杠、大小写差异 → 必须折叠成同一个 ID；不同仓库 → 必须不同。

**主工作树归并**：主仓 → 返回自身；linked worktree → 返回主仓；非 git 目录 → 报错且报文可读。

**项目解析**（替代原 `resolveRepoInput` 的表驱动用例）：

| 输入 | 表状态 | 期望 |
|---|---|---|
| `project_id` 命中 | 有 | 返回该行 `path` |
| `project_id` 未命中 | 有若干 | `ErrProjectNotRegistered`，报文列已登记的 `name → path` 两列 |
| `project_id` 未命中 | 表为空 | 同上，报文含「本机尚无任何项目」 |
| `project_name` 命中 / 未命中 | — | 同上两种 |
| 两者都空 | — | 400 参数错误 |

**登记**：
- 本机零参数 → 登记 cwd 主工作树；
- 远程不给 path → clone 到 `repo_root/<名字>`，`repo_root` 未配时用默认值；
- 远程给 path 且 origin 一致 → 登记；**origin 不一致 → 拒**；
- 同一项目重复登记 → 主键冲突，报文指向已有位置；
- 名字冲突 → 落到 `name-2`。

**自动登记编排**（CLI 层）：未登记 → 触发登记 → 重发成功；登记失败 → 透出原文、**不重试**、
dispatch 整体失败。

**迁移**：旧表有行 → 迁入并算出 `project_id`；同 origin 多行 → 保留最早、其余 Warn 出完整信息；
空表 → 空跑；跑第二次 → 旧表已 DROP，无操作。

**端到端**：未登记项目 dispatch → CLI 自动登记 → 任务建成；`project rm` 后再派 → 再次自动登记。

`go build ./...`、`go test ./...`、`go vet` 全绿是底线。

---

## 13. 明确不做

- **不建 `projects` 表**。`project_locations` 的一行就是一个 location，项目身份是 `project_id`
  这个派生值，没有需要独立持久化的项目属性。真需要了（项目别名、图标、归档标记）是 W3a 的事。
- **不做 workspace / worktree 探测**——W3a 的事，且已定为现场探测、不落库。
- **不给 `tasks` 加 `project_id`**——见 §8。
- **不做机器范围维度**（`--target all` 之类）。与 `handoff upgrade` 不同，派发必须有唯一目标；
  一个项目大概率只在一台开发机上做，要两台就显式一台一台登记。
- **不保留任何 `--repo` / `repo` 子命令的别名**——见 §10。

---

## 14. 风险与回滚

**风险一：某台执行机上的项目目录已被移走或改名。** 迁移时该行仍会迁入（迁移不探测文件系统），
下一次派发在 `EnsureRepoUsable` 处报「路径不存在」。处置是 `project rm` 后重新 `project add`，
报文会指出这条路。

**风险二：自动 clone 在慢网络或大仓库上耗时长。** 首次派发会因此显著变慢。缓解是把
clone 耗时打进日志与 CLI 输出，让人知道在等什么；不做超时中断——中断一个进行到一半的
clone 只会留下半个目录。

**风险三：`--repo` 删除后，未及更新的外部脚本全部失败。** 这是刻意的：失败是显式的
未知 flag 报错，比静默走到错误仓库好。仓库内的调用点（README、`skills/handoff`、测试）在本轮一并改完。

**回滚**：改动集中在 `internal/agentd/reporegistry.go`（重写）、`repoadmin.go`、
`internal/store/`（新表 + 迁移）、`cmd/repo.go`→`cmd/project.go`、`cmd/dispatch.go`。
revert 即恢复旧行为；已迁入 `project_locations` 的数据不会自动回到 `repos`，
但那些行本来就该在，重新 `repo add` 一次即可。
