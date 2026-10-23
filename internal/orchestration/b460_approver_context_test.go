package orchestration

// b460_approver_context_test.go —— B460 S7 V6：审批提示词的「任务上下文」段与「只读形态
// 裁决政策」段。零值上下文时提示词与本卡之前逐字节一致（testdata 金样本由基线模板生成）；
// 生产路径两条（Manager consultApprover 面、OpenCode bindApproval 面）证明上下文确实从
// 任务记录组装并送到审批者 OneShot。
import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/permgate"
	"github.com/Xsxdot/handoff/internal/proto"
)

const (
	b460Summary = "统计行数的调查任务"
	b460Perm    = "Bash: python3 - <<'EOF'\nprint(1)\nEOF"
)

func TestB460PromptZeroContextGolden(t *testing.T) {
	want, err := os.ReadFile(filepath.Join("testdata", "b460_prompt_zero_context.golden"))
	if err != nil {
		t.Fatal(err)
	}
	got := approverPrompt(b460Summary, b460Perm, "N0NCE", ApproverTaskContext{})
	if got != string(want) {
		t.Fatalf("零值上下文提示词必须与基线逐字节一致\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestB460PromptWithContext(t *testing.T) {
	tc := ApproverTaskContext{Mode: proto.TaskModeReadonly, Workdir: "/wt/x",
		ArtifactsDir: "/data/tasks/t1/artifacts", TaskTmpDir: "/data/tmp/t1"}
	got := approverPrompt(b460Summary, b460Perm, "N0NCE", tc)
	for _, s := range []string{"任务上下文：", "readonly", "/wt/x", "/data/tasks/t1/artifacts",
		"HANDOFF_ARTIFACTS_DIR", "/data/tmp/t1", "只读任务的裁决政策：", "~/.codex/auth.json",
		"~/.ssh/", "拿不准时升级。"} {
		if !strings.Contains(got, s) {
			t.Fatalf("只读上下文提示词缺 %q：\n%s", s, got)
		}
	}
	// 上下文段在「任务背景」之后、「权限请求」之前
	if i, j := strings.Index(got, "任务上下文："), strings.Index(got, "权限请求："); i < 0 || j < i {
		t.Fatalf("上下文段位置不对：\n%s", got)
	}
	tc.Mode = proto.TaskModeBranch
	got = approverPrompt(b460Summary, b460Perm, "N0NCE", tc)
	if !strings.Contains(got, "任务上下文：") || strings.Contains(got, "只读任务的裁决政策") {
		t.Fatalf("branch 形态应有上下文段、无只读政策段：\n%s", got)
	}
	tc.ArtifactsDir = ""
	got = approverPrompt(b460Summary, b460Perm, "N0NCE", tc)
	if strings.Contains(got, "产物目录") {
		t.Fatalf("无产物目录时不得出现该行：\n%s", got)
	}
}

// promptCapture 是捕获审批提示词的 OneShot 函数（线程安全），裁决恒 approve。
type promptCapture struct {
	mu      sync.Mutex
	prompts []string
}

func (p *promptCapture) fn(_ context.Context, req executor.OneShotReq) (executor.OneShotReply, error) {
	p.mu.Lock()
	p.prompts = append(p.prompts, req.Prompt)
	p.mu.Unlock()
	nonce := extractNonceForTest(req.Prompt)
	return executor.OneShotReply{Text: `{"decision":"approve","nonce":"` + nonce + `"}`,
		Status: executor.OneShotOK}, nil
}

func (p *promptCapture) last() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.prompts) == 0 {
		return ""
	}
	return p.prompts[len(p.prompts)-1]
}

func b460AssertTaskContextInPrompt(t *testing.T, m *Manager, prompt string, task *proto.Task) {
	t.Helper()
	for _, s := range []string{task.Workdir(), TaskArtifactsDir(m.cfg.DataDir, task.ID),
		executor.TaskTmpDir(m.cfg.DataDir, task.ID), "readonly", "只读任务的裁决政策："} {
		if s == "" || !strings.Contains(prompt, s) {
			t.Fatalf("生产路径审批提示词缺 %q：\n%s", s, prompt)
		}
	}
}

// TestB460ManagerConsultCarriesTaskContext：经 mgr.Dispatch 派只读任务（fake 执行器），
// 发一条 Consult 形态权限（内联 Python heredoc），审批者 OneShot 捕获的提示词含该任务的
// 工作树、产物目录、临时目录与只读政策。
func TestB460ManagerConsultCarriesTaskContext(t *testing.T) {
	var cap promptCapture
	m, st, _ := newTestManagerWithApproverFunc(t, approverStep(b460Perm), cap.fn)
	repo := initTestRepo(t)
	pid := registerTestProject(t, m, repo)
	task, err := m.Dispatch(context.Background(), DispatchReq{ProjectID: pid, Prompt: "调查",
		Executor: "fake", Mode: proto.TaskModeReadonly})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	waitEvent(t, st, task.ID, proto.EventTypeApproverDecision)
	cur, err := st.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	b460AssertTaskContextInPrompt(t, m, cap.last(), cur)
}

// TestB460OpenCodeConsultCarriesTaskContext：OpenCode 面 bindApproval 闭包同样组装上下文。
func TestB460OpenCodeConsultCarriesTaskContext(t *testing.T) {
	var cap promptCapture
	m, st, _ := newTestManagerWithApproverFunc(t, nil, cap.fn)
	now := time.Now().UTC()
	task := &proto.Task{ID: "b460-oc-ctx", RepoPath: t.TempDir(), WorkDir: t.TempDir(),
		Mode: proto.TaskModeReadonly, State: proto.TaskStateRunning, Executor: "fake",
		CreatedAt: now, UpdatedAt: now}
	mustCreateTask(t, st, task)
	if err := os.MkdirAll(TaskArtifactsDir(m.cfg.DataDir, task.ID), 0o700); err != nil {
		t.Fatal(err)
	}
	c := m.bindApproval(task.ID, executor.PolicySnapshot{Version: "v1", TaskID: task.ID})
	if _, err := c.Request(context.Background(), executor.ApprovalRequest{
		NativeID: "oc-1", Text: b460Perm,
		Perm: &executor.PermRequest{Tool: executor.PermToolBash, Command: "python3 - <<'EOF'\nprint(1)\nEOF"},
	}); err != nil {
		t.Fatal(err)
	}
	b460AssertTaskContextInPrompt(t, m, cap.last(), task)
}

// TestB460TaskContextOldTaskWithoutArtifacts：产物目录不存在的老任务 → ArtifactsDir 为空。
func TestB460TaskContextOldTaskWithoutArtifacts(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	mustCreateTask(t, st, &proto.Task{ID: "old-1", RepoPath: t.TempDir(), WorkDir: t.TempDir(),
		State: proto.TaskStateRunning, Executor: "fake"})
	tc := m.approverTaskContext("old-1")
	if tc.ArtifactsDir != "" || tc.Workdir == "" || tc.TaskTmpDir == "" {
		t.Fatalf("老任务上下文 = %+v", tc)
	}
	if tc := m.approverTaskContext("no-such"); tc != (ApproverTaskContext{}) {
		t.Fatalf("读任务失败应返回零值上下文，得 %+v", tc)
	}
}

// TestB460SharedChainCarriesTaskContext：共享链本机环（decideRing 自机分支）与他机环
// （RemoteDecider）都拿到同一份上下文。
func TestB460SharedChainCarriesTaskContext(t *testing.T) {
	tc := ApproverTaskContext{Mode: proto.TaskModeReadonly, Workdir: "/wt/y",
		ArtifactsDir: "/d/tasks/y/artifacts", TaskTmpDir: "/d/tmp/y"}
	var cap promptCapture
	shot := &stubShot{fn: cap.fn}
	a := newSharedApprover(t, nil)
	a.SetSelfMachine(func(m string) bool { return m == "self" })
	a.SetLocalShot(func(string) (executor.OneShot, error) { return shot, nil })
	a.SetChainReader(&funcChainReader{fn: func(context.Context) (ApproverChain, error) {
		return ApproverChain{Rings: []ApproverRing{{Machine: "self", Executor: "codex"}}}, nil
	}})
	if d := a.Decide(context.Background(), "Bash: ls", "摘要", tc); !d.Approve {
		t.Fatalf("本机环应 approve: %+v", d)
	}
	if p := cap.last(); !strings.Contains(p, "/wt/y") || !strings.Contains(p, "只读任务的裁决政策") {
		t.Fatalf("本机环提示词缺上下文：\n%s", p)
	}

	rd := &stubRemoteDecider{fn: func(ApproverRing) RemoteOutcome { return remoteApprove("m") }}
	a.SetRemoteDecider(rd)
	a.SetChainReader(&funcChainReader{fn: func(context.Context) (ApproverChain, error) {
		return ApproverChain{Rings: []ApproverRing{{Machine: "linux-01", Executor: "codex"}}}, nil
	}})
	if d := a.Decide(context.Background(), "Bash: ls", "摘要", tc); !d.Approve {
		t.Fatalf("他机环应 approve: %+v", d)
	}
	if got := rd.lastTaskContext(); got != tc {
		t.Fatalf("他机环应收到同一上下文，得 %+v", got)
	}
}

// TestB460PolicyCredentialListSingleSource 锁 S10：只读政策段的凭据清单与规则层（permgate）同源——
// 规则层改判 Consult 的每一类位置，审批者的政策里都点名要升级，两边不会漂移。
func TestB460PolicyCredentialListSingleSource(t *testing.T) {
	got := approverPrompt(b460Summary, b460Perm, "N0NCE", ApproverTaskContext{Mode: proto.TaskModeReadonly, Workdir: "/wt"})
	if strings.Contains(credentialListText(), "compgen -e") {
		t.Fatal("name-only compgen policy must not be nested under the credential list's execute-any-item escalation")
	}
	for _, loc := range permgate.CredentialLocations() {
		if !strings.Contains(got, loc.Path) {
			t.Errorf("只读政策段缺凭据位置 %q", loc.Path)
		}
		if loc.Note != "" && !strings.Contains(got, loc.Note) {
			t.Errorf("只读政策段缺凭据位置说明 %q", loc.Note)
		}
	}
	for _, s := range []string{permgate.SecretEnvCommandsText, ".env.*", "GROK_HOME",
		"compgen -e", "裸 env/printenv 或 printenv NAME 实际输出变量值",
		"历史 `env | cut -d= -f1 | sort` 与 `env | cut -d= -f1 | wc -l` 仍须升级",
		"已验证为只读的 CLI 子命令 --help 应批准", `"$TMPDIR/handoff-fc" dispatch --help`,
		"deploy.sh --help 不因 --help 标志而视为只读，必须升级",
		"compgen -e 只输出变量名，可按只读任务批准"} {
		if !strings.Contains(got, s) {
			t.Errorf("只读政策段缺 %q", s)
		}
	}
	approved := strings.Index(got, "compgen -e 只输出变量名，可按只读任务批准")
	credentialList := strings.Index(got, "凭据清单（读取或执行其中任何一项即升级）")
	if approved < 0 || credentialList < 0 || approved > credentialList {
		t.Fatalf("compgen names-only approval must appear outside and before credential-list escalation:\n%s", got)
	}
}

func TestB469PromptDistinguishesMutationFromVerifiedReadOnlyHelp(t *testing.T) {
	got := approverPrompt("summary", "Bash: help", "N0NCE", ApproverTaskContext{Mode: proto.TaskModeReadonly})
	for _, phrase := range []string{
		"涉及实际调用 handoff 自身 CLI 执行变更操作的请求（如 dispatch/continue/done/reply/run），一律升级给上级协调者",
		"已验证的 Handoff CLI 本体纯 --help 只展示帮助，不会执行对应子命令，包括 upgrade --help（只查看升级帮助、不执行升级）",
		"实际执行 upgrade、二进制替换或更新仍必须升级",
		"未验证脚本（如 deploy.sh --help）仍按脚本执行审批，不因 --help 放行",
		"权限请求含内联代码或脚本（python -c / heredoc、psql -c、rg --pre）或 git checkout/restore -- 时，读完正文再裁决：只在任务仓库内读写文件、跑测试时批准；写库、联网、改仓库外路径、读密钥的，一律升级。",
	} {
		if !strings.Contains(got, phrase) {
			t.Errorf("assembled readonly prompt missing %q", phrase)
		}
	}
	if strings.Contains(got, "涉及调用 handoff 自身 CLI 变更任务状态的操作") {
		t.Fatal("the unconditional self-command sentence still overrides verified readonly help")
	}
}
