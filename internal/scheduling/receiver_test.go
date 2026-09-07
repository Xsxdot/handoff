package scheduling_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

func TestBindingHomeDirJSONTag(t *testing.T) {
	f, ok := reflect.TypeOf(scheduling.Binding{}).FieldByName("HomeDir")
	if !ok {
		t.Fatal("Binding 必须有 HomeDir")
	}
	if !strings.Contains(string(f.Tag), `json:"home_dir,omitempty"`) {
		t.Fatalf("Binding.HomeDir tag = %s", f.Tag)
	}
}

func TestTaskCarrierJSONKey(t *testing.T) {
	f, ok := reflect.TypeOf(proto.Task{}).FieldByName("Carrier")
	if !ok {
		t.Fatal("proto.Task 必须有 Carrier")
	}
	if !strings.Contains(string(f.Tag), `json:"carrier,omitempty"`) {
		t.Fatalf("Task.Carrier tag = %s", f.Tag)
	}
}

func TestKindAndSentinelLiterals(t *testing.T) {
	if scheduling.ReceiverCarrier != "carrier" {
		t.Fatalf("ReceiverCarrier = %q", scheduling.ReceiverCarrier)
	}
	if scheduling.ReceiverSquad != "squad" {
		t.Fatalf("ReceiverSquad = %q", scheduling.ReceiverSquad)
	}
	if scheduling.KindDefaultCarrier != "default_carrier" {
		t.Fatalf("KindDefaultCarrier = %q", scheduling.KindDefaultCarrier)
	}
	if scheduling.DefaultCarrierID != "current" {
		t.Fatalf("DefaultCarrierID = %q", scheduling.DefaultCarrierID)
	}
	if scheduling.ErrNoDefault.Error() != "scheduling: 没有有效的默认载体" {
		t.Fatalf("ErrNoDefault = %q", scheduling.ErrNoDefault)
	}
	if scheduling.ErrNameConflict.Error() != "scheduling: 名称同时登记为载体和小队" {
		t.Fatalf("ErrNameConflict = %q", scheduling.ErrNameConflict)
	}
	if scheduling.ErrPhysicalOverride.Error() != "scheduling: 禁止覆盖已绑定载体的机器、引擎或 HOME" {
		t.Fatalf("ErrPhysicalOverride = %q", scheduling.ErrPhysicalOverride)
	}
}

func TestEffectiveReceiverName(t *testing.T) {
	name, via, err := scheduling.EffectiveReceiverName("muse", "other")
	if err != nil || name != "muse" || via {
		t.Fatalf("显式名: name=%q via=%v err=%v", name, via, err)
	}
	name, via, err = scheduling.EffectiveReceiverName("  muse  ", "other")
	if err != nil || name != "muse" || via {
		t.Fatalf("显式名去空白: name=%q via=%v err=%v", name, via, err)
	}
	name, via, err = scheduling.EffectiveReceiverName("", "muse")
	if err != nil || name != "muse" || !via {
		t.Fatalf("空名走默认: name=%q via=%v err=%v", name, via, err)
	}
	_, _, err = scheduling.EffectiveReceiverName("", "")
	if !errors.Is(err, scheduling.ErrNoDefault) {
		t.Fatalf("无默认必须 ErrNoDefault，实得 %v", err)
	}
	_, _, err = scheduling.EffectiveReceiverName("  ", "  ")
	if !errors.Is(err, scheduling.ErrNoDefault) {
		t.Fatalf("空白等同空: %v", err)
	}
}

func TestClassifyRegistered(t *testing.T) {
	kind, err := scheduling.ClassifyRegistered("muse", true, false)
	if err != nil || kind != scheduling.ReceiverCarrier {
		t.Fatalf("只载体: kind=%q err=%v", kind, err)
	}
	kind, err = scheduling.ClassifyRegistered("rd", false, true)
	if err != nil || kind != scheduling.ReceiverSquad {
		t.Fatalf("只小队: kind=%q err=%v", kind, err)
	}
	_, err = scheduling.ClassifyRegistered("dup", true, true)
	if !errors.Is(err, scheduling.ErrNameConflict) {
		t.Fatalf("冲突必须 ErrNameConflict，实得 %v", err)
	}
	_, err = scheduling.ClassifyRegistered("missing", false, false)
	if !errors.Is(err, scheduling.ErrNotFound) {
		t.Fatalf("都不存在必须 ErrNotFound，实得 %v", err)
	}
	_, err = scheduling.ClassifyRegistered("", false, false)
	if !errors.Is(err, scheduling.ErrNotFound) {
		t.Fatalf("空名必须 ErrNotFound，实得 %v", err)
	}
}

func TestCheckDefaultKind(t *testing.T) {
	if err := scheduling.CheckDefaultKind(scheduling.ReceiverCarrier); err != nil {
		t.Fatalf("默认载体合法: %v", err)
	}
	if err := scheduling.CheckDefaultKind(scheduling.ReceiverSquad); !errors.Is(err, scheduling.ErrNoDefault) {
		t.Fatalf("默认命中小队必须 ErrNoDefault，实得 %v", err)
	}
}

func TestResolveLookupTable(t *testing.T) {
	reg := map[string][2]bool{
		"muse": {true, false},
		"rd":   {false, true},
		"dup":  {true, true},
	}
	lookup := func(name string) (bool, bool) {
		pair := reg[name]
		return pair[0], pair[1]
	}

	got, err := scheduling.ResolveLookup("muse", "other", lookup)
	if err != nil || got.Kind != scheduling.ReceiverCarrier || got.Name != "muse" || got.ViaDefault {
		t.Fatalf("显式载体: %+v err=%v", got, err)
	}

	got, err = scheduling.ResolveLookup("rd", "", lookup)
	if err != nil || got.Kind != scheduling.ReceiverSquad || got.Name != "rd" || got.ViaDefault {
		t.Fatalf("显式小队: %+v err=%v", got, err)
	}

	_, err = scheduling.ResolveLookup("dup", "", lookup)
	if !errors.Is(err, scheduling.ErrNameConflict) {
		t.Fatalf("显式冲突: %v", err)
	}

	_, err = scheduling.ResolveLookup("ghost", "", lookup)
	if !errors.Is(err, scheduling.ErrNotFound) {
		t.Fatalf("显式不存在: %v", err)
	}

	got, err = scheduling.ResolveLookup("", "muse", lookup)
	if err != nil || got.Kind != scheduling.ReceiverCarrier || got.Name != "muse" || !got.ViaDefault {
		t.Fatalf("默认载体: %+v err=%v", got, err)
	}

	_, err = scheduling.ResolveLookup("", "", lookup)
	if !errors.Is(err, scheduling.ErrNoDefault) {
		t.Fatalf("无默认: %v", err)
	}

	_, err = scheduling.ResolveLookup("", "ghost", lookup)
	if !errors.Is(err, scheduling.ErrNoDefault) {
		t.Fatalf("默认指向不存在: %v", err)
	}

	_, err = scheduling.ResolveLookup("", "rd", lookup)
	if !errors.Is(err, scheduling.ErrNoDefault) {
		t.Fatalf("默认指向小队: %v", err)
	}

	_, err = scheduling.ResolveLookup("", "dup", lookup)
	if !errors.Is(err, scheduling.ErrNameConflict) {
		t.Fatalf("默认名冲突仍是冲突: %v", err)
	}
}

func TestCrossKindConflict(t *testing.T) {
	if err := scheduling.CrossKindConflict(false); err != nil {
		t.Fatalf("对侧不存在应通过: %v", err)
	}
	if err := scheduling.CrossKindConflict(true); !errors.Is(err, scheduling.ErrNameConflict) {
		t.Fatalf("对侧存在必须冲突: %v", err)
	}
}

func TestIdentityOfAndBindPhysical(t *testing.T) {
	c := scheduling.Carrier{Name: "muse", Machine: "linux-01", CLI: "opencode", HomeDir: "~/.handoff/home/muse"}
	id := scheduling.IdentityOf(c)
	if id.Machine != "linux-01" || id.CLI != "opencode" || id.HomeDir != "~/.handoff/home/muse" {
		t.Fatalf("IdentityOf 字段丢失: %+v", id)
	}

	got, err := scheduling.BindPhysical(id, scheduling.PhysicalOverlay{})
	if err != nil || got != id {
		t.Fatalf("空覆盖应保持身份: %+v err=%v", got, err)
	}

	sameHome := "~/.handoff/home/muse"
	got, err = scheduling.BindPhysical(id, scheduling.PhysicalOverlay{
		Machine: "linux-01", CLI: "opencode", HomeDir: &sameHome,
	})
	if err != nil || got != id {
		t.Fatalf("相同值重述应通过: %+v err=%v", got, err)
	}

	_, err = scheduling.BindPhysical(id, scheduling.PhysicalOverlay{Machine: "other"})
	if !errors.Is(err, scheduling.ErrPhysicalOverride) {
		t.Fatalf("换机器必须覆盖禁令: %v", err)
	}
	_, err = scheduling.BindPhysical(id, scheduling.PhysicalOverlay{CLI: "claude"})
	if !errors.Is(err, scheduling.ErrPhysicalOverride) {
		t.Fatalf("换引擎必须覆盖禁令: %v", err)
	}
	otherHome := "/tmp/other"
	_, err = scheduling.BindPhysical(id, scheduling.PhysicalOverlay{HomeDir: &otherHome})
	if !errors.Is(err, scheduling.ErrPhysicalOverride) {
		t.Fatalf("换 HOME 必须覆盖禁令: %v", err)
	}
	empty := ""
	_, err = scheduling.BindPhysical(id, scheduling.PhysicalOverlay{HomeDir: &empty})
	if !errors.Is(err, scheduling.ErrPhysicalOverride) {
		t.Fatalf("显式空 HOME 覆盖非空身份必须拒绝: %v", err)
	}
}

func TestEffectiveModel(t *testing.T) {
	if got := scheduling.EffectiveModel("gpt-x", ""); got != "gpt-x" {
		t.Fatalf("空请求应保留载体模型: %q", got)
	}
	if got := scheduling.EffectiveModel("gpt-x", "gpt-y"); got != "gpt-y" {
		t.Fatalf("请求模型应覆盖: %q", got)
	}
	if got := scheduling.EffectiveModel("gpt-x", "  gpt-y  "); got != "gpt-y" {
		t.Fatalf("请求模型应去空白后覆盖: %q", got)
	}
}

func TestDefaultCarrierRecordGolden(t *testing.T) {
	body, err := scheduling.EncodeDefaultCarrier("muse")
	if err != nil {
		t.Fatal(err)
	}
	want := []byte(`{"name":"muse"}`)
	if string(body) != string(want) {
		t.Fatalf("EncodeDefaultCarrier 字节 = %s, want %s", body, want)
	}
	name, err := scheduling.DecodeDefaultCarrier(body)
	if err != nil || name != "muse" {
		t.Fatalf("DecodeDefaultCarrier = %q err=%v", name, err)
	}

	_, err = scheduling.EncodeDefaultCarrier("")
	if !errors.Is(err, scheduling.ErrNoDefault) {
		t.Fatalf("空名编码必须 ErrNoDefault: %v", err)
	}
	_, err = scheduling.DecodeDefaultCarrier([]byte(`{"name":""}`))
	if !errors.Is(err, scheduling.ErrNoDefault) {
		t.Fatalf("空 name 解码必须 ErrNoDefault: %v", err)
	}
	name, err = scheduling.DecodeDefaultCarrier([]byte(`{"name":"muse","extra":true}`))
	if err != nil || name != "muse" {
		t.Fatalf("未知字段必须忽略（json 缺省）: name=%q err=%v", name, err)
	}

	round, err := json.Marshal(scheduling.DefaultCarrierRecord{Name: "muse"})
	if err != nil {
		t.Fatal(err)
	}
	if string(round) != string(want) {
		t.Fatalf("json.Marshal 结构体 = %s, want %s", round, want)
	}
}
