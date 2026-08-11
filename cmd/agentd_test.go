// agentd 命令测试：HTTP server 超时配置（P1-3）。
//
// 覆盖：newAgentdHTTPServer 的四个超时字段全部非零——这是「防 slowloris / 防
// 半死连接挂起」的配置级守卫；另断言 WriteTimeout ≥ agentd.RunCmdTimeout——
// handleTaskRun 同步执行 RunCmd，写超时小于命令执行上限会把长审阅命令掐断
// （退出码 124 契约无法兑现，见 cmd/agentd.go newAgentdHTTPServer 注释）。
// http.Server 超时行为本身由 net/http 保证，httptest 用自己的 server 无法覆盖，
// 故只做配置存在性断言（why 见 P1-3 修法）。
package cmd

import (
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/xushixin/handoff/internal/agentd"
	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/store"
)

// 注册表必须认识全部执行者名：dispatch --executor <name> 的路由前提。
//
// 为什么每个名字都要断言而不是只断言数量：B2（claude）与 B3（grok）是并行开发的
// 两条分支，各自往注册表里加了一行，合并时 cmd/agentd.go 这一处必然冲突——手工
// 解冲突时漏掉任一行都不会编译报错，症状要拖到「派发时报未注册」才暴露。
func TestAdapterRegistryHasAllExecutors(t *testing.T) {
	ads := defaultAdapters(slog.Default())
	for _, want := range []string{"opencode", "claude", "grok", "codex", "fake"} {
		if _, ok := ads[want]; !ok {
			names := make([]string, 0, len(ads))
			for n := range ads {
				names = append(names, n)
			}
			t.Fatalf("adapter 注册表缺 %s，实际注册: %v", want, names)
		}
	}
}

func TestNewAgentdHTTPServerTimeouts(t *testing.T) {
	s := newAgentdHTTPServer("127.0.0.1:0", http.NewServeMux())
	if s.Addr != "127.0.0.1:0" {
		t.Errorf("Addr=%q, want 127.0.0.1:0", s.Addr)
	}
	if s.ReadHeaderTimeout <= 0 {
		t.Errorf("ReadHeaderTimeout 必须非零（防 slowloris），实际 %v", s.ReadHeaderTimeout)
	}
	if s.ReadTimeout <= 0 {
		t.Errorf("ReadTimeout 必须非零（请求体读取上限），实际 %v", s.ReadTimeout)
	}
	if s.WriteTimeout <= 0 {
		t.Errorf("WriteTimeout 必须非零（响应写入上限），实际 %v", s.WriteTimeout)
	}
	if s.WriteTimeout < agentd.RunCmdTimeout {
		t.Errorf("WriteTimeout %v 必须 >= run 路由执行上限 %v（否则长审阅命令被掐断）",
			s.WriteTimeout, agentd.RunCmdTimeout)
	}
	if s.IdleTimeout <= 0 {
		t.Errorf("IdleTimeout 必须非零（keep-alive 空闲回收），实际 %v", s.IdleTimeout)
	}
}

// 空闲判据必须严格按 D12：只有 running 与 waiting_answer 算忙。
//
// why 逐态钉住：把 waiting_review 算进去，一个挂了三天等审核的任务会让升级
// 无限期阻塞；把 running 漏掉，换版会中断正在跑的执行者。两个方向都错得很贵。
func TestActiveTaskCountCountsOnlyRunningAndWaitingAnswer(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	states := []proto.TaskState{
		proto.TaskStatePending,
		proto.TaskStateRunning,
		proto.TaskStateWaitingAnswer,
		proto.TaskStateWaitingReview,
		proto.TaskStateCompleted,
		proto.TaskStateFailed,
	}
	for i, s := range states {
		task := &proto.Task{
			ID:       fmt.Sprintf("00000000-0000-0000-0000-00000000000%d", i),
			Name:     string(s),
			RepoPath: dir,
			State:    proto.TaskStatePending,
		}
		if err := st.CreateTask(task); err != nil {
			t.Fatalf("CreateTask(%s): %v", s, err)
		}
		if s == proto.TaskStatePending {
			continue // 创建时已是 pending，保持原状
		}
		if s == proto.TaskStateFailed {
			// failed 可直达（transitTable: pending→failed）
			if err := st.UpdateTaskState(task.ID, s); err != nil {
				t.Fatalf("UpdateTaskState(%s): %v", s, err)
			}
			continue
		}
		// 其余状态不能从 pending 直达（transitTable），先 running 再一步到位；
		// running 本身由第一步到位，不再重复迁
		if err := st.UpdateTaskState(task.ID, proto.TaskStateRunning); err != nil {
			t.Fatalf("UpdateTaskState(running) for %s: %v", s, err)
		}
		if s != proto.TaskStateRunning {
			if err := st.UpdateTaskState(task.ID, s); err != nil {
				t.Fatalf("UpdateTaskState(%s): %v", s, err)
			}
		}
	}

	n, err := activeTaskCount(st)
	if err != nil {
		t.Fatalf("activeTaskCount: %v", err)
	}
	if n != 2 {
		t.Fatalf("活跃任务数=%d，期望 2（只有 running 与 waiting_answer）", n)
	}
}
