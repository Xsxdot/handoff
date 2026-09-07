// recoverer.go —— 执行会话的可选恢复能力（B233.2）。
//
// 职责：把 manager 内部 restorer 的形状提升为执行契约包的导出接口。
//
// 边界：
//   - 不进 Adapter 五动作（与 AskResponder 同形：消费方类型断言）
//   - 签名必须与现状 restorer.Resume(ResumeReq) 逐字相同，否则现有五家
//     Resume 方法不再满足接口
//   - 本文件无 I/O
package executor

// Recoverer 是执行会话的可选恢复能力。未实现则按不存活走失败恢复路径。
type Recoverer interface {
	Resume(req ResumeReq) (ResumeOutcome, error)
}
