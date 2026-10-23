package accounts_test

// b467_ignore_test.go —— B467 段 A 账号域：忽略标记（plan §4 / L11–L14、Q1/Q8、§6 步骤 1）。
//
// 职责：锁「忽略」是账号级、与账号定义同 id 的独立 registry 行——账号定义行、登录行、读数行
// 一概不动；被载体引用不能忽略；忽略/恢复幂等；发现（新登录、登录消失、补 label）、改名、
// 删除后重建都不把忽略改回来；坏忽略行按「已忽略、at 未知」处理。
// 边界：真实 *accounts.Service + 内存 registry；HTTP 面与探测器在 agentd 侧测试承接。

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/schedclient"
	"github.com/Xsxdot/handoff/internal/scheduling/accounts"
)

func ignoreRow(t *testing.T, mem *memRegistry, id string) (accounts.IgnoreMark, bool) {
	t.Helper()
	rec, err := mem.Get(accounts.KindIgnore, id)
	if errors.Is(err, schedclient.ErrNotFound) {
		return accounts.IgnoreMark{}, false
	}
	if err != nil {
		t.Fatalf("读忽略行: %v", err)
	}
	var m accounts.IgnoreMark
	if err := json.Unmarshal(rec.Body, &m); err != nil {
		t.Fatalf("解码忽略行: %v", err)
	}
	return m, true
}

func TestIgnoreMarkLifecycleAndDiscoveryNeverUndoesIt(t *testing.T) {
	mem := newMem()
	svc := accounts.New(mem)
	seedAccount(t, svc)
	if err := svc.RecordProbe(ogID, successAt(0, 1, 10)); err != nil {
		t.Fatalf("播种读数: %v", err)
	}
	before, err := svc.Get(ogID)
	if err != nil {
		t.Fatal(err)
	}
	if before.Ignored != nil || before.ProbeLogin == "" || before.State != accounts.StateOK {
		t.Fatalf("前置：未忽略、有可探登录、状态正常，得 %+v", before)
	}
	defBefore, err := mem.Get(accounts.KindAccount, ogID)
	if err != nil {
		t.Fatal(err)
	}

	// ① 忽略后读面带 Ignored、ProbeLogin 为空、State 不被改写成 fail；定义行一个字节不变。
	v, err := svc.Ignore(ogID, "linux-01")
	if err != nil {
		t.Fatalf("忽略: %v", err)
	}
	if v.Ignored == nil || v.Ignored.By != "linux-01" || v.Ignored.At.IsZero() {
		t.Fatalf("忽略后读面应带 at/by: %+v", v.Ignored)
	}
	if v.ProbeLogin != "" {
		t.Fatalf("已忽略账号不得有可探登录: %q", v.ProbeLogin)
	}
	if v.State != before.State {
		t.Fatalf("已忽略账号的 State 应保持按读数推导（%v），得 %v", before.State, v.State)
	}
	row, ok := ignoreRow(t, mem, ogID)
	if !ok || !row.At.Equal(v.Ignored.At) || row.By != "linux-01" {
		t.Fatalf("忽略行 body 不符: %+v（存在=%v）", row, ok)
	}
	defAfter, err := mem.Get(accounts.KindAccount, ogID)
	if err != nil {
		t.Fatal(err)
	}
	if defAfter.Version != defBefore.Version || string(defAfter.Body) != string(defBefore.Body) {
		t.Fatalf("忽略不得改账号定义行: before=%s after=%s", defBefore.Body, defAfter.Body)
	}
	list, err := svc.List()
	if err != nil || len(list) != 1 || list[0].Ignored == nil || list[0].ProbeLogin != "" {
		t.Fatalf("List 应带忽略读面: %+v %v", list, err)
	}
	assess, err := svc.Assess(ogID, time.Now())
	if err != nil || !assess.Ignored {
		t.Fatalf("Assess 应报告已忽略: %+v %v", assess, err)
	}

	// ③ 再忽略幂等：at/by 不变。
	again, err := svc.Ignore(ogID, "mac-mini")
	if err != nil {
		t.Fatalf("重复忽略应成功: %v", err)
	}
	if again.Ignored == nil || !again.Ignored.At.Equal(v.Ignored.At) || again.Ignored.By != "linux-01" {
		t.Fatalf("重复忽略不得改 at/by: %+v", again.Ignored)
	}

	// ② 被载体引用的账号不能忽略，忽略行不产生。
	mem2 := newMem()
	svc2 := accounts.New(mem2)
	seedAccount(t, svc2)
	logins, err := svc2.Logins()
	if err != nil || len(logins) != 1 {
		t.Fatalf("播种登录: %+v %v", logins, err)
	}
	svc2.SetReferrers(func() (map[string][]string, error) {
		return map[string][]string{logins[0].ID: {"c2", "c1"}}, nil
	})
	_, err = svc2.Ignore("opencode-go-aaaa", "linux-01")
	var refErr *accounts.ReferencedError
	if !errors.Is(err, accounts.ErrReferenced) || !errors.As(err, &refErr) {
		t.Fatalf("被引用应拒忽略并包 ErrReferenced: %v", err)
	}
	if len(refErr.Carriers) != 2 || refErr.Carriers[0] != "c1" || refErr.Carriers[1] != "c2" {
		t.Fatalf("拒绝体应点名载体（排序）: %+v", refErr)
	}
	if !strings.Contains(err.Error(), "不能忽略") {
		t.Fatalf("拒绝文案应说「不能忽略」而不是「删除」: %v", err)
	}
	if _, ok := ignoreRow(t, mem2, ogID); ok {
		t.Fatal("被拒时不得产生忽略行")
	}

	// ④ 恢复清标记、可探登录回来；再恢复幂等。
	restored, err := svc.Restore(ogID)
	if err != nil {
		t.Fatalf("恢复: %v", err)
	}
	if restored.Ignored != nil || restored.ProbeLogin == "" {
		t.Fatalf("恢复后应无 Ignored 且可探登录回来: %+v", restored)
	}
	if _, ok := ignoreRow(t, mem, ogID); ok {
		t.Fatal("恢复应删掉忽略行")
	}
	if _, err := svc.Restore(ogID); err != nil {
		t.Fatalf("重复恢复应成功: %v", err)
	}

	// ⑤ 忽略后跑发现：新登录、登录消失、补 label 都不改忽略，登录状态照常变。
	if _, err := svc.Ignore(ogID, "linux-01"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Reconcile(accounts.Discovery{Machine: "linux-01", At: time.Now(), Homes: []accounts.ScannedHome{
		{Home: "~", HomeAbs: "/home/u", Logins: []accounts.FoundLogin{
			{Kind: accounts.KindOpencodeGo, Source: "opencode.db", Fingerprint: "sha256:aaaa11112222"}}},
		{Home: "~/.handoff/home/x", HomeAbs: "/home/u/.handoff/home/x", Logins: []accounts.FoundLogin{
			{Kind: accounts.KindOpencodeGo, Source: "opencode.db", Fingerprint: "sha256:aaaa11112222"}}},
	}}); err != nil {
		t.Fatalf("发现（新登录）: %v", err)
	}
	v, err = svc.Get(ogID)
	if err != nil || v.Ignored == nil || len(v.Logins) != 2 || v.ProbeLogin != "" {
		t.Fatalf("发现新登录后仍应已忽略且无从可探登录: %+v %v", v, err)
	}
	if _, err := svc.Reconcile(accounts.Discovery{Machine: "linux-01", At: time.Now(), Homes: []accounts.ScannedHome{
		{Home: "~/.handoff/home/x", HomeAbs: "/home/u/.handoff/home/x", Logins: []accounts.FoundLogin{
			{Kind: accounts.KindOpencodeGo, Source: "opencode.db", Fingerprint: "sha256:aaaa11112222", Label: "who@example.com"}}},
	}}); err != nil {
		t.Fatalf("发现（登录消失 + 补 label）: %v", err)
	}
	v, err = svc.Get(ogID)
	if err != nil || v.Ignored == nil {
		t.Fatalf("发现后仍应已忽略: %+v %v", v, err)
	}
	if v.Account.Label != "who@example.com" {
		t.Fatalf("补 label 应照常生效: %+v", v.Account)
	}
	var states []string
	for _, lv := range v.Logins {
		states = append(states, string(lv.Login.State))
	}
	if len(states) != 2 || !strings.Contains(states[0]+","+states[1], "gone") {
		t.Fatalf("登录状态应随发现变化（一条 gone）: %v", states)
	}

	// ⑥ 改名后仍已忽略。
	a := v.Account
	a.Name = "og-renamed"
	if _, err := svc.Put(a, v.Version); err != nil {
		t.Fatalf("改名: %v", err)
	}
	v, err = svc.Get(ogID)
	if err != nil || v.Ignored == nil || v.Account.Name != "og-renamed" {
		t.Fatalf("改名后仍应已忽略: %+v %v", v, err)
	}

	// ⑦ 删除后由发现重建 → 仍已忽略（Q8：删除不连带删忽略行）。
	if err := svc.Delete(ogID, v.Version); err != nil {
		t.Fatalf("删除: %v", err)
	}
	if _, ok := ignoreRow(t, mem, ogID); !ok {
		t.Fatal("删除账号不得连带删忽略行")
	}
	if _, err := svc.Reconcile(accounts.Discovery{Machine: "linux-01", At: time.Now(), Homes: []accounts.ScannedHome{
		{Home: "~", HomeAbs: "/home/u", Logins: []accounts.FoundLogin{
			{Kind: accounts.KindOpencodeGo, Source: "opencode.db", Fingerprint: "sha256:aaaa11112222"}}},
	}}); err != nil {
		t.Fatalf("重建发现: %v", err)
	}
	v, err = svc.Get(ogID)
	if err != nil || v.Ignored == nil {
		t.Fatalf("删除后重建的账号应仍是已忽略: %+v %v", v, err)
	}

	// ⑧ 坏的忽略行按「已忽略、at 未知」，不让坏行悄悄恢复账号。
	mem3 := newMem()
	svc3 := accounts.New(mem3)
	seedAccount(t, svc3)
	if _, err := mem3.Put(accounts.KindIgnore, ogID, 0, []byte("{bad json"), "test"); err != nil {
		t.Fatal(err)
	}
	v, err = svc3.Get(ogID)
	if err != nil || v.Ignored == nil || !v.Ignored.At.IsZero() || v.ProbeLogin != "" {
		t.Fatalf("坏忽略行应按已忽略、at 未知: %+v %v", v, err)
	}
}
