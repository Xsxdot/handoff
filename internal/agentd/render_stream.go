// render_stream.go —— 任务实况（render.log）的流式读取接口。
//
// 职责：
//   - 按 offset / tail 截取 render.log 并写出；follow=1 时持续追送增量
//   - 通过响应头告知客户端当前文件大小，供断线续传对齐
//
// 边界：
//   - 不解析内容：render.log 是模型回合文本的原样增量，本文件只做字节搬运
//   - 不做轮转/清理：render.log 随任务目录在归档时一起走
//   - 不是事件流：结构化事件走 /ws/events，本接口只服务「人要看的实况」
//
// 为什么用轮询而不是 fsnotify：单文件、1s 粒度、任务数量级在个位数，
// 轮询 stat 的成本可以忽略；换 fsnotify 要多一个依赖和一套跨平台差异，
// 而它换来的延迟改善对「人在看文本」这个场景毫无意义。
package agentd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const (
	// renderPollInterval 是 follow 模式下探测文件增长的间隔。
	renderPollInterval = time.Second
	// renderHeartbeat 是无新内容时发送保活字节的间隔：中间设备（代理、NAT）
	// 常在 30–60s 空闲后断开连接，20s 心跳留足余量。
	renderHeartbeat = 20 * time.Second
	// renderDefaultTail 是不带任何参数时从尾部回溯的字节数：跟实况而不刷屏。
	renderDefaultTail = 4 << 10
)

// handleTaskRender 流式输出任务的 render.log。
//
// 查询参数：
//   - offset: 起始字节偏移；与 tail 互斥，两者都不给时按 renderDefaultTail 回溯
//   - tail:   从文件尾部回溯的字节数
//   - follow: 1 表示到达文件尾后不关闭连接，持续追送增量
//
// 响应：200 + text/plain 流；响应头 X-Handoff-Render-Size 为响应开始时的文件大小。
//
// 注意：
//   - render.log 尚不存在时返回 200 空内容而非 404——任务刚 dispatch、模型还没
//     吐第一个字是完全正常的状态，attach 应该连上等着，而不是报错让人以为任务不对
//   - 客户端断开（Ctrl+C）时 r.Context() 被取消，本函数随即返回，不留 goroutine
func (s *Server) handleTaskRender(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	if _, ok := s.taskRepoOrErr(w, taskID); !ok {
		return // taskRepoOrErr 已写 404
	}
	renderPath := filepath.Join(s.conf().DataDir, "tasks", taskID, "render.log")

	size := renderSize(renderPath)
	offset, err := renderStartOffset(r, size)
	if err != nil {
		s.log.Warn("render 请求参数非法", "task", taskID, "cause", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	follow := r.URL.Query().Get("follow") == "1"

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Handoff-Render-Size", strconv.FormatInt(size, 10))
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)

	s.log.Info("render 流开始", "task", taskID, "offset", offset, "size", size, "follow", follow)
	sent, err := streamRender(r.Context(), w, flusher, renderPath, offset, follow)
	if err != nil && !errors.Is(err, context.Canceled) {
		// 客户端主动断开是正常收尾，不是错误；其余情况要能查
		s.log.Error("render 流中断", "task", taskID, "sent", sent, "cause", err)
		return
	}
	s.log.Info("render 流结束", "task", taskID, "sent", sent)
}

// renderSize 返回 render.log 当前字节数；文件不存在时返回 0（见 handleTaskRender 的注意）。
func renderSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// renderStartOffset 依据 offset/tail 参数算出起始偏移。
//
// 优先级：显式 offset > tail > renderDefaultTail。
// offset 超过当前大小时钳到大小（不报错：文件可能刚被归档重建，钳住即可继续 follow）。
func renderStartOffset(r *http.Request, size int64) (int64, error) {
	q := r.URL.Query()
	if v := q.Get("offset"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("offset 非法: %q", v)
		}
		if n > size {
			return size, nil
		}
		return n, nil
	}
	back := int64(renderDefaultTail)
	if v := q.Get("tail"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("tail 非法: %q", v)
		}
		back = n
	}
	if size <= back {
		return 0, nil
	}
	return size - back, nil
}

// streamRender 从 offset 起把 path 的内容写到 w；follow 为真时持续追送增量。
//
// 返回：已发送字节数与终止原因（客户端断开时返回 ctx.Err()）。
//
// 注意：文件不存在时不报错——follow 模式下等它出现即可（任务刚起、模型还没吐字）。
func streamRender(ctx context.Context, w io.Writer, flusher http.Flusher,
	path string, offset int64, follow bool) (int64, error) {
	var sent int64
	lastBeat := time.Now()
	for {
		n, err := copyFrom(w, path, offset)
		if err != nil {
			return sent, err
		}
		if n > 0 {
			offset += n
			sent += n
			lastBeat = time.Now()
			if flusher != nil {
				flusher.Flush()
			}
		}
		if !follow {
			return sent, nil
		}
		// 心跳：长时间无新内容时发一个换行保活。用换行而非注释语法，
		// 因为本接口是纯文本流不是 SSE，客户端直接打印，多一个空行无害
		if n == 0 && time.Since(lastBeat) >= renderHeartbeat {
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

// copyFrom 把 path 从 offset 起的全部剩余内容拷到 w，返回拷贝字节数。
//
// 文件不存在返回 (0, nil)：follow 模式下这是「还没开始产出」的正常状态。
func copyFrom(w io.Writer, path string, offset int64) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("打开 %s: %w", path, err)
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return 0, fmt.Errorf("定位 %s 到 %d: %w", path, offset, err)
	}
	n, err := io.Copy(w, f)
	if err != nil {
		return n, fmt.Errorf("读 %s: %w", path, err)
	}
	return n, nil
}
