// b23313_managerfactory_wire_test.go —— 外部测试包接线：把 gateway 测试工厂接到
// 真实编排实现 internal/orchestration.Manager。
//
// 为什么放在 package agentd_test：只有外部测试包可以同时 import agentd 与
// orchestration 而不触发 import cycle（package agentd 内部测试不能 import
// orchestration）。init 在测试运行前执行，两种测试包共享同一二进制。
package agentd_test

import (
	"github.com/Xsxdot/handoff/internal/agentd"
	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/orchestration"
)

func init() {
	agentd.ManagerFactory = func(d agentd.ManagerDeps) agentd.TestManager {
		m := orchestration.NewManager(d.Store, d.Hub, d.Ads, d.Cfg, d.EnvMapping, nil, d.Gate, d.Log)
		m.SetWorkspace(agentd.NewGitCapability())
		live := d.LiveConfig
		if live == nil {
			cfg := d.Cfg
			live = func() *config.Config { return cfg }
		}
		m.SetLiveConfig(live)
		return m
	}
}
