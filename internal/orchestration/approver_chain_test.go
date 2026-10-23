// approver_chain_test.go —— B437 共享审批链的承重测试。
//
// 覆盖：链校验表驱动、链文件/body 编解码、逐环裁决与失败五分类、启用门、
// last-good 与 clear 回落、以及本次补充 P2 约束（链读取 2s 独立时限 + 限频 WARN）。
package orchestration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/executor"
)

// ---- 测试替身 ----

// funcChainReader 是链读取口的可编排替身。
type funcChainReader struct {
	mu sync.Mutex
	fn func(ctx context.Context) (ApproverChain, error)
}

func (r *funcChainReader) Current(ctx context.Context) (ApproverChain, error) {
	r.mu.Lock()
	fn := r.fn
	r.mu.Unlock()
	return fn(ctx)
}

func (r *funcChainReader) set(fn func(ctx context.Context) (ApproverChain, error)) {
	r.mu.Lock()
	r.fn = fn
	r.mu.Unlock()
}

// stubRemoteDecider 记录调用并按环返回结果。
type stubRemoteDecider struct {
	mu    sync.Mutex
	calls []ApproverRing
	tcs   []ApproverTaskContext // B460：每次调用收到的任务上下文
	fn    func(ring ApproverRing) RemoteOutcome
}

func (d *stubRemoteDecider) DecideRemote(_ context.Context, ring ApproverRing, _, _ string, tc ApproverTaskContext, _ time.Duration) RemoteOutcome {
	d.mu.Lock()
	d.calls = append(d.calls, ring)
	d.tcs = append(d.tcs, tc)
	d.mu.Unlock()
	return d.fn(ring)
}

func (d *stubRemoteDecider) lastTaskContext() ApproverTaskContext {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.tcs) == 0 {
		return ApproverTaskContext{}
	}
	return d.tcs[len(d.tcs)-1]
}

func (d *stubRemoteDecider) called() []ApproverRing {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]ApproverRing(nil), d.calls...)
}

func remoteApprove(model string) RemoteOutcome {
	return RemoteOutcome{Decision: "approve", Reason: "对端放行", Model: model, ElapsedMS: 7}
}

// ---- 链校验 ----

func TestValidateApproverChainTable(t *testing.T) {
	known := []string{"linux-01", "mac-mini"}
	cases := []struct {
		name  string
		chain ApproverChain
		known []string
		want  string // 期望错误包含的子串；空 = 合法
	}{
		{"合法", ApproverChain{Rings: []ApproverRing{{Machine: "linux-01", Executor: "codex"}}}, known, ""},
		{"空链", ApproverChain{}, known, "不能为空"},
		{"machine 空", ApproverChain{Rings: []ApproverRing{{Executor: "codex"}}}, known, "machine 不能为空"},
		{"executor 空", ApproverChain{Rings: []ApproverRing{{Machine: "linux-01"}}}, known, "executor 不能为空"},
		{"executor 非法名", ApproverChain{Rings: []ApproverRing{{Machine: "linux-01", Executor: "nope"}}}, known, "不是受支持的执行器"},
		{"未知机器", ApproverChain{Rings: []ApproverRing{{Machine: "ghost", Executor: "codex"}}}, known, "不在设备表"},
		{"重复环（model 同）", ApproverChain{Rings: []ApproverRing{
			{Machine: "linux-01", Executor: "codex", Model: "a"},
			{Machine: "linux-01", Executor: "codex", Model: "a"},
		}}, known, "重复"},
		{"重复环（model 异）", ApproverChain{Rings: []ApproverRing{
			{Machine: "linux-01", Executor: "codex", Model: "a"},
			{Machine: "linux-01", Executor: "codex", Model: "b"},
		}}, known, "重复"},
		{"同机不同执行器合法", ApproverChain{Rings: []ApproverRing{
			{Machine: "linux-01", Executor: "codex"},
			{Machine: "linux-01", Executor: "claude"},
		}}, known, ""},
		{"timeout 负", ApproverChain{Timeout: -time.Second, Rings: []ApproverRing{{Machine: "linux-01", Executor: "codex"}}}, known, "不能为负"},
		{"nil knownMachines 跳过机器校验", ApproverChain{Rings: []ApproverRing{{Machine: "whatever", Executor: "codex"}}}, nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateApproverChain(tc.chain, tc.known)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("应合法，得到: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("应含 %q，得到: %v", tc.want, err)
			}
		})
	}
}

func TestDecodeApproverChainFile(t *testing.T) {
	// 省略 timeout → 默认
	chain, err := DecodeApproverChainFile([]byte("candidates:\n  - machine: a\n    executor: codex\n"))
	if err != nil {
		t.Fatalf("省略 timeout 应合法: %v", err)
	}
	if chain.Timeout != DefaultApproverChainTimeout {
		t.Fatalf("省略 timeout 应取默认 %s，得到 %s", DefaultApproverChainTimeout, chain.Timeout)
	}
	if len(chain.Rings) != 1 || chain.Rings[0].Machine != "a" {
		t.Fatalf("candidates 应解成 rings: %+v", chain.Rings)
	}
	// 显式 60s
	chain, err = DecodeApproverChainFile([]byte("timeout: 60s\ncandidates:\n  - machine: a\n    executor: codex\n    model: m\n"))
	if err != nil || chain.Timeout != time.Minute || chain.Rings[0].Model != "m" {
		t.Fatalf("显式 timeout/模型解码失败: %+v %v", chain, err)
	}
	// 显式 0 → 拒绝
	if _, err := DecodeApproverChainFile([]byte("timeout: 0s\ncandidates:\n  - machine: a\n    executor: codex\n")); err == nil {
		t.Fatal("显式 timeout=0 应拒绝")
	}
	// 显式负 → 拒绝
	if _, err := DecodeApproverChainFile([]byte("timeout: -1s\ncandidates:\n  - machine: a\n    executor: codex\n")); err == nil {
		t.Fatal("显式 timeout<0 应拒绝")
	}
}

func TestApproverChainBodyRoundTrip(t *testing.T) {
	chain := ApproverChain{Timeout: 90 * time.Second, Rings: []ApproverRing{
		{Machine: "linux-01", Executor: "codex", Model: "m1"},
		{Machine: "mac-mini", Executor: "claude"},
	}}
	body, err := EncodeApproverChain(chain)
	if err != nil {
		t.Fatal(err)
	}
	// 冻结 F5：body 顶层与每环的键集逐键精确一致（多键少键都红）。
	assertApproverBodyKeys(t, "body 顶层", approverBodyKeys(t, body), []string{"rings", "timeout"})
	var top struct {
		Rings []json.RawMessage `json:"rings"`
	}
	if err := json.Unmarshal(body, &top); err != nil {
		t.Fatalf("body 应为可解的 JSON 对象: %v", err)
	}
	if len(top.Rings) != 2 {
		t.Fatalf("应两个环: %s", body)
	}
	// model 有值 → 出键；省略 → 不出键（omitempty）。
	assertApproverBodyKeys(t, "ring[0]", approverBodyKeys(t, top.Rings[0]), []string{"executor", "machine", "model"})
	assertApproverBodyKeys(t, "ring[1]", approverBodyKeys(t, top.Rings[1]), []string{"executor", "machine"})
	back, err := DecodeApproverChain(body)
	if err != nil {
		t.Fatalf("解码 body: %v", err)
	}
	if back.Timeout != chain.Timeout || len(back.Rings) != 2 || back.Rings[0].Model != "m1" {
		t.Fatalf("往返不等: %+v", back)
	}
}

// approverBodyKeys 取一个 JSON 对象的键集（升序）。
func approverBodyKeys(t *testing.T, raw []byte) []string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("应为 JSON 对象: %v (%s)", err, raw)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// assertApproverBodyKeys 断言键集逐键一致（复用 proto 侧 assertKeys 形态）。
func assertApproverBodyKeys(t *testing.T, label string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s 键集不符：want %v got %v", label, want, got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s 键集不符：want %v got %v", label, want, got)
		}
	}
}

// ---- 逐环裁决与失败分类 ----

func TestDecideSharedChainRemoteApprove(t *testing.T) {
	rd := &stubRemoteDecider{fn: func(ApproverRing) RemoteOutcome { return remoteApprove("remote-model") }}
	a := newSharedApprover(t, nil)
	a.SetSelfMachine(func(string) bool { return false })
	a.SetRemoteDecider(rd)
	a.SetChainReader(&funcChainReader{fn: func(context.Context) (ApproverChain, error) {
		return ApproverChain{Rings: []ApproverRing{{Machine: "linux-01", Executor: "codex"}}}, nil
	}})
	d := a.Decide(context.Background(), "Bash: ls", "摘要", ApproverTaskContext{})
	if !d.Approve || d.Err != nil {
		t.Fatalf("应 approve: %+v", d)
	}
	if len(d.Attempts) != 1 {
		t.Fatalf("应一条 attempt: %+v", d.Attempts)
	}
	at := d.Attempts[0]
	if at.Machine != "linux-01" || at.Executor != "codex" || at.Model != "remote-model" {
		t.Fatalf("attempt 应带 machine/executor/对端 model: %+v", at)
	}
}

func TestB463DefaultWarnLogsRingOutcomeAndSkippedCodex(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	a := newSharedApproverWithLog(t, log)
	a.SetSelfMachine(func(string) bool { return false })
	a.SetRemoteDecider(&stubRemoteDecider{fn: func(ApproverRing) RemoteOutcome { return remoteApprove("m") }})
	a.SetChainReader(&funcChainReader{fn: func(context.Context) (ApproverChain, error) {
		return ApproverChain{Rings: []ApproverRing{
			{Machine: "linux-01", Executor: "claude"},
			{Machine: "mac-mini", Executor: "codex"},
		}}, nil
	}})
	if d := a.Decide(context.Background(), "Bash: sensitive-command-value", "", ApproverTaskContext{}); !d.Approve {
		t.Fatalf("首环应 approve: %+v", d)
	}
	got := buf.String()
	for _, want := range []string{"审批者环开始", "审批者环结果", "审批者环跳过", "executor=codex", "前序环已给出裁决", "request_id=ap-", "ring_index=1", "model=默认", "elapsed_ms="} {
		if !strings.Contains(got, want) {
			t.Fatalf("默认 Warn 日志缺少 %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "sensitive-command-value") {
		t.Fatalf("日志不应记录权限原文:\n%s", got)
	}
}

type b463ImmediateRemoteDecider struct{}

func (b463ImmediateRemoteDecider) DecideRemote(context.Context, ApproverRing, string, string, ApproverTaskContext, time.Duration) RemoteOutcome {
	return remoteApprove("m")
}

func TestB463LocalRingFailureLogsDoNotExposePermissionOrCause(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	shot := &stubShot{err: errors.New("executor rejected sensitive-command-value")}
	a := newSharedApproverWithLog(t, log)
	a.SetSelfMachine(func(string) bool { return true })
	a.SetLocalShot(func(string) (executor.OneShot, error) { return shot, nil })
	a.SetChainReader(&funcChainReader{fn: func(context.Context) (ApproverChain, error) {
		return ApproverChain{Rings: []ApproverRing{{Machine: "self", Executor: "codex", Model: "m"}}}, nil
	}})
	d := a.Decide(context.Background(), "Bash: sensitive-command-value", "", ApproverTaskContext{})
	if d.Err == nil || len(d.Attempts) != 1 || d.Attempts[0].Decision != "error" {
		t.Fatalf("真实本机环失败应保留失败分类: %+v", d)
	}
	got := buf.String()
	if strings.Contains(got, "sensitive-command-value") {
		t.Fatalf("默认 Warn 环日志泄露权限或执行错误正文:\n%s", got)
	}
	for _, want := range []string{"单环裁决失败", "审批者环结果", "request_id=ap-", "executor=codex", "model=m", "failed=true", "elapsed_ms="} {
		if !strings.Contains(got, want) {
			t.Fatalf("环失败日志缺少安全诊断字段 %q:\n%s", want, got)
		}
	}
}

func TestB463LocalRingDeadlineIsClassifiedWithoutCause(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	ctx, cancel := context.WithTimeout(withApproverRequestID(context.Background()), 5*time.Millisecond)
	defer cancel()
	shot := &stubShot{fn: func(ctx context.Context, _ executor.OneShotReq) (executor.OneShotReply, error) {
		<-ctx.Done()
		return executor.OneShotReply{}, ctx.Err()
	}}
	out := RunLocalRing(ctx, shot, "codex", "m", "Bash: sensitive-command-value", "",
		ApproverTaskContext{}, 0, nil, log)
	if out.Decision != "error" || out.ErrorKind != ApproverFailTimeout {
		t.Fatalf("真实 context deadline 应分类为 timeout，得 %+v", out)
	}
	got := buf.String()
	if strings.Contains(got, "sensitive-command-value") || strings.Contains(got, "deadline exceeded") {
		t.Fatalf("timeout 日志不能包含权限/错误正文:\n%s", got)
	}
	for _, want := range []string{"request_id=ap-", "error_kind=timeout", "elapsed_ms="} {
		if !strings.Contains(got, want) {
			t.Fatalf("timeout 日志缺诊断字段 %q:\n%s", want, got)
		}
	}
}

func TestB463LocalRingResolutionFailureLogsDoNotExposeCause(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	a := newSharedApproverWithLog(t, log)
	a.SetSelfMachine(func(string) bool { return true })
	a.SetLocalShot(func(string) (executor.OneShot, error) {
		return nil, errors.New("resolve failed: sensitive-command-value")
	})
	a.SetChainReader(&funcChainReader{fn: func(context.Context) (ApproverChain, error) {
		return ApproverChain{Rings: []ApproverRing{{Machine: "self", Executor: "codex", Model: "m"}}}, nil
	}})
	d := a.Decide(context.Background(), "Bash: ls", "", ApproverTaskContext{})
	if d.Err == nil || len(d.Attempts) != 1 || d.Attempts[0].ErrorKind != ApproverFailInternal {
		t.Fatalf("本机 OneShot 解析失败应保留失败分类: %+v", d)
	}
	got := buf.String()
	if strings.Contains(got, "sensitive-command-value") {
		t.Fatalf("日志不应记录 OneShot 解析错误正文:\n%s", got)
	}
	for _, want := range []string{"request_id=ap-", "error_kind=internal", "elapsed_ms="} {
		if !strings.Contains(got, want) {
			t.Fatalf("环解析失败日志缺少诊断字段 %q:\n%s", want, got)
		}
	}
}

func TestB463ConcurrentRingLogsHaveRequestIDs(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	a := newSharedApproverWithLog(t, log)
	a.SetSelfMachine(func(string) bool { return false })
	a.SetRemoteDecider(b463ImmediateRemoteDecider{})
	a.SetChainReader(&funcChainReader{fn: func(context.Context) (ApproverChain, error) {
		return ApproverChain{Rings: []ApproverRing{{Machine: "linux-01", Executor: "codex"}}}, nil
	}})
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if d := a.Decide(context.Background(), "Bash: ls", "", ApproverTaskContext{}); !d.Approve {
				t.Errorf("并发裁决应 approve: %+v", d)
			}
		}()
	}
	wg.Wait()
	ids := map[string]bool{}
	for _, line := range strings.Split(buf.String(), "\n") {
		if !strings.Contains(line, "审批者环") {
			continue
		}
		var id string
		for _, field := range strings.Fields(line) {
			if strings.HasPrefix(field, "request_id=") {
				id = strings.TrimPrefix(field, "request_id=")
			}
		}
		if id == "" {
			t.Fatalf("环日志缺 request_id: %s", line)
		}
		ids[id] = true
	}
	if len(ids) != 2 {
		t.Fatalf("两次并发请求应分别可关联，request ids=%v; logs:\n%s", ids, buf.String())
	}
}

func TestDecideSharedChainFailoverToSecondRing(t *testing.T) {
	rd := &stubRemoteDecider{fn: func(ring ApproverRing) RemoteOutcome {
		if ring.Machine == "linux-01" {
			return RemoteOutcome{Decision: "error", ErrorKind: ApproverFailUnreachable, Reason: "connection refused"}
		}
		return remoteApprove("m")
	}}
	a := newSharedApprover(t, nil)
	a.SetSelfMachine(func(string) bool { return false })
	a.SetRemoteDecider(rd)
	a.SetChainReader(&funcChainReader{fn: func(context.Context) (ApproverChain, error) {
		return ApproverChain{Rings: []ApproverRing{
			{Machine: "linux-01", Executor: "codex"},
			{Machine: "mac-mini", Executor: "claude"},
		}}, nil
	}})
	d := a.Decide(context.Background(), "x", "", ApproverTaskContext{})
	if !d.Approve || d.Err != nil {
		t.Fatalf("第二环应 approve: %+v", d)
	}
	if len(d.Attempts) != 2 {
		t.Fatalf("应两条 attempt: %+v", d.Attempts)
	}
	if d.Attempts[0].Reason != "机器不可达" || d.Attempts[0].Machine != "linux-01" {
		t.Fatalf("首环应记「机器不可达」+ machine: %+v", d.Attempts[0])
	}
}

func TestDecideSharedChainFailureClassTable(t *testing.T) {
	cases := []struct {
		kind string
		want string
	}{
		{ApproverFailUnreachable, "机器不可达"},
		{ApproverFailUnsupported, "对端不支持"},
		{ApproverFailNoExecutor, "对端无此执行器"},
		{ApproverFailTimeout, "超时"},
		{ApproverFailInvalidOutput, "裁决无效"},
		{ApproverFailInternal, "对端内部错误"},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			rd := &stubRemoteDecider{fn: func(ApproverRing) RemoteOutcome {
				return RemoteOutcome{Decision: "error", ErrorKind: tc.kind}
			}}
			a := newSharedApprover(t, nil)
			a.SetSelfMachine(func(string) bool { return false })
			a.SetRemoteDecider(rd)
			a.SetChainReader(&funcChainReader{fn: func(context.Context) (ApproverChain, error) {
				return ApproverChain{Rings: []ApproverRing{{Machine: "linux-01", Executor: "codex"}}}, nil
			}})
			d := a.Decide(context.Background(), "x", "", ApproverTaskContext{})
			if d.Err == nil || d.Approve {
				t.Fatalf("应 fail-closed: %+v", d)
			}
			if len(d.Attempts) != 1 || d.Attempts[0].Reason != tc.want {
				t.Fatalf("应记 %q，得到: %+v", tc.want, d.Attempts)
			}
		})
	}
}

func TestDecideSharedChainLocalRingNoNetwork(t *testing.T) {
	shot := &stubShot{out: `{"decision":"approve","reason":"本机放行"}`}
	rd := &stubRemoteDecider{fn: func(ApproverRing) RemoteOutcome { t.Fatal("本机环不应走网络"); return RemoteOutcome{} }}
	a := newSharedApprover(t, nil)
	a.SetSelfMachine(func(name string) bool { return name == "self" })
	a.SetLocalShot(func(name string) (executor.OneShot, error) { return shot, nil })
	a.SetRemoteDecider(rd)
	a.SetChainReader(&funcChainReader{fn: func(context.Context) (ApproverChain, error) {
		return ApproverChain{Rings: []ApproverRing{{Machine: "self", Executor: "codex"}}}, nil
	}})
	d := a.Decide(context.Background(), "x", "", ApproverTaskContext{})
	if !d.Approve || d.Err != nil {
		t.Fatalf("本机环应 approve: %+v", d)
	}
	if d.Attempts[0].Machine != "self" {
		t.Fatalf("审计 machine 应为自名: %+v", d.Attempts[0])
	}
	if len(rd.called()) != 0 {
		t.Fatal("本机环不得调用远程裁决口")
	}
}

func TestDecideSharedChainRemoteMissingDecider(t *testing.T) {
	a := newSharedApprover(t, nil)
	a.SetSelfMachine(func(string) bool { return false })
	a.SetChainReader(&funcChainReader{fn: func(context.Context) (ApproverChain, error) {
		return ApproverChain{Rings: []ApproverRing{{Machine: "linux-01", Executor: "codex"}}}, nil
	}})
	d := a.Decide(context.Background(), "x", "", ApproverTaskContext{})
	if d.Err == nil || d.Attempts[0].Machine != "linux-01" {
		t.Fatalf("无远程裁决口应 fail-closed 且记 machine: %+v", d)
	}
}

// ---- 启用门 ----

func TestAvailableGate(t *testing.T) {
	// 本地空 + 无读取口 → false
	if a := newSharedApprover(t, nil); a.Available() {
		t.Fatal("本地空且无链读取口应不可用")
	}
	// 本地非空 → true（不读账本）
	local, err := NewApprover(config.ApproverConfig{Executor: config.ExecutorList{"opencode"}, Timeout: time.Second}, nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if !local.Available() {
		t.Fatal("本地链非空应可用")
	}
	// 本地空 + 链读取口有链 → true
	a := newSharedApprover(t, nil)
	a.SetChainReader(&funcChainReader{fn: func(context.Context) (ApproverChain, error) {
		return ApproverChain{Rings: []ApproverRing{{Machine: "linux-01", Executor: "codex"}}}, nil
	}})
	if !a.Available() {
		t.Fatal("共享链可读应可用（首设即生效）")
	}
	// 本地空 + 链不存在 → false
	b := newSharedApprover(t, nil)
	b.SetChainReader(&funcChainReader{fn: func(context.Context) (ApproverChain, error) {
		return ApproverChain{}, ErrApproverChainAbsent
	}})
	if b.Available() {
		t.Fatal("链不存在应不可用")
	}
}

// ---- last-good / clear 回落 ----

func TestSharedChainLastGoodOnReadFailure(t *testing.T) {
	reader := &funcChainReader{fn: func(context.Context) (ApproverChain, error) {
		return ApproverChain{Rings: []ApproverRing{{Machine: "linux-01", Executor: "codex"}}}, nil
	}}
	a := newSharedApprover(t, nil)
	a.SetSelfMachine(func(string) bool { return false })
	a.SetRemoteDecider(&stubRemoteDecider{fn: func(ApproverRing) RemoteOutcome { return remoteApprove("m") }})
	a.SetChainReader(reader)
	if d := a.Decide(context.Background(), "x", "", ApproverTaskContext{}); !d.Approve {
		t.Fatalf("首读应 approve: %+v", d)
	}
	// 读失败 → 沿用 last-good
	reader.set(func(context.Context) (ApproverChain, error) { return ApproverChain{}, errors.New("账本抖动") })
	if d := a.Decide(context.Background(), "x", "", ApproverTaskContext{}); !d.Approve || d.Err != nil {
		t.Fatalf("读失败应沿用 last-good: %+v", d)
	}
}

func TestSharedChainClearDiscardsLastGood(t *testing.T) {
	// 本地链可 approve，用于区分回落目标
	a, err := NewApprover(config.ApproverConfig{Executor: config.ExecutorList{"opencode"}, Timeout: time.Second}, nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	a.BindOneShot(&stubShot{out: `{"decision":"approve","reason":"本地放行"}`})
	reader := &funcChainReader{fn: func(context.Context) (ApproverChain, error) {
		return ApproverChain{Rings: []ApproverRing{{Machine: "linux-01", Executor: "codex"}}}, nil
	}}
	a.SetSelfMachine(func(string) bool { return false })
	a.SetRemoteDecider(&stubRemoteDecider{fn: func(ApproverRing) RemoteOutcome {
		return RemoteOutcome{Decision: "escalate", Reason: "对端升级"}
	}})
	a.SetChainReader(reader)
	// 首读走共享链 → escalate
	if d := a.Decide(context.Background(), "x", "", ApproverTaskContext{}); d.Approve || d.Err != nil || d.Attempts[0].Machine != "linux-01" {
		t.Fatalf("首读应走共享链 escalate: %+v", d)
	}
	// clear（ErrApproverChainAbsent）→ 丢弃 last-good，回落本地链 approve
	reader.set(func(context.Context) (ApproverChain, error) { return ApproverChain{}, ErrApproverChainAbsent })
	d := a.Decide(context.Background(), "x", "", ApproverTaskContext{})
	if !d.Approve || d.Err != nil {
		t.Fatalf("clear 后应回落本地链 approve: %+v", d)
	}
	if d.Attempts[0].Machine != "" {
		t.Fatalf("本地链 attempt 不应带 machine: %+v", d.Attempts[0])
	}
	// 再读失败：last-good 已清，不应滞留死链 → 仍回落本地链
	reader.set(func(context.Context) (ApproverChain, error) { return ApproverChain{}, errors.New("账本抖动") })
	if d := a.Decide(context.Background(), "x", "", ApproverTaskContext{}); !d.Approve {
		t.Fatalf("clear 后读失败不应复活死链，应回落本地链: %+v", d)
	}
}

// ---- 本次补充 P2 约束：2s 独立时限 + 限频 WARN ----

func TestSharedChainReadTimeoutUsesLastGood(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	reader := &funcChainReader{fn: func(context.Context) (ApproverChain, error) {
		return ApproverChain{Rings: []ApproverRing{{Machine: "linux-01", Executor: "codex"}}}, nil
	}}
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	a := newSharedApproverWithLog(t, log)
	a.SetSelfMachine(func(string) bool { return false })
	a.SetRemoteDecider(&stubRemoteDecider{fn: func(ApproverRing) RemoteOutcome { return remoteApprove("m") }})
	a.SetChainReader(reader)
	// 先成功读一次，建立 last-good
	if d := a.Decide(context.Background(), "x", "", ApproverTaskContext{}); !d.Approve {
		t.Fatalf("首读应 approve: %+v", d)
	}
	// 读挂起：Decide 应在 ~2s 内返回并走 last-good，不被拖到裁决时限（此处设 30s）
	reader.set(func(ctx context.Context) (ApproverChain, error) { <-release; return ApproverChain{}, nil })
	start := time.Now()
	d := a.Decide(context.Background(), "x", "", ApproverTaskContext{})
	elapsed := time.Since(start)
	if !d.Approve || d.Err != nil {
		t.Fatalf("读超时应走 last-good: %+v", d)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("读封顶应在 ~%s 内返回，实测 %s", ApproverChainReadTimeout, elapsed)
	}
	// 连续多次读超时只出一条 WARN
	a.Decide(context.Background(), "x", "", ApproverTaskContext{})
	a.Decide(context.Background(), "x", "", ApproverTaskContext{})
	warns := strings.Count(buf.String(), "读共享审批链失败")
	if warns != 1 {
		t.Fatalf("同类告警应限频为 1 条，实测 %d 条:\n%s", warns, buf.String())
	}
}

// TestShouldConsultApproverSharedGate 钉住启用门：本地链为空的共享机，链首设后
// 门即 true（不重启）；链不存在时门 false（与今天「直接升级人工」逐字一致）。
func TestShouldConsultApproverSharedGate(t *testing.T) {
	ap := newSharedApprover(t, nil)
	reader := &funcChainReader{fn: func(context.Context) (ApproverChain, error) {
		return ApproverChain{}, ErrApproverChainAbsent
	}}
	ap.SetChainReader(reader)
	m := &Manager{approver: ap, log: slog.Default(), apDisabled: map[string]bool{}}
	if m.shouldConsultApprover("t1") {
		t.Fatal("链不存在时门应 false")
	}
	// 首设（链出现）→ 下一次请求门读到链 → true
	reader.set(func(context.Context) (ApproverChain, error) {
		return ApproverChain{Rings: []ApproverRing{{Machine: "linux-01", Executor: "codex"}}}, nil
	})
	if !m.shouldConsultApprover("t1") {
		t.Fatal("链首设后门应 true（不重启即生效）")
	}
	// 停用优先于可用
	m.apDisabled["t1"] = true
	if m.shouldConsultApprover("t1") {
		t.Fatal("停用后门应 false")
	}
}

func newSharedApprover(t *testing.T, _ interface{}) *Approver {
	t.Helper()
	return newSharedApproverWithLog(t, slog.Default())
}

func newSharedApproverWithLog(t *testing.T, log *slog.Logger) *Approver {
	t.Helper()
	a, err := NewApproverForShared(config.ApproverConfig{Timeout: time.Second}, nil, log)
	if err != nil {
		t.Fatal(err)
	}
	if a == nil {
		t.Fatal("NewApproverForShared 应返回非 nil（共享机恒构造）")
	}
	return a
}
