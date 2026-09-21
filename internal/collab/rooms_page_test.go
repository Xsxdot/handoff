// B374 分页列表测试（契约冻结清单 F12–F16）：全部经 Service.ListRoomsPage
// 接缝进入，不直接调 trimRoomPage。夹具一律 Project:"" 且查询 project=""，
// 因为 listRooms 恒在列表尾部追加 global 群房间（LastActivity 零值沉底），
// 精确条数断言用 cardIDs 过滤出 card 房间，避免把群房间算进条数。
package collab

import (
	"fmt"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/collab/room"
	"github.com/Xsxdot/handoff/internal/proto"
)

// pageCards 造 n 张活动降序卡（下标 0 最新），项目留空（不额外产生 project 群房间）。
func pageCards(base time.Time, n int) []proto.Card {
	cards := make([]proto.Card, 0, n)
	for i := 0; i < n; i++ {
		cards = append(cards, proto.Card{
			ID: fmt.Sprintf("B%03d", i), Title: fmt.Sprintf("B%03d", i),
			Status: "进行中", UpdatedAt: base.Add(-time.Duration(i) * time.Second),
		})
	}
	return cards
}

// idsOf 取 page 里全部房间 ID（含群房间）。
func idsOf(rooms []proto.RoomSummary) []string {
	out := make([]string, 0, len(rooms))
	for _, r := range rooms {
		out = append(out, r.ID)
	}
	return out
}

// cardIDs 只取 card 房间 ID：listRooms 恒追加 global（有 project 时还有 project:<p>），
// 它们 LastActivity 为零值，不应参与「按卡裁剪」的条数断言。
func cardIDs(rooms []proto.RoomSummary) []string {
	out := make([]string, 0, len(rooms))
	for _, r := range rooms {
		if r.Kind == room.KindCard {
			out = append(out, r.ID)
		}
	}
	return out
}

// TestListRoomsPagePaginatesFlatOrder 锁 F12/F15：沿扁平序翻完全部页，
// 页内不重复、页间不丢，并集=全量、交集=∅；has_more ⟺ next_cursor 非空。
func TestListRoomsPagePaginatesFlatOrder(t *testing.T) {
	base := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	fake := &fakeLC{cards: pageCards(base, 7), leases: map[string]time.Time{}}
	svc := New(fake)
	full, err := svc.ListRoomsForMember("", "")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	cursor := ""
	for pages := 0; ; pages++ {
		page, err := svc.ListRoomsPage("", "", cursor, 3)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Rooms) > 3 {
			t.Fatalf("单页不得超过 limit=3，实得 %d（直通镜像会红）", len(page.Rooms))
		}
		got = append(got, idsOf(page.Rooms)...)
		if !page.HasMore {
			if page.NextCursor != "" {
				t.Fatalf("has_more=false 时 next_cursor 必须为空: %q", page.NextCursor)
			}
			break
		}
		if page.NextCursor == "" {
			t.Fatal("has_more=true 必须给 next_cursor")
		}
		cursor = page.NextCursor
		if pages > 20 {
			t.Fatal("翻页不收敛")
		}
	}
	seen := map[string]bool{}
	for _, id := range got {
		if seen[id] {
			t.Fatalf("页间重复: %s", id)
		}
		seen[id] = true
	}
	want := map[string]bool{}
	for _, r := range full {
		want[r.ID] = true
	}
	if len(got) != len(want) {
		t.Fatalf("并集=%d 全量=%d", len(got), len(want))
	}
	for id := range want {
		if !seen[id] {
			t.Fatalf("丢条目: %s", id)
		}
	}
}

// TestListRoomsPageLocatesByRoomIDNotIDOrder 锁 F13：主判据是扁平序里 roomID
// 的位置之后起算，不是 ID 序。同刻三房间，游标指向中间 ID B1。
func TestListRoomsPageLocatesByRoomIDNotIDOrder(t *testing.T) {
	base := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	cards := []proto.Card{
		{ID: "B2", Title: "B2", Status: "进行中", UpdatedAt: base},
		{ID: "B1", Title: "B1", Status: "进行中", UpdatedAt: base},
		{ID: "B3", Title: "B3", Status: "进行中", UpdatedAt: base},
	}
	fake := &fakeLC{cards: cards, leases: map[string]time.Time{}}
	page, err := New(fake).ListRoomsPage("", "", encodeRoomCursor(base, "B1"), 10)
	if err != nil {
		t.Fatal(err)
	}
	// 扁平序（同刻 SliceStable 保插入序）= [B2,B1,B3]；主判据=B1 之后=[B3]。
	// ID 序兜底 x.ID>"B1" 会给 [B2,B3] → 红。
	got := cardIDs(page.Rooms)
	if len(got) != 1 || got[0] != "B3" {
		t.Fatalf("主判据必须按 roomID 位置取，实得 %v", got)
	}
}

// TestListRoomsPageFallbackSkipsSameInstant 锁 F14：游标所指房间已不在列表时
// 兜底跳过所有 LastActivity 不早于游标时刻的条目；同刻插入序不可恢复是已知限制。
func TestListRoomsPageFallbackSkipsSameInstant(t *testing.T) {
	base := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	// 房间 A0 不在列表（已消失），游标时刻 = base；两张同刻卡都被跳过。
	fake := &fakeLC{cards: []proto.Card{
		{ID: "B1", Title: "B1", Status: "进行中", UpdatedAt: base},
		{ID: "B2", Title: "B2", Status: "进行中", UpdatedAt: base},
	}, leases: map[string]time.Time{}}
	page, err := New(fake).ListRoomsPage("", "", encodeRoomCursor(base, "A0"), 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := cardIDs(page.Rooms); len(got) != 0 {
		t.Fatalf("兜底必须跳过同刻全部 card: %v", got)
	}
	// 混合：一条更早的卡要留下（证明不是「全空」假绿）。
	fake2 := &fakeLC{cards: []proto.Card{
		{ID: "B1", Title: "B1", Status: "进行中", UpdatedAt: base},
		{ID: "B2", Title: "B2", Status: "进行中", UpdatedAt: base.Add(-time.Second)},
	}, leases: map[string]time.Time{}}
	page2, err := New(fake2).ListRoomsPage("", "", encodeRoomCursor(base, "A0"), 10)
	if err != nil {
		t.Fatal(err)
	}
	// 扁平序 [B1(base), B2(base-1s)]；跳过不早于 base 的 B1，从 B2 起算。
	if got := cardIDs(page2.Rooms); len(got) != 1 || got[0] != "B2" {
		t.Fatalf("兜底应从首个更早条目起算，实得 %v", got)
	}
}

// TestListRoomsPageFallbackFiltersNonMonotonicFlatOrder 锁 F14 兜底在非单调扁平序上
// 按扁平序逐条过滤，而不是从首个更早条目切尾巴：active 段里有一条活动早于游标时刻的
// 房间，其后 sunk 段的终态房间活动晚于游标时刻（降序排序只在段内成立，跨段非单调）。
// 兜底若用「首个 Before(at) 的下标切尾巴」，会把该 sunk 房间错误带回。
func TestListRoomsPageFallbackFiltersNonMonotonicFlatOrder(t *testing.T) {
	base := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	at := base
	// 扁平序 = active[A(at-1s)] + sunk[Z(at+1h)]；游标房间 A0 已不在列表。
	fake := &fakeLC{cards: []proto.Card{
		{ID: "A", Title: "A", Status: "进行中", UpdatedAt: at.Add(-time.Second)},
		{ID: "Z", Title: "Z", Status: "已完成", UpdatedAt: at.Add(time.Hour)},
	}, leases: map[string]time.Time{}}
	page, err := New(fake).ListRoomsPage("", "", encodeRoomCursor(at, "A0"), 10)
	if err != nil {
		t.Fatal(err)
	}
	got := cardIDs(page.Rooms)
	if len(got) != 1 || got[0] != "A" {
		t.Fatalf("兜底必须逐条保留 LastActivity.Before(at) 的条目（不得带回 sunk Z）: %v", got)
	}
}

// TestListRoomsPageCursorRoomPresentVsRemoved 锁主判据/兜底两条路径的切换：
// 房间仍在 → 其位置之后；房间已被移走 → 首个更早时刻起算。
func TestListRoomsPageCursorRoomPresentVsRemoved(t *testing.T) {
	base := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	fake := &fakeLC{cards: pageCards(base, 4), leases: map[string]time.Time{}} // B000..B003
	svc := New(fake)
	first, err := svc.ListRoomsPage("", "", "", 2) // [B000,B001]
	if err != nil {
		t.Fatal(err)
	}
	if got := cardIDs(first.Rooms); len(got) != 2 || got[0] != "B000" || got[1] != "B001" {
		t.Fatalf("首页: %v", got)
	}
	cursor := encodeRoomCursor(first.Rooms[1].LastActivity, first.Rooms[1].ID)
	nxt, err := svc.ListRoomsPage("", "", cursor, 2) // 主判据 → [B002,B003]
	if err != nil {
		t.Fatal(err)
	}
	if got := cardIDs(nxt.Rooms); len(got) != 2 || got[0] != "B002" {
		t.Fatalf("房间仍在时应从其后起算，实得 %v", got)
	}
	// 移走 B001 后重放同一游标 → 兜底：跳过不早于 B001 时刻的 card。
	fake.cards = []proto.Card{fake.cards[0], fake.cards[2], fake.cards[3]}
	after, err := svc.ListRoomsPage("", "", cursor, 2)
	if err != nil {
		t.Fatal(err)
	}
	// 扁平序 [B000(base), B002(base-2s), B003(base-3s), global]；
	// B000 不早于 B001(base-1s) → 跳过；从 B002 起算 → [B002,B003]。
	if got := cardIDs(after.Rooms); len(got) != 2 || got[0] != "B002" {
		t.Fatalf("房间消失后兜底应从首个更早条目起算，实得 %v", got)
	}
}

// TestListRoomsPageKeepsTerminalRoomsReachable 锁 F16：分页不剪枝终态卡房间，
// 且 ReadOnly 标记不变。
func TestListRoomsPageKeepsTerminalRoomsReachable(t *testing.T) {
	base := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	fake := &fakeLC{cards: []proto.Card{
		{ID: "B1", Title: "B1", Status: "进行中", UpdatedAt: base},
		{ID: "B9", Title: "B9", Status: "已完成", UpdatedAt: base.Add(time.Hour)},
	}, leases: map[string]time.Time{}}
	svc := New(fake)
	var seenTerminal bool
	cursor := ""
	for i := 0; i < 5; i++ {
		page, err := svc.ListRoomsPage("", "", cursor, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Rooms) > 1 {
			t.Fatalf("单页不得超过 limit=1，实得 %d（直通镜像会红）", len(page.Rooms))
		}
		for _, r := range page.Rooms {
			if r.ID == "B9" {
				seenTerminal = true
				if !r.ReadOnly {
					t.Fatalf("终态卡 ReadOnly 必须为 true: %+v", r)
				}
			}
		}
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}
	if !seenTerminal {
		t.Fatal("分页不得剪枝终态卡房间")
	}
}

// TestListRoomsPageLimitClamp 锁 limit 收敛（F6/F7）与两侧常量同值。
func TestListRoomsPageLimitClamp(t *testing.T) {
	base := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	fake := &fakeLC{cards: pageCards(base, 250), leases: map[string]time.Time{}}
	svc := New(fake)
	for _, c := range []struct {
		in   int
		want int
	}{{0, 50}, {-1, 50}, {1000, 200}} {
		page, err := svc.ListRoomsPage("", "", "", c.in)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Rooms) != c.want {
			t.Fatalf("limit=%d 应收敛到 %d，实得 %d", c.in, c.want, len(page.Rooms))
		}
	}
	if roomsPageDefaultLimit != 50 || roomsPageMaxLimit != 200 {
		t.Fatalf("limit 常量漂移: %d/%d", roomsPageDefaultLimit, roomsPageMaxLimit)
	}
}
