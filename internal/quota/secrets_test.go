// secrets_test.go —— 「密钥不离机」承重安全属性（B453 段 2，plan V5）与 opencode-go R1 真样本。
//
// 职责：三家 HTTP 适配器用含哨兵串的假凭据跑成功、401（正文回显 Authorization）、
// 断网、超时四条路径，断言 error 文本、返回读数（含 JSON 序列化）、日志捕获中零出现
// 哨兵串；指纹不是凭据的子串。另锁 R1 真机取得的 opencode-go 401 api_key_expired 正文
// 判 credential_expired。
// 边界：agy 的凭据由 agy CLI 自管、本包不读，故不在本表；其 stderr 只截尾部进原因。
package quota

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestOpencodeGoRealSample401ApiKeyExpired 用 R1（2026-10-22，t453-linux 真实调用
// opencode.ai）取得的错误体：过期 key 判 credential_expired 并指路新建 key。
func TestOpencodeGoRealSample401ApiKeyExpired(t *testing.T) {
	_, err := ParseOpencodeGoUsage(401, readTestdata(t, "opencodego_401_api_key_expired.json"))
	var f *Failure
	if !errors.As(err, &f) || f.Category != CategoryCredentialExpired || !strings.Contains(f.Message, "OpenCode Console") {
		t.Fatalf("真样本 api_key_expired 应判 credential_expired: %v", err)
	}
}

type leakCase struct {
	name  string
	probe func(base string, timeout time.Duration) (Snapshot, error)
}

func leakAdapters(t *testing.T) []leakCase {
	ogHome := writeOpencodeAuth(t, `{"opencode-go":{"type":"api","key":"`+sentinelKey+`"}}`)
	codexHome := writeCodexAuth(t, `{"tokens":{"access_token":"`+fakeJWT(time.Now().Add(time.Hour), sentinelKey)+`","account_id":"acct","refresh_token":"`+sentinelKey+`"}}`)
	grokHome := writeGrokAuth(t, `{"https://auth.x.ai::u":{"key":"`+sentinelKey+`","user_id":"u"}}`)
	return []leakCase{
		{name: "opencode-go", probe: func(base string, to time.Duration) (Snapshot, error) {
			return (&OpencodeGo{BaseURL: base, CredRelPath: testCredRelPath, Timeout: to}).Probe(context.Background(),
				Request{Kind: KindOpencodeGo, Home: ogHome, Entry: "opencode-go"})
		}},
		{name: "codex", probe: func(base string, to time.Duration) (Snapshot, error) {
			return (&Codex{BaseURL: base, CredRelPath: codexRel, Timeout: to}).Probe(context.Background(),
				Request{Kind: KindCodex, Home: codexHome, Entry: "tokens"})
		}},
		{name: "grok", probe: func(base string, to time.Duration) (Snapshot, error) {
			return (&Grok{BaseURL: base, CredRelPath: grokRel, Timeout: to}).Probe(context.Background(),
				Request{Kind: KindGrok, Home: grokHome, Entry: "https://auth.x.ai"})
		}},
	}
}

func TestSecretsNeverLeakAcrossAdapters(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)

	okBodies := map[string]string{
		"opencode-go": `{"usage":{"weekly":{"percent":1}},"echo":"%s"}`,
		"codex":       `{"rate_limit":{"primary_window":{"used_percent":1,"limit_window_seconds":18000}},"echo":"%s"}`,
		"grok":        `{"config":{"creditUsagePercent":1},"echo":"%s"}`,
	}
	for _, ad := range leakAdapters(t) {
		paths := map[string]http.HandlerFunc{
			"成功正文回显头": func(w http.ResponseWriter, r *http.Request) {
				_, _ = fmt.Fprintf(w, okBodies[ad.name], r.Header.Get("Authorization"))
			},
			"401 正文回显头": func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"bad token ` + r.Header.Get("Authorization") + `"}`))
			},
			"超时": func(w http.ResponseWriter, r *http.Request) {
				select {
				case <-r.Context().Done():
				case <-time.After(2 * time.Second):
				}
			},
		}
		for pname, h := range paths {
			t.Run(ad.name+"/"+pname, func(t *testing.T) {
				srv := httptest.NewServer(h)
				defer srv.Close()
				snap, err := ad.probe(srv.URL, 300*time.Millisecond)
				assertNoSentinel(t, snap, err)
			})
		}
		t.Run(ad.name+"/断网", func(t *testing.T) {
			dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			url := dead.URL
			dead.Close()
			snap, err := ad.probe(url, time.Second)
			if err == nil {
				t.Fatal("断网应失败")
			}
			assertNoSentinel(t, snap, err)
		})
	}
	if strings.Contains(logs.String(), sentinelKey) || strings.Contains(logs.String(), "SENTINEL") {
		t.Fatal("日志里出现了凭据")
	}
	if strings.Contains(sentinelKey, strings.TrimPrefix(Fingerprint(sentinelKey), "sha256:")) {
		t.Fatal("指纹是凭据的子串")
	}
}

func assertNoSentinel(t *testing.T, snap Snapshot, err error) {
	t.Helper()
	if err != nil && strings.Contains(err.Error(), "SENTINEL") {
		t.Fatalf("错误文本泄露凭据: %s", err)
	}
	raw, _ := json.Marshal(snap)
	if strings.Contains(string(raw), "SENTINEL") || strings.Contains(fmt.Sprintf("%+v", snap), "SENTINEL") {
		t.Fatalf("读数泄露凭据: %s", raw)
	}
}
