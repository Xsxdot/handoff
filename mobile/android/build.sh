#!/usr/bin/env bash
# mobile/android/build.sh —— Android 壳的一键构建：先核心（gomobile AAR），再壳（Gradle APK）。
#
# 职责：把「核心构建先于壳」这条顺序写死，供 headless 验收与协调者复现。
# 边界：不修改任何被跟踪文件；核心产物落 dist/mobile/（gitignored）；不跑真机。
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$HERE/../.." && pwd)"

# 1) 核心产物（唯一合法来源）
"$ROOT/mobile/build.sh" android

# 2) 壳 APK
cd "$HERE"
./gradlew :app:assembleDebug
