# B453 台账

## spec（2026-10-21，主会话）

- 来源：2026-10-21 B452 讨论中用户裁定（卡 note seq 29187）：主动探测而非派发失败被动读回；每个 agent 可多账号、账号可配代理；定时扫；状态 正常/低额度/无额度；claude 不做，先只做 opencode。追加裁定（同日）：一个载体只绑一个模型；opencode 载体可以用 codex 额度，故探测按账号 kind 选，不按载体 CLI。
- 卡：`handoff card move B453 spec`；分支 `git checkout -b cards/B453-charter-1 origin/main`（基线 fad1aadf）。
- 本卡起按全局规则「机械活交熟练工」做现状调查：W1 `code-investigate`（mimo@linux-01）读代码现状八项；W2 `code-investigate --receiver mini --here` 外部 provider 额度接口调研。
- 本机读数：mac-mini `~/.local/share/opencode/auth.json` 不存在（用户称已登 opencode-go，凭据位置待 W2）；linux-01 root 主 HOME auth.json 只有 `deepseek: api`。生产载体（只读）：mimo/dp/cline@linux-01 opencode-go 模型 online，mini@mac-mini opencode-go/deepseek-v4.1-flash standalone online，mbp@macbook-pro opencode/deepseek-v4.1-flash-free。
