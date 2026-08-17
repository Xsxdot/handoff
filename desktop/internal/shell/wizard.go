// 本文件把同步阻塞的 initflow.AskAll 桥接到异步 GUI：Select / Input /
// Confirm 各自把问题封装成 Question 发出，阻塞在 Transport.Ask 上等前端
// 答案回来，再落回 initflow 的语义（空答落默认、非法值拒绝、取消映射成
// ErrCanceled）。AskAll 写给 io.Writer 的说明文字由 NoticeWriter 逐行转成
// 前端通知，避免 GUI 里静默丢掉 warnIfNotReady 之类的警告。
//
// 边界：
//   - **不 import Wails**。Transport 是接口而非 Wails 的 app.Event，Wails
//     侧的实现与收发在 desktop/main.go；本包一旦 import Wails 就再也不能用
//     普通 go test 覆盖，而这套桥接逻辑恰恰是最需要测的部分。
//   - 不写配置、不问问题集合——问什么、怎么落盘仍是 initflow 与调用方的职责。
//   - 不记录答案内容（可能含 token 或私有路径），日志只出标题与是否取到。
package shell

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"

	"github.com/Xsxdot/handoff/internal/initflow"
)

// Question 是发给前端的一问。
//
// **刻意不加 json tag**：Go 默认按字段名原样序列化（Kind / Title / Default /
// Options），前端 wizard.ts 的 interface 正是按这些名字读的。加一组小写 tag
// 会让前端拿到一个字段全 undefined 的对象——而且不报错，只是渲染成空白页。
type Question struct {
	// Kind 取 "select" / "input" / "confirm"，决定前端渲染成哪种控件。
	Kind string
	// Title 是问题文本，直接来自 initflow，不在这里改写。
	Title string
	// Default 是预选值；confirm 用 "true" / "false" 表示。
	Default string
	// Options 仅 Kind=="select" 时有意义。
	Options []initflow.Option
}

// Transport 是问答的传输层。**它是接口而不是 Wails 的 app.Event**，
// 因为本包一旦 import Wails 就再也不能用普通 go test 覆盖，
// 而这套桥接逻辑（空答落默认、非法值拒绝、取消映射）恰恰是最需要测的部分。
// Wails 侧的实现在 desktop/main.go。
type Transport interface {
	// Ask 发出一问并阻塞等答案。返回的字符串是原始答案，可能为空。
	Ask(q Question) (string, error)
	// Notice 转发一条面向用户的说明或警告，不需要应答。
	Notice(line string)
}

// eventPrompter 是实现 initflow.Prompter 的事件驱动桥。
// 每次问答先查 ctx，已取消直接返回 ErrCanceled，随后阻塞在 Transport.Ask 上。
type eventPrompter struct {
	ctx context.Context
	tr  Transport
}

// NewEventPrompter 构造 initflow.Prompter 的事件驱动实现。
//
// 参数：
//   - ctx: 向导生命周期。取消（用户关窗 / 上层停止）时问答返回 initflow.ErrCanceled
//   - tr: 问答传输层。Wails 侧实现在 main.go；测试用假实现
func NewEventPrompter(ctx context.Context, tr Transport) initflow.Prompter {
	return &eventPrompter{ctx: ctx, tr: tr}
}

// canceled 把 ctx 取消统一映射成 initflow.ErrCanceled，cmd 侧靠它决定不写盘。
func (p *eventPrompter) canceled(kind, title string, cause error) error {
	slog.Warn("wizard 问答取消", "kind", kind, "title", title, "cause", cause)
	return fmt.Errorf("wizard %s %q：%w", kind, title, initflow.ErrCanceled)
}

// Select 发一问选一项。空答落默认；答案不在选项 Value 集合里返回错误——
// 不能静默接受，那会把一个非法值写进 config.yaml。
func (p *eventPrompter) Select(title string, options []initflow.Option, def string) (string, error) {
	if err := p.ctx.Err(); err != nil {
		return "", p.canceled("select", title, err)
	}
	q := Question{Kind: "select", Title: title, Default: def, Options: options}
	slog.Debug("wizard select 发问", "title", title)
	ans, err := p.tr.Ask(q)
	if err != nil {
		slog.Error("wizard select 传输失败", "title", title, "cause", err)
		return "", err
	}
	slog.Debug("wizard select 取到答案", "title", title, "answered", ans != "")
	if ans == "" {
		return def, nil
	}
	for _, o := range options {
		if ans == o.Value {
			return ans, nil
		}
	}
	slog.Warn("wizard select 答案不在选项内，拒绝接受", "title", title)
	return "", fmt.Errorf("wizard select %q：答案不在选项内", title)
}

// Input 发一问读一行。空答落默认。
func (p *eventPrompter) Input(title, def string) (string, error) {
	if err := p.ctx.Err(); err != nil {
		return "", p.canceled("input", title, err)
	}
	q := Question{Kind: "input", Title: title, Default: def}
	slog.Debug("wizard input 发问", "title", title)
	ans, err := p.tr.Ask(q)
	if err != nil {
		slog.Error("wizard input 传输失败", "title", title, "cause", err)
		return "", err
	}
	slog.Debug("wizard input 取到答案", "title", title, "answered", ans != "")
	if ans == "" {
		return def, nil
	}
	return ans, nil
}

// Confirm 发一问取布尔。空答落默认；非布尔答案解析失败返回错误。
func (p *eventPrompter) Confirm(title string, def bool) (bool, error) {
	if err := p.ctx.Err(); err != nil {
		return false, p.canceled("confirm", title, err)
	}
	d := "false"
	if def {
		d = "true"
	}
	q := Question{Kind: "confirm", Title: title, Default: d}
	slog.Debug("wizard confirm 发问", "title", title)
	ans, err := p.tr.Ask(q)
	if err != nil {
		slog.Error("wizard confirm 传输失败", "title", title, "cause", err)
		return false, err
	}
	slog.Debug("wizard confirm 取到答案", "title", title, "answered", ans != "")
	if ans == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(ans)
	if err != nil {
		// 错误里不带原始答案：它可能含敏感内容
		slog.Error("wizard confirm 答案不是布尔值", "title", title, "cause", err)
		return false, fmt.Errorf("wizard confirm %q：答案不是布尔值：%w", title, err)
	}
	return b, nil
}

// noticeWriter 把 io.Writer 的字节流按 \n 切行、跳过空行后逐条转发给 Transport。
// 必须缓冲跨 Write 调用的半行：fmt.Fprintf 可能分多次写，若一写就切会
// 把一条通知劈成两半。
type noticeWriter struct {
	tr  Transport
	buf []byte
}

// NewNoticeWriter 构造 io.Writer，把面向用户的说明/警告逐行转成前端通知。
func NewNoticeWriter(tr Transport) io.Writer {
	return &noticeWriter{tr: tr}
}

func (w *noticeWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			// 没有完整行，留在缓冲里等下一次 Write 拼上
			return len(p), nil
		}
		line := strings.TrimSpace(string(w.buf[:i]))
		w.buf = w.buf[i+1:]
		if line != "" {
			w.tr.Notice(line)
		}
	}
}
