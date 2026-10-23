# B463 实施台账

## 偏离与自主决定

- 偏离：无。
- 自主决定：固定 `mvdan.cc/sh/v3` 为 `v3.14.1`，该版本与模块 Go 1.26.1 兼容，采用 BSD-3-Clause 许可；按计划的可逆实现权限内选择。新增依赖仅用于 POSIX shell AST 解析。Go 1.26要求同步整理 go.mod 中既有直接导入依赖的 direct/indirect 标记，并移除两个不再需要的间接依赖项；未进行全量 tidy，以免扩大 go.sum 改动。
- 自主决定：权限日志只记录审批请求类别、环/候选身份、裁决、耗时和跳过原因，不记录权限原文或环境值；在默认 WARN 下可见，属于计划授权的脱敏日志实现。
- 自主决定（review 修复）：用私有 context value 生成进程内请求 ID，关联同一裁决的环 WARN 日志；不改 wire。AST wrapper 子脚本从词元原始 source range 取值，保留 `$action` 等动态文本。
- 自主决定（review Important 修复）：共享链的本机 OneShot 失败日志及其外层环失败日志不输出权限原文、执行错误正文、裁决理由或 env 解析细节；改记 request_id、环/执行器/模型、失败类别和耗时。错误对象仍按原有调用契约返回。属于既定“日志不泄密”边界内的修正。

## 证据

- 红：新增 `TestB463ShellSyntaxFalsePositives` 后运行 `go test ./internal/permgate -run TestB463ShellSyntaxFalsePositives -count=1`，两条循环中的 grep 反例均因 `self-command` 被升级；失败原因为旧分段/包装器文本判据把 `grep -c` 参数误作执行载荷，原因与计划一致。
- 绿：`go test ./internal/permgate -count=1` → `ok`。
- 绿：`go test ./internal/orchestration -count=1` → `ok`（51.335s）。新增默认 WARN 日志用例确认环开始、结果、跳过及 codex 跳过原因可见，且日志不包含权限原文。
- 绿：`go test -mod=readonly ./internal/permgate ./internal/approval ./internal/orchestration -count=1` → 三包均 `ok`（orchestration 50.511s）；Approval 测试经 `Authority.Judge` 验证 Gate 的循环误报降为 Consult。
- 绿：`go build -mod=readonly ./...` → exit 0；`git diff --check` → 无输出。
- Review 退回后红：三条新增 Gate 反例分别暴露「变更后只读覆盖命中」「循环体同类覆盖」「`sh -c` 动态变量丢失」；修后 `go test -mod=readonly ./internal/permgate -run TestB463ShellSyntaxFalsePositives -count=1` 与 `go test -mod=readonly ./internal/orchestration -run TestB463 -count=1` 均通过。
- Review 退回后绿：`go test -mod=readonly ./internal/permgate ./internal/approval ./internal/orchestration -count=1` → 三包通过（orchestration 53.838s）；随后 `go build -mod=readonly ./...` 和 `git diff --check` 通过。新增并发日志测试断言两条同环请求有不同 request_id。
- 样本追加：协调者以无游标只读事件回放补回 B461 第 13 张原权限事件（`/tmp/b469-full-permission-events.jsonl`，seq 65126，`"$TMPDIR/handoff-fc" dispatch --help`）。新增 `TestAuthorityB463HistoricalHelpCommandsRemainConsult`，将 13 张原命令逐条穿过生产 `Authority.Judge` → Gate，均保持 Consult 且不触发 `RuleSelfCommand`；dispatch/reply/upgrade 的实际 `handoff` 变更命令仍为 Escalate/self-command。`go test -mod=readonly ./internal/approval -run '^TestAuthorityB463HistoricalHelpCommandsRemainConsult$' -count=1` 通过；全包 `go test -mod=readonly ./internal/approval -count=1` 通过。
- 二次 review Important 红：新增 `TestB463LocalRingFailureLogsDoNotExposePermissionOrCause`，经 `Approver.Decide` → 共享链 → 本机 OneShot 真环入口运行；失败断言确实抓到默认 WARN 日志中的 `Bash: sensitive-command-value` 及 executor error 正文，并发现逐环失败和全环失败错误日志也转抄正文。
- 二次 review Important 绿：`RunLocalRing` 的缺少 OneShot、env 解析、执行失败、无效输出、成功出口，以及本机 OneShot 解析失败、逐环/全环错误日志均改为只记安全类别与关联字段；新增真实本机环失败脱敏测试。默认 WARN 失败脱敏回归先红后绿。`go test -mod=readonly ./internal/orchestration -count=1` 通过（54.158s）；最终变更后的 B463 触及用例通过；`go build -mod=readonly ./...` exit 0；`git diff --check` 无输出。
- 样本追加：协调者恢复 B453 env 原票 `30a850b2-671b-464d-8468-951225b57eaa` seq 65278 与 `afb30d7b...` seq 66383；名称查询原命令为 `bash: env | cut -d= -f1 | sort`，重复计数命令为 `bash: env | cut -d= -f1 | wc -l`，原因均为“审批模型未形成允许”。新增 Authority/Gate 表驱动回归覆盖名称原命令与 `env | cut -d= -f2` 值读取反例，两者都保持 `Consult`（须经审批，不 AutoAllow），并非 self-command。目标用例通过，后续隔离 S2 快照完整 `go test -mod=readonly ./internal/approval -count=1` 通过（0.678s）。
- 二次 review Important 超时分类：新增真实 deadline OneShot 测试，在隔离 S2 快照先红，实得 `ErrorKind:internal`、期望 timeout；复制修复后绿。`RunLocalRing` 用 `errors.Is(err, context.DeadlineExceeded)` 或调用 context 的 deadline 归为既有 `timeout` 类别，不记错误正文；`context.Canceled` 保留既有 `internal` 类别（当前失败词表无独立 cancelled 项）。目标用例红后绿。最终无 S3 的 S2 快照 `go test -mod=readonly ./internal/orchestration -count=1` 通过（56.891s）、`go test -mod=readonly ./internal/approval -count=1` 通过（0.678s）、`go build -mod=readonly ./...` exit 0。
- 全量包测试未运行；实现阶段按三段律只运行触及包测试与全仓构建。未做 mutation，按任务要求留给 review。

## 实施结果

- `IsSelfCommand` 现在优先解析 shell AST 并逐个判断实际命令节点；保留现有三级判据、grep/rg 严格搜索例外、执行包装器与 eval 保护。解析失败保守回退原判据。原示例循环中的 grep 搜索参数不再误命中；真实 handoff 变更、shell/python 执行包装器和 eval 门禁回归保持。
- AST 遍历在首次命中后保持单调，不会被同一序列后续只读节点覆盖；`sh -c` 从源文本范围提取脚本内容，动态变量保留并按未知子命令保守命中。
- 本地候选与共享审批环增加 WARN 开始/结果记录；干净裁决后剩余环记录跳过原因。共享环日志不输出权限文本、错误正文或模型理由，失败使用 `error_kind` 分类关联。
- B461 13 张 help 与 B453 两张 env 名称原权限事件已恢复并穿过 `Authority.Judge` 测试；`env | cut -d= -f2` 值读取保持非 AutoAllow。

## 未验证项

- 隔离真实 codex 多环环境下的前环 approve、escalate、故障/超时，以及新进程未设置 `HANDOFF_LOG_LEVEL` 的真实运行日志。
- B453 已恢复的真实 env 工单是两条环境变量名称查询；其他环境查询形态仍靠隔离门禁反例覆盖。
- 隔离 agentd 与真实 adapter 进程链；Authority → Gate 入口有包级回归，但未启动真实 adapter/agentd。
- shell 方言、平台差异及解析失败输入的生产分布。
- Review 变异复验与调用者侧集成验收。
