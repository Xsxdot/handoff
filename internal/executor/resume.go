// resume.go —— 执行恢复的共享数据契约。
//
// 职责：
//   - 定义 ResumeReq / ResumeOutcome / ResumeMode 常量，供 manager 与三个 adapter 共用
//
// 边界：
//   - 只有数据，没有接口：恢复能力的接口由消费方（manager）定义并做类型断言，
//     这样「不支持恢复的 adapter 一律按不存活走 failed 恢复路径」仍是自然语义，
//     executor.Adapter 的五动作核心契约也不被污染
//   - 无 I/O、无实现（与本包其余部分同规格）
package executor

// ResumeReq 是一次恢复请求。
//
// 字段说明：
//   - TaskDir: DataDir/tasks/<id>，恢复凭据（serve.json / claude.json）与会话
//     数据（grok 的 grokhome）都在里面
//   - RepoPath: 任务工作区（worktree 任务为 Workdir）。冷恢复时它是新进程的 cwd，
//     claude 的会话文件路径按 cwd 编码，传错等于找不到会话
//   - SessionID: 落库的 task.ExecutorSession；空则无法载入原会话
//   - Env: 冷恢复重起进程时要注入的环境变量（KEY=VALUE，已解析已展开）。
//     **不传就是静默故障**：进程能起来，但用户配的代理/密钥全没了，一调模型才失败。
//     值绝不进日志，要打只打 key 名
//   - Model: 冷恢复重起进程时的模型名（serve.json/claude.json 都没存它，
//     只有 task.Model 有）
//   - MarkRoot: 已解析的托管 worktree 根；冷恢复重起进程时复用原归属凭据
//   - Cold: true=允许冷恢复（进程已死时重起进程 + 载入原会话）；
//     false=只热重连，进程不在即判不可恢复
//   - Discipline: 按执行者裁出的执行纪律块正文；恢复路径也必须带上，避免
//     codex 的常驻 developerInstructions 在恢复后消失
type ResumeReq struct {
	TaskID     string
	TaskDir    string
	RepoPath   string
	SessionID  string
	Env        []string
	Discipline string
	Model      string
	MarkRoot   string
	Cold       bool
}

// 恢复实际走到的级别（ResumeOutcome.Mode 的取值）。
//
// 四级阶梯的第 1 级「运行态还在 adapter 内存里」不在此列——那种情况根本不会
// 调到 Resume。
const (
	ResumeModeReattach = "reattach" // 第 2 级：进程还活着，重连
	ResumeModeCold     = "cold"     // 第 3 级：重起进程 + 载入原会话，上下文完整
	ResumeModeFresh    = "fresh"    // 第 4 级：原会话载不进，已新开会话，上下文从下一条指令开始
)

// ResumeOutcome 是恢复结果。
//
// 字段说明：
//   - Alive: 是否拿到了可用的运行态。false 时其余字段除 Note 外均为空
//   - Mode: ResumeMode* 之一；Alive=false 时为空串
//   - SessionID: 恢复后实际生效的会话 id。fresh 时是新 id，manager 据此落库
//   - Note: 一句话结论，manager 转成事件文本或错误信息给协调者看
type ResumeOutcome struct {
	Alive     bool
	Mode      string
	SessionID string
	Note      string
}
