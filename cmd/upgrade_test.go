// handoff upgrade 的机器范围行为测试。全部经包级缝注入，不联网、不写 ~/、
// 不触碰真实二进制（activateBinary / newReleaseFetcher / execSkillInstall 都是替身）。
package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/client"
	"github.com/xushixin/handoff/internal/config"
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

// fakePeer 实现 agentdPeer。两种形态：
//   - m 非 nil：读 fakeMachine（既有测试的形态）；
//   - m 为 nil：用直接字段（自拉测试的形态），pull/platform/version 各自为政。
//
// pullCalls/pushCalls/lastSum 记录调用，供选路断言。
type fakePeer struct {
	m *fakeMachine

	// 自拉测试形态
	pull     *bool
	platform string
	version  string

	pullCalls int
	pushCalls int
	lastSum   string
}

func (p *fakePeer) Status(ctx context.Context) (*proto.StatusResp, error) {
	if p.m != nil {
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
	resp := &proto.StatusResp{
		Version: proto.BuildInfo{Version: p.version, Platform: p.platform},
	}
	// 自拉测试形态：始终上报托管（要走到 remoteUpgrade），Pull 随 p.pull——
	// nil 表示老 agentd 不报 pull，正是降级推送要测的情形；若只在 p.pull != nil
	// 时给 Update，pull=nil 会整段塌成「未上报托管状态」而提前跳过，测不到降级
	resp.Update = &proto.UpdateStatus{Managed: true, Pull: p.pull}
	return resp, nil
}

func (p *fakePeer) PushUpdate(_ context.Context, tag, sum string, _ []byte, _ bool) (*proto.UpdateResp, error) {
	p.pushCalls++
	p.lastSum = sum
	if p.m != nil {
		p.m.pushed = true
		if p.m.pushErr != nil {
			return nil, p.m.pushErr
		}
	}
	return &proto.UpdateResp{OK: true, Version: tag, Prev: "/x.prev", Restarted: true}, nil
}

func (p *fakePeer) PullUpdate(_ context.Context, tag, sum string, _ bool) (*proto.UpdateResp, error) {
	p.pullCalls++
	p.lastSum = sum
	return &proto.UpdateResp{OK: true, Accepted: true, Version: tag}, nil
}

func (p *fakePeer) RestartAgentd(_ context.Context, _ bool) (*proto.UpdateResp, error) {
	return &proto.UpdateResp{OK: true, Restarted: true}, nil
}

func (p *fakePeer) WaitVersion(_ context.Context, _ string, _, _ time.Duration, _ bool) error {
	if p.m != nil {
		return p.m.waitErr
	}
	return nil
}

// fakeFetcher 实现 releaseFetcher。sum 是 FetchChecksum 返回的校验和；
// checksumCalls / archiveCalls 记录调用次数，供「checksums 只下一次」类断言用。
// 零值即可用（既有测试不关心计数）。
type fakeFetcher struct {
	sum           string
	checksumCalls int
	archiveCalls  int
}

func (f *fakeFetcher) Fetch(context.Context, release.Release, string) (string, error) {
	return "/tmp/.handoff.new", nil
}

func (f *fakeFetcher) FetchArchive(context.Context, release.Release, string, string) ([]byte, string, error) {
	f.archiveCalls++
	return []byte("TGZ"), strings.Repeat("a", 64), nil
}

func (f *fakeFetcher) FetchChecksum(context.Context, release.Release, string, string) (string, error) {
	f.checksumCalls++
	return f.sum, nil
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
	newReleaseFetcher = func() releaseFetcher { return &fakeFetcher{} }
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
		upgradePush = false
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

func runUpgradeNow(t *testing.T, machines ...map[string]*fakeMachine) string {
	t.Helper()
	if len(machines) > 0 {
		setupUpgradeTest(t, machines[0])
	}
	out, _ := runUpgrade(t, "--now")
	return out
}

func runUpgradeNowErr(t *testing.T, machines map[string]*fakeMachine) (string, error) {
	t.Helper()
	setupUpgradeTest(t, machines)
	return runUpgrade(t, "--now")
}

// withStubs 把 upgrade 的七个缝整体换成给定的一台替身机器（自拉测试形态）。
// listEndpoints 只返回一台远端 devbox，不处理本机。
func withStubs(t *testing.T, fetcher *fakeFetcher, peer *fakePeer) {
	t.Helper()
	oldC, oldF, oldA, oldLE := newReleaseChecker, newReleaseFetcher, newAgentdClient, listEndpoints
	oldAct, oldRol, oldExec := activateBinary, rollbackBinary, execSkillInstall
	newReleaseChecker = func() releaseChecker {
		return checkerFunc(func(context.Context) (release.Release, error) {
			return release.Release{Tag: "v0.1.1"}, nil
		})
	}
	newReleaseFetcher = func() releaseFetcher { return fetcher }
	byName := map[string]*fakePeer{"devbox": peer}
	newAgentdClient = func(ep Endpoint) agentdPeer { return byName[ep.Name] }
	listEndpoints = func(only string) ([]Endpoint, error) {
		return []Endpoint{{Name: "devbox", Addr: "http://devbox"}}, nil
	}
	activateBinary = func(string, string) (string, error) { return "/tmp/handoff.prev", nil }
	rollbackBinary = func(string) error { return nil }
	execSkillInstall = func(context.Context, string) (string, error) { return "", nil }
	t.Cleanup(func() {
		newReleaseChecker, newReleaseFetcher, newAgentdClient, listEndpoints = oldC, oldF, oldA, oldLE
		activateBinary, rollbackBinary, execSkillInstall = oldAct, oldRol, oldExec
	})
}

// withStubsMulti 同 withStubs，但注入多台替身机器（按端点名索引，避免与
// probeMachine/process 各调一次 newAgentdClient 打架）。
func withStubsMulti(t *testing.T, fetcher *fakeFetcher, peers []*fakePeer) {
	t.Helper()
	oldC, oldF, oldA, oldLE := newReleaseChecker, newReleaseFetcher, newAgentdClient, listEndpoints
	oldAct, oldRol, oldExec := activateBinary, rollbackBinary, execSkillInstall
	newReleaseChecker = func() releaseChecker {
		return checkerFunc(func(context.Context) (release.Release, error) {
			return release.Release{Tag: "v0.1.1"}, nil
		})
	}
	newReleaseFetcher = func() releaseFetcher { return fetcher }
	byName := make(map[string]*fakePeer, len(peers))
	eps := make([]Endpoint, 0, len(peers))
	for idx, p := range peers {
		name := fmt.Sprintf("devbox%d", idx)
		byName[name] = p
		eps = append(eps, Endpoint{Name: name, Addr: "http://" + name})
	}
	newAgentdClient = func(ep Endpoint) agentdPeer { return byName[ep.Name] }
	listEndpoints = func(only string) ([]Endpoint, error) {
		if only != "" {
			if _, ok := byName[only]; !ok {
				return nil, fmt.Errorf("target %q 未在配置中定义", only)
			}
			return []Endpoint{{Name: only, Addr: "http://" + only}}, nil
		}
		return eps, nil
	}
	activateBinary = func(string, string) (string, error) { return "/tmp/handoff.prev", nil }
	rollbackBinary = func(string) error { return nil }
	execSkillInstall = func(context.Context, string) (string, error) { return "", nil }
	t.Cleanup(func() {
		newReleaseChecker, newReleaseFetcher, newAgentdClient, listEndpoints = oldC, oldF, oldA, oldLE
		activateBinary, rollbackBinary, execSkillInstall = oldAct, oldRol, oldExec
	})
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

// 配置里的代理必须真的传进 release 的 client。断言打在 Transport 的 Proxy
// 行为上而不是指针相等——函数值不可比较，且我们要的本来就是行为。
func TestProxyTransportUsesConfig(t *testing.T) {
	rt := proxyTransport(&config.Config{Proxy: "socks5://127.0.0.1:1080"})
	tr, ok := rt.(*http.Transport)
	if !ok {
		t.Fatalf("期望 *http.Transport，实得 %T", rt)
	}
	req, _ := http.NewRequest(http.MethodGet, "https://api.github.com/x", nil)
	u, err := tr.Proxy(req)
	if err != nil || u == nil || u.Host != "127.0.0.1:1080" {
		t.Fatalf("代理未接上，proxy=%v err=%v", u, err)
	}
}

// 未配代理时必须返回 nil（= 标准库默认 transport），而不是一个"什么都不代理"
// 的自造 transport——后者会丢掉 DefaultTransport 的连接池与 HTTP/2 设置。
func TestProxyTransportNilWhenUnset(t *testing.T) {
	if rt := proxyTransport(&config.Config{}); rt != nil {
		t.Fatalf("未配代理时应返回 nil，实得 %T", rt)
	}
}

// 坏代理不得让 CLI 崩：配置校验已经在 Load 时挡过一道，这里是纵深防御，
// 走到这儿只可能是有人绕过 Load 直接构造了 Config。降级为不用代理并打日志。
func TestProxyTransportBadValueDegrades(t *testing.T) {
	if rt := proxyTransport(&config.Config{Proxy: "socks4://h:1"}); rt != nil {
		t.Fatalf("坏代理应降级为 nil，实得 %T", rt)
	}
}

// 对端上报 pull=true 时走自拉：不下 20MB 资产，只下 checksums 并下发 tag+sum。
func TestRemoteUpgradeUsesPullWhenCapable(t *testing.T) {
	// 沿用本文件既有的替身装配方式（listEndpoints / newAgentdClient /
	// newReleaseChecker / newReleaseFetcher 四个缝全部替换）
	fetcher := &fakeFetcher{sum: "abc"}
	peer := &fakePeer{pull: boolPtr(true), platform: "linux/amd64", version: "v0.9.0"}
	withStubs(t, fetcher, peer)

	runUpgradeNow(t)

	if fetcher.archiveCalls != 0 {
		t.Errorf("自拉模式不得下载资产，实得 %d 次", fetcher.archiveCalls)
	}
	if peer.pullCalls != 1 {
		t.Errorf("应调一次 PullUpdate，实得 %d", peer.pullCalls)
	}
	if peer.pushCalls != 0 {
		t.Errorf("不该调 PushUpdate，实得 %d", peer.pushCalls)
	}
	if peer.lastSum != "abc" {
		t.Errorf("下发的 sha256 = %q，期望 abc", peer.lastSum)
	}
}

// 对端没上报 pull（老 agentd，nil）→ 自动降级推送，升级链路不断。
func TestRemoteUpgradeFallsBackToPushWhenPullNil(t *testing.T) {
	fetcher := &fakeFetcher{sum: "abc"}
	peer := &fakePeer{pull: nil, platform: "linux/amd64", version: "v0.9.0"}
	withStubs(t, fetcher, peer)

	runUpgradeNow(t)

	if peer.pushCalls != 1 {
		t.Errorf("老 agentd 应降级推送，实得 push=%d pull=%d", peer.pushCalls, peer.pullCalls)
	}
}

// --push 强制走推送，无论对端能力如何——内网执行机出不了网时的逃生路径。
func TestPushFlagForcesPushMode(t *testing.T) {
	fetcher := &fakeFetcher{sum: "abc"}
	peer := &fakePeer{pull: boolPtr(true), platform: "linux/amd64", version: "v0.9.0"}
	withStubs(t, fetcher, peer)

	upgradePush = true
	defer func() { upgradePush = false }()
	runUpgradeNow(t)

	if peer.pushCalls != 1 || peer.pullCalls != 0 {
		t.Errorf("--push 应强制推送，实得 push=%d pull=%d", peer.pushCalls, peer.pullCalls)
	}
}

// checksums.txt 对一个 release 只下一次，同平台的多台机器共用缓存——
// 这正是自拉省流量的点，每台机器各下一次会把省下的流量又还回去一部分，
// 而且平白多几次 GitHub 请求。
// 注意：校验和按平台不同，缓存键是 goos/goarch，所以两台机器必须是**同平台**
// 才只下一次（不同平台的校验和本来就不同，各下一次是正确行为）。
func TestChecksumFetchedOncePerRun(t *testing.T) {
	fetcher := &fakeFetcher{sum: "abc"}
	withStubsMulti(t, fetcher, []*fakePeer{
		{pull: boolPtr(true), platform: "linux/amd64", version: "v0.9.0"},
		{pull: boolPtr(true), platform: "linux/amd64", version: "v0.9.0"},
	})

	runUpgradeNow(t)

	if fetcher.checksumCalls != 1 {
		t.Errorf("checksums 应只下一次，实得 %d 次", fetcher.checksumCalls)
	}
}
