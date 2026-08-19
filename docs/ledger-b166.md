# B166 一期执行 ledger

## Task 0：环境自检

- `go version`：`go version go1.26.1 linux/amd64`
- `node -v || echo "NO_NODE"`：`v24.16.0`
- `npm -v || echo "NO_NPM"`：`11.13.0`
- `cd web && ls node_modules >/dev/null 2>&1 && echo "NODE_MODULES_OK" || echo "NODE_MODULES_MISSING"`：`NODE_MODULES_MISSING`
- 随后执行 `cd web && npm ci`：`added 290 packages, and audited 291 packages in 2s`；`found 0 vulnerabilities`
- `cd desktop && go test ./internal/... 2>&1 | tail -5`：

  ```text
  2026/08/19 22:26:39 INFO 已催进程管理器拉起 agentd
  2026/08/19 22:26:39 ERROR 等待被取消 want_version=v0.4.0 attempts=2 cause="context canceled"
  FAIL
  FAIL github.com/Xsxdot/handoff/desktop/internal/shell 0.070s
  ```

  本步失败，原始输出如上；先继续按计划实现，后续收尾复跑并如实记录。

## 进度

- Task 0：已完成，范围无代码提交。

## Task 1：桌面端发布物的名字与校验和

- 失败测试确认：`go test ./internal/release/ -run 'TestDesktopAssetName|TestFetchChecksumFor' -v` 原始编译错误为 `undefined: DesktopAssetName` 与 `FetchChecksumFor undefined`。
- 实现后：上述定向测试通过；`go test ./internal/release/ -v` 全部通过，`TestActivateUnwritableDir` 按既有测试在 root 下 skip。
- 双裁决第 1 轮：spec 符合，新增 `DesktopAssetName` 三平台精确映射，`FetchChecksum` 保持转发兼容，`FetchChecksumFor` 按传入资产名解析且错误/日志带资产名；代码质量通过，gofmt/vet/包测试均通过，无修复轮次。
- 收尾验证：`gofmt -l .` 无输出；`go vet ./...` 无输出；`go test ./internal/release/`：`ok github.com/Xsxdot/handoff/internal/release 0.086s`。
- 提交范围：`internal/release/` 与本 ledger，提交信息按计划为 `feat(release): 桌面端发布物的资产名与按名取校验和`。
