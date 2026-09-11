// b23313_managerfactory_test.go —— B233.13 迁包后 gateway 白盒测试构造真实编排实现的桥。
//
// 职责：package agentd 生产不得 import internal/orchestration（出站封界），本文件只
// 在测试构建里声明一个由外部测试包（package agentd_test）接线的工厂变量；外部测试
// 包可以同时 import 二者，因此消除了「白盒测试需要 *Manager」与 import cycle 的矛盾。
//
// 边界：仅测试构建可见（_test.go）；不进入生产导入面。ManagerDeps 只带 gateway 能
// 命名的类型，Approver 等编排包私有类型不出现。
package agentd

import (
	"log/slog"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/permgate"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
	"github.com/Xsxdot/handoff/internal/workspace"
)

// ManagerDeps 是 gateway 测试构造编排实现所需的输入（与 cmd 组装点同口径）。
type ManagerDeps struct {
	Store      *store.Store
	Hub        *Hub
	Ads        map[string]executor.Adapter
	Cfg        *config.Config
	EnvMapping func() map[string]string
	Gate       *permgate.Gate
	Log        *slog.Logger
	// LiveConfig 非 nil 时作为活配置取值函数注入（等价 cmd 的 SetLiveConfig(srv.Conf())）。
	LiveConfig func() *config.Config
}

// TestManager 是 gateway 白盒测试消费编排实现的接口：OrchestrationClient 加上
// 组装点/测试要用的装配方法（均未进入生产契约）。
type TestManager interface {
	OrchestrationClient
	SetWorkspace(workspace.Capability)
	SetLiveConfig(func() *config.Config)
	TaskProcCount(taskID string) (int, bool)
	SweepTaskProcs(taskID string)
	ForceReclaim(taskID, reason string) error
	MismatchTransit() func(taskID string, to proto.TaskState, reason string) error
	ResumeTask(taskID string) bool
}

// ManagerFactory 由 agentd_test 外部测试包在 init 中接线到 orchestration.NewManager。
// 生产 package agentd 永不设它；若未接线，NewTestManager 直接 Fatal 提示。
var ManagerFactory func(ManagerDeps) TestManager
