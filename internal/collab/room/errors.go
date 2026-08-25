// 协作房间域的执法错误哨兵（契约 §3.3 哨兵集冻结，映射表不变）。
// 定义在执法内核子包、由根包 collab 再导出：gateway/CLI 只认 collab.ErrXxx
// 符号，根包与子包不可互 import（根包依赖子包），哨兵只能单点定义在子包。
package room

import "errors"

var (
	// ErrNoRoom 400：房间不存在
	ErrNoRoom = errors.New("collab: 房间不存在")
	// ErrKindNotAllowed 400：kind 不在白名单 / pointer 走错门
	ErrKindNotAllowed = errors.New("collab: 消息形态不在白名单")
	// ErrReadOnly 409：房间并入冻结或终态归档
	ErrReadOnly = errors.New("collab: 房间已只读（并入冻结或终态归档）")
	// ErrNotWriter 403：书写者与房间身份不符
	ErrNotWriter = errors.New("collab: 书写者与房间身份不符")
	// ErrNotBound 409：需要绑定席位的动作遇到无绑定卡
	ErrNotBound = errors.New("collab: 卡无绑定席位")
)