//go:build linux

// taskmark_linux.go —— 本文件是 Task 1 为满足编译而落的桩，真实实现由 Task 2 / Task 3 替换。
package prochost

func attributes(pid int, cred TaskCred) (bool, error) { return false, errNotSupported }
