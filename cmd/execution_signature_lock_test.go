package cmd

// B233.16 T1 编译期签名锁：把每个执行消费点的依赖面钉在能力接口上。把任一
// 参数类型改回聚合 *client.Client（或改写收敛后的具名入口签名），本文件编译失败。
// 这是本卡「有牙」的主形态，不依赖字符串扫描。

import (
	"context"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/discipline"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/spf13/cobra"
)

var (
	_ func(*cobra.Command, client.ExecutionClient, string) error                              = runStop
	_ func(*cobra.Command, client.ExecutionClient, string, string) error                      = runReply
	_ func(*cobra.Command, client.ExecutionClient, string, string) error                      = runContinue
	_ func(context.Context, client.ExecutionClient, client.DispatchOpts) (*proto.Task, error) = dispatchTask

	_ func(context.Context, dispatchClient, string) (discipline.ResolvedDiscipline, error)                        = resolveBareDiscipline
	_ func(context.Context, dispatchClient, *ledger.Store, string, string) (discipline.ResolvedDiscipline, error) = resolveCardDispatchDiscipline
	_ func(context.Context, compensateClient, string, string, string) error                                       = compensateCardDispatch

	_ func(*cobra.Command, string, string, waitClient) error = runUntilDone
	_ func(*cobra.Command, string, string, waitClient) error = runFollow
	_ func(context.Context, waitClient, time.Duration)       = warnIfTimeoutBelowStall
	_ func(*cobra.Command, waitClient, string, *proto.Event) = autoSyncAfterWait
)

func TestExecutionSignatureLocks(t *testing.T) {}
