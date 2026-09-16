// B374 分页列表错误映射（契约冻结清单 F10）：游标非法 → 400，列表组装失败 → 500。
//
// 为什么单独测 roomsListErrorStatus 而不是走 HTTP：该纯函数是「游标非法 400」与
// 「列表组装失败 500」的唯一分界点；纯函数测试把这条映射从 HTTP 装配里解耦，防止
// 后人把「照抄 collabErr 哨兵表」顺手扩大成别的错误也 400。HTTP 侧另有
// TestRoomsListPaginationHTTP 的非法游标 400 / limit=abc 400 端到端钉住。
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
