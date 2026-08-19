//go:build integration

// 本文件是跨仓集成测试骨架。它需要同一环境中的 handoff-server 二进制，
// 由审核者在两仓齐全时按 CONTRIBUTING.md 的说明执行；普通 go test 不会编译它。
package relay

import "testing"

// TestRelayIntegrationAgainstHandoffServer 验证真实 relay 控制面、两层 mux、
// E2E 会话与 agentd HTTP 端点的完整链路。审核环境负责提供临时数据库、master key
// 和 handoff-server admin API；测试应创建账户及 register/connect 凭证，启动真实
// agentd，再通过 relay target 调用只读 API，并断言 relay 计量 used_bytes > 0。
func TestRelayIntegrationAgainstHandoffServer(t *testing.T) {
	t.Skip("reviewer-only: requires a handoff-server binary and its admin API")
}
