//go:build !darwin && !linux

// procenum_other.go —— 非 darwin/linux 的空实现。
//
// 一律返回 errNotSupported 而不是空集：调用方必须据此降级为「未知」，
// 而不是渲染出一个 0 让人误以为足迹是空的（见 procenum.go 的 why）。
//
// **Windows 上这个缺席的含义与其它平台不同，别照字面理解**：进程回收职责已由
// Job Object 承担（shim 持 KILL_ON_JOB_CLOSE 的 job，子进程无法自行逃逸），
// 所以这里返回未实现**不**意味着「进程可能逃逸没人管」。缺的只是「足迹观测」
// ——看不到某个任务开了多少进程，以及依赖计数的 TaskBudget 告警档。
// 硬上限档（TaskHardLimit）由 job 的 ActiveProcessLimit 接管，仍然生效。
// 详见 spec 2026-08-17-windows-native-executor-design.md 的 3.2 与 11.6.1。
package prochost

func enumProcs() ([]procEntry, error) { return nil, errNotSupported }

func procLimit() (int, error) { return 0, errNotSupported }
