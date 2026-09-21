#!/usr/bin/env bash
# mobile/build.sh —— 用 gomobile 把 mobile/bind 绑定面编成 Android AAR / iOS XCFramework。
#
# 职责：给壳工程一条可复现的绑定产物构建命令，把工具链版本与两条硬约束写死在这里。
#
# 边界：
#   - 只在装了 Xcode / Android NDK 的机器上跑（darwin）；本仓 linux 开发机不执行它
#   - 不修改任何被跟踪文件；产物落 $ROOT/dist/mobile/（已被 .gitignore 的 /dist/ 覆盖）
#   - 不含协议逻辑：绑定面在 mobile/bind，核在 internal/mobilecore
#
# 工具链（gate 台账：docs/superpowers/specs/b369-contract.md §0，darwin 双端 PASS）：
#   go 1.26.1 + gomobile@v0.0.0-20260908204917-8b95e45f8d3e + Xcode 26.6 + NDK 30.0.16248370
#
# 两条硬约束（写死在下面的命令里）：
#   1. Android 显式 -androidapi 21：NDK 30 移除了 API<21 的 platform，而 gomobile
#      缺省 minsdk=16，不加会在 bind 时报 "unsupported API version 16"。
#   2. go.mod 带 tool golang.org/x/mobile/cmd/gobind：新版 gomobile 要求
#      golang.org/x/mobile 在本模块依赖图内，否则报 "missing golang.org/x/mobile dependency"。
set -euo pipefail

GOMOBILE_VERSION="v0.0.0-20260908204917-8b95e45f8d3e"
NDK_VERSION="30.0.16248370"

MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$MODULE_DIR/.." && pwd)"
OUT_DIR="$ROOT/dist/mobile"

usage() {
  printf '用法：%s android|ios|all\n' "$0" >&2
}

target="${1:-}"
case "$target" in
  android|ios|all) ;;
  -h|--help|"")
    usage
    exit 0
    ;;
  *)
    printf '未知目标：%s\n' "$target" >&2
    usage
    exit 2
    ;;
esac

command -v gomobile >/dev/null 2>&1 || {
  printf '找不到 gomobile；安装：go install golang.org/x/mobile/cmd/gomobile@%s\n' "$GOMOBILE_VERSION" >&2
  exit 1
}
command -v gobind >/dev/null 2>&1 || {
  printf '找不到 gobind；安装：go install golang.org/x/mobile/cmd/gobind@%s（或先跑 gomobile init）\n' "$GOMOBILE_VERSION" >&2
  exit 1
}
mkdir -p "$OUT_DIR"

build_android() {
  if [ -z "${ANDROID_NDK_HOME:-}" ]; then
    printf 'Android 构建需要 ANDROID_NDK_HOME（例：<sdk>/ndk/%s）\n' "$NDK_VERSION" >&2
    exit 1
  fi
  printf '==> Android AAR（-androidapi 21）\n' >&2
  ( cd "$MODULE_DIR" && gomobile bind -target=android -androidapi 21 \
      -o "$OUT_DIR/handoff-mobile.aar" ./bind )
}

build_ios() {
  printf '==> iOS XCFramework\n' >&2
  ( cd "$MODULE_DIR" && gomobile bind -target=ios \
      -o "$OUT_DIR/handoff-mobile.xcframework" ./bind )
}

case "$target" in
  android) build_android ;;
  ios) build_ios ;;
  all)
    build_android
    build_ios
    ;;
esac

printf '==> 产物已写入 %s\n' "$OUT_DIR" >&2
