// stream.go —— out.jsonl 的增量解析（agy stream-json → 内部消息）。
//
// 职责：
//   - 从指定 offset 起持续读 out.jsonl 的新行，宽容解码为 streamMsg 交回调
//   - 维护已消费 offset，供 proc.json 持久化与 agentd 重启后续读
package agy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync/atomic"
	"time"

	"github.com/Xsxdot/handoff/internal/executor/rawtap"
)

// tailPollInterval 是文件无新内容时的轮询间隔。
const tailPollInterval = 200 * time.Millisecond

// garbageLimit 是连续非 JSON 行的上限。
const garbageLimit = 64

type agyInitData struct {
	CWD            string   `json:"cwd"`
	Tools          []string `json:"tools"`
	PermissionMode string   `json:"permission_mode"`
}

type agyToolInfo struct {
	Name       string          `json:"name"`
	Parameters json.RawMessage `json:"parameters"`
	Output     json.RawMessage `json:"output"`
	Error      json.RawMessage `json:"error"`
}

type agyStepUpdateData struct {
	ConversationID  string          `json:"conversation_id"`
	StepIndex       int             `json:"step_index"`
	State           string          `json:"state"` // "ACTIVE", "DONE", "ERROR"
	StepType        string          `json:"step_type"` // "user_input", "agent_response", "tool", "system_message"
	TextDelta       string          `json:"text_delta"`
	ToolName        string          `json:"tool_name"`
	ToolInfo        *agyToolInfo    `json:"tool_info"`
	DurationSeconds float64         `json:"duration_seconds"`
	Usage           *AgyUsageRaw    `json:"usage"`
}

type agyResultData struct {
	ConversationID  string       `json:"conversation_id"`
	Status          string       `json:"status"` // "SUCCESS", "ERROR"
	Response        string       `json:"response"`
	Error           string       `json:"error"`
	DurationSeconds float64      `json:"duration_seconds"`
	NumTurns        int          `json:"num_turns"`
	Usage           *AgyUsageRaw `json:"usage"`
}

// streamMsg 是一行 stream-json 的解码结果。
type streamMsg struct {
	Event          string             `json:"event"`
	ConversationID string             `json:"conversation_id"`
	Init           *agyInitData       `json:"init"`
	StepUpdate     *agyStepUpdateData `json:"step_update"`
	Result         *agyResultData     `json:"result"`

	// handoff_exit 哨兵
	Type     string `json:"type"`
	ExitCode int    `json:"code"`
}

// tailer 从指定 offset 增量读 out.jsonl。
type tailer struct {
	path   string
	log    *slog.Logger
	offset atomic.Int64
	rawTap *rawtap.Tap
}

func newTailer(path string, offset int64, log *slog.Logger) *tailer {
	t := &tailer{path: path, log: log}
	t.offset.Store(offset)
	return t
}

func (t *tailer) Offset() int64 {
	return t.offset.Load()
}

func (t *tailer) Run(ctx context.Context, onMsg func(streamMsg)) error {
	t.log.Info("tailer 启动", "path", t.path, "offset", t.Offset())
	defer t.rawTap.Close()
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

func (t *tailer) scanOnce(onMsg func(streamMsg), garbage *int) (int64, error) {
	f, err := os.Open(t.path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
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
		if line[len(line)-1] != '\n' {
			break
		}
		consumed += int64(len(line))
		t.offset.Add(int64(len(line)))
		t.rawTap.Write(line)

		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			continue
		}
		var msg streamMsg
		if err := json.Unmarshal(trimmed, &msg); err != nil {
			*garbage++
			t.log.Debug("跳过非 JSON 行", "path", t.path, "garbage", *garbage, "line", string(trimmed))
			if *garbage >= garbageLimit {
				return consumed, fmt.Errorf("连续 %d 行非 JSON 数据，流已损坏", *garbage)
			}
			continue
		}
		*garbage = 0
		onMsg(msg)
	}
	return consumed, nil
}
