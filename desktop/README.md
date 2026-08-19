# handoff-desktop 桌面薄壳

`handoff-desktop` 是 handoff 的控制台桌面壳：在已经配置过 handoff 的机器上双击打开，托盘常驻，窗口里加载 agentd 伺服的真实控制台。它是「怎么握手的窗口」，不重写任何一份业务逻辑。

## 它是什么

- Wails v3 应用（Vite + TypeScript 前端 + Go 后端），放在本 `desktop/` 目录，作为**独立的嵌套 Go module**（module path `github.com/Xsxdot/handoff/desktop`，经 `replace github.com/Xsxdot/handoff => ../` 指回主模块）。
- 启动时用主模块的 `internal/{config,client,service}`：定位 agentd（读配置、判断这台机器配没配过）、换一次性 ticket 拿控制台 URL、判断 agentd 装没装/起没起并托管拉起。
- 托盘只有两项：「打开控制台」「退出（agentd 继续运行）」。关窗口或退出薄壳**不会停 agentd**——它该由服务托管并活过薄壳，执行者不能随关窗陪葬。

## 边界

- **不放业务逻辑。** 定位、握手、生命周期、路径校验全在 `internal/shell/`，那里**不 import Wails**，因而能用普通 `go test` 覆盖；`main.go` 只做装配与错误呈现。往 `main.go` 里写逻辑之前先问：它能不能挪进 `internal/shell`。
- **不内嵌 agentd、不代理请求、不自己实现鉴权。** 全是复用主模块的逻辑。
- **Windows 不在范围内。** `service.New` 对 Windows 直接返回错误，薄壳原样呈现，不 panic、不假装成功。

## 构建

必须先构建前端产物，再跑 Go 侧：

```sh
export PATH="$(go env GOPATH)/bin:$PATH"   # wails3 装在这
cd desktop
wails3 task build                          # npm ci + vite build + go build（走 Wails 的 Taskfile）
```

产物落在 `desktop/bin/`。

**为什么不能裸调构建步骤，也不能裸 `go build ./...`：**

- 前端构建必须走 Wails 的 Taskfile——v3 的 vite 插件依赖 binding 生成器先产出 bindings，裸调 `npm run build` 必失败。
- `main.go` 用 `//go:embed all:frontend/dist` 把前端嵌进二进制。`frontend/dist` 是构建产物（已 gitignore），**新克隆下来不存在**；此时在 desktop 模块根裸跑 `go test ./...` / `go build ./...` 会因为 embed 找不到目录而报 `pattern all:frontend/dist: no matching files found`。这是预期行为：**先跑一次 `wails3 task build` 生成 dist，再跑 Go 侧命令**。`internal/shell/` 的测试（`cd desktop && go test ./internal/shell/...`）不依赖 dist，随时可跑。
- **Linux 构建必须带 `-tags gtk3`。** v3 默认后端是 gtk4 + webkitgtk-6.0（要求 GTK ≥ 4.14），而项目基线是 Ubuntu 22.04 / Debian 12 的 `webkit2gtk-4.1`，只有 `-tags gtk3` 才走它。构建标签已配置在 `build/linux/Taskfile.yml` 的 BUILD_FLAGS（dev 与 production 两条分支都带了）。

## 构建后工作区必须保持干净

本壳的承重边界是「构建不脏掉已跟踪文件」（handoff 自己的 dispatch 硬要求本地工作区干净，脏了就拒发）。已在构建流程里钉死的几处，改动时别破坏：

- `build/darwin/Assets.car`、`build/darwin/icons.icns` 是构建期从 `build/appicon.icon` 重新生成的产物，已 gitignore，不入仓库。
- 前端依赖用 `npm ci` 而不是 `npm install`（`build/Taskfile.yml` 的 `install:frontend:deps:npm`）——ci 绝不改写 `package-lock.json`。
- `desktop/go.sum` 必须保持完整（含主模块 `internal/` 依赖带进来的 modernc.org 链），否则构建时的 `go mod tidy` 会补写它、把工作区弄脏。

验收标准（改了构建配置后要重验）：

```
在干净检出上：wails3 task build，之后 git status --porcelain 必须为空。
```
