package prochost

import (
	"errors"
	"testing"
)

// TestMarkMembersUnsupportedReportsNotSupported 钉住「平台不支持」必须表达为
// supported=false，而不是空集——空集会被上层当成「确实没有成员」。
func TestMarkMembersUnsupportedReportsNotSupported(t *testing.T) {
	old := attributesFn
	defer func() { attributesFn = old }()
	attributesFn = func(pid int, cred TaskCred) (bool, error) { return false, errNotSupported }

	members, supported := markMembers(TaskCred{TaskID: "t1"}, []procEntry{{PID: 10}, {PID: 11}})
	if supported {
		t.Fatalf("平台不支持时 supported 必须为 false")
	}
	if len(members) != 0 {
		t.Fatalf("平台不支持时不得返回成员，实得 %v", members)
	}
}

// TestMarkMembersEmptyCredIsNoop 钉住凭据为空时判据整个不参与：
// 这是「仅托管 worktree 可杀」与「升级前 proc.json 无字段」两条降级的共同出口。
func TestMarkMembersEmptyCredIsNoop(t *testing.T) {
	old := attributesFn
	defer func() { attributesFn = old }()
	called := 0
	attributesFn = func(pid int, cred TaskCred) (bool, error) { called++; return true, nil }

	members, supported := markMembers(TaskCred{}, []procEntry{{PID: 10}})
	if called != 0 {
		t.Fatalf("凭据为空时不应调用平台原语，实调 %d 次", called)
	}
	if supported || len(members) != 0 {
		t.Fatalf("凭据为空应表达为不可用：supported=%v members=%v", supported, members)
	}
}

// TestMarkMembersSkipsPerPIDFailure 钉住单个 pid 读失败不影响整批——
// 进程在枚举与读取之间退出是常态，不是异常。
func TestMarkMembersSkipsPerPIDFailure(t *testing.T) {
	old := attributesFn
	defer func() { attributesFn = old }()
	attributesFn = func(pid int, cred TaskCred) (bool, error) {
		switch pid {
		case 10:
			return true, nil
		case 11:
			return false, errors.New("no such process")
		default:
			return false, nil
		}
	}

	members, supported := markMembers(TaskCred{TaskID: "t1"},
		[]procEntry{{PID: 10}, {PID: 11}, {PID: 12}})
	if !supported {
		t.Fatalf("有 pid 读成功时 supported 应为 true")
	}
	if len(members) != 1 || members[0] != 10 {
		t.Fatalf("应只归属 pid=10，实得 %v", members)
	}
}
