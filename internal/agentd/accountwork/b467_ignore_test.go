package accountwork

// b467_ignore_test.go —— B467 段 A 探测器：已忽略账号不探（S3）、立即探测被拒（新哨兵）、
// 恢复后同一 scan 立即探到（S4 后半）。
//
// 边界：内存账本 + 假额度适配器；真实 Prober，不读任何真实凭据。

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/quota"
	"github.com/Xsxdot/handoff/internal/scheduling/accounts"
)

type countingQuota struct{ calls atomic.Int32 }

func (c *countingQuota) Probe(context.Context, quota.Request) (quota.Snapshot, error) {
	c.calls.Add(1)
	return quota.Snapshot{Windows: []quota.Window{{Key: "weekly", UsedPercent: 12}}}, nil
}

func TestProberSkipsIgnoredAccountAndRestoreResumes(t *testing.T) {
	acc := accounts.New(newMem())
	if _, err := acc.Reconcile(accounts.Discovery{Machine: "m1", At: time.Now(), Homes: []accounts.ScannedHome{{
		Home: "~", HomeAbs: "/home/u",
		Logins: []accounts.FoundLogin{{Kind: accounts.KindOpencodeGo, Source: "auth.json",
			Fingerprint: "sha256:aaaa11112222"}}}}}); err != nil {
		t.Fatal(err)
	}
	id := accounts.AccountID(accounts.KindOpencodeGo, "", "sha256:aaaa11112222")
	if _, err := acc.Ignore(id, "m1"); err != nil {
		t.Fatalf("忽略: %v", err)
	}
	q := &countingQuota{}
	p := NewProber(ProberConfig{
		Accounts: acc, Quota: q, IsSelf: func(string) bool { return true }, Writer: "m1",
		ExpandHome: func(string) (string, error) { return t.TempDir(), nil },
	})
	p.scanEvery = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)
	defer p.Stop()

	if _, err := p.ProbeNow(context.Background(), id); !errors.Is(err, ErrIgnored) {
		t.Fatalf("已忽略账号立即探测应返回 ErrIgnored，得 %v", err)
	}
	time.Sleep(120 * time.Millisecond)
	if n := q.calls.Load(); n != 0 {
		t.Fatalf("已忽略账号到期也不得调额度接口，得 %d 次", n)
	}
	v, err := acc.Get(id)
	if err != nil || v.Record.LastAttemptAt != nil {
		t.Fatalf("已忽略账号不应产生探测尝试: %+v %v", v.Record, err)
	}

	if _, err := acc.Restore(id); err != nil {
		t.Fatalf("恢复: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		v, err = acc.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if v.Record.Reading != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("恢复后同一/下一轮扫描未在 5s 内探到")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if q.calls.Load() == 0 {
		t.Fatal("恢复后应调用额度接口")
	}
}
