package grok

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mkEntry 造一条最小账号条目：expires_at 用于比较，marker 落在 key 字段上，
// 用来验证收编是**整体搬运原文**而不是逐字段拼装（grok 加字段不能把凭据写残）。
func mkEntry(t *testing.T, expiresAt, marker string) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(map[string]string{
		"expires_at":    expiresAt,
		"key":           marker,
		"refresh_token": "rt-" + marker,
	})
	if err != nil {
		t.Fatal(err)
	}
	return json.RawMessage(b)
}

// markerOf 取出条目里的 key 字段，用于断言"哪一侧的原文胜出"。
func markerOf(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var e struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatalf("解析条目失败: %v", err)
	}
	return e.Key
}

const acct = "https://auth.x.ai::b1a00492-0000-0000-0000-000000000000"

// TestEntryExpiresAtParsesRealFormat 钉住真机实测的带小数秒格式。
// devbox 上实测 expires_at = 2026-08-09T15:55:11.522980Z，
// 少了小数秒支持就会让所有收编静默走 fail-closed 分支（永远不写回）。
func TestEntryExpiresAtParsesRealFormat(t *testing.T) {
	got, err := entryExpiresAt(mkEntry(t, "2026-08-09T15:55:11.522980Z", "m"))
	if err != nil {
		t.Fatalf("解析真机格式出错: %v", err)
	}
	if got.IsZero() {
		t.Fatalf("解析结果为零值")
	}
}

func TestEntryExpiresAtRejectsBadInput(t *testing.T) {
	cases := map[string]json.RawMessage{
		"非 JSON 对象":      json.RawMessage(`"just a string"`),
		"缺 expires_at":   json.RawMessage(`{"key":"k"}`),
		"expires_at 非时间": json.RawMessage(`{"expires_at":"not-a-time"}`),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := entryExpiresAt(raw); err == nil {
				t.Errorf("应报错，实际返回 nil")
			}
		})
	}
}

// TestMergeNewerEntriesAdoptsOnlyStrictlyNewer 是本设计的误伤防线：
// 相等和更旧都必须不写——用户刚 grok login 过时倒灌回去等于直接弄坏登录态。
func TestMergeNewerEntriesAdoptsOnlyStrictlyNewer(t *testing.T) {
	const authExp = "2026-08-09T15:55:11.522980Z"
	cases := []struct {
		name        string
		taskExp     string
		wantAdopted bool
	}{
		{"任务侧更晚 → 收编", "2026-08-09T21:55:11.522980Z", true},
		{"两侧相等 → 不收编", authExp, false},
		{"任务侧更旧 → 不收编（防倒灌）", "2026-08-09T09:55:11.522980Z", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			authority := authFile{acct: mkEntry(t, authExp, "authority")}
			task := authFile{acct: mkEntry(t, c.taskExp, "task")}

			merged, adopted := mergeNewerEntries(authority, task)

			if c.wantAdopted {
				if len(adopted) != 1 || adopted[0] != acct {
					t.Fatalf("adopted = %v，期望 [%s]", adopted, acct)
				}
				if m := markerOf(t, merged[acct]); m != "task" {
					t.Errorf("胜出方 = %q，期望 task 侧原文", m)
				}
			} else {
				if len(adopted) != 0 {
					t.Fatalf("adopted = %v，期望空", adopted)
				}
				if m := markerOf(t, merged[acct]); m != "authority" {
					t.Errorf("胜出方 = %q，期望保留 authority 原值", m)
				}
			}
			// 输入不可被就地改写：调用方还要拿 authority 打新旧 expires_at 日志
			if m := markerOf(t, authority[acct]); m != "authority" {
				t.Errorf("authority 被就地修改了")
			}
		})
	}
}

// TestMergeNewerEntriesKeepsOtherAccounts 守住多账号：auth.json 是账号字典，
// 整文件覆盖会在用户有第二个账号时把它抹掉（spec §4.2）。
func TestMergeNewerEntriesKeepsOtherAccounts(t *testing.T) {
	const other = "https://auth.x.ai::other-client"
	authority := authFile{
		acct:  mkEntry(t, "2026-08-09T15:55:11.522980Z", "authority-a"),
		other: mkEntry(t, "2026-08-09T15:55:11.522980Z", "authority-b"),
	}
	task := authFile{acct: mkEntry(t, "2026-08-09T21:55:11.522980Z", "task-a")}

	merged, adopted := mergeNewerEntries(authority, task)

	if len(adopted) != 1 || adopted[0] != acct {
		t.Fatalf("adopted = %v，期望只收编 %s", adopted, acct)
	}
	if m := markerOf(t, merged[other]); m != "authority-b" {
		t.Errorf("其他账号被动了：%q", m)
	}
	if len(merged) != 2 {
		t.Errorf("merged 键数 = %d，期望 2", len(merged))
	}
}

// TestMergeNewerEntriesFailsClosed 三条 fail-closed 分支各一例。
func TestMergeNewerEntriesFailsClosed(t *testing.T) {
	const newer = "2026-08-09T21:55:11.522980Z"
	const older = "2026-08-09T15:55:11.522980Z"
	cases := []struct {
		name      string
		authority authFile
		task      authFile
	}{
		{
			name:      "权威侧没有该键 → 无从比较，不收编",
			authority: authFile{},
			task:      authFile{acct: mkEntry(t, newer, "task")},
		},
		{
			name:      "权威侧 expires_at 不可解析 → 不收编",
			authority: authFile{acct: json.RawMessage(`{"expires_at":"garbage"}`)},
			task:      authFile{acct: mkEntry(t, newer, "task")},
		},
		{
			name:      "任务侧 expires_at 不可解析 → 不收编",
			authority: authFile{acct: mkEntry(t, older, "authority")},
			task:      authFile{acct: json.RawMessage(`{"expires_at":"garbage"}`)},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, adopted := mergeNewerEntries(c.authority, c.task)
			if len(adopted) != 0 {
				t.Errorf("adopted = %v，期望空（fail-closed）", adopted)
			}
		})
	}
}

// TestResetAuthLinkNoWindowOnFailure 钉住 resetAuthLink 的原子复位契约：
// 复位失败（Symlink 建不出软链）时，任务侧原文件必须仍在、内容未变——证明
// 不存在「先删后建」的破链窗口。
//
// 旧实现是「先 Remove(link) 再 Symlink(link)」两步：Symlink 一旦失败，home 里的
// auth.json 就消失了，下一轮巡检 os.Lstat 报错直接 return、永不重试，任务从此
// 没有凭据（与 spec §5「下轮再试恢复」的承诺相悖）。
func TestResetAuthLinkNoWindowOnFailure(t *testing.T) {
	home := t.TempDir()
	link := filepath.Join(home, authFileName)
	original := []byte(`{"acct":{"expires_at":"2026-08-09T21:55:11Z","key":"task"}}`)
	if err := os.WriteFile(link, original, 0o600); err != nil {
		t.Fatal(err)
	}
	// 注入点必须让 Symlink 失败而 Remove 仍能成功——否则旧的两步实现在第一步
	// 就被挡住，窗口根本不会被走到，用例形同虚设。超长 target 触发
	// ENAMETOOLONG，而目录保持可写。
	//
	// 长度必须**同时**越过两个平台的上限：macOS 的软链目标上限是 PATH_MAX
	// 1024，Linux 是 4096。原先取 2000 只越过了 macOS，Linux 上 Symlink
	// **成功**，注入当场失效——报错推迟到后面 ReadFile 跟链解析时才发生，
	// 表现为一句莫名其妙的「原文件应仍在: file name too long」。取 8192
	// 是为了在两边都稳稳落在 ENAMETOOLONG 上。
	tooLong := "/" + strings.Repeat("a", 8192)

	resetAuthLink(link, tooLong, slog.Default())

	// 先证注入真的生效：link 必须还是普通文件。少了这条，哪天上限又变了，
	// Symlink 悄悄成功会让整个用例退化成「读一个软链」的无关断言
	if fi, err := os.Lstat(link); err != nil {
		t.Fatalf("Lstat 任务侧文件: %v", err)
	} else if fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal("注入失效：Symlink 竟然成功了，本用例要验的失败路径根本没被走到")
	}

	got, err := os.ReadFile(link)
	if err != nil {
		t.Fatalf("复位失败后任务侧原文件应仍在: %v", err)
	}
	if string(got) != string(original) {
		t.Errorf("复位失败后原文件内容被改了：%s", got)
	}
	// 临时软链不许残留
	ents, _ := os.ReadDir(home)
	for _, e := range ents {
		if strings.Contains(e.Name(), "handoff-link-") {
			t.Errorf("残留临时软链: %s", e.Name())
		}
	}
}
