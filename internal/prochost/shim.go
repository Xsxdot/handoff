// shim.go —— shim 进程的主体逻辑。
//
// 职责：
//   - 持有存活锁（整个生命周期），作为 prochost.Alive 的唯一判据
//   - 打开 stdout/stderr 追加落盘文件；InputCh 非空时以 O_RDWR 持有 FIFO 读端
//   - 在 spawn executor 之前安装进程围栏（RLIMIT_NPROC），executor 全树继承
//   - spawn 真正的 executor，把它的 pid 记进 child.pid
//   - wait 子进程，退出后向 stdout 追加 handoff_exit 哨兵
//
// 边界：
//   - 不认识 executor 协议、不解析输出：只做搬运与收尸
//   - 不写任务状态、不连 agentd：shim 与 agentd 之间只有文件（锁、child.pid、日志）
//   - 不写 proc.json：那是 adapter 的独占文件，双写者会丢更新（见 recordChildPID）
//   - 不决定围栏值取多少：那是 prochost 策略层（fence.go）的事，shim 只负责装
//
// 为什么必须有 shim（而不是 agentd 直接 detach executor）：退出哨兵需要一个
// 常驻父进程 waitpid 才能拿到。agentd 重启后，reparent 给 init 的 executor
// 已经没法被 waitpid，「agentd 离线期间 executor 退出」的退出码就永远丢了——
// 那正是恢复流程最需要知道的事。shim 用一个极轻的进程换回这个语义。
//
// 为什么 shim 以 O_RDWR 打开 FIFO：只读打开会在写端全部关闭时收到 EOF，
// executor 的 stdin 随即关闭；O_RDWR 让 shim 自己同时是写端，FIFO 永不 EOF。
// 这是旧 sh 脚本 `exec 3<> in.fifo` 的等价手法。
package prochost

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// SentinelPrefix 是死亡哨兵行的类型标记，adapter 扫 stdout 判死时匹配它。
const SentinelPrefix = `"type":"handoff_exit"`

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

	// 围栏必须在 spawn 之前装：rlimit 随 fork 继承，装晚一步 executor 就在
	// 围栏外面了。装不上不阻断——防护装置故障不该变成拒绝服务
	if spec.NprocLimit > 0 {
		if ferr := setNprocLimit(spec.NprocLimit); ferr != nil {
			l.Warn("安装进程围栏失败，本任务无围栏保护", "limit", spec.NprocLimit, "cause", ferr)
		} else {
			l.Info("进程围栏已安装", "limit", spec.NprocLimit)
		}
	} else {
		l.Info("本任务未设进程围栏", "reason", "spec 未下发围栏值")
	}

	cmd := exec.Command(spec.Argv[0], spec.Argv[1:]...)
	cmd.Dir = spec.Dir
	cmd.Env = spec.Env
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if spec.InputCh != "" {
		// O_RDWR 而非 O_RDONLY：见文件头注释的 why（FIFO 永不 EOF）
		fifo, ferr := os.OpenFile(spec.InputCh, os.O_RDWR, 0)
		if ferr != nil {
			l.Error("打开输入通道失败", "path", spec.InputCh, "cause", ferr)
			return fmt.Errorf("打开输入通道 %s: %w", spec.InputCh, ferr)
		}
		defer fifo.Close()
		cmd.Stdin = fifo
	}

	// env 只打 key 名：值可能含凭据（代理 URL 里的 user:pass、API key）
	l.Info("shim 拉起执行者进程", "bin", spec.Argv[0], "dir", spec.Dir,
		"env_keys", envKeys(spec.Env), "input_ch", spec.InputCh != "")
	if err := cmd.Start(); err != nil {
		l.Error("拉起执行者进程失败", "bin", spec.Argv[0], "cause", err)
		return fmt.Errorf("拉起 %s: %w", spec.Argv[0], err)
	}
	childPID := cmd.Process.Pid
	if err := recordChildPID(spec.InfoPath, childPID); err != nil {
		// 只是诊断信息（回收靠锁与进程组，不靠它），写不进去不值得杀掉已经起来的执行者
		l.Warn("记录 child.pid 失败，不影响执行", "path", childPIDPath(spec.InfoPath), "cause", err)
	}
	l.Info("执行者进程已启动", "child_pid", childPID)

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
