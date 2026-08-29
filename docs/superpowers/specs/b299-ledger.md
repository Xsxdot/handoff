# B299 台账

- 2026-08-29：真机验收二次唤醒落「载体已更换」。读 `coordinatorRunner.Resume` 只传 CLI+SessionID；`launchRound` 返回 `Rebuilt: true` 写死；本机 `pool.For("local")` 未登记。用户「修吧」。开 B299，定级 L1。
- 红：SessionRef 尚无 HomeDir 时编译红；补字段后断言红——`TestWakeResumeCarriesIsolatedHome` 首次 Rebuilt=true；`TestWakeResumeOverlaysCurrentCarrierHome` Resume ref HOME 空；`TestIgnitionVerticalSlice` 同样误标；`TestResumeTurnRequestCarriesIsolatedHome` TurnRequest.HomeDir 空。
- 实现：SessionRef 加 HomeDir/Workdir/Model；launchRound 写入且 Rebuilt 跟 rebuild；Wake overlay 后 Resume；resumeTurnRequest 映射；本机 disciplineTargetCap 不走 target 池；回合开始日志加 home_dir。
- 绿：`go test ./internal/keystone ./internal/agentd ./internal/hostapi -count=1` ok（agentd 全包 65s）。`go build ./...` 通过。`gofmt -l` 改动文件空。
- 变异（均编译过、断言唯一、复原）：
  1. resumeTurnRequest HomeDir="" → TestResumeTurnRequestCarriesIsolatedHome 红。
  2. Rebuilt: true 写死 → TestWakeResumeCarriesIsolatedHome + TestIgnitionVerticalSlice 红。
  3. overlay 跳过 HomeDir → TestWakeResumeOverlaysCurrentCarrierHome 仍是 /old，红。
  4. 本机分支 `if false && isLocalMachine` → TestDisciplineTargetCapLocalDoesNotNeedPool panic 在 pool.For(nil)。

