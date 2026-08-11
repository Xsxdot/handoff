#!/usr/bin/env bash
# install.sh 的单元测试：只测能纯函数化的部分（平台归一、tag 解析、sha256）。
#
# 用法：bash install_test.sh
# 全通过时静默退出 0；有失败时逐条打印期望/实得并退 1。
#
# 边界：不测下载与安装本身——那需要真实 Release，属真机验证（P2）。
set -uo pipefail

HANDOFF_INSTALL_LIB=1 . "$(dirname "$0")/install.sh"

fails=0

# check <说明> <期望> <实得>
check() {
  if [ "$2" != "$3" ]; then
    printf 'FAIL  %s\n      期望 %s\n      实得 %s\n' "$1" "$2" "$3" >&2
    fails=$((fails + 1))
  fi
}

# with_uname <系统> <架构> <命令...>：在被替身的 uname 下执行命令。
# bash 是动态作用域，被调命令里的 uname 会命中这里定义的函数。
with_uname() {
  local s="$1" m="$2"
  shift 2
  uname() { case "$1" in -s) printf '%s' "$s" ;; -m) printf '%s' "$m" ;; esac; }
  "$@"
  unset -f uname
}

# with_curl_url <重定向后的地址> <命令...>：替身 curl，只回显给定地址。
with_curl_url() {
  local u="$1"
  shift
  curl() { printf '%s' "$u"; }
  "$@"
  unset -f curl
}

# 四个受支持平台都要归一正确
check "darwin arm64"  "darwin_arm64" "$(with_uname Darwin arm64 detect_platform)"
check "darwin x86_64" "darwin_amd64" "$(with_uname Darwin x86_64 detect_platform)"
check "linux aarch64" "linux_arm64"  "$(with_uname Linux aarch64 detect_platform)"
check "linux x86_64"  "linux_amd64"  "$(with_uname Linux x86_64 detect_platform)"

# Windows 必须被明确拒绝，且理由里要点出 B37——否则用户只会以为是漏了平台
out="$( (with_uname Windows_NT x86_64 detect_platform) 2>&1 )" && rc=0 || rc=$?
check "Windows 退出码" "1" "$rc"
case "$out" in
  *B37*) ;;
  *) printf 'FAIL  Windows 的拒绝理由应点出 backlog B37\n      实得 %s\n' "$out" >&2
     fails=$((fails + 1)) ;;
esac

# 32 位架构不在矩阵内，必须拒绝而不是装一个跑不起来的包
out="$( (with_uname Linux i686 detect_platform) 2>&1 )" && rc=0 || rc=$?
check "i686 退出码" "1" "$rc"

# tag 从 releases/latest 的重定向地址里取最后一段
check "解析 tag" "v0.1.0" \
  "$(with_curl_url 'https://github.com/Xsxdot/handoff/releases/tag/v0.1.0' latest_tag)"

# 仓库还没有任何 release 时，GitHub 会重定向到 .../releases（末段不是 vX.Y.Z）。
# 此时必须报错，而不是去下载一个名为 handoff_releases_... 的不存在资产
out="$( (with_curl_url 'https://github.com/Xsxdot/handoff/releases' latest_tag) 2>&1 )" && rc=0 || rc=$?
check "无 release 时退出码" "1" "$rc"

# sha256_of 要在 Linux（sha256sum）与 macOS（shasum）上都得出同一个值
tmpf="$(mktemp)"
printf 'abc' > "$tmpf"
check "sha256(abc)" \
  "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" \
  "$(sha256_of "$tmpf")"
rm -f "$tmpf"

if [ "$fails" -ne 0 ]; then
  printf '\n%d 项失败\n' "$fails" >&2
  exit 1
fi
