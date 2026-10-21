# B461 台账

## spec（2026-10-22，主会话）

- 来源：用户问「收尾是不是也可以做成熟练工」，主会话拆为只读检查（本卡）与换版脚本（B462）；用户「可以，建两张卡，排在B453后面，如果B453做完我还没回来，你就自主继续推进这两张卡」。
- 分支 `cards/B461-charter-1` 自 origin/main d905c3ab，工作树在会话 scratchpad（避开主检出里 B453 实现者）。
- 现状：仓内熟练工定义只有 `skills/handoff/skilled/code-investigate.json`（receiver mimo、readonly）；生产 `template list` 有 code-investigate v1。
- 范围收窄：handoff 自身命令被权限门硬升级（B451/B460），换版后冒烟不归本卡；前端 vitest 缺 node_modules，不归本卡。
- 定级 L1，spec 即 plan。
