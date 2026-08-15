# W4c 工作区文件写入与在线编辑 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让控制台中央区的文件 tab 从只读变成可编辑，写回经服务端哈希前置条件做冲突保护。

**Architecture:** 冲突判定放在写请求本身，不建 watcher——读的时候 agentd 连内容一起给 `sha256`，写的时候客户端原样带回，agentd 比对不一致就 409 并附上磁盘现状。这条路走得通的前提是 handoff 本来就「agentd 持有全部状态、客户端无状态可随时崩溃换机」。写入用同目录 tmp + `Root.Rename` 原子替换（executor 就在同一个工作树里跑，裸覆盖有窗口让它读到半截文件），不 fsync。

**Tech Stack:** Go 1.26.1（`os.Root` 的 `Lstat`/`OpenFile`/`Rename`/`Remove`，`crypto/sha256`）；React 19 + TypeScript + vitest + @testing-library/react。

**Spec:** [2026-08-13-w4c-workspace-file-write-design.md](../specs/2026-08-13-w4c-workspace-file-write-design.md)
**Backlog:** B81
**原型（形态基准）:** `prototypes/desktop-console/` —— 文件 tab 的三态、保存按钮、冲突条两个出口以它为准。

## Global Constraints

- **Go 版本下限 1.26.1**（`go.mod` 已是）。`os.Root` 的 `Lstat`/`Rename` 都要它。
- **大小上限沿用既有 `maxRunOutput = 1 << 20`（1 MiB）**，不新增常量、不改数值。读侧写侧同一个。
- **`GET /api/tasks/{id}/file` 的响应体必须逐字节不变**：仍是 `{"content": "..."}`，截断时正文仍带那行中文提示，二进制时仍是原始字节。这是 `handoff fetch` 的既有契约，`internal/client/client.go:763` 的 `struct{Content string}` 不许动。
- **错误文案用中文原文，前端原样透传**，不吞成「操作失败」。文案取值见 spec §9 表格，逐字使用。
- **日志用 `s.log` / `log()`，禁 `fmt.Printf`**。**文件正文不进日志**；主令牌、ticket 明文、cookie 明文同样不进。哈希只打前 8 位。
- **`web/src/` 生产代码零 `console.*`**（B74/B75 已确立），本期不破例。
- 每个实现类 task 都必须含「加关键节点日志」与「加意图注释」两个 step（`instrumenting-code`）。
- 新文件写文件头注释（职责 + 边界）；导出函数写参数/返回/注意。

---

## File Structure

| 文件 | 动作 | 职责 |
|---|---|---|
| `internal/proto/projects.go` | 修改 | 新增 `FileRead` / `FileWriteReq` / `FileWriteResp` / `FileConflictResp` 四个契约类型 |
| `internal/agentd/workspace.go` | 修改 | `ReadFile` 返回 `proto.FileRead`；新增 `isBinaryPrefix`、`isGitPath`、`WriteFile`、`atomicReplace`；新增五个哨兵错误 |
| `internal/agentd/workspacefiles.go` | 修改 | `workspaceRootOrErr` 403→400；`handleWorkspaceFile` 返回完整 `FileRead`（二进制置空正文）；新增 `handleWorkspaceFileWrite` |
| `internal/agentd/server.go` | 修改 | `handleTaskFile` 自己拼截断提示（CLI 契约不变）；注册 `PUT /api/workspaces/file` |
| `internal/agentd/workspace_write_test.go` | 新建 | `WriteFile` 的拒绝面、冲突、mode 保留、tmp 清理 |
| `internal/proto/contract_fixture_test.go` | 修改 | 四个新类型进 fixture 清单 |
| `web/src/api/types.ts` | 修改 | `FileRead` / `FileWriteResp` / `FileConflictResp` 的 TS 镜像 |
| `web/src/api/client.ts` | 修改 | `ApiError` 带 `body`；新增 `writeWorkspaceFile`；`fetchWorkspaceFile` 返回类型换成 `FileRead` |
| `web/src/app/workbench/tabs.ts` | 修改 | `file` tab 内容加 `draft?` / `baseSha?` |
| `web/src/app/workbench/FileTab.tsx` | 重写 | 三态头部、textarea、保存、⌘S、冲突条、草稿双层 |
| `web/src/app/workbench/fileDraft.ts` | 新建 | localStorage 草稿层（键、去抖、LRU 淘汰、静默降级），纯函数 + 一个 hook 无关的模块 |
| `web/src/app/shell/Shell.tsx` | 修改 | `renderContent` 给 `FileTab` 接草稿回写；`beforeCloseTab` 拦脏文件 tab |

---

## Task 1: `ReadFile` 返回结构体，截断提示搬到 CLI 端点

写入必须踩在保真的读上面。现在 `ReadFile` 把中文截断提示**拼进正文**返回，且返回 `string`——直接加写端点会造成「1.2 MiB 的文件存回去变成 1 MiB 加一行中文注释」。

**Files:**
- Modify: `internal/proto/projects.go`（追加 `FileRead`）
- Modify: `internal/agentd/workspace.go:811-862`（`ReadFile`）、`:948-952`（`truncatedNotice` 保留原样不动）
- Modify: `internal/agentd/server.go:1092`（`handleTaskFile`）
- Modify: `internal/agentd/workspacefiles.go:146-184`（`handleWorkspaceFile`）
- Test: `internal/agentd/workspace_test.go`（改既有 `TestReadFileSizeCap` 等）、`internal/agentd/workspacefiles_test.go`

**Interfaces:**
- Produces: `proto.FileRead{Content string; Size int64; Truncated bool; Binary bool; SHA256 string}`；`agentd.ReadFile(repo, rel string) (proto.FileRead, error)`；`agentd.isBinaryPrefix(b []byte) bool`
- Consumes: 既有 `maxRunOutput`、`truncatedNotice`、`ErrPathEscape` / `ErrPathIsDir` / `ErrNotRegularFile`

- [ ] **Step 1: 写失败的测试**

新建 `internal/agentd/workspace_read_struct_test.go`：

```go
package agentd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadFileTextGivesSHA 验证普通文本文件返回完整内容 + 哈希 + 真实大小。
func TestReadFileTextGivesSHA(t *testing.T) {
	dir := t.TempDir()
	body := "module handoff\n\ngo 1.26.1\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFile(dir, "go.mod")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if got.Content != body {
		t.Errorf("Content=%q, want %q", got.Content, body)
	}
	if got.Size != int64(len(body)) {
		t.Errorf("Size=%d, want %d", got.Size, len(body))
	}
	if got.Truncated || got.Binary {
		t.Errorf("Truncated=%v Binary=%v, want false false", got.Truncated, got.Binary)
	}
	sum := sha256.Sum256([]byte(body))
	if want := hex.EncodeToString(sum[:]); got.SHA256 != want {
		t.Errorf("SHA256=%q, want %q", got.SHA256, want)
	}
}

// TestReadFileTruncatedHasNoNotice 是本期最要紧的一条：截断的正文里**不再**
// 含那行中文提示，且不给哈希（不完整的内容当基线等于允许把文件截断后存回去）。
func TestReadFileTruncatedHasNoNotice(t *testing.T) {
	dir := t.TempDir()
	raw := bytes.Repeat([]byte("x"), maxRunOutput+4096)
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFile(dir, "big.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !got.Truncated {
		t.Error("Truncated=false, want true")
	}
	if len(got.Content) != maxRunOutput {
		t.Errorf("len(Content)=%d, want %d", len(got.Content), maxRunOutput)
	}
	if strings.Contains(got.Content, "内容已截断") {
		t.Error("正文仍含截断提示——提示必须留在 handleTaskFile 里，不能进 ReadFile 的返回")
	}
	if got.Size != int64(len(raw)) {
		t.Errorf("Size=%d, want 磁盘真实大小 %d", got.Size, len(raw))
	}
	if got.SHA256 != "" {
		t.Errorf("SHA256=%q, want 空（截断内容不可当写入基线）", got.SHA256)
	}
}

// TestReadFileBinary 验证前 8 KiB 出现 NUL 即判为二进制，且不给哈希。
func TestReadFileBinary(t *testing.T) {
	dir := t.TempDir()
	raw := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)
	if err := os.WriteFile(filepath.Join(dir, "logo.png"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFile(dir, "logo.png")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !got.Binary {
		t.Error("Binary=false, want true")
	}
	if got.SHA256 != "" {
		t.Errorf("SHA256=%q, want 空", got.SHA256)
	}
	if got.Size != int64(len(raw)) {
		t.Errorf("Size=%d, want %d", got.Size, len(raw))
	}
}

// TestIsBinaryPrefix 钉住判据边界：NUL 落在 8 KiB 之内算，之外不算。
func TestIsBinaryPrefix(t *testing.T) {
	inside := append(bytes.Repeat([]byte("a"), binaryProbeBytes-1), 0x00)
	if !isBinaryPrefix(inside) {
		t.Error("第 8192 字节的 NUL 应判为二进制")
	}
	outside := append(bytes.Repeat([]byte("a"), binaryProbeBytes), 0x00)
	if isBinaryPrefix(outside) {
		t.Error("第 8193 字节的 NUL 不在探测窗口内，不该判为二进制")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run 'TestReadFile(Text|Truncated|Binary)|TestIsBinaryPrefix' -v`
Expected: 编译失败——`got.Content undefined (type string has no field or method Content)`、`undefined: isBinaryPrefix`、`undefined: binaryProbeBytes`

- [ ] **Step 3: 在 proto 里加 `FileRead`**

在 `internal/proto/projects.go` 的 `DirListResult` 之后追加：

```go
// FileRead 是一次文件读取的完整结论（GET /api/workspaces/file 的响应体）。
//
// 为什么是结构体而不是继续返回一个 content 字符串：写回需要知道「这份内容完不
// 完整、是不是文本、基线哈希是多少」，而这三件事只有读的那一刻知道。让调用方
// 二次判断（比如按扩展名猜二进制）必然与服务端分叉成「前端说能编辑、后端说不能」。
//
// SHA256 只在**完整且是文本**时才有值。它唯一的用途是当写入前置条件，而
// Binary / Truncated 两种情况本来就不许写——**空值即「这文件不可编辑」**，
// 前端不必再判一次，后端也不必为一个注定被拒的写入算哈希。
//
// Size 是磁盘真实大小，不是 len(Content)：截断时两者不同，而用户要看到的是真实大小。
type FileRead struct {
	Content   string `json:"content"`
	Size      int64  `json:"size"`
	Truncated bool   `json:"truncated,omitempty"` // 超过 1 MiB，只返回开头
	Binary    bool   `json:"binary,omitempty"`    // 前 8 KiB 出现 NUL 字节
	SHA256    string `json:"sha256,omitempty"`
}
```

- [ ] **Step 4: 改 `ReadFile` 的返回**

`internal/agentd/workspace.go`——先在 import 块加 `"bytes"`、`"crypto/sha256"`、`"encoding/hex"`（`fmt`/`io`/`os`/`strings` 已有）。

在 `ReadFile` 之前加常量与判定函数：

```go
// binaryProbeBytes 是二进制判定的探测长度：前 8 KiB 内出现 NUL 字节即判为二进制。
//
// 判据抄自 orca 的 relay 文件通道（BINARY_PROBE_BYTES = 8192）。它朴素、无依赖，
// 且对源码/配置/文案这类真正需要在线编辑的东西零误判——真正的文本文件不会在
// 头 8 KiB 里塞 NUL。
const binaryProbeBytes = 8192

// isBinaryPrefix 判定一段内容的开头是否含 NUL 字节。
//
// 参数：
//   - b: 已读到的内容（可能已被 maxRunOutput 截断）
//
// 返回：前 min(len(b), 8192) 字节内出现 0x00 为真
func isBinaryPrefix(b []byte) bool {
	if len(b) > binaryProbeBytes {
		b = b[:binaryProbeBytes]
	}
	return bytes.IndexByte(b, 0) >= 0
}
```

把 `ReadFile` 的签名改成 `func ReadFile(repo, rel string) (proto.FileRead, error)`，所有 `return "", err` 改成 `return proto.FileRead{}, err`，并把结尾那段（现 `workspace.go:849-862`）换成：

```go
	// 只读 maxRunOutput+1 字节：多出的 1 字节用于判定「是否超限」，
	// 不额外多一次 Stat 也能得到截断结论（真实大小取已打开的 f.Stat）
	b, err := io.ReadAll(io.LimitReader(f, int64(maxRunOutput)+1))
	if err != nil {
		return proto.FileRead{}, fmt.Errorf("读取文件 %s: %w", filepath.Join(repo, cleaned), err)
	}
	out := proto.FileRead{Size: fi.Size()}
	if len(b) > maxRunOutput {
		log().Warn("文件超过读取上限，内容已截断", "repo", repo, "path", rel,
			"size", fi.Size(), "limit", maxRunOutput)
		out.Truncated = true
		b = b[:maxRunOutput]
	}
	out.Content = string(b)
	out.Binary = isBinaryPrefix(b)
	// 哈希只在「完整且是文本」时才算：它唯一的用途是当写入的前置条件，
	// 而截断内容当基线等于允许把文件截断后存回去，二进制本来就不许写。
	// 空值在契约上就是「这文件不可编辑」，前后端共用这一个判据
	if !out.Binary && !out.Truncated {
		sum := sha256.Sum256(b)
		out.SHA256 = hex.EncodeToString(sum[:])
	}
	return out, nil
```

同时更新 `ReadFile` 文件头注释里「大小上限」与「返回」两段：截断提示不再由本函数拼接，改由端点层按各自契约决定（`handleTaskFile` 拼、`handleWorkspaceFile` 不拼）。

- [ ] **Step 5: `handleTaskFile` 自己拼截断提示（CLI 契约不变）**

`internal/agentd/server.go` 里 `handleTaskFile` 的 `content, err := ReadFile(repo, rel)` 改成：

```go
	res, err := ReadFile(repo, rel)
	if err != nil {
		// …既有错误映射整段保持不变…
	}
	// 截断提示留在 CLI 这条线上：handoff fetch 的用途就是看文件开头，提示是给
	// 审核者看的（没有它，审核者会把第 1 MiB 处当成文件末尾去推理）。搬到这里
	// 之后 ReadFile 的返回才是保真的，在线编辑那条线才敢把内容存回磁盘。
	// 本端点的响应体因此逐字节不变，handoff fetch 行为零变更
	content := res.Content
	if res.Truncated {
		content += truncatedNotice(res.Size)
	}
	writeJSON(w, http.StatusOK, map[string]string{"content": content})
```

注意 `s.log.Info` 那行的 `"bytes", len(content)` 保持——它统计的是实际吐出去的字节数。

- [ ] **Step 6: `handleWorkspaceFile` 返回完整 `FileRead`**

`internal/agentd/workspacefiles.go` 结尾改成：

```go
	res, err := ReadFile(root, rel)
	if err != nil {
		// …既有错误映射整段保持不变…
	}
	// 二进制的正文在**端点层**置空，不在 ReadFile 里置空：ReadFile 是
	// handoff fetch 与在线编辑共用的一段代码，在那里抹掉内容会把 CLI 的
	// 既有行为一起改掉（fetch 一个 PNG 现在返回原始字节，那是已发布契约）。
	// 而对浏览器，返回一串被 UTF-8 替换字符打烂的内容既没有展示价值，
	// 又会诱使人把它存回去
	if res.Binary {
		res.Content = ""
	}
	s.log.Info("工作树读文件完成", "root", root, "rel", rel,
		"bytes", len(res.Content), "size", res.Size,
		"truncated", res.Truncated, "binary", res.Binary)
	writeJSON(w, http.StatusOK, res)
```

> **与 spec §2.1 的一处偏离，有意为之**：spec 写的是「`Binary` 为真时 `Content` 是空串」，落在 `FileRead` 这个类型上。实现改为在 workspace 端点置空，因为 `ReadFile` 是共用的——在那里置空会让 `handoff fetch` 对二进制文件的输出从「原始字节」变成「空」，违反 Global Constraints 第三条。`proto.FileRead` 的注释里不再承诺 Binary 时正文为空。

- [ ] **Step 7: 修既有测试的编译错误**

`internal/agentd/workspace_test.go:307` 的 `TestReadFileSizeCap` 与 `workspace_minor_test.go:22` 都按旧的 `string` 返回写的。改法：
- 取内容改成 `.Content`
- `TestReadFileSizeCap` 里断言「正文含截断提示」的部分**移到** `handleTaskFile` 的端点测试里（那才是提示现在的归属），本函数只断言 `Truncated == true` 与 `len(Content) == maxRunOutput`

在 `internal/agentd/workspacefiles_test.go`（或既有的 server 端点测试文件）补一条 CLI 契约回归防线：

```go
// TestTaskFileKeepsTruncatedNotice 是 CLI 契约的回归防线：GET /api/tasks/{id}/file
// 在截断时仍必须把那行中文提示拼进正文，否则 handoff fetch 的输出静默变样。
func TestTaskFileKeepsTruncatedNotice(t *testing.T) {
	// …建一个 maxRunOutput+4096 字节的文件，走 handleTaskFile，断言：
	//   1. body["content"] 以 truncatedNotice(size) 结尾
	//   2. len(body["content"]) == maxRunOutput + len(notice)
}
```

- [ ] **Step 8: 加关键节点日志**

本 task 的日志已随代码落在这几处，逐条确认：
- `ReadFile` 截断：既有 `Warn`（带 `size` / `limit`）保留
- `handleWorkspaceFile` 成功出口：`Info` 带 `rel` / `bytes` / `size` / `truncated` / `binary`——**成功路径不静默**，而且这几个布尔值正是前端三态的来源，排障时要能直接对上
- `handleTaskFile` 成功出口：既有 `Info` 保留
- **不打 `res.Content`**，任何级别都不打

- [ ] **Step 9: 加意图注释**

- `proto.FileRead` 的类型注释：为什么是结构体、`SHA256` 为什么只在完整文本时才有值、`Size` 为什么不是 `len(Content)`（Step 3 已写全，此处只做确认）
- `binaryProbeBytes` / `isBinaryPrefix`：判据来源与为什么够用
- `ReadFile` 文件头「大小上限」段：改写成「截断结论由本函数给出，截断**提示**由端点层按各自契约拼」
- `handleTaskFile`：为什么提示留在这里（Step 5 已写）
- `handleWorkspaceFile`：为什么二进制在端点层置空而不在 `ReadFile` 里置空（Step 6 已写）

- [ ] **Step 10: 跑测试确认通过**

```bash
go test ./internal/... && go vet ./... && gofmt -l .
```
Expected: PASS，`gofmt -l` 无输出

- [ ] **Step 11: 提交**

```bash
git add internal/proto/projects.go internal/agentd/workspace.go internal/agentd/workspacefiles.go internal/agentd/server.go internal/agentd/workspace_read_struct_test.go internal/agentd/workspace_test.go internal/agentd/workspace_minor_test.go internal/agentd/workspacefiles_test.go
git commit -m "feat(agentd): ReadFile 返回 FileRead 结构体，截断提示搬到 CLI 端点"
```

---

## Task 2: 白名单拒绝从 403 改成 400

`workspacefiles.go` 的文件头注释已随 PTY 落地更正为「这是参数校验，不是安全边界」，但状态码还留在旧说辞上——不命中返回 403「你没有权限浏览这个目录」。留着 403 等于继续宣称一个不存在的安全边界（PTY spec §1 已证伪：控制台会话在能力上等价于主令牌）。

**Files:**
- Modify: `internal/agentd/workspacefiles.go:85-102`（`workspaceRootOrErr`）
- Test: `internal/agentd/workspacefiles_test.go`

**Interfaces:**
- Produces: `workspaceRootOrErr` 在白名单不命中时写 **400**，错误文案不变（「路径不是本机已探测到的工作树，拒绝访问」）
- Consumes: 无

- [ ] **Step 1: 写失败的测试**

在 `internal/agentd/workspacefiles_test.go` 里，把既有那条断言 403 的用例改成 400，并补一条说明性用例：

```go
// TestWorkspaceWhitelistRejectsWith400 钉住状态码语义：白名单是**参数校验**
// 不是权限边界，所以是 400 不是 403。403 会让人以为控制台会话比主令牌弱，
// 而它们在能力上等价（PTY spec §1）。
func TestWorkspaceWhitelistRejectsWith400(t *testing.T) {
	// …构造一个不在登记表里的 path，打 GET /api/workspaces/dir，断言：
	//   resp.StatusCode == http.StatusBadRequest
	//   body["error"] == "路径不是本机已探测到的工作树，拒绝访问"
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestWorkspaceWhitelist -v`
Expected: FAIL，`状态码=403, want 400`

- [ ] **Step 3: 改状态码与注释**

```go
	root, ok := s.resolveWorkspace(r.Context(), path)
	if !ok {
		// 400 而不是 403：白名单是**参数校验**，不是安全边界。控制台会话在
		// 能力上等价于主令牌（auth 中间件让两者落在同一个 mux 上，其中包含
		// POST /api/tasks/{id}/run 的 sh -c），所以这里挡不住任何有心人，
		// 它挡的是「前端传了个打错的路径，把 agentd 变成任意目录浏览器」。
		// 403 会宣称一个不存在的权限模型，误导排障的人往鉴权方向找。
		// 完整论证见 docs/superpowers/specs/2026-08-12-w4-pty-terminal-design.md §1
		s.log.Warn("工作树白名单拒绝", "path", path, "url_path", r.URL.Path)
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "路径不是本机已探测到的工作树，拒绝访问"})
		return "", false
	}
```

同时把 `handleWorkspaceDir` / `handleWorkspaceFile` 的函数注释里「否则 403」改成「否则 400」。

- [ ] **Step 4: 排查破坏面**

```bash
grep -rn "403" web/src --include="*.ts" --include="*.tsx" | grep -v node_modules
grep -rn "StatusForbidden" internal/client/
```
Expected: 前端只显示 `error` 文本不看码；CLI 不走这两个端点。任一处有对 403 的分支判断，就在本 step 一并改掉并在提交信息里说明。

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/agentd/ && go vet ./internal/agentd/`
Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add internal/agentd/workspacefiles.go internal/agentd/workspacefiles_test.go
git commit -m "fix(agentd): 工作树白名单拒绝改 400，不再宣称不存在的权限边界"
```

---

## Task 3: `WriteFile` —— 原子替换与全部拒绝面

写入的全部判断力在这一层。HTTP 层（Task 4）只做状态码映射。

**Files:**
- Modify: `internal/agentd/workspace.go`（新增哨兵错误、`isGitPath`、`WriteFile`、`atomicReplace`）
- Test: `internal/agentd/workspace_write_test.go`（新建）

**Interfaces:**
- Consumes: Task 1 的 `proto.FileRead`、`ReadFile`、`isBinaryPrefix`；既有 `maxRunOutput`、`ErrPathEscape` / `ErrPathIsDir` / `ErrNotRegularFile`、`rootErrIsEscape`
- Produces:
  - `agentd.ErrGitDirWrite` / `ErrSymlinkTarget` / `ErrBinaryFile` / `ErrFileTooLarge` / `ErrBaseMismatch`
  - `agentd.WriteFile(repo, rel, content, baseSHA256 string) (proto.FileRead, error)`
  - `agentd.isGitPath(cleaned string) bool`

- [ ] **Step 1: 写失败的测试**

新建 `internal/agentd/workspace_write_test.go`：

```go
package agentd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sha256hex 是测试里算基线哈希的小工具，与 ReadFile 的算法保持一致。
func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// seedFile 在临时工作树里放一个文件，返回工作树根与它的基线哈希。
func seedFile(t *testing.T, name, body string, mode fs.FileMode) (string, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	return dir, sha256hex(body)
}

// TestWriteFileHappyPath 验证基线匹配时写入成功，返回新哈希与新大小，磁盘确已更新。
func TestWriteFileHappyPath(t *testing.T) {
	dir, base := seedFile(t, "go.mod", "module handoff\n", 0o644)
	next := "module handoff\n\ngo 1.26.1\n"
	got, err := WriteFile(dir, "go.mod", next, base)
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if got.SHA256 != sha256hex(next) {
		t.Errorf("返回的 SHA256=%q, want %q", got.SHA256, sha256hex(next))
	}
	if got.Size != int64(len(next)) {
		t.Errorf("返回的 Size=%d, want %d", got.Size, len(next))
	}
	onDisk, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != next {
		t.Errorf("磁盘内容=%q, want %q", onDisk, next)
	}
}

// TestWriteFileConflict 验证基线对不上时返回 ErrBaseMismatch，且**带回磁盘现状**
// （省掉前端一次往返，冲突条要靠它显示「磁盘上现在是什么」）。
func TestWriteFileConflict(t *testing.T) {
	dir, _ := seedFile(t, "a.txt", "executor 改过的内容\n", 0o644)
	cur, err := WriteFile(dir, "a.txt", "我的改动\n", sha256hex("我读到的旧内容\n"))
	if !errors.Is(err, ErrBaseMismatch) {
		t.Fatalf("err=%v, want ErrBaseMismatch", err)
	}
	if cur.Content != "executor 改过的内容\n" {
		t.Errorf("冲突返回的 Content=%q, want 磁盘现状", cur.Content)
	}
	if cur.SHA256 != sha256hex("executor 改过的内容\n") {
		t.Errorf("冲突返回的 SHA256 必须是磁盘现状的哈希，才能当下一次的基线")
	}
	onDisk, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
	if string(onDisk) != "executor 改过的内容\n" {
		t.Error("冲突时磁盘必须原封不动")
	}
}

// TestWriteFileEmptyBaseIsConflict 验证空基线一律当冲突处理：调用方没读过就想写，
// 正是覆盖别人改动的场景。
func TestWriteFileEmptyBaseIsConflict(t *testing.T) {
	dir, _ := seedFile(t, "a.txt", "x\n", 0o644)
	if _, err := WriteFile(dir, "a.txt", "y\n", ""); !errors.Is(err, ErrBaseMismatch) {
		t.Fatalf("err=%v, want ErrBaseMismatch", err)
	}
}

// TestWriteFileRejects 逐条钉住拒绝面。
func TestWriteFileRejects(t *testing.T) {
	t.Run("git 目录", func(t *testing.T) {
		dir, base := seedFile(t, ".git/config", "[core]\n", 0o644)
		if _, err := WriteFile(dir, ".git/config", "[core]\n\tpager = sh -c evil\n", base); !errors.Is(err, ErrGitDirWrite) {
			t.Fatalf("err=%v, want ErrGitDirWrite", err)
		}
	})
	t.Run("git 指针文件本身", func(t *testing.T) {
		dir, base := seedFile(t, ".git", "gitdir: /elsewhere\n", 0o644)
		if _, err := WriteFile(dir, ".git", "gitdir: /evil\n", base); !errors.Is(err, ErrGitDirWrite) {
			t.Fatalf("err=%v, want ErrGitDirWrite", err)
		}
	})
	t.Run("符号链接", func(t *testing.T) {
		dir, base := seedFile(t, "real.txt", "x\n", 0o644)
		if err := os.Symlink("real.txt", filepath.Join(dir, "link.txt")); err != nil {
			t.Skipf("本平台建不了符号链接: %v", err)
		}
		if _, err := WriteFile(dir, "link.txt", "y\n", base); !errors.Is(err, ErrSymlinkTarget) {
			t.Fatalf("err=%v, want ErrSymlinkTarget", err)
		}
	})
	t.Run("目录", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := WriteFile(dir, "sub", "x", "deadbeef"); !errors.Is(err, ErrPathIsDir) {
			t.Fatalf("err=%v, want ErrPathIsDir", err)
		}
	})
	t.Run("现盘是二进制", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "logo.png"), []byte("\x89PNG\x00\x00"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := WriteFile(dir, "logo.png", "text", "deadbeef"); !errors.Is(err, ErrBinaryFile) {
			t.Fatalf("err=%v, want ErrBinaryFile", err)
		}
	})
	t.Run("现盘超限", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "big.txt"), bytes.Repeat([]byte("x"), maxRunOutput+1), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := WriteFile(dir, "big.txt", "small", "deadbeef"); !errors.Is(err, ErrFileTooLarge) {
			t.Fatalf("err=%v, want ErrFileTooLarge", err)
		}
	})
	t.Run("新内容超限", func(t *testing.T) {
		dir, base := seedFile(t, "a.txt", "x\n", 0o644)
		huge := strings.Repeat("y", maxRunOutput+1)
		if _, err := WriteFile(dir, "a.txt", huge, base); !errors.Is(err, ErrFileTooLarge) {
			t.Fatalf("err=%v, want ErrFileTooLarge", err)
		}
	})
	t.Run("路径逃逸", func(t *testing.T) {
		dir, _ := seedFile(t, "a.txt", "x\n", 0o644)
		if _, err := WriteFile(dir, "../outside.txt", "y", "deadbeef"); !errors.Is(err, ErrPathEscape) {
			t.Fatalf("err=%v, want ErrPathEscape", err)
		}
	})
	t.Run("不存在", func(t *testing.T) {
		dir := t.TempDir()
		if _, err := WriteFile(dir, "nope.txt", "y", "deadbeef"); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("err=%v, want fs.ErrNotExist", err)
		}
	})
}

// TestWriteFileKeepsMode 验证可执行位不被写丢——那是个静默故障：脚本还在，
// 但下次跑它会 permission denied。
func TestWriteFileKeepsMode(t *testing.T) {
	dir, base := seedFile(t, "run.sh", "#!/bin/sh\necho a\n", 0o755)
	if _, err := WriteFile(dir, "run.sh", "#!/bin/sh\necho b\n", base); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	fi, err := os.Stat(filepath.Join(dir, "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Errorf("mode=%v, want 0755", fi.Mode().Perm())
	}
}

// TestWriteFileNoTmpLeftBehind 验证被拒的写入不在工作树里留 tmp 文件。
// 留下的话会进 git status，下一次 dispatch 的「工作区必须干净」检查直接拒发。
func TestWriteFileNoTmpLeftBehind(t *testing.T) {
	dir, _ := seedFile(t, "a.txt", "x\n", 0o644)
	_, _ = WriteFile(dir, "a.txt", "y\n", "对不上的哈希")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("留下了临时文件 %s", e.Name())
		}
	}
}

// TestIsGitPath 钉住 .git 判据的边界：.gitignore 这类前缀相同但不同的路径不能误伤。
func TestIsGitPath(t *testing.T) {
	yes := []string{".git", ".git/config", ".git/hooks/pre-commit", "./.git/HEAD"}
	no := []string{".gitignore", ".gitattributes", "a/.gitmodules", "src/git/x.go"}
	for _, p := range yes {
		if !isGitPath(filepath.Clean(p)) {
			t.Errorf("isGitPath(%q)=false, want true", p)
		}
	}
	for _, p := range no {
		if isGitPath(filepath.Clean(p)) {
			t.Errorf("isGitPath(%q)=true, want false", p)
		}
	}
}
```

> 判据只挡**工作树根下**的 `.git`（`a/.git/config` 这种嵌套子模块不在本期范围，工作树里也不该有）。测试用例里 `a/.gitmodules` 属于「不挡」一侧正是这个意思。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run 'TestWriteFile|TestIsGitPath' -v`
Expected: 编译失败——`undefined: WriteFile`、`undefined: ErrGitDirWrite`、`undefined: isGitPath`

- [ ] **Step 3: 加哨兵错误**

在 `internal/agentd/workspace.go:58` 那个 `var (...)` 块里追加：

```go
	// 以下五个是在线编辑（B81）的拒绝面。文案就是 HTTP 层原样吐给用户的中文，
	// 所以每条都要能独立读懂——「操作失败」帮不上任何人
	ErrGitDirWrite   = errors.New("不允许写入 .git 目录")
	ErrSymlinkTarget = errors.New("目标是符号链接，不支持在线编辑")
	ErrBinaryFile    = errors.New("二进制文件不支持在线编辑")
	ErrFileTooLarge  = errors.New("文件超过 1 MB，不支持在线编辑")
	ErrBaseMismatch  = errors.New("文件已被改动")
```

- [ ] **Step 4: 写 `isGitPath` 与 `atomicReplace`**

```go
// isGitPath 判定一个已 Clean 的相对路径是否落在工作树根的 .git 下。
//
// 为什么要挡：`.git` 在 worktree 里是个几十字节的指针文件（内容 `gitdir: <路径>`），
// 改它能把整个工作树重指向别处；在主仓库里 `.git/config` 写进 core.pager /
// core.sshCommand / hooksPath，就是下一次任何 git 操作时的任意命令执行，改 HEAD、
// 删 index 也都能直接搞坏仓库。
//
// 这不是提权（控制台会话本来就与主令牌等价，见 spec §1.1），是「一次误操作就把
// 仓库弄坏」——正是那条参数校验闸门该挡的东西。
//
// 只挡工作树**根下**的 .git：嵌套子模块不在本期范围。前缀相同的 .gitignore /
// .gitattributes 不受影响。
//
// 参数：
//   - cleaned: 已经 filepath.Clean 过的相对路径
func isGitPath(cleaned string) bool {
	return cleaned == ".git" || strings.HasPrefix(cleaned, ".git"+string(filepath.Separator))
}

// atomicReplace 用同目录临时文件 + rename 原子替换目标文件。
//
// 为什么做原子替换（而不是像 orca 那样对用户文件裸 WriteFile）：executor 就在
// 同一个工作树里跑，裸覆盖有一个窗口能让它读到半截文件。orca 的编辑对象通常
// 没有一个高频读者在旁边，我们有。
//
// 为什么**不** fsync：工作树在 git 管着，掉电丢一次编辑不是灾难，而每次保存
// fsync 的代价在远程机上更明显。orca 只对自己的状态文件做 fsync——那些丢了
// 没有第二份，工作树文件不是。
//
// 参数：
//   - root: 已打开的工作树 Root（全程不出根）
//   - cleaned: 目标文件的相对路径
//   - data: 新内容
//   - perm: 目标文件原有的权限位（保留可执行位，丢了是静默故障）
func atomicReplace(root *os.Root, cleaned string, data []byte, perm fs.FileMode) error {
	tmp := filepath.Join(filepath.Dir(cleaned),
		fmt.Sprintf(".%s.%d.%d.tmp", filepath.Base(cleaned), os.Getpid(), time.Now().UnixNano()))
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return fmt.Errorf("创建临时文件 %s: %w", tmp, err)
	}
	// 任何没走到 rename 的路径（含 panic）都要把 tmp 删掉：留一个 .foo.tmp
	// 在工作树里会进 git status，下一次 dispatch 的「工作区必须干净」检查会直接拒发
	committed := false
	defer func() {
		if !committed {
			_ = f.Close()
			if err := root.Remove(tmp); err != nil {
				log().Warn("清理临时文件失败", "tmp", tmp, "cause", err)
			}
		}
	}()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("写临时文件 %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("关闭临时文件 %s: %w", tmp, err)
	}
	if err := root.Rename(tmp, cleaned); err != nil {
		return fmt.Errorf("替换文件 %s: %w", cleaned, err)
	}
	committed = true
	return nil
}
```

import 块需要补 `"io/fs"` 与 `"time"`（若尚未存在）。

- [ ] **Step 5: 写 `WriteFile`**

```go
// WriteFile 用新内容原子替换工作树内一个已存在的文本文件，带基线哈希前置条件。
//
// 冲突保护的整个机制就是这个前置条件：调用方把它**读到那一版**的 sha256 带回来，
// 本函数比对磁盘现状，不一致就拒绝并把现状带回去。为什么不用 mtime：executor 在
// 工作树里频繁跑 git 操作，checkout/rebase 会动 mtime 但不动内容，用 mtime 会把
// 大量无害情况报成冲突。
//
// **已知窗口，如实记录**：第 6 步读哈希与第 9 步 rename 之间不是原子的。executor
// 恰好在这个窗口里写同一个文件，本函数检测不到，结果是它的改动被覆盖。加锁解决
// 不了——锁只挡得住 agentd 自己的并发写，executor 直接动文件系统，根本不经过这里。
// 窗口从「整个编辑时长」缩到「一次读 + 一次 rename」，是这条路能拿到的全部。
//
// 参数：
//   - repo: 工作树绝对路径（调用方必须已过白名单闸门，本函数不做白名单判定）
//   - rel: 相对工作树根的路径
//   - content: 新内容
//   - baseSHA256: 调用方读到那一版的 sha256 十六进制串；空串一律判为不匹配
//
// 返回：
//   - err == nil：res 是**新内容**的结论（SHA256 可直接当下一次的基线，Size 是新大小）
//   - errors.Is(err, ErrBaseMismatch)：res 是**磁盘现状**（含正文与现状哈希），
//     给调用方省一次往返
//   - 其余错误：res 是零值
//   - 错误取值：ErrPathEscape / ErrGitDirWrite / ErrSymlinkTarget / ErrPathIsDir /
//     ErrNotRegularFile / ErrBinaryFile / ErrFileTooLarge / ErrBaseMismatch /
//     fs.ErrNotExist（含 %w 链）
func WriteFile(repo, rel, content, baseSHA256 string) (proto.FileRead, error) {
	cleaned := filepath.Clean(rel)
	if rel == "" || cleaned == "." || filepath.IsAbs(cleaned) ||
		cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		log().Warn("文件写入路径逃逸被拒绝", "repo", repo, "path", rel)
		return proto.FileRead{}, fmt.Errorf("%w: %q", ErrPathEscape, rel)
	}
	if isGitPath(cleaned) {
		log().Warn("文件写入命中 .git 被拒绝", "repo", repo, "path", rel)
		return proto.FileRead{}, fmt.Errorf("%w: %q", ErrGitDirWrite, rel)
	}
	// 新内容也受同一个上限约束。理由对称：读不回来的东西就不该写得进去，
	// 否则存一次之后这个文件自己就变成不可编辑的了
	if len(content) > maxRunOutput {
		log().Warn("写入内容超过上限被拒绝", "repo", repo, "path", rel,
			"bytes", len(content), "limit", maxRunOutput)
		return proto.FileRead{}, fmt.Errorf("%w: %q", ErrFileTooLarge, rel)
	}
	root, err := os.OpenRoot(repo)
	if err != nil {
		return proto.FileRead{}, fmt.Errorf("打开工作树 %s: %w", repo, err)
	}
	defer root.Close()

	// Lstat 而不是 Stat：要看到符号链接本身。原子替换的 rename 会把链接换成普通
	// 文件，语义悄悄就变了——与其猜用户想改链接还是改目标，不如拒掉并说清楚
	fi, err := root.Lstat(cleaned)
	if err != nil {
		if rootErrIsEscape(err) {
			log().Warn("文件写入路径逃逸被拒绝", "repo", repo, "path", rel)
			return proto.FileRead{}, fmt.Errorf("%w: %q", ErrPathEscape, rel)
		}
		return proto.FileRead{}, fmt.Errorf("检查文件 %s: %w", filepath.Join(repo, cleaned), err)
	}
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		log().Warn("文件写入目标是符号链接被拒绝", "repo", repo, "path", rel)
		return proto.FileRead{}, fmt.Errorf("%w: %q", ErrSymlinkTarget, rel)
	case fi.IsDir():
		log().Warn("文件写入目标是目录被拒绝", "repo", repo, "path", rel)
		return proto.FileRead{}, fmt.Errorf("%w: %q", ErrPathIsDir, rel)
	case !fi.Mode().IsRegular():
		log().Warn("文件写入目标不是普通文件被拒绝", "repo", repo, "path", rel,
			"mode", fi.Mode().String())
		return proto.FileRead{}, fmt.Errorf("%w: %q", ErrNotRegularFile, rel)
	}

	// 复用 ReadFile 而不是另写一遍读取：「这文件能不能编辑」的判据必须由**同一段
	// 代码**在读侧和写侧给出。两边各判一次，早晚会分叉成「前端说能编辑、后端说不能」
	cur, err := ReadFile(repo, cleaned)
	if err != nil {
		return proto.FileRead{}, err
	}
	// 这两种情况下 cur.SHA256 必然是空值，下面的比对必定不通过——但要在这里用
	// **说得清的理由**拒掉，而不是让它掉进一个「哈希对不上」的 409，
	// 那会让用户以为「文件被谁改了」
	if cur.Binary {
		log().Warn("文件写入目标是二进制被拒绝", "repo", repo, "path", rel)
		return proto.FileRead{}, fmt.Errorf("%w: %q", ErrBinaryFile, rel)
	}
	if cur.Truncated {
		log().Warn("文件写入目标超过读取上限被拒绝", "repo", repo, "path", rel, "size", cur.Size)
		return proto.FileRead{}, fmt.Errorf("%w: %q", ErrFileTooLarge, rel)
	}
	if cur.SHA256 != baseSHA256 {
		log().Warn("文件写入基线不匹配", "repo", repo, "path", rel,
			"base", shortHash(baseSHA256), "current", shortHash(cur.SHA256))
		return cur, fmt.Errorf("%w: %q", ErrBaseMismatch, rel)
	}

	log().Info("开始原子替换文件", "repo", repo, "path", rel, "bytes", len(content))
	if err := atomicReplace(root, cleaned, []byte(content), fi.Mode().Perm()); err != nil {
		log().Error("文件写入失败", "repo", repo, "path", rel, "cause", err)
		return proto.FileRead{}, err
	}
	sum := sha256.Sum256([]byte(content))
	res := proto.FileRead{
		Content: content,
		Size:    int64(len(content)),
		SHA256:  hex.EncodeToString(sum[:]),
	}
	log().Info("文件写入完成", "repo", repo, "path", rel,
		"bytes", res.Size, "sha256", shortHash(res.SHA256))
	return res, nil
}

// shortHash 取哈希前 8 位供日志用。全量 64 位十六进制串在日志里既占地方又没人读，
// 而排障时要的只是「这两个是不是同一个」。
func shortHash(h string) string {
	if len(h) <= 8 {
		return h
	}
	return h[:8]
}
```

- [ ] **Step 6: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run 'TestWriteFile|TestIsGitPath' -v`
Expected: 全部 PASS

- [ ] **Step 7: 自查关键节点日志**

对照 `instrumenting-code` 逐条确认：
- 进入 → 每个拒绝分支都有 `Warn` 带 `path` 与原因（8 条：逃逸 / .git / 新内容超限 / 符号链接 / 目录 / 非普通文件 / 二进制 / 现盘超限）
- 冲突 → `Warn` 带两个哈希的前 8 位
- 原子替换前 `Info`、失败 `Error` 带 cause
- **成功出口 `Info`**——成功路径不静默
- `tmp` 清理失败 `Warn`（它会污染 git status，静默吞掉会让人在 dispatch 被拒时找不到原因）
- **`content` 不出现在任何一条日志里**

- [ ] **Step 8: 自查意图注释**

- `WriteFile`：为什么用哈希不用 mtime、TOCTOU 窗口为什么加锁也解决不了、为什么复用 `ReadFile`、二进制/截断为什么要在比对之前单独拒
- `isGitPath`：为什么拒 `.git`（两种破坏面写清楚）、为什么只挡根下
- `atomicReplace`：为什么原子替换、为什么不 fsync、为什么保留 mode、tmp 为什么必须清
- `Lstat` 那行：为什么不是 `Stat`
- `shortHash`：为什么只打前 8 位

- [ ] **Step 9: 提交**

```bash
go test ./internal/... && go vet ./... && gofmt -l .
git add internal/agentd/workspace.go internal/agentd/workspace_write_test.go
git commit -m "feat(agentd): WriteFile 原子替换 + 哈希前置条件 + 完整拒绝面"
```

---

## Task 4: `PUT /api/workspaces/file` 端点

HTTP 层只做两件事：解请求体、把 Task 3 的错误映射成状态码。判断力全在 `WriteFile` 里。

**Files:**
- Modify: `internal/proto/projects.go`（追加三个请求/响应类型）
- Modify: `internal/agentd/workspacefiles.go`（新增 `handleWorkspaceFileWrite`）
- Modify: `internal/agentd/server.go:188-226`（路由与顶部路由清单注释）
- Modify: `internal/proto/contract_fixture_test.go`（四个新类型进 fixture）
- Test: `internal/agentd/workspacefiles_test.go`

**Interfaces:**
- Consumes: Task 3 的 `WriteFile` 与全部哨兵错误；Task 2 改过的 `workspaceRootOrErr`
- Produces: `proto.FileWriteReq{Content, BaseSHA256}`、`proto.FileWriteResp{SHA256, Size}`、`proto.FileConflictResp{Error, Current}`；路由 `PUT /api/workspaces/file`

- [ ] **Step 1: 写失败的测试**

在 `internal/agentd/workspacefiles_test.go` 追加：

```go
// TestWorkspaceFileWriteOK 走完整 HTTP 链路：白名单 → 写入 → 200 + 新哈希。
func TestWorkspaceFileWriteOK(t *testing.T) {
	// …起测试 Server，登记一个工作树，PUT /api/workspaces/file?path=&rel=go.mod
	// body: {"content":"module handoff\n\ngo 1.26.1\n","base_sha256":"<旧哈希>"}
	// 断言 200，body.sha256 == 新内容哈希，body.size == 新长度
}

// TestWorkspaceFileWriteConflict 验证 409 的 body 带着 current（省掉前端一次往返）。
func TestWorkspaceFileWriteConflict(t *testing.T) {
	// …用一个对不上的 base_sha256 发 PUT，断言：
	//   409
	//   body.error == "文件已被改动"（含路径后缀，用 strings.Contains 断言前缀）
	//   body.current.content == 磁盘现状
	//   body.current.sha256 != ""
}

// TestWorkspaceFileWriteStatusMap 逐条钉住错误到状态码的映射。
func TestWorkspaceFileWriteStatusMap(t *testing.T) {
	// 表驱动，覆盖：
	//   白名单不中 → 400   缺 rel → 400        .git → 400
	//   符号链接 → 400     目录 → 400          二进制 → 400
	//   超限 → 400         逃逸 → 400          不存在 → 404
	//   哈希不匹配 → 409   请求体不是合法 JSON → 400
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestWorkspaceFileWrite -v`
Expected: 404（路由没注册）或编译失败（`proto.FileWriteReq` 未定义）

- [ ] **Step 3: 加 proto 类型**

在 `internal/proto/projects.go` 的 `FileRead` 之后追加：

```go
// FileWriteReq 是 PUT /api/workspaces/file 的请求体。
//
// BaseSHA256 必填：它是调用方**读到那一版**的哈希，服务端拿它与磁盘现状比对，
// 不一致就 409。空串一律判为不匹配——没读过就想写，正是覆盖别人改动的场景。
type FileWriteReq struct {
	Content    string `json:"content"`
	BaseSHA256 string `json:"base_sha256"`
}

// FileWriteResp 是写入成功后的响应。
//
// SHA256 是**新内容**的哈希，调用方直接拿它当下一次写入的 base_sha256，
// 不需要为了拿新基线再读一次。
type FileWriteResp struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// FileConflictResp 是 409 的响应体。
//
// 带上 Current（磁盘现状的完整读取结论）是为了让冲突界面一次成型：用户要在
// 「放弃我的改动」和「用我的内容覆盖」之间选，两个动作都需要磁盘现状——前者
// 要它的正文，后者要它的哈希当新基线。分两次请求会在两次之间再开一个窗口。
type FileConflictResp struct {
	Error   string   `json:"error"`
	Current FileRead `json:"current"`
}
```

- [ ] **Step 4: 写端点**

`internal/agentd/workspacefiles.go` 追加：

```go
// handleWorkspaceFileWrite 处理 PUT /api/workspaces/file?path=&rel=[&machine=]。
//
// 判断力全在 WriteFile 里，本函数只做三件事：解请求体、调它、把哨兵错误映射成
// 状态码。**中文错误原文原样透传**，不吞成「操作失败」——用户看到「不允许写入
// .git 目录」能立刻明白，看到「操作失败」只能来问。
//
// 参数（查询串）：
//   - path: 工作树绝对路径（必须命中白名单，否则 400）
//   - rel: 工作树内的相对路径（必须）
//   - machine: 可选，转发到指定机器（复用 forwardIfRequested，与两个读端点同一条路）
func (s *Server) handleWorkspaceFileWrite(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	rel := r.URL.Query().Get("rel")
	root, ok := s.workspaceRootOrErr(w, r)
	if !ok {
		return
	}
	if rel == "" {
		s.log.Warn("工作树写文件缺 rel 参数", "root", root)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少 rel 参数"})
		return
	}
	var req proto.FileWriteReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.log.Warn("工作树写文件请求体解析失败", "root", root, "rel", rel, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体不是合法 JSON"})
		return
	}
	// 请求体里的 content 可能有几百 KB，日志只记长度不记内容
	s.log.Info("工作树写文件请求", "root", root, "rel", rel,
		"bytes", len(req.Content), "base", shortHash(req.BaseSHA256))

	res, err := WriteFile(root, rel, req.Content, req.BaseSHA256)
	if err != nil {
		switch {
		case errors.Is(err, ErrBaseMismatch):
			// 409 的 body 带磁盘现状：冲突界面的两个出口都要用它
			s.log.Warn("工作树写文件冲突", "root", root, "rel", rel,
				"base", shortHash(req.BaseSHA256), "current", shortHash(res.SHA256))
			writeJSON(w, http.StatusConflict, proto.FileConflictResp{
				Error: "文件已被改动", Current: res})
		case errors.Is(err, ErrPathEscape):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "路径不合法（不允许逃出工作树）"})
		case errors.Is(err, ErrGitDirWrite):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "不允许写入 .git 目录"})
		case errors.Is(err, ErrSymlinkTarget):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "目标是符号链接，不支持在线编辑"})
		case errors.Is(err, ErrPathIsDir):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "路径是目录，不是文件"})
		case errors.Is(err, ErrNotRegularFile):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "路径不是普通文件"})
		case errors.Is(err, ErrBinaryFile):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "二进制文件不支持在线编辑"})
		case errors.Is(err, ErrFileTooLarge):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "文件超过 1 MB，不支持在线编辑"})
		case errors.Is(err, fs.ErrNotExist):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "文件不存在"})
		default:
			s.log.Error("工作树写文件失败", "root", root, "rel", rel, "cause", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "写入文件失败"})
		}
		return
	}
	s.log.Info("工作树写文件完成", "root", root, "rel", rel,
		"bytes", res.Size, "sha256", shortHash(res.SHA256))
	writeJSON(w, http.StatusOK, proto.FileWriteResp{SHA256: res.SHA256, Size: res.Size})
}
```

import 补 `"encoding/json"`、`"io/fs"`（`errors`/`net/http`/`proto` 已有）。

> 除 `ErrBaseMismatch` 外的分支不再各打一行 `Warn`：`WriteFile` 内部每个拒绝点已经打过了，这里再打一遍是重复噪声。

- [ ] **Step 5: 注册路由**

`internal/agentd/server.go`：

```go
	mux.HandleFunc("PUT /api/workspaces/file", s.handleWorkspaceFileWrite)
```

放在 `mux.HandleFunc("GET /api/workspaces/file", ...)` 之后。同时更新文件顶部路由清单注释（`:188-193` 那段）：

```go
//   - PUT  /api/workspaces/file         写工作树内单个文件（同上白名单，带哈希前置条件）
```

- [ ] **Step 6: 契约 fixture 补四个新类型**

在 `internal/proto/contract_fixture_test.go` 的 `cases` 里追加：

```go
		{"FileRead", fileReadSample()},
		{"FileWriteReq", fileWriteReqSample()},
		{"FileWriteResp", fileWriteRespSample()},
		{"FileConflictResp", fileConflictSample()},
```

并在文件末尾按既有 `dirListSample` 的样式写四个样本函数。样本要**代表性**：`fileReadSample` 用一份可编辑文本（有 sha256、非截断非二进制），因为那才是常态。

跑：

```bash
go test ./internal/proto/ -run TestContractFixtures -update
git diff internal/proto/testdata/
```
Expected: 只新增四个 `.json`，既有 fixture 一字未动。**逐字 review 这个 diff**——既有 fixture 变了就说明改坏了别的契约。

- [ ] **Step 7: 跑测试确认通过**

```bash
go build ./... && go test ./... && go vet ./... && gofmt -l .
```
Expected: 全绿

- [ ] **Step 8: 自查日志与注释**

- 进入写请求 `Info`（带 `bytes` 与 base 前 8 位，**不带 content**）
- 409 `Warn` 带两个哈希前 8 位
- 500 `Error` 带 cause
- 成功出口 `Info`
- 端点注释写清参数与「判断力在 WriteFile 里」这条边界
- 三个 proto 类型各自的注释（Step 3 已写全）

- [ ] **Step 9: 提交**

```bash
git add internal/proto/ internal/agentd/workspacefiles.go internal/agentd/server.go internal/agentd/workspacefiles_test.go
git commit -m "feat(agentd): 新增 PUT /api/workspaces/file 写端点"
```

> **后端到此完整可交付**：这时 `curl -X PUT` 就能改工作树里的文件并拿到冲突保护，前端还没接。工期不够时在这里停是安全的。

---

## Task 5: 前端 API 客户端

**Files:**
- Modify: `web/src/api/types.ts`
- Modify: `web/src/api/client.ts:37-50`（`ApiError`）、`:52-63`（`bodyOrError`）、`:65-75`（`parseResponse`）、`:215-219`（`fetchWorkspaceFile`）
- Test: `web/src/api/client.test.ts`（若不存在则新建）

**Interfaces:**
- Consumes: Task 4 的端点契约
- Produces:
  - `types.ts`：`FileRead`、`FileWriteResp`、`FileConflictResp`
  - `client.ts`：`ApiError` 多一个 `readonly body: unknown`；`fetchWorkspaceFile(...): Promise<FileRead>`；`writeWorkspaceFile(path, rel, req, machine?): Promise<FileWriteResp>`

- [ ] **Step 1: 写失败的测试**

`web/src/api/client.test.ts`：

```ts
import { describe, expect, it, vi, afterEach } from 'vitest'
import { ApiError, writeWorkspaceFile } from './client'

afterEach(() => vi.unstubAllGlobals())

function stubFetch(status: number, body: unknown) {
  vi.stubGlobal(
    'fetch',
    vi.fn().mockResolvedValue(
      new Response(JSON.stringify(body), {
        status,
        headers: { 'Content-Type': 'application/json' },
      }),
    ),
  )
}

describe('writeWorkspaceFile', () => {
  it('成功时返回新哈希与新大小', async () => {
    stubFetch(200, { sha256: 'abc123', size: 42 })
    await expect(writeWorkspaceFile('/w/b2-b3', 'go.mod', { content: 'x', base_sha256: 'old' }))
      .resolves.toEqual({ sha256: 'abc123', size: 42 })
  })

  it('409 抛的 ApiError 必须带上 body——冲突界面的两个出口都要用 current', async () => {
    stubFetch(409, {
      error: '文件已被改动',
      current: { content: '别人的内容', size: 6, sha256: 'newhash' },
    })
    const err = await writeWorkspaceFile('/w/b2-b3', 'go.mod', {
      content: 'x',
      base_sha256: 'old',
    }).catch((e) => e as ApiError)
    expect(err).toBeInstanceOf(ApiError)
    expect(err.status).toBe(409)
    expect(err.message).toBe('文件已被改动')
    expect((err.body as { current: { sha256: string } }).current.sha256).toBe('newhash')
  })

  it('400 的中文原文照旧透传', async () => {
    stubFetch(400, { error: '不允许写入 .git 目录' })
    await expect(
      writeWorkspaceFile('/w/b2-b3', '.git/config', { content: 'x', base_sha256: 'old' }),
    ).rejects.toThrow('不允许写入 .git 目录')
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/api/client.test.ts`
Expected: FAIL —— `writeWorkspaceFile is not a function`

- [ ] **Step 3: 加 TS 类型**

`web/src/api/types.ts`，替换既有 `FileResult` 一节（**保留 `FileResult`**，`fetchTaskFile` 还在用）并追加：

```ts
// FileRead 是 GET /api/workspaces/file 的响应体（proto.FileRead 的镜像）。
//
// sha256 只在**完整且是文本**时有值。空值即「这文件不可编辑」——前端拿它当
// 三态判据，不要另外按扩展名猜二进制，那必然与服务端分叉。
export interface FileRead {
  content: string
  size: number
  truncated?: boolean
  binary?: boolean
  sha256?: string
}

// FileWriteReq 是 PUT /api/workspaces/file 的请求体。
export interface FileWriteReq {
  content: string
  base_sha256: string
}

// FileWriteResp 是写入成功的响应；sha256 直接当下一次写入的 base_sha256。
export interface FileWriteResp {
  sha256: string
  size: number
}

// FileConflictResp 是 409 的响应体，current 是磁盘现状。
export interface FileConflictResp {
  error: string
  current: FileRead
}
```

- [ ] **Step 4: `ApiError` 带上 body**

```ts
// ApiError 携带 HTTP 状态码、agentd 返回的 error 字段，以及**完整响应体**。
//
// 为什么要留 body：409 的响应体除了 error 还带着 current（磁盘现状），冲突界面
// 的两个出口都要用它——「放弃我的改动」要它的正文，「用我的内容覆盖」要它的
// 哈希当新基线。只留一个 message 就得为了拿现状再发一次请求，而两次请求之间
// 又是一个新窗口。
//
// 参数：
//   - status: HTTP 状态码；0 表示请求根本没到 agentd（网络/反代层失败）
//   - message: 人类可读的原因
//   - body: 已解析的响应体；解析不出时为 undefined
export class ApiError extends Error {
  readonly status: number
  readonly body: unknown

  constructor(status: number, message: string, body?: unknown) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.body = body
  }
}
```

`bodyOrError` 改成同时返回原文与解析后的 body：

```ts
// bodyOrError 从非 2xx 响应里提取 agentd 的 {"error": "…"} 原文与完整响应体；
// 读不到时回退到「状态码 + 状态文本」兜底文案。
async function bodyOrError(resp: Response): Promise<{ detail: string; body: unknown }> {
  try {
    const body = (await resp.json()) as { error?: string }
    return { detail: body.error ?? '', body }
  } catch {
    // 响应体不是 JSON 时用兜底文案，body 留 undefined
    return { detail: '', body: undefined }
  }
}
```

`parseResponse` 相应改：

```ts
  if (!resp.ok) {
    const { detail, body } = await bodyOrError(resp)
    throw new ApiError(resp.status, detail || `agentd 返回 ${resp.status} ${resp.statusText}`, body)
  }
```

- [ ] **Step 5: 加 `putJSON` 与两个工作树文件函数**

```ts
// putJSON 以 JSON body 发起 PUT 请求。
function putJSON<T>(path: string, body: unknown): Promise<T> {
  return request<T>(path, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
}
```

```ts
// fetchWorkspaceFile 读工作树内单个文件（GET /api/workspaces/file）。
//
// 返回的是完整 FileRead 而不是只有 content：写回需要 sha256 当基线，
// 三态展示需要 binary / truncated / size。
export function fetchWorkspaceFile(path: string, rel: string, machine?: string): Promise<FileRead> {
  return request<FileRead>(`/api/workspaces/file?${workspaceQuery(path, rel, machine)}`)
}

// writeWorkspaceFile 写回工作树内单个文件（PUT /api/workspaces/file）。
//
// 参数：
//   - req.base_sha256: **上一次读到那一版**的哈希；对不上时抛 409 的 ApiError，
//     其 body 是 FileConflictResp（带磁盘现状）
//
// 注意：成功返回的 sha256 就是下一次写入的 base_sha256，不需要再读一次
export function writeWorkspaceFile(
  path: string,
  rel: string,
  req: FileWriteReq,
  machine?: string,
): Promise<FileWriteResp> {
  return putJSON<FileWriteResp>(`/api/workspaces/file?${workspaceQuery(path, rel, machine)}`, req)
}
```

`fetchTaskFile` 继续返回 `FileResult`，一个字都不改（CLI 那条线的形状没变）。

- [ ] **Step 6: 修 `FileTab.test.tsx` 的既有 mock**

`fetchWorkspaceFile` 的返回类型变了，既有测试里的 `mockResolvedValue({ content: '…' })` 会在 typecheck 时报错。给每处补上 `size` 与 `sha256`。

- [ ] **Step 7: 跑测试确认通过**

```bash
cd web && npx vitest run && npm run typecheck && npm run lint
```
Expected: 全绿

- [ ] **Step 8: 自查注释**

`FileRead.sha256` 为什么是三态判据、`ApiError.body` 为什么必须留、`writeWorkspaceFile` 的 `base_sha256` 语义——Step 3/4/5 已逐条写入，此处确认无遗漏。（前端不加日志：`web/src/` 生产代码零 `console.*`，本条义务由注释 + 测试兑现。）

- [ ] **Step 9: 提交**

```bash
git add web/src/api/ web/src/app/workbench/FileTab.test.tsx
git commit -m "feat(web): API 客户端支持工作树文件写回，ApiError 带上响应体"
```

---

## Task 6: `FileTab` 改成可编辑（三态 + 保存 + ⌘S）

**Files:**
- Rewrite: `web/src/app/workbench/FileTab.tsx`
- Test: `web/src/app/workbench/FileTab.test.tsx`

**Interfaces:**
- Consumes: Task 5 的 `fetchWorkspaceFile` / `writeWorkspaceFile` / `FileRead`
- Produces: `<FileTab base={BaseDir} rel={string} />`——本 task 内 props 不变，草稿只活在组件 state 里（活过 tab 切换是 Task 8 的事）

- [ ] **Step 1: 写失败的测试**

替换 `FileTab.test.tsx` 里的用例集（保留既有三条读取用例，补齐 mock 字段），追加：

```ts
vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return { ...actual, fetchWorkspaceFile: vi.fn(), writeWorkspaceFile: vi.fn() }
})
const { fetchWorkspaceFile, writeWorkspaceFile } = await import('../../api/client')

const TEXT = { content: 'module handoff\n', size: 15, sha256: 'basehash' }

describe('FileTab 编辑', () => {
  it('打字后出现脏标记，保存按钮从禁用变可点', async () => {
    vi.mocked(fetchWorkspaceFile).mockResolvedValue(TEXT)
    render(<FileTab base={base} rel="go.mod" />)
    const box = await screen.findByRole('textbox')
    expect(screen.getByRole('button', { name: /保存/ })).toBeDisabled()
    await userEvent.type(box, 'x')
    expect(screen.getByText('未保存')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /保存/ })).toBeEnabled()
  })

  it('保存成功后回基线：脏标记消失、按钮变灰、下一次保存用新哈希当 base', async () => {
    vi.mocked(fetchWorkspaceFile).mockResolvedValue(TEXT)
    vi.mocked(writeWorkspaceFile).mockResolvedValue({ sha256: 'newhash', size: 16 })
    render(<FileTab base={base} rel="go.mod" />)
    const box = await screen.findByRole('textbox')
    await userEvent.type(box, 'x')
    await userEvent.click(screen.getByRole('button', { name: /保存/ }))
    await waitFor(() => expect(screen.queryByText('未保存')).not.toBeInTheDocument())
    expect(writeWorkspaceFile).toHaveBeenCalledWith(
      '/w/b2-b3', 'go.mod', { content: 'module handoff\nx', base_sha256: 'basehash' }, 'devbox',
    )
    // 再改一次，base 必须换成上一次返回的新哈希，而不是原始基线
    await userEvent.type(box, 'y')
    await userEvent.click(screen.getByRole('button', { name: /保存/ }))
    await waitFor(() =>
      expect(vi.mocked(writeWorkspaceFile).mock.calls[1][2].base_sha256).toBe('newhash'),
    )
  })

  it('二进制：无编辑框、无保存按钮，说明为什么不能编辑', async () => {
    vi.mocked(fetchWorkspaceFile).mockResolvedValue({ content: '', size: 49382, binary: true })
    render(<FileTab base={base} rel="logo.png" />)
    expect(await screen.findByText(/二进制文件，不支持在线编辑/)).toBeInTheDocument()
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /保存/ })).not.toBeInTheDocument()
  })

  it('超限：显示真实大小与「仅显示开头」，只读', async () => {
    vi.mocked(fetchWorkspaceFile).mockResolvedValue({
      content: 'a'.repeat(100), size: 3_355_443, truncated: true,
    })
    render(<FileTab base={base} rel="fixtures.json" />)
    expect(await screen.findByText(/仅显示开头/)).toBeInTheDocument()
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /保存/ })).not.toBeInTheDocument()
  })

  it('⌘S 在 tab 内触发保存', async () => {
    vi.mocked(fetchWorkspaceFile).mockResolvedValue(TEXT)
    vi.mocked(writeWorkspaceFile).mockResolvedValue({ sha256: 'newhash', size: 16 })
    render(<FileTab base={base} rel="go.mod" />)
    const box = await screen.findByRole('textbox')
    await userEvent.type(box, 'x')
    fireEvent.keyDown(box, { key: 's', metaKey: true })
    await waitFor(() => expect(writeWorkspaceFile).toHaveBeenCalled())
  })

  it('⌘S 挂在 tab 容器上，容器外按不触发', async () => {
    vi.mocked(fetchWorkspaceFile).mockResolvedValue(TEXT)
    render(<FileTab base={base} rel="go.mod" />)
    await screen.findByRole('textbox')
    fireEvent.keyDown(document.body, { key: 's', metaKey: true })
    expect(writeWorkspaceFile).not.toHaveBeenCalled()
  })

  it('保存失败时原文透传，草稿不丢', async () => {
    vi.mocked(fetchWorkspaceFile).mockResolvedValue(TEXT)
    vi.mocked(writeWorkspaceFile).mockRejectedValue(new ApiError(400, '不允许写入 .git 目录'))
    render(<FileTab base={base} rel="go.mod" />)
    const box = await screen.findByRole('textbox')
    await userEvent.type(box, 'x')
    await userEvent.click(screen.getByRole('button', { name: /保存/ }))
    expect(await screen.findByText('不允许写入 .git 目录')).toBeInTheDocument()
    expect(box).toHaveValue('module handoff\nx')
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/workbench/FileTab.test.tsx`
Expected: FAIL —— 找不到 `textbox`（现在渲染的是 `<pre>`）

- [ ] **Step 3: 重写 `FileTab.tsx`**

```tsx
// FileTab —— 查看并编辑基准目录下的一个文件（B81 spec §6）。
//
// 职责：
//   - 取 GET /api/workspaces/file，按 sha256 有没有值分出三态：可编辑 / 二进制 / 超限
//   - 可编辑时提供 textarea、脏标记、保存按钮与 ⌘S
//   - 写回经 PUT /api/workspaces/file，带上读到那一版的 sha256 当前置条件
//
// 边界：
//   - **不做语法高亮、不引 Monaco/CodeMirror**（spec §0）。判据是「能不能改个配置、
//     改段文案」，不是在浏览器里重建一个 IDE
//   - 不自动保存：executor 就在同一个工作树里干活，自动保存等于让人在不看屏幕的
//     时候和 agent 抢写
//   - 不监听文件变更：agentd 没有推送通道。代价是「文件被 executor 改了」只在按
//     保存时才知道（spec §1.2 明知并接受的取舍）
//   - 不新建、不删除、不改名：只编辑已存在的文件
//
// 错误处理：agentd 的中文错误原文原样透传（诚实展示纪律），不吞成「操作失败」。
import { useCallback, useEffect, useRef, useState } from 'react'
import { fetchWorkspaceFile, writeWorkspaceFile } from '../../api/client'
import type { FileRead } from '../../api/types'
import { errorMessage } from '../lib/format'
import type { BaseDir } from './useWorkbench'

export function FileTab({ base, rel }: { base: BaseDir; rel: string }) {
  const [read, setRead] = useState<FileRead | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [draft, setDraft] = useState('')
  // baseSha 是「我这份草稿是从哪一版改出来的」。保存成功后换成服务端返回的新哈希，
  // 而不是重新读一次——那会在两次请求之间再开一个窗口
  const [baseSha, setBaseSha] = useState('')
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState('')

  useEffect(() => {
    // cancelled 防止「快速连点两个文件」时先发的请求后到，把后选的内容盖掉
    let cancelled = false
    setRead(null)
    setError(null)
    setSaveError('')
    fetchWorkspaceFile(base.path, rel, base.machine || undefined)
      .then((r) => {
        if (cancelled) return
        setRead(r)
        setDraft(r.content)
        setBaseSha(r.sha256 ?? '')
      })
      .catch((err) => {
        if (!cancelled) setError(errorMessage(err))
      })
    return () => {
      cancelled = true
    }
  }, [base.path, base.machine, rel])

  // editable 的判据只有一个：sha256 有没有值。二进制与超限在服务端就不给哈希，
  // 前端不再按扩展名或大小另判一次——两边各判一次早晚会分叉
  const editable = read !== null && baseSha !== ''
  const dirty = editable && draft !== read.content

  const save = useCallback(async () => {
    if (!editable || !dirty || saving) return
    setSaving(true)
    setSaveError('')
    try {
      const res = await writeWorkspaceFile(
        base.path,
        rel,
        { content: draft, base_sha256: baseSha },
        base.machine || undefined,
      )
      // 回基线：read.content 换成刚存进去的内容，baseSha 换成新哈希。
      // 这样 dirty 立刻变 false，而下一次保存自动用新基线
      setRead((r) => (r === null ? r : { ...r, content: draft, size: res.size, sha256: res.sha256 }))
      setBaseSha(res.sha256)
    } catch (err) {
      // 保存失败**不动草稿**：用户的输入是唯一一份，界面上再没有第二处能找回来
      setSaveError(errorMessage(err))
    } finally {
      setSaving(false)
    }
  }, [base.path, base.machine, rel, draft, baseSha, editable, dirty, saving])

  return (
    <div
      className="flex h-full flex-col"
      onKeyDown={(e) => {
        // ⌘S 挂在**本 tab 的容器上走冒泡**，不挂 window、更不用 capture：
        // 分屏时另一侧可能是终端，⌘S 在终端有焦点时应该归终端。这与 B74 的
        // ⌘K 是同一个教训的另一面
        if ((e.metaKey || e.ctrlKey) && e.key === 's') {
          e.preventDefault()
          void save()
        }
      }}
    >
      <div className="flex items-center gap-2 border-b px-3 py-1.5 text-xs text-muted-foreground">
        <span className="truncate font-mono text-foreground">{rel}</span>
        <span className="ml-auto shrink-0">{headerNote(read, dirty)}</span>
        {editable && (
          <button
            type="button"
            className="shrink-0 rounded border px-2 py-0.5 disabled:opacity-50"
            disabled={!dirty || saving}
            onClick={() => void save()}
          >
            {saving ? '保存中…' : '保存'}
          </button>
        )}
      </div>
      {saveError !== '' && <p className="border-b px-3 py-1.5 text-xs text-destructive">{saveError}</p>}
      <div className="min-h-0 flex-1 overflow-auto">
        {error !== null ? (
          <p className="p-4 text-sm text-destructive">{error}</p>
        ) : read === null ? (
          <p className="p-4 text-sm text-muted-foreground">正在读取 {rel}…</p>
        ) : read.binary ? (
          <p className="p-4 text-sm text-muted-foreground">
            二进制文件，不支持在线编辑。前 8 KiB 里出现了 NUL 字节，agentd 不会把它当文本返回。
          </p>
        ) : editable ? (
          <textarea
            className="h-full w-full resize-none whitespace-pre p-4 font-mono text-xs leading-relaxed outline-none"
            spellCheck={false}
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
          />
        ) : (
          <pre className="p-4 font-mono text-xs leading-relaxed whitespace-pre-wrap">{read.content}</pre>
        )}
      </div>
    </div>
  )
}

// headerNote 给出头部右侧那句话：三态各一句，可编辑时只在脏的时候出现。
//
// 为什么把「为什么不能编辑」写在头部而不是只置灰保存按钮：一个灰按钮不解释任何
// 事情，用户会反复点它。二进制与超限是两个不同的原因，各说各的
function headerNote(read: FileRead | null, dirty: boolean): string {
  if (read === null) return ''
  if (read.binary) return '二进制文件，不支持在线编辑'
  if (read.truncated) return `文件 ${formatSize(read.size)}，仅显示开头 1 MB，不支持在线编辑`
  return dirty ? '未保存' : ''
}
```

`web/src/app/lib/format.ts` 目前只有 `shortID` / `shortCommit` / `formatRelative` / `formatFull` / `errorMessage`，没有字节格式化。在那里补一个：

```ts
// formatSize 把字节数格式化成人能读的大小。
//
// 用 1024 进制并保留一位小数：这里的读者是在判断「这文件为什么不给我编辑」，
// 3.2 MB 比 3355443 字节能直接回答那个问题
export function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  const units = ['KB', 'MB', 'GB']
  let v = bytes / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(1)} ${units[i]}`
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/workbench/FileTab.test.tsx`
Expected: PASS

- [ ] **Step 5: 加意图注释**

Step 3 已逐处写入，逐条确认到位：
- 文件头：职责 + 四条边界（不高亮 / 不自动保存 / 不监听变更 / 不新建删除），每条带理由
- `editable` 判据为什么只看 `sha256`
- ⌘S 为什么走冒泡不走 capture、不挂 window
- 保存失败为什么不动草稿
- 保存成功为什么用返回的新哈希而不是重新读一次
- `headerNote` 为什么要写明原因而不只是置灰按钮

- [ ] **Step 6: 提交**

```bash
cd web && npx vitest run && npm run typecheck && npm run lint && npm run build
cd .. && git add web/src/app/workbench/FileTab.tsx web/src/app/workbench/FileTab.test.tsx web/src/app/lib/format.ts
git commit -m "feat(web): 文件 tab 从只读改成可编辑，三态 + 保存 + ⌘S"
```

---

## Task 7: 409 冲突条与两个出口

**Files:**
- Modify: `web/src/app/workbench/FileTab.tsx`
- Test: `web/src/app/workbench/FileTab.test.tsx`

**Interfaces:**
- Consumes: Task 5 的 `ApiError.body`（409 时是 `FileConflictResp`）、Task 6 的 `FileTab` 内部结构
- Produces: `FileTab` 内的 `conflict` 状态与冲突条 UI；**不改对外 props**

- [ ] **Step 1: 写失败的测试**

```ts
const CONFLICT = new ApiError(409, '文件已被改动', {
  error: '文件已被改动',
  current: { content: 'executor 改过的内容\n', size: 20, sha256: 'diskhash' },
})

describe('FileTab 冲突', () => {
  it('409 亮冲突条，两个出口都在', async () => {
    vi.mocked(fetchWorkspaceFile).mockResolvedValue(TEXT)
    vi.mocked(writeWorkspaceFile).mockRejectedValue(CONFLICT)
    render(<FileTab base={base} rel="go.mod" />)
    const box = await screen.findByRole('textbox')
    await userEvent.type(box, 'x')
    await userEvent.click(screen.getByRole('button', { name: /^保存$/ }))
    expect(await screen.findByText(/文件已在磁盘上变了/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /放弃我的改动/ })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /用我的内容覆盖/ })).toBeInTheDocument()
  })

  it('放弃我的改动：草稿换成磁盘版本，基线换成磁盘哈希，脏标记清掉', async () => {
    vi.mocked(fetchWorkspaceFile).mockResolvedValue(TEXT)
    vi.mocked(writeWorkspaceFile).mockRejectedValue(CONFLICT)
    render(<FileTab base={base} rel="go.mod" />)
    const box = await screen.findByRole('textbox')
    await userEvent.type(box, 'x')
    await userEvent.click(screen.getByRole('button', { name: /^保存$/ }))
    await userEvent.click(await screen.findByRole('button', { name: /放弃我的改动/ }))
    expect(box).toHaveValue('executor 改过的内容\n')
    expect(screen.queryByText('未保存')).not.toBeInTheDocument()
    expect(screen.queryByText(/文件已在磁盘上变了/)).not.toBeInTheDocument()
  })

  it('用我的内容覆盖：二次确认后拿 current.sha256 当新 base 重发', async () => {
    vi.mocked(fetchWorkspaceFile).mockResolvedValue(TEXT)
    vi.mocked(writeWorkspaceFile)
      .mockRejectedValueOnce(CONFLICT)
      .mockResolvedValueOnce({ sha256: 'afterforce', size: 17 })
    render(<FileTab base={base} rel="go.mod" />)
    const box = await screen.findByRole('textbox')
    await userEvent.type(box, 'x')
    await userEvent.click(screen.getByRole('button', { name: /^保存$/ }))
    await userEvent.click(await screen.findByRole('button', { name: /用我的内容覆盖/ }))
    // 二次确认：覆盖是不可逆的，而我们没有 watcher，用户在按保存之前从没被警告过
    await userEvent.click(await screen.findByRole('button', { name: /确认覆盖/ }))
    await waitFor(() =>
      expect(vi.mocked(writeWorkspaceFile).mock.calls[1][2]).toEqual({
        content: 'module handoff\nx',
        base_sha256: 'diskhash', // 不是空串、不是原始 basehash
      }),
    )
  })

  it('覆盖时磁盘又变了：第二次照样 409，冲突条重新出现', async () => {
    vi.mocked(fetchWorkspaceFile).mockResolvedValue(TEXT)
    vi.mocked(writeWorkspaceFile).mockRejectedValue(CONFLICT)
    render(<FileTab base={base} rel="go.mod" />)
    const box = await screen.findByRole('textbox')
    await userEvent.type(box, 'x')
    await userEvent.click(screen.getByRole('button', { name: /^保存$/ }))
    await userEvent.click(await screen.findByRole('button', { name: /用我的内容覆盖/ }))
    await userEvent.click(await screen.findByRole('button', { name: /确认覆盖/ }))
    expect(await screen.findByText(/文件已在磁盘上变了/)).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/workbench/FileTab.test.tsx -t 冲突`
Expected: FAIL —— 找不到「文件已在磁盘上变了」

- [ ] **Step 3: 实现冲突态**

在 `FileTab.tsx` 里加：

```tsx
// ConflictState 是一次未解决的写入冲突。current 是服务端在 409 里附带的磁盘现状，
// 两个出口都要用它：「放弃」要它的正文，「覆盖」要它的哈希当新基线
type ConflictState = { current: FileRead; confirming: boolean }
```

`save` 的 catch 分支改成先认冲突：

```tsx
    } catch (err) {
      const cur = conflictCurrent(err)
      if (cur !== null) {
        setConflict({ current: cur, confirming: false })
        setSaveError('')
        return
      }
      setSaveError(errorMessage(err))
    }
```

```tsx
// conflictCurrent 从一个错误里认出 409 冲突并取出磁盘现状；不是冲突时返回 null。
//
// 防御性地校验形状而不是直接断言：旧版 agentd 可能只回 {error}，那时应当退回
// 普通错误展示，而不是让界面因为读不到 current 崩掉
function conflictCurrent(err: unknown): FileRead | null {
  if (!(err instanceof ApiError) || err.status !== 409) return null
  const body = err.body as { current?: FileRead } | undefined
  const cur = body?.current
  if (cur === undefined || typeof cur.content !== 'string') return null
  return cur
}
```

冲突条渲染在头部之下、正文之上：

```tsx
      {conflict !== null && (
        <div className="border-b bg-muted px-3 py-2 text-xs">
          <p className="text-foreground">文件已在磁盘上变了（很可能是 executor 改的）。</p>
          {conflict.confirming ? (
            <div className="mt-1.5 flex items-center gap-2">
              <span className="text-destructive">
                覆盖会丢掉磁盘上那一版的改动，不可撤销。
              </span>
              <button type="button" className="rounded border px-2 py-0.5"
                onClick={() => void overwrite()}>确认覆盖</button>
              <button type="button" className="rounded border px-2 py-0.5"
                onClick={() => setConflict({ ...conflict, confirming: false })}>取消</button>
            </div>
          ) : (
            <div className="mt-1.5 flex items-center gap-2">
              <button type="button" className="rounded border px-2 py-0.5"
                onClick={() => discard()}>放弃我的改动，载入磁盘版本</button>
              <button type="button" className="rounded border px-2 py-0.5"
                onClick={() => setConflict({ ...conflict, confirming: true })}>用我的内容覆盖</button>
            </div>
          )}
        </div>
      )}
```

两个出口：

```tsx
  // discard 把草稿整体换成磁盘现状，基线跟着换——等价于「重新打开这个文件」
  const discard = () => {
    if (conflict === null) return
    setRead(conflict.current)
    setDraft(conflict.current.content)
    setBaseSha(conflict.current.sha256 ?? '')
    setConflict(null)
  }

  // overwrite 拿 current.sha256 当**新的** base_sha256 重发一次。
  //
  // 为什么不是「跳过校验」：覆盖的语义是「我看过磁盘上那一版了，接受它当新基线」。
  // 若这中间磁盘又变了，第二次照样 409——这正确，而一个 force 标志会把它变成静默覆盖。
  //
  // 为什么要二次确认：orca 敢在「警告过了」之后直接放行手动保存，因为它有 watcher,
  // 横幅在你按保存之前就出现了。我们没有 watcher，冲突只在保存那一刻才暴露，
  // 用户在此之前从没被警告过，所以覆盖必须自带确认
  const overwrite = async () => {
    if (conflict === null) return
    const next = conflict.current.sha256 ?? ''
    setConflict(null)
    setBaseSha(next)
    await saveWith(next)
  }
```

把 `save` 拆成 `saveWith(sha: string)`，`save()` = `saveWith(baseSha)`——否则 `overwrite` 里刚 `setBaseSha` 的新值在同一轮里读不到（React state 更新是异步的，这是最容易在这里踩的一个坑）。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/workbench/FileTab.test.tsx`
Expected: PASS

- [ ] **Step 5: 加意图注释**

Step 3 已写入的四处逐条确认：`ConflictState.current` 为什么两个出口都要、`conflictCurrent` 为什么要校验形状、`overwrite` 为什么不是 force 标志、为什么需要二次确认（和 orca 的差别）。补一条 `saveWith` 的：**为什么要显式传哈希而不是读 state**。

- [ ] **Step 6: 提交**

```bash
cd web && npx vitest run && npm run typecheck && npm run lint
cd .. && git add web/src/app/workbench/FileTab.tsx web/src/app/workbench/FileTab.test.tsx
git commit -m "feat(web): 409 冲突条与两个出口（放弃 / 覆盖）"
```

---

## Task 8: 草稿活过 tab 切换

`WorkbenchPage.tsx:141` 只渲染 `activeTab`——**切到别的 tab 会把 `FileTab` 卸载掉**。草稿只要活在组件 state 里，「点一下隔壁终端再切回来，改的字全没了」。

**Files:**
- Modify: `web/src/app/workbench/tabs.ts:23-31`（`TabContent` 的 `file` 一支）
- Modify: `web/src/app/workbench/FileTab.tsx`（收 `draft` / `baseSha` 初值，卸载时回写）
- Modify: `web/src/app/shell/Shell.tsx:207-208`（`renderContent` 的 `file` 分支）
- Test: `web/src/app/workbench/tabs.test.ts`、`FileTab.test.tsx`

**Interfaces:**
- Consumes: 既有 `setTabContent` / `wb.setContent`（终端 tab 回写 `sessionId` 走的就是这条路）
- Produces:
  - `TabContent` 的 `file` 一支变成 `{ kind: 'file'; rel: string; draft?: string; baseSha?: string }`
  - `FileTab` 新增两个可选 prop：`initial?: { draft: string; baseSha: string }`、`onDraftChange: (d: { draft: string; baseSha: string } | null) => void`
  - `dedupKey` 对 `file` **仍只取 `rel`**（草稿不参与去重）

- [ ] **Step 1: 写失败的测试**

`tabs.test.ts` 追加：

```ts
it('file tab 的去重键只看 rel，草稿不参与——同一个文件不该因为改了字就开出第二个 tab', () => {
  expect(dedupKey({ kind: 'file', rel: 'a.go', draft: 'x', baseSha: 'h' }))
    .toBe(dedupKey({ kind: 'file', rel: 'a.go' }))
})
```

`FileTab.test.tsx` 追加：

```ts
it('卸载时把草稿回写出去，不是每敲一个字都回写', async () => {
  vi.mocked(fetchWorkspaceFile).mockResolvedValue(TEXT)
  const onDraftChange = vi.fn()
  const { unmount } = render(
    <FileTab base={base} rel="go.mod" onDraftChange={onDraftChange} />,
  )
  const box = await screen.findByRole('textbox')
  await userEvent.type(box, 'abc')
  // 打字期间零回写：每次回写都会把整棵 WorkbenchPage 重渲染一遍
  expect(onDraftChange).not.toHaveBeenCalled()
  unmount()
  expect(onDraftChange).toHaveBeenCalledWith({
    draft: 'module handoff\nabc',
    baseSha: 'basehash',
  })
})

it('干净时卸载回写 null，不留一份和磁盘一样的假草稿', async () => {
  vi.mocked(fetchWorkspaceFile).mockResolvedValue(TEXT)
  const onDraftChange = vi.fn()
  const { unmount } = render(<FileTab base={base} rel="go.mod" onDraftChange={onDraftChange} />)
  await screen.findByRole('textbox')
  unmount()
  expect(onDraftChange).toHaveBeenCalledWith(null)
})

it('带 initial 挂载时直接用草稿，不等网络', async () => {
  vi.mocked(fetchWorkspaceFile).mockReturnValue(new Promise(() => {}))
  render(
    <FileTab base={base} rel="go.mod"
      initial={{ draft: '切走之前改的内容', baseSha: 'basehash' }}
      onDraftChange={vi.fn()} />,
  )
  expect(await screen.findByRole('textbox')).toHaveValue('切走之前改的内容')
  expect(screen.getByText('未保存')).toBeInTheDocument()
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/workbench/`
Expected: FAIL —— `onDraftChange` 不是已知 prop（typecheck 报错）

- [ ] **Step 3: 扩 `TabContent`**

`tabs.ts`：

```ts
  // file 的 draft / baseSha 是**草稿寄存**，不是文件内容本身。
  //
  // 为什么必须放在这里：WorkbenchPage 只渲染 activeTab，切到别的 tab 会把 FileTab
  // 整个卸载掉。草稿活在组件 state 里的话，「点一下隔壁终端再切回来」改的字就全没了。
  // 沿用终端 tab 回写 sessionId 的同一条路（setTabContent）。
  //
  // 两个字段一起存：只存 draft 不存 baseSha，切回来之后就不知道这份草稿是从哪一版
  // 改出来的，保存时只能瞎猜一个基线
  | { kind: 'file'; rel: string; draft?: string; baseSha?: string }
```

`dedupKey` 的 `file` 分支**不动**（本来就只取 `rel`），但在它上面补一句注释说明草稿为什么不参与。

- [ ] **Step 4: `FileTab` 收 initial、卸载时回写**

```tsx
export function FileTab({
  base,
  rel,
  initial,
  onDraftChange,
}: {
  base: BaseDir
  rel: string
  initial?: { draft: string; baseSha: string }
  onDraftChange?: (d: { draft: string; baseSha: string } | null) => void
}) {
```

初始读取那个 effect 里，命中 `initial` 时用草稿覆盖读到的正文：

```tsx
      .then((r) => {
        if (cancelled) return
        setRead(r)
        // 有草稿就用草稿，但 read.content 仍是磁盘那一版——dirty 是两者之差，
        // 这样切回来时脏标记还在，而不是把草稿误当成干净内容
        setDraft(initial?.draft ?? r.content)
        setBaseSha(r.sha256 ?? '')
      })
```

`initial` 命中且网络还没回来时也要先把 textarea 画出来（上面第三条测试）——所以 `read` 为 null 但 `initial` 存在时，用 `initial` 造一个临时的 `read`：

```tsx
  // 带草稿挂载时先用草稿把编辑框画出来，不等网络：切回一个 tab 却看到「正在读取…」
  // 再闪回自己的内容，是在告诉用户「你的改动可能没了」
  const [read, setRead] = useState<FileRead | null>(
    initial === undefined ? null : { content: initial.draft, size: 0, sha256: initial.baseSha },
  )
```

> 注意：这样 `read.content === initial.draft`，`dirty` 会短暂为 false。真正的磁盘正文在网络回来后写入 `read`，脏标记随即出现。为了让脏标记从第一帧就正确，`initial` 命中时同时用一个 `dirtyHint` state 兜住——最简做法是把 `read` 初值的 `content` 设为 `''` 而 `draft` 设为 `initial.draft`，两者不等，`dirty` 从第一帧就是 true。**采用后者**。

卸载回写：

```tsx
  // draftRef 记住最新草稿，卸载时一次性刷出去。
  //
  // 为什么不是每次 onChange 都回写：那会把整棵 WorkbenchPage 重渲染一遍。orca 正是
  // 在这儿栽过（issue #826：一次 reload dispatch 扇出成 N 次 EditorPanel 重建把渲染
  // 进程卡死，只能加 75ms 去抖）。我们不用去抖这种概率性方案——打字只动组件本地
  // state，卸载时刷一次，精确且打字期间父层零重渲染
  const draftRef = useRef<{ draft: string; baseSha: string } | null>(null)
  useEffect(() => {
    draftRef.current = dirty ? { draft, baseSha } : null
  }, [dirty, draft, baseSha])

  // 回调本身也用 ref 存住，卸载 effect 的依赖才能是空数组。
  //
  // 直接把 onDraftChange 写进依赖的话，调用方每次渲染传一个新的内联箭头函数就会
  // 触发一次「清理 + 重建」——而清理函数正是回写草稿的那一句，草稿会在用户还在
  // 打字的时候被提前刷出去。用 ref 就对调用方零要求，不必让 Shell 那边额外维护
  // 一个 useCallback（那种约束写在别处、坏在这里，最难查）
  const notifyRef = useRef(onDraftChange)
  notifyRef.current = onDraftChange
  useEffect(() => {
    return () => notifyRef.current?.(draftRef.current)
  }, [])

保存成功后 `draftRef` 会随 `dirty` 变 false 而置 null，卸载时正确回写 null。

- [ ] **Step 5: Shell 接线**

`Shell.tsx` 的 `renderContent` 里 `case 'file'`：

```tsx
                      case 'file':
                        return (
                          <FileTab
                            base={base}
                            rel={c.rel}
                            initial={
                              c.draft !== undefined && c.baseSha !== undefined
                                ? { draft: c.draft, baseSha: c.baseSha }
                                : undefined
                            }
                            // 草稿必须写回这个 tab：不写回的话切一次 tab 就把改动
                            // 丢了（WorkbenchPage 只渲染 activeTab，切走即卸载）
                            onDraftChange={(d) =>
                              wb.setContent(group, tabId, {
                                kind: 'file',
                                rel: c.rel,
                                draft: d?.draft,
                                baseSha: d?.baseSha,
                              })
                            }
                          />
                        )
```

内联箭头函数在这里是安全的——Step 4 的 `notifyRef` 已经让 `FileTab` 对回调引用的稳定性零要求。`renderContent` 的其他分支（`onSession` 那条）本来也是这么写的，保持一致。

- [ ] **Step 6: 跑测试确认通过**

```bash
cd web && npx vitest run && npm run typecheck && npm run lint
```
Expected: PASS

- [ ] **Step 7: 加意图注释**

- `TabContent.file` 的两个新字段：为什么必须放在 tab 内容里（卸载坑），为什么两个一起存
- `dedupKey`：草稿为什么不参与去重
- `draftRef` + 卸载回写：为什么不是每次 onChange 都回写（orca #826 的教训），为什么不用去抖
- `onDraftChangeRef`：为什么用 ref 存最新回调而不是让调用方保证稳定引用
- `initial` 命中时 `read` 初值为什么 content 设空串（让脏标记从第一帧就正确）

- [ ] **Step 8: 提交**

```bash
git add web/src/app/workbench/tabs.ts web/src/app/workbench/tabs.test.ts web/src/app/workbench/FileTab.tsx web/src/app/workbench/FileTab.test.tsx web/src/app/shell/Shell.tsx
git commit -m "feat(web): 文件草稿活过 tab 切换，卸载时一次性回写"
```

---

## Task 9: 关闭脏 tab 时拦一道

**Files:**
- Modify: `web/src/app/shell/Shell.tsx:86-119`（`beforeCloseTab` 与确认弹层）
- Test: `web/src/app/shell/Shell.test.tsx`（若不存在则在 `WorkbenchPage.test.tsx` 里补一条 `onBeforeClose` 的行为测试）

**Interfaces:**
- Consumes: `WorkbenchPage` 已有的 `onBeforeClose(c, group, tabId): boolean`（PTY 那条线加的）、Task 8 的 `TabContent.file.draft`、既有 `ConfirmDialog`
- Produces: `beforeCloseTab` 多一条 `file` 分支

- [ ] **Step 1: 写失败的测试**

```ts
it('关一个有草稿的文件 tab 会先弹确认，不直接关掉', async () => {
  // …渲染 Shell（或直接测 beforeCloseTab 这个纯函数式分支），
  // tab 内容为 { kind: 'file', rel: 'go.mod', draft: '改过的', baseSha: 'h' }，
  // 点 tab 上的 ×，断言：
  //   出现「关闭未保存的文件」确认弹层
  //   api.close 未被调用
})

it('干净的文件 tab 直接关，不打扰', async () => {
  // …tab 内容为 { kind: 'file', rel: 'go.mod' }（无 draft），
  // 点 ×，断言 api.close 被调用一次，无弹层
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/shell/`
Expected: FAIL —— 脏 tab 被直接关掉了

- [ ] **Step 3: 实现**

`Shell.tsx`：

```tsx
  const [closingDirtyFile, setClosingDirtyFile] = useState<{ group: number; tabId: string; rel: string } | null>(null)
```

```tsx
  const beforeCloseTab = (c: TabContent, group: number, tabId: string): boolean => {
    // 有草稿的文件 tab：关掉就是把用户唯一一份未保存的输入丢掉，且没有回收站。
    // 与终端那条分支同一个理由——不可逆操作先问一句
    if (c.kind === 'file' && c.draft !== undefined) {
      setClosingDirtyFile({ group, tabId, rel: c.rel })
      return false
    }
    if (c.kind !== 'terminal' || !c.sessionId) return true
    // …终端那段保持不变…
  }
```

```tsx
      <ConfirmDialog
        open={closingDirtyFile !== null}
        title="关闭未保存的文件"
        description={
          `${closingDirtyFile?.rel ?? ''} 还有未保存的改动，关掉就没了。\n` +
          '只是想看别的东西的话直接切到别的 tab——草稿会留着。'
        }
        confirmLabel="不保存，关闭"
        destructive
        onConfirm={() => {
          if (closingDirtyFile) wb.close(closingDirtyFile.group, closingDirtyFile.tabId)
          setClosingDirtyFile(null)
        }}
        onCancel={() => setClosingDirtyFile(null)}
      />
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npx vitest run && npm run typecheck && npm run lint`
Expected: PASS

- [ ] **Step 5: 加意图注释**

`beforeCloseTab` 的 file 分支：为什么脏才拦（干净时拦是纯打扰）、为什么文案要点明「切 tab 不丢」（Task 8 刚做的事，用户不知道）。

- [ ] **Step 6: 提交**

```bash
git add web/src/app/shell/Shell.tsx web/src/app/shell/Shell.test.tsx
git commit -m "feat(web): 关闭有未保存草稿的文件 tab 时先确认"
```

---

## Task 10: localStorage 草稿层与过期草稿

活过刷新与误关。在浏览器里编辑，最常见的丢失是刷新和误关，不是换机器。

**Files:**
- Create: `web/src/app/workbench/fileDraft.ts`
- Create: `web/src/app/workbench/fileDraft.test.ts`
- Modify: `web/src/app/workbench/FileTab.tsx`
- Test: `web/src/app/workbench/FileTab.test.tsx`

**Interfaces:**
- Consumes: Task 6/7/8 的 `FileTab` 内部结构
- Produces（`fileDraft.ts`）：
  - `draftKey(machine: string, workspacePath: string, rel: string): string`
  - `loadDraft(key: string): { draft: string; baseSha: string; savedAt: number } | null`
  - `saveDraft(key: string, draft: string, baseSha: string): void`（含 LRU 淘汰与静默降级）
  - `clearDraft(key: string): void`

- [ ] **Step 1: 写失败的测试**

`fileDraft.test.ts`：

```ts
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { clearDraft, draftKey, loadDraft, saveDraft } from './fileDraft'

beforeEach(() => localStorage.clear())

describe('fileDraft', () => {
  it('键包含机器、工作树路径与相对路径——三者任一不同就是另一份草稿', () => {
    expect(draftKey('devbox', '/w/b2-b3', 'go.mod'))
      .not.toBe(draftKey('local', '/w/b2-b3', 'go.mod'))
    expect(draftKey('devbox', '/w/b2-b3', 'go.mod'))
      .not.toBe(draftKey('devbox', '/w/other', 'go.mod'))
  })

  it('存了能取回来，clear 之后取不到', () => {
    const k = draftKey('devbox', '/w/b2-b3', 'go.mod')
    saveDraft(k, '改过的内容', 'basehash')
    expect(loadDraft(k)).toMatchObject({ draft: '改过的内容', baseSha: 'basehash' })
    clearDraft(k)
    expect(loadDraft(k)).toBeNull()
  })

  it('配额满时静默降级，不抛错——这时用户正在打字，一个存储配额的报错帮不上任何忙', () => {
    const k = draftKey('devbox', '/w/b2-b3', 'go.mod')
    const spy = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new DOMException('QuotaExceededError')
    })
    expect(() => saveDraft(k, 'x', 'h')).not.toThrow()
    spy.mockRestore()
  })

  it('配额满时按 savedAt 淘汰最旧的草稿再重试', () => {
    // …先塞三份草稿（savedAt 递增），mock setItem 头一次抛配额错、之后成功，
    // 断言：最旧那份的键被 removeItem，且新草稿最终写进去了
  })

  it('坏数据当作没有草稿，不让界面崩', () => {
    const k = draftKey('devbox', '/w/b2-b3', 'go.mod')
    localStorage.setItem(k, '{不是合法 JSON')
    expect(loadDraft(k)).toBeNull()
  })
})
```

`FileTab.test.tsx` 追加：

```ts
it('草稿写 localStorage 去抖 500ms，不是每按一次键写一次', async () => {
  vi.useFakeTimers()
  // …打三个字，断言此刻 localStorage 里还没有；推进 500ms 后才有
  vi.useRealTimers()
})

it('刷新后（重新挂载且无 initial）从 localStorage 恢复草稿', async () => {
  // …先 saveDraft 一份 baseSha 与服务端一致的草稿，
  // 渲染 FileTab，断言 textarea 是草稿内容且脏标记在
})

it('过期草稿（baseSha 与磁盘对不上）直接亮冲突条，走同一套 UI', async () => {
  vi.mocked(fetchWorkspaceFile).mockResolvedValue({ ...TEXT, sha256: 'diskchanged' })
  // …localStorage 里的草稿 baseSha 是 'basehash'
  render(<FileTab base={base} rel="go.mod" />)
  expect(await screen.findByText(/本地草稿基于的版本已经变了/)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /放弃我的改动/ })).toBeInTheDocument()
})

it('保存成功后删掉 localStorage 里的草稿', async () => {
  // …保存成功，断言 loadDraft 返回 null
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/workbench/fileDraft.test.ts`
Expected: FAIL —— 模块不存在

- [ ] **Step 3: 写 `fileDraft.ts`**

```ts
// fileDraft —— 文件编辑草稿的 localStorage 层（B81 spec §7.2）。
//
// 职责：
//   - 按「机器 + 工作树路径 + 相对路径」给每份草稿一个稳定的键
//   - 存/取/删草稿，配额满时按最近使用淘汰
//
// 边界：
//   - **不碰工作树**。草稿绝不能落在工作树里——那是 executor 正在干活的 git 仓库，
//     草稿文件会进 git status、会被 agent 顺手 commit、会污染 handoff diff、
//     会让下一次 dispatch 的「工作区必须干净」检查直接拒发
//   - 不做跨机接管（换了机器连浏览器 tab 都没了）。服务端草稿是 B82
//   - 不判断草稿新旧对错：baseSha 的比对归 FileTab
//   - 写不进去就静默放弃，绝不抛错——调用它的时候用户正在打字
const PREFIX = 'handoff:draft:'

// StoredDraft 是落在 localStorage 里的一条草稿。
//
// savedAt 存在的唯一理由是配额满时的淘汰排序，不用于展示——「你的草稿存于 3 分钟前」
// 对用户没有任何可行动的价值
interface StoredDraft {
  draft: string
  baseSha: string
  savedAt: number
}

// draftKey 组出一份草稿的键。
//
// 三段都必须进键：同一个 rel 在两台机器上、在同一台机器的两个工作树里，
// 是三份互不相干的文件。少一段就会串味
export function draftKey(machine: string, workspacePath: string, rel: string): string {
  return `${PREFIX}${machine}:${workspacePath}:${rel}`
}

// loadDraft 取回一份草稿；不存在或数据损坏时返回 null。
//
// 损坏当作没有：一份存坏的草稿救不回来，而让 JSON.parse 抛到渲染层会把整个
// 文件 tab 弄白屏——那比丢一份草稿糟得多
export function loadDraft(key: string): StoredDraft | null {
  try {
    const raw = localStorage.getItem(key)
    if (raw === null) return null
    const v = JSON.parse(raw) as StoredDraft
    if (typeof v.draft !== 'string' || typeof v.baseSha !== 'string') return null
    return v
  } catch {
    return null
  }
}

// saveDraft 写入一份草稿，配额满时按 savedAt 淘汰最旧的再重试一次。
//
// **永不抛错**：调用它的时候用户正在打字，一个存储配额的报错帮不上任何忙，
// 而草稿在内存里（TabContent）还有一份，退回去就是了
export function saveDraft(key: string, draft: string, baseSha: string): void {
  const payload = JSON.stringify({ draft, baseSha, savedAt: Date.now() } satisfies StoredDraft)
  try {
    localStorage.setItem(key, payload)
    return
  } catch {
    // 落到淘汰逻辑
  }
  if (!evictOldest()) return
  try {
    localStorage.setItem(key, payload)
  } catch {
    // 淘汰一份还是不够就放弃：内存草稿仍在，静默降级
  }
}

// clearDraft 删掉一份草稿（保存成功 / 放弃草稿时调用）。
export function clearDraft(key: string): void {
  try {
    localStorage.removeItem(key)
  } catch {
    // 删不掉也没有可行动的补救
  }
}

// evictOldest 淘汰 savedAt 最旧的一份草稿；没有可淘汰的返回 false。
//
// 只按草稿自己的键前缀扫，绝不动别人的 localStorage 条目
function evictOldest(): boolean {
  let oldestKey: string | null = null
  let oldestAt = Infinity
  for (let i = 0; i < localStorage.length; i++) {
    const k = localStorage.key(i)
    if (k === null || !k.startsWith(PREFIX)) continue
    const v = loadDraft(k)
    const at = v?.savedAt ?? 0 // 坏数据优先淘汰
    if (at < oldestAt) {
      oldestAt = at
      oldestKey = k
    }
  }
  if (oldestKey === null) return false
  clearDraft(oldestKey)
  return true
}
```

- [ ] **Step 4: `FileTab` 接上**

三处接线：

1. **去抖写入**（500 ms）：

```tsx
  // localStorage 这一层**不要求精确**：刷新丢掉最后半秒的输入可以接受，而每次
  // 按键写一次 localStorage 会掉帧。这与内存层（卸载时精确刷一次）是两种不同的
  // 要求，所以用两种不同的做法，不是一处该统一而没统一
  useEffect(() => {
    if (!dirty) return
    const key = draftKey(base.machine || 'local', base.path, rel)
    const t = setTimeout(() => saveDraft(key, draft, baseSha), 500)
    return () => clearTimeout(t)
  }, [dirty, draft, baseSha, base.machine, base.path, rel])
```

2. **挂载时恢复**：读取 effect 里，`initial` 为空时回退到 `loadDraft`。

3. **过期草稿走冲突条**：

```tsx
        // 草稿连 baseSha 一起存，就是为了这一刻：拿它和磁盘现在的 sha256 一比，
        // 不等说明磁盘在你离开期间变了。走**同一条冲突条、同两个出口**，
        // 不发明第二套逻辑——用户面对的是同一个问题
        if (stored !== null && r.sha256 !== undefined && stored.baseSha !== r.sha256) {
          setConflict({ current: r, confirming: false, reason: 'stale-draft' })
        }
```

冲突条文案按 `reason` 分两句：`'save'` → 「文件已在磁盘上变了（很可能是 executor 改的）。」；`'stale-draft'` → 「本地草稿基于的版本已经变了。」两个按钮完全一样。

4. **保存成功 / 放弃草稿时 `clearDraft`**。

- [ ] **Step 5: 跑测试确认通过**

```bash
cd web && npx vitest run && npm run typecheck && npm run lint && npm run build
```
Expected: 全绿

- [ ] **Step 6: 加意图注释**

- `fileDraft.ts` 文件头四条边界（Step 3 已写）——尤其「草稿绝不能落在工作树里」那条的四个后果
- `saveDraft` 为什么永不抛错
- `loadDraft` 为什么坏数据当作没有
- `evictOldest` 为什么只扫自己的前缀
- `FileTab` 里去抖那段：为什么这一层用去抖而内存层用卸载刷新（两种要求，不是该统一没统一）
- 过期草稿为什么复用冲突条

- [ ] **Step 7: 提交**

```bash
git add web/src/app/workbench/fileDraft.ts web/src/app/workbench/fileDraft.test.ts web/src/app/workbench/FileTab.tsx web/src/app/workbench/FileTab.test.tsx
git commit -m "feat(web): 草稿落 localStorage，过期草稿走同一条冲突条"
```

---

## Task 11: 端到端验收与回归

**Files:** 无代码改动（发现问题回到对应 task 修）

- [ ] **Step 1: 全量回归**

```bash
go build ./... && go test ./... && go vet ./... && gofmt -l .
```

```bash
cd web && npx vitest run && npm run typecheck && npm run lint && npm run build
```
Expected: 全绿，`gofmt -l .` 无输出

- [ ] **Step 2: CLI 契约回归（spec §12 第 9 条）**

在改动前的 commit 上对一个 2 MB 文件跑一次 `handoff fetch`，存下输出；改动后再跑一次，`diff` 必须为空。

```bash
git stash list  # 确认工作区状态
handoff fetch <task> <2MB 文件路径> > /tmp/after.txt
diff /tmp/before.txt /tmp/after.txt && echo "CLI 契约一致"
```
Expected: 逐字节一致（含结尾那行「===== 内容已截断 =====」）

- [ ] **Step 3: 手工走一遍 spec §12 的十条验收**

逐条勾（1–8 在浏览器里走，9 是 Step 2，10 是 Step 1）：

1. 打开工作树里一个文本文件，改、存，刷新页面后文件内容确是新的
2. 保存后按钮变灰、脏标记消失，再次保存不报冲突（基线已换）
3. 保存前用 `handoff run <task> 'echo x >> <文件>'` 制造外部改动，再保存 → 冲突条出现，两个出口都按预期工作
4. 二进制文件（PNG）打开显示「不支持在线编辑」，无保存按钮，**正文不是乱码**
5. 超过 1 MB 的文件打开显示真实大小 + 「仅显示开头 1 MB」，无保存按钮
6. `.git` 下的文件在树里仍能看到、能点开只读，保存被拒且理由说得清
7. 改了字之后切到隔壁终端 tab 再切回来，改动还在
8. 改了字之后刷新页面，改动还在；若这期间文件被改过，回来时直接是冲突条

- [ ] **Step 4: 对照原型**

跑 `prototypes/desktop-console/`（`npm run dev`），把真实界面与原型的三态、保存按钮位置、冲突条两个出口逐一对照。**形态不一致时以原型为准**——它是 brainstorm 阶段确认过的最终形态基准。差异若是原型错了，改原型并在 `prototypes/desktop-console/AGENTS.md` 记一笔。

- [ ] **Step 5: 回填 backlog**

`docs/superpowers/backlog.md` 的 B81 行：
- 状态 `🔨 doing` → `✅ done(已验)`
- `验收` 列填实际命令与结果，例如 `go test ./... (ok) + web vitest (N passed)；原型对照已确认 08-13`
- 原型有改动时把改动一并记在 `变更痕迹`

```bash
git add docs/superpowers/backlog.md
git commit -m "docs(backlog): B81 完成，回填验收证据"
```

---

## 自查（写完计划后的复盘，不是执行步骤）

**Spec 覆盖**：§1.1 白名单 400 → Task 2；§1.2/§1.3 冲突前置条件与三条借鉴 → Task 3；§2 读侧改造 → Task 1；§3 写端点 → Task 3+4；§4 拒绝面五条 → Task 3；§5 原子替换不 fsync → Task 3；§6 前端三态/textarea/⌘S/关闭拦截 → Task 6+9；§7 草稿双层 → Task 8+10；§8 冲突两个出口 → Task 7；§9 错误呈现 → Task 4；§10 日志与注释 → 每个 task 的专门 step；§11 测试 → 各 task 的 Step 1；§12 验收 → Task 11。§13 三条未决原样留在 spec 里，本计划不动。

**已知的两处与 spec 的偏离**（都在正文里就地标注了理由）：
1. `Binary` 时正文置空从 `ReadFile` 移到 workspace 端点（Task 1 Step 6）——否则 `handoff fetch` 对二进制文件的输出会静默变样。
2. spec §7.2 说草稿回写「用一个 ref 记住最新值，卸载时刷上去」，实现里还需要**第二个 ref 存住回调本身**（Task 8 Step 4 的 `notifyRef`），否则调用方每次渲染换一个新的内联回调会让卸载 effect 反复清理重建，草稿在打字途中被提前刷出去。

**类型一致性**：`proto.FileRead` ↔ `types.ts` 的 `FileRead` 字段名与 json tag 逐个对应（`content` / `size` / `truncated` / `binary` / `sha256`）；`FileWriteReq.base_sha256` 在 Go 与 TS 两侧都是下划线形态；`WriteFile` 返回的 `proto.FileRead` 在成功与冲突两种情况下语义不同，已在函数注释里写死。
