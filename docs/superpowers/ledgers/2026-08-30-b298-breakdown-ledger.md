# B298 breakdown 节点台账

日期：2026-08-30（法定路径逐字使用；实际出稿日 2026-08-29）
节点：breakdown（L3 轻档，出稿者：跨子系统单 agent）
上游：`docs/superpowers/specs/b298.md`（已批准）、`docs/superpowers/specs/b298-contract.md`（冻结 26e2ab7fb5）

## 事实流水

- 2026-08-29 读 `docs/superpowers/specs/b298.md`：状态行「已批准（2026-08-29，用户原话「批准」）」，L3 轻档，明确「不扇出并行子卡」。
- 2026-08-29 读 `docs/superpowers/specs/b298-contract.md`：72 条原子断言（wire 1–13、agentd 14–23、client 24–31、CLI 32–41、收口 42–58、批处理 59–72），§6 含移交 plan 附区；交棒欠账含「全包 go test ./internal/proto ./internal/client ./internal/agentd ./cmd 退出 1，原文见 contract 台账」。
- 2026-08-29 契约头部状态行读数为「状态：**已批准**（引用 spec，2026-08-29）」，未见「已冻结」字样；正文自称「冻结随本提交落盘」。待与项目既有契约文档惯例比对后定性。
- 2026-08-29 `git status`：分支 `cards/B298-charter`，工作树干净；HEAD `26e2ab7fb5`（contract freeze commit），前一提交 `b0a1ac44ab`（spec）。
- 2026-08-29 Ticket 0 骨架实读：`internal/proto/gc.go` DTO 完整（5 类型）；`internal/agentd/gc.go` `Manager.GC`(36) 返回 `ErrGCUnwired`(22)、`handleGC`(52) 成功路径无写响应（503 空壳）；`internal/client/gc.go` `GCPreview`(35)/`GC`(75) 已含双 404 探测与 200 解码；`cmd/gc.go` `runGC`(57) 已接 client 与过旧降级，但 execute 失败非零退出（断言 40）未实现，`renderGC`(95) 人读细节待补。
- 2026-08-29 Ticket 0 测试现状：`internal/proto/gc_test.go#TestGCGoldenJSON`（wire 形状 + releasable_bytes 缺席/零）、`internal/client/gc_test.go#TestGCPostDouble404IsUnsupported`（探测顺序 + POST body force）、`cmd/gc_test.go#TestRunGCDegradesOnOldAgentd` + `TestRenderGCDistinguishesUnknownBytes`、`internal/agentd/gc_test.go#TestHandleGCTicket0`（503 空壳，须退役）。client 200 解码路径（断言 26/27）无测试锁定——归 T4 链路测试。
- 2026-08-29 符号核对（grep 实测行号）：manager.go `Done`:1387 / `Stop`:1502 / `compensateWorkspace`:1090；reclaim.go `Reclaim`:251 / `ReclaimList`:342 / 包内 `shortTaskID`；server.go GET/POST `/api/gc` 路由 :498-499、`root.Handle("/", s.auth(mux))` :617、`mux.Handle("/api/", api)` :600；store.go `ListTasks`:414；tempdir.go `TaskTmpDir`:18；proto.go `IsTerminal`:34 / `TerminalStates`:31；Manager 持 `m.cfg.DataDir` 与 `m.st`。
- 2026-08-29 收口提前 return 差异：`Done`/`Stop` 的工作树清理失败不提前 return（降级 progress 事件/日志后继续）；`compensateWorkspace` 失败分支提前 return——缓存删除插入点不得被截走 → 边界澄清 C-2。
- 2026-08-29 target.json 实核：contracts 数组 37 条；edge[22]=`d_orchestration→d_execution`（`TaskTmpDir` 走此在册面，manager.go 现状即 import internal/executor:62）；契约 §6 声称五条面=contracts[6][8][14][16][26] 全在册。
- 2026-08-29 包规模（架构法第三条判据 2 命中）：internal/agentd 非测试源文件 64、cmd 51（均 ≥40 无子包）——拆解稿 §3.0 显式回答「能圈出有界文件集」，不插竖切还债卡；gc 前缀家族两包各 2 文件 <3 无层内分解信号。
- 2026-08-29 `ReclaimListResp.Scanned` 既有语义=「本次体检过的终态任务总数」（proto/reclaim.go:61-62 注释）；`GCResp.Scanned` 无语义钉（金样本仅零值键）→ 拆解稿岔口 1，倾向对齐 reclaim 语义。
- 2026-08-29 契约状态位惯例比对：b229-contract「状态：已冻结（2026-08-25，提交 97dcaf96）·拆解期仅允许头部状态元数据与 §8 修订记录追加」、b239「contract 轮冻结稿」、b249「冻结状态：本提交随 target.json 与视图 diff 一并冻结」——B298 契约头部原只有「已批准」，属状态位失真。
- 2026-08-29 `git cat-file -t 377dbd7058` → commit（存在但不在任何分支，`git log --all | grep -c` = 0）；`git diff 377dbd7058 26e2ab7fb5` 仅契约台账 +1 行——台账所记冻结提交系 amend 前身，冻结内容与 HEAD 一致。
- 2026-08-29 `codegraph sym` 不接受 `file#Symbol` 查询形：对 `Manager.Done`/`TaskTmpDir`/`newTargetClient` 等全部返回「不在图中」，与 contract 台账第 15 行同款——锚点合法性以 `resolve --doc` 为准（contract 文档同形锚已全 ok）。
- 2026-08-29 决策：修正契约头部状态行为「已冻结（2026-08-29，提交 26e2ab7fb5）」并追加 §8 拆解期修订记录（C-0 状态位、C-1 TaskTmpDir 面归属、C-2 收口失败不抑制缓存删除、C-3 附区吸收落点、scanned 待拍板条）；依据=b229 先例 + 本节点「状态位确认回写/边界澄清回写」纪律；冻结正文 §1–§7 未动一字。
- 2026-08-29 契约全包测试失败定性材料：失败行为 `integration_test.go:972`（git couldn't create cache file …/xcrun_db-… Operation not permitted）与 `status_test.go`:299/339/352——与 Bash 沙箱拦写缓存目录形状一致，疑似环境失败；不归因，拆解稿岔口 2 交 implement 首步复跑定性。
- 2026-08-29 产出：`docs/superpowers/specs/b298-breakdown.md` 落盘（待拍板岔口 3 条；子系统 5 触及 + 1 只消费；ticket T1–T4；行为闭环 9 行；缺陷族 5+3 逐族；真机清单 6 条）。
- 2026-08-29 `codegraph resolve --repo . --view cards-B298-charter --doc docs/superpowers/specs/b298-breakdown.md` 退出码 0：20 个锚（ok 9、moved 11、坏锚 0）——moved 为图中记录行号与现状漂移（baseline 保鲜债，contract 节点同款，如 `Manager.Done` 锚 nodeId 命中但图记旧行号），无 error/miss，未修图。
- 2026-08-29 稿首格式微调：待拍板岔口清单紧跟状态行，卡/台账元数据行移至岔口清单之后（逐字遵守稿首格式纪律）。
- 2026-08-29 收尾 `git status --short`：`M docs/superpowers/specs/b298-contract.md`（头部状态行 + §8 修订记录）、新增 breakdown 与本台账；未 git commit——拍板即提交归协调者。
