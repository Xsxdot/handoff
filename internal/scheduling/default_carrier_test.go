package scheduling_test

// B233.5 T1 接缝测试：默认载体 singleton 只保存已登记载体名。

import (
	"errors"
	"testing"

	"github.com/Xsxdot/handoff/internal/schedclient"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

func TestDefaultCarrierRoundtrip(t *testing.T) {
	svc, facade := newEmptySvc(t)
	_, err := svc.DefaultCarrier()
	if !errors.Is(err, scheduling.ErrNoDefault) {
		t.Fatalf("无记录必须 ErrNoDefault，实得 %v", err)
	}
	if err := svc.SetDefaultCarrier(""); !errors.Is(err, scheduling.ErrNoDefault) {
		t.Fatalf("空名写入必须 ErrNoDefault，实得 %v", err)
	}
	putOnlineCarrier(t, svc, scheduling.Carrier{Name: "muse", Machine: "local", CLI: "opencode", Credential: scheduling.CredentialStandalone})
	if err := svc.SetDefaultCarrier("muse"); err != nil {
		t.Fatalf("SetDefaultCarrier: %v", err)
	}
	got, err := svc.DefaultCarrier()
	if err != nil || got != "muse" {
		t.Fatalf("DefaultCarrier=%q err=%v，want muse", got, err)
	}
	rec, err := facade.Get(scheduling.KindDefaultCarrier, scheduling.DefaultCarrierID)
	if err != nil {
		t.Fatalf("读 singleton: %v", err)
	}
	name, err := scheduling.DecodeDefaultCarrier(rec.Body)
	if err != nil || name != "muse" {
		t.Fatalf("body 必须走 EncodeDefaultCarrier 形状，decode=%q err=%v", name, err)
	}
}

func TestSetDefaultCarrierRejectsSquadOnlyName(t *testing.T) {
	svc, _ := newEmptySvc(t)
	if err := svc.PutSquad(scheduling.Squad{Name: "rd", Role: scheduling.RoleExecutor}, 0); err != nil {
		t.Fatal(err)
	}
	err := svc.SetDefaultCarrier("rd")
	if err == nil || !(errors.Is(err, scheduling.ErrNoDefault) || errors.Is(err, scheduling.ErrNotFound) || errors.Is(err, schedclient.ErrNotFound)) {
		t.Fatalf("默认名不得是小队，实得 %v", err)
	}
}
