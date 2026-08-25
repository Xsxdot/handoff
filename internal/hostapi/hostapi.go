// Package hostapi 是 d_execution_host（进程承载）的入站薄门面（B156.3
// §7.0）：无头拉起/续接一个 CLI 会话进程、喂 prompt、收回合输出。只暴露承载
// 能力——task 生命周期、waiting_review、verdict 解析等派发状态机概念一律
// 不经此门面；执行域照旧不认识账本/会话/编制域。
//
// 命名说明：包名取 hostapi 而不是 internal/prochost/api，因为代码图扫描按
// 包名生成容器 id，多个域各开 `api` 子包会撞容器。实现走 `opencode run` 无头
// 形态（见 driver.go），同属 d_execution 顶层域，不跨子系统边界；会话身份由
// CLI 自家存储承担，本门面零状态。
package hostapi

import (
	"context"
	"errors"
	"time"
)

// ErrUnavailable 是骨架期的兼容哨兵（契约 §7 冻结导出面，保留不删）。
// RunTurn 实装后正常路径不再返回它：名单外 CLI 得到含 CLI 名的「未实装」
// 错误（岔口一裁决 A），名单内失败得到具体原因。
var ErrUnavailable = errors.New("hostapi: 协调者会话承载尚未接线")

// TurnRequest 描述一回合的无头执行。SessionID 为空 = 新建会话，非空 = 续接；
// Timeout 到点即判回合失败，不无限等待。
type TurnRequest struct {
	CLI       string        // opencode / claude / ...
	HomeDir   string        // 隔离 HOME 档案根（协调者 = 全套）
	Workdir   string        // 进程 cwd
	Model     string        // 空 = CLI 缺省
	Prompt    string        // 本回合输入
	SessionID string        // 空 = 新建
	Env       []string      // 追加环境变量 KEY=VALUE；值不得进日志
	Timeout   time.Duration // 0 = 用包内缺省（30 分钟）
}

// TurnReply 是一回合的产出：生效会话 id 与回合末输出原文。
type TurnReply struct {
	SessionID string
	Output    string
}

// Host 是进程承载门面的服务句柄。
type Host struct{}

// New 构造承载门面。
func New() *Host { return &Host{} }

// RunTurn 执行一回合（实装本体见 driver.go：run 形态驱动 + JSONL 解析 +
// 超时执法）。签名是契约 §7 冻结面，一字不改。
func (h *Host) RunTurn(ctx context.Context, req TurnRequest) (TurnReply, error) {
	return runTurn(ctx, req)
}
