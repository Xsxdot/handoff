# 台账：B156.2.3（C3 d_ledger(ledgerstep)·终态遗留裁决补解析接入）plan 轮（2026-08-26，边干边追加）

- 分支 cards/B156.2.3-charter，HEAD=c2e91ca0（工作树 clean 起步）。
- [亲读] 契约 `docs/superpowers/specs/b156.2-contract.md` §3.7 全部 + §4 补解析块 3 条：中间态定义（卡 Status∈{已完成,终止} 且存在 status=open 的 Decision）、处置（RunOnce 入口检测→转等人 reason 前缀「终态遗留裁决」+timeline 说明评论）、绝不伪造 decision_answered、幂等判据（EnsureComment dedupe_key 生效）。
- [亲读] breakdown C3 四段式：有界文件集 node.go ＋ node_test.go；入口指针 node.go RunOnce(:130 起)、haltForHuman 落卡三件套先例(:81-100)、open 判定复用 ListDecisions(openOnly) 按卡过滤。
- [亲读] node.go：当前无任何补解析/ListDecisions/EnsureComment/decision_answered 引用（grep 零命中）；RunOnce 在 :130，haltForHuman 在 :81-98，!n.Node.Dispatch 检查在 :143。
- [亲读] C2 已把 EnsureComment 填肉合入功能线（commit e917933e，events.go:228-280），不再是 return false,nil 空壳——条 2 变异靶（EnsureComment 换 AddComment 应翻红）现在可真跑。
- [亲测] 基线：`go build ./...` → BUILD_OK（EXIT=0）；`go test ./internal/ledgerstep/...` → ok 1.198s。
- [亲测] 图闸：`go run . graph check --repo . --view cards-B156.2-charter-4` → EXIT=0，fails=0（JSON 解析）；视图 cards-B156.2-charter-4 存在。
- [探针实测] 临时写 internal/ledgerstep/zz_probe_c3_test.go 两支探针，`go test ./internal/ledgerstep/ -run TestProbeC3 -v` 输出原文：
  - PASS TestProbeC3IntermediateStateConstructible：OpenDecision + MoveCard→已完成 + ListDecisions(true) 按卡过滤命中 open 裁决 + CloseCard→终止 全部可构造（bug 工作流 MoveCard 到已完成无 gate 拦截；CloseCard 需受控 reason 词表）。
  - FAIL TestProbeC3TodayNoHalt：今天 RunOnce 对终态+open 裁决不拦，走派发→Await("")→「裁决解析失败」转等人，reason 实得「裁决解析失败」；探针断言「reason 应以前缀开头」翻红（日志原文：`today action="needs_human" reason="裁决解析失败"`、`PROBE-EXPECT-RED 正断言：reason 应以前缀开头，实得 "裁决解析失败"`）。
  → 证明：正断言（转等人真的发生 + reason 前缀）今天写下去确实红（最薄路径条成立）；负断言（无 decision_answered）今天即绿——即协调者补充说的「反面断言稳定假绿」实锤，必须配正断言。探针已删除，未入提交。
- [判断] 检测点落位：RunOnce 读卡后、`!n.Node.Dispatch` 检查之前。理由：真实恢复场景是终态叶子列（无 Dispatch）被再驱动——runner.go:91 对纯人工列直接调 RunOnce 且不装配 WriteGate/Session；检测放 Dispatch 检查之后会被「纯人工列不可执行」的配置错误吞掉，放之前才满足契约「RunOnce 入口检测」。
- [判断] 说明评论走 EnsureComment（新私有 helper haltForHumanEnsure 与 haltForHuman 同构但换幂等写）；dedupeKey=「终态遗留裁决:」+cardID（卡级，跨节点驱动同一张卡也只一条；C2 plan 建议的 `<前缀>:<卡号>:<节点名>` 含节点名，同一卡被两个节点驱动会留两条，与契约「重复驱动同一张卡不产生第二条」字面不符，故取卡级；C2 注释明示「建议非冻结，C3 自定」）。
- [判断] 变异靶两发：①删/反转终端态守卫→正断言红（Test1「应转等人/不得派发」）；②haltForHumanEnsure 内 EnsureComment 换 AddComment→幂等测试红（两次驱动后 comment 恰 1 条翻成 2 条）。
- [判断] 有界文件集 node.go＋node_test.go，与 C4（service.go+子包）零交集；node_test.go 既有 import 块已含 context/fmt/strings/testing/ledger，测试代码零新增 import。
- [产出物] docs/superpowers/plans/b156.2.3-plan.md（随本提交入库）。

## 实现轮（2026-08-26，分支 cards/B156.2.3-charter-2，T3.1+T3.2）

- [亲测] 基线复核：`go test ./internal/ledgerstep/...` → `ok github.com/Xsxdot/handoff/internal/ledgerstep 2.034s`（EXIT=0）；`grep -n "终态遗留\|ListDecisions\|EnsureComment" internal/ledgerstep/node.go` → 零命中（grep_exit=1）。
- [亲测] 跑红（写三支测试后）：`go test ./internal/ledgerstep/ -run 'TestNodeStepLeftover' -v > out.log 2>&1; echo EXIT=$?` → EXIT=1。三支 FAIL 原文（节选）：
  - `TestNodeStepLeftoverDecisionHaltsForHuman`: `node_test.go:682: 正断言：reason 应以「终态遗留裁决」开头，实得 "裁决解析失败"`；日志尾部 `裁决解析失败转等人 ... cause="报文中没有 handoff-verdict block"`（今天不拦、走派发→Await("")→裁决解析失败路径，与 plan 预测一致）。
  - `TestNodeStepLeftoverDecisionHaltIsIdempotent`: `node_test.go:745: 重复驱动两次后应仍恰 1 条说明评论（EnsureComment dedupe_key 生效），实得 2`。
  - `TestNodeStepLeftoverDecisionOnTerminatedCard`: `node_test.go:769: reason 前缀: "裁决解析失败"`。
  - 三支均为断言红（非编译红），失败原因=功能缺失，非 typo。
- [判断] 常量引用：ledgerstep 包一贯用全限定名 `ledger.StatusDone`/`ledger.StatusClosed`（plan 代码块的裸名是省略写法），落地用全限定名保编译。
- [亲测] 跑绿（实现三处改动：import strings / haltForHumanEnsure / RunOnce 检测块）：同上命令 EXIT=0，三支 PASS。日志关键行：`WARN 检测到终态遗留裁决，转等人 status=已完成 open_decisions=1`、`INFO 说明评论已落账 dedupe_key=终态遗留裁决:P1`、第二次驱动 `INFO 说明评论幂等跳过：同键已存在`。
- [亲测] 触及包全量：`go test ./internal/ledgerstep/...` → ok 0.886s（无回归）。
- [变异甲] 守卫条件取反为 `card.Status != StatusDone && card.Status != StatusClosed`（改语义不改有没有用）。先 grep 命中唯一（count=1，node.go:170）。`go build` EXIT=0。两次输出（协调者点名必记）：
  - 守卫移除 + 全断言 → `go test -run '^TestNodeStepLeftoverDecisionHaltsForHuman$' -v` → EXIT=1，红：`node_test.go:682: 正断言：reason 应以「终态遗留裁决」开头，实得 "裁决解析失败"`（即「加上正断言后的红」）。
  - 守卫移除 + 临时只留负断言（注释掉 Action/reason/派发/评论正断言，`_ = dispatched` + `_ = commentBody` 保编译）→ 同命令 EXIT=0，PASS（即「只留负断言时的绿」）→ 证明负断言单独是稳定假绿，正断言把它锁住。临时改动用 git checkout 复原后重贴三支测试（见下条事故记录），revert 后 `go test -run 'TestNodeStepLeftover'` EXIT=0。
- [事故] 变异甲 revert 时误用 `git checkout internal/ledgerstep/node_test.go` 从 index 恢复——三支测试尚未 commit，被一并回滚。重新用 edit 重贴完整三支测试（内容逐字与 plan §3 代码块一致），`go test -run 'TestNodeStepLeftover'` EXIT=0 复原。教训：未 commit 的新增内容不能靠 git checkout 恢复。
- [变异乙] haltForHumanEnsure 内 `n.St.EnsureComment(cardID, dedupeKey, body, n.actor())` 换成 `n.St.AddComment(cardID, body, "普通", n.actor())`。先 grep 命中唯一（count=1）。`go build` EXIT=0。`go test -run '^TestNodeStepLeftoverDecisionHaltIsIdempotent$' -v` → EXIT=1，红：`node_test.go:745: 重复驱动两次后应仍恰 1 条说明评论（EnsureComment dedupe_key 生效），实得 2`。revert 时踩到「同一段文本出现两次」（haltForHuman 与 haltForHumanEnsure 同构），改用带注释锚的上下文才命中唯一。revert 后 `go test -count=1 ./internal/ledgerstep/...` → ok 0.927s。
- [亲测] 日志/注释检查：`grep -n 'Println\|fmt.Print' internal/ledgerstep/node.go` → 零命中；`gofmt -l` 两文件零输出。
- [亲测] T3.2 回归：`go test -count=1 ./internal/ledgerstep/... ./internal/ledger/...` → EXIT=0，输出 `ok github.com/Xsxdot/handoff/internal/ledgerstep 0.969s`、`ok github.com/Xsxdot/handoff/internal/ledger 2.800s`、`ok github.com/Xsxdot/handoff/internal/ledger/api 1.357s`。
- [亲测] T3.2 图闸：`go run . graph check --repo . --view cards-B156.2-charter-4` → EXIT=0；解析 JSON `fails` 为空数组；warns 为既有谱（anchor-off-domain×2 / best-dangling×3 / container-misplaced / legacy 预算内直调 / oversized-package / prefix-family），无 d_ledger/ledgerstep 相关新增条目；首行提示「预算棘轮判据已跳过：无法读取基准 70d243f… 的 codegraph/target.json」（基准缺失，既有形态，非 fail）。
- [亲测] 占位残留扫描：`grep -n "return 0, nil\|return false, nil" internal/ledgerstep/node.go` → 零命中（scan1_exit=1）；`grep -c "终态遗留裁决" internal/ledgerstep/node.go` → 5（注释×2 + reason 格式化 + logger.Warn + dedupeKey）。
- [产出物] 提交 T3.1：`feat(ledgerstep): 终态遗留裁决补解析接入（B156.2.3 欠账#7 其余半）`；提交 T3.2：台账收尾。

## 补丁轮（2026-08-26，分支 cards/B156.2.3-charter-3，只补一支测试，实现一行未动）

协调者验收变异（去掉 node.go 循环里的 `d.CardID == cardID` 过滤→存活）证明「按卡过滤」从未被测试行使过：node_test.go 三处 OpenDecision（657/718/757 行）全开在被测卡自己身上。本轮补跨卡隔离测试。

- [亲测] 基线复核：`go test -count=1 ./internal/ledgerstep/` → EXIT=0，`ok github.com/Xsxdot/handoff/internal/ledgerstep 1.438s`。
- [产出物] `internal/ledgerstep/node_test.go` 末尾追加 `TestNodeStepLeftoverDecisionIgnoresOpenDecisionOnOtherCard`（建 A、B 两卡；B 上开一条 open 裁决、A 一条没有；A 移到已完成后驱动 RunOnce；断言 A 正常派发 ActionDispatched、无 needs_human、无「终态遗留裁决」评论）。实现零改动，既有三支测试一字未动。
- [亲测] 自验判据 1（当前实现绿）：`go test -count=1 -run '^TestNodeStepLeftoverDecisionIgnoresOpenDecisionOnOtherCard$' -v ./internal/ledgerstep/` → EXIT=0 PASS。日志关键行：`INFO 已派发 node=待审阅 card=P1 target=linux-01 task=task-1`、`INFO 节点结束（只派发不裁决）... action=dispatched`——A 未被拦下。全量 `go test -count=1 ./internal/ledgerstep/` → EXIT=0 ok 1.033s（无回归）。
- [变异] 自验判据 2（去掉过滤翻红）：变异点 `internal/ledgerstep/node.go:178`（`if d.CardID == cardID {`，循环内唯一，`grep -c "d.CardID == cardID"`=1）；施加位置与执行位置同一棵树（本 worktree，HEAD=08db7563）。变异方式=整段去掉过滤守卫（`if d.CardID == cardID { ... }` 换成无条件 append），改语义不改「有没有用」。`go build ./...` → BUILD_EXIT=0（编译过，非编译红）。`go test -count=1 -run '^TestNodeStepLeftoverDecisionIgnoresOpenDecisionOnOtherCard$' -v ./internal/ledgerstep/` → EXIT=1 翻红，断言原文：`node_test.go:844: 跨卡隔离：A 卡无 open 裁决，B 卡的裁决不得拦下 A——应正常派发，实得 action="needs_human" reason="终态遗留裁决：卡已到终态（已完成）但仍有 1 条未答复裁决"`。日志关键行：`WARN 检测到终态遗留裁决，转等人 ... status=已完成 open_decisions=1`——B 的裁决拦住了 A，正是协调者变异的存活路径，本测试把它锁住。
- [亲测] 还原变异（Edit 加回守卫）后：`git status --short` 仅 ` M internal/ledgerstep/node_test.go`（node.go 零 diff）；`go test -count=1 ./internal/ledgerstep/` → EXIT=0 ok 0.884s 回绿；`gofmt -l internal/ledgerstep/node_test.go` 零输出；`go vet ./internal/ledgerstep/` → EXIT=0。
- [产出物] 本轮提交：`test(ledgerstep): 补终态遗留裁决跨卡隔离测试（B156.2.3 补丁轮）`（含测试与台账追加）。