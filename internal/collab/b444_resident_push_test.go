// b444_resident_push_test.go —— B444 P1（S5）SitResident 的 push 形状校验（plan §5 A1.7）。
//
// 判据：push 行必须带 push_cli、push_session、carrier 且身份为 agent:；desktop 行不得带任何
// push/bearing 字段；驱动形态词表外的值一律拒。拒绝 = ErrResidentIdentity 且零写入（fakeLC.sitCalls 为空）。
// 每个拒绝配一个同形状通过的对照，防「全拒」假绿。
package collab

import (
	"errors"
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
)

func b444PushRow() proto.SessionResident {
	return proto.SessionResident{
		Session: "session:1", Identity: "agent:coord", DriverMode: proto.ResidentDriverPush,
		PushCLI: "opencode", PushSession: "ses_1", Carrier: "coord-carrier",
		CarrierMachine: "linux-01", HomeDir: "/h", Workdir: "/w", Model: "m",
	}
}

func TestB444SitResidentPushShape(t *testing.T) {
	cases := []struct {
		name string
		edit func(*proto.SessionResident)
		ok   bool
	}{
		{"完整 push 行", func(*proto.SessionResident) {}, true},
		{"push 行可无机器/home/workdir/model（本机缺省）", func(r *proto.SessionResident) {
			r.CarrierMachine, r.HomeDir, r.Workdir, r.Model = "", "", "", ""
		}, true},
		{"push 行身份为 user:", func(r *proto.SessionResident) { r.Identity = "user:alice" }, false},
		{"push 行缺 push_session", func(r *proto.SessionResident) { r.PushSession = "" }, false},
		{"push 行缺 push_cli", func(r *proto.SessionResident) { r.PushCLI = "" }, false},
		{"push 行缺 carrier", func(r *proto.SessionResident) { r.Carrier = "" }, false},
		{"push 行带桌面端 cli_session", func(r *proto.SessionResident) { r.CLISession = "x" }, false},
		{"push 行带桌面端 desktop_session", func(r *proto.SessionResident) { r.DesktopSession = "local_x" }, false},
		{"驱动形态非法", func(r *proto.SessionResident) { r.DriverMode = "pull" }, false},
		{"desktop 行带 push_session", func(r *proto.SessionResident) {
			*r = proto.SessionResident{Session: "session:1", Identity: "agent:org", PushSession: "ses_1"}
		}, false},
		{"desktop 行带 carrier", func(r *proto.SessionResident) {
			*r = proto.SessionResident{Session: "session:1", Identity: "agent:org", Carrier: "c"}
		}, false},
		{"desktop 行带 model", func(r *proto.SessionResident) {
			*r = proto.SessionResident{Session: "session:1", Identity: "agent:org", Model: "m"}
		}, false},
		{"desktop 行（桌面端地址）", func(r *proto.SessionResident) {
			*r = proto.SessionResident{Session: "session:1", Identity: "agent:org", DesktopSession: "local_x", CLISession: "c"}
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := b444PushRow()
			tc.edit(&r)
			f := &fakeLC{}
			_, err := New(f).SitResident(r)
			if tc.ok {
				if err != nil || len(f.sitCalls) != 1 {
					t.Fatalf("应通过且恰一次写调用: err=%v calls=%+v", err, f.sitCalls)
				}
				if f.sitCalls[0] != r {
					t.Fatalf("写调用应原样透传 push 字段:\n得 %+v\n应 %+v", f.sitCalls[0], r)
				}
				return
			}
			if !errors.Is(err, ErrResidentIdentity) {
				t.Fatalf("应 ErrResidentIdentity，得 %v", err)
			}
			if len(f.sitCalls) != 0 {
				t.Fatalf("被拒仍写了账本: %+v", f.sitCalls)
			}
		})
	}
}
