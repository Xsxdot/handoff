# W3a：项目与机器控制面——后端设计

> 状态：待评审
> 前置：`handoff/b62-repo-registration`（登记归一化，[spec](2026-08-11-repo-registration-normalization-design.md)）**已合入本分支**后方可开工。
> 上游：[Web 控制台总方案](2026-08-11-web-console-master-design.md) §5；W3b（前端）另出 spec。

## 0. 范围与分工

按总方案 §6 的并行节奏，W3 拆成：

- **W3a（本 spec）**：W3 的**全部后端**——项目/位置/工作区模型、机器投影、agentd→agentd 转发、事件镜像、状态汇总、配套 CLI。只动 `internal/` + `cmd/`（`internal/proto/` 属审核者独占面，本期由审核者亲自改）。
- **W3b**：W3 的**全部前端**——项目树、机器列表、看板筛选。只动 `web/`。

**W3a 没有前端，验收面就是 CLI 与 curl**。因此 §7 的 CLI 子命令不是附赠品，是一等交付物。

### 0.1 对总方案 §5 的两处显式偏离

总方案 §5 写「machines / projects / locations / workspaces 的**持久化**与 API」。本 spec 推翻其中两处：

1. **workspaces 不持久化**（用户已定）：worktree 会在 agentd 背后被 `git worktree add/remove` 改动，落表必然产生说谎的行；现场探测 `git worktree list` 只需几毫秒。
2. **machines 不建表**：`~/.handoff/config.yaml` 的 `targets` 段已经是机器的真相（addr/user/token），再建表就是两份真相——改了配置忘了改表，就会有一台早已删除的机器永远躺在列表里。机器列表投影自 `cfg.Targets` + 本机自身，探活现场进行。

### 0.2 对「新表成唯一真相」决定的实现层收窄

早前定的方向是「`projects`+`workspaces` 新表成唯一真相，`repos` 降级为兼容投影」。落到实现，本 spec 把它收窄为：**概念模型是 project → location → workspace 三层，但物理存储仍是 `repos` 一张表**（B62 之后它已是可信的登记总账）。理由：

- `project_id` 是 `origin_url` 的纯函数（§1.2），workspace 是现场探测——**三层里两层是推导出来的，没有任何东西需要新表来存**；
- 若真建 `projects`/`locations` 新表又保留 `repos`，两边就要同步，恰好制造了「两份真相」——与降级 `repos` 的初衷背道而驰。

「降级为投影」发生在 **API/概念层**：`repos` 的 CLI 与 REST 原样保留，但语义上它登记的是「location」；项目视图由分组推导。物理表连名字都不改，零迁移。若未来项目需要自有属性（改显示名、置顶、归档），届时再加 `projects` 表不迟——那时它有真正要存的东西。

## 1. 概念模型：三层，每层的真相在哪

```
project  (跨机同一)   ←— project_id = sha256(normalizeGitURL(origin_url)) 前 16 hex
  └─ location (机器×目录) ←— repos 表：name / path / origin_url（B62 后：登记必有 origin）
       └─ workspace (工作树)  ←— 现场探测 git worktree list --porcelain
```

| 层 | 含义 | 真相所在 | 持久化 |
|---|---|---|---|
| project | 「handoff 这个项目」，跨机器同一 | 由 origin_url 推导 | 否（纯函数） |
| location | 该项目在某台机器上的一份 checkout | `repos` 表 | **是**（唯一持久层） |
| workspace | location 下的各工作树（含主工作区） | `git worktree list` | 否（现场探测） |

### 1.1 为什么 location 是独立一层

同一个 origin 在同一台机器上可以被 clone 到两个目录（如 `~/workspace/handoff` 与 `~/.codex/worktrees/.../handoff`）。它们是同一个 project 的两个 location，各自有自己的 worktree 群。压掉这一层就无法表达这个真实存在的形态。

### 1.2 project_id 推导（跨机归并的锚）

```go
// projectID 由 origin_url 推导项目标识：跨机器、跨路径稳定。
// 归一化复用 reporegistry.go 的 normalizeGitURL（剥 scheme/凭据/.git 后缀、host 小写），
// 因此 git@github.com:x/handoff.git 与 https://github.com/x/handoff 得到同一个 id。
func projectID(originURL string) string {
    n := normalizeGitURL(originURL)
    if n == "" {
        return "" // B62 后登记必有 origin，空串只可能来自历史脏数据，调用方按「未归属」处理
    }
    sum := sha256.Sum256([]byte(n))
    return hex.EncodeToString(sum[:])[:16]
}
```

两台机器各自对自己的登记算 `projectID`，汇总时按 id 归并——不需要任何跨机协商。

### 1.3 任务的项目归属：join，不加列

`tasks` 表**不加** `project_id` 列。列表/详情响应在读取时按 `task.repo_path` →（`filepath.Clean` 匹配 location 的 `path`，或其某个 workspace 的路径）→ `origin_url` → `projectID` 现算。理由：

- B62 后新任务的 `repo_path` 必然对应一条登记，join 恒成立；
- 历史任务或已注销仓库的任务诚实显示「未归属」（`project_id: ""`），而不是一列陈旧数据说谎;
- 加列的代价（回填 + 注销后列失真）只换来微不足道的查询加速。

注意 `--new-worktree` 任务的 `repo_path` 指向主仓库、`work_dir` 指向工作树——join 用 `repo_path` 即可命中 location。

## 2. 工作区探测

```go
// probeWorkspaces 对一个 location 现场探测其全部工作树。
// 输出含主工作区自身（git worktree list 第一行即主工作区）。
// 探测失败（目录被删、不是 git 仓库）不报错中断，返回带 error 说明的空列表——
// 项目树必须能展示「登记还在、目录已失效」这种真实状态,而不是整棵树 500。
type Workspace struct {
    Path     string `json:"path"`
    Branch   string `json:"branch"`    // detached 时为空,另给 head
    Head     string `json:"head"`      // 短 sha
    IsMain   bool   `json:"is_main"`
    // Managed: 是否 agentd 自建（路径落在 ~/.handoff/worktrees/ 下）。
    // 用于 UI 区分「任务工作树」与「人手开的工作树」。
    Managed  bool   `json:"managed"`
}
```

实现：`git -C <path> worktree list --porcelain` 一次调用解析全部；每行工作树可另附当前挂在其上的任务（按 `tasks.work_dir` 匹配，只查活跃态）。探测在**每次 API 请求时现做**，不缓存——本机文件系统调用，毫秒级，缓存只会引入失真窗口。

## 3. REST：项目与工作区

新增（均要求会话/令牌鉴权，与现有 `/api/*` 同门禁）：

```
GET /api/projects            本机项目树：项目 → locations → workspaces（含探测）
GET /api/projects?scope=all  跨机汇总版（§5 转发合并；见 §5.3 响应形状）
```

响应（单机）：

```json
{
  "projects": [{
    "project_id": "a1b2c3d4e5f60718",
    "origin_url": "git@github.com:xushixin/handoff.git",
    "name": "handoff",                  // 取该项目下首条登记的 name
    "locations": [{
      "name": "handoff",               // repos.name，即登记名
      "path": "/Users/sycm/workspace/handoff",
      "workspaces": [ ... ],           // §2 的探测结果
      "probe_error": ""                // 探测失败时的人话说明，空=正常
    }]
  }],
  "unowned": []                        // origin 为空的历史脏登记，诚实列出不吞
}
```

登记/注销**复用现有** `POST /api/repos`、`DELETE /api/repos/{name}`，不另开项目级写接口——项目树的「登记/注销」按钮打的就是这两个端点（W3a 已定作用域：导航/筛选 + 登记/注销，无创建/克隆）。

`GET /api/tasks` 增加查询参数：

```
GET /api/tasks?project=<project_id>   按项目过滤（join 见 §1.3）
GET /api/tasks?scope=all              跨机汇总（§5）
```

响应中的每个 task 增两个注解字段（proto 变更，审核者亲自改）：

```go
// proto.Task 新增（wire 注解，不入库）：
Machine   string `json:"machine"`    // ""=本机；否则为本机 cfg.Targets 里的机器名，由汇总方盖章
ProjectID string `json:"project_id"` // §1.3 join 结果；未归属为 ""
```

**`Machine` 不复用已有的 `Task.Target`**：`target` 存的是「当年派发它的那个 CLI 管这台机器叫什么」——换一台笔记本、换一份配置派发，同一台机器可以叫不同名字，它是历史记录不是路由键。`Machine` 由**本机 agentd 在汇总时盖章**（「我从哪个 target 拉来的」），必与本机 `cfg.Targets` 的键一致，§5.1 的透明路由和 UI 的机器筛选才有可靠的锚。`ProjectID` 则由任务所在机器自己注解（登记表在它那里）；汇总方只盖 `Machine`，不重算归属。

## 4. 机器投影与探活

```
GET /api/machines
```

列表 = `本机` + `cfg.Targets` 全部条目。每台的运行数据现场探测（并发、共 3s 预算）：

```json
{
  "machines": [{
    "name": "",                        // ""=本机（与 tasks.target 的空串语义一致；UI 显示「本机」）
    "addr": "127.0.0.1:7777",
    "reachable": true,
    "version": "...",                  // 探活取自 GET /api/status
    "active_tasks": 3,
    "error": ""                        // 不可达时的原因原文，reachable=false 时必非空
  }]
}
```

探活即向各机发 `GET /api/status`（本机走进程内直查，不自拨 HTTP）。**不可达是数据不是错误**：单台超时/拒连不影响整个响应 200，`error` 带原文——这与 `handoff status` 的分诊表一致。

## 5. agentd→agentd：转发与汇总

本机 agentd 用 `cfg.Targets` 里现成的 `addr`+`token` 充当其它 agentd 的客户端（与 CLI `--target` 同源同凭据，信任模型零新增）。浏览器只与本机 agentd 有会话（cookie host-only），一切跨机流量由本机 agentd 代理。

### 5.1 透明按任务路由（写操作与取证）

任务 id 是 UUID，全网唯一。对 `GET/POST /api/tasks/{id}/...`（reply/continue/done/stop/resume/diff/file/run/render）：

1. 本机 `tasks` 表有该 id → 本机处理（现状不变）;
2. 否则查镜像索引（§6 的 `mirror_tasks`）得所属机器 → 原样转发，响应原样透传（**状态码与中文报错原文都不改写**，§4.8 纪律）;
3. 两处都没有 → 404，与今天一致。

`render` 是流式响应，转发用 `io.Copy` 直通、客户端断开即断上游（AbortController→context 取消链）。

### 5.2 防环与超时预算

- 转发请求一律带 `X-Handoff-Forwarded: 1`；收到带此头的请求**永不再向外扇出**（`scope=all` 降级为仅本机）。一跳封顶，A→B→A 不可能成环。
- 汇总的扇出请求超时 **3s**（短于任何调用方超时），单台超时按不可达记入响应，不拖垮整体。

### 5.3 汇总响应形状：缺席必须可见

`GET /api/tasks?scope=all`、`GET /api/projects?scope=all` 的响应固定为：

```json
{
  "machines": [{ "name": "devbox", "ok": true,  "fetched_at": "...", "error": "" },
               { "name": "nas",    "ok": false, "fetched_at": "...", "error": "dial tcp ...: connection refused" }],
  "tasks":    [ ... ]     // 或 "projects"，各机结果合并，条目带 machine 字段
}
```

**硬约束：任何一台机器没答上来，都必须出现在 `machines` 里且 `ok:false` 带原因**——静默少几行是本设计的头号失败模式。`tasks?scope=all` 的远端部分取自 §6.3 的快照缓存（不现场扇出，见其理由）；`projects?scope=all` 现场扇出（低频操作，实时性换简单）。

## 6. 事件镜像

**目的**：远端任务的事件流在本机可订阅——浏览器永远只连本机一条 WS；这同时消解了 B61（整机级订阅）的需求。

### 6.1 发现式订阅（不改派发链路）

今天 `dispatch --target` 是 CLI 直拨远端，本机 agentd 不知情。镜像因此不挂在派发上，而是 agentd 自己发现：

- 常驻 **discovery loop**（30s 间隔，启动即跑一轮）：对每个 target `GET /api/tasks`，
  - 活跃任务（pending/running/waiting_answer/waiting_review）且尚未订阅 → 开一条上游 WS `ws://<addr>/ws/events?task=<id>&from_seq=<本机镜像水位>`；
  - 终态任务 → 收掉订阅（终态后事件不再产生；最后的 completed/failed 事件在订阅内已到达）。
- 发现有延迟没有关系：事件在远端落库，订上后凭 `from_seq` 补拉，**一条不丢**。首次订阅水位为 0，全量镜像该任务历史。

上游断线按现有客户端惯例退避重连（300ms 起、×2、上限 10s）；断线状态记入 §6.3 快照的机器可达性。

### 6.2 存储：`mirror_events` + `mirror_tasks`

远端 `events.seq` 是**远端库的全局自增**，不能混入本机 `events` 表（本机 seq 也是全局自增主键，必撞）。单开两张表：

```sql
CREATE TABLE IF NOT EXISTS mirror_events (
  task_id TEXT NOT NULL, seq INTEGER NOT NULL,   -- seq 保留远端原值：远端是权威，本机不重编号
  type TEXT NOT NULL, payload TEXT NOT NULL, created_at TIMESTAMP NOT NULL,
  PRIMARY KEY (task_id, seq));                    -- 幂等：重连补拉重复到达时 INSERT OR IGNORE

CREATE TABLE IF NOT EXISTS mirror_tasks (
  task_id TEXT PRIMARY KEY, target TEXT NOT NULL, -- §5.1 透明路由的索引
  snapshot TEXT NOT NULL,                          -- 最近一次拉到的任务体 JSON（§6.3）
  fetched_at TIMESTAMP NOT NULL);
```

镜像是**远端权威日志的副本**（replication），不是第二份真相：可随时整表删掉，从远端按 `from_seq=0` 重建。这与「workspaces 不落表」不矛盾——workspace 的真相在本机文件系统里随时可廉价重读，事件的真相在另一台机器上，不落库则每次断线都要跨网重放全量。归档清理：`done` 后的任务镜像保留（审阅历史仍可看），提供 `DELETE /api/mirror/{task_id}` 手动清理即可，不做自动过期（YAGNI）。

### 6.3 状态汇总：事件触发刷新 + 慢对账

远端任务的**状态**不从事件推导（那是重新实现状态机）。机制：

- **事件即门铃**:镜像收到任一事件 → 对该 target 发一次 `GET /api/tasks`（500ms 防抖合并突发），更新 `mirror_tasks.snapshot`;
- **慢对账**：discovery loop 的 30s 轮本身就是对账——补漏「不伴随事件的跃迁」与断线空窗;
- 看板的 `?scope=all` 直接读 `mirror_tasks` 快照 + 本机任务，**不现场扇出**——看板 2.5s 轮询打的是本机，快慢与远端可达性解耦；快照的 `fetched_at` 与机器可达性随响应给出（§5.3），UI 可显示数据新旧。

浏览器与本机的 W2 契约（2.5s REST 轮询 + 详情页单条 WS）**一行不改**。

### 6.4 对外服务：`/ws/events` 覆盖镜像任务

`handleEvents` 对 `?task=<id>`：本机任务走现有路径（`events` 表 + Hub）；镜像任务从 `mirror_events` 重放 `from_seq` 之后的历史，再挂到镜像 Hub 上收活事件。对浏览器**协议完全同形**——帧就是带 seq 的 Event，`ws.ts` 无感。

CLI `wait` **本期不动**：`--target` 直拨远端照旧。镜像跑稳后，`wait` 不带 `--target` 也能等远端任务（走本机镜像），那是后续演进，不进 W3a。

## 7. CLI（验收面）

```
handoff project ls [--target <机器>|--all]   项目树：项目 → location → workspace（树形人类可读 + --json）
handoff machines                             §4 的机器列表（表格 + --json）
handoff tasks --all                          跨机任务汇总（复用现有 tasks 输出格式 + machine 列）
```

登记/注销继续用现有 `handoff repo add/rm`（B62 已把它变成必经之路），不新增 project 级写命令。

## 8. 日志与观测（instrumenting-code 清单）

- 镜像连接生命周期必打点：订阅建立（task/target/from_seq）、断线（原因）、重连成功（续拉水位）、订阅收掉（终态）;
- 转发路径:转发发起（task/target/方法）、上游非 2xx（状态码 + 报错原文）、上游超时;
- discovery loop 每轮 Info 一行汇总（各 target 活跃数/新订/收掉/不可达），单任务细节降 Debug;
- 快照刷新失败必 Warn 带原因；成功路径 Debug（高频，防刷屏）;
- **凭据纪律**：target 的 token 一律不入日志；机器名、addr、任务 id 可以。

## 9. 测试清单

- `projectID`:表驱动——ssh/https/大小写/.git 后缀折叠为同 id；空 origin 得空串;
- 工作区探测:porcelain 解析（主工作区/分支/detached/managed 判定）；目录失效返回 probe_error 不炸树;
- 任务归属 join：命中/未登记/已注销三态;
- 转发:防环头收敛（带头请求不再扇出）、超时计不可达、报错原文透传不改写;
- 镜像:`INSERT OR IGNORE` 幂等（重连重放不重不丢）、水位续拉、终态收订阅;
- 汇总形状:单台不可达时 `machines[].ok=false` 且 `error` 非空、条目不静默缺席;
- `/ws/events` 对镜像任务:from_seq 重放 + 活事件续接,帧形状与本机任务无差别（复用 W2 的 FakeWebSocket 思路在 Go 侧做 httptest）。

## 10. 风险与回滚

| 风险 | 缓解 |
|---|---|
| 镜像/转发引入远端依赖，远端抖动波及本机 API | 快照缓存隔离看板路径（§6.3）；扇出 3s 预算；单台失败不 500（§5.3） |
| `mirror_*` 表脏 | 副本可整表删除重建（§6.2），零数据风险 |
| proto 注解字段影响现有客户端 | 只增不改；CLI/前端对未知字段天然容忍（encoding/json 忽略） |
| B62 未合并先开工 | 任务归属 join 恒空、项目树恒空——因此 §0 把合并列为硬前置 |

回滚：镜像与转发都在新增代码路径上，关掉路由注册即回到 W2 形态；无 schema 破坏（只增表、增列零）。

## 11. 交付物与 W3b 交接

W3a 完成的判据：§7 三条 CLI 在「本机 + devbox」真实两机上各自吐出正确结果；§9 测试全绿。交给 W3b 的契约：§3/§4/§5.3 的响应形状 + `/ws/events` 对镜像任务同形——W3b 只消费,不需要知道镜像的存在。
