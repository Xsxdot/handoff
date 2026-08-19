// taskmark.go —— 任务标记：不依赖采样时机的进程归属判据（平台无关契约）。
//
// 职责：
//   - 定义 TaskCred：一次归属判定所需的全部凭据
//   - 声明平台原语 attributes 的契约，并提供批量判定 markMembers
//   - applyTaskMark：在 Start 处把标记注入执行者的环境
//
// 边界：
//   - 只回答「这个进程属不属于这个任务」，不发信号、不做存活判定——
//     回收是 footprint.go 的 Sweep 的事
//   - **实现一律不得 fork**（同 procenum.go 的硬约束）
//   - 与 pgid / roster 是**并列**的第三条来源，不替代它们：pgid 覆盖同组，
//     roster 覆盖采到过的逃逸后代，标记覆盖「壳活得太短、一次都没采到」的那批
package prochost

import (
	"os"
	"path/filepath"
)

// TaskMarkEnvKey 是注入执行者环境的标记变量名。
//
// 三平台都注入。macOS 上它对 Apple 平台二进制不可读（内核屏蔽），因此不作判据，
// 但人工用 `ps -E` 排障时仍然有用。
const TaskMarkEnvKey = "HANDOFF_TASK_ID"

// TaskCred 是一次归属判定所需的全部凭据，由 Handle 投影而来。
//
// 两个字段各自的零值都表示「对应判据不可用」，不是「判据通过」——这与 Handle
// 的 omitempty 降级纪律是同一条：升级前写下的 proc.json 没有这些字段，读出空串
// 就该退回 pgid + roster，而不是拿一个空凭据去匹配。
type TaskCred struct {
	// TaskID 是本任务的 UUID。linux 判据拿它与进程 environ 里的
	// TaskMarkEnvKey 比对。
	TaskID string
	// MarkRoot 是 cwd 判据的比对根，**已做符号链接解析**的绝对路径。
	//
	// 空串表示本任务不允许用 cwd 归属。agentd 只在托管 worktree 形态下填它——
	// 「仅托管 worktree 可杀」这条边界因此是数据决定的，不存在「某处忘了检查」
	// 的可能。托管 worktree 在 DataDir/worktrees 下，是 handoff 自建自删的目录，
	// 人类没有理由待在里面；而共享主仓里一定有用户自己的编辑器与 shell，
	// 拿 cwd 去杀会打掉它们。
	MarkRoot string
}

// empty 报告凭据是否完全不可用（两条判据都没有比对依据）。
func (c TaskCred) empty() bool { return c.TaskID == "" && c.MarkRoot == "" }

// attributesFn 是平台原语的测试缝（包级 var 而非直接调用），与 enumProcsFn /
// aliveFn / killGroupFn 同款路数：判据测试要喂固定结论，不能依赖真实进程。
var attributesFn = attributes

// markMembers 在一次进程快照里筛出属于 cred 的成员。
//
// 参数：cred 为任务凭据；procs 为一次进程快照（与其它判据共用，避免重复枚举）
//
// 返回：
//   - members: 归属本任务的 pid
//   - supported: 本平台是否具备标记判定能力。**false 时 members 必然为空，
//     且调用方必须理解为「这条判据不可用」而非「没有成员」**
//
// 注意：
//   - 单个 pid 读失败（进程刚退出、权限不足）只跳过该条，不影响整批——进程在
//     枚举与读取之间消失是常态
//   - 成功路径刻意不打日志：Footprint 被 handoff status 按任务高频调用，
//     每个 pid 记一行会把 agentd.log 淹掉。汇总由调用方在边界上打一次
func markMembers(cred TaskCred, procs []procEntry) (members []int, supported bool) {
	if cred.empty() {
		return nil, false
	}
	for _, p := range procs {
		ok, err := attributesFn(p.PID, cred)
		if err != nil {
			if isNotSupported(err) {
				// 平台整体不具备该能力：没必要把剩下几百个 pid 再问一遍
				return nil, false
			}
			// 单个进程读不到：跳过。这是常态，降到 Debug 避免刷屏
			log().Debug("读任务标记失败，跳过该进程", "pid", p.PID, "cause", err)
			continue
		}
		supported = true
		if ok {
			members = append(members, p.PID)
		}
	}
	return members, supported
}

// isNotSupported 判别「本平台不具备该能力」这一类错误。
//
// 为什么单独抽一个函数：darwin 的实现会在运行期自检失败后也退回这个语义
// （见 taskmark_darwin.go 的偏移量自检），判别点集中在一处才不会漏。
func isNotSupported(err error) bool { return err == ErrNotSupported }

// applyTaskMark 把任务标记注入 spec.Env。
//
// 为什么放在 Start 而不是各 adapter：Start 是四个 adapter 的唯一汇流点，
// 在这里注入既让 adapter 零改动，也保证 Handle.TaskID 与实际注入的环境变量
// 出自同一处赋值、不可能对不上。紧邻的 applyFencePolicy 是同款先例。
//
// 注意：spec.TaskID 为空时什么都不做——没有 id 就没有可比对的标记，
// 注入一个空值只会让判据把所有没有该变量的进程都算成命中。
func applyTaskMark(spec *Spec) {
	if spec.TaskID == "" {
		return
	}
	spec.Env = append(spec.Env, TaskMarkEnvKey+"="+spec.TaskID)
}

// ResolveMarkRoot 把一个工作目录路径归一成可用于 cwd 比对的形态。
//
// 参数：dir 为任务工作区目录；managed 表示它是否为 agentd 托管的 worktree
//
// 返回：可比对的绝对路径；不允许用 cwd 归属时返回空串
//
// 注意：**必须做符号链接解析**。内核返回的 cwd 是解析后的（macOS 上
// /var/... 会变成 /private/var/...），直接拿未解析的路径去比会得到一个
// 看起来很干净的假阴性——判据静默失效而没有任何报错。
func ResolveMarkRoot(dir string, managed bool) string {
	if !managed || dir == "" {
		return ""
	}
	resolved, err := filepathEvalSymlinks(dir)
	if err != nil {
		log().Warn("解析 worktree 路径失败，本任务不启用 cwd 归属",
			"dir", dir, "cause", err)
		return ""
	}
	return resolved
}

// filepathEvalSymlinks 是 filepath.EvalSymlinks 的测试缝：单测要构造解析失败。
var filepathEvalSymlinks = filepath.EvalSymlinks

// MarkCapability 报告本平台是否具备任务标记归属能力。
//
// 返回：
//   - supported: 是否可用
//   - reason: 不可用的原因（供启动期日志直接呈现）；可用时为空串
//
// 为什么要单独一个导出函数而不是让 agentd 自己试：能力判定的依据在包内
// （平台实现 + darwin 的运行期自检），暴露一个明确的问句比让调用方
// 拿一个假 pid 去试要诚实得多。
func MarkCapability() (supported bool, reason string) {
	// 用本进程当探针：它一定存在，且凭据给足以便走到平台实现里。
	_, err := attributesFn(os.Getpid(), TaskCred{TaskID: "capability-probe", MarkRoot: os.TempDir()})
	if err != nil && isNotSupported(err) {
		return false, "本平台不支持任务标记归属"
	}
	return true, ""
}
