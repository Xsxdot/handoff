// fallback.go —— 「回合结束但没有协议 trailer」时的共用裁决。
//
// 职责：
//   - 构造无 trailer 且 git 有新提交时的回合结果（一律 OK=false）
//   - 构造该结果的失败原因文案，保证判定依据 / git 实况 / 正文尾部三者齐全
//
// 边界：
//   - 不查 git（那是 GitTurnStatus），不判断 hasNew：调用方把结论传进来
//   - 不处理 !hasNew 与 git 查询失败两条分支：它们仍转 question，由各 adapter
//     自行处置（各家的空文本守卫与原生提问抑制不同，强行统一会丢掉这些差异）
//   - 纯函数：不打日志。判定结果由各 adapter 在调用点记录（那里才有 taskID）
//
// 为什么这段判定必须共用：四个 adapter 曾各写一份，已经漂移——opencode 的
// summary 取回合末 200 字，grok/codex 取一句固定文案，同一个判定给协调者看的
// 东西完全不同。四份副本各自漂移正是 B74 这类问题的温床。
package turn

import (
	"fmt"

	"github.com/xushixin/handoff/internal/executor"
)

// shortCommitLen 是失败原因里 commit 的展示长度。
//
// 为什么截短：失败原因会整条进事件 payload 并展示给协调者，40 位全长 hash
// 挤掉的是正文尾部——而正文尾部才是协调者判断「这回合到底干到哪儿」的依据。
const shortCommitLen = 7

// noTrailerTailRunes 是失败原因里保留的正文尾部长度。
const noTrailerTailRunes = 200

// NoTrailerFailReason 构造「回合未输出协议 trailer」的失败原因文案。
//
// 参数：
//   - branch, commit: GitTurnStatus 查到的 git 实况
//   - text: 回合正文全文（本函数负责截尾）
//
// 返回：一条同时包含判定依据、git 实况、正文尾部的文案
//
// 注意：三者缺一，协调者就得回去翻日志——这条要求来自 spec §3.2，不是格式偏好。
func NoTrailerFailReason(branch, commit, text string) string {
	short := commit
	if len(short) > shortCommitLen {
		short = short[:shortCommitLen]
	}
	return fmt.Sprintf("回合结束但未输出协议 trailer；git 实况 %s@%s（相对回合起点有新提交）；回合末尾：%s",
		branch, short, TailRunes(text, noTrailerTailRunes))
}

// NoTrailerResult 构造「无 trailer 但 git 有新提交」时的回合结果。
//
// 参数：
//   - sessionID: executor 会话标识，供续接与归档
//   - branch, commit: GitTurnStatus 查到的 git 实况
//   - text: 回合正文全文
//
// 返回：OK=false 的结果，git 实况保留在结构化字段里
//
// 为什么是 OK=false 而不是 OK=true：模型没有宣布完成，handoff 不替它宣布。
// 翻转不给协调者增加任何一次操作——OK 与 !OK 都落到 waiting_review，
// 而 done 与 continue 在该状态下都合法。变的只是那条事件从「已完成，摘要如下」
// （邀请协调者不看 diff 就 done）变成「有新提交，但模型未按纪律宣布完成」
// （要求看一眼）。代价为零，收益是不再有假完成。
//
// 注意：Branch/CommitHash 必须继续填。翻转若把 git 实况降级成一段自由文本，
// 协调者与任何下游都无法再结构化地取用它。
func NoTrailerResult(sessionID, branch, commit, text string) *executor.Result {
	return &executor.Result{
		OK:         false,
		Branch:     branch,
		CommitHash: commit,
		SessionID:  sessionID,
		FailReason: NoTrailerFailReason(branch, commit, text),
		VoidReason: executor.VoidReasonTurnDiscipline,
	}
}
