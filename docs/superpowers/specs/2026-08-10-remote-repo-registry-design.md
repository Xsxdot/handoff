# 执行机仓库登记表设计（B46）

> 状态：设计定稿，待转 writing-plans
> 来源：B46「远程执行机上的仓库首次落地要人工 ssh clone 再回填 `--repo`」

## 1. 要解决的问题

把一个本地项目**第一次**派到某台执行机上时，审核者必须：ssh 上去挑一个位置 → `git clone` → 记住路径 → 回到本地把这个路径填进 `--repo`。换一台执行机或换一个项目就重来一遍。handoff 全程不参与，也不记得任何仓库。

现状的三个结构性事实：

- `internal/config/config.go` 的 `Target{Addr, Token, User}` 没有任何仓库字段——handoff 不记得任何仓库。
- `cmd/dispatch.go:83` 硬要求 `--repo`，且它指的是**执行机上已存在的路径**。
- `EnsureRepoUsable`（`internal/agentd/workspace.go:357`，B45 引入）只做 `git rev-parse --git-dir` 校验，不负责创建。

但把仓库送上去需要的两样东西 handoff 手里都有：ssh 通道（`attach`/`pull` 走 `sshHostFromTarget(Target) + RepoPath`）与 git 同步能力（agentd 在基线缺失时会自己 `git fetch --all --prune`）。

## 2. 已决策的取舍

以下四条是设计讨论中定下的，实现阶段不要重新推导。

### 2.1 clone 由 agentd 执行，不是 CLI 走 ssh

- agentd 本就在执行机上跑 git（`gitRun`），并且**已经会自己 `git fetch --all --prune`**——clone 复用同一条凭据路径，不是新机制。
- 不依赖 sshd。B45 的实测场景就是一台没开 sshd 的 Windows；走 ssh 的方案在那台机器上根本不存在。
- 失败按 B45 立好的模式归类：包 `ErrRepoUnusable` → 400 带 git 原文，而不是扁平 500。

### 2.2 落地是显式命令，不是 dispatch 的隐式副作用

「把项目落到某台执行机上」与「派发一个任务」频率差两个数量级。塞进 dispatch 意味着一次手滑的 `--target` 就在远程机器上凭空造出目录；做成显式命令，clone 失败在命令当场炸，而不是埋在一次派发里。

### 2.3 用登记表，不用「约定根目录 + 路径推导」

曾考虑过的替代方案是：执行机配一个 `repo_root`，派发时由仓库名推导出路径，不存在就 clone，零新增状态。**否决的决定性理由：执行机上可能已经有这个仓库了，只是不在推导出来的那个路径上。** 推导对既有克隆完全是盲的，会在 `repo_root/<name>` 再克隆一份，造成同一台机器上两份同源仓库——最难查的那类状态分裂。

`repo_root` 这个配置项仍然保留，但语义变了：它是 `repo add --clone` 的**默认落点**，不是派发时的推导规则。

### 2.4 登记表放执行机 agentd 的 SQLite

不放审核者本地配置。理由是 handoff 的架构主线——agentd 持有全部状态、审核者随时可换机接管。放本地配置的话，换机接管时登记表整个消失。

## 3. 数据模型

`internal/store` 现有 `tasks` / `events` / `tickets` 三张表，新增第四张，同为 `CREATE TABLE IF NOT EXISTS` 形态：

```sql
CREATE TABLE IF NOT EXISTS repos (
  name       TEXT PRIMARY KEY,
  path       TEXT NOT NULL UNIQUE,
  origin_url TEXT NOT NULL,
  created_at TEXT NOT NULL
);
```

字段约束的理由：

- `name` 为主键。唯一性是**每台执行机各自的**（每台机器一个 agentd、一份 DB），这正是想要的粒度。
- `path` 加 `UNIQUE`。两个名字指向同一路径会同时破坏自动匹配（变成假歧义）和 B42 的工作目录占用判定。
- `origin_url` **不**唯一。同一仓库在一台机器上有两份克隆是合法用法（例如基于不同基线），由解析规则报歧义并列出候选，不由 schema 一刀禁止。
- `created_at` 为 RFC3339 字符串，与既有表的时间字段形态一致。

## 4. 配置

`internal/config/config.go` 的 `Config` **顶层**新增一个字段（与 `Listen` / `Token` / `DataDir` 并排，即执行机上 agentd 读的那一层）：

```go
// RepoRoot 是 repo add --clone 未显式指定 --path 时的默认落点根目录，
// 实际路径为 RepoRoot/<name>。空=未配置，此时 --clone 必须显式给 --path。
RepoRoot string
```

放顶层而不是放进 `Target`：`Target` 在**审核者本地**配置里被读取（见 `cmd/pull.go` 的 `cfg.Targets[task.Target]`），放那儿会让「仓库放哪」变成审核者的本地状态，换一台审核机接管就得重配。放顶层的语义是「每台执行机自己决定它的仓库放在哪」，审核者本地一个字都不用配。

## 5. 命令面

```bash
handoff repo add [名字] --path /root/work/handoff --target devbox      # 形态一：登记已有克隆
handoff repo add [名字] --clone [--url <URL>] [--path <落点>] --target devbox  # 形态二：克隆并登记
handoff repo ls --target devbox                                        # 列出登记 + 每条的实际状态
handoff repo rm handoff --target devbox                                # 只删登记
```

规则：

- `--path` 与 `--clone` 二选一。都不给即报错，报文说明两种形态。
- **名字可省**，默认取 origin URL 末段（去 `.git`）。显式的是「落地」这个动作；名字沿用仓库自己的名字不构成隐式副作用。
- 形态二的 URL 默认取本地 cwd 的 `git remote get-url origin`（与基线取自 cwd 的 HEAD 同源、同一个 caveat：cwd 必须是同一个仓库），可用 `--url` 覆盖。
- **`origin_url` 落库的取值分两种形态，不混用**：
  - 形态一由 **agentd 在执行机上现读**（`git -C <path> remote get-url origin`），而不是采信 cwd 上送的值——登记的是那个路径上真实存在的仓库，它的 origin 才是权威。读不到 origin（仓库无 remote）时拒绝登记，因为没有 origin 就无法参与 §6 的自动匹配。
  - 形态二用**实际 clone 所使用的 URL**。
- 名字的缺省值由 **agentd 在拿到上述 `origin_url` 之后**推导，不在 CLI 侧猜。
- 形态二的落点 = `--path`，未给则 `RepoRoot/<名字>`；`RepoRoot` 未配置且未给 `--path` 时报错。
- `repo ls` 对每条登记顺带报实际状态：`有效` / `路径不存在` / `不是 git 仓库`。这是漂移的可见化手段（见 §8）。

HTTP 侧新增 `POST /api/repos`、`GET /api/repos`、`DELETE /api/repos/<name>`，与既有 `/api/tasks` 同构。

## 6. dispatch 的仓库解析

解析在 **agentd 侧**执行（表在那儿）。CLI 上送两样：用户原样输入的 `Repo`，以及 cwd 的 `OriginURL`（cwd 不是 git 仓库时为空）。

`proto.DispatchReq` 新增 `OriginURL string`；`Repo` 字段语义扩展为「路径或登记名或空」。

三分支：

| 输入 | 处置 |
|------|------|
| 含路径特征字符 | 当作路径，**完全是今天的行为**，不碰登记表 |
| 非空、无路径特征 | 按名字查登记表；查不到 → 400，报文列出该机器上已登记的名字 |
| 空 | 拿 `OriginURL` 归一化后匹配登记表：唯一命中 → 选中；零命中 → 400，列出已登记的并提示 `handoff repo add`；多命中 → 400，列出候选并要求用 `--repo <名字>` 显式指定 |

「路径特征字符」定义为 `/`、`\`、`:` 三者之一。只判 `/` 在类 Unix 执行机上已经够用，但 Windows 绝对路径 `C:\repos\x` 不含 `/`，会被误判成登记名——B37（prochost Windows 实现）目前搁置，此处多两个字符即可让规则不依赖那条搁置状态。

这条规则不产生实践中的歧义：`--repo` 指的是执行机上的绝对路径，不存在单段相对路径的用法。

`OriginURL` 为空且 `Repo` 也为空时 → 400，报文说明「当前目录不是 git 仓库，无法自动匹配，请给 `--repo`」。

**CLI 侧改动**：`cmd/dispatch.go:83` 的「`--repo` 必须指定」前置检查放开，把错误让给 agentd。好处有二：报错里能带上「这台机器上登记了什么」；本机派发与远程派发走同一条解析路径，无特例（本机派发也是发给本机 agentd，它同样有自己的登记表）。

### 6.1 URL 归一化

匹配前对两侧 URL 做归一化，规则：

1. 去掉尾部 `/`
2. 去掉尾部 `.git`
3. `git@host:path` 形式换算为 `host/path`
4. 剥掉 `ssh://` / `https://` / `http://` scheme 与 `user@` 前缀
5. host 部分转小写

不做归一化的后果：本地是 ssh 形式而执行机当初用 https clone 的，就会「明明登记了却零命中」——失败虽然响亮，但很费解，而这十几行能消掉它。

归一化只用于**比对**，登记表里存原始 URL。

## 7. 安全边界

这四条是硬约束，实现时不得省略：

1. **形态一登记前必须过 `EnsureRepoUsable`**（`internal/agentd/workspace.go:357`）。不是 git 仓库就拒，不登记空壳。
2. **形态二的目标路径已存在就拒**，绝不往里 clone，绝不覆盖。
3. **登记在 clone 成功之后才落库**。否则 clone 失败会留下一条指向不存在路径的记录。
4. **`repo rm` 只删登记，永不删磁盘上的仓库**；且路径正被活跃任务占用时拒绝——复用 B42 引入的 `ErrWorkdirBusy` 语义（`internal/agentd/server.go` 的 `writeDispatchError` 中已有该哨兵）。

## 8. 漂移

登记表与文件系统会漂移，两个方向不对称：

- **登记说有、实际没了**：有现成检测点——dispatch 用登记路径时本来就要过 `EnsureRepoUsable`，坏了就是 400 带 git 原文。再加上 `repo ls` 顺带报实际状态，漂移就是可见的而非静默的。
- **机器上有仓库但没登记**：无害。那只是没被 handoff 管，和今天完全一样。

不引入任何后台巡检或自动清理。检测点已经在必经之路上，够了。

## 9. 错误与状态码映射

沿用 `internal/agentd/server.go` 既有的哨兵 + `errors.Is` switch 模式。新增哨兵：

| 哨兵 | 状态码 | 场景 |
|------|--------|------|
| `ErrRepoNotRegistered` | 400 | 按名字查不到；或空 `Repo` 时零命中 |
| `ErrRepoAmbiguous` | 400 | 空 `Repo` 时 origin 匹配到多条 |
| `ErrRepoAlreadyExists` | 409 | 名字或路径已被登记；或 `--clone` 的目标路径已存在 |

复用既有哨兵：

- `ErrRepoUnusable` → 400：形态一的路径不是 git 仓库、clone 失败、dispatch 时登记路径已坏。
- `ErrWorkdirBusy` → 409：`repo rm` 时路径被活跃任务占用。

所有 400/409 的响应体必须带可读 `error`，不得扁平化——这是 B45 立下的规矩，`errors.Is` 链必须保住（server 的 switch 靠哨兵判定）。

## 10. 日志

按 `instrumenting-code` 的要求，以下点位必须有结构化日志（用项目既有的 `log()` slog，不得 `fmt.Printf`）：

- `repo add` 入口：name / path / url / 形态（登记 vs 克隆）
- clone 前后各一条；失败时 Error 带 git stderr 原文与 cause
- 登记落库成功：name / path
- `repo rm`：name / path / 是否因占用被拒
- dispatch 解析结果：走的哪个分支、命中哪条登记、候选条数
- 每个拒绝分支：Warn 带具体原因（与响应体同源）

## 11. 测试

- **解析规则做成纯函数 + 表驱动测试 + 变异检验**，照 B44 `decideBranchAction` 那一套：解析函数不碰 DB、不碰 git，入参是（用户输入的 Repo、OriginURL、登记条目列表），出参是（选中的路径 或 具体哪类错误）。
- **URL 归一化**单独一张表，覆盖 ssh/https/带不带 `.git`/带不带 `user@`/大小写 host 各种形态。
- **store 层**：`repos` 表 CRUD + 两个 UNIQUE 约束的冲突路径。
- **integration**：
  - `repo add --path` 指向非 git 路径 → 400 且带 git 可读原文
  - `repo add --path` 指向一个没有 origin remote 的 git 仓库 → 拒绝（见 §5）
  - `repo add --path` 省略名字 → 名字由 agentd 现读的 origin 末段推导，且落库的 `origin_url` 是该路径上仓库的真实 origin，而非 cwd 的
  - `repo add --clone` 落点已存在 → 409
  - clone 失败后登记表里没有残留记录（验安全边界 3）
  - dispatch 带短名 → 用的是登记的路径
  - dispatch 空 `--repo` + 唯一 origin 匹配 → 自动选中
  - dispatch 空 `--repo` + 多匹配 → 400 且报文列出候选
  - `repo rm` 时路径被活跃任务占用 → 409
- **真机验收**：在 devbox 上用**隔离实例**（新端口 + 新 DataDir + 独立二进制 + 独立仓库副本）验证 clone 真的落地、派发真的跑起来。**不得启停、覆盖、复用监听 7777 的那个 agentd**——它持有正在跑的任务。
- 全套闸门：`gofmt -l .`、`go build ./...`、`go vet ./...`、`go test ./... -count=1`、`go test -race ./cmd/ ./internal/agentd/ ./internal/store/`、`GOOS=windows GOARCH=amd64 go build ./...`。

## 12. 不在本次范围内

- 跨执行机的全局仓库视图（`repo ls` 只报单台）。每台机器各自持有自己的登记，这是 §2.4 的直接推论。
- 自动同步/更新已登记仓库（`git pull`）。派发时的基线校验与 `git fetch --all --prune` 回退已经覆盖了这件事。
- `repo rm` 连带删除磁盘仓库。见安全边界 4，这是刻意不做。
- 登记表的导入导出/迁移工具。规模没到。
