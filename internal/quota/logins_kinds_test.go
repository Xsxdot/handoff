// logins_kinds_test.go —— 离线取材的其余 kind（B464 段 2，plan V2/V4）：codex、grok、agy、claude、
// 第三方 API key。
//
// 职责：每 kind 一张夹具表（存在/缺失/形状不对/到期字段有无/refresh 有无），断言产出的来源名、
// 指纹、label、到期、可自刷新与凭据变更时刻；「没登录」不是问题、形状不对才是；结果、问题
// 文案、日志里零出现哨兵凭据与原始身份材料（V4）。
// 边界：夹具键名只取台账「段 0 真机事实 R0」读到的键形状，值一律哨兵；夹具 HOME 只在临时
// 目录；claude 的登录判据用假 `claude` 脚本（不调真实 CLI、不碰钥匙串）。
package quota

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// 原始身份材料哨兵（V4：只许以短指纹出现）。邮箱是 label，允许出现。
const (
	sentinelAcct = "acct-SENTINEL-codex"
	sentinelUser = "user-SENTINEL-grok"
	sentinelSub  = "sub-SENTINEL-agy"
	sentinelUUID = "uuid-SENTINEL-claude"
	sentinelDS   = "sk-SENTINEL-deepseek-key"
)

// fakeIDToken 造一个 payload 带给定 claims 的 JWT（适配器不验签）。
func fakeIDToken(claims map[string]any) string {
	enc := base64.RawURLEncoding
	raw, _ := json.Marshal(claims)
	return enc.EncodeToString([]byte(`{"alg":"none"}`)) + "." + enc.EncodeToString(raw) + "." + sentinelKey
}

func writeHomeFile(t *testing.T, home, rel, content string) {
	t.Helper()
	p := filepath.Join(home, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// captureLogs 把默认 logger 换成 JSON 捕获（Debug 级），测试结束还原。
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &logs
}

// findingCase 是一张取材表的一行：want 为空 = 不认登录；problem = 应恰报一个问题。
type findingCase struct {
	name    string
	home    string
	want    *LoginFinding
	problem bool
}

func runFindingTable(t *testing.T, reg *Registry, cases []findingCase, dump *bytes.Buffer) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, problems := reg.Logins(tc.home)
			raw, _ := json.Marshal(got)
			dump.Write(raw)
			for _, p := range problems {
				dump.WriteString(p.Error())
			}
			if tc.problem != (len(problems) == 1) || len(problems) > 1 {
				t.Fatalf("problems = %v，want problem=%v", problems, tc.problem)
			}
			if tc.want == nil {
				if len(got) != 0 {
					t.Fatalf("不应认出登录，得 %+v", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("应恰认出一份登录，得 %+v（problems %v）", got, problems)
			}
			assertFinding(t, got[0], *tc.want)
		})
	}
}

func assertFinding(t *testing.T, got, want LoginFinding) {
	t.Helper()
	if got.Kind != want.Kind || got.Provider != want.Provider || got.Source != want.Source ||
		got.Fingerprint != want.Fingerprint || got.Label != want.Label || got.Refreshable != want.Refreshable {
		t.Fatalf("登录 = %+v\nwant %+v", got, want)
	}
	if (got.ExpiresAt == nil) != (want.ExpiresAt == nil) ||
		(got.ExpiresAt != nil && !got.ExpiresAt.Equal(*want.ExpiresAt)) {
		t.Fatalf("到期 = %v，want %v", got.ExpiresAt, want.ExpiresAt)
	}
	if got.ChangedAt == nil || got.ChangedAt.IsZero() {
		t.Fatalf("应带凭据变更时刻（文件 mtime）：%+v", got)
	}
}

func secPtr(t time.Time) *time.Time { u := time.Unix(t.Unix(), 0).UTC(); return &u }

func TestLoginsCodexTable(t *testing.T) {
	logs := captureLogs(t)
	exp := time.Now().Add(222 * time.Hour)
	access := fakeJWT(exp, sentinelKey)
	idTok := fakeIDToken(map[string]any{"email": "cx@example.com", "sub": sentinelSub, "https://api.openai.com/auth": map[string]any{}})
	full := `{"OPENAI_API_KEY":"","auth_mode":"chatgpt","last_refresh":"2026-10-21T00:00:00Z","tokens":{"access_token":"` + access +
		`","account_id":"` + sentinelAcct + `","id_token":"` + idTok + `","refresh_token":"` + sentinelKey + `"}}`
	noRefresh := `{"tokens":{"access_token":"` + access + `","account_id":"` + sentinelAcct + `","id_token":"` + idTok + `"}}`
	noIDToken := `{"tokens":{"access_token":"` + access + `","account_id":"` + sentinelAcct + `","refresh_token":"` + sentinelKey + `"}}`
	fp := Fingerprint(sentinelAcct)
	cases := []findingCase{
		{"完整 ChatGPT 登录", writeCodexAuth(t, full), &LoginFinding{Kind: KindCodex, Source: "auth.json", Fingerprint: fp,
			Label: "cx@example.com", ExpiresAt: secPtr(exp), Refreshable: true}, false},
		{"无 refresh_token", writeCodexAuth(t, noRefresh), &LoginFinding{Kind: KindCodex, Source: "auth.json", Fingerprint: fp,
			Label: "cx@example.com", ExpiresAt: secPtr(exp)}, false},
		{"无 id_token：label 空", writeCodexAuth(t, noIDToken), &LoginFinding{Kind: KindCodex, Source: "auth.json", Fingerprint: fp,
			ExpiresAt: secPtr(exp), Refreshable: true}, false},
		{"只有 OPENAI_API_KEY：不认作登录（Q19）", writeCodexAuth(t, `{"OPENAI_API_KEY":"`+sentinelKey+`","tokens":null}`), nil, false},
		{"凭据文件缺失", t.TempDir(), nil, false},
		{"tokens 缺 account_id", writeCodexAuth(t, `{"tokens":{"access_token":"`+access+`","refresh_token":"`+sentinelKey+`"}}`), nil, true},
		{"不是 JSON", writeCodexAuth(t, `oops `+sentinelKey), nil, true},
	}
	var dump bytes.Buffer
	runFindingTable(t, NewRegistry(map[string]Prober{KindCodex: &Codex{CredRelPath: codexRel}}), cases, &dump)
	assertNoSecret(t, dump.String()+logs.String(), sentinelKey, sentinelAcct, sentinelSub)
}

func TestLoginsGrokTable(t *testing.T) {
	logs := captureLogs(t)
	later := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)
	sooner := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	entry := func(name, user, email string, exp time.Time, refresh bool) string {
		e := map[string]any{"auth_mode": "oauth", "key": sentinelKey, "email": email, "expires_at": exp.Format(time.RFC3339)}
		if user != "" {
			e["user_id"] = user
		}
		if refresh {
			e["refresh_token"] = sentinelKey
		}
		raw, _ := json.Marshal(map[string]any{name: e})
		return string(raw)
	}
	merge := func(parts ...string) string {
		out := map[string]json.RawMessage{}
		for _, p := range parts {
			var m map[string]json.RawMessage
			_ = json.Unmarshal([]byte(p), &m)
			for k, v := range m {
				out[k] = v
			}
		}
		raw, _ := json.Marshal(out)
		return string(raw)
	}
	prefixed := "https://auth.x.ai::0b8e3f6c-SENTINEL-uuid"
	cases := []findingCase{
		{"新格式条目 issuer::<uuid>（R0-6）", writeGrokAuth(t, entry(prefixed, sentinelUser, "gk@example.com", later, true)),
			&LoginFinding{Kind: KindGrok, Source: "auth.json", Fingerprint: Fingerprint(sentinelUser), Label: "gk@example.com",
				ExpiresAt: &later, Refreshable: true}, false},
		{"精确条目名仍认", writeGrokAuth(t, entry("https://auth.x.ai", sentinelUser, "gk@example.com", later, false)),
			&LoginFinding{Kind: KindGrok, Source: "auth.json", Fingerprint: Fingerprint(sentinelUser), Label: "gk@example.com",
				ExpiresAt: &later}, false},
		{"无 user_id 回落 email", writeGrokAuth(t, entry(prefixed, "", "gk@example.com", later, true)),
			&LoginFinding{Kind: KindGrok, Source: "auth.json", Fingerprint: Fingerprint("gk@example.com"), Label: "gk@example.com",
				ExpiresAt: &later, Refreshable: true}, false},
		{"多条取 expires_at 最晚（已过期也照认身份）", writeGrokAuth(t, merge(
			entry("https://auth.x.ai::old", "user-old-SENTINEL", "old@example.com", sooner, true),
			entry(prefixed, sentinelUser, "gk@example.com", later, true))),
			&LoginFinding{Kind: KindGrok, Source: "auth.json", Fingerprint: Fingerprint(sentinelUser), Label: "gk@example.com",
				ExpiresAt: &later, Refreshable: true}, false},
		{"只有别的 issuer", writeGrokAuth(t, entry("https://other.example::x", sentinelUser, "x@example.com", later, true)), nil, false},
		{"凭据文件缺失", t.TempDir(), nil, false},
		{"条目既无 user_id 也无 email", writeGrokAuth(t, entry(prefixed, "", "", later, true)), nil, true},
		{"不是 JSON", writeGrokAuth(t, `{"https://auth.x.ai::u":`+sentinelKey), nil, true},
	}
	var dump bytes.Buffer
	runFindingTable(t, NewRegistry(map[string]Prober{KindGrok: &Grok{CredRelPath: grokRel}}), cases, &dump)
	assertNoSecret(t, dump.String()+logs.String(), sentinelKey, sentinelUser, "user-old-SENTINEL", "SENTINEL-uuid")
}

func TestLoginsAgyTable(t *testing.T) {
	logs := captureLogs(t)
	expiry := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	tokenFile := func(claims map[string]any, refresh bool) string {
		tok := map[string]any{"access_token": sentinelKey, "expires_in": 3599, "expiry": expiry.Format(time.RFC3339Nano), "token_type": "Bearer"}
		if refresh {
			tok["refresh_token"] = sentinelKey
		}
		top := map[string]any{"auth_method": "consumer", "token": tok}
		if claims != nil {
			top["id_token"] = fakeIDToken(claims)
		}
		raw, _ := json.Marshal(top)
		return string(raw)
	}
	home := func(content string) string {
		h := t.TempDir()
		writeHomeFile(t, h, agyTokenRelPath, content)
		return h
	}
	claims := map[string]any{"email": "agy@example.com", "sub": sentinelSub, "email_verified": true}
	cases := []findingCase{
		{"离线 token 文件（Q7 分支 a）", home(tokenFile(claims, true)), &LoginFinding{Kind: KindAgy, Source: "antigravity-oauth-token",
			Fingerprint: Fingerprint(sentinelSub), Label: "agy@example.com", ExpiresAt: &expiry, Refreshable: true}, false},
		{"无 refresh_token", home(tokenFile(claims, false)), &LoginFinding{Kind: KindAgy, Source: "antigravity-oauth-token",
			Fingerprint: Fingerprint(sentinelSub), Label: "agy@example.com", ExpiresAt: &expiry}, false},
		{"token 文件缺失", t.TempDir(), nil, false},
		{"id_token 缺 sub", home(tokenFile(map[string]any{"email": "agy@example.com"}, true)), nil, true},
		{"没有 id_token", home(tokenFile(nil, true)), nil, true},
		{"不是 JSON", home(`{"token":` + sentinelKey), nil, true},
	}
	var dump bytes.Buffer
	runFindingTable(t, NewRegistry(map[string]Prober{KindAgy: &Agy{}}), cases, &dump)
	assertNoSecret(t, dump.String()+logs.String(), sentinelKey, sentinelSub)
}

// fakeClaude 写一个假 claude：记调用参数与关键环境到 $HOME，按 $HOME/fake-status 输出、
// $HOME/fake-exit 退出码、$HOME/fake-sleep 存在则先睡 5s（超时路径）。
func fakeClaude(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "claude")
	script := "#!/bin/sh\n" +
		"echo \"$@\" >> \"$HOME/claude-calls\"\n" +
		"echo \"HOME=$HOME CLAUDE_CONFIG_DIR=$CLAUDE_CONFIG_DIR ANTHROPIC_API_KEY=$ANTHROPIC_API_KEY\" >> \"$HOME/claude-env\"\n" +
		"if [ -f \"$HOME/fake-sleep\" ]; then sleep 5; fi\n" +
		"cat \"$HOME/fake-status\" 2>/dev/null\n" +
		"exit $(cat \"$HOME/fake-exit\" 2>/dev/null || echo 0)\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// claudeHome 造一个 claude HOME：.claude.json（oauthAccount 可缺）+ 假 CLI 的输出与退出码。
func claudeHome(t *testing.T, oauth map[string]any, status string, exit int) string {
	t.Helper()
	h := t.TempDir()
	if oauth != nil {
		raw, _ := json.Marshal(map[string]any{"numStartups": 3, "oauthAccount": oauth})
		writeHomeFile(t, h, ".claude.json", string(raw))
	}
	if status != "" {
		writeHomeFile(t, h, "fake-status", status)
	}
	writeHomeFile(t, h, "fake-exit", fmt.Sprint(exit))
	return h
}

func claudeCalls(t *testing.T, home string) int {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, "claude-calls"))
	if err != nil {
		return 0
	}
	return strings.Count(string(raw), "\n")
}

func TestLoginsClaudeTable(t *testing.T) {
	if !procGroupSupported {
		t.Skip("claude 登录判据需要进程组（unix）")
	}
	logs := captureLogs(t)
	t.Setenv("CLAUDE_CONFIG_DIR", "/should/be/dropped")
	t.Setenv("ANTHROPIC_API_KEY", sentinelKey)
	bin := fakeClaude(t)
	account := map[string]any{"accountUuid": sentinelUUID, "emailAddress": "cl@example.com", "organizationUuid": "org-SENTINEL"}
	in := `{"loggedIn":true,"authMethod":"sentinel-method"}`
	out := `{"loggedIn":false,"authMethod":"none"}`
	loggedIn := claudeHome(t, account, in, 0)
	slow := claudeHome(t, account, in, 0)
	writeHomeFile(t, slow, "fake-sleep", "1")
	noFile := claudeHome(t, nil, in, 0)
	cases := []findingCase{
		{"已登录：身份取 accountUuid、label 取 emailAddress", loggedIn, &LoginFinding{Kind: KindClaude, Source: "claude-auth",
			Fingerprint: Fingerprint(sentinelUUID), Label: "cl@example.com", Refreshable: true}, false},
		{"oauthAccount 在但 loggedIn=false（R0-1 旧缓存）：不认", claudeHome(t, account, out, 0), nil, false},
		{"未登录且非零退出：不认、不报问题", claudeHome(t, account, out, 1), nil, false},
		{"没有 .claude.json：不认、不起子进程", noFile, nil, false},
		{"已登录但 oauthAccount 缺 accountUuid", claudeHome(t, map[string]any{"emailAddress": "cl@example.com"}, in, 0), nil, true},
		{"CLI 输出不是 JSON", claudeHome(t, account, "garbage "+sentinelKey, 0), nil, true},
		{"CLI 超时：报问题（本轮不下已失效结论）", slow, nil, true},
	}
	var dump bytes.Buffer
	reg := NewRegistry(nil)
	reg.RegisterLoginReader(KindClaude, &Claude{Binary: bin, Timeout: 500 * time.Millisecond})
	runFindingTable(t, reg, cases, &dump)
	if n := claudeCalls(t, noFile); n != 0 {
		t.Fatalf("没有 .claude.json 不应起 claude 子进程，起了 %d 次", n)
	}
	envRaw, err := os.ReadFile(filepath.Join(loggedIn, "claude-env"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(envRaw)); got != "HOME="+loggedIn+" CLAUDE_CONFIG_DIR= ANTHROPIC_API_KEY=" {
		t.Fatalf("claude 子进程环境应只以登录 HOME 判定（去掉 CLAUDE_CONFIG_DIR/ANTHROPIC_API_KEY）：%q", got)
	}
	callsRaw, _ := os.ReadFile(filepath.Join(loggedIn, "claude-calls"))
	if strings.TrimSpace(string(callsRaw)) != "auth status --json" {
		t.Fatalf("claude 调用参数 = %q", callsRaw)
	}
	assertNoSecret(t, dump.String()+logs.String(), sentinelKey, sentinelUUID, "org-SENTINEL")

	// CLI 不在：不认、不报问题（该机没装 claude）
	missing := NewRegistry(nil)
	missing.RegisterLoginReader(KindClaude, &Claude{Binary: filepath.Join(t.TempDir(), "no-claude")})
	if got, problems := missing.Logins(loggedIn); len(got) != 0 || len(problems) != 0 {
		t.Fatalf("claude 未安装应静默不认：%+v %v", got, problems)
	}
}

// TestLoginsClaudeCachesByFileState 锁住「凭据文件不变不重跑 CLI」：发现节拍 60s，逐 HOME 起
// node 子进程的成本只在 .claude.json / .claude/.credentials.json 变化后付。
func TestLoginsClaudeCachesByFileState(t *testing.T) {
	if !procGroupSupported {
		t.Skip("claude 登录判据需要进程组（unix）")
	}
	bin := fakeClaude(t)
	home := claudeHome(t, map[string]any{"accountUuid": sentinelUUID, "emailAddress": "cl@example.com"}, `{"loggedIn":true}`, 0)
	c := &Claude{Binary: bin, Timeout: 2 * time.Second}
	for i := 0; i < 3; i++ {
		if got, problems := c.Logins(home); len(got) != 1 || len(problems) != 0 {
			t.Fatalf("第 %d 次：%+v %v", i+1, got, problems)
		}
	}
	if n := claudeCalls(t, home); n != 1 {
		t.Fatalf("文件不变应只跑一次 CLI，跑了 %d 次", n)
	}
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(filepath.Join(home, ".claude.json"), later, later); err != nil {
		t.Fatal(err)
	}
	writeHomeFile(t, home, "fake-status", `{"loggedIn":false}`)
	if got, _ := c.Logins(home); len(got) != 0 {
		t.Fatalf(".claude.json 变化后应重跑并读到未登录：%+v", got)
	}
	if n := claudeCalls(t, home); n != 2 {
		t.Fatalf("文件变化后应重跑一次，共 %d 次", n)
	}
}

func TestLoginsAPIKeyEntriesAreThirdPartyKeys(t *testing.T) {
	logs := captureLogs(t)
	home := writeOpencodeAuth(t, `{"opencode-go":{"type":"api","key":"`+sentinelKey+`"},`+
		`"deepseek":{"type":"api","key":"`+sentinelDS+`"},`+
		`"anthropic":{"type":"oauth","access":"`+sentinelKey+`","refresh":"`+sentinelKey+`"},`+
		`"empty":{"type":"api","key":""}}`)
	got, problems := ogRegistry().Logins(home)
	if len(problems) != 0 {
		t.Fatalf("problems: %v", problems)
	}
	var pairs []string
	for _, f := range got {
		pairs = append(pairs, f.Kind+"/"+f.Provider+"/"+f.Source+"="+f.Fingerprint)
		if f.ChangedAt == nil {
			t.Fatalf("应带凭据变更时刻：%+v", f)
		}
		if f.Kind == KindAPIKey && (f.ExpiresAt != nil || f.Label != "") {
			t.Fatalf("第三方 key 无到期、无 label：%+v", f)
		}
	}
	sort.Strings(pairs)
	want := []string{"apikey/deepseek/auth.json=" + Fingerprint(sentinelDS), "opencode-go//auth.json=" + Fingerprint(sentinelKey)}
	if fmt.Sprint(pairs) != fmt.Sprint(want) {
		t.Fatalf("登录 = %v，want %v（type=oauth 第三方条目与空 key 不认，Q12）", pairs, want)
	}
	raw, _ := json.Marshal(got)
	assertNoSecret(t, string(raw)+logs.String(), sentinelKey, sentinelDS)
}

func TestLoginsOpencodeDBRefreshAndChangedAt(t *testing.T) {
	home := t.TempDir()
	writeOpencodeDB(t, home, loginRow{id: "acc_SENTINEL_id", org: "org_SENTINEL", token: sentinelKey})
	got := mustFindings(t, home)
	if len(got) != 1 || !got[0].Refreshable || got[0].ChangedAt == nil {
		t.Fatalf("opencode.db 登录应带 refresh 与凭据变更时刻：%+v", got)
	}
}

func TestRegistryLoginsKeepFilterSkipsKinds(t *testing.T) {
	bin := fakeClaude(t)
	home := claudeHome(t, map[string]any{"accountUuid": sentinelUUID}, `{"loggedIn":true}`, 0)
	writeHomeFile(t, home, codexRel, `{"tokens":{"access_token":"`+fakeJWT(time.Now().Add(time.Hour), sentinelKey)+`","account_id":"`+sentinelAcct+`"}}`)
	reg := NewRegistry(map[string]Prober{KindCodex: &Codex{CredRelPath: codexRel}})
	reg.RegisterLoginReader(KindClaude, &Claude{Binary: bin, Timeout: 2 * time.Second})
	all, _ := reg.Logins(home)
	if len(all) != 2 {
		t.Fatalf("不过滤应认出 claude + codex：%+v", all)
	}
	only, _ := reg.Logins(home, func(kind string) bool { return kind != KindClaude })
	if len(only) != 1 || only[0].Kind != KindCodex {
		t.Fatalf("过滤掉 claude 后只剩 codex：%+v", only)
	}
	if n := claudeCalls(t, home); n != 1 {
		t.Fatalf("被过滤的 kind 不得起子进程：claude 共跑 %d 次，want 1", n)
	}
}
