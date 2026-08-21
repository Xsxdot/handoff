// 判据⑩（双协调机单镜像者）的真 PG 形态。SQLite 的同名测试跑在单连接
// 串行库上，验不到「两台协调机各自持连接并发抢同一行」这件事——而 lease
// 存在的全部意义就是防这个：两个 agentd 同时镜像同一批 task，事件会双写。
// 默认 skip，设 LEDGER_TEST_PG_DSN 后启用（清理只删本测试自己的行）。
package ledger

import (
	"sync"
	"testing"
	"time"
)

func TestPGMirrorLeaseExclusive(t *testing.T) {
	s := newPGStore(t)
	// 每个协调机各自开一条独立连接——同进程共用 Store 会被 mutate 的
	// 进程内串行掩盖掉真正要验的跨进程竞争
	other := newPGStore(t)
	t.Cleanup(func() {
		_, _ = s.db.Exec(`DELETE FROM mirror_lease WHERE id = 1`)
	})
	if _, err := s.db.Exec(`DELETE FROM mirror_lease WHERE id = 1`); err != nil {
		t.Fatalf("清场: %v", err)
	}

	const ttl = 30 * time.Second
	// 20 轮并发抢占，每轮必须恰有一个赢家
	for round := 0; round < 20; round++ {
		var wg sync.WaitGroup
		results := make([]bool, 2)
		errs := make([]error, 2)
		stores := []*Store{s, other}
		holders := []string{"coord-A", "coord-B"}
		start := make(chan struct{})
		for i := range stores {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				results[i], errs[i] = stores[i].AcquireMirrorLease(holders[i], ttl)
			}(i)
		}
		close(start)
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("第 %d 轮 %s 抢 lease 报错: %v", round, holders[i], err)
			}
		}
		// 第一轮：恰一人拿到。后续轮：持有者续约成功、另一方被挡
		if results[0] && results[1] {
			t.Fatalf("第 %d 轮两台协调机同时持有 lease——镜像会双写", round)
		}
		if !results[0] && !results[1] {
			t.Fatalf("第 %d 轮无人持有 lease——镜像停摆", round)
		}
	}

	// 过期抢占：TTL 归零后另一方必须能接管（持有者进程死掉不能锁死镜像）
	if _, err := s.AcquireMirrorLease("coord-A", time.Nanosecond); err != nil {
		t.Fatalf("短 TTL: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	took, err := other.AcquireMirrorLease("coord-B", ttl)
	if err != nil {
		t.Fatalf("过期接管: %v", err)
	}
	if !took {
		t.Fatal("lease 过期后另一台协调机应能接管")
	}
	// 接管后原持有者不得再续上（防止死而复生的老进程继续写）
	if again, err := s.AcquireMirrorLease("coord-A", ttl); err != nil || again {
		t.Fatalf("被接管的一方不应再拿到 lease: got=%v err=%v", again, err)
	}
}
