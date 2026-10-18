// 职责：打洞信令子协议 v1 的唯一编解码处（coordinator ↔ listener，跑在 relay
// 会话的 E2E 密文内）。边界：只认字节，不做 I/O 调度、不认识 relay 控制帧与 E2E；
// 冻结断言见 docs/superpowers/plans/b438-plan.md §6 F1（W1–W15）。
package p2p

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// SignalMagic 开启一条打洞信令会话，也作为应答的开头。与 relay 的
// previewRawMagic 等长（22 字节），listener 一次 Peek 即可三分会话模式。
const SignalMagic = "handoff-p2p-ice-sig-v1"

// SignalVersion 是信令消息 JSON 的版本号；不认识的版本回 unsupported_version。
const SignalVersion = 1

// MaxCandidates 是单条信令消息允许携带的候选数上限。
const MaxCandidates = 32

// maxSignalBody 是消息体长度上限（字节）；越界即 bad_request。
const maxSignalBody = 16384

// 拒绝码（应答 reject 字段）。disabled / unsupported_version 让发起方判
// 「对端不支持」并长退避；busy / bad_request / 未知码按普通失败退避。
const (
	RejectDisabled           = "disabled"
	RejectBusy               = "busy"
	RejectBadRequest         = "bad_request"
	RejectUnsupportedVersion = "unsupported_version"
)

// ErrPeerUnsupported 表示对端不支持打洞：旧 listener（读不到应答魔数）、
// 关闭了 p2p、或不认识本端版本。发起方据此长退避且只打一次 INFO。
var ErrPeerUnsupported = errors.New("p2p: 对端不支持打洞")

// Description 是一端的 ICE 描述：本次会话的 ICE 凭据与候选。Candidates 每项是
// ice.Candidate.Marshal() 的输出——其中含 ufrag，绝不能进日志。
type Description struct {
	Ufrag      string
	Pwd        string
	Candidates []string
}

// RequestError 是 listener 侧解请求失败的分类，Code 即应回给发起方的拒绝码。
type RequestError struct {
	Code  string
	Cause error
}

func (e *RequestError) Error() string {
	return fmt.Sprintf("p2p 信令请求非法（%s）: %v", e.Code, e.Cause)
}

func (e *RequestError) Unwrap() error { return e.Cause }

// RejectedError 是 listener 明确拒绝。Code 为 disabled / unsupported_version 时
// 同时满足 errors.Is(err, ErrPeerUnsupported)。
type RejectedError struct{ Code string }

func (e *RejectedError) Error() string { return "p2p 对端拒绝打洞: " + e.Code }

// Is 让「对端关闭/不认识版本」的拒绝归入 ErrPeerUnsupported。
func (e *RejectedError) Is(target error) bool {
	return target == ErrPeerUnsupported && (e.Code == RejectDisabled || e.Code == RejectUnsupportedVersion)
}

type wireMessage struct {
	V          *int      `json:"v,omitempty"`
	OK         *bool     `json:"ok,omitempty"`
	Reject     string    `json:"reject,omitempty"`
	Ufrag      string    `json:"ufrag,omitempty"`
	Pwd        string    `json:"pwd,omitempty"`
	Candidates *[]string `json:"candidates,omitempty"`
}

// WriteRequest 写一条打洞请求：魔数 ‖ uint32 大端长度 ‖ JSON。
func WriteRequest(w io.Writer, d Description) error {
	v := SignalVersion
	c := nonNil(d.Candidates)
	return writeSignal(w, wireMessage{V: &v, Ufrag: d.Ufrag, Pwd: d.Pwd, Candidates: &c})
}

// WriteAccept 写一条接受应答，携带应答方的 ICE 描述。
func WriteAccept(w io.Writer, d Description) error {
	v, ok := SignalVersion, true
	c := nonNil(d.Candidates)
	return writeSignal(w, wireMessage{V: &v, OK: &ok, Ufrag: d.Ufrag, Pwd: d.Pwd, Candidates: &c})
}

// WriteReject 写一条拒绝应答。
func WriteReject(w io.Writer, code string) error {
	v, ok := SignalVersion, false
	return writeSignal(w, wireMessage{V: &v, OK: &ok, Reject: code})
}

// ReadRequest 读一条完整请求（含魔数）。失败一律返回 *RequestError，其 Code 是
// listener 应回的拒绝码。
func ReadRequest(r io.Reader) (Description, error) {
	m, err := readSignal(r)
	if err != nil {
		return Description{}, &RequestError{Code: RejectBadRequest, Cause: err}
	}
	if m.V == nil {
		return Description{}, &RequestError{Code: RejectBadRequest, Cause: errors.New("缺少 v")}
	}
	if *m.V != SignalVersion {
		return Description{}, &RequestError{Code: RejectUnsupportedVersion, Cause: fmt.Errorf("v=%d", *m.V)}
	}
	d, err := descriptionOf(m)
	if err != nil {
		return Description{}, &RequestError{Code: RejectBadRequest, Cause: err}
	}
	return d, nil
}

// ReadAnswer 读应答。读不齐魔数（EOF/连接关闭）或魔数不符 = 旧 listener，返回
// ErrPeerUnsupported；拒绝应答返回 *RejectedError；接受应答返回对端描述。
func ReadAnswer(r io.Reader) (Description, error) {
	magic := make([]byte, len(SignalMagic))
	if _, err := io.ReadFull(r, magic); err != nil {
		return Description{}, fmt.Errorf("%w: 读应答魔数: %v", ErrPeerUnsupported, err)
	}
	if string(magic) != SignalMagic {
		return Description{}, fmt.Errorf("%w: 应答魔数不符", ErrPeerUnsupported)
	}
	m, err := readBody(r)
	if err != nil {
		return Description{}, fmt.Errorf("p2p 读应答: %w", err)
	}
	if m.OK == nil || !*m.OK {
		if m.Reject == "" {
			return Description{}, &RejectedError{Code: RejectBadRequest}
		}
		return Description{}, &RejectedError{Code: m.Reject}
	}
	return descriptionOf(m)
}

func descriptionOf(m wireMessage) (Description, error) {
	if m.Ufrag == "" || m.Pwd == "" {
		return Description{}, errors.New("ufrag/pwd 为空")
	}
	if m.Candidates == nil {
		return Description{}, errors.New("缺少 candidates")
	}
	if len(*m.Candidates) > MaxCandidates {
		return Description{}, fmt.Errorf("候选数 %d 超过上限 %d", len(*m.Candidates), MaxCandidates)
	}
	return Description{Ufrag: m.Ufrag, Pwd: m.Pwd, Candidates: *m.Candidates}, nil
}

func writeSignal(w io.Writer, m wireMessage) error {
	body, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("p2p 编码信令: %w", err)
	}
	if len(body) > maxSignalBody {
		return fmt.Errorf("p2p 信令消息过长: %d", len(body))
	}
	buf := make([]byte, 0, len(SignalMagic)+4+len(body))
	buf = append(buf, SignalMagic...)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(body)))
	buf = append(buf, body...)
	_, err = w.Write(buf)
	return err
}

func readSignal(r io.Reader) (wireMessage, error) {
	magic := make([]byte, len(SignalMagic))
	if _, err := io.ReadFull(r, magic); err != nil {
		return wireMessage{}, err
	}
	if string(magic) != SignalMagic {
		return wireMessage{}, errors.New("魔数不符")
	}
	return readBody(r)
}

func readBody(r io.Reader) (wireMessage, error) {
	var size [4]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return wireMessage{}, err
	}
	n := binary.BigEndian.Uint32(size[:])
	if n == 0 || n > maxSignalBody {
		return wireMessage{}, fmt.Errorf("消息体长度非法: %d", n)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return wireMessage{}, err
	}
	var m wireMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return wireMessage{}, fmt.Errorf("JSON: %w", err)
	}
	return m, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
