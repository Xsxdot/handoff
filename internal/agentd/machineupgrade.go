// 本文件实现 POST /api/machines/{name}/upgrade：把一台远端执行机升到最新。
//
// 边界（承重）：
//   - **不重写编排。** 七种结论、两道闸、pull/push 择路、等新版本上线全在
//     internal/upgrade 里，与 handoff upgrade 共用同一份。这里只做三件事：
//     探一台机器、把结论翻成 HTTP 状态码、把动作丢进后台
//   - **不处理本机。** 本机 agentd 的版本由薄壳的同步路决定（spec §6.5）；
//     在这里再开一个入口就是第二条换版路径，两条会打架
//   - **不造进度流。** 中途阶段只进日志。但**终态必须可查**：GET /api/machines
//     里那台的 upgrade 段带着最近一次的结论。原先只说「判据是 version 变了」，
//     那只覆盖成功；失败时版本不会变，控制台就永远停在「升级中」（真机实测）
package agentd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/release"
	"github.com/Xsxdot/handoff/internal/upgrade"
)

const (
	machineUpgradeProbeTimeout = 5 * time.Second
	machineUpgradeTimeout      = 15 * time.Minute
)

// machineUpgradeRunner 是后台升级动作的缝。
//
// 生产实现只调用 upgrade.RemoteOne；测试替换它以断开真实下载、推送与重启。
type machineUpgradeRunner func(ctx context.Context, m upgrade.Machine, target config.Target,
	rel release.Release, force bool, progress func(string)) upgrade.Result

// executeMachineUpgrade 是 machineUpgradeRunner 的生产实现。
func (s *Server) executeMachineUpgrade(ctx context.Context, m upgrade.Machine, target config.Target,
	rel release.Release, force bool, progress func(string)) upgrade.Result {
	if s.machineUpgradeInstaller == nil {
		return upgrade.Result{Verdict: upgrade.VerdictNeedsUpgrade, Status: upgrade.StatusFail,
			Reason: "远端升级下载器未就绪"}
	}
	return upgrade.RemoteOne(ctx, s.log, m, client.New(target.Addr, target.Token),
		s.machineUpgradeInstaller, rel, upgrade.Options{Force: force}, progress)
}

// beginMachineUpgrade 抢占单台机器的后台升级槽位。
//
// 抢到时把状态置为 Running 并**清掉上一轮的终态**：留着会让控制台把旧结果
// 当成这一轮的结论。
func (s *Server) beginMachineUpgrade(name string) bool {
	s.upgradeMu.Lock()
	defer s.upgradeMu.Unlock()
	if s.machineUpgrades == nil {
		s.machineUpgrades = make(map[string]*proto.MachineUpgrade)
	}
	if st := s.machineUpgrades[name]; st != nil && st.Running {
		return false
	}
	s.machineUpgrades[name] = &proto.MachineUpgrade{Running: true}
	return true
}

// abortMachineUpgrade 放弃一个刚抢到、但还没真正开跑的槽位（前置校验没过）。
// 恢复成「本进程没发起过」，不留一条没有内容的终态。
func (s *Server) abortMachineUpgrade(name string) {
	s.upgradeMu.Lock()
	defer s.upgradeMu.Unlock()
	delete(s.machineUpgrades, name)
}

// finishMachineUpgrade 落终态并释放槽位。
//
// 承重：这是控制台知道「升级结束了」的唯一途径——升级没有进度流，成功可以靠
// 版本变化推断，失败**只有这条**。
func (s *Server) finishMachineUpgrade(name string, result upgrade.Result) {
	s.upgradeMu.Lock()
	defer s.upgradeMu.Unlock()
	s.machineUpgrades[name] = &proto.MachineUpgrade{
		Running: false,
		Status:  result.Status.String(),
		Verdict: result.Verdict.String(),
		Reason:  result.Reason,
		Remedy:  result.Remedy,
		From:    result.From,
		To:      result.To,
	}
}

// machineUpgradeState 返回某台机器的升级状态快照（无=nil）。
func (s *Server) machineUpgradeState(name string) *proto.MachineUpgrade {
	s.upgradeMu.Lock()
	defer s.upgradeMu.Unlock()
	st, ok := s.machineUpgrades[name]
	if !ok || st == nil {
		return nil
	}
	snapshot := *st // 拷贝：调用方拿去序列化，不能让它读到之后的写
	return &snapshot
}

// handleMachineUpgrade 处理 POST /api/machines/{name}/upgrade。
func (s *Server) handleMachineUpgrade(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if isLocalMachineName(name) {
		s.log.Warn("拒绝升级本机", "name", name)
		writeJSON(w, http.StatusBadRequest, proto.MachineUpgradeResp{
			Verdict: "local", Reason: "本机版本由薄壳同步路决定，不能从执行机端点升级",
		})
		return
	}
	target, ok := s.conf().Targets[name]
	if !ok {
		s.log.Warn("升级未知执行机", "name", name)
		writeJSON(w, http.StatusNotFound, proto.MachineUpgradeResp{
			Verdict: "unknown", Reason: "机器不在配置中",
		})
		return
	}
	if !s.beginMachineUpgrade(name) {
		s.log.Warn("拒绝重复执行机升级", "name", name)
		writeJSON(w, http.StatusConflict, proto.MachineUpgradeResp{
			Verdict: "in_progress", Reason: "该机器已有升级在进行中",
		})
		return
	}
	keepSlot := false
	defer func() {
		if !keepSlot {
			// 没跑起来就不留终态：前置校验的结论已经在 HTTP 响应里给了调用方
			s.abortMachineUpgrade(name)
		}
	}()

	probeCtx, cancel := context.WithTimeout(r.Context(), machineUpgradeProbeTimeout)
	defer cancel()
	s.log.Info("开始探测执行机升级目标", "name", name, "addr", target.Addr)
	status, err := client.New(target.Addr, target.Token).Status(probeCtx)
	probeErr := err
	if errors.Is(err, client.ErrStatusUnsupported) {
		// 旧 agentd 没有 status 端点：这是可判定的「过旧」，不是网络不可达。
		probeErr = nil
	}
	m := projectUpgradeMachine(name, status, probeErr)
	if err != nil && !errors.Is(err, client.ErrStatusUnsupported) {
		result := upgrade.Result{Verdict: upgrade.VerdictUnreachable, Status: upgrade.StatusFail,
			Reason: err.Error()}
		s.log.Warn("执行机升级目标够不着", "name", name, "cause", err)
		s.writeMachineUpgradeResult(w, http.StatusBadGateway, m, result, false)
		return
	}

	if s.latestFetch == nil {
		err = errors.New("最新版查询依赖未注入")
	} else {
		var rel release.Release
		rel, err = s.latestFetch(probeCtx)
		if err == nil && rel.Tag == "" {
			err = errors.New("最新版 tag 为空")
		}
		if err == nil {
			result := machineUpgradePreflight(m, rel.Tag, r.URL.Query().Get("force") == "1")
			if result.Verdict != upgrade.VerdictNeedsUpgrade || result.Status != upgrade.StatusOK {
				code := machineUpgradeStatusCode(result)
				switch result.Verdict {
				case upgrade.VerdictNeedsUpgrade:
					s.log.Warn("闸一拒绝执行机升级", "name", name, "reason", result.Reason,
						"forcible", result.Forcible)
				case upgrade.VerdictUnmanaged:
					s.log.Warn("闸二拒绝执行机升级", "name", name, "reason", result.Reason,
						"forcible", result.Forcible)
				default:
					s.log.Info("跳过执行机升级", "name", name, "verdict", result.Verdict.String(),
						"reason", result.Reason)
				}
				s.writeMachineUpgradeResult(w, code, m, result, false)
				return
			}
			force := r.URL.Query().Get("force") == "1"
			keepSlot = true
			s.log.Info("受理执行机升级", "name", name, "target", rel.Tag, "force", force)
			writeResult := proto.MachineUpgradeResp{
				Accepted: true, Verdict: result.Verdict.String(), Busy: m.Busy,
			}
			go s.runMachineUpgradeBackground(name, m, target, rel, force)
			writeJSON(w, http.StatusAccepted, writeResult)
			return
		}
	}

	s.log.Error("查询执行机升级目标版本失败", "name", name, "cause", err)
	s.writeMachineUpgradeResult(w, http.StatusBadGateway, m, upgrade.Result{
		Verdict: upgrade.VerdictNeedsUpgrade, Status: upgrade.StatusFail, Reason: err.Error(),
	}, false)
}

// projectUpgradeMachine 把一次 status 探测原样投影到 internal/upgrade 的输入。
func projectUpgradeMachine(name string, status *proto.StatusResp, probeErr error) upgrade.Machine {
	m := upgrade.Machine{Name: name, Err: probeErr}
	if status == nil {
		return m
	}
	m.Agentd = status.Version.Version
	m.Revision = status.Version.Revision
	m.Platform = status.Version.Platform
	if status.Update != nil {
		managed := status.Update.Managed
		m.Managed = &managed
		m.Pull = status.Update.Pull
	}
	for _, task := range status.Active {
		if task.State == string(proto.TaskStateRunning) || task.State == string(proto.TaskStateWaitingAnswer) {
			m.Busy++
		}
	}
	return m
}

// machineUpgradePreflight 将共享判据和远端闸翻成一个不执行 I/O 的结果。
// 动作本身仍只由后续的 RemoteOne 执行，避免 202 响应前下载或推送。
func machineUpgradePreflight(m upgrade.Machine, latest string, force bool) upgrade.Result {
	v := upgrade.Classify(m, latest)
	result := upgrade.Result{Verdict: v}
	switch v {
	case upgrade.VerdictUnreachable:
		result.Status = upgrade.StatusFail
		if m.Err != nil {
			result.Reason = m.Err.Error()
		}
	case upgrade.VerdictTooOld:
		result.Status = upgrade.StatusSkip
		result.Reason = "对端 agentd 过旧，未上报平台，需先手工升级到 ≥v0.1.1"
	case upgrade.VerdictLatest:
		result.Status = upgrade.StatusSkip
		result.Reason = "已是最新"
	case upgrade.VerdictUnmanaged:
		result.Status = upgrade.StatusSkip
		result.Reason = "agentd 非托管启动，重启后不会被拉起"
		result.Remedy = "先在该机器上 handoff service install"
	case upgrade.VerdictManagedUnknown:
		result.Status = upgrade.StatusSkip
		result.Reason = "对端未上报托管状态，无法确认换版后能否被拉起"
	case upgrade.VerdictNeedsUpgrade:
		if m.Busy > 0 && !force {
			result.Status = upgrade.StatusSkip
			result.Forcible = true
			result.Reason = fmt.Sprintf("%d 个活跃任务", m.Busy)
			result.Remedy = fmt.Sprintf("handoff upgrade --now --target %s --force", m.Name)
			break
		}
		result.Status = upgrade.StatusOK
	case upgrade.VerdictAgentdDown:
		result.Status = upgrade.StatusFail
		result.Reason = "agentd 未运行"
	}
	return result
}

// machineUpgradeStatusCode 是端点的五种 HTTP 结论映射。
func machineUpgradeStatusCode(result upgrade.Result) int {
	switch result.Verdict {
	case upgrade.VerdictUnreachable:
		return http.StatusBadGateway
	case upgrade.VerdictUnmanaged, upgrade.VerdictTooOld, upgrade.VerdictManagedUnknown:
		return http.StatusUnprocessableEntity
	case upgrade.VerdictLatest:
		return http.StatusOK
	case upgrade.VerdictNeedsUpgrade:
		if result.Status == upgrade.StatusSkip {
			return http.StatusConflict
		}
		return http.StatusAccepted
	default:
		return http.StatusUnprocessableEntity
	}
}

func (s *Server) writeMachineUpgradeResult(w http.ResponseWriter, code int, m upgrade.Machine,
	result upgrade.Result, accepted bool) {
	writeJSON(w, code, proto.MachineUpgradeResp{
		Accepted: accepted, Verdict: result.Verdict.String(), Reason: result.Reason,
		Remedy: result.Remedy, Forcible: result.Forcible, Busy: m.Busy,
	})
}

// runMachineUpgradeBackground 使用独立 context：请求返回后 r.Context 已不可用。
func (s *Server) runMachineUpgradeBackground(name string, m upgrade.Machine, target config.Target,
	rel release.Release, force bool) {
	ctx, cancel := context.WithTimeout(context.Background(), machineUpgradeTimeout)
	defer cancel()
	progress := func(message string) {
		s.log.Info("执行机升级进度", "name", name, "stage", message)
	}
	runner := s.machineUpgradeRunner
	if runner == nil {
		runner = s.executeMachineUpgrade
	}
	result := runner(ctx, m, target, rel, force, progress)
	// 先落终态再打日志：落盘失败会让控制台一直转，比日志缺一行严重
	s.finishMachineUpgrade(name, result)
	if result.Status == upgrade.StatusOK {
		s.log.Info("后台执行机升级成功", "name", name, "from", result.From, "to", result.To)
		return
	}
	s.log.Warn("后台执行机升级结束", "name", name, "status", result.Status.String(),
		"verdict", result.Verdict.String(), "reason", result.Reason)
}

func isLocalMachineName(name string) bool {
	if name == "" || name == "本机" {
		return true
	}
	host, err := os.Hostname()
	return err == nil && name == host
}
