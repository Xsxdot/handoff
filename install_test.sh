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

# make_probe <桩 handoff 的退出码>：搭一个自足的安装探针目录并回显它的路径。
#
# 目录里有：fixture/（假资产与 checksums）、stub/（替身 uname 与 curl）、
# tmp/（喂给 install.sh 的 TMPDIR）、bin/（安装落点，由 install.sh 自己建）。
#
# 为什么替身做成 PATH 上的可执行文件而不是 shell 函数：install.sh 要以**真子
# 进程**跑（EXIT trap 只在脚本进程退出时展开），函数替身跨不过进程边界。
make_probe() {
  local exit_code="$1" dir fixture stub
  dir="$(mktemp -d)"
  fixture="${dir}/fixture"
  stub="${dir}/stub"
  mkdir -p "$fixture" "$stub" "${dir}/tmp"
  printf '#!/bin/sh\nexit %s\n' "$exit_code" > "${fixture}/handoff"
  ( cd "$fixture" && tar czf handoff_v0.1.0_darwin_arm64.tar.gz handoff &&
    sha256_of handoff_v0.1.0_darwin_arm64.tar.gz | \
      awk '{print $1 "  handoff_v0.1.0_darwin_arm64.tar.gz"}' > checksums.txt )

  # 替身 uname：把平台钉死成 darwin/arm64，与 fixture 里的资产名对上
  cat > "${stub}/uname" <<'STUB'
#!/bin/sh
case "$1" in -s) printf 'Darwin' ;; -m) printf 'arm64' ;; esac
STUB
  # 替身 curl：带 -I 的那次（latest_tag 解析重定向）回一个末段是 v0.1.0 的
  # 地址；带 -o 的那两次按目标文件名从 fixture 取件。判 -I 必须排在用 dst
  # 之前——latest_tag 那次同时带着 -o /dev/null
  cat > "${stub}/curl" <<'STUB'
#!/bin/sh
dst=""; head=0
while [ $# -gt 0 ]; do
  case "$1" in
    -o) dst="$2"; shift ;;
    -*I*) head=1 ;;
  esac
  shift
done
if [ "$head" -eq 1 ]; then
  printf 'https://github.com/Xsxdot/handoff/releases/tag/v0.1.0'
  exit 0
fi
[ -n "$dst" ] || exit 1
# 记下 install.sh 让我们往哪儿写：这就是它 mktemp 出来的临时目录。
# 测试据此断言那个目录真的落在 TMPDIR 里——否则「已清理」会退化成
# 「本来就没在这儿建过」的假绿（BSD mktemp 无模板时正是如此）
dirname "$dst" > "${HANDOFF_TEST_WITNESS}"
cp "${HANDOFF_TEST_FIXTURE}/$(basename "$dst")" "$dst"
STUB
  chmod +x "${stub}/uname" "${stub}/curl"
  printf '%s' "$dir"
}

# run_install <探针目录> <stdout 去处> <stderr 去处>：以真子进程跑 install.sh，
# 回显它的退出码。安装目录经环境变量隔离到探针目录内——子进程跑法下这是可靠的，
# INSTALL_DIR 在子进程 source install.sh 的那一刻才求值。
run_install() {
  local dir="$1" out="$2" err="$3" rc=0
  env PATH="${dir}/stub:${PATH}" \
      TMPDIR="${dir}/tmp" \
      HANDOFF_INSTALL_DIR="${dir}/bin" \
      HANDOFF_TEST_FIXTURE="${dir}/fixture" \
      HANDOFF_TEST_WITNESS="${dir}/witness" \
      bash "$(dirname "$0")/install.sh" > "$out" 2> "$err" || rc=$?
  printf '%s' "$rc"
}

# 四个受支持平台都要归一正确
check "darwin arm64"  "darwin_arm64" "$(with_uname Darwin arm64 detect_platform)"
check "darwin x86_64" "darwin_amd64" "$(with_uname Darwin x86_64 detect_platform)"
check "linux aarch64" "linux_arm64"  "$(with_uname Linux aarch64 detect_platform)"
check "linux x86_64"  "linux_amd64"  "$(with_uname Linux x86_64 detect_platform)"

# Windows 必须被明确拒绝，且理由里要给出路（install.ps1）——
# 只说「不支持」会让用户以为 Windows 根本装不了，而现在它是能装的
out="$( (with_uname Windows_NT x86_64 detect_platform) 2>&1 )" && rc=0 || rc=$?
check "Windows 退出码" "1" "$rc"
case "$out" in
  *install.ps1*) ;;
  *) printf 'FAIL  Windows 的拒绝理由应指向 install.ps1\n      实得 %s\n' "$out" >&2
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

# main 的完整成功路径：以**真子进程**跑 install.sh。
#
# why（这条测试的由来）：修复前 tmp 是 main 的 local，而 EXIT trap 在 main 返回
# 之后才展开它，set -u 当场判未绑定：安装明明成功，退出码却是 1，临时目录也永远
# 留着。两个症状都在正常输出之后才出现，肉眼极易放过——必须由测试来盯。
#
# why 必须是真子进程：EXIT trap 只在**脚本进程**退出时展开，在 `( ... main )`
# 子 shell 里调 main 根本不触发它。叠上 BSD mktemp 无模板时忽略 TMPDIR，探针
# 目录下压根不会有东西——两件事合起来让「临时目录已清理」在 macOS 上成了一条
# 恒真的假绿，08-13 才由 ubuntu 上的 GNU mktemp（认 TMPDIR）捅破。
probe_dir="$(make_probe 0)"
rc="$(run_install "$probe_dir" /dev/null /dev/null)"
check "成功路径退出码" "0" "$rc"
check "装出的二进制存在" "yes" "$([ -x "${probe_dir}/bin/handoff" ] && echo yes || echo no)"
# 先证「它真的建在 TMPDIR 里」，再证「跑完不剩东西」。少了前一条，后一条在
# 忽略 TMPDIR 的平台上会变成恒真
witness="$(cat "${probe_dir}/witness" 2>/dev/null || printf '(没记到)')"
case "$witness" in
  "${probe_dir}/tmp"/*) ;;
  *) printf 'FAIL  临时目录应建在 TMPDIR 下\n      期望 %s/tmp/* \n      实得 %s\n' \
       "$probe_dir" "$witness" >&2
     fails=$((fails + 1)) ;;
esac
check "临时目录已清理" "0" "$(find "${probe_dir}/tmp" -mindepth 1 -maxdepth 1 | wc -l | tr -d ' ')"
rm -rf "$probe_dir"

# 桩二进制 exit 3（skill install 失败）：安装主流程**仍必须退 0**，且 stderr
# 要明说「skill 没装上、之后可手动补」。二进制已经装好了，skill 是附属动作，
# 让一个附属动作把整条安装拖成失败，用户会以为 handoff 没装上；静默失败则会让
# 用户拿到一份旧 skill 而毫不知情——两个症状都不许出现。
#
# 同样走真子进程：这条断言盯的是**脚本的退出码**，而 EXIT trap 正是能把它顶成
# 非零的那个东西（见上一块的由来），在子 shell 里调 main 就恰好绕开了它。
probe_dir="$(make_probe 3)"
stderr_log="${probe_dir}/stderr.log"
rc="$(run_install "$probe_dir" /dev/null "$stderr_log")"
check "skill 安装失败时安装仍退 0" "0" "$rc"
err="$(cat "$stderr_log")"
case "$err" in
  *"skill 安装失败"*) ;;
  *) printf 'FAIL  stderr 应提示 skill 安装失败\n      实得 %s\n' "$err" >&2
     fails=$((fails + 1)) ;;
esac
rm -rf "$probe_dir"

# 装完必须把下一步指向 handoff init，并说清不托管的后果。
#
# why 值得一条断言：这是本脚本唯一直接影响用户下一步动作的产物。B71 现场那台
# 机器的 agentd 从来没被托管过——因为安装脚本从头到尾没提过 handoff init，
# 而托管提示躺在 init 的最后一行，用户要先知道该跑 init 才看得到它。
out="$(print_next_steps 2>&1)"
case "$out" in
  *"handoff init"*) ;;
  *) printf 'FAIL  下一步提示必须点名 handoff init\n      实得 %s\n' "$out" >&2
     fails=$((fails + 1)) ;;
esac
case "$out" in
  *重启*) ;;
  *) printf 'FAIL  下一步提示必须说清不托管的后果（重启后不会自己回来）\n      实得 %s\n' "$out" >&2
     fails=$((fails + 1)) ;;
esac

if [ "$fails" -ne 0 ]; then
  printf '\n%d 项失败\n' "$fails" >&2
  exit 1
fi
