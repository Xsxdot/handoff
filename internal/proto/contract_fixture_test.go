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
		{"Frame", frameSample(now)},
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
		Machine:         "",
		ProjectID:       "a1b2c3d4e5f60718",
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
// 两台覆盖两种结局：本机（name 空串、probe_ms 恒 0）与不可达的远端
// （reachable=false + error 带原文，且仍然出现在列表里——缺席必须可见）。
func machinesSample() MachinesResp {
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
		},
	}
}

// tasksRespSample 返回 TasksResp（?scope=all）的代表性样本。
//
// 一台成功一台失败：失败那台照样占一行且带原文，这正是 §5.3 的硬约束。
// tasks 里给一条本机任务（machine 空串）——远端条目形状同构，只是 machine
// 由汇总方盖上 target 名。
func tasksRespSample(now time.Time, taskID string) TasksResp {
	return TasksResp{
		Machines: []MachineStatus{
			{Name: "", Ok: true, FetchedAt: now, Error: ""},
			{Name: "devbox", Ok: false, FetchedAt: now,
				Error: "dial tcp 10.0.0.8:7777: connect: connection refused"},
		},
		Tasks: []TaskView{{Task: taskSample(now, taskID), Watchers: 1}},
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
