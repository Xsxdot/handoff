#!/usr/bin/env bash
#
# install.sh —— 把仓库里的 handoff skill 装到本机的各个 agent。
#
# 职责：
#   - 把 skills/handoff/ 复制到 Claude Code 的 ~/.claude/skills/handoff（基准副本）
#   - 让 codex / opencode / grok 三家的 skills 目录符号链接到那份基准副本
#
# 边界：
#   - 不改任何 agent 的配置文件（三家都按约定自动扫描 skills 目录）
#   - agent 的 home 目录不存在就跳过它，不代为创建
#   - 幂等：重复执行等于「把仓库最新版重新同步过去」
set -euo pipefail

REPO_SKILL="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/handoff"
BASE="$HOME/.claude/skills/handoff"

[ -f "$REPO_SKILL/SKILL.md" ] || { echo "找不到 $REPO_SKILL/SKILL.md" >&2; exit 1; }

# 基准副本：Claude Code。用副本而不是软链到仓库——仓库切分支/移动时
# 四个 agent 不会一起失效，代价是改动后要重跑本脚本同步。
mkdir -p "$(dirname "$BASE")"
rm -rf "$BASE"
cp -R "$REPO_SKILL" "$BASE"
echo "已安装基准副本: $BASE"

# 其余三家软链到基准副本：改一次基准，三家同时生效
for dir in "$HOME/.codex/skills" "$HOME/.config/opencode/skills" "$HOME/.grok/skills"; do
    parent="$(dirname "$dir")"
    if [ ! -d "$parent" ]; then
        echo "跳过 $dir（$parent 不存在）"
        continue
    fi
    mkdir -p "$dir"
    # 先删再建：目标可能是上一次装的软链，也可能是手工放的实体目录
    rm -rf "$dir/handoff"
    ln -s "$BASE" "$dir/handoff"
    echo "已软链: $dir/handoff -> $BASE"
done
