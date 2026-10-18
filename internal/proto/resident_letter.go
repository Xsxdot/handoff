// resident_letter.go —— B435 驻场者信箱来信的正文 wire（契约 docs/superpowers/specs/b435-contract.md ④）。
//
// 职责：定义「寻址到驻场者的会话消息」被写成桌面端信箱来信时的正文——一封只做
// 门铃的信：告诉驻场者哪个会话有几条新消息、用哪条命令取。消息原文不进信，取信
// 一律经 `handoff session wait <会话>`（持久交付游标只由它推进，信不推进）。
//
// 边界：文件名、目录、原子写入、mod 取信与提交方式沿用 B434（desktop_coordinator.go
// 与 b434-contract.md §3.6，本卡不改）；本文件只定正文。mod 不解析正文，只认首行
// DesktopWakeMarker 前缀，故正文改形无需改 mod。
package proto

import "fmt"

// ResidentLetterTakeCmd 是来信第二行「取信」命令前缀；其后紧跟会话 id。
const ResidentLetterTakeCmd = "handoff session wait "

// ResidentLetter 是一封驻场者来信的全部变量。
type ResidentLetter struct {
	// Session 寻址命中所在会话房间 id（session:<n>）。
	Session string
	// Count 本批命中该驻场者地址的消息条数（≥1）。
	Count int
	// MinSeq / MaxSeq 本批命中消息的账本 seq 区间（含两端）。
	MinSeq, MaxSeq int64
}

// RenderResidentLetter 渲染来信正文：恰两行，各以 "\n" 结尾——
//
//	[handoff] 会话 <session> 有 <n> 条新消息寻址到你（账本 seq <min>–<max>）
//	取信：handoff session wait <session>
//
// 正文只含 handoff 自生成的骨架（会话 id、条数、seq），不含任何第三方原文，
// 因此无需 B434 的围栏、名册与转义。区间分隔符是 U+2013（–），与 B434 卡级信同款。
func RenderResidentLetter(l ResidentLetter) string {
	return fmt.Sprintf("%s 会话 %s 有 %d 条新消息寻址到你（账本 seq %d–%d）\n取信：%s%s\n",
		DesktopWakeMarker, l.Session, l.Count, l.MinSeq, l.MaxSeq, ResidentLetterTakeCmd, l.Session)
}
