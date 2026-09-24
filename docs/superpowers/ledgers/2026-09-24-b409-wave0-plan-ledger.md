# B409 Wave 0 plan 台账

日期：2026-09-24
卡：B409
计划：`docs/superpowers/plans/2026-09-24-b409-wave0.md`

## 基线与输入

- 当前分支 `codex/session-reliable-mentions`。计划编写时 HEAD=`16498397`；房间历史限域契约提交=`25fa5c5f`；获用户批准的 r2 性能 spec 提交=`16498397`。
- `handoff card show B409` 实测卡仍在 `contract`；附件为获批 spec。B409 已通过 `handoff card note` 记录 L3 重档范围、内置浏览器验收与本机/`linux-01` 后续部署授权。
- 本计划只走 S2 房间历史首屏 Wave 0；S1 卡片投影、S3 定向收件箱/session wait、S4 诊断与 S5 全方言最终验收仍属父卡剩余工作。

## 代码与边界查证

- `codegraph who-calls n_collab_Service_History` 找到 HTTP 房间消息与 CLI room read 两个入口；历史方法图锚为 `moved`。真源码确认 `roomsapi.go` 传 `r.Context()` 到 `HistoryContext`，`cmd/room.go` 调 `History` 包装；`HistoryContext` 当前从 seq 0 调 `ReadAllEventsContext` 后再在协作层筛消息类型/房间/游标。
- `codegraph sym n_ledger_Store_EventsFromAsc` 锚为 `ok`，查询现状只支持 seq 游标与可选 card IDs；该全流读取同时被其他调用者使用，计划保留其语义。
- `codegraph sym n_collab_Service_HistoryContext`、`n_collab_room_ReadAllEventsContext`、`SessionTab`、`SessionChat`、`fetchRoomMessages` 均未命中图，已按规则回落源码。B409 contract 台账列出查询 helper 图覆盖债；本计划引用的 UI 入口来自 `SessionTab` 与 `SessionChat` 真源码。
- `SessionTab` 把空数据下的 loading、请求错误/会话失效分别传给 `SessionChat`；`SessionChat` 分别呈现“正在读取消息…”、“消息暂时无法读取”和真空态。计划先回归锁住现状，不先假定需改 UI。
- `codegraph check --repo . --stale` 实测 `fails=[]`；warning 种类为既有 best-dangling、budget-raised、legacy、oversized-package、prefix-family。`codegraph validate --repo .` 实测仍有既有 `cards-B272-charter` 无效领域与 `cards-B374-charter` 重复新增容器两项完整性问题；本卡不改这两份视图。

## 性能库前置条件

- `docker version` 返回 Docker CLI 29.2.1，但连接 `desktop-linux` 的本地 Docker socket 失败（daemon 未启动）。`/Applications/Docker.app` 存在。
- 本机 PATH 无 `psql`、`postgres` 或 `initdb`；后续计划使用本地 Docker Desktop 提供隔离 PostgreSQL。计划验收要求数据库名精确为 `handoff_b409_test`，DSN 不写入仓库或可见日志。
- 计划编写期间没有启动数据库/agentd，也没有修改实现代码；共享 PostgreSQL 仍只读，不含本卡合成事件。

## Charter 决定

- S2 房间历史通过 B156.2 既有 `LedgerClient` 接缝读取；新增跨域方法语义已在提交 `25fa5c5f` 冻结，目标图不新增方向。实现细节只允许选 SQL、索引或可重建查询投影。
- 用户明确要求所有目标页面 p95 小于 2 秒、全流程继续到完成、UI 用 Codex 内置浏览器且不经 SuperDev；用户已授权后续更新本机及 `linux-01` agentd。本 Wave 0 只先验房间消息首屏；部署留给父卡故事闭合后的最终 acceptance。
- Wave 0 计划通过后，父卡仍需按 Charter L3 要求由独立上下文起草 breakdown，协调者审议后再进入其余故事。

## 计划修订记录

- 在进入实现前按 `charter:implement` 与 `instrumenting-code` 复核发现，原计划没有把阶段性结构化日志、敏感值排除、职责注释和关键兼容规则的原因注释列为交付步骤。已在计划第 4 步补入可观测性与意图注释要求，并顺延后续步骤；这是独立的计划纠正提交，没有 amend 已冻结的计划提交。

## 计划节点自审

- `codegraph --help` 没有 `flow` 子命令；本计划未把 `chain` 冒充有序调用流程，流程事实按真源码核对。
- `codegraph resolve --doc docs/superpowers/plans/2026-09-24-b409-wave0.md --repo .` 返回成功；8 个 `file#Symbol` 锚均解析到源码，状态为 `moved`，因此计划明确按源码而非旧节点片段执行。
- `git diff --cached --check` 退出 0；暂存的只有 Wave 0 计划与本台账，没有实现代码。
- 提交计划时的命令与原始输出：`git commit -m "docs: plan room history Wave 0"` → `[codex/session-reliable-mentions 1286275c] docs: plan room history Wave 0`、`2 files changed, 97 insertions(+)`。随后按 plan 纪律只 amend 一次，把本条历史输出收进计划提交；amend 后不追写新 hash。
