# B276 acceptance 台账

- 2026-08-28 卡进 acceptance。审查 pass，对象 `9abc17116`，分支 `cards/B276-charter-2`。review 任务 `f8ee8a83` completed.commit 与 implement 同一 SHA。
- 复跑（本机）：`go build ./...` 退出 0。
  - `TestBareDispatchProbeFailureDoesNotClaimUnsupported` / `TestCardDispatchProbeFailureDoesNotClaimUnsupported` / `TestCardStepProbeFailureDoesNotClaimUnsupported` PASS。
  - `TestStartCardStepRejectsUnsupportedTarget` nil/false 仍含升级文案 PASS。
  - `TestStatusWebEmbeddedJSONAndTextStates` false/true/nil PASS；`TestStatusReportsWebEmbeddedStubOverHTTP` PASS。
  - `TestResolveServiceBinFallsBackFromGoBuildCache` / `TestResolveServiceBinSkipsTempFallback` / `TestIsEphemeralBin` PASS。
  - `TestRootRejectsDeletedGraphCommand` PASS；`go run . graph --help` 原文 `Error: unknown command "graph" for "handoff"`。
  - skill 178/254/596 含 `show --target` + `pending_tickets`。
  - `TestCardUpdateAcceptanceReportsInFlightTasks` 四子例 PASS；`TestPatchCardAcceptanceReportsInFlightTasks` PASS。
- 变异（均先唯一锚点+编译过，已还原）：
  - M1 裸派发探活失败改成 fall-through → `TestBareDispatchProbeFailureDoesNotClaimUnsupported` FAIL。
  - M2 `resp.WebEmbedded = nil` → `TestStatusReportsWebEmbeddedStubOverHTTP` FAIL。
  - M3 `resolveServiceBinFrom` 不再跳过 ephemeral 候选 → `TestResolveServiceBinSkipsTempFallback` FAIL。
  - M4 平台层改回 `handoff graph` → `TestComposeEnabledKeepsHeadBaseTailOrderAndSources` FAIL。
  - M5 skill 178 改回「404 跳过」→ 目标句 grep 无命中；还原后命中。
  - M6 `SetAcceptance` 在飞判定恒 false → `TestCardUpdateAcceptanceReportsInFlightTasks` FAIL。
- 真机：review commit = implement `9abc17116`。现役 agentd 仍 `69f25dfd`，活 GET `/api/status` 无 `web_embedded`（未换二进制）。分支内 httptest 已锁 stub=false。
- 无本卡 codegraph/diffs 视图，跳过图对账。
