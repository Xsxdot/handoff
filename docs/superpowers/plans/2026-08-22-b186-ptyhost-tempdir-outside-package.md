# B186 实现计划：ptyhost 测试的临时目录搬出包目录（且必须仍然够短）

> 2026-08-22。协调者写。卡：B186（低）。

## 事实基线（协调者在 985f37135 上查证）

`internal/ptyhost/client_test.go` 的 `shortRoot`：

```go
func shortRoot(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp(".", "pc-")
	...
}
```

`MkdirTemp(".", ...)` 把临时目录建在**包目录内**，于是全量并发跑时它会出现在
`./...` 的包枚举里，偶发撞红 `TestWindowsCrossCompiles`。

**这个写法不是随手写的，改的时候不许把它的理由丢掉**：函数名叫 `shortRoot`，
要的是**短路径**——ptyhost 会在 root 下建 unix socket，而 unix socket 路径有
~104 字节上限（macOS）。`os.MkdirTemp("", ...)` 在 macOS 上落到
`/var/folders/…/T/`，那是一条长路径，正是它当初要躲开的东西。
**只把 `"."` 换成 `""` 或 `t.TempDir()` 会把一个偶发红换成另一个偶发红。**

## 设计决定

1. **换到 `/tmp` 下的短路径**（`os.MkdirTemp("/tmp", "ph-")`），既在包目录外、
   又仍然短。Windows 上没有 unix socket 路径限制、也没有 `/tmp`，走 `t.TempDir()`。
2. **平台分支要显式**：用 `runtime.GOOS == "windows"` 判，不要靠「`/tmp` 存不存在」
   隐式判——隐式判会在 `/tmp` 被容器裁掉时静默退回长路径，红了也看不出为什么。
3. **把理由写进注释**：下一个人看到 `/tmp` 硬编码的第一反应会是「为什么不用
   t.TempDir」，注释必须当场答了它（104 字节上限 + 包目录污染 `./...` 枚举）。

## Task 1：改 shortRoot

`internal/ptyhost/client_test.go` 的 `shortRoot` 改为：

```go
// shortRoot 造一个**既短又不在包目录内**的会话根目录。
//
// 两个约束同时成立才行：
//   - 短：root 下要建 unix socket，路径有 ~104 字节上限（macOS）。t.TempDir()
//     在 macOS 上落在 /var/folders/…/T/ 那条长路径下，会把 socket 路径顶爆。
//   - 不在包目录内：曾经用 MkdirTemp(".", …) 满足「短」，代价是临时目录出现在
//     ./... 的包枚举里，全量并发跑时偶发撞红 TestWindowsCrossCompiles（B186）。
//
// 于是显式落在 /tmp 下。Windows 没有 unix socket 路径限制也没有 /tmp，用 t.TempDir()。
func shortRoot(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return t.TempDir()
	}
	root, err := os.MkdirTemp("/tmp", "ph-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}
```

需要时补 `runtime` 的 import。

## 测试映射

判据是「行为不变 + 污染消失」，两条都要真跑：

1. `go test ./internal/ptyhost/ -count=1` 全绿（行为不变）。
2. 跑测试的**同时**确认包目录没被污染：跑完执行
   `git status --porcelain internal/ptyhost/` **必须无输出**，且
   `ls internal/ptyhost/ | grep -c '^pc-'` 为 0。把两条命令的实际输出抄进 ledger。
3. `go test ./internal/ptyhost/ -run TestClient -count=3` 连跑三遍仍绿（socket 路径
   长度这类问题只在真跑时暴露，跑一遍容易蒙混）。

## 测试范围

- `go test ./internal/ptyhost/ -count=1`
- `go build ./...`、`go vet ./...`、`gofmt -l .` 无输出
- **不要跑全量 `./...`**：本卡的现象正是在全量并发下才出现，验证它需要协调者
  在本机做（且全量跑不属于任何单个 task，见三段律）。

## 不属于本次

- 不改 `TestWindowsCrossCompiles` 本身（它没错，是被污染的受害者）。
- 不改 ptyhost 生产代码。
- 不去调 socket 路径上限相关的实现。
