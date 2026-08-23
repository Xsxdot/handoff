package grok_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor/grok"
	"github.com/Xsxdot/handoff/internal/testperm"
)

const (
	acct     = "https://auth.x.ai::b1a00492-0000-0000-0000-000000000000"
	expOld   = "2026-08-09T15:55:11.522980Z"
	expNewer = "2026-08-09T21:55:11.522980Z"
)

// authJSON 造一份 auth.json 文本；marker 落在 key 字段上用于判定哪一侧胜出。
func authJSON(t *testing.T, expiresAt, marker string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]map[string]string{
		acct: {"expires_at": expiresAt, "key": marker, "refresh_token": "rt-" + marker},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// fakeHome 把 HOME 指向临时目录并按需写出权威副本，返回
// （权威 auth.json 路径, 任务级 grokhome 路径）。
// authorityBody 为 nil 表示"权威副本不存在"。
func fakeHome(t *testing.T, authorityBody []byte) (authPath, homeDir string) {
	t.Helper()
	fake := t.TempDir()
	t.Setenv("HOME", fake)
	grokDir := filepath.Join(fake, ".grok")
	if err := os.MkdirAll(grokDir, 0o700); err != nil {
		t.Fatal(err)
	}
	authPath = filepath.Join(grokDir, "auth.json")
	if authorityBody != nil {
		if err := os.WriteFile(authPath, authorityBody, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	homeDir = filepath.Join(t.TempDir(), "grokhome")
	if err := os.MkdirAll(homeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	return authPath, homeDir
}

// writeTaskCopy 在任务 home 里放一份**普通文件**形态的 auth.json，
// 模拟 grok 刚在这里刷新过（替换掉了软链）。
func writeTaskCopy(t *testing.T, homeDir string, body []byte) string {
	t.Helper()
	p := filepath.Join(homeDir, "auth.json")
	if err := os.WriteFile(p, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// assertSymlinkTo 断言 link 是指向 target 的软链。
func assertSymlinkTo(t *testing.T, link, target string) {
	t.Helper()
	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("lstat %s 失败: %v", link, err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s 不是软链（mode=%v）", link, fi.Mode())
	}
	got, err := os.Readlink(link)
	if err != nil {
		t.Fatal(err)
	}
	if got != target {
		t.Errorf("软链指向 %s，期望 %s", got, target)
	}
}

// markerIn 读出文件里该账号条目的 key 字段。
func markerIn(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("解析 %s 失败: %v", path, err)
	}
	return m[acct]["key"]
}

// 用例 1（spec §7）：软链未被替换 → 零动作，权威文件字节不变。
func TestSyncAuthLeavesIntactSymlinkAlone(t *testing.T) {
	authPath, homeDir := fakeHome(t, authJSON(t, expOld, "authority"))
	if err := grok.EnsureAuthLink(homeDir); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := grok.SyncAuthToAuthority(homeDir, nil); err != nil {
		t.Fatalf("SyncAuthToAuthority 出错: %v", err)
	}

	after, _ := os.ReadFile(authPath)
	if string(before) != string(after) {
		t.Errorf("权威文件被动了：%s", after)
	}
	assertSymlinkTo(t, filepath.Join(homeDir, "auth.json"), authPath)
}

// 用例 2（spec §7）：普通文件且 expires_at 更晚 → 收编写回 + 软链恢复。
func TestSyncAuthAdoptsNewerCopyAndRestoresLink(t *testing.T) {
	authPath, homeDir := fakeHome(t, authJSON(t, expOld, "authority"))
	writeTaskCopy(t, homeDir, authJSON(t, expNewer, "task"))

	if err := grok.SyncAuthToAuthority(homeDir, nil); err != nil {
		t.Fatalf("SyncAuthToAuthority 出错: %v", err)
	}

	if m := markerIn(t, authPath); m != "task" {
		t.Errorf("权威副本 key = %q，期望被任务侧新凭据收编为 task", m)
	}
	if fi, err := os.Stat(authPath); err != nil {
		t.Fatal(err)
	} else if fi.Mode().Perm() != 0o600 {
		t.Errorf("权威副本权限 = %v，期望 0600", fi.Mode().Perm())
	}
	assertSymlinkTo(t, filepath.Join(homeDir, "auth.json"), authPath)
}

// 用例 3（spec §7）：任务侧更旧 → 不写回（防倒灌），**但软链仍被恢复**。
// 后半句是自审补的防线：陈旧拷贝留在 home 里，任务下次临期必然拿已作废的
// refresh token 去刷、必然失败。
func TestSyncAuthRefusesStaleCopyButStillRestoresLink(t *testing.T) {
	authPath, homeDir := fakeHome(t, authJSON(t, expNewer, "authority"))
	writeTaskCopy(t, homeDir, authJSON(t, expOld, "task"))

	if err := grok.SyncAuthToAuthority(homeDir, nil); err != nil {
		t.Fatalf("SyncAuthToAuthority 出错: %v", err)
	}

	if m := markerIn(t, authPath); m != "authority" {
		t.Errorf("权威副本被倒灌成 %q，期望保持 authority", m)
	}
	assertSymlinkTo(t, filepath.Join(homeDir, "auth.json"), authPath)
}

// 用例 4（spec §7）：多账号，只覆盖该账号键，其他键原值保留。
func TestSyncAuthKeepsOtherAccounts(t *testing.T) {
	const other = "https://auth.x.ai::other-client"
	authority := map[string]map[string]string{
		acct:  {"expires_at": expOld, "key": "authority-a"},
		other: {"expires_at": expOld, "key": "authority-b"},
	}
	ab, _ := json.Marshal(authority)
	authPath, homeDir := fakeHome(t, ab)
	writeTaskCopy(t, homeDir, authJSON(t, expNewer, "task"))

	if err := grok.SyncAuthToAuthority(homeDir, nil); err != nil {
		t.Fatalf("SyncAuthToAuthority 出错: %v", err)
	}

	b, _ := os.ReadFile(authPath)
	var got map[string]map[string]string
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got[acct]["key"] != "task" {
		t.Errorf("目标账号未被收编：%q", got[acct]["key"])
	}
	if got[other]["key"] != "authority-b" {
		t.Errorf("其他账号被动了：%q", got[other]["key"])
	}
}

// 用例 5（spec §7）：权威文件缺失 → 跳过、不创建、**也不建软链**。
// 用户可能刚 grok logout，不替他凭空造回来；软链指向不存在的目标只会让
// 下次启动更难诊断。
func TestSyncAuthSkipsWhenAuthorityMissing(t *testing.T) {
	authPath, homeDir := fakeHome(t, nil)
	link := writeTaskCopy(t, homeDir, authJSON(t, expNewer, "task"))

	if err := grok.SyncAuthToAuthority(homeDir, nil); err != nil {
		t.Fatalf("权威缺失不该返回错误，实际: %v", err)
	}

	if _, err := os.Stat(authPath); !os.IsNotExist(err) {
		t.Errorf("不应创建权威文件，err=%v", err)
	}
	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("任务侧副本不该被删: %v", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Errorf("权威缺失时不应复位软链")
	}
}

// 用例 6（spec §7）：任务侧 JSON 损坏 → 不写，权威文件字节级不变；软链仍被恢复
// （那份拷贝已经读不动了，留着毫无价值，接回权威副本反而可能救活任务）。
func TestSyncAuthIgnoresCorruptTaskCopyButRestoresLink(t *testing.T) {
	authPath, homeDir := fakeHome(t, authJSON(t, expOld, "authority"))
	before, _ := os.ReadFile(authPath)
	writeTaskCopy(t, homeDir, []byte("{not json"))

	if err := grok.SyncAuthToAuthority(homeDir, nil); err != nil {
		t.Fatalf("任务侧损坏不该返回错误，实际: %v", err)
	}

	after, _ := os.ReadFile(authPath)
	if string(before) != string(after) {
		t.Errorf("权威文件被动了：%s", after)
	}
	assertSymlinkTo(t, filepath.Join(homeDir, "auth.json"), authPath)
}

// 用例 6b：权威侧 JSON 损坏 → 不写、**不复位软链**。
// 复位等于把任务手里那份可能仍有效的凭据换成一个指向坏文件的链接（spec §5）。
func TestSyncAuthKeepsTaskCopyWhenAuthorityCorrupt(t *testing.T) {
	_, homeDir := fakeHome(t, []byte("{not json"))
	link := writeTaskCopy(t, homeDir, authJSON(t, expNewer, "task"))

	if err := grok.SyncAuthToAuthority(homeDir, nil); err != nil {
		t.Fatalf("权威侧损坏不该返回错误，实际: %v", err)
	}

	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("任务侧副本不该被删: %v", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Errorf("权威侧损坏时不应复位软链")
	}
	if m := markerIn(t, link); m != "task" {
		t.Errorf("任务侧副本内容被动了：%q", m)
	}
}

// 用例 7（spec §7）：写回失败 → **任务侧副本被保留、软链未被恢复**。
// 与用例 3 方向相反：那条守"别倒灌"，这条守"别把唯一一份新凭据丢掉"。
func TestSyncAuthKeepsTaskCopyWhenWriteFails(t *testing.T) {
	authPath, homeDir := fakeHome(t, authJSON(t, expOld, "authority"))
	grokDir := filepath.Dir(authPath)
	testperm.DenyWrite(t, grokDir)
	link := writeTaskCopy(t, homeDir, authJSON(t, expNewer, "task"))

	if err := grok.SyncAuthToAuthority(homeDir, nil); err == nil {
		t.Fatalf("写回失败应返回错误")
	}

	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("任务侧副本不该被删: %v", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Errorf("写回失败时不应复位软链——那份副本可能是唯一的新凭据")
	}
	if m := markerIn(t, link); m != "task" {
		t.Errorf("任务侧副本内容被动了：%q", m)
	}
}
