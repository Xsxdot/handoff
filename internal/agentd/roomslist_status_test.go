// B374 分页列表错误映射（契约冻结清单 F4）：游标非法 → 400，列表组装失败 → 500。
//
// 为什么单独测 roomsListErrorStatus 而不是走 HTTP：Ticket 0 的
// parseRoomsListParams 是空壳，游标不经 query 进入 handler，400 分支在
// GET /api/rooms 上暂不可达；把映射抽成纯函数后，这条可观测语义仍有一支能变红
// 的测试钉住，等 implement 轮把 query 搬进 handler 时不会悄悄改掉映射。
package agentd

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/Xsxdot/handoff/internal/collab"
)

func TestRoomsListErrorStatusMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"裸哨兵", collab.ErrInvalidCursor, http.StatusBadRequest},
		{"裹一层", fmt.Errorf("%w: base64 解码: boom", collab.ErrInvalidCursor), http.StatusBadRequest},
		{"双裹", fmt.Errorf("上游: %w", fmt.Errorf("%w: x", collab.ErrInvalidCursor)), http.StatusBadRequest},
		{"其它错误", errors.New("读卡失败"), http.StatusInternalServerError},
		{"nil", nil, http.StatusInternalServerError},
	}
	for _, c := range cases {
		if got := roomsListErrorStatus(c.err); got != c.want {
			t.Fatalf("%s: roomsListErrorStatus(%v) = %d，want %d", c.name, c.err, got, c.want)
		}
	}
}

// TestRoomsListErrorStatusDistinguishesServiceFailures 反向锚：只有
// ErrInvalidCursor 走 400，房间域其它哨兵（如 ErrNoRoom）与通用错误都必须落 500，
// 防止后人把「照抄 collabErr 的哨兵表」顺手把 errNoRoom 也映射成 400。
func TestRoomsListErrorStatusDistinguishesServiceFailures(t *testing.T) {
	for _, err := range []error{collab.ErrNoRoom, collab.ErrReadOnly, collab.ErrNotWriter} {
		if got := roomsListErrorStatus(err); got != http.StatusInternalServerError {
			t.Fatalf("非游标错误 %v 不能映射为 %d（应 500）", err, got)
		}
	}
}
