// shim.go —— shim 进程的主体逻辑。
//
// 职责：
//   - 持有存活锁（整个生命周期），作为 prochost.Alive 的唯一判据
//   - 打开 stdout/stderr 追加落盘文件；InputCh 非空时经 openInputChannel 准备子进程 stdin（平台各自实现）
//   - 在 spawn executor 之前安装进程容器（unix=RLIMIT_NPROC，Windows=Job Object），
//     executor 全树继承
//   - spawn 真正的 executor，把它的 pid 记进 child.pid
//   - wait 子进程，退出后向 stdout 追加 handoff_exit 哨兵
//
// 边界：
//   - 不认识 executor 协议、不解析输出：只做搬运与收尸
//   - 不写任务状态、不连 agentd：shim 与 agentd 之间只有文件（锁、child.pid、日志）
//   - 不写 proc.json：那是 adapter 的独占文件，双写者会丢更新（见 recordChildPID）
//   - 不决定围栏值取多少：那是 prochost 策略层（fence.go）的事，shim 只负责安装容器
//
// 为什么必须有 shim（而不是 agentd 直接 detach executor）：退出哨兵需要一个
// 常驻父进程 waitpid 才能拿到。agentd 重启后，reparent 给 init 的 executor
// 已经没法被 waitpid，「agentd 离线期间 executor 退出」的退出码就永远丢了——
// 那正是恢复流程最需要知道的事。shim 用一个极轻的进程换回这个语义。
//
// 为什么输入通道必须「永不 EOF」：读端一旦 EOF，executor 的 stdin 就关闭，
// 它跑完第一条指令后再也收不到后续投递。unix 上靠 shim 以 O_RDWR 打开 FIFO
// （自己同时是写端）保证，Windows 上靠 shim 攥着匿名管道写端保证——两边实现
// 不同、契约相同，见 openInputChannel。
package prochost

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// SentinelPrefix 是死亡哨兵行的类型标记，adapter 扫 stdout 判死时匹配它。
const SentinelPrefix = `"type":"handoff_exit"`

// rosterInterval 是后代名册的采样间隔。
//
// 为什么是 1s（B103 从 15s 下调）：名册现在是累积的（mergeRoster），漏记只可能
// 发生在「工具壳的整个存活窗口内一次都没采到」。executor 的 Bash 工具壳往往只活
// 约 1 秒（grok 把每条命令 setsid 成新会话后立刻返回），15s 的 tick 几乎必然错过
// 它——08-15 实测 450 个 `sleep 900` 一个都没进名册。1s 把这个窗口压到最小。
//
// 代价是每秒一次全进程表枚举。可接受的依据：enumProcs 走 sysctl/procfs，**不 fork**
// （procenum.go 的硬约束），所以它在「机器已经 fork 不动」时仍然可用，也不会自我
// 加剧；并且内容未变时不落盘，稳态下没有磁盘写入。
//
// 是变量而非常量：测试要把它调到毫秒级，否则每条周期用例都真等 1s。
var rosterInterval = time.Second

// RunShim 是 shim 进程的入口：读 spec、持锁、拉起 executor、收尸写哨兵。
//
// 参数：specPath 为 Start 落盘的 spec.json 路径
//
// 返回：
//   - 锁已被持有（同任务已有 shim 在跑）、spec 不可读、子进程拉不起来时返回错误
//   - 子进程本身以非零码退出**不算错误**：那是正常业务结果，经哨兵传达
//
// 注意：本函数会阻塞到子进程退出，调用方（handoff _shim）随后即可退出。
func RunShim(specPath string) error {
	spec, err := readSpec(specPath)
	if err != nil {
		return err
	}
	l := log().With("lock", spec.LockPath)

	// 存活锁必须最先拿：拿不到说明同任务已有 shim 在跑，起第二个会让两个 executor
	// 抢同一会话（数据损坏级后果，与 claudecode 冷恢复互斥同一道理）
	lock, err := AcquireLock(spec.LockPath)
	if err != nil {
		l.Error("抢占存活锁失败，同任务可能已有 shim 在跑", "cause", err)
		return fmt.Errorf("shim 抢锁: %w", err)
	}
	defer lock.Release()

	stdout, err := openAppend(spec.Stdout)
	if err != nil {
		l.Error("打开 stdout 落盘文件失败", "path", spec.Stdout, "cause", err)
		return err
	}
	defer stdout.Close()
	stderr := stdout
	if spec.Stderr != "" && spec.Stderr != spec.Stdout {
		stderr, err = openAppend(spec.Stderr)
		if err != nil {
			l.Error("打开 stderr 落盘文件失败", "path", spec.Stderr, "cause", err)
			return err
		}
		defer stderr.Close()
	}

	// 进程容器必须在 spawn 之前装：unix 的 rlimit 随 fork 继承、Windows 的 job
	// 成员身份随 CreateProcess 继承，装晚一步执行者就在容器外面了。
	//
	// **两个平台的失败语义刻意不同**，由各自的实现决定，不要在这里统一：
	// unix 上容器只是围栏，装不上仍可跑（Warn 后返回 nil）；Windows 上容器是
	// job，而 job 是 killGroup 唯一的回收手段，建不起来就必须硬失败——否则
	// 这个任务将永远杀不干净。
	if cerr := installProcessContainer(spec.NprocLimit); cerr != nil {
		l.Error("安装进程容器失败，放弃拉起执行者", "limit", spec.NprocLimit, "cause", cerr)
		return fmt.Errorf("安装进程容器: %w", cerr)
	}

	cmd := exec.Command(spec.Argv[0], spec.Argv[1:]...)
	cmd.Dir = spec.Dir
	cmd.Env = spec.Env
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if spec.InputCh != "" {
		// 「永不 EOF」由平台实现各自保证：unix 靠 O_RDWR，Windows 靠 shim
		// 攥着匿名管道写端。契约见 openInputChannel 的文档
		in, cleanup, ferr := openInputChannel(spec.InputCh)
		if ferr != nil {
			l.Error("打开输入通道失败", "path", spec.InputCh, "cause", ferr)
			return fmt.Errorf("打开输入通道 %s: %w", spec.InputCh, ferr)
		}
		defer cleanup()
		cmd.Stdin = in
		l.Info("输入通道已就位", "path", spec.InputCh)
	}

	// env 只打 key 名：值可能含凭据（代理 URL 里的 user:pass、API key）
	l.Info("shim 拉起执行者进程", "bin", spec.Argv[0], "dir", spec.Dir,
		"env_keys", envKeys(spec.Env), "input_ch", spec.InputCh != "")
	// 注意 shim 这一处的特殊性：这里的 EAGAIN 很可能是**围栏自己造成的**——uid
	// 占用已经 ≥ L，shim 连 executor 都 fork 不出来。日志必须带 fence 字段，
	// 否则排障的人会以为是系统上限满了，去查错的方向。
	if err := cmd.Start(); err != nil {
		if note, _ := ExplainForkFailure(err); note != "" {
			l.Error("拉起执行者进程失败（进程配额）", "bin", spec.Argv[0],
				"note", note, "fence", spec.NprocLimit, "cause", err)
			return fmt.Errorf("%s: 拉起 %s: %w", note, spec.Argv[0], err)
		}
		l.Error("拉起执行者进程失败", "bin", spec.Argv[0], "cause", err)
		return fmt.Errorf("拉起 %s: %w", spec.Argv[0], err)
	}
	childPID := cmd.Process.Pid
	if err := recordChildPID(spec.InfoPath, childPID); err != nil {
		// 只是诊断信息（回收靠锁与进程组，不靠它），写不进去不值得杀掉已经起来的执行者
		l.Warn("记录 child.pid 失败，不影响执行", "path", childPIDPath(spec.InfoPath), "cause", err)
	}
	l.Info("执行者进程已启动", "child_pid", childPID)

	// 出生登记：趁进程树还活着，周期把后代名册落盘。executor 一死后代就被
	// reparent 给 init/launchd，ppid 链当场断——名册是那之后唯一还能凭出生
	// 事实认人的东西（why 见 roster.go 的 descendantsOf）
	stopRoster := make(chan struct{})
	rosterDone := make(chan struct{})
	go func() {
		defer close(rosterDone)
		// 同一个 sampler 跨轮复用：它持有上一轮的序列化结果，"内容未变则不写"
		// 依赖这份状态；每轮新建一个等于关掉这个优化
		sampler := &rosterSampler{path: rosterPath(spec.InfoPath)}
		runRosterSampling(stopRoster, sampler, l)
	}()

	code := 0
	if werr := cmd.Wait(); werr != nil {
		var ee *exec.ExitError
		if errors.As(werr, &ee) {
			code = ee.ExitCode()
		} else {
			l.Error("等待执行者进程失败", "child_pid", childPID, "cause", werr)
			code = -1
		}
	}
	// executor 已退出，停止采样。最后一次快照留在盘上，它 ≈ 死亡时刻的存活者，
	// 正是第二段清扫要点名的那批
	close(stopRoster)
	<-rosterDone
	l.Info("出生登记已停止", "roster", rosterPath(spec.InfoPath))
	if spec.Sentinel {
		if _, err := fmt.Fprintf(stdout, "{%s,\"code\":%d}\n", SentinelPrefix, code); err != nil {
			// 哨兵写不出去 = adapter 永远发现不了死亡，这是必须 Error 的严重情况
			l.Error("写死亡哨兵失败，恢复流程将无法判死", "child_pid", childPID, "cause", err)
		}
	}
	l.Info("执行者进程已退出", "child_pid", childPID, "code", code, "sentinel", spec.Sentinel)
	return nil
}

// ChildPIDFileName 是 shim 记录执行者 pid 的文件名（与 proc.json 同目录）。
const ChildPIDFileName = "child.pid"

// ChildPID 读取 shim 记下的执行者 pid（诊断用）。
//
// 参数：infoPath 为 adapter 的 proc.json 路径，仅用于定位同目录的 child.pid
//
// 返回：文件缺失（shim 没起来过/还没 spawn 完）或内容非法时返回错误——
// 绝不返回 0 冒充成功，0 会被误读成「pid 为 0 的进程」。
func ChildPID(infoPath string) (int, error) {
	p := childPIDPath(infoPath)
	b, err := os.ReadFile(p)
	if err != nil {
		return 0, fmt.Errorf("读 %s: %w", p, err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, fmt.Errorf("解析 %s 内容 %q: %w", p, b, err)
	}
	return pid, nil
}

// childPIDPath 由 proc.json 路径推出 child.pid 路径（两者同目录）。
func childPIDPath(infoPath string) string {
	return filepath.Join(filepath.Dir(infoPath), ChildPIDFileName)
}

// readSpec 读取并校验 spec.json。
func readSpec(path string) (Spec, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Spec{}, fmt.Errorf("读 spec %s: %w", path, err)
	}
	var s Spec
	if err := json.Unmarshal(b, &s); err != nil {
		return Spec{}, fmt.Errorf("解析 spec %s: %w", path, err)
	}
	if len(s.Argv) == 0 || s.LockPath == "" || s.Stdout == "" {
		return Spec{}, fmt.Errorf("spec %s 字段不完整（argv/lock_path/stdout 必填）", path)
	}
	return s, nil
}

// openAppend 以追加模式打开落盘文件（不存在则以 0600 创建）。
func openAppend(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("打开 %s: %w", path, err)
	}
	return f, nil
}

// recordChildPID 把执行者 pid 写进 proc.json 同目录的 child.pid（0600，整份覆盖）。
//
// 为什么不写进 proc.json：那会让 proc.json 有两个写者。adapter 在 Start 返回后
// 会整份覆写 proc.json 补上 Handle.PID，shim 这边若做读-改-写，就存在这样的交错：
// shim 读到 adapter 的旧版 → adapter 写入含 PID 的新版 → shim 写回旧版+child_pid，
// **Handle.PID 归零**。后果不是丢个诊断字段：prochost.Kill 在 PID<=0 时直接返回
// nil，Reap 于是打出「兜底回收完成」而执行者还活着——假成功加孤儿进程，正是
// 本设计要消灭的那类失配。给 shim 一个独占文件，这个窗口从结构上就不存在。
// TestRunShimNeverTouchesProcInfo 钉死这条。
func recordChildPID(infoPath string, pid int) error {
	p := childPIDPath(infoPath)
	if err := os.WriteFile(p, []byte(strconv.Itoa(pid)), 0o600); err != nil {
		return fmt.Errorf("写 %s: %w", p, err)
	}
	return nil
}

// envKeys 提取 KEY=VALUE 列表里的 key（日志用；值绝不出现在日志里）。
func envKeys(env []string) []string {
	keys := make([]string, 0, len(env))
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i > 0 {
			keys = append(keys, kv[:i])
		}
	}
	return keys
}

// rosterSampler 持有名册的采样状态：路径与上一轮落盘的字节。
//
// 为什么要有状态：名册现在每秒采一次，稳态下内容根本不变；把上一轮的序列化
// 结果留着比一比，就能把「每秒一次原子写 + rename」降成「变了才写」。
// 2000 进程的任务名册约 60KB，不做这件事就是每秒几十 KB 的无谓 I/O。
//
// 边界：本类型不负责启停节奏（那是 RunShim 里的 ticker），也不做任何存活判定
// 与信号发送（那是 footprint.go 的事）。
type rosterSampler struct {
	path   string
	last   []byte // 上一轮落盘的序列化结果；nil 表示还没写过
	writes int    // 实际落盘次数，仅供测试断言「未变则不写」
}

// runRosterSampling 驱动后代名册的首次采样与周期采样。
//
// 参数：stop 为执行者退出时关闭的停止信号；sampler 为跨轮复用的采样器；l 为日志器。
//
// 注意：sample 返回 false 表示本平台永久不支持进程枚举，此时只记一条 Info 并退出；
// 其它错误返回 true，仍按周期重试，因为 unix 上的 sysctl/procfs 可能只是本轮读取失败。
func runRosterSampling(stop <-chan struct{}, sampler *rosterSampler, l *slog.Logger) {
	if !sampler.sample(l) {
		return
	}
	tk := time.NewTicker(rosterInterval)
	defer tk.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tk.C:
			if !sampler.sample(l) {
				return
			}
		}
	}
}

// sample 采一轮名册：枚举进程、与上一轮合并、必要时落盘。
//
// 参数：l 为日志器；本方法所有失败都只记日志并返回，不中断任务——名册写不出去
// 只意味着这一轮没有第二段清扫的依据，不值得让任务失败。
//
// 注意：周期日志一律 Debug 级（每秒一次，Info 会把任务日志刷满）；只有单次采样
// 耗时超过间隔一半时才升 Warn——那意味着采样本身开始拖累这台机器，是必须看见的事。
//
// 返回：true 继续按周期采样；false 表示本平台永久不支持进程枚举，调用方应停止循环。
func (s *rosterSampler) sample(l *slog.Logger) bool {
	if s.path == "" {
		l.Warn("无 info_path，无法落盘后代名册，本任务不做出生登记")
		return true
	}
	start := time.Now()
	procs, err := enumProcsFn()
	if err != nil {
		if errors.Is(err, errNotSupported) {
			l.Info("本平台不做后代名册采样，回收由进程容器承担", "cause", err)
			return false
		}
		l.Warn("枚举进程失败，本轮跳过出生登记", "cause", err)
		return true
	}
	prev, err := readRoster(s.path)
	if err != nil {
		// 名册损坏：这一轮从空名册重建，不能因此放弃采样——否则一次损坏会让
		// 这个任务此后永远没有名册
		l.Warn("读回上一轮名册失败，本轮从空名册重建", "path", s.path, "cause", err)
		prev = nil
	}
	entries := mergeRoster(prev, descendantsOf(os.Getpid(), procs), procs)
	b, err := marshalRoster(entries)
	if err != nil {
		l.Warn("序列化后代名册失败，本轮跳过出生登记", "cause", err)
		return true
	}
	cost := time.Since(start)
	if s.last != nil && bytes.Equal(b, s.last) {
		// 内容未变是稳态常态，机器变慢时这一路径才最常走——耗时超标在这里也必须能
		// Warn 出来，否则「名册把机器拖慢」这一信号在稳态下永不出现
		if cost > rosterInterval/2 {
			l.Warn("后代名册采样耗时偏高", "path", s.path, "count", len(entries),
				"cost", cost, "interval", rosterInterval)
			return true
		}
		l.Debug("后代名册未变，跳过落盘", "count", len(entries), "cost", cost)
		return true
	}
	if err := writeRosterBytes(s.path, b); err != nil {
		l.Warn("落盘后代名册失败，本轮跳过出生登记", "path", s.path, "cause", err)
		return true
	}
	s.last = b
	s.writes++
	if cost > rosterInterval/2 {
		// 采样耗时逼近间隔意味着「名册把机器拖慢了」——这是必须能被看见的事，
		// 否则它只会表现为一台莫名其妙变慢的机器
		l.Warn("后代名册采样耗时偏高", "path", s.path, "count", len(entries),
			"cost", cost, "interval", rosterInterval)
		return true
	}
	l.Debug("后代名册已更新", "path", s.path, "count", len(entries), "cost", cost)
	return true
}

// snapshotRoster 采一轮名册（无状态入口，仅供不需要跨轮比对的调用方使用）。
//
// 参数：l 为日志器；infoPath 为 proc.json 路径（名册与它同目录）
func snapshotRoster(l *slog.Logger, infoPath string) {
	(&rosterSampler{path: rosterPath(infoPath)}).sample(l)
}
