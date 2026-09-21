// authorize.go —— ApprovalClient 的 Request/Await 合成。
//
// 职责：Request 后若 pending 则 Await，避免调用方把 pending 当终态。
// 边界：不进 Adapter；client == nil 不得解释成 allow。
package executor

import (
	"context"
	"fmt"
)

// Authorize 向 client 请求一次授权。Request 返回 pending 时 Await 直到终态。
//
// 参数：client 必须非 nil；req 为原生权限。
// 返回：终态 allow/deny；ctx 取消时返回错误，不泄漏 waiter。
func Authorize(ctx context.Context, client ApprovalClient, req ApprovalRequest) (ApprovalDecision, error) {
	if client == nil {
		return ApprovalDecision{}, fmt.Errorf("ApprovalClient 为空，不能当成免审")
	}
	res, err := client.Request(ctx, req)
	if err != nil {
		return ApprovalDecision{}, err
	}
	if res.Decision.Status == ApprovalPending {
		return client.Await(ctx, res.Ref)
	}
	if res.Decision.Status == ApprovalAllow || res.Decision.Status == ApprovalDeny {
		return res.Decision, nil
	}
	return ApprovalDecision{}, fmt.Errorf("审批 client 返回非法状态 %q", res.Decision.Status)
}
