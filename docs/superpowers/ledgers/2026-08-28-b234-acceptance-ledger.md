# B234 acceptance 台账

- 2026-08-28 review-4 pass，卡在 acceptance。实现尖端 `cd0ab0d5d`（`cards/B234-charter-5`）。
- 本机 mac 复跑（生产 agentd 仍在跑，临时端口不是干净池）：
  - `go test ./internal/testhttp -count=1` → `ok ... 0.505s` 退出 0
  - `go test ./internal/ptyhost ./internal/ptyhost/engine ./internal/ptyhost/hostproc -count=1` → 三包 ok（23.4s / 13.2s / 11.2s）退出 0
  - `go test ./internal/agentd -count=1` → 退出 1；**没有** `can't assign requested address` / `EADDRNOTAVAIL` / `directory not empty`
  - 唯一 FAIL：`TestDisciplinesListFromLedgerOnly` `names = [charter-implement charter-review review]`。`origin/main` 的 `newLedgerEnv` 已种子 `discipline.NameReview="review"`（B271 夹具），该用例期望只有两个 charter 名。单跑同样红。**不是本卡回归，合 main 门（不得再是 EADDRNOTAVAIL）成立。**
- 变异（均先 `go build` 通过，再跑缝级测试；每发后 `git checkout --` 还原，工作树干净）：
  - M1 跳过 `prepareClientConn` 的 `setLinger` → `TestNewServerSetsLingerOnAcceptAndDefaultClientDial` 红（调用次数=1）
  - M2 `RetryDialContext` 循环改成 1 次（`MaxDialAttempts` 仍为 4）→ `TestRetryDialContextIsBoundedAndPreservesError` 红（dial 次数=1，期望 4）
  - M3 `Engine.Close` 不等 `exitedDone` → `TestCloseWaitsForReapAndLateTrap` 红（Close 在 late 前返回）
  - M4 `waitPtyhostExit` 改成立刻成功 → `TestClientOpenCloseWaitsForPtyhostAndShell` 与 `TestCloseDoesNotTreatControlEOFAsSuccess` 红
  - M5 `mirror_test` 去掉一处 `ConfigureClient` → `TestMirrorPoolClientsUseFixtureTransport` 红
- 真机：合 main 门就是本机 `go test ./internal/agentd`（上）。未重装生产 agentd（spec OOS）。点 × 关终端要用含 Close 修法的二进制，本期不部署。
- 无代码图视图 diff，跳过图对账。
- 审查落卡 findings：client.New / sockBuf / 日志 / late 夹具 / NewPool 均已在后续 implement 轮修完。第 3 轮超轮已 reset-node。
