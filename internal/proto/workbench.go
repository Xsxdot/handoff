// 工作台状态同步的线格式类型（2026-08-20 状态同步 spec §4.2）。
//
// 职责：定义 /api/workbench/state 四个端点的请求/响应形状。
// 边界：
//   - 不含任何行为
//   - **Payload 一律是字符串**，内容是前端序列化好的 JSON。agentd 不解析它，
//     所以也不该让 JSON 解码器替它解析一遍（spec §4.2）
package proto

// WorkbenchBase 是一个基准目录的持久化状态行。
type WorkbenchBase struct {
	BaseKey string `json:"base_key"`
	// Payload 是前端序列化好的 JSON 字符串（布局 + 基准目录元数据）。
	Payload string `json:"payload"`
	// UpdatedAt 是毫秒时间戳。毫秒而非秒：淘汰按它排序，秒级精度下同秒写入的
	// 多行并列，裁掉哪一条就成了随机的。
	UpdatedAt int64 `json:"updated_at"`
}

// WorkbenchStateResp 是 GET /api/workbench/state 的响应。
//
// Selected / Dock 没有内容时是**空串**而不是缺键：两者都是「当前没有」这个
// 明确结论，缺键会让前端分不清它和「这版服务端还不认识这个字段」。
type WorkbenchStateResp struct {
	Selected string          `json:"selected"`
	Dock     string          `json:"dock"`
	Bases    []WorkbenchBase `json:"bases"`
}

// WorkbenchBaseReq 是 PUT /api/workbench/state/base 的请求体。
//
// Payload 用指针表达三态里的两态：**取 null = 删除该行**，否则是要写入的内容。
// 为什么不用空串当删除信号：空串是一个合法但无意义的 payload，用它当信号会让
// 「前端 bug 发了个空串」静默变成「删掉用户的布局」。
type WorkbenchBaseReq struct {
	BaseKey string  `json:"base_key"`
	Payload *string `json:"payload"`
}

// WorkbenchSelectedReq 是 PUT /api/workbench/state/selected 的请求体。
// BaseKey 为空串表示「当前没有选中任何目录」，这是合法状态，会落库成空串。
type WorkbenchSelectedReq struct {
	BaseKey string `json:"base_key"`
}

// WorkbenchDockReq 是 PUT /api/workbench/state/dock 的请求体。
// Payload 取 null = 清空悬浮窗现场，语义同 WorkbenchBaseReq.Payload。
type WorkbenchDockReq struct {
	Payload *string `json:"payload"`
}
