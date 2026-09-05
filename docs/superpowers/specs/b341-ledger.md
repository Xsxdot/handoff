# B341 台账

- 2026-09-05 B340 真机：verdict pass 后 `git push origin cards/B340-charter` 400 工作树已回收；`git ls-remote` 仍命中 `8f42fe3d`。
- 根因：`awaitNode` 在 Publish 之前 `cl.Done`。
- 实现：Await 只取报文；RunOnce 在 Publish 之后 defer FinishTask→Done。
- `go test ./internal/ledgerstep/ -count=1` ok 0.793s；`go build ./...` 绿。
- 变异：Await 成功后立刻 FinishTask（可编译）→ 顺序 `[await done publish]`，`TestNodeStepPublishesWorkBranchBeforeFinishTask` 红；已还原。
