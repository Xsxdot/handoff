package agentd

// b453_rerun_test.go —— B453 S6 门面侧（V12）：只读任务派发留存原始输入、撞额度标账号、
// 同小队另挑成员重跑（排除链上已用账号/无账号载体按载体名；B464 Q23 起排除集存账号 id）、
// 链接与链终止文案。
//
// 夹具：b452 双 agentd（A 受理、B 执行 + fake 执行器），共享账本；载体全在 B 机。
import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/fake"
	"github.com/Xsxdot/handoff/internal/orchestration"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/scheduling"
	"github.com/Xsxdot/handoff/internal/scheduling/accounts"
)

const b453RerunSquad = "t453-rq"

const b453QuotaFail = "stream error: insufficient_quota: You exceeded your current quota"

// rerunMember 是重跑夹具的小队成员：载体名 + 所选账号（label，空 = 无账号 · 主 HOME）。
type rerunMember struct{ name, account string }

// b453RerunPair 在 b452 双机夹具上接账号域（两台同一账本）、以一轮 B 机发现建出各账号的登录
// （每个账号一个 `~/.handoff/home/<账号>`，label = 账号名）、登记小队成员（全在 B 机、选对应登录）
// 与撞额度回调。返回账号域与「账号名 → 账号 id」。
func b453RerunPair(t *testing.T, steps []fake.Step, members []rerunMember) (*b452Pair, *accounts.Service, map[string]string) {
	t.Helper()
	p := newB452Pair(t, steps)
	acc := accounts.New(p.a.srv.SchedulingRegistry())
	for _, h := range []*b452Host{p.a, p.b} {
		h.srv.SetAccounts(acc)
		h.sched.SetAccounts(acc)
	}
	acc.SetReferrers(p.a.sched.LoginReferrers)
	disc := accounts.Discovery{Machine: b452MachineB, At: time.Now()}
	seen := map[string]bool{}
	for _, m := range members {
		if m.account == "" || seen[m.account] {
			continue
		}
		seen[m.account] = true
		disc.Homes = append(disc.Homes, accounts.ScannedHome{Home: "~/.handoff/home/" + m.account, HomeAbs: "/home/" + m.account,
			Logins: []accounts.FoundLogin{{Kind: accounts.KindCodex, Source: "auth.json",
				Fingerprint: "sha256:" + strings.Repeat(m.account[len(m.account)-1:], 12), Label: m.account}}})
	}
	res, err := acc.Reconcile(disc)
	if err != nil {
		t.Fatalf("发现建账号: %v", err)
	}
	loginOf, ids := map[string]string{}, map[string]string{}
	for _, l := range res.Logins {
		v, err := acc.Get(l.Account)
		if err != nil {
			t.Fatal(err)
		}
		loginOf[v.Account.Name], ids[v.Account.Name] = l.ID, l.Account
	}
	var ms []scheduling.SquadMember
	for _, m := range members {
		c := scheduling.Carrier{Name: m.name, Machine: b452MachineB, CLI: "codex", MaxConcurrency: 2}
		if m.account != "" {
			c.Login = loginOf[m.account]
		} else {
			c.NoAccount = true
		}
		putOnlineCarrier(t, p.a.sched, c)
		ms = append(ms, scheduling.SquadMember{Carrier: c.Name, MaxConcurrency: 2})
	}
	if err := p.a.sched.PutSquad(scheduling.Squad{Name: b453RerunSquad, Role: scheduling.RoleExecutor, Members: ms}, 0); err != nil {
		t.Fatalf("登记小队: %v", err)
	}
	p.b.mgr.SetQuotaHandler(p.b.srv)
	return p, acc, ids
}

func b453ReadonlyViaA(t *testing.T, p *b452Pair) proto.Task {
	t.Helper()
	code, body := b452Post(t, p.a, p.body(`,"receiver":"`+b453RerunSquad+`","mode":"readonly"`), false)
	if code != http.StatusOK {
		t.Fatalf("经 A 只读派发应 200，得 %d %s", code, body)
	}
	return decodeTask(t, body)
}

func readRerunInput(t *testing.T, h *b452Host, taskID string) map[string]json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(h.srv.conf().DataDir, "tasks", taskID, rerunInputFile))
	if err != nil {
		t.Fatalf("读重跑输入: %v", err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("解码重跑输入: %v (%s)", err, raw)
	}
	return obj
}

func b453GetB(t *testing.T, p *b452Pair, id string) *proto.Task {
	t.Helper()
	task, err := p.b.st.GetTask(id)
	if err != nil {
		t.Fatalf("B 读任务 %s: %v", id, err)
	}
	return task
}

// 只读任务（经 A 转发到 B）在 B 留存原始输入：去掉冻结身份四键与落点键，留计划原文与形态。
func TestB453ReadonlyDispatchPersistsRerunInput(t *testing.T) {
	p, _, _ := b453RerunPair(t, nil, []rerunMember{{"rq-1", "acc-1"}, {"rq-2", "acc-2"}})
	task := b453ReadonlyViaA(t, p)
	in := readRerunInput(t, p.b, task.ID)
	for _, k := range []string{"carrier", "squad", "home_dir", "frozen_target", "target", "executor", "receiver"} {
		if _, ok := in[k]; ok {
			t.Fatalf("重跑输入不得留落点/冻结键 %q：%v", k, in)
		}
	}
	if string(in["prompt"]) != `"调查一下"` || string(in["mode"]) != `"readonly"` {
		t.Fatalf("重跑输入应留计划原文与形态：%v", in)
	}
	p.stopOnB(t, task.ID)
}

// MarkTaskQuota：标任务所用账号（任务行存账号 id，B464 Q23）无额度，返回撞到的窗口与预计恢复
// （告知文案用账号名）；未选账号不标。
func TestB453MarkTaskQuotaMarksAccountAndSummarizes(t *testing.T) {
	p, acc, ids := b453RerunPair(t, nil, []rerunMember{{"rq-1", "acc-1"}})
	usedReading(t, acc, "acc-1", 100)
	hit := p.b.srv.MarkTaskQuota(&proto.Task{ID: "t-x", Carrier: "rq-1", Account: ids["acc-1"], AccountKind: accounts.KindCodex},
		orchestration.QuotaFailure{Signature: "insufficient_quota", Text: b453QuotaFail})
	if hit.Account != "acc-1" || len(hit.Windows) != 1 || !strings.Contains(hit.Windows[0], "100%") || hit.RecoverAt == nil {
		t.Fatalf("告知素材不全: %+v", hit)
	}
	a, err := acc.Assess("acc-1", time.Now())
	if err != nil || a.State != accounts.StateOut {
		t.Fatalf("账号应被标无额度，得 %+v %v", a, err)
	}
	if none := p.b.srv.MarkTaskQuota(&proto.Task{ID: "t-y", Carrier: "rq-1"}, orchestration.QuotaFailure{Text: b453QuotaFail}); none.Account != "" {
		t.Fatalf("未选账号不应标记: %+v", none)
	}
}

// V12 门面：重跑挑同小队另一成员（排除原账号），新任务 rerun_of 指回、输入同一份、链上已用账号随行；
// 新任务再重跑时链上账号全被排除 → 拒并点名排除集。
func TestB453RerunReadonlyExcludesChainAccounts(t *testing.T) {
	p, _, ids := b453RerunPair(t, nil, []rerunMember{{"rq-1", "acc-1"}, {"rq-1b", "acc-1"}, {"rq-2", "acc-2"}})
	orig := b453ReadonlyViaA(t, p)
	if orig.Carrier != "rq-1" {
		t.Fatalf("首派应落 rq-1，得 %s", orig.Carrier)
	}
	f := orchestration.QuotaFailure{Signature: "insufficient_quota", Text: b453QuotaFail}
	out, err := p.b.srv.RerunReadonly(context.Background(), b453GetB(t, p, orig.ID), f)
	if err != nil {
		t.Fatalf("重跑应成功: %v", err)
	}
	if out.Carrier != "rq-2" || out.Ref.Machine != b452MachineB {
		t.Fatalf("应跳过同账号的 rq-1b 落 rq-2@B，得 %+v", out)
	}
	nt := b453GetB(t, p, out.Ref.Task)
	if nt.RerunOf == nil || *nt.RerunOf != (proto.TaskRef{Task: orig.ID, Machine: b452MachineB}) {
		t.Fatalf("新任务 rerun_of 应指回原任务，得 %+v", nt.RerunOf)
	}
	if nt.Mode != proto.TaskModeReadonly || nt.Squad != b453RerunSquad {
		t.Fatalf("新任务形态/小队 = %q/%q", nt.Mode, nt.Squad)
	}
	in := readRerunInput(t, p.b, nt.ID)
	if string(in["prompt"]) != `"调查一下"` {
		t.Fatalf("新任务输入应是同一份计划原文：%s", in["prompt"])
	}
	var excl []string
	_ = json.Unmarshal(in["exclude_accounts"], &excl)
	if len(excl) != 1 || excl[0] != ids["acc-1"] {
		t.Fatalf("新任务的链上已用账号应为 [acc-1 的 id]（B464 Q23），得 %v", excl)
	}
	if _, err := p.b.srv.RerunReadonly(context.Background(), nt, f); err == nil ||
		!strings.Contains(err.Error(), "重跑链已排除") || !strings.Contains(err.Error(), "acc-1") || !strings.Contains(err.Error(), "acc-2") {
		t.Fatalf("链上账号全用过应拒并点名排除集，得 %v", err)
	}
	p.stopOnB(t, orig.ID)
	p.stopOnB(t, nt.ID)
}

// 无账号载体按载体名计入排除集（防同一载体反复撞）。
func TestB453RerunReadonlyExcludesUnboundByCarrierName(t *testing.T) {
	p, _, _ := b453RerunPair(t, nil, []rerunMember{{"rq-n1", ""}, {"rq-n2", ""}})
	orig := b453ReadonlyViaA(t, p)
	f := orchestration.QuotaFailure{Text: b453QuotaFail}
	out, err := p.b.srv.RerunReadonly(context.Background(), b453GetB(t, p, orig.ID), f)
	if err != nil || out.Carrier != "rq-n2" {
		t.Fatalf("应换到 rq-n2，得 %+v %v", out, err)
	}
	if _, err := p.b.srv.RerunReadonly(context.Background(), b453GetB(t, p, out.Ref.Task), f); err == nil ||
		!strings.Contains(err.Error(), "rq-n1") || !strings.Contains(err.Error(), "rq-n2") {
		t.Fatalf("两个无账号载体都用过应拒并点名，得 %v", err)
	}
	p.stopOnB(t, orig.ID)
	p.stopOnB(t, out.Ref.Task)
}

// 端到端（编排 → 门面回调 → 派发）：原任务撞额度 → failed + rerun_by；新任务完成后自动归档。
func TestB453QuotaRerunEndToEnd(t *testing.T) {
	p, acc, _ := b453RerunPair(t, []fake.Step{}, []rerunMember{{"rq-1", "acc-1"}, {"rq-2", "acc-2"}})
	orig := b453ReadonlyViaA(t, p)
	p.b.fake.Add(orig.ID, fake.Step{Finish: executor.Result{OK: false, FailReason: b453QuotaFail}})
	_ = p.b.fake.Send(context.Background(), orig.ID, "go")
	var cur *proto.Task
	eventually(t, 10*time.Second, "原任务 failed 且带 rerun_by", func() bool {
		cur = b453GetB(t, p, orig.ID)
		return cur.State == proto.TaskStateFailed && cur.RerunBy != nil
	})
	if a, _ := acc.Assess("acc-1", time.Now()); a.State != accounts.StateOut {
		t.Fatalf("acc-1 应被标无额度，得 %s", a.State)
	}
	nt := b453GetB(t, p, cur.RerunBy.Task)
	if nt.Carrier != "rq-2" {
		t.Fatalf("新任务应在 rq-2，得 %s", nt.Carrier)
	}
	p.b.fake.Add(nt.ID, fake.Step{Finish: executor.Result{OK: true, Summary: "s", FinalText: "## 结论\nok"}})
	_ = p.b.fake.Send(context.Background(), nt.ID, "go")
	eventually(t, 10*time.Second, "新任务完成并自动归档", func() bool {
		return b453GetB(t, p, nt.ID).State == proto.TaskStateCompleted
	})
	// 名额归还在编排的撞额度处置 goroutine 里：迁 failed → 追加事件 → 读快照 → 终态回调归还。
	// 测试看到 failed + rerun_by 时它可能还没走到归还；等两边名额都归还再结束，免得读早了误红、
	// 或归还落在 teardown 关库之后（scheduling.release.error: database is closed）。
	eventually(t, 10*time.Second, "原载体 rq-1 名额归还", func() bool { return p.carrierCount(t, "rq-1") == 0 })
	eventually(t, 10*time.Second, "新载体 rq-2 名额归还", func() bool { return p.carrierCount(t, "rq-2") == 0 })
	if recs := p.b.logs.find("重跑"); len(recs) == 0 {
		t.Fatal("B 应有重跑 Info 日志")
	}
}
