// probe.go —— 只读存活探测的共享数据契约。
//
// 职责：
//   - 定义 ProbeReq / ProbeOutcome，供 manager 与各 adapter 共用
//
// 边界：
//   - 只有数据，没有接口：能力接口由消费方（manager）定义并做类型断言，这样
//     「不支持探活的 adapter 一律按 unknown 处理」是自然语义，executor.Adapter
//     的五动作核心契约也不被污染（与 resume.go 同规格）
//   - 无 I/O、无实现
package executor

// ProbeReq 是一次只读存活探测请求。
//
// 字段说明：
//   - TaskID: 目标任务
//   - TaskDir: DataDir/tasks/<id>，恢复凭据（serve.json / claude.json）在里面
//   - SessionID: 落库的 task.ExecutorSession
type ProbeReq struct {
	TaskID    string
	TaskDir   string
	SessionID string
}

// ProbeOutcome 是一次探测的结论。
//
// 字段说明：
//   - Alive: executor 是否仍在
//   - Note: 一句话理由，直接给协调者看（如「执行者进程 pid 1234 不存在」）；
//     Alive=true 时为空
//
// 三态怎么区分：实现方用 error 表达「探不出结论」——err != nil 即 unknown，
// 调用方**不得把它当 dead**。假阳性是诊断命令最贵的失败模式。
type ProbeOutcome struct {
	Alive bool
	Note  string
}
