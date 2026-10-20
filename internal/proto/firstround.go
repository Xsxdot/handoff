// firstround.go —— B448 协调者首轮转交的 agentd↔agentd wire DTO（plan §8 冻结 F1–F31）。
//
// 职责：受理机（发起转交的 agentd，经 internal/client）与载体机（执行首轮的 agentd，收件 handler）之间的
// 请求、成功回执与错误体形状，以及错误 kind 词表。
// 边界：只在 agentd 之间流转，Web 不消费，不进 web/src/api/testdata（先例 CoordinatorWakeReq）。
// JSON 键名、kind 词表与状态码是冻结契约，改动须走 plan 冻结流程。
package proto

// FirstRoundTransferReq 是首轮转交请求体：
//   - POST /api/cards/{id}/coordinator/first-round（卡「叫机器人」/ 换绑）
//   - POST /api/sessions/{id}/resident/first-round（会话驻场者首轮）
//
// Squad / Carrier 是受理机冻结的小队与载体，键恒在（无 omitempty，F6/F7）——收件端区分「缺键」与
// 「空串」两种不成形（F8–F11）。AcceptedBy 只进日志（F12）。Expect 只在卡换绑时出现：非空的旧席位身份，
// 收件端以 RebindSeatMode(expect) 落座（F13/F14）。
type FirstRoundTransferReq struct {
	Squad      string `json:"squad"`
	Carrier    string `json:"carrier"`
	AcceptedBy string `json:"accepted_by,omitempty"`
	Expect     string `json:"expect,omitempty"`
}

// CoordinatorFirstRoundResp 是卡首轮转交的 200 回执：CoordinatorLaunchResp 的全部既有键（woke 恒在）加
// 执行机名 handled_by（非空，F16）。受理机回给调用方时剥掉 handled_by（F18）。
type CoordinatorFirstRoundResp struct {
	CoordinatorLaunchResp
	HandledBy string `json:"handled_by"`
}

// ResidentFirstRoundResp 是驻场者首轮转交的 200 回执：SessionResidentOutcome 的既有键（identity、carrier、
// carrier_machine 非空）加 handled_by（F17）。
type ResidentFirstRoundResp struct {
	SessionResidentOutcome
	HandledBy string `json:"handled_by"`
}

// FirstRoundTransferError 是两个首轮转交端点的非 2xx 体（plan §8「错误回执」）。Pointed 只在驻场端点出现：
// 为真当且仅当收件端已在该会话落了失败指针行（F30）。SessionID 只在 kind=seat_conflict 时出现（F25）。
type FirstRoundTransferError struct {
	Error     string `json:"error"`
	Kind      string `json:"kind"`
	SessionID string `json:"session_id,omitempty"`
	Pointed   bool   `json:"pointed,omitempty"`
}

// 首轮转交错误 kind 词表（冻结，F20–F29）。括号内是收件端状态码。
const (
	FirstRoundKindInvalid           = "invalid"            // 400：请求不成形 / 缺转交头（受理端缺陷，入口 502）
	FirstRoundKindNotFound          = "not_found"          // 409：卡不存在（业务上绝不回 404）
	FirstRoundKindNoSlot            = "no_slot"            // 409：执行机名额满
	FirstRoundKindAdmissionRejected = "admission_rejected" // 400：冻结准入被拒（非成员、离线、角色不符）
	FirstRoundKindCASConflict       = "cas_conflict"       // 409：Launch 前席位状态不适合
	FirstRoundKindSeatConflict      = "seat_conflict"      // 409：回合已跑但席位未落（带 session_id）
	FirstRoundKindResidentOccupied  = "resident_occupied"  // 409：会话已有驻场者
	FirstRoundKindReadOnly          = "read_only"          // 409：会话已归档
	FirstRoundKindNoRoom            = "no_room"            // 409：会话不存在
	FirstRoundKindNotLocal          = "not_local"          // 409：载体不在本机（防环拒绝）
	FirstRoundKindRoundFailed       = "round_failed"       // 502：首轮执行失败
)
