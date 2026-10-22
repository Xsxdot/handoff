package cmd

// b453_notice_test.go —— B453 S6/S7 CLI 侧：退出 5 的 notice 块（F-B460-1）与跟重跑链后的
// 输出（产物、续等命令取自链尾新任务）。
import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/spf13/cobra"
)

// F-B460-1：kind: notice 块全文取请求体 notice（不截断、多行原样），答复行恰一条「已知悉」。
func TestPendingTicketNoticeBlock(t *testing.T) {
	notice := "分支任务撞额度，已停下：不自动换人，工作树保留。\n载体：ca@linux-01\n" +
		strings.Repeat("长", 300) + "\n接管见 B459；本工单答「已知悉」即关单，不会在原会话续跑。"
	b, _ := json.Marshal(proto.NoticeRequest{Kind: proto.TicketKindNotice, Reason: "quota_exhausted", Notice: notice, Carrier: "ca"})
	tk := proto.Ticket{ID: "tk-n1", TaskID: "t1", Kind: proto.TicketKindNotice, Request: b}
	var out bytes.Buffer
	if err := writePendingTickets(&out, "t1", "handoff wait t1 --final", []proto.Ticket{tk}); err != nil {
		t.Fatal(err)
	}
	want := "ticket: tk-n1\nkind: notice\n--- 全文 ---\n" + notice + "\n--- 全文完 ---\n" +
		"reply: handoff reply t1 --ticket tk-n1 --answer \"已知悉\"\n" +
		"\nresume: handoff wait t1 --final\n"
	if out.String() != want {
		t.Fatalf("notice 块形状不符 F-B460-1:\n got %q\nwant %q", out.String(), want)
	}
}

// 经 wait --final 端到端：waiting_answer 桩里一张 notice → 退出 5、stdout 是 notice 块。
func TestPendingTicketNoticeExit5ViaWaitFinal(t *testing.T) {
	b, _ := json.Marshal(proto.NoticeRequest{Kind: proto.TicketKindNotice, Notice: "撞额度告知全文"})
	a := b460PendingAgentd(t, proto.Ticket{ID: "tk-n2", TaskID: "t1", Kind: proto.TicketKindNotice, Request: b})
	stdout, _, err := runB452CLI(t, a, t.TempDir(), "wait", "t1", "--final")
	if ExitCode(err) != ExitPendingTicket {
		t.Fatalf("应退出码 5，得 %d %v", ExitCode(err), err)
	}
	if !strings.Contains(stdout, "kind: notice\n--- 全文 ---\n撞额度告知全文\n") ||
		!strings.Contains(stdout, "reply: handoff reply t1 --ticket tk-n2 --answer \"已知悉\"\n") ||
		strings.Contains(stdout, "--approve") {
		t.Fatalf("stdout 应是 notice 块：%q", stdout)
	}
}

// b453ChainClient 是 runFinalWait 的最小替身：WaitFinal 返回已跟过链的结论；记录产物下载的目标。
type b453ChainClient struct {
	out          *client.FinalOutcome
	artifactTask string
	artifactMach string
}

func (c *b453ChainClient) WaitFinal(context.Context, string, string) (*client.FinalOutcome, error) {
	return c.out, nil
}
func (c *b453ChainClient) AttachOn(context.Context, string, string) (*client.AttachInfo, error) {
	return nil, nil
}
func (c *b453ChainClient) Artifacts(_ context.Context, taskID, machine string) (io.ReadCloser, error) {
	c.artifactTask, c.artifactMach = taskID, machine
	return nil, nil
}

// V13 CLI：跟链后产物按链尾任务与其机器取；stderr 说明已跟链；非完成时错误与续等命令点名新任务。
func TestFinalWaitAfterRerunChainUsesTailTask(t *testing.T) {
	cli := &b453ChainClient{out: &client.FinalOutcome{
		Task:      proto.Task{ID: "t2", State: proto.TaskStateCompleted, ArtifactsDir: "/nonexistent/b453/t2/artifacts"},
		Event:     proto.Event{Type: proto.EventTypeCompleted},
		Completed: true, FinalText: "新任务结论", HasFinalText: true,
		Machine:      "linux-01",
		FollowedFrom: []proto.TaskRef{{Task: "t1", Machine: "mac-mini"}},
	}}
	var stdout, stderr bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetContext(context.Background())
	if err := runFinalWait(cmd, cli, "t1", "mac-mini", 0); err != nil {
		t.Fatalf("runFinalWait: %v", err)
	}
	if stdout.String() != "新任务结论\n" {
		t.Fatalf("stdout 应是新任务结论，得 %q", stdout.String())
	}
	if cli.artifactTask != "t2" || cli.artifactMach != "linux-01" {
		t.Fatalf("产物应按链尾 t2@linux-01 取，得 %s@%s", cli.artifactTask, cli.artifactMach)
	}
	if !strings.Contains(stderr.String(), "t1") || !strings.Contains(stderr.String(), "t2") {
		t.Fatalf("stderr 应说明已从 t1 跟到 t2：%q", stderr.String())
	}

	cli.out = &client.FinalOutcome{Task: proto.Task{ID: "t3", State: proto.TaskStateFailed},
		Event: proto.Event{Type: proto.EventTypeFailed}, Reason: "额度耗尽；没有可换的成员", Machine: "linux-02",
		FollowedFrom: []proto.TaskRef{{Task: "t1"}, {Task: "t2", Machine: "linux-01"}}}
	err := runFinalWait(cmd, cli, "t1", "", 0)
	if ExitCode(err) != ExitTaskFailed || !strings.Contains(err.Error(), "t3") || !strings.Contains(err.Error(), "没有可换的成员") {
		t.Fatalf("链尾失败应退出 3 并点名 t3，得 %d %v", ExitCode(err), err)
	}
}
