// Package ptyservice 提供 owner agentd 持有的普通 PTY 会话与有界输出 replay。
//
// 本文件职责：
//   - 为每个 PTY session 分配单调输出 seq
//   - 同时按 frame 数和原始字节数限制 replay 内存
//   - cursor 过旧时生成仅含当前保留窗口的 bounded snapshot
//
// 边界：
//   - Ring 不是完整终端模拟器，不解析 ANSI，也不持久化 terminal bytes
//   - 并发保护由 session runtime 承担，Ring 本身不加锁
package ptyservice

// OutputFrame 是 Ring 内部保存的原始终端输出帧。
type OutputFrame struct {
	Seq  int64
	Data []byte
}

// OutputSnapshot 是 cursor 过旧时可用于恢复当前保留窗口的有界快照。
type OutputSnapshot struct {
	ThroughSeq int64
	Data       []byte
}

// Ring 是按 frame 数与字节数双重有界的 FIFO replay ring。
type Ring struct {
	maxFrames int
	maxBytes  int
	frames    []OutputFrame
	bytes     int
	through   int64
}

// NewRing 创建有界输出 ring；非正限制会归一为 1，禁止无界增长。
func NewRing(maxFrames, maxBytes int) *Ring {
	if maxFrames <= 0 {
		maxFrames = 1
	}
	if maxBytes <= 0 {
		maxBytes = 1
	}
	return &Ring{maxFrames: maxFrames, maxBytes: maxBytes}
}

// Append 复制并追加一帧，返回其严格单调 seq。
func (r *Ring) Append(data []byte) OutputFrame {
	r.through++
	copyData := append([]byte(nil), data...)
	frame := OutputFrame{Seq: r.through, Data: copyData}
	r.frames = append(r.frames, frame)
	r.bytes += len(copyData)
	for len(r.frames) > 1 && (len(r.frames) > r.maxFrames || r.bytes > r.maxBytes) {
		r.bytes -= len(r.frames[0].Data)
		r.frames[0] = OutputFrame{}
		r.frames = r.frames[1:]
	}
	// 单帧超过 byte limit 时仍保留尾部而不是整帧丢失；这样 snapshot 至少
	// 能恢复最近输出，且内存上界仍然成立。
	if len(r.frames) == 1 && r.bytes > r.maxBytes {
		last := r.frames[0]
		last.Data = append([]byte(nil), last.Data[len(last.Data)-r.maxBytes:]...)
		r.frames[0] = last
		r.bytes = len(last.Data)
	}
	return frame
}

// Replay 返回 after 之后的保留帧。after 早于窗口前沿时不拼接残历史，而是
// 标记 expired 并返回当前有界 snapshot。
func (r *Ring) Replay(after int64) ([]OutputFrame, bool, OutputSnapshot) {
	snapshot := OutputSnapshot{ThroughSeq: r.through}
	if len(r.frames) > 0 && after < r.frames[0].Seq-1 {
		// snapshot 最多复制整个 ring，只在游标确实过期时付出这笔内存；普通
		// attach 只复制 after 之后的帧，避免每条长连接长期持有无用副本。
		for _, frame := range r.frames {
			snapshot.Data = append(snapshot.Data, frame.Data...)
		}
		return nil, true, snapshot
	}
	replay := make([]OutputFrame, 0, len(r.frames))
	for _, frame := range r.frames {
		if frame.Seq > after {
			replay = append(replay, OutputFrame{Seq: frame.Seq, Data: append([]byte(nil), frame.Data...)})
		}
	}
	return replay, false, snapshot
}

// ThroughSeq 返回最后分配的 seq。
func (r *Ring) ThroughSeq() int64 { return r.through }

// FrameCount 返回当前保留 frame 数。
func (r *Ring) FrameCount() int { return len(r.frames) }

// ByteCount 返回当前保留原始字节数。
func (r *Ring) ByteCount() int { return r.bytes }
