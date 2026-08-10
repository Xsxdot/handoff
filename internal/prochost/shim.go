// shim.go —— shim 进程的主体逻辑。
//
// 职责：
//   - 持有存活锁（整个生命周期），作为 prochost.Alive 的唯一判据
//   - 打开 stdout/stderr 追加落盘文件；InputCh 非空时以 O_RDWR 持有 FIFO 读端
//   - spawn 真正的 executor，把 child_pid 补写进 proc.json
//   - wait 子进程，退出后向 stdout 追加 handoff_exit 哨兵
//
// 边界：
//   - 不认识 executor 协议、不解析输出：只做搬运与收尸
//   - 不写任务状态、不连 agentd：shim 与 agentd 之间只有文件（锁、proc.json、日志）
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
		// 只是诊断信息，写不进去不值得杀掉已经起来的执行者
		l.Warn("补写 child_pid 失败，不影响执行", "info", spec.InfoPath, "cause", err)
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

// ChildPID 读取 proc.json 里 shim 补写的 child_pid（诊断用）。
func ChildPID(infoPath string) (int, error) {
	m, err := readInfoMap(infoPath)
	if err != nil {
		return 0, err
	}
	v, ok := m["child_pid"].(float64)
	if !ok {
		return 0, fmt.Errorf("%s 缺 child_pid 字段", infoPath)
	}
	return int(v), nil
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

// recordChildPID 把 child_pid 合并进已存在的 proc.json。
//
// 为什么是「合并」而不是「覆盖」：proc.json 由 adapter 先写（Handle、session_id 等），
// shim 只补一个字段。整份覆盖会把 adapter 写的恢复凭据抹掉，重启后无法恢复。
func recordChildPID(infoPath string, pid int) error {
	m, err := readInfoMap(infoPath)
	if err != nil {
		m = map[string]any{}
	}
	m["child_pid"] = pid
	b, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("序列化 proc.json: %w", err)
	}
	if err := os.WriteFile(infoPath, b, 0o600); err != nil {
		return fmt.Errorf("写 %s: %w", infoPath, err)
	}
	return nil
}

// readInfoMap 把 proc.json 读成松散 map（便于只改一个字段而不认识其余结构）。
func readInfoMap(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读 %s: %w", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("解析 %s: %w", path, err)
	}
	return m, nil
}

// envKeys 提取 KEY=VALUE 列表里的 key（日志用；值绝不出现在日志里）。
func envKeys(env []string) []string {
	keys := make([]string, 0, len(env))
	for _, kv := range env {
		if i := indexByte(kv, '='); i > 0 {
			keys = append(keys, kv[:i])
		}
	}
	return keys
}

// indexByte 是 strings.IndexByte 的本地别名，避免为一处调用引入 strings 依赖。
func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
