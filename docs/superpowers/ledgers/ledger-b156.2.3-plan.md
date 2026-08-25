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