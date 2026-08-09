// stream.go —— out.jsonl 的增量解析（claude stream-json → 内部消息）。
//
// 职责：
//   - 从指定 offset 起持续读 out.jsonl 的新行，宽容解码为 streamMsg 交回调
//   - 维护已消费 offset，供 claude.json 持久化与 agentd 重启后续读
//   - 从 stream_event 中提取模型正文增量（只认 text_delta）
//
// 边界：
//   - 不映射 AdapterEvent、不碰 render.log：那是 adapter.go 的职责
//   - 不管进程死活：哨兵行原样交出，判定在 proc.go / adapter.go
//
// 为什么轮询文件而不是接管管道：进程活在 tmux 里、stdout 经 tee 落盘，
// agentd 重启后没有任何管道可继承，文件 + offset 是唯一能跨重启接续的形态。
package claudecode

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync/atomic"
	"time"
)

// tailPollInterval 是文件无新内容时的轮询间隔。
// 取 200ms：与 opencode 看门狗活跃档同量级，实况延迟人眼不可察，
// 而每任务每天约 43 万次 read 系统调用的成本远低于 fork tmux 进程。
const tailPollInterval = 200 * time.Millisecond

// garbageLimit 是连续非 JSON 行的上限。claude 偶发往 stdout 打非协议内容时可跳过，
// 但连续 64 行全解析失败说明流已实质损坏（脚本形态变化/tee 混入 stderr），
// 继续等下去只是空转——返回错误交 adapter 转 failed。
const garbageLimit = 64

// streamMsg 是一行 stream-json 的宽容解码结果。
//
// 只声明 adapter 用得到的字段：claude 的消息体字段很多且随版本变化，
// 全量建模会让每次 claude 升级都变成一次编译错误。
type streamMsg struct {
	Type      string          `json:"type"`
	Subtype   string          `json:"subtype"`
	SessionID string          `json:"session_id"`
	Message   json.RawMessage `json:"message"`
	Event     json.RawMessage `json:"event"`
	Result    string          `json:"result"`
	IsError   bool            `json:"is_error"`
	ExitCode  int             `json:"code"` // handoff_exit 哨兵携带
}

// tailer 从指定 offset 增量读 out.jsonl。
type tailer struct {
	path   string
	log    *slog.Logger
	offset atomic.Int64 // 已消费的字节数（含换行符）；Run 写、Offset 读
}

// newTailer 创建 out.jsonl 的增量读取器。
//
// 参数：
//   - path: out.jsonl 路径（proc.go 的 tee 落盘目标）
//   - offset: 起始读取位置（0 表示从头；agentd 重启时用 claude.json 持久化的值）
//   - log: 日志入口
func newTailer(path string, offset int64, log *slog.Logger) *tailer {
	t := &tailer{path: path, log: log}
	t.offset.Store(offset)
	return t
}

// Offset 返回已消费的字节 offset（供持久化到 claude.json 供重启续读）。
func (t *tailer) Offset() int64 {
	return t.offset.Load()
}

// Run 持续读新行并把每条解码结果交回调，直到 ctx 取消或流损坏。
//
// 返回：
//   - ctx 取消时返回 nil；连续 garbageLimit 行解析失败（流损坏）返回错误
//
// 注意：
//   - 回调在调用 Run 的 goroutine 内同步执行：回调慢会拖慢消费，但保证顺序
//   - 半行（未以换行符结尾）不推进 offset：下轮轮询会从同一位置重读
func (t *tailer) Run(ctx context.Context, onMsg func(streamMsg)) error {
	t.log.Info("tailer 启动", "path", t.path, "offset", t.Offset())
	garbage := 0
	for {
		adv, err := t.scanOnce(onMsg, &garbage)
		if err != nil {
			t.log.Error("流损坏，中止解析", "path", t.path, "garbage", garbage, "cause", err)
			return err
		}
		if adv == 0 {
			select {
			case <-ctx.Done():
				t.log.Info("tailer 退出", "path", t.path, "offset", t.Offset())
				return nil
			case <-time.After(tailPollInterval):
			}
		}
	}
}

// scanOnce 从当前 offset 读到文件尾，处理所有完整行，返回消费的字节数。
//
// 半行（未以换行符结尾）留在 offset 之外不推进，由下一轮轮询补齐。
func (t *tailer) scanOnce(onMsg func(streamMsg), garbage *int) (int64, error) {
	f, err := os.Open(t.path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil // out.jsonl 还没被 tee 创建：等下一轮
		}
		return 0, fmt.Errorf("打开 %s: %w", t.path, err)
	}
	defer f.Close()
	if _, err := f.Seek(t.offset.Load(), io.SeekStart); err != nil {
		return 0, fmt.Errorf("seek %s: %w", t.path, err)
	}
	r := bufio.NewReader(f)
	var consumed int64
	for {
		line, rerr := r.ReadBytes('\n')
		if len(line) == 0 {
			if rerr == io.EOF {
				break
			}
			if rerr != nil {
				return consumed, fmt.Errorf("读 %s: %w", t.path, rerr)
			}
			continue
		}
		// 半行（EOF 且无换行结尾）不推进 offset，下轮从同一位置重读补齐
		if line[len(line)-1] != '\n' {
			break
		}
		consumed += int64(len(line))
		t.offset.Add(int64(len(line)))

		var m streamMsg
		if jerr := json.Unmarshal(line, &m); jerr != nil {
			*garbage++
			if *garbage >= garbageLimit {
				return consumed, fmt.Errorf("连续 %d 行非 JSON，流已损坏", *garbage)
			}
			t.log.Warn("out.jsonl 非 JSON 行，跳过", "path", t.path,
				"garbage_consecutive", *garbage, "line", truncate(string(line), 80))
		} else {
			*garbage = 0
			onMsg(m)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return consumed, fmt.Errorf("读 %s: %w", t.path, rerr)
		}
	}
	return consumed, nil
}

// truncate 截断日志行预览（按字节，日志用）。
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// textDelta 从 stream_event 里提取模型正文增量。
//
// 返回：
//   - text, true：event 是 content_block_delta 且 delta 是 text_delta
//   - "", false：thinking_delta / signature_delta / 其他——思维链绝不能进
//     render.log 与回合文本（与 opencode 的 reasoning 隔离一致）
func textDelta(ev json.RawMessage) (string, bool) {
	var e struct {
		Type  string `json:"type"`
		Delta struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"delta"`
	}
	if err := json.Unmarshal(ev, &e); err != nil {
		return "", false
	}
	if e.Type != "content_block_delta" || e.Delta.Type != "text_delta" {
		return "", false
	}
	return e.Delta.Text, true
}
