package prochost

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

// 围栏原语在受支持平台上必须能读到一个正数上限；不支持的平台必须明确报
// errFenceNotSupported 而不是返回 0——0 会被误读成「上限为零」。
func TestGetNprocLimitReportsPositiveOrNotSupported(t *testing.T) {
	n, err := getNprocLimit()
	switch runtime.GOOS {
	case "darwin", "linux":
		if err != nil {
			t.Fatalf("受支持平台读上限失败: %v", err)
		}
		if n <= 0 {
			t.Fatalf("上限应为正数，得到 %d", n)
		}
	default:
		if !errors.Is(err, errFenceNotSupported) {
			t.Fatalf("不支持的平台应返回 errFenceNotSupported，得到 %v", err)
		}
	}
}

// 非正数围栏值是调用方的 bug，必须当场拒绝：把 RLIMIT_NPROC 设成 0 会让
// 这个进程再也 fork 不出任何东西，是不可逆的自杀。
func TestSetNprocLimitRejectsNonPositive(t *testing.T) {
	for _, n := range []int{0, -1} {
		if err := setNprocLimit(n); err == nil {
			t.Fatalf("围栏值 %d 应被拒绝，却返回了 nil", n)
		}
	}
}

// withFakeProcs 把两个读数缝换成固定值，恢复交给 t.Cleanup。
// 与 B69 的 enumProcsFn 同款路数：判据测试必须喂固定快照，不能依赖真机。
func withFakeProcs(t *testing.T, used int, limit int, limitErr error) {
	t.Helper()
	oldEnum, oldLimit := enumProcsFn, procLimitFn
	procs := make([]procEntry, used)
	for i := range procs {
		procs[i] = procEntry{PID: i + 1, PGID: i + 1, StartedAt: 1}
	}
	enumProcsFn = func() ([]procEntry, error) { return procs, nil }
	procLimitFn = func() (int, error) { return limit, limitErr }
	t.Cleanup(func() { enumProcsFn, procLimitFn = oldEnum, oldLimit })
}

// withFakeLimit 把「本进程实际生效的软限」读缝换成固定值，恢复交给 t.Cleanup。
// 归因（ExplainForkFailure）读的是这个值而不是 procLimitFn/策略 L——见
// getNprocLimitFn 的 why。
func withFakeLimit(t *testing.T, limit int, limitErr error) {
	t.Helper()
	old := getNprocLimitFn
	getNprocLimitFn = func() (int, error) { return limit, limitErr }
	t.Cleanup(func() { getNprocLimitFn = old })
}

// withPolicy 临时改策略，恢复交给 t.Cleanup。
func withPolicy(t *testing.T, disabled bool, ratio float64) {
	t.Helper()
	oldD, oldR := fenceDisabled, fenceReserveRatio
	fenceDisabled, fenceReserveRatio = disabled, ratio
	t.Cleanup(func() { fenceDisabled, fenceReserveRatio = oldD, oldR })
}

// 正常机器：2666 的上限、10% 保留额 → 围栏 2400，救护车道 266。
func TestFenceLimitLeavesAmbulanceLane(t *testing.T) {
	withFakeProcs(t, 0, 2666, nil)
	withPolicy(t, false, 0.1)
	got, err := fenceLimit()
	if err != nil {
		t.Fatalf("算围栏失败: %v", err)
	}
	if got != 2400 {
		t.Fatalf("围栏应为 2400，得到 %d", got)
	}
}

// 小机器：比例算出来的保留额低于下限时，下限接管——救护车道再窄
// 也要塞得下 agentd + sshd + 登录 shell。
func TestFenceLimitReserveFloorTakesOver(t *testing.T) {
	withFakeProcs(t, 0, 1000, nil)
	withPolicy(t, false, 0.1) // 10% = 100 < 200
	got, err := fenceLimit()
	if err != nil {
		t.Fatalf("算围栏失败: %v", err)
	}
	if got != 800 { // 1000 - 200
		t.Fatalf("围栏应为 800（下限 200 接管），得到 %d", got)
	}
}

// 上限小到留不出保留额：不设围栏，且**不是错误**——这台机器本来就没有
// 划分的余地，硬划会让 executor 一个进程都起不来。
func TestFenceLimitTooSmallDisablesFence(t *testing.T) {
	withFakeProcs(t, 0, 150, nil)
	withPolicy(t, false, 0.1)
	got, err := fenceLimit()
	if err != nil {
		t.Fatalf("上限过小不应报错，得到 %v", err)
	}
	if got != 0 {
		t.Fatalf("应返回 0（不设围栏），得到 %d", got)
	}
}

// 策略关闭时直接返回 0，不去读系统上限。
func TestFenceLimitDisabled(t *testing.T) {
	withFakeProcs(t, 0, 2666, nil)
	withPolicy(t, true, 0.1)
	got, err := fenceLimit()
	if err != nil || got != 0 {
		t.Fatalf("策略关闭应返回 (0, nil)，得到 (%d, %v)", got, err)
	}
}

// 读数不可信时 Known=false，且 Full/NearFull 恒为 false——调用方据此
// fail-open。为「量不出来」而拒绝派发，代价远大于收益。
func TestCheckAdmissionUnknownFailsOpen(t *testing.T) {
	withFakeProcs(t, 100, 0, ErrNotSupported)
	withPolicy(t, false, 0.1)
	a := CheckAdmission()
	if a.Known {
		t.Fatalf("读不到上限时 Known 应为 false，得到 %+v", a)
	}
	if a.Full() || a.NearFull() {
		t.Fatalf("未知状态下 Full/NearFull 必须恒 false，得到 %+v", a)
	}
}

// 水位判定以**围栏值**为参考上限，不是系统上限：2400 的九成是 2160。
func TestCheckAdmissionWatermarkUsesFenceLimit(t *testing.T) {
	withPolicy(t, false, 0.1)
	withFakeProcs(t, 2159, 2666, nil)
	if a := CheckAdmission(); a.NearFull() {
		t.Fatalf("2159/2400 未到九成，不该判高水位: %+v", a)
	}
	withFakeProcs(t, 2160, 2666, nil)
	a := CheckAdmission()
	if !a.Known || a.Limit != 2400 {
		t.Fatalf("参考上限应为围栏值 2400，得到 %+v", a)
	}
	if !a.NearFull() {
		t.Fatalf("2160/2400 已达九成，应判高水位: %+v", a)
	}
	if a.Full() {
		t.Fatalf("2160/2400 还没满，不该判 Full: %+v", a)
	}
}

// EAGAIN + 占用 ≥ 实际生效软限 = 确定归因；文案必须带真实数字（协调者要靠它
// 一眼定性）。参考上限是本进程实际软限（getNprocLimit），不是策略层重算的默认 L。
func TestExplainForkFailureQuotaExhausted(t *testing.T) {
	withFakeProcs(t, 2450, 2666, nil)
	withFakeLimit(t, 2400, nil)
	note, quota := ExplainForkFailure(fmt.Errorf("fork/exec /bin/sh: %w", syscall.EAGAIN))
	if !quota {
		t.Fatalf("占用贴住上限的 EAGAIN 应判为配额耗尽，得到 quota=false note=%q", note)
	}
	if !strings.Contains(note, "2450") || !strings.Contains(note, "2400") {
		t.Fatalf("归因文案必须带 used/limit 真实数字，得到 %q", note)
	}
}

// 归因必须引用「真正在起作用的那个限值」：shim 是独立进程、从不调用
// SetFencePolicy，策略层默认 L 与它实际装上的围栏可能完全不同——烟测实证
// （装了围栏 100、占用 363，代码却按默认 2400 把确定的配额耗尽判成「不像配额
// 问题」）。本例模拟 shim 上下文：实际软限 100、占用 363 ≥ 100，必须判配额
// 耗尽、数字是 100 而不是策略默认 2400。
func TestExplainForkFailureUsesActualLimitNotPolicyDefault(t *testing.T) {
	withFakeProcs(t, 363, 2666, nil) // used=363；procLimitFn 仍返回系统上限 2666
	withFakeLimit(t, 100, nil)       // 实际生效软限 = 围栏 100
	// 不设 withPolicy：shim 进程从不 SetFencePolicy，策略层保持默认 0.1 → L=2400。
	// 归因若落到默认 L 就会把 363 < 2160 判成「不像配额问题」——正是本用例要防的。
	note, quota := ExplainForkFailure(fmt.Errorf("fork/exec /bin/sleep: %w", syscall.EAGAIN))
	if !quota {
		t.Fatalf("占用 363 ≥ 围栏 100，应确定判为配额耗尽，得到 quota=false note=%q", note)
	}
	if !strings.Contains(note, "363") || !strings.Contains(note, "100") {
		t.Fatalf("归因文案必须带真实 used/limit（100），得到 %q", note)
	}
	if strings.Contains(note, "2400") {
		t.Fatalf("归因不得引用策略默认 L=2400，得到 %q", note)
	}
}

// EAGAIN 但占用低于实际软限：**如实说不知道**。会说谎的诊断比没有诊断更糟——
// 这正是本次事故里「报错长得像 flaky 测试」把排障带偏 43 分钟的反面。
func TestExplainForkFailureLowUsageStaysHonest(t *testing.T) {
	withFakeProcs(t, 800, 2666, nil)
	withFakeLimit(t, 2400, nil)
	note, quota := ExplainForkFailure(fmt.Errorf("fork/exec /bin/sh: %w", syscall.EAGAIN))
	if quota {
		t.Fatalf("低占用不该判配额耗尽: %q", note)
	}
	if !strings.Contains(note, "未知") {
		t.Fatalf("低占用应如实说原因未知，得到 %q", note)
	}
}

// 非 EAGAIN 的错误一律不认领：返回空串，调用方据此不改写原错误。
func TestExplainForkFailureIgnoresUnrelated(t *testing.T) {
	note, quota := ExplainForkFailure(errors.New("permission denied"))
	if note != "" || quota {
		t.Fatalf("无关错误不该被认领，得到 (%q, %v)", note, quota)
	}
	if note, _ := ExplainForkFailure(nil); note != "" {
		t.Fatalf("nil 错误不该被认领，得到 %q", note)
	}
}

// Start 必须自己把围栏值填进 Spec：四个 adapter 各自构造 Spec，交给它们填
// 等于四处都可能漏，而漏掉的后果是这个任务完全没有保护、且没人看得出来。
func TestApplyFencePolicyFillsSpec(t *testing.T) {
	withFakeProcs(t, 0, 2666, nil) // 见 fence_test.go
	withPolicy(t, false, 0.1)
	var spec Spec
	applyFencePolicy(&spec)
	if spec.NprocLimit != 2400 {
		t.Fatalf("Spec 应被填入围栏值 2400，得到 %d", spec.NprocLimit)
	}
}

// 策略关闭时字段保持 0——0 是「不设围栏」的约定值，shim 据此跳过安装。
func TestApplyFencePolicyDisabledLeavesZero(t *testing.T) {
	withFakeProcs(t, 0, 2666, nil)
	withPolicy(t, true, 0.1)
	spec := Spec{NprocLimit: 999} // 故意预置脏值，确认会被覆盖成 0
	applyFencePolicy(&spec)
	if spec.NprocLimit != 0 {
		t.Fatalf("策略关闭时围栏值应为 0，得到 %d", spec.NprocLimit)
	}
}

// TestFenceLimitHardLimitMode 钉住 Windows 模式下的围栏值来源。
//
// 为什么要有这条：Windows 没有 RLIMIT_NPROC 式的每用户上限，reserve_ratio 那套
// 算不出数（procLimit 返回未实现），照搬会让围栏静默缺席——真机日志实测过
// 「读不到系统进程上限，本次不设围栏」。本用例把「换一套取值来源」钉死。
func TestFenceLimitHardLimitMode(t *testing.T) {
	oldMode, oldHard, oldDisabled := fenceHardLimitMode, fenceTaskHardLimit, fenceDisabled
	t.Cleanup(func() {
		fenceHardLimitMode, fenceTaskHardLimit, fenceDisabled = oldMode, oldHard, oldDisabled
	})
	fenceHardLimitMode, fenceDisabled = true, false

	// 取 TaskHardLimit 原值，不做保留比例换算
	fenceTaskHardLimit = 1200
	got, err := fenceLimit()
	if err != nil || got != 1200 {
		t.Fatalf("硬上限模式应取 TaskHardLimit 原值：got=%d err=%v，want=1200 nil", got, err)
	}

	// TaskHardLimit==0 是「不启用该档」的合法表达，不是错误
	fenceTaskHardLimit = 0
	got, err = fenceLimit()
	if err != nil || got != 0 {
		t.Fatalf("TaskHardLimit=0 应为不设围栏且不报错：got=%d err=%v，want=0 nil", got, err)
	}

	// 全局关掉围栏优先于一切
	fenceDisabled, fenceTaskHardLimit = true, 1200
	got, err = fenceLimit()
	if err != nil || got != 0 {
		t.Fatalf("fenceDisabled 应优先：got=%d err=%v，want=0 nil", got, err)
	}
}

// TestFenceLimitHardLimitModeIgnoresProcLimit 证明硬上限模式**根本不碰** procLimit。
//
// 这是本次改动的要害：Windows 上 procLimit 返回错误，只要 fenceLimit 还调它，
// 围栏就还是算不出来。用一个必然失败的 procLimitFn 把这条路钉死。
func TestFenceLimitHardLimitModeIgnoresProcLimit(t *testing.T) {
	oldMode, oldHard, oldFn := fenceHardLimitMode, fenceTaskHardLimit, procLimitFn
	t.Cleanup(func() {
		fenceHardLimitMode, fenceTaskHardLimit, procLimitFn = oldMode, oldHard, oldFn
	})
	fenceHardLimitMode, fenceTaskHardLimit = true, 800
	procLimitFn = func() (int, error) { return 0, errors.New("本平台不支持进程枚举") }

	got, err := fenceLimit()
	if err != nil || got != 800 {
		t.Fatalf("硬上限模式不得依赖 procLimit：got=%d err=%v，want=800 nil", got, err)
	}
}
