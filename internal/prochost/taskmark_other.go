//go:build !darwin && !linux

// taskmark_other.go —— 非 darwin/linux 的任务标记空实现。
//
// 一律返回 errNotSupported 而不是 false：false 的含义是「读到了，且不属于」，
// 与「这个平台我们读不了」是两回事——后者必须让调用方降级为 pgid + roster，
// 而不是据此认定进程不属于任务（那会让清扫漏掉真正的残留）。
//
// Windows 走这条：归属问题已由 B37 的 Job Object 从源头消解（内核容器连坐回收），
// 不需要事后判定。
package prochost

func attributes(pid int, cred TaskCred) (bool, error) { return false, errNotSupported }
