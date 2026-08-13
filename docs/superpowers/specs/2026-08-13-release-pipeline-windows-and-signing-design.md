# 发布路径加固：验证门、Windows 协调者分发、macOS 签名、许可证（B86）

> **定位**：把「从 push 到用户装上」这条路径补完整。四件事互相独立、共用一条流水线：
> ①发布前没有任何测试门 ②Windows 协调者拿不到二进制 ③macOS 浏览器下载会被 Gatekeeper 拦
> ④仓库已公开却没有许可证。
>
> **分支**：`handoff/release-pipeline-hardening`，基于 `main`（B84 合入之后）。
>
> **来源**：08-13 用户提出三条（Windows 资产、mac 签名、许可证）。勘察时发现第四条更靠前的
> 缺口：仓库**只有 `release.yml` 一个 workflow**，且它不跑 `go test` —— 专为发布路径写的
> `release_workflow_test.go` 与 `install_test.sh` 在真正发布时一次都不执行。
>
> **与 B84 的关系**：B84 spec §4 把「不发 Windows release 资产」列为非目标并写明「要做另立
> 一条」。本文就是那一条。B84 让纯协调者机**能用**，本文让它**装得上**。

---

## 1. 病灶

### 1.1 发布路径上没有验证门

`.github/workflows/` 下只有 `release.yml`（[release.yml](../../../.github/workflows/release.yml)）。它 checkout →
交叉编译 → `gh release create`，全程不跑 `go build ./...` 之外的任何检查。

后果是一条闭环的空转：B54.1 专门写了 `release_workflow_test.go` 来钉住「资产命名与
ldflags 注入路径是一处约定多处消费」，写了 `install_test.sh` 来钉住 install.sh 的平台
归一与校验逻辑 —— **而这两份测试在发布这条路径上从不被执行**。它们只在有人手动
`go test ./...` 时才跑。推 tag 的那一刻没有任何东西挡着。

这不是理论风险：B54.1 的验收记录里，`install.sh` 的 EXIT trap 缺陷是在真机 P2 才暴露的；
单元测试与人眼都放过了。测试的价值全部依赖「它真的会跑」。

### 1.2 Windows 协调者拿不到二进制

B84 修完之后，一台 Windows 机器可以完整走通协调者回路（dispatch / wait / diff / done
全是 HTTP + 本地 git，无平台原语）。但它**没有获得二进制的途径**：

- release 矩阵只有 darwin/linux 四项（[release.yml:23-32](../../../.github/workflows/release.yml)）
- `install.sh` 是 bash，且探到 Windows 主动 die（[install.sh:53](../../../install.sh)）
- `handoff upgrade` 会报「没有 windows/amd64 的资产」

于是唯一路径是「先装 Go 工具链再 `go build`」。对一个定位是「一行命令装上」的 CLI，
这等于 Windows 不在分发范围内。

### 1.3 macOS 浏览器下载路径被 Gatekeeper 拦

需要精确区分两条路径，否则会把成本花在没有收益的地方：

- **`curl | bash`（主路径）**：curl 不设 `com.apple.quarantine`，Gatekeeper 根本不介入。
  Go 交叉编译 darwin/arm64 产出的二进制自带 adhoc linker-signed 签名，能直接跑。
  **这条路径今天就是好的，签名对它零收益。**
- **从 Releases 页面用浏览器下 tar.gz**：浏览器打 quarantine，归档工具把它传播到解出的
  文件上，Gatekeeper 介入 —— adhoc 签名过不了，用户看到「无法打开，因为无法验证开发者」。

签名与公证买的是且仅是第二条路径。

### 1.4 公开仓库没有许可证

`Xsxdot/handoff` 是 public，已发到 v0.2.0，`licenseInfo` 为 `null`。默认即「保留所有权利」：
别人下载安装处在法律灰地带，也无法被打包进 Homebrew / winget 这类要求明确许可证的渠道。

---

## 2. 设计一：验证门（可复用 workflow）

### 2.1 新增 `ci.yml`，同时被 PR 与 release 消费

```yaml
on:
  pull_request:
  push:
    branches: [main]
  workflow_call:      # 供 release.yml 复用
```

**`workflow_call` 是关键**：release 的前置校验必须与 PR 校验是**同一份定义**。写两份必然
漂移 —— 漂移的方向还总是「release 那份更松」，因为没人在推 tag 时盯着它。

`release.yml` 增加：

```yaml
jobs:
  verify:
    uses: ./.github/workflows/ci.yml
  build-unix:
    needs: verify
  build-darwin:
    needs: verify
```

### 2.2 门禁内容

ubuntu-latest 上：

| 检查 | 命令 | 挡住什么 |
|---|---|---|
| 构建 | `go build ./...` | 编译不过 |
| 静态检查 | `go vet ./...` | vet 级缺陷 |
| 测试 | `go test ./... -count=1` | 含 `release_workflow_test.go`、`install_test.go` |
| 格式 | `gofmt -l .` 输出必须为空 | 格式漂移 |
| 平台门禁 | `GOOS=windows GOARCH=amd64 go build ./...` | 非 unix 侧编译断裂（既有约定） |
| 安装脚本 | `bash install_test.sh` | install.sh 的平台归一与校验逻辑 |

windows-latest 上：

| 检查 | 命令 | 挡住什么 |
|---|---|---|
| 安装脚本（PS 5.1） | `powershell.exe -File install_test.ps1` | install.ps1 在 **Windows 自带的** PowerShell 上的行为 |
| 安装脚本（PS 7） | `pwsh -File install_test.ps1` | 同上，在新版 PowerShell 上 |

**两个版本都要跑，不是冗余**：§3.3 把「兼容 PS 5.1」列为硬要求，而 5.1 与 7 在
`Invoke-WebRequest` 的响应对象上有实质差异。只跑 `pwsh` 等于把这条硬要求写了却不验。

`gofmt -l .` 需显式判空：它对未格式化文件只打印文件名、退出码仍是 0，直接当命令用等于
没有这道门。

### 2.3 install.ps1 为什么必须有自己的测试

`install.ps1` 会被 `irm ... | iex` 从互联网执行，与 install.sh 同级的信任地位。install.sh
有 `HANDOFF_INSTALL_LIB=1` 的「只加载函数」模式与 `install_test.sh` 的桩测试；ps1 必须对位，
否则它是整条发布路径上唯一无测试覆盖的可执行产物。

覆盖三项（不追求 install.sh 那样的完整 main 路径）：架构归一（`AMD64`/`ARM64` → `amd64`/`arm64`，
不认的架构必须报错退出）、**校验和不符必须拒装且不留残件**、安装目录解析（默认值与
`HANDOFF_INSTALL_DIR` 覆盖）。

---

## 3. 设计二：Windows 协调者分发

### 3.1 矩阵与归档格式

矩阵从 4 涨到 6：新增 `windows/amd64`、`windows/arm64`。

| 平台 | 归档 | 包内文件 |
|---|---|---|
| darwin/{arm64,amd64} | `.tar.gz` | `handoff` |
| linux/{amd64,arm64} | `.tar.gz` | `handoff` |
| windows/{amd64,arm64} | **`.zip`** | `handoff.exe` |

**Windows 用 zip 而非 tar.gz**，理由是手动下载路径：zip 在资源管理器里双击即开，tar.gz
必须敲命令行；`Expand-Archive` 存在于每一个 PowerShell，而 `tar.exe` 只有 Win10 1803+ 才有。
代价是归档格式不再唯一 —— 用 §3.2 的嗅探把这个代价关在一个函数里。

**Windows 二进制的定位写死为「只能当协调者」**，agentd 仍然跑不起来（B37 不动）。这不是
遗憾，是本次分发的全部前提。

### 3.2 自更新链路的三处改动

| 位置 | 现状 | 改法 |
|---|---|---|
| [client.go:66](../../../internal/release/client.go) `AssetName` | 硬编码 `.tar.gz` | `goos == "windows"` → `.zip`，否则 `.tar.gz` |
| [install.go:220](../../../internal/release/install.go) `extractBinary` | 只解 tar.gz，只认包内 `handoff` | 按魔数分派 gzip/zip；认 `handoff` 或 `handoff.exe` |
| [install.go:45](../../../internal/release/install.go) `TempName` | `.handoff.new-<tag>`，无后缀 | Windows 上追加 `.exe` |

**`extractBinary` 按魔数（`PK\x03\x04` / `\x1f\x8b`）分派，不按调用方传的 goos。** 传 goos 会
制造第二个真相来源：一旦它与实际字节不符，报错会指向错误的方向。字节是权威。这条选择的
额外好处是 `InstallArchive` / `Fetch` / `FetchArchive` 三个签名全部不用动，agentd 侧的跨平台
推送路径零改动。

**`TempName` 的 `.exe` 不是洁癖**：`selfCheck` 要 `exec` 这个临时文件跑 `version`，Windows 上
没有 `.exe` 后缀的文件起不来 —— 症状会是「自检失败」，而真因是文件名。判据用 `runtime.GOOS`
（安装总是发生在目标机本地）。

实现上拆成两层：导出的 `TempName(tag)` 只做 `tempName(tag, runtime.GOOS)` 的转发，逻辑落在
包内的 `tempName(tag, goos)`。**否则这条行为在非 Windows 的 CI 上永远测不到** —— 与 §3.6
的 `roleOptions` 同一个理由。

**`Activate` 不改。** 它是「先把旧的 rename 成 `.prev`，再把新的 rename 进来」；Windows 允许
rename 一个正在运行的 exe（只是不允许覆盖或删除）。这个顺序恰好就是 Windows 自更新的标准
手法。**要在 `Activate` 的注释里补上这句**，否则将来有人「优化」成先删后写，Windows 上会当场炸。

### 3.3 `install.ps1`

与 `install.sh` 逐条对位，边界完全一致：**不改 PATH、不写服务、不提权**。

```powershell
irm https://handoff.gosuper.dev/install.ps1 | iex
```

| 环节 | 实现 | 陷阱 |
|---|---|---|
| 架构探测 | `$env:PROCESSOR_ARCHITECTURE` → amd64/arm64 | 32 位与其他架构必须报错退出，不静默装 |
| 取最新 tag | 跟随 `releases/latest` 的重定向取末段 | **PS 5.1 与 PS 7 取最终 URL 的写法不同**（见下） |
| 下载 | `Invoke-WebRequest` | 需显式 `-UseBasicParsing`（PS 5.1 上不加会依赖 IE 引擎） |
| 校验 | `Get-FileHash -Algorithm SHA256` | 与 checksums.txt 比对，不符即拒装并清理 |
| 解包 | `Expand-Archive -Force` | — |
| 落点 | `$env:LOCALAPPDATA\Programs\handoff\handoff.exe` | 可用 `HANDOFF_INSTALL_DIR` 覆盖（与 sh 版同名） |
| 收尾 | 调**刚装好的那个 exe** 跑 `skill install` | 失败只警告不算安装失败（同 install.sh） |
| PATH | 不在 PATH 时打印该加什么 | 明说本脚本不会去改 |

**PowerShell 5.1 兼容是硬要求**：Windows 自带的就是 5.1，PS 7 需要用户先装。5.1 上
`Invoke-WebRequest` 的 `BaseResponse` 是 `HttpWebResponse`（用 `.ResponseUri`），7 上是
`HttpResponseMessage`（用 `.RequestMessage.RequestUri`）。只测 7 会让脚本在绝大多数
Windows 机器上第一步就挂。

落点选 `%LOCALAPPDATA%\Programs\` 而不是 `~/.local/bin`：前者是 Windows 上的既有惯例
（无需管理员、用户级），后者在 Windows 上没有任何工具认。

### 3.4 分发入口

Cloudflare Worker（[deploy/install-redirect/worker.js](../../../deploy/install-redirect/worker.js)）加一条
`/install.ps1` 路由（连同 `/install.ps1/`），302 到仓库 raw。Worker 现有的「除白名单外一律 404」
边界不变，文件头注释同步。

### 3.5 `install.sh` 的 Windows 分支改指向

从「B37 所以不支持」改成「Windows 请用 install.ps1」，并给出那行命令。
`install_test.sh:47-52` 里断言拒绝理由含 `B37` 的那条同步改成断言含 `install.ps1`
—— 断言的意图始终是「拒绝时必须给出路」，只是出路变了。

### 3.6 `handoff init` 在 Windows 上只给协调者

`cmd/init.go:209` 的角色选择在所有平台上都列「执行机 / 协调者 / 两者」。Windows 上选前两者
会一路走到 `service install` 才撞墙（[service.go:77](../../../internal/service/service.go)）。

改法：把选项裁剪抽成纯函数 `roleOptions(goos string) []promptOption`，Windows 上只返回协调者
一项，并在其上方打一行说明（agentd 的进程承载层在 Windows 未实现，B37）。`defaultRole` 在
Windows 上直接返回 `roleCoordinator`。

**抽成纯函数是为了可测**：`runtime.GOOS` 写在选择逻辑里，则这条行为在非 Windows 的 CI 上
永远测不到。

---

## 4. 设计三：macOS 签名与公证

### 4.1 job 拆分

`build` 一个 job 拆成两个：

- `build-unix`（ubuntu-latest）：linux×2 + windows×2，纯交叉编译
- `build-darwin`（macos-latest）：darwin×2，构建 → 签名 → 公证 → 打包

**`CGO_ENABLED=0` 必须显式设置。** 现有 workflow 已设，但迁到 macOS runner 后这条从「冗余」
变成「承重」：macOS 上 CGO 默认是开的，一旦开启，产物会动态链接系统库并被打上构建机的
最低系统版本约束 —— 二进制会在更老的 macOS 上拒绝启动，而这个症状要等到用户机器上才出现。

### 4.2 步骤

1. **写 App Store Connect API Key 文件**（`$RUNNER_TEMP/AuthKey.p8`，`chmod 600`）
2. **导入 `.p12` 到临时钥匙串**（照搬 super-dev 已趟平的写法：`security create-keychain` →
   `import` → `set-key-partition-list` → `list-keychains`）
3. **构建两个 arch**（arm64 原生，amd64 交叉）
4. **逐个签名**：`codesign --force --options runtime --timestamp --sign "$APPLE_SIGNING_IDENTITY"`
   —— `--options runtime`（硬化运行时）是公证的**前置条件**，不加会被拒
5. **两个 arch 打进同一个 zip 一次提交公证**：`ditto -c -k` → `xcrun notarytool submit --wait`
   —— notarytool 接受 zip 内多个 Mach-O，合并提交省一次等待（每次 1-3 分钟）
6. **验证**：`codesign --verify --strict --verbose=2` + `spctl -a -t exec -vv`，后者必须报
   `source=Notarized Developer ID`
7. **用签好的二进制打 tar.gz**

`checksums.txt` 在 release job 里对全部下载物统一计算，天然发生在签名之后 —— 现有结构不用动。
但这条约束要写进 workflow 注释：**签名会改字节，先算校验和再签就全错**。

### 4.3 secrets 缺失即硬失败

需要 6 个（复用 super-dev 同名）：`APPLE_CERTIFICATE`、`APPLE_CERTIFICATE_PASSWORD`、
`APPLE_SIGNING_IDENTITY`、`APPLE_API_ISSUER`、`APPLE_API_KEY`、`APPLE_API_KEY_CONTENT`。
（`APPLE_TEAM_ID` 用不上：用 API Key 公证时不需要它。）

任一为空即 `exit 1` 并指名缺哪个。**这与 super-dev 的策略相反** —— 它未配置时仍产出未签名包，
因为那是给 desktop 的兼容退路。handoff 不需要这条退路：打 tag 只有仓库所有者会做，
而一个静默发出去的未签名版本是会在几个月后咬人的陷阱（症状出现在用户机器上，且看不出根因）。

### 4.4 裸 CLI 无法 staple

`xcrun stapler` 只支持 `.app` / `.dmg` / `.pkg`。裸二进制的公证票据只存在 Apple 服务端，
按 cdhash 匹配，**用户联网时**校验通过。这是固有限制不是配置问题，README 的 Troubleshooting
要如实写明（离线首次运行可能被拦，处置是联网后重试或 `xattr -d com.apple.quarantine`）。

### 4.5 Windows 侧不做代码签名

Authenticode 需要另买 OV/EV 证书，且 SmartScreen 的信誉积累还需要下载量。本次记为已知限制
写进 README：Windows 首次运行可能出现「Windows 已保护你的电脑」，需点「更多信息 → 仍要运行」。

---

## 5. 设计四：许可证与元文件

### 5.1 LICENSE

Apache-2.0 全文，版权行 `Copyright 2026 Xsxdot`。

**不加 NOTICE**：Apache-2.0 并不要求它，加了反而给下游多一条必须传播的义务。
super-dev 有 NOTICE 是因为它按产品分发。

**不给 `.go` 文件加 license header**：这个仓库的文件头注释承担的是「职责 + 边界」，
是给读代码的人的第一句话。在它上面压 11 行法律样板会把真正该读的内容挤到屏幕外。
Apache-2.0 推荐但不要求逐文件标注。

**依赖侧无阻碍**（已核）：直接依赖全是 MIT/BSD/Apache；`modernc.org/sqlite` 是纯 Go 的
BSD-3，不含 CGO，不牵扯 SQLite 本体。

### 5.2 CHANGELOG.md 必须承重

Keep a Changelog 格式，回填已有四个 tag：v0.1.0 / v0.1.1 / v0.1.2（08-11）、v0.2.0（08-13）。

**并且让 release workflow 真的用它**：按 tag 抽取对应小节（从 `## [vX.Y.Z]` 到下一个 `## ` 之前）
写临时文件走 `gh release create --notes-file`；抽不到则回落 `--generate-notes` 并打印一条
警告。不这么做的话 CHANGELOG 就是个没人看、也没人维护的摆设 —— 而没人维护的文档比没有更糟，
它会让读者相信一份过期的事实。

### 5.3 README

- 顶部加 license badge，底部加 License 章节
- 「安装」章节补 Windows：PowerShell 一行命令，**紧跟一句「Windows 上 handoff 只能当协调者」**
  —— 这句话必须与安装命令同屏，放到别处等于没有
- 「升级」章节说明 Windows 自更新可用
- 「Troubleshooting」补两条：macOS 浏览器下载被 Gatekeeper 拦（§4.4）、Windows SmartScreen 提示（§4.5）

---

## 6. 契约测试的重写

`release_workflow_test.go` 现有一条 `TestWorkflowCoversExactlyFourPlatforms`，注释里明写
「多一项（尤其 windows）等于发一个 agentd 根本跑不起来的二进制」。**这条断言本次要反转，
但不能简单删掉** —— 它守的位置仍然需要有东西守着，只是约束变了：

| 断言 | 内容 | 挡住什么 |
|---|---|---|
| （保留）注入路径 | ldflags `-X …/internal/buildinfo.releaseVersion=` | 写成 GitHub owner 会静默失效 |
| （改）资产命名 | unix 侧 `.tar.gz`、windows 侧 `.zip` 两套模式都在 | 与 install 脚本、自更新的契约漂移 |
| （反转）矩阵 | 正好 6 项，逐项列出，含两项 windows | 少一项等于某平台装不上 |
| （新）签名不可摘 | darwin job `runs-on: macos*`，且含 `--options runtime`、`notarytool submit`、`spctl` | 有人为了让 CI 变快把签名步骤删了，静默发未签名版本 |
| （新）验证门不可摘 | release.yml 含 `uses: ./.github/workflows/ci.yml` | 有人为了赶发布把前置校验去掉 |

新断言的共同性质：**它们守的都是「删掉之后一切照常绿、只有用户会遭殃」的东西**。这正是
值得为一个 CI 配置写单测的理由。

---

## 7. 非目标

- **不做 agentd 的 Windows 支持**（B37 维持「已评估·暂不做」）。本文让 Windows 协调者装得上，
  不是让它能跑任务。
- **不发 `.pkg` / `.dmg`**，不进 Homebrew / winget / Scoop。
- **不做 Windows Authenticode 签名**（§4.5）。
- **不加 SECURITY.md / CONTRIBUTING.md**。08-13 用户明确不选。附一句备忘：SECURITY.md 对这个
  项目有实际意义（远程代码执行 + token 认证 + 权限门），将来想补不受本次影响。
- **不给非 darwin 平台补桌面通知**。沿用 B84 §4 的结论。
- **不改 `handoff upgrade` 的判定逻辑**。Windows 上它本来就走通用路径，本次只是让它能找到
  资产、能解开包。

---

## 8. 测试

**`internal/release`**：

1. `AssetName` 对 windows 返回 `.zip`、对 darwin/linux 返回 `.tar.gz`；
2. `extractBinary` 解 tar.gz（内含 `handoff`）成功；
3. `extractBinary` 解 zip（内含 `handoff.exe`）成功；
4. `extractBinary` 对内容不是归档的字节报错，且报文能区分「不是 gzip 也不是 zip」；
5. 包内没有目标文件时报错（两种格式各一条）；
6. `TempName` 在 Windows 上以 `.exe` 结尾 —— 用注入 goos 的内部函数测，不依赖 CI 跑在 Windows 上。

**`cmd`**：

7. `roleOptions("windows")` 只含协调者；`roleOptions("darwin")` / `("linux")` 含三项；
8. `defaultRole` 在 windows 下返回 `roleCoordinator`（不看探测结果）。

**workflow 契约**：§6 五条，全部在 `release_workflow_test.go` 里。

**安装脚本**：

9. `install_test.sh` 既有用例保持绿；Windows 拒绝理由改断言含 `install.ps1`；
10. 新增 `install_test.ps1`（§2.3 三项），在 windows-latest 上跑。

**平台门禁**：`GOOS=windows GOARCH=amd64 go build ./...` 与 `GOOS=windows GOARCH=arm64 go build ./...`
均须绿（后者是新增，arm64 是本次新发的资产）。

**真机验收**（不可用交叉编译或 CI 绿代替）：

11. **发一个真 tag**，确认产出 6 个资产 + checksums.txt，release notes 来自 CHANGELOG 而非
    自动生成；
12. **macOS**：从 Releases 页面用**浏览器**下 darwin tar.gz，解开后直接运行不被 Gatekeeper 拦
    （这正是签名唯一要买的东西，用 curl 下载验证等于没验证）；
13. **Windows**：`irm .../install.ps1 | iex` 装上 → `handoff init`（角色只有协调者）→
    `dispatch --target <执行机>` → `wait` → `diff` → `done` 全程走通 → `handoff upgrade` 能自更新。
    **无 Windows 机器时如实记为「未验」，不得记为已验。**

---

## 9. 风险

**风险一：`spctl` 因公证传播延迟偶发失败。** 票据在 Apple 侧生效需要一点时间，紧跟 `submit --wait`
之后立刻 `spctl` 有概率报未公证。缓解：重试 3 次、间隔 20s；仍失败即 fail 整个 job。
**不接受**「spctl 失败只警告」—— 那会让这道验证退化成装饰。

**风险二：darwin 构建从 ubuntu 交叉编译迁到 macOS runner，产物可能与此前不等价。**
最大的具体风险是 CGO（§4.1），已用显式 `CGO_ENABLED=0` 关掉。残余风险是 Go 工具链版本差异，
由 `go-version-file: go.mod` 两边一致兜住。

**风险三：Windows 全程无真机验收。** 本次改动的收益全部兑现在 Windows 机器上，而 CI 的
windows-latest 只能覆盖 install.ps1 与编译，覆盖不了 init/dispatch/upgrade 的真实回路。
按 §8.13 如实记录，不得因为 CI 绿就记已验。

**风险四：`irm | iex` 与 `curl | bash` 同级的信任形态。** 不新增风险类别（既有 install.sh
已是同一形态），但把它扩到了第二个平台。缓解是 §2.3 的测试覆盖与 Worker 的 302（脚本唯一
权威是仓库文件，Worker 不托管内容）。

**风险五：CHANGELOG 抽取失败会静默降级成自动生成的 notes。** 这是刻意的（发布不该因为
文档格式失误而中断），但必须打印警告，否则「CHANGELOG 承重」这个设计会在无人察觉中失效。
