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

// LatestResp 是 GET /api/update/latest 的响应。
//
// Tag 是最新发布的版本号。空串表示查不出（限流、断网、缓存为空），消费方一律
// 按「没有新版」处理；通知是锦上添花，绝不能自己变成故障源。
type LatestResp struct {
	Tag       string `json:"tag"`
	CheckedAt string `json:"checked_at,omitempty"` // RFC3339；空=从未查过。
}

// DownloadState 是桌面端安装包下载的进度与结果。
type DownloadState struct {
	// Stage：idle / downloading / verifying / done / failed。
	Stage string `json:"stage"`
	Tag   string `json:"tag,omitempty"`
	// Percent 为 -1 表示不可知（服务端没给 Content-Length）。
	Percent int    `json:"percent"`
	Path    string `json:"path,omitempty"` // done 时的绝对路径。
	Opened  bool   `json:"opened"`         // 是否成功唤起文件管理器。
	Error   string `json:"error,omitempty"`
}

// MachineUpgradeResp 是 POST /api/machines/{name}/upgrade 的响应。
type MachineUpgradeResp struct {
	// Accepted=true 时升级已在后台开始，进度靠 GET /api/machines 的 version 变化观察。
	Accepted bool   `json:"accepted"`
	Verdict  string `json:"verdict"`
	Reason   string `json:"reason,omitempty"`
	Remedy   string `json:"remedy,omitempty"`
	// Forcible 表示这次拒绝能不能被 ?force=1 越过。非托管永远 false。
	Forcible bool `json:"forcible"`
	Busy     int  `json:"busy,omitempty"`
}
