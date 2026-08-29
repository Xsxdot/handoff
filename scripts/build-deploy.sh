#!/usr/bin/env bash
# 构建一个**版本戳可信**的部署二进制。
#
# 职责：按当前工作树（而不是主工作树）的真实 HEAD 与脏状态注入版本信息，
#       交叉编译出可直接部署到执行机的二进制。
# 边界：只构建，不上传、不停服务、不换版——换版是操作者的动作，见 README。
#       不修改工作区里的任何被跟踪文件。
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
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

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

echo "==> 构建 $GOOS/$GOARCH  版本 $STAMP  提交 ${REV:0:12} ($COMMIT_TIME)  modified=$MODIFIED"
# 四个注入点缺一不可：releaseVersion 填「版本号」这一格（自动戳给不出），
# releaseRevision / releaseModified / releaseTime 则是**纠正**自动戳——它非空
# 但指向主工作树。status 把 revision 与时刻并排显示，只纠正一个会自相矛盾。
B=github.com/Xsxdot/handoff/internal/buildinfo
CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" go build \
  -trimpath \
  -ldflags "-s -w -X $B.releaseVersion=$STAMP -X $B.releaseRevision=$REV -X $B.releaseModified=$MODIFIED -X $B.releaseTime=$COMMIT_TIME" \
  -o "$OUT" .

echo "==> 自检：注入的提交号必须真的进了产物"
# 判据只能是「产物里找不找得到这个 sha」：
#   - go version -m 读不到 -ldflags——Go 刻意不记它（可能含密钥），实测确认；
#   - 交叉编译的产物在本机跑不起来，问不了它自己。
# 注入的是字符串常量，落在数据段，grep -a 找得到。自动戳里的 sha 是**错的那个**
# （主工作树的），所以找到当前工作树的 sha 才说明注入生效。
if ! grep -aq -- "$REV" "$OUT"; then
  echo "版本注入自检失败：产物里找不到当前工作树的提交号 $REV" >&2
  echo "自动戳（很可能指向主工作树）：" >&2
  go version -m "$OUT" 2>/dev/null | grep -E "vcs\.(revision|modified)" >&2 || true
  exit 1
fi
# 字段形态是 `build<TAB>vcs.revision=<sha>`，等号连着，不能按空白切第 3 列
AUTO_REV="$(go version -m "$OUT" 2>/dev/null | grep -o 'vcs\.revision=[0-9a-f]*' | cut -d= -f2)"
if [ -n "$AUTO_REV" ] && [ "$AUTO_REV" != "$REV" ]; then
  echo "提示：Go 自动戳是 ${AUTO_REV:0:12}（主工作树），已被注入值 ${REV:0:12} 覆盖"
fi

echo "OK：${OUT}（$(du -h "$OUT" | cut -f1)），版本 $STAMP"
echo
echo "部署到执行机时记得：运行中的 exe 会被占用，必须先停 agentd 再覆盖。"
echo "Windows 上 schtasks /end 只杀外层 cmd.exe，agentd 孙进程不会跟着退——"
echo "要按 pid 停（Get-Process handoff | Stop-Process -Force）。"
