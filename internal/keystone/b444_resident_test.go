// b444_resident_test.go —— B444 P1 驻场者键的 launch/resume 与驻场者简报（plan §5 A1.8、缺陷族 R1）。
//
// 缝：Service.WakeResident / LaunchForResident（生产入口）送进 Runner 的 ref/spec 与 prompt。
// 判据：① 驻场 ref 原样交给 Runner.Resume，且驻场 ref 表与卡 ref 表互不串（Forget 卡不清驻场、
// 同名键不互盖）；② 简报含 `--agent <小队>` 发言命令与 `card coordinate` 指路，零卡且标题为
// 「新会话」时含 CAS 改名指令，有卡或已命名时不含；任何情形都不含卡席位句 `--cli/--session`。
package keystone

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/proto"
)

func b444Ref() keysclient.SessionRef {
	return keysclient.SessionRef{CLI: "opencode", SessionID: "ses_r1", HomeDir: "/h", Workdir: "/w", Model: "m"}
}

func TestB444WakeResidentResumesGivenRef(t *testing.T) {
	rec := &specRecorder{}
	svc := New(rec, nil, stubLedgerView{}, nil)
	brief := ResidentBrief{Session: "session:7", Agent: "coord", Title: "已命名", Cards: []string{"B1"}}
	got, err := svc.WakeResident(context.Background(), b444Ref(), brief,
		[]WakeEvent{{Kind: WakeMessage, Summary: "人说了话"}})
	if err != nil {
		t.Fatalf("WakeResident: %v", err)
	}
	if !got.Woke || got.SessionID != "ses_r1" {
		t.Fatalf("回执应 woke 且会话为 ses_r1，得 %+v", got)
	}
	if len(rec.resumes) != 1 || rec.resumes[0].SessionID != "ses_r1" || rec.resumes[0].HomeDir != "/h" ||
		rec.resumes[0].Workdir != "/w" || rec.resumes[0].Model != "m" || rec.resumes[0].CLI != "opencode" {
		t.Fatalf("Runner.Resume 应恰一次且 ref 原样，得 %+v", rec.resumes)
	}
	if len(rec.launches) != 0 {
		t.Fatalf("驻场续轮不得 Launch，得 %d", len(rec.launches))
	}
	// R1：驻场键与卡键分表——同名字符串的 Forget(卡) 不清驻场 ref。
	svc.Forget("session:7")
	svc.mu.Lock()
	_, kept := svc.residents["session:7"]
	_, leaked := svc.sessions["session:7"]
	svc.mu.Unlock()
	if !kept || leaked {
		t.Fatalf("驻场 ref 应在驻场表、不进卡表：kept=%v leaked=%v", kept, leaked)
	}
}

// 续轮失败一律保留会话不重建（驻场行重写不在 keystone 职责内）。
func TestB444WakeResidentFailureKeepsSessionNoLaunch(t *testing.T) {
	rec := &failingResumeRecorder{err: keysclient.ErrSessionNotFound}
	svc := New(rec, nil, stubLedgerView{}, nil)
	_, err := svc.WakeResident(context.Background(), b444Ref(), ResidentBrief{Session: "session:7", Agent: "coord"},
		[]WakeEvent{{Kind: WakeMessage, Summary: "x"}})
	if !errors.Is(err, keysclient.ErrSessionNotFound) {
		t.Fatalf("应透传 resume 错误（%%w），得 %v", err)
	}
	if rec.launches != 0 {
		t.Fatalf("驻场续轮失败不得重建 Launch，得 %d", rec.launches)
	}
}

func TestB444LaunchForResidentConsumesSpec(t *testing.T) {
	rec := &specRecorder{}
	svc := New(rec, nil, stubLedgerView{}, nil)
	spec := keysclient.SessionSpec{CLI: "opencode", HomeDir: "/h", Model: "m", Workdir: "/w"}
	got, err := svc.LaunchForResident(context.Background(), spec, ResidentBrief{Session: "session:8", Agent: "coord", Title: proto.SessionUntitledTitle})
	if err != nil {
		t.Fatalf("LaunchForResident: %v", err)
	}
	if len(rec.launches) != 1 || rec.launches[0].CLI != "opencode" || rec.launches[0].HomeDir != "/h" ||
		rec.launches[0].Workdir != "/w" || rec.launches[0].Model != "m" {
		t.Fatalf("Launch 应原样消费 spec，得 %+v", rec.launches)
	}
	if got.SessionID != "sess-new" {
		t.Fatalf("回执会话应为 Runner 生成的 sess-new，得 %+v", got)
	}
	svc.mu.Lock()
	ref := svc.residents["session:8"]
	svc.mu.Unlock()
	if ref.SessionID != "sess-new" || ref.HomeDir != "/h" {
		t.Fatalf("launch 后驻场 ref 应记新会话，得 %+v", ref)
	}
}

// A1.8 简报内容。
func TestB444ResidentBriefing(t *testing.T) {
	cases := []struct {
		name       string
		brief      ResidentBrief
		wantRename bool
	}{
		{"零卡未命名", ResidentBrief{Session: "session:9", Agent: "coord", Title: proto.SessionUntitledTitle}, true},
		{"零卡已命名", ResidentBrief{Session: "session:9", Agent: "coord", Title: "排查登录"}, false},
		{"有卡未命名", ResidentBrief{Session: "session:9", Agent: "coord", Title: proto.SessionUntitledTitle, Cards: []string{"B7"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, mode := range []string{"resume", "launch"} {
				rec := &b435PromptRecorder{}
				svc := New(rec, nil, stubLedgerView{}, nil)
				var err error
				if mode == "resume" {
					_, err = svc.WakeResident(context.Background(), b444Ref(), tc.brief, []WakeEvent{{Kind: WakeMessage, Summary: "人话"}})
				} else {
					_, err = svc.LaunchForResident(context.Background(), keysclient.SessionSpec{CLI: "opencode"}, tc.brief)
				}
				if err != nil || len(rec.prompts) != 1 {
					t.Fatalf("%s: err=%v prompts=%d", mode, err, len(rec.prompts))
				}
				p := rec.prompts[0]
				for _, want := range []string{
					"handoff session send --agent coord session:9 <正文>",
					"handoff card coordinate <卡号>",
					// 协调者裁决 9（R3 嵌套名额暂不改）：叫协调者报无名额 / 409 时在会话里说明，不循环重试。
					residentBriefingCoordinateNoSlot,
					briefingRoomTone,
				} {
					if !strings.Contains(p, want) {
						t.Fatalf("%s 简报缺 %q:\n%s", mode, want, p)
					}
				}
				rename := "handoff session rename session:9 --agent coord --expect-title " + proto.SessionUntitledTitle
				if got := strings.Contains(p, rename); got != tc.wantRename {
					t.Fatalf("%s 简报 CAS 改名指令出现=%v，应 %v:\n%s", mode, got, tc.wantRename, p)
				}
				for _, banned := range []string{"--cli", "--session", "HANDOFF_SESSION_"} {
					if strings.Contains(p, banned) {
						t.Fatalf("%s 驻场简报不得含卡席位句 %q:\n%s", mode, banned, p)
					}
				}
				for _, c := range tc.brief.Cards {
					if !strings.Contains(p, c) {
						t.Fatalf("%s 简报应列出会话里的卡 %s:\n%s", mode, c, p)
					}
				}
			}
		})
	}
}

type failingResumeRecorder struct {
	err      error
	launches int
}

func (r *failingResumeRecorder) Launch(keysclient.SessionSpec, string) (keysclient.TurnResult, error) {
	r.launches++
	return keysclient.TurnResult{SessionID: "should-not"}, nil
}

func (r *failingResumeRecorder) Resume(keysclient.SessionRef, string) (keysclient.TurnResult, error) {
	return keysclient.TurnResult{}, r.err
}
