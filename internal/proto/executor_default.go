// executor_default.go —— 控制台配置机器级缺省执行者的线格式（B160）。
//
// 职责：GET / PUT /api/executor/default 的请求与响应结构。
//
// 边界：
//   - 只覆盖 config 的 executor 段两个标量字段，不碰 approver、proc_fence 等
//     其它机器级配置（哪些不给写、为什么，见 spec §1.2）
//   - 不含任何密钥字段
package proto

// ExecutorDefaultResp 是 GET /api/executor/default 的响应。
//
// Model 的语义是「**Default 的**默认模型」，不是全局默认——agentd 的
// resolveModel 只在 execName == Default 时才套用它，派别的执行器返回空串。
// 界面文案必须照这个语义写，不要写成「不分执行器」（那是修过的旧行为）。
type ExecutorDefaultResp struct {
	Default   string   `json:"default"`   // 当前缺省执行者名
	Model     string   `json:"model"`     // 缺省执行者的默认模型；空串 = 用执行器自身默认
	Available []string `json:"available"` // 该机已注册的 adapter 名，按名字升序
}

// ExecutorDefaultReq 是 PUT /api/executor/default 的请求体。
//
// 两个字段都是**整体替换**语义：缺席与空串一视同仁。Model 为空串是一个有意义
// 的取值（= 不设默认模型），不是「这一项不改」——本接口没有「不改」这个表达。
type ExecutorDefaultReq struct {
	Default string `json:"default"`
	Model   string `json:"model"`
}
