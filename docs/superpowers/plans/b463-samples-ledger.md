# B463 原始权限样本取证台账

## 偏离与自主决定

1. `handoff show` 近期快照只覆盖 12 张；协调者随后经现有 `StreamEventsOnce` 从 seq 0 有界只读回放，恢复了全部 13 张 help 原权限事件。没有推进审核者 cursor，也没有用命令规律补造缺失项。
2. 首次未能从 B453 台账定位 env 名称原票；协调者按问题发生时段和已有任务索引缩小范围后，找到 `30a850b2` 的原权限事件。只读回放，没有运行历史命令或读取环境值。

## 查询范围与证据

- 仓内索引：`docs/superpowers/specs/b461-ledger.md` §S4（任务 `5cad453b`；原记载 13 张、v1 逐子命令运行 `--help`）、同文件 §S4 补派（`5ee97f19`）、`docs/superpowers/specs/b453-ledger.md` R4c（记录代理相关 `env | grep -i proxy | sort` 派发行为）与收尾段（任务 `16fd8b2a`）。
- 只读任务索引：`handoff tasks --all`，完整索引暂存 `/tmp/b463-samples-tasks.json`；按全 UUID 从本机任务快照查询，不打印或复制服务配置/环境值。
- 原事件：`handoff show 5cad453b-24df-4452-9986-8a5450dd7a2f` 输出暂存 `/tmp/b463-show-local.json`；`handoff show 16fd8b2a-9756-46a2-be13-9fcae3006c8c` 输出暂存 `/tmp/b463-show-16fd.json`。前者 `recent_events` 中可见 12 个 `permission_request`；后者只见 grep 包装命令事件，与 env 命令无关。原始可见权限事件摘录（命令及原因）暂存 `/tmp/b463-5cad-permissions.tsv`。未用 `wait` 消费游标，未运行历史任务命令。

## B461 首跑 help 工单：已恢复 13/13

下表的命令和原因逐字段来自任务 `5cad453b-24df-4452-9986-8a5450dd7a2f` 的权限事件。每张原因均为 `审批模型未形成允许`。完整回放原始文件 `/tmp/b463-full-permission-events.jsonl` 补足了快照缺少的 seq65126。

| # | 权限事件序号 | 原命令 |
|---:|---:|---|
| 1 | 65131 | `bash: "$TMPDIR/handoff-fc" service --help` |
| 2 | 65147 | `bash: "$TMPDIR/handoff-fc" run --help` |
| 3 | 65151 | `bash: "$TMPDIR/handoff-fc" reply --help` |
| 4 | 65159 | `bash: "$TMPDIR/handoff-fc" done --help` |
| 5 | 65171 | `bash: "$TMPDIR/handoff-fc" continue --help` |
| 6 | 65193 | `bash: "$TMPDIR/handoff-fc" status --help` |
| 7 | 65197 | `bash: "$TMPDIR/handoff-fc" account --help` |
| 8 | 65206 | `bash: "$TMPDIR/handoff-fc" account list --help` |
| 9 | 65210 | `bash: "$TMPDIR/handoff-fc" account probe --help` |
| 10 | 65218 | `bash: "$TMPDIR/handoff-fc" upgrade --help` |
| 11 | 65223 | `bash: "$TMPDIR/handoff-fc" project rm --help` |
| 12 | 65229 | `bash: "$TMPDIR/handoff-fc" project add --help` |
| 13 | 65126 | `bash: "$TMPDIR/handoff-fc" dispatch --help` |

## B453 排查 env 名称原样本：已恢复

从 `2026-10-21T19:20:34Z` 的 code-investigate 任务 `30a850b2-671b-464d-8468-951225b57eaa` 从 seq0 只读回放：seq65278 原权限为 `bash: env | cut -d= -f1 | sort`，reason=`审批模型未形成允许`。原始 `/tmp/b463-env-permission-events.jsonl`。同任务另一请求 seq65270 曾包含裸 `env` 值输出，不能当成“只列名称”的同义例。

后续已换审批环任务 `afb30d7b-2a99-4ace-8ab3-6525732a9a4d` 的 seq66383 为 `bash: env | cut -d= -f1 | wc -l`，reason同上；证实名称统计仍被模型干净升级，而非解析器故障。以上只读取原权限描述，没有读取命令实际输出。

B453 台账 R4c 的派发 prompt `env | grep -i proxy | sort` 不作为本样本；任务 `16fd8b2a` 的 grep 包装器误报也不混入 env 证据。

**结论：**B461 help 原权限样本 13/13 与 env 名称原权限已恢复；是否经新版本真实链逐条通过，另见实施与集成台账，恢复原文不等于验收。
