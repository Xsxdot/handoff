// footprint.go —— 任务进程足迹的身份校验与两个孪生原语。
//
// 职责：
//   - classify：给定 Handle 与一次进程快照，判定「哪些进程属于这个任务」
//   - Footprint（只数）/ Sweep（回收）：共用 classify，保证数出来的与被杀的是同一批
//
// 边界：
//   - 不负责枚举进程（那是 procenum_*.go），不负责判定 shim 存活（那是 Alive）
//   - 不修改任何任务状态、不发事件：那是 agentd 的事
//   - **不替代 Kill**：Kill 杀活着的执行者、前提是存活锁被持有；Sweep 收已死执行者
//     的残留、前提是锁已释放。两者风险模型不同，不得互相代劳（见 Sweep 的文档）
package prochost

// Verdict 是一次足迹判定的结论。
//
// 为什么要三态而不是 bool：判不出结论时猜一个值就是制造假阳性，而一条会说谎的
// 诊断比没有更糟——因为你会信它。与 ActiveTask.Live 的三态是同一条纪律。
type Verdict string

const (
	// VerdictOK 身份校验通过，members 可信。
	VerdictOK Verdict = "ok"
	// VerdictLeaderReuse pgid 已被复用（组长位置被无关进程占据），整组放弃。
	VerdictLeaderReuse Verdict = "leader_reuse"
	// VerdictNoCredential 凭据不全（Handle.StartedAt 缺失，多见于升级前写下的
	// proc.json），无法做时间下界校验，放弃。
	VerdictNoCredential Verdict = "no_credential"
)

// classify 判定进程快照中哪些成员属于 h 所代表的任务。
//
// 参数：
//   - h: 任务的进程句柄；h.PID 既是 shim pid 也是进程组 id（Setsid 保证）
//   - procs: 一次进程快照（当前 uid 的全部进程）
//   - lockHeld: h.LockPath 上的存活锁是否仍被持有，即 shim 是否还活着
//
// 返回：
//   - members: 通过校验的成员 pid；判定为放弃时**必然为空**
//   - v: 判定结论
//
// 三条规则（spec §3.2）：
//
// 规则一（组长身份判定，以存活锁为准，一票否决）：
//   - 锁仍被持有 ⇒ 组长就是我们的 shim，pgid 不可能被复用（锁由内核在进程死亡时
//     释放，这个判据本身免疫 pid 复用），正常计数
//   - 锁已释放 ⇒ 组长已死；此时组内若仍有活的 pid==pgid==h.PID，那必然是内核把
//     这个 pid 分配给了新进程且它成了组长，即 pgid 被复用 ⇒ 整组放弃
//
// 规则二（会话封闭性，无需代码）：过了规则一之后，组内成员只可能是我们的后代。
// 依据：shim 调用过 setsid，该进程组属于 shim 独有的会话；setpgid(2) 要求目标
// 进程组与调用者同会话，会话外的进程加不进来。
//
// 规则三（时间下界，双保险）：成员启动时刻必须 ≥ h.StartedAt，否则排除。规则二
// 理论上已经封闭，这条防的是理论之外——代价是漏杀而非误杀。
//
// 注意：本函数刻意不打日志——调用方（Footprint/Sweep）在边界上统一记录入参与
// 结论；这里再记一遍等于同一件事写两次，且 status 会高频调用它。
func classify(h Handle, procs []procEntry, lockHeld bool) (members []int, v Verdict) {
	if h.PID <= 0 || h.StartedAt <= 0 {
		return nil, VerdictNoCredential
	}
	if !lockHeld {
		for _, p := range procs {
			if p.PID == h.PID && p.PGID == h.PID {
				// 组长位置被人占着，而我们的 shim 已经死了 ⇒ pid 被复用
				return nil, VerdictLeaderReuse
			}
		}
	}
	for _, p := range procs {
		if p.PGID != h.PID {
			continue
		}
		if p.StartedAt < h.StartedAt {
			continue // 规则三：比 shim 还早的不可能是它的后代
		}
		members = append(members, p.PID)
	}
	return members, VerdictOK
}
