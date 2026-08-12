package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/proto"
)

// TestIsProjectNotRegistered 验证 CLI 能从 agentd 的 400 报文里认出
// 「项目未登记」，从而触发自动补登记后重发（spec §6.2）。
//
// 为什么按文本判：错误跨进程传递，errors.Is 在这一侧失效；agentd 的
// ErrProjectNotRegistered 报文以「项目未登记」四字开头，两边靠这个约定对齐。
func TestIsProjectNotRegistered(t *testing.T) {
	yes := []string{
		"dispatch 失败: HTTP 400: 项目未登记: project_id=9f2a1c7d5e3b0a84；本机已登记的项目：（本机尚无任何项目）",
		"项目未登记: \"nova\"；本机已登记的项目：handoff → /w/handoff",
	}
	for _, s := range yes {
		if !isProjectNotRegistered(errStr(s)) {
			t.Errorf("应识别为未登记: %q", s)
		}
	}
	no := []string{
		"dispatch 失败: HTTP 409: 工作区不干净",
		"dispatch 失败: HTTP 400: 请求未指明项目（project_id 与 project_name 至少其一）",
		"",
	}
	for _, s := range no {
		if isProjectNotRegistered(errStr(s)) {
			t.Errorf("不应识别为未登记: %q", s)
		}
	}
	if isProjectNotRegistered(nil) {
		t.Error("nil 不应识别为未登记")
	}
}

// errStr 把字符串包成 error，供表驱动用例使用。
type errStr string

func (e errStr) Error() string { return string(e) }

// TestDispatchWithAutoRegisterRetriesOnce 验证编排的正常路径：
// 首次派发被拒 → 触发一次登记 → 重发成功。
func TestDispatchWithAutoRegisterRetriesOnce(t *testing.T) {
	dispatches, registers := 0, 0
	task, err := dispatchWithAutoRegister(
		func() (*proto.Task, error) {
			dispatches++
			if dispatches == 1 {
				return nil, errStr("HTTP 400: 项目未登记: project_id=abc")
			}
			return &proto.Task{ID: "t1"}, nil
		},
		func() error { registers++; return nil },
	)
	if err != nil {
		t.Fatalf("重发应成功: %v", err)
	}
	if task == nil || task.ID != "t1" {
		t.Fatalf("应返回重发得到的任务, got %+v", task)
	}
	if dispatches != 2 || registers != 1 {
		t.Fatalf("应派发 2 次、登记 1 次，got dispatch=%d register=%d", dispatches, registers)
	}
}

// TestDispatchWithAutoRegisterGivesUpAfterOneRetry 验证登记成功后仍被拒时**不再重试**：
// 那说明另有原因（如刚被别人 project rm 掉），无限重试会把可诊断的失败变成死循环。
func TestDispatchWithAutoRegisterGivesUpAfterOneRetry(t *testing.T) {
	dispatches, registers := 0, 0
	_, err := dispatchWithAutoRegister(
		func() (*proto.Task, error) {
			dispatches++
			return nil, errStr("HTTP 400: 项目未登记: project_id=abc")
		},
		func() error { registers++; return nil },
	)
	if err == nil {
		t.Fatal("持续被拒时应返回错误")
	}
	if dispatches != 2 || registers != 1 {
		t.Fatalf("最多派发 2 次、登记 1 次，got dispatch=%d register=%d", dispatches, registers)
	}
}

// TestDispatchWithAutoRegisterSurfacesRegisterFailure 验证登记失败时透出原文、
// **不重发**：clone 失败或落点被占都需要人去那台机器上处置，替它猜只会掩盖真因。
func TestDispatchWithAutoRegisterSurfacesRegisterFailure(t *testing.T) {
	dispatches := 0
	_, err := dispatchWithAutoRegister(
		func() (*proto.Task, error) {
			dispatches++
			return nil, errStr("HTTP 400: 项目未登记: project_id=abc")
		},
		func() error { return errStr("落点 /root/work/handoff 已存在") },
	)
	if err == nil {
		t.Fatal("登记失败时 dispatch 应整体失败")
	}
	if !strings.Contains(err.Error(), "落点 /root/work/handoff 已存在") {
		t.Errorf("应透出登记失败原文，got %q", err.Error())
	}
	if dispatches != 1 {
		t.Fatalf("登记失败后不应重发，got dispatch=%d", dispatches)
	}
}

// TestDispatchWithAutoRegisterPassesThroughOtherErrors 验证非「未登记」的错误
// 原样透出，绝不触发登记——工作区不干净之类的失败自动登记帮不上任何忙。
func TestDispatchWithAutoRegisterPassesThroughOtherErrors(t *testing.T) {
	sentinel := errStr("HTTP 409: 工作区不干净")
	registers := 0
	_, err := dispatchWithAutoRegister(
		func() (*proto.Task, error) { return nil, sentinel },
		func() error { registers++; return nil },
	)
	if !errors.Is(err, error(sentinel)) {
		t.Fatalf("应原样透出原错误，got %v", err)
	}
	if registers != 0 {
		t.Fatalf("不该触发登记，got register=%d", registers)
	}
}
