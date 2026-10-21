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

## implement（2026-10-21，subagent）

- 加载 `charter:implement`；读 plan（已拍板，Q1 甲）、spec r1/r1.1、本台账。工作树 `.claude/worktrees/cards+B435-charter-1`，分支 `cards/B451-charter-1` @ `fb5f2fd9`，工作树干净。
- plan §2 第 1 步基线复核：`go test ./internal/permgate/ -run 'ManualChecklist|Heredoc|SearchPattern' -count=1` → `ok  	github.com/Xsxdot/handoff/internal/permgate	0.608s`。
- 承重假设复读成立：`judgeCommand`（`blacklist.go:133-165`）只有 Consult/Escalate 出口；`rmInScopeVerdict` 非 cd/rm 段 `default` 分支即否决（`rmtargets.go:148-153`）；`closedHeredoc` 其余段只许 `cd X`（`heredoc.go:109-118`）、`validateHeredocCat` 只认裸 `cat`（`:265-266`）——人工清单形态进不了前两个 AutoAllow 出口。
- 判据层先红（改测试未改产品）：`go test ./internal/permgate/ -count=1` → `FAIL	github.com/Xsxdot/handoff/internal/permgate	0.376s`。断言红、原因均为功能缺失：`TestJudgeManualChecklistConsults` 20 形态全部 `verdict = escalate（rule="manual-gate" …保持人工裁决）`（S2）；`TestJudgeManualChecklistHardEscalationWins` 5 行红——-c+自指令、heredoc+自指令、psql 黑名单+包装器、eval、rg --pre+自指令都被人工清单截成 `manual-gate`（S4；越界落点、截断两行修前已绿，它们本就先于人工清单）；`TestJudgeManualChecklistCompoundNeverAutoAllow` 5 行 escalate（S5）；`TestSearchPatternExceptionThroughJudge` rg --pre 行、`TestJudgeHeredocUnmatchedKeepsStatusQuo` python 消费正文行 escalate（S2 旁支）。
- 判据层产品改动：`manualgate.go` 命中改返回 Consult（闭包 `escalate`→`consult`，五句理由尾「保持人工裁决」→「交审批链裁决」，形态判据零改动），文件头/函数注释改写；`permgate.go` `judgeBash` 人工清单检查挪到 `judgeCommand` 非 Consult 原样返回之后、`safeCommandID` 之前，日志改「人工清单命中，交审批链裁决」（Info，带 reason）。转绿：`go test ./internal/permgate/ -count=1` → `ok  	github.com/Xsxdot/handoff/internal/permgate	0.400s`；`gofmt -l internal/permgate/` 空。
- 编排层测试（未改产品代码）：`permgate_wire_test.go` 删旧「无复用腿」`TestManualChecklistEscalatesToGateTicketWithoutConsult`，新增 `TestManualChecklistConsultsApproverAndApproves`（S1 Manager）、`TestManualChecklistApproverEscalateCreatesTicket`（S3a）、`TestManualChecklistApproverUnavailableCreatesTicket`（S3b，审批者 nil）、`TestManualChecklistApproverDisabledNamesItInTicket`（S3c）；`TestManualChecklistReusesSameTaskHumanAllow` 按 Q1 甲改写（桩回 escalate，断言审批者恰调 1 次、`manual-2:once`、reuse=1、request=0、auto_allow=0、无待答工单）；`approval_client_test.go` 新增 `TestApprovalClientManualChecklistConsultsApprover`（S1 OpenCode）。桩一律经 `injectNonceForTest`/`extractNonceForTest` 回显 nonce。
- 编排层先红（产品文件临时 `git checkout HEAD --` 回基线，测试保持新版）：`go test ./internal/orchestration/ -run 'ManualChecklist' -count=1 -v` → `FAIL	github.com/Xsxdot/handoff/internal/orchestration	0.567s`。断言红：OpenCode `decision = {Status:pending Reason:人工清单：…保持人工裁决 Rule:}`；S1 Manager「审批者未被调用」；S3a `approver_decision = []`；S3c「工单理由必须点名停用，得到 ""」；复用腿「应先问审批者一次（Q1 甲），实得 0 次」。S3b 基线即绿——改前改后都建工单，它是不回退锁，不是新行为。
- 回填新版产品文件后转绿：`go test ./internal/orchestration/ -run 'ManualChecklist' -count=1` → `ok  	github.com/Xsxdot/handoff/internal/orchestration	0.659s`；permgate `ok … 0.247s`。
- 提示词先红：新增 `TestDecidePromptInlineCodeLine`（含新行全文、位于生产环境行与自指令行之间、无 `%!`、nonce 可抠）；改模板前 `go test ./internal/orchestration/ -run TestDecidePromptInlineCodeLine -count=1` → `approver_test.go:1208: prompt 缺内联代码一行` / `FAIL … 0.523s`。`approver.go` 模板在「涉及生产环境」行后插入 spec 实现决定 3 原文，doc 注释加「2026-10-21 增补内联代码一行（B451）」。转绿：`-run 'TestDecidePrompt|Nonce'` → `TestDecidePromptContainsContext`/`TestDecideRequiresMatchingNonce`/`TestDecidePromptInlineCodeLine` PASS，`ok … 0.522s`。逐字核对：脚本从 spec 抠出该行，`approver.go` 含「\n行\n」= True、测试含 = True、行内含 `%` = False。
- 收尾：`go test ./internal/permgate/ ./internal/orchestration/ ./internal/approval/ -count=1` → `ok permgate 0.226s` / `ok orchestration 34.005s` / `ok approval 0.783s`（未遇环境性假红，无需 `env -u` 复跑）；`go build ./...`、`go vet ./...` 通过；`gofmt -l` 九个改动 Go 文件输出为空。全量 `go test ./...` 未跑（归 review/finish）。真机步骤未做（plan §3.3 由协调者执行）。
- 残留词搜：`/usr/bin/grep -rn -e 保持人工裁决 …` internal cmd 无命中；`heredoc.go:13`、`permgate.go:228` 注释仍称「人工门」指本判据，语义未变，`heredoc.go` 不在改动边界，未动。

## review（2026-10-21，独立 subagent，只读）

- 裁决：两轴通过，无 Critical/Important。Minor：M1 S3 审批者升级腿未断言 approver_decision 里的理由；M2 `permgate.go:228`、`heredoc.go:13` 注释仍称「人工门」；M3 `TestJudgeManualChecklistShapeBoundaries` 沿用 `wantEqual != 0` 零值跳过。
- 复跑：`go build ./...` ok；`go vet ./...` ok；`gofmt -l` 全仓 1401 文件为空；`go test ./... -count=1` 仅 `cmd` 的 `TestSessionSendActorResolution` 红，unset 会话环境变量后 `ok …/cmd 76.863s`（已知环境假红）；permgate/orchestration/approval 全绿。
- 变异：`manualgate.go:56` Consult→Escalate（唯一命中，编译过）→ 9 支红，含真实 `handlePermission` 与 `approval.Client.Request` 入口；还原后 15 支 PASS，工作树干净。
- 提示词行与 spec 实现决定 3 逐字一致（266 字节，无 %）。

## 真机（2026-10-21，协调者执行）

- 被验对象：分支 ec5d7a77 代码。`go version -m` 的 vcs.revision 显示 6de4b8e8+dirty（linked worktree 的 vcs 戳不可信），改用二进制内容核身份：含「交审批链裁决」6 处、「读完正文再裁决」1 处、「保持人工裁决」0 处。
- **环境替代（待用户认可）**：spec 指定 mac-mini。mac-mini 隔离实例（127.0.0.1:7796）起成，但 mac-mini 的 opencode 为 v2.0.29，`POST /session` 返回 405，派发失败（`启动 executor 失败: 创建会话: 状态码 405`）；linux-01 的 opencode 为 1.19.3。改在 linux-01 起隔离实例：127.0.0.1:7797，独立 datadir `/root/b451-iso`，SQLite 测试账本，本地审批链 agy；载体 opencode，模型 `opencode-go/deepseek-v4.1-flash`（与 E21 生产同款）。在役 `handoff-agentd.service` 全程 active、未动。
- 任务 95f41f3f，提示词两步：
  - ① `python3 - <<'PY'` 改工作树 `notes.txt`：日志「人工清单命中，交审批链裁决」（06:19:21）；事件 #3 `approver_decision` approve（executor=agy），#4 `ticket_answered` allow；无 `permission_request`；`notes.txt` = `line1-edited`。
  - ② `python3 - <<'PY'` 写 `os.path.join(os.path.expanduser('~'),'b451-probe.txt')`：日志同上（06:19:49）；#5 `approver_decision` escalate，理由「尝试在任务仓库外路径(~/b451-probe.txt)写入文件，超出仓库范围」；#6 `permission_request`（reason「审批模型未形成允许」）；任务 `waiting_answer`，待答 1。经 reply API 答 deny → #7 `ticket_answered` deny，#8 `turn_failed`「回合因权限被拒终止」；`ls /root/b451-probe.txt` → No such file。
  - plan 未验项：(a) opencode 未对正文拼出的路径填 Paths，请求走到了审批者；(b) 本地链模型对仓库外写入判 escalate；(c) 每条 bash 都发了权限请求；(d) CLI 用 `--config` + `--agentd` 指向隔离实例。
- 旁见：`handoff reply <task> --deny` 在该隔离实例上报 `ticket_id 与 answer 不能为空`（CLI 没取到待答工单），改用 `POST /api/tasks/<id>/reply` 直答成功。
- 收尾：任务 done 归档；按 pid 停隔离 agentd；删 `/root/b451-iso` 与 mac-mini 草稿 datadir/仓库；两台都无 `b451-probe.txt`。
