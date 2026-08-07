// adapter.go —— opencode 语义到 executor.Adapter 契约的翻译层。
//
// 职责：
//   - 把 StartServe/CreateSession/PromptAsync/SubscribeEvents 编排成 Adapter 的
//     五动作：Start（环境物料 → serve → 会话 → 初始 prompt → 订阅映射）、
//     Send（同一会话续接）、RespondPermission（权限应答转发）、
//     Stop（kill serve + 关事件流）、Events（事件通道）
//   - SSE 事件 → AdapterEvent 映射：permission 类 → permission 事件；消息文本
//     增量累积 → render.log 追加 + 节流 progress；session idle → ParseTrailer
//     分类（ask/finish/none 兜底 git 实况裁决）；serve 死亡 → failed result
//   - 可见性：回合文本增量追加到 <taskDir>/render.log，供 tmux 第二窗口
//     `tail -f <taskDir>/render.log` 旁观模型执行
//
// 边界：
//   - 不写 store、不做审批判断（见 executor.go 包级边界）：会话 id 等一切持久化
//     诉求经事件（progress「会话就绪」/ Result.SessionID）或返回值交给 manager 落库
//   - 不做任务状态机迁移：6 状态迁移完全由 manager 负责，本层只产事件、收指令
//   - 不重试、不决策：SSE 解析宽容（未知事件 Debug 跳过、绝不 panic）；
//     trailer 缺失时兜底只做「是否有新提交」的事实裁决，没有新提交就交审核者
//   - 文本累积只依赖 message.updated 携带 role（可区分 user/assistant 归零回合）；
//     message.part.updated 无 role 字段、当前整类 Debug 跳过。若真实冒烟证实
//     文本流主要走 part.updated 且 message.updated 无 parts，回合文本会恒空
//     （idle 空回合已 Warn 可见），届时扩展 part.updated 累积路径
//     ——Task 12 e2e 的验证项
package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/xushixin/handoff/internal/executor"
)

// 看门狗与节流参数：
//   - watchdogProbeInterval/watchdogFailThreshold：serve 存活探活节奏，连续
//     3 次死亡（约 600ms 窗口）才判定死亡，吸收探活抖动，不误杀正常任务
//   - progressThrottle：progress 事件节流，同一回合至多每 30s 一条，
//     防止高频文本增量刷爆事件库
const (
	watchdogProbeInterval = 200 * time.Millisecond
	watchdogFailThreshold = 3
	progressThrottle      = 30 * time.Second
)

// serveHandle 抽象 serve 进程的存活/销毁/诊断：真实实现是 *Proc（procHandle），
// 测试注入假探活，绕开 tmux 依赖。
type serveHandle interface {
	Alive() bool
	Kill() error
	PaneTail() string
}

// procHandle 把 *Proc 适配成 serveHandle（PaneTail 透传未导出的 capturePaneTail）。
type procHandle struct{ p *Proc }

func (h procHandle) Alive() bool      { return h.p.Alive() }
func (h procHandle) Kill() error      { return h.p.Kill() }
func (h procHandle) PaneTail() string { return h.p.capturePaneTail() }

// Adapter 是 opencode 的 executor.Adapter 实现（语义翻译层）。
//
// 并发安全：runs 表由 mu 保护；每个任务的运行态（回合累积、事件通道）只被
// 该任务自己的订阅 goroutine 访问，不做跨任务共享。
type Adapter struct {
	log  *slog.Logger
	mu   sync.Mutex
	runs map[string]*runState // taskID -> 运行态
}

// New 创建 opencode adapter。
//
// 参数：
//   - log: 本模块日志入口（nil 时退回 slog.Default()）
func New(log *slog.Logger) *Adapter {
	if log == nil {
		log = slog.Default()
	}
	return &Adapter{log: log, runs: make(map[string]*runState)}
}

// runState 是单任务运行的完整状态。
//
// turn/msgSeen/lastProgress 只被订阅 goroutine 读写（SSE 回调同步执行），
// 无需加锁；stopCh/evCh 的关闭权有明确归属（见 subscribeLoop/Stop）。
type runState struct {
	taskID       string
	taskDir      string
	repoPath     string
	session      string
	api          *API
	handle       serveHandle
	runCtx       context.Context
	runCancel    context.CancelFunc
	evCh         chan executor.AdapterEvent
	stopCh       chan struct{}
	stopOnce     sync.Once
	closeOnce    sync.Once
	renderPath   string
	startCommit  string            // 任务起点 commit（兜底分类的基线）
	turn         string            // 当前回合累积文本
	msgSeen      map[string]string // messageID -> 已见文本（增量对账）
	lastProgress time.Time         // 上次发 progress 的时刻（节流）
}

// newRun 创建并登记一个任务的运行态。
func (a *Adapter) newRun(taskID, taskDir, repoPath string) *runState {
	r := &runState{
		taskID:     taskID,
		taskDir:    taskDir,
		repoPath:   repoPath,
		evCh:       make(chan executor.AdapterEvent, 16),
		stopCh:     make(chan struct{}),
		renderPath: filepath.Join(taskDir, "render.log"),
		msgSeen:    make(map[string]string),
	}
	r.runCtx, r.runCancel = context.WithCancel(context.Background())
	a.mu.Lock()
	a.runs[taskID] = r
	a.mu.Unlock()
	return r
}

// lookup 按任务 id 取运行态。
func (a *Adapter) lookup(taskID string) *runState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.runs[taskID]
}

// drop 注销一个任务的运行态（启动失败清理用）。
func (a *Adapter) drop(taskID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.runs, taskID)
}

// Start 按「WriteTaskEnv → StartServe → 建会话 → 初始 prompt → 订阅映射」
// 流程启动任务执行并立即返回。
//
// 参数：
//   - ctx: 控制启动阶段的超时/取消（CreateSession/PromptAsync 受其约束）；
//     不代表执行生命周期（执行延续到 Stop）
//   - req: 任务快照、计划原文与任务工作目录
//
// 返回：
//   - 任一启动阶段失败（环境物料生成/serve 启动/建会话/发 prompt）返回错误，
//     调用方（manager）应把任务标记 failed
//
// 注意：
//   - serve 已拉起但后续阶段失败时自动 Kill 清理 tmux 残留，避免半启动进程占端口
func (a *Adapter) Start(ctx context.Context, req executor.StartReq) (err error) {
	a.log.Info("adapter 开始启动任务", "task", req.Task.ID,
		"task_dir", req.TaskDir, "repo", req.Task.RepoPath)
	defer func() {
		if err != nil {
			a.log.Error("adapter 启动任务失败", "task", req.Task.ID, "cause", err)
		}
	}()

	configPath, _, err := WriteTaskEnv(req.TaskDir, req.Task.ID, req.PlanContent)
	if err != nil {
		return err
	}
	proc, err := StartServe(ctx, req.Task.RepoPath, req.Task.ID, configPath, a.log)
	if err != nil {
		return err
	}
	a.log.Info("opencode serve 已启动", "task", req.Task.ID,
		"port", proc.Port, "tmux", proc.TmuxSession)
	// serve 连接凭据落盘（serve.json）：agentd 重启后 RecoverOnStartup 凭它探活
	// 与重建订阅；写失败不阻断启动（缺失时重启恢复按「执行器已不在」处理）
	if err := writeServeInfo(req.TaskDir, proc); err != nil {
		a.log.Warn("写 serve 连接凭据失败，重启恢复将不可用", "task", req.Task.ID, "cause", err)
	}
	api := NewAPI(fmt.Sprintf("http://127.0.0.1:%d", proc.Port), proc.Password)
	if _, err := a.startRun(ctx, req, api, procHandle{p: proc}); err != nil {
		if kerr := proc.Kill(); kerr != nil {
			a.log.Warn("清理 serve 残留失败", "task", req.Task.ID, "cause", kerr)
		}
		return err
	}
	return nil
}

// startRun 完成「建会话 → 发初始 prompt → 记录起点 commit → 启动订阅与看门狗」。
//
// 这是 Start 的内部骨架，单独成函数以便测试注入 httptest server 与假探活
// （免真实 tmux/opencode 二进制）。
//
// 返回：
//   - sessionID: 新建的 opencode 会话 id（经 Result.SessionID 随结果事件上报，
//     供 manager 落 task.ExecutorSession）
//   - err: 建会话/发 prompt 失败；失败时运行态已注销
func (a *Adapter) startRun(ctx context.Context, req executor.StartReq, api *API, handle serveHandle) (sessionID string, err error) {
	r := a.newRun(req.Task.ID, req.TaskDir, req.Task.RepoPath)
	r.api = api
	r.handle = handle
	a.log.Info("adapter 启动运行", "task", r.taskID, "task_dir", r.taskDir, "repo", r.repoPath)
	defer func() {
		if err != nil {
			a.log.Error("adapter 启动运行失败", "task", r.taskID, "cause", err)
			r.runCancel()
			a.drop(r.taskID)
		} else {
			a.log.Info("adapter 运行已启动", "task", r.taskID, "session", sessionID)
		}
	}()

	sessionID, err = api.CreateSession(ctx)
	if err != nil {
		return "", err
	}
	r.session = sessionID
	a.log.Info("opencode 会话已建", "task", r.taskID, "session", sessionID)

	// 「会话就绪」信号：建会话成功立即带 SessionID 发一条 progress——manager 据此
	// 落 task.ExecutorSession。为什么不用 result 做唯一通道：审核主路径常以
	// question 收尾、result 永不出现；Task 12 重启恢复要拿会话 id 重建 SSE，
	// 必须让它在首个事件就到 manager。progress 只入库不阻塞，零风险。
	a.emit(r, executor.AdapterEvent{Type: "progress", SessionID: sessionID, Text: "会话就绪"})

	// 初始 prompt 取 WriteTaskEnv 生成的 prompt.md（回合制纪律模板渲染产物）
	promptPath := filepath.Join(r.taskDir, promptFileName)
	promptContent, err := os.ReadFile(promptPath)
	if err != nil {
		return "", fmt.Errorf("读取任务 prompt %s: %w", promptPath, err)
	}
	if err := api.PromptAsync(ctx, sessionID, string(promptContent)); err != nil {
		return "", err
	}
	a.log.Info("opencode 初始 prompt 已发", "task", r.taskID,
		"session", sessionID, "prompt_len", len(promptContent))

	// 记录任务起点 commit：兜底分类用 git 实况裁决「是否有新提交」的基线；
	// 非 git 仓库或查询失败时留空，兜底一律按无新提交处理（转提问，不卡死）
	r.captureStartCommit(a)

	go r.subscribeLoop(a)
	go a.watchdog(r)
	return sessionID, nil
}

// captureStartCommit 记录任务起点 commit：git 兜底分类（fallbackClassify）用
// 「是否有新提交」裁决的基线；非 git 仓库或查询失败时留空，兜底一律按无新提交处理。
//
// 由 startRun 与 Resume 复用：两者都需在订阅启动前定下本回合的 git 基线。
func (r *runState) captureStartCommit(a *Adapter) {
	if out, err := exec.Command("git", "-C", r.repoPath, "rev-parse", "HEAD").Output(); err != nil {
		a.log.Warn("捕获任务起点 commit 失败，兜底按无新提交处理",
			"task", r.taskID, "repo", r.repoPath, "cause", err)
	} else {
		r.startCommit = strings.TrimSpace(string(out))
	}
}

// Resume 重建 agentd 重启前已在执行的任务（spec §8「存活则重连 SSE 继续」）：
// 从任务目录的 serve.json 恢复 serve 连接凭据并探活（tmux 会话存在 + HTTP 应答）；
// 存活则重建 SSE 订阅、看门狗与事件通道，返回 true。
//
// 参数：
//   - taskID: 任务 ID（tmux 会话名按 handoff-<id8> 确定性推导，与 StartServe 同规则）
//   - taskDir: 任务目录（serve.json 所在，即 DataDir/tasks/<id>）
//   - repoPath: 任务仓库路径（重启后重新捕获 git 兜底分类的起点 commit 基线）
//   - sessionID: 既有 opencode 会话 id（即 task.ExecutorSession）
//
// 返回：
//   - alive: 执行器是否存活；false 时调用方（manager）应把任务迁移
//     failed/waiting_review 交审核者裁决
//   - err: 重建失败（serve.json 缺失/损坏、sessionID 为空），此时视为不可恢复
//
// 注意：
//   - sessionID 为空时拒绝恢复：mapEvent 按会话 id 过滤事件，空 id 会把全部
//     事件当「其他会话」丢弃，静默恢复等于无声断流，宁可交审核者裁决
//   - 重启时正在进行的回合文本累积在内存里已丢失：重建后的回合从 SSE 重放的新
//     快照重新累积（msgSeen 重新对账），idle 分类的 git 基线以重启时刻的 HEAD
//     为准——这是 MVP 接受的缝隙，由 e2e 清单「agentd 重启」项实测观察
//   - 与 Start 的对称性：Stop（done 归档）对恢复出来的运行态同样有效，
//     会 kill 掉 tmux 会话回收资源
func (a *Adapter) Resume(taskID, taskDir, repoPath, sessionID string) (alive bool, err error) {
	a.log.Info("adapter 恢复任务执行", "task", taskID, "session", sessionID)
	defer func() {
		if err != nil {
			a.log.Error("adapter 恢复任务失败", "task", taskID, "cause", err)
		} else if alive {
			a.log.Info("adapter 任务已恢复", "task", taskID, "session", sessionID)
		}
	}()

	if sessionID == "" {
		return false, fmt.Errorf("任务 %s 缺 executor_session，无法重建订阅", taskID)
	}
	si, err := readServeInfo(taskDir)
	if err != nil {
		return false, err
	}
	proc := &Proc{Port: si.Port, Password: si.Password, TmuxSession: si.TmuxSession}
	if !proc.Alive() {
		a.log.Info("恢复探活失败：执行器已不在", "task", taskID, "tmux", proc.TmuxSession)
		return false, nil
	}
	r := a.newRun(taskID, taskDir, repoPath)
	r.session = sessionID
	r.api = NewAPI(fmt.Sprintf("http://127.0.0.1:%d", proc.Port), proc.Password)
	r.handle = procHandle{p: proc}
	r.captureStartCommit(a)
	go r.subscribeLoop(a)
	go a.watchdog(r)
	return true, nil
}

// Events 返回任务的事件流通道（Start 后可用；Stop 或执行终结后关闭）。
func (a *Adapter) Events(taskID string) <-chan executor.AdapterEvent {
	if r := a.lookup(taskID); r != nil {
		return r.evCh
	}
	return nil
}

// Send 向同一会话续发指令（原生续接：上下文完整保留）。
//
// 参数：
//   - text: 审核者的回答/修改指令，原样透传，不得加工
func (a *Adapter) Send(ctx context.Context, taskID, text string) (err error) {
	r := a.lookup(taskID)
	if r == nil {
		return fmt.Errorf("任务 %s 不在运行中", taskID)
	}
	a.log.Info("adapter 收到续接指令", "task", taskID, "text", truncateRunes(text, 80))
	defer func() {
		if err != nil {
			a.log.Error("adapter 续接指令发送失败", "task", taskID, "cause", err)
		} else {
			a.log.Info("adapter 续接指令已发送", "task", taskID, "session", r.session)
		}
	}()
	return r.api.PromptAsync(ctx, r.session, text)
}

// RespondPermission 把审核者的权限裁决转发给 opencode server。
//
// 参数：
//   - permID: 与 permission 事件中的 PermissionID 一致（manager 的 ticket id）
//   - decision: "once"（批准本次）或 "reject"（拒绝）
func (a *Adapter) RespondPermission(ctx context.Context, taskID, permID, decision string) (err error) {
	r := a.lookup(taskID)
	if r == nil {
		return fmt.Errorf("任务 %s 不在运行中", taskID)
	}
	a.log.Info("adapter 收到权限应答", "task", taskID, "perm", permID, "decision", decision)
	defer func() {
		if err != nil {
			a.log.Error("adapter 权限应答转发失败", "task", taskID, "perm", permID, "cause", err)
		} else {
			a.log.Info("adapter 权限应答已转发", "task", taskID, "perm", permID)
		}
	}()
	return r.api.RespondPermission(ctx, r.session, permID, decision)
}

// Stop 终止任务执行：取消订阅 → kill serve（tmux 会话）→ 事件通道关闭。
//
// 注意：
//   - 幂等：重复 Stop 不 panic；事件通道只关闭一次（由订阅 goroutine 持有关闭权）
func (a *Adapter) Stop(taskID string) (err error) {
	r := a.lookup(taskID)
	if r == nil {
		return fmt.Errorf("任务 %s 不在运行中", taskID)
	}
	a.log.Info("adapter 停止任务", "task", taskID)
	defer func() {
		if err != nil {
			a.log.Error("adapter 停止任务失败", "task", taskID, "cause", err)
		} else {
			a.log.Info("adapter 任务已停止", "task", taskID)
		}
	}()
	// 先关 stopCh（让 emit 让路）再取消运行 ctx（打断 SSE 订阅），最后 kill 进程
	r.stopOnce.Do(func() {
		close(r.stopCh)
		r.runCancel()
	})
	if r.handle != nil {
		if kerr := r.handle.Kill(); kerr != nil {
			return fmt.Errorf("kill serve: %w", kerr)
		}
	}
	return nil
}

// subscribeLoop 是单任务的事件流主循环（唯一持有 evCh 关闭权的 goroutine）：
// 订阅 SSE → mapEvent 逐条映射产出 AdapterEvent；订阅退出后按退出原因
// （serve 死亡 / 流不可恢复）产出 failed 结果，随后关闭事件通道。
func (r *runState) subscribeLoop(a *Adapter) {
	defer func() {
		r.closeOnce.Do(func() { close(r.evCh) })
		a.log.Info("opencode 事件流订阅退出", "task", r.taskID)
	}()
	err := r.api.SubscribeEvents(r.runCtx, func(raw json.RawMessage) {
		a.mapEvent(r, raw)
	})
	select {
	case <-r.stopCh:
		return // 正常 Stop 关停：静默退出，不产事件
	default:
	}
	if !r.handle.Alive() {
		// 看门狗判定 serve 死亡后已取消 runCtx：订阅随连接断开而退出，
		// 此处产出 failed 结果，让审核者看到死亡现场（含 stderr 尾部）
		tail := tailRunes(r.handle.PaneTail(), 200)
		a.log.Error("opencode serve 已退出", "task", r.taskID, "stderr_tail", tail)
		a.emit(r, executor.AdapterEvent{Type: "result", Result: &executor.Result{
			OK: false, SessionID: r.session,
			FailReason: "opencode serve 已退出: " + tail,
		}})
		return
	}
	a.log.Warn("opencode 事件流意外中断，按失败结束回合", "task", r.taskID, "cause", err)
	a.emit(r, executor.AdapterEvent{Type: "result", Result: &executor.Result{
		OK: false, SessionID: r.session,
		FailReason: "opencode 事件流意外中断: " + tailRunes(fmt.Sprint(err), 200),
	}})
}

// watchdog 是 serve 存活看门狗：周期探活，连续 3 次失败判定 serve 死亡，
// 取消运行 ctx 让订阅退出——failed 结果由 subscribeLoop 统一产出（保持
// 「事件通道只有一个写入者」的关闭权约定）。
func (a *Adapter) watchdog(r *runState) {
	failures := 0
	ticker := time.NewTicker(watchdogProbeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.stopCh:
			return
		case <-r.runCtx.Done():
			return
		case <-ticker.C:
			if r.handle.Alive() {
				failures = 0
				continue
			}
			failures++
			if failures >= watchdogFailThreshold {
				a.log.Error("opencode serve 探活失败达阈值，判定死亡",
					"task", r.taskID, "failures", failures)
				r.runCancel()
				return
			}
		}
	}
}

// emit 向事件通道投递一条 AdapterEvent 并打产出日志（所有事件统一的出口）。
//
// 返回 false 表示 Stop 已关闭 stopCh，事件未被投递。
func (a *Adapter) emit(r *runState, ev executor.AdapterEvent) bool {
	switch ev.Type {
	case "permission":
		a.log.Info("adapter 产出权限事件", "task", r.taskID, "type", ev.Type,
			"perm", ev.PermissionID, "text", truncateRunes(ev.Text, 80))
	case "question":
		a.log.Info("adapter 产出提问事件", "task", r.taskID, "type", ev.Type,
			"text", truncateRunes(ev.Text, 80))
	case "progress":
		a.log.Info("adapter 产出进度事件", "task", r.taskID, "type", ev.Type,
			"text", truncateRunes(ev.Text, 80))
	case "result":
		ok := ev.Result != nil && ev.Result.OK
		a.log.Info("adapter 产出结果事件", "task", r.taskID, "type", ev.Type, "ok", ok)
	default:
		a.log.Info("adapter 产出未知事件", "task", r.taskID, "type", ev.Type)
	}
	select {
	case r.evCh <- ev:
		return true
	case <-r.stopCh:
		return false
	}
}

// sseEvent 是 SSE 事件的通用外壳：type 区分事件类别，properties 是各类载荷，
// sessionID 用于多任务并发时的会话隔离（真实 /event 是全服务器广播流）。
type sseEvent struct {
	Type       string          `json:"type"`
	Properties json.RawMessage `json:"properties"`
	SessionID  string          `json:"sessionID"`
}

// mapEvent 把一条 SSE 事件映射为 0~N 条 AdapterEvent（宽容解析：未知事件
// Debug 跳过，绝不 panic、绝不中断订阅）。
func (a *Adapter) mapEvent(r *runState, raw json.RawMessage) {
	var ev sseEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		a.log.Debug("SSE 事件解析失败，跳过", "task", r.taskID, "cause", err)
		return
	}
	// 会话隔离：真实 /event 广播全服务器事件，只处理本任务会话的事件；
	// 无 sessionID 字段的事件（如 server.connected）不做过滤
	if ev.SessionID != "" && ev.SessionID != r.session {
		a.log.Debug("收到其他会话事件，跳过", "task", r.taskID,
			"type", ev.Type, "session", ev.SessionID)
		return
	}
	switch {
	case strings.Contains(ev.Type, "permission"):
		a.mapPermission(r, ev.Properties)
	case ev.Type == "message.updated":
		a.mapMessageUpdated(r, ev.Properties)
	case ev.Type == "session.idle":
		a.mapIdle(r, raw)
	case ev.Type == "session.status":
		a.mapSessionStatus(r, ev.Properties)
	case ev.Type == "session.error":
		a.log.Warn("opencode 会话报错", "task", r.taskID,
			"properties", truncateRunes(string(ev.Properties), 200))
	default:
		a.log.Debug("未知 SSE 事件，跳过", "task", r.taskID, "type", ev.Type)
	}
}

// mapPermission 处理 permission 类事件（如 permission.updated）：
// 提取 permissionID 与工具/参数描述，产出 permission 事件。
//
// PermissionID 即 manager 的 ticket id（稳定幂等：SSE 重连重放同一权限请求
// 时复用同一 id，CreateTicket 按 id 去重）。
func (a *Adapter) mapPermission(r *runState, props json.RawMessage) {
	var pr struct {
		ID      string `json:"id"`
		Request struct {
			Description string `json:"description"`
			Tool        string `json:"tool"`
			Arguments   any    `json:"arguments"`
		} `json:"request"`
	}
	if err := json.Unmarshal(props, &pr); err != nil {
		a.log.Debug("permission 载荷解析失败，跳过", "task", r.taskID, "cause", err)
		return
	}
	if pr.ID == "" {
		a.log.Debug("permission 事件缺 id，跳过", "task", r.taskID)
		return
	}
	// 描述优先；缺描述时退回「工具名 + 参数」拼凑，保证审核者至少能看到在请求什么
	text := pr.Request.Description
	if text == "" {
		text = pr.Request.Tool
		if args, err := json.Marshal(pr.Request.Arguments); err == nil && string(args) != "null" {
			text += " " + string(args)
		}
	}
	a.emit(r, executor.AdapterEvent{
		Type: "permission", PermissionID: pr.ID, Text: truncateRunes(text, 200),
	})
}

// mapMessageUpdated 处理 message.updated：user 消息开启新回合（清空累积），
// assistant 消息把文本增量追加进回合缓冲、写 render.log、节流发 progress。
//
// why（回合文本累积）：
//   - 服务端可能对同一消息反复发 message.updated（快照逐次增长），直接 append
//     全文会重复累积；按 messageID 记录已见文本，只追加「已见前缀之后」的
//     增量（TrimPrefix），幂等且不丢字
//   - user 消息（初始 prompt / Send 续接）到来即清空回合缓冲：模型的新一轮
//     输出从零开始，上一回合的残留文本不会污染本轮 trailer 判定
func (a *Adapter) mapMessageUpdated(r *runState, props json.RawMessage) {
	var msg struct {
		ID    string `json:"id"`
		Role  string `json:"role"`
		Parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"parts"`
	}
	if err := json.Unmarshal(props, &msg); err != nil {
		a.log.Debug("message.updated 载荷解析失败，跳过", "task", r.taskID, "cause", err)
		return
	}
	if msg.Role == "user" {
		r.clearTurn()
		return
	}
	if msg.Role != "assistant" {
		a.log.Debug("忽略非 assistant 消息", "task", r.taskID, "role", msg.Role, "msg", msg.ID)
		return
	}
	var text string
	for _, p := range msg.Parts {
		if p.Type == "text" {
			text += p.Text
		}
	}
	if text == "" {
		return
	}
	seen := r.msgSeen[msg.ID]
	delta := strings.TrimPrefix(text, seen)
	if delta == "" {
		return // 同一条消息的重复快照，无新增
	}
	if seen != "" && !strings.HasPrefix(text, seen) {
		// 快照被服务端改写（与已见不一致）：放弃增量对账，直接按全文累积
		a.log.Debug("消息快照与已见文本不一致，按全文累积", "task", r.taskID, "msg", msg.ID)
	}
	r.msgSeen[msg.ID] = text
	r.turn += delta
	if err := r.appendRender(delta); err != nil {
		a.log.Warn("追加 render.log 失败", "task", r.taskID, "cause", err)
	}
	a.maybeProgress(r)
}

// mapSessionStatus 处理 session.status：status=idle 视为回合结束（与
// session.idle 同语义，真实样本两种形态都存在，宽容对待）。
func (a *Adapter) mapSessionStatus(r *runState, props json.RawMessage) {
	var st struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(props, &st); err != nil {
		a.log.Debug("session.status 载荷解析失败，跳过", "task", r.taskID, "cause", err)
		return
	}
	if st.Status == "idle" {
		a.mapIdle(r, nil)
	}
}

// mapIdle 回合结束（idle）时分类收尾：ParseTrailer 判定 ask/finish/none，
// none 走 git 实况兜底（why 见 fallbackClassify）。分类后清空回合缓冲。
//
// 空回合（无累积文本）跳过分类并 Warn：idle 但无文本说明文本流可能没被本层
// 接住（如真实流主要走 message.part.updated 而 message.updated 无 parts，
// 见文件头边界注释）——这是「任务可能静默挂死」的观测点，必须可见而非 Debug
// 静默。raw 为触发 idle 的 SSE 事件原文，仅用于日志上下文。
func (a *Adapter) mapIdle(r *runState, raw json.RawMessage) {
	if strings.TrimSpace(r.turn) == "" {
		a.log.Warn("idle 但回合无文本，跳过分类", "task", r.taskID,
			"event", tailRunes(string(raw), 120))
		return
	}
	text := r.turn
	kind, t := ParseTrailer(text)
	switch kind {
	case "ask":
		a.emit(r, executor.AdapterEvent{Type: "question", Text: t.Question})
	case "finish":
		a.emit(r, executor.AdapterEvent{Type: "result", Result: &executor.Result{
			OK: true, Branch: t.Branch, CommitHash: t.Commit,
			Summary: t.Summary, SessionID: r.session,
		}})
	case "none":
		a.fallbackClassify(r, text)
	}
	r.clearTurn()
}

// fallbackClassify 是「模型未按纪律输出协议 trailer」的兜底分类。
//
// why（兜底分类规则）：回合结束但 ParseTrailer 判 none——模型可能干完活却
// 忘了写 {"branch":...} 协议。此时拿 git 实况裁决：相对任务起点有新 commit →
// 认定干完了（result OK，branch/commit 用 git 实况，summary 取回合末 200
// 字符，Warn 记录「executor 不守纪律」——这是审核者发现纪律问题的观测点）；
// 没有新 commit → 把回合全文交给审核者裁决（question），流程不卡死。
func (a *Adapter) fallbackClassify(r *runState, text string) {
	a.log.Warn("回合未输出协议 trailer，走 git 兜底", "task", r.taskID,
		"turn_tail", tailRunes(text, 120))
	branch, commit, hasNew, err := a.gitTurnStatus(r)
	if err != nil || !hasNew {
		if err != nil {
			a.log.Error("git 兜底查询失败", "task", r.taskID, "cause", err)
		}
		a.log.Info("兜底判定无新提交，转提问交审核者裁决", "task", r.taskID, "has_new", hasNew)
		a.emit(r, executor.AdapterEvent{Type: "question", Text: text})
		return
	}
	a.emit(r, executor.AdapterEvent{Type: "result", Result: &executor.Result{
		OK: true, Branch: branch, CommitHash: commit,
		Summary: tailRunes(text, 200), SessionID: r.session,
	}})
}

// gitTurnStatus 查询仓库当前分支与 HEAD commit，并对比任务起点判定是否有新提交。
func (a *Adapter) gitTurnStatus(r *runState) (branch, commit string, hasNew bool, err error) {
	b, err := exec.Command("git", "-C", r.repoPath, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return "", "", false, fmt.Errorf("查询当前分支: %w", err)
	}
	c, err := exec.Command("git", "-C", r.repoPath, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", "", false, fmt.Errorf("查询 HEAD commit: %w", err)
	}
	branch = strings.TrimSpace(string(b))
	commit = strings.TrimSpace(string(c))
	hasNew = r.startCommit != "" && commit != "" && commit != r.startCommit
	return branch, commit, hasNew, nil
}

// maybeProgress 节流发 progress：同一回合至多每 progressThrottle 一条。
func (a *Adapter) maybeProgress(r *runState) {
	now := time.Now()
	if now.Sub(r.lastProgress) < progressThrottle {
		return
	}
	r.lastProgress = now
	a.emit(r, executor.AdapterEvent{Type: "progress", Text: tailRunes(r.turn, 200)})
}

// clearTurn 清空回合累积（user 消息到来 / 回合分类终结时调用）。
func (r *runState) clearTurn() {
	r.turn = ""
	r.msgSeen = make(map[string]string)
}

// appendRender 把消息文本增量追加进 render.log（tmux 第二窗口 tail -f 可见）。
func (r *runState) appendRender(delta string) error {
	f, err := os.OpenFile(r.renderPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(delta)
	return err
}

// tailRunes 取字符串末尾最多 n 个字符（按 rune 截断，日志/摘要用，
// 不切断多字节 UTF-8 字符）。
func tailRunes(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[len(rs)-n:])
}
