// Package wire 是 agentd 与 ptyhost 进程之间的帧格式。
//
// 职责：
//   - 定义帧布局 [类型:1][长度:4 大端][载荷] 与两种帧（PTY 原始字节 / 控制帧 JSON）
//   - 定义控制帧的词汇表与共用载荷形状
//   - 编解码，仅此而已
//
// 边界：
//   - 不认识 socket、不认识会话、不打日志：错误原样上抛，由两侧各自带上下文记录
//   - 不做版本协商：ReadFrame 只管解出来，版本怎么处置是调用方的事
//   - 不解析转义序列：数据帧就是一段字节
//
// 为什么数据走裸字节而不是塞进 JSON：PTY 输出是高频路径，base64 会带来 33% 膨胀
// 加两次编解码。两段连接刻意同形，agentd 在中间转译时不需要状态机。
package wire

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

// ProtoVersion 是当前协议版本。
//
// 只有破坏性变更才 +1。加字段走未知字段忽略，不动版本号。
const ProtoVersion = 1

// 帧类型。
const (
	KindData    byte = 0 // 载荷是 PTY 原始字节
	KindControl byte = 1 // 载荷是 Control 的 JSON
)

// MaxFrame 是单帧载荷的上限（1 MiB）。
//
// 长度前缀来自对端，直接信它去 make([]byte, n) 就是一个可被对端指定大小的分配。
// PTY 单次读取远小于此值，控制帧也只有几百字节，保留余量但不接受任意大分配。
const MaxFrame = 1 << 20

// 控制帧类型。
const (
	CtrlAttach   = "attach"
	CtrlAttached = "attached"
	CtrlResize   = "resize"
	CtrlStat     = "stat"
	CtrlStatResp = "stat_resp"
	CtrlExit     = "exit"
	CtrlKill     = "kill"
)

// Control 是所有控制帧共用的载荷。
//
// ExitCode 用指针表达三态里的两态：缺席 = 还活着，出现 = 已退出且这是退出码。
// Truncated / Foreground 不带 omitempty：false 是有意义的结论。
type Control struct {
	Type         string `json:"type"`
	Since        uint64 `json:"since,omitempty"`
	Truncated    bool   `json:"truncated"`
	ProtoVersion int    `json:"proto_version,omitempty"`
	Cols         int    `json:"cols,omitempty"`
	Rows         int    `json:"rows,omitempty"`
	BytesOut     uint64 `json:"bytes_out,omitempty"`
	Foreground   bool   `json:"foreground"`
	Attached     int    `json:"attached,omitempty"`
	ExitCode     *int   `json:"exit_code,omitempty"`
}

// writeFrame 是两个写入口的共同实现。
func writeFrame(w io.Writer, kind byte, body []byte) error {
	if len(body) > MaxFrame {
		return fmt.Errorf("帧长度 %d 超过上限 %d", len(body), MaxFrame)
	}
	var head [5]byte
	head[0] = kind
	binary.BigEndian.PutUint32(head[1:], uint32(len(body)))
	// 头和体拼成一次 Write，避免调用方即使忘记串行也写出交错的半帧。
	buf := make([]byte, 0, len(head)+len(body))
	buf = append(buf, head[:]...)
	buf = append(buf, body...)
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("写帧（类型 %d，%d 字节）: %w", kind, len(body), err)
	}
	return nil
}

// WriteData 写一个数据帧。
//
// 参数：w 是目标；p 是 PTY 原始字节。本函数在返回前用完 p，调用方之后可以复用它。
// 返回：写失败或长度超上限时报错。
func WriteData(w io.Writer, p []byte) error { return writeFrame(w, KindData, p) }

// WriteControl 写一个控制帧。
//
// 参数：w 是目标；c 是控制内容，Type 可以是本包的 Ctrl* 常量，也可由未来版本扩展。
// 返回：JSON 编码失败或写失败时报错。
func WriteControl(w io.Writer, c Control) error {
	body, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("编码控制帧 %s: %w", c.Type, err)
	}
	return writeFrame(w, KindControl, body)
}

// ReadFrame 读一个帧。
//
// 返回：kind 为 KindData 时 data 是载荷、ctrl 为 nil；kind 为 KindControl 时 ctrl
// 是解出的控制帧、data 为 nil。io.EOF 表示干净关闭，io.ErrUnexpectedEOF 表示半截帧。
// 未知帧类型会被完整读取后忽略，以便未来新增帧类型时旧端保持连接。
//
// 注意：调用方必须区分 io.EOF 与 io.ErrUnexpectedEOF：前者是订阅者走了，后者是对端
// 死了，在 agentd 侧一个该静默一个该记 Warn。
func ReadFrame(r io.Reader) (byte, []byte, *Control, error) {
	var head [5]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		// ReadFull 在一个字节都没读到时给 io.EOF，读了一部分才给 ErrUnexpectedEOF。
		return 0, nil, nil, err
	}
	kind := head[0]
	n := binary.BigEndian.Uint32(head[1:])
	if n > MaxFrame {
		return 0, nil, nil, fmt.Errorf("帧长度 %d 超过上限 %d", n, MaxFrame)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		if err == io.EOF {
			return 0, nil, nil, io.ErrUnexpectedEOF
		}
		return 0, nil, nil, err
	}
	switch kind {
	case KindData:
		return kind, body, nil, nil
	case KindControl:
		var c Control
		if err := json.Unmarshal(body, &c); err != nil {
			return 0, nil, nil, fmt.Errorf("解码控制帧: %w", err)
		}
		return kind, nil, &c, nil
	default:
		return kind, nil, nil, nil
	}
}
