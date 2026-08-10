// Package machinegateway 把项目创建命令路由到所属机器的 agentd。
//
// 职责：
//   - 以控制面 Machine 状态为门禁，拒绝断线或不兼容的开发机
//   - local 命令直达本机 machineauthority
//   - remote 命令按稳定 machine_id 转发到已配对 peer client
//
// 边界：
//   - 不使用 SSH、不读取 endpoint/token；凭证只由 peer registry 持有
//   - 不做项目 Location 业务校验；该规则仍由 controlplane.ProjectService 负责
package machinegateway

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/xushixin/handoff/internal/controlplane"
	"github.com/xushixin/handoff/internal/machineauthority"
	"github.com/xushixin/handoff/internal/peer"
)

// CatalogReader 是命令路由所需的最小机器事实读取端口。
type CatalogReader interface {
	GetMachine(context.Context, string) (controlplane.Machine, error)
}

// LocalAuthority 是本机项目目录检查与 clone 的窄端口。
type LocalAuthority interface {
	InspectPath(context.Context, string) (controlplane.PathInspection, error)
	Clone(context.Context, machineauthority.CloneCommand) (controlplane.PathInspection, error)
}

// PeerCommanderResolver 按稳定 machine_id 返回远端 agentd 命令客户端。
type PeerCommanderResolver interface {
	CommanderForMachine(context.Context, string) (controlplane.MachineCommander, error)
}

// Commander 实现 controlplane.MachineCommander 的本机/远端统一路由。
type Commander struct {
	catalog CatalogReader
	local   LocalAuthority
	peers   PeerCommanderResolver
	log     *slog.Logger
}

// NewCommander 创建项目机器命令路由器。
func NewCommander(catalog CatalogReader, local LocalAuthority, peers PeerCommanderResolver, log *slog.Logger) *Commander {
	if log == nil {
		log = slog.Default()
	}
	return &Commander{catalog: catalog, local: local, peers: peers, log: log}
}

// InspectPath 在目录所属机器上检查既有路径。
func (c *Commander) InspectPath(ctx context.Context, command controlplane.InspectPathCommand) (controlplane.PathInspection, error) {
	started := time.Now()
	machine, remote, err := c.resolve(ctx, command.MachineID)
	if err != nil {
		return controlplane.PathInspection{}, err
	}
	var inspection controlplane.PathInspection
	if remote == nil {
		inspection, err = c.local.InspectPath(ctx, command.Path)
	} else {
		inspection, err = remote.InspectPath(ctx, command)
	}
	if err != nil {
		c.log.Error("项目目录检查失败", "operation_id", command.OperationID, "target_id", command.TargetID,
			"machine_id", machine.ID, "owner", ownerName(remote), "cause", err)
		return controlplane.PathInspection{}, err
	}
	c.log.Info("项目目录检查完成", "operation_id", command.OperationID, "target_id", command.TargetID,
		"machine_id", machine.ID, "owner", ownerName(remote), "is_repo", inspection.IsRepo,
		"elapsed_ms", time.Since(started).Milliseconds())
	return inspection, nil
}

// Clone 在目录所属机器上执行 Git clone。
func (c *Commander) Clone(ctx context.Context, command controlplane.CloneLocationCommand) (controlplane.PathInspection, error) {
	started := time.Now()
	machine, remote, err := c.resolve(ctx, command.MachineID)
	if err != nil {
		return controlplane.PathInspection{}, err
	}
	var inspection controlplane.PathInspection
	if remote == nil {
		inspection, err = c.local.Clone(ctx, machineauthority.CloneCommand{
			GitURL: command.GitURL, ClonePath: command.ClonePath,
		})
	} else {
		inspection, err = remote.Clone(ctx, command)
	}
	if err != nil {
		c.log.Error("项目仓库 clone 失败", "operation_id", command.OperationID, "target_id", command.TargetID,
			"machine_id", machine.ID, "owner", ownerName(remote), "cause", err)
		return controlplane.PathInspection{}, err
	}
	c.log.Info("项目仓库 clone 完成", "operation_id", command.OperationID, "target_id", command.TargetID,
		"machine_id", machine.ID, "owner", ownerName(remote), "is_repo", inspection.IsRepo,
		"elapsed_ms", time.Since(started).Milliseconds())
	return inspection, nil
}

func (c *Commander) resolve(ctx context.Context, machineID string) (controlplane.Machine, controlplane.MachineCommander, error) {
	machine, err := c.catalog.GetMachine(ctx, machineID)
	if err != nil {
		return controlplane.Machine{}, nil, fmt.Errorf("读取目标机器 %s: %w", machineID, err)
	}
	if machine.Status != controlplane.MachineStatusConnected {
		return controlplane.Machine{}, nil, fmt.Errorf("开发机 %s 当前不可用（status=%s）", machineID, machine.Status)
	}
	switch machine.Kind {
	case controlplane.MachineKindLocal:
		if c.local == nil {
			return controlplane.Machine{}, nil, fmt.Errorf("本机项目命令执行者未就绪")
		}
		return machine, nil, nil
	case controlplane.MachineKindRemote:
		if machine.Capabilities[peer.CapabilityProjectCommands] < 1 {
			return controlplane.Machine{}, nil, fmt.Errorf("开发机 %s 不支持项目目录命令，请升级 agentd", machineID)
		}
		if c.peers == nil {
			return controlplane.Machine{}, nil, fmt.Errorf("远端项目命令路由未就绪")
		}
		remote, err := c.peers.CommanderForMachine(ctx, machineID)
		if err != nil {
			return controlplane.Machine{}, nil, fmt.Errorf("解析远端开发机 %s: %w", machineID, err)
		}
		return machine, remote, nil
	default:
		return controlplane.Machine{}, nil, fmt.Errorf("机器 %s 类型不支持项目命令: %s", machineID, machine.Kind)
	}
}

func ownerName(remote controlplane.MachineCommander) string {
	if remote == nil {
		return "local"
	}
	return "peer"
}

var _ controlplane.MachineCommander = (*Commander)(nil)
