// B374 分页游标金样本（契约冻结清单 F8）：encodeRoomCursor 的 wire 编码是
// base64url_nopad(json{a,r})，跨实现一致性靠本测试锁定，不靠「逐字节一致」声明。
//
// 本文件只测编解码这一处可执行冻结；页裁剪与 legacy 分支归 implement 轮。
package collab

import (
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

// TestListRoomsPageRejectsMalformedCursor 锁住分页入口对非法游标的透传拒绝：
// Ticket 0 的 trimRoomPage 是直通镜像，但「非法游标 → error」这一可观测分支
// 已真实生效，必须能被测红，否则 gateway 的 400 路径无守护。
func TestListRoomsPageRejectsMalformedCursor(t *testing.T) {
	if _, err := New(&fakeLC{}).ListRoomsPage("", "", "!!!not-base64!!!", 50); err == nil {
		t.Fatal("非法游标必须从 ListRoomsPage 透传出错")
	}
}
