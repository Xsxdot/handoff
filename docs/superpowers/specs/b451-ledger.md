# B451 台账

## spec（2026-10-21，主会话）

- 卡：`handoff card add` → B451（charter v15，待办）；`card move B451 spec`；note seq 29058 记来源。
- 分支：`git checkout -b cards/B451-charter-1 origin/main`（基线 6de4b8e8）。
- 生产读数（linux-01，任务 c01a8fb9，E21 implement，opencode deepseek-v4.1-flash）：`handoff show` 最近 100 事件 = permission_auto_allow 48、permission_request 21、ticket_answered 25、approver_decision 5；permission_request 理由 20 条为「人工清单：内联 Python（标准输入脚本）」；人工答复间隔 8–12s；13:19 起 1 张待答。opencode 库同会话工具计数 bash 82 / write 9 / edit 7 / read 2，工具错误 0。
- 代码读数：人工清单判据 `internal/permgate/manualgate.go:48-82`；调用点 `internal/permgate/permgate.go:292-298`（在 `judgeCommand` 之前）；Consult 出口 `internal/orchestration/manager.go:2090-2108`；审批者收 `ev.Text` 全文 `manager.go:2366`；审批者 escalate 走 `escalatePermission`（无理由）`manager.go:2440`；提示词模板 `internal/orchestration/approver.go:703-712`。
- 受影响测试：`internal/permgate/manualgate_test.go`、`internal/permgate/selfcmd_test.go:185`、`internal/orchestration/permgate_wire_test.go:352`（无复用腿）与 `:390`（复用腿）。
- 共享审批链现状：`handoff approver show` → 版本 1，linux-01/agy → linux-01/codex gpt-6-luna，单环 1m。
- 批准：2026-10-21 用户「批准 B451 spec r1，含提示词那一行，按甲推进」；回写 spec 头部状态行。

## plan（2026-10-21，subagent 出稿）

- 加载 `charter:plan`、`charter:defect-families`（项目无自有缺陷族清单，用基线）；产出 `docs/superpowers/plans/b451-plan.md`，基线 `60722bef`。
- 读 `internal/permgate/manualgate.go:1-106`：`RuleManualGate` `:25`；`manualGateSegment` `:48-84`，escalate 闭包 `:49-51`；与 spec `:48-82` 吻合。
- 读 `internal/permgate/permgate.go:150-322`：`Judge` 截断出口 `:161-164`；`judgeBash` `:225-322` 顺序 = heredoc 剥离 `:230-234` → cd 基准不可证明 Escalate `:240-244` → 落点循环 `:253-271` → rm 出口 `:281-284` → heredoc 闭集出口 `:288-291` → 人工清单 `:292-298` → `judgeCommand` `:301-304` → 白名单 `:305-310` → 复合只读 `:314-319`。spec 现状 3 吻合；实现决定 2 顺序表漏 cd 基准出口（在最前，不动，不影响结论）。
- 读 `internal/permgate/blacklist.go:1-187`：`judgeCommand` `:133-165` 只返回 Consult/Escalate（doc `:116`）；`execWrapperRx` `:68-71` 含通用 `-[ceE]`，故 `psql -c`/`python -c` + 黑名单字样 = 硬升级；eval 升级在包装器分支 `:150-159`。推论：人工清单挪到 `judgeCommand` 之后，heredoc 内联 Python 正文会被黑名单/自指令/eval 扫到（正文含 `.eval(`、`sudo` 等即硬升级）。
- 读 `internal/permgate/heredoc.go:1-335`：闭集只认裸 cat（`:265-266`）、其余段只能 `cd X`（`:109-118`）、终止符后无命令（`:84-86`）；`heredocInScopeVerdict` `:310-335`（spec 写 `:303-333`，轻微漂移）。读 `rmtargets.go:60-162`：非 cd/rm 段否决 `:148-153`。spec「前三步天然进不去人工清单形态」成立。
- 读 `internal/orchestration/manager.go:2040-2164,2174-2240,2290-2460,2565-2610`：Consult 分支 `:2090-2108`；**复用判定只在 `escalatePermissionReasoned` `:2129-2131`**；`consultApprover` `:2351-2453`，Decide `:2365`（spec 写 `:2366`）；审批者 escalate 出口 `:2452`（spec 写 `:2440`，那是 Err 分支，两者都无理由，结论不变）；`approvePermission` `:2565-2587` 批准时建工单并答 allow。
- **与 spec 偏差**：spec 测试决定「有复用腿保持断言不变」在 Manager 路径不成立——审批者启用时 Consult 先问审批者，`TestManualChecklistReusesSameTaskHumanAllow` 的「不得经 Consult」断言（`permgate_wire_test.go:441-445`）必红。列为 plan Q1 待拍板（荐甲：路由不动，改写复用腿为审批者 escalate → 复用）。
- 读 `internal/approval/client.go:115-460`：OpenCode 路径 `Request` `:122-191` 复用 `:164-168` 早于 Consult `:170-173`；审批者 escalate 理由「审批模型未形成允许」`:406`。spec 现状 1 与 S3 只描述 Manager 路径，漏了这条第二消费路径（真机 opencode 走它）；plan 补一支 OpenCode 路径 S1 测试。
- 读 `internal/orchestration/approver.go:370-385,670-711`：模板 `:704-711`，生产环境行 `:709`，自指令行 `:710`；调用点 `approver.go:380` 与 `approver_chain.go:353`（`RunLocalRing`，注释 `:320` 称代他机端点也调它 → 远端环用环宿主机二进制的模板，部分换版期间远端旧环无新行）。
- 搜 `internal/orchestration/*_test.go`、`internal/agentd/approverapi_test.go`：无任何断言模板逐行内容或 nonce 次数的测试（只有 `approver_test.go:108-116` 含原文/摘要与 nonce 抽取）；spec「nonce 出现两次、编号对齐等既有断言」不存在，plan 改为新增模板断言 + go vet printf。
- 读测试：`manualgate_test.go:1-83`（`wantEqual` 零值跳过 = AutoAllow 被跳过，既有怪癖）；`selfcmd_test.go:170-197`（`:185` rg --pre）；`permgate_wire_test.go:300-458`（无复用腿 `:352-399`、复用腿 `:401-458`；spec 写 `:352`/`:390`）；**`heredoc_test.go:68`「python 消费正文走人工门」期望 Escalate——spec 漏列，plan 列入必改**；`permgate_test.go:219-221` 只断言非 AutoAllow，不受影响；`blacklist_test.go:53` 直调 judgeCommand，不受影响。
- **与 spec 偏差**：S4「理由与规则名同现状」——现状下组合命令先命中人工清单，记 `manual-gate`，不是硬升级的规则名；plan 将参照定为「该硬升级单独出现时的裁决」（实现决定 2 的推论），并记审计面变化（自指令组合命令日志 Info→Warn，`approval/authority.go:221-226`）。
- `/usr/bin/grep -rn manual-gate|人工清单`（除 b383 文档）：产品代码引用仅 permgate 两文件；`internal/ledgerstep/node.go:246` 的「人工清单」是另一概念，无关。
- plan 拍板（主会话，2026-10-21）：Q1 → 甲；§7 偏差 1–6 认可；spec 修订 r1.1 事实更正；roadmap 记 heredoc 正文被硬升级判据扫描一条。
