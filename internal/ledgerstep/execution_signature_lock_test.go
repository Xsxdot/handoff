package ledgerstep

// B233.16 T1 编译期签名锁：卡步编排消费点的依赖面钉在能力接口上。把
// StepRunner.Clients 字段类型改回 func(string) (*client.Client, error)，或把
// clientFinalMessage 的参数改回聚合，本文件编译失败。

import (
	"context"
	"testing"
)

var (
	_ func(string) (StepClient, error)                                  = (&StepRunner{}).Clients
	_ func(context.Context, finalMessageClient, string) (string, error) = clientFinalMessage
)

func TestExecutionSignatureLocks(t *testing.T) {}
