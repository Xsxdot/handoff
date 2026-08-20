package engine

// ring 是一个定长字节环形缓冲，用于 PTY 输出的断线续传回放。
//
// 核心不变式：写入的第 i 个字节（从 0 计）恒落在 buf[i%len(buf)]。因此只要知道
// 累计写入量 n，就能算出任一绝对序号还在不在环里——不需要额外维护头尾指针。
//
// 并发：ring 自身不加锁，由持有它的 session 统一在锁内调用。
type ring struct {
	buf []byte
	n   uint64 // 累计写入的字节数，也是下一个字节的绝对序号
}

func newRing(size int) *ring {
	return &ring{buf: make([]byte, size)}
}

// total 返回累计写入的字节数，即下一个字节的绝对序号。
func (r *ring) total() uint64 { return r.n }

// write 把 p 追加进环。p 超过环容量时只保留末尾 len(buf) 个字节，
// 但 n 仍按 len(p) 累加——n 是「输出了多少」，不是「留下了多少」。
func (r *ring) write(p []byte) {
	size := len(r.buf)
	if size == 0 {
		r.n += uint64(len(p))
		return
	}
	// 超容量时前面的部分必然会被自己覆盖掉，直接丢，只搬最后一段。
	if len(p) > size {
		r.n += uint64(len(p) - size)
		p = p[len(p)-size:]
	}
	off := int(r.n % uint64(size))
	c := copy(r.buf[off:], p)
	if c < len(p) { // 跨过环尾，剩下的绕回开头
		copy(r.buf, p[c:])
	}
	r.n += uint64(len(p))
}

// since 回取绝对序号 from 之后的全部字节。
//
// 返回：
//   - data: 实际能给出的字节（新分配的副本，调用方可在锁外持有）
//   - start: data 首字节的绝对序号；调用方据此推进自己的游标
//   - truncated: from 早于环里最旧的字节，中间有一段永久丢失
//
// 注意：from 大于 total 时（客户端报了一个未来的游标，通常是换了会话）按当前
// 尾部处理，返回空而不是报错——重连路径上宁可少画一段，也不能把连接打掉。
func (r *ring) since(from uint64) (data []byte, start uint64, truncated bool) {
	size := uint64(len(r.buf))
	oldest := uint64(0)
	if r.n > size {
		oldest = r.n - size
	}
	if from < oldest {
		from, truncated = oldest, true
	}
	if from > r.n {
		from = r.n
	}
	out := make([]byte, 0, r.n-from)
	for i := from; i < r.n; i++ {
		out = append(out, r.buf[i%size])
	}
	return out, from, truncated
}
