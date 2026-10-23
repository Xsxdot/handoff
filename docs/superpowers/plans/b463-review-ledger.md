# B463 / S2 子卡审查台账（2026-10-23，技术阶段）

## 偏离与自主决定

- 实施交回首节已列依赖版本、WARN 脱敏日志与 review 后的私有请求 ID、AST 原始源片段提取；对照 plan r1.3 的「决策权限」均为界内可逆实现决定，未见未披露的范围扩张。
- 本审查自行选用 `/tmp` 独立快照、确定性 loopback OpenAI-compatible provider 与隔离 HOME 跑真实 CLI，避免 S3 在共享工作树的并行改动污染 S2 证据；这是验收环境选择，不替代真实模型判断或历史原载荷。

## 双轴技术阶段裁决

| 维度 | 裁决与证据 |
|---|---|
| 故事目标 | **S2 技术阶段通过，最终验收保留模型判断/平台边界**。AST 循环 grep 误报已降为 Consult，真 handoff 调用、赋值前缀、命令替换、包装器仍升级；真实多环 approve/escalate/无效输出/超时与 codex 跳过/执行已观察。权限原文泄露与超时分类两项 Important 已修并复验。B461 help 13/13 与 B453 两条 env 原票已恢复，15 条在真实 agentd/adapter/Authority/Gate/OneShot 链中逐条回放；固定绝对路径 alias 与原 `$TMPDIR` 文本的差异由原文本 Authority 测试覆盖并明示。 |
| 架构与证据 | **触及路径通过，验收边界明确**。解析在 `permgate`，审批环在 `orchestration`；独立快照经真实 `opencode` / `codex` OneShot、本机共享 `ChainReader` 与 `RunLocalRing` 运行。协调者在隔离 agentd 中完成真实 opencode bash permission → adapter → Authority/Gate → OneShot → 原生回答的五例与历史 15 例，原始 `/tmp/b469-p0-runtime/s2-adapter/report-five-cases.json`、`report.json` 及 `b463-integration-ledger.md`。未改 wire；无新增序列化字段。默认 WARN 与 nonce 裁决可见；loopback 协议响应只证明调用链，不证明实际模型判断质量。 |
| scope drift | 未见新增静态自动允许或缩减黑名单。旧 agy 专项仍按 spec 暂缓。 |
| 测试有牙 | 根协调者在 `/tmp/b469-p0-runtime/mutation-overlay.json` 令 `if hit` 变为 `if hit && false`，`s2-mutated.log` 中两条生产 Gate 反例转红；恢复后 `s2-restored.log` 两包转绿。本审查未重复变异。 |
| 日志与注释 | **通过（本轮路径）**。权限原文泄露与超时原因分类均经默认 WARN 新进程复验；环级日志具 request_id、环/模型/耗时及跳过原因。非本机、非当前 CLI 方言仍见未验边界。 |
| 适用契约 | 未触碰既有 `keysclient.Runner` / `hostapi.Host.RunTurn` 或新增冻结 wire；S2 仅权限判据与审批链日志。 |

## Finding

- **Important（已核销）— 默认 WARN 下失败日志泄露权限原文。**旧 `RunLocalRing` 在 OneShot 进程失败时输出 `permission=turn.TruncateRunes(permission, 80)`；修前原始证据保存在 `/tmp/b469-p0-runtime/s2-prior/s2-cli-invalid.stderr:7`、`s2-cli-timeout.stderr:2,7`。实现侧改为 request_id/model/error_kind/耗时，并清理外层逐环与全环的错误正文；刷新快照后四种真实 CLI 场景再跑，`/tmp/b469-p0-runtime/s2-cli-report.json` 中 `secret_marker_logged=false`，各 `s2-cli-*.stderr` 无测试权限标记。新鲜三包测试全绿。
- **Important（已核销）— 超时在默认 WARN 下无法识别。**修前隔离 provider 延迟 2.5 秒、环时限 1 秒时只记 `error_kind=internal`，与无凭据 codex 进程失败同类。实现侧对真实 deadline 做 `ApproverFailTimeout` 分类并有测试红绿；独立快照重建后 `/tmp/b469-p0-runtime/s2-cli-timeout.stderr:2,4` 记录 opencode `error_kind=timeout`，后环 codex 仍为 `internal`，没有权限原文或错误正文。四场景结果保持原样。

## 原始验收证据与边界

- 快照：`/tmp/b469-p0-runtime/s2-snapshot`，由 `5ba19a7941148133728904ba7d7727d2def5a681` 的 `git archive` 加本轮仅 S2 的 `go.mod/go.sum`、`internal/permgate/selfcmd*`、`internal/approval/authority_test.go`、`internal/orchestration/approver*` 覆盖；探针在快照 `cmd/s2probe`，二进制 `/tmp/b469-p0-runtime/s2-probe`。共享实现文件未改。
- 探针由 `/tmp/b469-p0-runtime/s2_cli_probe.py` 启动四个**未设置 `HANDOFF_LOG_LEVEL`**的新进程。每个进程仅保留 PATH/TMPDIR/locale，使用独立 HOME、XDG 与 CODEX_HOME；provider 只监听 `127.0.0.1`。真实 CLI 版本 `opencode 1.18.32`、`codex-cli 0.155.1`。确定性 provider 返回带当次 prompt nonce 的 approve/escalate 或无效文本；它不是实际模型判断。
- 汇总 `/tmp/b469-p0-runtime/s2-cli-report.json`：approve、escalate 都只执行 opencode 第一环，codex 跳过；invalid、timeout 都执行第二环，隔离无凭据 codex 真实失败并保持 fail closed。环级开始/结果/失败/跳过均在各 `s2-cli-*.stderr`，同次 request_id 一致。超时进程结果约 1.04 秒；CLI 出口正常，结构化结果的 `failed=true` 明确失败。原始泄密日志留在 `s2-prior/`，当前日志由刷新 `approver.go`、`approver_chain.go` 后重建的快照产生。
- 新鲜 `go test -mod=readonly ./internal/permgate ./internal/approval ./internal/orchestration -count=1` 三包通过。只读 `go test -overlay` 临时探针验证 `handoff dispatch T1; handoff tasks`、单双引号动态 `sh -c`、赋值前缀、`$(handoff dispatch)`、`env` / `find -exec` 均经 Gate Escalate；`grep -c "handoff $x"` 循环为 Consult。
- B461 help 13/13 与 B453 两条 env 原权限载荷已由协调者经原任务事件只读回放恢复，详见 `b463-samples-ledger.md`。协调者在隔离 agentd 真调用链又逐条跑 15 例：`/tmp/b469-p0-runtime/s2-adapter/report.json` 的 `cases=15`、`agent_exit=0`，逐例 `approver_decision=approve`、原生 `ticket_answered` 一条及完成 ask 一条，任务进入 `waiting_answer`。实验把 `$TMPDIR/handoff-fc` 换成固定绝对 alias 路径，原文本另由 `TestAuthorityB463HistoricalHelpCommandsRemainConsult` 表测。原始报告与差异说明见 `b463-integration-ledger.md`。未运行真实模型判断、跨机环、其他 shell 方言/平台验证；不把 loopback fixture 的 approve 推广为生产模型会批准。
