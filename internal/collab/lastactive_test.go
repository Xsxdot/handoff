// B385 缝级测试：会话范围「最后活跃」账本推导（缝 1）与旧传输脸读侧归一（缝 2）。
// 纯函数外形——不碰 Service、不碰租约；行为红线来自 spec 读数语义（冻结）。
package collab

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/collab/room"
	"github.com/Xsxdot/handoff/internal/proto"
)

func roomEv(actor, roomID string, at time.Time) proto.LedgerEvent {
	raw, _ := json.Marshal(proto.RoomMessage{Room: roomID, Kind: proto.RoomMsgUser, Body: "hi"})
	return proto.LedgerEvent{Type: room.RoomEventType, Actor: actor, Payload: raw, CreatedAt: at}
}

func cardEv(actor, cardID string, at time.Time) proto.LedgerEvent {
	return proto.LedgerEvent{CardID: cardID, Type: "comment", Actor: actor, Payload: []byte(`{}`), CreatedAt: at}
}

// TestDeriveMemberLastActive 缝 1：会话房间消息 ∪ 本会话卡事件；范围外不计；
// 同一身份同刻/多刻取最大；系统 actor（agentd/mirror/system:*）不算任何人；
// 无活动身份零值穿透（不出现在结果表）。
func TestDeriveMemberLastActive(t *testing.T) {
	base := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	later := base.Add(5 * time.Minute)
	latest := later.Add(time.Minute)

	session := proto.Session{
		ID:      "session:1",
		Members: []string{"user:sy", "user:alice", "user:sycm", "agentd"},
		Cards:   []string{"B1", "B2"},
	}
	byCard := map[string]proto.Card{
		"B1": {ID: "B1", DriverSession: "cli:opencode#s1"},
		"B2": {ID: "B2"}, // 空座
		"B9": {ID: "B9", DriverSession: "cli:codex#outside"},
	}

	events := []proto.LedgerEvent{
		// 会话房间：user:sy 两刻，取最大 later。
		roomEv("user:sy", session.ID, base),
		roomEv("user:sy", session.ID, later),
		// 旧传输脸归一命中 user:sycm（本地段精确相等）→ latest。
		roomEv("cli:sycm@mac.local", session.ID, latest),
		// 范围外：别的会话房间。
		roomEv("user:alice", "session:other", later),
		// 范围外：不在本会话的卡。
		cardEv("user:sy", "B9", latest),
		// 本会话卡事件：席位精确命中。
		cardEv("cli:opencode#s1", "B1", base),
		cardEv("cli:opencode#s1", "B1", later), // 同身份取最大
		// 系统组件：不算任何人活动。
		cardEv("agentd", "B1", latest),
		cardEv("mirror", "B1", latest),
		cardEv("system:pointer", "B1", latest),
		roomEv("agentd", session.ID, latest),
		// 本地段无对应成员：不归一、不命中。
		roomEv("cli:bob@host", session.ID, latest),
	}

	got := deriveMemberLastActive(events, session, byCard)

	if !got["user:sy"].Equal(later) {
		t.Fatalf("user:sy 应取会话内最大时刻 %v，实得 %v", later, got["user:sy"])
	}
	if !got["user:sycm"].Equal(latest) {
		t.Fatalf("旧脸 cli:sycm@mac.local 应归一命中 user:sycm=%v，实得 %v", latest, got["user:sycm"])
	}
	if !got["cli:opencode#s1"].Equal(later) {
		t.Fatalf("席位应取卡事件最大时刻 %v，实得 %v", later, got["cli:opencode#s1"])
	}
	if at, ok := got["user:alice"]; ok && !at.IsZero() {
		t.Fatalf("user:alice 范围外活动不得计入，实得 %v", at)
	}
	if at, ok := got["user:bob"]; ok {
		t.Fatalf("未命中成员的 actor 不得建键: %v", at)
	}
	if _, ok := got["cli:codex#outside"]; ok {
		t.Fatalf("会话外席位不得建键: %v", got["cli:codex#outside"])
	}
	// 系统 actor 不得污染任何成员读数：user:sy / 席位的最大值不是 latest。
	if got["user:sy"].Equal(latest) {
		t.Fatalf("agentd/system 事件不得抬高 user:sy 读数: %v", got["user:sy"])
	}
	if got["cli:opencode#s1"].Equal(latest) {
		t.Fatalf("系统事件不得抬高席位读数: %v", got["cli:opencode#s1"])
	}
	// 脏成员身份恰为 agentd：系统行仍不得算「任何人」的活动（isSystemActor
	// 承重断言——否则 targets 过滤会把它当成该成员的读数）。
	if at, ok := got["agentd"]; ok && !at.IsZero() {
		t.Fatalf("系统组件事件不得记入 agentd 成员读数: %v", at)
	}
}

// TestDeriveMemberLastActiveZeroPassthrough 无任何范围内活动 → 空表；
// 查不到的身份回零值（前端渲染「未记录」的输入）。
func TestDeriveMemberLastActiveZeroPassthrough(t *testing.T) {
	session := proto.Session{ID: "session:1", Members: []string{"user:sy"}, Cards: []string{"B1"}}
	byCard := map[string]proto.Card{"B1": {ID: "B1", DriverSession: "cli:opencode#s1"}}
	got := deriveMemberLastActive(nil, session, byCard)
	if len(got) != 0 {
		t.Fatalf("空事件流应得空表: %v", got)
	}
	if !got["user:sy"].IsZero() || !got["cli:opencode#s1"].IsZero() {
		t.Fatal("缺失身份查表应回零值")
	}
}

// TestNormalizeLegacyFace 缝 2 表驱动：cli:/web: 旧脸本地段与 user:<名> 精确
// 相等才归一；席位脸、无 @、空本地段/空主机、无匹配一律原样（宁窄勿宽）。
func TestNormalizeLegacyFace(t *testing.T) {
	users := map[string]bool{"sycm": true, "alice": true}
	cases := []struct {
		name  string
		actor string
		want  string
	}{
		{"cli 命中", "cli:sycm@sycmdeMacBook-Air.local", "user:sycm"},
		{"web 命中", "web:alice@127.0.0.1", "user:alice"},
		{"本地段不等不归一", "cli:other@host", "cli:other@host"},
		{"席位脸精确原样", "cli:opencode#s1", "cli:opencode#s1"},
		{"统一记法原样", "user:sycm", "user:sycm"},
		{"agent 身份原样", "agent:opencode", "agent:opencode"},
		{"无 @ 原样", "cli:sycm", "cli:sycm"},
		{"空本地段原样", "cli:@host", "cli:@host"},
		{"空主机原样", "cli:sycm@", "cli:sycm@"},
		{"系统标识原样", "agentd", "agentd"},
		{"空串原样", "", ""},
	}
	for _, tc := range cases {
		if got := normalizeLegacyFace(tc.actor, users); got != tc.want {
			t.Errorf("%s: normalize(%q)=%q want %q", tc.name, tc.actor, got, tc.want)
		}
	}
}
