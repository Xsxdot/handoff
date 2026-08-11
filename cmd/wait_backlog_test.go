// wait_backlog_test.go —— runFollow 的摘要行输出契约。
//
// 职责：钉住「摘要走 stdout、单行、合法 JSON、type 为 backlog_summary」。
// 边界：不验对账逻辑本身（那在 internal/client 覆盖），只验 cmd 层的接线与输出通道。
package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/client"
	"github.com/xushixin/handoff/internal/proto"
)

func TestBacklogSummaryLineIsSingleJSON(t *testing.T) {
	var sb strings.Builder
	sum := &client.BacklogSummary{
		Type: client.BacklogSummaryType, TaskID: "task-xyz",
		FromSeq: 100, ToSeq: 109, State: proto.TaskStateWaitingAnswer,
		Missed: 2, Stale: 1, Actionable: []proto.Ticket{{ID: "new1", Kind: "gate"}},
	}
	if err := writeBacklogLine(&sb, sum); err != nil {
		t.Fatalf("writeBacklogLine: %v", err)
	}
	out := sb.String()
	if strings.Count(out, "\n") != 1 || !strings.HasSuffix(out, "\n") {
		t.Fatalf("输出 = %q，必须恰好一行（Monitor 按行解析，多一行就多一次唤醒）", out)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &got); err != nil {
		t.Fatalf("摘要行不是合法 JSON: %v（原文 %q）", err, out)
	}
	if got["type"] != client.BacklogSummaryType {
		t.Fatalf("type = %v, want %q", got["type"], client.BacklogSummaryType)
	}
	if _, ok := got["actionable"]; !ok {
		t.Fatal("摘要行必须带 actionable——那是审核者唯一能直接据以 reply 的字段")
	}
}
