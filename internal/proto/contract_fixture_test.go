// contract_fixture_test.go —— 契约 fixture 生成与断言。
//
// 职责：
//   - 把本包所有对外类型的代表性样本 json.Marshal 成 JSON，写进
//     web/src/api/testdata/<类型名>.json，供前端 vitest 与 Go 侧共同钉住线格式
//   - **断言生成结果与已存文件逐字节一致**：字段改名、类型变化、新增字段、
//     omitempty/时间格式变化都会让本测试当场变红
//   - 提供显式更新方式：go test ./internal/proto/ -run TestContractFixtures -update
//     重写全部 fixture（不覆盖则绝不悄悄改文件）
//
// 为什么选「断言序列化产物」而不是 JSON schema / 代码生成器：前后端真正会
// 对不上的恰恰是序列化结果而非结构体定义——omitempty 缺键、time.Time 的
// RFC3339 格式、指针字段是 null 还是缺席，只有把实际 JSON 钉下来才有人报警。
//
// 边界：
//   - 只覆盖 internal/proto 导出的类型；agentd 包内的局部 struct（如
//     taskDetail/replyResult）不属于本契约，由 agentd 侧测试自行断言
//   - 样本值是固定值而非 time.Now()，保证 fixture 可重复、可入 git
package proto

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// updateFixtures 以 -update 显式开启「重写 fixture」模式。
//
// 为什么默认关：fixture 是契约的快照，任何人改结构体都该先看到测试变红，
// 而不是被悄悄覆盖——「显式更新」是让契约变化可见的机制，不是便利开关。
var updateFixtures = flag.Bool("update", false, "重写 web/src/api/testdata/*.json 契约 fixture")

// fixtureDir 返回 web/src/api/testdata 的绝对路径。
//
// 为什么用 runtime.Caller 定位而非相对 cwd：go test 的 cwd 通常是包目录，
// 但 -C 或外部工具（如 goland）可能从别处运行；按本文件位置推导根目录
// 无论从哪启动都稳定。
func fixtureDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("无法定位测试文件自身路径")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "web", "src", "api", "testdata")
}

// TestContractFixtures 为每个对外类型生成 fixture 并与已存文件逐字节比对；
// 传 -update 时改为重写。
//
// 注意：断言「逐字节一致」意味着换 Go 版本导致的时间格式变化也会变红——
// 这正是本测试的目的（时间格式是前后端契约的一部分），需要时用 -update
// 显式刷新并随提交一并 review 差异。
func TestContractFixtures(t *testing.T) {
	now := time.Date(2026, 8, 11, 10, 30, 0, 0, time.FixedZone("CST", 8*3600))
	taskID := "7ec762e7-3bd2-412c-a39c-e4cf8b4057ad"

	cases := []struct {
		name   string
		sample any
	}{
		{"Task", taskSample(now, taskID)},
		{"Event", eventSample(now, taskID)},
		{"Ticket", ticketSample(now, taskID)},
		{"ProjectLocation", projectLocationSample(now)},
		{"ProjectTreeResp", projectTreeSample()},
		{"MachinesResp", machinesSample()},
		{"TasksResp", tasksRespSample(now, taskID)},
		{"AuthTicketResp", authTicketSample(now)},
		{"SessionInfo", sessionSample(now)},
		{"BuildInfo", buildSample()},
		{"ActiveTask", activeTaskSample(taskID)},
		{"StatusResp", statusSample(now, taskID)},
		{"PtySession", ptySessionSample(now)},
		{"PtySessionsResp", ptySessionsRespSample(now)},
		{"Frame", frameSample(now)},
		{"DirListResult", dirListSample()},
		{"TaskPlan", taskPlanSample()},
		{"FileRead", fileReadSample()},
		{"FileWriteReq", fileWriteReqSample()},
		{"FileWriteResp", fileWriteRespSample()},
		{"FileConflictResp", fileConflictSample()},
		{"ProjectBranchesResp", projectBranchesSample()},
		{"CreateWorktreeReq", createWorktreeReqSample()},
		{"DisciplineResp", disciplineRespSample()},
		{"DisciplineMappingReq", disciplineMappingReqSample()},
		{"EnvResp", envRespSample()},
		{"EnvKeysResp", envKeysRespSample()},
		{"EnvMappingReq", envMappingReqSample()},
		{"ExecutorDefaultResp", executorDefaultRespSample()},
		{"ExecutorDefaultReq", executorDefaultReqSample()},
		{"WorkbenchBase", workbenchBaseSample()},
		{"WorkbenchStateResp", workbenchStateRespSample()},
		{"WorkbenchBaseReq", workbenchBaseReqSample()},
		{"WorkbenchSelectedReq", workbenchSelectedReqSample()},
		{"WorkbenchDockReq", workbenchDockReqSample()},
		{"NewCardReq", NewCardReq{Title: "需要定性", Project: "handoff"}},
		{"CardCreateResp", CardCreateResp{ID: "B167"}},
		{"MigrateCardReq", MigrateCardReq{Workflow: "domain", Status: "拆解"}},
		{"MigrateCardResp", MigrateCardResp{
			OK: true, ID: "B167",
			From: CardWorkflowLocation{ID: "B167", Workflow: "bug", WorkflowVersion: 1, Status: "进行中"},
			To:   CardWorkflowLocation{ID: "B167", Workflow: "domain", WorkflowVersion: 1, Status: "拆解"},
			Event: LedgerEvent{Seq: 9, CardID: "B167", Type: "workflow_migrated", Actor: "web:127.0.0.1",
				Payload: json.RawMessage(`{"from_workflow":"bug","from_version":1,"from_status":"进行中","to_workflow":"domain","to_version":1,"to_status":"拆解"}`), CreatedAt: now},
		}},
		{"LedgerEvent", LedgerEvent{Seq: 9, CardID: "B167", Type: "workflow_migrated", Actor: "web:127.0.0.1",
			Payload: json.RawMessage(`{"from_workflow":"bug","from_version":1,"from_status":"进行中","to_workflow":"domain","to_version":1,"to_status":"拆解"}`), CreatedAt: now}},
		{"CardView", CardView{ID: "B167", Title: "需要定性", Status: "待办", Priority: "中", Project: "handoff", Workflow: "triage", Parent: "", BaseBranch: "main",
			Attachments: []Attachment{{Kind: "spec", Path: "specs/example.md"}}, BlockedBy: []string{}, Following: "", Needs: "", Blocked: false, MergedCount: 0, OpenDecisions: 0, ChildrenTotal: 0, ChildrenDone: 0, Conflict: false, OpenTickets: 0}},
		{"CardDetail", CardDetail{Card: Card{ID: "B167", Title: "需要定性", Status: "待办", Priority: "中", Project: "handoff", WorkflowName: "triage", WorkflowVersion: 1}, Relations: []Relation{}, Events: []LedgerEvent{}, TaskStates: []TaskStateRow{}, EffectiveBaseBranch: "main", Decisions: []Decision{}, Needs: "", Children: []CardBrief{}}},
		{"NodeDef", NodeDef{Name: "定性中", Next: "已定性"}},
		{"FlowDetail", FlowDetail{Name: "triage", Version: 1, Nodes: []NodeDef{{Name: "待办", Next: "定性中"}, {Name: "定性中", Next: "已定性"}, {Name: "已定性"}}, States: []string{"待办", "定性中", "已定性"}}},
	}

	dir := fixtureDir(t)
	if *updateFixtures {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("创建 fixture 目录: %v", err)
		}
	}
	for _, c := range cases {
		data, err := json.MarshalIndent(c.sample, "", "  ")
		if err != nil {
			t.Fatalf("marshal %s: %v", c.name, err)
		}
		data = append(data, '\n')

		path := filepath.Join(dir, c.name+".json")
		if *updateFixtures {
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatalf("写 fixture %s: %v", path, err)
			}
			t.Logf("已重写 %s", path)
			continue
		}
		stored, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s: 读取 fixture %s 失败（契约尚未生成？请运行 -update）：%v", c.name, path, err)
			continue
		}
		if !bytes.Equal(stored, data) {
			t.Errorf("%s: 序列化结果与 fixture 不一致（契约已漂移，如需接受变更请用 -update）：\n--- 期望(已存) ---\n%s\n--- 实际(现生成) ---\n%s", c.name, stored, data)
		}
	}
}

// workbenchBaseSample 是一行基准状态的代表性样本。
func workbenchBaseSample() WorkbenchBase {
	return WorkbenchBase{
		BaseKey:   "/Users/dev/repo@linux-01",
		Payload:   `{"v":1,"base":{"kind":"workspace"},"wb":{"active":0}}`,
		UpdatedAt: 1755648000000,
	}
}

// workbenchStateRespSample 覆盖三个字段同时有值的情形。
func workbenchStateRespSample() WorkbenchStateResp {
	return WorkbenchStateResp{
		Selected: "/Users/dev/repo@linux-01",
		Dock:     `{"v":1,"windowOpen":true}`,
		Bases:    []WorkbenchBase{workbenchBaseSample()},
	}
}

// workbenchBaseReqSample 取「有 payload」那一支；null 那一支由 agentd 侧用例覆盖。
func workbenchBaseReqSample() WorkbenchBaseReq {
	p := `{"v":1}`
	return WorkbenchBaseReq{BaseKey: "/Users/dev/repo", Payload: &p}
}

func workbenchSelectedReqSample() WorkbenchSelectedReq {
	return WorkbenchSelectedReq{BaseKey: "/Users/dev/repo"}
}

func workbenchDockReqSample() WorkbenchDockReq {
	p := `{"v":1,"tabs":[]}`
	return WorkbenchDockReq{Payload: &p}
}

// buildSample 返回 BuildInfo 的代表性样本（release 构建：Version 与 Revision 都有）。
func buildSample() BuildInfo {
	return BuildInfo{
		Version:  "v0.1.0",
		Revision: "482aab1f9e12a3b4c5d6e7f8a9b0c1d2e3f4a5b6",
		Time:     "2026-08-11T10:00:00Z",
		Modified: false,
		Go:       "go1.26.1",
	}
}

// taskSample 返回 Task 的代表性样本（running 的 managed-worktree 任务）。
//
// sampleCtxWindow 是 fixture 用的窗口上限；单独取变量只因为 Go 不能对字面量取址。
var sampleCtxWindow = 258400

func taskSample(now time.Time, taskID string) Task {
	return Task{
		ID:              taskID,
		Target:          "opencode",
		RepoPath:        "/home/dev/handoff",
		Branch:          "handoff/w1-web-scaffold",
		PlanPath:        ".handoff/plans/w1.md",
		PlanSummary:     "W1：Web 控制台前端地基",
		ExecutorSession: "sess-01HX",
		State:           TaskStateRunning,
		CreatedAt:       now,
		UpdatedAt:       now.Add(3 * time.Minute),
		Name:            "W1 前端地基",
		Executor:        "opencode",
		Model:           "",
		WorkDir:         "/home/dev/handoff/.worktrees/w1",
		WorktreeManaged: true,
		BaseCommit:      "482aab1f9e12a3b4c5d6e7f8a9b0c1d2e3f4a5b6",
		BaseAhead:       1,
		RepoDirtyCount:  2,
		RepoDirtyFiles:  "web/package.json, internal/proto/proto.go 等 2 处",
		// B80：给非零值才能把线格式钉进 fixture（两个字段都带 omitempty）。
		// context_window 给值是为了钉住「有分母」的形状；无分母时该键缺席，
		// 由 web 侧的 Usage 可选字段与 TaskHeader 测试覆盖。
		ActualModel: "gpt-5.6-sol",
		Usage:       &Usage{ContextTokens: 24668, ContextWindow: &sampleCtxWindow},
		Machine:     "",
		ProjectID:   "a1b2c3d4e5f60718",
	}
}

// eventSample 返回 Event 的代表性样本（question 事件）。
func eventSample(now time.Time, taskID string) Event {
	return Event{
		Seq:       7,
		TaskID:    taskID,
		Type:      EventTypeQuestion,
		Payload:   json.RawMessage(`{"text":"继续吗"}`),
		CreatedAt: now,
	}
}

// ticketSample 返回 Ticket 的代表性样本（已回答且已送达的 gate 工单；
// 三个指针字段全部非 nil，钉住「存在时出现」的序列化结果）。
func ticketSample(now time.Time, taskID string) Ticket {
	answer := "allow"
	answeredAt := now.Add(5 * time.Minute)
	deliveredAt := now.Add(6 * time.Minute)
	return Ticket{
		ID:          "tk-1",
		TaskID:      taskID,
		Kind:        "gate",
		Request:     json.RawMessage(`{"cmd":"go build ./..."}`),
		Answer:      &answer,
		CreatedAt:   now,
		AnsweredAt:  &answeredAt,
		DeliveredAt: &deliveredAt,
	}
}

// projectLocationSample 返回 ProjectLocation 的代表性样本（登记有效）。
//
// 它同时是 POST /api/projects 的响应体形状：B62 登记成功返回 200 + 完整位置。
func projectLocationSample(now time.Time) ProjectLocation {
	return ProjectLocation{
		ProjectID: "a1b2c3d4e5f60718",
		Name:      "handoff",
		Path:      "/home/dev/handoff",
		OriginURL: "git@github.com:xushixin/handoff.git",
		CreatedAt: now,
		Status:    "有效",
	}
}

// projectTreeSample 返回 ProjectTreeResp 的代表性样本（**单机**形态）。
//
// 为什么样本必须是单机形态：单机响应里每个项目恒 0 或 1 个 location
// （ADR-0008 / W3a §1.1），前端契约测试正是拿这条不变式做断言。汇总形态
// （多 location + machines 栏）由 TasksResp 样本一并钉住 MachineStatus。
//
// 两个工作树覆盖两种形态：主工作树（人手 clone）与 agentd 自建的任务工作树。
func projectTreeSample() ProjectTreeResp {
	return ProjectTreeResp{
		Projects: []ProjectNode{{
			ProjectID: "a1b2c3d4e5f60718",
			OriginURL: "git@github.com:xushixin/handoff.git",
			Name:      "handoff",
			Locations: []ProjectLocationNode{{
				Machine: "",
				Name:    "handoff",
				Path:    "/home/dev/handoff",
				Workspaces: []Workspace{
					{
						Path:    "/home/dev/handoff",
						Branch:  "main",
						Head:    "482aab1",
						IsMain:  true,
						Managed: false,
					},
					{
						Path:    "/home/dev/.handoff/worktrees/w1",
						Branch:  "handoff/w1-web-scaffold",
						Head:    "9e12a3b",
						IsMain:  false,
						Managed: true,
					},
				},
				ProbeError: "",
			}},
		}},
		Unowned: []string{},
	}
}

// machinesSample 返回 MachinesResp 的代表性样本。
//
// 三台覆盖三种结局：本机（name 空串、probe_ms 恒 0、upgrade 恒缺席）、不可达的
// 远端（reachable=false + error 带原文，且仍然出现在列表里——缺席必须可见），
// 以及**升级失败过**的远端——upgrade 段是控制台唯一的失败出口，线格式必须钉住。
func machinesSample() MachinesResp {
	ptyOK := true
	return MachinesResp{
		Machines: []Machine{
			{
				Name:            "",
				Addr:            "127.0.0.1:7777",
				Reachable:       true,
				Version:         "v0.1.0",
				Executors:       []string{"claude", "opencode"},
				DefaultExecutor: "opencode",
				ProbeMs:         0,
				ActiveTasks:     1,
				Error:           "",
				PtySupported:    &ptyOK,
			},
			{
				Name:            "devbox",
				Addr:            "10.0.0.8:7777",
				Reachable:       false,
				Version:         "",
				Executors:       []string{},
				DefaultExecutor: "",
				ProbeMs:         3000,
				ActiveTasks:     0,
				Error:           "dial tcp 10.0.0.8:7777: connect: connection refused",
			},
			{
				Name:            "mac-02",
				Addr:            "10.0.0.9:7777",
				Reachable:       true,
				Version:         "v0.3.1",
				Executors:       []string{"codex"},
				DefaultExecutor: "codex",
				ProbeMs:         12,
				ActiveTasks:     0,
				Error:           "",
				Upgrade: &MachineUpgrade{
					Status:  "fail",
					Verdict: "needs_upgrade",
					Reason:  "下载 checksums.txt: 尝试 3 次仍失败: i/o timeout",
				},
			},
		},
	}
}

// tasksRespSample 返回 TasksResp（?scope=all）的代表性样本。
//
// 一台成功一台失败：失败那台照样占一行且带原文，这正是 §5.3 的硬约束。
// tasks 里本机与远端各一条：远端那条的 machine 由汇总方盖上 target 名。
//
// 为什么样本里必须有远端条目而不能只留一条本机任务（曾经如此）：这个信封存在
// 的全部理由就是「让跨机任务在一份列表里出现」，而 machine 章是消费方分辨它们
// 的唯一依据。样本里没有远端条目，前端契约测试就无从断言这一栏，跨机任务被整片
// 漏掉时没有任何一层会变红——控制台看板就这样空了一整轮。
func tasksRespSample(now time.Time, taskID string) TasksResp {
	remote := taskSample(now, "9c1f0b47-2f5a-4a6e-8f3b-5d7c1e2a4b90")
	remote.Machine = "devbox"
	remote.State = "waiting_review"
	remote.Name = "B12 远端派发"
	return TasksResp{
		Machines: []MachineStatus{
			{Name: "", Ok: true, FetchedAt: now, Error: ""},
			{Name: "devbox", Ok: false, FetchedAt: now,
				Error: "dial tcp 10.0.0.8:7777: connect: connection refused"},
		},
		Tasks: []TaskView{
			{Task: taskSample(now, taskID), Watchers: 1},
			{Task: remote, Watchers: 0},
		},
	}
}

// authTicketSample 返回 AuthTicketResp 的代表性样本。
func authTicketSample(now time.Time) AuthTicketResp {
	return AuthTicketResp{
		URL:       "http://127.0.0.1:7777/console?ticket=abc123",
		ExpiresAt: now.Add(60 * time.Second),
	}
}

// sessionSample 返回 SessionInfo 的代表性样本（已吊销会话：revoked_at 存在）。
func sessionSample(now time.Time) SessionInfo {
	revoked := now.Add(24 * time.Hour)
	return SessionInfo{
		ID:         "sess-01HX",
		DeviceName: "xushixin-mbp / Chrome",
		CreatedAt:  now,
		ExpiresAt:  now.Add(30 * 24 * time.Hour),
		LastSeenAt: now.Add(1 * time.Hour),
		RevokedAt:  &revoked,
	}
}

// activeTaskSample 返回 ActiveTask 的代表性样本（探活为 alive，note 为空）。
func activeTaskSample(taskID string) ActiveTask {
	return ActiveTask{
		ID:       taskID,
		Name:     "W1 前端地基",
		State:    "running",
		Executor: "opencode",
		RepoPath: "/home/dev/handoff",
		Live:     LiveAlive,
		Note:     "",
	}
}

// statusSample 返回 StatusResp 的代表性样本。
//
// 注意 TaskCounts 六个状态键恒存在：键缺了要能当场暴露（0 与缺键对消费方是两回事）。
func statusSample(now time.Time, taskID string) StatusResp {
	ptyOK := true
	revealOK := true
	return StatusResp{
		Version:         buildSample(),
		Listen:          "127.0.0.1:7777",
		DataDir:         "/home/dev/.handoff",
		StartedAt:       now,
		Executors:       []string{"claude", "opencode"},
		DefaultExecutor: "opencode",
		TaskCounts: map[string]int{
			"pending":        0,
			"running":        1,
			"waiting_answer": 0,
			"waiting_review": 0,
			"completed":      3,
			"failed":         0,
		},
		Active: []ActiveTask{activeTaskSample(taskID)},
		// 放在 Active 之后：能力位与运行时数据分开，一眼能看出它是 agentd 上报的。
		PtySupported:    &ptyOK,
		RevealSupported: &revealOK,
	}
}

// ptySessionSample 返回 PtySession 的代表性样本（活着的会话：exit_code 缺席）。
func ptySessionSample(now time.Time) PtySession {
	return PtySession{
		ID:         "2f0f6a3c-8f1e-4f2a-9a77-1c2d3e4f5a6b",
		Machine:    "",
		BasePath:   "/home/dev/handoff",
		BaseKind:   "workspace",
		Shell:      "/bin/zsh",
		CreatedAt:  now,
		Cols:       120,
		Rows:       40,
		Attached:   1,
		Foreground: true,
		PID:        48213,
		BytesOut:   81920,
	}
}

// ptySessionsRespSample 覆盖 scope=all 信封：一条本机活会话 + 一条远端已退出会话
// （exit_code 出现），外加两行机器应答。
func ptySessionsRespSample(now time.Time) PtySessionsResp {
	code := 3
	remote := ptySessionSample(now)
	remote.ID = "9b8a7c6d-5e4f-4a3b-2c1d-0e9f8a7b6c5d"
	remote.Machine = "devbox"
	remote.Attached = 0
	remote.ExitCode = &code
	remote.Foreground = false
	return PtySessionsResp{
		Sessions: []PtySession{ptySessionSample(now), remote},
		Machines: []MachineStatus{
			{Name: "", Ok: true, FetchedAt: now},
			{Name: "devbox", Ok: true, FetchedAt: now},
		},
	}
}

// frameSample 返回 Frame 的代表性样本（被截断的 tool_result）。
//
// 为什么选 tool_result 而不是 text：它是字段最多的一种帧，能同时钉住
// Part/Status/Output/Truncated/Bytes 五个字段的序列化结果；text 帧只有
// Part+Delta，钉不住 omitempty 的边界。
func frameSample(now time.Time) Frame {
	return Frame{
		Seq:       42,
		TS:        now,
		Turn:      2,
		Type:      FrameToolResult,
		Part:      "toolu_01ABCdefGHIjklMNOpqrs",
		Status:    "error",
		Output:    "go: downloading …\n…（已截断）…\nFAIL\texit status 1",
		Truncated: true,
		Bytes:     193422,
	}
}

// dirListSample 返回 DirListResult 的代表性样本。
//
// 一目录一文件覆盖 Size 的 omitempty 边界：目录不带 size 键，普通文件带。
// 第三项是被 .gitignore 排除的文件，覆盖 Ignored 的 true 分支——未忽略的两项
// 同时钉住「false 被省略成缺键」这条契约。
func dirListSample() DirListResult {
	return DirListResult{
		Entries: []DirEntry{
			{Name: "internal", IsDir: true},
			{Name: "go.mod", IsDir: false, Size: 1284},
			{Name: "coverage.out", IsDir: false, Size: 40960, Ignored: true},
		},
	}
}

// taskPlanSample 返回 TaskPlan 的代表性样本（未截断：truncated 键被省略）。
func taskPlanSample() TaskPlan {
	return TaskPlan{
		Name:    "b119-dispatch.md",
		Content: "# 执行纪律\n\n按 plan 逐 task 实现。\n",
		Size:    36,
	}
}

// fileReadSample 返回 FileRead 的代表性样本（可编辑文本：非截断非二进制，有 sha256）。
func fileReadSample() FileRead {
	return FileRead{
		Content: "module handoff\n\ngo 1.26.1\n",
		Size:    29,
		SHA256:  "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
	}
}

// fileWriteReqSample 返回 FileWriteReq 的代表性样本（带 base_sha256）。
func fileWriteReqSample() FileWriteReq {
	return FileWriteReq{
		Content:    "module handoff\n\ngo 1.26.1\n",
		BaseSHA256: "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
	}
}

// fileWriteRespSample 返回 FileWriteResp 的代表性样本。
func fileWriteRespSample() FileWriteResp {
	return FileWriteResp{
		SHA256: "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
		Size:   29,
	}
}

// fileConflictSample 返回 FileConflictResp 的代表性样本（带磁盘现状 current）。
func fileConflictSample() FileConflictResp {
	return FileConflictResp{
		Error: "文件已被改动",
		Current: FileRead{
			Content: "module handoff\n",
			Size:    15,
			SHA256:  "8b1a9953c4611296a827abf8c47804d7",
		},
	}
}

// projectBranchesSample 是分支列表响应的契约样本：一条被占用、一条空闲。
func projectBranchesSample() ProjectBranchesResp {
	return ProjectBranchesResp{
		Branches: []ProjectBranch{
			{Name: "main", Worktree: "/Users/dev/code/handoff"},
			{Name: "feat/b114-sidebar-prefs", Worktree: ""},
		},
		Default:      "main",
		WorktreeRoot: "/Users/dev/.handoff/worktrees/manual",
	}
}

// createWorktreeReqSample 是建树请求的契约样本。
func createWorktreeReqSample() CreateWorktreeReq {
	return CreateWorktreeReq{Mode: "new_branch", Branch: "feat/b114-sidebar-prefs", Base: "main"}
}

// disciplineRespSample 返回 DisciplineResp 的代表性样本：三档各出现一次
// （default / file / off），并含一个未注册但配置里有的 executor。
func disciplineRespSample() DisciplineResp {
	return DisciplineResp{
		Dir: "/Users/dev/.handoff/discipline",
		Builtins: []DisciplineBuiltin{
			{Tier: "subagent", Content: "# 执行纪律（先读这段，再读 plan）\n\n1. 逐 task 派全新 subagent 实现。\n"},
			{Tier: "single-context", Content: "# 执行纪律（先读这段，再读 plan）\n\n1. 在本会话内自己逐 task 实现。\n"},
		},
		Files: []DisciplineFile{
			{Name: "codex-strict.md", Size: 128,
				SHA256: "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"},
		},
		Bindings: []DisciplineBinding{
			{Executor: "codex", Mode: "file", File: "codex-strict.md", DefaultTier: "single-context"},
			{Executor: "grok", Mode: "off", DefaultTier: "single-context"},
			{Executor: "opencode", Mode: "default", DefaultTier: "subagent"},
		},
	}
}

// disciplineMappingReqSample 返回 DisciplineMappingReq 的代表性样本。
func disciplineMappingReqSample() DisciplineMappingReq {
	return DisciplineMappingReq{
		Bindings: []DisciplineBinding{
			{Executor: "codex", Mode: "file", File: "codex-strict.md", DefaultTier: "single-context"},
			{Executor: "opencode", Mode: "default", DefaultTier: "subagent"},
		},
	}
}

// envRespSample 返回 EnvResp 的代表性样本：两档各出现一次（off / file），
// 并含一个未注册但配置里有的 executor。
func envRespSample() EnvResp {
	return EnvResp{
		Dir: "/Users/dev/.handoff/env",
		Files: []EnvFile{
			{Name: "proxy.env", Size: 64,
				SHA256: "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"},
		},
		Bindings: []EnvBinding{
			{Executor: "codex", Mode: "file", File: "proxy.env"},
			{Executor: "opencode", Mode: "off"},
		},
	}
}

// envKeysRespSample 返回 EnvKeysResp 的代表性样本：一条普通、一条重复、
// 一条空值（ValueBytes=0，omitempty 不适用于它，必须仍然出现在 JSON 里）。
func envKeysRespSample() EnvKeysResp {
	return EnvKeysResp{Keys: []EnvKey{
		{Key: "HTTPS_PROXY", ValueBytes: 34},
		{Key: "GOPROXY", ValueBytes: 21, Duplicate: true},
		{Key: "EMPTY_ONE", ValueBytes: 0},
	}}
}

// envMappingReqSample 返回 EnvMappingReq 的代表性样本。
func envMappingReqSample() EnvMappingReq {
	return EnvMappingReq{Bindings: []EnvBinding{
		{Executor: "codex", Mode: "file", File: "proxy.env"},
		{Executor: "opencode", Mode: "off"},
	}}
}

// executorDefaultRespSample 返回 ExecutorDefaultResp 的代表性样本。
func executorDefaultRespSample() ExecutorDefaultResp {
	return ExecutorDefaultResp{
		Default:   "opencode",
		Model:     "opencode-go/deepseek-v4-flash",
		Available: []string{"claude", "codex", "fake", "grok", "opencode"},
	}
}

// executorDefaultReqSample 返回 ExecutorDefaultReq 的代表性样本：
// model 刻意给空串——「不设默认模型」是常态取值，必须在线格式里出现过一次。
func executorDefaultReqSample() ExecutorDefaultReq {
	return ExecutorDefaultReq{Default: "codex", Model: ""}
}
