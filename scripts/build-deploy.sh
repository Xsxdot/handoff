#!/usr/bin/env bash
# 构建一个**版本戳可信**的部署二进制（内嵌前端产物）。
#
# 职责：按当前工作树（而不是主工作树）的真实 HEAD 与脏状态注入版本信息；
#       构建前端并经 embedweb 标签嵌进二进制，交叉编译出可直接部署到执行机的
#       完整产物——部署机不用再另起一份前端静态文件。
# 边界：只构建，不上传、不停服务、不换版——换版是操作者的动作，见 README。
#       不修改工作区里的任何被跟踪文件（internal/webui/dist 与 /dist/ 都在
#       .gitignore 里，前端产物落进去不算弄脏工作区）。
#
# 为什么需要这个脚本（B146）：`go build` 的 VCS 自动戳在 **linked git
# worktree** 里读的是**主工作树**的 HEAD 与脏状态，不是当前 worktree 的。
# 同一份源码的对照实测：
#
#   linked worktree（实为 85c1e2322、零脏文件） → vcs.revision=c32a1f8b1998, modified=true
#   独立克隆（同一提交）                        → vcs.revision=85c1e2322a08, modified=false
#
# 前者恰好是主工作树当时的状态。这个仓库几乎所有开发都在 .claude/worktrees/*
# 里做，所以「戳是错的」是常态而非例外——而 handoff 的 version / status /
# upgrade 三处都读它，部署上去的 agentd 会自报一个不对应任何提交的版本，
# 还可能凭空带上「带未提交改动」。
#
# 用法：
#   scripts/build-deploy.sh                      # 构建本机平台
#   scripts/build-deploy.sh windows/amd64        # 交叉编译
#   OUT=/tmp/handoff.exe scripts/build-deploy.sh windows/amd64
#   scripts/build-deploy.sh --pack <输出目录> <os/arch> [<os/arch>...]   # 打包模式（B462）
#
# 打包模式：每个平台出一个 release 形态的包 + 一份 checksums.txt，供
# `handoff upgrade --from <输出目录>` 推给全队换版。
#   - 包名 handoff_<tag>_<goos>_<goarch>.tar.gz（Windows 为 .zip），包根只有
#     handoff（Windows 为 handoff.exe）；tag = 当前 HEAD 前 12 位（与裸构建同一注入规则）。
#     这是与 .github/workflows/release.yml、internal/release.AssetName/archiveExt、
#     install.sh、install.ps1 同一个契约，改一处必须处处同步。
#   - checksums.txt 是 sha256sum 两空格格式（无 sha256sum 时用 shasum -a 256，同格式）。
#   - 拒绝脏树：已跟踪改动、未跟踪的 *.go、web/ 下的未跟踪文件，任一存在即拒绝——
#     部署物必须对应一个提交（其余未跟踪文件不进产物，不拦）。拒绝在构建前端之前发生。
#   - 输出目录必须不存在或为空（留着旧 tag 的包会被 upgrade --from 的「多个 tag」挡住）。
#   - 参数错（缺目录、缺平台、平台不是 os/arch、目录非空）退出 2；脏树退出 1。
#   - 末行打印下一步要敲的 handoff upgrade --from <绝对目录>。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

B=github.com/Xsxdot/handoff/internal/buildinfo

# build_frontend 构建前端并拷进 internal/webui/dist/（go:embed 只认这个路径）。
build_frontend() {
  echo "==> 构建前端"
  cd "$ROOT/web"
  # node_modules 在就直接增量构建（npm ci 会全量重装，部署构建要快）；
  # 缺了才退回 ci 保证一次装齐
  if [ ! -d node_modules ]; then
    npm ci
  fi
  npm run build
  [ -f "$ROOT/web/dist/index.html" ] || { echo "前端产物缺 index.html" >&2; exit 1; }

  echo "==> 把产物拷进 internal/webui/dist/"
  # embed.go 的 go:embed 只认这个路径；它在 .gitignore 里，拷进去不算脏工作区
  rm -rf "$ROOT/internal/webui/dist"
  cp -R "$ROOT/web/dist" "$ROOT/internal/webui/dist"
  cd "$ROOT"
}

# check_injected <产物> 确认注入的提交号真的进了产物（判据见调用处注释）。
check_injected() {
  local out="$1"
  echo "==> 自检：注入的提交号必须真的进了产物"
  # 判据只能是「产物里找不找得到这个 sha」：
  #   - go version -m 读不到 -ldflags——Go 刻意不记它（可能含密钥），实测确认；
  #   - 交叉编译的产物在本机跑不起来，问不了它自己。
  # 注入的是字符串常量，落在数据段，grep -a 找得到。自动戳里的 sha 是**错的那个**
  # （主工作树的），所以找到当前工作树的 sha 才说明注入生效。
  if ! grep -aq -- "$REV" "$out"; then
    echo "版本注入自检失败：产物里找不到当前工作树的提交号 $REV" >&2
    echo "自动戳（很可能指向主工作树）：" >&2
    go version -m "$out" 2>/dev/null | grep -E "vcs\.(revision|modified)" >&2 || true
    exit 1
  fi
  # 字段形态是 `build<TAB>vcs.revision=<sha>`，等号连着，不能按空白切第 3 列
  # 无 vcs.revision 行（如本机 worktree 构建）时 grep 无命中返回 1，set -o pipefail
  # 下会让命令替换赋值直接终止脚本；末尾 || true 把「无自动戳」降级成空值，
  # 走下面既有的 [ -n "$AUTO_REV" ] 提示分支，不打断构建收尾。
  local auto_rev
  auto_rev="$(go version -m "$out" 2>/dev/null | grep -o 'vcs\.revision=[0-9a-f]*' | cut -d= -f2 || true)"
  if [ -n "$auto_rev" ] && [ "$auto_rev" != "$REV" ]; then
    echo "提示：Go 自动戳是 ${auto_rev:0:12}（主工作树），已被注入值 ${REV:0:12} 覆盖"
  fi
}

# check_embedded 确认前端产物真的嵌入。
check_embedded() {
  echo "==> 自检：前端产物必须真的嵌入"
  # go:embed 一个只有空壳的 dist 也能编译过，光「编译过了」不代表前端在里面；
  # embedweb 下的 webui 测试就是这道门（与 release 流水线同一判据）
  CGO_ENABLED=0 go test -tags embedweb ./internal/webui/...
}

# pack_mode <输出目录> <os/arch>... 是打包模式的全部流程（见文件头「打包模式」）。
pack_mode() {
  if [ $# -lt 2 ]; then
    echo "用法：scripts/build-deploy.sh --pack <输出目录> <os/arch> [<os/arch>...]（至少一个平台）" >&2
    exit 2
  fi
  local dir="$1"
  shift
  local plat seen=" "
  for plat in "$@"; do
    if [ "${plat%%/*}" = "$plat" ] || [ -z "${plat##*/}" ] || [ -z "${plat%%/*}" ]; then
      echo "平台参数要写成 os/arch，例如 linux/amd64（收到：$plat）" >&2
      exit 2
    fi
    case "$seen" in *" $plat "*)
      echo "平台重复：$plat" >&2
      exit 2
      ;;
    esac
    seen="$seen$plat "
  done
  if [ -e "$dir" ] && { [ ! -d "$dir" ] || [ -n "$(ls -A "$dir")" ]; }; then
    echo "输出目录必须不存在或为空：$dir（里面留着旧包会被 upgrade --from 的「多个 tag」拒绝）" >&2
    exit 2
  fi

  # 脏树判据（在构建前端之前，拒绝要秒回）：已跟踪改动与 STAMP 同一条命令；
  # 未跟踪的 .go 会被编进二进制，web/ 下的未跟踪文件会进前端产物。
  local tracked untracked_go untracked_web
  tracked="$(git status --porcelain --untracked-files=no)"
  untracked_go="$(git ls-files --others --exclude-standard -- '*.go')"
  untracked_web="$(git ls-files --others --exclude-standard -- web/)"
  if [ -n "$tracked" ] || [ -n "$untracked_go" ] || [ -n "$untracked_web" ]; then
    echo "拒绝打包：工作树不干净，部署物必须对应一个提交。先提交或清理下列文件：" >&2
    [ -n "$tracked" ] && printf '%s\n' "$tracked" >&2
    [ -n "$untracked_go" ] && printf '?? %s\n' $untracked_go >&2
    [ -n "$untracked_web" ] && printf '?? %s\n' $untracked_web >&2
    exit 1
  fi

  REV="$(git rev-parse HEAD)"
  local commit_time stamp
  commit_time="$(git show -s --format=%cI HEAD)"
  stamp="${REV:0:12}"
  mkdir -p "$dir"
  dir="$(cd "$dir" && pwd)"
  echo "==> 打包模式：版本 $stamp  提交 ${REV:0:12} ($commit_time)  输出 $dir  平台 $*"

  build_frontend

  local stage
  stage="$(mktemp -d)"
  # shellcheck disable=SC2064 # 立即展开：trap 触发时 stage 已出作用域
  trap "rm -rf '$stage'" EXIT
  local goos goarch bin asset
  local assets=()
  for plat in "$@"; do
    goos="${plat%%/*}"
    goarch="${plat##*/}"
    bin=handoff
    [ "$goos" = windows ] && bin=handoff.exe
    rm -rf "${stage:?}/$plat"
    mkdir -p "$stage/$plat"
    echo "==> 构建 $goos/$goarch  版本 $stamp"
    # 四个注入点与裸构建一致；工作树已判干净，releaseModified 恒 false
    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build \
      -trimpath \
      -tags embedweb \
      -ldflags "-s -w -X $B.releaseVersion=$stamp -X $B.releaseRevision=$REV -X $B.releaseModified=false -X $B.releaseTime=$commit_time" \
      -o "$stage/$plat/$bin" .
    check_injected "$stage/$plat/$bin"
    if [ "$goos" = windows ]; then
      asset="handoff_${stamp}_${goos}_${goarch}.zip"
      (cd "$stage/$plat" && zip -q "$dir/$asset" "$bin")
    else
      asset="handoff_${stamp}_${goos}_${goarch}.tar.gz"
      # COPYFILE_DISABLE：防 macOS bsdtar 往包里夹带 ._handoff
      COPYFILE_DISABLE=1 tar czf "$dir/$asset" -C "$stage/$plat" "$bin"
    fi
    assets+=("$asset")
    echo "    打包 $asset"
  done

  echo "==> 生成 checksums.txt"
  # 两者都输出 `<hex>␠␠<name>`，与 release.yml 的 sha256sum 同格式（sumFor 解析）
  (
    cd "$dir"
    if command -v sha256sum >/dev/null 2>&1; then
      sha256sum "${assets[@]}" >checksums.txt
    else
      shasum -a 256 "${assets[@]}" >checksums.txt
    fi
  )

  check_embedded

  echo "OK：$dir（$# 个平台），版本 $stamp"
  echo
  echo "下一步（巡检后加 --now 换版；--target 只换一台）："
  echo "handoff upgrade --from $dir"
}

if [ "${1:-}" = "--pack" ]; then
  shift
  pack_mode "$@"
  exit 0
fi

TARGET="${1:-}"
if [ -n "$TARGET" ]; then
  GOOS="${TARGET%%/*}"
  GOARCH="${TARGET##*/}"
  if [ "$GOOS" = "$TARGET" ] || [ -z "$GOARCH" ]; then
    echo "平台参数要写成 os/arch，例如 windows/amd64（收到：$TARGET）" >&2
    exit 2
  fi
else
  GOOS="$(go env GOHOSTOS)"
  GOARCH="$(go env GOHOSTARCH)"
fi

# 取**当前工作树**的真实状态。git rev-parse / status 都按 cwd 解析，
# 不受主工作树影响——这正是它们能纠正 go build 自动戳的原因。
REV="$(git rev-parse HEAD)"
DIRTY_COUNT="$(git status --porcelain --untracked-files=no | wc -l | tr -d ' ')"
COMMIT_TIME="$(git show -s --format=%cI HEAD)"

# 版本号形态：<短号> 或 <短号>+dirty。刻意不冒充 vX.Y.Z——releaseTagRe 只认
# 真正的 release tag，本脚本产出的是开发构建，说清楚比看起来正规更重要。
STAMP="${REV:0:12}"
if [ "$DIRTY_COUNT" != "0" ]; then
  STAMP="${STAMP}+dirty${DIRTY_COUNT}"
  echo "注意：工作树有 $DIRTY_COUNT 处未提交改动，已如实标进版本号" >&2
fi

OUT="${OUT:-$ROOT/dist/handoff-${GOOS}-${GOARCH}$([ "$GOOS" = windows ] && echo .exe || true)}"
mkdir -p "$(dirname "$OUT")"

MODIFIED=false
[ "$DIRTY_COUNT" != "0" ] && MODIFIED=true

build_frontend

echo "==> 构建 $GOOS/$GOARCH  版本 $STAMP  提交 ${REV:0:12} ($COMMIT_TIME)  modified=$MODIFIED"
# 四个注入点缺一不可：releaseVersion 填「版本号」这一格（自动戳给不出），
# releaseRevision / releaseModified / releaseTime 则是**纠正**自动戳——它非空
# 但指向主工作树。status 把 revision 与时刻并排显示，只纠正一个会自相矛盾。
B=github.com/Xsxdot/handoff/internal/buildinfo
CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" go build \
  -trimpath \
  -tags embedweb \
  -ldflags "-s -w -X $B.releaseVersion=$STAMP -X $B.releaseRevision=$REV -X $B.releaseModified=$MODIFIED -X $B.releaseTime=$COMMIT_TIME" \
  -o "$OUT" .

check_injected "$OUT"

check_embedded

echo "OK：${OUT}（$(du -h "$OUT" | cut -f1)），版本 $STAMP"
echo
echo "部署到执行机时记得：运行中的 exe 会被占用，必须先停 agentd 再覆盖。"
echo "Windows 上 schtasks /end 只杀外层 cmd.exe，agentd 孙进程不会跟着退——"
echo "要按 pid 停（Get-Process handoff | Stop-Process -Force）。"
