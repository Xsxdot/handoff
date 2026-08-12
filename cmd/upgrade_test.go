// handoff upgrade 的机器范围行为测试。全部经包级缝注入，不联网、不写 ~/、
// 不触碰真实二进制（activateBinary / newReleaseFetcher / execSkillInstall 都是替身）。
package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/client"
	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/release"
)

// fakeMachine 是一台被完全替身化的机器：既不联网也不动任何真实文件。
//
// 用指针（map[string]*fakeMachine）：fake peer 的 PushUpdate 要把 pushed
// 写回调用方拿到的 map，值类型的话测试读不到写入。
type fakeMachine struct {
	platform  string
	version   string
	managed   bool
	noUpdate  bool // 不上报 Update 字段（模拟只报平台不报托管的对端）
	busy      int
	statusErr error
	pushErr   error
	waitErr   error
	pushed    bool
}

// fakeMachines 是当前测试的机器表。键 __本机 的数据用于本机端点。
var fakeMachines map[string]*fakeMachine

// fakePeer 实现 agentdPeer，读取 fakeMachine 的数据。
type fakePeer struct {
	m *fakeMachine
}

func (p *fakePeer) Status(ctx context.Context) (*proto.StatusResp, error) {
	if p.m.statusErr != nil {
		return nil, p.m.statusErr
	}
	resp := &proto.StatusResp{
		Version: proto.BuildInfo{Version: p.m.version, Platform: p.m.platform},
	}
	if !p.m.noUpdate {
		resp.Update = &proto.UpdateStatus{Managed: p.m.managed}
	}
	for i := 0; i < p.m.busy; i++ {
		resp.Active = append(resp.Active, proto.ActiveTask{ID: "t", State: string(proto.TaskStateRunning)})
	}
	return resp, nil
}

func (p *fakePeer) PushUpdate(_ context.Context, tag, _ string, _ []byte, _ bool) (*proto.UpdateResp, error) {
	p.m.pushed = true
	if p.m.pushErr != nil {
		return nil, p.m.pushErr
	}
	return &proto.UpdateResp{OK: true, Version: tag, Prev: "/x.prev", Restarted: true}, nil
}

func (p *fakePeer) RestartAgentd(_ context.Context, _ bool) (*proto.UpdateResp, error) {
	return &proto.UpdateResp{OK: true, Restarted: true}, nil
}

func (p *fakePeer) WaitVersion(_ context.Context, _ string, _, _ time.Duration) error {
	return p.m.waitErr
}

// fakeFetcher 实现 releaseFetcher，两个方法都不落盘不联网。
type fakeFetcher struct{}

func (fakeFetcher) Fetch(context.Context, release.Release, string) (string, error) {
	return "/tmp/.handoff.new", nil
}

func (fakeFetcher) FetchArchive(context.Context, release.Release, string, string) ([]byte, string, error) {
	return []byte("TGZ"), strings.Repeat("a", 64), nil
}

type checkerFunc func(context.Context) (release.Release, error)

func (f checkerFunc) Latest(ctx context.Context) (release.Release, error) { return f(ctx) }

// setupUpgradeTest 把七个缝换成读 fakeMachines 的替身。
func setupUpgradeTest(t *testing.T, machines map[string]*fakeMachine) {
	t.Helper()
	oldC, oldF, oldA, oldR := newReleaseChecker, newReleaseFetcher, activateBinary, rollbackBinary
	oldAC, oldLE := newAgentdClient, listEndpoints
	oldExec := execSkillInstall
	fakeMachines = machines

	newReleaseChecker = func() releaseChecker {
		return checkerFunc(func(context.Context) (release.Release, error) {
			return release.Release{Tag: "v0.1.1"}, nil
		})
	}
	newReleaseFetcher = func() releaseFetcher { return fakeFetcher{} }
	activateBinary = func(string, string) (string, error) { return "/tmp/handoff.prev", nil }
	rollbackBinary = func(string) error { return nil }
	newAgentdClient = func(ep Endpoint) agentdPeer {
		name := ep.Name
		if ep.Local {
			name = "__本机"
		}
		m, ok := fakeMachines[name]
		if !ok {
			m = &fakeMachine{platform: runtime.GOOS + "/" + runtime.GOARCH, version: "v0.1.0", managed: true}
		}
		return &fakePeer{m: m}
	}
	listEndpoints = func(only string) ([]Endpoint, error) {
		names := make([]string, 0)
		for n := range fakeMachines {
			if n != "__本机" {
				names = append(names, n)
			}
		}
		sort.Strings(names)
		if only != "" {
			if _, ok := fakeMachines[only]; !ok {
				return nil, fmt.Errorf("target %q 未在配置中定义", only)
			}
			return []Endpoint{{Name: only, Addr: "http://" + only}}, nil
		}
		eps := []Endpoint{{Name: "本机", Addr: "http://127.0.0.1:7777", Local: true}}
		for _, n := range names {
			eps = append(eps, Endpoint{Name: n, Addr: "http://" + n})
		}
		return eps, nil
	}
	execSkillInstall = func(context.Context, string) (string, error) { return "", nil }

	t.Cleanup(func() {
		newReleaseChecker, newReleaseFetcher, activateBinary, rollbackBinary = oldC, oldF, oldA, oldR
		newAgentdClient, listEndpoints = oldAC, oldLE
		execSkillInstall = oldExec
		fakeMachines = nil
	})
}

func runUpgrade(t *testing.T, args ...string) (string, error) {
	t.Helper()
	resetFlags(t)
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs(append([]string{"upgrade"}, args...))
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		upgradeCheck, upgradeNow, upgradeRollback, upgradeForce = false, false, false, false
	})
	err := rootCmd.ExecuteContext(context.Background())
	return buf.String(), err
}

func runUpgradeCheck(t *testing.T, machines map[string]*fakeMachine) string {
	t.Helper()
	setupUpgradeTest(t, machines)
	out, err := runUpgrade(t)
	if err != nil {
		t.Fatalf("upgrade（--check）: %v", err)
	}
	return out
}

func runUpgradeNow(t *testing.T, machines map[string]*fakeMachine) string {
	t.Helper()
	setupUpgradeTest(t, machines)
	out, _ := runUpgrade(t, "--now")
	return out
}

func runUpgradeNowErr(t *testing.T, machines map[string]*fakeMachine) (string, error) {
	t.Helper()
	setupUpgradeTest(t, machines)
	return runUpgrade(t, "--now")
}

// TestUpgradeCheckRendersEveryMachine 巡检必须一台不落，够不着的也要有一行。
//
// why：漏掉一台够不着的机器，操作者会以为它已经是最新的——而它恰恰是最
// 需要被看见的那台。
func TestUpgradeCheckRendersEveryMachine(t *testing.T) {
	out := runUpgradeCheck(t, map[string]*fakeMachine{
		"devbox": {platform: "linux/amd64", version: "v0.1.0", managed: true},
		"prod":   {platform: "linux/amd64", version: "v0.1.1", managed: true},
		"aliyun": {statusErr: errors.New("dial tcp 10.0.0.5:7777: connect: connection refused")},
	})
	for _, want := range []string{
		"最新     v0.1.1",
		"本机",
		"devbox", "需要升级",
		"prod", "已是最新",
		"aliyun", "够不着", "connection refused",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("巡检输出缺 %q:\n%s", want, out)
		}
	}
	// 本机那一行必须分别显示二进制与 agentd 两个版本：换掉磁盘上的文件后
	// 正在跑的 agentd 仍是旧进程，这是正常且常见的中间态。合成一个数字就
	// 必然骗人——显示旧版让人以为没升成，显示新版让人以为已在跑新代码
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "本机") {
			if !strings.Contains(line, "二进制") || !strings.Contains(line, "agentd") {
				t.Fatalf("本机行必须分别显示二进制与 agentd 版本，实得 %q", line)
			}
		}
	}
}

// TestUpgradeSkipsBusyAndOffersForceLine 闸一被拦时必须给出**可直接复制**的
// --force 命令行，且行里带正确的 target 名字。
func TestUpgradeSkipsBusyAndOffersForceLine(t *testing.T) {
	out := runUpgradeNow(t, map[string]*fakeMachine{
		"devbox": {platform: "linux/amd64", version: "v0.1.0", managed: true, busy: 3},
	})
	if !strings.Contains(out, "handoff upgrade --now --target devbox --force") {
		t.Fatalf("缺可复制的 --force 行:\n%s", out)
	}
}

// TestUpgradeUnmanagedNeverOffersForce 是「处置必须对症」最要紧的一条：
// --force 不越过闸二，给出这条命令等于让用户跑一条注定失败的命令。
func TestUpgradeUnmanagedNeverOffersForce(t *testing.T) {
	out := runUpgradeNow(t, map[string]*fakeMachine{
		"prod": {platform: "linux/amd64", version: "v0.1.0", managed: false},
	})
	if strings.Contains(out, "--force") {
		t.Fatalf("非托管不该给 --force：它不越过闸二\n%s", out)
	}
	if !strings.Contains(out, "handoff service install") {
		t.Fatalf("非托管应提示先装服务:\n%s", out)
	}
}

// TestUpgradeUnreachableInventsNoRemedy 够不着时只报原始错误原文。
// 编一条处置建议就是在猜，而猜出来的建议会把人引到错误的方向。
func TestUpgradeUnreachableInventsNoRemedy(t *testing.T) {
	out := runUpgradeNow(t, map[string]*fakeMachine{
		"aliyun": {statusErr: errors.New("dial tcp 10.0.0.5:7777: connect: connection refused")},
	})
	if !strings.Contains(out, "connection refused") {
		t.Fatalf("必须原样带出错误原文:\n%s", out)
	}
	if strings.Contains(out, "handoff ") {
		t.Fatalf("够不着时不该编任何处置命令:\n%s", out)
	}
}

// TestUpgradePartialFailureContinues 一台失败不阻断其余，且退出码非零。
//
// why：这些机器之间本来就没有事务关系，让一台连不上的机器阻止其余全部
// 升级是纯损失。
func TestUpgradePartialFailureContinues(t *testing.T) {
	machines := map[string]*fakeMachine{
		"aliyun": {statusErr: errors.New("connection refused")},
		"devbox": {platform: "linux/amd64", version: "v0.1.0", managed: true},
	}
	out, err := runUpgradeNowErr(t, machines)
	if err == nil {
		t.Fatal("有机器失败时退出码必须非零")
	}
	if !machines["devbox"].pushed && !strings.Contains(out, "devbox") {
		t.Fatalf("其余机器必须照常升级:\n%s", out)
	}
}

// TestUpgradeRefusesUnknownPlatform 对端没上报平台（老 agentd）时必须
// 明确拒绝，而不是猜一个默认值给一台 linux 机器推 darwin 二进制。
func TestUpgradeRefusesUnknownPlatform(t *testing.T) {
	out := runUpgradeNow(t, map[string]*fakeMachine{
		"old": {platform: "", version: "v0.1.0", managed: true},
	})
	if !strings.Contains(out, "未上报平台") {
		t.Fatalf("应明说对端过旧未上报平台:\n%s", out)
	}
}

// TestUpgradeLocalGoesLast 本机换版会重启本机 agentd，而操作者很可能正用
// 它盯着任务。把干扰最大的一步放最后，前面出问题时不至于白扰一次。
//
// 断言的是**动作序列**而不是输出顺序：输出可以排版成任何样子，真正要锁住的
// 是「本机的重启发生在所有 target 都处理完之后」。
func TestUpgradeLocalGoesLast(t *testing.T) {
	var order []string
	recordOrder = func(name string) { order = append(order, name) }
	t.Cleanup(func() { recordOrder = func(string) {} })

	runUpgradeNow(t, map[string]*fakeMachine{
		"aaa":  {platform: "linux/amd64", version: "v0.1.0", managed: true},
		"zzz":  {platform: "linux/amd64", version: "v0.1.0", managed: true},
		"__本机": {platform: "darwin/arm64", version: "v0.1.0", managed: true},
	})
	if len(order) == 0 {
		t.Fatal("没有记录到任何机器被处理")
	}
	if order[len(order)-1] != "本机" {
		t.Fatalf("本机必须排最后，实得顺序 %v", order)
	}
}

// TestUpgradeReportsUnconfirmedRestart 轮询超时必须报「已换版但新进程未上线」
// 并附回滚命令，**绝不含糊成「升级完成」**。
func TestUpgradeReportsUnconfirmedRestart(t *testing.T) {
	out := runUpgradeNow(t, map[string]*fakeMachine{
		"devbox": {platform: "linux/amd64", version: "v0.1.0", managed: true, waitErr: errors.New("timeout")},
	})
	if strings.Contains(out, "升级完成") {
		t.Fatalf("未确认上线不许报完成:\n%s", out)
	}
	if !strings.Contains(out, "--rollback") {
		t.Fatalf("未上线时必须给回滚出路:\n%s", out)
	}
}

// 两边一致性：同一台机器，handoff upgrade（只读）与 handoff upgrade --now 必须
// 从同一个结论出发。B64 的病根就是两套分支表各活各的——这条测试是它的护栏，
// 其余用例是它的分解。
func TestCheckAndNowAgreeOnEveryMachine(t *testing.T) {
	const plat = "linux/amd64"
	cases := []struct {
		name      string
		m         fakeMachine
		wantCheck string // 巡检行必须含
		wantNow   string // --now 输出必须含
		denyNow   string // --now 输出**不得**含；空串表示不检查
	}{
		{
			name:      "过旧未上报平台（B64 原始症状：曾被报成非托管）",
			m:         fakeMachine{statusErr: client.ErrStatusUnsupported},
			wantCheck: "对端过旧",
			wantNow:   "对端 agentd 过旧",
			denyNow:   "service install",
		},
		{
			name:      "上报平台但不上报托管：不知道就说不知道",
			m:         fakeMachine{platform: plat, version: "v0.1.0", noUpdate: true},
			wantCheck: "需要升级",
			wantNow:   "未上报托管状态",
			denyNow:   "service install",
		},
		{
			name:      "非托管且落后：硬拒并给对症建议",
			m:         fakeMachine{platform: plat, version: "v0.1.0", managed: false},
			wantCheck: "需要升级",
			wantNow:   "service install",
		},
		{
			name:      "非托管但已是最新：没事可做，不催装 service",
			m:         fakeMachine{platform: plat, version: "v0.1.1", managed: false},
			wantCheck: "已是最新",
			wantNow:   "已是最新",
			denyNow:   "service install",
		},
		{
			name:      "有活跃任务但已是最新：不建议一条注定白跑的 --force",
			m:         fakeMachine{platform: plat, version: "v0.1.1", managed: true, busy: 2},
			wantCheck: "已是最新",
			wantNow:   "已是最新",
			denyNow:   "--force",
		},
		{
			name:      "托管且落后：正常升级",
			m:         fakeMachine{platform: plat, version: "v0.1.0", managed: true},
			wantCheck: "需要升级",
			wantNow:   "成功",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			machines := map[string]*fakeMachine{
				"__本机":   {platform: plat, version: "v0.1.1", managed: true},
				"devbox": &c.m,
			}
			check := runUpgradeCheck(t, machines)
			if !strings.Contains(check, c.wantCheck) {
				t.Errorf("check 输出缺 %q：\n%s", c.wantCheck, check)
			}
			machines2 := map[string]*fakeMachine{
				"__本机":   {platform: plat, version: "v0.1.1", managed: true},
				"devbox": &c.m,
			}
			now := runUpgradeNow(t, machines2)
			if !strings.Contains(now, c.wantNow) {
				t.Errorf("--now 输出缺 %q：\n%s", c.wantNow, now)
			}
			if c.denyNow != "" && strings.Contains(now, c.denyNow) {
				t.Errorf("--now 输出不该含 %q：\n%s", c.denyNow, now)
			}
		})
	}
}
