// 运行锁：一张卡一轮节点编排的占用记录（运行尺度），与 cards.driver_session
// 承载的归属锁（人尺度）分立。权威在账本：取得、续租、释放、过期抢占全部
// 经 mutate 事务收口在本文件；agentd 的进程内在飞集合只是本进程快速去重。
//
// Ticket 0（B239 契约轮）：本文件只冻结签名、类型与表结构，方法体直返零值，
// 无任何可观测行为；行为语义逐条见 docs/superpowers/specs/b239-contract.md §3，
// 由实现轮补齐并配缝 1 测试。表结构随 ensureSchema 落地（store.go）。
package ledger

import "time"

// RunLock 运行锁一行。回答四件事：哪张卡、哪个节点、谁持有、租期到几点。
// Holder 是承载编排的那次运行（运行标识），不是人尺度归属身份；归属仍在
// cards.driver_session，两者互不代写。
type RunLock struct {
	CardID     string    `json:"card_id"`
	Node       string    `json:"node"`
	Holder     string    `json:"holder"`
	AcquiredAt time.Time `json:"acquired_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// AcquireRunLock 取得运行锁。无行、持有者是自己、或已过期 → 得到锁（过期时
// 覆盖并在卡上落抢占事件）；他主持有且未过期 → 拒绝，acquired=false 且返回
// 现存锁（谁在跑、哪个节点、租期到几点）。过期判定用 Store 可注入时钟。
func (s *Store) AcquireRunLock(cardID, node, holder string, ttl time.Duration) (RunLock, bool, error) {
	return RunLock{}, false, nil
}

// RenewRunLock 续租：只有当前持有者可续。返回 false = 已失去（被抢或从未
// 持有），调用方必须停止对这张卡的一切写。
func (s *Store) RenewRunLock(cardID, holder string, ttl time.Duration) (bool, error) {
	return false, nil
}

// ReleaseRunLock 释放运行锁（回合结束的尽力而为清理；非持有者是 no-op，
// 失去锁的权威信号在 RenewRunLock 的 false）。
func (s *Store) ReleaseRunLock(cardID, holder string) error {
	return nil
}

// RunLockOf 读单卡的运行锁行；第二个返回值 = 行是否存在。**不过滤过期**：
// 「是否正在跑」由消费侧按 ExpiresAt 与同一注入时钟判定。
func (s *Store) RunLockOf(cardID string) (RunLock, bool, error) {
	return RunLock{}, false, nil
}

// AllRunLocks 全部运行锁行（看板批量判定用）。同样不过滤过期。
func (s *Store) AllRunLocks() ([]RunLock, error) {
	return nil, nil
}
