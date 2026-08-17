package shell_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/desktop/internal/shell"
	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/initflow"
)

// fakeTransport 记录发出的问题，并按预置答案逐个应答。
type fakeTransport struct {
	asked   []shell.Question
	answers []string
	notices []string
}

func (f *fakeTransport) Ask(q shell.Question) (string, error) {
	f.asked = append(f.asked, q)
	if len(f.answers) == 0 {
		return "", errors.New("测试用例没有预置足够的答案")
	}
	a := f.answers[0]
	f.answers = f.answers[1:]
	return a, nil
}

func (f *fakeTransport) Notice(line string) { f.notices = append(f.notices, line) }

func TestEventPrompterSelectReturnsAnswer(t *testing.T) {
	tr := &fakeTransport{answers: []string{"executor"}}
	p := shell.NewEventPrompter(context.Background(), tr)

	opts := []initflow.Option{{Value: "coordinator", Label: "协调者"}, {Value: "executor", Label: "执行机"}}
	got, err := p.Select("这台机器的角色", opts, "coordinator")
	if err != nil {
		t.Fatalf("Select 返回错误：%v", err)
	}
	if got != "executor" {
		t.Errorf("Select=%q, want %q", got, "executor")
	}
	if len(tr.asked) != 1 {
		t.Fatalf("应当只发出 1 个问题，实际 %d", len(tr.asked))
	}
	q := tr.asked[0]
	if q.Kind != "select" || q.Title != "这台机器的角色" || q.Default != "coordinator" {
		t.Errorf("问题描述不对：%+v", q)
	}
	if len(q.Options) != 2 || q.Options[1].Value != "executor" {
		t.Errorf("选项没有原样传给前端：%+v", q.Options)
	}
}

// 空答案必须落回默认值——这是「一路回车保持不变」在 GUI 侧的对应物。
func TestEventPrompterEmptyAnswerFallsBackToDefault(t *testing.T) {
	tr := &fakeTransport{answers: []string{""}}
	p := shell.NewEventPrompter(context.Background(), tr)
	got, err := p.Input("执行者模型", "sonnet")
	if err != nil {
		t.Fatalf("Input 返回错误：%v", err)
	}
	if got != "sonnet" {
		t.Errorf("Input=%q, want 默认值 %q", got, "sonnet")
	}
}

func TestEventPrompterConfirmParsesBool(t *testing.T) {
	tr := &fakeTransport{answers: []string{"false"}}
	p := shell.NewEventPrompter(context.Background(), tr)
	got, err := p.Confirm("自动同步", true)
	if err != nil {
		t.Fatalf("Confirm 返回错误：%v", err)
	}
	if got {
		t.Error("Confirm 应当返回 false")
	}
}

// 用户关掉向导窗口 = 取消。必须映射成 initflow.ErrCanceled，
// 因为 cmd 侧靠它决定「不写盘」——半截答案落盘比取消本身更糟。
func TestEventPrompterCancelMapsToErrCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := shell.NewEventPrompter(ctx, &fakeTransport{answers: []string{"x"}})
	if _, err := p.Input("随便问", "d"); !errors.Is(err, initflow.ErrCanceled) {
		t.Fatalf("取消后应返回 initflow.ErrCanceled，实际 %v", err)
	}
}

// AskAll 写给 io.Writer 的说明文字在 GUI 里不能凭空丢掉：
// warnIfNotReady 之类的警告是用户必须看到的。
func TestNoticeWriterForwardsNonBlankLines(t *testing.T) {
	tr := &fakeTransport{}
	w := shell.NewNoticeWriter(tr)
	if _, err := w.Write([]byte("\n第一行\n\n第二行\n")); err != nil {
		t.Fatalf("Write 返回错误：%v", err)
	}
	if len(tr.notices) != 2 {
		t.Fatalf("应当转发 2 条非空通知，实际 %d：%v", len(tr.notices), tr.notices)
	}
	if !strings.Contains(tr.notices[0], "第一行") {
		t.Errorf("通知内容不对：%v", tr.notices)
	}
}

// 整条链路：真的用 EventPrompter 驱动 initflow.AskAll 跑一遍。
// 这是本 task 存在的理由——不是「实现了一个接口」，而是
// 「AskAll 一行不改就能驱动 GUI」。
func TestAskAllRunsThroughEventPrompter(t *testing.T) {
	tr := &fakeTransport{answers: []string{"coordinator", "true", ""}}
	p := shell.NewEventPrompter(context.Background(), tr)
	cfg := newTestConfig()

	done := make(chan error, 1)
	go func() {
		_, _, err := initflow.AskAll(shell.NewNoticeWriter(tr), p, cfg, nil, false)
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("AskAll 经 EventPrompter 失败：%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("AskAll 超时未返回：EventPrompter 可能死锁了")
	}
	if len(tr.asked) == 0 {
		t.Error("一个问题都没发给前端")
	}
}

// newTestConfig 给 AskAll 一个最小可用配置。协调者分支只碰
// Sync.Auto 与 Targets：零值即可，无需额外填充。
func newTestConfig() *config.Config {
	return &config.Config{}
}
