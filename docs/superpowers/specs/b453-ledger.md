# B453 台账

## spec（2026-10-21，主会话）

- 来源：2026-10-21 B452 讨论中用户裁定（卡 note seq 29187）：主动探测而非派发失败被动读回；每个 agent 可多账号、账号可配代理；定时扫；状态 正常/低额度/无额度；claude 不做，先只做 opencode。追加裁定（同日）：一个载体只绑一个模型；opencode 载体可以用 codex 额度，故探测按账号 kind 选，不按载体 CLI。
- 卡：`handoff card move B453 spec`；分支 `git checkout -b cards/B453-charter-1 origin/main`（基线 fad1aadf）。
- 本卡起按全局规则「机械活交熟练工」做现状调查：W1 `code-investigate`（mimo@linux-01）读代码现状八项；W2 `code-investigate --receiver mini --here` 外部 provider 额度接口调研。
- 本机读数：mac-mini `~/.local/share/opencode/auth.json` 不存在（用户称已登 opencode-go，凭据位置待 W2）；linux-01 root 主 HOME auth.json 只有 `deepseek: api`。生产载体（只读）：mimo/dp/cline@linux-01 opencode-go 模型 online，mini@mac-mini opencode-go/deepseek-v4.1-flash standalone online，mbp@macbook-pro opencode/deepseek-v4.1-flash-free。
- W1 交回（任务 db010c7d，mimo@linux-01，基线 fad1aadf，exit 0）：完整报告入仓 `docs/superpowers/specs/b453-facts-code.md`。要点（现状读数，执行节点复核）：载体四态 `status.go:22-30`，ApplyDetect 顺序 不可达>限额>需登录>上线（`:106-119`）；quota 只由人工 detect 写入，roadmap 590-591 未做；Select/admitInto/AdmitFrozen/AdmitSeatCarrier/LaunchAdmit/AdmitCarrier 一律只认 online（quota 与离线同等跳过）；detect 只对 opencode 真跑回合（`driver.go:52`），失败只截 stderr 尾部（`:170-181`）；model 不解析 provider 前缀、原样 `-m` 透传（`driver.go:240-244`）；载体无专属 env，任务 env 按 executor 名映射（`manager.go:767`）；周期任务统一 ticker+ctx，组装在 `cmd/agentd.go:367-401`；Web `settings/SchedulingPage.tsx` + `api/scheduling.ts`，`GET /api/squads` 回 carriers/squads/running；registry 无 kind 白名单，纯新 kind 不升 ledgerSchemaVersion（`schema_version.go:19-22`）；roadmap 相关行 323/362/426/447/590/600/986。
- 熟练工旁见：最终文本含执行者的过程叙述前缀（「只读调查 8 项现状 — 正在…」）与协议尾注，与 B452 roadmap「尾注行」同源，待并入同一条。
- 范围裁定（2026-10-21 用户「可以，重建的新建一张卡，最后做」）：B453 = 派发时跳过低额度/无额度 + 只读任务撞额度自动换小队成员重跑；分支任务中途撞额度只标无额度、停下等人、工单写明原因。分支任务接管另建 B459（低优先级，排在 B453–B455 之后）。
