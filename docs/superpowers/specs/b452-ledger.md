# B452 台账

## spec（2026-10-21，主会话）

- 来源对话：2026-10-21 用户提出五项升级（鉴权同步、额度、规则/skill 同步、派发形态、内置浏览器），主会话用三个 Explore subagent 摸现状后给分析；用户裁定顺序：熟练工（本卡）→ 账号与额度（B453）→ 执行器浏览器（B454）→ skill 同步（B455）；凭据同步不做。
- 卡：`handoff card add` → B452（charter v15，待办）；note seq 29186 记来源与两处疑似缺陷；`card move B452 spec`。同批建 B453/B454/B455（note seq 29187–29189）。
- 分支：`git checkout -b cards/B452-charter-1 origin/main`（基线 a9150118）。工作树带一处既有未提交改动 `docs/superpowers/specs/b434-ledger.md`（+7 行，非本卡，不动）。
- 现状读数（Explore subagent，2026-10-21，待精确复核）：dispatch template `TemplateDef` 含 executor/target/purpose/branch_prefix/prompt/discipline/model_by_target（`internal/ledger/templates.go:15-33`）；模板只经 `card dispatch --template` 调用（`cmd/card_dispatch.go:323,427-436`）；裸 `handoff dispatch` 无模板、只有 `--discipline-file`（`cmd/dispatch.go:199-226,367`）；提示词组装 `turn/protocol.go:46-89`：协议规则 → 纪律 → plan/附加指令；交回物尾注 `{branch,commit,summary}`/`{ask}` 解析进 `executor.Result`（`executor.go:80-95`），含 FinalText；无 `--target` 即本机 agentd（`cmd/root.go:241-249`）；项目须在目标机登记，未登记 CLI 自动 clone 并重试一次（`cmd/dispatch.go:83-110`）；未提交改动阻断派发（`--allow-dirty` 覆盖），未推送提交因目标机 fetch 不到而失败（`workspace/gitworkspace.go:1218` ErrBaseCommitMissing）。
- 账本现有模板（`handoff template list`）：charter-default v5、charter-story-default v1、domain-* v1、feature-impl v2、impl-codex v1、review-generic v3。review-generic 的 purpose=review 已是「不提交」形态：收尾行只输出 `{"summary":…}`，裁决走 handoff-verdict 块。
