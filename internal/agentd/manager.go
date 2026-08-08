// 本文件实现 handoff 的状态机中枢与 adapter 事件中介（系统的心脏）。
//
// 职责：
//   - 任务生命周期的唯一状态写入者：dispatch（pending→running）、permission/question
//     中介（→waiting_answer→running）、result（→waiting_review）、continue/done（→running/completed）
//   - 把 executor 的 AdapterEvent 中介为「ticket + 事件 + 状态迁移」三件套：
//     permission/question 落 ticket 并挂到 hub.WaitAnswer 等审核者应答，
//     progress 只入库，result 落 completed/failed 事件进 waiting_review
//   - 审核者应答经 reply 回程（server）→ NotifyAnswer 唤醒等待 goroutine → 回传 executor
//
// 中介时序与工单幂等的不变量（P1-2/P1-6/P1-7）：
//   - 权限工单 id = taskID+":"+permID（按任务命名空间隔离，跨任务 permID 碰撞不吞工单）；
//     question 工单 id 为 uuid。reply 路由整链（事件 payload / hub 等待键 / 应答回程）
//     都用工单 id，只有回传 adapter 时还原裸 permID
//   - 中介顺序为「落库 → 置 waiting_answer → 启动 waiter goroutine → Publish」：
//     状态先就位（reply 回程 resumeIfIdle 的回迁判定依赖它）；waiter 的 hub 注册
//     是异步的，reply 先于注册到达时退化为「无等待者 → 自愈中继」路径兜底
//     （详见 handlePermission 的顺序契约）
//   - CreateTicket 返回 created=false（重放）时跳过全部后续动作，幂等是完整的
//
// 边界：
//   - 不做审批判断：「allow 之外一律 reject」「回答原样透传」是仅有的两条翻译规则，
//     批不批、答什么由审核者（人/上层）决定
//   - 不直接接触 executor 进程/会话细节，一切经由 executor.Adapter 契约
//   - 不负责看门狗（Task 12）等横向能力；git 工作区操作委托 workspace 包（Task 9）
//   - dispatch 前经 workspace.PrepareBranch 在任务仓库开任务分支（脏工作区拒绝），
//     其余 git 操作（diff/fetch/run）由 server 路由直接调用 workspace 包
//
// 「失败也进 waiting_review」的 why：
//
//	失败的 result 同样进 waiting_review 而非自动重试或直接 failed——让审核者看到
//	失败现场（failed 事件携带原因）决定重试话术；自动重试会在同一坑里反复烧
//	token 且无人知情，failed 终态又让任务失去续接入口。人工裁决是唯一解。
//
// 状态迁移并发安全：
//   - 全部迁移经 store.UpdateTaskState 的 CAS（WHERE state=旧值）落库，双写者
//     只有一个赢家；reply 回程的 resumeIfIdle 与应答 goroutine 的回迁 race 由
//     CAS + transitBestEffort（容忍 ErrBadTransit）吸收
package agentd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/store"
)

// eventType 常量：AdapterEvent.Type 的合法取值（与 proto.EventType 的映射见各 handler）。
const (
	adapterEventPermission = "permission"
	adapterEventQuestion   = "question"
	adapterEventProgress   = "progress"
	adapterEventResult     = "result"
)

// errBadDispatchRequest 是 Dispatch 入参错误的哨兵（server 层映射为 400）。
var errBadDispatchRequest = errors.New("dispatch 请求参数非法")

// Manager 是任务状态机中枢与 adapter 事件中介。
//
// 并发安全：无共享可变字段（st/hub/ad/cfg/log 构造后只读），
// 每个任务的中介 goroutine 与应答 goroutine 通过 store CAS + hub 路由协作。
type Manager struct {
	st  *store.Store
	hub *Hub
	ad  executor.Adapter
	cfg *config.Config
	log *slog.Logger
}

// NewManager 创建任务管理器。
//
// 参数：
//   - st: 持久化存储（任务/事件/工单的唯一落库点）
//   - hub: 进程内实时路由（事件广播 + ticket 应答等待）
//   - ad: executor 挂载实现（opencode / fake）
//   - cfg: 配置（DataDir 用于派生任务目录）
//   - log: 本模块日志入口
//
// 注意：
//   - 调用方须保证 log 为统一配置后的 logger；st/hub 必须已就绪
func NewManager(st *store.Store, hub *Hub, ad executor.Adapter, cfg *config.Config, log *slog.Logger) *Manager {
	return &Manager{st: st, hub: hub, ad: ad, cfg: cfg, log: log}
}

// DispatchReq 是 Dispatch 的入参：任务仓库与 base64 编码的计划内容。
type DispatchReq struct {
	Repo     string // 任务仓库路径（executor 工作区）
	PlanB64  string // plan 内容，base64 编码（路由/CLI 层编码，此处解码）
	PlanName string // plan 文件名（归档展示用，写入 task 的 PlanPath 目录下）
	Target   string // 目标主机名（归档展示用，记入 task.Target）
}

// ticketRequest 是工单 request 列的通用载体，kind 区分 gate/ask。
type ticketRequest struct {
	Kind       string `json:"kind"`
	Permission string `json:"permission,omitempty"` // gate：权限描述
	Question   string `json:"question,omitempty"`   // ask：问题原文
}

// permissionPayload 是 permission_request 事件的 payload（含 ticket_id 供回复）。
type permissionPayload struct {
	TicketID   string `json:"ticket_id"`
	Permission string `json:"permission"`
	Kind       string `json:"kind"`
}

// questionPayload 是 question 事件的 payload（含 ticket_id 供回复）。
type questionPayload struct {
	TicketID string `json:"ticket_id"`
	Question string `json:"question"`
	Kind     string `json:"kind"`
}

// progressPayload 是 progress 事件的 payload。
type progressPayload struct {
	Text string `json:"text"`
}

// completedPayload 是 completed 事件的 payload。
type completedPayload struct {
	Branch     string `json:"branch"`
	CommitHash string `json:"commit"`
	Summary    string `json:"summary"`
}

// failedPayload 是 failed 事件的 payload。
type failedPayload struct {
	FailReason string `json:"fail_reason"`
}

// Dispatch 派发一个新任务：准备任务分支 → 建任务 → 建 taskDir 写 plan → Adapter.Start →
// running → 启动中介 goroutine 消费事件流。
//
// 参数：
//   - req: 仓库路径与 base64 计划（字段说明见 DispatchReq）
//
// 返回：
//   - 已入库的任务（state 为 running）；Adapter.Start 失败时返回错误，
//     此时任务已落库并迁移为 failed（供审核者经 tasks 命令查看失败现场）
//
// 注意：
//   - 任务分支（handoff/<id8>）的准备发生在建任务之前：分支准备是纯前置校验
//     （工作区干净/可开分支），失败时不落任何任务记录，审核者修好仓库重新
//     dispatch 即可——不会为每次被拒的派发留下 failed 噪音
//   - 分支名经 store.SetTaskField 白名单字段 "branch" 写入任务（不随 CreateTask
//     带列写入，保持「创建期只写创建时已知的字段」的约定）
func (m *Manager) Dispatch(ctx context.Context, req DispatchReq) (task *proto.Task, err error) {
	m.log.Info("dispatch 进入", "repo", req.Repo, "plan_name", req.PlanName, "target", req.Target)
	defer func() {
		if err != nil {
			m.log.Error("dispatch 失败", "repo", req.Repo, "cause", err)
		} else {
			m.log.Info("dispatch 完成", "task", task.ID)
		}
	}()

	if req.Repo == "" || req.PlanB64 == "" {
		return nil, fmt.Errorf("%w: repo=%q plan_b64 长度=%d", errBadDispatchRequest, req.Repo, len(req.PlanB64))
	}
	planContent, err := base64.StdEncoding.DecodeString(req.PlanB64)
	if err != nil {
		return nil, fmt.Errorf("%w: 解码 plan_b64: %v", errBadDispatchRequest, err)
	}

	now := time.Now().UTC()
	taskID := uuid.NewString()

	// 派发前置：在任务仓库上准备任务分支（handoff/<id8>）。
	// 为什么放在建任务之前：分支准备是纯前置校验（工作区干净/可开分支），失败时
	// 不留孤儿任务记录，审核者修好仓库后重新 dispatch 即可（见 Dispatch doc 注意）
	branch, err := PrepareBranch(req.Repo, taskID)
	if err != nil {
		return nil, fmt.Errorf("git 工作区准备: %w", err)
	}

	// taskDir 是任务专属工作目录（计划文件与 executor 侧任务物料都放这里）。
	// why 0700：目录内存 serve 启动脚本 run_serve.sh（0600，含随机密码）与
	// serve.json（0600，含密码）——目录对他人可读会让文件名的存在性可被
	// 探知，且任何权限疏漏都直接暴露凭据；与 DataDir（agentd 启动时 0700）
	// 保持一致
	taskDir := filepath.Join(m.cfg.DataDir, "tasks", taskID)
	if err := os.MkdirAll(taskDir, 0o700); err != nil {
		return nil, fmt.Errorf("创建任务目录 %s: %w", taskDir, err)
	}
	planPath := filepath.Join(taskDir, req.PlanName)
	if err := os.WriteFile(planPath, planContent, 0o600); err != nil {
		return nil, fmt.Errorf("写计划文件 %s: %w", planPath, err)
	}

	task = &proto.Task{
		ID:       taskID,
		Target:   req.Target,
		RepoPath: req.Repo,
		// PlanPath 不在 SetTaskField 白名单，只能在创建时一并写入
		PlanPath:  planPath,
		State:     proto.TaskStatePending,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := m.st.CreateTask(task); err != nil {
		return nil, err
	}

	// 分支名经 SetTaskField 白名单写入（见 Dispatch doc 注意）
	if err := m.st.SetTaskField(taskID, "branch", branch); err != nil {
		m.log.Error("写入任务分支失败", "task", taskID, "branch", branch, "cause", err)
		// 分支已在仓库建好但任务记录写不上：按派发失败处理，落 failed 供人工清理
		m.transitBestEffort(taskID, proto.TaskStateFailed, "写分支名失败")
		return nil, fmt.Errorf("记录任务分支: %w", err)
	}
	// 内存态同步补上 branch，保证传给 adapter 的 StartReq.Task 完整
	task.Branch = branch

	if err := m.ad.Start(ctx, executor.StartReq{Task: *task, PlanContent: string(planContent), TaskDir: taskDir}); err != nil {
		m.log.Error("adapter 启动失败", "task", taskID, "cause", err)
		// pending→failed 合法；失败现场留在任务里，审核者可见
		m.transitBestEffort(taskID, proto.TaskStateFailed, "adapter start 失败")
		return nil, fmt.Errorf("启动 executor: %w", err)
	}
	if err := m.transit(taskID, proto.TaskStateRunning, "dispatch"); err != nil {
		return nil, err
	}
	// 返回最新快照：transit 已把状态改为 running（含 updated_at 刷新），
	// 不能让调用方拿到创建时的 pending 旧值
	task, err = m.st.GetTask(taskID)
	if err != nil {
		return nil, fmt.Errorf("读取派发后的任务: %w", err)
	}
	go m.mediate(taskID)
	return task, nil
}

// Continue 向任务续发修改指令：要求任务处于 waiting_review，先回迁 running 再
// 经 Adapter.Send 原样透传指令（同一会话续接，上下文完整保留）。
//
// 返回：
//   - 任务不存在返回 store.ErrNotFound；状态不允许续接返回 store.ErrBadTransit
//   - Send 失败时任务回迁 waiting_review（审核者可重试指令），并返回该错误
func (m *Manager) Continue(ctx context.Context, taskID, instructions string) (err error) {
	m.log.Info("continue 进入", "task", taskID)
	defer func() {
		if err != nil {
			m.log.Error("continue 失败", "task", taskID, "cause", err)
		} else {
			m.log.Info("continue 完成", "task", taskID)
		}
	}()

	cur, err := m.st.GetTask(taskID)
	if err != nil {
		return err
	}
	if cur.State != proto.TaskStateWaitingReview {
		m.log.Warn("continue 状态不允许", "task", taskID, "state", cur.State)
		return fmt.Errorf("任务 %s 状态 %s 不允许续接（需 waiting_review）: %w", taskID, cur.State, store.ErrBadTransit)
	}
	if err := m.transit(taskID, proto.TaskStateRunning, "continue"); err != nil {
		return err
	}
	if err := m.ad.Send(ctx, taskID, instructions); err != nil {
		m.log.Error("续发指令失败", "task", taskID, "cause", err)
		// 回迁 waiting_review：指令没送达，回到审核者可重试的位置，不让任务死在 running
		m.transitBestEffort(taskID, proto.TaskStateWaitingReview, "continue 发送失败回迁")
		return fmt.Errorf("续发指令: %w", err)
	}
	return nil
}

// Done 归档任务：要求任务处于 waiting_review，迁移 completed 后调用 Adapter.Stop
// 回收 executor 侧资源。
//
// 返回：
//   - 任务不存在返回 store.ErrNotFound；状态不允许归档返回 store.ErrBadTransit
//
// 注意：
//   - Stop 失败仅打 Error 日志不影响归档：任务已完成，executor 残留交给人工兜底
func (m *Manager) Done(ctx context.Context, taskID string) (err error) {
	m.log.Info("done 进入", "task", taskID)
	defer func() {
		if err != nil {
			m.log.Error("done 失败", "task", taskID, "cause", err)
		} else {
			m.log.Info("done 完成", "task", taskID)
		}
	}()

	cur, err := m.st.GetTask(taskID)
	if err != nil {
		return err
	}
	if cur.State != proto.TaskStateWaitingReview {
		m.log.Warn("done 状态不允许", "task", taskID, "state", cur.State)
		return fmt.Errorf("任务 %s 状态 %s 不允许归档（需 waiting_review）: %w", taskID, cur.State, store.ErrBadTransit)
	}
	if err := m.transit(taskID, proto.TaskStateCompleted, "done"); err != nil {
		return err
	}
	if err := m.ad.Stop(taskID); err != nil {
		m.log.Error("停止 executor 失败", "task", taskID, "cause", err)
	}
	return nil
}

// unaryAPITimeout 是 executor 侧一次一元调用（建会话/发 prompt/权限应答）的等待上限。
//
// 为什么 30s：与 opencode 客户端的一元超时同值（客户端 Timeout 已兜底），这里再
// 包一层 ctx 截止时间是双保险——未来 executor 若无客户端级超时，管理层的截止时间
// 仍保证「审核者 reply 回程不会永久挂起」。与等待阶段（WaitAnswer，人工应答时长
// 无上限）无关，见 unaryCtx。
const unaryAPITimeout = 30 * time.Second

// unaryCtx 为一次 executor 一元调用派生带截止时间的 ctx。
//
// 为什么派生子 ctx 而不直接把 parent 传下去：等待阶段（WaitAnswer 等人工应答）
// 时长无上限，截止时间必须只包住调用本身、不能缩短等待窗口——parent 在等待期
// 结束后可能已接近/超过 30s（审核者思考了 5 分钟才回复），直接复用会把本应成功
// 的调用掐死在截止时间上。
func unaryCtx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, unaryAPITimeout)
}

// RelayAnswer 在 reply 回程找不到等待者时，把已落库的审核者应答直接回传给
// executor，自愈「agentd 重启后等待 goroutine 消亡 → 回答丢失 → executor 永远阻塞」。
//
// 场景：agentd 重启时 waitPermission/waitQuestion goroutine 随进程消亡，且 /event
// 不重放历史的话，审核者的 reply 在 hub 里找不到等待者；若应答就此丢弃，任务状态
// 被 resumeIfIdle 回迁 running，executor 却永远等不到权限裁决——工单已答、二次
// reply 404、done 409，不可恢复。本方法读取工单把应答按既有翻译规则直接送达 executor。
//
// 参数：
//   - taskID: 工单所属任务 ID（reply 路由的路径参数）
//   - ticketID: 已回答的工单 ID（AnswerTicket 已落库，此处只负责回传）
//   - answer: 审核者应答原文（与落库值一致）
//
// 返回：
//   - 工单不存在/不属于该任务/类型不识别返回错误；adapter 回传失败返回错误
//
// 规则（与 waitPermission/waitQuestion 的翻译规则完全一致）：
//   - kind=gate：answer trim 后为 "allow" → RespondPermission("once")，其余一律 "reject"
//   - kind=ask：answer 原文原样经 Send 透传
func (m *Manager) RelayAnswer(taskID, ticketID, answer string) error {
	tk, err := m.st.GetTicket(ticketID)
	if err != nil {
		return fmt.Errorf("读取工单 %s: %w", ticketID, err)
	}
	if tk.TaskID != taskID {
		return fmt.Errorf("工单 %s 不属于任务 %s", ticketID, taskID)
	}
	var req ticketRequest
	if err := json.Unmarshal(tk.Request, &req); err != nil {
		return fmt.Errorf("解析工单 %s 请求体: %w", ticketID, err)
	}
	switch req.Kind {
	case "gate":
		decision := "reject"
		if strings.TrimSpace(answer) == "allow" {
			decision = "once"
		}
		// ticket id 已按任务命名空间化（taskID:permID，见 handlePermission 的 why），
		// 而 adapter 契约要求裸 PermissionID：剥掉 taskID 前缀还原。invariant：
		// gate 工单 id 恒由 handlePermission 以 taskID+":"+permID 生成，
		// TrimPrefix 精确还原（permID 自身含 ":" 也不影响前缀剥离）
		permID := strings.TrimPrefix(ticketID, taskID+":")
		m.log.Info("reply 无等待者，自愈中继权限应答", "task", taskID,
			"ticket", ticketID, "perm", permID, "decision", decision)
		// 不用 context.Background() 直传：一元调用必须有界（unaryCtx 的 why），
		// 半死的 executor 在 unaryAPITimeout 内必然失败，reply 回程不永久挂起
		actx, acancel := unaryCtx(context.Background())
		defer acancel()
		if err := m.ad.RespondPermission(actx, taskID, permID, decision); err != nil {
			return fmt.Errorf("中继权限应答: %w", err)
		}
		return nil
	case "ask":
		m.log.Info("reply 无等待者，自愈中继提问回答", "task", taskID, "ticket", ticketID)
		actx, acancel := unaryCtx(context.Background())
		defer acancel()
		if err := m.ad.Send(actx, taskID, answer); err != nil {
			return fmt.Errorf("中继提问回答: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("工单 %s 类型 %q 不支持中继", ticketID, req.Kind)
	}
}

// mediate 是单任务的事件中介循环（每任务一个 goroutine，由 Dispatch 启动）：
// 消费 Adapter.Events 直到通道关闭（Stop），每类事件交给对应 handler。
//
// 任务级 ctx 与取消时机（P1-2）：taskCtx 供 waitPermission/waitQuestion 的应答
// 等待共用，中介循环退出（adapter 事件通道关闭 = 执行终结：Done/Stop/serve 死亡）
// 时 defer cancel 取消全部在等应答的 waiter——否则「审核者永不回答 + 执行终结」
// 的组合会让 wait goroutine 用 context.Background() 永久挂死。
//
// 为什么不在 result 到达时取消：result → waiting_review 后任务仍活（审核者可
// continue/回答挂起工单），回答晚于 result 到达是合法流程（应答后 executor 被
// 唤醒续跑，见 TestTransitToReviewTwoHopFromWaitingAnswer）——只有「事件通道
// 关闭」这一执行终结信号才是取消时机。
func (m *Manager) mediate(taskID string) {
	m.log.Info("中介循环启动", "task", taskID)
	taskCtx, cancelTask := context.WithCancel(context.Background())
	defer cancelTask()
	events := m.ad.Events(taskID)
	for ev := range events {
		m.handleEvent(taskCtx, taskID, ev)
	}
	m.log.Info("中介循环结束", "task", taskID)
}

// handleEvent 按事件类型分发中介处理，并打每类事件的入口日志。
func (m *Manager) handleEvent(ctx context.Context, taskID string, ev executor.AdapterEvent) {
	switch ev.Type {
	case adapterEventPermission:
		m.log.Info("权限请求事件", "task", taskID, "perm", ev.PermissionID,
			"text", truncateRunes(ev.Text, 80))
		m.handlePermission(ctx, taskID, ev)
	case adapterEventQuestion:
		m.log.Info("提问事件", "task", taskID, "text", truncateRunes(ev.Text, 80))
		m.handleQuestion(ctx, taskID, ev)
	case adapterEventProgress:
		m.log.Info("进度事件", "task", taskID, "text", truncateRunes(ev.Text, 80))
		m.handleProgress(taskID, ev)
	case adapterEventResult:
		if ev.Result != nil {
			m.log.Info("执行结果事件", "task", taskID, "ok", ev.Result.OK,
				"branch", ev.Result.Branch, "commit", ev.Result.CommitHash)
		} else {
			m.log.Warn("执行结果事件缺 Result", "task", taskID)
		}
		m.handleResult(taskID, ev)
	default:
		m.log.Warn("未知 adapter 事件", "task", taskID, "type", ev.Type)
	}
}

// handlePermission 中介权限请求：ticket(id=taskID:permID, kind=gate) → 事件 →
// waiting_answer → goroutine 等审核者应答后回传 executor。
//
// 顺序契约（P1-2 时序修复）：ticket/事件落库后，**先置 waiting_answer，再启动
// waiter goroutine，最后才 Publish**。严格说「先注册 waiter」不成立：waitPermission
// 的 hub 注册在 goroutine 启动后才异步发生，Publish 后 reply 仍可能先于注册到达——
// 该情形退化为「无等待者 → 自愈中继」（RelayAnswer 直接回传 executor），任务不卡死。
// 真正必须先就位的是**状态**：resumeIfIdle 读到 running 会跳过回迁，应答已交付但
// 任务随后落回 waiting_answer 且无人再答，永久卡死（探针 1/60 复现「waiting_answer
// 但 pending_tickets=0」）。状态先就位后，reply 无论命中 waiter 还是走中继，
// resumeIfIdle 必看到 waiting_answer，回迁判定一致。
func (m *Manager) handlePermission(ctx context.Context, taskID string, ev executor.AdapterEvent) {
	if ev.PermissionID == "" {
		m.log.Error("权限事件缺 PermissionID", "task", taskID)
		return
	}
	// ticket id 按任务命名空间隔离：taskID:permID（P1-6）。opencode 的权限 id
	// 按会话生成、跨任务不保证唯一，裸 permID 作 ticket id 时第二个任务的工单
	// 会被 INSERT OR IGNORE 静默吞掉——attach 显示 0 挂起项且任务永远无法应答。
	// 命名空间化后各任务工单 id 全局唯一；回传 executor 仍用裸 permID
	// （adapter 契约，见 waitPermission/RelayAnswer）
	ticketID := taskID + ":" + ev.PermissionID
	req, _ := json.Marshal(ticketRequest{Kind: "gate", Permission: ev.Text})
	created, err := m.st.CreateTicket(&proto.Ticket{
		ID: ticketID, TaskID: taskID, Kind: "gate",
		Request: req, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		m.log.Error("创建权限工单失败", "task", taskID, "perm", ev.PermissionID, "ticket", ticketID, "cause", err)
		return
	}
	if !created {
		// 重放（SSE 断线重连/agentd 重启后订阅重建）：ticket 已存在，跳过全部
		// 中介动作——不追加第二条事件、不重复迁移、不起第二个 waiter、不重复
		// 广播（P1-7）。幂等只做一半（只去重工单）会让审核者被重复唤醒、
		// RespondPermission 被调两次。已答的重放同样跳过：应答已落库并已被
		// 既有等待者或自愈中继送达 executor，这里再动就是重复交付
		m.log.Debug("权限请求重放，跳过中介", "task", taskID, "perm", ev.PermissionID, "ticket", ticketID)
		return
	}
	evt, err := m.st.AppendEvent(taskID, proto.EventTypePermissionRequest, permissionPayload{
		TicketID: ticketID, Permission: ev.Text, Kind: "gate",
	})
	if err != nil {
		m.log.Error("追加 permission_request 事件失败", "task", taskID, "cause", err)
		return
	}
	m.transitBestEffort(taskID, proto.TaskStateWaitingAnswer, "permission_request")
	go m.waitPermission(ctx, taskID, ev.PermissionID, ticketID)
	m.hub.Publish(evt)
}

// waitPermission 阻塞等待权限工单的审核者应答，按规则回传 executor 并回迁 running。
//
// 规则：应答 trim 后等于 "allow" → RespondPermission("once")，其余一律 "reject"
// （deny:原因 等拒绝表达全部归入 reject，原因已随应答落库可追溯）。
//
// 为什么先回迁 running 再回传 executor：respond 一发出 executor 立刻继续执行并可能
// 立即产出下一个事件（如 result），若回迁在 respond 之后，result 可能先到而状态还
// 卡在 waiting_answer，导致 waiting_answer→waiting_review 非法迁移被拒。先落状态
// 保证「executor 恢复执行时状态必为 running」。
//
// 参数：permID 是 executor 侧裸权限 id（adapter 契约），ticketID 是命名空间化的
// 工单 id（hub 等待键与 reply 路由都用它）。
func (m *Manager) waitPermission(ctx context.Context, taskID, permID, ticketID string) {
	start := time.Now()
	ans, err := m.hub.WaitAnswer(ctx, ticketID)
	if err != nil {
		m.log.Warn("权限应答等待被取消", "task", taskID, "perm", permID, "ticket", ticketID, "cause", err)
		return
	}
	m.log.Info("权限应答已收到", "task", taskID, "perm", permID, "ticket", ticketID,
		"wait_ms", time.Since(start).Milliseconds(), "answer", truncateRunes(ans, 80))

	// 回迁失败（reply 回程的 resumeIfIdle 已抢先回迁）由 transitBestEffort 容忍
	m.transitBestEffort(taskID, proto.TaskStateRunning, "permission 已应答")
	decision := "reject"
	if strings.TrimSpace(ans) == "allow" {
		decision = "once"
	}
	// 派生子 ctx 只约束调用本身（unaryCtx 的 why）：等答案阶段早已结束，此处的
	// parent 是任务级 ctx（取消无截止），不加超时的话半死 executor 会让本调用挂死
	actx, acancel := unaryCtx(ctx)
	defer acancel()
	if err := m.ad.RespondPermission(actx, taskID, permID, decision); err != nil {
		// executor 侧可能已不在（进程被杀）：记录错误并保持现状，交由审核者裁决
		m.log.Error("回应权限失败", "task", taskID, "perm", permID, "decision", decision, "cause", err)
		return
	}
}

// handleQuestion 中介提问：ticket(uuid, kind=ask) → 事件 → waiting_answer →
// goroutine 等审核者回答后原样透传 executor。
//
// 顺序契约与 handlePermission 相同（P1-2）：先置 waiting_answer 再 Publish；
// waiter 注册异步，reply 先于注册到达时退化为自愈中继路径兜底。
func (m *Manager) handleQuestion(ctx context.Context, taskID string, ev executor.AdapterEvent) {
	// 提问工单 id 用 uuid：问题没有天然稳定 id，回答一次即终结
	ticketID := uuid.NewString()
	req, _ := json.Marshal(ticketRequest{Kind: "ask", Question: ev.Text})
	created, err := m.st.CreateTicket(&proto.Ticket{
		ID: ticketID, TaskID: taskID, Kind: "ask",
		Request: req, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		m.log.Error("创建提问工单失败", "task", taskID, "ticket", ticketID, "cause", err)
		return
	}
	if !created {
		// uuid 碰撞理论不可达，防御性保留与 handlePermission 相同的重放跳过语义
		m.log.Debug("提问重放，跳过中介", "task", taskID, "ticket", ticketID)
		return
	}
	evt, err := m.st.AppendEvent(taskID, proto.EventTypeQuestion, questionPayload{
		TicketID: ticketID, Question: ev.Text, Kind: "ask",
	})
	if err != nil {
		m.log.Error("追加 question 事件失败", "task", taskID, "cause", err)
		return
	}
	m.transitBestEffort(taskID, proto.TaskStateWaitingAnswer, "question")
	go m.waitQuestion(ctx, taskID, ticketID)
	m.hub.Publish(evt)
}

// waitQuestion 阻塞等待提问工单的回答，把回答原文原样透传给 executor 并回迁 running。
//
// 为什么原文透传：回答语义（技术建议、需求取舍）只有审核者理解，manager 不做
// 任何加工——无损透传是「回答什么由审核者决定」承诺的执行保证。
//
// 为什么先回迁 running 再 Send：同 waitPermission——Send 一发出 executor 立刻恢复
// 执行并可能立即产出 result，先落状态保证 result 到达时状态必为 running。
func (m *Manager) waitQuestion(ctx context.Context, taskID, ticketID string) {
	start := time.Now()
	ans, err := m.hub.WaitAnswer(ctx, ticketID)
	if err != nil {
		m.log.Warn("提问应答等待被取消", "task", taskID, "ticket", ticketID, "cause", err)
		return
	}
	m.log.Info("提问应答已收到", "task", taskID, "ticket", ticketID,
		"wait_ms", time.Since(start).Milliseconds(), "answer", truncateRunes(ans, 80))

	m.transitBestEffort(taskID, proto.TaskStateRunning, "question 已应答")
	actx, acancel := unaryCtx(ctx)
	defer acancel()
	if err := m.ad.Send(actx, taskID, ans); err != nil {
		m.log.Error("回发提问回答失败", "task", taskID, "ticket", ticketID, "cause", err)
		return
	}
}

// handleProgress 中介进度事件：只入库广播，不改任务状态。
//
// SessionID 非空时先落 task.ExecutorSession：「会话就绪」信号（adapter 建会话
// 成功后第一条 progress）是会话 id 到达 manager 的可靠通道——审核主路径常以
// question 收尾、result 永不出现。落库失败仅 Warn，不影响主流程（会话 id 属
// 可修复的辅助字段，进度广播照常）。
func (m *Manager) handleProgress(taskID string, ev executor.AdapterEvent) {
	if ev.SessionID != "" {
		if err := m.st.SetTaskField(taskID, "executor_session", ev.SessionID); err != nil {
			m.log.Warn("落库 executor_session 失败", "task", taskID, "session", ev.SessionID, "cause", err)
		}
	}
	evt, err := m.st.AppendEvent(taskID, proto.EventTypeProgress, progressPayload{Text: ev.Text})
	if err != nil {
		m.log.Error("追加 progress 事件失败", "task", taskID, "cause", err)
		return
	}
	m.hub.Publish(evt)
}

// handleResult 中介回合结果：OK → completed 事件，!OK → failed 事件；两者都进
// waiting_review（why 见文件头）——失败也交审核者裁决，不自动重试烧 token。
//
// 顺序为什么是「追加事件 → 迁移状态 → 广播」而非广播在前：审核者（或脚本）收到
// completed 事件后可能立即执行 continue/done，若状态尚未回迁 waiting_review 会被
// 409 拒绝——先落状态再唤醒，保证「事件到达时状态已就绪」。迁移失败（如并发
// done 已抢先归档）则不广播，避免唤醒审核者去操作一个已终结的任务。
func (m *Manager) handleResult(taskID string, ev executor.AdapterEvent) {
	if ev.Result == nil {
		m.log.Error("result 事件缺 Result", "task", taskID)
		return
	}
	// 已归档（done 后）的杂散 result 直接丢弃，不重复追加事件
	cur, err := m.st.GetTask(taskID)
	if err != nil {
		m.log.Error("读取任务失败", "task", taskID, "cause", err)
		return
	}
	if cur.State == proto.TaskStateCompleted || cur.State == proto.TaskStateFailed {
		m.log.Debug("任务已终结，忽略 result 事件", "task", taskID, "state", cur.State)
		return
	}

	r := ev.Result
	// result 落 executor_session 是「会话就绪」progress 的双保险：适配器在
	// result 上也携带 SessionID，即使 progress 通道异常（如乱序丢失），会话 id
	// 仍经此落库。失败仅 Warn，不影响回合结果中介主流程。
	if r.SessionID != "" {
		if err := m.st.SetTaskField(taskID, "executor_session", r.SessionID); err != nil {
			m.log.Warn("落库 executor_session 失败", "task", taskID, "session", r.SessionID, "cause", err)
		}
	}
	var evt proto.Event
	if r.OK {
		evt, err = m.st.AppendEvent(taskID, proto.EventTypeCompleted, completedPayload{
			Branch: r.Branch, CommitHash: r.CommitHash, Summary: r.Summary,
		})
	} else {
		evt, err = m.st.AppendEvent(taskID, proto.EventTypeFailed, failedPayload{FailReason: r.FailReason})
	}
	if err != nil {
		m.log.Error("追加 result 事件失败", "task", taskID, "cause", err)
		return
	}
	if err := m.transitToReview(taskID); err != nil {
		m.log.Error("回迁 waiting_review 失败，不广播事件", "task", taskID, "cause", err)
		return
	}
	m.hub.Publish(evt)
}

// transitToReview 把任务迁入 waiting_review；若当前状态不允许直跳（典型为回答-续跑
// 链路尚未回迁 running 的 waiting_answer 防御场景），按最新快照走兜底路径重试。
//
// 为什么失败后必须重读重试而不是直接报错：result 事件已在 handleResult 中追加落库，
// 一旦本方法返回错误，事件会连同 Publish 一起被丢弃，任务可能卡死在 running——
// 竞态细节见 transitToReviewRetry。
func (m *Manager) transitToReview(taskID string) error {
	if err := m.transit(taskID, proto.TaskStateWaitingReview, "result"); err == nil {
		return nil
	}
	return m.transitToReviewRetry(taskID)
}

// transitToReviewRetry 在首跳失败后按最新快照重试进入 waiting_review。
//
// 两种可收敛路径：
//   - waiting_answer：回答-续跑链路尚未回迁 running 的防御场景，两跳经 running 进入
//   - running：残留竞态——首跳失败后、重读前应答 goroutine 已把 waiting_answer 回迁
//     running（running→waiting_review 合法），直接重试补跳即可，已追加的结果事件
//     不因该时序丢失（否则任务卡死在 running 直到看门狗）
//
// 注意：
//   - 两跳中的 running 迁移容忍 ErrBadTransit：该迁移与应答 goroutine 的回迁并发竞争
//     同一 CAS，输家（本方法）说明 running 已达成（谁赢都是 running），继续补跳即收敛
//   - 其余状态（如 done 已抢先归档的 completed/failed）返回错误：任务已终结，
//     由 handleResult 决定不广播，避免唤醒审核者去操作一个已终结的任务
func (m *Manager) transitToReviewRetry(taskID string) error {
	cur, err := m.st.GetTask(taskID)
	if err != nil {
		return err
	}
	switch cur.State {
	case proto.TaskStateWaitingAnswer:
		if err := m.transit(taskID, proto.TaskStateRunning, "result 到达前回迁"); err != nil && !errors.Is(err, store.ErrBadTransit) {
			return err
		}
	case proto.TaskStateRunning:
		// 残留竞态窗口：首跳失败后、重读前应答 goroutine 已回迁 running，直接补跳
	default:
		return fmt.Errorf("任务不在 waiting_answer/running，无法进入 waiting_review: %s", cur.State)
	}
	return m.transit(taskID, proto.TaskStateWaitingReview, "result")
}

// transit 迁移任务状态并记录 from→to 迁移日志；已在目标状态时幂等返回 nil。
func (m *Manager) transit(taskID string, to proto.TaskState, reason string) error {
	cur, err := m.st.GetTask(taskID)
	if err != nil {
		return err
	}
	if cur.State == to {
		return nil
	}
	if err := m.st.UpdateTaskState(taskID, to); err != nil {
		return err
	}
	m.log.Info("任务状态迁移", "task", taskID, "from", cur.State, "to", to, "reason", reason)
	return nil
}

// transitBestEffort 容忍 ErrBadTransit 的迁移。
//
// 为什么容忍：reply 回程的 resumeIfIdle 与应答 goroutine 的回迁并发竞争同一迁移，
// CAS 保证只有一个赢家；输家拿 ErrBadTransit 说明目标状态已达成（resumeIfIdle
// 已抢先回迁 running），无需重试也无需告警。其余错误照实记录。
func (m *Manager) transitBestEffort(taskID string, to proto.TaskState, reason string) {
	if err := m.transit(taskID, to, reason); err != nil && !errors.Is(err, store.ErrBadTransit) {
		m.log.Error("状态迁移失败", "task", taskID, "to", to, "reason", reason, "cause", err)
	}
}

// restorer 是「agentd 重启后重建执行」的可选 adapter 能力（opencode 实现）。
//
// 为什么不用类型断言外的方案：executor.Adapter 是 Task 3 定稿的五动作契约，
// 为恢复能力加方法会污染 fake 等全部实现；且 fake 的运行态随进程消亡、本就不该
// 支持恢复。把恢复作为可选能力（interface 断言）既保住核心契约，又让
// 「不支持恢复的 adapter 重启后一律按不存活走 failed 恢复路径」成为自然语义。
type restorer interface {
	Resume(taskID, taskDir, repoPath, sessionID string) (bool, error)
}

// ResumeTask 恢复 agentd 重启前已在执行的任务：探测执行器存活；存活则经 adapter
// 重建 SSE 订阅并重启本任务的中介循环（spec §8「存活则重连 SSE 继续」）。
//
// 返回：
//   - true：执行器存活且事件流已重建，任务继续执行
//   - false：执行器已不在（或 adapter 无恢复能力），供 RecoverOnStartup 把任务
//     迁移 failed/waiting_review 交审核者裁决
//
// 注意：
//   - 本方法作为 RecoverOnStartup 的探活闭包传入：存活的「重建订阅 + 重启中介
//     循环」动作封装在闭包内部（见 watchdog.go RecoverOnStartup 的 seam 说明）
//   - 重启前已挂起的权限/提问等待不需要在此重建：reply 回程的 resumeIfIdle
//     自带「回答后无未答工单即回迁 running」的兜底（server.go），审核者回答
//     挂起工单后任务自然恢复执行
//   - 失败的恢复（adapter 报错）返回 false 而非错误：探活闭包契约只有 bool，
//     具体原因已由 Error 日志留痕，恢复路径按不存活处理是保守且安全的选择
//     （宁可交审核者裁决，不可静默吞掉仍在执行的任务事件）
func (m *Manager) ResumeTask(taskID string) bool {
	task, err := m.st.GetTask(taskID)
	if err != nil {
		m.log.Error("恢复读取任务失败", "task", taskID, "cause", err)
		return false
	}
	r, ok := m.ad.(restorer)
	if !ok {
		m.log.Warn("adapter 不支持执行恢复，任务按不存活处理", "task", taskID)
		return false
	}
	taskDir := filepath.Join(m.cfg.DataDir, "tasks", taskID)
	alive, err := r.Resume(taskID, taskDir, task.RepoPath, task.ExecutorSession)
	if err != nil {
		m.log.Error("重建任务执行失败", "task", taskID, "cause", err)
		return false
	}
	if alive {
		m.log.Info("任务执行已重建，重启中介循环", "task", taskID)
		go m.mediate(taskID)
	}
	return alive
}
