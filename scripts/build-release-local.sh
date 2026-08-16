#!/usr/bin/env bash
# 在本地复现 release 流水线的构建步骤，用于在改 workflow 之前/之后验证
# 「前端构建 → go build -tags embedweb → 产物含前端」这条链路真的通。
#
# 职责：只构建与自检，不签名、不打包、不上传——那些是 CI 的事。
# 边界：不修改工作区里的任何被跟踪文件；产物落在 mktemp 出来的目录里。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="$(mktemp -d)"
trap 'rm -rf "$OUT"' EXIT

echo "==> 构建前端"
cd "$ROOT/web"
npm ci
npm run build
[ -f "$ROOT/web/dist/index.html" ] || { echo "前端产物缺 index.html" >&2; exit 1; }

echo "==> 把产物拷进 internal/webui/dist/"
rm -rf "$ROOT/internal/webui/dist"
cp -R "$ROOT/web/dist" "$ROOT/internal/webui/dist"

echo "==> 带 embedweb 标签构建"
cd "$ROOT"
CGO_ENABLED=0 go build -trimpath -tags embedweb -ldflags "-s -w" -o "$OUT/handoff" .

echo "==> 带标签跑 webui 测试（确认产物真进去了，不是空壳）"
CGO_ENABLED=0 go test -tags embedweb ./internal/webui/...

echo "==> 自检：工作区不能因为构建而变脏"
if ! git -C "$ROOT" diff --quiet || \
   [ -n "$(git -C "$ROOT" status --porcelain --untracked-files=normal | grep -v '^?? internal/webui/dist/' || true)" ]; then
  echo "构建让工作区变脏了——这会破坏 dispatch 的干净工作区前置条件" >&2
  git -C "$ROOT" status --short >&2
  exit 1
fi

echo "OK：产物 $( du -h "$OUT/handoff" | cut -f1 )，工作区干净"
