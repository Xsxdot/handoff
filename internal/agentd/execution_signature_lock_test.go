package agentd

// B233.16 T1 编译期签名锁：agentd 侧执行消费点的依赖面钉在能力接口上。把任一
// 参数类型改回聚合 *client.Client（或改写具名入口签名），本文件编译失败。

import (
	"context"
	"testing"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/ledgerstep"
	"github.com/Xsxdot/handoff/internal/proto"
)

var (
	_ func(context.Context, client.ExecutionClient, ledgerstep.DispatchOpts, string) (*proto.Task, error) = dispatchStep
	_ func(context.Context, statusClient) (*proto.StatusResp, error)                                      = probeStatus

	_ func(*Server, context.Context, stopReclaimClient, string, string, string) error = (*Server).compensateStep
	_ func(*Server, context.Context, runClient, string, string, string) error         = (*Server).pushWorkBranchVia
	_ func(*Server, context.Context, ledgerstep.DispatchOpts) (string, string, error) = (*Server).stepTransport
)

func TestExecutionSignatureLocks(t *testing.T) {}
