// machinegateway Commander 路由测试。
//
// 职责：
//   - 本机命令只进入本机 authority
//   - 已连接远端命令只进入对应 peer commander
//   - 断开的开发机在任何 I/O 前拒绝
//
// 边界：
//   - 使用内存 fake，不启动 HTTP；peer wire 由 peer/agentd 专项测试覆盖
package machinegateway

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/xushixin/handoff/internal/controlplane"
	"github.com/xushixin/handoff/internal/machineauthority"
)

type catalogFake struct {
	machines map[string]controlplane.Machine
}

func (f catalogFake) GetMachine(_ context.Context, id string) (controlplane.Machine, error) {
	machine, ok := f.machines[id]
	if !ok {
		return controlplane.Machine{}, controlplane.ErrNotFound
	}
	return machine, nil
}

type localAuthorityFake struct {
	inspected string
	cloned    machineauthority.CloneCommand
}

func (f *localAuthorityFake) InspectPath(_ context.Context, path string) (controlplane.PathInspection, error) {
	f.inspected = path
	return controlplane.PathInspection{Path: path}, nil
}

func (f *localAuthorityFake) Clone(_ context.Context, command machineauthority.CloneCommand) (controlplane.PathInspection, error) {
	f.cloned = command
	return controlplane.PathInspection{Path: command.ClonePath}, nil
}

type peerCommanderFake struct {
	inspected controlplane.InspectPathCommand
}

func (f *peerCommanderFake) InspectPath(_ context.Context, command controlplane.InspectPathCommand) (controlplane.PathInspection, error) {
	f.inspected = command
	return controlplane.PathInspection{Path: command.Path}, nil
}

func (f *peerCommanderFake) Clone(_ context.Context, command controlplane.CloneLocationCommand) (controlplane.PathInspection, error) {
	return controlplane.PathInspection{Path: command.ClonePath}, nil
}

type peerResolverFake struct {
	commander controlplane.MachineCommander
	calls     int
}

func (f *peerResolverFake) CommanderForMachine(_ context.Context, _ string) (controlplane.MachineCommander, error) {
	f.calls++
	if f.commander == nil {
		return nil, errors.New("peer missing")
	}
	return f.commander, nil
}

func TestCommanderRoutesLocalAndRemoteWithoutSSH(t *testing.T) {
	local := &localAuthorityFake{}
	remote := &peerCommanderFake{}
	resolver := &peerResolverFake{commander: remote}
	catalog := catalogFake{machines: map[string]controlplane.Machine{
		"local": {ID: "local", Kind: controlplane.MachineKindLocal, Status: controlplane.MachineStatusConnected},
		"remote": {ID: "remote", Kind: controlplane.MachineKindRemote, Status: controlplane.MachineStatusConnected,
			Capabilities: map[string]int{"project_commands": 1}},
	}}
	commander := NewCommander(catalog, local, resolver, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if _, err := commander.InspectPath(context.Background(), controlplane.InspectPathCommand{
		OperationID: "op", TargetID: "local-target", MachineID: "local", Path: "/local/repo",
	}); err != nil {
		t.Fatal(err)
	}
	if local.inspected != "/local/repo" || resolver.calls != 0 {
		t.Fatalf("local route inspected=%q peer_calls=%d", local.inspected, resolver.calls)
	}
	if _, err := commander.InspectPath(context.Background(), controlplane.InspectPathCommand{
		OperationID: "op", TargetID: "remote-target", MachineID: "remote", Path: "/remote/repo",
	}); err != nil {
		t.Fatal(err)
	}
	if remote.inspected.Path != "/remote/repo" || resolver.calls != 1 {
		t.Fatalf("remote route command=%+v peer_calls=%d", remote.inspected, resolver.calls)
	}
}

func TestCommanderRejectsDisconnectedRemoteBeforePeerIO(t *testing.T) {
	resolver := &peerResolverFake{commander: &peerCommanderFake{}}
	commander := NewCommander(catalogFake{machines: map[string]controlplane.Machine{
		"remote": {ID: "remote", Kind: controlplane.MachineKindRemote, Status: controlplane.MachineStatusUnavailable},
	}}, &localAuthorityFake{}, resolver, slog.New(slog.NewTextHandler(io.Discard, nil)))

	_, err := commander.InspectPath(context.Background(), controlplane.InspectPathCommand{
		OperationID: "op", TargetID: "target", MachineID: "remote", Path: "/repo",
	})
	if err == nil {
		t.Fatal("断开的开发机应不可读且不可执行项目命令")
	}
	if resolver.calls != 0 {
		t.Fatalf("断开时不应触发 peer I/O，calls=%d", resolver.calls)
	}
}
