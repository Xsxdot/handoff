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
| `.github/workflows/release.yml`（改） | 新增 `build-desktop-linux` / `build-desktop-darwin` 两个 job；`release` job 收集薄壳资产 |
| `desktop/build/linux/nfpm/nfpm.yaml`（改） | 依赖声明从 gtk4 那套换成 gtk3 那套 |
| `docs/ledger-w5b3.md`（新） | 进度与未验项落账 |

---

## Task 1: 修正 nfpm 的依赖声明

**Files:**
- Modify: `desktop/build/linux/nfpm/nfpm.yaml`

**Interfaces:**
- Consumes: 无
- Produces: 一份声明 gtk3 运行时依赖的 nfpm 配置，供 Task 2 的 deb 打包使用

**为什么单列**：这是一处**现存的错误配置**，不是新增功能。它独立于流水线改动，可以单独验证（`nfpm` 生成的包元数据里能直接看到依赖列表）。

> **spec §6.2 的说法需要更正**：那里写「`depends` 段目前整段被注释」。**实际不是**——`depends` 段是生效的，填的是 **gtk4 那套**（`libgtk-4-1` / `libwebkitgtk-6.0-4`，约 28-44 行），gtk3 那套躺在其下的注释块里。后果比「整段被注释」更糟：包会声明一组它根本不用的依赖，在 Ubuntu 22.04 上连装都装不上（那儿没有 `libgtk-4-1`）。

- [ ] **Step 1: 确认当前生效的是哪一套**

Run: `sed -n '20,70p' desktop/build/linux/nfpm/nfpm.yaml`
Expected: 看到生效的 `depends:` 是 `libgtk-4-1` / `libwebkitgtk-6.0-4`，且下方注释块里有 `libgtk-3-0` / `libwebkit2gtk-4.1-0`

- [ ] **Step 2: 换成 gtk3 那套**

把生效的 `depends` 与 `overrides` 段替换为注释块里给出的 gtk3 版本（注释块本身随之删除，避免留下两份互相矛盾的声明）：

```yaml
# 依赖必须与构建 tag 一致：本项目按 spec §2 锁 Ubuntu 22.04 基线，
# 薄壳用 -tags gtk3 构建，实际链的是 libwebkit2gtk-4.1 + libgtk-3
#（P1-linux 探针 ldd 实测）。声明成 gtk4 那套会让包在 22.04 上装不上，
# 而症状要等到用户机器上才出现。改构建 tag 时必须同步改这里。
depends:
  - libgtk-3-0
  - libwebkit2gtk-4.1-0
overrides:
  rpm:
    depends:
      - gtk3
      - webkit2gtk4.1
  archlinux:
    depends:
      - gtk3
      - webkit2gtk-4.1
```

- [ ] **Step 3: 确认没有残留的 gtk4 声明**

Run: `grep -n "gtk-4\|gtk4\|webkitgtk-6\|webkitgtk6" desktop/build/linux/nfpm/nfpm.yaml`
Expected: **无输出**。有输出说明还有一处没换或注释块没删干净。

- [ ] **Step 4: 确认 YAML 仍合法**

Run: `python3 -c "import yaml,sys; d=yaml.safe_load(open('desktop/build/linux/nfpm/nfpm.yaml')); print(d['depends'])"`
Expected: `['libgtk-3-0', 'libwebkit2gtk-4.1-0']`

- [ ] **Step 5: Commit**

```bash
git add desktop/build/linux/nfpm/nfpm.yaml
git commit -m "fix(desktop): nfpm 依赖改为 gtk3 那套，与构建 tag 对齐

生效的 depends 之前是 gtk4（libgtk-4-1 / libwebkitgtk-6.0-4），而薄壳按
spec §2 的 22.04 基线用 -tags gtk3 构建，实际链的是 libwebkit2gtk-4.1 +
libgtk-3。声明错的后果是包在 22.04 上装都装不上，且症状要到用户机器上
才出现。gtk3 那套本来就躺在下面的注释块里，扶正并删掉注释块，避免留下
两份互相矛盾的声明。"
```

---

## Task 2: Linux 薄壳构建 job

**Files:**
- Modify: `.github/workflows/release.yml`

**Interfaces:**
- Consumes: Task 1 修正后的 `nfpm.yaml`
- Produces: 名为 `build-desktop-linux` 的 job，产出 artifact `handoff-desktop_linux`，内含 `handoff-desktop_${TAG}_linux_amd64.AppImage` 与 `handoff-desktop_${TAG}_linux_amd64.deb`

- [ ] **Step 1: 写 job**

在 `build-darwin` job 之后插入。逐条注释都要写清「为什么」：

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
      - uses: actions/setup-node@v4
        with:
          node-version: 20

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

      - uses: actions/upload-artifact@v4
        with:
          name: handoff-desktop_linux
          path: handoff-desktop_*
```

- [ ] **Step 2: 校验 YAML 合法**

Run: `python3 -c "import yaml; d=yaml.safe_load(open('.github/workflows/release.yml')); print(list(d['jobs'].keys()))"`
Expected: 输出的 job 列表里含 `build-desktop-linux`

- [ ] **Step 3: 确认没有误碰既有 job**

Run: `git diff .github/workflows/release.yml | grep -E "^-" | grep -v "^---" | head -20`
Expected: **无输出**（本 task 只新增，不删改任何既有行）

- [ ] **Step 4: 确认 deb 里的依赖是 gtk3 那套**

本地无法跑完整 job，但可以单独验证 nfpm 配置被正确消费——这一条留到 Task 5 的真实流水线跑通后确认，此处只记账。

- [ ] **Step 5: Commit**

```bash
git add .github/workflows/release.yml
git commit -m "ci(release): 新增 Linux 薄壳构建 job

原生 runner 锁 ubuntu-22.04；装 wails3 与构建薄壳两步都带 -tags gtk3
（不带则在准备工具阶段就 No package 'gtk4' found，报错像是代码问题）。
内嵌的 handoff 由同一次流水线以 CGO_ENABLED=0 + embedweb 编出，薄壳自身
开 CGO。资产用独立前缀 handoff-desktop_，避开 install.sh 与自更新的精确
拼名。末尾有工作区干净检查。"
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
      - uses: actions/setup-node@v4
        with:
          node-version: 20
      - name: 装 wails3
        run: go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.8

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

      # 钥匙串装载：与既有 build-darwin 同款。抽成复合步骤会牵动那个已在
      # 生产用的 job，风险大于收益，因此这里重复一次（两处必须同步改）。
      - name: 装载签名证书
        run: |
          set -euo pipefail
          for v in APPLE_CERTIFICATE APPLE_CERTIFICATE_PASSWORD APPLE_SIGNING_IDENTITY; do
            eval "test -n \"\${$v:-}\"" || { echo "缺少 secret: $v" >&2; exit 1; }
          done
          keychain_path="$RUNNER_TEMP/desktop-signing.keychain-db"
          cert_path="$RUNNER_TEMP/cert.p12"
          echo "$APPLE_CERTIFICATE" | base64 --decode > "$cert_path"
          security create-keychain -p "" "$keychain_path"
          security set-keychain-settings -lut 21600 "$keychain_path"
          security unlock-keychain -p "" "$keychain_path"
          security import "$cert_path" -P "$APPLE_CERTIFICATE_PASSWORD" \
            -A -t cert -f pkcs12 -k "$keychain_path"
          # 让 codesign 能在无 UI 提示下使用私钥
          security set-key-partition-list -S apple-tool:,apple:,codesign: \
            -s -k "" "$keychain_path" >/dev/null
          security list-keychain -d user -s "$keychain_path"
          security find-identity -v -p codesigning "$keychain_path" \
            | grep -F "$APPLE_SIGNING_IDENTITY" > /dev/null || {
              echo "钥匙串里找不到 APPLE_SIGNING_IDENTITY" >&2; exit 1; }

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

      # 第三步：签 + 公证 bundle。--deep 是必要的：内嵌的 handoff 在
      # bundle 里，外层签名要覆盖到它。
      - name: 签名并公证 bundle
        env:
          APPLE_API_KEY_CONTENT: ${{ secrets.APPLE_API_KEY_CONTENT }}
        run: |
          set -euo pipefail
          app="desktop/bin/handoff-desktop.app"
          codesign --force --deep --options runtime --timestamp \
            --sign "$APPLE_SIGNING_IDENTITY" "$app"
          codesign --verify --strict --verbose=2 "$app"
          # App Store Connect API Key 只能以文件路径传给 notarytool
          key_path="$RUNNER_TEMP/notary.p8"
          printf '%s\n' "$APPLE_API_KEY_CONTENT" > "$key_path"
          grep -q "BEGIN PRIVATE KEY" "$key_path" || {
            echo "APPLE_API_KEY_CONTENT 不像是一份 .p8 私钥" >&2; exit 1; }
          TAG="${GITHUB_REF_NAME}"
          ditto -c -k --keepParent "$app" "handoff-desktop_${TAG}_darwin_arm64.zip"
          xcrun notarytool submit "handoff-desktop_${TAG}_darwin_arm64.zip" \
            --key "$key_path" --key-id "$APPLE_API_KEY" --issuer "$APPLE_API_ISSUER" \
            --wait
          # 装订票据后重新打包，让离线机器也能通过 Gatekeeper
          xcrun stapler staple "$app"
          rm -f "handoff-desktop_${TAG}_darwin_arm64.zip"
          ditto -c -k --keepParent "$app" "handoff-desktop_${TAG}_darwin_arm64.zip"

      - name: 确认工作区干净
        run: |
          set -euo pipefail
          out="$(git status --porcelain)"
          test -z "$out" || { echo "构建污染了工作区："; echo "$out"; exit 1; }

      - uses: actions/upload-artifact@v4
        with:
          name: handoff-desktop_darwin
          path: handoff-desktop_*.zip
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

- [ ] **Step 2: 把薄壳资产扩进校验和与发布命令**

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

- [ ] **Step 3: 补一条回归，钉死「install.sh 不会误抓薄壳包」**

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

- [ ] **Step 4: 跑测试**

Run: `go test ./internal/release/ -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add .github/workflows/release.yml internal/release
git commit -m "ci(release): 薄壳资产接进发布与校验和

handoff-desktop_* 不匹配既有的 handoff_* 通配，所以既不会被 install.sh
与自更新误抓（那正是选这个前缀的理由），也不会被自动收进 checksums 与
发布资产——必须显式列出。补一条用例钉死 CLI 资产名不会撞上薄壳前缀。"
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

**「流水线语法正确」不等于「流水线跑得通」**——本计划的所有 CI 改动都未经真实 runner 执行，这一条也要写进 ledger。

- [ ] **Step 4: Commit**

```bash
git add docs/ledger-w5b3.md
git commit -m "docs(ledger): W5b-3 构建链进度与四条未验项"
```
