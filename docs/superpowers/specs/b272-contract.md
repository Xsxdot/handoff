# B272 契约增量：拖文件写入远端 ~/.handoff/drop/

**上游状态：已批准**（源 spec：`docs/superpowers/specs/b272.md`，头部已核实「已批准」）
**级别：L3 轻档**
**冻结状态：随本提交冻结**

## 1. 现状查证

| 现状 | 出处 | B272 |
|---|---|---|
| `forwardBodyLimit = 1<<20`，`forwardTo` 用 `io.LimitReader(r.Body, forwardBodyLimit)` **截断不拒绝** | `internal/agentd/forward.go#forwardBodyLimit`、`#forwardTo` | 本能力不用 `forwardTo`；专用转发读 `MaxBytes+1`，超限 413 |
| `WriteFile` 只改已存在文本、拒二进制、1 MiB | `internal/agentd/workspace.go#WriteFile` | 不复用 |
| 原生拖放 `term.input(shellQuote(paths))` | `web/src/app/workbench/TerminalTab.tsx`、`desktopFileDrop.ts#shellQuote` | 跨机时原生 accept 空操作 |
| 控制台会话与主令牌同一 mux | `internal/agentd/server.go#Handler`、`#auth` | 新路由走同一 `auth` |
| `os.UserHomeDir` 读 HOME | 标准库 | 落盘 HOME 由 handler 注入 `dropdir.Put` |
| 线格式金样本由 `TestContractFixtures` 生成 | `internal/proto/contract_fixture_test.go` | 新增 `DropPutResp` |

组装点：`internal/agentd/server.go#Handler` 注册 `POST /api/drop`。不新增依赖方向：控制台 → 门面 HTTP → 既有 `?machine=` 通道（本能力专用搬运函数）。落盘规则在 `internal/dropdir#Put`。

## 2. 精确签名

### 2.1 落盘（缝 1）

```go
package dropdir
const MaxBytes = 32 << 20 // 未编码字节
func Dir(home string) string
func Put(home, originalName string, r io.Reader) (absPath string, n int64, err error)
```

哨兵：`ErrTooLarge`、`ErrBadName`、`ErrNoHome`、`ErrNotFile`。

`Put`：只取 `filepath.Base`；空 / `.` / `..` → `ErrBadName`；读 `MaxBytes+1`，超过 → `ErrTooLarge` 且不留成品名；目录 `$HOME/.handoff/drop/`（`MkdirAll` 0700）；先写点开头临时文件再放到目标名；同名 `photo.png` → `photo-2.png`，已有 `photo-2.png` 再写 `photo-2.png` → `photo-2-2.png`；`.env` 整名当 stem。创建不得覆盖已有目标名。

### 2.2 HTTP（缝 2）

- **方法/路径**：`POST /api/drop`
- **查询**：`name` 必填（用户文件名，服务端再 basename）；`machine` 可选，非空则专用转发
- **请求体**：原始字节，`Content-Type: application/octet-stream`。**不是 JSON，不 base64**
- **成功 200**：

```go
type DropPutResp struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
}
```

`path` 为写成的绝对路径；`bytes` 为未编码长度。

- **400**：缺/非法 `name`、目录/非文件、机器未定义（转发）
- **413**：未编码内容 > 32 MiB（`Content-Length` 或实读）。不得截断当成功
- **401**：与其它 `/api` 相同
- **502**：转发对端不可达
- **500**：HOME 不可得、落盘失败

转发：`machine` 非空且未带 `X-Handoff-Forwarded` 时，本机读完整 ≤32 MiB 体再 POST 到目标同一路径，带转发头。其它 API 仍走 `forwardTo` 的 1 MiB。

### 2.3 控制台（缝 3）

```ts
export function uploadDropFile(name: string, data: Blob, machine?: string): Promise<DropPutResp>
```

`TerminalTab`：`base.machine` 非空 → 远程（不论 origin）；否则 origin host 非 loopback → 远程；否则同机。远程走 HTML5 `File` + `uploadDropFile`，成功 `term.input(shellQuote(path)+' ')`；原生 `registerFileDropTarget.accept` 在远程为空操作。同机桌面仍走原生路径插入。目录：可见拒绝、不上传。

金样本：`web/src/api/testdata/DropPutResp.json`。

## 3. 冻结清单（一条一断言）

1. `POST /api/drop?name=` 成功时响应含 `path` 绝对路径与 `bytes`，磁盘该路径内容与请求体逐字节相同。
2. 请求体不是 JSON；合法 ≤32 MiB 内容不得因编码膨胀被拒。
3. 内容 > 32 MiB 返回 413，目标名不存在。
4. `?machine=` 把 ≤32 MiB 原字节交到对端 `Put`，对端文件与请求体一致。
5. `?machine=` 且体 > 32 MiB 返回 413，对端不留下截断文件。
6. `forwardTo` / `forwardBodyLimit` 仍为 1 MiB，其它路由行为不变。
7. 同名不覆盖：第二次 `photo.png` 写成 `photo-2.png`，原文件仍在。
8. 已有 `photo-2.png` 再写 `photo-2.png` 得到 `photo-2-2.png`。
9. `.env` 冲突写成 `.env-2`。
10. 含路径分隔的 `name` 只取 basename，不逃出 drop 目录。
11. 失败/超限不留下非点开头的成品名。
12. 鉴权与其它 `/api` 相同（未授权 401）。
13. 跨机时原生 accept 不 `term.input` 本机路径。
14. 跨机 HTML5 成功后 `term.input` 的是响应 `path`（经 `shellQuote`）加空格。

## 4. 拍板记录

1. **体用原始字节不是 JSON+base64**。难逆转（32 MiB 上限会分裂）；后人会想改回 JSON；否掉的方案是复用 `FileWriteReq.Content string`。
2. **本能力专用转发，不抬全球 `forwardBodyLimit`**。反过来把全球改成 32 MiB 会放大所有转发 DoS 面；测试锁的是「其它路由仍 1 MiB」。
3. **落盘 HOME 不是 DataDir**。用户裁定；后人会想跟 `env/` 对齐。

命中三重闸门三条。无其它。

## 5. Ticket 0

`dropdir.Put`、`POST /api/drop`、`DropPutResp` 金样本、`uploadDropFile` 已落码。越过空壳的可观测行为由 `TestPut*`、`TestDrop*`、`TestContractFixtures` 锁住。轻档无直通竖切法定步骤。

## 6. 移交 plan

- handler 的 `homeDir` 测试钩子（避免把 `HOME` 指到 `t.TempDir` 弄脏模块缓存）
- `putMu` 进程内串行；跨进程靠 `os.Link` 不覆盖
- 硬链失败时的 rename 回退
- HTML5 与原生双通道分流写在 `TerminalTab`

## 7. 目标图

新增方向 `d_gateway →` dropdir 包级函数（落盘规则）。`d_web` 只走既有 HTTP。本轮 baseline 尚未重扫新包；contract 冻结以本文档 + 金样本为准，图节点在 recon 补视图 diff。
