// scheduling_client.go —— gateway 对编制域的出站 client 契约（B233.14 Ticket 0）。
//
// 职责：定义 Server 生产字段持有的编制接口，方法集覆盖 handler / receiver_bind /
// scheddispatch / schedapi / scheddrain / coordapi 今日对 s.scheduling 的生产调用；
// 具体实现由组装点注入。实现节点把编排与组装收口后，Server.scheduling 字段类型
// 改为本接口，`scheduling.New` 只在 cmd 组装点出现。
//
// 边界：不放业务实现、不新增 HTTP/CLI/事件类型。DTO / 包级纯函数（Carrier、Squad、
// Binding、IgnitionRequest、IdentityOf、BindPhysical、ResolveLookup…）留在
// internal/scheduling，gateway 为消费这些类型可以 import 它；生产字段类型必须是
// 接口，禁止 *scheduling.Service。Service 不 import gateway——无环，不搬 DTO。
package agentd

import "github.com/Xsxdot/handoff/internal/scheduling"

// SchedulingClient 是 gateway 生产路径消费编制域能力的唯一接口。
//
// 接口按架构法第九条定义在使用方（gateway）；实现是 internal/scheduling 的
// *Service。方法集是生产调用闭包：多一个即越界，少一个即 handler 编不过。
// 新增方法先回 contract 节点。
//
// 注意：SetDefaultCarrier 当前无生产消费点（仅测试使用），不入本接口——它是
// B233.14 contract 查证的「疑似漂移」常量，漂移归 B233.5 后续卡。
type SchedulingClient interface {
	// —— 接收者名称解析（receiver_bind / coordinator_home）——
	DefaultCarrier() (string, error)
	Carrier(name string) (scheduling.Carrier, error)
	Squad(name string) (scheduling.Squad, error)

	// —— 准入与占用（receiver_bind / scheddispatch / server / scheddrain）——
	AdmitCarrier(name string) (scheduling.Binding, error)
	Admit(req scheduling.IgnitionRequest) (scheduling.Binding, error)
	AdmitFrozen(binding scheduling.Binding) (scheduling.Binding, error)
	Select(req scheduling.IgnitionRequest) (scheduling.Binding, error)
	LaunchAdmit(squadName string) (scheduling.Binding, error)
	Release(squadName, carrierName string) error

	// —— 点火队列（scheddispatch / scheddrain）——
	Enqueue(req scheduling.IgnitionRequest, kind string) (int, error)
	PopReady(kind string) (scheduling.IgnitionRequest, bool, error)
	QueueSnapshot() ([]scheduling.QueuedRequest, error)

	// —— 载体/小队登记读（schedapi / coordapi）——
	CarrierRows() ([]scheduling.CarrierRow, error)
	SquadRows() ([]scheduling.SquadRow, error)

	// —— 载体/小队登记写（schedapi）——
	PutCarrier(c scheduling.Carrier, expect int) error
	DeleteCarrier(name string, expect int) error
	PutSquad(q scheduling.Squad, expect int) error

	// —— 检测写状态（schedapi）——
	ApplyDetect(name string, ev scheduling.DetectEvidence, detail string) (scheduling.Carrier, error)
}

// 编译期断言：编制域实现必须满足 gateway 使用方接口。实现节点切换字段类型时
// 由本断言在编译期暴露方法集错配，不留给集成阶段。
var _ SchedulingClient = (*scheduling.Service)(nil)

// 编译期断言：编制域 *Service 满足 gateway 使用方接口（方法集闭包核对）。
// 少一个方法或签名漂移即编译失败；多出来的方法不影响（接口只声明消费面）。
var _ SchedulingClient = (*scheduling.Service)(nil)
