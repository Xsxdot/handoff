// approver_wiring.go —— 共享审批链的组装点适配器（B437）。
//
// 职责：
//   - ledgerChainReader：把账本注册表读面适配成 orchestration.ChainReader，
//     把 ledger.ErrNotFound 翻译成「共享链确定不存在」（ErrApproverChainAbsent）
//   - warnLocalApproverSuperseded：启动期一次 WARN——共享链已设且本地 approver 键
//     非空时，提示本地键已被取代、可删除（S6）
//
// 边界：只做读取与翻译，不做校验（body 校验在 orchestration.DecodeApproverChain）；
// 不写账本（写路径在 CLI approver set/clear）。
package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/ledger"
	ledgerapi "github.com/Xsxdot/handoff/internal/ledger/api"
	"github.com/Xsxdot/handoff/internal/orchestration"
)

// ledgerChainReader 是共享审批链的账本读取适配器。
type ledgerChainReader struct {
	facade *ledgerapi.Facade
}

func newLedgerChainReader(facade *ledgerapi.Facade) *ledgerChainReader {
	return &ledgerChainReader{facade: facade}
}

// Current 读当前共享审批链。
//
// 不存在（ErrNotFound）→ ErrApproverChainAbsent（权威「无链」结论，发起方据此
// 清 last-good 回落本地链）；其余错误（含 body 解码失败）原样上抛（发起方持 last-good）。
func (r *ledgerChainReader) Current(ctx context.Context) (orchestration.ApproverChain, error) {
	if r.facade == nil {
		return orchestration.ApproverChain{}, orchestration.ErrApproverChainAbsent
	}
	entry, err := r.facade.Get(orchestration.ApproverChainKind, orchestration.ApproverChainID)
	if err != nil {
		if errors.Is(err, ledger.ErrNotFound) {
			return orchestration.ApproverChain{}, orchestration.ErrApproverChainAbsent
		}
		return orchestration.ApproverChain{}, fmt.Errorf("读共享审批链: %w", err)
	}
	return orchestration.DecodeApproverChain(entry.Body)
}

// warnLocalApproverSuperseded 启动期检查：共享链可读且本地 approver 键非空时打一次 WARN。
//
// 读失败（含无链、超时）静默——启动期不因一次账本抖动把「本地键被取代」误报成事实。
func warnLocalApproverSuperseded(reader *ledgerChainReader, cfg config.ApproverConfig, log *slog.Logger) {
	if len(cfg.Executor) == 0 {
		return
	}
	if _, err := orchestration.ReadChainWithTimeout(context.Background(), reader); err != nil {
		return
	}
	log.Warn("本地审批链键已被共享链取代，可删除",
		"keys", "approver.executor/model/models/timeout",
		"note", "裁决以共享链为准；本地键不再生效")
}
