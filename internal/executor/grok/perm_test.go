package grok_test

import (
	"context"
	"errors"
	"testing"

	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/executor/grok"
)

func TestRespondPermissionUnknownTaskIsNotRunning(t *testing.T) {
	a := grok.New(nil)
	err := a.RespondPermission(context.Background(), "no-such-task", "call-1", "once")
	if !errors.Is(err, executor.ErrTaskNotRunning) {
		t.Fatalf("未知任务必须包装 ErrTaskNotRunning，得到 %v", err)
	}
}

func TestRespondPermissionUnknownPermIDIsNotRunning(t *testing.T) {
	a, _ := grok.NewAdapterWithRunForTest("t1")
	err := a.RespondPermission(context.Background(), "t1", "call-does-not-exist", "once")
	if !errors.Is(err, executor.ErrTaskNotRunning) {
		t.Fatalf("挂起表查不到必须包装 ErrTaskNotRunning（executor 已不在），得到 %v", err)
	}
}

func TestDecisionMapsToACPOptionIDs(t *testing.T) {
	cases := map[string]string{"once": "allow-once", "reject": "reject-once"}
	for decision, want := range cases {
		if got := grok.OptionIDForTest(decision); got != want {
			t.Errorf("decision %q → %q，期望 %q", decision, got, want)
		}
	}
	// 未知裁决必须 fail-closed 到拒绝，绝不误放行
	if got := grok.OptionIDForTest("garbage"); got != "reject-once" {
		t.Errorf("未知裁决必须 fail-closed 为 reject-once，得到 %q", got)
	}
}

func TestVoidAllPendingCountsAndClears(t *testing.T) {
	_, r := grok.NewAdapterWithRunForTest("t1")
	grok.NotePendingForTest(r, "c1", []byte("1"))
	grok.NotePendingForTest(r, "c2", []byte("2"))
	if n := grok.VoidAllPendingForTest(r); n != 2 {
		t.Errorf("作废数 = %d，期望 2", n)
	}
	if n := grok.VoidAllPendingForTest(r); n != 0 {
		t.Errorf("重复作废应为 0，得到 %d", n)
	}
}

func TestPermissionsVolatileIsTrue(t *testing.T) {
	// 实测：重连 + session/load 后未决权限不会被重发，manager 据此不恢复
	if !grok.New(nil).PermissionsVolatile() {
		t.Error("grok 的权限随连接消亡，必须返回 true")
	}
}
