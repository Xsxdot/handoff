// 本文件锁死升级前探测对 relay 机器的行为。
//
// 注意边界：这里只罩「选路走对了」，不罩「推 tar.gz 大包能不能过 yamux」——
// 后者要真 relay 环境，列在 spec §6 的真机验收里。
package agentd

import (
	"context"
	"strings"
	"testing"
)

// TestMachineUpgradeProbeRelayNoHost：升级前探测不再对 relay 机器报 no Host。
func TestMachineUpgradeProbeRelayNoHost(t *testing.T) {
	s := relayOnlyServer(t)
	c, err := s.pool.For("linux-01")
	if err != nil {
		t.Fatalf("取客户端失败: %v", err)
	}
	_, statusErr := c.Status(context.Background())
	if statusErr == nil {
		t.Skip("拨到了真 relay，本用例只在拨不通时有意义")
	}
	if strings.Contains(statusErr.Error(), "no Host in request URL") {
		t.Fatalf("relay 机器不该报 no Host：%v", statusErr)
	}
}
