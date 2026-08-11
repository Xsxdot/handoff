# Handoff Desktop Resilience and Acceptance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把前三个 checkpoint 加固为可长期常开的桌面控制台，完成 mixed-version、cursor reset、远端恢复、多桌面并发、安全边界、macOS 包装和设计规格九个真实场景的最终证据。

**Architecture:** 保持 Desktop → Local agentd → Remote agentd 单拓扑。所有长连接以 capability + durable cursor 恢复；所有资源写入只在 Machine=`connected` 开放；多桌面共享 agentd 权威 command/operation ledger。自动化验证后，再在 macOS + 本机 agentd + 一台真实远端 Linux agentd + 真实 OpenCode 上执行最终验收。

**Tech Stack:** Go 1.26、TLS/HTTP/WS、SQLite retention、Electron/React、Vitest、Playwright multi-process、electron-builder、macOS、本机及远端 Linux agentd。

## Global Constraints

- 计划 01–03 completion gate 全部通过后开始；本计划只加固，不重写事实所有权。
- 不使用 Orca SSH 连接、SFTP、port-forward 或旧 remote runtime 做任何产品 fallback。真实验收可由操作者预先部署远端 agentd，但桌面运行时只能走 agentd peer protocol。
- 默认拒绝非 loopback 明文 transport；显式 `allow_insecure_private` 只用于用户确认的受保护私网开发模式，并在 UI/日志中标明，不可静默开启。
- 远端断开不是只读：只保留 catalog metadata；文件、TaskTUI、Terminal、Preview 和命令全部不可用。
- 多桌面冲突由服务端 command ID、operation ID、ticket expected version 和 canonical result 处理；renderer 不猜赢家。
- 每个代码任务先写失败测试；完成前补结构化日志、职责/边界头注释、导出项文档和复杂恢复原因注释。任何证据、日志、trace、数据库 dump 都先脱敏。
- 只有九个场景均有真实证据、当前 commit 全套测试通过、独立审阅通过后，才可声明第二阶段完成；不以“mock 已过”或“代码看起来正确”替代。

---

### Task 1: 固化 protocol version、capability 协商和 mixed-version 契约

**Files:**
- Create: `internal/peer/version.go`
- Create: `internal/peer/version_test.go`
- Create: `internal/peer/capabilities.go`
- Create: `internal/peer/capabilities_test.go`
- Create: `internal/peer/testdata/v1-core-hello.json`
- Create: `internal/peer/testdata/v1-core-machine-event.json`
- Create: `internal/agentd/capability_server_test.go`
- Create: `desktop/src/shared/handoff/capabilities.ts`
- Create: `desktop/src/shared/handoff/capabilities.test.ts`
- Modify: `internal/peer/protocol.go`
- Modify: `internal/peer/client.go`
- Modify: `internal/peer/supervisor.go`
- Modify: `internal/agentd/peer_server.go`
- Modify: `internal/agentd/desktop_server.go`
- Modify: `desktop/src/shared/handoff/contracts.ts`

**Interfaces:**
- Consumes: Plans 01–03 peer hello, resource/task capabilities, Git command runner, and current public fixtures.
- Produces: `DesktopProtocolVersion`, `MinimumCompatibleVersion`, versioned capability set/negotiation result, mixed-version fixtures, and owner-scoped Git behavior-probe cache.

- [ ] 写红灯矩阵：current client ↔ v1-core host、v1-core client ↔ current host；新 optional JSON field 可忽略；缺核心 capability 标 incompatible；新 frame kind 未协商不能发送；已分配的 kind/version 不复用。

- [ ] 写 Git capability 红灯：2.25 baseline path；preferred 较新命令被拒后窄判定 fallback；缓存按 machine/owner 隔离；升级后到期重探，不仅解析 `git --version`。

- [ ] 运行红灯：

  ```bash
  go test ./internal/peer ./internal/agentd ./internal/machineauthority -run 'Version|Capability|Mixed|GitFallback'
  (cd desktop && corepack pnpm exec vitest run --config config/vitest.config.ts src/shared/handoff/capabilities.test.ts src/shared/handoff/contracts.test.ts)
  ```

- [ ] 固定 `DesktopProtocolVersion=1` 和独立 capability version：`catalog`、`machine_events`、`files`、`git`、`pty`、`preview`、`task_frames`、`task_commands`、`artifacts`。协议 version 只在 wire 破坏性变化时 bump；功能增长优先 capability。

- [ ] hello 响应同时返回 `protocol_version`、`minimum_compatible_version`、capabilities。Supervisor 只在本阶段核心能力全满足后转 connected；缺能力保留 catalog metadata 并标 incompatible，写操作关闭。

- [ ] 为当前 wire 保存最小 v1 fixture 和 scripted peer；测试覆盖 optional field strip、缺字段 fallback、content semantic 变化。不要依赖未来 Git tag 才有 cross-version 测试。

- [ ] Git capability cache 遵循 owner machine 隔离；实现 first fallback、subsequent skip、concurrent probe coalescing、TTL re-probe 测试。

- [ ] 日志记录 local/remote protocol、negotiated capability map、missing core keys、Git probe/fallback/TTL；不记录 endpoint userinfo 或 secret。

- [ ] 补齐职责头、导出文档和“optional field 何时安全”“新 kind 必须协商”“行为探测优于版本字符串”的原因注释。

- [ ] 运行绿灯：

  ```bash
  gofmt -w internal/peer internal/agentd/capability_server_test.go internal/agentd/peer_server.go internal/agentd/desktop_server.go internal/machineauthority
  go test ./internal/peer ./internal/agentd ./internal/machineauthority
  (cd desktop && corepack pnpm exec vitest run --config config/vitest.config.ts src/shared/handoff)
  (cd desktop && corepack pnpm typecheck)
  ```

- [ ] Commit:

  ```bash
  git add internal/peer internal/agentd internal/machineauthority desktop/src/shared/handoff
  git commit -m "feat: negotiate handoff protocol capabilities"
  ```

### Task 2: 实现 durable cursor retention、CURSOR_EXPIRED 和原子 snapshot reset

**Files:**
- Create: `internal/store/retention.go`
- Create: `internal/store/retention_test.go`
- Create: `internal/controlplane/retention.go`
- Create: `internal/controlplane/retention_test.go`
- Modify: `internal/store/machine_events.go`
- Modify: `internal/store/control_events.go`
- Modify: `internal/store/task_frames.go`
- Modify: `internal/taskview/snapshot.go`
- Modify: `internal/agentd/control_stream.go`
- Modify: `internal/agentd/task_frame_stream.go`
- Modify: `internal/agentd/peer_server.go`
- Modify: `desktop/src/main/handoff/agentd-client.ts`
- Modify: `desktop/src/main/handoff/task-client.ts`
- Modify: `desktop/src/renderer/src/features/handoff/catalog/catalog-store.ts`
- Modify: `desktop/src/renderer/src/features/handoff/tasks/task-session-store.ts`

**Interfaces:**
- Consumes: durable control/machine/task streams, bootstrap/session snapshots, and Main reconnect clients.
- Produces: retention floors, `410 CURSOR_EXPIRED {stream,floor,current}`, safe compaction, and atomic CatalogStore/TaskSessionStore snapshot replacement.

- [ ] 写 store 红灯测试：按事件数和时间 retention；保留 floor/through cursor；active peer cursor 尚未追上时不误删 machine events；TaskFrame 只有 snapshot 覆盖后才可压缩；compaction crash 不产生 floor 超过可用 snapshot 的状态。

- [ ] 写 HTTP/WS 红灯测试：after < floor 返回 `410 CURSOR_EXPIRED`，details 含 stream/floor/current 不含内部内容；bootstrap/session snapshot 后订阅 through cursor 无窗口；reset 时旧 reducer 状态原子替换，不拼接残历史。

- [ ] 运行红灯：

  ```bash
  go test ./internal/store ./internal/controlplane ./internal/taskview ./internal/agentd -run 'Retention|CursorExpired|SnapshotReset'
  (cd desktop && corepack pnpm exec vitest run --config config/vitest.config.ts src/main/handoff src/renderer/src/features/handoff/catalog src/renderer/src/features/handoff/tasks)
  ```

- [ ] retention 默认值放配置：control 7d/100k、machine 7d/100k per machine、TaskFrame snapshot 后保留 10k/7d；测试注入小阈值。配置验证拒绝非正值和 floor 不安全组合。

- [ ] compactor 先确认 durable snapshot through seq，再删 frame；control snapshot 由 bootstrap 可完整重建；machine snapshot 必须由 owner authoritative snapshot 重建。每轮用小批事务，避免长锁。

- [ ] Main 收到 CURSOR_EXPIRED 不无限重连：catalog 调 bootstrap，task 调 session；renderer store 用单次 `replace` 发布，避免用户看到中间半状态。

- [ ] 日志记录 stream/floor/after/current、deleted count、snapshot through、reset reason/duration；不记录 frame payload。补齐职责头、导出文档和“先 snapshot 后 compact”“reset 不拼旧历史”的原因注释。

- [ ] 运行绿灯与 race：

  ```bash
  gofmt -w internal/store internal/controlplane/retention.go internal/controlplane/retention_test.go internal/taskview/snapshot.go internal/agentd/control_stream.go internal/agentd/task_frame_stream.go internal/agentd/peer_server.go
  go test -race ./internal/store ./internal/controlplane ./internal/taskview ./internal/agentd
  go test ./...
  (cd desktop && corepack pnpm exec vitest run --config config/vitest.config.ts src/main/handoff src/renderer/src/features/handoff)
  (cd desktop && corepack pnpm typecheck)
  ```

- [ ] Commit:

  ```bash
  git add internal/store internal/controlplane internal/taskview internal/agentd desktop/src/main/handoff desktop/src/renderer/src/features/handoff
  git commit -m "feat: recover expired desktop stream cursors"
  ```

### Task 3: 加固远端连接状态机、重连顺序和已打开资源恢复

**Files:**
- Create: `internal/peer/state_machine.go`
- Create: `internal/peer/state_machine_test.go`
- Create: `internal/peer/backoff.go`
- Create: `internal/peer/backoff_test.go`
- Create: `internal/peer/recovery_integration_test.go`
- Modify: `internal/peer/supervisor.go`
- Modify: `internal/resourcegateway/router.go`
- Modify: `internal/agentd/file_stream.go`
- Modify: `internal/agentd/pty_server.go`
- Modify: `internal/agentd/preview_server.go`
- Modify: `internal/agentd/task_frame_stream.go`
- Modify: `desktop/src/main/handoff/agentd-client.ts`
- Modify: `desktop/src/main/handoff/resource-client.ts`
- Modify: `desktop/src/main/handoff/pty-client.ts`
- Modify: `desktop/src/main/handoff/task-client.ts`
- Modify: `desktop/src/renderer/src/features/handoff/components/WorkbenchShell.tsx`

**Interfaces:**
- Consumes: Task 1 capability result, Task 2 cursors/reset, and all open resource subscription descriptors.
- Produces: per-Machine generation-fenced `StateMachine.Transition`, staged `Authenticate→Negotiate→CatchUp→Reconcile→Reattach→Connected`, and explicit UI recovery outcomes.

- [ ] 写红灯状态机测试，严格只允许：unavailable→connecting→reconciling→connected，协商失败→incompatible，网络失败→unavailable；写操作只有 connected；状态变化不改 TaskState。

- [ ] 写恢复集成红灯：断线期间产生 machine events，重连 catch-up 后 Reconcile 修复漏差，再 reattach file watch/PTY/task frame/preview；connected 只在全部核心步骤成功后发布；任何一步失败回 unavailable/reconciling 并保留 cursor。

- [ ] 写 UI 红灯：connecting/reconciling 有清晰阶段；unavailable 时遮住所有现场；PTY owner 已结束则 tab ended；Preview 需重新建 session 时明确提示；恢复后 Workspace tab/layout 不丢。

- [ ] 运行红灯：

  ```bash
  go test ./internal/peer ./internal/resourcegateway ./internal/agentd -run 'StateMachine|Recovery|Reconnect'
  (cd desktop && corepack pnpm exec vitest run --config config/vitest.config.ts src/main/handoff src/renderer/src/features/handoff)
  ```

- [ ] Supervisor 每机器一个 generation；旧 generation 的迟到事件/响应必须丢弃，不能覆盖新连接状态。Backoff exponential + full jitter，有上限；成功稳定窗口后重置。

- [ ] 恢复顺序必须编码成可测试 stage，而不是散落 callbacks：Authenticate → Negotiate → CatchUp → Reconcile → Reattach → Connected。Reattach 失败不能伪装 connected。

- [ ] file/task subscriptions 用 cursor 重建；PTY 校验 session+incarnation；Preview 旧 nonce owner 失效时创建需要用户显式动作，不能偷偷改 URL 指向新 session。

- [ ] 日志记录 machine/generation/stage/attempt/backoff/from-to cursor/reattach counts/failure cause；成功 stable reset 也记录。补齐职责头、导出文档和 generation fencing/connected gate 的原因注释。

- [ ] 运行绿灯与断线 stress：

  ```bash
  gofmt -w internal/peer internal/resourcegateway/router.go internal/agentd/file_stream.go internal/agentd/pty_server.go internal/agentd/preview_server.go internal/agentd/task_frame_stream.go
  go test -race ./internal/peer ./internal/resourcegateway ./internal/agentd
  go test ./...
  (cd desktop && corepack pnpm exec vitest run --config config/vitest.config.ts src/main/handoff src/renderer/src/features/handoff)
  (cd desktop && corepack pnpm typecheck)
  ```

- [ ] Commit:

  ```bash
  git add internal/peer internal/resourcegateway internal/agentd desktop/src/main/handoff desktop/src/renderer/src/features/handoff
  git commit -m "feat: recover remote handoff resources by generation"
  ```

### Task 4: 加固 transport、secret、path、preview 和日志脱敏边界

**Files:**
- Create: `internal/peer/transport_policy.go`
- Create: `internal/peer/transport_policy_test.go`
- Create: `internal/peer/tls_config.go`
- Create: `internal/peer/tls_config_test.go`
- Create: `internal/security/redaction.go`
- Create: `internal/security/redaction_test.go`
- Create: `internal/security/security_integration_test.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/peer/client.go`
- Modify: `internal/machineauthority/authorized_root.go`
- Modify: `internal/preview/proxy.go`
- Modify: `internal/logx/logx.go`
- Modify: `desktop/src/main/handoff/logger.ts`
- Modify: `desktop/src/main/handoff/agentd-config.ts`
- Modify: `desktop/src/preload/handoff.ts`
- Modify: `desktop/src/shared/handoff/contracts.test.ts`

**Interfaces:**
- Consumes: configured peer targets, authorized Workspace roots, Preview proxy, agentd/desktop loggers, and preload contracts.
- Produces: `TransportPolicy.Validate`, shared TLS transport factory, `security.Redact`, DNS-rebinding-safe Preview dial policy, and secret-free public errors/logs.

- [ ] 写 transport 红灯：loopback HTTP 允许；非 loopback HTTP 默认拒绝；显式受保护私网模式只允许 RFC1918/Tailscale CGNAT 地址且 UI 有 insecure marker；HTTPS 校验证书/hostname；可配置 CA/pinned SPKI；AUTH_FAILED 不回显 token。

- [ ] 写安全红灯：`../`、encoded traversal、symlink swap、absolute Windows/Unix path、NUL；Preview SSRF 到非 loopback/metadata IP；renderer/preload contract 不含 token/secretRef；日志 redactor 清除 Authorization、token、env、userinfo、answer/content 字段。

- [ ] 运行红灯：

  ```bash
  go test ./internal/peer ./internal/security ./internal/machineauthority ./internal/preview ./internal/config -run 'Transport|TLS|Redact|Traversal|SSRF'
  (cd desktop && corepack pnpm exec vitest run --config config/vitest.config.ts src/main/handoff src/preload src/shared/handoff/contracts.test.ts)
  ```

- [ ] Config 为 Target 增加可选 TLS：`ca_file`、`server_name`、`spki_sha256`；受保护私网明文必须 `allow_insecure_private: true`，不能由默认值启用。配置错误在 agentd 启动期 fail-closed。

- [ ] 所有 peer HTTP/WS 使用同一 `http.Transport/TLSConfig` 工厂，设置 dial/TLS/header/idle timeout 和连接池上限；禁止跳过证书验证。

- [ ] `security.Redact` 在日志入口做 key/value 与 URL userinfo 双层清理；测试扫描 `agentd.log`、desktop log 和 error JSON，确认固定 secret fixture 不出现。

- [ ] Preview 只接受解析后所有地址均为 loopback的 host，防 DNS rebinding；建立连接后校验 remote addr；重定向也重新执行 policy。

- [ ] `AuthorizedRoot` 增加并发 symlink swap 测试；最终 open/write 仍由 `os.Root` handle 完成。补齐职责头、导出文档和 fail-closed/private-mode/DNS-rebinding 原因注释。

- [ ] 运行绿灯：

  ```bash
  gofmt -w internal/peer internal/security internal/machineauthority/authorized_root.go internal/preview/proxy.go internal/config/config.go internal/config/config_test.go internal/logx/logx.go
  go test -race ./internal/peer ./internal/security ./internal/machineauthority ./internal/preview ./internal/config
  go test ./...
  (cd desktop && corepack pnpm exec vitest run --config config/vitest.config.ts src/main/handoff src/preload src/shared/handoff)
  (cd desktop && corepack pnpm typecheck)
  ```

- [ ] Commit:

  ```bash
  git add internal/peer internal/security internal/machineauthority/authorized_root.go internal/preview/proxy.go internal/config internal/logx desktop/src/main/handoff desktop/src/preload desktop/src/shared/handoff
  git commit -m "security: harden agentd resource transport boundaries"
  ```

### Task 5: 验证多桌面实例、命令/Operation 竞争和生命周期独立

**Files:**
- Create: `internal/taskcommand/concurrency_test.go`
- Create: `internal/controlplane/operation_concurrency_test.go`
- Create: `internal/agentd/multi_client_integration_test.go`
- Modify: `internal/taskcommand/service.go`
- Modify: `internal/controlplane/project_service.go`
- Modify: `internal/store/task_commands.go`
- Modify: `internal/store/operations.go`
- Create: `desktop/src/shared/handoff-mode.ts`
- Create: `desktop/src/shared/handoff-mode.test.ts`
- Create: `desktop/src/main/handoff/window-registry.ts`
- Create: `desktop/src/main/handoff/window-registry.test.ts`
- Modify: `desktop/src/main/startup/single-instance-lock.ts`
- Modify: `desktop/src/main/startup/single-instance-lock.test.ts`
- Modify: `desktop/src/main/index.ts`
- Modify: `desktop/src/renderer/src/main.tsx`
- Modify: `desktop/src/main/handoff/register-handoff-ipc.ts`
- Modify: `desktop/src/main/handoff/register-handoff-ipc.test.ts`
- Create: `desktop/tests/e2e/handoff-multi-instance.spec.ts`
- Create: `desktop/config/scripts/run-handoff-multi-instance-e2e.mjs`
- Modify: `desktop/package.json`

**Interfaces:**
- Consumes: Plan 01 Operation ledger, Plan 03 task command/ticket ledger, Orca single-instance hook, and multiple Handoff renderer windows sharing one local agentd.
- Produces: shared `isHandoffWorkbenchMode`, `HandoffWindowRegistry.create/list/remove`, second-launch→new-window behavior with one safe Electron profile, per-`task_id`/`operation_id` concurrency guarantees, canonical duplicate/conflict responses, and `pnpm test:e2e:handoff-multi-instance`.

- [ ] 写 Go race 红灯：100 个并发 approve 对同 Ticket 只有一个 adapter call；command canonical result 一致；并发相同 project operation 每目标只 clone/register 一次；不同 command/operation 不被全局锁串死。

- [ ] 写多窗口产品红灯：第一次启动获得 Electron 单实例锁；同一用户再次启动不只聚焦旧窗口，而是由首进程创建第二个 Handoff BrowserWindow；两窗口连接同一 local agentd、同时打开同 Task、同时批准，一个 resolved，另一个 409/权威结果；关闭全部窗口后 executor/PTY/Operation 继续，重开从 agentd 恢复。

- [ ] 写 WindowRegistry/Main 红灯：每个 window/webContents 有独立 IPC subscription scope、selected Workspace 和 TaskSession 引用；关闭一个窗口只清理该 sender；macOS activate 在无窗口时创建一个，有窗口时不无故复制；显式 legacy Orca 开发模式保持原先“聚焦旧窗口”语义。

- [ ] 运行红灯：

  ```bash
  go test -race ./internal/taskcommand ./internal/controlplane ./internal/agentd -run 'Concurrent|MultiClient'
  (cd desktop && corepack pnpm exec vitest run --config config/vitest.config.ts src/shared/handoff-mode.test.ts src/main/handoff/window-registry.test.ts src/main/handoff/register-handoff-ipc.test.ts src/main/startup/single-instance-lock.test.ts)
  (cd desktop && corepack pnpm test:e2e:handoff-multi-instance)
  ```

- [ ] 锁粒度按 `task_id` / `operation_id`；数据库唯一约束和 CAS 是最终裁决，进程 mutex 只减少重复工作，不能是唯一正确性来源。

- [ ] 保留 Electron `requestSingleInstanceLock`，避免两个 Main 进程并发写同一 userData；Handoff 模式的 `second-instance` callback 调 `HandoffWindowRegistry.create()`，Orca legacy/serve 模式仍走既有 activation/focus。不得用随机 userData 绕锁来伪装产品多开。

- [ ] `HandoffWindowRegistry` 固定公开 `create(): BrowserWindow`、`list(): readonly BrowserWindow[]`、`remove(webContentsId: number): void`；内部用 webContents ID 索引并在 `closed/render-process-gone` 清理 sender 订阅。Registry 只管理窗口生命周期，不持有 Project/Workspace/Task 业务状态。

- [ ] `shared/handoff-mode.ts` 导出 `isHandoffWorkbenchMode(options: { env: NodeJS.ProcessEnv; isDev: boolean }): boolean`，默认返回 true，仅开发态且 `HANDOFF_LEGACY_ORCA_UI=1` 返回 false；packaged 永远是 Handoff。Plan 01 renderer fallback、single-instance callback 和 Plan 04 产品身份测试统一使用该函数，禁止三处各读不同环境变量。

- [ ] command/operation duplicate 请求返回同一 canonical projection 和原始 accepted/delivered 状态；不同 payload hash 返回冲突，不覆盖首个请求。

- [ ] E2E 启动首个 Electron，再以正常产品命令二次启动并等待首进程出现第二个窗口；两个窗口共享同一 userData 和 fake/real local agentd。另用 Go multi-client test 覆盖跨进程 HTTP 客户端。失败保存两个窗口截图、trace、command journal 和 agentd DB 查询。

- [ ] 日志记录 client/window correlation、command/operation winner/conflict、executor call count；不记录回答内容。补齐职责头、导出文档和“DB CAS 才是最终裁决”“桌面关闭不拥有任务生命周期”的原因注释。

- [ ] 运行绿灯：

  ```bash
  go test -race ./internal/taskcommand ./internal/controlplane ./internal/agentd
  go test ./...
  (cd desktop && corepack pnpm exec vitest run --config config/vitest.config.ts src/shared/handoff-mode.test.ts src/main/handoff src/main/startup/single-instance-lock.test.ts)
  (cd desktop && corepack pnpm test:e2e:handoff-multi-instance)
  (cd desktop && corepack pnpm typecheck)
  ```

- [ ] Commit:

  ```bash
  git add internal/taskcommand internal/controlplane internal/agentd internal/store desktop/src/shared/handoff-mode.ts desktop/src/shared/handoff-mode.test.ts desktop/src/main/handoff/window-registry.ts desktop/src/main/handoff/window-registry.test.ts desktop/src/main/handoff/register-handoff-ipc.ts desktop/src/main/handoff/register-handoff-ipc.test.ts desktop/src/main/startup/single-instance-lock.ts desktop/src/main/startup/single-instance-lock.test.ts desktop/src/main/index.ts desktop/src/renderer/src/main.tsx desktop/tests/e2e/handoff-multi-instance.spec.ts desktop/config/scripts/run-handoff-multi-instance-e2e.mjs desktop/package.json
  git commit -m "test: enforce multi-desktop command concurrency"
  ```

### Task 6: 完成可观测性、边界容量和架构守卫

**Files:**
- Create: `internal/observability/context.go`
- Create: `internal/observability/context_test.go`
- Create: `internal/agentd/health_server.go`
- Create: `internal/agentd/health_server_test.go`
- Create: `internal/agentd/capacity_integration_test.go`
- Create: `desktop/src/main/handoff/open-logs.ts`
- Create: `desktop/src/main/handoff/open-logs.test.ts`
- Create: `desktop/src/renderer/src/features/handoff/components/ConnectionDiagnostics.tsx`
- Create: `desktop/src/renderer/src/features/handoff/components/ConnectionDiagnostics.test.tsx`
- Create: `desktop/config/scripts/check-handoff-architecture.mjs`
- Create: `desktop/config/scripts/check-handoff-architecture.test.mjs`
- Modify: `desktop/package.json`
- Modify: `internal/agentd/server.go`

**Interfaces:**
- Consumes: all Phase 2 server/Main/renderer operations and stream limits.
- Produces: correlation context helpers, `GET /v1/health`, fixed capacity limits, ConnectionDiagnostics/open-known-logs UI, and required `check:handoff-architecture` script.

- [ ] 写日志 context 红灯：HTTP/WS/peer/reconcile/operation/file/PTY/task command/frame 自动携带适用 IDs 和 correlation ID；错误有 cause；成功路径有终态；redaction fixture 不出现。

- [ ] 写容量红灯：100 projects/20 machines/1000 workspaces/500 active task summaries bootstrap 有界；10 后台任务不向未打开 TaskTUI 推 frame；慢 control/task/PTY client 主动断开；message batching 和 artifact range 保持内存上限。

- [ ] 写架构脚本红灯，AST/导入扫描拒绝：Handoff→Orca SSH/old project/worktree persistence/runtime terminal；TaskTUI→xterm/pty/executor-specific；renderer→node fs/net/child_process；preload contract→token/secret。

- [ ] 运行红灯：

  ```bash
  go test ./internal/observability ./internal/agentd -run 'LogContext|Capacity|Health'
  (cd desktop && corepack pnpm exec vitest run --config config/vitest.config.ts src/main/handoff/open-logs.test.ts src/renderer/src/features/handoff/components/ConnectionDiagnostics.test.tsx)
  (cd desktop && corepack pnpm run check:handoff-architecture)
  ```

- [ ] `GET /v1/health` 返回 local agentd、DB、projector/peer summary 与 protocol version，不返回 token/path 内容。UI diagnostics 显示可行动状态和“打开本机 agentd 日志”。

- [ ] Open logs 只能打开已知 agentd/desktop log path，不接受 renderer 任意路径。成功/失败由 Main `HandoffLogger` 记录。

- [ ] 为每条 stream 设明确 replay batch、live buffer、max frame/body、deadline；容量测试用 metrics/heap delta 断言无无界增长，不只断言请求完成。

- [ ] 将 `check:handoff-architecture` 加入 `check:code-quality:changed` 或独立 required script；不得靠 code review 肉眼长期维护边界。

- [ ] 补齐职责头、导出文档和 correlation/有界断开/架构守卫原因注释；确认无 `fmt.Printf/print/console.log` 新日志。

- [ ] 运行绿灯：

  ```bash
  gofmt -w internal/observability internal/agentd/health_server.go internal/agentd/health_server_test.go internal/agentd/capacity_integration_test.go internal/agentd/server.go
  go test -race ./internal/observability ./internal/agentd
  go test ./...
  (cd desktop && corepack pnpm exec vitest run --config config/vitest.config.ts src/main/handoff src/renderer/src/features/handoff)
  (cd desktop && corepack pnpm run check:handoff-architecture)
  (cd desktop && corepack pnpm run check:code-quality:changed)
  (cd desktop && corepack pnpm run check:max-lines-ratchet)
  ```

- [ ] Commit:

  ```bash
  git add internal/observability internal/agentd desktop/src/main/handoff desktop/src/renderer/src/features/handoff desktop/config/scripts desktop/package.json
  git commit -m "feat: add handoff diagnostics and architecture gates"
  ```

### Task 7: 配置 Handoff 产品入口并产出可测试 macOS 包

**Files:**
- Create: `desktop/resources/icon-source/handoff.svg`
- Create: `desktop/docs/HANDOFF-DEVELOPMENT.md`
- Modify: `desktop/package.json`
- Modify: `desktop/config/electron-builder.config.cjs`
- Modify: `desktop/src/shared/app-identity.ts`
- Create: `desktop/src/shared/app-identity.test.ts`
- Modify: `desktop/src/main/index.ts`
- Modify: `desktop/src/renderer/src/main.tsx`
- Modify: `desktop/resources/icon-source/generate.sh`
- Create: `desktop/config/scripts/handoff-package-smoke.mjs`
- Create: `desktop/config/scripts/handoff-package-smoke.test.mjs`

**Interfaces:**
- Consumes: Plan 01 imported Electron build pipeline, HandoffApp entry, local agentd config contract, and upstream MIT attribution.
- Produces: Handoff product identity/assets, macOS `.app`, `HANDOFF-DEVELOPMENT.md`, and `handoff-package-smoke.mjs` using temporary HOME/config.

- [ ] 写 package contract 红灯：productName/bundle ID/executable/display title 为 Handoff；默认 renderer 是 HandoffApp；legacy Orca UI 只能显式开发 flag 打开；包内不包含真实 config/token；启动时 local agentd 不可用显示恢复页而不是白屏/crash。

- [ ] 写 packaged smoke 红灯：启动 `.app`、等待 ProjectTree/connection state、捕获 page errors/unhandled rejection、关闭；使用临时 HOME/config 和 fake agentd，不读真实用户配置。

- [ ] 运行红灯：

  ```bash
  (cd desktop && corepack pnpm exec vitest run --config config/vitest.config.ts config/scripts/handoff-package-smoke.test.mjs src/shared/app-identity.test.ts)
  ```

- [ ] 从已确认的原型视觉语言制作一个中性 Handoff 开发图标（深色圆角底 + 终端/交接抽象符号），以可编辑 SVG 为 source，再用现有 icon pipeline 生成平台资产；不得继续发布 Orca 商标图标。

- [ ] 更新 Electron builder metadata 为 Handoff 的独立 bundle ID/productName；保留上游 MIT attribution 和 `UPSTREAM.md`。本阶段不删除 Orca 旧源码。

- [ ] `HANDOFF-DEVELOPMENT.md` 写清：本阶段桌面要求本机 agentd 已运行；远端 agentd/target 由配置预置；无安装/配对 UI；如何启动 fake、本机、打包和收集日志。

- [ ] Main 启动 Handoff client 的关键阶段写结构化日志；local agentd unavailable 是可恢复产品状态，不是未处理异常。新文件补职责/边界头注释、导出文档和 legacy fallback 仅开发使用的原因注释。

- [ ] 构建和 smoke：

  ```bash
  (cd desktop && corepack pnpm typecheck)
  (cd desktop && corepack pnpm run build:desktop)
  (cd desktop && corepack pnpm run build:unpack)
  (cd desktop && node config/scripts/handoff-package-smoke.mjs)
  ```

- [ ] 验证 `.app` 的 Info.plist、可执行名、签名状态（本地 unsigned 可接受但要记录）和资源；把绝对产物路径写入后续 evidence，不提交构建产物。

- [ ] Commit:

  ```bash
  git add desktop/resources/icon-source desktop/docs/HANDOFF-DEVELOPMENT.md desktop/package.json desktop/config/electron-builder.config.cjs desktop/config/scripts/handoff-package-smoke.mjs desktop/config/scripts/handoff-package-smoke.test.mjs desktop/src/shared/app-identity.ts desktop/src/shared/app-identity.test.ts desktop/src/main/index.ts desktop/src/renderer/src/main.tsx
  git commit -m "build: package Handoff desktop for macOS validation"
  ```

### Task 8: 建立九场景自动化验收 harness 与证据模板

**Files:**
- Create: `tests/acceptance/harness.go`
- Create: `tests/acceptance/harness_test.go`
- Create: `tests/acceptance/scenarios_test.go`
- Create: `desktop/tests/e2e/handoff-phase2-acceptance.spec.ts`
- Create: `desktop/config/scripts/run-handoff-phase2-acceptance.mjs`
- Modify: `desktop/package.json`
- Create: `docs/superpowers/evidence/phase2-final-template.md`

**Interfaces:**
- Consumes: Plans 01–03 checkpoint scripts and Task 5 multi-instance runner.
- Produces: acceptance `Scenario{ID,Preconditions,Steps,Assertions,Artifacts,LogCorrelations,Cleanup,Result}`, nine fixed scenario registrations, `pnpm test:e2e:handoff-phase2-acceptance`, and evidence template.

- [ ] 先写 failing harness contract：每个场景必须登记 ID、preconditions、steps、assertions、artifact paths、log correlations、cleanup 和 result；缺任何项即测试失败，避免最终只留一张截图。

- [ ] 为九场景写 fake/localhost 自动化版本，覆盖 API/UI 编排和证据输出路径；明确标记 `automated`，不能标记 `real-remote`。

- [ ] 运行红灯：

  ```bash
  go test ./tests/acceptance
  (cd desktop && corepack pnpm test:e2e:handoff-phase2-acceptance)
  ```

- [ ] Harness 每次运行生成唯一 run ID 和临时根；记录 git commit、Go/Node/Electron/Handoff/OpenCode 版本、机器匿名 ID、开始/结束时间和退出码。清理只作用于 run-owned 路径/进程。

- [ ] 九个 scenario ID 固定：`project-persistence`、`clone-two-sides`、`external-git-push`、`workspace-linkage`、`files-and-terminal`、`remote-preview`、`task-ownership`、`structured-task-loop`、`disconnect-and-concurrency`。

- [ ] E2E 失败保存每个窗口 screenshot/trace、agentd log slice、desktop log slice、DB query、process evidence 和 sanitized wire journal；模板按同 ID 留槽位。

- [ ] 测试代码用结构化 harness 日志记录场景进入、外部调用、断言失败、cleanup 和成功结果；不记录 secret/content/answer/reasoning。新文件补职责/边界头注释、导出文档和“自动化不等于真实远端证据”的注释。

- [ ] 跑完整自动化：

  ```bash
  go test -race ./tests/acceptance ./internal/...
  go test ./...
  (cd desktop && corepack pnpm test:e2e:handoff-phase2-acceptance)
  (cd desktop && corepack pnpm typecheck)
  (cd desktop && corepack pnpm run check:handoff-architecture)
  ```

- [ ] Commit:

  ```bash
  git add tests/acceptance desktop/tests/e2e/handoff-phase2-acceptance.spec.ts desktop/config/scripts/run-handoff-phase2-acceptance.mjs desktop/package.json docs/superpowers/evidence/phase2-final-template.md
  git commit -m "test: add phase two acceptance harness"
  ```

### Task 9: 在真实 macOS + 本机 agentd + 远端 Linux agentd 上执行九场景

**Files:**
- Create: `docs/superpowers/evidence/phase2-final-real-machine.md`
- Create: `docs/superpowers/evidence/phase2-final-real-machine/manifest.json`
- Create: `docs/superpowers/evidence/phase2-final-real-machine/README.md`

**Interfaces:**
- Consumes: Task 7 macOS package, Task 8 harness, one local and one pre-provisioned remote agentd, test Git remote, and real OpenCode login state.
- Produces: one sanitized final real-machine report plus manifest/artifacts covering all nine scenario IDs and any separately committed root-cause fixes.

- [ ] 验收前确认外部条件，不满足就停止并明确报告缺口：macOS 包、本机 agentd、已预置的一台远端 Linux agentd、可用 HTTPS 或明确受保护私网模式、两个桌面 userData、测试 Git remote、真实 OpenCode 登录态。不要自行扩权部署或读取未授权凭据。

- [ ] 固定并记录 desktop/agentd commit 和版本；两台 agentd 使用同一协议 build；先跑 health/capability，确认 local/remote machine 最终 connected。

- [ ] 场景 1 `project-persistence`：Finder 添加本机已有目录 + 远端粘贴已有 path；重启桌面后 Project/Location/main Workspace/计数一致。证据含 UI、bootstrap 摘要、DB IDs。

- [ ] 场景 2 `clone-two-sides`：分别在本机和远端 clone，验证默认 path 可改；对同 operation ID 重试不重复 clone。证据含 Operation transitions、目录 inode/HEAD、调用计数。

- [ ] 场景 3 `external-git-push`：在普通终端外部创建 branch/worktree，桌面无需刷新出现；断线期间再改一次，重连 catch-up + Reconcile 修复。证据含 machine_seq/control_revision 和 UI。

- [ ] 场景 4 `workspace-linkage`：切换 main/worktree，验证 breadcrumb、标签组、右栏 root；返回旧 Workspace 标签/split 保留。证据含两个 Workspace ID 和截图。

- [ ] 场景 5 `files-and-terminal`：两端读取/编辑/保存；外部修改触发 VERSION_CONFLICT；Terminal `pwd` 正确、左右分屏、断线重连；旧 session 结束不自动换 shell。

- [ ] 场景 6 `remote-preview`：远端启动 loopback HTTP 服务，经 local agentd Preview 在 Browser 打开；断远端后页面明确不可用且代理关闭；恢复需按产品契约重新连接/创建。

- [ ] 场景 7 `task-ownership`：在已登记 worktree dispatch 自动归属；在项目外目录 dispatch 建 detached Workspace；添加匹配 Project 后 Workspace/Task ID 不变并归并。

- [ ] 场景 8 `structured-task-loop`：真实 OpenCode 完成六类 frame、artifact、审批/回答、continue、stop 或终态；DB/日志证明 task_seq、command ID、ticket version；查询确认 reasoning 未落盘。

- [ ] 场景 9 `disconnect-and-concurrency`：远端断开时资源/TUI/操作均不可用但 metadata 保留；两个桌面同时审批，只有一个 executor delivery，另一个看到 canonical conflict；关闭桌面不停止 executor。

- [ ] 每个场景结束执行专属 cleanup，只删除 run-owned repo/worktree/preview/session；物料清单写是否可恢复。不得 `rm -rf` 未解析路径、用户仓库根或远端广目录。

- [ ] 如果任何场景失败，Task 9 立即保持未完成并保存原始错误/correlation；回到 Tasks 1–8 中明确拥有该行为的任务，使用 systematic-debugging 先定位根因和精确文件，再在该任务的文件清单/测试边界内完成失败测试→修复→聚焦/全量验证；随后从失败场景重跑。Task 9 本身不直接修改未知 source file，不能只改 evidence 或 UI 文案绕过。

- [ ] evidence 文档填写每个场景的命令、时间、退出码、截图/trace、日志字段、数据库/进程事实和结论；敏感值替换为稳定 hash/匿名 ID。

- [ ] 此任务的运行日志和 evidence 本身就是观测产物；如新增修复代码，必须补文件头/导出文档/关键日志并单独 commit。纯 evidence commit：

  ```bash
  git add docs/superpowers/evidence/phase2-final-real-machine.md docs/superpowers/evidence/phase2-final-real-machine
  git commit -m "test: record phase two real machine acceptance"
  ```

### Task 10: 全量验证、独立双轴审阅和第二阶段交付

**Files:**
- Create: `docs/superpowers/evidence/phase2-final-review.md`

**Interfaces:**
- Consumes: current commit, all Go/desktop/E2E/package verification commands, Task 9 evidence, and two independent review reports.
- Produces: `phase2-final-review.md`, zero unresolved P0/P1 findings, and a branch ready for `superpowers:finishing-a-development-branch` without automatic merge/push/deletion.

- [ ] 从干净进程状态新跑 Go：

  ```bash
  go test -race ./internal/...
  go test ./...
  go vet ./...
  ```

- [ ] 从干净依赖状态新跑 desktop：

  ```bash
  (cd desktop && corepack pnpm install --frozen-lockfile)
  (cd desktop && corepack pnpm typecheck)
  (cd desktop && corepack pnpm test)
  (cd desktop && corepack pnpm run check:handoff-architecture)
  (cd desktop && corepack pnpm run check:code-quality:changed)
  (cd desktop && corepack pnpm run check:max-lines-ratchet)
  (cd desktop && corepack pnpm run build:desktop)
  (cd desktop && corepack pnpm run build:unpack)
  (cd desktop && node config/scripts/handoff-package-smoke.mjs)
  ```

- [ ] 新跑所有 Handoff E2E：catalog、resources、task-tui、multi-instance、phase2 acceptance；保留当前 run IDs 与结果，不复用旧绿灯输出。

- [ ] 使用 `superpowers:requesting-code-review` 发起两个独立审阅：
  1. Spec review：逐条对照设计 §§2–18 和九场景，专查遗漏/越界。
  2. Code quality/security review：专查事实所有权、事务、并发、恢复、资源授权、日志泄漏、Orca SSH 依赖和测试真实性。

- [ ] 对每条审阅反馈先复现/核验；需要代码修改时 Task 10 保持未完成，回到拥有该文件/行为的 Tasks 1–8 处理，并按其精确文件清单重跑聚焦测试、全量测试和相关真实场景。P0/P1 未清零不得生成最终 review 结论。

- [ ] 用 instrumenting-code 清单逐项审计所有 Phase 2 新文件：入口/外部调用/错误/状态/成功日志；错误上下文；无 print；文件头；导出注释；复杂 why；无 secret/content/reasoning。

- [ ] `phase2-final-review.md` 记录：最终 commit、所有验证命令/退出码、九场景 evidence 链接、双轴审阅者与结论、已修 finding、明确延期到第三阶段的 Orca 瘦身项。

- [ ] 检查 worktree：只允许用户既有未跟踪项；无 node_modules/build/log/token/evidence 临时大文件误入 Git。

- [ ] 最终文档 commit：

  ```bash
  git add docs/superpowers/evidence/phase2-final-review.md
  git commit -m "docs: close handoff desktop phase two validation"
  ```

- [ ] 完成后使用 `superpowers:finishing-a-development-branch` 给用户选择 merge/PR/保留分支；同时提示是否把已实现 UI 形态回流 `prototypes/base/`。未经用户选择不擅自 merge、push 或删除 worktree。

## Plan 04 Completion Gate

- [ ] protocol/capability mixed-version、Git fallback、cursor expiry/snapshot reset 全绿。
- [ ] 远端严格按 authenticate→negotiate→catch-up→reconcile→reattach→connected 恢复，generation fencing 有 race 证据。
- [ ] 非 loopback transport、secret、path、Preview 和日志安全测试全绿；renderer/preload 不含 token。
- [ ] 两个真实桌面实例并发审批只有一次 executor delivery，桌面关闭不改变任务生命周期。
- [ ] macOS `.app` 构建与 packaged smoke 通过，Handoff 产品身份和上游 attribution 正确。
- [ ] 九个场景均有真实 macOS/本机 agentd/远端 Linux agentd/OpenCode 证据，而非仅 mock。
- [ ] Go、desktop、所有 Handoff E2E、架构守卫和独立双轴审阅均基于最终 commit 通过。
- [ ] 第二阶段未删除 Orca 旧代码；第三阶段瘦身以本阶段依赖图、架构守卫和九场景作为回归门。
