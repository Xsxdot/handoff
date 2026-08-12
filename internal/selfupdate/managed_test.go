// managed.go 托管判据的测试：systemd / launchd 环境变量识别 + PPID 反例。
package selfupdate

import "testing"

// 托管判据的表驱动。取值全部来自 spec §4.7 的 P1 实测表。
func TestIsManaged(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"launchd 托管", map[string]string{"XPC_SERVICE_NAME": "dev.gosuper.handoff.agentd"}, true},
		{"systemd 托管", map[string]string{"INVOCATION_ID": "5a1c9f0e"}, true},
		{"ssh 里裸跑", map[string]string{}, false},
		{"tmux 里跑", map[string]string{"TMUX": "/tmp/tmux-501/default,1,0"}, false},
		// 从 Finder / Terminal.app 启动的进程继承 XPC_SERVICE_NAME=0，
		// 那是 launchd 给非 XPC 服务的占位值，不是托管
		{"Terminal.app 的占位值", map[string]string{"XPC_SERVICE_NAME": "0"}, false},
		{"空串等于没有", map[string]string{"XPC_SERVICE_NAME": "", "INVOCATION_ID": ""}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			getenv := func(k string) string { return c.env[k] }
			if got := IsManaged(getenv); got != c.want {
				t.Fatalf("IsManaged=%v，期望 %v", got, c.want)
			}
		})
	}
}

// TestIsManagedIgnoresPPID 是这条防线最重要的反例：手工 nohup 起的进程
// 被 init 收养后 PPID 同样是 1，拿 PPID 当判据会把所有裸进程误判成托管，
// 正好把「非托管则拒绝换版」这条防线打穿。
func TestIsManagedIgnoresPPID(t *testing.T) {
	if IsManaged(func(string) string { return "" }) {
		t.Fatal("环境变量全空必须判非托管（fail-closed）")
	}
}

func TestIsManagedSystemd(t *testing.T) {
	if !IsManaged(func(k string) string {
		if k == "INVOCATION_ID" {
			return "abc"
		}
		return ""
	}) {
		t.Fatal("INVOCATION_ID 非空应判托管")
	}
}

func TestIsManagedLaunchdPlaceholder(t *testing.T) {
	// 从 Finder / Terminal.app 启动的进程会继承 XPC_SERVICE_NAME=0
	//（launchd 给非 XPC 服务的占位值），只判非空会把桌面上手动跑的误判成托管
	if IsManaged(func(k string) string {
		if k == "XPC_SERVICE_NAME" {
			return "0"
		}
		return ""
	}) {
		t.Fatal("XPC_SERVICE_NAME=0 是占位值，必须判非托管")
	}
}
