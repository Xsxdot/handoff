// update.go —— POST /api/update：接收推来的二进制，换版并触发重启。
//
// 职责：
//   - 复检两道闸（活跃任务 / 非托管），拒绝时给出可判别的 reason
//   - 校验 sha256、解包、自检、原子换版并保留 .prev
//   - 换版成功后触发优雅关停，由进程管理器拉起新二进制
//
// 边界：
//   - push 模式（body 非空）不出网：资产由 CLI 下载并推来，这里只收字节。
//     pull 模式（mode=pull，B87 新增）相反：agentd 自己出网按 tag 下载
//   - 不做回滚编排：换版失败时 release.Activate 自己把 .prev 换回去，
//     人工回滚是 handoff upgrade --rollback，不在这条路径上
//   - 不做鉴权加码：持有 bearer token 的人本来就能 handoff run 执行任意命令，
//     推二进制不构成提权。token 就是信任边界（spec D4）
package agentd

import (
	"context"
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
	// FetchByTag 按 tag 下载本机平台的资产并用 wantSum 校验。自拉模式专用。
	//
	// 为什么是 tag 而不是 release.Release：agentd **不查 GitHub API**——
	// 资产地址是确定性的（release.AssetURL），而 api.github.com 有
	// 60 次/小时/IP 的匿名限流，多台执行机很可能共用一个代理出口 IP
	FetchByTag func(ctx context.Context, tag, goos, goarch, wantSum string) ([]byte, error)
	// Platform 返回本机 goos/goarch。抽成缝只为可测：写死 runtime.GOOS 时
	// "按本机平台取资产名"这条行为在单一平台的 CI 上验不出来
	Platform func() (string, string)
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

	mode := r.URL.Query().Get("mode")
	switch mode {
	case "", proto.UpdateModePush, proto.UpdateModePull:
	default:
		// 不静默降级成某个默认模式：猜错的代价是装错东西，或者白重启一次
		// 而调用方以为换版成功了
		s.log.Warn("换版被拒：未知 mode", "mode", mode, "tag", tag)
		writeJSON(w, http.StatusBadRequest, proto.UpdateError{
			Error: fmt.Sprintf("未知 mode %q，只支持 %q 与 %q（省略 mode 时按 body 空不空判别）",
				mode, proto.UpdateModePull, proto.UpdateModePush),
		})
		return
	}

	// 显式 mode 与 body 必须自洽。两种模式的意图互斥，不做"猜一个"的兼容：
	// 调用方说了 push 却没带字节、说了 pull 却带了字节，都是 bug，
	// 静默按另一种模式处理会让那个 bug 以"换版成功了"的样子存活下去
	if mode == proto.UpdateModePush && len(body) == 0 {
		s.log.Warn("换版被拒：mode=push 但 body 为空", "tag", tag)
		writeJSON(w, http.StatusBadRequest, proto.UpdateError{
			Error: "mode=push 必须带 tar.gz 原文；只想重启请省略 mode 并发空 body",
		})
		return
	}
	if mode == proto.UpdateModePull && len(body) > 0 {
		s.log.Warn("换版被拒：mode=pull 但带了 body", "tag", tag, "bytes", len(body))
		writeJSON(w, http.StatusBadRequest, proto.UpdateError{
			Error: "mode=pull 由 agentd 自己下载，不接受 body",
		})
		return
	}

	if mode == proto.UpdateModePull {
		s.handlePullUpdate(w, tag, sum, busy)
		return
	}

	// 纯重启模式（D8）
	if len(body) == 0 {
		s.log.Info("换版：body 为空，只重启不换版", "busy", busy)
		writeJSON(w, http.StatusOK, proto.UpdateResp{OK: true, Restarted: true})
		s.triggerRestart("收到重启请求")
		return
	}

	// push 与自拉同抢 release.TempName(tag) 这个确定性临时文件路径：自拉在跑时
	// 再受理 push，两个换版会往同一个文件写、互相截断出一个坏二进制
	if s.pull.busy() {
		s.log.Warn("换版被拒：已有自拉换版在进行中", "tag", tag)
		writeJSON(w, http.StatusConflict, proto.UpdateError{
			Error:  "已有自拉换版在进行中，等它跑完再推送；去 status 看 pull_state",
			Reason: proto.UpdateReasonPullInProgress,
		})
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

// handlePullUpdate 处理自拉换版：校验参数、抢并发锁、受理后交给后台。
//
// 参数：
//   - tag / sum: 协调者下发的目标版本与期望 sha256，**必须成对**
//   - busy: 已统计出的活跃任务数，只用于日志
//
// 注意：
//   - 两道闸已由调用方检查过，这里不重复
func (s *Server) handlePullUpdate(w http.ResponseWriter, tag, sum string, busy int) {
	if tag == "" || sum == "" {
		s.log.Warn("自拉被拒：缺 tag 或 sha256", "tag", tag, "has_sum", sum != "")
		writeJSON(w, http.StatusBadRequest, proto.UpdateError{
			Error: "mode=pull 时 tag 与 sha256 都必须给：缺了它们无从校验完整性，也无从自检",
		})
		return
	}
	if !s.pull.begin(tag) {
		// force 不越过这一条：两个自拉会往同一个临时文件路径写
		//（release.TempName(tag) 是确定性的），互相截断出一个坏二进制
		cur := s.pull.snapshot()
		s.log.Warn("自拉被拒：已有一个自拉在跑", "tag", tag, "current", cur)
		writeJSON(w, http.StatusConflict, proto.UpdateError{
			Error:  "已有一个自拉换版在进行中，去 status 看 pull_state",
			Reason: proto.UpdateReasonPullInProgress,
		})
		return
	}
	s.log.Info("自拉换版已受理", "tag", tag, "sha256", sum, "busy", busy)
	writeJSON(w, http.StatusAccepted, proto.UpdateResp{OK: true, Accepted: true, Version: tag})
	// 必须在写完响应之后再起 goroutine——与 triggerRestart 同一条纪律：
	// 先动作后响应会让客户端拿到一个断掉的连接
	go s.runPull(tag, sum)
}

// runPull 在后台执行一次自拉换版：下载 → 安装（校验+解包+自检）→ 换版 → 重启。
//
// 参数：
//   - tag: 目标版本
//   - sum: 协调者下发的期望 sha256。**不是本机自己取的**——校验和与资产走
//     两条不同的信任路径，本机代理/镜像被投毒时才抓得住（spec §5.5）
//
// 注意：
//   - 由 handlePullUpdate 在抢到 pull 锁之后起。任何失败路径都必须调
//     s.pull.fail 释放锁，否则这台 agentd 从此再也不能自拉
//   - 成功路径不释放锁：换版成功即触发重启，进程整个换掉
func (s *Server) runPull(tag, sum string) {
	ctx := s.pullBaseCtx
	if ctx == nil {
		ctx = context.Background()
	}
	goos, goarch := s.upd.Platform()
	s.log.Info("自拉换版开始", "tag", tag, "platform", goos+"/"+goarch, "sha256", sum)

	s.pull.stage(proto.PullStageDownloading)
	tgz, err := s.upd.FetchByTag(ctx, tag, goos, goarch, sum)
	if err != nil {
		s.log.Error("自拉换版失败：下载或校验不过", "tag", tag,
			"platform", goos+"/"+goarch, "cause", err)
		s.pull.fail(err)
		return
	}
	s.log.Info("自拉换版：资产已就绪", "tag", tag, "bytes", len(tgz))

	target, err := s.upd.Executable()
	if err != nil {
		s.log.Error("自拉换版失败：取当前二进制路径", "tag", tag, "cause", err)
		s.pull.fail(err)
		return
	}

	s.pull.stage(proto.PullStageInstalling)
	// 临时文件必须与目标同目录：os.Rename 的原子性只在同一文件系统内成立
	newPath, err := s.upd.Install(tgz, sum, tag, filepath.Dir(target))
	if err != nil {
		s.log.Error("自拉换版失败：校验或自检未通过", "tag", tag, "target", target, "cause", err)
		s.pull.fail(err)
		return
	}
	prev, err := s.upd.Activate(newPath, target)
	if err != nil {
		s.log.Error("自拉换版失败：替换二进制出错", "tag", tag, "target", target, "cause", err)
		s.pull.fail(err)
		return
	}

	s.log.Info("自拉换版完成，准备重启", "tag", tag, "target", target, "prev", prev)
	s.triggerRestart("自拉换版到 " + tag)
}

// activeCount 返回活跃任务数（running + waiting_answer）。
//
// waiting_review 不计入：它在等协调者裁决，挂几天都正常，计入等于让升级
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
