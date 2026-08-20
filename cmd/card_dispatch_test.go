package cmd

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestCardDispatchClaimAndSnapshot(t *testing.T) {
	dir := t.TempDir()

	out, _, err := runLedgerCLI(t, dir, "card", "add", "要派的卡", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &c); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runLedgerCLI(t, dir, "card", "update", c.ID, "--accept", "测试全绿"); err != nil {
		t.Fatal(err)
	}

	var gotPrompt, gotProject string
	restore := swapDispatchTransport(func(prompt, branch, target, project string) (string, error) {
		gotPrompt, gotProject = prompt, project
		return "T-fake-1", nil
	})
	defer restore()

	out, _, err = runLedgerCLI(t, dir, "card", "dispatch", c.ID,
		"--template", "feature-impl", "--target", "mac-02", "--discipline-override", "implement")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !strings.Contains(out, "T-fake-1") {
		t.Fatalf("输出应含 task id: %q", out)
	}
	if strings.Contains(gotPrompt, "# 执行纪律") || !strings.Contains(gotPrompt, "要派的卡") ||
		!strings.Contains(gotPrompt, "测试全绿") {
		t.Fatalf("prompt 拼装: %q", gotPrompt)
	}
	if gotProject != "demo" {
		t.Fatalf("派发未带 project: %q", gotProject)
	}

	show, _, err := runLedgerCLI(t, dir, "card", "show", c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(show, `"Status":"进行中"`) && !strings.Contains(show, `"status":"进行中"`) {
		t.Fatalf("未认领: %q", show)
	}
	if _, _, err := runLedgerCLI(t, dir, "card", "dispatch", c.ID, "--template", "feature-impl",
		"--target", "mac-02", "--discipline-override", "implement"); err == nil ||
		!strings.Contains(err.Error(), "认领") {
		t.Fatalf("重复派发应报已认领: %v", err)
	}
	if !strings.Contains(show, "dispatched") || !strings.Contains(show, "discipline_name") {
		t.Fatalf("快照事件缺失: %q", show)
	}
}

// 派发失败必须连驱动租约一起退：只退状态会让卡停在「待办但有主」，
// 而驱动身份带 pid——同一个人换个进程重试会被自己挡住 5 分钟。
func TestCardDispatchFailureReleasesLease(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runLedgerCLI(t, dir, "card", "add", "会派失败的卡", "--project", "demo")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &c); err != nil {
		t.Fatal(err)
	}

	restore := swapDispatchTransport(func(prompt, branch, target, project string) (string, error) {
		return "", errors.New("起点在任务仓库中不存在")
	})
	if _, _, err := runLedgerCLI(t, dir, "card", "dispatch", c.ID,
		"--template", "feature-impl", "--target", "mac-02", "--discipline-override", "implement"); err == nil {
		t.Fatal("传输失败时派发应报错")
	}
	restore()

	show, _, err := runLedgerCLI(t, dir, "card", "show", c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(show, `"driver_session":""`) && strings.Contains(show, `"driver_session":"`) {
		t.Fatalf("派发失败后租约未释放: %q", show)
	}

	// 真正的判据：换一个会话（新进程即新会话）能立刻重派
	restore = swapDispatchTransport(func(prompt, branch, target, project string) (string, error) {
		return "T-retry-1", nil
	})
	defer restore()
	if _, _, err := runLedgerCLI(t, dir, "card", "dispatch", c.ID,
		"--template", "feature-impl", "--target", "mac-02", "--discipline-override", "implement"); err != nil {
		t.Fatalf("失败后重派应放行: %v", err)
	}
}

// TestCardDispatchSendsDisciplineName 模板的角色名要随派发请求上送，
// 而不是被 CLI 读成正文拼进 prompt。
func TestCardDispatchSendsDisciplineName(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runLedgerCLI(t, dir, "card", "add", "要派的卡", "--project", "demo")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &c); err != nil {
		t.Fatal(err)
	}

	var got dispatchRequest
	restore := swapDispatchTransportWithOpts(func(req dispatchRequest) (string, error) {
		got = req
		return "T-fake-1", nil
	})
	defer restore()

	if _, _, err := runLedgerCLI(t, dir, "card", "dispatch", c.ID,
		"--template", "feature-impl", "--target", "mac-02"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if got.discipline != "implement" {
		t.Fatalf("请求里应带角色名 implement，实得 %q", got.discipline)
	}
}

// TestCardDispatchNoDisciplineInPrompt prompt 里不许再出现纪律块正文。
// 这是本次重构的核心判据：两份纪律块同时在场时，审阅那次的「只读，不写」
// 会被实现块的「每个 task 完成即 commit」推翻。
func TestCardDispatchNoDisciplineInPrompt(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runLedgerCLI(t, dir, "card", "add", "要派的卡", "--project", "demo")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &c); err != nil {
		t.Fatal(err)
	}

	var got dispatchRequest
	restore := swapDispatchTransportWithOpts(func(req dispatchRequest) (string, error) {
		got = req
		return "T-fake-2", nil
	})
	defer restore()

	if _, _, err := runLedgerCLI(t, dir, "card", "dispatch", c.ID,
		"--template", "feature-impl", "--target", "mac-02"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	for _, mark := range []string{"# 审阅纪律", "# 执行纪律", "只读，不写", "每个 task 完成即 commit"} {
		if strings.Contains(got.prompt, mark) {
			t.Fatalf("prompt 里不该再有纪律块正文，命中 %q：\n%s", mark, got.prompt)
		}
	}
	if !strings.Contains(got.prompt, "要派的卡") {
		t.Fatalf("模板正文应还在：\n%s", got.prompt)
	}
}

// TestCardDispatchOverrideReplacesName --discipline-override 改的是名字，
// 不再是文件路径。
func TestCardDispatchOverrideReplacesName(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runLedgerCLI(t, dir, "card", "add", "要派的卡", "--project", "demo")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &c); err != nil {
		t.Fatal(err)
	}

	var got dispatchRequest
	restore := swapDispatchTransportWithOpts(func(req dispatchRequest) (string, error) {
		got = req
		return "T-fake-3", nil
	})
	defer restore()

	if _, _, err := runLedgerCLI(t, dir, "card", "dispatch", c.ID,
		"--template", "feature-impl", "--target", "mac-02",
		"--discipline-override", "review"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if got.discipline != "review" {
		t.Fatalf("override 应替换名字，实得 %q", got.discipline)
	}
}

// TestCardDispatchSnapshotRecordsDisciplineName 派发事件快照要答得出
// 「这次用的哪块纪律」——正文不再经过 CLI，指纹算不出来了，
// 但那个问题本身没消失，答案换成名字。
func TestCardDispatchSnapshotRecordsDisciplineName(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runLedgerCLI(t, dir, "card", "add", "要派的卡", "--project", "demo")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &c); err != nil {
		t.Fatal(err)
	}
	restore := swapDispatchTransportWithOpts(func(req dispatchRequest) (string, error) {
		return "T-fake-4", nil
	})
	defer restore()
	if _, _, err := runLedgerCLI(t, dir, "card", "dispatch", c.ID,
		"--template", "feature-impl", "--target", "mac-02"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	show, _, err := runLedgerCLI(t, dir, "card", "show", c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(show, `"discipline_name":"implement"`) {
		t.Fatalf("快照应记下角色名: %q", show)
	}
}
