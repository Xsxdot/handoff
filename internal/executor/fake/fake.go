// Package fake 提供脚本化的 executor.Adapter 实现，供集成测试驱动全链路。
//
// 职责：
//   - 按脚本（Step 队列）逐步产出 AdapterEvent：Permission（发权限请求后阻塞至
//     RespondPermission）、Question（发提问后阻塞至 Send）、Finish（发回合结果）
//   - 完整记录 Send / RespondPermission / Stop 的全部实参，供测试断言
//   - 支持运行中追加脚本（Add），模拟「协调者续接指令 → executor 继续执行并再完成」
//
// 边界：
//   - 无真实执行：不碰文件系统、不启动进程，只做事件流编排
//   - 不写 store、不做审批判断（与 executor 契约一致，见 executor.go 包级边界）
//   - 仅服务于测试与演示（agentd --executor=fake 冒烟），不用于生产
//
// 事件流语义：
//   - 事件通道在 Stop 前保持打开：任务进入 waiting_review 后协调者可能继续续接，
//     新步骤由 Add 注入；Stop 后通道关闭，manager 的中介循环随之退出
//   - 未启动任务调 Events 返回已关闭通道（契约：通道关闭 = 执行终结），
//     range 立即结束而非永久阻塞（P1-11，与 opencode adapter 语义一致）
package fake

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/Xsxdot/handoff/internal/executor"
)

// Step 是 fake 脚本的一个步骤，三种形态互斥（至多填一个字段）：
//   - Permission: 发 permission 事件（PermissionID 形如 perm-<n>），阻塞至 RespondPermission 被调
//   - Question:   发 question 事件，阻塞至 Send 被调
//   - Finish:     发 result 事件（Result.OK/FailReason 决定完成还是失败）
type Step struct {
	Permission string
	Question   string
	Finish     executor.Result
}

// SendCall 记录一次 Send 调用的实参。
type SendCall struct {
	TaskID string
	Text   string
}

// PermCall 记录一次 RespondPermission 调用的实参。
type PermCall struct {
	TaskID   string
	PermID   string
	Decision string
	Reason   string // 协调者的拒绝理由；批准时为空
}

// taskRun 是单个任务的 fake 运行状态：脚本队列 + 事件通道 + 阻塞点。
type taskRun struct {
	taskID   string
	evCh     chan executor.AdapterEvent
	steps    []Step        // 待执行脚本（mu 保护）
	sendCh   chan string   // Send 实参（Question 步骤阻塞于此；无等待提问时作续接门禁）
	permCh   chan PermCall // RespondPermission 实参（Permission 步骤阻塞于此）
	stopCh   chan struct{} // Stop 信号
	stopOnce sync.Once
	permSeq  int
	started  bool // Start 已调用（Events 据此区分「未启动」返回已关闭通道）
}

// Fake 是脚本化 Adapter，记录全部 Send/RespondPermission/Stop 实参供断言。
//
// 并发安全：所有记录与脚本队列的访问都在 mu 保护下；事件通道按任务隔离。
type Fake struct {
	mu      sync.Mutex
	script  []Step              // New 时传入的初始脚本（每个任务 Start 时拷贝一份）
	runs    map[string]*taskRun // taskID -> 运行态
	sends   []SendCall
	perms   []PermCall
	stops   []string
	sendErr error // 非 nil 时 Send 一律返回该错误（测试注入）
	permErr error // 非 nil 时 RespondPermission 一律返回该错误（测试注入）
	// firstPermID 是首个权限请求的 PermissionID（PermID() 探查用，锁内读写）。
	firstPermID string
}

// SetSendError 注入 Send 的错误返回值（默认 nil 不注入）。
//
// 用途：构造「executor 侧递送失败」的测试场景——如模拟 adapter 无该任务运行态
// （opencode adapter 的「任务不在运行中」错误），用于 reply 中继失败路径的断言。
func (f *Fake) SetSendError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sendErr = err
}

// SetPermError 注入 RespondPermission 的错误返回值（默认 nil 不注入）。
//
// 用途：同 SetSendError，面向权限应答中继的失败场景。
func (f *Fake) SetPermError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.permErr = err
}

// New 创建脚本化 fake adapter。
//
// 参数：
//   - script: 初始步骤队列；运行中可用 Add 追加（典型为测试在续接前注入下一步）
func New(script []Step) *Fake {
	return &Fake{
		script: append([]Step(nil), script...),
		runs:   make(map[string]*taskRun),
	}
}

// Add 为任务追加脚本步骤。
//
// 注意：
//   - 追加后步骤要等「续接门禁」放行才会执行：脚本耗尽后（任务已进 waiting_review），
//     runner 阻塞在 Send 上，协调者的 continue 指令（Send）到达才解锁后续步骤——
//     这保证「executor 先收到指令、再继续产出事件」的顺序，测试据此确定性断言
func (f *Fake) Add(taskID string, steps ...Step) {
	r := f.runner(taskID)
	f.mu.Lock()
	r.steps = append(r.steps, steps...)
	f.mu.Unlock()
	f.log().Debug("fake 追加脚本步骤", "task", taskID, "n", len(steps))
}

// Start 启动任务的脚本执行循环并立即返回（异步）。
func (f *Fake) Start(_ context.Context, req executor.StartReq) error {
	r := f.runner(req.Task.ID)
	f.mu.Lock()
	// 每个任务从初始脚本的一份拷贝开始，Add 只作用于该任务
	r.steps = append([]Step(nil), f.script...)
	r.started = true
	f.mu.Unlock()
	f.log().Debug("fake 任务启动", "task", req.Task.ID, "task_dir", req.TaskDir)
	go r.run(f)
	return nil
}

// Events 返回任务的事件流通道；任务未启动时返回**已关闭**通道。
//
// 契约（与 opencode adapter 一致，P1-11）：通道关闭 = 执行终结，消费方
// （manager 中介循环）靠 range 在关闭时立即退出。未启动任务若返回惰性新建的
// 打开通道，range 会永久阻塞——对未知任务等价于旧的 nil 通道。已启动任务的
// 通道由 run 循环在 Stop/退出时关闭（defer close(evCh)）。
func (f *Fake) Events(taskID string) <-chan executor.AdapterEvent {
	f.mu.Lock()
	r, ok := f.runs[taskID]
	// started 必须在锁内读取：Start 在 mu 保护下写它，锁外读与 Start 并发时会
	// 触发 data race（-race 可检出）；evCh 构造后只读，锁内取引用即可
	started := ok && r.started
	f.mu.Unlock()
	if !started {
		ch := make(chan executor.AdapterEvent)
		close(ch)
		return ch
	}
	return r.evCh
}

// Send 记录实参；若恰有 Question 步骤在阻塞则解除其阻塞，否则作为「续接指令」记录，
// 运行循环继续等待后续步骤（模拟 executor 收到修改指令后继续干活）。
func (f *Fake) Send(_ context.Context, taskID, text string) error {
	f.mu.Lock()
	err := f.sendErr
	f.mu.Unlock()
	if err != nil {
		f.log().Debug("fake 注入 Send 错误", "task", taskID, "cause", err)
		return err
	}
	r := f.runner(taskID)
	f.log().Debug("fake 收到 Send", "task", taskID, "text", truncateRunes(text, 80))
	f.mu.Lock()
	f.sends = append(f.sends, SendCall{TaskID: taskID, Text: text})
	f.mu.Unlock()
	select {
	case r.sendCh <- text:
	default:
	}
	return nil
}

// RespondPermission 记录实参并解除对应任务的 Permission 步骤阻塞。
func (f *Fake) RespondPermission(_ context.Context, taskID, permID, decision, reason string) error {
	f.mu.Lock()
	err := f.permErr
	f.mu.Unlock()
	if err != nil {
		f.log().Debug("fake 注入 RespondPermission 错误", "task", taskID, "perm", permID, "reason", reason, "cause", err)
		return err
	}
	r := f.runner(taskID)
	f.log().Debug("fake 收到 RespondPermission", "task", taskID, "perm", permID, "decision", decision, "reason", reason)
	f.mu.Lock()
	f.perms = append(f.perms, PermCall{TaskID: taskID, PermID: permID, Decision: decision, Reason: reason})
	f.mu.Unlock()
	select {
	case r.permCh <- PermCall{TaskID: taskID, PermID: permID, Decision: decision, Reason: reason}:
	default:
	}
	return nil
}

// Stop 记录并终止任务的脚本循环，事件通道随即关闭。
//
// 注意：
//   - 幂等：重复 Stop 不 panic，事件通道只关闭一次
func (f *Fake) Stop(taskID string) error {
	r := f.runner(taskID)
	f.log().Debug("fake 收到 Stop", "task", taskID)
	f.mu.Lock()
	f.stops = append(f.stops, taskID)
	f.mu.Unlock()
	r.stopOnce.Do(func() { close(r.stopCh) })
	return nil
}

// Sends 返回全部 Send 调用实参（按调用顺序）。
func (f *Fake) Sends() []SendCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]SendCall(nil), f.sends...)
}

// Perms 返回全部 RespondPermission 调用实参（按调用顺序）。
func (f *Fake) Perms() []PermCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]PermCall(nil), f.perms...)
}

// Stops 返回已收到 Stop 的任务 ID 列表。
func (f *Fake) Stops() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.stops...)
}

// PermID 返回本 fake 发出的第一个权限请求的 PermissionID（测试探查用）。
//
// 为什么只需「第一个」：单任务测试里权限请求从 perm-1 递增，首个即对应
// 任务的第一个权限工单 id（taskID:perm-1）。
func (f *Fake) PermID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.firstPermID
}

// LastDecision 返回最后一次 RespondPermission 收到的 decision；未收到任何应答
// 时返回空串（测试探查用，如断言审批者自动批准路径 executor 收到 once）。
func (f *Fake) LastDecision() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if n := len(f.perms); n > 0 {
		return f.perms[n-1].Decision
	}
	return ""
}

// runner 返回任务运行态，不存在时惰性创建。
func (f *Fake) runner(taskID string) *taskRun {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.runs[taskID]
	if !ok {
		r = &taskRun{
			taskID: taskID,
			evCh:   make(chan executor.AdapterEvent, 16),
			sendCh: make(chan string, 1),
			permCh: make(chan PermCall, 1),
			stopCh: make(chan struct{}),
		}
		f.runs[taskID] = r
	}
	return r
}

// run 是脚本执行主循环：逐步骤执行，阻塞步骤等待对应动作解除。
func (r *taskRun) run(f *Fake) {
	defer close(r.evCh)
	for {
		step, ok := r.nextStep(f)
		if !ok {
			return // Stop
		}
		switch {
		case step.Permission != "":
			permID := r.nextPermID()
			f.log().Debug("fake 发 permission 事件", "task", r.taskID, "perm", permID)
			f.mu.Lock()
			if f.firstPermID == "" {
				f.firstPermID = permID
			}
			f.mu.Unlock()
			if !r.emit(f, executor.AdapterEvent{Type: "permission", PermissionID: permID,
				Text: step.Permission, Perm: permRequestFromText(step.Permission)}) {
				return
			}
			f.log().Debug("fake 等待权限应答", "task", r.taskID, "perm", permID)
			select {
			case p := <-r.permCh:
				f.log().Debug("fake 权限已应答", "task", r.taskID, "perm", p.PermID, "decision", p.Decision)
			case <-r.stopCh:
				return
			}
		case step.Question != "":
			f.log().Debug("fake 发 question 事件", "task", r.taskID)
			if !r.emit(f, executor.AdapterEvent{Type: "question", Text: step.Question}) {
				return
			}
			f.log().Debug("fake 等待 Send", "task", r.taskID)
			select {
			case txt := <-r.sendCh:
				f.log().Debug("fake 提问已应答", "task", r.taskID, "text", truncateRunes(txt, 80))
			case <-r.stopCh:
				return
			}
		default: // Finish
			res := step.Finish
			f.log().Debug("fake 发 result 事件", "task", r.taskID, "ok", res.OK)
			if !r.emit(f, executor.AdapterEvent{Type: "result", Result: &res}) {
				return
			}
		}
	}
}

// nextStep 取出下一个待执行步骤；脚本为空时阻塞等待 Send（续接门禁）/ Stop。
//
// 续接门禁的 why：脚本耗尽时任务已进 waiting_review，此时 runner 只被 Send 唤醒——
// 协调者的 continue 指令（Send）到达才解锁新步骤，保证「executor 收到指令后才
// 继续产出事件」的因果顺序（Add 只入队，不唤醒）。
//
// 为什么 Send 在这里只排空不记录：Send 的实参已由 fake.Send 同步记录（断言数据源
// 唯一），这里把 sendCh 里的文本消费掉只是为了让门禁与阻塞语义清晰——若留着不清，
// 下一步 Question 阻塞时会把这条「续接指令」误当成提问答案。
func (r *taskRun) nextStep(f *Fake) (Step, bool) {
	for {
		f.mu.Lock()
		if len(r.steps) > 0 {
			s := r.steps[0]
			r.steps = r.steps[1:]
			f.mu.Unlock()
			return s, true
		}
		f.mu.Unlock()
		select {
		case <-r.sendCh: // 续接门禁放行（模拟 executor 收到指令后继续执行）
		case <-r.stopCh:
			return Step{}, false
		}
	}
}

// emit 向事件通道投递事件；阻塞直至 manager 消费或 Stop。
//
// 为什么阻塞而非丢事件：测试全链路依赖每条事件都被 manager 中介处理，
// 丢事件会让流程静默卡死，阻塞投递把问题暴露为超时而不是虚假通过。
func (r *taskRun) emit(f *Fake, ev executor.AdapterEvent) bool {
	select {
	case r.evCh <- ev:
		return true
	case <-r.stopCh:
		return false
	}
}

// nextPermID 为任务内第 n 个权限请求生成稳定的 PermissionID（perm-<n>）。
//
// 稳定性即幂等性：同一次权限请求重放时复用同一 id，manager 的 CreateTicket 按
// id 幂等去重（见 executor.go 包级幂等约定）。
func (r *taskRun) nextPermID() string {
	f := fmt.Sprintf("perm-%d", r.permSeq+1)
	r.permSeq++
	return f
}

// log 返回运行时 slog.Default()（与 store/config 同款修正，见 client.go 的说明）。
func (f *Fake) log() *slog.Logger { return slog.Default() }

// permRequestFromText 从权限描述文本派生出结构化载荷（测试替身专用）。
//
// 真实 adapter（claude/grok/opencode）从各自的协议载荷提取结构；fake 没有
// 协议，只能从描述文本推导，供审批链集成测试把请求路由进 Consult 出口——
// 不带 Perm 的事件会被 manager 按 fail-closed 升级人工，审批链集成就测不到了。
//
// 规则与真实 adapter 的展示文本同构：带 "Bash: " 前缀取命令，带 "Write: " /
// "Edit: " 前缀取路径，其余按未识别工具退回判 Text。
func permRequestFromText(text string) *executor.PermRequest {
	switch {
	case strings.HasPrefix(text, "Bash: "):
		return &executor.PermRequest{Tool: executor.PermToolBash, Command: text[len("Bash: "):]}
	case strings.HasPrefix(text, "Write: "):
		return &executor.PermRequest{Tool: executor.PermToolWrite, Paths: []string{text[len("Write: "):]}}
	case strings.HasPrefix(text, "Edit: "):
		return &executor.PermRequest{Tool: executor.PermToolEdit, Paths: []string{text[len("Edit: "):]}}
	default:
		return &executor.PermRequest{Tool: executor.PermToolOther}
	}
}

// truncateRunes 将字符串按 rune 截断为最多 n 个字符（日志防刷屏）。
func truncateRunes(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n])
}
