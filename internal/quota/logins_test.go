// logins_test.go —— 离线取材（B464 段 1，plan V2/V4 的 opencode-go 子集）。
//
// 职责：锁 opencode-go 两来源（auth.json 的 opencode-go API key、opencode.db 当前登录账号）
// 在夹具 HOME 里各认成一份登录、身份不互认；「没登录」不是问题、形状不对才是；取材结果与
// 问题文案、日志里零出现哨兵凭据与原始身份材料。
// 边界：夹具 HOME 只在临时目录；opencode.db 表形状沿用 opencodego_login_test.go 的 1.19.3 schema。
package quota

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"

	"path/filepath"
	"strings"
	"testing"
)

func ogRegistry() *Registry {
	return NewRegistry(map[string]Prober{KindOpencodeGo: &OpencodeGo{CredRelPath: testCredRelPath}})
}

func TestLoginsOpencodeTwoSourcesDoNotMerge(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)

	keyOnly := writeOpencodeAuth(t, `{"opencode-go":{"type":"api","key":"`+sentinelKey+`"},"other":{"type":"oauth","access":"x"}}`)
	dbOnly := t.TempDir()
	writeOpencodeDB(t, dbOnly, loginRow{id: "acc_SENTINEL_id", org: "org_SENTINEL", token: sentinelKey})
	both := writeOpencodeAuth(t, `{"opencode-go":{"type":"api","key":"`+sentinelKey+`"}}`)
	writeOpencodeDB(t, both, loginRow{id: "acc_SENTINEL_id", org: "org_SENTINEL", token: sentinelKey})
	empty := t.TempDir()

	keyFP, oauthFP := Fingerprint(sentinelKey), Fingerprint("acc_SENTINEL_id/org_SENTINEL")
	cases := []struct {
		name string
		home string
		want []string // source=fingerprint
	}{
		{"只有 auth.json key", keyOnly, []string{"auth.json=" + keyFP}},
		{"只有 opencode.db", dbOnly, []string{"opencode.db=" + oauthFP}},
		{"两者都有", both, []string{"auth.json=" + keyFP, "opencode.db=" + oauthFP}},
		{"都没有", empty, nil},
	}
	var dump bytes.Buffer
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, problems := ogRegistry().Logins(tc.home)
			if len(problems) != 0 {
				t.Fatalf("「没登录」不是问题，得 %v", problems)
			}
			var pairs []string
			for _, f := range got {
				if f.Kind != KindOpencodeGo {
					t.Fatalf("kind = %q", f.Kind)
				}
				pairs = append(pairs, f.Source+"="+f.Fingerprint)
			}
			if fmt.Sprint(pairs) != fmt.Sprint(tc.want) {
				t.Fatalf("登录 = %v，want %v", pairs, tc.want)
			}
			raw, _ := json.Marshal(got)
			dump.Write(raw)
		})
	}
	if keyFP == oauthFP {
		t.Fatal("两来源身份不得互认")
	}
	for _, f := range mustFindings(t, both) {
		if f.Source == "opencode.db" && f.ExpiresAt == nil {
			t.Fatalf("opencode.db 登录应带 token 到期时刻：%+v", f)
		}
		// I1：OAuth 登录 label 取 account.email（自动起名用）；身份材料仍是账号 id/org。
		if f.Source == "opencode.db" && f.Label != "u@example.com" {
			t.Fatalf("opencode.db 登录 label 应取 account.email，得 %q", f.Label)
		}
	}
	assertNoSecret(t, dump.String()+logs.String(), sentinelKey, "acc_SENTINEL_id", "org_SENTINEL")
}

func TestLoginsOpencodeShapeProblemsAreReportedWithoutSecrets(t *testing.T) {
	badJSON := writeOpencodeAuth(t, `{"opencode-go":`+`"`+sentinelKey)
	notAPI := writeOpencodeAuth(t, `{"opencode-go":{"type":"oauth","key":"`+sentinelKey+`"}}`)
	noOrg := t.TempDir()
	writeOpencodeDB(t, noOrg, loginRow{id: "acc_SENTINEL_id", org: "-", token: sentinelKey})
	for name, home := range map[string]string{"auth.json 不是 JSON": badJSON, "条目不是 API key": notAPI, "登录账号无组织": noOrg} {
		t.Run(name, func(t *testing.T) {
			got, problems := ogRegistry().Logins(home)
			if len(got) != 0 || len(problems) != 1 {
				t.Fatalf("形状不对应恰报一个问题、不认登录：%+v %v", got, problems)
			}
			if !strings.Contains(problems[0].Error(), filepath.Join(home, ".local")) {
				t.Fatalf("问题应点名路径：%v", problems[0])
			}
			assertNoSecret(t, problems[0].Error(), sentinelKey, "acc_SENTINEL_id")
		})
	}
	// 本平台无已知落点：静默跳过，不报问题
	reg := NewRegistry(map[string]Prober{KindOpencodeGo: &OpencodeGo{}})
	if got, problems := reg.Logins(notAPI); len(got) != 0 || len(problems) != 0 {
		t.Fatalf("无落点平台应跳过：%+v %v", got, problems)
	}
}

// TestLoginsOpencodeDBSchemaWithoutEmailColumn 锁 m5：旧版 opencode.db 的 account 表没有 email
// 列时容错——登录仍被认出（来源/指纹照常），Label 留空，不报问题。
func TestLoginsOpencodeDBSchemaWithoutEmailColumn(t *testing.T) {
	home := t.TempDir()
	writeOpencodeDB(t, home, loginRow{id: "acc_legacy_1", org: "org_legacy", token: sentinelKey, noEmail: true})
	got, problems := ogRegistry().Logins(home)
	if len(problems) != 0 {
		t.Fatalf("无 email 列不是问题，得 %v", problems)
	}
	if len(got) != 1 || got[0].Kind != KindOpencodeGo || got[0].Source != "opencode.db" {
		t.Fatalf("无 email 列时仍应认出登录：%+v", got)
	}
	if got[0].Label != "" {
		t.Fatalf("无 email 列时 Label 应为空，得 %q", got[0].Label)
	}
	if got[0].Fingerprint != Fingerprint("acc_legacy_1/org_legacy") {
		t.Fatalf("身份仍取账号 id/org：%q", got[0].Fingerprint)
	}
	assertNoSecret(t, got[0].Fingerprint, sentinelKey, "acc_legacy_1", "org_legacy")
}

func mustFindings(t *testing.T, home string) []LoginFinding {
	t.Helper()
	got, problems := ogRegistry().Logins(home)
	if len(problems) != 0 {
		t.Fatalf("problems: %v", problems)
	}
	return got
}

func assertNoSecret(t *testing.T, text string, secrets ...string) {
	t.Helper()
	for _, s := range secrets {
		if strings.Contains(text, s) {
			t.Fatalf("输出含凭据/原始身份 %q：%s", s, text)
		}
	}
}
