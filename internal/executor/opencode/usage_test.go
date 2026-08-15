// opencode 用量解析测试：输入是 08-13 旁听 mac-02 一个 running 任务的
// /event SSE 抓到的真实帧（探针笔记 §3.1）。
package opencode_test

import (
	"testing"

	"github.com/Xsxdot/handoff/internal/executor/opencode"
)

// TestParseMessageUsageAddsCacheNotTotal 覆盖两条规则：缓存要相加；
// **不能取 tokens.total**——total 含 output 与 reasoning，不是 context 占用。
func TestParseMessageUsageAddsCacheNotTotal(t *testing.T) {
	props := []byte(`{"sessionID":"ses_x","info":{"id":"msg_1","role":"assistant",
      "cost":0.0001408596,
      "tokens":{"total":47071,"input":131,"output":182,"reasoning":294,
                "cache":{"write":0,"read":46464}},
      "modelID":"deepseek-v4-flash","providerID":"opencode-go",
      "time":{"created":1786628040082,"completed":1786628048168}}}`)

	model, u, ok := opencode.ParseMessageUsageForTest(props)
	if !ok {
		t.Fatalf("应解析成功")
	}
	if model != "deepseek-v4-flash" {
		t.Fatalf("model = %q", model)
	}
	if u == nil || u.ContextTokens != 46595 {
		t.Fatalf("应为 131+46464+0=46595（不是 total 的 47071），得到 %+v", u)
	}
	if u.ContextWindow != nil {
		t.Fatalf("opencode 不报窗口，ContextWindow 必须是 nil")
	}
}

// TestParseMessageUsageSkipsFreshMessage 覆盖界面陷阱：新建的 assistant 消息
// tokens 全是 0，同一条消息随后才被补完。若不跳过零值帧，界面会在每条新消息
// 开头闪回 0。
func TestParseMessageUsageSkipsFreshMessage(t *testing.T) {
	props := []byte(`{"sessionID":"ses_x","info":{"id":"msg_2","role":"assistant",
      "cost":0,"tokens":{"input":0,"output":0,"reasoning":0,
      "cache":{"read":0,"write":0}},"modelID":"deepseek-v4-flash",
      "time":{"created":1786628048172}}}`)

	model, u, ok := opencode.ParseMessageUsageForTest(props)
	if !ok || model != "deepseek-v4-flash" {
		t.Fatalf("模型名应仍然有效，得到 ok=%v model=%q", ok, model)
	}
	if u != nil {
		t.Fatalf("零值帧不该产生 Usage（否则界面闪回 0），得到 %+v", u)
	}
}

// TestParseMessageUsageIgnoresUserMessage 覆盖角色过滤：只算模型输出侧。
func TestParseMessageUsageIgnoresUserMessage(t *testing.T) {
	props := []byte(`{"info":{"id":"msg_3","role":"user",
      "tokens":{"input":99,"cache":{"read":1}},"modelID":"x"}}`)
	if _, u, _ := opencode.ParseMessageUsageForTest(props); u != nil {
		t.Fatalf("user 消息不该产生 Usage，得到 %+v", u)
	}
}
