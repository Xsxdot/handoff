// ptyhost_pressure_test.go —— 机器级进程压力统计对 ptyhost 的排除判据。
//
// 职责：用固定进程快照验证 ptyhost 凭据必须同时匹配 PID 与启动时刻。
// 边界：不启动真实 shell，不验证 PTY 协议；真实会话的凭据产生由 ptyhost 客户端负责，
// 本文件只钉住 prochost 的计数边界。
package prochost

import "testing"

func TestCheckAdmissionExcludesOnlyVerifiedPtyhost(t *testing.T) {
	oldEnum, oldLimit, oldProvider := enumProcsFn, procLimitFn, currentPtyhostCredentialProvider()
	t.Cleanup(func() {
		enumProcsFn, procLimitFn = oldEnum, oldLimit
		SetPtyhostCredentialProvider(oldProvider)
	})
	withPolicy(t, true, 0.1)
	enumProcsFn = func() ([]procEntry, error) {
		return []procEntry{
			{PID: 101, StartedAt: 1001}, // 已认证的 ptyhost
			{PID: 102, StartedAt: 1002}, // 普通同 uid 进程
			{PID: 103, StartedAt: 1003}, // 未知身份进程
		}, nil
	}
	procLimitFn = func() (int, error) { return 10, nil }
	SetPtyhostCredentialProvider(func() []ProcessCredential {
		return []ProcessCredential{{PID: 101, StartedAt: 1001}}
	})

	got := CheckAdmission()
	if !got.Known {
		t.Fatal("固定进程快照的余量不应是未知")
	}
	if got.Used != 2 {
		t.Fatalf("used = %d，期望只排除 1 个 ptyhost 后为 2", got.Used)
	}
	if got.Limit != 10 {
		t.Fatalf("limit = %d，期望 10", got.Limit)
	}
}

func TestCheckAdmissionCountsUnverifiedProcesses(t *testing.T) {
	oldEnum, oldLimit, oldProvider := enumProcsFn, procLimitFn, currentPtyhostCredentialProvider()
	t.Cleanup(func() {
		enumProcsFn, procLimitFn = oldEnum, oldLimit
		SetPtyhostCredentialProvider(oldProvider)
	})
	withPolicy(t, true, 0.1)
	enumProcsFn = func() ([]procEntry, error) {
		return []procEntry{
			{PID: 201, StartedAt: 2001}, // PID 相同但启动时刻不同：可能已复用
			{PID: 202, StartedAt: 2002}, // 普通同 uid 进程
			{PID: 203, StartedAt: 2003}, // 凭据提供者没有登记：未知身份
		}, nil
	}
	procLimitFn = func() (int, error) { return 10, nil }
	SetPtyhostCredentialProvider(func() []ProcessCredential {
		return []ProcessCredential{
			{PID: 201, StartedAt: 2999},
			{PID: 999, StartedAt: 9999},
		}
	})

	got := CheckAdmission()
	if got.Used != 3 {
		t.Fatalf("used = %d，未验证的进程必须全部计入", got.Used)
	}
}
