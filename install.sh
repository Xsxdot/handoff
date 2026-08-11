#!/usr/bin/env bash
# handoff 一行安装脚本。
#
# 用法：curl -fsSL https://handoff.gosuper.dev/install | bash
#
# 职责：
#   - 探测平台，从 GitHub Release 拉对应资产，校验 sha256，装到 ~/.local/bin
#
# 边界：
#   - 只在「本机还没有 handoff」时用一次；后续换版走 handoff upgrade / agentd 自更新
#   - 不写服务单元、不改用户的 shell rc 文件、不 sudo
#   - 不支持 Windows：agentd 依赖的进程承载层 Windows 实现尚未完成（backlog B37）
#
# 环境变量：
#   HANDOFF_INSTALL_DIR  覆盖安装目录（默认 ~/.local/bin）
#   HANDOFF_INSTALL_LIB  设为 1 时只加载函数不执行主流程（供 install_test.sh 用）
set -euo pipefail

REPO="Xsxdot/handoff"
INSTALL_DIR="${HANDOFF_INSTALL_DIR:-$HOME/.local/bin}"

# log 输出到 stderr：stdout 留给可能被管道消费的内容，诊断信息不该混进去。
log() { printf '%s\n' "$*" >&2; }

# die 打印失败原因后退出。
#
# 每个失败分支都必须经它退出——脚本挂掉时用户能看到的只有这一行，
# 缺上下文的「安装失败」等于让用户去猜网络、权限还是平台。
die() {
  printf 'handoff 安装失败：%s\n' "$*" >&2
  exit 1
}

# detect_platform 把 uname 输出归一成 Release 资产用的 <os>_<arch>。
#
# 返回（stdout）：形如 darwin_arm64
#
# 注意：不在矩阵内的平台一律 die 并说明原因。静默装一个跑不起来的二进制，
# 比当场报错糟得多——症状会推迟到 agentd 启动时才出现，且看不出根因。
detect_platform() {
  local os arch
  case "$(uname -s)" in
    Darwin) os=darwin ;;
    Linux) os=linux ;;
    MINGW* | MSYS* | CYGWIN* | Windows_NT)
      die "暂不支持 Windows：agentd 依赖的进程承载层 Windows 实现尚未完成（backlog B37）" ;;
    *) die "不支持的系统 $(uname -s)（仅 Darwin/Linux）" ;;
  esac
  case "$(uname -m)" in
    x86_64 | amd64) arch=amd64 ;;
    arm64 | aarch64) arch=arm64 ;;
    *) die "不支持的架构 $(uname -m)（仅 amd64/arm64）" ;;
  esac
  printf '%s_%s' "$os" "$arch"
}

# latest_tag 解析 releases/latest 的重定向，取最新 tag。
#
# 返回（stdout）：形如 v0.1.0
#
# why（不打 api.github.com）：匿名 API 限流 60 次/小时/IP，安装这条路径不该
# 被限流影响。重定向没有限流。
latest_tag() {
  local url tag
  url="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/${REPO}/releases/latest")" ||
    die "取最新版本失败：连不上 github.com"
  tag="${url##*/}"
  case "$tag" in
    v*) ;;
    # 仓库一个 release 都没有时，GitHub 重定向到 .../releases，末段不是版本号
    *) die "取最新版本失败：${REPO} 还没有任何 release（重定向到 ${url}）" ;;
  esac
  printf '%s' "$tag"
}

# sha256_of 算文件的 sha256。
#
# 参数：$1 文件路径
# 返回（stdout）：64 位小写 hex
#
# 两套实现：Linux 有 sha256sum，macOS 基础系统只有 shasum。
sha256_of() {
  if command -v sha256sum > /dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

# main 是安装主流程。
main() {
  command -v curl > /dev/null 2>&1 || die "需要 curl，请先安装"
  command -v tar > /dev/null 2>&1 || die "需要 tar，请先安装"

  local platform tag tarball tmp want got
  platform="$(detect_platform)"
  tag="$(latest_tag)"
  tarball="handoff_${tag}_${platform}.tar.gz"

  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT

  log "handoff ${tag}  ${platform}"

  curl -fsSL -o "${tmp}/${tarball}" \
    "https://github.com/${REPO}/releases/download/${tag}/${tarball}" ||
    die "下载 ${tarball} 失败（该平台的资产可能不存在于 ${tag}）"
  curl -fsSL -o "${tmp}/checksums.txt" \
    "https://github.com/${REPO}/releases/download/${tag}/checksums.txt" ||
    die "下载 checksums.txt 失败"

  # checksums.txt 每行是 "<sha>  <裸文件名>"；sha256sum 的 * 前缀（二进制模式）也认
  want="$(awk -v f="$tarball" '$2 == f || $2 == "*" f {print $1}' "${tmp}/checksums.txt")"
  [ -n "$want" ] || die "checksums.txt 里没有 ${tarball} 的条目"
  got="$(sha256_of "${tmp}/${tarball}")"
  [ "$want" = "$got" ] ||
    die "校验失败：期望 ${want}，实得 ${got}。不安装，下载物已清理"

  tar xzf "${tmp}/${tarball}" -C "$tmp" || die "解包 ${tarball} 失败"
  [ -f "${tmp}/handoff" ] || die "包内没有 handoff 可执行文件"

  mkdir -p "$INSTALL_DIR" || die "创建 ${INSTALL_DIR} 失败"
  # install 而非 mv：目标已存在时原子覆盖，脚本因此可以反复重跑
  install -m 0755 "${tmp}/handoff" "${INSTALL_DIR}/handoff" ||
    die "写入 ${INSTALL_DIR} 失败（目录不可写？可用 HANDOFF_INSTALL_DIR 换一个目录）"

  log "已安装 ${INSTALL_DIR}/handoff  ${tag}"

  case ":${PATH}:" in
    *":${INSTALL_DIR}:"*) ;;
    *)
      log ""
      log "注意：${INSTALL_DIR} 不在 PATH 里。把下面这行加进你的 shell 配置："
      log "  export PATH=\"${INSTALL_DIR}:\$PATH\""
      log "（本脚本不会去改你的配置文件）"
      ;;
  esac
}

# 被 install_test.sh source 时只加载函数，不执行主流程
if [ "${HANDOFF_INSTALL_LIB:-}" != "1" ]; then
  main "$@"
fi
