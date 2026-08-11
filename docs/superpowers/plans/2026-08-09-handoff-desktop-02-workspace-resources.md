# Handoff Desktop Workspace Resources Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让当前 Workspace 的右侧文件树和中间 Terminal、Editor、Browser 标签在本机与远端拥有同一套 agentd 语义，支持保存冲突、标签恢复、左右分屏、PTY 重连和远端 localhost Preview。

**Architecture:** renderer 只通过 `window.handoff` 调 Electron Main，Main 只连本机 agentd；本机 agentd 用 Workspace ID 路由到本机 `machineauthority` 或远端 peer。文件、Git、PTY 和 Preview 都由所属机器 agentd 授权和执行，不允许 Electron 本地 Node `fs`/`child_process`/`node-pty` 快捷路径。

**Tech Stack:** Go 1.26 `os.Root`、SQLite、fsnotify、creack/pty、HTTP/WS reverse proxy、Electron `<webview>`、xterm PaneManager、Monaco、React、Zustand、Zod、Vitest、Playwright。

## Global Constraints

- 计划 01 completion gate 全部通过后再开始；稳定 `machine_id/workspace_id/control_revision` 不得改为路径身份。
- 远端 `unavailable/incompatible/connecting/reconciling` 时文件、Git、PTY 和 Preview 全部不可用；不能回退 Orca SSH，也不能把缓存内容称为只读现场。
- 所有资源 API 使用 `workspace_id + relative_path`。renderer 不提交任意远端绝对路径；Machine Authority 必须在 owner 端做最终授权。
- 文件保存使用 version + `if_match` 和原子 rename；冲突绝不静默覆盖。PTY session ID 与 incarnation 不可偷偷重绑新 shell。
- Git 命令保持 2.25 基线；watcher 只是提示，Reconcile/显式读取才是事实。
- 每个代码任务先写失败测试，再最小实现；完成前补结构化日志、文件头职责/边界、导出项文档和关键“为什么”注释。
- 日志只记录资源 ID、相对路径、版本、端口、session/incarnation/seq、耗时和安全摘要；不得记录文件内容、终端输入、token 或完整终端输出。

---

### Task 1: 定义资源 wire contract、Workspace 路由和 Machine 离线门禁

**Files:**
- Create: `internal/resourcegateway/router.go`
- Create: `internal/resourcegateway/router_test.go`
- Create: `internal/workspaceapi/contracts.go`
- Create: `internal/workspaceapi/contracts_test.go`
- Create: `internal/desktopapi/resources.go`
- Create: `internal/desktopapi/resources_test.go`
- Create: `internal/desktopapi/resource_assembler.go`
- Create: `internal/desktopapi/resource_assembler_test.go`
- Create: `internal/desktopapi/testdata/file-entry.json`
- Create: `internal/desktopapi/testdata/file-document.json`
- Create: `internal/desktopapi/testdata/file-search-result.json`
- Create: `internal/desktopapi/testdata/pty-frame.json`
- Create: `internal/desktopapi/testdata/preview-session.json`
- Modify: `internal/controlplane/repository.go`
- Modify: `internal/peer/protocol.go`
- Modify: `desktop/src/shared/handoff/contracts.ts`
- Modify: `desktop/src/shared/handoff/contracts.test.ts`

**Interfaces:**
- Consumes: Plan 01 `controlplane.Repository`, stable `workspace_id/machine_id`, peer hello/capabilities, and `desktopapi.Problem`.
- Produces: provider-owned `workspaceapi.Authority`, `WorkspaceRef`, `FileEntry`, `FileDocument`, `WriteFileCommand`, `GitStatusSnapshot`, `PtySession`, `PtyServerFrame`, `PreviewSession`; `desktopapi.ResourceAssembler`; and Router methods keyed only by Workspace/resource IDs.

- [ ] 写 Router 红灯测试：local Workspace 调 local authority；remote Workspace 只调 peer；未知 Workspace 返回 RESOURCE_NOT_FOUND；Machine 非 connected 返回 MACHINE_OFFLINE/CAPABILITY_UNSUPPORTED；任何分支都不能拿 endpoint/token 返回给调用方。

- [ ] 写 Go/TS golden 红灯测试，覆盖 `FileEntry`、`FileDocument`、`FileSearchResult`、`GitStatusSnapshot`、`PtySession`、`PtyServerFrame`、`PreviewSession` 和 Problem。未知可选字段被忽略，缺少 ID/version/incarnation/seq 拒绝。

- [ ] 运行红灯：

  ```bash
  go test ./internal/resourcegateway ./internal/workspaceapi ./internal/desktopapi
  (cd desktop && corepack pnpm exec vitest run --config config/vitest.config.ts src/shared/handoff/contracts.test.ts)
  ```

- [ ] 定义 owner-side 端口，不让 gateway 依赖具体 local/peer 实现：

  ```go
  type Authority interface {
      ListDirectory(context.Context, WorkspaceRef, string) ([]FileEntry, error)
      ReadFile(context.Context, WorkspaceRef, string) (FileDocument, error)
      WriteFile(context.Context, WorkspaceRef, WriteFileCommand) (FileDocument, error)
      SearchFiles(context.Context, WorkspaceRef, SearchFilesCommand) (FileSearchResult, error)
      GitStatus(context.Context, WorkspaceRef) (GitStatusSnapshot, error)
      CreateTerminal(context.Context, WorkspaceRef, CreateTerminalCommand) (PtySession, error)
      GetTerminal(context.Context, string) (PtySession, error)
      CreatePreview(context.Context, WorkspaceRef, CreatePreviewCommand) (PreviewSession, error)
  }
  ```

  该接口和所有共享 command/result 类型位于 `internal/workspaceapi`；`machineauthority` 与 peer client 实现它，`resourcegateway` 只消费，禁止各消费方再定义一份相似端口。

- [ ] `resourcegateway.Router` 先从 control repository 解析 Workspace 和 Machine，再检查 status/capability，最后选择 local authority 或 peer authority。所有方法返回 typed `desktopapi.ProblemError`，HTTP adapter 不靠字符串分类。

- [ ] 扩展 capability keys：`files=1`、`git=1`、`pty=1`、`preview=1`。客户端只有收到 peer hello echo 后才调用；旧 peer 缺 capability 时明确 incompatible/unsupported。

- [ ] 资源 DTO 的 path 一律是 `/` 分隔的 Workspace-relative path；根目录用空字符串。公开 DTO 不含 owner absolute root，只有 bootstrap Workspace metadata 可显示路径摘要。

- [ ] `desktopapi.ResourceAssembler` 纯转换 `workspaceapi` command/result 与 HTTP DTO，覆盖 file/search/Git/PTY/Preview；handler 不内联字段映射。assembler 测试覆盖可选字段、空集合、binary/base64、seq/incarnation 和 Problem 映射，不做 I/O/授权/业务判断。

- [ ] 日志在 Router 记录 operation、machine/workspace、capability decision、owner route 和耗时；拒绝路径记录 problem code，不记录文件内容或 token。

- [ ] 补齐所有新文件头注释、导出项文档和“为什么 owner 端还要二次鉴权”的注释。

- [ ] 运行绿灯：

  ```bash
  gofmt -w internal/resourcegateway internal/workspaceapi internal/desktopapi/resources.go internal/desktopapi/resources_test.go internal/desktopapi/resource_assembler.go internal/desktopapi/resource_assembler_test.go internal/controlplane/repository.go internal/peer/protocol.go
  go test ./internal/resourcegateway ./internal/workspaceapi ./internal/desktopapi ./internal/peer
  (cd desktop && corepack pnpm exec vitest run --config config/vitest.config.ts src/shared/handoff/contracts.test.ts)
  (cd desktop && corepack pnpm typecheck)
  ```

- [ ] Commit:

  ```bash
  git add internal/resourcegateway internal/workspaceapi internal/desktopapi internal/controlplane/repository.go internal/peer/protocol.go desktop/src/shared/handoff
  git commit -m "feat: define workspace resource gateway contracts"
  ```

### Task 2: 实现安全文件浏览、读取、原子保存和变更流

**Files:**
- Create: `internal/machineauthority/authorized_root.go`
- Create: `internal/machineauthority/authorized_root_test.go`
- Create: `internal/machineauthority/files.go`
- Create: `internal/machineauthority/files_test.go`
- Create: `internal/machineauthority/file_search.go`
- Create: `internal/machineauthority/file_search_test.go`
- Create: `internal/machineauthority/file_watch.go`
- Create: `internal/machineauthority/file_watch_test.go`
- Create: `internal/agentd/resource_server.go`
- Create: `internal/agentd/resource_server_test.go`
- Create: `internal/agentd/file_stream.go`
- Create: `internal/agentd/file_stream_test.go`
- Modify: `internal/agentd/server.go`
- Modify: `internal/resourcegateway/router.go`
- Modify: `internal/peer/client.go`
- Modify: `internal/agentd/peer_server.go`

**Interfaces:**
- Consumes: Task 1 `workspaceapi.Authority`, `WorkspaceRef`, file DTOs, and owner routing.
- Produces: `machineauthority.AuthorizedRoot`, `ListDirectory/ReadFile/WriteFile/SearchFiles`, versioned file HTTP routes, and `files/stream?after=seq` for both local and peer owners.

- [ ] 写安全红灯测试：拒绝 absolute、`..`、NUL、symlink escape、目录冒充文件；允许根内 symlink；list/search 不越 root；读取版本等于内容 SHA-256；外部修改后旧 `if_match` 返回 VERSION_CONFLICT；成功写同目录临时文件 + fsync + atomic rename；失败不留半文件。

- [ ] 写搜索红灯测试：literal query 只扫描授权 root、跳过 `.git`/二进制/大文件；结果为相对 path/line/column/preview；`max_results` 上限 200、单文件 2MiB、单请求扫描 64MiB；达到任一上限返回 `truncated=true` 而不是继续占用内存；日志不含 query 或 preview。

- [ ] 写 stream 红灯测试：外部 create/modify/remove 合并成相对路径事件；事件带 per-workspace seq；断线可 after 重放；缓冲溢出主动断开而不是无限增长；Workspace unavailable 立即终止流。

- [ ] 运行红灯：

  ```bash
  go test ./internal/machineauthority ./internal/agentd ./internal/resourcegateway -run 'AuthorizedRoot|File|Directory|VersionConflict|FileStream'
  ```

- [ ] `AuthorizedRoot` 使用 Go `os.OpenRoot`/`os.Root` 作为最终边界；先做语法拒绝，再由 Root API 防 symlink/TOCTOU 越界。不能只靠 `filepath.Clean + strings.HasPrefix`。

- [ ] 文件 DTO 精确定义：

  ```go
  type FileDocument struct {
      WorkspaceID string `json:"workspace_id"`
      Path string `json:"path"`
      Version string `json:"version"`
      ContentBase64 string `json:"content_base64"`
      Size int64 `json:"size"`
      ModifiedAt time.Time `json:"modified_at"`
  }

  type WriteFileCommand struct {
      CommandID string `json:"command_id"`
      Path string `json:"path"`
      IfMatch string `json:"if_match"`
      ContentBase64 string `json:"content_base64"`
      CreateOnly bool `json:"create_only"`
  }

  type SearchFilesCommand struct {
      Query string `json:"query"`
      Path string `json:"path"`
      MaxResults int `json:"max_results"`
  }

  type FileSearchMatch struct {
      Path string `json:"path"`
      Line int `json:"line"`
      Column int `json:"column"`
      Preview string `json:"preview"`
  }

  type FileSearchResult struct {
      Matches []FileSearchMatch `json:"matches"`
      Truncated bool `json:"truncated"`
      ScannedFiles int `json:"scanned_files"`
      ScannedBytes int64 `json:"scanned_bytes"`
  }
  ```

  `CreateOnly` 支持“另存为”；普通保存必须 `IfMatch` 非空。

- [ ] 注册 routes：

  ```text
  GET /v1/workspaces/{workspace_id}/entries?path=<relative>
  GET /v1/workspaces/{workspace_id}/file?path=<relative>
  PUT /v1/workspaces/{workspace_id}/file
  POST /v1/workspaces/{workspace_id}/files/search
  GET /v1/workspaces/{workspace_id}/files/stream?after=<seq>  (WebSocket)
  ```

- [ ] remote peer 提供同构 owner routes；local agentd gateway 转发公开 DTO/Problem，不把 desktop Bearer token 复用为 remote token，也不把 remote token返回 Main。

- [ ] 文件 watcher 只做 UI invalidation；文件内容仍在用户打开/重载时读取。忽略 `.git` 内高频内容，Git 元数据由 Git Reconciler 负责。

- [ ] 日志覆盖 list/read/write/watch subscribe/unsubscribe/conflict/success，带 machine/workspace/relative path/version/size；绝不打 content_base64。

- [ ] 补齐文件职责注释、导出方法文档和 `os.Root`、原子写、watcher 仅提示的“为什么”注释。

- [ ] 运行绿灯与回归：

  ```bash
  gofmt -w internal/machineauthority internal/agentd/resource_server.go internal/agentd/resource_server_test.go internal/agentd/file_stream.go internal/agentd/file_stream_test.go internal/resourcegateway/router.go internal/peer/client.go internal/agentd/peer_server.go internal/agentd/server.go
  go test ./internal/machineauthority ./internal/agentd ./internal/resourcegateway ./internal/peer
  go test ./...
  ```

- [ ] Commit:

  ```bash
  git add internal/machineauthority internal/agentd internal/resourcegateway internal/peer
  git commit -m "feat: proxy versioned workspace file operations"
  ```

### Task 3: 提供 Workspace Git 基础状态和外部变化联动

**Files:**
- Create: `internal/machineauthority/git_status.go`
- Create: `internal/machineauthority/git_status_test.go`
- Modify: `internal/machineauthority/reconciler.go`
- Modify: `internal/machineauthority/reconciler_test.go`
- Modify: `internal/agentd/resource_server.go`
- Modify: `internal/agentd/resource_server_test.go`
- Modify: `internal/resourcegateway/router.go`
- Modify: `internal/peer/client.go`
- Modify: `internal/desktopapi/resources.go`

**Interfaces:**
- Consumes: Task 1 `GitStatusSnapshot`, Plan 01 Git Reconciler, and authorized Workspace root lookup.
- Produces: `machineauthority.GitStatus(context.Context, WorkspaceRef) (GitStatusSnapshot, error)` and `GET /v1/workspaces/{workspace_id}/git/status` through local/peer routing.

- [ ] 写红灯测试：解析 `git status --porcelain=v2 -z --branch` 的 modified/added/deleted/renamed/untracked/conflict；非 Git Workspace 返回可识别空能力；外部 branch/worktree 变化仍由计划 01 Reconciler 推 control event；文件状态查询不修改仓库。

- [ ] 写 Git 2.25 契约测试，命令不得包含 `worktree list -z`、`for-each-ref --exclude` 或 2.25 后新增的必需 flag。

- [ ] 运行红灯：

  ```bash
  go test ./internal/machineauthority ./internal/agentd -run 'GitStatus|GitCompatibility|ExternalGit'
  ```

- [ ] 注册 `GET /v1/workspaces/{workspace_id}/git/status`，返回 branch/head/upstream/ahead/behind 与 entries；只提供基础状态，不实现 staging/commit/PR。

- [ ] 状态命令用参数数组执行，设置超时和输出上限；拒绝 shell 拼接。错误带 stderr 安全尾部与 exit code，但 HTTP 只返回可行动 Problem。

- [ ] 把 file watcher 的非 `.git` 事件与 Git watcher 的 `.git` 事件分开；前者刷新文件树，后者触发完整 Reconcile 和显式 status invalidation。

- [ ] 日志记录 machine/workspace/branch/head/entry count/duration；不记录 diff 或文件内容。补齐新文件头、导出文档和 Git watcher 不是事实源的原因注释。

- [ ] 运行绿灯：

  ```bash
  gofmt -w internal/machineauthority/git_status.go internal/machineauthority/git_status_test.go internal/machineauthority/reconciler.go internal/machineauthority/reconciler_test.go internal/agentd/resource_server.go internal/agentd/resource_server_test.go internal/resourcegateway/router.go internal/peer/client.go internal/desktopapi/resources.go
  go test ./internal/machineauthority ./internal/agentd ./internal/resourcegateway ./internal/peer
  go test ./...
  ```

- [ ] Commit:

  ```bash
  git add internal/machineauthority internal/agentd internal/resourcegateway internal/peer internal/desktopapi/resources.go
  git commit -m "feat: expose workspace git status through agentd"
  ```

### Task 4: 实现 agentd 普通 PTY 会话、输出 replay 和远端双向代理

**Files:**
- Create: `internal/ptyservice/service.go`
- Create: `internal/ptyservice/service_unix.go`
- Create: `internal/ptyservice/service_other.go`
- Create: `internal/ptyservice/service_test.go`
- Create: `internal/ptyservice/ring.go`
- Create: `internal/ptyservice/ring_test.go`
- Create: `internal/store/pty.go`
- Create: `internal/store/pty_test.go`
- Create: `internal/agentd/pty_server.go`
- Create: `internal/agentd/pty_server_test.go`
- Modify: `go.mod`
- Modify: `go.sum`
- Modify: `internal/resourcegateway/router.go`
- Modify: `internal/peer/client.go`
- Modify: `internal/agentd/peer_server.go`
- Modify: `cmd/agentd.go`

**Interfaces:**
- Consumes: Task 1 Workspace owner routing, authorized roots, peer transport, and durable store.
- Produces: `ptyservice.Service.Create/Get/Connect/Input/Resize/Close`, `PtySession{ID,Incarnation,WorkspaceID,State,ThroughSeq}`, and versioned PTY client/server frames with replay.

- [ ] 写红灯测试：创建 shell cwd 等于 Workspace root；输出 seq 单调；resize 生效；disconnect 后 session 仍活；after seq replay 后接 live 不丢不重；session create/exit 与 `pty.upsert/pty.exit` machine event 同事务；agentd 重启后旧 active session 标 ended；旧 session ID 不会创建新 shell；远端代理双向 input/resize/output/exit。

- [ ] 写 ring 边界测试：按字节和 frame 数双限；cursor 太旧返回 CURSOR_EXPIRED + snapshot/through seq；慢客户端断开；无界终端输出不能增长 agentd 内存。

- [ ] 运行红灯：

  ```bash
  go test ./internal/ptyservice ./internal/store ./internal/agentd ./internal/resourcegateway -run 'Pty|Terminal|Replay|Incarnation'
  ```

- [ ] 添加 `github.com/creack/pty`。Unix 使用登录 shell和 `cmd.Dir=workspace root`；非支持平台返回 CAPABILITY_UNSUPPORTED，本阶段不伪装 Windows PTY 已完成。

- [ ] 定义 session 身份：

  ```go
  type Session struct {
      ID string `json:"terminal_session_id"`
      Incarnation string `json:"incarnation"`
      WorkspaceID string `json:"workspace_id"`
      State string `json:"state"`
      Shell string `json:"shell"`
      ThroughSeq int64 `json:"through_seq"`
      ExitCode *int `json:"exit_code"`
  }
  ```

- [ ] WebSocket wire 全部是版本化 JSON frame，第一帧必须为 `subscribed` 并回显 capability；server frame 为 `snapshot|data|status|exit|problem`，client frame 为 `input|resize|ack`。data 用 base64，包含 session ID、incarnation、seq；未来新增 frame kind 必须 capability 协商。

- [ ] 注册 routes：

  ```text
  POST   /v1/workspaces/{workspace_id}/terminals
  GET    /v1/terminals/{terminal_session_id}
  DELETE /v1/terminals/{terminal_session_id}
  GET    /v1/terminals/{terminal_session_id}/stream?incarnation=<id>&after=<seq>
  ```

- [ ] 创建命令含 `command_id/cols/rows`，幂等返回同一 Session；关闭显式终止，不删除历史 metadata。远端 local agentd 只转发 frames，不解析/重写 terminal 字节。

- [ ] PTY 创建、状态和退出持久化时同步追加 owner `machine_event`；本机控制面只投影 session 摘要，terminal bytes 永不进入 machine/control event。

- [ ] 日志覆盖 spawn/attach/replay/resize/exit/close/cursor expired/proxy reconnect，带 machine/workspace/session/incarnation/seq/count；不记录用户 input 或 output 内容。

- [ ] 补齐职责头注释、导出文档和“session ID 不重绑”“agentd 重启标 ended”的原因注释。

- [ ] 运行绿灯与 race：

  ```bash
  gofmt -w internal/ptyservice internal/store/pty.go internal/store/pty_test.go internal/agentd/pty_server.go internal/agentd/pty_server_test.go internal/resourcegateway/router.go internal/peer/client.go internal/agentd/peer_server.go cmd/agentd.go
  go test -race ./internal/ptyservice ./internal/store ./internal/agentd ./internal/resourcegateway ./internal/peer
  go test ./...
  ```

- [ ] Commit:

  ```bash
  git add go.mod go.sum internal/ptyservice internal/store internal/agentd internal/resourcegateway internal/peer cmd/agentd.go
  git commit -m "feat: stream agentd terminal sessions with replay"
  ```

### Task 5: 实现受限 Preview session 和远端 localhost 代理

**Files:**
- Create: `internal/preview/service.go`
- Create: `internal/preview/service_test.go`
- Create: `internal/preview/proxy.go`
- Create: `internal/preview/proxy_test.go`
- Create: `internal/store/preview.go`
- Create: `internal/store/preview_test.go`
- Create: `internal/agentd/preview_server.go`
- Create: `internal/agentd/preview_server_test.go`
- Modify: `internal/resourcegateway/router.go`
- Modify: `internal/peer/client.go`
- Modify: `internal/agentd/peer_server.go`
- Modify: `cmd/agentd.go`

**Interfaces:**
- Consumes: Task 1 owner routing, local agentd listener, authenticated peer calls, and Workspace availability.
- Produces: `preview.Service.Create/Get/Close`, `PreviewSession`, owner-loopback reverse proxy, and local nonce URL under `/v1/preview-proxy/{nonce}/`.

- [ ] 写红灯测试：只允许端口 1–65535 和 owner loopback `127.0.0.1/::1/localhost`；拒绝任意 host/TCP proxy；session nonce 不可预测、绑定 machine/workspace/port、有 TTL；HTTP path/query/header 和 WebSocket upgrade 可代理；Machine 断开立即失效。

- [ ] 写远端测试：desktop 访问的始终是本机 loopback URL；本机 agentd 经 authenticated peer 调远端 owner；响应不暴露 remote endpoint/token；同 `command_id` 返回同一 Preview session。

- [ ] 运行红灯：

  ```bash
  go test ./internal/preview ./internal/store ./internal/agentd ./internal/resourcegateway -run 'Preview|Loopback|WebSocketProxy'
  ```

- [ ] 注册控制 routes：

  ```text
  POST   /v1/workspaces/{workspace_id}/previews
  GET    /v1/previews/{preview_session_id}
  DELETE /v1/previews/{preview_session_id}
  ANY    /v1/preview-proxy/{nonce}/{path...}
  ```

  创建响应只返回本机 `http://127.0.0.1:<agentd-port>/v1/preview-proxy/<nonce>/`。

- [ ] Proxy 必须剥离外部 Authorization/Cookie 中与 agentd 有关的凭据；peer 鉴权由内部 request 重新添加。限制响应 header/body 和空闲时间，关闭时释放 upstream。

- [ ] 日志记录 session create/open/close/expire/upstream unavailable，带 machine/workspace/session/port/duration；不得记录 nonce 全值、Cookie、Authorization 或页面内容。

- [ ] 补齐职责头注释、导出文档和“为什么只允许 owner loopback”“为何 URL 用短期 nonce 而非 agent token”的注释。

- [ ] 运行绿灯：

  ```bash
  gofmt -w internal/preview internal/store/preview.go internal/store/preview_test.go internal/agentd/preview_server.go internal/agentd/preview_server_test.go internal/resourcegateway/router.go internal/peer/client.go internal/agentd/peer_server.go cmd/agentd.go
  go test ./internal/preview ./internal/store ./internal/agentd ./internal/resourcegateway ./internal/peer
  go test ./...
  ```

- [ ] Commit:

  ```bash
  git add internal/preview internal/store internal/agentd internal/resourcegateway internal/peer cmd/agentd.go
  git commit -m "feat: proxy workspace localhost previews through agentd"
  ```

### Task 6: 扩展 Main/Preload 的 files、git、pty、preview adapter

**Files:**
- Create: `desktop/src/main/handoff/resource-client.ts`
- Create: `desktop/src/main/handoff/resource-client.test.ts`
- Create: `desktop/src/main/handoff/pty-client.ts`
- Create: `desktop/src/main/handoff/pty-client.test.ts`
- Modify: `desktop/src/main/handoff/register-handoff-ipc.ts`
- Modify: `desktop/src/main/handoff/register-handoff-ipc.test.ts`
- Modify: `desktop/src/preload/handoff-api-types.ts`
- Modify: `desktop/src/preload/handoff.ts`
- Modify: `desktop/src/shared/handoff/contracts.ts`

**Interfaces:**
- Consumes: Tasks 2–5 files/Git/PTY/Preview HTTP/WS contracts and Plan 01 Main authentication/config boundary.
- Produces: `window.handoff.files`, `.git`, `.pty`, and `.preview` methods with sender-scoped unsubscribe callbacks and no raw socket/token exposure.

- [ ] 写红灯测试：所有 API 只能收 Workspace/resource ID 和 relative path；拒绝绝对 path/任意 endpoint；HTTP/WS Problem 映射一致；file stream/PTY subscription 按 sender 清理；token 从不出现在 IPC result/error/log。

- [ ] 写 PTY transport 红灯测试：subscribed 前不发 input；data 按 seq 写入；duplicate 忽略；gap 请求 snapshot；incarnation 改变把旧 tab 标 ended，不自动 spawn。

- [ ] 运行红灯：

  ```bash
  (cd desktop && corepack pnpm exec vitest run --config config/vitest.config.ts src/main/handoff src/preload)
  ```

- [ ] `window.handoff` 增加：

  ```ts
  files.list(workspaceId, relativePath)
  files.read(workspaceId, relativePath)
  files.write(workspaceId, command)
  files.search(workspaceId, command)
  files.onChanged(workspaceId, after, callback)
  git.status(workspaceId)
  pty.create(workspaceId, command)
  pty.connect(sessionId, incarnation, after, callbacks)
  pty.input(sessionId, incarnation, data)
  pty.resize(sessionId, incarnation, cols, rows)
  pty.close(sessionId, incarnation)
  preview.create(workspaceId, command)
  preview.close(sessionId)
  ```

- [ ] IPC 事件携带 subscription ID；preload 返回 unsubscribe；Main 按 `webContents.id + subscriptionId` 隔离多个窗口。PTY 字节只在 Main 做 base64 decode/encode，renderer transport 提供 string/Uint8Array，不暴露 socket。

- [ ] Main 结构化日志记录 API 开始/结果/Problem、subscription 生命周期和 seq，不记录 file content/terminal data；成功返回也必须有结果摘要。补齐文件头、导出文档和 sender-scope 的原因注释。

- [ ] 运行绿灯：

  ```bash
  (cd desktop && corepack pnpm exec vitest run --config config/vitest.config.ts src/shared/handoff src/main/handoff src/preload)
  (cd desktop && corepack pnpm typecheck)
  (cd desktop && corepack pnpm run check:code-quality:changed)
  ```

- [ ] Commit:

  ```bash
  git add desktop/src/shared/handoff desktop/src/main/handoff desktop/src/preload
  git commit -m "feat: expose workspace resources through narrow IPC"
  ```

### Task 7: 实现每 Workspace 的标签、左右分屏和 UI 状态持久化

**Files:**
- Create: `desktop/src/renderer/src/features/handoff/workbench/workspace-session-store.ts`
- Create: `desktop/src/renderer/src/features/handoff/workbench/workspace-session-store.test.ts`
- Create: `desktop/src/renderer/src/features/handoff/workbench/tab-model.ts`
- Create: `desktop/src/renderer/src/features/handoff/workbench/split-tree.ts`
- Create: `desktop/src/renderer/src/features/handoff/workbench/split-tree.test.ts`
- Create: `desktop/src/renderer/src/features/handoff/components/WorkbenchTabs.tsx`
- Create: `desktop/src/renderer/src/features/handoff/components/WorkbenchTabs.test.tsx`
- Create: `desktop/src/renderer/src/features/handoff/components/WorkbenchSplitLayout.tsx`
- Create: `desktop/src/renderer/src/features/handoff/components/WorkbenchSplitLayout.test.tsx`
- Modify: `desktop/src/renderer/src/features/handoff/components/WorkbenchShell.tsx`

**Interfaces:**
- Consumes: Plan 01 selected `workspace_id` and Task 6 resource session IDs.
- Produces: `HandoffTab` union, split-tree reducer, and WorkspaceSessionStore actions for open/focus/close/split/resize/hydrate keyed by Workspace ID.

- [ ] 写红灯测试：每个 Workspace 独立 tabs/groups/split tree/active tab；切换 Workspace 恢复原布局；关闭 Workspace 不删除服务器资源；同一 file/task tab 去重；split 左右比例 clamp 0.15–0.85；持久化只含 UI IDs，不含内容、token、TaskFrame 或 terminal bytes。

- [ ] 运行红灯：

  ```bash
  (cd desktop && corepack pnpm exec vitest run --config config/vitest.config.ts src/renderer/src/features/handoff/workbench src/renderer/src/features/handoff/components/WorkbenchTabs.test.tsx src/renderer/src/features/handoff/components/WorkbenchSplitLayout.test.tsx)
  ```

- [ ] Tab union 精确定义：

  ```ts
  type HandoffTab =
    | { id: string; kind: 'terminal'; workspaceId: string; sessionId: string; incarnation: string }
    | { id: string; kind: 'editor'; workspaceId: string; relativePath: string }
    | { id: string; kind: 'browser'; workspaceId: string; previewSessionId?: string; url: string }
    | { id: string; kind: 'task'; workspaceId: string; taskId: string }
  ```

  Task tab 只占模型，不在本计划实现内容。

- [ ] split tree 使用纯 reducer，leaf 指向 tab group ID；React 组件只负责渲染与 pointer ratio。可从 Orca `TabGroupSplitLayout` 提取通用 resize 行为，但不得让 Handoff 组件调用 Orca worktree store。

- [ ] localStorage key 带 schema version；hydrate 遇到未知版本或不存在的 Workspace ID 时安全丢弃对应 UI state，不改 agentd 事实。

- [ ] 新建 terminal/editor/browser 的默认动作都以当前 selected Workspace 为 owner；选中 Workspace 时 breadcrumb、toolbar、tab group、right root 同步更新。

- [ ] renderer 不打 console 日志；无效持久化状态通过可测试的 recovery result 返回，Main 只在必要时记录摘要。补齐文件头、导出文档和 Workspace 隔离原因注释。

- [ ] 运行绿灯：

  ```bash
  (cd desktop && corepack pnpm exec vitest run --config config/vitest.config.ts src/renderer/src/features/handoff)
  (cd desktop && corepack pnpm typecheck)
  ```

- [ ] Commit:

  ```bash
  git add desktop/src/renderer/src/features/handoff
  git commit -m "feat: persist handoff workspace tabs and split layout"
  ```

### Task 8: 实现右侧文件树、Monaco Editor 和冲突处理

**Files:**
- Create: `desktop/src/renderer/src/features/handoff/files/file-store.ts`
- Create: `desktop/src/renderer/src/features/handoff/files/file-store.test.ts`
- Create: `desktop/src/renderer/src/features/handoff/components/HandoffFileTree.tsx`
- Create: `desktop/src/renderer/src/features/handoff/components/HandoffFileTree.test.tsx`
- Create: `desktop/src/renderer/src/features/handoff/components/HandoffEditorPane.tsx`
- Create: `desktop/src/renderer/src/features/handoff/components/HandoffEditorPane.test.tsx`
- Create: `desktop/src/renderer/src/features/handoff/components/FileConflictDialog.tsx`
- Create: `desktop/src/renderer/src/features/handoff/components/FileConflictDialog.test.tsx`
- Create: `desktop/src/renderer/src/components/editor/MonacoSurface.tsx`
- Create: `desktop/src/renderer/src/components/editor/MonacoSurface.test.tsx`
- Modify: `desktop/src/renderer/src/components/editor/MonacoEditor.tsx`
- Modify: `desktop/src/renderer/src/features/handoff/components/WorkbenchShell.tsx`

**Interfaces:**
- Consumes: Task 6 `window.handoff.files/git`, Task 7 editor-tab identity, and Workspace availability.
- Produces: FileStore keyed by `(workspaceId, relativePath)`, transport-neutral `MonacoSurface` props, `HandoffFileTree`, `HandoffEditorPane`, and explicit VERSION_CONFLICT actions.

- [ ] 写红灯测试：Workspace 切换重置右栏 root；按目录懒加载；搜索输入调用当前 Workspace 的 bounded literal search、点击结果打开文件并定位行；file event 只 invalidates 对应 Workspace/path；点击文件打开/聚焦 editor tab；dirty 文件收到外部 change 不覆盖 buffer；保存发 `if_match`；409 展示 reload/diff/save as/manual merge；remote unavailable 隐藏内容并禁用操作。

- [ ] 写 surface 红灯测试，证明 `MonacoSurface` 只依赖注入的 value/language/viewState/onChange/onSave/readOnly/theme，不读 Orca `useAppStore` 或 Worktree 类型；原 Orca `MonacoEditor` 改成 wrapper 后已有聚焦测试仍通过。

- [ ] 运行红灯：

  ```bash
  (cd desktop && corepack pnpm exec vitest run --config config/vitest.config.ts src/renderer/src/features/handoff/files src/renderer/src/features/handoff/components/HandoffFileTree.test.tsx src/renderer/src/features/handoff/components/HandoffEditorPane.test.tsx src/renderer/src/components/editor/MonacoSurface.test.tsx)
  ```

- [ ] `file-store` 按 Workspace + relative path 保存 directory cache、document version、dirty buffer、conflict；切换 Workspace 不混用 cache。内容不写 localStorage。

- [ ] 从 Orca `MonacoEditor` 提取 transport-neutral `MonacoSurface`；保留 setup、内容同步、键盘保存、theme/font/view state。Orca diff comment/Worktree store 装饰留在 wrapper，不带进 Handoff。

- [ ] `HandoffFileTree` 遵循 Orca sidebar token、13px dense row、git decoration token；状态来自 agentd GitStatus，不在 renderer 运行 Git。搜索结果只保留 path/position，清空查询或切换 Workspace 即丢弃 preview，不写 localStorage。

- [ ] conflict dialog 的“重载”明确丢弃本地 buffer前二次确认；“Diff”用 Monaco Diff；“另存为”使用 CreateOnly；“人工合并”保留两份内容，不自动选胜者。

- [ ] 异步错误可重试且不 overclaim；renderer 无 console logging。Main/agentd 已记录资源日志。补齐文件头、导出文档和 dirty buffer 不被 watch 覆盖的原因注释。

- [ ] 运行绿灯和原 Orca editor 回归：

  ```bash
  (cd desktop && corepack pnpm exec vitest run --config config/vitest.config.ts src/renderer/src/features/handoff src/renderer/src/components/editor/MonacoSurface.test.tsx src/renderer/src/components/editor/EditorContent.test.tsx src/renderer/src/components/editor/MonacoEditor.content-owner.test.tsx)
  (cd desktop && corepack pnpm typecheck)
  (cd desktop && corepack pnpm run check:code-quality:changed)
  (cd desktop && corepack pnpm run check:max-lines-ratchet)
  ```

- [ ] Commit:

  ```bash
  git add desktop/src/renderer/src/features/handoff desktop/src/renderer/src/components/editor
  git commit -m "feat: browse and edit handoff workspace files"
  ```

### Task 9: 实现 xterm Terminal、Browser Pane 和资源不可用状态

**Files:**
- Create: `desktop/src/renderer/src/components/terminal-surface/XtermSurface.tsx`
- Create: `desktop/src/renderer/src/components/terminal-surface/XtermSurface.test.tsx`
- Create: `desktop/src/renderer/src/features/handoff/terminal/handoff-pty-transport.ts`
- Create: `desktop/src/renderer/src/features/handoff/terminal/handoff-pty-transport.test.ts`
- Create: `desktop/src/renderer/src/features/handoff/components/HandoffTerminalPane.tsx`
- Create: `desktop/src/renderer/src/features/handoff/components/HandoffTerminalPane.test.tsx`
- Create: `desktop/src/renderer/src/features/handoff/components/HandoffBrowserPane.tsx`
- Create: `desktop/src/renderer/src/features/handoff/components/HandoffBrowserPane.test.tsx`
- Modify: `desktop/src/renderer/src/components/browser-pane/browser-page-webview.ts`
- Modify: `desktop/src/renderer/src/features/handoff/components/WorkbenchShell.tsx`
- Modify: `desktop/src/renderer/src/features/handoff/architecture-boundary.test.ts`

**Interfaces:**
- Consumes: Task 6 `window.handoff.pty/preview`, Task 7 terminal/browser tabs and split groups, and Workspace availability.
- Produces: transport-neutral `XtermSurface`, `HandoffPtyTransport`, `HandoffTerminalPane`, `HandoffBrowserPane`, and a shared unavailable overlay that never falls back to SSH.

- [ ] 写 xterm 红灯测试：surface 只接 transport callbacks；输入/resize 发给同 session/incarnation；replay 后 live 顺序正确；exit 显示“会话已结束”；incarnation 不匹配不 spawn；左右 split 后两个 Terminal tab 各有独立 session。

- [ ] 写 Browser 红灯测试：普通 URL 可导航；远端 localhost 只能由 preview create 返回 URL；Machine unavailable 时保留 tab/url 但展示不可用遮罩；重连后显式 reconnect 恢复；不调用 SSH port forward。

- [ ] 运行红灯：

  ```bash
  (cd desktop && corepack pnpm exec vitest run --config config/vitest.config.ts src/renderer/src/components/terminal-surface src/renderer/src/features/handoff/terminal src/renderer/src/features/handoff/components/HandoffTerminalPane.test.tsx src/renderer/src/features/handoff/components/HandoffBrowserPane.test.tsx)
  ```

- [ ] 在 Handoff feature 内封装现有 Orca `PaneManager` 能力为 `XtermSurface`，复用 xterm 初始化、fit、主题、复制粘贴、链接和清理；transport 通过 props 注入。不得复制或依赖 Orca `pty-transport`、runtime environment 或 SSH path，也不得修改 PaneManager 去理解 Handoff 资源模型。

- [ ] `handoff-pty-transport` 将 `window.handoff.pty` 映射为 surface transport，维护 last seq；收到 gap/CURSOR_EXPIRED 请求 snapshot；ended 状态只有用户点击“新建终端”才创建新 session。

- [ ] `HandoffBrowserPane` 复用 `ensureBrowserPageWebview`、webview registry、地址栏视觉组件和 find 基础行为；将 Orca store 依赖留在旧 BrowserPane。Preview tab 只保存 Preview session ID 与本机 URL。

- [ ] unavailable overlay 统一包住 Terminal/Editor/Browser；不展示缓存内容，不发送 read/input/navigation；本机 Workspace 不受远端状态影响。

- [ ] 更新架构守卫，额外拒绝 Handoff imports 中出现 `ssh`, `runtime-environment`, `pty-transport.ts`, `port-forward`, `ProjectHostSetup`。

- [ ] renderer 不使用 console logging；Main/agentd 结构化日志记录 session/preview 的打开、重连、结束、错误和成功生命周期。补齐文件头、导出文档和旧 session 不自动替换、preview 不走 SSH 的原因注释。

- [ ] 运行绿灯和上游 surface 回归：

  ```bash
  (cd desktop && corepack pnpm exec vitest run --config config/vitest.config.ts src/renderer/src/components/terminal-surface src/renderer/src/features/handoff src/renderer/src/components/browser-pane/webview-registry.test.ts src/renderer/src/lib/pane-manager/pane-manager-registry.test.ts)
  (cd desktop && corepack pnpm typecheck)
  (cd desktop && corepack pnpm run check:code-quality:changed)
  (cd desktop && corepack pnpm run check:max-lines-ratchet)
  ```

- [ ] Commit:

  ```bash
  git add desktop/src/renderer/src/components/terminal-surface desktop/src/renderer/src/components/browser-pane/browser-page-webview.ts desktop/src/renderer/src/features/handoff
  git commit -m "feat: add handoff terminal and browser workspace tabs"
  ```

### Task 10: 完成工作区资源自动化 checkpoint

**Files:**
- Modify: `desktop/tests/fixtures/fake-handoff-agentd.ts`
- Create: `desktop/tests/e2e/handoff-workspace-resources.spec.ts`
- Create: `desktop/config/scripts/run-handoff-resources-e2e.mjs`
- Modify: `desktop/package.json`
- Create: `docs/superpowers/evidence/phase2-checkpoint-02.md`

**Interfaces:**
- Consumes: Tasks 1–9 resource wire, Main/Preload APIs, Workbench stores, and real local temporary Workspace resources.
- Produces: `pnpm test:e2e:handoff-resources`, enhanced fake agentd resource fixtures, and checkpoint-02 evidence/journals.

- [ ] 写失败 E2E：切换两个 Workspace 保留各自 tabs/split；右栏 root 联动；本地/远端搜索并从结果定位文件；打开/编辑/保存；外部修改触发 conflict；Terminal cwd/输入/resize/reconnect/exit；Browser preview；远端断开时三个资源 pane 和搜索全不可用、本机仍正常。

- [ ] fake agentd 必须通过真实 `/v1` HTTP/WS，模拟 owner local/remote、file version、PTY seq/incarnation、preview URL 和 Machine 状态；不能直接注入 renderer 状态。

- [ ] 增加 `pnpm test:e2e:handoff-resources`，失败保存 screenshot、trace、DOM、fake request journal 和 terminal frame journal；journal 只存长度/seq，不存输入内容。

- [ ] 用真实本机临时 Git 仓库跑 Go integration：创建 worktree、外部改文件、保存冲突、PTY `pwd`、启动 loopback HTTP server 并经 Preview 访问。测试资源只放 `t.TempDir()`，清理显式验证。

- [ ] 新测试/脚本补职责/边界头注释、导出文档和真实 wire/真实本机资源为何缺一不可的注释；关键阶段有结构化测试日志。

- [ ] 运行 checkpoint：

  ```bash
  go test -race ./internal/machineauthority ./internal/ptyservice ./internal/preview ./internal/agentd ./internal/resourcegateway ./internal/peer
  go test ./...
  (cd desktop && corepack pnpm exec vitest run --config config/vitest.config.ts src/shared/handoff src/main/handoff src/renderer/src/features/handoff)
  (cd desktop && corepack pnpm typecheck)
  (cd desktop && corepack pnpm test:e2e:handoff-resources)
  (cd desktop && corepack pnpm run check:code-quality:changed)
  (cd desktop && corepack pnpm run check:max-lines-ratchet)
  ```

- [ ] `phase2-checkpoint-02.md` 记录 commit、命令、结果、本机真实资源路径（只记临时根摘要）、截图/trace、日志 correlation IDs 和已知未覆盖项。

- [ ] Commit:

  ```bash
  git add desktop/tests/fixtures/fake-handoff-agentd.ts desktop/tests/e2e/handoff-workspace-resources.spec.ts desktop/config/scripts/run-handoff-resources-e2e.mjs desktop/package.json docs/superpowers/evidence/phase2-checkpoint-02.md
  git commit -m "test: prove handoff workspace resource checkpoint"
  ```

## Plan 02 Completion Gate

- [ ] 本地/远端都只经 local agentd resource gateway；architecture test 不含 Orca SSH/旧 Worktree transport。
- [ ] 文件越界、symlink escape、版本冲突和原子写测试通过；UI 无静默覆盖。
- [ ] PTY ID/incarnation/seq 和显式 ended 语义通过 race/E2E；没有旧 session 自动换新 shell。
- [ ] Preview 只代理 owner loopback，断线即时不可用，不形成任意 TCP proxy。
- [ ] 每 Workspace 的 tabs/split/right-root 隔离和持久化通过 E2E。
- [ ] Go/desktop 全套当前验证通过；日志/注释按 instrumenting-code 清单复核，且没有敏感内容进入日志或 IPC。
