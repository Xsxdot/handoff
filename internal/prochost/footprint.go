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

import (
	"errors"
	"fmt"
	"time"
)

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

// enumProcsFn 是进程枚举的测试缝（包级 var 而非直接调用）：判据测试要喂固定
// 快照，与 aliveFn / killGroupFn 同款路数。
var enumProcsFn = enumProcs

// lookupStartedAt 读回某个 pid 的内核启动时刻（unix 纳秒）；读不到返回 0。
//
// 为什么容忍失败：这是诊断凭据不是运行必需品，取不到只降级为「不能自动清扫」，
// 绝不能反过来影响已经拉起的执行者。
func lookupStartedAt(pid int) int64 {
	procs, err := enumProcsFn()
	if err != nil {
		log().Warn("枚举进程失败，无法读取启动时刻", "pid", pid, "cause", err)
		return 0
	}
	for _, p := range procs {
		if p.PID == pid {
			return p.StartedAt
		}
	}
	log().Warn("枚举结果中没有该 pid，无法读取启动时刻", "pid", pid)
	return 0
}

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

// Footprint 枚举 h 所代表任务当前占用的进程。
//
// 参数：h 为任务的进程句柄（来自 proc.json）
//
// 返回：
//   - members: 通过身份校验的成员 pid
//   - v: 判定结论；非 VerdictOK 时 members 必然为空
//   - err: 进程枚举失败（平台不支持时为 errNotSupported）
//
// 注意：
//   - **只读，绝不发信号**——它是 Sweep 的孪生只读版本，两者共用 classify
//   - 对**存活中**与**已死亡**的执行者均可调用：判据随存活锁状态自动切换
func Footprint(h Handle) (members []int, v Verdict, err error) {
	procs, err := enumProcsFn()
	if err != nil {
		log().Error("足迹枚举失败", "pid", h.PID, "cause", err)
		return nil, VerdictNoCredential, err
	}
	members, v = classify(h, procs, aliveFn(h))
	log().Debug("足迹判定完成", "pid", h.PID, "verdict", string(v), "members", len(members))
	return members, v, nil
}

// ErrExecutorAlive 表示执行者仍然活着，Sweep 不适用。
//
// 调用方靠 errors.Is 判别，禁止按错误文本判——与 ErrLockHeld / ErrStillAlive 同款。
var ErrExecutorAlive = errors.New("执行者仍存活，Sweep 不适用")

// Sweep 回收一个**已死**执行者留下的残留后代。
//
// 参数：h 为任务的进程句柄（来自 proc.json）
//
// 返回：
//   - killed: 发信号时组内通过身份校验的成员数（0 表示没动手）
//   - v: 判定结论；非 VerdictOK 时必然 killed == 0
//   - err: 执行者仍存活（ErrExecutorAlive）、枚举失败、或已发信号但复核仍存活
//     （ErrStillAlive）
//
// 注意：
//   - **前提是执行者已死**。存活锁仍被持有时直接拒绝——杀活着的执行者是 Kill
//     的职责，两者风险模型不同（Kill 以「锁在」为发信号的前提，Sweep 以「锁不在」
//     为前提并逐个校验成员身份），互相代劳会把 Kill 那条纪律绕过去
//   - 判定为放弃（leader_reuse / no_credential）**不是错误**，是正常结论：
//     调用方据 v 决定是否上报人工，不该按 err != nil 判
//   - 与 Kill 一致：发完 SIGKILL 必须复核，复核窗口走完仍存活返回 ErrStillAlive
func Sweep(h Handle) (killed int, v Verdict, err error) {
	if aliveFn(h) {
		log().Warn("执行者仍存活，拒绝清扫", "pid", h.PID, "lock", h.LockPath)
		return 0, VerdictOK, ErrExecutorAlive
	}
	procs, eerr := enumProcsFn()
	if eerr != nil {
		log().Error("清扫前枚举进程失败", "pid", h.PID, "cause", eerr)
		return 0, VerdictNoCredential, eerr
	}
	members, v := classify(h, procs, false)
	if v != VerdictOK {
		log().Warn("清扫放弃", "pid", h.PID, "verdict", string(v))
		return 0, v, nil
	}
	if len(members) == 0 {
		log().Info("无残留可清扫", "pid", h.PID)
		return 0, VerdictOK, nil
	}
	log().Info("回收残留进程组", "pid", h.PID, "members", len(members), "pids", members)
	if kerr := killGroupFn(h.PID); kerr != nil {
		log().Error("回收残留进程组失败", "pid", h.PID, "cause", kerr)
		return 0, VerdictOK, fmt.Errorf("回收进程组 %d: %w", h.PID, kerr)
	}
	// 复核：与 Kill 同款窗口。SIGKILL 异步生效，不复核就是「杀没杀掉我们不知道，
	// 而且假装知道」——B47 修的正是这个
	for i, d := range killVerifyBackoff {
		time.Sleep(d)
		rest, rerr := enumProcsFn()
		if rerr != nil {
			log().Error("复核枚举失败", "pid", h.PID, "cause", rerr)
			break
		}
		if left, _ := classify(h, rest, false); len(left) == 0 {
			log().Info("清扫完成，已确认残留退出", "pid", h.PID,
				"killed", len(members), "probe", i+1)
			return len(members), VerdictOK, nil
		}
	}
	log().Error("已发 SIGKILL 但复核窗口走完仍有残留", "pid", h.PID,
		"window", killVerifyWindow)
	return len(members), VerdictOK,
		fmt.Errorf("%w: pgid=%d，已发 SIGKILL 并复核 %s", ErrStillAlive, h.PID, killVerifyWindow)
}
