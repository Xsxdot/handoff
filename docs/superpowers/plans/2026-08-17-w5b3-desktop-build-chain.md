# W5b-3 薄壳构建链与分发 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 release 流水线产出可分发的三平台薄壳资产——macOS 签名公证的 `.app`/dmg、Linux 的 AppImage 与 deb——且不干扰既有的 CLI 资产与 `install.sh`。

**Architecture:** 在 `release.yml` 里新增两个 job（`build-desktop-linux` / `build-desktop-darwin`），各自原生构建；macOS 那条严格按 spec §5.4 的顺序「签 handoff → 嵌进薄壳 → 签+公证 bundle」，因为释出到 `~/.local/bin/` 的二进制脱离了 bundle 的签名覆盖。薄壳资产用独立前缀 `handoff-desktop_`，与 CLI 资产互不干扰。

**Tech Stack:** GitHub Actions、Wails v3 beta.8（`wails3 task`）、nfpm、Go 1.26。

## Global Constraints

以下为 spec 的项目级约束，**每个 task 的需求都隐含包含本节**：

- **Linux 一律锁 `ubuntu-22.04`，不用 `ubuntu-latest`**。AppImage 要在最老的目标发行版上构建；`ubuntu-latest` 会随 GitHub 滚动，某天静默抬高 glibc 基线，而这个变化不体现在任何一次提交里。
- **Linux 上装 `wails3` 工具与构建薄壳，两步都要带 `-tags gtk3`**。不带的话在 22.04 上直接 `No package 'gtk4' found` 退出 1——卡点在准备工具阶段，报错却像「薄壳代码有问题」。
- **薄壳必须开 CGO**（Wails 绑 WKWebView / WebKitGTK），因此会带上最低系统版本约束。**绝不因此去动 handoff 本体的 `CGO_ENABLED=0`**——那一行保护的是 CLI/agentd，与薄壳无关（`release.yml:167` 的注释）。
- **签名顺序是承重的**（spec §5.4）：先签 `handoff`，再嵌进薄壳，最后签+公证 bundle。顺序颠倒会让内嵌产物的签名与最终字节不符。
- **资产命名不得与 `install.sh` 及自更新的既有契约冲突**。CLI 资产是 `handoff_<tag>_<goos>_<goarch>.tar.gz|zip`，由 `install.sh:122` 与 `internal/release/client.go:83` **精确拼名**取用；薄壳资产必须用独立前缀。
- **不得往仓库提交任何构建产物**。`desktop/frontend/dist`、`desktop/bin`、`internal/webui/dist`、`desktop/internal/embedbin/handoff` 都是 gitignore 的；任一 job 结束时 `git status --porcelain` 必须为空。
- 日志与注释：workflow 的每个非显然步骤写「为什么」的中文注释（本仓库 `release.yml` 既有风格），新增脚本段落用 `set -euo pipefail`。
- **本计划不触发真实发布**：不打 tag、不跑真公证。签名公证 job 写完即止，P2 记为未验（用户决定）。

## 不在本计划范围内

| 不做 | 理由 |
|---|---|
| Windows 薄壳 runner | spec §4.6 已定为选项 B（纯协调者），但 **P1-win 运行侧仍未验**（无 Windows 机器）。接进流水线只会产出一个没人验证过能跑的安装包。等 B37 那一轮带着真机一起做 |
| B110 目录选择器 | **2026-08-17 实测后搁置**，见 spec §4.5 尾部的实测块与 backlog B110。控制台页面够不到 Wails 运行时，连 `ExecJS` 都不能搭桥 |
| P1-linux 运行侧 / P3（AppImage 跨发行版） | 无带图形界面的真 Linux（用户确认）。**产物照出，但不声称 Linux 可用** |
| P2（Gatekeeper 放行已释出的二进制） | 需真向 Apple 提交公证，属对外可见操作。流水线写好，等用户亲自发版时自然验证 |

## File Structure

| 文件 | 责任 |
|---|---|
| `.github/workflows/release.yml`（改） | 新增 `build-desktop-linux` / `build-desktop-darwin` 两个 job；`release` job 扩 `needs` 并收集薄壳资产 |
| `desktop/.gitignore`（改） | 补 AppImage 打包在仓库树内落下的三个副产物，否则 job 的干净检查必挂 |
| `desktop/build/linux/nfpm/nfpm.yaml`（**只读核对**） | 依赖已是 gtk3 那套（`c799c2b95` 已落），本轮不改 |
| `internal/release/`（改） | 补一条钉死 CLI 资产名不撞薄壳前缀的用例 |
| `docs/ledger-w5b3.md`（新） | 进度与未验项落账 |

> **2026-08-18 协调者在基线上复核过全部判据，改了六处**（每处都在对应位置有说明块）：
> ①Task 1 的改动已在基线完成，降级为只读核对，且原判据的 grep 模式会误伤正确配置；
> ②Task 2 补 `.gitignore`，否则 AppImage 打包落在仓库树内的三个文件会让「工作区干净」当场失败；
> ③Node 版本 20 → `'24'`（唯一来源是 `web/.nvmrc`）；
> ④Task 3 的 `--deep` 理由是错的（内嵌 CLI 是 `go:embed` 字节块，不是 bundle 里的文件），已去掉并改写纪律；
> ⑤Task 3 的钥匙串步骤改回照抄既有 build-darwin 那份，并补上 `status: Accepted` 与 spctl 两道公证门；
> ⑥Task 4 补 `release` job 的 `needs` 扩展——原计划整份漏了它。

---

## Task 1: 核对 nfpm 的依赖声明（**改动已在基线上完成，本 task 只做核对**）

> **2026-08-18 协调者基线复核后重写。** 本 task 原本要求把 nfpm 的 `depends`
> 从 gtk4 那套改成 gtk3 那套。**该改动已经落在基线上了**（提交 `c799c2b95`
> "fix(desktop): nfpm 依赖改为 gtk3 那套，与构建 tag 对齐"），所以原文的
> Step 1（「Expected: 看到生效的 depends 是 gtk4」）在当前代码上**必然对不上**。
> 不要因此以为自己看错了或仓库不对——照下面的核对步骤走即可，**本 task 不产生提交**。
>
> 原 Step 3 的判据**本身就是错的**，即使在正确的终态上也过不去：它 grep 的模式
> 含 `gtk4` 与 `gtk-4`，而正确的 gtk3 配置里就有 `webkit2gtk4.1`（rpm）与
> `libwebkit2gtk-4.1-0`（deb）——两者都含这个子串。照原判据执行会得出「还没清干净」
> 的错误结论，进而可能把**正确的行**删掉。已改为只查真正的 gtk4 依赖名。

**Files:**
- 只读核对：`desktop/build/linux/nfpm/nfpm.yaml`

**Interfaces:**
- Consumes: 无
- Produces: 无（确认 Task 2 的 deb 打包所依赖的前提成立）

- [ ] **Step 1: 确认生效的是 gtk3 那套**

Run: `sed -n '20,60p' desktop/build/linux/nfpm/nfpm.yaml`
Expected: 生效的 `depends:` 是 `libgtk-3-0` / `libwebkit2gtk-4.1-0`；`overrides.rpm` 是 `gtk3` / `webkit2gtk4.1`；`overrides.archlinux` 是 `gtk3` / `webkit2gtk-4.1`

- [ ] **Step 2: 确认没有残留的 gtk4 依赖声明**

Run: `grep -nE "libgtk-4|libwebkitgtk-6|gtk4-|webkitgtk6" desktop/build/linux/nfpm/nfpm.yaml`
Expected: **无输出**。注意判据只查 GTK4/WebKitGTK-6.0 的**依赖包名**，不要用会误伤 `webkit2gtk-4.1` 的宽泛模式。

- [ ] **Step 3: 确认 YAML 仍合法**

Run: `python3 -c "import yaml,sys; d=yaml.safe_load(open('desktop/build/linux/nfpm/nfpm.yaml')); print(d['depends'])"`
Expected: `['libgtk-3-0', 'libwebkit2gtk-4.1-0']`

- [ ] **Step 4: 不提交**

本 task 无改动，**不要**造一个空提交，也不要为了「让 task 有产出」去改动这个文件。ledger 里记一行「Task 1 已在基线完成（c799c2b95），本轮仅核对」。

---

## Task 2: Linux 薄壳构建 job

**Files:**
- Modify: `.github/workflows/release.yml`

**Files:**
- Modify: `.github/workflows/release.yml`
- Modify: `desktop/.gitignore`

**Interfaces:**
- Consumes: Task 1 核对过的 `nfpm.yaml`
- Produces: 名为 `build-desktop-linux` 的 job，产出 artifact `handoff-desktop_linux`，内含 `handoff-desktop_${TAG}_linux_amd64.AppImage` 与 `handoff-desktop_${TAG}_linux_amd64.deb`

- [ ] **Step 1: 先给 AppImage 打包的副产物补 `.gitignore`（否则本 job 的干净检查必挂）**

> **2026-08-18 协调者基线复核补入。** `wails3 task linux:package` 会在**仓库树内**
> 落三个未被忽略的文件，job 末尾的 `git status --porcelain` 会当场非空、整个
> job 失败——而症状看起来像「薄壳构建污染了工作区」，很容易被误判成代码问题。
> 三个文件的来源都在 `desktop/build/linux/Taskfile.yml` 里写死：
> `create:appimage` 的 `cp "{{.APP_BINARY}}" "{{.APP_NAME}}"` 与
> `cp ../../appicon.png "{{.APP_NAME}}.png"` 落前两个，
> `generate:dotdesktop` 的 `-outputfile .../build/linux/{{.APP_NAME}}.desktop` 落第三个。
> 现有 `desktop/.gitignore` 只忽略了 `build/linux/appimage/build`（构建目录），漏了这三个。

在 `desktop/.gitignore` 的 `build/linux/appimage/build` 一行之后追加：

```gitignore
# linux 打包副产物：create:appimage 会把二进制与图标 cp 进 appimage 目录，
# generate:dotdesktop 会生成 .desktop 文件。三者都落在仓库树内，不忽略的话
# 「构建后工作区干净」这条判据（W5b-1 起就是承重判据）必然失败。
build/linux/appimage/handoff-desktop
build/linux/appimage/handoff-desktop.png
build/linux/handoff-desktop.desktop
```

Verify: `cd desktop && git check-ignore -q build/linux/appimage/handoff-desktop && git check-ignore -q build/linux/appimage/handoff-desktop.png && git check-ignore -q build/linux/handoff-desktop.desktop && echo OK`
Expected: 打印 `OK`

- [ ] **Step 2: 写 job**

在 `build-darwin` job 之后插入。逐条注释都要写清「为什么」。

> **注意两处已按基线修正**：①`node-version` 用 `'24'` 并带 npm 缓存，与既有
> `build-unix` / `build-darwin` 一致——本仓库 Node 版本的唯一来源是 `web/.nvmrc`
> （当前为 `24`），原计划写的 `20` 与之不符；②`wails3 task linux:package` 实际
> 还会产出 rpm 与 archlinux 包（`create:rpm` / `create:aur`），本计划只取
> AppImage 与 deb 发布，其余留在 `desktop/bin/`（已被忽略）不管即可。

```yaml
  # 薄壳的 Linux 资产必须在原生 runner 上构建：webkit2gtk 经 cgo，
  # CGO_ENABLED=0 交叉编译编不过（P1-linux 探针实测）。
  # runner 锁 22.04 而非 ubuntu-latest：AppImage 要在最老的目标发行版上
  # 构建，latest 会随 GitHub 滚动、某天静默抬高 glibc 基线，而这个变化
  # 不体现在任何一次提交里。
  build-desktop-linux:
    needs: verify
    runs-on: ubuntu-22.04
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      # Node 版本与 web/.nvmrc 同一来源，与既有 build-unix / build-darwin 保持一致
      - uses: actions/setup-node@v4
        with:
          node-version: '24'
          cache: npm
          cache-dependency-path: web/package-lock.json

      # 22.04 上只有 webkit2gtk-4.1 这一代；4.0 见 spec §7 的非目标
      - name: 装系统依赖
        run: |
          set -euo pipefail
          sudo apt-get update
          sudo apt-get install -y libgtk-3-dev libwebkit2gtk-4.1-dev

      # 承重：装 wails3 这一步**本身**也要带 -tags gtk3。不带的话在 22.04 上
      # 直接 `No package 'gtk4' found` 退出 1——卡点在准备工具阶段，比构建期
      # 更早，报错却和「薄壳代码有问题」长得一模一样（P1-linux 探针实测）。
      - name: 装 wails3
        run: go install -tags gtk3 github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.8

      # 薄壳内嵌的 handoff 必须是同一次流水线编出来的那份，且 CLI 保持
      # CGO_ENABLED=0（release.yml:167 的既有约束，与薄壳开 CGO 互不影响）
      - name: 构建要内嵌的 handoff
        env:
          CGO_ENABLED: '0'
        run: |
          set -euo pipefail
          TAG="${GITHUB_REF_NAME}"
          npm --prefix web ci
          npm --prefix web run build
          test -f web/dist/index.html
          rm -rf internal/webui/dist
          cp -R web/dist internal/webui/dist
          go build -trimpath -tags embedweb \
            -ldflags "-s -w -X github.com/Xsxdot/handoff/internal/buildinfo.releaseVersion=${TAG}" \
            -o desktop/internal/embedbin/handoff .

      - name: 构建薄壳并打包
        working-directory: desktop
        env:
          CGO_ENABLED: '1'
        run: |
          set -euo pipefail
          TAG="${GITHUB_REF_NAME}"
          npm --prefix frontend ci
          # 两处 tag 缺一不可：gtk3 决定链哪一代 webkit，embedbin 决定
          # embedbin.Available() 走真产物那一支
          wails3 task linux:package \
            GO_FLAGS="-tags gtk3,embedbin -ldflags=-X=github.com/Xsxdot/handoff/desktop/internal/embedbin.Version=${TAG}"

      - name: 重命名为发布资产名
        run: |
          set -euo pipefail
          TAG="${GITHUB_REF_NAME}"
          # 独立前缀 handoff-desktop_：install.sh 与自更新都是精确拼
          # handoff_<tag>_<平台> 取 CLI 资产，前缀不同就不会被误抓
          mv desktop/bin/*.AppImage "handoff-desktop_${TAG}_linux_amd64.AppImage"
          mv desktop/bin/*.deb      "handoff-desktop_${TAG}_linux_amd64.deb"

      # 干净检查：构建产物不得入库（W5b-1 在这里栽过一次）
      - name: 确认工作区干净
        run: |
          set -euo pipefail
          out="$(git status --porcelain)"
          test -z "$out" || { echo "构建污染了工作区："; echo "$out"; exit 1; }

      # if-no-files-found: error 与既有 job 一致：静默上传一个空 artifact
      # 会让 release job 在收集资产时才暴露问题，那时已经离现场很远了
      - uses: actions/upload-artifact@v4
        with:
          name: handoff-desktop_linux
          path: handoff-desktop_*
          if-no-files-found: error
```

- [ ] **Step 3: 校验 YAML 合法**

Run: `python3 -c "import yaml; d=yaml.safe_load(open('.github/workflows/release.yml')); print(list(d['jobs'].keys()))"`
Expected: 输出的 job 列表里含 `build-desktop-linux`

- [ ] **Step 4: 确认没有误碰既有 job**

Run: `git diff .github/workflows/release.yml | grep -E "^-" | grep -v "^---" | head -20`
Expected: **无输出**（本 task 对 release.yml 只新增，不删改任何既有行；`.gitignore` 的追加不在此文件内）

- [ ] **Step 5: 确认 deb 里的依赖是 gtk3 那套**

本地无法跑完整 job，但可以单独验证 nfpm 配置被正确消费——这一条留到 Task 5 的真实流水线跑通后确认，此处只记账。

- [ ] **Step 6: Commit**

```bash
git add .github/workflows/release.yml desktop/.gitignore
git commit -m "ci(release): 新增 Linux 薄壳构建 job

原生 runner 锁 ubuntu-22.04；装 wails3 与构建薄壳两步都带 -tags gtk3
（不带则在准备工具阶段就 No package 'gtk4' found，报错像是代码问题）。
内嵌的 handoff 由同一次流水线以 CGO_ENABLED=0 + embedweb 编出，薄壳自身
开 CGO。资产用独立前缀 handoff-desktop_，避开 install.sh 与自更新的精确
拼名。末尾有工作区干净检查。

同时给 AppImage 打包的三个副产物补 .gitignore：create:appimage 会把二进制
与图标 cp 进仓库树内的 appimage 目录，generate:dotdesktop 会生成 .desktop
文件，三者都不在忽略名单里，会让干净检查当场失败。"
```

---

## Task 3: macOS 薄壳构建 job（含签名公证）

**Files:**
- Modify: `.github/workflows/release.yml`

**Interfaces:**
- Consumes: 既有 `build-darwin` job 里已建立的钥匙串装载步骤与 secrets（`APPLE_CERTIFICATE` / `APPLE_CERTIFICATE_PASSWORD` / `APPLE_SIGNING_IDENTITY` / `APPLE_API_ISSUER` / `APPLE_API_KEY` / `APPLE_API_KEY_CONTENT`）
- Produces: 名为 `build-desktop-darwin` 的 job，产出 artifact `handoff-desktop_darwin`，内含 `handoff-desktop_${TAG}_darwin_arm64.zip`

**承重（spec §5.4）：签名顺序不能颠倒。** 释出到 `~/.local/bin/handoff` 的二进制**脱离了 `.app` bundle 的签名覆盖**，Gatekeeper 会单独校验它。所以内嵌进去的必须是**已单独签名**的那份，顺序固定为：

```
签 handoff → 嵌进薄壳 → 构建薄壳 → 签 + 公证 bundle
```

一旦先签 bundle 再补嵌，签名与最终字节就不符了（`release.yml:158` 的既有注释讲的是同一件事）。

> **2026-08-18 协调者基线复核：原文对「内嵌」的机制描述是错的，已改。** 原 Step 1
> 在签 bundle 那一步写着「`--deep` 是必要的：内嵌的 handoff 在 bundle 里，外层签名
> 要覆盖到它」。**实际不是这样**——`desktop/internal/embedbin/embed.go` 用的是
> `//go:embed handoff`，那份 CLI 是**编进薄壳可执行文件里的字节块**，不是 bundle
> 里的一个文件。由此三条推论，都写进了下面的 job：
>
> 1. **`--deep` 在这里没有作用**，因为 bundle 里根本没有嵌套的代码项（`create:app`
>    只放 `Contents/MacOS/handoff-desktop` + icns + Info.plist）。Apple 本身也已
>    不建议用 `--deep` 签名。已去掉，外层只签 bundle 自己。
> 2. **「先签内层」这条纪律不但成立，而且比原文更硬**：一旦嵌进去它就只是数据，
>    之后**再没有任何机会**给它签名。顺序颠倒的后果不是「签名与字节不符」，是
>    释出到 `~/.local/bin/handoff` 的那份**根本没有签名**。
> 3. **公证覆盖不到它**：notary 服务只看得见提交物里的 Mach-O，看不见被 embed
>    进另一个二进制的字节块，因此**这次提交不会为它登记票据**。它在用户机器上
>    能否放行，取决于它的 cdhash 是否与 `build-darwin` 那条独立公证过的 CLI 资产
>    一致（同样的 `-trimpath` + 同样的 ldflags + 同一个签名身份，理论上一致，
>    **但从未实测**）。**这正是 P2 要验的东西**，Task 5 的 ledger 必须把这条机制
>    写清楚，而不是笼统写一句「P2 未验」。

- [ ] **Step 1: 写 job**

```yaml
  # 薄壳的 darwin 资产必须在 macOS runner 上构建：codesign 与 notarytool
  # 只在那儿有，且 Wails 绑 WKWebView 需要 CGO。
  build-desktop-darwin:
    needs: verify
    runs-on: macos-latest
    env:
      APPLE_CERTIFICATE: ${{ secrets.APPLE_CERTIFICATE }}
      APPLE_CERTIFICATE_PASSWORD: ${{ secrets.APPLE_CERTIFICATE_PASSWORD }}
      APPLE_SIGNING_IDENTITY: ${{ secrets.APPLE_SIGNING_IDENTITY }}
      APPLE_API_ISSUER: ${{ secrets.APPLE_API_ISSUER }}
      APPLE_API_KEY: ${{ secrets.APPLE_API_KEY }}
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      # Node 版本与 web/.nvmrc 同一来源，与既有 build-darwin 保持一致
      - uses: actions/setup-node@v4
        with:
          node-version: '24'
          cache: npm
          cache-dependency-path: web/package-lock.json
      - name: 装 wails3
        run: go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.8

      # 与既有 build-darwin 同款的前置门：secrets 缺失即硬失败，不产出未签名
      # 资产。一个静默发出去的未签名薄壳会在几个月后咬人，且症状出现在用户机器上。
      - name: 检查签名凭据齐备
        env:
          APPLE_API_KEY_CONTENT: ${{ secrets.APPLE_API_KEY_CONTENT }}
        run: |
          set -euo pipefail
          missing=""
          for v in APPLE_CERTIFICATE APPLE_CERTIFICATE_PASSWORD APPLE_SIGNING_IDENTITY \
                   APPLE_API_ISSUER APPLE_API_KEY APPLE_API_KEY_CONTENT; do
            if [ -z "${!v:-}" ]; then missing="${missing} ${v}"; fi
          done
          if [ -n "$missing" ]; then
            printf '缺少签名 secret：%s\n未签名的 release 不发。\n' "$missing" >&2
            exit 1
          fi
          echo "签名凭据齐备"

      # 第一步：编出 handoff 并**单独签名**。它会被释出到 ~/.local/bin/，
      # 脱离 bundle 的签名覆盖，Gatekeeper 单独校验它（spec §5.4）。
      # CLI 保持 CGO_ENABLED=0：开了 CGO 会让它带上构建机的最低系统版本
      # 约束，在更老的 macOS 上拒绝启动（release.yml:167）。
      - name: 构建并签名要内嵌的 handoff
        env:
          CGO_ENABLED: '0'
        run: |
          set -euo pipefail
          TAG="${GITHUB_REF_NAME}"
          npm --prefix web ci
          npm --prefix web run build
          test -f web/dist/index.html
          rm -rf internal/webui/dist
          cp -R web/dist internal/webui/dist
          go build -trimpath -tags embedweb \
            -ldflags "-s -w -X github.com/Xsxdot/handoff/internal/buildinfo.releaseVersion=${TAG}" \
            -o desktop/internal/embedbin/handoff .

      # App Store Connect API Key 只能以文件路径传给 notarytool（与既有 job 同款，
      # 把路径经 GITHUB_ENV 传给后面的公证步骤）
      - name: 写 App Store Connect API Key
        env:
          APPLE_API_KEY_CONTENT: ${{ secrets.APPLE_API_KEY_CONTENT }}
        run: |
          set -euo pipefail
          key_path="${RUNNER_TEMP}/AuthKey.p8"
          printf '%s\n' "$APPLE_API_KEY_CONTENT" > "$key_path"
          chmod 600 "$key_path"
          # 只确认私钥文件头形态，不打印密钥正文
          head -n 1 "$key_path" | grep -q 'BEGIN PRIVATE KEY' || {
            echo "APPLE_API_KEY_CONTENT 不像是一份 .p8 私钥" >&2; exit 1; }
          echo "APPLE_API_KEY_PATH=${key_path}" >> "$GITHUB_ENV"

      # 钥匙串装载：**逐字照抄既有 build-darwin 的「导入签名证书到临时钥匙串」
      # 步骤**（本文件内已在生产使用、已验证过的那一份），只改钥匙串文件名以免
      # 与它撞名。抽成复合步骤会牵动那个已在生产用的 job，风险大于收益，因此
      # 这里重复一次——**两处必须同步改**。
      #
      # 2026-08-18 协调者复核：原计划自己另写了一版，与既有实现有两处实质差异，
      # 都已改回既有版本——①原版用空口令建钥匙串，既有版用 `openssl rand`
      # 随机口令；②原版 `security list-keychain -d user -s "$keychain_path"`
      # 会把用户搜索链**整个替换**成只剩这一个钥匙串，既有版把原有列表拼在后面
      # （`$(security list-keychains -d user | sed 's/"//g')`），不会踢掉系统钥匙串。
      - name: 导入签名证书到临时钥匙串
        run: |
          set -euo pipefail
          cert_path="${RUNNER_TEMP}/desktop-certificate.p12"
          keychain_path="${RUNNER_TEMP}/handoff-desktop-build.keychain-db"
          keychain_password="$(openssl rand -base64 32)"
          # Secrets 存的是 openssl base64 -A 的单行内容
          echo "$APPLE_CERTIFICATE" | base64 --decode > "$cert_path"
          security create-keychain -p "$keychain_password" "$keychain_path"
          security set-keychain-settings -lut 21600 "$keychain_path"
          security unlock-keychain -p "$keychain_password" "$keychain_path"
          security import "$cert_path" -P "$APPLE_CERTIFICATE_PASSWORD" \
            -A -t cert -f pkcs12 -k "$keychain_path"
          # 让 codesign 能在无 UI 提示下使用私钥
          security set-key-partition-list -S apple-tool:,apple:,codesign: \
            -s -k "$keychain_password" "$keychain_path"
          security list-keychains -d user -s "$keychain_path" \
            $(security list-keychains -d user | sed 's/"//g')
          security find-identity -v -p codesigning "$keychain_path" \
            | grep -F "$APPLE_SIGNING_IDENTITY" > /dev/null || {
              echo "钥匙串里找不到 APPLE_SIGNING_IDENTITY" >&2; exit 1; }
          rm -f "$cert_path"

      - name: 签名内嵌的 handoff
        run: |
          set -euo pipefail
          codesign --force --options runtime --timestamp \
            --sign "$APPLE_SIGNING_IDENTITY" desktop/internal/embedbin/handoff
          codesign --verify --strict --verbose=2 desktop/internal/embedbin/handoff

      # 第二步：把已签名的 handoff 嵌进薄壳并构建 bundle。
      # 注意 `wails3 task build` 只出裸二进制，**`.app` 要靠 package 任务
      # 组装**——只跑 build 会拿到一个陈旧的 .app（实测踩过）。
      - name: 构建薄壳 .app
        working-directory: desktop
        env:
          CGO_ENABLED: '1'
        run: |
          set -euo pipefail
          TAG="${GITHUB_REF_NAME}"
          npm --prefix frontend ci
          wails3 task package \
            GO_FLAGS="-tags embedbin -ldflags=-X=github.com/Xsxdot/handoff/desktop/internal/embedbin.Version=${TAG}"

      # 第三步：签 + 公证 bundle。**不用 --deep**：bundle 里没有嵌套代码项
      #（create:app 只放一个可执行文件 + icns + Info.plist），内嵌的 handoff 是
      # go:embed 进可执行文件的字节块、不是 bundle 里的文件，--deep 对它没有作用；
      # Apple 本身也已不建议用 --deep 签名。
      - name: 签名并公证 bundle
        run: |
          set -euo pipefail
          app="desktop/bin/handoff-desktop.app"
          TAG="${GITHUB_REF_NAME}"
          # --options runtime（硬化运行时）是公证的前置条件，不加会被拒
          codesign --force --options runtime --timestamp \
            --sign "$APPLE_SIGNING_IDENTITY" "$app"
          codesign --verify --strict --verbose=2 "$app"

          ditto -c -k --keepParent "$app" "notarize-desktop.zip"
          xcrun notarytool submit "notarize-desktop.zip" \
            --key "$APPLE_API_KEY_PATH" \
            --key-id "$APPLE_API_KEY" \
            --issuer "$APPLE_API_ISSUER" \
            --wait 2>&1 | tee /tmp/notary-desktop.log
          # 承重：notarytool 在 status 为 Invalid 时**仍可能退 0**，光看退出码不够。
          # 这条门是既有 build-darwin 已经踩出来的教训，薄壳这条路同样要有。
          grep -q 'status: Accepted' /tmp/notary-desktop.log || {
            echo "公证未被接受：" >&2; cat /tmp/notary-desktop.log >&2; exit 1; }

          # 装订票据后重新打包，让离线机器也能通过 Gatekeeper
          xcrun stapler staple "$app"
          rm -f "notarize-desktop.zip"
          ditto -c -k --keepParent "$app" "handoff-desktop_${TAG}_darwin_arm64.zip"

      # 验的是**即将被打包的这个 bundle**，不是提交上去的那份——中间任何一次
      # 重签、改字节、拿错文件都会让两者分叉，代价是用户机器上的一次弹窗。
      # 注意喂法与 CLI 那条相反：app bundle 用 `-t exec`，裸 CLI 才用 `-t open`
      #（既有 build-darwin 的长注释记着 2026-08-13 那次踩反了的经过）。
      - name: 校验 bundle 的签名与票据
        run: |
          set -euo pipefail
          app="desktop/bin/handoff-desktop.app"
          codesign -dv --verbose=4 "$app" 2>/tmp/codesign-desktop.log || {
            echo "codesign -dv 失败：" >&2; cat /tmp/codesign-desktop.log >&2; exit 1; }
          grep -q 'Authority=Developer ID Application' /tmp/codesign-desktop.log || {
            echo "签发者不是 Developer ID Application：" >&2
            cat /tmp/codesign-desktop.log >&2; exit 1; }
          grep -Eq '^CodeDirectory .*flags=0x[0-9a-f]+\(.*runtime.*\)' /tmp/codesign-desktop.log || {
            echo "硬化运行时未开，公证会被拒：" >&2
            cat /tmp/codesign-desktop.log >&2; exit 1; }
          # 票据发布到 Apple 边缘有传播延迟，所以重试而不是一次定生死
          ok=0
          for attempt in 1 2 3; do
            if spctl -a -vvv -t exec "$app" >/tmp/spctl-desktop.log 2>&1; then ok=1; break; fi
            echo "spctl 第 ${attempt} 次未通过，20s 后重试：" >&2
            cat /tmp/spctl-desktop.log >&2
            sleep 20
          done
          [ "$ok" = 1 ] || {
            echo "公证票据在本机查不到：" >&2; cat /tmp/spctl-desktop.log >&2; exit 1; }
          grep -q 'source=Notarized Developer ID' /tmp/spctl-desktop.log || {
            echo "评估通过但来源不是 Notarized Developer ID：" >&2
            cat /tmp/spctl-desktop.log >&2; exit 1; }
          echo "bundle 签名与公证票据校验通过"

      - name: 确认工作区干净
        run: |
          set -euo pipefail
          out="$(git status --porcelain)"
          test -z "$out" || { echo "构建污染了工作区："; echo "$out"; exit 1; }

      - uses: actions/upload-artifact@v4
        with:
          name: handoff-desktop_darwin
          path: handoff-desktop_*.zip
          if-no-files-found: error
```

- [ ] **Step 2: 校验 YAML 合法**

Run: `python3 -c "import yaml; d=yaml.safe_load(open('.github/workflows/release.yml')); print('build-desktop-darwin' in d['jobs'])"`
Expected: `True`

- [ ] **Step 3: 本地用 adhoc 签名验证「顺序不自毁」**

这一步在 macOS 上跑，验证的是**签名顺序本身成不成立**（先签内层再签外层，外层不会让内层失效），不涉及 Developer ID 与公证：

```bash
set -euo pipefail
go build -o desktop/internal/embedbin/handoff .
codesign --force --sign - desktop/internal/embedbin/handoff
codesign --verify --strict desktop/internal/embedbin/handoff && echo "内层签名 OK"
cd desktop && wails3 task package GO_FLAGS="-tags embedbin" && cd ..
codesign --force --deep --sign - desktop/bin/handoff-desktop.app
codesign --verify --strict desktop/bin/handoff-desktop.app && echo "外层签名 OK"
```
Expected: 两行 OK 都打印。**不在 macOS 上执行本计划时跳过本步并在 ledger 记明跳过**。

- [ ] **Step 4: 清理并确认工作区干净**

Run: `rm -rf desktop/internal/embedbin/handoff desktop/bin internal/webui/dist && git status --porcelain`
Expected: **零输出**

- [ ] **Step 5: Commit**

```bash
git add .github/workflows/release.yml
git commit -m "ci(release): 新增 macOS 薄壳构建 job，按 spec §5.4 的签名顺序

顺序承重：签 handoff → 嵌进薄壳 → 构建 → 签+公证 bundle。释出到
~/.local/bin/ 的二进制脱离 bundle 签名覆盖，Gatekeeper 单独校验它，所以
内嵌的必须是已单独签名那份；顺序颠倒会让签名与最终字节不符。

CLI 仍 CGO_ENABLED=0（开 CGO 会带上构建机的最低系统版本约束），薄壳自身
开 CGO。用 package 任务而非 build：build 只出裸二进制，.app 要靠 package
组装，只跑 build 会拿到陈旧的 bundle。"
```

---

## Task 4: 资产接进 release job

**Files:**
- Modify: `.github/workflows/release.yml`（`release` job）

**Interfaces:**
- Consumes: Task 2/3 产出的 artifact `handoff-desktop_linux` / `handoff-desktop_darwin`
- Produces: 发布页上同时含 CLI 与薄壳资产，且 `checksums.txt` 覆盖两者

**为什么单列**：`release` job 现在用 `handoff_*` 通配收集资产（`:331` 的 `sha256sum`、`:357-361` 的 `gh release create`）。`handoff-desktop_*` **不匹配** `handoff_*`（前缀后是 `-` 不是 `_`），所以薄壳资产既不会被误抓、也**不会被自动收进去**——必须显式加，否则校验和缺一半。

- [ ] **Step 1: 确认既有通配确实不覆盖薄壳资产**

Run:
```bash
mkdir -p /tmp/globcheck && cd /tmp/globcheck && rm -f handoff*
touch handoff_v1_darwin_arm64.tar.gz handoff-desktop_v1_darwin_arm64.zip
echo "handoff_* 匹配到："; ls handoff_* 2>/dev/null
```
Expected: 只列出 `handoff_v1_darwin_arm64.tar.gz`；`handoff-desktop_*` **不在其中**

- [ ] **Step 2: 先把两个新 job 接进 `release` 的 `needs`（漏了这条，前面全白做）**

> **2026-08-18 协调者基线复核补入。原计划整份漏了这一步。** `release` job 现在是
> `needs: [build-unix, build-darwin]`（`release.yml:314`）。不扩这个列表，GitHub
> Actions 就会在这两个 job 一完成时立刻起 `release`——那时薄壳 job 多半还没上传
> 完 artifact，`download-artifact` 拿不到薄壳资产，紧接着 Step 3 那条
> `sha256sum ... handoff-desktop_*` 会因为**通配匹配不到文件**直接失败，整次发布
> 挂在最后一步。而且这个失败是**时序相关**的，重跑可能就过了，属于最难查的一类。

```yaml
  release:
    needs: [build-unix, build-darwin, build-desktop-linux, build-desktop-darwin]
```

Verify: `python3 -c "import yaml; d=yaml.safe_load(open('.github/workflows/release.yml')); print(d['jobs']['release']['needs'])"`
Expected: 四个 job 名都在列表里

- [ ] **Step 3: 把薄壳资产扩进校验和与发布命令**

**下载步骤不用改**：现行 `download-artifact` 用的是 `path: dist` + `merge-multiple: true`，新 job 的 artifact 会自动落进 `dist/`。要改的只有两处通配：

```yaml
      # 算 checksums 那一步（现行是 cd dist 之后 sha256sum handoff_*.tar.gz handoff_*.zip）
      # 薄壳资产用 handoff-desktop_ 前缀，不匹配 handoff_*，必须显式列出，
      # 否则会漏出 checksums（install.sh 侧则相反：正因为前缀不同，它精确
      # 拼出的 handoff_<tag>_<平台> 不会误抓到薄壳包）
          sha256sum handoff_*.tar.gz handoff_*.zip handoff-desktop_* > checksums.txt
```

```yaml
      # gh release create 那一步，资产列表补上薄壳
            dist/handoff_*.tar.gz dist/handoff_*.zip \
            dist/handoff-desktop_* dist/checksums.txt
```

> **不要动 checksums 的计算时机。** 现行注释写明它必须在签名之后算（签名会改字节），对下载下来的成品统一算天然满足这个顺序——不要把它挪进构建 job。

- [ ] **Step 4: 补一条回归，钉死「install.sh 不会误抓薄壳包」**

CLI 侧的资产名由 `internal/release.AssetName`（`client.go:83`）拼出，`install.sh:122` 与 `install.ps1` 拼的是同一格式。给 Go 侧补一条用例：

```go
// 薄壳资产用 handoff-desktop_ 前缀发布。这条钉死 CLI 侧拼出的资产名
// 永远不会撞上薄壳包——两边都改名才会失效，那时这条会红。
//
// 为什么值得单列一条：AssetName 的 doc 里已经写明「格式必须与 release.yml
// 的产出逐字一致，不一致的症状是查得到版本但下不到东西」。加了薄壳资产
// 之后，这个格式又多了一个必须避开的邻居。
func TestAssetNameNeverMatchesDesktopAsset(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		name := AssetName("v1.2.3", goos, "arm64")
		if strings.HasPrefix(name, "handoff-desktop") {
			t.Fatalf("CLI 资产名撞上薄壳前缀: %s", name)
		}
		if !strings.HasPrefix(name, "handoff_") {
			t.Fatalf("CLI 资产名不再以 handoff_ 开头，install.sh 会取不到: %s", name)
		}
	}
}
```

- [ ] **Step 5: 跑测试**

Run: `go test ./internal/release/ -count=1`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add .github/workflows/release.yml internal/release
git commit -m "ci(release): 薄壳资产接进发布与校验和

handoff-desktop_* 不匹配既有的 handoff_* 通配，所以既不会被 install.sh
与自更新误抓（那正是选这个前缀的理由），也不会被自动收进 checksums 与
发布资产——必须显式列出。补一条用例钉死 CLI 资产名不会撞上薄壳前缀。

release 的 needs 同时扩到四个 job：不扩的话它会在薄壳 artifact 上传完成
之前就起跑，checksums 那步的通配匹配不到文件而失败，且失败是时序相关的。"
```

---

## Task 5: 端到端与未验项落账

**Files:**
- Create: `docs/ledger-w5b3.md`

- [ ] **Step 1: 全量测试与格式**

Run: `go test ./... -count=1 && gofmt -l . && cd desktop && gofmt -l .`
Expected: 测试全绿；两个模块的 `gofmt -l` 均**零输出**

- [ ] **Step 2: 确认工作区干净**

Run: `git status --porcelain`
Expected: **零输出**

- [ ] **Step 3: 写 ledger，把未验项**逐条**记明**

`docs/ledger-w5b3.md` 必须含以下四条未验项，各写清「为什么没验」与「什么条件下能验」：

| 项 | 状态 | 原因 |
|---|---|---|
| P1-linux（运行） | ⛔ 未验 | 无带图形界面的真 Linux。**产物照出，但不得声称 Linux 可用** |
| P3（AppImage 跨发行版） | ⛔ 未验 | 同上 |
| P2（Gatekeeper 放行已释出的二进制） | ⛔ 未验 | 需真向 Apple 提交公证，属对外可见操作；按用户决定，流水线写好但不触发真发布 |
| P1-win（运行） | ⛔ 未验 | 无 Windows 机器；Windows runner 按本计划范围**不接进流水线** |

**P2 那一行必须写清具体机制，不能只写「未验」**（照抄 Task 3 开头那个说明块的结论）：
内嵌的 CLI 是 `//go:embed handoff` 进薄壳可执行文件的**字节块**，不是 bundle 里的文件，
所以 notary 服务这次提交**看不见它**、不会为它登记票据；它被释出到 `~/.local/bin/handoff`
后能否过 Gatekeeper，取决于它的 cdhash 是否与 `build-darwin` 那条独立公证过的 CLI 资产
一致（同 `-trimpath`、同 ldflags、同签名身份，理论上一致，**从未实测**）。
P2 要验的就是这一条，验法是发版后在一台干净 mac 上装 `.app`、让它释出 CLI、
再对释出的那份跑 `spctl -a -vvv -t open --context context:primary-signature ~/.local/bin/handoff`。

**「流水线语法正确」不等于「流水线跑得通」**——本计划的所有 CI 改动都未经真实 runner 执行，这一条也要写进 ledger。

- [ ] **Step 4: Commit**

```bash
git add docs/ledger-w5b3.md
git commit -m "docs(ledger): W5b-3 构建链进度与四条未验项"
```
