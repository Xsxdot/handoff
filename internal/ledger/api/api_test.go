// 缝级断言：client.LedgerClient（spec B156.2 测试接缝清单②）的实现侧行为。
// 入口一律经接口类型变量，不直引 Facade 具体类型（编译期 implements 背书
// 在 api.go:27）。卡与合并用 ledger 公开 API 造：PutWorkflow/CreateCard/
// MergeCards 均为导出方法，无需包内夹具。
package api

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/collab/client"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

func newFacadeStore(t *testing.T) (*ledger.Store, client.LedgerClient) {
	t.Helper()
	st, err := ledger.Open(filepath.Join(t.TempDir(), "facade.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st, New(st)
}

func mustBugWorkflow(t *testing.T, st *ledger.Store) {
	t.Helper()
	if _, err := st.PutWorkflow("bug", ledger.WorkflowDef{States: []string{"待办", "进行中", "已完成"}}); err != nil {
		t.Fatal(err)
	}
}

// 旧协作门面仍保留编译契约，但不得成为新席位的写入口；租约镜像仍可用。
func TestFacadeLegacyBindDriverDisabledAndLeaseThroughClientSeam(t *testing.T) {
	st, lc := newFacadeStore(t)
	mustBugWorkflow(t, st)
	card, err := st.CreateCard(ledger.NewCard{Title: "缝级", Project: "p", Workflow: "bug", Actor: "t"})
	if err != nil {
		t.Fatal(err)
	}

	if err := lc.BindDriver(card.ID, "sess-a", "car-a", ""); !errors.Is(err, ledger.ErrBadState) {
		t.Fatalf("旧门面写入口应停用: %v", err)
	}
	got, err := lc.GetCard(card.ID)
	if err != nil || got.DriverSession != "" || got.DriverSource != "" {
		t.Fatalf("旧门面不得写入新席位: %v %+v", err, got)
	}

	if _, ok, _ := lc.DriverLease("sess-a"); ok {
		t.Fatal("未续租前应不存在")
	}
	if ok, err := st.RenewDriverLease("sess-a", time.Minute); err != nil || !ok {
		t.Fatalf("续租（Store 层，非接口面）: %v %v", ok, err)
	}
	exp, ok, err := lc.DriverLease("sess-a")
	if err != nil || !ok || exp.IsZero() || !exp.After(time.Now().Add(30*time.Second)) {
		t.Fatalf("DriverLease 应返回真实过期时刻: %v ok=%v err=%v", exp, ok, err)
	}
}

// §11.4 一④ C1 半：「following」在席出键正形断言＋GetCard 恒缺席
// （单卡读不派生跟随态）。这是手写投影 cardWire 的序列化边界回归锁。
func TestFollowingProjectionKeyPresence(t *testing.T) {
	st, lc := newFacadeStore(t)
	mustBugWorkflow(t, st)
	carrier, err := st.CreateCard(ledger.NewCard{Title: "承载", Project: "p", Workflow: "bug", Actor: "t"})
	if err != nil {
		t.Fatal(err)
	}
	member, err := st.CreateCard(ledger.NewCard{Title: "成员", Project: "p", Workflow: "bug", Actor: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.MergeCards([]string{member.ID}, carrier.ID, "t"); err != nil {
		t.Fatal(err)
	}

	all, err := lc.ListAllCards("")
	if err != nil {
		t.Fatal(err)
	}
	var memberWire *proto.Card
	for i := range all {
		if all[i].ID == member.ID {
			memberWire = &all[i]
		}
	}
	if memberWire == nil || memberWire.Following != carrier.ID {
		t.Fatalf("列表投影应带 Following=%q: %+v", carrier.ID, memberWire)
	}
	raw, err := json.Marshal(memberWire)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"following":"`+carrier.ID+`"`) {
		t.Fatalf("在席必须出键: %s", raw)
	}

	single, err := lc.GetCard(member.ID)
	if err != nil {
		t.Fatal(err)
	}
	if single.Following != "" {
		t.Fatalf("GetCard 不派生跟随态（§11.4）: %q", single.Following)
	}
	rawSingle, err := json.Marshal(single)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rawSingle), `"following"`) {
		t.Fatalf("缺席不得出键（omitempty）: %s", rawSingle)
	}
}
