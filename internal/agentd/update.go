// update.go —— POST /api/update：接收推来的二进制，换版并触发重启。
//
// 职责：
//   - 复检两道闸（活跃任务 / 非托管），拒绝时给出可判别的 reason
//   - 校验 sha256、解包、自检、原子换版并保留 .prev
//   - 换版成功后触发优雅关停，由进程管理器拉起新二进制
//
// 边界：
//   - **不出网**：资产由 CLI 下载并推来，这里只收字节（B59 spec D1）
//   - 不做回滚编排：换版失败时 release.Activate 自己把 .prev 换回去，
//     人工回滚是 handoff upgrade --rollback，不在这条路径上
//   - 不做鉴权加码：持有 bearer token 的人本来就能 handoff run 执行任意命令，
//     推二进制不构成提权。token 就是信任边界（spec D4）
package agentd

import (
	"fmt"
	"io"
	"net/http"
	"path/filepath"

	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/selfupdate"
)

// UpdateDeps 是换版接口的外部依赖集合。
//
// 抽成结构体而不是散落的包级变量：这些依赖全都是「会真的动这台机器」的动作
// （执行文件、rename 二进制、停进程），测试必须能整体替换掉，漏替一个就会
// 在 CI 上真的把测试二进制 rename 掉。
type UpdateDeps struct {
	// Getenv 取环境变量，闸二的判据来源
	Getenv func(string) string
	// Executable 返回当前二进制的真实路径（须已 EvalSymlinks）
	Executable func() (string, error)
	// Install 校验+解包+自检，返回可供 Activate 的临时文件路径
	Install func(tgz []byte, wantSum, wantTag, destDir string) (string, error)
	// Activate 原子换版，返回旧二进制的留存路径
	Activate func(newPath, target string) (string, error)
}

// handleUpdate 处理换版请求。
//
// 请求：POST /api/update?tag=&sha256=&force=，body 为 tar.gz 原文；
// **body 为空表示只重启不换版**——本机的二进制由 CLI 直接换掉了，
// 但正在跑的 agentd 仍是旧进程，需要重启才生效（spec D8）。
//
// 注意：
//   - 两道闸的检查顺序是「闸二在前」：非托管是硬拒绝，先说这一条能让操作者
//     少绕一圈（他会先去装服务，而不是先去等任务结束）
//   - 触发关停必须在写完响应之后。优雅关停会等在途请求结束，所以本 handler
//     返回前进程不会退——但反过来先 Trigger 再写响应，客户端拿到的就是一个
//     断掉的连接，等于把一次成功的换版报成失败
func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	force := r.URL.Query().Get("force") != ""
	tag := r.URL.Query().Get("tag")
	sum := r.URL.Query().Get("sha256")
	s.log.Info("换版请求", "tag", tag, "force", force, "content_length", r.ContentLength)

	// 闸二在闸一之前：非托管是硬拒绝（force 也不越过），先说这一条能让操作者
	// 少绕一圈——他会直接去装服务，而不是先去等任务结束再撞第二堵墙
	if !selfupdate.IsManaged(s.upd.Getenv) {
		s.log.Warn("换版被拒：agentd 非托管启动", "tag", tag, "force", force)
		writeJSON(w, http.StatusConflict, proto.UpdateError{
			Error:  "agentd 非托管启动，换版后没有进程管理器把它拉起来",
			Reason: proto.UpdateReasonUnmanaged,
		})
		return
	}

	// 闸一：活跃任务，force 可越过
	busy, err := s.activeCount()
	if err != nil {
		s.log.Error("换版预检：查任务列表失败", "cause", err)
		writeJSON(w, http.StatusInternalServerError, proto.UpdateError{Error: "内部错误"})
		return
	}
	if busy > 0 && !force {
		s.log.Warn("换版被拒：有活跃任务", "tag", tag, "busy", busy)
		writeJSON(w, http.StatusConflict, proto.UpdateError{
			Error:  fmt.Sprintf("有 %d 个活跃任务（running/waiting_answer）", busy),
			Reason: proto.UpdateReasonBusy,
		})
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxUpdateBytes))
	if err != nil {
		s.log.Error("换版：读请求体失败", "tag", tag, "cause", err)
		writeJSON(w, http.StatusBadRequest, proto.UpdateError{Error: "读请求体: " + err.Error()})
		return
	}

	// 纯重启模式（D8）
	if len(body) == 0 {
		s.log.Info("换版：body 为空，只重启不换版", "busy", busy)
		writeJSON(w, http.StatusOK, proto.UpdateResp{OK: true, Restarted: true})
		s.triggerRestart("收到重启请求")
		return
	}

	if tag == "" || sum == "" {
		s.log.Warn("换版被拒：带 body 但缺 tag 或 sha256", "tag", tag, "has_sum", sum != "")
		writeJSON(w, http.StatusBadRequest, proto.UpdateError{
			Error: "带二进制时 tag 与 sha256 都必须给：缺了它们无从校验完整性，也无从自检",
		})
		return
	}

	target, err := s.upd.Executable()
	if err != nil {
		s.log.Error("换版：取当前二进制路径失败", "tag", tag, "cause", err)
		writeJSON(w, http.StatusInternalServerError, proto.UpdateError{Error: "取当前二进制路径: " + err.Error()})
		return
	}
	// 临时文件必须与目标同目录：os.Rename 的原子性只在同一文件系统内成立
	s.log.Info("换版：开始校验与解包", "tag", tag, "target", target, "bytes", len(body))
	newPath, err := s.upd.Install(body, sum, tag, filepath.Dir(target))
	if err != nil {
		s.log.Error("换版被拒：校验或自检未通过", "tag", tag, "cause", err)
		writeJSON(w, http.StatusBadRequest, proto.UpdateError{Error: err.Error()})
		return
	}
	prev, err := s.upd.Activate(newPath, target)
	if err != nil {
		s.log.Error("换版失败：替换二进制出错", "tag", tag, "target", target, "cause", err)
		writeJSON(w, http.StatusInternalServerError, proto.UpdateError{Error: "替换二进制: " + err.Error()})
		return
	}

	s.log.Info("换版完成，准备重启", "tag", tag, "target", target, "prev", prev, "busy", busy)
	writeJSON(w, http.StatusOK, proto.UpdateResp{OK: true, Version: tag, Prev: prev, Restarted: true})
	s.triggerRestart("换版到 " + tag)
}

// activeCount 返回活跃任务数（running + waiting_answer）。
//
// waiting_review 不计入：它在等审核者裁决，挂几天都正常，计入等于让升级
// 被无限期阻塞（沿用 B54.3 的 D12）。
func (s *Server) activeCount() (int, error) {
	tasks, err := s.st.ListTasks()
	if err != nil {
		return 0, fmt.Errorf("列任务: %w", err)
	}
	n := 0
	for _, t := range tasks {
		if t.State == proto.TaskStateRunning || t.State == proto.TaskStateWaitingAnswer {
			n++
		}
	}
	return n, nil
}

// triggerRestart 触发优雅关停。restart 未注入时只打日志——这只可能发生在
// 测试或 bootstrap 顺序出错时，静默返回会让「换版成功但永远不重启」变成
// 一个查不出根因的现象。
func (s *Server) triggerRestart(reason string) {
	if s.restart == nil {
		s.log.Error("换版后无法触发重启：restart 未注入", "reason", reason)
		return
	}
	if !s.restart(reason) {
		s.log.Warn("触发重启：已在关停中，本次触发被忽略", "reason", reason)
	}
}
