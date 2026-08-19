// frames_stream.go —— 结构化回合帧（frames.jsonl）的流式读取接口。
//
// 职责：
//   - 按 offset / tail 截取 frames.jsonl 并写出；follow=1 时持续追送增量
//   - **只在完整行边界切**：ndjson 的消费方按行解析，半行会让它解析失败
//   - 通过响应头告知客户端当前文件大小，供断线续传对齐
//
// 边界：
//   - 不解析帧内容：本文件只认换行符，不认 JSON 里有什么
//   - 不做轮转/清理：frames.jsonl 随任务目录走
//   - 不是事件流：控制面事件走 /ws/events，本接口服务「回合过程的完整复现」
//
// 与 render_stream.go 的关系：形态刻意照抄（同样的参数语义、轮询间隔、心跳、
// 文件不存在返回 200 空内容），唯一的实质差异是行边界对齐。两者共用
// renderStartOffset / copyFrom / renderPollInterval / renderHeartbeat，
// 避免两份会漂移的偏移语义。
package agentd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/Xsxdot/handoff/internal/executor/turn"
)

// framesDefaultTail 是不带参数时从尾部回溯的字节数。
// 与 render 保持一致：两个接口的「默认看多少」不该无缘无故不同。
const framesDefaultTail = renderDefaultTail

// handleTaskFrames 流式输出任务的 frames.jsonl。
//
// 查询参数（语义与 /render 完全一致，单位都是**字节**）：
//   - offset: 起始字节偏移；与 tail 互斥，两者都不给时按 framesDefaultTail 回溯
//   - tail:   从文件尾部回溯的字节数
//   - follow: 1 表示到达文件尾后不关闭连接，持续追送增量
//
// 响应：200 + application/x-ndjson 流；响应头 X-Handoff-Frames-Size 为响应
// 开始时的文件大小。
//
// 注意：
//   - frames.jsonl 尚不存在时返回 200 空内容而非 404——任务刚 dispatch、
//     模型还没产出第一帧是正常状态（与 /render 同一处置）
//   - 客户端断开时 r.Context() 被取消，本函数随即返回，不留 goroutine
func (s *Server) handleTaskFrames(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	if _, ok := s.taskRepoOrErr(w, taskID); !ok {
		return // taskRepoOrErr 已写 404
	}
	framesPath := filepath.Join(s.conf().DataDir, "tasks", taskID, turn.FramesFileName)

	size := renderSize(framesPath)
	offset, err := renderStartOffset(r, size)
	if err != nil {
		s.log.Warn("frames 请求参数非法", "task", taskID, "cause", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	follow := r.URL.Query().Get("follow") == "1"

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("X-Handoff-Frames-Size", strconv.FormatInt(size, 10))
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)

	s.log.Info("frames 流开始", "task", taskID, "offset", offset, "size", size, "follow", follow)
	sent, err := streamFrames(r.Context(), w, flusher, framesPath, offset, follow)
	if err != nil && !errors.Is(err, context.Canceled) {
		s.log.Error("frames 流中断", "task", taskID, "sent", sent, "cause", err)
		return
	}
	s.log.Info("frames 流结束", "task", taskID, "sent", sent)
}

// streamFrames 从 offset 起把 path 的内容按**完整行**写到 w；follow 为真时持续追送。
//
// 返回：已发送字节数与终止原因（客户端断开时返回 ctx.Err()）。
//
// 与 streamRender 的差异只有一处：这里维护一个「已读到但还不完整」的尾部，
// 不把它发出去，下一轮补齐后再连同后续内容一起发。
func streamFrames(ctx context.Context, w io.Writer, flusher http.Flusher,
	path string, offset int64, follow bool) (int64, error) {
	var sent int64
	first := true
	lastBeat := time.Now()
	for {
		chunk, err := readFrom(path, offset)
		if err != nil {
			return sent, err
		}
		if first && len(chunk) > 0 && !startsAtLineStart(path, offset) {
			// 起点不在行首（offset 由 tail 回溯算出，可能落在半行中间）：
			// 跳到下一个完整行的开头，客户端第一行才解析得动。起点在行首时
			// 首行本身完整，不做对齐——对齐反而会把这条完整行吞掉
			aligned := alignToLineStart(chunk)
			offset += int64(len(chunk) - len(aligned))
			chunk = aligned
		}
		first = false
		complete, held := trimIncompleteTail(chunk)
		if len(complete) > 0 {
			n, werr := w.Write(complete)
			sent += int64(n)
			offset += int64(n)
			if werr != nil {
				return sent, werr
			}
			lastBeat = time.Now()
			if flusher != nil {
				flusher.Flush()
			}
		}
		_ = held // 被扣住的残缺尾部不推进 offset，下一轮重读
		if !follow {
			return sent, nil
		}
		// 心跳：ndjson 里一个空行不是合法帧，但按行解析的客户端会跳过空行，
		// 用它保活比自造一种「心跳帧」干净——心跳不该混进数据模型
		if len(complete) == 0 && time.Since(lastBeat) >= renderHeartbeat {
			if _, err := w.Write([]byte("\n")); err != nil {
				return sent, err
			}
			if flusher != nil {
				flusher.Flush()
			}
			lastBeat = time.Now()
		}
		select {
		case <-ctx.Done():
			return sent, ctx.Err()
		case <-time.After(renderPollInterval):
		}
	}
}

// readFrom 读出 path 从 offset 起的全部剩余内容。
//
// 文件不存在返回 (nil, nil)：follow 模式下这是「还没开始产出」的正常状态
// （与 copyFrom 同一处置，只是这里要拿到字节而不是直接搬运）。
func readFrom(path string, offset int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	return io.ReadAll(f)
}

// startsAtLineStart 判断 path 在 offset 处的字节是否恰好位于行首。
//
// 行首 = 文件开头，或紧跟在换行符之后。streamFrames 用它决定首轮要不要走
// alignToLineStart：起点在行首时首行完整，对齐反而会把这条完整行吞掉。
//
// 判据：
//   - offset <= 0：文件开头，必是行首
//   - offset > 0：读 offset-1 处的一个字节，是换行符即行首
//
// 读前一字节失败时返回 false，走对齐路径丢残缺头：该路径只在「readFrom 刚
// 读过、文件紧接着被并发删除/替换」的极端窗口里出现，宁可丢半行，也不能让
// 客户端把残缺首行当成完整帧解析——何况失败概率极低，正常路径读前一字节是
// 可靠的（文件刚被 readFrom 打开读过）。
func startsAtLineStart(path string, offset int64) bool {
	if offset <= 0 {
		return true
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	if _, err := f.Seek(offset-1, io.SeekStart); err != nil {
		return false
	}
	buf := make([]byte, 1)
	n, err := f.Read(buf)
	if err != nil || n != 1 {
		return false
	}
	return buf[0] == '\n'
}

// alignToLineStart 丢掉开头那个不完整的行，返回从第一个完整行开始的切片。
//
// 整段都没有换行时返回空切片：一个完整行都没有，什么也发不了。
func alignToLineStart(b []byte) []byte {
	i := bytes.IndexByte(b, '\n')
	if i < 0 {
		return nil
	}
	// 换行就在末尾、其后没有任何内容：整个切片就是一条完整行，
	// 它本身不是「残缺头部」，原样返回（行首起点一字不丢）。
	if i == len(b)-1 {
		return b
	}
	return b[i+1:]
}

// trimIncompleteTail 把 b 切成「完整行部分」与「被扣住的残缺尾部长度」。
//
// 为什么服务端要保证这件事，而客户端还要再缓冲一层：服务端保证是契约
// （任何客户端都能按行解析），客户端缓冲是防御（代理与中间设备可能在任意
// 字节处切包）。两层都要有。
func trimIncompleteTail(b []byte) (complete []byte, held int) {
	i := bytes.LastIndexByte(b, '\n')
	if i < 0 {
		return nil, len(b)
	}
	return b[:i+1], len(b) - (i + 1)
}
