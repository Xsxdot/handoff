# W5b-3 台账：薄壳构建链与分发

范围：5 个 task（Task 1 为只读核对，不产生提交）。分支 `claude/desktop-wrap-up-63b5ef`。
恢复现场以本 ledger + git log 为准。

**执行方式与前两份不同**：W5b-1 / W5b-2 派 mac-02 + opencode 执行、协调者复验；
本份由协调者**本机直接实现**（用户 2026-08-18 明确「不派发，你直接做」）。
曾派出去一次（任务 `4a63654e`），开跑约 4 分钟后按用户指示 `handoff stop` 中止，
远端 worktree 已 `reclaim --force` 回收，其半成品（改了 `desktop/.gitignore`、
建了一份 ledger）**全部丢弃重做**，未合入任何内容。

## 派发前的基线复核（六处判据与现状不符）

计划写于 08-17，实现于 08-18，中间基线动过。派发前把每条判据放到当前代码上跑了一遍，
改了六处（详见 commit `2cea54e9`，计划文件内每处都有说明块）。其中两处会直接卡住执行：

1. **Task 1 的改动已在基线上**（`c799c2b95`），原判据「确认当前生效的是 gtk4」必然对不上。
   且原清理判据 `grep "gtk-4\|gtk4\|..."` **在正确终态上也过不去**——正确的 gtk3 配置里
   本就有 `webkit2gtk-4.1` / `webkit2gtk4.1`，都含那个子串。照它执行会得出「没清干净」的
   错误结论，进而删掉正确的行。
2. **AppImage 打包会在仓库树内落三个未忽略的文件**，让 job 末尾的「工作区干净」必然失败。
3. Node 版本 `20` → `'24'`（唯一来源 `web/.nvmrc`）。
4. Task 3 对内嵌机制的描述是错的（详见下节）。
5. Task 3 自写的钥匙串步骤与既有 `build-darwin` 有两处实质差异，改回照抄既有那份。
6. **Task 4 整份漏了 `release` job 的 `needs` 扩展**。

## 进度

- 2026-08-18 **Task 1（nfpm 依赖核对）完成，无提交**。改动已在基线上（`c799c2b95`）。
  核对结果：`depends` = `['libgtk-3-0','libwebkit2gtk-4.1-0']`，rpm = `['gtk3','webkit2gtk4.1']`，
  archlinux = `['gtk3','webkit2gtk-4.1']`；用修正后的精确模式
  `grep -nE "libgtk-4|libwebkitgtk-6|gtk4-|webkitgtk6"` 查残留，无输出；YAML 经 pyyaml 解析合法。

- 2026-08-18 **Task 2（Linux 薄壳 job + gitignore）完成，commit `df2958c0d`**。
  `desktop/.gitignore` 补三条，`git check-ignore` 逐条实测命中：
  `build/linux/appimage/handoff-desktop`、同目录 `.png`、`build/linux/handoff-desktop.desktop`
  （来源都在 `desktop/build/linux/Taskfile.yml`：`create:appimage` 的两条 `cp`、
  `generate:dotdesktop` 的 `-outputfile`）。release.yml 只新增不删改（`git diff | grep '^-'` 空）。

- 2026-08-18 **Task 3（macOS 薄壳 job）完成，commit `be7bead53`**。本轮挖出并修掉一个
  **会静默发出空壳的传参错误**，见下节。

- 2026-08-18 **Task 4（资产接进发布）完成，commit `eda9492b3`**。`release.needs` 扩到四个 job；
  `sha256sum` 与 `gh release create` 各补 `handoff-desktop_*`；补用例
  `TestAssetNameNeverMatchesDesktopAsset`。通配隔离已实测：临时目录里放两个文件，
  `handoff_*` 只匹配到 CLI 那个，薄壳包不在其中。

- 2026-08-18 **Task 5（终验与落账）完成**。见「验收证据」。

## 本轮挖出的两个计划外缺陷（都属静默失效类）

### 1. `GO_FLAGS` 被 Taskfile 静默忽略，会发出不含内嵌 CLI 的薄壳

计划两个 job 都写 `wails3 task package GO_FLAGS="-tags embedbin -ldflags=..."`。
**整个 Taskfile 没有任何地方消费 `GO_FLAGS`**——它只认 `EXTRA_TAGS`。`--dry` 实测对比：

```
GO_FLAGS=...   → go build -tags production ...          # embedbin 不在
EXTRA_TAGS=... → go build -tags production,embedbin ... # 正确
```

后果不是构建失败，是编出一个 `embedbin.Available()` 走 stub、根本不含内嵌 CLI 的薄壳，
而这要到**用户双击之后**才暴露——「双击就能用」当场破功，恰是 W5b-2 整轮工作的目的。
已改用 `EXTRA_TAGS=embedbin`；`gtk3` 不必再传，linux 的 `BUILD_FLAGS` 本就写死了它。

### 2. 模板没有 ldflags 注入口，`embedbin.Version` 发不出去

`BUILD_FLAGS` 把 `-ldflags` 写死成 `"-w -s"`，没有任何外部注入口，而 release 构建要注入
`embedbin.Version`。缺它同样是静默的：`DecideRelease` 永远判不出内嵌版本，
`release.go` 的保守规则一路走 `use-existing`，**「已装的 CLI 比内嵌的旧」这条提示分支彻底失效**。
已给 `desktop/build/{darwin,linux}/Taskfile.yml` 加 `EXTRA_LDFLAGS` 钩子（与模板既有的
`EXTRA_TAGS` 同一风格）。不传这两个变量时构建行为与改动前逐字一致，已用 `--dry` 回归确认。

## 计划中一处事实错误（已在计划与代码注释两侧改正）

计划原文：「`--deep` 是必要的：内嵌的 handoff 在 bundle 里，外层签名要覆盖到它」。
**实际不是**：`embed.go` 用 `//go:embed handoff`，那份 CLI 是**编进薄壳可执行文件的字节块**，
不是 bundle 里的文件。三条推论都已落进 workflow 注释：

1. `--deep` 在这里没有作用（`create:app` 只放一个可执行文件 + icns + Info.plist，
   bundle 里没有嵌套代码项），且 Apple 已不建议用它签名。**已去掉。**
2. 「先签内层」这条纪律比原文更硬：嵌进去之后它就只是数据，**再没有任何机会给它签名**。
   顺序颠倒的后果不是「签名与字节不符」，是释出的那份**根本没签名**。
3. 公证覆盖不到它（见下方 P2）。

## 验收证据（协调者本机，macOS + wails3 v3.0.0-beta.8）

- 主模块 `go test ./... -count=1`：**33 包 ok，0 FAIL**
- desktop 模块 `go test ./... -count=1`：**2 包 ok，0 FAIL**
- `gofmt -l`：两模块**均零输出**
- `go vet ./...`：两模块**均无输出**
- `git status --porcelain`：零输出（构建产物已清）
- `go.mod` / `go.sum` / `desktop/go.mod` / `desktop/go.sum`：相对 HEAD **均未改动**
  （`wails3 task` 的 `go:mod:tidy` 依赖跑过，确认是 no-op）
- **签名顺序实测**（Task 3 Step 3，本机真跑）：`go build` 出 CLI → adhoc 签内层 →
  `codesign --verify --strict` 通过 → `wails3 task package` → **不带 `--deep`** 签外层 →
  `codesign --verify --strict` 通过。产物 `go version -m` 显示 `-tags=production,embedbin`，
  注入的版本串在二进制里命中（`strings | grep -c` = 1）。
- **变异复验六条全部翻红**（照 B86 立下的房规：CI 配置跑不了真 runner，就把每道门
  故意打断、确认测试真的红。每条都确认变异真改到文件、跑完还原、工作区干净）：
  ① linux 那处 `EXTRA_TAGS` 改回 `GO_FLAGS`（本轮真踩过的错）
  ② linux 那处去掉 `EXTRA_LDFLAGS`
  ③ 去掉装 wails3 的 `-tags gtk3`
  ④ 内嵌 CLI 不再单独签名
  ⑤ 薄壳 job 漏注入 CLI 版本路径
  ⑥ `AssetName` 改成 `handoff-desktop_` 前缀（撞车检测）

  **其中第①条第一次跑是绿的，当场抓出我自己刚写的那道门是假门**：
  `TestDesktopJobsCarryLoadBearingFlags` 初版用 `strings.Contains`，而两个薄壳 job
  各传一次 `EXTRA_TAGS`，改坏一处另一处还在，Contains 照样满足。这与本文件顶上
  `TestWorkflowInjectsVersionAtModulePath` 注释里写的是同一个坑。已改为逐条带期望
  出现次数（两个 job 各一次的记 2、单 job 的记 1），重跑六条全红。
  **这条门若不做变异复验，会以「已验」的姿态一直假绿下去。**

- **既有契约测试 `TestWorkflowInjectsVersionAtModulePath` 曾翻红并已正确收口**：
  它断言 workflow 恰含 **2** 处 CLI 版本注入，而两个薄壳 job 各自也要编一份 CLI 嵌进壳里，
  实得 4 处。这个数字是「编 CLI 的地方有几处」的代理，已改为 4 并在注释里写明是哪四处、
  以及漏掉薄壳那两处的症状（壳能跑，但它释出的 CLI 自称 unknown、自更新永远认为已是最新）。

## 未验项（四条，逐条写明为什么没验、什么条件下能验）

| 项 | 状态 | 为什么没验 | 什么条件下能验 |
|---|---|---|---|
| P1-linux（**运行**侧） | ⛔ 未验 | 无带图形界面的真 Linux（用户确认）。构建侧已于 08-17 在 Ubuntu 22.04.5 容器里实证 | 一台有显示服务的 Linux |
| P3（AppImage 跨发行版） | ⛔ 未验 | 同上 | 同上，且需非 Ubuntu 发行版 |
| P2（Gatekeeper 放行**已释出**的 CLI） | ⛔ 未验 | 需真向 Apple 提交公证，属对外可见操作；本轮按用户决定不触发真发布 | 见下方专段 |
| P1-win（运行侧） | ⛔ 未验 | 无 Windows 机器；Windows runner 按本计划范围**不接进流水线**（spec §4.6 已定选项 B，但运行侧没人验过） | 等 B37 那轮带真机一起做 |

**产物照出，但不得声称 Linux 可用。**

### P2 的具体机制（不能只写「未验」）

内嵌的 CLI 是 `//go:embed handoff` 进薄壳可执行文件的**字节块**，不是 bundle 里的文件，
所以 notary 服务这次提交**看不见它**、不会为它登记票据。它被释出到 `~/.local/bin/handoff`
后能否过 Gatekeeper，取决于它的 cdhash 是否与 `build-darwin` 那条**独立公证过的 CLI 资产**
一致（同 `-trimpath`、同 ldflags、同签名身份，理论上一致，**从未实测**）。

验法：发版后在一台干净 mac 上装 `.app`、让它释出 CLI，再对释出的那份跑

```
spctl -a -vvv -t open --context context:primary-signature ~/.local/bin/handoff
```

期望 `accepted` 且 `source=Notarized Developer ID`。
（喂法与 bundle 那条相反：app bundle 用 `-t exec`，裸 CLI 才用 `-t open`——
既有 `build-darwin` 的长注释记着 2026-08-13 踩反过一次的经过。）

### 最大的那条：流水线本身从未在真 runner 上跑过

**「流水线语法正确」不等于「流水线跑得通」。** 本轮所有 CI 改动都只经过
YAML 解析、`--dry` 对比与本机等价复现，**没有任何一次真实 runner 执行**：
两个新 job 的 `apt-get`、`go install -tags gtk3`、`npm ci`、`wails3 task linux:package`、
资产重命名的通配、以及 `release` job 收集四份 artifact 的整条链路，都还是纸面推演。
第一次打 tag 发版时要盯着这两个 job 看。
