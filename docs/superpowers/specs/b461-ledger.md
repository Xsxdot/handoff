# B461 台账

## spec（2026-10-22，主会话）

- 来源：用户问「收尾是不是也可以做成熟练工」，主会话拆为只读检查（本卡）与换版脚本（B462）；用户「可以，建两张卡，排在B453后面，如果B453做完我还没回来，你就自主继续推进这两张卡」。
- 分支 `cards/B461-charter-1` 自 origin/main d905c3ab，工作树在会话 scratchpad（避开主检出里 B453 实现者）。
- 现状：仓内熟练工定义只有 `skills/handoff/skilled/code-investigate.json`（receiver mimo、readonly）；生产 `template list` 有 code-investigate v1。
- 范围收窄：handoff 自身命令被权限门硬升级（B451/B460），换版后冒烟不归本卡；前端 vitest 缺 node_modules，不归本卡。
- 定级 L1，spec 即 plan。
- 实现：`skills/handoff/skilled/finish-check.json`（receiver mimo、readonly，五项检查 + 回报格式），SKILL.md 熟练工节加 finish-check 段，CHANGELOG 新增一条。`go test ./internal/skill/ ./cmd/ -run 'Skill|Template'` 见下行。

## S4 真实派发（2026-10-22）
- 命令：`handoff dispatch --template finish-check --prompt "卡 B453 base=a4e206d8" --timeout 40m --wait`（模板 v1，receiver mimo@linux-01），任务 5cad453b，检出 085ff40b。
- 过程：第 ④ 项逐个子命令跑 `"$TMPDIR/handoff-fc" <子命令> --help`，每条走审批链未形成允许 → 人工工单，共 13 张（退出码 5 照 reply/resume 行续等；主会话用只批「严格匹配 handoff-fc … --help 单行」的循环批准，其余一律停）。
- 结论原文：格式 通过 0（1504 文件）；静态检查 通过；全量测试 不通过 ok 65 / FAIL 2（已知抖动 0）/ 无测试 7；命令表 有差异 11；残余与 CHANGELOG 通过（未入 0，CHANGELOG 命中）。
- FAIL 2 = B453 的 `internal/agentd TestB453QuotaRerunEndToEnd` 与 `internal/orchestration TestB453QuotaReadonly*`，单包复跑仍红；mac 上同包全绿 → 平台/环境相关，交 B453 排查（另派熟练工在 linux-01 定位）。这正是 finish-check 要抓的东西：mac 上的「67 ok」不等于全队绿。
- 命令表差异 11 项（completion、decision、discipline、frames、machines、preview、room、session、squad、template、workflow 未进两份 README）为存量，落 roadmap。
- 判定：S4 通过（真实派发、五项都跑出结论、差异与红都有出处）；模板缺陷一处——④ 逐条读帮助触发 13 张工单。
- 修正：④ 改为「先拿顶层清单，再用一条 for 循环把全部子命令帮助导进 $HANDOFF_ARTIFACTS_DIR/help.txt」，一次运行至多一张工单。改后模板需 `handoff template put` 升 v2。
