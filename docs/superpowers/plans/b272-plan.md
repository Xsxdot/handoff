# B272 实现计划

卡：B272。L3 轻档。契约：`docs/superpowers/specs/b272-contract.md`。

## Task 1 — dropdir.Put（缝 1）

- [x] 表驱动：新文件、同名 `-2`、`photo-2.png`→`photo-2-2.png`、`.env-2`、basename、超限无成品、刚好 32 MiB、并发不覆盖、tmp 残留不占名
- [x] 日志：开始 / 拒绝 / 完成（不含文件内容）
- [x] 文件头职责+边界；`Put` 文档注释

## Task 2 — HTTP + 专用转发（缝 2）

- [x] `POST /api/drop` 原字节；413；非法名 400
- [x] `?machine=` 原字节 roundtrip；超限不对端留截断文件
- [x] 不改 `forwardBodyLimit`
- [x] `DropPutResp` 金样本
- [x] 日志：上传开始/完成、转发开始/完成、超限 Warn

## Task 3 — TerminalTab（缝 3）

- [x] 跨机 HTML5 上传后 `term.input(shellQuote(path)+' ')`
- [x] 跨机原生 accept 空操作
- [x] 同机 HTML5 不上传
- [x] 目录拒绝；上传失败不插路径
- [x] 进行中 / 错误 `drop-notice`
- [x] `logTermDrop`

## 真机

见 breakdown 真机清单。本计划不代替 acceptance。
