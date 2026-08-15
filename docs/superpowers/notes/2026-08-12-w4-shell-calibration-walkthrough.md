# W4 外壳校准期 真机走查记录（spec §9 十三条）

> 对应 spec：`docs/superpowers/specs/2026-08-12-w4-shell-calibration-design.md` §9
> 走查日期：2026-08-12 · 走查人：集成方会话（Claude）
> 结论摘要：**10 条通过 · 1 条不通过 · 1 条部分通过 · 1 条通过但另有 3 处未记录的偏离**

这份记录补的是一个空缺：外壳校准期做过一次真机走查，但结论只留在当时的会话里，
没有落到仓库任何地方。下次验收、或者另一个会话接手时无从对照。以下每条都注明
**怎么验的**与**看到了什么**；造不出条件的如实写「未验」并给原因，不打勾。

---

## 0. 走查环境（可复现）

生产 agentd 跑的是 main @ `76c3d6cc172a`，那个提交里**没有** `internal/agentd/workspacefiles.go`
（`git show 76c3d6cc172a:internal/agentd/workspacefiles.go` 直接失败），也就没有
`/api/workspaces/dir` 与 `/api/workspaces/file`——W4 前端在它上面跑不起来。所以走查
必须用 `w4-delivery` 分支自己编出来的二进制，且**不能**复用生产实例（同 DataDir 起
第二个 agentd 会被文件锁挡下，这是设计如此）。

沿用 B38/B48 验证时的既有做法：起一个**完全隔离的旁路实例**。

| 项 | 值 |
|---|---|
| 二进制 | `/tmp/handoff-w4`，由 `w4-delivery` 工作树 `go build` 得到（确认带 `console` 子命令） |
| 数据目录 | `/tmp/w4-walk`（独立），DB 用 `sqlite3 ~/.handoff/handoff.db ".backup ..."` 拷贝而来 |
| 监听 | `127.0.0.1:7788`（生产 7777 全程未动） |
| 令牌 | 现场随机新生成，不复用生产主令牌 |
| 前端 | `AGENTD_URL=http://127.0.0.1:7788 pnpm dev`，落在 **5174**（5173 被另一个 dev server 占着，没去动它） |
| 浏览器 | 视口 **1440×1024**，与 `prototypes/desktop-console/implementation-complete-workbench.png` 同尺寸 |
| 数据 | 3 个项目（handoff / probe-sandbox / sq），16 个 completed + 12 个 failed，**0 个活跃任务** |

**必须走 `localhost` 而不是 `127.0.0.1`**：`web/vite.config.ts` 的代理刻意没开
`changeOrigin`（agentd 的 Host 白名单与 coder/websocket 的 Origin 校验都要求浏览器的
Host 原样转发），而会话 cookie 是 host-only 的——两个主机名混用的现象是「登录过却 401」。

SuperDev 本次不可用（`list_projects` 报 `agent token required`），按铁律先查过了，
够不着才退回普通后台进程起 agentd 与 vite。这一条如实记下，不是绕过。

### 造数据的两处手工干预（重要，别当成真实任务流）

本机没有任何活跃任务，criterion 4 的 `waiting_review` 分支和 criterion 10 的工单
角标都造不出来。为此在**旁路库**（不是生产库）里手工改了两处：

1. 把 `3e3af89f-…`（probe S3 codex，`failed`，工作树 `~/.handoff/worktrees/3e3af89f`
   是唯一还活着的）先改成 `waiting_review`，再改成 `waiting_answer`。
2. 插入两张未回答工单（一张 gate、一张 question）挂在同一个任务上。

**改的是数据，不是响应**——前端走的仍是真实的 `/api/tasks/{id}`、`/api/tasks/{id}/diff`、
`/api/tasks/{id}/reply` 代码路径，diff 也确实在真实工作树上跑了 git。但这两条的
结论仍然弱于真实任务流的验证，下面逐条标注了。

---

## 1. 逐条结论

| # | 判据 | 结论 |
|---|---|---|
| 1 | 四级树；断开的机器仍在列并标原因 | ⚠️ **部分通过**（四级 ✓，断开机器不在树里 ✗） |
| 2 | 点目录 → 面包屑 + 右栏文件树 + 中央该目录的 tab 组 | ✅ 通过 |
| 3 | 切走再切回，两边 tab 组各自保持 | ✅ 通过 |
| 4 | 点任务 → TUI tab（时间线/事件流/指令框）；`waiting_review` 有审阅取证与 diff | ✅ 通过（review 分支为手工造态） |
| 5 | 点文件 → 只读文件 tab，不重复开 | ✅ 通过 |
| 6 | 左右分屏，切目录两组一起换 | ✅ 通过 |
| 7 | `+` → 空白 tab，三项带快捷键 | ✅ 通过 |
| 8 | 悬浮按钮 → 只有「新终端」，基准是 home | ✅ 通过 |
| 9 | 终端 tab 能关能分屏，内容是「PTY 后端尚未实现」 | ✅ 通过 |
| 10 | 工单角标 + 就地裁决 + 角标下降 | ⚠️ **部分通过**（角标与就地裁决 ✓，角标下降未验） |
| 11 | 看板四列 + **橙色**干预标记 + 点卡片跳转 | ❌ **不通过**（四列与跳转 ✓，干预标记是红色） |
| 12 | 设置能看到开发机列表与详情 | ✅ 通过 |
| 13 | 与原型并排，差异只应落在 §8 的五条 + §8.6 | ⚠️ **通过但有增补**（四区对得上，另有 3 处未记录的偏离） |

---

## 2. 逐条明细

### 1 — 四级树 / 断开机器 ⚠️ 部分通过

**四级 ✓**：`handoff → 本机 → w4-delivery / main / feat/b74-… → （任务）`。probe-sandbox 下
`probe-S3-codex` 目录里挂着任务行「probe S3 codex」，第四级坐实。主目录 `main` 带 home
图标，与工作树同一缩进层级，**平级 ✓**。

**断开的机器仍在列 ✗**。配置里放了一台注定不可达的 `offline-box`（`http://127.0.0.1:9`）。
`GET /api/projects/tree?scope=all` 的信封里它确实在：

```
{"name":"offline-box","ok":false,
 "error":"请求项目树: Get \"http://127.0.0.1:9/api/projects/tree\": dial tcp 127.0.0.1:9: connect: connection refused"}
```

但左栏树里**没有它**。根因在架构，不在渲染：

- `ProjectTree.tsx` 的机器节点是按 **project location** 渲染的，`locationProblem()`
  要先有一个 `loc.machine === 'offline-box'` 的 location，才能去 `machines[]` 里查
  `ok===false` 并标「已断开」。
- `internal/agentd/projectfanout.go` 的文件头写死了 **「现场扇出（不读缓存）」**。
  一台机器不可达 ⇒ 它一个 location 都不返回 ⇒ 树里既没有那台机器，也没有它上面的项目。

也就是说 `locationProblem()` 里 `machines[].ok===false` 这个分支在真机上**打不到**——
能打到的只有同机 location 的 `probe_error`。断开的机器上有项目时，那些项目是**整个
消失**的，不是「在列并标原因」。

断开原因本身没有丢：设置页 → 开发机 → offline-box 的「断开原因」栏有完整原文
（见 criterion 12）。丢的是**树里的可见性**。

> 这条要真修，需要 agentd 侧存一份 last-known 的跨机 location 快照（任务列表的
> `scope=all` 已经走镜像了，项目树刻意没走）。属于设计决策，不在本期范围，单独记。

### 2 — 点目录 ✅

点 `handoff → 本机 → w4-delivery`：面包屑变成 `handoff / 本机 / w4-delivery`；右栏出现该
目录的文件树（.claude / cmd / docs / internal / web / go.mod / README.md …）；中央切到该目录
的 tab 组，空态里写着「基准目录 w4-delivery」并列出三项。

### 3 — tab 组按目录各自保持 ✅

在 `w4-delivery` 开了 README.md + CONTEXT.md（左组）与 go.mod（右组），切到 `main`
（该目录自己的工作台：单组、空、随后开了它自己的 README.md），再切回 `w4-delivery`
——两组连同各自的激活项（左 README.md、右 go.mod）原样回来。

### 4 — 任务 TUI tab ✅（review 分支为手工造态）

点任务行 → 中央开出 `TUI · 3e3af89f`，自上而下：**回合时间线**（此任务 frames.jsonl 为空，
显示「等待模型输出…（frames.jsonl 尚为空属正常）」）、**事件流**（7 条，含 #133 question /
#134 failed / #135 tickets_voided）、底部固定的**推进任务**指令输入区。三段齐备。

把该任务改成 `waiting_review` 后，`TuiTab.tsx:52` 的 `inReview` 生效，事件流下方挂出
**审阅取证**面板：`改动 / 跑命令 / 读文件` 三页签、基准分支输入框 + 加载按钮，diff 已自动
跑完并给出「没有差异（分支与基准一致）」——**这是真的在 `~/.handoff/worktrees/3e3af89f`
上执行的 git diff**，不是占位文案。同时底部推进区从「当前状态没有可用的推进操作」变成
修改指令输入框 + 继续派发 + 完成任务 / 停止任务。

### 5 — 文件 tab 只读、不重复开 ✅

点 README.md → 开出文件 tab，正文渲染，右上角有「只读」角标。再点 CONTEXT.md → 第二个 tab。
**再点一次 README.md** → tab 条仍然只有两个，README.md 被激活而非新开。

### 6 — 分屏 ✅

tab 条右侧分屏图标 → 右侧新开一组（空态）。此时点右栏 go.mod，它落进右组。切目录时
两组整体随目录换（`main` 只有单组，切回 `w4-delivery` 两组连同内容一起回来）。
home 基准下同样可分屏，且新组的空态**只列「新终端」**（见 criterion 8）。

### 7 — `+` 空白 tab ✅

点 `+` → 开出标题为「新建标签页」的 tab，中间列「基准目录 probe-S3-codex」+ 三项：
`新终端 ⌘T` / `打开文件 ⇧⌘O` / `打开任务 TUI ⇧⌘A`。选「新终端」后**该 tab 原地变成**
终端 tab（标题 `bash · probe-S3-codex`），不是另开一个。

### 8 — 悬浮按钮只有新终端、基准是 home ✅

右下角悬浮按钮 → 弹出面板标题写着「**基准 home（不挂在任何项目上）**」，面板里**只有**
`新终端 ⌘T` 一项。开出来的 tab 标题是 `bash · home`，内容区头部 cwd 显示 `~`，面包屑
只剩 `home`，右栏文件树整个收起。

> 附带观察：home 基准下没有文件浏览，这是已知的推迟项（并行交接文档 §4「home 基准
> 文件浏览」），不是本条判据的一部分。

### 9 — 终端 tab 能关能分屏 ✅

内容区正文：**「PTY 后端尚未实现。当前查看 executor 现场请用 handoff attach <task>。」**
关闭按钮的 aria-label 是 `关闭 bash · home`，点掉即关（当前**无**二次确认——确认框是
PTY 计划 Task 16 才引入的，本期不算缺陷）。分屏见 criterion 6。

### 10 — 工单角标与就地裁决 ⚠️ 部分通过

**角标 ✓**：造出两张未回答工单后，左栏底部工单图标上出现橙色角标 `2`。

**就地裁决 ✓（按钮是真的）**：点开是「工单（2）」弹层，两张卡片各带任务名、工作树路径、
机器名与「跳到该任务 ↗」；gate 那张有 **批准 / 拒绝** 按钮，question 那张有**回答输入框 +
提交回答**。点「批准」后卡片内红字回显 **「agentd 返回 502 Bad Gateway」**——裁决确实
打到了真实的 `reply` 接口，落库成功（`tickets.answer='allow'`，`answered_at` 已写），
只是投递不到 executor（这个任务的 executor 早就没了）。错误原文透出这一点是对的。

**角标下降 ✗ 未验**：`TicketsOverlay.tsx:60` 只在 `onReply` **成功**时调
`tickets.refresh()`；本次唯一能造出的裁决必然 502，走不到那一支。而
`useGlobalTickets` 的重取条件是「`waiting_answer` 任务的 id 集合发生变化」，
id 集合没变 ⇒ 角标停在 2。

> 代码上这条应当成立，但**没有真机证据**。要真验，需要一个活着的 executor 挂起工单——
> 也就是本地起一个真任务。本次没有这么做（见下面「没做什么」）。**不要因为「看代码
> 应该没问题」就把它改成通过。**

### 11 — 看板 ❌ 不通过（颜色）

点左栏顶部「任务看板」→ 弹出四列看板：**等待执行 0 / 进行中 1 / Review 0 / 完成 27**，
顶部有搜索框、项目筛选、开发机筛选、「只看待处理」开关，右上角「共 28 个任务」。

**干预态标记是红色，不是橙色**。`probe S3 codex` 卡片上的「等你答复」徽章与旁边一堆
「失败」徽章**同色**——这正是 B75 要解决的问题，本次真机确认它仍然存在。

**点卡片跳转 ✓**：点「probe S3 codex」卡片 → 弹层关闭，面包屑跳到
`probe-sandbox / 本机 / probe-S3-codex`，该目录的 TUI tab 被激活（此前开的
`bash · probe-S3-codex` 终端 tab 也还在，顺带又验了一次 criterion 3）。

### 12 — 设置里的开发机 ✅

设置 → 开发机：顶部三个计数（开发机 2 / 在线 1 / 运行任务 1），左侧卡片列出 `本机`
（已连接）与 `offline-box`（已断开），右侧详情面板给出 addr、状态、Agent 版本、延迟、
运行任务数、项目目录数、最后心跳、可用执行者（claude / codex / fake / grok / opencode，
opencode 标「默认」）、机器操作（可用执行者 / 重启 agent / 打开终端）。

点 `offline-box`：详情里「**断开原因**」一栏是完整原文
`状态查询请求: Get "http://127.0.0.1:9/api/status": dial tcp 127.0.0.1:9: connect: connection refused`。
原因没丢——丢的只是左栏树里的可见性（criterion 1）。

### 13 — 与原型并排 ⚠️ 通过，但有三处未记录的偏离

**四个区域的位置与层次对得上**：左栏（顶部导航 + 项目树 + 底部操作条）、面包屑
（项目 / 机器 / 目录）、中央左右分屏、右栏文件树（搜索 + 树 + 刷新/收起）——位置与
层级关系与 `implementation-complete-workbench.png` 一致。

§8 的五条偏离都如约出现：TUI tab 是 handoff 自渲染（8.1）、中央下方**没有** dock
（8.2）、看板是弹层（8.3）、开发机在设置里（8.4）、**没有** `◉ localhost:5173` 预览 tab（8.5）。

**§8 没记、但真机上确实存在的差异有三处**：

| 差异 | 原型 | 实现 | 性质 |
|---|---|---|---|
| 左栏顶部搜索框 | 「搜索项目、机器或任务 ⌘K」 | 没有 | 就是 **B74**（已排期，并行交接文档 P2） |
| 底部状态栏 | 整条：分支 / Go 1.24.5 / ⚠0 / 行列 / UTF-8 / LF / 机器 / 时间 | 没有 | 未见任何裁决记录 |
| 面包屑右侧 | `已连接` 状态 + 分支芯片 + 通知/账号入口 | 只有分屏图标 | 未见任何裁决记录 |

反向增加一处：**右下角悬浮新建按钮**原型里没有，是 spec §2 新引入的（criterion 8 就是
在验它），属于有意为之。

> 建议：把后两行补进 spec §8，或明确判定为「暂缓」。现在它们既不在偏离清单里、也没有
> 对应的 backlog 行，属于**无人认领的差异**——下次验收还会再撞一次。

---

## 3. 本次**没有**做的事（避免下次误读这份记录）

- **没有在本机派发真任务**。criterion 10 的角标下降、以及真实的 permission/question
  事件流，都需要一个活着的 executor。`fake` 执行者救不了场：`cmd/agentd.go:254` 用
  `fake.New(nil)` 注册的是**空脚本**，`Add()` 只在测试里用——派下去会直接落
  `waiting_review`，一张工单都不产生。
- **没有动生产实例**（7777）与另一个占着 5173 的 dev server。
- **没有改任何生产数据**。手工造态全在 `/tmp/w4-walk` 的库拷贝上。
- **没有验 W4e `handoff tui`**：那是另一条线，不在 §9 十三条里。

## 4. 这份走查产出的待办

| 事项 | 归属建议 |
|---|---|
| 看板干预态改橙色 | 已有 **B75**，本次真机复现确认 |
| 左栏搜索框 | 已有 **B74** |
| 断开机器在树里整个消失（criterion 1 后半句不成立） | **新**：需要 agentd 侧的 last-known location 快照，或明确改判据 |
| 底部状态栏、面包屑右侧连接态/分支芯片 | **新**：补进 spec §8 的偏离清单，或开 backlog 行 |
| criterion 10 的「角标下降」补一次真机验证 | 挂到下次有真任务在跑时顺手做 |
