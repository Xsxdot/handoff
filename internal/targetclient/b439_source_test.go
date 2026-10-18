// B439 WU2 F5：池的数据源缝——有源走设备表快照，无源回落活配置 targets:。
package targetclient

import (
	"log/slog"
	"reflect"
	"sync"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
)

// fakeSource 是内存设备表快照源，供池数据源缝测试。
type fakeSource struct {
	mu      sync.Mutex
	targets map[string]config.Target
}

func newFakeSource(targets map[string]config.Target) *fakeSource {
	return &fakeSource{targets: targets}
}

func (f *fakeSource) Names() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.targets))
	for n := range f.targets {
		out = append(out, n)
	}
	return out
}

func (f *fakeSource) Target(name string) (config.Target, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.targets[name]
	return t, ok
}

func (f *fakeSource) set(name string, t config.Target) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.targets[name] = t
}

func (f *fakeSource) del(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.targets, name)
}

// TestB439PoolSourceOverridesConfig：注入源后 Names/For 走源，不再看活配置。
func TestB439PoolSourceOverridesConfig(t *testing.T) {
	// 活配置里放一个「幽灵」名字，源里放另一个；池必须只认源。
	p := NewPool(confOf(map[string]config.Target{
		"ghost": {Addr: "10.9.9.9:7777", Token: "g"},
	}), slog.Default())
	defer p.Close()
	src := newFakeSource(map[string]config.Target{
		"linux-01": {Addr: "10.0.0.1:7777", Token: "t"},
	})
	p.SetSource(src)

	if got := p.Names(); !reflect.DeepEqual(got, []string{"linux-01"}) {
		t.Fatalf("Names 应来自源，实得 %v", got)
	}
	if _, err := p.For("linux-01"); err != nil {
		t.Fatalf("源中已登记机器 For 应成功: %v", err)
	}
	if _, err := p.For("ghost"); err == nil {
		t.Fatal("活配置里的名字在注入源后不该被认")
	}
	if _, err := p.For("unregistered"); err == nil {
		t.Fatal("源中未登记的名字必须被拒")
	}
}

// TestB439PoolSourceRebuildsOnChange：源里连接方式变了 → 重建 client（实例判等语义不变）。
func TestB439PoolSourceRebuildsOnChange(t *testing.T) {
	p := NewPool(confOf(nil), slog.Default())
	defer p.Close()
	src := newFakeSource(map[string]config.Target{"linux-01": {Addr: "10.0.0.1:7777", Token: "old"}})
	p.SetSource(src)

	a, err := p.For("linux-01")
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	src.set("linux-01", config.Target{Addr: "10.0.0.1:7777", Token: "new"})
	b, err := p.For("linux-01")
	if err != nil {
		t.Fatalf("改源后 For: %v", err)
	}
	if a == b {
		t.Fatal("源里连接方式变更后必须重建 client")
	}
	// 未变则复用同一实例（订阅不撕的前提）。
	c, _ := p.For("linux-01")
	if b != c {
		t.Fatal("源未变时必须复用同一 client 实例")
	}
}

// TestB439PoolSourceNilFallsBack：SetSource(nil) 回落活配置（S5）。
func TestB439PoolSourceNilFallsBack(t *testing.T) {
	p := NewPool(confOf(map[string]config.Target{"mac-02": {Addr: "10.0.0.2:7777", Token: "t"}}), slog.Default())
	defer p.Close()
	src := newFakeSource(map[string]config.Target{"linux-01": {Addr: "10.0.0.1:7777", Token: "t"}})
	p.SetSource(src)
	p.SetSource(nil)
	if got := p.Names(); !reflect.DeepEqual(got, []string{"mac-02"}) {
		t.Fatalf("置 nil 后应回落活配置，实得 %v", got)
	}
}

// TestB439PoolTargetDoesNotBuildClient：Target 只读连接方式，不构造 client（探活显示用）。
func TestB439PoolTargetDoesNotBuildClient(t *testing.T) {
	p := NewPool(confOf(nil), slog.Default())
	defer p.Close()
	p.SetSource(newFakeSource(map[string]config.Target{"linux-01": {Addr: "10.0.0.1:7777", Token: "t"}}))
	tgt, ok := p.Target("linux-01")
	if !ok || tgt.Addr != "10.0.0.1:7777" {
		t.Fatalf("Target = %+v ok=%v", tgt, ok)
	}
	if n := p.size(); n != 0 {
		t.Fatalf("Target 不该构造 client，池内应有 0 条，实得 %d", n)
	}
}
