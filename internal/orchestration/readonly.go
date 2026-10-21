// readonly.go —— B452 只读形态与产物目录在执行机编排一侧的生命周期。
//
// 职责：
//   - 派发期形态校验（mode 取值、只读与分支类参数互斥）
//   - 产物目录的布局、创建与提示词小节、执行器环境变量
//   - 只读任务 completed 之后的自动归档，以及 agentd 重启后的补归档
//   - 自动归档成功认领时经 TerminalNotifier 通知门面（门面据此归还载体/小队名额）
//
// 边界：不判名额、不碰编制域（名额归还由门面实现 TerminalNotifier）；不改 Done/Stop 的
// 既有语义，自动归档就是一次普通的 Done。
package orchestration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/workspace"
)

// ReadonlyAutoArchiveNote 是只读任务自动归档的 archived 事件说明（用户可见、测试锁定）。
const ReadonlyAutoArchiveNote = "只读任务完成，自动归档"

// TerminalNotifier 由组装点注入（实现在控制门面）：编排在 HTTP 以外的路径把任务推入终态、
// 且本次调用是认领者（TerminalOutcome.Claimed）时调用它，门面据此归还名额。
//
// 注意：只在「编排内部触发的终态」上调用——handleDone/handleStop 自己归还，不经这里，
// 否则同一次终态会被归还两次。
type TerminalNotifier interface {
	TaskTerminal(task *proto.Task)
}

// terminalNotifierBox 让 atomic.Value 存放接口值时类型恒定。
type terminalNotifierBox struct{ n TerminalNotifier }

// SetTerminalNotifier 注入终态回调；nil 表示不通知（名额只能等启动对账兜底）。
func (m *Manager) SetTerminalNotifier(n TerminalNotifier) {
	m.terminal.Store(terminalNotifierBox{n: n})
	m.log.Info("终态回调已注入", "present", n != nil)
}

func (m *Manager) terminalNotifier() TerminalNotifier {
	box, _ := m.terminal.Load().(terminalNotifierBox)
	return box.n
}

// TaskArtifactsDir 返回执行机上任务的产物目录 <dataDir>/tasks/<id>/artifacts（B452 F11，路径布局即契约）。
func TaskArtifactsDir(dataDir, taskID string) string {
	return filepath.Join(dataDir, "tasks", taskID, proto.ArtifactsDirName)
}

// validateDispatchMode 校验派发形态并就地规整请求：只读形态强制 NewWorktree。
//
// 返回：readonly 是否只读形态；err 非 nil 时调用方必须零副作用拒发（此刻尚未落库建树）。
func validateDispatchMode(req *DispatchReq) (readonly bool, err error) {
	switch req.Mode {
	case "", proto.TaskModeBranch:
		return false, nil
	case proto.TaskModeReadonly:
	default:
		return false, fmt.Errorf("%w: mode=%q 非法（合法值 %s、%s）", workspace.ErrBadDispatchRequest,
			req.Mode, proto.TaskModeBranch, proto.TaskModeReadonly)
	}
	if req.Branch != "" || req.NewBranch != "" || req.Worktree != "" {
		return true, fmt.Errorf("%w: 只读形态（mode=readonly）不接受 branch/new-branch/worktree，它总在基线提交上建 detached 托管树",
			workspace.ErrBadWorkspaceReq)
	}
	req.NewWorktree = true
	return true, nil
}

// artifactsPromptSection 是只读任务提示词末尾的「## 产物目录」小节（Q7 乙：只给 readonly）。
func artifactsPromptSection(dir string) string {
	return "\n\n## 产物目录\n\n本任务的产物目录是 `" + dir + "`（环境变量 " + proto.ArtifactsEnvVar +
		" 同值）。需要交付的报告、清单、截图等文件写到这里：它不在工作树里、不进 git，" +
		"工作树回收后仍保留，调用方会拿到整个目录。"
}

// autoArchiveReadonly 是只读任务 completed 之后的自动归档：一次普通 Done，认领成功则回调门面。
//
// 为什么由 handleResult 另起 goroutine 调用：Done 会停 executor、关事件通道，同步调用等于
// 在中介循环里停自己（段 0 探针验证 goroutine 形态不互锁）。
func (m *Manager) autoArchiveReadonly(taskID, trigger string) bool {
	before, err := m.st.GetTask(taskID)
	if err != nil {
		m.log.Error("只读任务自动归档：读任务快照失败", "task", taskID, "trigger", trigger, "cause", err)
		return false
	}
	m.log.Info("只读任务自动归档开始", "task", taskID, "machine", before.Target,
		"carrier", before.Carrier, "squad", before.Squad, "trigger", trigger)
	outcome, err := m.Done(context.Background(), taskID, ReadonlyAutoArchiveNote)
	if err != nil {
		m.log.Error("只读任务自动归档失败，任务留在原状态待人工 done", "task", taskID,
			"machine", before.Target, "trigger", trigger, "cause", err)
		return false
	}
	if !outcome.Claimed {
		m.log.Info("只读任务已由他人归档，自动归档不回调", "task", taskID, "machine", before.Target, "trigger", trigger)
		return false
	}
	snap, err := m.st.GetTask(taskID)
	if err != nil {
		// 终态迁移不改身份：用迁移前快照通知，避免「读不回快照」变成名额泄漏（同 handleDone）
		m.log.Error("只读任务自动归档后读快照失败，用迁移前快照回调", "task", taskID, "cause", err)
		snap = before
	}
	if n := m.terminalNotifier(); n != nil {
		n.TaskTerminal(snap)
	} else {
		m.log.Warn("只读任务自动归档完成但未注入终态回调，名额待启动对账兜底", "task", taskID)
	}
	m.log.Info("只读任务自动归档完成", "task", taskID, "machine", snap.Target,
		"carrier", snap.Carrier, "squad", snap.Squad, "trigger", trigger)
	return true
}

// ArchiveCompletedReadonly 是启动补归档：agentd 在只读任务 completed 之后、自动归档之前重启，
// 任务会停在 waiting_review 占着名额与工作树。本函数找出「只读 + waiting_review + 最后一条回合
// 结果是 completed」的任务逐个归档，返回认领归档的个数。最后一条是 turn_failed 的不动（等人裁决）。
//
// 注意：须在 SetTerminalNotifier 之后、对外服务之前调用；同步执行。
func (m *Manager) ArchiveCompletedReadonly() int {
	tasks, err := m.st.ListTasks()
	if err != nil {
		m.log.Error("启动补归档：列任务失败", "cause", err)
		return 0
	}
	n := 0
	for _, t := range tasks {
		if t.Mode != proto.TaskModeReadonly || t.State != proto.TaskStateWaitingReview {
			continue
		}
		last, err := m.lastTurnResult(t.ID)
		if err != nil {
			m.log.Error("启动补归档：读事件失败，跳过", "task", t.ID, "cause", err)
			continue
		}
		if last != proto.EventTypeCompleted {
			m.log.Info("启动补归档：最后回合非完成，留待人工裁决", "task", t.ID, "last", last)
			continue
		}
		if m.autoArchiveReadonly(t.ID, "startup") {
			n++
		}
	}
	m.log.Info("启动补归档完成", "archived", n)
	return n
}

// lastTurnResult 返回任务最近一条回合结果事件的类型（completed / turn_failed / failed），无则空串。
func (m *Manager) lastTurnResult(taskID string) (proto.EventType, error) {
	evs, err := m.st.EventsFrom(taskID, 0, 500)
	if err != nil {
		return "", err
	}
	for i := len(evs) - 1; i >= 0; i-- {
		switch evs[i].Type {
		case proto.EventTypeCompleted, proto.EventTypeTurnFailed, proto.EventTypeFailed:
			return evs[i].Type, nil
		}
	}
	return "", nil
}

// withArtifactsDir 返回带唯一 HANDOFF_ARTIFACTS_DIR 行的 env（F17）。先删同名旧行再追加，
// 理由同 withCarrierHome：env 文件允许重复键，覆盖优先级必须固定为 handoff 下发值。
func withArtifactsDir(env []string, dir string) []string {
	out := make([]string, 0, len(env)+1)
	prefix := proto.ArtifactsEnvVar + "="
	for _, kv := range env {
		if len(kv) >= len(prefix) && kv[:len(prefix)] == prefix {
			continue
		}
		out = append(out, kv)
	}
	return append(out, prefix+dir)
}

// resumeEnvWithArtifacts 是续接/启动恢复两条路径的产物变量注入：只在产物目录确实存在时注入。
//
// 为什么以「目录存在」为判据：B452 之前派发的任务没有产物目录，恢复时不凭空造目录、
// 也不给执行器一个指向不存在路径的变量；B452 之后派发的任务目录在派发期已建。
func (m *Manager) resumeEnvWithArtifacts(env []string, taskID string) []string {
	dir := TaskArtifactsDir(m.cfg.DataDir, taskID)
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return env
	}
	return withArtifactsDir(env, dir)
}

// ensureArtifactsDir 建任务产物目录（0700，与任务目录同权限纪律）。
func ensureArtifactsDir(dir string) error {
	return os.MkdirAll(dir, 0o700)
}
