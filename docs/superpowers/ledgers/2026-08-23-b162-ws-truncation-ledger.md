# B162 WS 截断诊断 ledger

- 确定性变异 1 / 通过：仅删除 `warned` 分支的 `noteTruncationDiagnosed("warned")`，其余实现不变；`go test ./internal/agentd/ -run 'TestWSTruncation' -count=1` 原始输出如下，两个用例均因等待信号超时失败，而非日志断言失败：

  ```text
  --- FAIL: TestWSTruncationWarnsOnRealGap (30.14s)
      ws_regression_round2_test.go:324: 等截断诊断完成超时；日志尾部：.79.27.99 127.0.0.1 ::1 fd7a:115c:a1e0::6534:1b65 localhost]"
          time=2026-08-23T00:18:37.631+08:00 level=DEBUG msg="WS 重放开始" task=task-ws-truncate from_seq=0 replays=5 store_max=20
          time=2026-08-23T00:18:37.631+08:00 level=INFO msg="WS 连接建立" task=task-ws-truncate from_seq=0 replayed=5
          time=2026-08-23T00:18:37.631+08:00 level=DEBUG msg="WS 重放归并完成" task=task-ws-truncate live_merged=0
          time=2026-08-23T00:18:37.631+08:00 level=WARN msg="WS 补发窗口截断且缺口未由实时流补齐" task=task-ws-truncate from_seq=0 replayed=5 gap_total=15 gap_delivered=0 store_max=20
  --- FAIL: TestWSTruncationGapCountedPerTask (30.22s)
      ws_regression_round2_test.go:376: 等截断诊断完成超时；日志尾部：.79.27.99 127.0.0.1 ::1 fd7a:115c:a1e0::6534:1b65 localhost]"
          time=2026-08-23T00:19:07.857+08:00 level=DEBUG msg="WS 重放开始" task=task-ws-gapcount from_seq=0 replays=5 store_max=40
          time=2026-08-23T00:19:07.858+08:00 level=INFO msg="WS 连接建立" task=task-ws-gapcount from_seq=0 replayed=5
          time=2026-08-23T00:19:07.858+08:00 level=DEBUG msg="WS 重放归并完成" task=task-ws-gapcount live_merged=0
          time=2026-08-23T00:19:07.858+08:00 level=WARN msg="WS 补发窗口截断且缺口未由实时流补齐" task=task-ws-gapcount from_seq=0 replayed=5 gap_total=15 gap_delivered=0 store_max=40
  FAIL
  FAIL	github.com/Xsxdot/handoff/internal/agentd	60.373s
  ```

- 确定性变异 2 / 通过：恢复变异 1 后仅将 `gapTotal > deliveredInGap` 临时改为恒假，使诊断走 `covered` 分支；同一测试命令原始输出如下，两个用例均因 verdict 非 `warned` 失败：

  ```text
  --- FAIL: TestWSTruncationWarnsOnRealGap (0.16s)
      ws_regression_round2_test.go:321: 截断诊断跑完了但判定是 "covered"，期望 warned；日志尾部：��" hosts="[10.99.0.59 100.79.27.99 127.0.0.1 ::1 fd7a:115c:a1e0::6534:1b65 localhost]"
          time=2026-08-23T00:19:52.660+08:00 level=DEBUG msg="WS 重放开始" task=task-ws-truncate from_seq=0 replays=5 store_max=20
          time=2026-08-23T00:19:52.661+08:00 level=INFO msg="WS 连接建立" task=task-ws-truncate from_seq=0 replayed=5
          time=2026-08-23T00:19:52.661+08:00 level=DEBUG msg="WS 重放归并完成" task=task-ws-truncate live_merged=0
          time=2026-08-23T00:19:52.661+08:00 level=DEBUG msg="WS 补发窗口截断但缺口已由实时流补齐" task=task-ws-truncate replayed=5 gap_total=15 store_max=20
  --- FAIL: TestWSTruncationGapCountedPerTask (0.30s)
      ws_regression_round2_test.go:373: 截断诊断跑完了但判定是 "covered"，期望 warned；日志尾部：��" hosts="[10.99.0.59 100.79.27.99 127.0.0.1 ::1 fd7a:115c:a1e0::6534:1b65 localhost]"
          time=2026-08-23T00:19:52.964+08:00 level=DEBUG msg="WS 重放开始" task=task-ws-gapcount from_seq=0 replays=5 store_max=40
          time=2026-08-23T00:19:52.964+08:00 level=INFO msg="WS 连接建立" task=task-ws-gapcount from_seq=0 replayed=5
          time=2026-08-23T00:19:52.964+08:00 level=DEBUG msg="WS 重放归并完成" task=task-ws-gapcount live_merged=0
          time=2026-08-23T00:19:52.964+08:00 level=DEBUG msg="WS 补发窗口截断但缺口已由实时流补齐" task=task-ws-gapcount replayed=5 gap_total=15 store_max=40
  FAIL
  FAIL	github.com/Xsxdot/handoff/internal/agentd	0.469s
  ```

- 确定性变异 3 / 通过：恢复全部实现后，`go test ./internal/agentd/ -run 'TestWSTruncation' -count=5` 原始输出为 `ok  	github.com/Xsxdot/handoff/internal/agentd	2.183s`；`go test ./internal/agentd/ -count=1` 原始输出为 `ok  	github.com/Xsxdot/handoff/internal/agentd	141.238s`。两次临时变异均已还原，`git diff --check` 实际无输出。Commit 范围：`682f2ae7^..682f2ae7`。
- 变异实验 / BLOCKED：临时在诊断块前插入 `time.Sleep(2 * time.Second)` 并恢复原 3 秒日志轮询；组合命令 `go test ./internal/agentd/ -run 'TestWSTruncation' -count=1` 原始输出：`--- FAIL: TestWSTruncationWarnsOnRealGap (30.11s)`；`    ws_regression_round2_test.go:316: 等待 seq=21 时读失败: failed to get reader: context deadline exceeded`；`FAIL`；`FAIL\tgithub.com/Xsxdot/handoff/internal/agentd\t32.296s`。独立运行第二条命令 `go test ./internal/agentd/ -run '^TestWSTruncationGapCountedPerTask$' -count=1` 原始输出：`ok  \tgithub.com/Xsxdot/handoff/internal/agentd\t2.173s`。计划要求两条旧用例均红，但第二条未红，故按计划停止；修改后绿实验未执行。临时延迟已移除，`git diff --check` 实际无输出。Commit 范围：工作区恢复后 ledger 记录提交。
- Task 1 / 完成裁决：spec 符合（新增测试专用 `onTruncationDiagnosed` 钩子与 nil-safe helper，error/warned/covered 三条诊断分支各调用一次，生产 nil 时行为不变）；代码质量符合（不改诊断判定与日志文案，回调位置在各分支日志之后）。验证：`gofmt -w internal/agentd/server.go` 实际完成；`go test ./internal/agentd/ -run 'TestWSTruncation' -count=1` 实际通过，原始输出 `ok  	github.com/Xsxdot/handoff/internal/agentd	0.239s`。修复轮次：0。Commit 范围：`HEAD^..HEAD`。
- Task 2 / 完成裁决：spec 符合（`newWSTestEnv` 注入容量为 4 的 `truncationDiagnosed` 通道与服务端钩子，两条截断用例改用已有 `wsDeadline(t, 10*time.Second)` 上下文等待并断言 `warned`，日志文案与 `gap_total=15` 断言保留）；代码质量符合（只改等待同步方式，不引入新挂钟常数，失败信息包含 verdict 与日志尾部）。验证：`gofmt -w internal/agentd/ws_regression_round2_test.go` 实际完成；`go test ./internal/agentd/ -run 'TestWSTruncation' -count=5` 实际通过，原始输出 `ok  	github.com/Xsxdot/handoff/internal/agentd	1.548s`。修复轮次：0。Commit 范围：`HEAD^..HEAD`。
