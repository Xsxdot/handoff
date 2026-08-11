// pending 持久化与托管判据的测试。
package selfupdate

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 往返：存进去什么读出来还是什么。
func TestPendingRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := &Pending{Version: "v0.2.0", Path: "/opt/bin/.handoff.new-v0.2.0", DownloadedAt: time.Unix(1760000000, 0).UTC()}
	if err := SavePending(dir, want); err != nil {
		t.Fatalf("SavePending: %v", err)
	}
	got, err := LoadPending(dir)
	if err != nil {
		t.Fatalf("LoadPending: %v", err)
	}
	if got == nil || got.Version != want.Version || got.Path != want.Path || !got.DownloadedAt.Equal(want.DownloadedAt) {
		t.Fatalf("往返不一致: %+v vs %+v", got, want)
	}
}

// 文件不存在是**正常状态**（绝大多数时候都没有待命更新），必须返回 (nil, nil)。
//
// why：把它当错误，更新循环每轮都会打一条 Error，日志里全是噪音，
// 真正的错误反而被淹掉。
func TestLoadPendingMissingIsNotError(t *testing.T) {
	got, err := LoadPending(t.TempDir())
	if err != nil {
		t.Fatalf("缺文件不该报错: %v", err)
	}
	if got != nil {
		t.Fatalf("缺文件应返回 nil，得到 %+v", got)
	}
}

// 坏 JSON 要报错而不是静默当成没有——静默会让「更新卡住」永远查不出原因。
func TestLoadPendingCorrupt(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(PendingPath(dir)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(PendingPath(dir), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPending(dir); err == nil {
		t.Fatal("坏 JSON 应报错")
	}
}

// ClearPending 幂等：没有文件时也返回 nil。
func TestClearPendingIdempotent(t *testing.T) {
	dir := t.TempDir()
	if err := ClearPending(dir); err != nil {
		t.Fatalf("无文件时 Clear 应成功: %v", err)
	}
	if err := SavePending(dir, &Pending{Version: "v1"}); err != nil {
		t.Fatal(err)
	}
	if err := ClearPending(dir); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	got, _ := LoadPending(dir)
	if got != nil {
		t.Fatal("Clear 之后应读不到")
	}
}

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

// PPID 绝不能成为判据——这条用一个反例钉住意图。
//
// why：手工 nohup / zsh -c … & 起的进程被 init 收养后 PPID 同样是 1。
// 拿 PPID==1 当托管判据会把所有裸进程误判成托管，正好打穿
// 「非托管则拒绝自动更新」这条最重要的防线。
func TestIsManagedIgnoresPPID(t *testing.T) {
	// 环境干净（模拟被 init 收养的裸进程），无论 PPID 是多少都必须是 false
	if IsManaged(func(string) string { return "" }) {
		t.Fatal("环境变量全空时必须判为非托管（fail-closed），不得依据 PPID")
	}
}
