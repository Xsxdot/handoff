package agentd

// b23314_scheduling_testhelpers_test.go —— B233.14 测试装配缝。
//
// 职责：SetupAutomation 不再构造编制域具体服务后，测试需要真实编制域时显式补装
// （镜像 cmd/agentd.go#setupLedger 的组装：SchedulingRegistry + SetKnownMachines
// + SetScheduling）。
// 边界：仅测试构建可见；不改生产导入面。SetupAutomationForTest 导出以便
// package agentd_test 复用（与 B23313 ManagerFactory 同款跨测试包接线）。

import (
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

// newSchedulingForTest 构造编制域具体服务并镜像 cmd 的 SetKnownMachines（经
// Server.Conf() 读活配置）——schedapi 的远程机器放行/未知机器拒绝用例依赖它。
func newSchedulingForTest(t *testing.T, srv *Server) *scheduling.Service {
	t.Helper()
	reg := srv.SchedulingRegistry()
	if reg == nil {
		t.Fatal("SchedulingRegistry() == nil：SetupAutomation 未装配账本门面")
	}
	svc := scheduling.New(reg)
	svc.SetKnownMachines(func(name string) bool {
		cfg := srv.Conf()()
		if cfg == nil {
			return false
		}
		_, ok := cfg.Targets[name]
		return ok
	})
	return svc
}

// SetupAutomationForTest 装配账本/rooms/keystone 并补装编制域具体服务。
func SetupAutomationForTest(t *testing.T, srv *Server, st *ledger.Store) *scheduling.Service {
	t.Helper()
	srv.SetupAutomation(st)
	svc := newSchedulingForTest(t, srv)
	srv.SetScheduling(svc)
	return svc
}

// mustScheduling 返回已装配的真实编制域服务；未装配时补装。返回具体
// *scheduling.Service，供不在接口内的方法（SetDefaultCarrier）与既有具体类型
// 夹具使用。
func mustScheduling(t *testing.T, srv *Server) *scheduling.Service {
	t.Helper()
	if svc, ok := srv.Scheduling().(*scheduling.Service); ok && svc != nil {
		return svc
	}
	svc := newSchedulingForTest(t, srv)
	srv.SetScheduling(svc)
	return svc
}
