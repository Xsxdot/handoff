// 本文件是 B233.20 非执行面消费收窄后 gateway 侧的注入适配器：把
// *targetclient.Pool 适配成 ledgermirror.Machines（账本事件镜像的机器源）。
//
// 边界：
//   - 只做类型适配，不承载域逻辑；For 透传池的规范实例，不造副本——
//     账本镜像按实例判等识别「机器配置已变更」（B163 ②），在适配层包
//     MarkForwarded 之类的副本会让订阅每轮退订重订
//   - 一跳封顶标记不在这里包：镜像的事件源方法本身就是带标记的缝
//     （client.ForwardedStreamEventsOnce），订阅时现做副本
package agentd

import (
	"context"
	"errors"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/ledgermirror"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/targetclient"
)

// LedgerMirrorMachines 把 *targetclient.Pool 适配成 ledgermirror.Machines，
// 并单独提供本机任务清单；本机自名不伪装成池内远端机器。
type LedgerMirrorMachines struct {
	pool       *targetclient.Pool
	localTasks interface{ ListTasks() ([]proto.Task, error) }
}

// NewLedgerMirrorMachines 返回账本事件镜像的机器源适配器。
//
// 生产必须与任务镜像共用同一个池实例：两个池等于两套 relay 隧道，
// relay 侧会看到重复的节点连接。
func NewLedgerMirrorMachines(pool *targetclient.Pool, localTasks interface{ ListTasks() ([]proto.Task, error) }) LedgerMirrorMachines {
	return LedgerMirrorMachines{pool: pool, localTasks: localTasks}
}

// Names 透传池的活机器清单（判据只有一处：池）。
func (m LedgerMirrorMachines) Names() []string { return m.pool.Names() }

// For 透传池的规范客户端实例（收窄为事件流订阅缝类型）。
func (m LedgerMirrorMachines) For(name string) (client.EventStreamClient, error) {
	return m.pool.For(name)
}

// Tasks reads the public all-task inventory used to recover unlinked task streams.
// The caller bounds and cancels this request as part of mirror reconciliation.
func (m LedgerMirrorMachines) Tasks(ctx context.Context, name string) ([]proto.TaskView, error) {
	c, err := m.pool.For(name)
	if err != nil {
		return nil, err
	}
	return c.ListTasks(ctx)
}

// LocalTasks exposes the host's persisted public task inventory independently
// of the remote pool; the local machine is deliberately absent from Names().
func (m LedgerMirrorMachines) LocalTasks(ctx context.Context) ([]proto.TaskView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m.localTasks == nil {
		return nil, errors.New("local task store unavailable")
	}
	tasks, err := m.localTasks.ListTasks()
	if err != nil {
		return nil, err
	}
	views := make([]proto.TaskView, 0, len(tasks))
	for _, task := range tasks {
		views = append(views, proto.TaskView{Task: task})
	}
	return views, nil
}

// 编译期锚：适配器必须始终满足 ledgermirror 侧机器源接口。
var _ ledgermirror.Machines = LedgerMirrorMachines{}
