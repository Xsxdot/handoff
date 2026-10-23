# B463 r1.4 S2 实施台账

## 偏离与自主决定

- 范围按协调者核定的 r1.4 S2 I1/I2 与 S2 Minor 执行：`internal/permgate/`、本地候选审批链 `Decide→decideLocalChain→decideWithCandidate`、对应测试及本台账。未改 S3、Sol 计划/契约、CHANGELOG；未提交或推送。上述选择均在 [b469-plan.md](b469-plan.md) 的本轮修复单元与决策权限内。
- 为弥补 `StripQuoted` 删除双引号内命令替换正文这一确定性漏判，只在 AST 明确存在 `$(...)` 时检查该节点的命令体；普通双引号内容仍沿现有咨询语义。此为已授权的 S2 AST Minor，不扩大静态白名单或包装器名单。
- shell `-c` 识别保留 `-c` 与有效短选项组合（如 `-lc`），要求选项 token 仅由已知 shell 短选项组成；不再因非选项参数/任意 token 中含字符 `c` 就解析为脚本。未构建通用 shell 解释器。
- 初版曾把静态 `env | cut -d= -f1` 行管线当作名称投影。Sol 用合成 `SECRET=dummy\nLEAK_SENTINEL` 反例证实 cut 会把值片段作为独立行输出；按用户随后裁决撤销该例外。最终仅 Bash `compgen -e` 是 names-only 候选，仍走 `Gate.Judge` 的 `Consult`/模型流程；裸 env、printenv、所有 env-cut 形态均保持凭据升级判据。没有引入 NUL/Perl 或其他静态 allowlist。
- 只读政策将 `compgen -e` 的批准说明单列在 credential-list 升级行之前，避免“执行凭据清单任一项即升级”误覆盖；只读 help 与 `deploy.sh --help` 的边界保留。权限日志类别限定 Bash/Write/Edit/WebFetch 固定映射，其他前缀输出 `unknown`，避免把任意冒号前缀内容写入默认 WARN。
- 实施者没有运行真实审批模型或 review mutation。协调者提供的真实隔离模型报告为本轮 prompt 修复依据；修复后的模型复跑仍由协调者执行。上述本地 fixture 与包测试不替代真实模型证据。

## 红绿记录

| 路径 | 红色证据 | 修复与绿色证据 |
|---|---|---|
| I1 credential / shared policy | 第一版“正例”测试曾通过，但 Sol 合成换行值反例证明 `env | cut -d= -f1 | sort` 可输出值片段。新增断言后重见红：两个 env-cut 形态 `credentialExposure(...).hit=false`，且 synthetic leak test 失败；共享政策边界与 compgen 独立位置断言也变红。 | 删除 env-cut 例外与 AST 闭集，仅保留 Bash `compgen -e` 不命中 credential-value guard；原 env-cut sort/count 继续 `Consult`。`TestB469EnvCutProjectionSyntheticMultilineValueLeak` 只用合成字符串；Gate 表确认所有命令仍为 Consult。共享 `SecretEnvCommandsText` 仍用于 prompt，但独立于凭据清单行。 |
| S2 syntax Minor | 新增的真实 Gate 表测试首次失败：`echo "$(handoff dispatch T1)"` 与其后追加只读调用均被判 `consult`，预期 `Escalate/self-command`；原因是 `StripQuoted` 抹去了双引号内可执行替换体。 | Gate 在 quote stripping 前只检查 AST command substitution 体；AST 自指令命中保持单调。测试同时覆盖 process substitution、if、function、静态/动态 `sh -c`、普通含 `c` 参数、引号命令字和变量命令字。`go test ./internal/permgate -run '^TestB469SelfCommandShellSyntaxThroughJudge$' -count=1` 通过。无法静态证明的 `"handoff" dispatch` 与 `H=handoff; $H dispatch` 保持 `Consult`。 |
| I2 本地候选链与 help 政策 | `TestB463LocalCandidate*` 首次 assertion-red 观察到真实 `Decide` 入口日志包含 permission/cause/output、env 路径、任务摘要与 nonce；后续 `TestB469PermissionLogClassUsesFixedTrustedMapping` 再次断言红，发现 `TOKEN_SENTINEL:` 被直接写成 `permission_class=token_sentinel`。协调者真实隔离模型运行报告 `/tmp/b469-r14-runtime/model-9bd1d74b/report.json`：23 样本中 5 个历史 help（run/reply/done/continue/upgrade）被误升；reason 明确是原 prompt 的 handoff 变更子命令一律升级压过只读 help。第一轮提示修后，协调者第二轮真实隔离 probe `/tmp/b469-r14-runtime/model-f8cf40ff/report.json` 为 22/23，唯一误升是 `upgrade --help`；新增装配断言首次红，表明 prompt 虽泛称已验证只读 help，却未明确 Handoff CLI 本体与 upgrade 的对应关系。 | 本地链仅记录 request ID、固定可信权限类别、执行者、模型、耗时与错误类别；哨兵测试覆盖自定义前缀、错误/输出/env/nonce 等敏感字段。prompt 明确“已验证的 Handoff CLI 本体纯 `--help`”包括 `upgrade --help` 且只展示帮助，同时实际 upgrade/二进制替换仍升级；未验证脚本 help 仍升级，不改 CLI 判据、不硬编码 13 命令、不动 B451 逐字批准句。`TestB469PromptDistinguishesMutationFromVerifiedReadOnlyHelp`、prompt inline-code 与 golden 定向测试通过。此为第三轮针对同一 help-policy 根因的提示修正；如协调者复跑仍误判，停止继续措辞试探，先复核审批证据/政策契约。真实模型复跑待协调者执行。 |

## 决策权限对照

- 在权限内：私有 AST helper 命名、严格命令形态匹配、在 Sol 证明 env-cut 行式存在泄漏后按用户裁决收回该例外、固定安全日志类别、help 政策说明，以及用真实 `Gate.Judge`/本地 `Decide` 编写回归夹具；均属计划明确的 I1/I2/Minor 和随后用户澄清。
- 未行使越权：未扩展 AutoAllow 白名单、未缩减凭据黑名单/包装器保护、未改变审批结果/审批控制面、未改 wire 或审批权限；不把 names-only policy fixture 说成真实模型通过。

## 验证与未验

- 触及包测试：`go test ./internal/permgate ./internal/approval ./internal/orchestration -count=1` ——通过；三包 `ok`，orchestration 用时 50.069s。包含本轮 prompt assembly regression、B451 原句位置/金样更新、I1/I2/S2 Minor 回归。
- 全仓编译：`go build -mod=readonly ./...` ——通过（exit 0，无输出）。
- 未验证：修复后的真实模型对 13 条 help 原票、B453 env names-only 原票及本轮隔离判词；review mutation；真实 CLI/执行机。旧模型报告只作为修复根因，不算本次绿灯。

Gate 回归逐条覆盖历史 help 原命令（原事件 seq 65126–65229，`$TMPDIR` 保留原文本）：`"$TMPDIR/handoff-fc" dispatch --help`、`service --help`、`run --help`、`reply --help`、`done --help`、`continue --help`、`status --help`、`account --help`、`account list --help`、`account probe --help`、`upgrade --help`、`project rm --help`、`project add --help`。`TestB463HistoricalHelpCommandsRemainConsultNotSelfCommand` 逐条断言 Gate 仍为 `Consult` 且不命中 `self-command`。B453 原始 env 命令 `env | cut -d= -f1 | sort` 与历史 count 形态均被凭据判据升级到 `Consult`；只有 `compgen -e` 不命中 credential-specific guard，但 Gate 仍是 `Consult`，没有因此增加 AutoAllow。readonly policy 把已验证只读 CLI 子命令 `--help` 和 `deploy.sh --help` 区分，且 compgen 批准行独立于 credential-list 升级清单。

提交冻结源码：`git commit -m "fix(approval): separate verified help from mutations and redact candidate logs"` 原始输出 `[codex/approval-batch-b469 361554e41] ...`、`12 files changed, 464 insertions(+), 40 deletions(-)`；随后只 amend 一次收本条，不追写新 hash。真实模型第三轮待 root运行，此提交不表示故事验收通过。
