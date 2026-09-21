package agentd

// b23314_scheduling_testhelpers_test.go —— B233.14 测试装配缝。
//
// 职责：SetupAutomation 不再构造编制域具体服务后，测试需要真实编制域时显式补装
// （镜像 cmd/agentd.go#setupLedger 的组装：SchedulingRegistry + SetKnownMachines
// + SetScheduling）。B233.18 起 rooms/hostapi/keystone 三域构造同样上移 cmd，
// 由 assembleDomainsForTest 经同一条导出构造缝（NewCoordinatorRunner 等）镜像
// 补装，保证装配完成后字段状态与改前 SetupAutomation 逐字段等价；cmd 包无法
// 被本测试包导入（import 环），镜像按 newSchedulingForTest 先例落在测试侧。
// 边界：仅测试构建可见；不改生产导入面。SetupAutomationForTest 导出以便
// package agentd_test 复用（原与 B23313 ManagerFactory 同款跨测试包接线；该桥已随
// B233.26 刀2 退役）。

import (
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/collab"
	"github.com/Xsxdot/handoff/internal/collab/cursor"
	"github.com/Xsxdot/handoff/internal/executor/opencode"
	"github.com/Xsxdot/handoff/internal/hostapi"
	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/orchestration"
	"github.com/Xsxdot/handoff/internal/scheduling"
	"github.com/Xsxdot/handoff/internal/toolchain"
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

// assembleDomainsForTest 镜像 cmd/agentd.go#setupLedger 的三域装配（B233.18：
// rooms/hostapi/keystone 构造上移 cmd 后，SetupAutomation 只剩 facade/cursor/
// ptyGate 纯装配，测试经导出构造函数 + Set* 注入缝补装三域）。
func assembleDomainsForTest(t *testing.T, srv *Server) {
	t.Helper()
	facade := srv.AutoLedger()
	if facade == nil {
		t.Fatal("AutoLedger() == nil：SetupAutomation 未装配账本门面")
	}
	rooms := collab.New(facade)
	rooms.SetCursorStore(cursor.New(filepath.Join(srv.Conf()().DataDir, "room-cursors.json")))
	srv.SetRooms(rooms)
	hostAPI := hostapi.NewWithCredentialPathFor(toolchain.CredRelPathFor)
	srv.SetHostAPI(hostAPI)
	prepareHome := orchestration.NewCoordinatorPrepareHome(srv.Conf(), srv.Providers(), srv.RuleLoader())
	coord := opencode.NewCoordinator(hostAPI, slog.Default())
	ks := keystone.New(NewCoordinatorRunner(coord, srv.Providers(), prepareHome),
		keystone.NewRoomNarrator(rooms), facade, NewAttachLocator(hostapi.ExpandHomePath))
	ks.SetSessionRefResolver(NewCoordinatorSessionRefResolver(srv, hostapi.ExpandHomePath))
	srv.SetKeystone(ks)
}

// SetupAutomationForTest 装配账本/rooms/keystone 并补装编制域具体服务。
func SetupAutomationForTest(t *testing.T, srv *Server, st *ledger.Store) *scheduling.Service {
	t.Helper()
	srv.SetupAutomation(st)
	assembleDomainsForTest(t, srv)
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
