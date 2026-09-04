# B330 台账

- 2026-09-04：B156.3 真机验收发现 charter v10 派发节点 Override.Squad 全空，GET /api/queue 空。建卡 B330。根因：cardstep 只在 squad 非空时 Admit。
- 2026-09-04：用户声明 runner 小队已就绪。`handoff squad list`：runner = executor，成员 muse/5。
- 2026-09-04：`handoff workflow show charter` → Version 10，七个 dispatch 节点 squad 空。定义导出 `/tmp/charter-v10.json`。
- 2026-09-04：不并入 B329。定级 L1：只 put，不改 Go。不 migrate 在飞卡。
- 2026-09-04：活代码锚：`internal/agentd/cardstep.go` 的 `node.Override.Squad != ""`；`scheddispatch.go#admitSquadStep`。
- 2026-09-04：独立审查 `docs/superpowers/reviews/b330-spec-review.md`。总判可以批准。无 Critical/Important。吸收 M1–M5 句子与 roadmap 两行。json 相对 v10 Def 除七处 squad 外 canonical 全等。
- 2026-09-04：bug-batch 即批准。实现由协调者本机 put，不派 linux-01 改活账本。
- 2026-09-04：实现记录：协调者已将 charter `workflow put` 至 v11；`workflow show` 对账为七个派发节点（contract/breakdown/plan/implement/review/integrate/图对账）`override.squad=runner`、五个人工列（待办/spec/acceptance/finish/已完成）squad 为空；本执行者未重复 put、未 migrate、未对 B330 执行 `--step`。本地 show 未验证：`go run . workflow show charter` 原始错误为 `打开账本库: 建账本 schema: 执行 DDL "INSERT INTO card_prefixes (project, pref": attempt to write a readonly database (8)`，只读 SQLite 读数为 v1。提交命令 `git commit -m "docs(b330): record charter runner wiring"` 原始输出：`[cards/B330-charter 8b36702c] docs(b330): record charter runner wiring`、`1 file changed, 1 insertion(+)`。
