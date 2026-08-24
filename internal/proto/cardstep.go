// cardstep.go 定义卡节点提交的纯协议类型。
//
// 职责：固定 CLI、agentd 与 Web 之间的 CardStepReq 线格式。
// 边界：本文件只定义协议数据，不执行 I/O、校验或业务逻辑；PlanPath 及任何本地
// 文件字段不属于 --step 请求。
package proto

// CardStepReq 是 POST /api/cards/{id}/step 的一次性请求。
//
// 字段语义：Step 是卡钉工作流中的节点名；Target、Executor、Model、Extra 是本次
// 环节的一次性 CLI 覆盖；Actor 是发起会话标识。Target、Executor、Model、Extra
// 为空时保持缺席语义；Actor 在规范请求中必须非空，旧看板缺席 Actor 时由 agentd
// 补 web:<r.RemoteAddr>。
//
// 注意：本类型没有 PlanPath 或任何本地文件字段；PlanPath 只属于不带 --step 的
// 直派路径，不得经 --step wire 传递。
type CardStepReq struct {
	Step     string `json:"step"`
	Target   string `json:"target,omitempty"`
	Executor string `json:"executor,omitempty"`
	Model    string `json:"model,omitempty"`
	Extra    string `json:"extra,omitempty"`
	Actor    string `json:"actor"`
}
