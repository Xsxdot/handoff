// replay_spike_test.go —— 真实 opencode SSE 抓包的重放测试。
//
// 职责：
//   - 把 testdata/spike3-events.jsonl、spike5-events.jsonl（opencode 1.18.15
//     serve 模式下 GET /event 的原始响应字节）原样回放给 adapter
//   - 断言 adapter 的事件映射与这两份样本一致：权限识别、reasoning 不泄漏、
//     文本快照与增量对账、idle 回合分类
//
// 边界：
//   - 不 mock SSE 解析：样本按原始字节喂进 streamOnce，走的是生产解析路径
//   - 只断言能从样本直接核对的事实（权限 id/命令、模型可见文本），
//     不把 opencode 的内部顺序假设写进断言
//
// 为什么必须入库：整个 adapter 的事件映射（文本载体是 message.part.updated
// /delta 而非 message.updated、回合结束信号是 session.status、权限事件是
// permission.asked）唯一的依据就是这两份抓包。样本留在本机 .superpowers 目录
// （已被 gitignore）等于结论无法从任何一个 clone 复核——一旦 opencode 改协议，
// 没有任何东西会变红。
package opencode

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/proto"
)

// spikeFixture 描述一份抓包样本及其可核对的事实。
type spikeFixture struct {
	file    string
	session string // 样本里的 opencode 会话 id（会话隔离过滤要匹配它）
	permID  string
	permTxt string
}

var (
	spike3 = spikeFixture{
		file:    "spike3-events.jsonl",
		session: "ses_020cb20e6ffeMOouAzhUg8oF2z",
		permID:  "per_fdf34ff7b001zXZKPUuq5QhoLj",
		permTxt: "bash: echo spike-hi",
	}
	spike5 = spikeFixture{
		file:    "spike5-events.jsonl",
		session: "ses_020c76668ffeQOGKdSfZUdQbUB",
		permID:  "per_fdf38ba18001SCrxGM5Kq2tCE2",
		permTxt: "bash: echo spike-hi",
	}
)

// replayServer 起一个假 opencode server：/event 原样吐出抓包字节，其余端点
// 给出建会话/发 prompt 的最小合法应答。
//
// 参数：
//   - fx: 样本描述（会话 id 用于 CreateSession 的应答，映射层据此做会话隔离）
func replayServer(t *testing.T, fx spikeFixture) *httptest.Server {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", fx.file))
	if err != nil {
		t.Fatalf("读取抓包样本 %s: %v", fx.file, err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			fmt.Fprintf(w, `{"id":%q}`, fx.session)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/prompt_async"):
			io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			w.Write(raw)
			w.(http.Flusher).Flush()
			// 抓包放完后保持连接：真实 /event 是长连接，立即 EOF 会触发重连重放，
			// 让「事件恰好出现一次」的断言不成立
			<-r.Context().Done()
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(func() {
		ts.CloseClientConnections()
		ts.Close()
	})
	return ts
}

// startReplay 用抓包样本驱动一个 adapter 运行态，返回其事件通道。
//
// repoPath 指向一个非 git 目录：兜底分类因此恒定判「无新提交」，
// 回合分类结果不依赖执行测试的仓库状态。
func startReplay(t *testing.T, fx spikeFixture) <-chan executor.AdapterEvent {
	t.Helper()
	ts := replayServer(t, fx)
	taskDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(taskDir, promptFileName), []byte("plan"), 0o644); err != nil {
		t.Fatalf("写 prompt.md: %v", err)
	}
	ad := New(slog.Default())
	ad.idleGrace = 20 * time.Millisecond
	taskID := "replay-" + fx.file
	req := executor.StartReq{
		Task:    proto.Task{ID: taskID, RepoPath: t.TempDir()},
		TaskDir: taskDir,
	}
	if _, err := ad.startRun(t.Context(), req, NewAPI(ts.URL, adapterTestPassword), &fakeProbe{alive: true}); err != nil {
		t.Fatalf("startRun: %v", err)
	}
	t.Cleanup(func() { _ = ad.Stop(taskID) })
	return ad.Events(taskID)
}

// collectReplay 收集事件直到通道静默 quiet 时长（抓包是有限的，收完即静默）。
func collectReplay(t *testing.T, ch <-chan executor.AdapterEvent, quiet time.Duration) []executor.AdapterEvent {
	t.Helper()
	var got []executor.AdapterEvent
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return got
			}
			got = append(got, ev)
		case <-time.After(quiet):
			return got
		}
	}
}

// TestReplaySpike3Permission 验证 spike3 样本重放出唯一一条权限事件，
// 且其 id 与描述与样本一致（权限映射的唯一实证依据）。
func TestReplaySpike3Permission(t *testing.T) {
	got := collectReplay(t, startReplay(t, spike3), 500*time.Millisecond)

	var perms []executor.AdapterEvent
	for _, ev := range got {
		if ev.Type == "permission" {
			perms = append(perms, ev)
		}
	}
	if len(perms) != 1 {
		t.Fatalf("spike3 应恰好产出 1 条权限事件，实际 %d 条（全部事件 %v）", len(perms), typesOf(got))
	}
	if perms[0].PermissionID != spike3.permID {
		t.Errorf("PermissionID = %q, want %q", perms[0].PermissionID, spike3.permID)
	}
	if perms[0].Text != spike3.permTxt {
		t.Errorf("权限描述 = %q, want %q", perms[0].Text, spike3.permTxt)
	}
}

// TestReplaySpike3NoLeak 验证 spike3 里 user 消息原文与模型 reasoning
// 都不会作为回合文本流出（reasoning 泄漏 = 思维链变成面向协调者的提问）。
func TestReplaySpike3NoLeak(t *testing.T) {
	got := collectReplay(t, startReplay(t, spike3), 500*time.Millisecond)

	for _, ev := range got {
		if strings.Contains(ev.Text, "运行 bash 命令") {
			t.Errorf("user 消息原文泄漏进 %s 事件: %q", ev.Type, ev.Text)
		}
		if strings.Contains(ev.Text, "The user wants me to") {
			t.Errorf("模型 reasoning 泄漏进 %s 事件: %q", ev.Type, ev.Text)
		}
	}
}

// TestReplaySpike5Classifies 验证 spike5（完整一轮：权限 → 应答 → 模型输出
// → idle）重放出权限事件，并在回合结束时把模型最终输出交给协调者。
//
// 样本的最终可见文本是 "spike-hi"：无协议 trailer、仓库无新提交 →
// 兜底分类判「转提问交协调者裁决」。
func TestReplaySpike5Classifies(t *testing.T) {
	got := collectReplay(t, startReplay(t, spike5), 800*time.Millisecond)

	var perm, question *executor.AdapterEvent
	for i, ev := range got {
		switch ev.Type {
		case "permission":
			perm = &got[i]
		case "question":
			question = &got[i]
		}
	}
	if perm == nil || perm.PermissionID != spike5.permID {
		t.Fatalf("spike5 应产出权限事件 %s，实际事件 %v", spike5.permID, typesOf(got))
	}
	if question == nil {
		t.Fatalf("spike5 回合结束应产出 question，实际事件 %v", typesOf(got))
	}
	if question.Text != "spike-hi" {
		t.Errorf("回合文本 = %q, want %q（含多余内容说明 reasoning/user 文本混入了回合）",
			question.Text, "spike-hi")
	}
}

// TestReplaySpike5SessionIsolation 验证会话隔离按样本的真实形态生效：
// 抓包里的 sessionID 位于 properties 而非顶层，若过滤只看顶层字段，
// 所有事件都会被当「无会话事件」放行——把会话 id 改掉后必须零事件产出。
func TestReplaySpike5SessionIsolation(t *testing.T) {
	other := spike5
	other.session = "ses_someone_else"
	got := collectReplay(t, startReplay(t, other), 500*time.Millisecond)

	for _, ev := range got {
		if ev.Type == "permission" || ev.Type == "question" || ev.Type == "result" {
			t.Errorf("会话不匹配时不应产出 %s 事件: %+v", ev.Type, ev)
		}
	}
}

// typesOf 汇总事件类型，供断言失败时给出可读现场。
func typesOf(evs []executor.AdapterEvent) []string {
	out := make([]string, 0, len(evs))
	for _, ev := range evs {
		out = append(out, ev.Type)
	}
	return out
}

// TestSpikeFixturesAreRealCaptures 守住样本本身：抓包必须是 SSE 原始字节
// （data: 前缀 + 空行分隔）且带 opencode 版本可辨识的会话事件。
// 样本被误替换成手写 JSON 时，上面几条重放断言就不再是「真实形态」的证据。
func TestSpikeFixturesAreRealCaptures(t *testing.T) {
	for _, fx := range []spikeFixture{spike3, spike5} {
		raw, err := os.ReadFile(filepath.Join("testdata", fx.file))
		if err != nil {
			t.Fatalf("读取抓包样本 %s: %v", fx.file, err)
		}
		if !strings.HasPrefix(string(raw), "data: ") {
			t.Errorf("%s 不是 SSE 原始抓包（应以 \"data: \" 开头）", fx.file)
		}
		var sawSession bool
		for line := range strings.SplitSeq(string(raw), "\n") {
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			var ev sseEvent
			if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &ev) != nil {
				continue
			}
			var prop struct {
				SessionID string `json:"sessionID"`
			}
			_ = json.Unmarshal(ev.Properties, &prop)
			if prop.SessionID == fx.session {
				sawSession = true
				break
			}
		}
		if !sawSession {
			t.Errorf("%s 中找不到会话 %s，样本与测试的会话 id 已失配", fx.file, fx.session)
		}
	}
}
