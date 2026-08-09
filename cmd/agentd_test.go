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
	"log/slog"
	"net/http"
	"testing"

	"github.com/xushixin/handoff/internal/agentd"
)

// 注册表必须认识全部执行者名：dispatch --executor <name> 的路由前提。
//
// 为什么每个名字都要断言而不是只断言数量：B2（claude）与 B3（grok）是并行开发的
// 两条分支，各自往注册表里加了一行，合并时 cmd/agentd.go 这一处必然冲突——手工
// 解冲突时漏掉任一行都不会编译报错，症状要拖到「派发时报未注册」才暴露。
func TestAdapterRegistryHasAllExecutors(t *testing.T) {
	ads := defaultAdapters(slog.Default())
	for _, want := range []string{"opencode", "claude", "grok", "fake"} {
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
