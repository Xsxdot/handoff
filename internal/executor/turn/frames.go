// frames.go —— 结构化回合帧落盘到 frames.jsonl。
//
// 职责：
//   - 把回合内容（正文/思维链/工具调用/工具结果/事件引用/回合边界）编码成
//     proto.Frame，逐行追加进任务目录的 frames.jsonl
//   - 维护任务内的帧号 seq 与回合号 turn（进程重启后从文件恢复）
//   - 对工具入参/输出做头尾截断，并如实记录原始长度
//
// 边界：
//   - 不认识任何具体 executor（与 AppendRender 同一层，是它的姊妹件）
//   - 不解释帧内容、不做过滤判定：谁该写 reasoning、谁不该，由 adapter 决定
//   - 不轮转、不清理：frames.jsonl 随任务目录走（done 不删任务目录）
//   - 不碰 render.log：两路输出彼此独立
//
// 为什么每次写都开关文件而不是长持文件句柄：与 AppendRender 完全一致的形态，
// 省掉 Close 的生命周期（adapter 重建、进程重启、任务归档三条路径都要管），
// 而帧的写入频率与 AppendRender 同量级，开销可以忽略。
package turn

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/xushixin/handoff/internal/proto"
)

// FramesFileName 是任务目录内帧文件的固定名字。
const FramesFileName = "frames.jsonl"

// frameLineLimit 是单帧编码后的硬上限。
//
// 为什么要有：头尾截断只管 Input/Output 两个字段，将来新增字段时可能悄悄写出
// 巨行把流式读取拖垮。这道闸让那种回归当场变成一条 Warn，而不是线上的卡顿。
const frameLineLimit = 16 << 10

// framesResumeScan 是恢复 seq/turn 时从文件尾部回读的字节数。
// 单帧上限 16KB，回读 64KB 足以覆盖到最后一条完整帧。
const framesResumeScan = 64 << 10

// FrameWriter 把结构化回合帧追加进任务目录的 frames.jsonl。
//
// 并发安全：seq 分配与写入在同一把锁内完成，保证「帧号顺序 == 文件字节顺序」。
// 这不是性能优化的牺牲品——按 offset 续读的客户端依赖这条不变式对齐。
//
// nil 安全：全部方法对 nil 接收者是空操作。构造失败时 adapter 直接持有 nil，
// 调用点不必到处判空——可见性失败不该在正常路径上撒判空代码。
type FrameWriter struct {
	path string
	log  *slog.Logger

	mu       sync.Mutex
	seq      int64
	turn     int
	nextPart int
}

// NewFrameWriter 打开（或准备创建）taskDir 下的 frames.jsonl，并恢复 seq/turn。
//
// 参数：
//   - taskDir: 任务目录（agentd 在 DataDir/tasks/<id> 下创建）
//   - log:     日志入口，可为 nil（测试里常传 nil）
//
// 返回：可用的 FrameWriter；只有 taskDir 不可读时才返回错误。
//
// 注意：文件不存在是正常起点（seq=0, turn=0），不是错误。
func NewFrameWriter(taskDir string, log *slog.Logger) (*FrameWriter, error) {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	w := &FrameWriter{path: filepath.Join(taskDir, FramesFileName), log: log}
	seq, turn, err := resumeFrameState(w.path)
	if err != nil {
		return nil, fmt.Errorf("恢复帧状态 %s: %w", w.path, err)
	}
	w.seq, w.turn = seq, turn
	// 恢复到的位置是「帧流断档」的第一诊断信号：seq 突然回到 0 说明文件被清过
	log.Info("帧写入器就绪", "path", w.path, "resume_seq", seq, "resume_turn", turn)
	return w, nil
}

// BeginTurn 开启新回合：turn 自增、part 计数归零，并写一条 turn_start 帧。
//
// reason 只应是 "dispatch"（Adapter.Start）或 "send"（Adapter.Send）。
func (w *FrameWriter) BeginTurn(reason string) error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	w.turn++
	w.nextPart = 0
	turn := w.turn
	w.mu.Unlock()
	w.log.Info("回合开始", "turn", turn, "reason", reason)
	return w.append(proto.Frame{Type: proto.FrameTurnStart, Reason: reason})
}

// NextPart 分配一个回合内唯一的 part 标识（p01、p02…）。
//
// 上游流自带 part / block / item 标识时**优先沿用上游的**，本方法只服务
// 那些没有标识的流——两个来源混用不会撞车，因为 p 前缀是本方法独有的。
func (w *FrameWriter) NextPart() string {
	if w == nil {
		return ""
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.nextPart++
	return fmt.Sprintf("p%02d", w.nextPart)
}

// Text 写一条模型正文增量帧。
func (w *FrameWriter) Text(part, delta string) error {
	if w == nil || delta == "" {
		return nil
	}
	return w.append(proto.Frame{Type: proto.FrameText, Part: part, Delta: delta})
}

// Reasoning 写一条思维链增量帧。
//
// 注意：本方法只负责落盘。「思维链不能进回合正文」是 adapter 的判定，
// 不在这里——本包不认识回合正文。
func (w *FrameWriter) Reasoning(part, delta string) error {
	if w == nil || delta == "" {
		return nil
	}
	return w.append(proto.Frame{Type: proto.FrameReasoning, Part: part, Delta: delta})
}

// ToolCall 写一条工具调用帧；input 超长时头尾截断。
func (w *FrameWriter) ToolCall(part, tool, input string) error {
	if w == nil {
		return nil
	}
	out, truncated, orig := HeadTail(input, FrameFieldHead, FrameFieldTail)
	if truncated {
		w.log.Debug("工具入参已截断", "tool", tool, "bytes", orig)
	}
	return w.append(proto.Frame{
		Type: proto.FrameToolCall, Part: part, Tool: tool,
		Input: out, Truncated: truncated, Bytes: truncatedBytes(truncated, orig),
	})
}

// ToolResult 写一条工具结果帧；output 超长时头尾截断。
func (w *FrameWriter) ToolResult(part, status, output string) error {
	if w == nil {
		return nil
	}
	out, truncated, orig := HeadTail(output, FrameFieldHead, FrameFieldTail)
	if truncated {
		w.log.Debug("工具输出已截断", "status", status, "bytes", orig)
	}
	return w.append(proto.Frame{
		Type: proto.FrameToolResult, Part: part, Status: status,
		Output: out, Truncated: truncated, Bytes: truncatedBytes(truncated, orig),
	})
}

// EventRef 写一条控制面事件的引用帧。
//
// 只存 seq 与类型名，不复制 payload：payload 的真相在 events 表，复制一份
// 就有了两份会漂移的真相。
func (w *FrameWriter) EventRef(refSeq int64, eventType string) error {
	if w == nil {
		return nil
	}
	return w.append(proto.Frame{Type: proto.FrameEvent, RefSeq: refSeq, Event: eventType})
}

// truncatedBytes 只在确实截断时返回原始长度，否则返回 0（让 omitempty 生效）。
//
// 为什么不无脑返回 orig：未截断的帧带一个 bytes 字段等于告诉前端「这里发生过
// 截断」，是误导。
func truncatedBytes(truncated bool, orig int64) int64 {
	if truncated {
		return orig
	}
	return 0
}

// append 分配 seq、编码成一行并追加进文件。
//
// 注意：seq 分配与写入必须在同一把锁内——分配完再放锁去写，两个 goroutine
// 就可能以 2、1 的顺序落盘，按 offset 续读的客户端会看到 seq 倒退。
func (w *FrameWriter) append(f proto.Frame) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.seq++
	f.Seq = w.seq
	f.Turn = w.turn
	f.TS = time.Now()

	line, err := json.Marshal(f)
	if err != nil {
		return fmt.Errorf("编码帧 seq=%d: %w", f.Seq, err)
	}
	if len(line) > frameLineLimit {
		// 不丢帧也不放行：截断字段兜不住的巨行要能被看见（见 frameLineLimit 注释）
		w.log.Warn("单帧超出行上限，仍照写但请排查字段体量",
			"seq", f.Seq, "type", f.Type, "line_bytes", len(line), "limit", frameLineLimit)
	}
	line = append(line, '\n')

	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("打开 %s: %w", w.path, err)
	}
	defer file.Close()
	if _, err := file.Write(line); err != nil {
		return fmt.Errorf("写 %s: %w", w.path, err)
	}
	return nil
}

// resumeFrameState 从已有的 frames.jsonl 末尾恢复 seq 与 turn。
//
// 返回 (0, 0, nil) 表示文件不存在或没有可解析的完整帧——都是正常起点。
//
// 为什么只回读尾部而不是整文件：帧文件可以很大（数千帧），而恢复只需要最后
// 一条。回读 framesResumeScan 字节，取其中最后一条能解析的完整行即可。
func resumeFrameState(path string) (seq int64, turn int, err error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return 0, 0, err
	}
	start := fi.Size() - framesResumeScan
	if start < 0 {
		start = 0
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return 0, 0, err
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), frameLineLimit*2)
	for sc.Scan() {
		var fr proto.Frame
		// 回读起点可能落在半行中间，第一行解析失败是预期内的，跳过即可
		if json.Unmarshal(sc.Bytes(), &fr) != nil {
			continue
		}
		if fr.Seq > seq {
			seq, turn = fr.Seq, fr.Turn
		}
	}
	// 扫描出错（如超长行）不当致命：宁可从当前已知的最大 seq 接着写
	return seq, turn, nil
}
