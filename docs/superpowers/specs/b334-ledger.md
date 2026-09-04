# B334 台账

- 2026-09-04 对话冻死：HOME/凭据选填；空 HOME=主 HOME；空凭据=standalone；machine 拒在 Put；不迁 muse。
- 定级 L1，批准写入 spec 头部。
- 分支 `feat/b334-carrier-form-slim` 自 `origin/main` `f25dea6d`。
- `go test ./internal/scheduling/ ./internal/hostapi/ ./internal/keystone/` 绿。
- `go test ./internal/agentd/` 绿（约 96s）。
- `npx vitest run src/app/settings/SchedulingPage.test.tsx src/api/scheduling.fetch.test.ts src/api/contract.test.ts` 66 passed。
- `go build ./...` 绿。
- 变异：`checkKnownMachine` 取反后 `TestPutCarrierDefaultsCredentialAndRequiresKnownMachine` 红（linux-01 被拒），已还原。
