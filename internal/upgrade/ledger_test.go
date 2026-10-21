// B462 S3：换版前账本可读 → 换版后必须仍可读才判成功。四格判据（纯函数）与 RemoteOne 接线。
package upgrade

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestJudgeLedgerAfterGrid(t *testing.T) {
	probeErr := errors.New("connection refused")
	for _, tc := range []struct {
		name     string
		before   *bool
		readable bool
		err      error
		want     LedgerCheck
	}{
		{"前可读后可读", boolPtr(true), true, nil, LedgerCheckOK},
		{"前可读后503", boolPtr(true), false, nil, LedgerCheckNotMounted},
		{"前可读后出错", boolPtr(true), false, probeErr, LedgerCheckUnconfirmed},
		{"前不可读后任意", boolPtr(false), false, probeErr, LedgerCheckSkip},
		{"前未知后任意", nil, false, nil, LedgerCheckSkip},
	} {
		if got := JudgeLedgerAfter(tc.before, tc.readable, tc.err); got != tc.want {
			t.Errorf("%s：得 %v，期望 %v", tc.name, got, tc.want)
		}
	}
}

func ledgerMachine(before *bool) Machine {
	m := managedMachine(boolPtr(false))
	m.LedgerReadable = before
	return m
}

func TestRemoteOneLedgerReadableBeforeAndAfterIsOK(t *testing.T) {
	peer := &fakePeer{probes: []probeAnswer{{readable: true}}}
	r := RemoteOne(context.Background(), nil, ledgerMachine(boolPtr(true)), peer, &fakeFetcher{}, testRelease(), testOptions(), nil)
	if r.Status != StatusOK || peer.probeCalls != 1 {
		t.Fatalf("前后均可读应成功且探一次：%+v probes=%d", r, peer.probeCalls)
	}
	if strings.Join(peer.seq, ",") != "wait,probe" {
		t.Fatalf("探针只能在 WaitVersion 成功之后调用，序列=%v", peer.seq)
	}
}

func TestRemoteOneLedgerNotMountedAfterUpgradeFails(t *testing.T) {
	peer := &fakePeer{probes: []probeAnswer{{readable: false}}}
	r := RemoteOne(context.Background(), nil, ledgerMachine(boolPtr(true)), peer, &fakeFetcher{}, testRelease(), testOptions(), nil)
	if r.Status != StatusFail || !strings.Contains(r.Reason, "新版已上线但账本未挂载") {
		t.Fatalf("换版后 503 应判失败「新版已上线但账本未挂载」，得 %+v", r)
	}
	if !strings.Contains(r.Remedy, "日志") || !strings.Contains(r.Remedy, "/tmp/prev") {
		t.Fatalf("处置应指向该机日志并附 prev 路径，得 %q", r.Remedy)
	}
	if peer.probeCalls != 1 {
		t.Fatalf("503 应立即判定不重试，probes=%d", peer.probeCalls)
	}
}

func TestRemoteOneLedgerUnknownOrUnreadableBeforeIsNotChecked(t *testing.T) {
	for _, before := range []*bool{nil, boolPtr(false)} {
		peer := &fakePeer{probes: []probeAnswer{{err: errors.New("boom")}}}
		r := RemoteOne(context.Background(), nil, ledgerMachine(before), peer, &fakeFetcher{}, testRelease(), testOptions(), nil)
		if r.Status != StatusOK || peer.probeCalls != 0 {
			t.Fatalf("before=%v：换版前不可读/未知不检查（不调探针），得 %+v probes=%d", before, r, peer.probeCalls)
		}
	}
}

func TestRemoteOneLedgerProbeErrorsThriceIsUnconfirmed(t *testing.T) {
	peer := &fakePeer{probes: []probeAnswer{{err: errors.New("dial tcp: connection reset")}}}
	r := RemoteOne(context.Background(), nil, ledgerMachine(boolPtr(true)), peer, &fakeFetcher{}, testRelease(), testOptions(), nil)
	if r.Status != StatusFail || !strings.Contains(r.Reason, "未能确认") || !strings.Contains(r.Reason, "connection reset") {
		t.Fatalf("换版后探针连错 3 次应判「未能确认」并带原文，得 %+v", r)
	}
	if peer.probeCalls != 3 {
		t.Fatalf("应探 3 次，得 %d", peer.probeCalls)
	}
}

func TestRemoteOneLedgerProbeErrorThenReadableIsOK(t *testing.T) {
	peer := &fakePeer{probes: []probeAnswer{{err: errors.New("EOF")}, {readable: true}}}
	r := RemoteOne(context.Background(), nil, ledgerMachine(boolPtr(true)), peer, &fakeFetcher{}, testRelease(), testOptions(), nil)
	if r.Status != StatusOK || peer.probeCalls != 2 {
		t.Fatalf("出错一次后可读应成功，得 %+v probes=%d", r, peer.probeCalls)
	}
}

// WaitVersion 失败时不探账本；推送超时原因追加 WaitVersion 原文（Q9）。
func TestRemoteOneWaitFailureSkipsProbeAndCarriesCause(t *testing.T) {
	peer := &fakePeer{waitErr: errors.New("agentd 未就绪（账本未挂载：自登记失败，第 3 次重试）"), probes: []probeAnswer{{readable: true}}}
	r := RemoteOne(context.Background(), nil, ledgerMachine(boolPtr(true)), peer, &fakeFetcher{}, testRelease(), testOptions(), nil)
	if r.Status != StatusFail || peer.probeCalls != 0 {
		t.Fatalf("WaitVersion 失败不得调探针，得 %+v probes=%d", r, peer.probeCalls)
	}
	if !strings.Contains(r.Reason, "已换版但新进程未在") || !strings.Contains(r.Reason, "自登记失败，第 3 次重试") {
		t.Fatalf("推送超时原因应保留前缀并追加原文，得 %q", r.Reason)
	}
}
