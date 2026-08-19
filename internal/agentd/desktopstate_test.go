// 本文件覆盖薄壳状态中转端点的缺席、往返与过期行为。
//
// 职责：锁住控制台读取薄壳状态时的 204/200 契约与 TTL 边界。
// 边界：不测试薄壳如何组装状态；那属于 desktop 模块的上报器。
package agentd

import (
	"net/http"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

func TestDesktopStateAbsentUntilReported(t *testing.T) {
	env := newTestAgentdEnv(t)
	if code := env.getJSON(t, "/api/desktop/state", nil); code != http.StatusNoContent {
		t.Fatalf("没上报过时得到 %d，想要 204——控制台靠 204 判断「没有壳」", code)
	}
}

func TestDesktopStateRoundTrip(t *testing.T) {
	env := newTestAgentdEnv(t)
	want := proto.DesktopState{AppVersion: "v0.3.1", SyncPlan: "blocked", SyncBusy: 2}
	if code := env.putJSON(t, "/api/desktop/state", want, nil); code != http.StatusOK {
		t.Fatalf("上报得到 %d，想要 200", code)
	}
	var got proto.DesktopState
	if code := env.getJSON(t, "/api/desktop/state", &got); code != http.StatusOK {
		t.Fatalf("读取得到 %d，想要 200", code)
	}
	if got != want {
		t.Fatalf("读到 %+v，想要 %+v", got, want)
	}
}

func TestDesktopStateExpiresAfterTTL(t *testing.T) {
	env := newTestAgentdEnv(t)
	if code := env.putJSON(t, "/api/desktop/state",
		proto.DesktopState{AppVersion: "v0.3.1", SyncPlan: "done"}, nil); code != http.StatusOK {
		t.Fatalf("上报失败")
	}
	// 把时钟推过 TTL：壳没在跑就等于没有壳，陈旧状态必须消失，否则纯浏览器
	// 会话会看到一个点了没反应的按钮。
	env.srv.desktopNow = func() time.Time { return time.Now().Add(desktopStateTTL + time.Second) }
	if code := env.getJSON(t, "/api/desktop/state", nil); code != http.StatusNoContent {
		t.Fatalf("过期后得到 %d，想要 204", code)
	}
}
