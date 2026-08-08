// agentd 命令测试：HTTP server 超时配置（P1-3）。
//
// 覆盖：newAgentdHTTPServer 的四个超时字段全部非零——这是「防 slowloris / 防
// 半死连接挂起」的配置级守卫。http.Server 超时行为本身由 net/http 保证，httptest
// 用自己的 server 无法覆盖，故只做配置存在性断言（why 见 P1-3 修法）。
package cmd

import (
	"net/http"
	"testing"
)

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
	if s.IdleTimeout <= 0 {
		t.Errorf("IdleTimeout 必须非零（keep-alive 空闲回收），实际 %v", s.IdleTimeout)
	}
}
