# B323 台账

- 2026-09-04：B156.3 真机验收。探针 B321 coordinate 241s rebuilt=false output_bytes=1487，room read count=0。隔离 HOME `handoff status` first-run 新 token → 401。
- 2026-09-04：B265 房间 hi → 载体已更换 循环。resume 477ms stderr 38 字节 = `Error: Session not found`。热路径 B321 二次唤醒 rebuilt=false。
- 2026-09-04：GET /coordinator command=`opencode --session <id>` 无 HOME。无 HOME 跑 → Session not found；`HOME=/Users/sycm/.handoff/home/muse` 同样命令 TUI 能起。
- 2026-09-04：muse 隔离 HOME 无 AGENTS.md/skills；`copyMainCredential` 注释「不搬运技能/规则树」；probe_test 锁 skills 不存在。
- 2026-09-04：仓库 `repos/handoff/~/.handoff/home/muse` 约 167M。`expandTurnHomeDir` 失败原样返回。
- 2026-09-04：用户 OK bug-batch。主卡 B323，并入 B324–B327。排除 B328/B329/B330。wait 不改。工作树 `/Users/sycm/.handoff/worktrees/b323-home` 分支 `cards/B323-charter` 自 `origin/main` bcc424cbd。主仓 `fix/B322-restore-pump` 脏，未在那棵树上写 spec。
- 2026-09-04 现状读数（worktree HEAD）：
  - `copyMainCredential` 只拷表内相对路径，空目录才拷。
  - `attachLocator` 拼 `cli --session id`。
  - `keystone.Locate` miss 重建 ref 无 HomeDir。
  - `Wake` 会 overlay spec.HomeDir，故冷 Resume 日志里有 home_dir 仍 Session not found → 会话不在该 HOME 的 db。
  - `config.DefaultPath` = `UserHomeDir()/.handoff/config.yaml`。
- 2026-09-04 分流：架构级（隔离 HOME 供给+使用同一路径）走本卡 spec，不在排查现场改。不镜像 Output 进房间。
- 2026-09-04：独立审查 `docs/superpowers/reviews/b323-spec-review.md`。无 Critical，维持 L2。吸收 I1–I6 为 r1 即批准。I5 选叫机器人补缺失表内 CLI 凭据（已存在不覆盖）。
