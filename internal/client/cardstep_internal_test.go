// cardstep_internal_test.go：CardStep 受理响应读取上限的内部测试。
//
// 为什么必须是内部测试：判据要**照着上限本身**构造边界输入。写死 4096 的话，
// 上限一旦调大，这条用例就变成「一段没超限的响应 + 尾随内容」——它照样报错、
// 照样绿，但验的已经不是超限那条防线了（为错误的理由通过）。
package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
)

// TestCardStepRejectsOversizedAck 钉死「超限的受理响应整体拒绝，而不是截断后当成受理」。
//
// 读上限只截断不报错：一段恰好等于上限的 {"ok":true,...} 后面无论接什么，截断都会
// 把它切掉，于是响应被当成合法受理——「拒尾随内容」那道防线在上限边界上被绕过。
func TestCardStepRejectsOversizedAck(t *testing.T) {
	head := `{"ok":true,"pad":"`
	tail := `"}`
	exact := head + strings.Repeat("x", maxCardStepAck-len(head)-len(tail)) + tail
	if len(exact) != maxCardStepAck {
		t.Fatalf("构造的合法前缀应恰好 %d 字节，实得 %d", maxCardStepAck, len(exact))
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(exact + `{"ok":false}`))
	}))
	defer ts.Close()
	err := New(ts.URL, "tok").
		CardStep(context.Background(), "B1", proto.CardStepReq{Step: "review", Actor: "cli:u@h#1"})
	if err == nil {
		t.Fatal("超限的受理响应应报错")
	}
}

// TestCardStepAckSizeLimitBoundary 从两侧钉死受理响应的大小上限本身。
//
// 分工同 agentd 侧：TestCardStepRejectsOversizedAck 验的是边界上的尾随内容，
// 而多读 1 字节本身就挡得住它；这条验上限本身——恰好等于上限的纯合法响应必须
// 被当成受理，多一个字节的同样合法的响应必须报错。
func TestCardStepAckSizeLimitBoundary(t *testing.T) {
	build := func(total int) string {
		head := `{"ok":true,"pad":"`
		tail := `"}`
		body := head + strings.Repeat("x", total-len(head)-len(tail)) + tail
		if len(body) != total {
			t.Fatalf("构造 %d 字节响应失败，实得 %d", total, len(body))
		}
		return body
	}
	serve := func(payload string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(payload))
		}))
	}
	t.Run("恰好等于上限视为受理", func(t *testing.T) {
		ts := serve(build(maxCardStepAck))
		defer ts.Close()
		if err := New(ts.URL, "tok").CardStep(context.Background(), "B1",
			proto.CardStepReq{Step: "review", Actor: "cli:u@h#1"}); err != nil {
			t.Fatalf("恰好 %d 字节的受理响应应通过，实得 %v", maxCardStepAck, err)
		}
	})
	t.Run("多一个字节即报错", func(t *testing.T) {
		ts := serve(build(maxCardStepAck + 1))
		defer ts.Close()
		if err := New(ts.URL, "tok").CardStep(context.Background(), "B1",
			proto.CardStepReq{Step: "review", Actor: "cli:u@h#1"}); err == nil {
			t.Fatalf("%d 字节的受理响应应报错", maxCardStepAck+1)
		}
	})
}
