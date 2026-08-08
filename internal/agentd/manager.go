// 本文件实现 handoff 的状态机中枢与 adapter 事件中介（系统的心脏）。
//
// 职责：
//   - 任务生命周期的唯一状态写入者：dispatch（pending→running）、permission/question
//     中介（→waiting_answer→running）、result（→waiting_review）、continue/done（→running/completed）
//   - 把 executor 的 AdapterEvent 中介为「ticket + 事件 + 状态迁移」三件套：
//     permission/question 落 ticket 并挂到 hub.WaitAnswer 等审核者应答，
//     progress 只入库，result 落 completed/failed 事件进 waiting_review
//   - 审核者应答经 reply 回程（server）→ NotifyAnswer 唤醒等待 goroutine → 回传 executor
//   - stop：审核者主动中止任务（停 executor、作废工单、落 failed）
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
//   - dispatch 前经 workspace.PrepareWorkspace 准备任务工作区（分支×worktree，
//     脏工作区/非法参数拒绝），其余 git 操作（diff/fetch/run）由 server 路由直接调用 workspace 包
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
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

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
// 并发安全：无共享可变字段（st/hub/ads/cfg/log 构造后只读），
// 每个任务的中介 goroutine 与应答 goroutine 通过 store CAS + hub 路由协作。
// approver 相关的 in-flight/失败计数/停用表由 apMu 保护。
type Manager struct {
	st  *store.Store
	hub *Hub
	// ads 是 executor 注册表（name → Adapter），构造后只读 map，并发安全依旧。
	// 任务经 task.Executor（adapterFor）或缺省名（resolveExecutor）路由到对应实现。
	ads map[string]executor.Adapter
	cfg *config.Config
	log *slog.Logger
	// approver 是分级审批链的廉价模型裁决器；nil=不启用（二期前行为：
	// 权限请求直接升级人工审核者）。构造后只读。
	approver *Approver
	// 审批链运行时状态（apMu 保护）：
	//   - apInflight：正在裁决中的 ticket id 集合（防 SSE 重放双呼审批者）
	//   - apFails：每任务连续裁决失败计数（Err 非 nil 才累计）
	//   - apDisabled：已停用审批链的任务集合（连续失败 3 次）
	apMu       sync.Mutex
	apInflight map[string]bool
	apFails    map[string]int
	apDisabled map[string]bool
}

// NewManager 创建任务管理器。
//
// 参数：
//   - st: 持久化存储（任务/事件/工单的唯一落库点）
//   - hub: 进程内实时路由（事件广播 + ticket 应答等待）
//   - ads: executor 注册表（name → Adapter，如 {"opencode": ..., "fake": ...}）；
//     任务按 executor 名路由，缺省名取 cfg.Executor.Default
//   - cfg: 配置（DataDir 用于派生任务目录、Executor.Default 为缺省执行者名）
//   - approver: 审批链裁决器；nil=不启用
//   - log: 本模块日志入口
//
// 注意：
//   - 调用方须保证 log 为统一配置后的 logger；st/hub 必须已就绪
func NewManager(st *store.Store, hub *Hub, ads map[string]executor.Adapter, cfg *config.Config, approver *Approver, log *slog.Logger) *Manager {
	return &Manager{
		st: st, hub: hub, ads: ads, cfg: cfg, approver: approver, log: log,
		apInflight: map[string]bool{},
		apFails:    map[string]int{},
		apDisabled: map[string]bool{},
	}
}

// adapterFor 按任务记录的执行者名解析其 adapter。
//
// 规则：task.Executor 非空时按该名查注册表；为空（老任务兼容）回退缺省执行者
// （cfg.Executor.Default）。查不到时报错（错误带已注册名列表）。
func (m *Manager) adapterFor(taskID string) (executor.Adapter, error) {
	task, err := m.st.GetTask(taskID)
	if err != nil {
		return nil, err
	}
	name := task.Executor
	if name == "" {
		name = m.cfg.Executor.Default
	}
	ad, ok := m.ads[name]
	if !ok {
		m.log.Error("任务执行者未注册，无法路由", "task", taskID, "executor", name,
			"registered", registeredNames(m.ads))
		return nil, fmt.Errorf("任务 %s 执行者 %q 未注册", taskID, name)
	}
	return ad, nil
}

// resolveExecutor 在 dispatch 期把请求的执行者名解析为 adapter。
//
// 规则：name 空回退缺省（cfg.Executor.Default）；未注册返回 errBadDispatchRequest
// 包装的错误（server 层映射 400）并列出已注册名。
func (m *Manager) resolveExecutor(name string) (string, executor.Adapter, error) {
	if name == "" {
		name = m.cfg.Executor.Default
	}
	ad, ok := m.ads[name]
	if !ok {
		m.log.Warn("dispatch 指定未注册执行者", "executor", name, "registered", registeredNames(m.ads))
		return "", nil, fmt.Errorf("%w: 执行者 %q 未注册（可用: %s）", errBadDispatchRequest, name, strings.Join(registeredNames(m.ads), ", "))
	}
	return name, ad, nil
}

// registeredNames 返回注册表全部执行者名（按字母序，供错误提示与日志）。
func registeredNames(ads map[string]executor.Adapter) []string {
	names := make([]string, 0, len(ads))
	for n := range ads {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// DispatchReq 是 Dispatch 的入参：任务仓库、base64 计划与二期派发参数。
type DispatchReq struct {
	Repo     string // 任务仓库路径（executor 工作区）
	PlanB64  string // plan 内容，base64 编码（路由/CLI 层编码，此处解码）
	PlanName string // plan 文件名（归档展示用，写入 task 的 PlanPath 目录下）
	Target   string // 目标主机名（归档展示用，记入 task.Target）
	// Prompt 是无 plan 文件时的直接指令（prompt-only 派发）；与 PlanB64 至少其一
	// 非空。plan 非空时作为「附加指令」拼接在计划之后。
	Prompt string
	// Name 是任务展示名（空时从 plan 名/prompt 派生，见 deriveName）。
	Name string
	// Executor 是任务选择的执行者名；空=缺省（cfg.Executor.Default）。
	Executor string
	// Model 是任务级模型覆盖；空=配置 executor.model，再空=executor 自身默认。
	Model string
	// Branch / NewBranch 分支二选一（与 PrepareWorkspace 的 WorkspaceReq 一致）：
	// Branch=切到已存在分支；NewBranch=新建分支（空且 Branch 空=自动 handoff/<id8>）。
	Branch    string
	NewBranch string
	// Base 是新分支起点（仅与 NewBranch/自动分支连用；空=HEAD）。
	Base string
	// Worktree / NewWorktree worktree 二选一：Worktree=用户自带 worktree；
	// NewWorktree=在 DataDir/worktrees 下新建 managed worktree（done 时删除）。
	Worktree    string
	NewWorktree bool
	// BaseCommit 是审核者本地 HEAD 的提交号（40 位十六进制），用于校验任务仓库
	// 不落后于本地；空=不校验（本地派发或调用方 cwd 不是 git 仓库）。
	BaseCommit string
}

// planSummaryLimit 是 plan 摘要的截断上限（按 rune 计）。
//
// 为什么 200：plan_summary 是 attach/tasks 里「这个任务要干什么」的速览字段，
// 完整计划已落 plan_path 文件；上限同时防超长单行 plan（如压缩过的 markdown）
// 撑爆任务行与终端输出。
const planSummaryLimit = 200

// planSummaryFromContent 从 plan 内容生成任务摘要。
//
// 规则：取首个非空行（markdown 计划的标题位，如 `# 修复登录态丢失`），按
// planSummaryLimit 截断；内容为空或全空行时返回空串。
// 为什么用「首个非空行」而非「前 N 字符整体截断」：plan 开头常有空行/分隔线/
// 注释行，前 N 字符的摘要可能全是空白与噪音；首行标题是 markdown 惯例的意图
// 浓缩位，审核者一眼即知任务方向（P1-12）。
func planSummaryFromContent(content []byte) string {
	for _, line := range strings.Split(string(content), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return truncateRunes(trimmed, planSummaryLimit)
		}
	}
	return ""
}

// deriveName 决定任务的展示名，优先级：显式 Name > plan 文件名（去日期前缀与
// .md 后缀）> prompt 前 20 rune（不切断单词）。
//
// 为什么去日期前缀：约定 plan 文件常以 2026-08-08-<主题>.md 命名，日期前缀是
// 归档排序用的噪音，做任务名会让列表里全是「2026-08-08-…」看不出主题。
// 为什么 prompt 截断不切断单词：任务列表一行能展示的长度有限，直接取前 20 rune
// 会把「…改成 br」这类半截英文留在列表里；截到下一个空白处（或末尾）让名字
// 以完整单词收尾，可读性远好于硬切。
func deriveName(name, planName, prompt string) string {
	if name != "" {
		return name
	}
	if planName != "" {
		n := planName
		n = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}-`).ReplaceAllString(n, "")
		n = strings.TrimSuffix(n, ".md")
		if n != "" {
			return n
		}
	}
	r := []rune(prompt)
	const limit = 20
	if len(r) > limit {
		cut := limit
		// 截断处若切断单词（下一字符非空白），顺延过残缺单词到完整词尾；
		// 再吞掉紧随的空白，保证名字以「完整单词 + 分隔空白」收尾而不是
		// 半截英文（词尾恰好落在 limit 内时两循环都不触发，直接按 limit 切）
		for cut < len(r) && !unicode.IsSpace(r[cut]) {
			cut++
		}
		for cut < len(r) && unicode.IsSpace(r[cut]) {
			cut++
		}
		return string(r[:cut])
	}
	return string(r)
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

// deliveryFailedPayload 是 delivery_failed 事件的 payload：哪张工单没送到、
// 为什么、以及审核者该做什么。
type deliveryFailedPayload struct {
	TicketID string `json:"ticket_id"`
	Reason   string `json:"reason"`
	Hint     string `json:"hint"`
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

// approverDecisionPayload 是 approver_decision 事件的 payload：审批者对一次权限
// 请求的裁决结果。Decision 取 approve/escalate/error（error=裁决本身失败）。
type approverDecisionPayload struct {
	TicketID   string `json:"ticket_id"`
	Permission string `json:"permission"`
	Decision   string `json:"decision"`
	Reason     string `json:"reason"`
	ElapsedMS  int64  `json:"elapsed_ms"`
}

// approverDisabledPayload 是 approver_disabled 事件的 payload：任务级审批链
// 因连续失败被停用的原因。
type approverDisabledPayload struct {
	Reason string `json:"reason"`
}

// maxApproverFails 是同任务审批者连续裁决失败（Err 非 nil）多少次后停用审批链。
//
// 为什么 3：对「已损坏的审批者命令」（如二进制被卸载、模型服务永久报错）的一次
// 重试代价是整条权限请求被升级、审核者被叫醒一次——反复重试只会形成「每次权限都
// 升级 + 每次审批都失败」的重试风暴，烧人工注意力的同时毫无收益；3 次是「确认
// 不是偶发抖动」的合理样本量。
const maxApproverFails = 3

// permEventTextLimit 是 permission_request / approver_decision 事件 payload 里
// 权限描述的展示上限。事件是唤醒消息，短即可；全文在工单里，经 handoff show 取。
const permEventTextLimit = 200

// permEventText 把权限描述压成事件 payload 用的短文本，超限时带显式截断标记——
// 无标记的截断会让审核者以为看到的就是全部（这正是 B6 的根因），有标记才知道
// 要去 handoff show 看工单里的全文。
func permEventText(s string) string {
	if len([]rune(s)) <= permEventTextLimit {
		return s
	}
	return truncateRunes(s, permEventTextLimit) + executor.TruncationMarker
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
	m.log.Info("dispatch 进入", "repo", req.Repo, "plan_name", req.PlanName, "target", req.Target,
		"executor", req.Executor, "model", req.Model, "name", req.Name,
		"branch", req.Branch, "new_branch", req.NewBranch, "base", req.Base,
		"base_commit", req.BaseCommit, "worktree", req.Worktree, "new_worktree", req.NewWorktree)
	defer func() {
		if err != nil {
			m.log.Error("dispatch 失败", "repo", req.Repo, "cause", err)
		} else {
			m.log.Info("dispatch 完成", "task", task.ID)
		}
	}()

	// 校验：repo 必填；plan 与 prompt 至少其一（prompt-only 派发）
	if req.Repo == "" || (req.PlanB64 == "" && req.Prompt == "") {
		return nil, fmt.Errorf("%w: repo=%q plan_b64 长度=%d prompt 长度=%d",
			errBadDispatchRequest, req.Repo, len(req.PlanB64), len(req.Prompt))
	}
	// dispatch 期解析执行者：req.Executor 空回退缺省；未注册按参数错误拒绝（400）
	execName, ad, err := m.resolveExecutor(req.Executor)
	if err != nil {
		return nil, err
	}
	model := req.Model
	if model == "" {
		model = m.cfg.Executor.Model // 配置级兜底；仍空则 executor 自身默认
	}

	// 内容合成：plan 解码后作为主体；prompt 非空时——
	//   plan 非空：拼接为「附加指令」小节（为什么：prompt 是派发当刻的补充意图，
	//   与 plan 是同一任务的两个信息面，放进同一文件让 executor 一次读全，避免
	//   二次上下文丢失）；plan 空：prompt 即任务内容
	var planContent []byte
	if req.PlanB64 != "" {
		planContent, err = base64.StdEncoding.DecodeString(req.PlanB64)
		if err != nil {
			return nil, fmt.Errorf("%w: 解码 plan_b64: %v", errBadDispatchRequest, err)
		}
		if req.Prompt != "" {
			planContent = append(planContent, []byte("\n\n## 附加指令（派发时提供）\n\n"+req.Prompt)...)
		}
	} else {
		planContent = []byte(req.Prompt)
	}
	planName := req.PlanName
	if planName == "" {
		planName = "prompt.md" // prompt-only 的 PlanName 兜底：无 plan 文件时固定名归档
	}
	summary := planSummaryFromContent(planContent)
	name := deriveName(req.Name, req.PlanName, req.Prompt)

	now := time.Now().UTC()
	taskID := uuid.NewString()

	// 远程基线校验（B4）：放在工作区准备之前——基准不对时后面建的分支全是错的，
	// 且此刻还没有任何落库/建树副作用，拒发是干净的
	if err := EnsureBaseCommit(ctx, req.Repo, req.BaseCommit); err != nil {
		return nil, err
	}

	// 派发前置：按分支×worktree 正交请求准备工作区（脏检查/建分支/建 worktree）。
	// 为什么放在建任务之前：工作区准备是纯前置校验，失败时不落孤儿任务记录，
	// 审核者修好仓库后重新 dispatch 即可（见 Dispatch doc 注意）
	ws, err := PrepareWorkspace(ctx, WorkspaceReq{
		Repo: req.Repo, TaskID: taskID,
		Branch: req.Branch, NewBranch: req.NewBranch, Base: req.Base,
		Worktree: req.Worktree, NewWorktree: req.NewWorktree,
		WorktreesDir: filepath.Join(m.cfg.DataDir, "worktrees"),
	})
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
		m.compensateManagedWorktree(ctx, req.Repo, ws)
		return nil, fmt.Errorf("创建任务目录 %s: %w", taskDir, err)
	}
	planPath := filepath.Join(taskDir, planName)
	if err := os.WriteFile(planPath, planContent, 0o600); err != nil {
		m.compensateManagedWorktree(ctx, req.Repo, ws)
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
		// 二期字段创建时即已知，随 CreateTask 写入（WorkDir 原地模式存空串，
		// 由 proto.Task.Workdir() 回退到 RepoPath；worktree 模式存实际工作目录）
		Name:            name,
		Executor:        execName,
		Model:           model,
		WorkDir:         ws.WorkDir,
		WorktreeManaged: ws.Managed,
	}
	if err := m.st.CreateTask(task); err != nil {
		m.compensateManagedWorktree(ctx, req.Repo, ws)
		return nil, err
	}

	// 分支名经 SetTaskField 白名单写入（见 Dispatch doc 注意）
	if err := m.st.SetTaskField(taskID, "branch", ws.Branch); err != nil {
		m.log.Error("写入任务分支失败", "task", taskID, "branch", ws.Branch, "cause", err)
		// 分支已在仓库建好但任务记录写不上：按派发失败处理，落 failed 供人工清理
		m.transitBestEffort(taskID, proto.TaskStateFailed, "写分支名失败")
		return nil, fmt.Errorf("记录任务分支: %w", err)
	}
	// 内存态同步补上 branch，保证传给 adapter 的 StartReq.Task 完整
	task.Branch = ws.Branch

	// plan 摘要经 SetTaskField 白名单落库（P1-12）：PlanPath 是 agentd 侧文件路径，
	// 审核者读不到——spec §7 要求全新会话能知道「这个任务本来要干什么」，
	// plan_summary 就是 attach/tasks 里那一眼速览。失败与 branch 同款按派发失败处理
	if err := m.st.SetTaskField(taskID, "plan_summary", summary); err != nil {
		m.log.Error("写入任务 plan 摘要失败", "task", taskID, "cause", err)
		m.transitBestEffort(taskID, proto.TaskStateFailed, "写 plan 摘要失败")
		return nil, fmt.Errorf("记录任务 plan 摘要: %w", err)
	}
	task.PlanSummary = summary
	m.log.Info("plan 摘要已生成", "task", taskID, "summary", truncateRunes(summary, 40))
	m.log.Info("工作区就绪", "task", taskID, "workdir", ws.WorkDir, "managed", ws.Managed)

	if err := ad.Start(ctx, executor.StartReq{Task: *task, PlanContent: string(planContent), TaskDir: taskDir}); err != nil {
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

// compensateManagedWorktree 在 dispatch 后续步骤失败时补偿清理已建的 managed
// worktree（P2-2）。
//
// why：PrepareWorkspace 成功意味着 worktree 已在磁盘建好，若随后任务记录落库前
// （MkdirAll/WriteFile/CreateTask）失败，没有任何任务持有该 worktree——done 的
// 清理只认 WorktreeManaged 的任务记录，无记录则永不清理，worktree 成为孤儿
// 永久占用磁盘。失败只记 Error，不覆盖/不替换原始派发错误。
func (m *Manager) compensateManagedWorktree(ctx context.Context, repo string, ws Workspace) {
	if !ws.Managed || ws.WorkDir == "" {
		return
	}
	m.log.Warn("dispatch 后续失败，补偿清理 managed worktree", "repo", repo, "workdir", ws.WorkDir)
	if err := RemoveManagedWorktree(ctx, repo, ws.WorkDir); err != nil {
		m.log.Error("补偿清理 managed worktree 失败", "repo", repo, "workdir", ws.WorkDir, "cause", err)
	}
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
	// 只有 taskID：经 adapterFor 解析该任务实际使用的 adapter（executor 已不在
	// 按 ErrTaskNotRunning 同语义处置——Send 由 executor 实现方自身包装该哨兵）
	ad, err := m.adapterFor(taskID)
	if err != nil {
		// 执行者无法路由（如任务记录损坏/缺省执行者未注册）：退回 waiting_review，
		// 审核者可见原因后可处置，不让任务死在 running
		m.transitBestEffort(taskID, proto.TaskStateWaitingReview, "continue 路由执行者失败回迁")
		return fmt.Errorf("解析任务 %s 执行者: %w", taskID, err)
	}
	if err := ad.Send(ctx, taskID, instructions); err != nil {
		m.log.Error("续发指令失败", "task", taskID, "cause", err)
		// 回迁 waiting_review：指令没送达，回到审核者可重试的位置，不让任务死在 running
		m.transitBestEffort(taskID, proto.TaskStateWaitingReview, "continue 发送失败回迁")
		return fmt.Errorf("续发指令: %w", err)
	}
	return nil
}

// Done 归档任务：要求任务处于 waiting_review，迁移 completed 后调用 Adapter.Stop
// 回收 executor 侧资源，并清理 agentd 管理的 worktree。
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
	// 任务归档：清理审批链运行时状态，防内存 map 随归档任务无界增长（P2-5）
	m.clearApproverState(taskID)
	// done 只持有 taskID：经 adapterFor 解析该任务实际使用的 adapter；解析失败
	// 仅 Error 日志不影响归档（任务已完成，executor 残留交给人工兜底，见 doc 注意）
	ad, err := m.adapterFor(taskID)
	if err != nil {
		m.log.Error("解析任务执行者失败", "task", taskID, "cause", err)
	} else if err := ad.Stop(taskID); err != nil {
		m.log.Error("停止 executor 失败", "task", taskID, "cause", err)
	}
	// worktree 清理（Stop 之后、err 已定型不覆盖）：agentd 管理的 worktree 随任务
	// 完成删除，释放磁盘并防止「每个任务一个残留目录」的无界堆积。
	//
	// 为什么只删 managed：用户自带 worktree（Managed=false）是审核者自己的资产，
	// agentd 无权删别人的工作树；为什么失败只降级不阻塞归档：任务已审核通过，
	// 残树是运维问题不是任务问题——留一条带原因的 progress 事件提示人工处理
	if cur.WorktreeManaged && cur.WorkDir != "" {
		m.log.Info("done 清理 managed worktree", "task", taskID, "workdir", cur.WorkDir)
		if werr := RemoveManagedWorktree(ctx, cur.RepoPath, cur.WorkDir); werr != nil {
			m.log.Error("清理 managed worktree 失败", "task", taskID, "workdir", cur.WorkDir, "cause", werr)
			if evt, aerr := m.st.AppendEvent(taskID, proto.EventTypeProgress, progressPayload{
				Text: fmt.Sprintf("worktree 清理失败：%v，请手动 git worktree remove", werr),
			}); aerr != nil {
				m.log.Error("追加 worktree 清理失败事件失败", "task", taskID, "cause", aerr)
			} else {
				m.hub.Publish(evt)
			}
		} else {
			m.log.Info("managed worktree 已清理", "task", taskID, "workdir", cur.WorkDir)
		}
	}
	return nil
}

// Stop 主动中止一个任务：停 executor、作废挂起工单、落 failed 并唤醒审核者。
//
// 参数：
//   - ctx: 上层上下文（HTTP 请求）
//   - taskID: 待中止的任务
//
// 返回：
//   - store.ErrNotFound: 任务不存在
//   - store.ErrBadTransit: 任务已是终态（completed/failed），无可中止
//   - 其余：落库失败
//
// 注意：
//   - 复用 failed 终态而不新增 aborted：状态机零改动，且 failed→running 已允许，
//     中止后仍可重新派发。「人为中止」与「真失败」的区分靠 failed 事件的
//     fail_reason 文本，不靠状态
//   - 不删分支、不删 worktree：那是 handoff done 归档时的职责，stop 只负责让它停下
//   - adapter.Stop 失败只 Warn 不中断：目的是让任务离开活跃态，executor 残留
//     由 tmux 会话兜底，不能因为「停不掉进程」就让任务永远卡在 running
func (m *Manager) Stop(ctx context.Context, taskID string) (err error) {
	m.log.Info("stop 进入", "task", taskID)
	defer func() {
		if err != nil {
			m.log.Error("stop 失败", "task", taskID, "cause", err)
		} else {
			m.log.Info("stop 完成", "task", taskID)
		}
	}()

	cur, err := m.st.GetTask(taskID)
	if err != nil {
		return err
	}
	if cur.State == proto.TaskStateCompleted || cur.State == proto.TaskStateFailed {
		m.log.Warn("stop 状态不允许", "task", taskID, "state", cur.State)
		return fmt.Errorf("任务 %s 已是终态 %s，无可中止: %w", taskID, cur.State, store.ErrBadTransit)
	}

	ad, aerr := m.adapterFor(taskID)
	if aerr != nil {
		m.log.Error("解析任务执行者失败", "task", taskID, "cause", aerr)
	} else if serr := ad.Stop(taskID); serr != nil {
		m.log.Warn("停止 executor 失败，继续落 failed", "task", taskID, "cause", serr)
	}

	if voided, verr := m.st.VoidPendingTickets(taskID); verr != nil {
		m.log.Error("作废挂起工单失败", "task", taskID, "cause", verr)
	} else if voided > 0 {
		m.log.Warn("任务被中止，挂起工单作废", "task", taskID, "voided", voided)
	}

	evt, err := m.st.AppendEvent(taskID, proto.EventTypeFailed, failedPayload{
		FailReason: "审核者主动中止（handoff stop）",
	})
	if err != nil {
		return fmt.Errorf("追加中止事件: %w", err)
	}
	if err := m.transit(taskID, proto.TaskStateFailed, "stop"); err != nil {
		return err
	}
	// 审批链运行时状态随任务终结清理，防内存 map 无界增长（与 Done 同款）
	m.clearApproverState(taskID)
	m.hub.Publish(evt)
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
		decision := gateDecision(answer)
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
		ad, err := m.adapterFor(taskID)
		if err != nil {
			return fmt.Errorf("解析任务 %s 执行者: %w", taskID, err)
		}
		if err := ad.RespondPermission(actx, taskID, permID, decision); err != nil {
			return fmt.Errorf("中继权限应答: %w", err)
		}
		m.markDelivered(taskID, ticketID)
		return nil
	case "ask":
		m.log.Info("reply 无等待者，自愈中继提问回答", "task", taskID, "ticket", ticketID)
		actx, acancel := unaryCtx(context.Background())
		defer acancel()
		ad, err := m.adapterFor(taskID)
		if err != nil {
			return fmt.Errorf("解析任务 %s 执行者: %w", taskID, err)
		}
		if err := ad.Send(actx, taskID, answer); err != nil {
			return fmt.Errorf("中继提问回答: %w", err)
		}
		m.markDelivered(taskID, ticketID)
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
	ad, err := m.adapterFor(taskID)
	if err != nil {
		// 任务执行者无法路由（缺省执行者未注册/记录损坏）：中介循环无从消费
		// 事件，按 executor 已不在处置——转交审核者裁决，不静默退出
		m.log.Error("中介循环无法路由执行者", "task", taskID, "cause", err)
		return
	}
	events := ad.Events(taskID)
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
// 审批链前置的时序说明（二期新增）：approver 启用且黑名单未命中时，请求先交
// consultApprover 做廉价模型裁决——approve 全程不动状态机（任务保持 running，
// executor 收 once 后继续跑），escalate 则完整走下方「落状态→建工单→waiter→
// Publish」的原契约。因此本函数的中介主体被提取为 escalatePermission，供原路径
// 与审批者 escalate 路径共用，保证两路行为完全一致。
//
// 审批链异步化对原契约的修正（P1-1）：一期 handlePermission 在 mediate 循环内
// 同步执行，同任务的 permission/result 天然串行，「escalate 完整走原契约」的
// 前提隐含了这一点；二期起 consultApprover 是独立 goroutine（最长 60s），裁决
// 期间任务可能已被 handleResult/done 推进到终态。因此 escalatePermission 只能
// 在任务仍处 running/waiting_answer 时被调用——consultApprover 在分流前重读
// 任务快照，状态已终态则只留 approver_decision 审计事件。
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
	if m.isPermissionReplay(taskID, ev.PermissionID, ticketID) {
		return
	}
	// 审批链前置分流：需要咨询审批者（且未停用/未命中黑名单）时，异步裁决——
	// 审批期间不阻塞 mediate 循环，同任务后续 progress 事件照常入库广播。
	// 已在裁决中的重放（markApproverInflight 返回 false）直接吞掉，不重复咨询。
	if m.shouldConsultApprover(taskID, ev.Text) {
		if m.markApproverInflight(ticketID) {
			go m.consultApprover(ctx, taskID, ev, ticketID)
		}
		return
	}
	m.escalatePermission(ctx, taskID, ev, ticketID)
}

// escalatePermission 把权限请求升级人工审核者：落状态 → 建工单 → 追加事件 →
// waiter → Publish（一期 handlePermission 的完整既有行为，顺序契约见其 doc）。
//
// 本函数同时是审批者 escalate 路径的出口——两路共用保证行为一致。
//
// 工单存权限描述全文，事件 payload 另行截断——全文是审核者裁决的依据，不能只存
// 唤醒用的摘要。
func (m *Manager) escalatePermission(ctx context.Context, taskID string, ev executor.AdapterEvent, ticketID string) {
	// 先落状态再建工单（U-1）：审核者经 attach 读到挂起工单后会立即 reply，
	// 「工单已可见但状态还没落 waiting_answer」这段窗口里的 reply 会走完中继、
	// resumeIfIdle 读到 running 直接返回，随后 manager 才盖上 waiting_answer——
	// 任务显示「等你回答」却零挂起工单，reply/continue/done 三条路全封死。
	// 反过来「状态已落但工单还没建」是安全的：reply 找不到工单只会 404，
	// 审核者重试即可，且工单随即出现。
	m.transitBestEffort(taskID, proto.TaskStateWaitingAnswer, "permission_request")
	req, _ := json.Marshal(ticketRequest{Kind: "gate", Permission: ev.Text})
	if _, err := m.st.CreateTicket(&proto.Ticket{
		ID: ticketID, TaskID: taskID, Kind: "gate",
		Request: req, CreatedAt: time.Now().UTC(),
	}); err != nil {
		m.log.Error("创建权限工单失败", "task", taskID, "perm", ev.PermissionID, "ticket", ticketID, "cause", err)
		// 工单没建成，waiting_answer 是虚假状态（无任何可答项），回迁 running
		m.transitBestEffort(taskID, proto.TaskStateRunning, "权限工单创建失败回滚")
		return
	}
	evt, err := m.st.AppendEvent(taskID, proto.EventTypePermissionRequest, permissionPayload{
		TicketID: ticketID, Permission: permEventText(ev.Text), Kind: "gate",
	})
	if err != nil {
		// 工单在、事件缺：不回滚工单，留给下一次重放由 isPermissionReplay 的
		// 「有工单无事件」分支自愈补发（N-4）
		m.log.Error("追加 permission_request 事件失败", "task", taskID, "ticket", ticketID, "cause", err)
		return
	}
	m.log.Info("权限升级人工审核者", "task", taskID, "ticket", ticketID,
		"perm_chars", len([]rune(ev.Text)), "event_truncated", len([]rune(ev.Text)) > permEventTextLimit)
	go m.waitPermission(ctx, taskID, ev.PermissionID, ticketID)
	m.hub.Publish(evt)
}

// shouldConsultApprover 判断该权限请求是否应走审批者裁决：
// 审批者已启用、任务未被停用、权限文本未命中黑名单、权限描述完整（未被截断）。
func (m *Manager) shouldConsultApprover(taskID, permission string) bool {
	if m.approver == nil {
		return false
	}
	m.apMu.Lock()
	disabled := m.apDisabled[taskID]
	m.apMu.Unlock()
	if disabled {
		m.log.Debug("审批链已停用，直接升级审核者", "task", taskID)
		return false
	}
	// 权限描述含截断标记（P1-3）：看到的是不完整的命令，危险片段可能落在
	// 截断之外——黑名单与廉价模型都不可信，直接升级人工是 fail-closed 的
	// 自然延伸（executor 侧截断标记见 executor.TruncationMarker 契约）
	if strings.Contains(permission, executor.TruncationMarker) {
		m.log.Warn("权限描述含截断标记，跳过审批者直接升级", "task", taskID,
			"permission", truncateRunes(permission, 120))
		return false
	}
	hit, _ := m.approver.Blacklisted(permission)
	return !hit
}

// markApproverInflight 尝试登记 ticket 的审批中状态；返回 false 表示已有同
// ticket 的裁决在途（SSE 重放），本次直接吞掉不重复咨询。
func (m *Manager) markApproverInflight(ticketID string) bool {
	m.apMu.Lock()
	defer m.apMu.Unlock()
	if m.apInflight[ticketID] {
		return false
	}
	m.apInflight[ticketID] = true
	return true
}

// consultApprover 在独立 goroutine 里完成一次权限请求的审批者裁决（不阻塞
// mediate 循环）。流程：Decide → 落 approver_decision 审计事件（不 Publish）→
// 按结果分流：approve 自动放行 / escalate 升级人工审核者 / Err 计数并 fail-closed。
func (m *Manager) consultApprover(ctx context.Context, taskID string, ev executor.AdapterEvent, ticketID string) {
	defer func() {
		m.apMu.Lock()
		delete(m.apInflight, ticketID)
		m.apMu.Unlock()
	}()
	m.log.Info("审批者开始裁决", "task", taskID, "ticket", ticketID,
		"perm", ev.PermissionID, "text", truncateRunes(ev.Text, 80))
	// 任务摘要取 PlanSummary；GetTask 失败用空串——摘要是上下文不是裁决前提，
	// 不因读失败把整个裁决拖下水
	summary := ""
	if task, err := m.st.GetTask(taskID); err == nil {
		summary = task.PlanSummary
	}
	d := m.approver.Decide(ctx, ev.Text, summary)

	decision := "error"
	switch {
	case d.Approve:
		decision = "approve"
	case d.Err == nil:
		decision = "escalate"
	}
	// 只入库不 Publish：approve 路径无人可唤醒（已自动放行），escalate 路径的
	// 唤醒由紧随其后的 permission_request 完成；approver_decision 是审计记录，
	// 审核者经 show 可见
	if _, err := m.st.AppendEvent(taskID, proto.EventTypeApproverDecision, approverDecisionPayload{
		TicketID: ticketID, Permission: permEventText(ev.Text), Decision: decision,
		Reason: d.Reason, ElapsedMS: d.ElapsedMS,
	}); err != nil {
		m.log.Error("追加 approver_decision 事件失败", "task", taskID, "ticket", ticketID, "cause", err)
	}

	// 重读任务状态后分流（P1-1）：审批链异步化打破了「permission 与 mediate 循环
	// 串行」的一期前提——Decide 是最长 60s 的独立 goroutine，窗口内 executor 可能
	// 死亡（handleResult 落 waiting_review）或被 done 归档（completed）。此时若
	// 照旧建工单/唤醒/回传，会重现 U-1/U-3 专门修掉的那类矛盾形态：状态已终态却
	// 带 pending 权限工单，reply 后答案守卫被消耗、RespondPermission 对已死
	// executor 失败。因此只在任务仍在 running/waiting_answer（可继续中介）时才
	// 分流；否则仅留 audit 事件 + Warn，不建工单、不 Publish、不回传。
	cur, gerr := m.st.GetTask(taskID)
	if gerr != nil {
		m.log.Error("审批者裁决后重读任务失败，按已终结处理", "task", taskID, "cause", gerr)
		return
	}
	if cur.State != proto.TaskStateRunning && cur.State != proto.TaskStateWaitingAnswer {
		m.log.Warn("审批者裁决期间任务已离开 running/waiting_answer，仅留审计事件",
			"task", taskID, "ticket", ticketID, "decision", decision, "state", cur.State)
		return
	}

	if d.Err != nil {
		// fail-closed：裁决失败升级审核者，并按连续失败计数决定是否停用
		//（防对已损坏审批者命令的重试风暴，见 maxApproverFails 的 why）
		m.countApproverFail(taskID)
		m.escalatePermission(ctx, taskID, ev, ticketID)
		return
	}
	// 干净裁决（approve 或 escalate）：失败计数清零
	m.apMu.Lock()
	delete(m.apFails, taskID)
	m.apMu.Unlock()

	if d.Approve {
		m.approvePermission(taskID, ticketID, ev.PermissionID, ev.Text, d.Reason)
		return
	}
	m.escalatePermission(ctx, taskID, ev, ticketID)
}

// clearApproverState 清理任务级审批链运行时状态（apFails/apDisabled）。
//
// 调用点：任务终结处——Done 归档（→completed）与 handleResult 的回合结束
// （→waiting_review）。为什么必须清理（P2-5）：这两张是进程内内存 map，任务
// 归档后若不清，条目随任务数无界增长；且任务被续接时旧的禁用标记/失败计数
// 也不该残留（新回合从干净状态重新评估审批链）。
func (m *Manager) clearApproverState(taskID string) {
	m.apMu.Lock()
	delete(m.apFails, taskID)
	delete(m.apDisabled, taskID)
	m.apMu.Unlock()
}

// countApproverFail 累计一次任务级裁决失败，达到 maxApproverFails 时停用该任务
// 的审批链并留 approver_disabled 审计事件（一次）。
func (m *Manager) countApproverFail(taskID string) {
	m.apMu.Lock()
	defer m.apMu.Unlock()
	m.apFails[taskID]++
	fails := m.apFails[taskID]
	if fails >= maxApproverFails && !m.apDisabled[taskID] {
		m.apDisabled[taskID] = true
		if _, err := m.st.AppendEvent(taskID, proto.EventTypeApproverDisabled, approverDisabledPayload{
			Reason: fmt.Sprintf("连续 %d 次裁决失败，审批链已停用（后续权限直接升级人工审核者）", maxApproverFails),
		}); err != nil {
			m.log.Error("追加 approver_disabled 事件失败", "task", taskID, "cause", err)
		}
		m.log.Warn("审批者连续失败已停用", "task", taskID, "fails", fails)
	}
}

// approvePermission 审批者批准路径：幂等建工单并自动答题 → RespondPermission(once)
// → 标记送达。全程不动任务状态机——任务保持 running，executor 收 once 后继续跑。
//
// 为什么不动状态：批准不是「任务被挂起等人工」，executor 恢复执行即续跑，
// 状态机不必经过 waiting_answer（那是「有未决人工事项」的语义，此处没有）。
func (m *Manager) approvePermission(taskID, ticketID, permID, permission, reason string) {
	m.log.Info("审批者自动批准权限", "task", taskID, "ticket", ticketID,
		"perm", permID, "reason", truncateRunes(reason, 80))
	req, _ := json.Marshal(ticketRequest{Kind: "gate", Permission: permission})
	if _, err := m.st.CreateTicket(&proto.Ticket{
		ID: ticketID, TaskID: taskID, Kind: "gate",
		Request: req, CreatedAt: time.Now().UTC(),
	}); err != nil {
		// 工单建不起来批准就无法落审计，按裁决失败处理（fail-closed）
		m.log.Error("审批者批准：创建工单失败", "task", taskID, "ticket", ticketID, "cause", err)
		m.countApproverFail(taskID)
		return
	}
	// 工单 answer 落**精确 "allow"**（P0-2）：gate 翻译规则是 answer 严格等于
	// "allow" 才放行，塞理由进 answer 会让审批者的批准在 resume 重投
	// （RelayAnswer）时被翻转成 reject；理由已完整落在 approver_decision 事件的
	// Reason 字段，answer 只需表达「批准」这一动作。
	if err := m.st.AnswerTicket(ticketID, "allow"); err != nil {
		m.log.Error("审批者批准：应答失败", "task", taskID, "ticket", ticketID, "cause", err)
		m.countApproverFail(taskID)
		return
	}
	ad, err := m.adapterFor(taskID)
	if err != nil {
		// 工单已被 AnswerTicket 消耗（answer IS NULL 守卫失效），executor 仍原地
		// 阻塞等待——必须产出 delivery_failed 事件让审核者知道该 resume（P1-4），
		// 与紧邻的 RespondPermission 失败分支一致；只记 Error 会让审核者毫无感知
		m.log.Error("审批者批准：解析执行者失败", "task", taskID, "cause", err)
		m.NoteDeliveryFailed(taskID, ticketID, err)
		return
	}
	actx, acancel := unaryCtx(context.Background())
	defer acancel()
	if err := ad.RespondPermission(actx, taskID, permID, "once"); err != nil {
		m.log.Error("审批者批准：回传 executor 失败", "task", taskID, "perm", permID, "cause", err)
		m.NoteDeliveryFailed(taskID, ticketID, err)
		return
	}
	m.markDelivered(taskID, ticketID)
	m.log.Info("审批者批准已送达", "task", taskID, "ticket", ticketID)
}

// isPermissionReplay 判定一次 permission 事件是否为「已完整中介过」的重放
// （SSE 断线重连 / agentd 重启后订阅重建都会重放同一权限请求）。
//
// 参数：permID 是 executor 侧裸权限 id（仅用于日志），ticketID 是命名空间化的工单 id。
//
// 返回：true 表示应跳过全部中介动作。
//
// 判定规则（why 这里不能只看工单是否存在）：
//   - 工单不存在 → 新请求，正常中介
//   - 工单存在且已应答 → 真重放，跳过：再动一次就是重复交付
//   - 工单存在但通知事件缺失 → **不是**重放，而是崩溃恰好落在「建工单」与
//     「追加事件」之间留下的半截状态。仅凭工单存在就跳过，会让 permission_request
//     永不产生、状态停在 running、无等待者，审核者的 wait 永远不触发（N-4）——
//     此处放行以补发事件，CreateTicket 本身幂等，不会产生第二张工单
//   - 工单存在且事件也在 → 真重放，跳过（P1-7 的幂等承诺）
func (m *Manager) isPermissionReplay(taskID, permID, ticketID string) bool {
	tk, err := m.st.GetTicket(ticketID)
	if errors.Is(err, store.ErrNotFound) {
		return false
	}
	if err != nil {
		m.log.Error("读取权限工单失败", "task", taskID, "perm", permID, "ticket", ticketID, "cause", err)
		return true // 读不到就不动，宁可少一次唤醒也不重复中介
	}
	if tk.Answer != nil {
		m.log.Debug("权限请求重放且已应答，跳过中介", "task", taskID, "perm", permID, "ticket", ticketID)
		return true
	}
	hasEvent, err := m.st.TicketHasEvent(taskID, ticketID)
	if err != nil {
		m.log.Error("查询权限工单通知事件失败", "task", taskID, "ticket", ticketID, "cause", err)
		return true
	}
	if hasEvent {
		m.log.Debug("权限请求重放，跳过中介", "task", taskID, "perm", permID, "ticket", ticketID)
		return true
	}
	m.log.Warn("权限工单存在但通知事件缺失，补发以自愈", "task", taskID, "perm", permID, "ticket", ticketID)
	return false
}

// gateDecision 把 gate 工单的应答翻译成 executor 的 decision，规则单一：
// answer trim 后严格等于 "allow" → "once"（批准本次），其余一律 "reject"。
//
// 为什么严格相等（P0-2）：answer 是契约值，只有精确的 allow 才代表批准；
// 审批者自动批准写入的也是精确 "allow"（理由在 approver_decision 事件的
// Reason 字段）——若把理由塞进 answer，resume 重投（RelayAnswer）时这条长串
// 会落在「其余一律 reject」上，审批者明确批准的操作被系统自己改判为拒绝。
//
// 两处调用（waitPermission 的应答回传、RelayAnswer 的自愈中继）必须走同一
// 翻译，复制字面量就是漂移面。
func gateDecision(answer string) string {
	if strings.TrimSpace(answer) == "allow" {
		return "once"
	}
	return "reject"
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
	decision := gateDecision(ans)
	// 派生子 ctx 只约束调用本身（unaryCtx 的 why）：等答案阶段早已结束，此处的
	// parent 是任务级 ctx（取消无截止），不加超时的话半死 executor 会让本调用挂死
	actx, acancel := unaryCtx(ctx)
	defer acancel()
	ad, err := m.adapterFor(taskID)
	if err != nil {
		m.log.Error("解析任务执行者失败", "task", taskID, "cause", err)
		return
	}
	if err := ad.RespondPermission(actx, taskID, permID, decision); err != nil {
		// executor 侧可能已不在（进程被杀）：记录错误并保持现状，交由审核者裁决。
		// 工单未标记送达，审核者可用 handoff resume 重投（见 RecoverStuck）
		m.log.Error("回应权限失败", "task", taskID, "perm", permID, "decision", decision, "cause", err)
		m.NoteDeliveryFailed(taskID, ticketID, err)
		return
	}
	m.markDelivered(taskID, ticketID)
}

// handleQuestion 中介提问：ticket(uuid, kind=ask) → 事件 → waiting_answer →
// goroutine 等审核者回答后原样透传 executor。
//
// 顺序契约与 handlePermission 相同（P1-2）：先置 waiting_answer 再 Publish；
// waiter 注册异步，reply 先于注册到达时退化为自愈中继路径兜底。
func (m *Manager) handleQuestion(ctx context.Context, taskID string, ev executor.AdapterEvent) {
	// 提问工单 id 用 uuid：问题没有天然稳定 id，回答一次即终结
	ticketID := uuid.NewString()
	// 先落状态再建工单：why 同 handlePermission（U-1）
	m.transitBestEffort(taskID, proto.TaskStateWaitingAnswer, "question")
	req, _ := json.Marshal(ticketRequest{Kind: "ask", Question: ev.Text})
	created, err := m.st.CreateTicket(&proto.Ticket{
		ID: ticketID, TaskID: taskID, Kind: "ask",
		Request: req, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		m.log.Error("创建提问工单失败", "task", taskID, "ticket", ticketID, "cause", err)
		m.transitBestEffort(taskID, proto.TaskStateRunning, "提问工单创建失败回滚")
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
		m.log.Error("追加 question 事件失败", "task", taskID, "ticket", ticketID, "cause", err)
		return
	}
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
	ad, err := m.adapterFor(taskID)
	if err != nil {
		m.log.Error("解析任务执行者失败", "task", taskID, "cause", err)
		return
	}
	if err := ad.Send(actx, taskID, ans); err != nil {
		m.log.Error("回发提问回答失败", "task", taskID, "ticket", ticketID, "cause", err)
		m.NoteDeliveryFailed(taskID, ticketID, err)
		return
	}
	m.markDelivered(taskID, ticketID)
}

// RecoverReport 是显式恢复操作的结果快照，原样作为 HTTP 响应体回给 CLI。
type RecoverReport struct {
	Task string `json:"task"`
	// Redelivered 是本次成功重投给 executor 的应答条数
	Redelivered int `json:"redelivered"`
	// ExecutorGone 为真表示 executor 已不在，任务已被交给审核者裁决
	ExecutorGone bool `json:"executor_gone"`
	// State 是操作完成后的任务状态
	State proto.TaskState `json:"state"`
	// Note 是给审核者看的一句话结论
	Note string `json:"note"`
}

// RecoverStuck 是审核者的显式恢复操作（CLI: handoff resume <task>），
// 用来解开「应答已落库但没送到 executor」这一类卡死。
//
// 为什么需要它：reply 的回程里，应答一旦落库就消耗掉了工单的 answer IS NULL
// 守卫；若此时中继失败（executor 半死、调用超时），审核者会拿到 502，而工单
// 已从 pending 里消失、任务停在 waiting_answer——reply 得 404、continue/done
// 得 409，CLI 上再无一条可走的路。此前唯一的出口是运维重启 agentd 让
// RecoverOnStartup 探活，而那条路只在 executor **已死**时有效：executor 还
// 活着并仍阻塞在权限上时，重启探活成功、订阅重建、已答工单从不重放，是彻底的
// 死锁。本方法把这条出口交到审核者自己手里。
//
// 参数：
//   - taskID: 任务 ID
//
// 返回：
//   - 恢复结果快照（即使返回错误也可能非 nil，用于区分「executor 已死」与「这次没成功」）
//   - 任务不存在、已终结，或重投过程中 executor 仍不可用时返回错误
//
// 行为：
//  1. 无未送达应答 → 空操作，不碰状态、不调用 executor
//  2. 有未送达应答 → 逐条重投；成功即标记送达，全部成功后任务回 running
//  3. 重投遇到 executor.ErrTaskNotRunning（executor 确实不在）→ 追加 failed
//     事件、作废挂起工单、任务转 waiting_review 交审核者，不再重试
//  4. 重投遇到其他错误（executor 还在，只是这次调用失败）→ 保持 waiting_answer
//     与未送达标记，返回错误；审核者稍后可再执行一次
//
// 注意：
//   - 幂等：已标记送达的应答不会被重投，重复执行是安全的
//   - 与 ResumeTask 的区别：ResumeTask 是 agentd 重启时的执行器存活探测与订阅
//     重建（进程级），本方法是单任务的应答重投（工单级），两者互不替代
func (m *Manager) RecoverStuck(taskID string) (*RecoverReport, error) {
	task, err := m.st.GetTask(taskID)
	if err != nil {
		return nil, fmt.Errorf("读取任务 %s: %w", taskID, err)
	}
	if task.State == proto.TaskStateCompleted || task.State == proto.TaskStateFailed {
		return nil, fmt.Errorf("任务 %s 已终结（%s），无可恢复项: %w", taskID, task.State, store.ErrBadTransit)
	}
	rep := &RecoverReport{Task: taskID, State: task.State}

	stuck, err := m.st.UndeliveredAnswers(taskID)
	if err != nil {
		return nil, err
	}
	if len(stuck) == 0 {
		rep.Note = "没有卡在半路的应答，无需恢复"
		m.log.Info("恢复操作：无未送达应答", "task", taskID, "state", task.State)
		return rep, nil
	}
	m.log.Info("恢复操作：开始重投未送达应答", "task", taskID, "count", len(stuck))

	for _, tk := range stuck {
		answer := ""
		if tk.Answer != nil {
			answer = *tk.Answer
		}
		if err := m.RelayAnswer(taskID, tk.ID, answer); err != nil {
			if errors.Is(err, executor.ErrTaskNotRunning) {
				// executor 确实不在：继续重投没有意义，转交审核者裁决
				rep.ExecutorGone = true
				rep.State = m.abandonToReview(taskID, tk.ID, err)
				rep.Note = "executor 已不在，任务已转交审核（可 continue 重新派发或 done 归档）"
				m.log.Warn("恢复操作：executor 已不在，任务转交审核",
					"task", taskID, "ticket", tk.ID, "cause", err)
				return rep, nil
			}
			// executor 还在，只是这次没打通：保持现状可重试
			rep.Note = "重投失败，executor 仍在但未响应；稍后可再执行一次 resume"
			m.log.Error("恢复操作：重投应答失败", "task", taskID, "ticket", tk.ID, "cause", err)
			return rep, fmt.Errorf("重投应答 %s: %w", tk.ID, err)
		}
		rep.Redelivered++
	}

	// 全部送达：executor 已恢复执行，状态回 running（若已有其他挂起工单，
	// transitBestEffort 的失败是良性的——状态本就该留在 waiting_answer）
	if task.State == proto.TaskStateWaitingAnswer {
		m.transitBestEffort(taskID, proto.TaskStateRunning, "恢复操作重投应答成功")
	}
	if cur, gerr := m.st.GetTask(taskID); gerr == nil {
		rep.State = cur.State
	}
	rep.Note = "应答已重新送达 executor，执行继续"
	m.log.Info("恢复操作完成", "task", taskID, "redelivered", rep.Redelivered, "state", rep.State)
	return rep, nil
}

// abandonToReview 在确认 executor 已不在时收尾：留下 failed 事件说明原因、
// 作废挂起工单（避免 attach 继续展示不可能被回答的项）、任务转 waiting_review。
// 返回收尾后的任务状态。
func (m *Manager) abandonToReview(taskID, ticketID string, cause error) proto.TaskState {
	if voided, verr := m.st.VoidPendingTickets(taskID); verr != nil {
		m.log.Error("恢复操作：作废挂起工单失败", "task", taskID, "cause", verr)
	} else if voided > 0 {
		m.log.Warn("恢复操作：挂起工单作废", "task", taskID, "voided", voided)
	}
	evt, err := m.st.AppendEvent(taskID, proto.EventTypeFailed, failedPayload{
		FailReason: fmt.Sprintf("恢复操作发现 executor 已不在，应答 %s 无法送达: %v", ticketID, cause),
	})
	if err != nil {
		m.log.Error("恢复操作：追加 failed 事件失败", "task", taskID, "cause", err)
	}
	if terr := m.transitToReview(taskID); terr != nil {
		m.log.Error("恢复操作：回迁 waiting_review 失败", "task", taskID, "cause", terr)
	} else if err == nil {
		m.hub.Publish(evt)
	}
	cur, gerr := m.st.GetTask(taskID)
	if gerr != nil {
		return proto.TaskStateWaitingAnswer
	}
	return cur.State
}

// markDelivered 记录「应答已送达 executor」。失败仅 Warn：送达本身已经发生，
// 标记丢失最坏只会让 RecoverStuck 多重投一次，不影响正确性。
func (m *Manager) markDelivered(taskID, ticketID string) {
	if err := m.st.MarkTicketDelivered(ticketID); err != nil {
		m.log.Warn("标记应答已送达失败", "task", taskID, "ticket", ticketID, "cause", err)
	}
}

// NoteDeliveryFailed 产出 delivery_failed 事件：应答已落库但没送到 executor。
//
// 参数：
//   - taskID: 任务 ID
//   - ticketID: 未送达的工单 ID
//   - cause: 送达失败的原因（原样进 payload 供审核者诊断）
//
// why（必须是事件而不只是日志）：此时 executor 仍原地阻塞，而工单已被应答消耗、
// 不再出现在 attach 的挂起项里——只写日志的话审核者这边完全无感，任务一路挂到
// 看门狗超时。产出事件才能唤醒审核者（wait 不过滤该类型），提示执行 handoff resume。
// 供 manager 内部的应答等待链路与 server 的 reply 回程共用。
func (m *Manager) NoteDeliveryFailed(taskID, ticketID string, cause error) {
	evt, err := m.st.AppendEvent(taskID, proto.EventTypeDeliveryFailed, deliveryFailedPayload{
		TicketID: ticketID,
		Reason:   truncateRunes(fmt.Sprint(cause), 500),
		Hint:     "应答已落库但未送达 executor，执行 handoff resume <task> 重投",
	})
	if err != nil {
		m.log.Error("追加 delivery_failed 事件失败", "task", taskID, "ticket", ticketID, "cause", err)
		return
	}
	m.hub.Publish(evt)
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
		// executor 已死，挂起工单一并作废（U-3）：与 RecoverOnStartup 的重启恢复
		// 路径同语义。不作废的话 attach 仍向审核者展示可操作的挂起项，而 executor
		// 已不在——一旦 reply，工单被消耗、中继失败返回 502，任务落进不可恢复状态
		if voided, verr := m.st.VoidPendingTickets(taskID); verr != nil {
			m.log.Error("作废挂起工单失败", "task", taskID, "cause", verr)
		} else if voided > 0 {
			m.log.Warn("executor 已终结，挂起工单作废", "task", taskID, "voided", voided)
		}
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
	// 回合结束（任务进入 waiting_review）：清理审批链运行时状态，防内存 map
	// 随任务无界增长；任务被续接时从干净状态重新评估（P2-5）
	m.clearApproverState(taskID)
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
	ad, err := m.adapterFor(taskID)
	if err != nil {
		m.log.Error("恢复读取任务执行者失败", "task", taskID, "cause", err)
		return false
	}
	r, ok := ad.(restorer)
	if !ok {
		m.log.Warn("adapter 不支持执行恢复，任务按不存活处理", "task", taskID)
		return false
	}
	taskDir := filepath.Join(m.cfg.DataDir, "tasks", taskID)
	// 恢复时 git 基线捕获与 serve 重建都在任务工作区（worktree 任务的 cwd 是
	// Workdir 而非主仓库，git -C 在 worktree 上照常工作），统一取 task.Workdir()
	alive, err := r.Resume(taskID, taskDir, task.Workdir(), task.ExecutorSession)
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
