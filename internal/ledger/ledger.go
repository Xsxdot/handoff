// Package ledger 是账本域的唯一入口：任务卡（Card）、类型化关系边、
// 账本事件单流、工作流聚合与裁决项的持久化与领域操作。
//
// 边界（蓝图 §3.8 模块化单体约束）：
//   - 本包不 import internal/agentd、internal/executor 的任何东西；
//   - 执行域与本包的全部联系 = card_tasks 弱引用表 +（对侧的）opaque
//     card_id 标签，无跨库外键；
//   - 账本库凭据只存在于协调机（config.ledger.dsn），executor 拿不到。
//
// 并发模型：所有写操作经 mutate() 串行化（PG advisory lock / SQLite
// 单写者），换取环检测、B 号分配、CAS 的读-判-写原子性；写 QPS 极小，
// 正确性优先于吞吐。
package ledger

import (
	"errors"
	"log/slog"
)

// 错误哨兵。调用方（CLI/agentd）按哨兵翻译为退出码或 HTTP 状态。
var (
	ErrNotFound    = errors.New("ledger: 记录不存在")
	ErrCASConflict = errors.New("ledger: 状态已被并发修改")         // Move 前值不符
	ErrGateBlocked = errors.New("ledger: workflow gate 拒绝") // 缺附件/缺判据
	ErrCycle       = errors.New("ledger: 阻塞边成环")
	ErrBadMerge    = errors.New("ledger: 合并校验失败") // 跨基线/链式/重复
	ErrBadState    = errors.New("ledger: 当前状态不允许该操作")
)

// log 返回全局 slog——函数而非包变量，令 main 侧后设的
// slog.SetDefault 也能生效（与 internal/store 同一模式）。
func log() *slog.Logger { return slog.Default() }
