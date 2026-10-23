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
