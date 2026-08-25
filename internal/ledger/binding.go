// 绑定席位与驱动活性租约（B156.2）。权威在账本域卡上（拍板记录 5.1）：
// 绑定 = cards.driver_session + 新列 driver_carrier，CAS 语义与派发即认领
// （ClaimCard）同源；心跳 = 新表 driver_leases 按 session 全局一行，租期
// 模式照抄运行锁但互不代写（runlock.go 文件头边界）。
// Ticket 0 空壳：本文件全部方法无可观测行为；schema 迁移与实现归实现节点。
package ledger

import (
	"time"
)

// DriverLease 驱动活性租约一行：哪个协调者会话、活到几点。
type DriverLease struct {
	Session   string
	ExpiresAt time.Time
}

const (
	// DriverLeaseTTL 驱动租约默认租期（照抄 RunLockTTL 数量级）。
	DriverLeaseTTL = 5 * time.Minute
	// DriverLeaseRenewInterval 生产续租 goroutine 的默认节拍。
	DriverLeaseRenewInterval = 2 * time.Minute
)

// ClaimCardAs 认领归属并登记载体类型。语义与 ClaimCard 完全一致（同事务、
// CAS、终态拒绝、重入幂等），另写 driver_carrier。既有 ClaimCard 保持原
// 签名不变、内部转调本方法传空载体。
// Ticket 0 空壳：无可观测行为。
func (s *Store) ClaimCardAs(id, owner, carrier string) error {
	_ = id
	_ = owner
	_ = carrier
	return nil
}

// RebindDriver 换绑：expect=当前绑定前值（CAS 冲突 ErrCASConflict）；成功
// 覆写 driver_session+driver_carrier 并落 EvDriverTakeover{from,to}。旧会话
// 的房间写权与推进权随当前值被覆写而剥失。
// Ticket 0 空壳：无可观测行为。
func (s *Store) RebindDriver(id, toSession, carrier, expect string) error {
	_ = id
	_ = toSession
	_ = carrier
	_ = expect
	return nil
}

// RenewDriverLease 续租/首建该 session 的租约行（upsert），返回是否生效。
// Ticket 0 空壳：恒返回 true, nil——调用方不得依赖此返回值做活性判断。
func (s *Store) RenewDriverLease(session string, ttl time.Duration) (bool, error) {
	_ = session
	_ = ttl
	return true, nil
}

// DropDriverLease 删除该 session 的租约行（幂等）。
// Ticket 0 空壳：无可观测行为。
func (s *Store) DropDriverLease(session string) error {
	_ = session
	return nil
}

// DriverLeaseOf 读单 session 租约；第二个返回值=行是否存在。
// 不过滤过期：「是否活着」由消费侧按 ExpiresAt 与同一注入时钟判定
// （RunLockOf 同一约定）。
// Ticket 0 空壳：恒返回 exists=false。
func (s *Store) DriverLeaseOf(session string) (DriverLease, bool, error) {
	_ = session
	return DriverLease{}, false, nil
}

// AllDriverLeases 全部租约行（看板批量判活用）。不过滤过期。
// Ticket 0 空壳：恒返回空切片。
func (s *Store) AllDriverLeases() ([]DriverLease, error) {
	return []DriverLease{}, nil
}
