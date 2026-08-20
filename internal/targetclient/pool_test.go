// 本文件验证 Pool 的复用、配置失效、删除清理与活快照枚举。
//
// 边界：使用直连伪配置验证池状态，不探测真实机器；relay 拨号由 Warm/relay 测试覆盖。
package targetclient

import (
	"errors"
	"log/slog"
	"reflect"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
)

func confOf(targets map[string]config.Target) func() *config.Config {
	c := &config.Config{Targets: targets}
	return func() *config.Config { return c }
}

// TestPoolReusesClient：同名两次 For 拿到同一个实例。
//
// why 这条最要紧：不复用等于每轮探活都新建一条 relay 隧道（WSS + CONNECT + E2E
// 握手），30s 一轮的循环会把对端和 relay 一起打爆。
func TestPoolReusesClient(t *testing.T) {
	p := NewPool(confOf(map[string]config.Target{
		"mac-02": {Addr: "10.0.0.2:7777", Token: "tok"},
	}), slog.Default())
	defer p.Close()

	a, err := p.For("mac-02")
	if err != nil {
		t.Fatalf("For 失败: %v", err)
	}
	b, err := p.For("mac-02")
	if err != nil {
		t.Fatalf("第二次 For 失败: %v", err)
	}
	if a != b {
		t.Fatal("同名两次 For 必须复用同一实例")
	}
}

// TestPoolRebuildsOnTargetChange：target 配置变了就重建。
//
// why 用整体比较而不是逐字段比：逐字段比会在 relay 加字段时漏掉新字段，
// 而漏掉的表现是「改了配置不生效」——最难查的那一类。
func TestPoolRebuildsOnTargetChange(t *testing.T) {
	targets := map[string]config.Target{"mac-02": {Addr: "10.0.0.2:7777", Token: "old"}}
	p := NewPool(confOf(targets), slog.Default())
	defer p.Close()

	a, _ := p.For("mac-02")
	targets["mac-02"] = config.Target{Addr: "10.0.0.2:7777", Token: "new"}
	b, err := p.For("mac-02")
	if err != nil {
		t.Fatalf("改配置后 For 失败: %v", err)
	}
	if a == b {
		t.Fatal("target 配置变更后必须重建 client")
	}
}

// TestPoolUnknownName：配置里没有的名字 → 报错，不造 client。
func TestPoolUnknownName(t *testing.T) {
	p := NewPool(confOf(map[string]config.Target{}), slog.Default())
	defer p.Close()
	if _, err := p.For("ghost"); err == nil {
		t.Fatal("未登记的机器不该造出 client")
	}
}

// TestPoolNoEndpointPropagates：无端点的 target 把 ErrNoEndpoint 透出去。
func TestPoolNoEndpointPropagates(t *testing.T) {
	p := NewPool(confOf(map[string]config.Target{"broken": {Token: "tok"}}), slog.Default())
	defer p.Close()
	if _, err := p.For("broken"); !errors.Is(err, ErrNoEndpoint) {
		t.Fatalf("要 ErrNoEndpoint，实得 %v", err)
	}
}

// TestPoolNamesFollowsLiveConfig：Names 跟随活快照，新增的机器立刻可见。
//
// why：Mirror 过去拿的是启动时的静态 cfg，控制台运行期加的机器要重启才被镜像。
// 这条锁死「加了就看得见」。
func TestPoolNamesFollowsLiveConfig(t *testing.T) {
	targets := map[string]config.Target{"mac-02": {Addr: "10.0.0.2:7777", Token: "t"}}
	p := NewPool(confOf(targets), slog.Default())
	defer p.Close()

	if got := p.Names(); !reflect.DeepEqual(got, []string{"mac-02"}) {
		t.Fatalf("Names = %v", got)
	}
	targets["linux-01"] = config.Target{Addr: "10.0.0.3:7777", Token: "t"}
	if got := p.Names(); !reflect.DeepEqual(got, []string{"linux-01", "mac-02"}) {
		t.Fatalf("Names 要跟随活快照且排序，实得 %v", got)
	}
}

// TestPoolDropsRemovedTarget：target 被删 → 从池里移出。
func TestPoolDropsRemovedTarget(t *testing.T) {
	targets := map[string]config.Target{"mac-02": {Addr: "10.0.0.2:7777", Token: "t"}}
	p := NewPool(confOf(targets), slog.Default())
	defer p.Close()

	if _, err := p.For("mac-02"); err != nil {
		t.Fatalf("For 失败: %v", err)
	}
	delete(targets, "mac-02")
	if _, err := p.For("mac-02"); err == nil {
		t.Fatal("已删除的机器不该还能拿到 client")
	}
	if n := p.size(); n != 0 {
		t.Fatalf("已删除的机器要从池里移出，实得 %d 条", n)
	}
}
