package agentd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/hostapi"
	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

// TestCoordinatorSessionRefResolverEmptyHomeOnlineCarrier 锁住已上线载体空 HOME 恢复接缝：
// 当已上线载体登记 HOME 为空时，作为合法主 HOME 展开为绝对路径；小队中没有 online 载体时依然报错。
func TestCoordinatorSessionRefResolverEmptyHomeOnlineCarrier(t *testing.T) {
	env, _ := newCoordEnv(t)
	svc := mustScheduling(t, env.srv)

	putOnlineCarrier(t, svc, scheduling.Carrier{
		Name:       "c1",
		Machine:    "本机",
		CLI:        "opencode",
		HomeDir:    "",
		Credential: scheduling.CredentialStandalone,
		Status:     scheduling.StatusOnline,
	})
	if err := svc.PutSquad(scheduling.Squad{
		Name:    "coord",
		Role:    scheduling.RoleCoordinator,
		Members: []scheduling.SquadMember{{Carrier: "c1", MaxConcurrency: 1}},
	}, 0); err != nil {
		t.Fatalf("登记协调者小队: %v", err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("无法读取当前用户主目录: %v", err)
	}
	wantHome := filepath.Clean(home)

	resolver := coordinatorSessionRefResolver{server: env.srv, expandHomeDir: hostapi.ExpandHomePath}

	// 1. 已上线载体 HomeDir 为空：成功恢复为主 HOME 绝对路径
	ref, err := resolver.ResolveSessionRef("card-test", keysclient.SessionRef{})
	if err != nil {
		t.Fatalf("已上线空 HOME 载体 ResolveSessionRef 失败: %v", err)
	}
	if ref.HomeDir != wantHome {
		t.Fatalf("ResolveSessionRef HomeDir = %q, want %q", ref.HomeDir, wantHome)
	}

	// 2. 小队中无 online 载体：失败
	if _, err := svc.ApplyDetect("c1", scheduling.DetectEvidence{Reachable: false}, "offline"); err != nil {
		t.Fatalf("设置载体 c1 offline: %v", err)
	}
	if _, err := resolver.ResolveSessionRef("card-test-offline", keysclient.SessionRef{}); err == nil {
		t.Fatal("小队无 online 载体时 ResolveSessionRef 应失败，但返回成功")
	}
}
