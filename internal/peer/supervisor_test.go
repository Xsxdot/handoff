// peer supervisor 测试：远端同步生命周期与断线语义。
//
// 职责：
//   - 远端断线保留投影但 Machine=unavailable
//   - catch-up 完成并 Reconcile 后才=connected
//   - 每台机器一个串行 worker，坏机器不阻塞其他
//
// 边界：
//   - 使用内存 fake client 与 fake repository，不发起真实 HTTP
package peer

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/controlplane"
)

// fakePeerClient 是可编程的 peer.Client。
type fakePeerClient struct {
	mu        sync.Mutex
	helloErr  error
	helloCaps map[string]int
	allEvents []MachineEvent // 全部事件（按序切批）
	called    int
}

func (f *fakePeerClient) Hello(ctx context.Context) (Hello, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.helloErr != nil {
		return Hello{}, f.helloErr
	}
	caps := f.helloCaps
	if caps == nil {
		caps = map[string]int{"catalog": 1, "machine_events": 1}
	}
	return Hello{ProtocolVersion: 1, Capabilities: caps}, nil
}

func (f *fakePeerClient) EventsAfter(ctx context.Context, machineID string, afterSeq int64, limit int) ([]MachineEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// 从 afterSeq 之后开始返回，每批最多 limit 条（模拟真实分页）
	var start int
	for i, ev := range f.allEvents {
		if ev.MachineSeq > afterSeq {
			start = i
			break
		}
		start = i + 1
	}
	end := start + limit
	if end > len(f.allEvents) {
		end = len(f.allEvents)
	}
	if start >= len(f.allEvents) {
		return nil, nil
	}
	f.called++
	return f.allEvents[start:end], nil
}

func (f *fakePeerClient) MachineSnapshot(ctx context.Context) (MachineSnapshot, error) {
	return MachineSnapshot{ThroughMachineSeq: 2, WorkspaceCount: 1}, nil
}

func (f *fakePeerClient) Close() {}

var _ = errors.New
var _ = time.Second

// TestSupervisorConnectedAfterCatchUp 验证 catch-up 完成后进入 connected。
func TestSupervisorConnectedAfterCatchUp(t *testing.T) {
	client := &fakePeerClient{allEvents: []MachineEvent{
		{MachineID: "m1", MachineSeq: 1, EventID: "e1", Kind: "workspace.upsert"},
		{MachineID: "m1", MachineSeq: 2, EventID: "e2", Kind: "workspace.upsert"},
	}}
	statuses := make(chan SupervisorState, 8)
	s := NewSupervisor(SupervisorConfig{
		MachineID: "m1",
		Client:    client,
		Projector: &recordingProjector{},
		OnState: func(st SupervisorState) {
			statuses <- st
		},
	})
	s.Run(context.Background())
	// 必须最终进入 connected
	deadline := time.After(5 * time.Second)
	connected := false
	for !connected {
		select {
		case st := <-statuses:
			if st == SupervisorStateConnected {
				connected = true
			}
		case <-deadline:
			t.Fatal("supervisor 未在 5s 内进入 connected")
		}
	}
	// 两批事件用 limit=200 一次拉完，但 catch-up 至少调用一次
	client.mu.Lock()
	called := client.called
	client.mu.Unlock()
	if called < 1 {
		t.Fatalf("catch-up 未调用: called=%d", called)
	}
}

// TestSupervisorClientErrorSetsUnavailable 验证客户端错误导致 Machine=unavailable。
func TestSupervisorClientErrorSetsUnavailable(t *testing.T) {
	client := &fakePeerClient{helloErr: errors.New("connection refused")}
	statuses := make(chan SupervisorState, 8)
	s := NewSupervisor(SupervisorConfig{
		MachineID: "m1",
		Client:    client,
		Projector: &recordingProjector{},
		OnState: func(st SupervisorState) {
			statuses <- st
		},
	})
	s.Run(context.Background())
	deadline := time.After(5 * time.Second)
	for {
		select {
		case st := <-statuses:
			if st == SupervisorStateUnavailable {
				return // 期望最终 unavailable
			}
		case <-deadline:
			t.Fatal("客户端错误应进入 unavailable")
		}
	}
}

func TestSupervisorReportsNegotiatedCapabilities(t *testing.T) {
	client := &fakePeerClient{helloCaps: map[string]int{
		"catalog": 1, "machine_events": 1, CapabilityFiles: 1, CapabilityGit: 1, "unknown": 1,
	}}
	var gotProtocol int
	var gotCapabilities map[string]int
	supervisor := NewSupervisor(SupervisorConfig{
		MachineID: "devbox", Client: client, Projector: &recordingProjector{},
		OnNegotiated: func(protocol int, capabilities map[string]int) {
			gotProtocol, gotCapabilities = protocol, capabilities
		},
	})
	supervisor.Run(context.Background())
	if gotProtocol != 1 || gotCapabilities[CapabilityFiles] != 1 || gotCapabilities[CapabilityGit] != 1 {
		t.Fatalf("negotiated = protocol:%d capabilities:%+v", gotProtocol, gotCapabilities)
	}
	if _, ok := gotCapabilities["unknown"]; ok {
		t.Fatalf("未知 capability 不应上报: %+v", gotCapabilities)
	}
}

// recordingProjector 记录被应用的 machine event。
type recordingProjector struct {
	mu      sync.Mutex
	applied []controlplane.MachineEvent
}

func (r *recordingProjector) Apply(ctx context.Context, ev controlplane.MachineEvent) (controlplane.ControlEvent, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.applied = append(r.applied, ev)
	return controlplane.ControlEvent{ControlRevision: int64(len(r.applied))}, true, nil
}
