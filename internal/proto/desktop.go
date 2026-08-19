// 本文件定义桌面薄壳与控制台之间经 agentd 中转的数据类型。
//
// 职责：只声明线上的数据形状。
// 边界：
//   - 不含任何指令类型。薄壳只上报、不接指令：让控制台点得动薄壳需要一条
//     反向通道，那条通道比它服务的动作还贵，设计上已排除（spec §5）。
//   - 不含凭据字段。这条通道只走版本与同步结论。
package proto

// DesktopState 是薄壳向控制台公开的自身状态。
//
// 字段全部由薄壳填，agentd 只做带 TTL 的转发。控制台据此判断「有没有新版」——
// 必须用薄壳的版本比，不能用 agentd 自己的版本：同步被拦或失败时两者恰好不等，
// 用 agentd 的版本会去劝用户下载一个他已经装好了的版本。
type DesktopState struct {
	// AppVersion 是薄壳自身版本（desktop 侧的 embedbin.Version）。空串=判不出
	// （开发构建未注入版本），此时控制台一律不提示。
	AppVersion string `json:"app_version"`
	// SyncPlan 是本次开机同步的结论：skip / blocked / failed / done。
	SyncPlan string `json:"sync_plan"`
	// SyncBusy 是 blocked 时的活跃任务数；-1 表示探测失败，不要当 0 用。
	SyncBusy int `json:"sync_busy"`
	// SyncError 是 failed 时的原文，供控制台原样展示。
	SyncError string `json:"sync_error,omitempty"`
}
