# B234 spec 台账

- 2026-08-28 用户「那就继续开下一批吧不要碰charter的卡」。原分组第五批 = B234 + B193。C7/C8/C11 不动。
- 工作树 `/Users/sycm/.handoff/worktrees/b234-macos-ports`，分支 `fix/b234-macos-test-ports`（从 `origin/main` @ `5d733488e`）。主仓工作区在 `cards/B284-handoff`，本卡不在那棵树上写。
- 卡 B234 待办→spec。B193 留待办，note「并入 B234」。源卡 B234 note：B230 验收本机 `go test ./internal/agentd/...` 偶发 `can't assign requested address`，失败集合每次不同，基线 `0c2deede` 同样红，linux-01 绿，TIME_WAIT 当时 138，机制当时未查清。
- 源卡 B193 note（2026-08-23 多轮）：身份是两族。族一临时端口耗尽（全量 40+ 处同文案，可伪装成 release 重试计数失败）；族二 PtyWS `TempDir RemoveAll: directory not empty`。族一量化：portrange 49152–65535、msl 15s、回收上限约 546 端口/秒。`httptest.NewServer` 形态已对，不是「该改成 httptest」。复现 `go test ./internal/agentd/ -count=12 -run TestPtyWS` 是池子已空条件下的确定性。族二走查：`Host.Close` 把 EOF/超时当成功；`Host.Close` 返回 ≠ 子进程已退出。当年「不派 linux-01」因为沙箱 `/tmp` 只读；B202 已完成 `ptytestroot`，该理由作废。
- B202 / B186 已完成，短路径根目录不在本卡。
- 代码（`5d733488e`）：`newTestAgentdEnvWithCfg` 已是 httptest + Cleanup Close；agentd 包内几乎无 `t.Parallel`。`Host.Close` 发 CtrlKill 后读一帧，EOF/超时当成功（`internal/ptyhost/client.go`）。`stopNow` 关连接不杀 PTY（`hostproc.go`）。`Engine.Close` SIGTERM 后立刻返回，SIGKILL 异步 `termGrace=2s`，注释写 DELETE 不该挂 2 秒。`Host.Open` 的 `cmd.Wait` 通道成功后丢掉。`shutdownPtySessions` 总预算 2s。`pty_ws_test.go` HOME=TempDir + base_kind home。
- 图：`codegraph who-calls n_ptyhost_Host_Close` 命中 `handleDeletePtySession` / `pumpPtyUplink` / `DELETE /api/pty/sessions/{id}` / `closePtySessionsForStop`。测试 Cleanup 不在图里。`sym Host.Close` 在 `d_sessions`。测试夹具无图 → 图覆盖债。
- 本机 agentd `3ae31175acad+dirty2` 已运行数小时、有 PTY 会话，符合「验收时本机不是干净端口池」。linux-01 agentd 仍 `016aef7e`；linux-01 上 B281/B282 在 review，B284 在 plan。本卡不碰它们。
- 无前端页面形态，不走原型。
- 定级 L2：测试夹具 + 兑现 Close 已写的收摊语义。废止 Engine.Close「DELETE 不该挂 2 秒」是产品注释，不是跨子系统契约。
- 弃选：Skip 这族错误、只靠 `-p 1`、全仓 Unix 套接字、测试 sleep / 重试 RemoveAll、Close 双语义、本期改完 115 处 httptest、碰 charter 卡。
- 族二红回路：HOME trap EXIT 写文件，Close 后立刻 RemoveAll。linux-01 能跑。
- 2026-08-28 独立审查 `01a0476e` 写入 `docs/superpowers/reviews/b234-spec-review.md`。总判修订后再批。Critical 3（Engine.Close 禁止第二次 cmd.Wait；红回路不得用 RemoveAll 当今天的红；Adopt 必须另有 PID/目录 Wait）+ Important 6。定级 L2 成立不抬。
- 协调者吸收 r1：C1–C3 与 I1–I6 全部写进正文。M1 改问题陈述表述。M2 落到实现决定（Open 注释）。M5 备注不回写 W4 plan。合入门拆成 linux-01 机制门 + mac `go test ./internal/agentd` 合 main 门。不抬 L3。
- 用户 2026-08-28「开下一批」+ 上批「老样子」授权批准 r1 并无人值守推进到合 main。
- 独立审查未用 `handoff card show`；who-calls 未跑 CLI，对的是 baseline.json。图覆盖债保留：测试夹具无图、`conn.Close` 假边、hostproc defer 的 eng.Close 无图。
