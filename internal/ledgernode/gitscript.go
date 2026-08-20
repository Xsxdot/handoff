// 真机 git 脚本的公共片段与失败归类。
//
// 职责：拼「把工作分支补齐到 origin」这段阶梯脚本，并把脚本的失败翻成
// 调用方能分辨的 Go 错误。
// 边界：只生成脚本文本与翻译错误，不执行任何命令——执行在 wire.go 的两个
// 注入点里，那里才有 repoDir 与 ctx。
package ledgernode

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
)

// ErrWorkBranchMissing 工作分支在协调者本地与 origin 上都不存在。
//
// 为什么要单独一个哨兵：MergeNode 把 DoMerge 的任何错误都记成「合并冲突」，
// 而这一条根本没走到合并——人看到「合并冲突」会去查代码冲突，白费一轮。
var ErrWorkBranchMissing = errors.New("工作分支在本地与 origin 都不存在")

// workBranchMissingMarker 脚本用它向 Go 侧报告「阶梯走到底了」。
// 用带前缀的哨兵串而不是只认退出码：脚本里任何一条 git 命令都可能退 3。
const workBranchMissingMarker = "HANDOFF_WORK_BRANCH_MISSING"

// syncWorkBranchScript 生成「把工作分支补齐到 origin」的 bash 片段（spec §3.3 阶梯）。
//
// 参数：branch 工作分支名（原样传入，内部做 shell 转义）。
// 返回：多行脚本文本，供调用方拼进完整脚本。
//
// 阶梯三条腿，缺一条都会退化成含糊失败：
//  1. 本地有该分支 → 推上 origin（常态：wait 的 sync.auto 已经 fetch 过）
//  2. 本地没有 → 试着从 origin 拉（可能别的协调机已经推过了）
//  3. 都没有 → 打 marker 退 3，由 classifyScriptError 翻成 ErrWorkBranchMissing
//
// 推送用显式 refspec `<分支>:<分支>`，不依赖当前分支或 upstream 配置——
// upstream 名字对不上时裸 push 什么都不做**且不报错**，那是最难查的一类失败。
func syncWorkBranchScript(branch string) string {
	ref := shellQuote("refs/heads/" + branch)
	name := shellQuote(branch)
	return strings.Join([]string{
		"if git rev-parse --verify --quiet " + ref + " >/dev/null; then",
		"  git push origin " + name + ":" + name,
		"elif git fetch origin " + name + "; then",
		"  :",
		"else",
		"  echo " + shellQuote(workBranchMissingMarker),
		"  exit 3",
		"fi",
	}, "\n")
}

// classifyScriptError 把脚本执行结果翻成 Go 错误。
//
// 参数：out 合并后的 stdout+stderr；err exec 的返回；action 用于错误文案的动作名
// （如「合并」「客观判据」）。err 为 nil 时返回 nil。
//
// 注意：命中 marker 时包装 ErrWorkBranchMissing 供 errors.Is 判定，同时**保留
// 脚本原始输出**——取证文化要求错误里始终看得到远端说了什么。
func classifyScriptError(out []byte, err error, action string) error {
	if err == nil {
		return nil
	}
	if bytes.Contains(out, []byte(workBranchMissingMarker)) {
		return fmt.Errorf("%w：\n%s", ErrWorkBranchMissing, out)
	}
	return fmt.Errorf("%s失败:\n%s", action, out)
}
