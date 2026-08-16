// 本文件覆盖 B64 的判据层：classify 是两个消费方（renderCheckRow / process）
// 唯一的结论来源，优先级只在它里面定义一次。
package cmd

import (
	"errors"
	"testing"

	"github.com/Xsxdot/handoff/internal/client"
)

func boolPtr(b bool) *bool { return &b }

func TestClassify(t *testing.T) {
	const latest = "v0.1.1"
	cases := []struct {
		name string
		ms   machineState
		want verdict
	}{
		{
			name: "远端够不着：版本无从得知，其余判据一概不成立",
			ms:   machineState{Ep: Endpoint{Name: "devbox"}, Err: errors.New("dial tcp: connection refused")},
			want: verdictUnreachable,
		},
		{
			name: "本机 agentd 未运行：不是失败，敲命令的人知道要不要起回来",
			ms:   machineState{Ep: Endpoint{Name: "本机", Local: true}, Bin: "v0.1.0", Err: client.ErrStatusUnsupported},
			want: verdictAgentdDown,
		},
		{
			name: "本机 agentd 未运行但二进制已最新：没事可做，不该重下重换",
			ms:   machineState{Ep: Endpoint{Name: "本机", Local: true}, Bin: latest, Err: errors.New("connection refused")},
			want: verdictLatest,
		},
		{
			name: "远端过旧未上报平台：排在托管判定之前",
			ms:   machineState{Ep: Endpoint{Name: "devbox"}, Agentd: "v0.1.0", Platform: ""},
			want: verdictTooOld,
		},
		{
			name: "远端过旧且未上报托管：仍报过旧，不得报非托管（B64 原始症状）",
			ms:   machineState{Ep: Endpoint{Name: "devbox"}, Agentd: "v0.1.0", Platform: "", Managed: nil},
			want: verdictTooOld,
		},
		{
			name: "远端非托管但已是最新：没事可做，不该催人装 service",
			ms: machineState{Ep: Endpoint{Name: "devbox"}, Agentd: latest,
				Platform: "linux/amd64", Managed: boolPtr(false)},
			want: verdictLatest,
		},
		{
			name: "远端有活跃任务但已是最新：busy 不参与判据，只在 needsUpgrade 后成为闸",
			ms: machineState{Ep: Endpoint{Name: "devbox"}, Agentd: latest,
				Platform: "linux/amd64", Managed: boolPtr(true), Busy: 3},
			want: verdictLatest,
		},
		{
			name: "远端明确上报非托管且落后：换完没人拉起，硬拒",
			ms: machineState{Ep: Endpoint{Name: "devbox"}, Agentd: "v0.1.1-rc", Platform: "linux/amd64",
				Managed: boolPtr(false)},
			want: verdictUnmanaged,
		},
		{
			name: "上报了平台却没上报托管：不知道就是不知道，不猜",
			ms: machineState{Ep: Endpoint{Name: "devbox"}, Agentd: "v0.1.1-rc", Platform: "linux/amd64",
				Managed: nil},
			want: verdictManagedUnknown,
		},
		{
			name: "远端托管且落后：正常升级路径",
			ms: machineState{Ep: Endpoint{Name: "devbox"}, Agentd: "v0.1.1-rc", Platform: "linux/amd64",
				Managed: boolPtr(true)},
			want: verdictNeedsUpgrade,
		},
		{
			name: "本机落后：二进制与 agentd 都要对齐才算最新",
			ms: machineState{Ep: Endpoint{Name: "本机", Local: true}, Bin: latest, Agentd: "v0.1.0",
				Platform: "darwin/arm64", Managed: boolPtr(true)},
			want: verdictNeedsUpgrade,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classify(&c.ms, latest); got != c.want {
				t.Errorf("classify = %s，期望 %s", got, c.want)
			}
		})
	}
}
