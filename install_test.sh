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

# main 的完整成功路径：桩掉 curl 之后不需要真实 Release 也能跑通，因此这里能覆盖
# 「装完之后」的两件事——退出码和清理。
#
# why（这条测试的由来）：修复前 tmp 是 main 的 local，而 EXIT trap 在 main 返回
# 之后才展开它，set -u 当场判未绑定：安装明明成功，退出码却是 1，临时目录也永远
# 留着。两个症状都在正常输出之后才出现，肉眼极易放过——必须由测试来盯。
probe_dir="$(mktemp -d)"
fixture="${probe_dir}/fixture"
mkdir -p "$fixture"
printf '#!/bin/sh\nexit 0\n' > "${fixture}/handoff"
( cd "$fixture" && tar czf handoff_v0.1.0_darwin_arm64.tar.gz handoff &&
  sha256_of handoff_v0.1.0_darwin_arm64.tar.gz | \
    awk '{print $1 "  handoff_v0.1.0_darwin_arm64.tar.gz"}' > checksums.txt )

# TMPDIR 指向一个空目录：main 里的 mktemp -d 会落在它下面，跑完数一下就知道清没清
mkdir -p "${probe_dir}/tmp"
(
  export TMPDIR="${probe_dir}/tmp"
  # 必须直接改 INSTALL_DIR，不能 export HANDOFF_INSTALL_DIR：INSTALL_DIR 在
  # install.sh 被 source 的那一刻（本文件第 10 行）就已求值定死了，此处再设环境
  # 变量完全不起作用——main 会把桩二进制装进真实的 ~/.local/bin，覆盖用户在用的
  # handoff。这不是假设：本条测试第一版就是这么写的，当场把本机 CLI 写坏了
  INSTALL_DIR="${probe_dir}/bin"
  # 上面那条一旦被后人改回去，这里当场拦下，绝不让测试碰 probe_dir 以外的任何路径
  case "$INSTALL_DIR" in
    "${probe_dir}"/*) ;;
    *) printf '安装目录未被隔离到探针目录（实得 %s），拒绝执行 main\n' "$INSTALL_DIR" >&2
       exit 99 ;;
  esac
  latest_tag() { printf 'v0.1.0'; }
  # 替身 curl：忽略地址，按 -o 的目标文件名从 fixture 取件
  curl() {
    local dst=""
    while [ $# -gt 0 ]; do
      [ "$1" = "-o" ] && { dst="$2"; shift; }
      shift
    done
    [ -n "$dst" ] && cp "${fixture}/$(basename "$dst")" "$dst"
  }
  with_uname Darwin arm64 main
) > /dev/null 2>&1 && rc=0 || rc=$?
check "成功路径退出码" "0" "$rc"
check "装出的二进制存在" "yes" "$([ -x "${probe_dir}/bin/handoff" ] && echo yes || echo no)"
check "临时目录已清理" "0" "$(find "${probe_dir}/tmp" -mindepth 1 -maxdepth 1 | wc -l | tr -d ' ')"
rm -rf "$probe_dir"

if [ "$fails" -ne 0 ]; then
  printf '\n%d 项失败\n' "$fails" >&2
  exit 1
fi
