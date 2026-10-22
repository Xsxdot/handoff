// b453_turn_error_test.go —— B453 Q14：opencode 把额度/授权类错误写在 stdout 的 error 事件行
// （`run --format json`）里，回合错误必须带出这段文本，分类（quota.ClassifyTurnFailure）才看得到。
//
// 事件形状来自上游 MessageV2 错误联合（name + data{message, statusCode, responseBody}），
// R6a 真样本到后替换为真样本回放。
package hostapi

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// installErrorEventCLI 装一个回放「step_start + error 事件行」后以 exitCode 退出的假 opencode。
func installErrorEventCLI(t *testing.T, exitCode string) {
	t.Helper()
	binDir := t.TempDir()
	script := `#!/bin/sh
printf '%s\n' '{"type":"step_start","sessionID":"ses_err","part":{"type":"step-start"}}'
printf '%s\n' '{"type":"error","timestamp":1,"sessionID":"ses_err","error":{"name":"APIError","data":{"message":"Forbidden: ENTITLEMENT_ERROR model not in plan","statusCode":403,"isRetryable":false,"responseBody":"{\"error\":{\"code\":\"ENTITLEMENT_ERROR\"}}"}}}'
echo "Error: Forbidden" >&2
exit ` + exitCode + `
`
	if err := os.WriteFile(filepath.Join(binDir, "opencode"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// 非零退出：错误文本除 stderr 尾部外还要带 stdout error 事件的消息（分类判据所在）。
func TestRunTurnNonzeroExitCarriesErrorEventText(t *testing.T) {
	installErrorEventCLI(t, "1")
	_, err := New().RunTurn(context.Background(), TurnRequest{CLI: "opencode", Workdir: t.TempDir(), Prompt: "p"})
	if err == nil {
		t.Fatal("非零退出应报错")
	}
	for _, want := range []string{"ENTITLEMENT_ERROR model not in plan", "403", "Error: Forbidden"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("回合错误缺 %q：%v", want, err)
		}
	}
}

// 零退出但只有 error 事件、无文本：不得当成功空输出（静默失败族），报错并带消息。
func TestRunTurnZeroExitErrorEventOnlyIsFailure(t *testing.T) {
	installErrorEventCLI(t, "0")
	reply, err := New().RunTurn(context.Background(), TurnRequest{CLI: "opencode", Workdir: t.TempDir(), Prompt: "p"})
	if err == nil {
		t.Fatalf("只有 error 事件的回合不得判成功：%+v", reply)
	}
	if !strings.Contains(err.Error(), "ENTITLEMENT_ERROR model not in plan") {
		t.Fatalf("回合错误缺 error 事件消息：%v", err)
	}
}

func TestOpencodeErrorText(t *testing.T) {
	cases := []struct {
		name, raw string
		want      []string
		empty     bool
	}{
		{name: "api error", raw: `{"name":"APIError","data":{"message":"quota exceeded","statusCode":429,"responseBody":"{\"code\":\"x\"}"}}`,
			want: []string{"APIError", "HTTP 429", "quota exceeded", `{"code":"x"}`}},
		{name: "unknown error", raw: `{"name":"UnknownError","data":{"message":"boom"}}`, want: []string{"UnknownError", "boom"}},
		{name: "name only", raw: `{"name":"MessageOutputLengthError","data":{}}`, want: []string{"MessageOutputLengthError"}},
		{name: "string", raw: `"plain failure"`, want: []string{"plain failure"}},
		{name: "null", raw: `null`, empty: true},
		{name: "absent", raw: ``, empty: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := OpencodeErrorText(json.RawMessage(c.raw))
			if c.empty {
				if got != "" {
					t.Fatalf("want empty, got %q", got)
				}
				return
			}
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Fatalf("%q 缺 %q", got, w)
				}
			}
		})
	}
}

// installReplayCLI 装一个把 testdata 样本原样回放到 stdout、stderr 为空并以 exitCode 退出的假 opencode。
func installReplayCLI(t *testing.T, sample, exitCode string) {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("testdata", "b453", sample))
	if err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	script := "#!/bin/sh\ncat '" + abs + "'\nexit " + exitCode + "\n"
	if err := os.WriteFile(filepath.Join(binDir, "opencode"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// R6a 真样本（2026-10-22 协调者采集，exit 1、stderr 空）：失败文本只在 stdout 的 error 事件行。
// 今天的检测回合 last_error 只有「stderr 尾部: 」空串（R4 实测）；本测锁定原文进入回合错误，
// 且检测分类为需登录类（凭据无效），**不是**额度耗尽。
func TestRunTurnRealKeyExpiredSampleCarriesTextAndIsNotQuota(t *testing.T) {
	installReplayCLI(t, "opencode_key_expired.real.stdout.jsonl", "1")
	_, err := New().RunTurn(context.Background(), TurnRequest{CLI: "opencode", Workdir: t.TempDir(), Prompt: "p"})
	if err == nil {
		t.Fatal("exit 1 应报错")
	}
	for _, want := range []string{"This API key has expired", "HTTP 401", "api_key_expired"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("回合错误缺 %q：%v", want, err)
		}
	}
	got := classifyTurnError("opencode", "opencode-go", err)
	if got.Outcome != WakeNeedLogin {
		t.Fatalf("API key 过期应判需登录（凭据无效），不是 %s：%s", got.Outcome, got.Detail)
	}
}

// R6a 真样本反例：成功回合不误判（有文本、无 error 事件）。
func TestRunTurnRealOkSamplesSucceed(t *testing.T) {
	for _, sample := range []string{"opencode_go_root_ok_not403.real.stdout.jsonl", "opencode_go_haiku_root_ok_not403.real.stdout.jsonl"} {
		t.Run(sample, func(t *testing.T) {
			installReplayCLI(t, sample, "0")
			reply, err := New().RunTurn(context.Background(), TurnRequest{CLI: "opencode", Workdir: t.TempDir(), Prompt: "p"})
			if err != nil {
				t.Fatalf("成功样本被判失败: %v", err)
			}
			if reply.SessionID == "" || strings.TrimSpace(reply.Output) == "" {
				t.Fatalf("成功样本应有会话与文本: %+v", reply)
			}
		})
	}
}
