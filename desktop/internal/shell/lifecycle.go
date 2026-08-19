// 本文件负责 agentd 的存活：判断它装没装、起没起，必要时用 internal/service 托管起来。
//
// 边界（三条都是承重的，见 spec §4.3）：
//   - **绝不把 agentd 跑进薄壳进程**。agentd 必须活过薄壳、必须能在无 GUI 机器上裸跑，
//     且 B59 的更新机制假设它由 service 托管
//   - **绝不在薄壳退出时停掉 agentd**。执行者不能随关窗陪葬
//   - 已在运行时**什么都不做**。重复 Install 会重装单元、打断正在跑的任务
package shell

import (
	"fmt"
	"log/slog"

	"github.com/Xsxdot/handoff/internal/service"
)

// newManager 是 service.New 的测试缝：测试注入替身，避免真的往 launchd/systemd 写单元。
// 生产路径永远是 service.New。
var newManager = service.New

// EnsureRunning 确保本机 agentd 处于运行状态。
//
// 参数：
//   - log: 日志入口，会透传给 service.Manager
//   - spec: 托管所需的二进制、配置与日志路径
//
// 返回：
//   - error：平台不支持、查状态失败、或安装失败。**平台不支持时原样带出原因**
//     （Windows 上 service.New 会说明是 B37 未完成），不要压成一句「失败」
//
// 注意：
//   - agentd 已在运行时本函数**不做任何事**，这是刻意的
func EnsureRunning(log *slog.Logger, spec service.Spec) error {
	m, err := newManager(log)
	if err != nil {
		log.Error("无法获得服务管理器", "cause", err)
		return fmt.Errorf("这台机器上无法托管 agentd: %w", err)
	}
	st, err := m.Status()
	if err != nil {
		log.Error("查询 agentd 状态失败", "kind", m.Kind(), "cause", err)
		return fmt.Errorf("查询 agentd 状态: %w", err)
	}
	if st.Running {
		log.Info("agentd 已在运行，无需干预", "kind", m.Kind(), "detail", st.Detail)
		return nil
	}
	log.Info("agentd 未在运行，准备托管拉起", "kind", m.Kind(), "installed", st.Installed, "bin", spec.BinPath)
	// 已装就只启动，不重写单元定义。
	//
	// 承重的理由在 Windows：Install 会先 `schtasks /Delete /F` 再重建任务，
	// 而本函数是换版路径上的常客（WaitAgentdBack 探不到新版本时会催一次），
	// 于是「升级一次 = 计划任务被删了重建一次」，用户对任务定义做过的任何
	// 修改和任务历史一并消失。Start 只触发、不重建。
	if st.Installed {
		serr := m.Start()
		if serr == nil {
			log.Info("agentd 已拉起（沿用既有单元定义）", "kind", m.Kind())
			return nil
		}
		// 不直接返回错误：既有单元可能真的坏了（指向已被删除的二进制、定义被
		// 改残），那种情况下重装才是对的自愈动作。降级为 Warn 后继续走 Install
		// ——把「省一次重写」置于「agentd 拉不起来」之上是本末倒置
		log.Warn("沿用既有单元拉起失败，改为重装", "kind", m.Kind(), "cause", serr)
	}
	if err := m.Install(spec); err != nil {
		log.Error("托管 agentd 失败", "kind", m.Kind(), "bin", spec.BinPath, "cause", err)
		return fmt.Errorf("托管并拉起 agentd: %w", err)
	}
	log.Info("agentd 已托管并拉起", "kind", m.Kind())
	return nil
}
