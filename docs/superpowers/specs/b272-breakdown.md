# Breakdown：B272 拖文件到远程终端（L3 轻档）

**状态：已拍板（2026-09-04）** — 轻档不扇出子卡，实现归本卡一轮。
**上游：spec 已批准，契约随实现同批冻结。**

## 待拍板清单

无。轻档不拆子卡；契约拍板已在 `b272-contract.md` §4。

## 1. 触及子系统

| id | 类型 | 本卡做什么 |
|---|---|---|
| dropdir 包（落盘规则，新） | 逻辑型 | `Put` 唯一名、32 MiB、不覆盖 |
| `d_gateway` | 边界型 | HTTP 解码、413、专用 32 MiB 转发 |
| `d_protocol` | 逻辑型 | `DropPutResp` |
| `d_web` | 边界型 | `TerminalTab` 分流 + `uploadDropFile` |
| `d_sessions` | — | **不碰**（不经 PTY 写文件） |

派卡资格：轻档不按子系统派卡。有界文件集见下。

## 2. 契约增量核对

冻结清单 1–14 均未越界。未新开接缝。边界澄清：落盘不进门面、不并入 `WriteFile` — 已写在契约 §2.1。

## 3. 行为闭环

| 触发 | 权威事实 | 消费者 | 可观察结果 | 归属 |
|---|---|---|---|---|
| 拖文件到 `machine` 非空终端 | 对端 `~/.handoff/drop/` 文件 + `DropPutResp.path` | 该终端 | 命令行插入对端绝对路径 | 本卡 |
| 同名再拖 | 新文件 `-2`，旧文件仍在 | 该终端 | 插入新路径 | 本卡 |
| 浏览器直连非 loopback | origin agentd 落盘 | 该终端 | 同上，不带 machine | 本卡 |
| 本机桌面终端拖文件 | 无上传 | 该终端 | 仍插入访达路径 | 本卡（不改） |
| >32 MiB / 目录 | 无成品文件 | 该终端 | 可见错误，不插路径 | 本卡 |

## 4. 实现范围（有界文件集，单轮）

- `internal/dropdir/*`
- `internal/agentd/drop.go`、`drop_test.go`、`server.go` 注册
- `internal/proto/projects.go`、`contract_fixture_test.go`、`web/src/api/testdata/DropPutResp.json`
- `web/src/api/{types,client,contract.test}.ts`
- `web/src/app/workbench/TerminalTab.tsx`、`.test.tsx`、`terminalDebug.ts`

## 5. 缺陷族

- **生命周期**：临时文件点开头，失败即删；崩溃残留不算占用。无，因为 Put 的 defer 删 tmp。未验证：进程被 SIGKILL 的孤儿，需真机，OOS 清理。
- **静默失败**：失败/目录/超限有 `drop-notice`。无静默成功插路径窗口，因为先等 `uploadDropFile`。
- **跨平台**：路径用 `filepath`；硬链失败回退 rename（持锁）。Windows PTY 本就降级，写入仍按 HOME。
- **假红假绿**：缝 3 断言跨机原生不 `input`；真机要求插入对端路径且无访达路径。
- **门禁**：同一 `auth`；basename 逃逸有表；不覆盖有并发测试。
- **序列化**：金样本 `DropPutResp`；体是原字节 roundtrip（`TestDropForwardDeliversBytesToRemote`）。
- **安全锁**：超限 413 + 无成品名；并发 8 路同名不覆盖。

## 真机清单（归协调者）

1. 桌面壳拖图片到登记远程机终端：对端 `ls` 有文件、checksum 一致、命令行是对端路径且无访达路径。
2. 同名再拖 → `-2`。
3. 本机终端拖文件仍是访达路径，远端 drop 不新增。
4. >32 MiB 与拖目录失败可见。
5. 浏览器直连远端 agentd 再拖一次。
