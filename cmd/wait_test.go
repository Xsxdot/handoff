// wait_test.go —— wait --follow 的 CLI 层契约测试。
//
// 职责：钉住空闲超时与 stalltimeout 的告警判据（纯函数），以及 --timeout
// 负值仍被拒绝这条既有行为在 follow 下不退化。
//
// 边界：事件交付/终态识别在 internal/client 的 follow_test 里验，这里不重复。
package cmd

import (
	"strings"
	"testing"
	"time"
)

// TestIdleTimeoutWarning 钉死 §2.2 的硬约束：--timeout 必须大于 stalltimeout。
//
// 为什么：两者都在测「多久没动静」，但 stalled 是 agentd 带着 last_seq 和 idle
// 时长给出的**诊断**，124 只是客户端说「我没收到东西」。同时到点时 124 会抢先
// 退出进程，把一次已诊断清楚的停滞降级成一次连接超时——信息严格更少。
func TestIdleTimeoutWarning(t *testing.T) {
	cases := []struct {
		name     string
		idle     time.Duration
		stall    time.Duration
		wantWarn bool
	}{
		{"小于 stalltimeout：告警", time.Hour, 2 * time.Hour, true},
		{"等于 stalltimeout：告警（同时到点 124 会抢先）", 2 * time.Hour, 2 * time.Hour, true},
		{"大于 stalltimeout：不告警", 3 * time.Hour, 2 * time.Hour, false},
		{"未设 --timeout：不告警", 0, 2 * time.Hour, false},
		{"对端未提供 stalltimeout：不告警（不拿未知当结论）", time.Hour, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := idleTimeoutWarning(c.idle, c.stall)
			if (got != "") != c.wantWarn {
				t.Fatalf("idleTimeoutWarning(%s, %s) = %q, wantWarn=%v",
					c.idle, c.stall, got, c.wantWarn)
			}
			if c.wantWarn && !strings.Contains(got, c.stall.String()) {
				t.Errorf("告警未点名对端的 stalltimeout: %q", got)
			}
		})
	}
}

// TestFollowRejectsNegativeTimeout 验证 --follow 下负时长同样被拒绝，
// 不因为走了新分支就把这条既有防线漏掉。
func TestFollowRejectsNegativeTimeout(t *testing.T) {
	t.Cleanup(func() { followFlag = false; waitTimeout = 0 })
	followFlag = true
	waitTimeout = -5 * time.Second
	err := waitCmd.RunE(waitCmd, []string{"任意-id"})
	if err == nil || !strings.Contains(err.Error(), "--timeout") {
		t.Fatalf("负时长应被拒绝并点名 --timeout，实际: %v", err)
	}
}
