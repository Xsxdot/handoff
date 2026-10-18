// b435_session_resident_test.go —— B435 R：房间驻场者（session sit/stand、session wait <session>）。
//
// 夹具：真 SQLite 账本 + 真 collab 匹配函数（roomServiceFor，CLI 组装点）+ 真 cobra 入口
// （runLedgerCLI）。会话与卡沿用 card wait 房间命中段夹具 newB435WaitEnv。
package cmd

import (
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
)

// TestB435ResidentDefaultRecipientViaCLIAssembly 是段 0b 前提：库里有驻场者行时，经 CLI 组装点
// roomServiceFor 拿到的匹配函数把「人类无 @」判给驻场者地址、不判给同会话根卡席位（K0 判定表
// 第 5 行先于第 6 行，C-219/C-220）。行由测试直写——证明 R 只需写面与 wait 改接。
func TestB435ResidentDefaultRecipientViaCLIAssembly(t *testing.T) {
	e := newB435WaitEnv(t)
	root := e.card(t)
	seat := e.pull(t, root, "rootpull")
	e.exec(t, `INSERT INTO session_residents (session, identity, sat_at) VALUES (?, ?, ?)`,
		e.session, "user:r", "2026-10-18T00:00:00Z")
	svc := roomServiceFor(e.st)
	msg := proto.RoomMessage{Room: e.session, Kind: proto.RoomMsgUser, Body: "大家好"}
	resident := proto.DeliveryAddress{Kind: proto.DeliveryResident, Session: e.session, Identity: "user:r"}
	hit, err := svc.Matches(msg, "user:x", resident)
	if err != nil || !hit {
		t.Fatalf("人类无 @ 应命中驻场者地址: hit=%v err=%v", hit, err)
	}
	rootAddr := proto.DeliveryAddress{Kind: proto.DeliverySeat, Session: e.session, Card: root, Identity: seat}
	hit, err = svc.Matches(msg, "user:x", rootAddr)
	if err != nil || hit {
		t.Fatalf("有驻场者时根卡席位不应命中: hit=%v err=%v", hit, err)
	}
}
