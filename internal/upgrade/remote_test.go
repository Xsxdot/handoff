package upgrade

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/release"
)

type fakePeer struct {
	pushCalls int
	pullCalls int
	waitCalls int
	waitErr   error
	pullErr   error
}

func (p *fakePeer) PushUpdate(context.Context, string, string, []byte, bool) (*proto.UpdateResp, error) {
	p.pushCalls++
	return &proto.UpdateResp{OK: true, Version: "v0.3.1", Prev: "/tmp/prev"}, nil
}

func (p *fakePeer) PullUpdate(context.Context, string, string, bool) (*proto.UpdateResp, error) {
	p.pullCalls++
	if p.pullErr != nil {
		return nil, p.pullErr
	}
	return &proto.UpdateResp{OK: true, Accepted: true, Version: "v0.3.1"}, nil
}

func (p *fakePeer) WaitVersion(context.Context, string, time.Duration, time.Duration, bool) error {
	p.waitCalls++
	return p.waitErr
}

type fakeFetcher struct {
	archiveCalls  int
	checksumCalls int
}

func (f *fakeFetcher) FetchArchive(context.Context, release.Release, string, string) ([]byte, string, error) {
	f.archiveCalls++
	return []byte("archive"), "archive-sum", nil
}

func (f *fakeFetcher) FetchChecksum(context.Context, release.Release, string, string) (string, error) {
	f.checksumCalls++
	return "checksum", nil
}

func testRelease() release.Release { return release.Release{Tag: "v0.3.1"} }

func testOptions() Options {
	return Options{WaitPull: time.Second, WaitPush: time.Second, WaitInterval: time.Millisecond}
}

func managedMachine(pull *bool) Machine {
	return Machine{
		Name: "devbox", Agentd: "v0.3.0", Platform: "linux/amd64",
		Managed: boolPtr(true), Pull: pull,
	}
}

// 对端支持自拉时走 pull，不下载资产。
func TestRemoteOneUsesPullWhenCapable(t *testing.T) {
	peer := &fakePeer{}
	fetcher := &fakeFetcher{}
	result := RemoteOne(context.Background(), nil, managedMachine(boolPtr(true)), peer, fetcher,
		testRelease(), testOptions(), nil)

	if result.Status != StatusOK || result.From != "v0.3.0" || result.To != "v0.3.1" {
		t.Fatalf("结果 = %+v，期望成功的版本迁移", result)
	}
	if peer.pullCalls != 1 || peer.pushCalls != 0 {
		t.Fatalf("应只走 pull，push=%d pull=%d", peer.pushCalls, peer.pullCalls)
	}
	if fetcher.archiveCalls != 0 {
		t.Fatalf("自拉模式不得下载资产，archive=%d", fetcher.archiveCalls)
	}
	if fetcher.checksumCalls != 1 {
		t.Fatalf("自拉模式应只取一次校验和，checksum=%d", fetcher.checksumCalls)
	}
}

// 对端没上报 pull 能力（nil）时退回 push——nil 不许当 true 用。
func TestRemoteOneFallsBackToPushWhenPullUnknown(t *testing.T) {
	peer := &fakePeer{}
	fetcher := &fakeFetcher{}
	result := RemoteOne(context.Background(), nil, managedMachine(nil), peer, fetcher,
		testRelease(), testOptions(), nil)

	if result.Status != StatusOK {
		t.Fatalf("结果 = %+v，期望成功", result)
	}
	if peer.pushCalls != 1 || peer.pullCalls != 0 {
		t.Fatalf("pull=nil 时应只走 push，push=%d pull=%d", peer.pushCalls, peer.pullCalls)
	}
	if fetcher.archiveCalls != 1 || fetcher.checksumCalls != 0 {
		t.Fatalf("推送模式资产/校验和调用不对，archive=%d checksum=%d", fetcher.archiveCalls, fetcher.checksumCalls)
	}
}

// 三种拒绝各给各的处置，且非托管与自拉在跑的 Forcible 必须是 false。
func TestRemoteOneRejectionsCarryMatchingRemedy(t *testing.T) {
	tests := []struct {
		name      string
		machine   Machine
		peer      *fakePeer
		wantForce bool
		wantText  string
	}{
		{
			name:    "busy",
			machine: Machine{Name: "devbox", Agentd: "v0.3.0", Platform: "linux/amd64", Managed: boolPtr(true), Busy: 2},
			peer:    &fakePeer{}, wantForce: true, wantText: "--force",
		},
		{
			name:      "unmanaged",
			machine:   managedMachine(boolPtr(true)),
			peer:      &fakePeer{pullErr: &client.UpdateRejected{Reason: proto.UpdateReasonUnmanaged, Msg: "agentd 非托管启动"}},
			wantForce: false, wantText: "service install",
		},
		{
			name:      "pull in progress",
			machine:   managedMachine(boolPtr(true)),
			peer:      &fakePeer{pullErr: &client.UpdateRejected{Reason: proto.UpdateReasonPullInProgress, Msg: "已有一个自拉换版在进行中"}},
			wantForce: false, wantText: "pull_state",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := RemoteOne(context.Background(), nil, tc.machine, tc.peer, &fakeFetcher{},
				testRelease(), testOptions(), nil)
			if result.Status != StatusSkip || result.Forcible != tc.wantForce {
				t.Fatalf("结果 = %+v，期望 skip/forcible=%v", result, tc.wantForce)
			}
			if !strings.Contains(result.Remedy, tc.wantText) {
				t.Fatalf("处置建议 %q 不含 %q", result.Remedy, tc.wantText)
			}
		})
	}
}

// 等新版本上线超时：pull 与 push 的 Reason 必须不同措辞。
func TestRemoteOneWaitFailureWordingDiffersByMode(t *testing.T) {
	for _, tc := range []struct {
		name     string
		pull     *bool
		pullMode bool
	}{
		{name: "pull", pull: boolPtr(true), pullMode: true},
		{name: "push", pull: boolPtr(false), pullMode: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			peer := &fakePeer{waitErr: errors.New("等待超时")}
			result := RemoteOne(context.Background(), nil, managedMachine(tc.pull), peer, &fakeFetcher{},
				testRelease(), testOptions(), nil)
			if result.Status != StatusFail {
				t.Fatalf("结果 = %+v，期望失败", result)
			}
			if tc.pullMode && !strings.Contains(result.Remedy, "pull_state") {
				t.Fatalf("自拉失败应引导查看 pull_state，结果 = %+v", result)
			}
			if !tc.pullMode && !strings.Contains(result.Remedy, "回滚") {
				t.Fatalf("推送失败应给回滚建议，结果 = %+v", result)
			}
		})
	}
}

// 平台格式非法时失败，且不去猜一个默认平台。
func TestRemoteOneRefusesMalformedPlatform(t *testing.T) {
	peer := &fakePeer{}
	fetcher := &fakeFetcher{}
	result := RemoteOne(context.Background(), nil, Machine{
		Name: "devbox", Agentd: "v0.3.0", Platform: "linux", Managed: boolPtr(true),
	}, peer, fetcher, testRelease(), testOptions(), nil)

	if result.Status != StatusFail || !strings.Contains(result.Reason, "格式非法") {
		t.Fatalf("结果 = %+v，期望平台格式失败", result)
	}
	if peer.pushCalls != 0 || peer.pullCalls != 0 || fetcher.archiveCalls != 0 || fetcher.checksumCalls != 0 {
		t.Fatalf("非法平台不得进入下载/推送，peer=%+v fetcher=%+v", peer, fetcher)
	}
}
