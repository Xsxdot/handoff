# B456 规格调查台账

## 偏离与自主决定

- 2026-10-23：用户开 B456，并指定聊天《按万里牛页面修改快递对账原型》为黑盒案例。先走了房间协调者入口；用户随后明确要求本地主会话直接推进、具体实现交给 gpt-6-luna。已取消远端出稿要求，本地占席；本地子 agent 只调查和制作规格原型，未进入正式实现。
- 当前工作区 `/Users/xushixin/.codex/worktrees/e113/handoff`，分支 `codex/b456-session-status`，开始时已跟踪/未跟踪工作树均干净。无 `codegraph/`。

## 事实与原始读数

1. `handoff card show B456`：初始 `status=待办`、`workflow=charter`、版本 15、无席位、无 task。
2. `read_thread(01a166f0-9ead-7dc1-b8f5-a1006b60bb4e)`：用户引用聊天标题《按万里牛页面修改快递对账原型》。当时聊天报告 E38 在 13:54 review fail 后，14:23 尚无新修复任务；修复在 14:49 completed，15:15 尚无第二轮 review。用户多次催促，最终要求原聊天拉回本地。此为聊天历史观察，不代表 E38 当前状态，不在本卡重复操作 E38。
3. `handoff card move B456 spec --expect 待办`：退出 0；事件 #33015。需求补充已落卡 #33018。
4. `handoff session create --owner agent:codex-b456 ...`：创建 `session:38`；`session join` 成功；最初出规格请求 #33022。
5. `handoff card coordinate B456`：15:17:53 发出后长时间无返回；15:19:20 `session detail` 仍 `status=empty`，不能称已启动。15:20:49 本地主会话 `card bind ... --cli codex --session 01a1679a-171c-7861-bb76-6d82cbd68596` 退出 0；席位事件 #33029。#33030 撤销远端要求，#33031 记录用户本地执行裁决；对仍在等待的本地 CLI 进程发 SIGINT，未停止/重启任何 agentd。
6. 原型基准读数：`prototypes/base/README.md` 记录会话已批准形态仍指向 `prototypes/b358-session-groups/pages/sessions-v2.html`；base 内没有对应 sessions 页面。B456 副本从 base 复制，并复用该已批准会话页面，不刷新全站、不改 base 形态。
7. 子 agent `b456_state_sources`（gpt-6-luna）只读调查：`internal/collab/sessions.go:704-718` 的成员状态只依据 DriverLease；到期值被填入 last_active，不能作为真实活动时间。`sessions.go:737-765` 的 nodes 是镜像历史，当前只投影 card_id/node/state；不能据此承诺执行者正运行。`sessions.go:779-901` 的 timeline 是结构事件，不等于执行现场。DTO `internal/proto/sessions.go`、TS 镜像 `web/src/api/rooms.ts`；真实入口 `SessionTab → SessionChat/SessionDetail`。
8. 已委派本地 gpt-6-luna：`b456_state_sources` 调查状态事实来源；`b456_prototype` 仅负责 gitignored 的 `prototypes/b456-session-status/`。正式代码尚未修改。
9. 第二轮事实调查：公开 `LedgerClient.EventsFromAscContext`（`internal/collab/client/client.go:70-82`）可按卡分页回放 `dispatched` 与 task_mirrored，身份关联用 source_target/source_task，任务内时序用 source_seq；镜像时间是源事件时间（`internal/ledger/mirror.go:64-78`），镜像可能滞后。`completed`/`turn_failed` 表示回合结束进入 waiting_review，不能当任务终态（`internal/ledger/taskstate.go:141-169`）。已有工单派生语义应在账本权威侧复用，不在 collab 再造不一致状态机。
10. pending launch 无专用取消 CLI。其最终落席只允许空座 BindSeatMode 或旧身份 CAS；晚到 launch 不会覆盖本地新席位（`internal/agentd/scheddrain.go:339-382`）。本地主会话停止的是等待中的 CLI，撤销的是远端工作授权，不把 HTTP 取消当远端进程已回收证明。15:22 后 card show 仍为本地席位且无挂账远端 task。
11. gpt-6-luna 原型已产生，主会话源码复查后要求修订：390px 隐藏旧侧栏、completed 改“执行回合已完成”、补“后继启动清除旧待接续”和“审批已答清除等待”对照情境。
12. SuperDev 无 handoff 可调试 deployment（list_services 与 list_browser_targets 只读确认，未修改服务）。尝试 IAB 打开 file:// 被 URL 策略拒绝，只允许 http/https；不换协议/换浏览器/自行起服务绕过。改为文件交付与静态检查，未声称视觉或实点通过。
13. 原型最终收口：E38 卡列 review 与 task waiting_review 分开，待接续属于卡协调者，后续复审归独立审查任务。主会话复跑 HTMLParser/ID 唯一性/资源存在性/Node 语法检查，原始输出：`PASS: HTML parser, 24 unique IDs, 8 local references, 1 inline script syntax checks; prototype patch refreshed.`
14. `git apply --reverse --check --directory=prototypes/b456-session-status docs/superpowers/specs/b456-prototype.patch` 退出 0；快照与当前副本对应。`git diff --check` 退出 0。规格、台账与原型增量快照入库，功能副本仍按约定忽略。spec 已挂 B456，卡停在 spec，待用户确认形态与范围。

## 待验证

- 正式实现需通过生产投影测试证明按 task 身份关联完成/继续/审批，旧镜像不可冒充实时值。
- 可点原型的默认形态、窄屏与状态切换待用户确认；本轮无浏览器视觉验收证据。

## 图覆盖债

当前 checkout 无 `codegraph/`，本轮以源码事实定位，未捏造图查询结果。

## 提交台账

```text
$ git commit -m 'docs(B456): define observable session roles and handoff prototype'
[codex/b456-session-status 87cb57569] docs(B456): define observable session roles and handoff prototype
 3 files changed, 341 insertions(+)
 create mode 100644 docs/superpowers/ledgers/b456-spec-ledger.md
 create mode 100644 docs/superpowers/specs/b456-prototype.patch
 create mode 100644 docs/superpowers/specs/b456-spec.md
```

随后仅 amend 一次收录提交台账；未推送、未合并、未修改正式运行代码。

## r2 当前页面形态重做（2026-10-23）

- 用户指出原型过时，首次附件为 ERP 导入账单弹窗；确认误发后提供正确 Handoff 截图 `/Users/xushixin/Downloads/AnyTimeDelete/截屏/截屏2026-10-23 15.48.32.png`。本轮依据这张图与当前源码更新 B456，非此前已撤回的“根据意见修改”请求。
- 现状源码：`Shell.tsx:1365` 桌面标题栏与统一项目树，旧左栏视图切换隐藏；`ProjectTree.tsx:435` 会话按项目聚合；`TabBar.tsx:132` 标签组；`desktop-workspace.css:35-36` 桌面标签栏与内容密度；`WorkbenchPage.tsx:488` 薄窗格标题；`SessionChat.tsx:270` 消息卡与底部输入区。与用户图一致的可见结构优先于旧 B358 原型。
- 已委派同一 gpt-6-luna 本地原型执行者：局部刷新 base 的独立当前会话镜像页与专用样式，并从它更新 B456 fork。base 不含新增状态栏；不重写全站/其他页面，不修改正式代码。主会话负责规格、README、增量快照和核验。
- 浏览器 file:// 上轮已被 URL 策略拒绝，本轮不绕过；只做文件/脚本检查与截图、源码的静态结构对照，不能宣称浏览器视觉或实点已验。
- 主会话源码复核后由 gpt-6-luna 收口：会话树/选中标签/窗格标题/消息一致；仅会话行可切换（click/Enter/Space）；树区独立滚动，归档与导航固定在侧栏底部；全局额度条在 workspace 外，角色条在会话输入区上；B456 专属样式不进入 base。最终纠正 E38/ERP 与 B456/handoff 的归属，默认选中 handoff 下的 B456。
- 主会话静态复跑原始输出：`PASS base/session-current.html: 0 unique IDs, 1 resource refs, 0 JS blocks; project ownership and global quota placement checked.`；`PASS b456-session-status/session-status.html: 13 unique IDs, 1 resource refs, 1 JS blocks; project ownership and global quota placement checked.`；两页 JS 语法与资源引用通过，未运行浏览器。
- 首轮快照检查因 minified HTML 缺末尾换行，diff 未写标准 no-newline 标记而失败；已修正快照生成，补标准标记，不改页面内容。随后 `git apply --reverse --check --directory=prototypes/b456-session-status docs/superpowers/specs/b456-prototype.patch` 与 `git diff --check` 分别退出 0。
- 从当前 base 复制、重命名当前会话页并应用新快照后，对首页、会话页、专用样式三文件逐字节比对，输出：`PASS: current base + prototype patch reconstructs all three authored fork files byte-for-byte.`
- 原预览路径 `prototypes/b456-session-status/pages/session-status.html` 已原位更新。基准与 B456 形态均保留未确认；没有以源码检查冒充视觉验收，没有开始正式运行时实现。

r2 提交原文：

```text
$ git commit -m 'docs(B456): refresh prototype against current session workspace'
[codex/b456-session-status e30070f76] docs(B456): refresh prototype against current session workspace
 7 files changed, 73 insertions(+), 189 deletions(-)
 create mode 100644 prototypes/base/pages/session-current.html
 create mode 100644 prototypes/base/shared/session-current.css
```

随后仅 amend 一次收录本提交台账；未推送或合并。

## r3 角色时间线、错误与可见运行内容（2026-10-23）

- 用户指出详情应该能看时间线、没接上的报错，以及驻场者和协调者的运行记录/内容。主会话读取当前副本确认：r2 只有概况与 `#mock-timeline` 占位链接，没有运行时间线；来源读取失败的示例不等于接续失败；没有分别展示驻场者/协调者的每轮内容。
- 已沿用 gpt-6-luna 本地执行：`b456_prototype` 只修改 fork 页面与专用样式；`b456_state_sources` 只读核查真实来源。base 现状页不加新能力，正式代码不改，规格仍是待批准草案。
- 来源核查：`internal/agentd/residentpush.go:88-117` 把房间消息截为 400 字摘要交给驻场者；`internal/keystone/resident.go:29-80` 的可见 Output 当前在内存结果，成功路径只留会话号/输出字节数，`residentpush.go:288-316` 失败写房间指针。房间 GET messages 可以读到原消息/实际发出的房间回复/失败指针，不能作为完整的每轮内容历史。
- 协调者 `wake_round` 评论在 `internal/agentd/scheddrain.go:715-755`、`wakeconsumer.go:406-442`，包含 start/end/fail、分类、session、时长和错误摘要；没有完整输入、输出与工具内容。`internal/agentd/coordapi.go:98-233` 当次响应可返回 Output，非可查询历史；`internal/hostapi/driver.go:158-212,320-358` 收集可见文本到内存，错误含 stderr 尾部，没有发现 Handoff 公开的逐轮 transcript 读取入口。
- 因此 spec r3 明确补观测记录与正式查询的需求：运行入口保存自身实际收到的内容/可见输出/失败；已有持久来源复用；来源不提供的工具细节与旧内容明示缺失。不能只做前端链接就宣称真实运行内容已接通，不新增调度状态源或重试/恢复操作。
- 原型增加概况/时间线切换、按轮次与事件展开正文、明确接续失败与仅缺后继两种场景、恢复后保留历史错误。主会话源码复核要求修正：驻场者恢复后仍等待的冲突、默认展开旧失败而非新成功、消息转交早于修复结束的时间矛盾、示例假 CLI 命令、灰色未知与明确失败的色彩混用。
- 最终修正已由 gpt-6-luna 完成：恢复情境的驻场者已收到 15:20 的接续回复；时间线最新轮优先展开，历史失败折叠；默认驻场者触发/发送示例移到 14:49 修复归档后；错误采用红点与错误块；不存在的示例命令改为人话动作摘要；清除重复 HTML 属性。
- 主会话复跑原始输出：`PASS role detail: 13 unique IDs, no duplicate attributes, 1 local resource, 1 JS syntax check; no placeholder timeline link.`；从 base 应用新快照逐字节重建三文件：`PASS: base + r3 snapshot reconstructs all three authored fork files byte-for-byte.`；快照反向校验与 `git diff --check` 均退出 0。
- 以上仅静态/源码与快照检查；没有浏览器视觉或点击验收，没有真实回合记录/工具内容接入。新能力保留在 fork，base 会话现状页没有新增时间线功能。

r3 提交原文：

```text
$ git commit -m 'docs(B456): add role timelines and run-content prototype'
[codex/b456-session-status 13e79e0fd] docs(B456): add role timelines and run-content prototype
 4 files changed, 147 insertions(+), 14 deletions(-)
```

随后仅 amend 一次收录本提交台账；未推送或合并。

## r4 用户原型与范围批准（2026-10-23）

- 当前用户明确按 `~/b456-proto-r4/README.md` 和今日卡 note 实施。已读 #33089、#33097、#33103 与 B471：S6/S7 拆出，B456 保留 S1-S5 和现有来源的最近动态。十条 r4 规则已回写 spec，状态改为已批准。
- `git fetch origin` 后 HEAD...origin/main 为 3/0，main 最新 a79b42867，无落后无需基线合并；工作区启动时干净。
- 保留用户本地/gpt-6-luna要求：独立 gpt-6-luna 起草 L3 plan、主会话拍板；另一既有 gpt-6-luna 先接收 r4 原型与快照，随后做正式实现。不启远端协调者，不改线上 agentd。
- r4 fork 与 README 已接收。对照页改用独立 session-baseline.css 修复404；五文件patch重建逐字节一致。base比较页仅补EOF换行，未改形态。192.168.0.167:8456 的状态页/对照页/两份CSS均HTTP200。
- 独立 plan r1 经主会话拍板，冻结 SessionDetail.role_status JSON；两位本地 gpt-6-luna 按后端/Web文件所有权协作，单卡不拆子卡。这是施工排序调整，不改变r4结果和B471边界。
- 基线预检：Go collab TestSessionReadsHonorCanceledRequest通过；Node24下 SessionTab 18 tests通过。web依赖 npm ci 已安装（不改lockfile）；系统Node23不满足依赖engine，后续用bundled Node24。
