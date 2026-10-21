package agentd

// b453_rerun_test.go —— B453 S6 门面侧（V12）：只读任务派发留存原始输入、撞额度标账号、
// 同小队另挑成员重跑（排除链上已用账号/无账号载体按载体名）、链接与链终止文案。
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

// b453RerunPair 在 b452 双机夹具上接账号域（两台同一账本）、登记小队成员（全在 B 机）与撞额度回调。
func b453RerunPair(t *testing.T, steps []fake.Step, members []scheduling.Carrier) (*b452Pair, *accounts.Service) {
	t.Helper()
	p := newB452Pair(t, steps)
	acc := accounts.New(p.a.srv.SchedulingRegistry())
	for _, h := range []*b452Host{p.a, p.b} {
		h.srv.SetAccounts(acc)
		h.sched.SetAccounts(acc)
	}
	acc.SetReferrers(p.a.sched.CarriersUsingAccount)
	seen := map[string]bool{}
	var ms []scheduling.SquadMember
	for _, c := range members {
		if c.Account != "" && !seen[c.Account] {
			seen[c.Account] = true
			if _, err := acc.Put(accounts.Account{Name: c.Account, Kind: accounts.KindCodex, Machine: b452MachineB,
				HomeDir: "/home/" + c.Account, Entry: "tokens", IntervalMinutes: 15, WarnPercent: 60, LowPercent: 80}, 0); err != nil {
				t.Fatalf("登记账号 %s: %v", c.Account, err)
			}
		}
		c.Machine, c.CLI, c.Credential, c.MaxConcurrency = b452MachineB, "fake", scheduling.CredentialStandalone, 2
		putOnlineCarrier(t, p.a.sched, c)
		ms = append(ms, scheduling.SquadMember{Carrier: c.Name, MaxConcurrency: 2})
	}
	if err := p.a.sched.PutSquad(scheduling.Squad{Name: b453RerunSquad, Role: scheduling.RoleExecutor, Members: ms}, 0); err != nil {
		t.Fatalf("登记小队: %v", err)
	}
	p.b.mgr.SetQuotaHandler(p.b.srv)
	return p, acc
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
	p, _ := b453RerunPair(t, nil, []scheduling.Carrier{{Name: "rq-1", Account: "acc-1"}, {Name: "rq-2", Account: "acc-2"}})
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

// MarkTaskQuota：标任务所用账号无额度，返回撞到的窗口与预计恢复；未选账号不标。
func TestB453MarkTaskQuotaMarksAccountAndSummarizes(t *testing.T) {
	p, acc := b453RerunPair(t, nil, []scheduling.Carrier{{Name: "rq-1", Account: "acc-1"}})
	readingWithFingerprint(t, acc, "acc-1", "", 100)
	hit := p.b.srv.MarkTaskQuota(&proto.Task{ID: "t-x", Carrier: "rq-1", Account: "acc-1", AccountKind: accounts.KindCodex},
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
	p, _ := b453RerunPair(t, nil, []scheduling.Carrier{
		{Name: "rq-1", Account: "acc-1"}, {Name: "rq-1b", Account: "acc-1"}, {Name: "rq-2", Account: "acc-2"}})
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
	if len(excl) != 1 || excl[0] != "acc-1" {
		t.Fatalf("新任务的链上已用账号应为 [acc-1]，得 %v", excl)
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
	p, _ := b453RerunPair(t, nil, []scheduling.Carrier{{Name: "rq-n1"}, {Name: "rq-n2"}})
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
	p, acc := b453RerunPair(t, []fake.Step{}, []scheduling.Carrier{{Name: "rq-1", Account: "acc-1"}, {Name: "rq-2", Account: "acc-2"}})
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
	if got := p.carrierCount(t, "rq-1"); got != 0 {
		t.Fatalf("原载体名额应已归还，得 %d", got)
	}
	if recs := p.b.logs.find("重跑"); len(recs) == 0 {
		t.Fatal("B 应有重跑 Info 日志")
	}
}
