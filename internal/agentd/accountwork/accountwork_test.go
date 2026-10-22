// accountwork_test.go —— 发现器扫描范围与密钥不离机（B464 段 1，plan V3/V4），探测器节拍与
// 「只探可探登录在本机的账号」（B453 S2 回归，随探测器迁入本包）。
//
// 边界：账本是内存 registry（schedclient CAS 契约同款）；额度接口与凭据都是夹具（临时 HOME、
// testhttp），不读本机真实凭据。HTTP 面与生产组装的竖切在 internal/agentd 的 b464 测试。
package accountwork

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/quota"
	"github.com/Xsxdot/handoff/internal/schedclient"
	"github.com/Xsxdot/handoff/internal/scheduling/accounts"
	"github.com/Xsxdot/handoff/internal/testhttp"
)

const (
	sentinelKey = "sk-SENTINEL-b464-accountwork-0123456789"
	credRel     = ".local/share/opencode/auth.json"
)

// memRegistry 是 schedclient.Registry 的内存实现（整行 CAS，与 ledger.RegistryPut 同判据）。
type memRegistry struct {
	mu   sync.Mutex
	rows map[string]schedclient.Record
}

func newMem() *memRegistry { return &memRegistry{rows: map[string]schedclient.Record{}} }

func (m *memRegistry) Put(kind, id string, expect int, body []byte, _ string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur := m.rows[kind+"/"+id]
	if cur.Version != expect {
		return 0, fmt.Errorf("%s/%s: %w", kind, id, schedclient.ErrCASConflict)
	}
	m.rows[kind+"/"+id] = schedclient.Record{ID: id, Version: expect + 1, Body: append([]byte(nil), body...)}
	return expect + 1, nil
}

func (m *memRegistry) Get(kind, id string) (schedclient.Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[kind+"/"+id]
	if !ok {
		return schedclient.Record{}, schedclient.ErrNotFound
	}
	return r, nil
}

func (m *memRegistry) List(kind string) ([]schedclient.Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []schedclient.Record
	for k, r := range m.rows {
		if strings.HasPrefix(k, kind+"/") {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *memRegistry) Delete(kind, id string, expect int, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cur, ok := m.rows[kind+"/"+id]; !ok || cur.Version != expect {
		return schedclient.ErrCASConflict
	}
	delete(m.rows, kind+"/"+id)
	return nil
}

func (m *memRegistry) dump() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var b strings.Builder
	for k, r := range m.rows {
		b.WriteString(k + "=" + string(r.Body) + "\n")
	}
	return b.String()
}

func writeAuth(t *testing.T, home, content string) {
	t.Helper()
	p := filepath.Join(home, credRel)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// recordingReader 记录被问到的 HOME，并对每个 HOME 报一份以路径为身份的假登录。
type recordingReader struct {
	mu    sync.Mutex
	homes []string
}

func (r *recordingReader) Logins(home string) ([]quota.LoginFinding, []error) {
	r.mu.Lock()
	r.homes = append(r.homes, home)
	r.mu.Unlock()
	return []quota.LoginFinding{{Kind: quota.KindOpencodeGo, Source: "auth.json", Fingerprint: quota.Fingerprint(home)}}, nil
}

// TestDiscovererScanScope 是 V3：只扫主 HOME 与 ~/.handoff/home/* 一层真实目录；更深层、普通
// 文件、指向范围外（/etc）的软链都不看。
func TestDiscovererScanScope(t *testing.T) {
	main := t.TempDir()
	root := filepath.Join(main, ".handoff", "home")
	for _, d := range []string{"a", "a/b", "c"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc", filepath.Join(root, "etc-link")); err != nil {
		t.Skipf("不支持软链: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "c"), filepath.Join(root, "inner-link")); err != nil {
		t.Fatal(err)
	}
	reader := &recordingReader{}
	acc := accounts.New(newMem())
	d := NewDiscoverer(DiscovererConfig{Accounts: acc, Quota: reader, Machine: "m1",
		MainHome: func() (string, error) { return main, nil }})
	res, err := d.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(reader.homes)
	want := []string{main, filepath.Join(root, "a"), filepath.Join(root, "c")}
	sort.Strings(want)
	if fmt.Sprint(reader.homes) != fmt.Sprint(want) {
		t.Fatalf("扫描范围 = %v，want %v", reader.homes, want)
	}
	homes := map[string]bool{}
	for _, l := range res.Logins {
		homes[l.Home] = true
	}
	if !homes["~"] || !homes["~/.handoff/home/a"] || !homes["~/.handoff/home/c"] || len(homes) != 3 {
		t.Fatalf("登录 HOME 写法应为 ~ 与 ~/.handoff/home/<dir>：%v", homes)
	}
}

// TestDiscovererSecretsNeverLeak 是 V4 的发现路径：含哨兵的假凭据（opencode-go key、opencode.db
// 不在、坏形状 auth.json）走成功与坏形状两路，日志捕获、账本全部行体、归并结果里零出现
// 哨兵与原始身份；账号 id 与指纹不是凭据子串。
func TestDiscovererSecretsNeverLeak(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)

	main := t.TempDir()
	writeAuth(t, main, `{"opencode-go":{"type":"api","key":"`+sentinelKey+`"}}`)
	bad := filepath.Join(main, ".handoff", "home", "bad")
	writeAuth(t, bad, `{"opencode-go":"`+sentinelKey)
	mem := newMem()
	acc := accounts.New(mem)
	reg := quota.NewRegistry(map[string]quota.Prober{quota.KindOpencodeGo: &quota.OpencodeGo{CredRelPath: credRel}})
	d := NewDiscoverer(DiscovererConfig{Accounts: acc, Quota: reg, Machine: "m1",
		MainHome: func() (string, error) { return main, nil },
		Logger:   slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))})
	res, err := d.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Logins) != 1 || res.Logins[0].Source != "auth.json" {
		t.Fatalf("主 HOME 应认出一条 auth.json 登录：%+v", res.Logins)
	}
	runs, _ := acc.DiscoveryRuns()
	if len(runs) != 1 || len(runs[0].Errors) != 1 || !strings.Contains(runs[0].Errors[0], bad) {
		t.Fatalf("坏形状 HOME 应作为问题记进 discovery_run 并点名路径：%+v", runs)
	}
	raw, _ := json.Marshal(res)
	all := logs.String() + mem.dump() + string(raw)
	if strings.Contains(all, sentinelKey) || strings.Contains(all, sentinelKey[3:15]) {
		t.Fatalf("发现路径泄露了凭据：%s", all)
	}
	id := accounts.AccountID(accounts.KindOpencodeGo, "", quota.Fingerprint(sentinelKey))
	if strings.Contains(sentinelKey, strings.TrimPrefix(id, "opencode-go:")) {
		t.Fatalf("账号 id 不得是凭据子串：%s", id)
	}
}

func TestProbeDueBackoff(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) *time.Time { t := now.Add(-d); return &t }
	fail := &accounts.Problem{Category: "network", Message: "x"}
	cases := []struct {
		name     string
		interval int
		rec      accounts.ReadingRecord
		want     bool
	}{
		{"从未尝试", 15, accounts.ReadingRecord{}, true},
		{"成功后未满间隔", 15, accounts.ReadingRecord{LastAttemptAt: ago(14 * time.Minute)}, false},
		{"成功后满间隔", 15, accounts.ReadingRecord{LastAttemptAt: ago(15 * time.Minute)}, true},
		{"失败 1 次 30s 内", 15, accounts.ReadingRecord{LastAttemptAt: ago(29 * time.Second), LastError: fail, FailureStreak: 1}, false},
		{"失败 1 次满 30s", 15, accounts.ReadingRecord{LastAttemptAt: ago(30 * time.Second), LastError: fail, FailureStreak: 1}, true},
		{"失败 3 次满 2m", 15, accounts.ReadingRecord{LastAttemptAt: ago(2 * time.Minute), LastError: fail, FailureStreak: 3}, true},
		{"失败 3 次未满 2m", 15, accounts.ReadingRecord{LastAttemptAt: ago(119 * time.Second), LastError: fail, FailureStreak: 3}, false},
		{"失败多次封顶为间隔（5 分钟）", 5, accounts.ReadingRecord{LastAttemptAt: ago(5 * time.Minute), LastError: fail, FailureStreak: 20}, true},
		{"失败多次封顶前不到", 5, accounts.ReadingRecord{LastAttemptAt: ago(4 * time.Minute), LastError: fail, FailureStreak: 20}, false},
	}
	for _, tc := range cases {
		v := accounts.View{Account: accounts.Account{IntervalMinutes: tc.interval}, Record: tc.rec}
		if got := probeDue(v, now); got != tc.want {
			t.Fatalf("%s: probeDue = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestProberProbesOnlyLocalProbeLogin 锁段 1 的「可探登录」最小形：每个账号只由其第一条正常
// 登录的所在机探；读数行记下所用登录；未到间隔不重探；可探登录在他机的账号本机不碰。
func TestProberProbesOnlyLocalProbeLogin(t *testing.T) {
	var calls atomic.Int32
	quotaSrv := testhttp.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+sentinelKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"usage":{"rolling":{"percent":34},"weekly":{"percent":58}}}`))
	}))
	main := t.TempDir()
	writeAuth(t, main, `{"opencode-go":{"type":"api","key":"`+sentinelKey+`"}}`)
	acc := accounts.New(newMem())
	for machine, fp := range map[string]string{"b": quota.Fingerprint(sentinelKey), "elsewhere": "sha256:999999999999"} {
		if _, err := acc.Reconcile(accounts.Discovery{Machine: machine, Homes: []accounts.ScannedHome{{Home: "~", HomeAbs: main,
			Logins: []accounts.FoundLogin{{Kind: accounts.KindOpencodeGo, Source: "auth.json", Fingerprint: fp}}}}}); err != nil {
			t.Fatal(err)
		}
	}
	p := NewProber(ProberConfig{
		Accounts: acc,
		Quota: quota.NewRegistry(map[string]quota.Prober{
			quota.KindOpencodeGo: &quota.OpencodeGo{BaseURL: quotaSrv.URL, CredRelPath: credRel}}),
		IsSelf: func(m string) bool { return m == "b" }, Writer: "b",
		ExpandHome: func(h string) (string, error) {
			if h != "~" {
				return "", fmt.Errorf("意外 HOME %q", h)
			}
			return main, nil
		},
	})
	p.scanEvery = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)
	localID := accounts.AccountID(accounts.KindOpencodeGo, "", quota.Fingerprint(sentinelKey))
	deadline := time.Now().Add(5 * time.Second)
	for {
		v, err := acc.Get(localID)
		if err != nil {
			t.Fatal(err)
		}
		if v.Record.Reading != nil {
			if v.Record.Login != v.ProbeLogin || v.ProbeLogin == "" {
				t.Fatalf("读数行应记所用登录 %q，得 %q", v.ProbeLogin, v.Record.Login)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("周期探测未在 5s 内写入本机账号读数")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	p.Stop()
	if calls.Load() != 1 {
		t.Fatalf("到期判据失效：额度接口被调 %d 次，want 1", calls.Load())
	}
	other, err := acc.Get("opencode-go:999999999999")
	if err != nil {
		t.Fatal(err)
	}
	if other.Record.Reading != nil || other.Record.LastAttemptAt != nil {
		t.Fatalf("可探登录在他机的账号不得被本机探测: %+v", other.Record)
	}
	if _, err := p.ProbeNow(context.Background(), "opencode-go:999999999999"); err == nil {
		t.Fatal("停止后立即探测应报未运行")
	}
}
