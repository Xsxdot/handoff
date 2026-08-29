package grok

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
)

// wdAuthJSON 造一份单账号 auth.json 文本。
func wdAuthJSON(t *testing.T, expiresAt, marker string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]map[string]string{
		"https://auth.x.ai::c": {"expires_at": expiresAt, "key": marker},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// wdSetup 造出：假 HOME 下的权威副本（旧凭据）+ 任务目录下 grokhome 里一份
// **更新的普通文件**副本，模拟"grok 刚刷新过、看门狗还没来得及收编"。
// 返回（权威 auth.json 路径, 任务目录）。
func wdSetup(t *testing.T) (authPath, taskDir string) {
	t.Helper()
	fake := t.TempDir()
	t.Setenv("HOME", fake)
	grokDir := filepath.Join(fake, ".grok")
	if err := os.MkdirAll(grokDir, 0o700); err != nil {
		t.Fatal(err)
	}
	authPath = filepath.Join(grokDir, authFileName)
	if err := os.WriteFile(authPath, wdAuthJSON(t, "2026-08-09T15:55:11.522980Z", "authority"), 0o600); err != nil {
		t.Fatal(err)
	}
	taskDir = t.TempDir()
	homeDir := filepath.Join(taskDir, homeDirName)
	if err := os.MkdirAll(homeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, authFileName),
		wdAuthJSON(t, "2026-08-09T21:55:11.522980Z", "task"), 0o600); err != nil {
		t.Fatal(err)
	}
	return authPath, taskDir
}

// wdMarker 读出权威副本里的 key 字段，判断是否已被收编。
func wdMarker(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m["https://auth.x.ai::c"]["key"]
}

// deadPort 返回一个刚被释放、确定没人监听的回环端口，
// 让 Proc.Alive() 立刻连接被拒（不必等超时）。
func deadPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

// 用例 8a（spec §7）：evClosed 正常退场前，必须补最后一次巡检。
// 否则任务终结时躺在 home 里的新凭据会随任务目录一起被归档掉。
func TestWatchdogSyncsAuthOnNormalExit(t *testing.T) {
	authPath, taskDir := wdSetup(t)
	a := New(nil)
	r := &runState{
		taskID:  "t-closed",
		taskDir: taskDir,
		env:     []string{"HOME=" + os.Getenv("HOME")},
		proc:    &Proc{TaskDir: taskDir, Port: deadPort(t)},
		evCh:    make(chan executor.AdapterEvent, 64),
	}
	// 事件通道已关闭 = 任务已终结，看门狗第一次醒来就该退场
	r.closeEvents()

	a.watchdog(r) // 同步跑：evClosed 分支会在一个 tick 内返回

	if m := wdMarker(t, authPath); m != "task" {
		t.Errorf("正常退场前未收编新凭据，权威副本 key = %q", m)
	}
}

// 用例 8b（spec §7）：探活判死退场前，同样必须补最后一次巡检。
// 漏这条就等于漏掉"任务跑挂了但刚刷新过"这一整类。
func TestWatchdogSyncsAuthOnDeathExit(t *testing.T) {
	authPath, taskDir := wdSetup(t)
	a := New(nil)
	r := &runState{
		taskID:  "t-dead",
		taskDir: taskDir,
		env:     []string{"HOME=" + os.Getenv("HOME")},
		proc:    &Proc{TaskDir: taskDir, Port: deadPort(t)},
		evCh:    make(chan executor.AdapterEvent, 64),
		// 预置 lastAuthSync 让循环内的节流巡检**不触发**，这样本用例唯一可能
		// 完成收编的路径就是退场 defer。不预置的话第一次循环就会顺手同步掉，
		// 用例即使删掉 defer 也照样绿——那就白测了
		lastAuthSync: time.Now(),
	}

	a.watchdog(r) // 连续 watchdogFailThreshold 次探活失败后判死退场（约 600ms）

	if m := wdMarker(t, authPath); m != "task" {
		t.Errorf("判死退场前未收编新凭据，权威副本 key = %q", m)
	}
	// 顺带确认判死路径本身没被改坏
	if !r.evClosed {
		t.Errorf("判死后事件通道应已关闭")
	}
}

// TestSyncAuthOnceWithoutCarrierHomeDoesNotWriteMain 没有显式载体 HOME 时，
// 看门狗不能把任务凭据写回机器主 HOME。
func TestSyncAuthOnceWithoutCarrierHomeDoesNotWriteMain(t *testing.T) {
	authPath, taskDir := wdSetup(t)
	a := New(nil)
	a.syncAuthOnce(&runState{taskID: "no-carrier", taskDir: taskDir})

	if marker := wdMarker(t, authPath); marker != "authority" {
		t.Fatalf("无载体 HOME 时不应写回机器主 HOME，key = %q", marker)
	}
}

// TestSyncAuthOnceUsesCarrierHome 看门狗收编必须写回 req.Env 指定的权威副本。
func TestSyncAuthOnceUsesCarrierHome(t *testing.T) {
	_, taskDir := wdSetup(t)
	carrierHome := t.TempDir()
	carrierAuthDir := filepath.Join(carrierHome, ".grok")
	if err := os.MkdirAll(carrierAuthDir, 0o700); err != nil {
		t.Fatal(err)
	}
	carrierAuth := filepath.Join(carrierAuthDir, authFileName)
	if err := os.WriteFile(carrierAuth, wdAuthJSON(t, "2026-08-09T15:55:11.522980Z", "carrier-authority"), 0o600); err != nil {
		t.Fatal(err)
	}

	a := New(nil)
	r := &runState{taskID: "carrier-sync", taskDir: taskDir,
		env: []string{"HOME=" + carrierHome}}
	a.syncAuthOnce(r)

	if marker := wdMarker(t, carrierAuth); marker != "task" {
		t.Fatalf("载体权威凭据未收编更新，key = %q", marker)
	}
}
