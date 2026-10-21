// opencodego_test.go —— opencode-go 额度适配的外部缝回归（B453 段 1）。
//
// 职责：锁两处承重——①响应体解析入口 ParseOpencodeGoUsage 的表（200 三窗口、
// 401、403/EntitlementError、429、5xx、字段缺失、秒/毫秒/字符串时间戳、越界钳制）；
// ②适配器 Probe 的真实路径（读 HOME 下 auth.json 条目 → Bearer 调用 → 解析），
// 含凭据缺失/形态不符与「密钥不外泄」（响应体回显 key 时错误文本不得出现 key）。
// 边界：样本形状来自 b453-facts-orca.md §1 opencode 节的转述（未经真机证实，U1）；
// 真机 R1 取到真样本后替换/补充本表。外部接口一律 httptest，不连 opencode.ai。
package quota

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestOpencodeGoParseUsageTable(t *testing.T) {
	resetSec := mustTime(t, "2026-10-22T10:00:00Z")
	resetMs := mustTime(t, "2026-10-25T00:00:00Z")
	resetStr := mustTime(t, "2026-11-01T00:00:00Z")
	type want struct {
		windows  []Window
		blocked  Category
		failure  Category
		noWindow bool
	}
	cases := []struct {
		name   string
		status int
		body   string
		want   want
	}{
		{
			name:   "200 三窗口 秒/毫秒/字符串时间戳",
			status: 200,
			body: `{"usage":{"rolling":{"percent":34,"resetsAt":` + itoa(resetSec.Unix()) + `},` +
				`"weekly":{"percent":58.5,"resetsAt":` + itoa(resetMs.UnixMilli()) + `},` +
				`"monthly":{"percent":22,"resetsAt":"2026-11-01T00:00:00Z"}}}`,
			want: want{windows: []Window{
				{Key: WindowSession, UsedPercent: 34, ResetsAt: &resetSec},
				{Key: WindowWeekly, UsedPercent: 58.5, ResetsAt: &resetMs},
				{Key: WindowMonthly, UsedPercent: 22, ResetsAt: &resetStr},
			}},
		},
		{
			name:   "200 部分窗口缺失与无重置时刻",
			status: 200,
			body:   `{"usage":{"weekly":{"percent":10}}}`,
			want:   want{windows: []Window{{Key: WindowWeekly, UsedPercent: 10}}},
		},
		{
			name:   "200 越界钳制到 0-100 与数字字符串时间戳",
			status: 200,
			body:   `{"usage":{"rolling":{"percent":130,"resetsAt":"` + itoa(resetSec.Unix()) + `"},"weekly":{"percent":-3}}}`,
			want: want{windows: []Window{
				{Key: WindowSession, UsedPercent: 100, ResetsAt: &resetSec},
				{Key: WindowWeekly, UsedPercent: 0},
			}},
		},
		{name: "200 缺 usage 字段", status: 200, body: `{}`, want: want{failure: CategoryParse}},
		{name: "200 usage 无任何窗口", status: 200, body: `{"usage":{}}`, want: want{failure: CategoryParse}},
		{name: "200 窗口缺 percent", status: 200, body: `{"usage":{"weekly":{"resetsAt":1}}}`, want: want{failure: CategoryParse}},
		{name: "200 非 JSON", status: 200, body: `<html>`, want: want{failure: CategoryParse}},
		{name: "401 凭据无效", status: 401, body: `{"error":"unauthorized"}`, want: want{failure: CategoryUnauthorized}},
		{name: "403 无订阅", status: 403, body: `{}`, want: want{blocked: CategoryNoSubscription, noWindow: true}},
		{name: "EntitlementError 正文", status: 400, body: `{"type":"error","error":{"type":"EntitlementError","message":"no go plan"}}`,
			want: want{blocked: CategoryNoSubscription, noWindow: true}},
		{name: "429 限速", status: 429, body: `slow down`, want: want{failure: CategoryRateLimited}},
		{name: "502 上游故障", status: 502, body: `bad gateway`, want: want{failure: CategoryUpstream}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snap, err := ParseOpencodeGoUsage(tc.status, []byte(tc.body))
			if tc.want.failure != "" {
				var f *Failure
				if !errors.As(err, &f) || f.Category != tc.want.failure {
					t.Fatalf("want failure %s, got snap=%+v err=%v", tc.want.failure, snap, err)
				}
				if strings.TrimSpace(f.Message) == "" {
					t.Fatalf("失败必须带可读原因: %+v", f)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if tc.want.blocked != "" {
				if snap.Blocked == nil || snap.Blocked.Category != tc.want.blocked {
					t.Fatalf("want blocked %s, got %+v", tc.want.blocked, snap.Blocked)
				}
			} else if snap.Blocked != nil {
				t.Fatalf("不应判不可用: %+v", snap.Blocked)
			}
			if tc.want.noWindow {
				if len(snap.Windows) != 0 {
					t.Fatalf("不可用读数不应带窗口: %+v", snap.Windows)
				}
				return
			}
			assertWindows(t, snap.Windows, tc.want.windows)
		})
	}
}

func assertWindows(t *testing.T, got, want []Window) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("窗口数 = %d，want %d：%+v", len(got), len(want), got)
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.Key != w.Key || g.Group != w.Group || g.UsedPercent != w.UsedPercent {
			t.Fatalf("窗口[%d] = %+v，want %+v", i, g, w)
		}
		switch {
		case w.ResetsAt == nil && g.ResetsAt != nil:
			t.Fatalf("窗口[%d] 不应有重置时刻: %v", i, g.ResetsAt)
		case w.ResetsAt != nil && (g.ResetsAt == nil || !g.ResetsAt.Equal(*w.ResetsAt)):
			t.Fatalf("窗口[%d] 重置时刻 = %v，want %v", i, g.ResetsAt, w.ResetsAt)
		}
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// writeOpencodeAuth 在临时 HOME 下按 opencode 的落点写 auth.json，返回 HOME。
func writeOpencodeAuth(t *testing.T, content string) string {
	t.Helper()
	home := t.TempDir()
	path := filepath.Join(home, testCredRelPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

const testCredRelPath = ".local/share/opencode/auth.json"

// sentinelKey 是哨兵密钥：任何错误文本、读数里出现它即泄密。
const sentinelKey = "sk-SENTINEL-b453-0123456789"

func TestOpencodeGoProbeReadsEntryAndSendsBearer(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		_, _ = w.Write([]byte(`{"usage":{"rolling":{"percent":12},"weekly":{"percent":40},"monthly":{"percent":5}}}`))
	}))
	defer srv.Close()
	home := writeOpencodeAuth(t, `{"deepseek":{"type":"api","key":"other"},"opencode-go":{"type":"api","key":"`+sentinelKey+`"}}`)
	authPath := filepath.Join(home, testCredRelPath)
	before, err := os.Stat(authPath)
	if err != nil {
		t.Fatal(err)
	}
	a := &OpencodeGo{BaseURL: srv.URL, CredRelPath: testCredRelPath}
	snap, err := a.Probe(context.Background(), Request{Kind: KindOpencodeGo, Home: home, Entry: "opencode-go"})
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if gotPath != "/zen/go/v1/usage" || gotAuth != "Bearer "+sentinelKey {
		t.Fatalf("请求形状 path=%q auth 前缀对=%v", gotPath, strings.HasPrefix(gotAuth, "Bearer "))
	}
	if len(snap.Windows) != 3 || snap.Windows[1].Key != WindowWeekly || snap.Windows[1].UsedPercent != 40 {
		t.Fatalf("读数 = %+v", snap)
	}
	after, err := os.Stat(authPath)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
		t.Fatal("凭据文件被改动：探测只许只读")
	}
}

func TestOpencodeGoProbeCredentialFailuresAndNoLeak(t *testing.T) {
	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 上游把收到的 Authorization 原样回显进错误正文：错误文本必须把它抹掉。
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad token ` + r.Header.Get("Authorization") + `"}`))
	}))
	defer echo.Close()
	cases := []struct {
		name  string
		auth  *string
		entry string
		want  Category
	}{
		{name: "凭据文件不存在", auth: nil, entry: "opencode-go", want: CategoryCredentialMissing},
		{name: "条目不存在", auth: ptr(`{"deepseek":{"type":"api","key":"x"}}`), entry: "opencode-go", want: CategoryCredentialMissing},
		{name: "条目不是 API key", auth: ptr(`{"opencode-go":{"type":"oauth","access":"` + sentinelKey + `"}}`), entry: "opencode-go", want: CategoryCredentialInvalid},
		{name: "凭据文件非 JSON", auth: ptr(`not json ` + sentinelKey), entry: "opencode-go", want: CategoryCredentialInvalid},
		{name: "401 正文回显 token", auth: ptr(`{"opencode-go":{"type":"api","key":"` + sentinelKey + `"}}`), entry: "opencode-go", want: CategoryUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if tc.auth != nil {
				home = writeOpencodeAuth(t, *tc.auth)
			}
			a := &OpencodeGo{BaseURL: echo.URL, CredRelPath: testCredRelPath}
			snap, err := a.Probe(context.Background(), Request{Kind: KindOpencodeGo, Home: home, Entry: tc.entry})
			var f *Failure
			if !errors.As(err, &f) || f.Category != tc.want {
				t.Fatalf("want %s, got snap=%+v err=%v", tc.want, snap, err)
			}
			if strings.Contains(err.Error(), sentinelKey) || strings.Contains(err.Error(), "SENTINEL") {
				t.Fatalf("错误文本泄露密钥: %s", err.Error())
			}
		})
	}
}

func TestOpencodeGoProbeNetworkFailureAndUnsupportedPlatform(t *testing.T) {
	home := writeOpencodeAuth(t, `{"opencode-go":{"type":"api","key":"`+sentinelKey+`"}}`)
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()
	a := &OpencodeGo{BaseURL: deadURL, CredRelPath: testCredRelPath}
	_, err := a.Probe(context.Background(), Request{Kind: KindOpencodeGo, Home: home, Entry: "opencode-go"})
	var f *Failure
	if !errors.As(err, &f) || f.Category != CategoryNetwork {
		t.Fatalf("断网应判 network，得 %v", err)
	}
	if strings.Contains(err.Error(), sentinelKey) {
		t.Fatalf("错误文本泄露密钥: %s", err.Error())
	}
	none := &OpencodeGo{BaseURL: deadURL}
	_, err = none.Probe(context.Background(), Request{Kind: KindOpencodeGo, Home: home, Entry: "opencode-go"})
	if !errors.As(err, &f) || f.Category != CategoryUnsupportedPlatform {
		t.Fatalf("无凭据落点应判 unsupported_platform，得 %v", err)
	}
}

func TestOpencodeGoRegistryRejectsUnknownKind(t *testing.T) {
	reg := NewRegistry(map[string]Prober{KindOpencodeGo: &OpencodeGo{}})
	_, err := reg.Probe(context.Background(), Request{Kind: "codex", Home: t.TempDir()})
	var f *Failure
	if !errors.As(err, &f) || f.Category != CategoryUnsupportedKind {
		t.Fatalf("未注册 kind 应判 unsupported_kind，得 %v", err)
	}
}

func ptr(s string) *string { return &s }
