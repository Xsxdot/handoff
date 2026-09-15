// B374 分页游标金样本（契约冻结清单 F8）：encodeRoomCursor 的 wire 编码是
// base64url_nopad(json{a,r})，跨实现一致性靠本测试锁定，不靠「逐字节一致」声明。
//
// 本文件只测编解码这一处可执行冻结；页裁剪与 legacy 分支归 implement 轮。
package collab

import (
	"errors"
	"testing"
	"time"
)

func TestEncodeRoomCursorGolden(t *testing.T) {
	cases := []struct {
		at     time.Time
		roomID string
		want   string
	}{
		{time.Unix(0, 1700000000000000000).UTC(), "B42", "eyJhIjoxNzAwMDAwMDAwMDAwMDAwMDAwLCJyIjoiQjQyIn0"},
		{time.Unix(0, 0).UTC(), "global", "eyJhIjowLCJyIjoiZ2xvYmFsIn0"},
		{time.Unix(0, 1234567890000000000).UTC(), "project:handoff", "eyJhIjoxMjM0NTY3ODkwMDAwMDAwMDAwLCJyIjoicHJvamVjdDpoYW5kb2ZmIn0"},
	}
	for _, c := range cases {
		if got := encodeRoomCursor(c.at, c.roomID); got != c.want {
			t.Fatalf("游标金样本漂移: (%s,%q) got %q want %q", c.at, c.roomID, got, c.want)
		}
	}
}

func TestDecodeRoomCursorRoundTrip(t *testing.T) {
	at := time.Unix(0, 1700000000000000000).UTC()
	raw := encodeRoomCursor(at, "B42")
	gotAt, gotID, err := decodeRoomCursor(raw)
	if err != nil {
		t.Fatalf("解码: %v", err)
	}
	if !gotAt.Equal(at) || gotID != "B42" {
		t.Fatalf("往返不一致: got (%s,%q) want (%s,%q)", gotAt, gotID, at, "B42")
	}
}

func TestDecodeRoomCursorEmptyIsFirstPage(t *testing.T) {
	at, id, err := decodeRoomCursor("")
	if err != nil {
		t.Fatalf("空游标应无错: %v", err)
	}
	if !at.IsZero() || id != "" {
		t.Fatalf("空游标应为零值首页: got (%s,%q)", at, id)
	}
}

func TestDecodeRoomCursorRejectsMalformed(t *testing.T) {
	for _, raw := range []string{"!!!not-base64!!!", "eyJhIjox", "bm90LWpzb24"} {
		if _, _, err := decodeRoomCursor(raw); err == nil {
			t.Fatalf("非法游标 %q 必须报错（gateway 据此映射 400）", raw)
		}
	}
}

// TestDecodeRoomCursorValidatesKeys 锁住 F4 的缺键/类型不符分支：json.Unmarshal
// 直接进 roomCursor 会把 `{}` 静默解成零值（首条时间 + 空 ID），那是合法游标与
// 「非法 base64」之间的黑洞。本测试断言 a/r 任一缺失或类型不符都必须报错，且错误
// 可被 errors.Is(err, ErrInvalidCursor) 识别（gateway 的 400 分支据此判定）。
func TestDecodeRoomCursorValidatesKeys(t *testing.T) {
	bad := []string{
		"e30",                                // {}        缺 a 与 r
		"eyJyIjoiQjQyIn0",                    // {"r":"B42"} 缺 a
		"eyJhIjoxNzAwMDAwMDAwMDAwMDAwMDAwfQ", // {"a":1.7e18} 缺 r
		"eyJhIjoieCIsInIiOiJCNDIifQ",         // {"a":"x","r":"B42"} a 类型不符
		"eyJhIjoxLCJyIjo5fQ",                 // {"a":1,"r":9} r 类型不符
	}
	for _, raw := range bad {
		_, _, err := decodeRoomCursor(raw)
		if err == nil {
			t.Fatalf("缺键/类型不符的游标 %q 必须报错，不得当合法游标", raw)
		}
		if !errors.Is(err, ErrInvalidCursor) {
			t.Fatalf("游标 %q 的错误必须裹 ErrInvalidCursor（gateway 映射 400），实得 %v", raw, err)
		}
	}
}

// TestListRoomsPageCursorErrorIsIdentifiable 锁住分页入口对非法游标的可识别拒绝：
// ListRoomsPage 的错误必须能被 errors.Is(err, ErrInvalidCursor) 认出——这正是
// agentd 把「游标非法 → 400」与「列表组装失败 → 500」分开的唯一依据（契约 F4）。
// 序列化用 errors.Is 而非字符串比较，防止未来换文案时静默失配。
func TestListRoomsPageCursorErrorIsIdentifiable(t *testing.T) {
	_, err := New(&fakeLC{}).ListRoomsPage("", "", "!!!not-base64!!!", 50)
	if err == nil {
		t.Fatal("非法游标必须从 ListRoomsPage 透传出错")
	}
	if !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("ListRoomsPage 非法游标错误必须裹 ErrInvalidCursor，实得 %v", err)
	}
}

func TestDecodeRoomCursorAcceptsValidKeys(t *testing.T) {
	// 正控：合法键集（含 a=0 与空 r 是合法值，不等于缺键）必须解码成功。
	raw := encodeRoomCursor(time.Unix(0, 0).UTC(), "")
	_, id, err := decodeRoomCursor(raw)
	if err != nil {
		t.Fatalf("合法键集 %q 不应报错: %v", raw, err)
	}
	if id != "" {
		t.Fatalf("空 r 是合法房间 ID（global 类群房间不用），got %q", id)
	}
}
